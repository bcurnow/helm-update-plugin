#!/usr/bin/env bash
set -euo pipefail

ls -ltra ${PASSPHRASE_FILE}

# Variables automatically passed from GoReleaser environment
TARGET_ENV="${GORELEASER_TARGET}"      # e.g., linux_amd64_v1
BINARY_PATH="${GORELEASER_BINARY_PATH}" # Absolute path to compiled binary
VERSION="${GORELEASER_VERSION}"         # e.g., v2.0.0
WORKSPACE="dist/helm-package/${TARGET_ENV}"
OUTPUT_DIR=dist/helm-package/output/${TARGET_ENV}

echo "Packaging Helm plugin layout for ${TARGET_ENV} using GPG key '${GPG_KEY_NAME}' in '${WORKSPACE}..."

mkdir -p ${WORKSPACE}
mkdir -p ${OUTPUT_DIR}

# 1. Recreate the precise folder layout Helm expects
mkdir -p "${WORKSPACE}/bin"
cp "${BINARY_PATH}" "${WORKSPACE}/bin/"
cp plugin.yaml "${WORKSPACE}/"

if [ -d scripts ]; then
    cp -r scripts "${WORKSPACE}/"
fi

ls -ltra "${WORKSPACE}"
# 2. Package and cryptographically sign using Helm
helm plugin package --sign --key "${GPG_KEY_NAME}" --keyring "${HOME}/.gnupg/secring.gpg" --passphrase-file ${PASSPHRASE_FILE} --destination "${OUTPUT_DIR}" "${WORKSPACE}"

# 3. Rename artifacts to match the matrix target layout
mv ${OUTPUT_DIR}/upgrade-check-*.tgz "dist/upgrade-check-${VERSION}-${TARGET_ENV}.tgz"
mv ${OUTPUT_DIR}/upgrade-check-*.tgz.prov "dist/upgrade-check-${VERSION}-${TARGET_ENV}.tgz.prov"

# 4. Cleanup
rm -rf "${WORKSPACE}"
rm -rf "${OUTPUT_DIR}"
