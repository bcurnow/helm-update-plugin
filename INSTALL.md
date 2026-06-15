# Installation Guide

## Prerequisites

Before installing `helm-upgrade-check-plugin`, ensure you have:

- **Helm 4** — [Install Helm](https://helm.sh/docs/intro/install/)
- **kubectl** — configured to access your Kubernetes cluster

Verify your Helm installation:

```bash
helm version
```

## Installation

Each release publishes a signed plugin archive to the [GitHub Releases page](https://github.com/bcurnow/helm-upgrade-check-plugin/releases). Replace `X.Y.Z` with the version you want to install:

```bash
helm plugin install https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/vX.Y.Z/upgrade-check-X.Y.Z.tgz
```

The archive works on all supported platforms — the included install script downloads the correct binary for your OS and architecture automatically.

### GPG Signature Verification (optional)

Each archive is accompanied by a `.tgz.prov` provenance file signed with GPG. If you have the signing key in your keyring, pass `--verify` to validate the signature before installation:

```bash
helm plugin install --verify https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/vX.Y.Z/upgrade-check-X.Y.Z.tgz
```

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

Uninstall the current version and install the new release:

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

**Solution** — Verify the plugin is installed:

```bash
helm plugin list
ls -la $(helm env HELM_PLUGINS)/upgrade-check/
```

### Permission denied

**Problem** — `permission denied` when running the plugin

**Solution** — Ensure the binary is executable:

```bash
chmod +x $(helm env HELM_PLUGINS)/upgrade-check/bin/helm-upgrade-check
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
