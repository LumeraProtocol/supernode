#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# Check if all required arguments are provided
if [ "$#" -lt 5 ]; then
    echo "Usage: $0 <original-data-dir> <new-dir1> <config-file1> <new-dir2> <config-file2>"
    echo "Example: $0 tests/system/supernode-data tests/system/supernode-data2 config.test-2.yml tests/system/supernode-data3 config.test-3.yml"
    exit 1
fi

# Assign arguments to variables
ORIGINAL_DIR="$1"
NEW_DIR1="$2"
CONFIG_FILE1="$3"
NEW_DIR2="$4"
CONFIG_FILE2="$5"

echo "Setting up additional supernode environments"
echo "Original directory: $ORIGINAL_DIR"
echo "New directory 1: $NEW_DIR1 with config: $CONFIG_FILE1"
echo "New directory 2: $NEW_DIR2 with config: $CONFIG_FILE2"

# Check if original directory exists
if [ ! -d "$ORIGINAL_DIR" ]; then
    echo "Error: Original directory $ORIGINAL_DIR does not exist"
    exit 1
fi

# Create the new directories if they don't exist
mkdir -p "$NEW_DIR1"
mkdir -p "$NEW_DIR2"

# Copy binary to new directories
if [ -f "$ORIGINAL_DIR/supernode" ]; then
    echo "Copying supernode binary to new directories..."
    cp "$ORIGINAL_DIR/supernode" "$NEW_DIR1/"
    cp "$ORIGINAL_DIR/supernode" "$NEW_DIR2/"
else
    echo "Error: Supernode binary not found in $ORIGINAL_DIR"
    exit 1
fi

# Copy config files to their respective directories
echo "Copying config files to respective directories..."
cp "$CONFIG_FILE1" "$NEW_DIR1/config.yaml"
cp "$CONFIG_FILE2" "$NEW_DIR2/config.yaml"

# Copy keyring from original directory to new directories
# The keyring is stored in the 'keys' directory
if [ -d "$ORIGINAL_DIR/keys" ]; then
    echo "Copying keyring from original directory to new directories..."
    mkdir -p "$NEW_DIR1/keys"
    mkdir -p "$NEW_DIR2/keys"
    
    # Use rsync with the --archive flag to preserve permissions and other attributes
    # If rsync is not available, you might need to install it or use cp -r instead
    if command -v rsync >/dev/null 2>&1; then
        rsync -a "$ORIGINAL_DIR/keys/" "$NEW_DIR1/keys/" 2>/dev/null || true
        rsync -a "$ORIGINAL_DIR/keys/" "$NEW_DIR2/keys/" 2>/dev/null || true
    else
        # Fallback to cp if rsync is not available
        cp -r "$ORIGINAL_DIR/keys/"* "$NEW_DIR1/keys/" 2>/dev/null || true
        cp -r "$ORIGINAL_DIR/keys/"* "$NEW_DIR2/keys/" 2>/dev/null || true
    fi
    
    echo "Keyring copied successfully"
else
    echo "Warning: Keyring directory not found in $ORIGINAL_DIR/keys"
    echo "You may need to manually set up the keyring in the new directories"
fi

echo "Additional supernode environments setup complete."
echo "- Directory 1: $NEW_DIR1"
echo "- Config 1: $NEW_DIR1/config.yaml"
echo "- Directory 2: $NEW_DIR2"
echo "- Config 2: $NEW_DIR2/config.yaml"