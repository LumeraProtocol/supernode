#!/bin/bash

# Cascade Download Heap Profiling Script
# Samples heap every 30 seconds during cascade downloads

# Configuration - modify these as needed
PROFILE_URL="http://157.180.8.121:4444:8082/debug/pprof/heap"
INTERVAL=10
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
PROFILE_DIR="profiles_${TIMESTAMP}"

# Allow override via command line
if [ "$1" != "" ]; then
    PROFILE_URL="$1"
fi

echo "=== Cascade Heap Profiling ==="
echo "Profile URL: $PROFILE_URL"
echo "Interval: ${INTERVAL}s"
echo "Output Dir: $PROFILE_DIR"
echo

# Create profile directory
mkdir -p "$PROFILE_DIR"
cd "$PROFILE_DIR"

# Test connection first
echo "Testing connection to profiling server..."
if ! curl -s --fail "$PROFILE_URL" > /dev/null; then
    echo "ERROR: Cannot connect to profiling server at $PROFILE_URL"
    echo "Make sure your supernode is running on testnet!"
    exit 1
fi

echo "✓ Connected to profiling server"
echo

# Take baseline
echo "Taking baseline heap snapshot..."
curl -s -o "heap_00s.prof" "$PROFILE_URL"
echo "✓ Baseline saved: heap_00s.prof"
echo

echo "*** NOW START YOUR CASCADE DOWNLOAD ***"
echo "Press ENTER when download has started..."
read

echo "Starting heap profiling every ${INTERVAL}s..."
echo "Press Ctrl+C to stop"
echo

# Counter for snapshots
counter=1

# Function to handle cleanup on exit
cleanup() {
    echo
    echo "Profiling stopped. Taking final snapshot..."
    final_elapsed=$((counter * INTERVAL))
    curl -s -o "heap_${final_elapsed}s_final.prof" "$PROFILE_URL"

    echo
    echo "=== Profiling Complete ==="
    echo "Location: $(pwd)"
    echo "Files created:"
    ls -la *.prof
    echo
    echo "Analysis commands:"
    echo "# Compare baseline to final:"
    echo "go tool pprof -http=:8080 -base heap_00s.prof heap_${final_elapsed}s_final.prof"
    exit 0
}

# Set up signal handler
trap cleanup INT TERM

# Main profiling loop
while true; do
    sleep $INTERVAL

    elapsed=$((counter * INTERVAL))
    minutes=$((elapsed / 60))
    seconds=$((elapsed % 60))

    timestamp=$(date +%H:%M:%S)
    filename="heap_${elapsed}s.prof"

    echo "[$timestamp] Taking snapshot $counter (${minutes}m ${seconds}s elapsed)..."

    if curl -s -o "$filename" "$PROFILE_URL"; then
        size=$(ls -lh "$filename" | awk '{print $5}')
        echo "✓ Saved: $filename ($size)"
    else
        echo "✗ Failed to get snapshot $counter"
    fi

    ((counter++))
done