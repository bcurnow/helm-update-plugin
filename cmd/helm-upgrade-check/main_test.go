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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helm-upgrade-check-plugin/pkg/upgradecheck"

	"github.com/fatih/color"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/helmpath"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

type fakeSearcher struct {
	res upgradecheck.ChartSearchResult
}

func (f *fakeSearcher) Search(_ string) (upgradecheck.ChartSearchResult, error) { return f.res, nil }

func TestMain_JSONOutput(t *testing.T) {
	// save originals
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	// stub functions
	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}

	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{Name: "rel1", Namespace: "ns", Chart: "mychart-1.0", ChartVersion: "1.0", AppVersion: "1.0"}}, nil
	}

	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{{Repo: "testrepo", Version: "2.0", AppVersion: "2.0-app"}},
		}}
	}

	// capture stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w

	// simulate --json flag
	os.Args = []string{"cmd", "--json"}

	// run
	main()

	// restore stdout and read
	_ = w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v\noutput: %s", err, string(out))
	}
	if warnings, ok := parsed["warnings"].([]interface{}); !ok || len(warnings) == 0 {
		t.Fatalf("expected warnings array in JSON output, got %v", parsed["warnings"])
	}

	results, ok := parsed["results"].([]interface{})
	if !ok {
		t.Fatalf("results not present or wrong type: %v", parsed["results"])
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0].(map[string]interface{})
	cmds, ok := res["commands"].([]interface{})
	repos, _ := res["repos"].([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %v", res["repos"])
	}
	repo0 := repos[0].(map[string]interface{})
	if repo0["latest_chart_version"] != "2.0" {
		t.Fatalf("expected latest_chart_version 2.0, got %v", repo0["latest_chart_version"])
	}
	if res["installed_chart_version"] != "1.0" {
		t.Fatalf("expected installed_chart_version 1.0, got %v", res["installed_chart_version"])
	}
	if up, _ := res["upgradable"].(bool); !up {
		t.Fatalf("expected upgradable=true")
	}
	if !ok || len(cmds) != 3 {
		t.Fatalf("expected 3 commands in JSON, got %v", res["commands"])
	}
	// The --version flag must use the chart version, not the app version.
	upgradeCmd, _ := cmds[2].(string)
	if !strings.Contains(upgradeCmd, "--version 2.0") {
		t.Fatalf("upgrade command must use chart version for --version, got: %s", upgradeCmd)
	}
	if strings.Contains(upgradeCmd, "--version 2.0-app") {
		t.Fatalf("upgrade command must not use app version for --version, got: %s", upgradeCmd)
	}
}

func TestMain_ChartVersionDiffersFromAppVersion(t *testing.T) {
	// ingress-nginx: chart 4.9.1, app 1.9.1. The upgrade command must pass the
	// chart version (4.10.0) to --version, not the app version (1.10.1).
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "ingress-nginx",
			Namespace:    "ingress",
			Chart:        "ingress-nginx-4.9.1",
			ChartVersion: "4.9.1",
			AppVersion:   "1.9.1",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				{Repo: "ingress-nginx", Version: "4.10.0", AppVersion: "1.10.1"},
			},
		}}
	}

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "--json"}
	main()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v\n%s", err, out)
	}
	results := parsed["results"].([]interface{})
	res := results[0].(map[string]interface{})

	if res["installed_chart_version"] != "4.9.1" {
		t.Errorf("installed_chart_version: got %v, want 4.9.1", res["installed_chart_version"])
	}
	repos, _ := res["repos"].([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %v", res["repos"])
	}
	repo0 := repos[0].(map[string]interface{})
	if repo0["latest_chart_version"] != "4.10.0" {
		t.Errorf("latest_chart_version: got %v, want 4.10.0", repo0["latest_chart_version"])
	}
	if res["installed_app_version"] != "1.9.1" {
		t.Errorf("installed_app_version: got %v, want 1.9.1", res["installed_app_version"])
	}
	if repo0["latest_app_version"] != "1.10.1" {
		t.Errorf("latest_app_version: got %v, want 1.10.1", repo0["latest_app_version"])
	}
	if up, _ := res["upgradable"].(bool); !up {
		t.Error("expected upgradable=true when chart version 4.9.1 < 4.10.0")
	}
	cmds, _ := res["commands"].([]interface{})
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(cmds))
	}
	upgradeCmd, _ := cmds[2].(string)
	if !strings.Contains(upgradeCmd, "--version 4.10.0") {
		t.Errorf("--version must use chart version 4.10.0, got: %s", upgradeCmd)
	}
	if strings.Contains(upgradeCmd, "1.10.1") || strings.Contains(upgradeCmd, "1.9.1") {
		t.Errorf("--version must not use app version, got: %s", upgradeCmd)
	}
}

