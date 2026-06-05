#!/usr/bin/env bash
set -euo pipefail

BINARY="helm-upgrade-check"
PLUGIN_DIR="${HELM_PLUGIN_DIR}"

# Detect OS
RAW_OS="$(uname -s)"
case "${RAW_OS}" in
    Linux)                 OS="linux"   ;;
    Darwin)                OS="darwin"  ;;
    MINGW*|MSYS*|CYGWIN*)  OS="windows" ;;
    *)
        echo "Unsupported OS: ${RAW_OS}" >&2
        exit 1
        ;;
esac

# Detect architecture
RAW_ARCH="$(uname -m)"
case "${RAW_ARCH}" in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)
        echo "Unsupported architecture: ${RAW_ARCH}" >&2
        exit 1
        ;;
esac

# GoReleaser names the Windows binary helm-upgrade-check.exe inside the archive,
# but we always install as helm-upgrade-check so plugin.yaml needs no OS-specific paths.
ARCHIVE_BINARY="${BINARY}"
if [[ "${OS}" == "windows" ]]; then
    ARCHIVE_BINARY="${BINARY}.exe"
fi

# Read version from plugin.yaml
VERSION="$(grep '^version:' "${PLUGIN_DIR}/plugin.yaml" | awk '{gsub(/"/, "", $2); print $2}')"
if [[ -z "${VERSION}" ]]; then
    echo "Could not determine plugin version from plugin.yaml" >&2
    exit 1
fi

FILENAME="helm-upgrade-check-plugin_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/v${VERSION}/${FILENAME}"

echo "Downloading ${FILENAME}..."

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

if command -v curl &>/dev/null; then
    curl -sSL "${URL}" -o "${TMP}/${FILENAME}"
elif command -v wget &>/dev/null; then
    wget -qO "${TMP}/${FILENAME}" "${URL}"
else
    echo "Error: curl or wget is required to install this plugin" >&2
    exit 1
fi

tar -xzf "${TMP}/${FILENAME}" -C "${TMP}"

BINARY_PATH="$(find "${TMP}" -name "${ARCHIVE_BINARY}" -type f | head -1)"
if [[ -z "${BINARY_PATH}" ]]; then
    echo "Error: ${ARCHIVE_BINARY} not found in ${FILENAME}" >&2
    exit 1
fi

mkdir -p "${PLUGIN_DIR}/bin"
cp "${BINARY_PATH}" "${PLUGIN_DIR}/bin/${BINARY}"
chmod +x "${PLUGIN_DIR}/bin/${BINARY}"

echo "Installed ${BINARY} v${VERSION}"
