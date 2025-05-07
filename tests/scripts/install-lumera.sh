#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# GitHub API URL for the latest release
GITHUB_API_URL="https://api.github.com/repos/LumeraProtocol/lumera/releases/latest"

echo "Fetching latest release information..."
RELEASE_INFO=$(curl -s -S -L "$GITHUB_API_URL")

# Extract tag name and download URL
if command -v jq >/dev/null 2>&1; then
    TAG_NAME=$(echo "$RELEASE_INFO" | jq -r '.tag_name')
    DOWNLOAD_URL=$(echo "$RELEASE_INFO" | jq -r '.assets[] | select(.name | test("linux_amd64.tar.gz$")) | .browser_download_url')
else
    TAG_NAME=$(echo "$RELEASE_INFO" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    DOWNLOAD_URL=$(echo "$RELEASE_INFO" | grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*linux_amd64\.tar\.gz[^"]*"' | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
fi

echo "Latest release: $TAG_NAME"
if [ -z "$DOWNLOAD_URL" ]; then
    echo "Error: Could not find Lumera download URL"
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