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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/helmpath"
	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b          string
		includePrerel bool
		want          bool
	}{
		{"1.2.3", "1.2.2", true, true},
		{"1.2.3", "1.2.3", true, false},
		{"v2.0", "v1.9", true, true},
		{"1.0", "1.0.1", true, false},
		{"1.0.0", "1.0", true, false}, // 1.0 is not a valid semver
		{"", "1", true, false},
		{"v1.19.4", "1.18.2", true, true},
		{"v1.20.0-alpha.1", "v1.19.4", true, true},           // pre-release is greater
		{"v1.20.0-alpha.1", "v1.19.4", false, false},         // pre-release should not be considered greater
		{"v1.20.0-alpha.1", "v1.19.4-alpha.1", false, true},  // If both are pre-release, the greater pre-release should win
		{"v1.19.0-alpha.1", "v1.20.4-alpha.1", false, false}, // If both are pre-release, the greater pre-release should win
		{"v1.0.0+build.1", "v1.0.0+build.2", true, false},    // build metadata should be ignored
		{"1.2.3", "not-a-version", true, false},              // unparseable right-hand side is never upgradable
		{"1.2.3", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.a+">"+c.b+" includePrerel="+fmt.Sprintf("%t", c.includePrerel), func(t *testing.T) {
			got := CompareVersions(c.a, c.b, c.includePrerel)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestLoadRepoEntries(t *testing.T) {
	tmpdir, err := os.MkdirTemp("", "repos")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()

	repoYAML := `apiVersion: v1
repositories:
- name: foo
  url: https://example.com/charts
`
	path := filepath.Join(tmpdir, "repositories.yaml")
	if err := os.WriteFile(path, []byte(repoYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	settings := cli.New()
	settings.RepositoryConfig = path

	entries, err := LoadRepoEntries(settings)
	assert.NoError(t, err)
	if assert.Len(t, entries, 1) {
		assert.Equal(t, "foo", entries[0].Name)
		assert.Equal(t, "https://example.com/charts", entries[0].URL)
	}
}

func TestChartSearcher_Search_MultipleRepos(t *testing.T) {
	// When a chart exists in multiple repos, all of them should be reported
	// and the highest version across all of them wins — no repo (including
	// bitnami) gets special treatment.
	repos := []*repo.Entry{
		{Name: "r1"},
		{Name: "bitnami"},
	}
	searcher := NewChartSearcher(repos, "", false)

	searcher.idxCache["r1"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"redis": {&repo.ChartVersion{Metadata: &chartv2.Metadata{Version: "1.0.0", AppVersion: "7.0.0"}}},
		},
	}
	searcher.idxCache["bitnami"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"redis": {&repo.ChartVersion{Metadata: &chartv2.Metadata{Version: "2.0.0", AppVersion: "7.2.0"}}},
		},
	}

	res, err := searcher.Search("redis")
	assert.NoError(t, err)
	assert.Equal(t, "2.0.0", res.Version)
	assert.Equal(t, "7.2.0", res.AppVersion)
	assert.ElementsMatch(t, []string{"r1", "bitnami"}, res.Repos)

	// ensure caching works
	searcher.idxCache = map[string]*repo.IndexFile{}
	res2, err := searcher.Search("redis")
	assert.NoError(t, err)
	assert.Equal(t, res, res2)
}

func TestChartSearcher_Search_ConcurrentLoadKeepsRepoVersionsDistinct(t *testing.T) {
	// Regression test: loadIndex is called concurrently (one goroutine per
	// repo) from Search, and idxCache used to be a plain map written from
	// those goroutines with no synchronization. That data race could corrupt
	// results so that two different repos ended up reporting the same
	// (wrong) chart/app version. This test forces the real disk-load path
	// (unlike the test above, which pre-populates idxCache before Search
	// runs, sidestepping the concurrent loadIndex path entirely) and repeats
	// the search many times to make a reintroduced race likely to surface.
	cacheDir := t.TempDir()
	writeIndex := func(repoName, version string) {
		yaml := fmt.Sprintf("apiVersion: v1\nentries:\n  grafana:\n    - name: grafana\n      version: %s\n      appVersion: %s\n", version, version)
		path := filepath.Join(cacheDir, helmpath.CacheIndexFile(repoName))
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeIndex("grafana", "8.0.0")
	writeIndex("grafana-community", "9.0.0")

	repos := []*repo.Entry{{Name: "grafana"}, {Name: "grafana-community"}}

	for i := 0; i < 50; i++ {
		searcher := NewChartSearcher(repos, cacheDir, false)
		res, err := searcher.Search("grafana")
		assert.NoError(t, err)

		byRepo := map[string]string{}
		for _, v := range res.Versions {
			byRepo[v.Repo] = v.Version
		}
		assert.Equal(t, "8.0.0", byRepo["grafana"], "iteration %d", i)
		assert.Equal(t, "9.0.0", byRepo["grafana-community"], "iteration %d", i)
	}
}

func TestChartSearcher_NoChart(t *testing.T) {
	repos := []*repo.Entry{{Name: "empty"}}
	s := NewChartSearcher(repos, "", false)
	s.idxCache["empty"] = &repo.IndexFile{Entries: map[string]repo.ChartVersions{}}
	r, err := s.Search("nonexistent")
	assert.NoError(t, err)
	assert.Empty(t, r.Repos)
	assert.Equal(t, "", r.Version)
}

func TestChartSearcher_Search_ReturnsSuccessfulReposAndFailures(t *testing.T) {
	cacheDir := t.TempDir()
	repos := []*repo.Entry{{Name: "good"}, {Name: "bad"}}
	searcher := NewChartSearcher(repos, cacheDir, false)
	searcher.idxCache["good"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"demo": {{Metadata: &chartv2.Metadata{Version: "1.2.3", AppVersion: "4.5.6"}}},
		},
	}

	res, err := searcher.Search("demo")

	assert.Error(t, err)
	assert.ErrorContains(t, err, `repo "bad"`)
	assert.Equal(t, []string{"good"}, res.Repos)
	assert.Equal(t, "1.2.3", res.Version)
}

func TestChartSearcher_Search_InvalidVersionReturnsError(t *testing.T) {
	repos := []*repo.Entry{{Name: "repo"}}
	searcher := NewChartSearcher(repos, "", false)
	searcher.idxCache["repo"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"demo": {
				{Metadata: &chartv2.Metadata{Version: "not-semver"}},
				{Metadata: &chartv2.Metadata{Version: "1.0.0"}},
			},
		},
	}

	res, err := searcher.Search("demo")

	assert.Error(t, err)
	assert.ErrorContains(t, err, `chart "demo" has invalid version "not-semver"`)
	assert.Equal(t, "1.0.0", res.Version)
}

// fakeOCI is a minimal implementation of the ociClient interface used in
// tests to simulate an OCI registry with two tags and a simple chart archive.
type fakeOCI struct{}

func (f *fakeOCI) Tags(ref string) ([]string, error) {
	return []string{"1.0.0", "2.0.0"}, nil
}

func (f *fakeOCI) Pull(ref string, opts ...registry.PullOption) (*registry.PullResult, error) {
	return &registry.PullResult{Chart: &registry.DescriptorPullSummaryWithMeta{
		DescriptorPullSummary: registry.DescriptorPullSummary{Data: archiveData},
	}}, nil
}

var archiveData []byte // set by test

func TestOCIRepositoryLookup(t *testing.T) {
	// build a minimal chart archive containing metadata with an appVersion
	chartYAML := "apiVersion: v2\nname: demo\nversion: 1.0.0\nappVersion: 5.5.5\n"
	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tarWriter := tar.NewWriter(gw)
	content := []byte(chartYAML)
	// create directory entry
	dirHdr := &tar.Header{Name: "demo/", Typeflag: tar.TypeDir, Mode: 0755}
	_ = tarWriter.WriteHeader(dirHdr)
	hdr := &tar.Header{Name: "demo/Chart.yaml", Mode: 0644, Size: int64(len(content))}
	_ = tarWriter.WriteHeader(hdr)
	_, _ = tarWriter.Write(content)
	_ = tarWriter.Close()
	_ = gw.Close()
	archiveData = buf.Bytes()

	origFactory := registryClientFactory
	registryClientFactory = func() (ociClient, error) {
		return &fakeOCI{}, nil
	}
	defer func() { registryClientFactory = origFactory }()

	repoEntry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/mychart"}
	s := NewChartSearcher([]*repo.Entry{repoEntry}, "", false)

	res, err := s.Search("mychart")
	assert.NoError(t, err)
	// Version is the chart version (OCI tag "1.0.0"), not the app version.
	assert.Equal(t, "1.0.0", res.Version)
	assert.Equal(t, "5.5.5", res.AppVersion)
	assert.Equal(t, []string{"ociRepo"}, res.Repos)
}

func TestPrintUpgradeCommands(t *testing.T) {
	buf := &bytes.Buffer{}
	assert.NoError(t, PrintUpgradeCommands(buf, "rel", "ns", "repo1", "chart", "1.2.3"))
	out := buf.String()
	assert.Contains(t, out, "helm get values --namespace ns rel")
	assert.Contains(t, out, "helm upgrade --namespace ns rel repo1/chart --version 1.2.3")
}

func TestSanitizeDisplay(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "release-1", want: "release-1"},
		{name: "control characters", in: "release\n\x1b[31m", want: "release[31m"},
		{name: "non printable unicode", in: "release\u200bname", want: "releasename"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeDisplay(tt.in))
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "safe value", in: "repo/chart-1.2.3", want: "repo/chart-1.2.3"},
		{name: "spaces", in: "release name", want: "'release name'"},
		{name: "shell metacharacters", in: "$(touch /tmp/pwn)", want: "'$(touch /tmp/pwn)'"},
		{name: "single quote", in: "it's", want: "'it'\\''s'"},
		{name: "empty", in: "", want: "''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ShellQuote(tt.in))
		})
	}
}

