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
	"os"

	"helm-upgrade-check-plugin/pkg/upgradecheck"

	"github.com/fatih/color"
	"helm.sh/helm/v4/pkg/cli"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

var exitFunc = os.Exit
var Version = "dev"

func main() {
	var debug bool
	var jsonOut bool
	var version bool
	var includePrerel bool

	flag.BoolVar(&debug, "debug", false, "enable debug output")
	flag.BoolVar(&debug, "d", false, "shorthand for --debug")
	flag.BoolVar(&jsonOut, "json", false, "output results as JSON")
	flag.BoolVar(&jsonOut, "j", false, "shorthand for --json")
	flag.BoolVar(&version, "version", false, "print version and exit")
	flag.BoolVar(&includePrerel, "include-prerelease", false, "consider pre-release versions as upgradable")
	flag.Parse()

	if version {
		fmt.Println("helm-upgrade-check version", Version)
		return
	}

	settings := cli.New()
	if debug {
		fmt.Printf("Debug: kubeconfig=%s, namespace=%s\n", settings.KubeConfig, settings.Namespace())
	}

	if !jsonOut {
		fmt.Print("Loading repository list...")
	}
	repoEntries, err := loadRepoEntriesFunc(settings)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error loading repo entries:", err)
		exitFunc(1)
	}
	if !jsonOut {
		fmt.Println("done!")
	}

	searcher := newChartSearcherFunc(repoEntries, settings.RepositoryCache, includePrerel)

	if !jsonOut {
		fmt.Print("Fetching Helm releases from all namespaces...")
	}
	releases, err := fetchReleasesFunc(settings, debug)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error retrieving releases:", err)
		exitFunc(1)
	}
	if !jsonOut {
		fmt.Println("done!")
	}

	// repoResult carries one repo's own version info for a chart, since a
	// chart mirrored across multiple repos can have a different version (and
	// upgradability) in each one.
	type repoResult struct {
		Repo               string `json:"repo"`
		LatestChartVersion string `json:"latest_chart_version"`
		LatestAppVersion   string `json:"latest_app_version"`
		Upgradable         bool   `json:"upgradable"`
	}

	type resultItem struct {
		ChartName             string       `json:"chart_name"`
		ReleaseName           string       `json:"release_name"`
		Namespace             string       `json:"namespace"`
		InstalledChartVersion string       `json:"installed_chart_version"`
		InstalledAppVersion   string       `json:"installed_app_version"`
		Repos                 []repoResult `json:"repos"`
		Upgradable            bool         `json:"upgradable"`
		Commands              []string     `json:"commands,omitempty"`
	}

	var results []resultItem
	var errors []upgradecheck.MissingChartError
	for _, rel := range releases {
		chartName := upgradecheck.ChartName(rel.Chart)
		installedChartVer := rel.ChartVersion
		if installedChartVer == "" {
			installedChartVer = "Unknown"
		}
		installedAppVer := rel.AppVersion
		if installedAppVer == "" {
			installedAppVer = "Unknown"
		}
		info := searcher.Search(chartName)
		if len(info.Versions) == 0 {
			errors = append(errors, upgradecheck.MissingChartError{Release: rel.Name, Namespace: rel.Namespace, Chart: chartName})
			continue
		}

		var repos []repoResult
		var commands []string
		upgradable := false
		for _, v := range info.Versions {
			latestChartVer := v.Version
			if latestChartVer == "" || latestChartVer == "null" {
				latestChartVer = "N/A"
			}
			// Compare chart versions — this is what helm upgrade --version accepts.
			chartNewer := upgradecheck.CompareVersions(latestChartVer, installedChartVer, includePrerel)
			// Guard: if both app versions are valid semver and the candidate's app
			// version is strictly older, suppress the upgrade signal. Equal app
			// versions and non-semver app versions return false (no regression), so
			// chart-only bumps still flag and unusual app version schemes fall back
			// to the chart-version decision.
			appRegresses := upgradecheck.CompareVersions(installedAppVer, v.AppVersion, includePrerel)
			repoUpgradable := chartNewer && !appRegresses
			repos = append(repos, repoResult{
				Repo:               v.Repo,
				LatestChartVersion: latestChartVer,
				LatestAppVersion:   v.AppVersion,
				Upgradable:         repoUpgradable,
			})
			if repoUpgradable {
				upgradable = true
				if len(commands) == 0 {
					commands = append(commands,
						fmt.Sprintf("helm get values --namespace %s %s -o yaml > %s.values", rel.Namespace, rel.Name, rel.Name),
						fmt.Sprintf("cat %s.values", rel.Name),
					)
				}
				commands = append(commands, fmt.Sprintf("helm upgrade --namespace %s %s %s/%s --version %s --values %s.values", rel.Namespace, rel.Name, v.Repo, chartName, latestChartVer, rel.Name))
			}
		}

		if !upgradable {
			// Nothing to upgrade to, so repos that aren't even running the
			// installed chart/app version are just noise (they're either
			// behind, or blocked by the app-regression guard) — keep only the
			// repo(s) that corroborate what's actually running. If none match
			// (e.g. the installed version isn't any repo's current latest),
			// fall back to showing everything rather than hiding the release.
			var matched []repoResult
			for _, rv := range repos {
				if rv.LatestChartVersion == installedChartVer && rv.LatestAppVersion == installedAppVer {
					matched = append(matched, rv)
				}
			}
			if len(matched) > 0 {
				repos = matched
			}
		}

		results = append(results, resultItem{
			ChartName:             chartName,
			ReleaseName:           rel.Name,
			Namespace:             rel.Namespace,
			InstalledChartVersion: installedChartVer,
			InstalledAppVersion:   installedAppVer,
			Repos:                 repos,
			Upgradable:            upgradable,
			Commands:              commands,
		})
	}

	if jsonOut {
		out := map[string]interface{}{
			"results":        results,
			"missing_charts": errors,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintln(os.Stderr, "error encoding JSON output:", err)
			os.Exit(1)
		}
		return
	}

	// Human output: print table then upgrade commands.
	// A "Running Version" column shows the installed chart version so it can
	// be compared against each repo's own Chart/App Version columns directly
	// — there's no reliable way to know which repo a running release actually
	// came from, so an "old -> new" arrow on a per-repo line would wrongly
	// imply that repo is the upgrade path.
	printFormat := "%-25s %-25s %-25s %-20s %-18s %-18s %-18s\n"
	upToDatePrintf := color.New(color.FgGreen).PrintfFunc()
	upgradablePrintf := color.New(color.FgBlue).PrintfFunc()
	fmt.Println()
	fmt.Printf(printFormat, "Chart Name", "Release Name", "Namespace", "Repo(s)", "Running Version", "Chart Version", "App Version")
	fmt.Printf(printFormat, "----------", "------------", "---------", "-------", "---------------", "-------------", "-----------")
	var upgradableResults []resultItem
	for _, r := range results {
		// A chart found in multiple repos gets one line per repo, each showing
		// that repo's own chart/app version rather than a shared value.
		for _, rv := range r.Repos {
			if rv.Upgradable {
				upgradablePrintf(printFormat, r.ChartName, r.ReleaseName, r.Namespace, rv.Repo, r.InstalledChartVersion, rv.LatestChartVersion, rv.LatestAppVersion)
			} else {
				upToDatePrintf(printFormat, r.ChartName, r.ReleaseName, r.Namespace, rv.Repo, r.InstalledChartVersion, rv.LatestChartVersion, rv.LatestAppVersion)
			}
		}
		if r.Upgradable {
			upgradableResults = append(upgradableResults, r)
		}
	}

	if len(upgradableResults) > 0 {
		fmt.Println("\n\nUpgrade commands:")
		fmt.Println("─────────────────────────────────────────────────────────────────────────────────────")
		for _, r := range upgradableResults {
			fmt.Printf("\n%s (%s):\n", r.ReleaseName, r.Namespace)
			for _, cmd := range r.Commands {
				fmt.Printf("  %s\n", cmd)
			}
		}
	}

	if len(errors) > 0 {
		fmt.Println("\n\nUnable to find chart information in any repo for the following releases:")
		printFormat = "%-20s %-20s %-20s\n"
		fmt.Printf(printFormat, "Release", "Namespace", "Chart")
		fmt.Printf(printFormat, "-------", "---------", "-----")
		for _, e := range errors {
			fmt.Printf(printFormat, e.Release, e.Namespace, e.Chart)
		}
	}
}

type chartSearcher interface {
	Search(string) upgradecheck.ChartSearchResult
}

var loadRepoEntriesFunc = func(settings *cli.EnvSettings) ([]*repo.Entry, error) {
	return upgradecheck.LoadRepoEntries(settings)
}

var fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
	return upgradecheck.FetchReleases(settings, debug)
}

var newChartSearcherFunc = func(repos []*repo.Entry, cacheDir string, includePrerel bool) chartSearcher {
	return upgradecheck.NewChartSearcher(repos, cacheDir, includePrerel)
}
