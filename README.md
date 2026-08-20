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
Chart Name      Release Name    Namespace       Repo(s)   Running Version   Chart Version     App Version
----------      ------------    ---------       -------   ---------------   -------------     -----------
```

| Column | Description |
|--------|-------------|
| **Running Version** | The chart version currently installed |
| **Chart Version** | This repo's own latest chart version (used for `--version` in `helm upgrade` when it's the upgrade path) |
| **App Version** | This repo's own latest application version |

A chart found in multiple repos gets one row per repo, each showing that repo's own Chart Version / App Version next to the same Running Version — there's no reliable way to know which repo a running release was actually installed from (Helm doesn't persist that), so the table doesn't guess; compare Running Version against each repo's row yourself.

> **Note:** `helm upgrade --version` takes the **chart version**, not the application version. These differ for many charts — for example `ingress-nginx` chart `4.9.1` ships app version `1.9.1`. The plugin always uses the chart version for upgrade commands.

### Status Indicators

- **Blue text** — this repo offers a newer chart version and its app version does not regress
- **Green text** — up-to-date from this repo: no newer version found, or the only "newer" chart ships an older app version (suppressed)

### Version Selection and Upgrade Suppression

For each repo that contains the chart, the plugin selects the **highest stable chart version** in the cached index. It then flags an upgrade only when both conditions hold:

1. The candidate chart version is **greater than** the installed chart version (semver comparison)
2. The candidate chart's app version is **not older than** the installed app version

If condition 2 fails — the highest available chart version ships an older application version than what is currently running — the upgrade is **suppressed** and the release appears green (up-to-date). This prevents a chart from a different repository branch or numbering scheme being shown as an update when it would actually downgrade the running application. When app versions are non-semver or absent, the comparison falls back to chart version only.

> **Important:** the plugin picks the *highest* chart version from each repo and tests it against the app-regression guard. It does not search backward through lower chart versions looking for a non-regressing upgrade. If your highest available chart version ships an older app, the release will show as up-to-date even if an intermediate chart version exists that would be a valid upgrade. This is expected and correct for well-maintained repositories where chart and app versions advance together.

### Multiple Repositories

If a chart is found in more than one configured repository (including Bitnami, which is treated like any other repo), each repo is evaluated **independently** — its own chart version and app version are compared against the installed release, since mirrored repos don't necessarily carry the same version. The results table shows one line per repo (see the Running Version column above), and a release is flagged as upgradable if **any** repo offers a valid upgrade.

When a release is **not** upgradable, repos whose own latest chart/app version doesn't exactly match what's running are dropped from the output — they're either behind or blocked by the app-regression guard, so they're just noise once there's nothing to do. Only the repo(s) that corroborate the running version are shown. If no repo's latest happens to exactly match what's running (e.g. it's since been superseded everywhere), all repos are shown instead of hiding the release.

### Upgrade Commands

For each release with at least one repo offering a valid upgrade, the plugin prints:

1. **Get current values** — saves the release's current values to a file
2. **Review values** — displays the saved values for inspection
3. **Execute upgrade** — one command per repo that offers an upgrade, each using that repo's own **chart** version

Example output:

```
ingress-nginx   ingress-nginx   ingress    ingress-nginx   4.9.1             4.10.0            1.10.1


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
  "installed_app_version": "1.9.1",
  "repos": [
    {
      "repo": "ingress-nginx",
      "latest_chart_version": "4.10.0",
      "latest_app_version": "1.10.1",
      "upgradable": true
    }
  ],
  "upgradable": true,
  "commands": [...]
}
```

Each entry in `repos` carries that repo's own `latest_chart_version` / `latest_app_version` / `upgradable` — a chart found in multiple repos can have a different version (and upgrade verdict) per repo. The top-level `upgradable` is `true` if any repo offers a valid upgrade.

The top-level JSON object contains `results`, `missing_charts`, and a `warnings` array. Warning details are also printed to stderr; stdout remains valid JSON for machine-readable consumers.

### Error Handling

If a release was installed from a chart that is no longer in any configured repository, the plugin reports it in a separate section at the end:

```
Unable to find chart information in any repo for the following releases:

Release          Namespace      Chart
-------          ---------      -----
custom-app       production     my-custom-chart
```

This is normal for charts installed directly from a local path or a repo that has since been removed.

Other problems are non-fatal: unreadable repository indexes, chart entries with invalid versions, and releases that cannot be decoded are reported as `warning:` lines on stderr, and the audit continues with the data it could load. If every configured repository fails to load its index, no comparison is possible and the plugin exits with status 1.

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
3. **Semantic versioning** — correctly compares versions, including pre-release handling (`--include-prerelease` to opt in).
4. **OCI registry support** — resolves `oci://` charts by querying tags and fetching manifests directly from the registry.

### API Integration

The plugin uses Helm 4's native Go SDK, not CLI commands:

- `helm.sh/helm/v4/pkg/cli` — Helm configuration and environment settings
- `helm.sh/helm/v4/pkg/action` — interacting with the cluster and listing releases
- `helm.sh/helm/v4/pkg/repo/v1` — loading locally cached repository indexes
- `helm.sh/helm/v4/pkg/release` — release accessor for reading installed chart metadata

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

## Contributing / Development

See [DEV.md](DEV.md) for build instructions, local development setup, and release procedures.

## License

See [LICENSE](LICENSE) file for details.

## Change Log

See [CHANGELOG.md](CHANGELOG.md) file for details.
