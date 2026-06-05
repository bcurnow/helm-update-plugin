# helm-upgrade-check-plugin

A Helm plugin that identifies deployed Helm releases and checks for available updates across configured chart repositories.

## Overview

`helm-upgrade-check-plugin` scans your entire Kubernetes cluster for installed Helm releases and compares the deployed chart versions against the latest versions available in your configured repositories. For any releases that have newer versions available, the plugin provides direct copy-paste upgrade commands.

The plugin is designed for:
- **Quick vulnerability/security audits** — identify outdated deployments at a glance
- **Release management** — keep track of which releases need updates
- **Cluster maintenance** — reduce manual version checking across multiple releases

## Features

- **Cluster-wide scanning** — detects all releases across all namespaces
- **Multi-repository support** — checks every configured Helm repository
- **Smart repository selection** — deduplicates when a chart appears in more than one repo (Bitnami rule, etc.)
- **Efficient lookups** — cached, on‑demand index downloads; only fetch what’s needed
- **Parallel index fetches** — repository indexes are downloaded in parallel to improve speed
- **Retry/backoff** for transient network errors when downloading indexes
- **OCI registry support** — resolves `oci://` charts by querying tags and fetching manifests
- **Semantic version comparison** — full semver with prerelease support and fallback ordering
- **Colored human output** — blue for out‑of‑date, green for current
- **JSON mode** via `--json`/`-j` for machine‑readable results
- **Copy‑paste upgrade commands** — ready‑to‑run `helm` commands printed alongside outdated releases
- **Extensive test coverage** (including OCI path, JSON output, helpers) and easy build/test targets

## Installation

### Prerequisites

- Helm 3.1.0 or later
- A configured kubeconfig with cluster access
- One or more configured Helm repositories

### Install the Plugin

```bash
helm plugin install https://github.com/bcurnow/helm-upgrade-check-plugin.git
```

Or for direct installation from a local checkout:

```bash
git clone https://github.com/bcurnow/helm-upgrade-check-plugin.git
cd helm-upgrade-check-plugin
make build
helm plugin install .
```

### Uninstall

```bash
helm plugin uninstall upgrade-check
```

## Usage

### Basic Usage

```bash
helm upgrade-check
```

Lists all Helm releases in your cluster and shows which ones have updates available.

### Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--update` | `-u` | Run `helm repo update` before checking chart versions | false |
| `--debug` | `-d` | Enable debug output showing loaded charts and releases | false |

### Examples

#### Check all releases without updating repositories

```bash
helm upgrade-check
```

#### Update repositories before checking

This is useful if your repo indexes may be stale:

```bash
helm upgrade-check --update
```

#### Enable debug output

Shows detailed logs of which charts and releases are being loaded:

```bash
helm upgrade-check --debug
```

#### Combine flags

```bash
helm upgrade-check -u -d
```

## Output Format

The plugin outputs a table with the following columns:

```
Chart Name      Release Name    Namespace       Chart Ver     Latest Chart  App Ver       Latest App    Repo(s)
----------      ------------    ---------       ---------     ------------  -------       ----------    -------
```

| Column | Description |
|--------|-------------|
| **Chart Ver** | The chart version currently installed (used for `--version` in `helm upgrade`) |
| **Latest Chart** | The latest chart version available in your repositories |
| **App Ver** | The application version bundled with the installed chart |
| **Latest App** | The application version bundled with the latest chart release |

> **Note:** `helm upgrade --version` takes the **chart version**, not the application version. These differ for many charts — for example `ingress-nginx` chart `4.9.1` ships app version `1.9.1`. The plugin always uses the chart version for upgrade commands.

### Status Indicators

- **Blue text** — out-of-date release: a newer chart version is available **and** its app version does not regress
- **Green text** — up-to-date release, or a "newer" chart from a different repo ships an older app version

> A higher chart version is only flagged as an upgrade when the candidate chart's app version is equal to or greater than the installed app version. This prevents a chart from a different repository (with its own version numbering) being shown as an update when it would actually downgrade the running application. When app versions are non-semver or absent, the comparison falls back to chart version only.

### Upgrade Commands

For each out-of-date release, the plugin prints three commands:

1. **Get current values** — saves the release's current values to a file
2. **Review values** — displays the saved values for inspection
3. **Execute upgrade** — performs the actual upgrade with the latest **chart** version

Example output:

```
ingress-nginx   ingress-nginx   ingress    4.9.1         4.10.0        1.9.1         1.10.1        ingress-nginx
  helm get values --namespace ingress ingress-nginx -o yaml > ingress-nginx.values
  cat ingress-nginx.values
  helm upgrade --namespace ingress ingress-nginx ingress-nginx/ingress-nginx --version 4.10.0 --values ingress-nginx.values
```

