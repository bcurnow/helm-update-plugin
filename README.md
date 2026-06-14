# helm-upgrade-check-plugin

A Helm 4 plugin that identifies deployed Helm releases and checks for available updates across configured chart repositories.

## Overview

`helm-upgrade-check-plugin` scans your entire Kubernetes cluster for installed Helm releases and compares the deployed chart versions against the latest versions available in your configured repositories. For any releases that have newer versions available, the plugin provides direct copy-paste upgrade commands.

The plugin reads the same locally cached repository indexes that `helm search repo` uses. Run `helm repo update` before invoking the plugin to ensure you are comparing against the latest available versions — exactly the same workflow you would use for any other Helm command.

The plugin is designed for:
- **Quick vulnerability/security audits** — identify outdated deployments at a glance
- **Release management** — keep track of which releases need updates
- **Cluster maintenance** — reduce manual version checking across multiple releases

## Requirements

- **Helm 4** — the plugin uses the Helm 4 Go SDK and requires a Helm 4 CLI to install and run
- **Helm repositories configured** — add repos with `helm repo add` and keep them current with `helm repo update`

## Installation

See [INSTALL.md](INSTALL.md) for installation, upgrade, and uninstall instructions.

## Usage

### Basic Usage

```bash
helm repo update          # refresh the local repo cache (same as before helm search repo)
helm upgrade-check        # check for upgrades using the cached indexes
```

### Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--include-prerelease` | | Include pre-release chart versions when checking for upgrades | false |
| `--json` | `-j` | Output results as JSON | false |
| `--debug` | `-d` | Enable debug output showing loaded releases | false |
| `--version` | | Print plugin version and exit | |

### Examples

#### Check all releases

```bash
helm upgrade-check
```

#### Include pre-release versions

```bash
helm upgrade-check --include-prerelease
```

#### Enable debug output

Shows which releases were loaded from the cluster:

```bash
helm upgrade-check --debug
```

#### Machine-readable JSON output

```bash
helm upgrade-check --json
```

## Output Format

The plugin outputs a table with the following columns:

```
Chart Name      Release Name    Namespace       Repo(s)   Chart Version     App Version
----------      ------------    ---------       -------   -------------     -----------
```

| Column | Description |
|--------|-------------|
| **Chart Version** | The chart version currently installed (used for `--version` in `helm upgrade`) |
| **App Version** | The application version bundled with the installed chart |

Up-to-date releases are shown in **green**. Releases with available upgrades show `installed → latest` with the installed version in **red** and the latest in **blue**.

> **Note:** `helm upgrade --version` takes the **chart version**, not the application version. These differ for many charts — for example `ingress-nginx` chart `4.9.1` ships app version `1.9.1`. The plugin always uses the chart version for upgrade commands.

### Status Indicators

- `old → new` in red/blue — a newer chart version is available and its app version does not regress
- **Green text** — up-to-date release, or a "newer" chart ships an older app version (suppressed)

> A higher chart version is only flagged as an upgrade when the candidate chart's app version is equal to or greater than the installed app version. This prevents a chart from a different repository (with its own version numbering) being shown as an update when it would actually downgrade the running application. When app versions are non-semver or absent, the comparison falls back to chart version only.

### Upgrade Commands

For each out-of-date release, the plugin prints three commands:

1. **Get current values** — saves the release's current values to a file
2. **Review values** — displays the saved values for inspection
3. **Execute upgrade** — performs the actual upgrade with the latest **chart** version

Example output:

```
ingress-nginx   ingress-nginx   ingress    ingress-nginx   4.9.1 → 4.10.0   1.9.1 → 1.10.1


Upgrade commands:
─────────────────────────────────────────────────────────────────────────────────────

ingress-nginx (ingress):
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

If a release was installed from a chart that is no longer in any configured repository, the plugin reports it in a separate section at the end:

```
Unable to find chart information in any repo for the following releases:

