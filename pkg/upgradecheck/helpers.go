// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright 2024 The helm-upgrade-plugin authors
//
// Licensed under the GNU General Public License v3.0 or later (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.gnu.org/licenses/gpl-3.0.html
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package upgradecheck

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/repo"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// CompareVersions returns true if v1 > v2 using a simple semver comparison.
// It strips any leading "v" prefixes and compares numeric components left to
// right.  When the initial segments are equal, the longer version string is
// considered greater (e.g. "1.2.0" > "1.2").
// If includePrerel is true, then the comparison will consider pre-release versions
// pre-release versions with a higher major.minor.patch will be considered greater than stable versions with a lower major.minor.patch
func CompareVersions(v1, v2 string, includePrerel bool) bool {
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")

	// Parse the versions, if either is fails (is not a semver), return false to avoid false positives.
	// This means that if a chart has an invalid version, we won't consider it upgradable, which is a safer failure mode than the opposite.
	sv1, err := semver.NewVersion(v1)
	if err != nil {
		return false
	}
	sv2, err := semver.NewVersion(v2)
	if err != nil {
		return false
	}

	// If pre-releases are not included then any pre-release version is never greater than any stable
	if !includePrerel && sv1.Prerelease() != "" {
		if sv2.Prerelease() == "" {
			// sv2 is a stable version, pre-release are not considered greater than stables, so return false
			return false
		}
	}

	// At this point, there are two possible scenarios:

	return sv1.GreaterThan(sv2)
}

// LoadRepoEntries reads the Helm repository YAML file specified by the
// provided EnvSettings and returns the slice of *repo.Entry objects.  This
// does not download any index files; it only parses the configuration.
func LoadRepoEntries(settings *cli.EnvSettings) ([]*repo.Entry, error) {
	repoFile := settings.RepositoryConfig
	repos, err := repo.LoadFile(repoFile)
	if err != nil {
		return nil, err
	}
	entries := make([]*repo.Entry, len(repos.Repositories))
	copy(entries, repos.Repositories)
	return entries, nil
}

// ChartSearchResult is the information returned from a chart lookup.
// Version is the latest chart version (used for --version in helm upgrade).
// AppVersion is the application version bundled with that chart release.
type ChartSearchResult struct {
	Repos      []string
	Version    string // latest chart version (authoritative for helm upgrade --version)
	AppVersion string // app version of the latest chart release (for display only)
}

// ChartSearcher performs on-demand lookups of repository indexes to resolve
// the latest version of a chart.  Results and index files are cached to avoid
// repeated network requests when scanning many releases.
type ChartSearcher struct {
	repos     []*repo.Entry
	getters   getter.Providers
	idxCache  map[string]*repo.IndexFile
	resultMap map[string]ChartSearchResult
}

// NewChartSearcher constructs a searcher using the provided repositories and
// HTTP getter providers.
func NewChartSearcher(repos []*repo.Entry, getters getter.Providers) *ChartSearcher {
	return &ChartSearcher{
		repos:     repos,
		getters:   getters,
		idxCache:  make(map[string]*repo.IndexFile),
		resultMap: make(map[string]ChartSearchResult),
	}
}