### JSON Output

Use `--json` / `-j` for machine-readable output. Each result includes:

```json
{
  "chart_name": "ingress-nginx",
  "release_name": "ingress-nginx",
  "namespace": "ingress",
  "installed_chart_version": "4.9.1",
  "latest_chart_version": "4.10.0",
  "installed_app_version": "1.9.1",
  "latest_app_version": "1.10.1",
  "repos": ["ingress-nginx"],
  "upgradable": true,
  "commands": [...]
}
```

### Error Handling

If a release was installed from a chart that is no longer in any configured repository, the plugin will report it in a separate error section at the end:

```
Unable to find chart information in any repo for the following releases:

Release          Namespace      Chart
-------          ---------      -----
custom-app       production     my-custom-chart
```

## Configuration

The plugin respects standard Helm configuration:

- **Kubeconfig** — set via `KUBECONFIG` environment variable or `~/.kube/config`
- **Helm repositories** — configured in `~/.config/helm/repositories.yaml`
- **Helm driver** — set via `HELM_DRIVER` environment variable (defaults to secrets)

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `KUBECONFIG` | Path to kubeconfig file for cluster access |
| `HELM_DRIVER` | Backend driver for Helm (secrets, configmap, memory) |

## Architecture

### Design Philosophy

The plugin balances efficiency with correctness:

1. **On-demand index loading** — only downloads repository indexes for charts you actually use, not all charts across all repos
2. **Caching** — caches index downloads to avoid re-fetching for multiple releases of the same chart
3. **Smart deduplication** — filters out false positives when a chart exists in multiple repositories (e.g., official repo and Bitnami)
4. **Semantic versioning** — correctly compares versions across repositories to find the true latest

### API Integration

The plugin uses Helm's native Go APIs (`helm.sh/helm/v3`), not CLI commands:

- `helm.sh/helm/v3/pkg/cli` — for Helm configuration and environment
- `helm.sh/helm/v3/pkg/action` — for interacting with the cluster and getting releases
- `helm.sh/helm/v3/pkg/repo` — for loading and searching repositories

This provides better performance, error handling, and reliability compared to shelling out to `helm` commands.

## Building from Source

### Prerequisites

- Go 1.21 or later
- GNU Make

### Build

```bash
make build
```

Outputs the binary to `bin/helm-upgrade-check`.

### Run Tests

```bash
make test
```

### Clean Build Artifacts

```bash
make clean
```

### Full Build Process

```bash
make all
```

Runs tidy, test, and build in sequence.

## Releasing a New Version

Releases are published to GitHub via [GoReleaser](https://goreleaser.com). GoReleaser
determines the version from the **git tag**, so the tag must be created and pushed
before running the release target.

### Prerequisites

- [GoReleaser](https://goreleaser.com/install/) installed and on your `$PATH`
- A GitHub personal access token with `repo` scope saved to
  `~/.config/goreleaser/helm-upgrade-check-plugin-github-token`

### Step-by-step

1. **Bump the version** in two places — they must match the tag you will create:

   ```bash
   # Makefile  — update VERSION=
   # plugin.yaml — update version:
   ```

2. **Update `CHANGELOG.md`** with a new entry for the release (follow the
   Keep a Changelog format already used in the file).

3. **Commit the version bump and changelog:**

   ```bash
   git add Makefile plugin.yaml CHANGELOG.md
   git commit -m "Release vX.Y.Z"
   ```

4. **Create and push the tag** — GoReleaser reads this to determine the version:

   ```bash
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

5. **Run the release:**

   ```bash
   make release
   ```

   This runs `goreleaser release --clean`, which:
   - Builds binaries for Linux, macOS, and Windows (amd64 + arm64)
   - Packages each binary into a `tar.gz` archive named `helm-upgrade-check-plugin_<version>_<os>_<arch>.tar.gz`
   - Generates `checksums.txt`
   - Publishes a GitHub Release with all archives attached
   - Injects the version into the binary via `-X main.Version`

   > **Note:** `make release` will fail if no tag exists on `HEAD`. Always push
   > the tag before running it.

   After the GitHub Release is published, `helm plugin install` and
   `helm plugin update` will automatically download the correct archive via
   `scripts/install.sh` — no manual binary copying is required.

## Troubleshooting

### No output or blank screen

**Cause** — plugin runs successfully but no releases found or all are up-to-date.

**Solution** — This is normal behavior. Run `helm list --all-namespaces` to verify that releases exist in your cluster.

**Solution** — Enable debug output with `-d` flag to see what's happening under the hood.

## License

See [LICENSE](LICENSE) file for details.

## Change Log

See [CHANGELOG.md](CHANGELOG.md) file for details.