func TestMain_LoadRepoEntriesError(t *testing.T) {
	origLoad := loadRepoEntriesFunc
	origExit := exitFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		exitFunc = origExit
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return nil, fmt.Errorf("fail")
	}
	var code int
	exitFunc = func(c int) { code = c; panic("exit") }

	// reset flags between test invocations
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	defer func() {
		r := recover()
		if r != "exit" {
			t.Fatalf("unexpected panic: %v", r)
		}
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	}()
	main()
}

func TestMain_FetchReleasesError(t *testing.T) {
	origFetch := fetchReleasesFunc
	origExit := exitFunc
	origArgs := os.Args
	defer func() {
		fetchReleasesFunc = origFetch
		exitFunc = origExit
		os.Args = origArgs
	}()

	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return nil, fmt.Errorf("no cluster")
	}
	var code int
	exitFunc = func(c int) { code = c; panic("exit") }

	// reset flags between test invocations
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
	defer func() {
		r := recover()
		if r != "exit" {
			t.Fatalf("unexpected panic: %v", r)
		}
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	}()
	main()
}

func TestMain_HumanOutput(t *testing.T) {
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{
			{Name: "r1", Namespace: "ns", Chart: "c-1.0", ChartVersion: "1.0", AppVersion: "1.0"},
			{Name: "r2", Namespace: "ns", Chart: "d-2.0", ChartVersion: "2.0", AppVersion: "2.0"},
		}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{{Repo: "repo", Version: "2.0"}},
		}}
	}

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "--debug"}
	main()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	if !strings.Contains(string(out), "Debug:") {
		t.Fatalf("debug message not printed")
	}
	if !strings.Contains(string(out), "Loading repository list") {
		t.Fatalf("load message missing")
	}
	if !strings.Contains(string(out), "Fetching Helm releases") {
		t.Fatalf("fetch message missing")
	}
}

func TestMain_AppVersionRegression_NotUpgradable(t *testing.T) {
	// Cilium-style: a higher chart version (3.1.9) exists but ships an older
	// app version (1.18.1 < installed 1.19.1). Must NOT be shown as upgradable.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "cilium",
			Namespace:    "kube-system",
			Chart:        "cilium-1.19.1",
			ChartVersion: "1.19.1",
			AppVersion:   "1.19.1",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				// higher chart version … but ships an older app version
				{Repo: "some-repo", Version: "3.1.9", AppVersion: "1.18.1"},
			},
		}}
	}

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "--json"}
	main()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	res := parsed["results"].([]interface{})[0].(map[string]interface{})
	if up, _ := res["upgradable"].(bool); up {
		t.Error("expected upgradable=false when latest chart ships an older app version")
	}
	if cmds, _ := res["commands"].([]interface{}); len(cmds) != 0 {
		t.Errorf("expected no upgrade commands when app would regress, got %v", cmds)
	}
}

func TestMain_ChartOnlyBump_IsUpgradable(t *testing.T) {
	// Chart version bumped (packaging fix) but app version is identical.
	// Must still show as upgradable per user preference.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "myapp",
			Namespace:    "default",
			Chart:        "myapp-1.0.0",
			ChartVersion: "1.0.0",
			AppVersion:   "2.0.0",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				// chart version bumped … same app version
				{Repo: "myrepo", Version: "1.1.0", AppVersion: "2.0.0"},
			},
		}}
	}

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "--json"}
	main()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	res := parsed["results"].([]interface{})[0].(map[string]interface{})
	if up, _ := res["upgradable"].(bool); !up {
		t.Error("expected upgradable=true when chart version bumped with same app version")
	}
	cmds, _ := res["commands"].([]interface{})
	if len(cmds) != 3 {
		t.Errorf("expected 3 upgrade commands, got %d", len(cmds))
	}
	upgradeCmd, _ := cmds[2].(string)
	if !strings.Contains(upgradeCmd, "--version 1.1.0") {
		t.Errorf("upgrade command should use chart version 1.1.0, got: %s", upgradeCmd)
	}
}

