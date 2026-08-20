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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	semver "github.com/Masterminds/semver/v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/helmpath"
	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/release"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// CompareVersions returns true if v1 > v2 according to semver, after stripping
// any leading "v" prefix.  Either version failing to parse as semver yields
// false, so an unparseable version is never treated as an upgrade.
// If includePrerel is true, a pre-release version with a higher
// major.minor.patch is considered greater than a stable version with a lower
// major.minor.patch; if it is false, a pre-release is never greater than a
// stable version.
func CompareVersions(v1, v2 string, includePrerel bool) bool {
	// Parse the versions, if either is fails (is not a semver), return false to avoid false positives.
	// This means that if a chart has an invalid version, we won't consider it upgradable, which is a safer failure mode than the opposite.
	sv1, err := parseVersion(v1)
	if err != nil {
		return false
	}
	sv2, err := parseVersion(v2)
	if err != nil {
		return false
	}

	// If pre-releases are not included then any pre-release version is never greater than any stable
	if excludedPrerelease(sv1, includePrerel) && sv2.Prerelease() == "" {
		// sv2 is a stable version, pre-release are not considered greater than stables, so return false
		return false
	}

	return sv1.GreaterThan(sv2)
}

// parseVersion parses v as a semantic version, tolerating the leading "v"
// prefix that chart and app versions frequently carry.
func parseVersion(v string) (*semver.Version, error) {
	return semver.NewVersion(strings.TrimPrefix(v, "v"))
}

// excludedPrerelease reports whether sv must be ignored because it is a
// pre-release and pre-releases were not requested.
func excludedPrerelease(sv *semver.Version, includePrerel bool) bool {
	return !includePrerel && sv.Prerelease() != ""
}

