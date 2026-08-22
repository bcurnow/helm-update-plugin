# Installation Guide

## Prerequisites

Before installing `helm-upgrade-check-plugin`, ensure you have:

- **Helm 4** — [Install Helm](https://helm.sh/docs/intro/install/)
- **kubectl** — configured to access your Kubernetes cluster
- **git** — only required for the recommended installation method below

Verify your Helm installation:

```bash
helm version
```

## Installation

There are two ways to install the plugin. They behave identically day-to-day — both end up running the same install script, which downloads the correct binary for your OS/architecture and verifies its checksum — but only one of them supports `helm plugin update` afterward. Pick based on whether that matters to you.

### Recommended: from Git (supports `helm plugin update`)

```bash
helm plugin install --verify=false https://github.com/bcurnow/helm-upgrade-check-plugin.git
```

Helm's `plugin update` command only works for plugins installed from a git source — it updates by running `git pull` against the remote and re-running the install hook, which then downloads whatever binary matches the newly-pulled `plugin.yaml`. Installing this way is what makes [Upgrading](#upgrading) with a single command possible.

`--verify=false` is required here, not optional: Helm's signature verification only applies to downloaded archives (a `.tgz` + `.tgz.prov` pair), and a git clone has no such pair to check. This does **not** mean the installed binary itself goes unverified — the install script still checksums it against the `checksums.txt` published with the release, the same as the archive method below; you're only forgoing the extra GPG signature layer.

### Alternative: signed archive (no `helm plugin update` support)

Each release also publishes a signed plugin archive to the [GitHub Releases page](https://github.com/bcurnow/helm-upgrade-check-plugin/releases). Replace `X.Y.Z` with the version you want to install:

```bash
helm plugin install https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/vX.Y.Z/upgrade-check-X.Y.Z.tgz
```

Each archive is accompanied by a `.tgz.prov` provenance file signed with GPG. If you have the signing key in your keyring, pass `--verify` to validate the signature before installation:

```bash
helm plugin install --verify https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/vX.Y.Z/upgrade-check-X.Y.Z.tgz
```

A plugin installed this way has no git remote for Helm to pull from, so `helm plugin update` will fail with `cannot get information about plugin source`. Upgrading means uninstalling and reinstalling the new version — see [Upgrading](#upgrading).

## Verification

Confirm the plugin installed correctly:

```bash
helm plugin list
```

You should see `upgrade-check` in the list. Test it:

```bash
helm upgrade-check --version
```

## Upgrading

**If you installed from Git:**

```bash
helm plugin update upgrade-check
```

This pulls the latest `plugin.yaml` from GitHub and downloads whichever release binary matches it — no need to know the new version number ahead of time.

**If you installed the signed archive:** `helm plugin update` isn't available (see [Installation](#installation)); uninstall and reinstall the specific new version instead:

```bash
helm plugin uninstall upgrade-check
helm plugin install https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/vX.Y.Z/upgrade-check-X.Y.Z.tgz
```

## Uninstallation

```bash
helm plugin uninstall upgrade-check
```

## Troubleshooting

### Plugin not found

**Problem** — `helm: no such plugin: upgrade-check`

**Solution** — Verify the plugin is installed and find its install directory (named `upgrade-check` for an archive install, `helm-upgrade-check-plugin.git` for a git install):

```bash
helm plugin list
ls -la $(helm env HELM_PLUGINS)/
```

### Permission denied

**Problem** — `permission denied` when running the plugin

**Solution** — Ensure the binary is executable (substitute the install directory found above):

```bash
chmod +x $(helm env HELM_PLUGINS)/<install-dir>/bin/helm-upgrade-check
```

### Cannot access cluster

**Problem** — `Unable to connect to the server`

**Solution** — Verify your kubeconfig and cluster connectivity:

```bash
kubectl cluster-info
kubectl auth can-i list releases --all-namespaces
```

### Repository connection issues

**Problem** — `failed to download index for`: network or authentication errors

**Solution** — Verify repository access and update indexes:

```bash
helm repo list
helm repo update
```

## System Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| Helm | 4.0.0 | latest |
| Kubernetes | 1.16 | 1.25+ |
| Memory | 50 MB | 200 MB |
| Network | For repo access | Broadband |

## Getting Help

- [GitHub Issues](https://github.com/bcurnow/helm-upgrade-check-plugin/issues) — Report bugs or request features
- [GitHub Discussions](https://github.com/bcurnow/helm-upgrade-check-plugin/discussions) — Ask questions and discuss
- [DEV.md](DEV.md) — Building and releasing from source
