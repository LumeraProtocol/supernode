#!/bin/bash
set -e  # Exit immediately if a command exits with a non-zero status

# Check if all required arguments are provided
if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <supernode-source-path> <data-dir-path> <config-file-path>"
    exit 1
fi

# Assign arguments to variables
SUPERNODE_SRC="$1"
DATA_DIR="$2"
CONFIG_FILE="$3"

echo "Setting up supernode test environment in $DATA_DIR"

# Create the data directory if it doesn't exist
mkdir -p "$DATA_DIR"

# Check if binary already exists
if [ ! -f "$DATA_DIR/supernode" ]; then
    echo "Building supernode binary from $SUPERNODE_SRC..."
    go build -o "$DATA_DIR/supernode" "$SUPERNODE_SRC"
else
    echo "Supernode binary already exists, skipping build..."
fi

# Check if config already exists
if [ ! -f "$DATA_DIR/config.yaml" ]; then
    echo "Copying config file from $CONFIG_FILE to $DATA_DIR..."
    cp "$CONFIG_FILE" "$DATA_DIR/config.yaml"
else
    echo "Config file already exists in $DATA_DIR, skipping copy..."
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
    echo "Error: Number of key names does not match number of mnemonics"
    exit 1
fi

echo "Setting up keyring with defined keys and mnemonics..."
# Loop through the arrays and recover each key with its corresponding mnemonic
for i in "${!KEY_NAMES[@]}"; do
    KEY_NAME="${KEY_NAMES[$i]}"
    MNEMONIC="${MNEMONICS[$i]}"
    
    echo "Recovering key: $KEY_NAME"
    # Pipe the mnemonic directly to the command and specify the base directory
    echo "$MNEMONIC" | "$DATA_DIR/supernode" keys recover "$KEY_NAME" --config="$DATA_DIR/config.yaml" --basedir="$DATA_DIR" || {
        echo "Note: Key recovery for $KEY_NAME may have failed if the key already exists. This is not necessarily an error."
    }
done

echo "Supernode test environment setup complete."
echo "- Binary: $DATA_DIR/supernode"
echo "- Config: $DATA_DIR/config.yaml"
echo "- Base Directory: $DATA_DIR"
echo "- Keys recovered: ${KEY_NAMES[*]}"