func TestMain_MultipleRepos_EachKeepsItsOwnVersion(t *testing.T) {
	// A chart found in two repos with different versions must report each
	// repo's own version rather than duplicating one repo's version onto the
	// other's line, and the upgrade command for each repo must reference that
	// repo's own version.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "redis",
			Namespace:    "default",
			Chart:        "redis-1.0.0",
			ChartVersion: "1.0.0",
			AppVersion:   "7.0.0",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				// r1 is already up to date at the installed version.
				{Repo: "r1", Version: "1.0.0", AppVersion: "7.0.0"},
				// bitnami has a newer version — must NOT be excluded, and must
				// not leak its version onto r1's line.
				{Repo: "bitnami", Version: "2.0.0", AppVersion: "7.2.0"},
			},
		}}
	}

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd", "--json"}
	main()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)

	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	res := parsed["results"].([]interface{})[0].(map[string]interface{})
	if up, _ := res["upgradable"].(bool); !up {
		t.Error("expected upgradable=true since bitnami offers a newer version")
	}

	repos, _ := res["repos"].([]interface{})
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %v", res["repos"])
	}
	byRepo := map[string]map[string]interface{}{}
	for _, ri := range repos {
		rm := ri.(map[string]interface{})
		byRepo[rm["repo"].(string)] = rm
	}

	r1 := byRepo["r1"]
	if r1["latest_chart_version"] != "1.0.0" {
		t.Errorf("r1 latest_chart_version: got %v, want 1.0.0", r1["latest_chart_version"])
	}
	if up, _ := r1["upgradable"].(bool); up {
		t.Error("r1 should not be upgradable — it's already at the installed version")
	}

	bitnami := byRepo["bitnami"]
	if bitnami["latest_chart_version"] != "2.0.0" {
		t.Errorf("bitnami latest_chart_version: got %v, want 2.0.0", bitnami["latest_chart_version"])
	}
	if up, _ := bitnami["upgradable"].(bool); !up {
		t.Error("bitnami should be upgradable — it has a newer version than installed")
	}

	// Only one upgrade command set should be generated (for bitnami), and it
	// must reference bitnami's own version, not r1's.
	cmds, _ := res["commands"].([]interface{})
	if len(cmds) != 3 {
		t.Fatalf("expected 3 commands (one repo is upgradable), got %d: %v", len(cmds), cmds)
	}
	upgradeCmd, _ := cmds[2].(string)
	if !strings.Contains(upgradeCmd, "bitnami/redis --version 2.0.0") {
		t.Errorf("upgrade command must use bitnami's own version, got: %s", upgradeCmd)
	}
}

// runMainCapturingOutput redirects both os.Stdout and color.Output (which is
// captured once at init from the original os.Stdout and does not track later
// reassignment — the "up to date"/"upgradable" rows print via
// color.PrintfFunc, so it must be redirected too) around a call to main, and
// returns everything written to stdout.
func runMainCapturingOutput(t *testing.T, args []string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	oldColorOutput := color.Output
	os.Stdout = w
	color.Output = w

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = args
	main()

	_ = w.Close()
	os.Stdout = oldStdout
	color.Output = oldColorOutput
	out, _ := io.ReadAll(r)
	return string(out)
}

func runMainCapturingStdoutStderr(t *testing.T, args []string) (string, string) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldColorOutput := color.Output
	oldArgs := os.Args
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		color.Output = oldColorOutput
		os.Args = oldArgs
	}()
	os.Stdout = stdoutW
	os.Stderr = stderrW
	color.Output = stdoutW
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ExitOnError)
	os.Args = args
	main()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	stdout, _ := io.ReadAll(stdoutR)
	stderr, _ := io.ReadAll(stderrR)
	return string(stdout), string(stderr)
}