func TestUpgradeCommands(t *testing.T) {
	cmds := UpgradeCommands("rel", "ns", "repo1", "chart", "1.2.3")
	assert.Equal(t, []string{
		"helm get values --namespace ns rel -o yaml > rel.values",
		"cat rel.values",
		"helm upgrade --namespace ns rel repo1/chart --version 1.2.3 --values rel.values",
	}, cmds)
	assert.Equal(t, cmds[:2], ValuesCommands("rel", "ns"))
	assert.Equal(t, cmds[2], UpgradeCommand("rel", "ns", "repo1", "chart", "1.2.3"))
}

func TestDisplayValue(t *testing.T) {
	assert.Equal(t, "1.2.3", DisplayValue("1.2.3", "N/A"))
	assert.Equal(t, "N/A", DisplayValue("", "N/A"))
	assert.Equal(t, "N/A", DisplayValue("null", "N/A"))
}

func TestConvertReleaseList(t *testing.T) {
	helmRel := &releasev1.Release{
		Name:      "r",
		Namespace: "n",
		Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Version: "1.2.3", AppVersion: "5.6.7"}},
	}
	out, err := convertReleaseList([]release.Releaser{helmRel})
	assert.NoError(t, err)
	if assert.Len(t, out, 1) {
		assert.Equal(t, "r", out[0].Name)
		assert.Equal(t, "n", out[0].Namespace)
		assert.Equal(t, "-1.2.3", out[0].Chart)
		assert.Equal(t, "1.2.3", out[0].ChartVersion)
		assert.Equal(t, "5.6.7", out[0].AppVersion)
	}
}