Release          Namespace      Chart
-------          ---------      -----
custom-app       production     my-custom-chart
```

This is normal for charts installed directly from a local path or a repo that has since been removed.

## Configuration

The plugin respects standard Helm configuration:

- **Kubeconfig** — set via `KUBECONFIG` environment variable or `~/.kube/config`
- **Helm repositories** — configured in `~/.config/helm/repositories.yaml` (managed by `helm repo add`)
- **Repository cache** — the locally cached index files in `$HELM_REPOSITORY_CACHE` (default `~/.cache/helm/repository/`), populated by `helm repo update`
- **Helm driver** — set via `HELM_DRIVER` environment variable (defaults to secrets)

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `KUBECONFIG` | Path to kubeconfig file for cluster access |
| `HELM_DRIVER` | Backend driver for Helm (secrets, configmap, memory) |
| `HELM_REPOSITORY_CACHE` | Directory containing cached repository index files |
| `HELM_NAMESPACE` | Default namespace (does not restrict which releases are listed) |

## Architecture

### Design Philosophy

The plugin follows the same model as other Helm commands:

1. **Reads locally cached indexes** — uses the same `$HELM_REPOSITORY_CACHE/<repo>-index.yaml` files that `helm search repo` reads, populated by `helm repo update`. No network requests are made during the check itself.
2. **In-memory result caching** — chart version lookups within a single run are memoized so multiple releases of the same chart (e.g. two cilium releases in different namespaces) only parse the index once.
3. **Smart deduplication** — when a chart exists in multiple repositories (e.g. an official repo and Bitnami), only the non-Bitnami repo is shown in upgrade commands to avoid ambiguity.
4. **Semantic versioning** — correctly compares versions, including pre-release handling (`--include-prerelease` to opt in).
5. **OCI registry support** — resolves `oci://` charts by querying tags and fetching manifests directly from the registry.

### API Integration

The plugin uses Helm 4's native Go SDK, not CLI commands:

- `helm.sh/helm/v4/pkg/cli` — Helm configuration and environment settings
- `helm.sh/helm/v4/pkg/action` — interacting with the cluster and listing releases
- `helm.sh/helm/v4/pkg/repo/v1` — loading locally cached repository indexes
- `helm.sh/helm/v4/pkg/release` — release accessor for reading installed chart metadata

## Building from Source

### Prerequisites

- Go 1.25 or later
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

Runs tidy, fmt, vet, test, and build in sequence.

## Releasing a New Version

Releases are published to GitHub via [GoReleaser](https://goreleaser.com). GoReleaser
determines the version from the **git tag**, so the tag must be created and pushed
before running the release target.

### Prerequisites

- [GoReleaser](https://goreleaser.com/install/) installed and on your `$PATH`
- A GitHub personal access token with `repo` scope saved to
  `~/.config/goreleaser/helm-upgrade-check-plugin-github-token`

### Step-by-step

1. **Prepare the release:**

   ```bash
   make prepare-release TAG=X.Y.Z
   git add plugin.yaml CHANGELOG.md
   git commit -m "Release vX.Y.Z"
   ```

2. **Create and push the tag** — the tag **must be annotated** (`-a`):

   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

3. **Run the release:**

   ```bash
   make release
   ```

   This runs `goreleaser release --clean`, which:
   - Builds binaries for Linux, macOS, and Windows (amd64 + arm64)
   - Packages each into a `tar.gz` archive
   - Generates `checksums.txt`
   - Publishes a GitHub Release with all archives attached

   After the GitHub Release is published, `helm plugin install` and
   `helm plugin update` will automatically download the correct archive via
   `scripts/install.sh`.

## Troubleshooting

### Releases show as up-to-date when upgrades exist

**Cause** — the locally cached repository indexes are stale.

**Solution** — run `helm repo update` to refresh them, then re-run the plugin.

### A release appears in the "Unable to find chart" section

**Cause** — the chart's repository is not configured, or the repository name in the
cached index does not match the chart name used during installation.

**Solution** — verify the repo is registered (`helm repo list`) and the cache is
current (`helm repo update`).

### No output or blank screen

**Cause** — no releases found or all are up-to-date.

**Solution** — run `helm list --all-namespaces` to verify that releases exist in your
cluster. Enable debug output with `-d` to see what the plugin loaded.

## License

See [LICENSE](LICENSE) file for details.

## Change Log

See [CHANGELOG.md](CHANGELOG.md) file for details.
