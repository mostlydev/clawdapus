#!/bin/sh
# discord-responder.sh — Background process for stub runtimes.
# Polls Discord channel via REST API, responds to mentions of DISCORD_BOT_ID.
#
# Required env vars: DISCORD_BOT_TOKEN, DISCORD_BOT_ID, ROLLCALL_CHANNEL_ID, CLAW_RUNTIME
set -eu

TOKEN="${DISCORD_BOT_TOKEN:-}"
BOT_ID="${DISCORD_BOT_ID:-}"
CHANNEL_ID="${ROLLCALL_CHANNEL_ID:-}"
RUNTIME="${CLAW_RUNTIME:-unknown}"
UA="DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)"

[ -n "$TOKEN" ] && [ -n "$BOT_ID" ] && [ -n "$CHANNEL_ID" ] || {
  echo "[discord-responder] missing env vars (TOKEN=${TOKEN:+set} BOT_ID=$BOT_ID CHANNEL=$CHANNEL_ID), exiting" >&2
  exit 0
}

echo "[discord-responder] polling channel $CHANNEL_ID for mentions of $BOT_ID (runtime=$RUNTIME)" >&2

fetch_messages() {
  url="https://discord.com/api/v10/channels/$CHANNEL_ID/messages?limit=20"
  if [ -n "${1:-}" ]; then
    url="${url}&after=$1"
  fi
  curl -s -w "\n---HTTP_CODE:%{http_code}---" \
    -H "Authorization: Bot $TOKEN" \
    -H "User-Agent: $UA" \
    "$url" 2>/dev/null
}

parse_response() {
  resp="$1"
  http_code=$(printf '%s' "$resp" | grep -o 'HTTP_CODE:[0-9]*' | head -1 | cut -d: -f2)
  json_body=$(printf '%s' "$resp" | sed 's/---HTTP_CODE:[0-9]*---$//')
}

# Snapshot the latest message ID so we only react to NEW messages.
echo "[discord-responder] taking baseline snapshot..." >&2
baseline_id=""
for attempt in 1 2 3; do
  resp=$(fetch_messages "")
  parse_response "$resp"
  if [ "$http_code" = "200" ]; then
    clean=$(printf '%s' "$json_body" | tr -d '\000-\011\013-\037')
    baseline_id=$(printf '%s' "$clean" | jq -r '.[0].id // empty' 2>/dev/null) || baseline_id=""
    if [ -n "$baseline_id" ]; then
      echo "[discord-responder] baseline message ID: $baseline_id" >&2
      break
    fi
  fi
  sleep 2
done

if [ -z "$baseline_id" ]; then
  echo "[discord-responder] could not get baseline (empty channel?), using 0" >&2
  baseline_id="0"
fi

# Now poll for new messages after the baseline.
for i in $(seq 1 60); do
  resp=$(fetch_messages "$baseline_id")
  parse_response "$resp"

  if [ "$http_code" != "200" ]; then
    echo "[discord-responder] curl returned HTTP $http_code (attempt $i)" >&2
    sleep 5
    continue
  fi

  # Sanitize control characters that break jq (keep newlines 0x0A).
  clean=$(printf '%s' "$json_body" | tr -d '\000-\011\013-\037')
  msg_count=$(printf '%s' "$clean" | jq 'length' 2>/dev/null) || msg_count="jq-error"

  # Check for a message mentioning our bot ID (not from our own bot).
  has_trigger=$(printf '%s' "$clean" | jq -r \
    --arg bid "$BOT_ID" \
    '[.[] | select(.author.id != $bid) | select(.content | test($bid))] | length' 2>/dev/null) || has_trigger=0

  if [ "$i" -le 3 ] || [ "$((i % 10))" -eq 0 ]; then
    echo "[discord-responder] attempt $i: new_msgs=$msg_count triggers=$has_trigger" >&2
  fi

  if [ "$has_trigger" -gt 0 ]; then
    # Check if we already responded (avoid duplicate responses).
    already=$(printf '%s' "$clean" | jq -r \
      --arg rt "$RUNTIME" \
      '[.[] | select(.author.id == $bid) | select(.content | ascii_downcase | test($rt | ascii_downcase))] | length' \
      --arg bid "$BOT_ID" 2>/dev/null) || already=0

    if [ "$already" -eq 0 ]; then
      echo "[discord-responder] found trigger (attempt $i), sending response for $RUNTIME" >&2
      send_resp=$(curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Bot $TOKEN" \
        -H "Content-Type: application/json" \
        -H "User-Agent: $UA" \
        -d "{\"content\":\"I'm running on ${RUNTIME}. Stub runtime reporting for duty!\"}" \
        "https://discord.com/api/v10/channels/$CHANNEL_ID/messages" 2>/dev/null)
      send_code=$(printf '%s' "$send_resp" | tail -1)
      echo "[discord-responder] send response HTTP $send_code" >&2
      exit 0
    else
      echo "[discord-responder] already responded for $RUNTIME (attempt $i)" >&2
      exit 0
    fi
  fi

  sleep 5
done

echo "[discord-responder] timed out after 5 minutes without finding trigger" >&2