func TestConvertReleaseList_ChartVersionDiffersFromAppVersion(t *testing.T) {
	// ingress-nginx is a real-world example: chart 4.9.1, app 1.9.1.
	// ChartVersion must carry the chart version, not the app version.
	helmRel := &releasev1.Release{
		Name:      "ingress-nginx",
		Namespace: "ingress",
		Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Name: "ingress-nginx", Version: "4.9.1", AppVersion: "1.9.1"}},
	}
	out, err := convertReleaseList([]release.Releaser{helmRel})
	assert.NoError(t, err)
	if assert.Len(t, out, 1) {
		assert.Equal(t, "ingress-nginx-4.9.1", out[0].Chart)
		assert.Equal(t, "4.9.1", out[0].ChartVersion)
		assert.Equal(t, "1.9.1", out[0].AppVersion)
	}
}

func TestConvertReleaseList_PartialFailure(t *testing.T) {
	valid := &releasev1.Release{
		Name:      "valid",
		Namespace: "default",
		Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Name: "demo", Version: "1.0.0"}},
	}
	invalid := &releasev1.Release{Name: "invalid", Namespace: "default"}

	out, err := convertReleaseList([]release.Releaser{valid, invalid})

	assert.Len(t, out, 1)
	assert.Equal(t, "valid", out[0].Name)
	assert.Error(t, err)
	assert.ErrorContains(t, err, `release "invalid" in namespace "default": chart is missing`)
}

