# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
| 1.0.2 | 2026-06-05 | Chart/app version accuracy, output improvements, Makefile CI targets | **Current** |

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
