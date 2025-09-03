#!/bin/bash
set -o pipefail

# Dynamic Supernode Version + Uptime Inventory
# Fetches supernode list from Lumera LCD and queries each node's /api/v1/status.
# Prints a breakdown sorted by version string (descending).
#
# Usage: ./check_versions.sh    (no args)

TIMEOUT=10
API_URL="https://lcd.testnet.lumera.io/LumeraProtocol/lumera/supernode/list_super_nodes?pagination.limit=1000&pagination.count_total=true"

TOTAL_CHECKED=0
UNREACHABLE_COUNT=0
INVALID_COUNT=0

FAILED_CALLS=()
INVALID_RESPONSES=()

declare -A VERSION_COUNTS
declare -A VERSION_UPTIME_SECS_SUM

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq is required. Install with: sudo apt-get install -y jq"
  exit 1
fi

echo "Fetching supernode list from Lumera API..."
echo "=============================================================================="
echo "Fetching from: $API_URL"

SUPERNODE_DATA=$(curl -s --connect-timeout "$TIMEOUT" --max-time 30 \
  --compressed \
  -H "User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36" \
  -H "Accept: application/json, text/plain, */*" \
  "$API_URL")
CURL_STATUS=$?

if [ $CURL_STATUS -ne 0 ]; then
  echo "ERROR: curl failed with exit code $CURL_STATUS"
  exit 1
fi

if [ -z "$SUPERNODE_DATA" ]; then
  echo "ERROR: Empty response from API"
  exit 1
fi

echo "API Response preview: $(echo "$SUPERNODE_DATA" | head -c 200)..."

if ! echo "$SUPERNODE_DATA" | jq empty >/dev/null 2>&1; then
  echo "ERROR: Invalid JSON response from API"
  echo "Full response:"
  echo "$SUPERNODE_DATA"
  exit 1
fi

echo "Parsing supernode data and extracting latest IP addresses..."

TEMP_FILE=$(mktemp)
echo "$SUPERNODE_DATA" | jq -r '
  .supernodes[]
  | .supernode_account as $acct
  | ((.prev_ip_addresses // [])
      | sort_by(.height|tonumber)
      | last
      | .address) as $ip
  | select($ip != null and ($ip|tostring) != "")
  | "\($acct),\($ip)"
' > "$TEMP_FILE"

SUPERNODE_COUNT=$(wc -l < "$TEMP_FILE" | awk '{print $1}')
echo "Found $SUPERNODE_COUNT supernodes to check"
echo "Starting version + uptime inventory"
echo "=============================================================================="

while IFS=',' read -r ACCOUNT LATEST_IP; do
  [ -z "$ACCOUNT" ] && continue
  TOTAL_CHECKED=$((TOTAL_CHECKED + 1))

  CLEAN_IP=$(echo "$LATEST_IP" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')
  HOSTPORT=$(echo "$CLEAN_IP" | sed -E 's|^https?://||')
  if ! echo "$HOSTPORT" | grep -qE ':[0-9]+$'; then
    HOSTPORT="${HOSTPORT}:4444"
  fi
  HOSTPORT=$(echo "$HOSTPORT" | sed -E 's/:4444$/:8002/; s/:443$/:8002/')
  if ! echo "$HOSTPORT" | grep -qE ':[0-9]+$'; then
    HOSTPORT="${HOSTPORT}:8002"
  fi

  STATUS_ENDPOINT="http://${HOSTPORT}/api/v1/status"
  RESPONSE=$(curl -s --connect-timeout "$TIMEOUT" --max-time "$TIMEOUT" "$STATUS_ENDPOINT")
  CURL_EXIT_CODE=$?

  if [ $CURL_EXIT_CODE -eq 0 ] && [ -n "$RESPONSE" ]; then
    VERSION=$(echo "$RESPONSE" | jq -r 'try .version // empty' 2>/dev/null)
    UPTIME_SECONDS=$(echo "$RESPONSE" | jq -r 'try .uptime_seconds // empty' 2>/dev/null)

    if [ -n "$VERSION" ]; then
      VERSION_COUNTS["$VERSION"]=$(( ${VERSION_COUNTS["$VERSION"]:-0} + 1 ))

      UPTIME_HOURS_STR="n/a"
      if [[ -n "$UPTIME_SECONDS" && "$UPTIME_SECONDS" =~ ^[0-9]+$ ]]; then
        UPTIME_HOURS_STR=$(awk -v s="$UPTIME_SECONDS" 'BEGIN{printf "%.2f", s/3600}')
        VERSION_UPTIME_SECS_SUM["$VERSION"]=$(( ${VERSION_UPTIME_SECS_SUM["$VERSION"]:-0} + UPTIME_SECONDS ))
      fi

      echo "• $ACCOUNT (http://$HOSTPORT): version=$VERSION, uptime_hours=$UPTIME_HOURS_STR"
    else
      echo "✗ $ACCOUNT (http://$HOSTPORT): ERROR (invalid response format)"
      INVALID_RESPONSES+=("$ACCOUNT (http://$HOSTPORT)")
      INVALID_COUNT=$((INVALID_COUNT + 1))
    fi
  else
    case $CURL_EXIT_CODE in
      6)  ERROR_MSG="DNS resolution failed" ;;
      7)  ERROR_MSG="connection refused" ;;
      28) ERROR_MSG="timeout" ;;
      *)  ERROR_MSG="curl error code $CURL_EXIT_CODE" ;;
    esac
    echo "✗ $ACCOUNT (http://$HOSTPORT): UNREACHABLE ($ERROR_MSG)"
    FAILED_CALLS+=("$ACCOUNT (http://$HOSTPORT) - $ERROR_MSG")
    UNREACHABLE_COUNT=$((UNREACHABLE_COUNT + 1))
  fi
