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
	"testing"
)

func TestChartName(t *testing.T) {
	cases := []struct {
		chart, version, want string
	}{
		{"redis-14.8.8", "14.8.8", "redis"},
		{"nginx-1.2.3", "1.2.3", "nginx"},
		{"simple", "", "simple"},
		{"complex-name-0.1", "0.1", "complex-name"},
		// Versions carrying pre-release or build metadata contain "-", so
		// splitting on the last "-" is not enough.
		{"redis-1.0.0-beta.1", "1.0.0-beta.1", "redis"},
		{"my-chart-2.3.4-rc.1+build.5", "2.3.4-rc.1+build.5", "my-chart"},
		// Version unknown: fall back to splitting on the last "-".
		{"redis-14.8.8", "", "redis"},
		// Version that doesn't match the suffix is ignored.
		{"redis-14.8.8", "9.9.9", "redis"},
	}
	for _, c := range cases {
		if got := ChartName(c.chart, c.version); got != c.want {
			t.Errorf("ChartName(%q, %q)=%q, want %q", c.chart, c.version, got, c.want)
		}
	}
}

func TestMissingChartError_Error(t *testing.T) {
	err := MissingChartError{Release: "redis", Namespace: "default", Chart: "redis"}

	if got := err.Error(); got != `release "redis" in namespace "default" uses chart "redis", which was not found in any configured repository` {
		t.Fatalf("unexpected error: %s", got)
	}
}
