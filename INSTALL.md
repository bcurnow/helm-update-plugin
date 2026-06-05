# Installation Guide

## Prerequisites

Before installing `helm-upgrade-check-plugin`, ensure you have:

- **Helm 3.1.0 or later** — [Install Helm](https://helm.sh/docs/intro/install/)
- **kubectl** — configured to access your Kubernetes cluster
- **Git** (optional, for installing from source)

Verify your Helm installation:

```bash
helm version
```

## Installation Methods

### Method 1: Install from GitHub (Recommended)

Install the latest version directly from the GitHub repository:

```bash
helm plugin install https://github.com/bcurnow/helm-upgrade-check-plugin.git --verify=false
```

> **Note:** `--verify=false` is required because Helm 4 defaults to verifying plugin sources via GPG signature, but git-based sources do not support this mechanism. The downloaded binary is still verified against its SHA256 checksum by the install script.

### Method 2: Install from Local Source

Clone the repository and install locally:

```bash
git clone https://github.com/bcurnow/helm-upgrade-check-plugin.git
cd helm-upgrade-check-plugin
make build
helm plugin install .
```

### Method 3: Manual Installation

1. Download or build the binary:

```bash
# Build from source
git clone https://github.com/bcurnow/helm-upgrade-check-plugin.git
cd helm-upgrade-check-plugin
make build
```

2. Find your Helm plugin directory:

```bash
echo $(helm env HELM_PLUGINS)
```

3. Create the plugin directory structure:

```bash
mkdir -p $(helm env HELM_PLUGINS)/upgrade-check/bin
```

4. Copy the binary:

```bash
cp bin/helm-upgrade-check $(helm env HELM_PLUGINS)/upgrade-check/bin/
chmod +x $(helm env HELM_PLUGINS)/upgrade-check/bin/helm-upgrade-check
```

5. Copy the plugin manifest:

```bash
cp plugin.yaml $(helm env HELM_PLUGINS)/upgrade-check/
```

## Verification

Verify the plugin is installed correctly:

```bash
helm plugin list
```

You should see `upgrade-check` in the list. Test the plugin:

```bash
helm upgrade-check
```

## Uninstallation

To remove the plugin:

```bash
helm plugin uninstall upgrade-check
```

## Upgrading

### From Git Installation

If you installed from GitHub, pull the latest version and reinstall:

```bash
helm plugin uninstall upgrade-check
helm plugin install https://github.com/bcurnow/helm-upgrade-check-plugin.git --verify=false
```

### From Local Installation

Rebuild and reinstall:

```bash
cd helm-upgrade-check-plugin
git pull
make clean build
helm plugin uninstall upgrade-check
helm plugin install .
```

## Configuration

### Kubeconfig

The plugin uses your default kubeconfig. Set a custom kubeconfig:

```bash
export KUBECONFIG=/path/to/kubeconfig
helm upgrade-check
```

Or use it inline:

```bash
KUBECONFIG=/path/to/kubeconfig helm upgrade-check
```

### Helm Repositories

The plugin accesses all repositories configured in your `repositories.yaml`. View configured repositories:

```bash
helm repo list
```

Add a new repository:

```bash
helm repo add myrepo https://example.com/charts
helm repo update
```

## Troubleshooting Installation

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
| Go (for building) | 1.19 | 1.21+ |
| Helm | 3.1.0 | 3.13.0+ |
| Kubernetes | 1.16 | 1.25+ |
| Memory | 50 MB | 200 MB |
| Network | For repo access | Broadband |

## Building from Source

### Requirements

- Go 1.21 or later
- Make
- Git

### Build Steps

```bash
# Clone repository
git clone https://github.com/bcurnow/helm-upgrade-check-plugin.git
cd helm-upgrade-check-plugin

# Install dependencies
make tidy

# Run tests
make test

# Build
make build

# Binary is in bin/helm-upgrade-check
./bin/helm-upgrade-check --help
```

### Cross-Compilation

Build for different platforms:

```bash
# macOS
GOOS=darwin GOARCH=amd64 make build

# Linux
GOOS=linux GOARCH=amd64 make build

# Windows
GOOS=windows GOARCH=amd64 make build
```

## Getting Help

- [GitHub Issues](https://github.com/bcurnow/helm-upgrade-check-plugin/issues) — Report bugs or request features
- [GitHub Discussions](https://github.com/bcurnow/helm-upgrade-check-plugin/discussions) — Ask questions and discuss
- Check existing [documentation](README.md)
