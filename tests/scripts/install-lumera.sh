#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# Sample usage:
# ./install-lumera.sh                  # uses latest release
# ./install-lumera.sh latest-tag       # uses latest tag from /tags
# ./install-lumera.sh v1.1.0           # installs this specific version
# LUMERAD_BINARY=/path/to/binary ./install-lumera.sh  # uses existing binary

install_binary() {
    local binary_path="$1"
    chmod +x "$binary_path"
    sudo cp "$binary_path" /usr/local/bin/lumerad
    
    # Verify installation
    if which lumerad > /dev/null; then
        echo "Installed: $(lumerad version 2>/dev/null || echo "unknown version")"
    else
        echo "Installation failed"
        exit 1
    fi
}

# Check if binary path is provided via environment variable
if [ -n "$LUMERAD_BINARY" ]; then
    if [ ! -f "$LUMERAD_BINARY" ]; then
        echo "Binary not found: $LUMERAD_BINARY"
        exit 1
    fi
    install_binary "$LUMERAD_BINARY"
    exit 0
fi

# Support mode argument: 'latest-release' (default), 'latest-tag', or specific version
MODE="${1:-latest-release}"

REPO="LumeraProtocol/lumera"
GITHUB_API="https://api.github.com/repos/$REPO"

# Determine tag and download URL based on mode
if [ "$MODE" == "latest-tag" ]; then
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
    echo "Error: Invalid mode '$MODE'"
    echo "Usage: $0 [latest-release|latest-tag|vX.Y.Z]"
    echo "   or: LUMERAD_BINARY=/path/to/binary $0"
    exit 1
fi

echo "Selected tag: $TAG_NAME"

# Validate that we have the required information
if [ -z "$TAG_NAME" ] || [ -z "$DOWNLOAD_URL" ]; then
    echo "Error: Could not determine tag or download URL"
    exit 1
fi

# Download and extract the release
TEMP_DIR=$(mktemp -d)
ORIG_DIR=$(pwd)
curl -L --progress-bar "$DOWNLOAD_URL" -o "$TEMP_DIR/lumera.tar.gz"

cd "$TEMP_DIR"
tar -xzf lumera.tar.gz
rm lumera.tar.gz

# Install WASM library
WASM_LIB=$(find . -type f -name "libwasmvm*.so" -print -quit)
if [ -n "$WASM_LIB" ]; then
    sudo cp "$WASM_LIB" /usr/lib/
fi

# Find and install lumerad binary
LUMERAD_PATH=$(find . -type f -name "lumerad" -print -quit)
if [ -n "$LUMERAD_PATH" ]; then
    install_binary "$LUMERAD_PATH"
else
    echo "Error: Could not find lumerad binary in the package"
    exit 1
fi

# Clean up
cd "$ORIG_DIR"
rm -rf "$TEMP_DIR"