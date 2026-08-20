#!/usr/bin/env bash
set -euo pipefail

# Variables automatically passed from GoReleaser environment
WORKSPACE="dist/helm-package"
OUTPUT_DIR=dist

if [ -d "${WORKSPACE}" ]; then
    echo "Already created the plugin package, returning..."
    exit 0
else
    echo "Packaging Helm plugin layout GPG key '${GPG_KEY_NAME}' in '${WORKSPACE}..."
fi

mkdir -p "${WORKSPACE}"

# 1. Recreate the precise folder layout Helm expects
# We don't need the binary because the hook script will download the right platform version from GitHub releases
cp plugin.yaml "${WORKSPACE}/"

if [ -d scripts ]; then
    cp -r scripts "${WORKSPACE}/"
fi

# 2. Package and cryptographically sign using Helm
helm plugin package --sign --key "${GPG_KEY_NAME}" --keyring "${HOME}/.gnupg/secring.gpg" --passphrase-file "${PASSPHRASE_FILE}" --destination "${OUTPUT_DIR}" "${WORKSPACE}"