func TestLoadIndex_CachedFile(t *testing.T) {
	// Write a minimal index file into a temp cache directory, simulating what
	// `helm repo update` produces, and verify loadIndex reads it correctly.
	indexYAML := `apiVersion: v1
generated: "2026-01-01T00:00:00Z"
entries:
  demo:
    - name: demo
      apiVersion: v2
      version: 0.1.0
      appVersion: 4.5.6
      urls:
        - https://example.com/demo-0.1.0.tgz
`
	cacheDir, err := os.MkdirTemp("", "helm-cache")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(cacheDir) }()

	indexFile := filepath.Join(cacheDir, helmpath.CacheIndexFile("local"))
	if err := os.WriteFile(indexFile, []byte(indexYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	repoEntry := &repo.Entry{Name: "local", URL: "https://example.com"}
	searcher := NewChartSearcher([]*repo.Entry{repoEntry}, cacheDir, false)

	idx, err := searcher.loadIndex(repoEntry)
	assert.NoError(t, err)
	if assert.NotNil(t, idx) {
		if ev := idx.Entries["demo"][0].AppVersion; ev != "4.5.6" {
			t.Errorf("unexpected appversion %s", ev)
		}
	}
	// second call should hit the in-memory cache
	idx2, err := searcher.loadIndex(repoEntry)
	assert.NoError(t, err)
	assert.Equal(t, idx, idx2)
}

func TestLoadIndex_RejectsUnsafeRepositoryName(t *testing.T) {
	searcher := NewChartSearcher(nil, t.TempDir(), false)
	for _, name := range []string{"", ".", "..", "../escape", "nested/repository", `nested\repository`} {
		t.Run(name, func(t *testing.T) {
			_, err := searcher.loadIndex(&repo.Entry{Name: name, URL: "https://example.com"})
			assert.EqualError(t, err, fmt.Sprintf("invalid repository name %q", name))
		})
	}
}

func TestChartSearcher_PrerelFiltering_StableReturned(t *testing.T) {
	// The root cause of the cilium bug: versions[0] is a pre-release (rc/alpha),
	// but a stable version exists behind it.  With includePrerel=false the
	// searcher must skip the pre-release and return the latest stable.
	repos := []*repo.Entry{{Name: "cilium"}}
	s := NewChartSearcher(repos, "", false)
	s.idxCache["cilium"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"cilium": {
				{Metadata: &chartv2.Metadata{Version: "1.20.0-rc.1", AppVersion: "1.20.0-rc.1"}},
				{Metadata: &chartv2.Metadata{Version: "1.19.4", AppVersion: "1.19.4"}},
				{Metadata: &chartv2.Metadata{Version: "1.19.1", AppVersion: "1.19.1"}},
			},
		},
	}
	res, err := s.Search("cilium")
	assert.NoError(t, err)
	assert.Equal(t, "1.19.4", res.Version)
	assert.Equal(t, "1.19.4", res.AppVersion)
	assert.Equal(t, []string{"cilium"}, res.Repos)
}

