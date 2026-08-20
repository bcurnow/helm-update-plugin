# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Silently dropped repository index failures** — repository cache, parsing, and OCI lookup errors are now surfaced instead of making charts appear absent.
- **Silently dropped undecodable releases** — release conversion failures are returned alongside successfully decoded releases so they remain visible to the caller.
- **Swallowed OCI accessor errors** — failures to access metadata from a loaded OCI chart are now propagated.

### Changed

- **Warnings are reported on stderr and in JSON** — non-fatal search and release conversion problems produce deduplicated `warning:` lines and are included in the top-level JSON `warnings` array.
- **All-repositories-failed searches are fatal** — the plugin exits with status 1 when every configured repository index fails to load because no comparison is possible.
- **Error-handling API changes** — `ChartSearcher.Search` and `PrintUpgradeCommands` now return errors, and `MissingChartError` now implements the `error` interface.

## [2.1.0] - 2026-08-19

### Fixed

- **Data race in concurrent repository index loading** — `ChartSearcher.loadIndex` is called from one goroutine per repo, but the `idxCache` map it read from and wrote to had no synchronization. Go maps are not safe for concurrent access even across distinct keys, so this could corrupt results whenever a chart existed in multiple repos — e.g. `grafana` and `grafana-community` both reporting the same (wrong) chart/app version instead of their own. `idxCache` is now guarded by a mutex. Confirmed with `go test -race` before and after the fix.
- **Human-readable table showed the installed version instead of each repo's own version** for "up to date" (green) rows. Once the data race above was fixed, the JSON output was already correct per-repo, but the table's non-upgradable branch still printed `r.InstalledChartVersion`/`r.InstalledAppVersion` for every repo row, so e.g. `grafana` and `grafana-community` both displayed the same version even though `grafana`'s own latest (`10.5.15`) genuinely lagged behind what was installed (`12.11.0`, from `grafana-community`).

### Changed

- **Index lookup now reads the local Helm cache** — the plugin no longer downloads repository indexes from the network on each run. Instead it reads the same `$HELM_REPOSITORY_CACHE/<repo>-index.yaml` files that `helm search repo` uses (populated by `helm repo update`). This aligns the plugin's view of available versions with the rest of the Helm toolchain and avoids redundant network traffic.
- **Multi-repo results now print one line per repo** instead of a single comma-separated repo list, in both the results table and the generated `helm upgrade` commands. Each repo is evaluated independently against its own chart/app version — a mirrored repo with a different version no longer has another repo's version incorrectly attributed to it, and `--version` in each generated command now matches that specific repo's own chart version.
- **Table layout: dropped the `old → new` arrow, added a `Running Version` column.** There's no reliable way to know which configured repo a running release was actually installed from (Helm doesn't persist that), so showing a per-repo `installed → latest` arrow implied a repo was the upgrade path when it might not be. The table now has a `Running Version` column (the installed chart version, shown once per release) alongside plain `Chart Version`/`App Version` columns per repo — compare them yourself rather than trusting an arrow tied to the wrong repo.
- **Non-upgradable releases drop repo rows that don't match the running version.** Once a release has no valid upgrade path, a repo whose own latest chart/app version doesn't equal what's actually installed is just noise (it's behind, or blocked by the app-regression guard). Only the repo(s) confirming the running version are kept; if none match exactly, all repos are shown rather than hiding the release. Applies to both the table and JSON output.

### Removed

- **`--update` / `-u` flag** — replaced by the standard `helm repo update` workflow. Run `helm repo update` before `helm upgrade-check` the same way you would before `helm search repo`.
- **Bitnami special-casing** — Bitnami is no longer excluded from version selection when a chart also exists in another repo. It is now treated like any other repository: every repo containing the chart is reported and evaluated on its own version.

## [2.0.0] - 2026-06-14

### Changed

- **Helm 4 SDK migration**: All Go source imports updated from `helm.sh/helm/v3` to `helm.sh/helm/v4`. The plugin now uses the Helm 4 SDK's accessor pattern (`release.NewAccessor`, `chart.NewAccessor`) to read release and chart metadata, and `settings.RESTClientGetter()` instead of constructing `genericclioptions.ConfigFlags` directly. Repository types use `helm.sh/helm/v4/pkg/repo/v1`, chart types use `helm.sh/helm/v4/pkg/chart/v2`, and concrete release types use `helm.sh/helm/v4/pkg/release/v1`.
- **Helm 4 plugin manifest**: `plugin.yaml` migrated to the Helm 4 `cli/v1` schema (`apiVersion: v1`, `type: cli/v1`, `runtime: subprocess`, `runtimeConfig`). The plugin now installs natively under Helm 4 and no longer supports the Helm 3 CLI.

### Breaking Changes