// Search returns a ChartSearchResult for chartName.  If the result was
// previously computed, it is returned from the cache; otherwise the method
// scans each repository index, updates the cache, and applies the "bitnami"
// deduplication rule.
func (s *ChartSearcher) Search(chartName string) ChartSearchResult {
	if r, ok := s.resultMap[chartName]; ok {
		return r
	}

	type result struct {
		repo       string
		version    string // chart version (authoritative)
		appVersion string // app version for display
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg := sync.WaitGroup{}
	ch := make(chan result, len(s.repos))
	sem := make(chan struct{}, 6) // concurrency limit

	for _, entry := range s.repos {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			idx, err := s.loadIndex(entry)
			if err != nil {
				return
			}
			if versions, ok := idx.Entries[chartName]; ok && len(versions) > 0 {
				cv := versions[0].Version
				av := ""
				if versions[0].Metadata != nil {
					av = versions[0].Metadata.AppVersion
				}
				ch <- result{repo: entry.Name, version: cv, appVersion: av}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	uniq := make(map[string]struct{})
	var latestChartVer, latestAppVer string
	for r := range ch {
		uniq[r.repo] = struct{}{}
		if latestChartVer == "" || CompareVersions(r.version, latestChartVer, true) {
			latestChartVer = r.version
			latestAppVer = r.appVersion
		}
	}
	reposFound := make([]string, 0, len(uniq))
	for k := range uniq {
		reposFound = append(reposFound, k)
	}

	if len(reposFound) > 1 {
		keep := make([]string, 0, len(reposFound))
		for _, r := range reposFound {
			if r == "bitnami" {
				continue
			}
			keep = append(keep, r)
		}
		if len(keep) > 0 {
			reposFound = keep
		}
	}

	res := ChartSearchResult{Repos: reposFound, Version: latestChartVer, AppVersion: latestAppVer}
	s.resultMap[chartName] = res
	return res
}

// ociClient defines the subset of registry.Client methods used by
// loadIndex.  Keeping an interface simplifies testing because we can provide
// lightweight fakes instead of the concrete *registry.Client type.
type ociClient interface {
	Tags(string) ([]string, error)
	Pull(string, ...registry.PullOption) (*registry.PullResult, error)
}

// registryClientFactory abstracts creation of an OCI client instance so tests
// can inject a fake implementation.
var registryClientFactory = func() (ociClient, error) {
	return registry.NewClient()
}

func (s *ChartSearcher) loadIndex(entry *repo.Entry) (*repo.IndexFile, error) {
	if idx, ok := s.idxCache[entry.Name]; ok {
		return idx, nil
	}
	// OCI registry support: when a repo URL starts with oci:// we query the
	// registry for tags, pull the latest chart and construct a minimal index
	// entry containing the application version.  This keeps downstream search
	// logic uniform whether charts come from normal repo indexes or OCI.
	if strings.HasPrefix(entry.URL, "oci://") {
		ref := strings.TrimPrefix(entry.URL, "oci://")
		client, err := registryClientFactory()
		if err != nil {
			return nil, err
		}
		tags, err := client.Tags(ref)
		if err != nil {
			return nil, err
		}
		if len(tags) == 0 {
			empty := &repo.IndexFile{Entries: map[string]repo.ChartVersions{}}
			s.idxCache[entry.Name] = empty
			return empty, nil
		}
		latest := tags[0]
		pullRes, err := client.Pull(fmt.Sprintf("%s:%s", ref, latest))
		if err != nil {
			return nil, err
		}
		ch, err := loader.LoadArchive(bytes.NewReader(pullRes.Chart.Data))
		if err != nil {
			return nil, err
		}
		appv := ""
		if ch.Metadata != nil {
			appv = ch.Metadata.AppVersion
			if appv == "" {
				appv = ch.Metadata.Version
			}
		}
		chartName := entry.Name
		if i := strings.LastIndex(ref, "/"); i != -1 {
			chartName = ref[i+1:]
		}
		idx := &repo.IndexFile{Entries: map[string]repo.ChartVersions{}}
		idx.Entries[chartName] = repo.ChartVersions{&repo.ChartVersion{
			Metadata: &chart.Metadata{
				Version:    latest,
				AppVersion: appv,
			},
			URLs: []string{entry.URL},
		}}
		s.idxCache[entry.Name] = idx
		return idx, nil
	}

	chartRepo, err := repo.NewChartRepository(entry, s.getters)
	if err != nil {
		return nil, err
	}

	var path string
	var tryErr error
	attempts := 3
	backoff := 500 * time.Millisecond
	for i := 0; i < attempts; i++ {
		path, tryErr = chartRepo.DownloadIndexFile()
		if tryErr == nil {
			break
		}
		if i < attempts-1 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	if tryErr != nil {
		return nil, tryErr
	}

	idx, err := repo.LoadIndexFile(path)
	if err != nil {
		return nil, err
	}
	s.idxCache[entry.Name] = idx
	return idx, nil
}

// UpdateRepositories iterates over the repositories defined in the provided
// EnvSettings and downloads their index files, returning an error if any
// repository fails to update.  It's essentially a programmatic equivalent of
// `helm repo update`.
func UpdateRepositories(settings *cli.EnvSettings) error {
	repoFile := settings.RepositoryConfig
	repos, err := repo.LoadFile(repoFile)
	if err != nil {
		return fmt.Errorf("failed to load repository file: %w", err)
	}

	getters := getter.All(settings)
	for _, entry := range repos.Repositories {
		chartRepo, err := repo.NewChartRepository(entry, getters)
		if err != nil {
			return fmt.Errorf("failed to create chart repository for %q: %w", entry.Name, err)
		}
		if _, err := chartRepo.DownloadIndexFile(); err != nil {
			return fmt.Errorf("failed to download index file for %q: %w", entry.Name, err)
		}
	}
	return nil
}

// PrintUpgradeCommands writes the series of helm commands required to inspect
// and upgrade a release to the supplied writer.  The commands are prefixed by
// two spaces to visually nest them beneath the release row in the output.
func PrintUpgradeCommands(w io.Writer, release, namespace, repos, chartName, version string) {
	fmt.Fprintf(w, "  helm get values --namespace %s %s -o yaml > %s.values\n", namespace, release, release)
	fmt.Fprintf(w, "  cat %s.values\n", release)
	fmt.Fprintf(w, "  helm upgrade --namespace %s %s %s/%s --version %s --values %s.values\n", namespace, release, repos, chartName, version, release)
}

// convertReleaseList turns the Helm SDK's slice of *release.Release into the
// simplified []upgradecheck.Release type used by the rest of the plugin.
func convertReleaseList(list []*release.Release) []Release {
	var out []Release
	for _, rel := range list {
		out = append(out, Release{
			Name:         rel.Name,
			Namespace:    rel.Namespace,
			Chart:        rel.Chart.Name() + "-" + rel.Chart.Metadata.Version,
			ChartVersion: rel.Chart.Metadata.Version,
			AppVersion:   rel.Chart.Metadata.AppVersion,
		})
	}
	return out
}

// FetchReleases retrieves all Helm releases across every namespace.  The
// returned slice uses the internal Release struct.  Debugging information is
// printed to stdout when the debug flag is true.
func FetchReleases(settings *cli.EnvSettings, debug bool) ([]Release, error) {
	cfg := new(action.Configuration)
	cfgGetter := &genericclioptions.ConfigFlags{
		KubeConfig: &settings.KubeConfig,
		Context:    &settings.KubeContext,
	}
	if err := cfg.Init(cfgGetter, "", os.Getenv("HELM_DRIVER"), nil); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm config: %w", err)
	}

	listCmd := action.NewList(cfg)
	listCmd.AllNamespaces = true
	listCmd.Deployed = true
	listCmd.Failed = true
	listCmd.Pending = true
	listCmd.Uninstalling = true
	listCmd.Uninstalled = true

	releaseList, err := listCmd.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	if debug {
		for _, rel := range releaseList {
			fmt.Printf("Debug: loaded release %s in namespace %s (chart: %s, app_version: %s)\n", rel.Name, rel.Namespace, rel.Chart.Name()+"-"+rel.Chart.Metadata.Version, rel.Chart.Metadata.AppVersion)
		}
	}

	return convertReleaseList(releaseList), nil
}