// DisplayValue returns value for display, substituting fallback when the
// value is absent — either empty or the literal "null" that repository
// indexes and release metadata can carry for a missing version.
func DisplayValue(value, fallback string) string {
	if value == "" || value == "null" {
		return fallback
	}
	return value
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

// RepoChartVersion is the chart version and app version found for a chart in
// a single repository.  A chart mirrored across multiple repositories can
// have a different version in each one, so these must be tracked per-repo
// rather than collapsed into a single "latest" value.
type RepoChartVersion struct {
	Repo       string
	Version    string // chart version in this repo (authoritative for helm upgrade --version)
	AppVersion string // app version of that chart release in this repo (for display only)
}

// ChartSearchResult is the information returned from a chart lookup.
// Versions holds one entry per repository that has the chart. Version and
// AppVersion summarize the single highest chart version across all repos
// (authoritative for determining whether any upgrade exists at all); use
// Versions when the per-repo version is needed, e.g. to build a
// `helm upgrade <repo>/<chart> --version <that repo's version>` command.
type ChartSearchResult struct {
	Repos      []string
	Version    string // highest chart version across all repos
	AppVersion string // app version paired with the highest chart version
	Versions   []RepoChartVersion
}

// ChartSearcher performs on-demand lookups of repository indexes to resolve
// the latest version of a chart.  Results and index files are cached to avoid
// repeated disk reads when scanning many releases.
type ChartSearcher struct {
	repos         []*repo.Entry
	idxCacheMu    sync.Mutex // guards idxCache; Search loads indexes from concurrent goroutines
	idxCache      map[string]*repo.IndexFile
	resultMap     map[string]ChartSearchResult
	resultErrMap  map[string]error
	includePrerel bool
	cacheDir      string // directory containing cached repo index files
}

// ChartSearchError describes failures encountered while loading repository
// indexes during a chart search.
type ChartSearchError struct {
	FailedRepos int
	TotalRepos  int
	err         error
}

// Error returns the aggregated repository search failure.
func (e *ChartSearchError) Error() string {
	return e.err.Error()
}

// Unwrap returns the underlying aggregated repository failures.
func (e *ChartSearchError) Unwrap() error {
	return e.err
}

// NewChartSearcher constructs a searcher using the provided repositories and
// local Helm repository cache directory.  When includePrerel is false,
// pre-release chart versions are skipped when determining the latest available
// version.  The cacheDir should be settings.RepositoryCache so that the plugin
// reads the same index files as `helm search repo` (populated by `helm repo
// update` or `helm upgrade-check --update`).
func NewChartSearcher(repos []*repo.Entry, cacheDir string, includePrerel bool) *ChartSearcher {
	if cacheDir == "" {
		cacheDir = helmpath.CachePath("repository")
	}
	return &ChartSearcher{
		repos:         repos,
		idxCache:      make(map[string]*repo.IndexFile),
		resultMap:     make(map[string]ChartSearchResult),
		resultErrMap:  make(map[string]error),
		includePrerel: includePrerel,
		cacheDir:      cacheDir,
	}
}

// Search returns a ChartSearchResult for chartName.  If the result was
// previously computed, it is returned from the cache; otherwise the method
// scans each repository index and updates the cache.  Results from repositories
// that load successfully are returned alongside any per-repository errors.
//
// Search is not safe for concurrent use: it fans out across repositories
// internally, but resultMap is unsynchronized, so callers must invoke it from
// a single goroutine.
func (s *ChartSearcher) Search(chartName string) (ChartSearchResult, error) {
	if r, ok := s.resultMap[chartName]; ok {
		return r, s.resultErrMap[chartName]
	}

	type result struct {
		repo       string
		version    string // chart version (authoritative)
		appVersion string // app version for display
	}
	type repoResult struct {
		result      result
		err         error
		indexLoaded bool
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg := sync.WaitGroup{}
	ch := make(chan repoResult, len(s.repos))
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
				ch <- repoResult{err: fmt.Errorf("repo %q: %w", entry.Name, err)}
				return
			}
			var entryErrs []error
			if versions, ok := idx.Entries[chartName]; ok {
				// Helm indexes sort entries newest-first, so iterate until we
				// find the first version that satisfies the pre-release policy.
				var cv, av string
				for _, v := range versions {
					if v == nil || v.Metadata == nil {
						entryErrs = append(entryErrs, fmt.Errorf("chart %q has an entry without metadata", chartName))
						continue
					}
					sv, err := parseVersion(v.Version)
					if err != nil {
						entryErrs = append(entryErrs, fmt.Errorf("chart %q has invalid version %q: %w", chartName, v.Version, err))
						continue
					}
					if excludedPrerelease(sv, s.includePrerel) {
						continue
					}
					cv = v.Version
					av = v.AppVersion
					break
				}
				if cv != "" {
					var entryErr error
					if len(entryErrs) > 0 {
						entryErr = fmt.Errorf("repo %q: %w", entry.Name, errors.Join(entryErrs...))
					}
					ch <- repoResult{result: result{repo: entry.Name, version: cv, appVersion: av}, indexLoaded: true, err: entryErr}
					return
				}
			}
			var entryErr error
			if len(entryErrs) > 0 {
				entryErr = fmt.Errorf("repo %q: %w", entry.Name, errors.Join(entryErrs...))
			}
			ch <- repoResult{indexLoaded: true, err: entryErr}
		}()
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var versions []RepoChartVersion
	var latestChartVer, latestAppVer string
	var searchErrs []error
	failedRepos := 0
	for r := range ch {
		if r.err != nil {
			searchErrs = append(searchErrs, r.err)
		}
		if !r.indexLoaded {
			failedRepos++
			continue
		}
		if r.result.version != "" {
			versions = append(versions, RepoChartVersion{Repo: r.result.repo, Version: r.result.version, AppVersion: r.result.appVersion})
			if latestChartVer == "" || CompareVersions(r.result.version, latestChartVer, s.includePrerel) {
				latestChartVer = r.result.version
				latestAppVer = r.result.appVersion
			}
		}
	}
	reposFound := make([]string, len(versions))
	for i, v := range versions {
		reposFound[i] = v.Repo
	}

	res := ChartSearchResult{Repos: reposFound, Version: latestChartVer, AppVersion: latestAppVer, Versions: versions}
	s.resultMap[chartName] = res
	if len(searchErrs) == 0 {
		return res, nil
	}
	searchErr := &ChartSearchError{
		FailedRepos: failedRepos,
		TotalRepos:  len(s.repos),
		err:         errors.Join(searchErrs...),
	}
	s.resultErrMap[chartName] = searchErr
	return res, searchErr
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

// getCachedIndex and setCachedIndex guard idxCache with a mutex because
// loadIndex is invoked concurrently (one goroutine per repo) from Search.
// Go maps are not safe for concurrent access even across distinct keys, and
// an unsynchronized map here previously produced corrupted/aliased results
// when a chart existed in multiple repos.
func (s *ChartSearcher) getCachedIndex(name string) (*repo.IndexFile, bool) {
	s.idxCacheMu.Lock()
	defer s.idxCacheMu.Unlock()
	idx, ok := s.idxCache[name]
	return idx, ok
}

func (s *ChartSearcher) setCachedIndex(name string, idx *repo.IndexFile) {
	s.idxCacheMu.Lock()
	defer s.idxCacheMu.Unlock()
	s.idxCache[name] = idx
}

func (s *ChartSearcher) loadIndex(entry *repo.Entry) (*repo.IndexFile, error) {
	if idx, ok := s.getCachedIndex(entry.Name); ok {
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
			s.setCachedIndex(entry.Name, empty)
			return empty, nil
		}
		latest := tags[0]
		pullRes, err := client.Pull(fmt.Sprintf("%s:%s", ref, latest))
		if err != nil {
			return nil, err
		}
		if pullRes == nil || pullRes.Chart == nil {
			return nil, fmt.Errorf("OCI pull returned no chart for %s:%s", ref, latest)
		}
		ch, err := loader.LoadArchive(bytes.NewReader(pullRes.Chart.Data))
		if err != nil {
			return nil, err
		}
		if ch == nil {
			return nil, fmt.Errorf("OCI pull returned an empty chart for %s:%s", ref, latest)
		}
		ca, err := chart.NewAccessor(ch)
		if err != nil {
			return nil, fmt.Errorf("accessing OCI chart metadata: %w", err)
		}
		appv := ""
		md := ca.MetadataAsMap()
		if av, ok := md["AppVersion"].(string); ok && av != "" {
			appv = av
		} else if v, ok := md["Version"].(string); ok {
			appv = v
		}
		chartName := entry.Name
		if i := strings.LastIndex(ref, "/"); i != -1 {
			chartName = ref[i+1:]
		}
		idx := &repo.IndexFile{Entries: map[string]repo.ChartVersions{}}
		idx.Entries[chartName] = repo.ChartVersions{&repo.ChartVersion{
			Metadata: &chartv2.Metadata{
				Version:    latest,
				AppVersion: appv,
			},
			URLs: []string{entry.URL},
		}}
		s.setCachedIndex(entry.Name, idx)
		return idx, nil
	}

	// Read the locally cached index file — the same file that `helm search repo`
	// uses and that `helm repo update` (or our --update flag) populates.
	path := filepath.Join(s.cacheDir, helmpath.CacheIndexFile(entry.Name))
	idx, err := repo.LoadIndexFile(path)
	if err != nil {
		return nil, err
	}
	s.setCachedIndex(entry.Name, idx)
	return idx, nil
}

