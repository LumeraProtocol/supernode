#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# Sample usage:
# ./install-lumera.sh                  # uses latest release
# ./install-lumera.sh latest-tag       # uses latest tag from /tags
# ./install-lumera.sh v1.1.0           # installs this specific version

# Support mode argument: 'latest-release' (default) or 'latest-tag'
MODE="${1:-latest-release}"

REPO="LumeraProtocol/lumera"
GITHUB_API="https://api.github.com/repos/$REPO"

echo "Installation mode: $MODE"

if [ "$MODE" == "latest-tag" ]; then
    echo "Fetching latest tag from GitHub..."
    if command -v jq >/dev/null 2>&1; then
        TAG_NAME=$(curl -s "$GITHUB_API/tags" | jq -r '.[0].name')
    else
        TAG_NAME=$(curl -s "$GITHUB_API/tags" | grep '"name"' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/')
    fi
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG_NAME}/lumera_${TAG_NAME}_linux_amd64.tar.gz"

elif [[ "$MODE" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    TAG_NAME="$MODE"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TAG_NAME}/lumera_${TAG_NAME}_linux_amd64.tar.gz"

elif [ "$MODE" == "latest-release" ]; then
    echo "Fetching latest release information..."
    RELEASE_INFO=$(curl -s -S -L "$GITHUB_API/releases/latest")

    # Extract tag name and download URL
    if command -v jq >/dev/null 2>&1; then
        TAG_NAME=$(echo "$RELEASE_INFO" | jq -r '.tag_name')
        DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | test("linux_amd64.tar.gz$")) | .browser_download_url')
    else
        TAG_NAME=$(echo "$RELEASE_INFO" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
        DOWNLOAD_URL=$(echo "$RELEASE_INFO" | grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*linux_amd64\.tar\.gz[^"]*"' | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    fi

else
    echo "❌ Error: Invalid mode '$MODE'"
    echo "Usage: $0 [latest-release|latest-tag|vX.Y.Z]"
    exit 1
fi

echo "Selected tag: $TAG_NAME"
echo "Download URL: $DOWNLOAD_URL"

if [ -z "$TAG_NAME" ] || [ -z "$DOWNLOAD_URL" ]; then
    echo "Error: Could not determine tag or download URL"
    exit 1
fi

# Download and extract the release
TEMP_DIR=$(mktemp -d)
ORIG_DIR=$(pwd)
echo "Downloading Lumera from $DOWNLOAD_URL"
curl -L --progress-bar "$DOWNLOAD_URL" -o "$TEMP_DIR/lumera.tar.gz"
cd "$TEMP_DIR"
tar -xzf lumera.tar.gz
rm lumera.tar.gz

# Install WASM library
WASM_LIB=$(find . -type f -name "libwasmvm*.so" -print -quit)
if [ -n "$WASM_LIB" ]; then
    echo "Installing WASM library: $WASM_LIB"
    sudo cp "$WASM_LIB" /usr/lib/
fi

# Find and install lumerad binary
LUMERAD_PATH=$(find . -type f -name "lumerad" -print -quit)
if [ -n "$LUMERAD_PATH" ]; then
    echo "Installing lumerad binary from: $LUMERAD_PATH"
    chmod +x "$LUMERAD_PATH"
    sudo cp "$LUMERAD_PATH" /usr/local/bin/
    
    # Verify installation
    if which lumerad > /dev/null; then
        echo "Installation successful. Lumerad version: $(lumerad version 2>/dev/null || echo "unknown")"
    else
        echo "Error: Lumerad installation failed"
        exit 1
    fi
else
    echo "Error: Could not find lumerad binary in the package"
    exit 1
fi

# Clean up
cd "$ORIG_DIR"
rm -rf "$TEMP_DIR"
echo "Lumera installation complete"