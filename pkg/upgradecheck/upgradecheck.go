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
	"strings"
)

// Chart represents information about a Helm chart from a repository.
type Chart struct {
	Name       string // e.g. "stable/redis" or "bitnami/redis"
	AppVersion string // application version available for this chart
}

// Release represents a Helm release installed in the cluster.
type Release struct {
	Name         string // release name
	Namespace    string // Kubernetes namespace
	Chart        string // chart name with version, e.g. "redis-14.8.8"
	ChartVersion string // chart version only, e.g. "14.8.8" (authoritative for --version)
	AppVersion   string // application version of the deployed release
}

// MissingChartError is used to report releases that could not be matched
// to a chart in any configured repository.
type MissingChartError struct {
	Release   string
	Namespace string
	Chart     string
}

// ChartName strips the version suffix from the string that Helm returns
// in the `.chart` field of `helm list`.
// For example, "redis-14.8.8" becomes "redis".
func ChartName(chart string) string {
	if idx := strings.LastIndex(chart, "-"); idx != -1 {
		return chart[:idx]
	}
	return chart
}

// ChartIndex provides quick lookup of repository names and latest application
// versions for chart names.  Instead of scanning a slice for every release,
// the index is built once and then queried in constant time.

type ChartIndex struct {
	repos   map[string][]string // chartName -> list of repos
	version map[string]string   // chartName -> latest app version
}

// NewChartIndex constructs an index from a slice of charts.
func NewChartIndex(charts []Chart) *ChartIndex {
	idx := &ChartIndex{
		repos:   make(map[string][]string),
		version: make(map[string]string),
	}
	for _, c := range charts {
		if i := strings.Index(c.Name, "/"); i != -1 {
			chartName := c.Name[i+1:]
			repoName := c.Name[:i]
			// record repo
			idx.repos[chartName] = append(idx.repos[chartName], repoName)
			// update version; later entries overwrite earlier ones, matching
			// the behaviour of the old FindUpgradeVersion which kept the last
			// matching item.
			idx.version[chartName] = c.AppVersion
		}
	}
	// deduplicate and apply bitnami rule
	for chart, list := range idx.repos {
		uniq := make(map[string]struct{})
		for _, r := range list {
			uniq[r] = struct{}{}
		}
		out := make([]string, 0, len(uniq))
		for r := range uniq {
			out = append(out, r)
		}
		if len(out) > 1 {
			keep := make([]string, 0, len(out))
			for _, r := range out {
				if r == "bitnami" {
					continue
				}
				keep = append(keep, r)
			}
			if len(keep) > 0 {
				out = keep
			}
		}
		idx.repos[chart] = out
	}
	return idx
}

// Repos returns repository names containing chart.
func (ci *ChartIndex) Repos(chartName string) []string {
	return ci.repos[chartName]
}

// UpgradeVersion returns the latest app version for chart.
func (ci *ChartIndex) UpgradeVersion(chartName string) string {
	return ci.version[chartName]
}

// FindRepos returns the list of repositories that contain a chart with the
// given name.  Maintained for backwards compatibility if callers still use it.
func FindRepos(chartName string, charts []Chart) []string {
	return NewChartIndex(charts).Repos(chartName)
}

// FindUpgradeVersion returns the most recent app_version for a given chart
// name.  It iterates through the list and remembers the last matching entry,
// which replicates `jq 'last(...)'` behaviour from the shell version.
func FindUpgradeVersion(chartName string, charts []Chart) string {
	return NewChartIndex(charts).UpgradeVersion(chartName)
}
