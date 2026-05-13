#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# Sample usage:
# ./install-lumera.sh                  # uses latest release
# ./install-lumera.sh latest-tag       # uses latest tag from /tags
# ./install-lumera.sh v1.1.0           # installs this specific version
# LUMERAD_BINARY=/path/to/binary ./install-lumera.sh  # uses existing binary

install_file() {
    local src="$1"
    local dest="$2"
    if [ -w "$(dirname "$dest")" ]; then
        cp "$src" "$dest"
        return 0
    fi
    if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
        sudo cp "$src" "$dest"
        return 0
    fi
    return 1
}

install_binary() {
    local binary_path="$1"
    chmod +x "$binary_path"

    if ! install_file "$binary_path" /usr/local/bin/lumerad; then
        local user_bin="${HOME:-$PWD}/bin"
        mkdir -p "$user_bin"
        cp "$binary_path" "$user_bin/lumerad"
        chmod +x "$user_bin/lumerad"
        export PATH="$user_bin:$PATH"
        echo "Installed without sudo to $user_bin/lumerad"
    fi
    
    # Verify installation
    if command -v lumerad > /dev/null; then
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
SOURCE_REF=""

REPO="LumeraProtocol/lumera"
GITHUB_API="https://api.github.com/repos/$REPO"

first_linux_amd64_asset_url() {
    if command -v jq >/dev/null 2>&1; then
        jq -r 'if type == "object" then ([.assets[]? | select(.name | test("linux_amd64.tar.gz$")) | .browser_download_url][0] // empty) else empty end'
    else
        grep -o '"browser_download_url"[[:space:]]*:[[:space:]]*"[^"]*linux_amd64\.tar\.gz[^"]*"' \
            | head -n 1 \
            | sed 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'
    fi
}

release_download_url_for_tag() {
    local tag="$1"
    curl -s "$GITHUB_API/releases/tags/$tag" | first_linux_amd64_asset_url
}

# Go pseudo-versions encode the source commit, not the release tag name. Map the
# trailing commit hash back to an actual GitHub release tag before looking for
# release assets. Example: v1.12.0-rc.0.20260506174431-80a4b00767a9 resolves to
# release tag v.1.12.0-rc2, whose asset is lumera_v.1.12.0-rc2_linux_amd64.tar.gz.
release_tag_for_pseudo_version() {
    local version="$1"
    local hash="${version##*-}"
    if ! [[ "$hash" =~ ^[0-9a-f]{12,40}$ ]]; then
        return 1
    fi

    # Prefer git refs over the GitHub tags API. The API can return a JSON
    # error/string under CI rate limiting, which made jq fail with:
    # "Cannot index string with string \"commit\"". ls-remote is stable for
    # both lightweight tags and annotated tag peeled refs (refs/tags/<tag>^{}).
    if command -v git >/dev/null 2>&1; then
        local git_tag
        git_tag=$(git ls-remote --tags "https://github.com/$REPO.git" \
            | awk -v h="$hash" '
                $1 ~ "^" h {
                    ref=$2
                    sub(/^refs\/tags\//, "", ref)
                    sub(/\^\{\}$/, "", ref)
                    print ref
                    exit
                }')
        if [ -n "$git_tag" ]; then
            echo "$git_tag"
            return 0
        fi
    fi

    # Fallback for environments without git. Guard on JSON shape so API errors
    # yield an empty result instead of a jq type error.
    if command -v jq >/dev/null 2>&1; then
        curl -s -S -L "$GITHUB_API/tags?per_page=100" \
            | jq -r --arg h "$hash" 'if type == "array" then ([.[] | select((.commit.sha? // "") | startswith($h)) | .name][0] // empty) else empty end'
    else
        curl -s -S -L "$GITHUB_API/tags?per_page=100" \
            | python3 -c 'import json,sys; h=sys.argv[1];
try:
    tags=json.load(sys.stdin)
except Exception:
    tags=[]
if not isinstance(tags, list):
    tags=[]
print(next((t.get("name", "") for t in tags if isinstance(t, dict) and t.get("commit",{}).get("sha","").startswith(h)), ""))' "$hash"
    fi
}

pseudo_version_hash() {
    local version="$1"
    local hash="${version##*-}"
    if [[ "$hash" =~ ^[0-9a-f]{12,40}$ ]]; then
        echo "$hash"
    fi
}

build_lumerad_from_source_ref() {
    local ref="$1"
    local out="$2"
    local src_dir="$3/lumera-src"

    if ! command -v git >/dev/null 2>&1; then
        echo "Error: git is required to build lumerad from source" >&2
        return 1
    fi
    if ! command -v "${GO:-go}" >/dev/null 2>&1; then
        echo "Error: go is required to build lumerad from source" >&2
        return 1
    fi

    # Pseudo-versions can contain a shortened commit hash that is not fetchable
    # directly by all servers. Use a normal clone so checkout can resolve the
    # abbreviation from the repository object database.
    git clone "https://github.com/$REPO.git" "$src_dir" >/dev/null
    (cd "$src_dir" && git switch --detach "$ref" >/dev/null)
    (cd "$src_dir" && "${GO:-go}" build -o "$out" ./cmd/lumera)
}

# Determine tag and download URL based on mode
if [ "$MODE" == "latest-tag" ]; then
    if command -v jq >/dev/null 2>&1; then
        TAG_NAME=$(curl -s "$GITHUB_API/tags?per_page=1" | jq -r '.[0].name')
    else
        TAG_NAME=$(curl -s "$GITHUB_API/tags?per_page=1" | grep '"name"' | head -n 1 | sed -E 's/.*"([^"]+)".*/\1/')
    fi
    DOWNLOAD_URL=$(release_download_url_for_tag "$TAG_NAME")

elif [[ "$MODE" =~ ^v\.?[0-9]+\.[0-9]+\.[0-9]+.*$ ]]; then
    TAG_NAME="$MODE"
    DOWNLOAD_URL=$(release_download_url_for_tag "$TAG_NAME")
    if [ -z "$DOWNLOAD_URL" ]; then
        RESOLVED_TAG=$(release_tag_for_pseudo_version "$MODE")
        if [ -n "$RESOLVED_TAG" ]; then
            TAG_NAME="$RESOLVED_TAG"
            DOWNLOAD_URL=$(release_download_url_for_tag "$TAG_NAME")
        else
            SOURCE_REF=$(pseudo_version_hash "$MODE")
            if [ -n "$SOURCE_REF" ]; then
                TAG_NAME="$MODE"
            fi
        fi
    fi

elif [ "$MODE" == "latest-release" ]; then
    RELEASE_INFO=$(curl -s -S -L "$GITHUB_API/releases/latest")

    # Extract tag name and download URL
    if command -v jq >/dev/null 2>&1; then
        TAG_NAME=$(echo "$RELEASE_INFO" | jq -r '.tag_name')
    else
        TAG_NAME=$(echo "$RELEASE_INFO" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    fi
    DOWNLOAD_URL=$(echo "$RELEASE_INFO" | first_linux_amd64_asset_url)

else
    echo "Error: Invalid mode '$MODE'"
    echo "Usage: $0 [latest-release|latest-tag|vX.Y.Z|go-pseudo-version]"
    echo "   or: LUMERAD_BINARY=/path/to/binary $0"
    exit 1
fi

echo "Selected tag: $TAG_NAME"

# Validate that we have either a release asset or a source ref to build.
if [ -z "$TAG_NAME" ] || { [ -z "$DOWNLOAD_URL" ] && [ -z "$SOURCE_REF" ]; }; then
    echo "Error: Could not determine tag, download URL, or source ref"
    exit 1
fi

# Download/extract release assets when available; otherwise build lumerad from
# the pseudo-version commit. Pseudo-version commits usually do not have GitHub
# release assets, but CI still needs the binary to match the Go module code.
TEMP_DIR=$(mktemp -d)
ORIG_DIR=$(pwd)

if [ -n "$DOWNLOAD_URL" ]; then
    curl -L --progress-bar "$DOWNLOAD_URL" -o "$TEMP_DIR/lumera.tar.gz"

    cd "$TEMP_DIR"
    tar -xzf lumera.tar.gz
    rm lumera.tar.gz

    # Install WASM library when the release bundle contains one.
    WASM_LIB=$(find . -type f -name "libwasmvm*.so" -print -quit)
    if [ -n "$WASM_LIB" ]; then
        install_file "$WASM_LIB" /usr/lib/$(basename "$WASM_LIB") || echo "Warning: could not install $(basename "$WASM_LIB"); continuing"
    fi
else
    echo "No release asset found for $MODE; building lumerad from source ref $SOURCE_REF"
    build_lumerad_from_source_ref "$SOURCE_REF" "$TEMP_DIR/lumerad" "$TEMP_DIR"
    cd "$TEMP_DIR"
fi

# Find and install lumerad binary
LUMERAD_PATH=$(find . -type f -name "lumerad" -print -quit)
if [ -n "$LUMERAD_PATH" ]; then
    install_binary "$LUMERAD_PATH"
else
    echo "Error: Could not find lumerad binary"
    exit 1
fi

# Clean up
cd "$ORIG_DIR"
rm -rf "$TEMP_DIR"