- **Requires Helm 4**: The plugin binary is built against the Helm 4 Go SDK and the `plugin.yaml` uses the Helm 4 manifest schema. It cannot be installed under Helm 3.

## [1.0.3] - 2026-06-14

### Fixed

- **Pre-release versions masking available stable upgrades**: The chart searcher was taking `versions[0]` from each repo index, which Helm sorts newest-first including pre-releases. If the top entry was e.g. `1.20.0-rc.1`, the subsequent `CompareVersions` call with `--include-prerelease=false` returned false and the release appeared up-to-date even though a newer stable version (e.g. `1.19.4`) was available. The searcher now iterates the index and skips pre-release entries unless `--include-prerelease` is set.

### Changed

- **Upgrade row color scheme**: Rows with an available upgrade now display the installed version in red and the latest version in blue (e.g. `1.19.1 -> 1.19.4`) instead of the entire row in a single blue. Makes version transitions immediately visible at a glance.
- **Makefile `VERSION` derived from git tag**: `VERSION` is now computed via `git describe --tags --abbrev=0` rather than hardcoded, so the Makefile no longer needs to be edited before each release.

## [1.0.2] - 2026-06-05

### Added

- **Makefile targets**: `fmt` (`go fmt`), `vet` (`go vet`), `lint` (`golangci-lint run`), `check` (CI gate: vet + lint + test), `generate` (`go generate`), `bench` (benchmarks), `coverage`, `coverage-html`, `install-dev` (build and reinstall plugin locally)
- **Chart version and app version in output**: Both chart version and app version are now shown as separate columns, each displaying `<current>` when up-to-date or `<current> -> <latest>` when an upgrade is available
- **App version in JSON output**: New fields `installed_chart_version`, `latest_chart_version`, `installed_app_version`, `latest_app_version` in `--json` results
- **Comprehensive release documentation**: README now includes step-by-step release instructions, noting that a git tag must exist on HEAD before `make release`
- **`scripts/install.sh`**: Install hook that detects OS and architecture at install time, downloads the correct binary from the GitHub Release assets, and places it in `$HELM_PLUGIN_DIR/bin/`. Runs automatically on `helm plugin install` and `helm plugin update`

### Changed

- **`helm upgrade --version` now uses chart version**: Previously used the application version, which caused failures for charts where chart version and app version differ (e.g. `ingress-nginx` chart `4.9.1` / app `1.9.1`)
- **Upgrade commands block moved below table**: All upgrade commands are printed together after the full table rather than inline with each row
- **Repo column position**: Moved to after Namespace and before Chart Version
- **`make all` now includes fmt and vet**: Runs `tidy fmt vet test build` instead of just `tidy test build`
- **`plugin.yaml` install model**: Replaced the six `platformCommand` entries (pointing at pre-committed binaries) with an `install`/`update` hook that downloads the correct binary from the GitHub Release at install time. `plugin.yaml` now uses a single `command` path (`$HELM_PLUGIN_DIR/bin/helm-upgrade-check`) with a two-entry `platformCommand` only for the Windows `.exe` suffix
- **GoReleaser now produces `tar.gz` archives**: Changed from `formats: [binary]` to `formats: [tar.gz]` so the install hook can download and extract predictably-named archives

### Fixed

- **App version regression guard**: A newer chart version is no longer flagged as an upgrade when the candidate chart ships an older application version (e.g. a chart from a different repository with higher chart version numbering but lower app version). Only suppressed when both app versions are valid semver and the candidate is strictly older; chart-only bumps and non-semver app versions still flag normally
- **Lint issues**: Resolved all `errcheck`, `staticcheck` (deprecated `io/ioutil`), and `govet` issues reported by `golangci-lint`

## [1.0.1] - 2026-03-05

Updated the name of the repository and then various references across the code base.

## [1.0.0] - 2024-01-15

### Added

- **Core Plugin Functionality**
  - Helm plugin that identifies release versions differing from latest in repositories
  - Supports all configured Helm repositories
  - Displays out-of-date and up-to-date releases with clear visual indicators

- **Smart Version Comparison**
  - Semantic version comparison (1.15.2 > 1.14.5)
  - Handles version prefix stripping (v1.0 vs 1.0)
  - Compares across multiple repositories for latest version

- **Performance Optimizations**
  - On-demand repository index loading (only load what you need)
  - Per-repository index caching (avoid re-downloading)
  - Result memoization (avoid re-searching same chart)
  - O(unique_charts × repos) complexity instead of O(charts × releases)

- **Chart Repository Intelligence**
  - Automatic Bitnami repository deduplication
  - Handles charts in multiple repositories
  - Clear repository attribution in output

- **Colored Terminal Output**
  - Blue for out-of-date releases (requires attention)
  - Green for up-to-date releases (no action needed)
  - White for upgrade commands
  - Auto-detection of TTY (colors disabled when piped)

