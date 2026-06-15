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

# Read version from plugin.yaml
VERSION="$(grep '^version:' "${PLUGIN_DIR}/plugin.yaml" | awk '{gsub(/"/, "", $2); print $2}')"
if [[ -z "${VERSION}" ]]; then
    echo "Could not determine plugin version from plugin.yaml" >&2
    exit 1
fi

FILENAME="helm-upgrade-check-plugin_${VERSION}_${OS}_${ARCH}"
if [[ "${OS}" == "windows" ]]; then
    FILENAME="${FILENAME}.exe"
fi

URL="https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/v${VERSION}/${FILENAME}"

CHECKSUMS_FILENAME="checksums.txt"
CHECKSUMS_URL="https://github.com/bcurnow/helm-upgrade-check-plugin/releases/download/v${VERSION}/${CHECKSUMS_FILENAME}"

echo "Downloading ${FILENAME}..."

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

if command -v curl &>/dev/null; then
    curl -sSL "${URL}" -o "${TMP}/${FILENAME}"
    curl -sSL "${CHECKSUMS_URL}" -o "${TMP}/${CHECKSUMS_FILENAME}"
elif command -v wget &>/dev/null; then
    wget -qO "${TMP}/${FILENAME}" "${URL}"
    wget -qO "${TMP}/${CHECKSUMS_FILENAME}" "${CHECKSUMS_URL}"
else
    echo "Error: curl or wget is required to install this plugin" >&2
    exit 1
fi

echo "Verifying checksum..."
if command -v sha256sum &>/dev/null; then
    (cd "${TMP}" && grep "${FILENAME}" "${CHECKSUMS_FILENAME}" | sha256sum --check --status) \
        || { echo "Error: checksum verification failed for ${FILENAME}" >&2; exit 1; }
elif command -v shasum &>/dev/null; then
    (cd "${TMP}" && grep "${FILENAME}" "${CHECKSUMS_FILENAME}" | shasum -a 256 --check --status) \
        || { echo "Error: checksum verification failed for ${FILENAME}" >&2; exit 1; }
else
    echo "Warning: neither sha256sum nor shasum found, skipping checksum verification" >&2
fi

mkdir -p "${PLUGIN_DIR}/bin"
cp "${TMP}/${FILENAME}" "${PLUGIN_DIR}/bin/${BINARY}"
chmod +x "${PLUGIN_DIR}/bin/${BINARY}"

echo "Installed ${BINARY} v${VERSION}"
