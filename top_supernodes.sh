#!/usr/bin/env bash

# Show top supernodes for a block height and non-top active supernodes
# This script retrieves supernodes using the same XOR distance ranking as the Go code:
# - GetTopSuperNodesForBlock uses XOR(blockHash, hash(validatorAddress)) to rank nodes
# - Nodes with smallest XOR distance are considered "top" nodes
#
# Usage:
#   ./top_supernodes.sh [--height <blockHeight>] [--top-count <n>] [--non-top-count <m>] [--state <STATE>] [--endpoint <host:port>]
#
# Defaults:
#   --endpoint      defaults to grpc.testnet.lumera.io:443
#   --height        if omitted, fetches latest block height from the chain
#   --top-count     number of top supernodes to fetch (default: 10, matching Go code limit)
#   --non-top-count number of non-top ACTIVE supernodes to show (default: 5)
#   --state         optional filter for top list (e.g., SUPERNODE_STATE_ACTIVE); omitted by default

set -euo pipefail

YELLOW='\033[1;33m'
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

ENDPOINT="grpc.testnet.lumera.io:443"
HEIGHT=""
TOP_COUNT="10"  # Default to 10 like the Go code
NON_TOP_COUNT="5"
STATE=""

usage() {
  echo "Usage: $0 [--height <blockHeight>] [--top-count <n>] [--non-top-count <m>] [--state <STATE>] [--endpoint <host:port>]" >&2
  echo "" >&2
  echo "Options:" >&2
  echo "  --height        Block height to query (defaults to latest)" >&2
  echo "  --top-count     Number of top supernodes to fetch (default: 10)" >&2
  echo "  --non-top-count Number of non-top active supernodes to show (default: 5)" >&2
  echo "  --state         Filter supernodes by state (e.g., SUPERNODE_STATE_ACTIVE)" >&2
  echo "  --endpoint      gRPC endpoint (default: grpc.testnet.lumera.io:443)" >&2
}

die() {
  echo -e "${RED}Error:${NC} $*" >&2
  exit 1
}

require_bin() {
  for b in "$@"; do
    command -v "$b" >/dev/null 2>&1 || die "Missing dependency: $b"
  done
}

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    --endpoint)
      ENDPOINT=${2:-}
      shift 2
      ;;
    --height)
      HEIGHT=${2:-}
      shift 2
      ;;
    --top-count)
      TOP_COUNT=${2:-}
      shift 2
      ;;
    --non-top-count)
      NON_TOP_COUNT=${2:-}
      shift 2
      ;;
    --state)
      STATE=${2:-}
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

echo -e "${YELLOW}Fetching top $TOP_COUNT supernodes and $NON_TOP_COUNT non-top active supernodes...${NC}"

require_bin grpcurl jq

# Get latest block height if not provided
if [[ -z "$HEIGHT" ]]; then
  echo "Querying latest block height from $ENDPOINT ..."
  latest_json=$(grpcurl "$ENDPOINT" cosmos.base.tendermint.v1beta1.Service.GetLatestBlock 2>/dev/null || true)
  if [[ -z "$latest_json" ]]; then
    die "Failed to fetch latest block. Ensure endpoint is reachable and grpcurl is configured."
  fi
  # Height is typically a string under block.header.height
  HEIGHT=$(echo "$latest_json" | jq -r '(.block.header.height // .sdkBlock.header.height // .block.header.height) // empty')
  if [[ -z "$HEIGHT" || "$HEIGHT" == "null" ]]; then
    die "Could not parse latest block height from response"
  fi
fi

if ! [[ "$HEIGHT" =~ ^[0-9]+$ ]]; then
  die "Invalid --height value: $HEIGHT"
fi

echo -e "${GREEN}Using block height:${NC} $HEIGHT"

# Build request for GetTopSuperNodesForBlock
# This uses the same XOR distance ranking as fetchSupernodes() in the Go SDK:
# 1. The chain computes XOR(blockHash, hash(validatorAddress)) for each node
# 2. Nodes are sorted by this XOR distance (smallest distance = top node)
# 3. This ensures deterministic selection based on block hash and validator addresses
req_obj=$(jq -n --argjson h "$HEIGHT" '{blockHeight: ($h|tonumber)}')
if [[ -n "$TOP_COUNT" ]]; then
  if ! [[ "$TOP_COUNT" =~ ^[0-9]+$ ]]; then
    die "Invalid --top-count value: $TOP_COUNT"
  fi
  req_obj=$(echo "$req_obj" | jq --argjson l "$TOP_COUNT" '. + {limit: ($l|tonumber)}')
fi
if [[ -n "$STATE" ]]; then
  # Expect full enum string like SUPERNODE_STATE_ACTIVE; leave as-is
  req_obj=$(echo "$req_obj" | jq --arg s "$STATE" '. + {state: $s}')
fi

echo "Fetching top $TOP_COUNT supernodes for height $HEIGHT (using XOR distance ranking)..."
top_json=$(grpcurl -d "$(echo "$req_obj" | jq -c .)" "$ENDPOINT" lumera.supernode.Query.GetTopSuperNodesForBlock 2>/dev/null || true)
if [[ -z "$top_json" ]]; then
  die "Failed to fetch top supernodes"
fi

top_accounts_json=$(echo "$top_json" | jq -c '[.supernodes[]?.supernodeAccount]')
top_count=$(echo "$top_accounts_json" | jq 'length')

echo -e "${YELLOW}\nTop supernodes ($top_count)${NC}"
echo "$top_json" | jq -r '
  .supernodes[]? |
  {
    supernodeAccount: .supernodeAccount,
    validatorAddress: .validatorAddress,
    ip: ((.prevIpAddresses // []) | if length==0 then null else (max_by(.height|tonumber).address) end)
  } |
  "- Account: \(.supernodeAccount) | Validator: \(.validatorAddress // "-") | IP: \(.ip // "-")"'

echo -e "${YELLOW}\nSelecting $NON_TOP_COUNT non-top ACTIVE supernodes...${NC}"
all_json=$(grpcurl "$ENDPOINT" lumera.supernode.Query.ListSuperNodes 2>/dev/null || true)
if [[ -z "$all_json" ]]; then
  die "Failed to fetch full supernodes list"
fi

# Filter: active by latest state, and not in top list; pick first 5
not_top_json=$(echo "$all_json" | jq --argjson top "$top_accounts_json" -c '
  [.supernodes[] |
    . as $n |
    { 
      supernodeAccount: .supernodeAccount,
      validatorAddress: .validatorAddress,
      latestState: ((.states // []) | if length==0 then null else (max_by(.height|tonumber).state) end),
      ip: ((.prevIpAddresses // []) | if length==0 then null else (max_by(.height|tonumber).address) end)
    } |
    select(.latestState == "SUPERNODE_STATE_ACTIVE") |
    select((.supernodeAccount as $acc | ($top | index($acc)) | not))
  ][0:'"$NON_TOP_COUNT"']')

not_top_count=$(echo "$not_top_json" | jq 'length')
echo -e "${GREEN}Found ${not_top_count} non-top ACTIVE supernodes${NC}"
echo "$not_top_json" | jq -r '.[] | "- Account: \(.supernodeAccount) | Validator: \(.validatorAddress // "-") | IP: \(.ip // "-")"'

echo -e "\n${GREEN}Done.${NC}"