// ValuesCommands returns the commands that save a release's current values to
// a file and display them for review, which precede any upgrade command.
func ValuesCommands(release, namespace string) []string {
	return []string{
		fmt.Sprintf("helm get values --namespace %s %s -o yaml > %s.values", namespace, release, release),
		fmt.Sprintf("cat %s.values", release),
	}
}

// UpgradeCommand returns the helm command that upgrades a release to version
// of chartName as published by repoName.  version is always a chart version,
// which is what `helm upgrade --version` accepts.
func UpgradeCommand(release, namespace, repoName, chartName, version string) string {
	return fmt.Sprintf("helm upgrade --namespace %s %s %s/%s --version %s --values %s.values", namespace, release, repoName, chartName, version, release)
}

// UpgradeCommands returns the full series of helm commands required to
// inspect and upgrade a release.
func UpgradeCommands(release, namespace, repoName, chartName, version string) []string {
	return append(ValuesCommands(release, namespace), UpgradeCommand(release, namespace, repoName, chartName, version))
}

// PrintUpgradeCommands writes the series of helm commands required to inspect
// and upgrade a release to the supplied writer.  The commands are prefixed by
// two spaces to visually nest them beneath the release row in the output.
func PrintUpgradeCommands(w io.Writer, release, namespace, repos, chartName, version string) error {
	for _, cmd := range UpgradeCommands(release, namespace, repos, chartName, version) {
		if _, err := fmt.Fprintf(w, "  %s\n", cmd); err != nil {
			return err
		}
	}
	return nil
}

