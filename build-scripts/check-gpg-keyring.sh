#!/usr/bin/env bash
set -euo pipefail

KEYRING_PATH="${HOME}/.gnupg/secring.gpg"

if [ -f "${KEYRING_PATH}" ]; then
    echo "GPG keyring found at ${KEYRING_PATH}. Verifying presence of key '${GPG_KEY_NAME}'..."
    echo "Searching for key '${GPG_KEY_NAME}' in the keyring..."
    if gpg --import --import-options show-only "${KEYRING_PATH}" 2>/dev/null | grep -F "${GPG_KEY_NAME}" >/dev/null 2>&1; then
        echo "GPG key '${GPG_KEY_NAME}' found in the keyring. Proceeding with packaging..."
    else
        echo "GPG key '${GPG_KEY_NAME}' NOT found in the keyring."
        echo "Available keys in the keyring:"
        gpg --show-keys "${KEYRING_PATH}"
        echo "Please ensure the correct GPG key is present in the keyring before running the packaging script."
        exit 1
    fi
else
    echo "No GPG keyring found at ${KEYRING_PATH}"
    echo "Please create this file using the following command:"
    echo "  gpg --export-secret-keys -o ${KEYRING_PATH}"
    exit 1
fi