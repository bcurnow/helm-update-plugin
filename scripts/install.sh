#!/usr/bin/env bash
set -euo pipefail

BINARY="helm-upgrade-check"
PLUGIN_DIR="${HELM_PLUGIN_DIR}"

# Local development escape hatch: `make install-dev` sets this to the binary
# it just built so that a local build is what gets installed, instead of this
# script downloading whatever plugin.yaml's version has published on GitHub
# (which may lag behind, or not exist yet, during development). Unset for a
# normal install, so real installs always verify a downloaded release.
if [[ -n "${HELM_UPGRADE_CHECK_LOCAL_BIN:-}" ]]; then
    if [[ ! -x "${HELM_UPGRADE_CHECK_LOCAL_BIN}" ]]; then
        echo "Error: HELM_UPGRADE_CHECK_LOCAL_BIN=${HELM_UPGRADE_CHECK_LOCAL_BIN} is not an executable file" >&2
        exit 1
    fi
    echo "Installing local development build from ${HELM_UPGRADE_CHECK_LOCAL_BIN}"
    mkdir -p "${PLUGIN_DIR}/bin"
    DEST="${PLUGIN_DIR}/bin/${BINARY}"
    # A local-directory `helm plugin install` symlinks PLUGIN_DIR straight back
    # to the source checkout, so the source binary and DEST are often already
    # the same file; skip the copy in that case instead of erroring on cp's
    # same-file check.
    if [[ "$(realpath -- "${HELM_UPGRADE_CHECK_LOCAL_BIN}")" != "$(realpath -m -- "${DEST}")" ]]; then
        cp "${HELM_UPGRADE_CHECK_LOCAL_BIN}" "${DEST}"
    fi
    chmod +x "${DEST}"
    echo "Installed ${BINARY} (local dev build)"
    exit 0
fi

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
if [[ -z "${VERSION}" || ! "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
    echo "Could not determine a plausible plugin version from plugin.yaml" >&2
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
    curl -fsSL "${URL}" -o "${TMP}/${FILENAME}"
    curl -fsSL "${CHECKSUMS_URL}" -o "${TMP}/${CHECKSUMS_FILENAME}"
elif command -v wget &>/dev/null; then
    wget -q --output-document="${TMP}/${FILENAME}" "${URL}"
    wget -q --output-document="${TMP}/${CHECKSUMS_FILENAME}" "${CHECKSUMS_URL}"
else
    echo "Error: curl or wget is required to install this plugin" >&2
    exit 1
fi

echo "Verifying checksum..."
CHECKSUM_LINES="$(grep -F -- "${FILENAME}" "${TMP}/${CHECKSUMS_FILENAME}" || true)"
if [[ -z "${CHECKSUM_LINES}" ]]; then
    echo "Error: no checksum entry found for ${FILENAME}" >&2
    exit 1
fi
if [[ "$(printf '%s\n' "${CHECKSUM_LINES}" | wc -l)" -ne 1 ]] || \
    ! printf '%s\n' "${CHECKSUM_LINES}" | awk -v expected="${FILENAME}" \
        'NF >= 2 && ($2 == expected || $2 == "*" expected) { found++ } END { exit found == 1 ? 0 : 1 }'; then
    echo "Error: expected exactly one checksum entry for ${FILENAME}" >&2
    exit 1
fi

if command -v sha256sum &>/dev/null; then
    (cd "${TMP}" && printf '%s\n' "${CHECKSUM_LINES}" | sha256sum --check --status) \
        || { echo "Error: checksum verification failed for ${FILENAME}" >&2; exit 1; }
elif command -v shasum &>/dev/null; then
    (cd "${TMP}" && printf '%s\n' "${CHECKSUM_LINES}" | shasum -a 256 --check --status) \
        || { echo "Error: checksum verification failed for ${FILENAME}" >&2; exit 1; }
else
    echo "Error: neither sha256sum nor shasum is available; refusing to install without checksum verification" >&2
    exit 1
fi

mkdir -p "${PLUGIN_DIR}/bin"
cp "${TMP}/${FILENAME}" "${PLUGIN_DIR}/bin/${BINARY}"
chmod +x "${PLUGIN_DIR}/bin/${BINARY}"

echo "Installed ${BINARY} v${VERSION}"
