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
		return
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
		return
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
		// UpgradeCommand is the helm upgrade for this specific repo. Repos are
		// alternative sources for the same release, so at most one of them may
		// ever be run; Commands carries the recommended one.
		UpgradeCommand string `json:"upgrade_command,omitempty"`
	}

	type resultItem struct {
		ChartName             string       `json:"chart_name"`
		ReleaseName           string       `json:"release_name"`
		Namespace             string       `json:"namespace"`
		InstalledChartVersion string       `json:"installed_chart_version"`
		InstalledAppVersion   string       `json:"installed_app_version"`
		Repos                 []repoResult `json:"repos"`
		Upgradable            bool         `json:"upgradable"`
		// RecommendedRepo is the repo whose chart Commands upgrades to; any
		// other upgradable repo is an alternative to it, never an addition.
		RecommendedRepo string   `json:"recommended_repo,omitempty"`
		Commands        []string `json:"commands,omitempty"`
	}

	var results []resultItem
	var errors []upgradecheck.MissingChartError
	for _, rel := range releases {
		chartName := rel.ChartName
		if chartName == "" {
			chartName = upgradecheck.ChartName(rel.Chart, rel.ChartVersion)
		}
		installedChartVer := rel.ChartVersion
		if installedChartVer == "" {
			installedChartVer = "Unknown"
		}
		installedAppVer := rel.AppVersion
		if installedAppVer == "" {
			installedAppVer = "Unknown"
		}
		info := searcher.Search(chartName)
		if debug {
			// A repo whose index failed to load looks exactly like a repo that
			// doesn't carry the chart, so report it rather than letting the
			// release quietly turn up as "chart not found".
			for _, re := range info.Errors {
				fmt.Fprintf(os.Stderr, "Debug: repo %s unavailable while searching for chart %s: %v\n", re.Repo, chartName, re.Err)
			}
		}
		if len(info.Versions) == 0 {
			errors = append(errors, upgradecheck.MissingChartError{Release: rel.Name, Namespace: rel.Namespace, Chart: chartName})
			continue
		}

		var repos []repoResult
		var commands []string
		var recommended *repoResult
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
			latestAppVer := v.AppVersion
			if latestAppVer == "" {
				latestAppVer = "Unknown"
			}
			rr := repoResult{
				Repo:               v.Repo,
				LatestChartVersion: latestChartVer,
				LatestAppVersion:   latestAppVer,
				Upgradable:         repoUpgradable,
			}
			if repoUpgradable {
				upgradable = true
				rr.UpgradeCommand = fmt.Sprintf("helm upgrade --namespace %s %s %s/%s --version %s --values %s.values", rel.Namespace, rel.Name, v.Repo, chartName, latestChartVer, rel.Name)
			}
			repos = append(repos, rr)
		}

		// Every upgradable repo is an alternative source for the same release,
		// not a step in a sequence, so exactly one is recommended: the highest
		// chart version on offer. Running a second would immediately re-upgrade
		// the release from a different repo's chart.
		for i := range repos {
			if !repos[i].Upgradable {
				continue
			}
			if recommended == nil || upgradecheck.CompareVersions(repos[i].LatestChartVersion, recommended.LatestChartVersion, includePrerel) {
				recommended = &repos[i]
			}
		}
		recommendedRepo := ""
		if recommended != nil {
			recommendedRepo = recommended.Repo
			commands = []string{
				fmt.Sprintf("helm get values --namespace %s %s -o yaml > %s.values", rel.Namespace, rel.Name, rel.Name),
				fmt.Sprintf("cat %s.values", rel.Name),
				recommended.UpgradeCommand,
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
				// Both sides are normalized to "Unknown" when absent, so a
				// chart with no app version in the index still matches a
				// release installed without one.
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
			RecommendedRepo:       recommendedRepo,
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
			exitFunc(1)
			return
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
	// A repo offering nothing while another repo does is "behind", not "up to
	// date" — printing it green next to the blue row reads as if the release
	// were already current.
	behindPrintf := color.New(color.FgYellow).PrintfFunc()
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
			} else if r.Upgradable {
				behindPrintf(printFormat, r.ChartName, r.ReleaseName, r.Namespace, rv.Repo, r.InstalledChartVersion, rv.LatestChartVersion, rv.LatestAppVersion)
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
			// Alternatives are commented out and labelled because the block
			// above is meant to be pasted wholesale: a second live helm upgrade
			// would upgrade the release again from a different repo's chart.
			for _, rv := range r.Repos {
				if rv.Upgradable && rv.Repo != r.RecommendedRepo {
					fmt.Printf("  # alternative: %s offers %s instead (pick one, do not run both)\n", rv.Repo, rv.LatestChartVersion)
					fmt.Printf("  # %s\n", rv.UpgradeCommand)
				}
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