// releaseInfo converts a single Helm SDK release into the simplified Release
// type used by the rest of the plugin.  The index is only used to identify the
// release in errors raised before its name is known.
func releaseInfo(index int, rel release.Releaser) (Release, error) {
	ra, err := release.NewAccessor(rel)
	if err != nil {
		return Release{}, fmt.Errorf("failed to decode release at index %d: %w", index, err)
	}
	chartValue := ra.Chart()
	if isNilChart(chartValue) {
		return Release{}, fmt.Errorf("release %q in namespace %q: chart is missing", ra.Name(), ra.Namespace())
	}
	ca, err := chart.NewAccessor(chartValue)
	if err != nil {
		return Release{}, fmt.Errorf("release %q in namespace %q: failed to access chart: %w", ra.Name(), ra.Namespace(), err)
	}
	md := ca.MetadataAsMap()
	if md == nil {
		return Release{}, fmt.Errorf("release %q in namespace %q: chart metadata is missing", ra.Name(), ra.Namespace())
	}
	version, _ := md["Version"].(string)
	appVersion, _ := md["AppVersion"].(string)
	return Release{
		Name:         ra.Name(),
		Namespace:    ra.Namespace(),
		Chart:        ca.Name() + "-" + version,
		ChartName:    ca.Name(),
		ChartVersion: version,
		AppVersion:   appVersion,
	}, nil
}

// convertReleaseList turns the Helm SDK's slice of release.Releaser into the
// simplified []upgradecheck.Release type used by the rest of the plugin.
func convertReleaseList(list []release.Releaser) ([]Release, error) {
	var out []Release
	var conversionErrs []error
	for i, rel := range list {
		info, err := releaseInfo(i, rel)
		if err != nil {
			conversionErrs = append(conversionErrs, err)
			continue
		}
		out = append(out, info)
	}
	return out, errors.Join(conversionErrs...)
}

// isNilChart detects typed-nil chart pointers accepted by Helm's accessor.
// reflect is needed because a typed nil pointer stored in chart.Charter is not equal to nil.
func isNilChart(ch chart.Charter) bool {
	if ch == nil {
		return true
	}
	value := reflect.ValueOf(ch)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

// FetchReleases retrieves all Helm releases across every namespace.  The
// returned slice uses the internal Release struct.  Debugging information is
// printed to stdout when the debug flag is true.  A non-nil error may
// accompany a non-empty slice when only some releases failed to decode.
func FetchReleases(settings *cli.EnvSettings, debug bool) ([]Release, error) {
	cfg := new(action.Configuration)
	if err := cfg.Init(settings.RESTClientGetter(), "", os.Getenv("HELM_DRIVER")); err != nil {
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

	releases, conversionErr := convertReleaseList(releaseList)

	if debug {
		for _, rel := range releases {
			fmt.Printf("Debug: loaded release %s in namespace %s (chart: %s, app_version: %s)\n", rel.Name, rel.Namespace, rel.Chart, rel.AppVersion)
		}
	}

	return releases, conversionErr
}
