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
	"fmt"
	"strings"
	"unicode"
)

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

// Error returns a description of the missing chart and its release.
func (e MissingChartError) Error() string {
	return fmt.Sprintf("release %q in namespace %q uses chart %q, which was not found in any configured repository", e.Release, e.Namespace, e.Chart)
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

// SanitizeDisplay removes non-printable runes before untrusted values are
// written to a human-readable terminal.
func SanitizeDisplay(value string) string {
	var sanitized strings.Builder
	for _, r := range value {
		if unicode.IsPrint(r) {
			sanitized.WriteRune(r)
		}
	}
	return sanitized.String()
}

// ShellQuote returns a shell-safe representation of a command argument.
// Values containing only conservative shell-safe characters are returned
// unchanged to preserve the existing command output for ordinary values.
func ShellQuote(value string) string {
	value = SanitizeDisplay(value)
	if value != "" && shellSafe(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func shellSafe(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune("_+-./:@%=,", r) {
			continue
		}
		return false
	}
	return true
}
