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
	cases := map[string]string{
		"redis-14.8.8":     "redis",
		"nginx-1.2.3":      "nginx",
		"simple":           "simple",
		"complex-name-0.1": "complex-name",
	}
	for in, want := range cases {
		if got := ChartName(in); got != want {
			t.Errorf("ChartName(%q)=%q, want %q", in, got, want)
		}
	}
}