func TestMain_SearchFailureWarnsAndKeepsSuccessfulRepo(t *testing.T) {
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
	}()

	cacheDir := t.TempDir()
	indexPath := filepath.Join(cacheDir, helmpath.CacheIndexFile("good"))
	if err := os.WriteFile(indexPath, []byte(`apiVersion: v1
entries:
  demo:
    - name: demo
      version: 2.0.0
      appVersion: 2.0.0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	repos := []*repo.Entry{{Name: "good"}, {Name: "bad"}}
	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return repos, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{
			{Name: "demo-one", Namespace: "default", Chart: "demo-1.0.0", ChartVersion: "1.0.0", AppVersion: "1.0.0"},
			{Name: "demo-two", Namespace: "staging", Chart: "demo-1.0.0", ChartVersion: "1.0.0", AppVersion: "1.0.0"},
		}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, _ string, includePrerel bool) chartSearcher {
		return upgradecheck.NewChartSearcher(repos, cacheDir, includePrerel)
	}

	origArgs := os.Args
	os.Args = []string{"cmd", "--json"}
	stdout, stderr := runMainCapturingStdoutStderr(t, os.Args)
	os.Args = origArgs

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	warnings, ok := parsed["warnings"].([]interface{})
	if !ok || len(warnings) != 1 {
		t.Fatalf("expected exactly one deduplicated search warning in JSON output: %v", parsed["warnings"])
	}
	if strings.Count(stderr, "warning:") != 1 || !strings.Contains(stderr, `repo "bad"`) {
		t.Fatalf("expected search warning on stderr, got %q", stderr)
	}
	if strings.Contains(stderr, "search for chart") {
		t.Fatalf("search warning should not include a chart wrapper: %q", stderr)
	}
	results := parsed["results"].([]interface{})
	if len(results) != 2 {
		t.Fatalf("expected both releases in results: %v", parsed["results"])
	}
	for _, result := range results {
		if len(result.(map[string]interface{})["repos"].([]interface{})) != 1 {
			t.Fatalf("expected successful repo to remain in results: %v", parsed["results"])
		}
	}
}

func TestMain_AllRepositorySearchFailureIsFatal(t *testing.T) {
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origExit := exitFunc
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		exitFunc = origExit
	}()

	cacheDir := t.TempDir()
	repos := []*repo.Entry{{Name: "bad-one"}, {Name: "bad-two"}}
	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return repos, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{Name: "demo", Namespace: "default", Chart: "demo-1.0.0", ChartVersion: "1.0.0", AppVersion: "1.0.0"}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, _ string, includePrerel bool) chartSearcher {
		return upgradecheck.NewChartSearcher(repos, cacheDir, includePrerel)
	}
	var code int
	exitFunc = func(c int) { code = c }

	origArgs := os.Args
	os.Args = []string{"cmd", "--json"}
	stdout, stderr := runMainCapturingStdoutStderr(t, os.Args)
	os.Args = origArgs

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if stdout != "" {
		t.Fatalf("fatal search failure must not emit JSON, got %q", stdout)
	}
	if !strings.Contains(stderr, "all configured repositories failed") {
		t.Fatalf("expected all-repositories-failed error, got %q", stderr)
	}
}

func TestMain_PartialReleaseDecodeWarnsAndContinues(t *testing.T) {
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{{Name: "repo"}}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{Name: "demo", Namespace: "default", Chart: "demo-1.0.0", ChartVersion: "1.0.0", AppVersion: "1.0.0"}}, fmt.Errorf("release at index 1 could not be decoded")
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{{Repo: "repo", Version: "2.0.0", AppVersion: "2.0.0"}},
		}}
	}

	origArgs := os.Args
	os.Args = []string{"cmd", "--json"}
	stdout, stderr := runMainCapturingStdoutStderr(t, os.Args)
	os.Args = origArgs

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout)
	}
	warnings, ok := parsed["warnings"].([]interface{})
	if !ok || len(warnings) == 0 || !strings.Contains(warnings[0].(string), "release could not be decoded") {
		t.Fatalf("expected partial release warning in JSON output: %v", parsed["warnings"])
	}
	if !strings.Contains(stderr, "warning:") {
		t.Fatalf("expected partial release warning on stderr, got %q", stderr)
	}
	if len(parsed["results"].([]interface{})) != 1 {
		t.Fatalf("expected successfully decoded release in results: %v", parsed["results"])
	}
}

// grafanaRepoLines finds the "grafana" and "grafana-community" repo rows (if
// present) among the printed table lines for a release in "monitoring".
func grafanaRepoLines(out string) (grafanaLine, communityLine string) {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Upgrade commands:") {
			break // only scan the table, not the generated helm upgrade commands
		}
		if !strings.Contains(l, "monitoring") {
			continue
		}
		if strings.Contains(l, "grafana-community") {
			communityLine = l
		} else if strings.Contains(l, "grafana") {
			grafanaLine = l
		}
	}
	return grafanaLine, communityLine
}

func TestMain_HumanOutput_SanitizesAndQuotesUntrustedValues(t *testing.T) {
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "release;echo pwn\x1b",
			Namespace:    "namespace\nname",
			Chart:        "chart;echo pwn-1.0.0",
			ChartVersion: "1.0.0",
			AppVersion:   "1.0.0",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{{
				Repo:       "repo;echo pwn",
				Version:    "2.0.0",
				AppVersion: "2.0.0\x1b",
			}},
		}}
	}

	out := runMainCapturingOutput(t, []string{"cmd"})
	table := strings.SplitN(out, "Upgrade commands:", 2)[0]
	if strings.Contains(table, "\x1b") {
		t.Fatalf("human-readable table contains an ANSI escape: %q", table)
	}
	if !strings.Contains(table, "release;echo pwn") {
		t.Fatalf("human-readable table omitted printable release text: %q", table)
	}
	if !strings.Contains(out, "helm get values --namespace namespacename 'release;echo pwn'") {
		t.Fatalf("upgrade commands did not quote untrusted values: %q", out)
	}
	if !strings.Contains(out, "helm upgrade --namespace namespacename 'release;echo pwn' 'repo;echo pwn'/'chart;echo pwn'") {
		t.Fatalf("helm upgrade command did not quote untrusted values: %q", out)
	}
}

func TestMain_HumanOutput_UpgradableRelease_EachRepoShowsItsOwnVersion(t *testing.T) {
	// Regression test: the human-readable table used to print the release's
	// installed version for every repo line instead of that repo's own
	// available version. That made two repos with genuinely different
	// versions (e.g. grafana at 10.5.15 vs grafana-community at 13.0.0, with
	// 12.11.0 installed) both display the same version, hiding the real data.
	// The release here is upgradable (grafana-community offers a newer
	// version), so both repo rows are kept — see the companion test below for
	// the not-upgradable case, where the non-matching row is dropped instead.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "grafana",
			Namespace:    "monitoring",
			Chart:        "grafana-12.11.0",
			ChartVersion: "12.11.0",
			AppVersion:   "13.2.0",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				// grafana repo lags behind what's installed — not upgradable,
				// but it must still show its own (older) version, not 12.11.0.
				{Repo: "grafana", Version: "10.5.15", AppVersion: "12.3.1"},
				// grafana-community offers a newer version, making the release
				// upgradable overall.
				{Repo: "grafana-community", Version: "13.0.0", AppVersion: "14.0.0"},
			},
		}}
	}

	out := runMainCapturingOutput(t, []string{"cmd"})
	grafanaLine, communityLine := grafanaRepoLines(out)

	// Columns: Chart Name, Release Name, Namespace, Repo(s), Running Version,
	// Chart Version, App Version.
	gf := strings.Fields(grafanaLine)
	cf := strings.Fields(communityLine)
	if len(gf) != 7 {
		t.Fatalf("expected 7 fields in grafana repo line, got %d: %v", len(gf), gf)
	}
	if len(cf) != 7 {
		t.Fatalf("expected 7 fields in grafana-community repo line, got %d: %v", len(cf), cf)
	}

	if gf[4] != "12.11.0" {
		t.Errorf("grafana repo line running version: got %q, want 12.11.0 (the installed version)", gf[4])
	}
	if gf[5] != "10.5.15" || gf[6] != "12.3.1" {
		t.Errorf("grafana repo line must show its own chart/app version 10.5.15/12.3.1, got chart=%q app=%q", gf[5], gf[6])
	}

	if cf[4] != "12.11.0" {
		t.Errorf("grafana-community repo line running version: got %q, want 12.11.0", cf[4])
	}
	if cf[5] != "13.0.0" || cf[6] != "14.0.0" {
		t.Errorf("grafana-community repo line must show its own chart/app version 13.0.0/14.0.0, got chart=%q app=%q", cf[5], cf[6])
	}
}

func TestMain_NotUpgradable_DropsRepoEntriesNotMatchingRunningVersion(t *testing.T) {
	// When a release isn't upgradable, a repo whose own latest doesn't match
	// what's actually running is just noise (it's either behind or blocked by
	// the app-regression guard) — it should be dropped rather than printed
	// alongside the repo that corroborates the running version.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "grafana",
			Namespace:    "monitoring",
			Chart:        "grafana-12.11.0",
			ChartVersion: "12.11.0",
			AppVersion:   "13.2.0",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				// grafana repo lags behind what's installed — not upgradable,
				// and doesn't match the running version, so it should be
				// dropped from the output entirely.
				{Repo: "grafana", Version: "10.5.15", AppVersion: "12.3.1"},
				// grafana-community exactly matches what's installed — not
				// upgradable, but it corroborates the running version and
				// must be kept.
				{Repo: "grafana-community", Version: "12.11.0", AppVersion: "13.2.0"},
			},
		}}
	}

	out := runMainCapturingOutput(t, []string{"cmd", "--json"})

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	res := parsed["results"].([]interface{})[0].(map[string]interface{})
	repos, _ := res["repos"].([]interface{})
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (grafana dropped, grafana-community kept), got %v", res["repos"])
	}
	repo0 := repos[0].(map[string]interface{})
	if repo0["repo"] != "grafana-community" {
		t.Errorf("expected surviving repo to be grafana-community, got %v", repo0["repo"])
	}

	humanOut := runMainCapturingOutput(t, []string{"cmd"})
	grafanaLine, communityLine := grafanaRepoLines(humanOut)
	if grafanaLine != "" {
		t.Errorf("grafana repo line should have been dropped, got: %q", grafanaLine)
	}
	if communityLine == "" {
		t.Error("grafana-community repo line should still be present")
	}
}

// stubPluginFuncs replaces the indirection hooks main() uses with stubs
// returning the supplied releases and search result, restoring the originals
// (and os.Args) when the test finishes.
func stubPluginFuncs(t *testing.T, releases []upgradecheck.Release, res upgradecheck.ChartSearchResult) {
	t.Helper()
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	t.Cleanup(func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	})

	loadRepoEntriesFunc = func(_ *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(_ *cli.EnvSettings, _ bool) ([]upgradecheck.Release, error) {
		return releases, nil
	}
	newChartSearcherFunc = func(_ []*repo.Entry, _ string, _ bool) chartSearcher {
		return &fakeSearcher{res: res}
	}
}

func TestMain_VersionFlag(t *testing.T) {
	// --version prints the version and exits before touching repos or the
	// cluster, so the stubs must never be called.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		os.Args = origArgs
	}()
	loadRepoEntriesFunc = func(_ *cli.EnvSettings) ([]*repo.Entry, error) {
		t.Error("repo entries must not be loaded when --version is given")
		return nil, nil
	}
	fetchReleasesFunc = func(_ *cli.EnvSettings, _ bool) ([]upgradecheck.Release, error) {
		t.Error("releases must not be fetched when --version is given")
		return nil, nil
	}

	out := runMainCapturingOutput(t, []string{"cmd", "--version"})
	if !strings.Contains(out, "helm-upgrade-check version "+Version) {
		t.Errorf("expected version output, got: %q", out)
	}
}

func TestMain_MissingChart_ReportedSeparately(t *testing.T) {
	// A release whose chart isn't in any repo can't be evaluated, so it is
	// listed under "Unable to find chart information" instead of the table.
	stubPluginFuncs(t,
		[]upgradecheck.Release{{Name: "mystery", Namespace: "ns", Chart: "mystery-1.0.0", ChartVersion: "1.0.0", AppVersion: "1.0.0"}},
		upgradecheck.ChartSearchResult{},
	)

	out := runMainCapturingOutput(t, []string{"cmd", "--json"})
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if results, _ := parsed["results"].([]interface{}); len(results) != 0 {
		t.Errorf("expected no results for a chart missing from every repo, got %v", results)
	}
	missing, _ := parsed["missing_charts"].([]interface{})
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing chart, got %v", parsed["missing_charts"])
	}
	entry := missing[0].(map[string]interface{})
	if entry["Release"] != "mystery" || entry["Namespace"] != "ns" || entry["Chart"] != "mystery" {
		t.Errorf("unexpected missing chart entry: %v", entry)
	}

	humanOut := runMainCapturingOutput(t, []string{"cmd"})
	if !strings.Contains(humanOut, "Unable to find chart information in any repo") {
		t.Errorf("expected the missing chart section in human output, got: %q", humanOut)
	}
	if !strings.Contains(humanOut, "mystery") {
		t.Errorf("expected the missing release to be listed, got: %q", humanOut)
	}
	if strings.Contains(humanOut, "Upgrade commands:") {
		t.Errorf("no upgrade commands should be printed for a missing chart, got: %q", humanOut)
	}
}

func TestMain_UnknownInstalledVersions(t *testing.T) {
	// A release without chart/app versions in its metadata still has to be
	// reported — as "Unknown" — rather than silently dropped.
	stubPluginFuncs(t,
		[]upgradecheck.Release{{Name: "rel", Namespace: "ns", Chart: "chart"}},
		upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{{Repo: "r1", Version: "2.0.0", AppVersion: "2.0.0"}},
		},
	)

	out := runMainCapturingOutput(t, []string{"cmd", "--json"})
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	res := parsed["results"].([]interface{})[0].(map[string]interface{})
	if res["installed_chart_version"] != "Unknown" {
		t.Errorf("installed_chart_version: got %v, want Unknown", res["installed_chart_version"])
	}
	if res["installed_app_version"] != "Unknown" {
		t.Errorf("installed_app_version: got %v, want Unknown", res["installed_app_version"])
	}
	// "Unknown" isn't semver, so no upgrade can be claimed.
	if up, _ := res["upgradable"].(bool); up {
		t.Error("expected upgradable=false when the installed version is unknown")
	}
}

func TestMain_MissingRepoVersionShownAsNA(t *testing.T) {
	// Index entries can carry an empty or literal "null" version; those are
	// displayed as N/A and never treated as an upgrade.
	for _, version := range []string{"", "null"} {
		t.Run("version="+version, func(t *testing.T) {
			stubPluginFuncs(t,
				[]upgradecheck.Release{{Name: "rel", Namespace: "ns", Chart: "chart-1.0.0", ChartVersion: "1.0.0", AppVersion: "1.0.0"}},
				upgradecheck.ChartSearchResult{
					Versions: []upgradecheck.RepoChartVersion{{Repo: "r1", Version: version, AppVersion: "1.0.0"}},
				},
			)

			out := runMainCapturingOutput(t, []string{"cmd", "--json"})
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("unmarshal: %v\n%s", err, out)
			}
			res := parsed["results"].([]interface{})[0].(map[string]interface{})
			repo0 := res["repos"].([]interface{})[0].(map[string]interface{})
			if repo0["latest_chart_version"] != "N/A" {
				t.Errorf("latest_chart_version: got %v, want N/A", repo0["latest_chart_version"])
			}
			if up, _ := res["upgradable"].(bool); up {
				t.Error("expected upgradable=false when the repo has no usable version")
			}
		})
	}
}

func TestMain_NotUpgradable_NoRepoMatchesRunning_KeepsAllEntries(t *testing.T) {
	// Edge case: if the installed version doesn't match ANY repo's current
	// latest (e.g. it's since been superseded everywhere, or was never any
	// repo's "latest"), dropping non-matching rows would hide the release
	// entirely. Fall back to showing everything instead.
	origLoad := loadRepoEntriesFunc
	origFetch := fetchReleasesFunc
	origNew := newChartSearcherFunc
	origArgs := os.Args
	defer func() {
		loadRepoEntriesFunc = origLoad
		fetchReleasesFunc = origFetch
		newChartSearcherFunc = origNew
		os.Args = origArgs
	}()

	loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
		return []*repo.Entry{}, nil
	}
	fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
		return []upgradecheck.Release{{
			Name:         "grafana",
			Namespace:    "monitoring",
			Chart:        "grafana-12.11.0",
			ChartVersion: "12.11.0",
			AppVersion:   "13.2.0",
		}}, nil
	}
	newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{
			Versions: []upgradecheck.RepoChartVersion{
				// Older than installed — not upgradable.
				{Repo: "grafana", Version: "10.5.15", AppVersion: "12.3.1"},
				// Higher chart version but app version regresses — blocked,
				// not upgradable, and doesn't match installed either.
				{Repo: "grafana-community", Version: "13.0.0", AppVersion: "12.0.0"},
			},
		}}
	}

	out := runMainCapturingOutput(t, []string{"cmd", "--json"})

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	res := parsed["results"].([]interface{})[0].(map[string]interface{})
	repos, _ := res["repos"].([]interface{})
	if len(repos) != 2 {
		t.Fatalf("expected both repos kept as a fallback since neither matches running, got %v", res["repos"])
	}
}
