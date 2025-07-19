#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# Colors for better readability
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print info messages
info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

# Function to print success messages
success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# Function to print warning messages
warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# Function to print error messages and exit
error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# Function to setup a primary supernode
setup_primary() {
    if [ "$#" -ne 3 ]; then
        error "Usage: $0 primary <supernode-source-path> <data-dir-path> <config-file-path>"
    fi

    SUPERNODE_SRC="$1"
    DATA_DIR="$2"
    CONFIG_FILE="$3"

    info "Setting up primary supernode environment in $DATA_DIR"

    # Create the data directory if it doesn't exist
    mkdir -p "$DATA_DIR"

    # Check if binary already exists
   if [ ! -f "$DATA_DIR/supernode" ]; then
    info "Building supernode binary from $SUPERNODE_SRC..."
    CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=amd64 \
    go build \
    -trimpath \
    -ldflags="-s -w" \
    -o "$DATA_DIR/supernode" "$SUPERNODE_SRC" || error "Failed to build supernode binary"
    success "Supernode binary built successfully"
else
    info "Supernode binary already exists, skipping build..."
fi

    # Check if config already exists
    if [ ! -f "$DATA_DIR/config.yml" ]; then
        info "Copying config file from $CONFIG_FILE to $DATA_DIR..."
        cp "$CONFIG_FILE" "$DATA_DIR/config.yml" || error "Failed to copy config file"
        success "Config file copied successfully"
    else
        info "Config file already exists in $DATA_DIR, skipping copy..."
    fi

    # Define arrays of key names and corresponding mnemonics
    KEY_NAMES=(
        "testkey1"
        "testkey2"
        "testkey3"
    )

    MNEMONICS=(
        "odor kiss switch swarm spell make planet bundle skate ozone path planet exclude butter atom ahead angle royal shuffle door prevent merry alter robust"
        "club party current length duck agent love into slide extend spawn sentence kangaroo chunk festival order plate rare public good include situate liar miss"
        "young envelope urban crucial denial zone toward mansion protect bonus exotic puppy resource pistol expand tell cupboard radio hurry world radio trust explain million"
    )

    # Check that the arrays have the same length
    if [ ${#KEY_NAMES[@]} -ne ${#MNEMONICS[@]} ]; then
        error "Number of key names does not match number of mnemonics"
    fi

    info "Setting up keyring with defined keys and mnemonics..."
    # Loop through the arrays and recover each key with its corresponding mnemonic
    for i in "${!KEY_NAMES[@]}"; do
        KEY_NAME="${KEY_NAMES[$i]}"
        MNEMONIC="${MNEMONICS[$i]}"
        
        info "Recovering key: $KEY_NAME"
        # Use mnemonic flag and specify the base directory
        "$DATA_DIR/supernode" keys recover "$KEY_NAME" --mnemonic="$MNEMONIC" --basedir="$DATA_DIR" 2>/dev/null || {
            warning "Key recovery for $KEY_NAME may have failed if the key already exists. This is not necessarily an error."
        }
    done

    success "Supernode primary environment setup complete."
    echo "- Binary: $DATA_DIR/supernode"
    echo "- Config: $DATA_DIR/config.yaml"
    echo "- Base Directory: $DATA_DIR"
    echo "- Keys recovered: ${KEY_NAMES[*]}"
}

# Function to setup secondary supernodes
setup_secondary() {
    if [ "$#" -lt 5 ]; then
        error "Usage: $0 secondary <original-data-dir> <new-dir1> <config-file1> <new-dir2> <config-file2> [additional-dirs-and-configs...]"
    fi

    ORIGINAL_DIR="$1"
    shift

    # Check if original directory exists
    if [ ! -d "$ORIGINAL_DIR" ]; then
        error "Original directory $ORIGINAL_DIR does not exist"
    fi
    
    # Check if the supernode binary exists in the original directory
    if [ ! -f "$ORIGINAL_DIR/supernode" ]; then
        error "Supernode binary not found in $ORIGINAL_DIR"
    fi

    info "Setting up additional supernode environments from $ORIGINAL_DIR"
    
    # Process pairs of directory and config file
    while [ "$#" -ge 2 ]; do
        NEW_DIR="$1"
        CONFIG_FILE="$2"
        shift 2

        info "Setting up secondary node in: $NEW_DIR with config: $CONFIG_FILE"
        
        # Create the new directory if it doesn't exist
        mkdir -p "$NEW_DIR"
        
        # Copy binary to new directory
        info "Copying supernode binary to $NEW_DIR..."
        cp "$ORIGINAL_DIR/supernode" "$NEW_DIR/" || error "Failed to copy supernode binary to $NEW_DIR"
        
        # Copy config file to the new directory
        info "Copying config file to $NEW_DIR..."
        cp "$CONFIG_FILE" "$NEW_DIR/config.yml" || error "Failed to copy config file to $NEW_DIR"
        
        # Copy keyring from original directory to new directory
        if [ -d "$ORIGINAL_DIR/keys" ]; then
            info "Copying keyring from original directory to $NEW_DIR..."
            mkdir -p "$NEW_DIR/keys"
            
            # Use rsync if available, otherwise use cp
            if command -v rsync >/dev/null 2>&1; then
                rsync -a "$ORIGINAL_DIR/keys/" "$NEW_DIR/keys/" 2>/dev/null || true
            else
                # Fallback to cp if rsync is not available
                cp -r "$ORIGINAL_DIR/keys/"* "$NEW_DIR/keys/" 2>/dev/null || true
            fi
            
            success "Keyring copied successfully to $NEW_DIR"
        else
            warning "Keyring directory not found in $ORIGINAL_DIR/keys"
            warning "You may need to manually set up the keyring in $NEW_DIR"
        fi
        
        success "Secondary node setup complete in $NEW_DIR"
    done
    
    # If there are unpaired arguments, warn the user
    if [ "$#" -eq 1 ]; then
        warning "Unpaired argument detected: $1. Arguments must be provided in pairs (directory and config file)."
    fi
}

# Function to display usage info
usage() {
    echo "Usage: $0 <command> [arguments...]"
    echo ""
    echo "Commands:"
    echo "  primary <supernode-source-path> <data-dir-path> <config-file-path>"
    echo "      - Sets up a primary supernode environment"
    echo ""
    echo "  secondary <original-data-dir> <new-dir1> <config-file1> <new-dir2> <config-file2> [additional-dirs-and-configs...]"
    echo "      - Sets up one or more secondary supernodes based on the primary"
    echo ""
    echo "  all <supernode-source-path> <primary-data-dir> <primary-config-file> <secondary-dir1> <secondary-config1> <secondary-dir2> <secondary-config2> [additional-dirs-and-configs...]"
    echo "      - Sets up both primary and secondary nodes in one command"
    echo ""
    echo "Examples:"
    echo "  $0 primary supernode/main.go tests/system/supernode-data1 tests/system/config.test-1.yml"
    echo "  $0 secondary tests/system/supernode-data1 tests/system/supernode-data2 tests/system/config.test-2.yml tests/system/supernode-data3 tests/system/config.test-3.yml"
    echo "  $0 all supernode/main.go tests/system/supernode-data1 tests/system/config.test-1.yml tests/system/supernode-data2 tests/system/config.test-2.yml tests/system/supernode-data3 tests/system/config.test-3.yml"
    exit 1
}

# Function to setup all nodes (primary + secondary)
setup_all() {
    if [ "$#" -lt 7 ]; then
        error "Usage: $0 all <supernode-source-path> <primary-data-dir> <primary-config-file> <secondary-dir1> <secondary-config1> <secondary-dir2> <secondary-config2> [additional-dirs-and-configs...]"
    fi

    SUPERNODE_SRC="$1"
    PRIMARY_DIR="$2"
    PRIMARY_CONFIG="$3"
    shift 3

    # Clean up existing directories if they exist
    if [ -d "$PRIMARY_DIR" ]; then
        info "Cleaning up existing primary directory: $PRIMARY_DIR"
        rm -rf "$PRIMARY_DIR"
    fi
    
    # Clean up secondary directories
    ARGS_COPY=("$@")
    for ((i=0; i<${#ARGS_COPY[@]}; i+=2)); do
        DIR="${ARGS_COPY[$i]}"
        if [ -d "$DIR" ]; then
            info "Cleaning up existing secondary directory: $DIR"
            rm -rf "$DIR"
        fi
    done

    # Setup primary node
    setup_primary "$SUPERNODE_SRC" "$PRIMARY_DIR" "$PRIMARY_CONFIG"
    
    # Setup secondary nodes
    setup_secondary "$PRIMARY_DIR" "$@"
    
    success "All supernode environments have been set up successfully"
}

# Main script logic
if [ "$#" -lt 1 ]; then
    usage
fi

COMMAND="$1"
shift

case "$COMMAND" in
    primary)
        setup_primary "$@"
        ;;
    secondary)
        setup_secondary "$@"
        ;;
    all)
        setup_all "$@"
        ;;
    *)
        error "Unknown command: $COMMAND"
        usage
        ;;
esac