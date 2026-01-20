#!/bin/bash
set -e

# pgmanager installer script
# Usage: curl -sSL https://raw.githubusercontent.com/subhanmahmood/pgmanager/master/install.sh | bash

REPO="subhanmahmood/pgmanager"
INSTALL_DIR="/usr/local/bin"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case $ARCH in
  x86_64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

case $OS in
  linux|darwin)
    ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# Get latest release tag
LATEST_TAG=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
  echo "Failed to get latest release"
  exit 1
fi

echo "Installing pgmanager ${LATEST_TAG} for ${OS}/${ARCH}..."

# Download binary
BINARY_NAME="pgmanager-${OS}-${ARCH}"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${BINARY_NAME}"

TMP_DIR=$(mktemp -d)
curl -sSL "$DOWNLOAD_URL" -o "${TMP_DIR}/pgmanager"
chmod +x "${TMP_DIR}/pgmanager"

# Install
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP_DIR}/pgmanager" "${INSTALL_DIR}/pgmanager"
else
  echo "Installing to ${INSTALL_DIR} (requires sudo)..."
  sudo mv "${TMP_DIR}/pgmanager" "${INSTALL_DIR}/pgmanager"
fi

rm -rf "$TMP_DIR"

echo "pgmanager installed successfully!"
echo "Run 'pgmanager --help' to get started."