func TestChartSearcher_PrerelFiltering_PrerelReturnedWhenEnabled(t *testing.T) {
	// With includePrerel=true the pre-release at the top of the index wins.
	repos := []*repo.Entry{{Name: "cilium"}}
	s := NewChartSearcher(repos, "", true)
	s.idxCache["cilium"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"cilium": {
				{Metadata: &chartv2.Metadata{Version: "1.20.0-rc.1", AppVersion: "1.20.0-rc.1"}},
				{Metadata: &chartv2.Metadata{Version: "1.19.4", AppVersion: "1.19.4"}},
			},
		},
	}
	res, err := s.Search("cilium")
	assert.NoError(t, err)
	assert.Equal(t, "1.20.0-rc.1", res.Version)
	assert.Equal(t, "1.20.0-rc.1", res.AppVersion)
}

func TestLoadRepoEntries_MissingFile(t *testing.T) {
	settings := cli.New()
	settings.RepositoryConfig = filepath.Join(t.TempDir(), "does-not-exist.yaml")

	entries, err := LoadRepoEntries(settings)
	assert.Error(t, err)
	assert.Nil(t, entries)
}

func TestChartSearcher_Search_SkipsUnusableIndexEntries(t *testing.T) {
	// Index entries without metadata or with a non-semver version must be
	// skipped rather than selected as the latest version.
	s := NewChartSearcher([]*repo.Entry{{Name: "r1"}}, "", false)
	s.idxCache["r1"] = &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"demo": {
				{Metadata: nil},
				{Metadata: &chartv2.Metadata{Version: "not-a-version", AppVersion: "9.9.9"}},
				{Metadata: &chartv2.Metadata{Version: "1.2.3", AppVersion: "4.5.6"}},
			},
		},
	}

	res, err := s.Search("demo")
	assert.Error(t, err, "unusable entries must be reported")
	assert.ErrorContains(t, err, `chart "demo" has an entry without metadata`)
	assert.ErrorContains(t, err, `chart "demo" has invalid version "not-a-version"`)
	assert.Equal(t, "1.2.3", res.Version)
	assert.Equal(t, "4.5.6", res.AppVersion)
}

