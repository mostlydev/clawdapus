#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "Usage: $(basename "$0") <bankroll_usd> <available_usd> <daily_pnl_usd> <runway_hours_estimate>"
  exit 1
fi

BANKROLL="$1"
AVAILABLE="$2"
DAILY_PNL="$3"
RUNWAY="$4"

STATE_FILE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/state/balance.json"

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 1
fi

mkdir -p "$(dirname "$STATE_FILE")"

if [[ ! -f "$STATE_FILE" ]]; then
  cat > "$STATE_FILE" <<JSON
{
  "timestamp": "1970-01-01T00:00:00Z",
  "bankroll_usd": 0,
  "available_usd": 0,
  "daily_pnl_usd": 0,
  "runway_hours_estimate": 0,
  "notes": "auto-created"
}
JSON
fi

TMP="${STATE_FILE}.tmp"

jq \
  --arg ts "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --argjson bankroll "$BANKROLL" \
  --argjson available "$AVAILABLE" \
  --argjson pnl "$DAILY_PNL" \
  --argjson runway "$RUNWAY" \
  '.timestamp = $ts
   | .bankroll_usd = $bankroll
   | .available_usd = $available
   | .daily_pnl_usd = $pnl
   | .runway_hours_estimate = $runway' \
  "$STATE_FILE" > "$TMP"

mv "$TMP" "$STATE_FILE"

echo "Updated $STATE_FILE"
