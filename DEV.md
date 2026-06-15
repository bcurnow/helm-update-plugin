# Developer Guide

## Prerequisites

- Go 1.25 or later
- GNU Make
- [GoReleaser](https://goreleaser.com/install/) (for releases)
- A GPG key registered at `dev@curnowtopia.com` (for signing release artifacts)

## Building

```bash
make build
```

Outputs the binary to `bin/helm-upgrade-check`.

## Testing

```bash
make test
```

Run benchmarks:

```bash
make bench
```

Run tests with coverage:

```bash
make coverage
```

Generate an HTML coverage report:

```bash
make coverage-html
```

## Full Build Pipeline

```bash
make all
```

Runs tidy, fmt, vet, test, and build in sequence.

## Local Development Install

Build the plugin and install it into your local Helm plugin directory:

```bash
make install-dev
```

This removes any existing `upgrade-check` plugin, then installs from the local source tree so changes are immediately testable with `helm upgrade-check`.

## Cross-Compilation

```bash
GOOS=darwin GOARCH=amd64 make build
GOOS=linux  GOARCH=amd64 make build
GOOS=windows GOARCH=amd64 make build
```

## Releasing a New Version

Releases are published to GitHub via [GoReleaser](https://goreleaser.com). GoReleaser determines the version from the git tag, so the tag must be created and pushed before running the release target.

### Additional Prerequisites

- A GitHub personal access token with `repo` scope saved to `~/.config/goreleaser/helm-upgrade-check-plugin-github-token`
- A GPG key and its passphrase (you will be prompted for the passphrase during the release)

### Step-by-step

1. **Prepare the release** — updates `plugin.yaml` to the new version:

   ```bash
   make prepare-release TAG=X.Y.Z
   git add plugin.yaml CHANGELOG.md
   git commit -m "Release vX.Y.Z"
   ```

2. **Create and push an annotated tag:**

   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

3. **Run the release:**

   ```bash
   make release
   ```

   You will be prompted for your GPG passphrase. GoReleaser then:
   - Builds binaries for Linux, macOS, and Windows (amd64 + arm64)
   - Packages each into a signed Helm plugin archive (`.tgz` + `.tgz.prov`)
   - Publishes a GitHub Release with the signed archives attached

### Test a Release Locally (Snapshot)

Run GoReleaser in snapshot mode to validate the full build pipeline without publishing:

```bash
make test-release
```

Artifacts are written to `dist/` and not uploaded anywhere.

## Lint

```bash
make lint
```

Requires [golangci-lint](https://golangci-lint.run/welcome/install/).

## Clean

```bash
make clean
```

Removes `bin/`, `dist/`, and coverage artifacts.