func TestChartSearcher_Search_IgnoresReposWhoseIndexFailsToLoad(t *testing.T) {
	// A repo with no cached index file must be skipped so the repos that do have
	// an index still produce a result.
	cacheDir := t.TempDir()
	indexYAML := "apiVersion: v1\nentries:\n  demo:\n    - name: demo\n      version: 1.0.0\n      appVersion: 2.0.0\n"
	if err := os.WriteFile(filepath.Join(cacheDir, helmpath.CacheIndexFile("good")), []byte(indexYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewChartSearcher([]*repo.Entry{{Name: "missing"}, {Name: "good"}}, cacheDir, false)
	res, err := s.Search("demo")
	assert.Error(t, err, "the repo whose index failed to load must be reported")
	assert.ErrorContains(t, err, `repo "missing"`)
	assert.Equal(t, []string{"good"}, res.Repos)
	assert.Equal(t, "1.0.0", res.Version)
}

func TestLoadIndex_MissingCachedFile(t *testing.T) {
	entry := &repo.Entry{Name: "local", URL: "https://example.com"}
	s := NewChartSearcher([]*repo.Entry{entry}, t.TempDir(), false)

	idx, err := s.loadIndex(entry)
	assert.Error(t, err, "expected an error when the cached index file does not exist")
	assert.Nil(t, idx)
}

// configurableOCI is an ociClient whose responses (including failures) are
// supplied per test so every branch of the OCI path in loadIndex can be
// exercised.
type configurableOCI struct {
	tags     []string
	tagsErr  error
	data     []byte
	pullErr  error
	pullRefs []string
}

func (f *configurableOCI) Tags(string) ([]string, error) {
	return f.tags, f.tagsErr
}

func (f *configurableOCI) Pull(ref string, _ ...registry.PullOption) (*registry.PullResult, error) {
	f.pullRefs = append(f.pullRefs, ref)
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	return &registry.PullResult{Chart: &registry.DescriptorPullSummaryWithMeta{
		DescriptorPullSummary: registry.DescriptorPullSummary{Data: f.data},
	}}, nil
}

// withRegistryClient swaps in a fake OCI client for the duration of the test.
func withRegistryClient(t *testing.T, client ociClient, err error) {
	t.Helper()
	orig := registryClientFactory
	registryClientFactory = func() (ociClient, error) { return client, err }
	t.Cleanup(func() { registryClientFactory = orig })
}

// chartArchive builds a minimal gzipped chart archive containing Chart.yaml.
func chartArchive(t *testing.T, chartYAML string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "demo/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	content := []byte(chartYAML)
	if err := tw.WriteHeader(&tar.Header{Name: "demo/Chart.yaml", Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLoadIndex_OCI_ClientFactoryError(t *testing.T) {
	withRegistryClient(t, nil, fmt.Errorf("no registry client"))

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.ErrorContains(t, err, "no registry client")
	assert.Nil(t, idx)
}

func TestLoadIndex_OCI_TagsError(t *testing.T) {
	withRegistryClient(t, &configurableOCI{tagsErr: fmt.Errorf("tag listing failed")}, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.ErrorContains(t, err, "tag listing failed")
	assert.Nil(t, idx)
}

func TestLoadIndex_OCI_NoTagsCachesEmptyIndex(t *testing.T) {
	// A registry with no tags is not an error — an empty index is cached so
	// repeated searches don't re-query the registry.
	client := &configurableOCI{}
	withRegistryClient(t, client, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.NoError(t, err)
	if assert.NotNil(t, idx) {
		assert.Empty(t, idx.Entries)
	}
	cached, ok := s.getCachedIndex("ociRepo")
	assert.True(t, ok)
	assert.Equal(t, idx, cached)
	assert.Empty(t, client.pullRefs, "no chart should be pulled when there are no tags")
}

func TestLoadIndex_OCI_PullError(t *testing.T) {
	withRegistryClient(t, &configurableOCI{tags: []string{"1.0.0"}, pullErr: fmt.Errorf("pull failed")}, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.ErrorContains(t, err, "pull failed")
	assert.Nil(t, idx)
}

func TestLoadIndex_OCI_InvalidArchive(t *testing.T) {
	withRegistryClient(t, &configurableOCI{tags: []string{"1.0.0"}, data: []byte("not a chart archive")}, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.Error(t, err, "expected an error when the pulled chart is not a valid archive")
	assert.Nil(t, idx)
}

func TestLoadIndex_OCI_PullsFirstTagAndCaches(t *testing.T) {
	// The first tag returned by the registry is the one pulled, the index is
	// keyed by the chart name from the reference (not the repo name), and the
	// result is cached so a second call does not hit the registry again.
	client := &configurableOCI{
		tags: []string{"2.0.0", "1.0.0"},
		data: chartArchive(t, "apiVersion: v2\nname: demo\nversion: 2.0.0\nappVersion: 9.9.9\n"),
	}
	withRegistryClient(t, client, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.NoError(t, err)
	if assert.NotNil(t, idx) && assert.Len(t, idx.Entries["demo"], 1) {
		assert.Equal(t, "2.0.0", idx.Entries["demo"][0].Version)
		assert.Equal(t, "9.9.9", idx.Entries["demo"][0].AppVersion)
		assert.Equal(t, []string{entry.URL}, idx.Entries["demo"][0].URLs)
	}
	assert.Equal(t, []string{"example.com/charts/demo:2.0.0"}, client.pullRefs)

	idx2, err := s.loadIndex(entry)
	assert.NoError(t, err)
	assert.Equal(t, idx, idx2)
	assert.Len(t, client.pullRefs, 1, "second loadIndex call must be served from the cache")
}

func TestLoadIndex_OCI_AppVersionFallsBackToChartVersion(t *testing.T) {
	// Charts published without an appVersion fall back to the chart version so
	// the App Version column isn't blank.
	withRegistryClient(t, &configurableOCI{
		tags: []string{"3.1.4"},
		data: chartArchive(t, "apiVersion: v2\nname: demo\nversion: 3.1.4\n"),
	}, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://example.com/charts/demo"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.NoError(t, err)
	if assert.NotNil(t, idx) && assert.Len(t, idx.Entries["demo"], 1) {
		assert.Equal(t, "3.1.4", idx.Entries["demo"][0].AppVersion)
	}
}

func TestLoadIndex_OCI_ChartNameDefaultsToRepoName(t *testing.T) {
	// A reference without a path segment leaves no chart name to derive, so the
	// repo name is used as the index key.
	withRegistryClient(t, &configurableOCI{
		tags: []string{"1.0.0"},
		data: chartArchive(t, "apiVersion: v2\nname: demo\nversion: 1.0.0\nappVersion: 1.0.0\n"),
	}, nil)

	entry := &repo.Entry{Name: "ociRepo", URL: "oci://registry"}
	s := NewChartSearcher([]*repo.Entry{entry}, "", false)

	idx, err := s.loadIndex(entry)
	assert.NoError(t, err)
	if assert.NotNil(t, idx) {
		assert.Contains(t, idx.Entries, "ociRepo")
	}
}

// unsupportedReleaser is a release.Releaser implementation that the Helm SDK
// has no accessor for, which makes release.NewAccessor fail.
type unsupportedReleaser struct{ release.Releaser }

func TestConvertReleaseList_SkipsReleasesWithoutAnAccessor(t *testing.T) {
	helmRel := &releasev1.Release{
		Name:      "ok",
		Namespace: "ns",
		Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Name: "ok", Version: "1.0.0", AppVersion: "1.0.0"}},
	}

	out, err := convertReleaseList([]release.Releaser{&unsupportedReleaser{}, helmRel})
	assert.Error(t, err, "the skipped release must be reported")
	if assert.Len(t, out, 1) {
		assert.Equal(t, "ok", out[0].Name)
	}
}

func TestConvertReleaseList_SkipsReleasesWithoutAUsableChart(t *testing.T) {
	// A release whose chart or chart metadata is missing must be skipped with an
	// error instead of panicking inside the Helm chart accessor.
	for name, rel := range map[string]*releasev1.Release{
		"nil chart":    {Name: "broken", Namespace: "ns"},
		"nil metadata": {Name: "broken", Namespace: "ns", Chart: &chartv2.Chart{}},
	} {
		t.Run(name, func(t *testing.T) {
			good := &releasev1.Release{
				Name:      "ok",
				Namespace: "ns",
				Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Name: "ok", Version: "1.0.0", AppVersion: "1.0.0"}},
			}

			out, err := convertReleaseList([]release.Releaser{rel, good})
			assert.ErrorContains(t, err, "broken")
			if assert.Len(t, out, 1) {
				assert.Equal(t, "ok", out[0].Name)
			}
		})
	}
}

func TestConvertReleaseList_Empty(t *testing.T) {
	out, err := convertReleaseList(nil)
	assert.NoError(t, err)
	assert.Empty(t, out)
}

// fakeAPIServer starts an HTTP server that impersonates just enough of the
// Kubernetes API for Helm's action.Configuration to initialize and for the
// secret storage driver to list releases, and returns a kubeconfig path
// pointing at it.
func fakeAPIServer(t *testing.T, secrets []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"major":"1","minor":"33","gitVersion":"v1.33.0"}`))
		case "/api/v1/secrets":
			_, _ = w.Write(secrets)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	contents := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: %s
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
current-context: fake
users:
- name: fake
  user: {}
`, srv.URL)
	if err := os.WriteFile(kubeconfig, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return kubeconfig
}

// helmReleaseSecretList renders a Kubernetes SecretList holding a single Helm
// release record, encoded the way Helm's secret storage driver expects
// (gzipped JSON, base64 encoded, then base64 encoded again by the API's
// representation of secret data).
func helmReleaseSecretList(t *testing.T, releaseJSON string) []byte {
	t.Helper()
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	if _, err := gw.Write([]byte(releaseJSON)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	list := map[string]any{
		"kind":       "SecretList",
		"apiVersion": "v1",
		"metadata":   map[string]any{"resourceVersion": "1"},
		"items": []any{map[string]any{
			"metadata": map[string]any{
				"name":      "sh.helm.release.v1.redis.v1",
				"namespace": "data",
				"labels":    map[string]string{"owner": "helm", "name": "redis", "version": "1", "status": "deployed"},
			},
			"type": "helm.sh/release.v1",
			"data": map[string]string{
				"release": base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(gz.Bytes()))),
			},
		}},
	}
	body, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestFetchReleases_ReturnsClusterReleases(t *testing.T) {
	secrets := helmReleaseSecretList(t, `{"name":"redis","namespace":"data","version":1,"info":{"status":"deployed"},"chart":{"metadata":{"apiVersion":"v2","name":"redis","version":"14.8.8","appVersion":"6.2.5"}}}`)

	t.Setenv("HELM_DRIVER", "secret")
	settings := cli.New()
	settings.KubeConfig = fakeAPIServer(t, secrets)

	releases, err := FetchReleases(settings, false)
	assert.NoError(t, err)
	if assert.Len(t, releases, 1) {
		assert.Equal(t, Release{
			Name:         "redis",
			Namespace:    "data",
			Chart:        "redis-14.8.8",
			ChartName:    "redis",
			ChartVersion: "14.8.8",
			AppVersion:   "6.2.5",
		}, releases[0])
	}
}

func TestFetchReleases_DebugPrintsLoadedReleases(t *testing.T) {
	secrets := helmReleaseSecretList(t, `{"name":"redis","namespace":"data","version":1,"info":{"status":"deployed"},"chart":{"metadata":{"apiVersion":"v2","name":"redis","version":"14.8.8","appVersion":"6.2.5"}}}`)

	t.Setenv("HELM_DRIVER", "secret")
	settings := cli.New()
	settings.KubeConfig = fakeAPIServer(t, secrets)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	releases, fetchErr := FetchReleases(settings, true)
	_ = w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	assert.NoError(t, fetchErr)
	assert.Len(t, releases, 1)
	assert.Contains(t, string(out), "Debug: loaded release redis in namespace data (chart: redis-14.8.8, app_version: 6.2.5)")
}

func TestFetchReleases_NoReleases(t *testing.T) {
	// The in-memory driver has no stored releases, so a reachable cluster
	// yields an empty result rather than an error.
	t.Setenv("HELM_DRIVER", "memory")
	settings := cli.New()
	settings.KubeConfig = fakeAPIServer(t, nil)

	releases, err := FetchReleases(settings, false)
	assert.NoError(t, err)
	assert.Empty(t, releases)
}

func TestFetchReleases_InitError(t *testing.T) {
	t.Setenv("HELM_DRIVER", "not-a-driver")
	settings := cli.New()

	_, err := FetchReleases(settings, false)
	assert.ErrorContains(t, err, "failed to initialize Helm config")
}

func TestFetchReleases_Error(t *testing.T) {
	settings := cli.New()
	// point at an invalid kubeconfig to force Init failure
	settings.KubeConfig = "/nonexistent/invalid"
	_, err := FetchReleases(settings, false)
	assert.Error(t, err, "expected error when kubeconfig invalid")
}
