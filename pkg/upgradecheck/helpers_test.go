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
	"fmt"
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

	res := searcher.Search("redis")
	assert.Equal(t, "2.0.0", res.Version)
	assert.Equal(t, "7.2.0", res.AppVersion)
	assert.ElementsMatch(t, []string{"r1", "bitnami"}, res.Repos)

	// ensure caching works
	searcher.idxCache = map[string]*repo.IndexFile{}
	res2 := searcher.Search("redis")
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
		res := searcher.Search("grafana")

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
	r := s.Search("nonexistent")
	assert.Empty(t, r.Repos)
	assert.Equal(t, "", r.Version)
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

	res := s.Search("mychart")
	// Version is the chart version (OCI tag "1.0.0"), not the app version.
	assert.Equal(t, "1.0.0", res.Version)
	assert.Equal(t, "5.5.5", res.AppVersion)
	assert.Equal(t, []string{"ociRepo"}, res.Repos)
}

func TestPrintUpgradeCommands(t *testing.T) {
	buf := &bytes.Buffer{}
	PrintUpgradeCommands(buf, "rel", "ns", "repo1", "chart", "1.2.3")
	out := buf.String()
	assert.Contains(t, out, "helm get values --namespace ns rel")
	assert.Contains(t, out, "helm upgrade --namespace ns rel repo1/chart --version 1.2.3")
}

func TestConvertReleaseList(t *testing.T) {
	helmRel := &releasev1.Release{
		Name:      "r",
		Namespace: "n",
		Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Version: "1.2.3", AppVersion: "5.6.7"}},
	}
	out := convertReleaseList([]release.Releaser{helmRel})
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
	out := convertReleaseList([]release.Releaser{helmRel})
	if assert.Len(t, out, 1) {
		assert.Equal(t, "ingress-nginx-4.9.1", out[0].Chart)
		assert.Equal(t, "4.9.1", out[0].ChartVersion)
		assert.Equal(t, "1.9.1", out[0].AppVersion)
	}
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
	res := s.Search("cilium")
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
	res := s.Search("cilium")
	assert.Equal(t, "1.20.0-rc.1", res.Version)
	assert.Equal(t, "1.20.0-rc.1", res.AppVersion)
}

func TestFetchReleases_Error(t *testing.T) {
	settings := cli.New()
	// point at an invalid kubeconfig to force Init failure
	settings.KubeConfig = "/nonexistent/invalid"
	_, err := FetchReleases(settings, false)
	assert.Error(t, err, "expected error when kubeconfig invalid")
}
