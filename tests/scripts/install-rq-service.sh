#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <data-dir>"
  exit 1
fi

# Destination inside supernode-data
data_dir="$1"
rq_dir="$data_dir/rqservice"

# Create and enter the rqservice directory
mkdir -p "$rq_dir"
cd "$rq_dir"

# Clone if needed
if [[ -d "rqservice" ]] || [[ -d ".git" ]]; then
  echo "Repository already present—skipping clone."
else
  git clone https://github.com/pastelnetwork/rqservice.git .
fi

# Build the binary in release mode
cargo build --release