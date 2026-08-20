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
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"helm-upgrade-check-plugin/pkg/upgradecheck"

	"github.com/fatih/color"
	"helm.sh/helm/v4/pkg/cli"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

var exitFunc = os.Exit
var Version = "dev"

type joinedError interface {
	error
	Unwrap() []error
}

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
	warnings := []string{}
	seenWarnings := map[string]struct{}{}
	addWarning := func(message string) {
		if _, ok := seenWarnings[message]; ok {
			return
		}
		seenWarnings[message] = struct{}{}
		warnings = append(warnings, message)
		fmt.Fprintln(os.Stderr, "warning:", message)
	}
	if len(repoEntries) == 0 {
		addWarning("no repositories are configured; every release will be reported as missing")
	}

	searcher := newChartSearcherFunc(repoEntries, settings.RepositoryCache, includePrerel)

	if !jsonOut {
		fmt.Print("Fetching Helm releases from all namespaces...")
	}
	releases, err := fetchReleasesFunc(settings, debug)
	if err != nil {
		if len(releases) == 0 {
			fmt.Fprintln(os.Stderr, "error retrieving releases:", err)
			exitFunc(1)
			return
		}
		for _, component := range flattenWarningErrors(err) {
			addWarning(fmt.Sprintf("release could not be decoded: %v", component))
		}
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
	var missingCharts []upgradecheck.MissingChartError
	for _, rel := range releases {
		chartName := rel.ChartName
		if chartName == "" {
			chartName = upgradecheck.ChartName(rel.Chart, rel.ChartVersion)
		}
		installedChartVer := upgradecheck.DisplayValue(rel.ChartVersion, "Unknown")
		installedAppVer := upgradecheck.DisplayValue(rel.AppVersion, "Unknown")
		info, searchErr := searcher.Search(chartName)
		if searchErr != nil {
			var chartSearchErr *upgradecheck.ChartSearchError
			if errors.As(searchErr, &chartSearchErr) && chartSearchErr.FailedRepos == chartSearchErr.TotalRepos && len(repoEntries) > 0 {
				fmt.Fprintf(os.Stderr, "error searching for chart %q: all configured repositories failed to load their indexes: %v\n", chartName, searchErr)
				exitFunc(1)
				return
			}
			for _, component := range flattenWarningErrors(searchErr) {
				addWarning(component.Error())
			}
		}
		if len(info.Versions) == 0 {
			missingCharts = append(missingCharts, upgradecheck.MissingChartError{Release: rel.Name, Namespace: rel.Namespace, Chart: chartName})
			continue
		}

		var repos []repoResult
		var commands []string
		var recommended *repoResult
		upgradable := false
		for _, v := range info.Versions {
			latestChartVer := upgradecheck.DisplayValue(v.Version, "N/A")
			// Compare chart versions — this is what helm upgrade --version accepts.
			chartNewer := upgradecheck.CompareVersions(latestChartVer, installedChartVer, includePrerel)
			// Guard: if both app versions are valid semver and the candidate's app
			// version is strictly older, suppress the upgrade signal. Equal app
			// versions and non-semver app versions return false (no regression), so
			// chart-only bumps still flag and unusual app version schemes fall back
			// to the chart-version decision.
			appRegresses := upgradecheck.CompareVersions(installedAppVer, v.AppVersion, includePrerel)
			repoUpgradable := chartNewer && !appRegresses
			// Normalized the same way as the installed app version so the
			// noise filter below can match a chart that has none.
			latestAppVer := upgradecheck.DisplayValue(v.AppVersion, "Unknown")
			rr := repoResult{
				Repo:               v.Repo,
				LatestChartVersion: latestChartVer,
				LatestAppVersion:   latestAppVer,
				Upgradable:         repoUpgradable,
			}
			if repoUpgradable {
				upgradable = true
				rr.UpgradeCommand = upgradecheck.UpgradeCommand(rel.Name, rel.Namespace, v.Repo, chartName, latestChartVer)
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
			commands = append(upgradecheck.ValuesCommands(rel.Name, rel.Namespace), recommended.UpgradeCommand)
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
			"missing_charts": missingCharts,
			"warnings":       warnings,
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
	printTableHeader(printFormat, "Chart Name", "Release Name", "Namespace", "Repo(s)", "Running Version", "Chart Version", "App Version")
	var upgradableResults []resultItem
	for _, r := range results {
		// A chart found in multiple repos gets one line per repo, each showing
		// that repo's own chart/app version rather than a shared value.
		for _, rv := range r.Repos {
			printRow := upToDatePrintf
			if rv.Upgradable {
				printRow = upgradablePrintf
			} else if r.Upgradable {
				printRow = behindPrintf
			}
			printRow(printFormat, r.ChartName, r.ReleaseName, r.Namespace, rv.Repo, r.InstalledChartVersion, rv.LatestChartVersion, rv.LatestAppVersion)
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

	if len(missingCharts) > 0 {
		fmt.Println("\n\nUnable to find chart information in any repo for the following releases:")
		printFormat = "%-20s %-20s %-20s\n"
		printTableHeader(printFormat, "Release", "Namespace", "Chart")
		for _, e := range missingCharts {
			fmt.Printf(printFormat, e.Release, e.Namespace, e.Chart)
		}
	}
}

func flattenWarningErrors(err error) []error {
	if err == nil {
		return nil
	}
	if unwrap, ok := err.(interface{ Unwrap() error }); ok {
		if joined, ok := unwrap.Unwrap().(joinedError); ok {
			return flattenJoinedErrors(joined)
		}
	}
	return flattenJoinedErrors(err)
}

func flattenJoinedErrors(err error) []error {
	joined, ok := err.(joinedError)
	if !ok {
		return []error{err}
	}
	var components []error
	for _, component := range joined.Unwrap() {
		if _, ok := component.(joinedError); ok {
			components = append(components, flattenJoinedErrors(component)...)
			continue
		}
		components = append(components, component)
	}
	return components
}

type chartSearcher interface {
	Search(string) (upgradecheck.ChartSearchResult, error)
}

// printTableHeader writes the column titles followed by a rule of dashes
// matching each title's width, using the shared row format.
func printTableHeader(format string, titles ...string) {
	cells := make([]any, len(titles))
	rule := make([]any, len(titles))
	for i, title := range titles {
		cells[i] = title
		rule[i] = strings.Repeat("-", len(title))
	}
	fmt.Printf(format, cells...)
	fmt.Printf(format, rule...)
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
