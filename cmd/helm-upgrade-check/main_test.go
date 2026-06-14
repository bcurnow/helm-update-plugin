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
	"strings"
	"testing"

	"helm-upgrade-check-plugin/pkg/upgradecheck"

	"helm.sh/helm/v4/pkg/cli"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

type fakeSearcher struct {
	res upgradecheck.ChartSearchResult
}

func (f *fakeSearcher) Search(_ string) upgradecheck.ChartSearchResult { return f.res }

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
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{Repos: []string{"testrepo"}, Version: "2.0", AppVersion: "2.0-app"}}
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

	results, ok := parsed["results"].([]interface{})
	if !ok {
		t.Fatalf("results not present or wrong type: %v", parsed["results"])
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0].(map[string]interface{})
	cmds, ok := res["commands"].([]interface{})
	if res["latest_chart_version"] != "2.0" {
		t.Fatalf("expected latest_chart_version 2.0, got %v", res["latest_chart_version"])
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
			Repos:      []string{"ingress-nginx"},
			Version:    "4.10.0", // latest chart version
			AppVersion: "1.10.1", // latest app version
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
	if res["latest_chart_version"] != "4.10.0" {
		t.Errorf("latest_chart_version: got %v, want 4.10.0", res["latest_chart_version"])
	}
	if res["installed_app_version"] != "1.9.1" {
		t.Errorf("installed_app_version: got %v, want 1.9.1", res["installed_app_version"])
	}
	if res["latest_app_version"] != "1.10.1" {
		t.Errorf("latest_app_version: got %v, want 1.10.1", res["latest_app_version"])
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
		return &fakeSearcher{res: upgradecheck.ChartSearchResult{Repos: []string{"repo"}, Version: "2.0"}}
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
			Repos:      []string{"some-repo"},
			Version:    "3.1.9",  // higher chart version …
			AppVersion: "1.18.1", // … but ships an older app version
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
			Repos:      []string{"myrepo"},
			Version:    "1.1.0", // chart version bumped …
			AppVersion: "2.0.0", // … same app version
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
