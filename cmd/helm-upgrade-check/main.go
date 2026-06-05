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
	"strings"

	"helm-upgrade-check-plugin/pkg/upgradecheck"

	"github.com/fatih/color"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
)

var exitFunc = os.Exit
var Version = "dev"

func main() {
	var update bool
	var debug bool
	var jsonOut bool
	var version bool
	var includePrerel bool

	flag.BoolVar(&update, "update", false, "run 'helm repo update' before checking")
	flag.BoolVar(&update, "u", false, "shorthand for --update")
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

	if update {
		if !jsonOut {
			fmt.Print("Updating Helm repositories...")
		}
		if err := updateRepositoriesFunc(settings); err != nil {
			fmt.Fprintln(os.Stderr, "error updating helm repos:", err)
			exitFunc(1)
		}
		if !jsonOut {
			fmt.Println("done!")
		}
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

	getters := getter.All(settings)
	searcher := newChartSearcherFunc(repoEntries, getters)

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

	type resultItem struct {
		ChartName             string   `json:"chart_name"`
		ReleaseName           string   `json:"release_name"`
		Namespace             string   `json:"namespace"`
		InstalledChartVersion string   `json:"installed_chart_version"`
		LatestChartVersion    string   `json:"latest_chart_version"`
		InstalledAppVersion   string   `json:"installed_app_version"`
		LatestAppVersion      string   `json:"latest_app_version"`
		Repos                 []string `json:"repos"`
		Upgradable            bool     `json:"upgradable"`
		Commands              []string `json:"commands,omitempty"`
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
		if len(info.Repos) == 0 {
			errors = append(errors, upgradecheck.MissingChartError{Release: rel.Name, Namespace: rel.Namespace, Chart: chartName})
			continue
		}
		latestChartVer := info.Version
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
		appRegresses := upgradecheck.CompareVersions(installedAppVer, info.AppVersion, includePrerel)
		upgradable := chartNewer && !appRegresses
		repoList := info.Repos

		var commands []string
		if upgradable {
			repoListStr := strings.Join(repoList, ",")
			commands = []string{
				fmt.Sprintf("helm get values --namespace %s %s -o yaml > %s.values", rel.Namespace, rel.Name, rel.Name),
				fmt.Sprintf("cat %s.values", rel.Name),
				// --version takes the chart version, not the app version.
				fmt.Sprintf("helm upgrade --namespace %s %s %s/%s --version %s --values %s.values", rel.Namespace, rel.Name, repoListStr, chartName, latestChartVer, rel.Name),
			}
		}

		results = append(results, resultItem{
			ChartName:             chartName,
			ReleaseName:           rel.Name,
			Namespace:             rel.Namespace,
			InstalledChartVersion: installedChartVer,
			LatestChartVersion:    latestChartVer,
			InstalledAppVersion:   installedAppVer,
			LatestAppVersion:      info.AppVersion,
			Repos:                 repoList,
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
	// Version columns show "<current>" when up-to-date or "<current> -> <latest>"
	// when an upgrade is available.
	printFormat := "%-25s %-25s %-25s %-25s %-35s %-35s\n"
	outOfDate := color.New(color.FgBlue)
	upToDate := color.New(color.FgGreen)
	fmt.Println()
	fmt.Printf(printFormat, "Chart Name", "Release Name", "Namespace", "Repo(s)", "Chart Version", "App Version")
	fmt.Printf(printFormat, "----------", "------------", "---------", "-------", "-------------", "-----------")
	var upgradableResults []resultItem
	for _, r := range results {
		repoListStr := strings.Join(r.Repos, ",")
		chartVer := r.InstalledChartVersion
		appVer := r.InstalledAppVersion
		if r.Upgradable {
			chartVer = r.InstalledChartVersion + " -> " + r.LatestChartVersion
			appVer = r.InstalledAppVersion + " -> " + r.LatestAppVersion
			outOfDate.Printf(printFormat, r.ChartName, r.ReleaseName, r.Namespace, repoListStr, chartVer, appVer)
			upgradableResults = append(upgradableResults, r)
		} else {
			upToDate.Printf(printFormat, r.ChartName, r.ReleaseName, r.Namespace, repoListStr, chartVer, appVer)
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

var updateRepositoriesFunc = func(settings *cli.EnvSettings) error {
	return upgradecheck.UpdateRepositories(settings)
}

var fetchReleasesFunc = func(settings *cli.EnvSettings, debug bool) ([]upgradecheck.Release, error) {
	return upgradecheck.FetchReleases(settings, debug)
}

var newChartSearcherFunc = func(repos []*repo.Entry, getters getter.Providers) chartSearcher {
	return upgradecheck.NewChartSearcher(repos, getters)
}