done < "$TEMP_FILE"

rm -f "$TEMP_FILE"

echo "=============================================================================="
echo "SUMMARY:"
echo "Total supernodes checked: $TOTAL_CHECKED"
REACHABLE_VALID=0

# Sort by version string (descending, natural sort)
TMP_SORT=$(mktemp)
for v in "${!VERSION_COUNTS[@]}"; do
  c=${VERSION_COUNTS["$v"]}
  echo -e "${v}\t${c}" >> "$TMP_SORT"
  REACHABLE_VALID=$((REACHABLE_VALID + c))
done

if [ -s "$TMP_SORT" ]; then
  echo "Version breakdown (sorted by version desc):"
  sort -V -r "$TMP_SORT" | awk -F'\t' '{printf "  - %s: %s\n", $1, $2}'
else
  echo "Version breakdown: (none reachable with valid response)"
fi
rm -f "$TMP_SORT"

echo "Invalid responses: $INVALID_COUNT"
echo "Unreachable: $UNREACHABLE_COUNT"
echo "Reachable & valid: $REACHABLE_VALID"

TMP_SORT2=$(mktemp)
for v in "${!VERSION_COUNTS[@]}"; do
  c=${VERSION_COUNTS["$v"]}
  sum=${VERSION_UPTIME_SECS_SUM["$v"]:-0}
  if [ "$c" -gt 0 ] && [ "$sum" -gt 0 ]; then
    avg_hours=$(awk -v s="$sum" -v n="$c" 'BEGIN{printf "%.2f", (s/n)/3600}')
  else
    avg_hours="n/a"
  fi
  echo -e "${v}\t${avg_hours}" >> "$TMP_SORT2"
done

if [ -s "$TMP_SORT2" ]; then
  echo ""
  echo "Average uptime by version (hours, sorted by version desc):"
  sort -V -r "$TMP_SORT2" | awk -F'\t' '{printf "  - %s: %s (avg)\n", $1, $2}'
fi
rm -f "$TMP_SORT2"

if [ ${#FAILED_CALLS[@]} -gt 0 ] || [ ${#INVALID_RESPONSES[@]} -gt 0 ]; then
  echo ""
  echo "FAILED CALLS DETAILS:"
  echo "====================="
  if [ ${#FAILED_CALLS[@]} -gt 0 ]; then
    echo "Connection failures (${#FAILED_CALLS[@]}):"
    for failed in "${FAILED_CALLS[@]}"; do
      echo "  - $failed"
    done
  fi
  if [ ${#INVALID_RESPONSES[@]} -gt 0 ]; then
    echo "Invalid response format (${#INVALID_RESPONSES[@]}):"
    for invalid in "${INVALID_RESPONSES[@]}"; do
      echo "  - $invalid"
    done
  fi
fi