- **Upgrade Command Suggestions**
  - Displays three essential Helm commands:
    - `helm get values` - view current release configuration
    - `helm get values ... | less` - review before upgrading
    - `helm upgrade` - perform the update
  - Commands indented under release for easy copying

- **Comprehensive Documentation**
  - README.md: Features, installation, usage, architecture, troubleshooting
  - INSTALL.md: Multiple installation methods and setup guides
  - CONTRIBUTING.md: Development guidelines and contribution process
  - ARCHITECTURE.md: Technical design and component documentation

- **Build Automation**
  - Makefile with targets: all, build, test, clean, tidy, help, version, release
  - Automated version injection via ldflags
  - Release build target for production builds

- **Test Coverage**
  - Unit tests for core functions (ChartName, FindRepos, FindUpgradeVersion, NeedsUpgrade)
  - In-memory test data (no external dependencies)
  - All 4 tests passing

- **Command-line Flags**
  - `--update` / `-u`: Show only out-of-date releases
  - `--debug` / `-d`: Print debug information

### Technical Details

- **Language:** Go 1.21+
- **Helm SDK:** v3 (pkg/action, pkg/cli, pkg/getter, pkg/repo)
- **Dependencies:**
  - helm.sh/helm/v3 - Helm package management
  - k8s.io/cli-runtime - Kubernetes configuration
  - github.com/fatih/color - Terminal colors

- **Architecture:**
  - `pkg/upgradecheck/` - Core business logic
  - `cmd/helm-upgrade-check/` - CLI entrypoint
  - On-demand chart searcher with caching
  - Semantic version comparison algorithm



### Known Limitations

- Simple semver comparison (doesn't handle complex pre-release ordering)
- Single comma-separated repository string in output (not individually selectable per repo)
- No JSON output format (text-only currently)
- No filtering by release name or namespace

### Performance Characteristics

| Operation | Complexity | Time (100 releases, 5 repos) |
|-----------|-----------|-----|
| Total execution | O(R × Repos) | ~2-3 seconds |
| Repository loading | O(1) | Instant |
| Release listing | O(R) | ~1 second |
| Chart searching | O(unique_charts × Repos) | ~1 second |
| Output formatting | O(R) | <100ms |

R = number of installed releases

### System Requirements

- Go 1.21 or later (to build from source)
- Helm 3.0+ (deployed in your environment)
- kubectl configured and accessible
- Access to configured Helm repositories (network)

### Tested On

- macOS 13.x, 14.x
- Linux (Ubuntu 20.04, 22.04)
- Kubernetes 1.24 - 1.28
- Helm 3.8, 3.10, 3.12, 3.13

## Planned for Future Releases

### [1.1.0] (Planned)

- [ ] Filter by release name/namespace regex
- [ ] Dry-run upgrade preview mode
- [ ] Webhook/Slack notifications

### [1.2.0] (Planned)

- [ ] Parallel repository downloads
- [ ] Persistent cache between runs
- [ ] Incremental repository checks
- [ ] Configuration file support (~/.helm/upgrade-check.yaml)

### [2.0.0] (Planned - Major Changes)

- [ ] Helm operator integration
- [ ] Custom comparison strategies
- [ ] Advanced filtering and searching
- [ ] Performance improvements for 1000+ releases

## Changelog Template for Future Versions

```markdown
## [X.Y.Z] - YYYY-MM-DD

### Added
- New features

### Changed
- Changes in existing functionality

### Deprecated
- Soon-to-be removed features

### Removed
- Removed features

### Fixed
- Bug fixes

### Security
- Security fixes
```

## Version History Summary

| Version | Date | Focus | Status |
|---------|------|-------|--------|
| 0.1.0 | Initial | Bash script prototype | Deprecated |
| 1.0.0 | 2024-01-15 | Go plugin, Helm SDK, optimizations, docs | Released |
| 1.0.1 | 2026-03-05 | Repository rename and reference updates | Released |
| 1.0.2 | 2026-06-05 | Chart/app version accuracy, output improvements, Makefile CI targets | Released |
| 1.0.3 | 2026-06-14 | Fix pre-release masking stable upgrades; red/blue upgrade color scheme | **Current** |

## Upgrade Instructions

### From 0.1.0 (Bash Script)

1. Uninstall old plugin: `helm plugin uninstall upgrade-check`
2. Install new version: `helm plugin install https://github.com/...`
3. No configuration changes needed; YAML format compatible
4. Verify: `helm upgrade-check` should display results

### Within 1.x Releases

Simply reinstall/upgrade using the same installation method.

## Notes

- All changes maintain backward compatibility within the 1.x series
- Version 2.0.0 (when released) may introduce breaking changes
- Check [Releases](https://github.com/.../releases) page for binary downloads
- Submit issues and feature requests via GitHub Issues
