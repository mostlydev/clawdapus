#!/bin/sh
# discord-responder.sh — Background process for stub runtimes.
# Polls Discord channel via REST API, responds to mentions of DISCORD_BOT_ID.
# When CLLAMA_TOKEN is set, makes a real LLM call through the cllama proxy
# before posting, proving the forced inference proxy contract.
#
# Required env vars: DISCORD_BOT_TOKEN, DISCORD_BOT_ID, ROLLCALL_CHANNEL_ID, CLAW_RUNTIME
# Optional env vars: CLLAMA_TOKEN (enables real LLM call through proxy)
set -eu

TOKEN="${DISCORD_BOT_TOKEN:-}"
BOT_ID="${DISCORD_BOT_ID:-}"
CHANNEL_ID="${ROLLCALL_CHANNEL_ID:-}"
RUNTIME="${CLAW_RUNTIME:-unknown}"
CLLAMA="${CLLAMA_TOKEN:-}"
CLLAMA_FORMAT="${ROLLCALL_CLLAMA_API_FORMAT:-openai}"
CLLAMA_MODEL="${ROLLCALL_CLLAMA_MODEL:-anthropic/claude-sonnet-4}"
UA="DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)"

[ -n "$TOKEN" ] && [ -n "$BOT_ID" ] && [ -n "$CHANNEL_ID" ] || {
  echo "[discord-responder] missing env vars (TOKEN=${TOKEN:+set} BOT_ID=$BOT_ID CHANNEL=$CHANNEL_ID), exiting" >&2
  exit 0
}

echo "[discord-responder] polling channel $CHANNEL_ID for mentions of $BOT_ID (runtime=$RUNTIME)" >&2
if [ -n "$CLLAMA" ]; then
  echo "[discord-responder] cllama token present — will route inference through proxy" >&2
else
  echo "[discord-responder] no cllama token — will use static response" >&2
fi

# Call cllama proxy for a real LLM response.
# Returns the LLM text on stdout, or empty string on failure.
call_cllama() {
  [ -n "$CLLAMA" ] || return 0

  case "$CLLAMA_FORMAT" in
    anthropic)
      payload=$(cat <<ENDJSON
{
  "model": "$CLLAMA_MODEL",
  "max_tokens": 80,
  "system": "You are a bot named ${RUNTIME}. Your runtime is ${RUNTIME}. When asked to introduce yourself, say exactly: I am [your name] and I run on ${RUNTIME}. Do not say you are Claude or made by Anthropic. Do not use markdown. One sentence only.",
  "messages": [
    {"role": "user", "content": "Introduce yourself."}
  ]
}
ENDJSON
)
      resp=$(curl -s --max-time 30 \
        -H "Authorization: Bearer $CLLAMA" \
        -H "Anthropic-Version: 2023-06-01" \
        -H "Content-Type: application/json" \
        -d "$payload" \
        "http://cllama:8080/v1/messages" 2>/dev/null) || { echo ""; return 0; }

      content=$(printf '%s' "$resp" | jq -r '
        if (.content | type) == "array" then
          [.content[]? | select(.type == "text") | (.text // "")]
          | join("")
        else
          empty
        end
      ' 2>/dev/null) || content=""
      printf '%s' "$content"
      return 0
      ;;
    openai)
      payload=$(cat <<ENDJSON
{
  "model": "$CLLAMA_MODEL",
  "max_tokens": 80,
  "messages": [
    {"role": "system", "content": "You are a bot named ${RUNTIME}. Your runtime is ${RUNTIME}. When asked to introduce yourself, say exactly: I am [your name] and I run on ${RUNTIME}. Do not say you are Claude or made by Anthropic. Do not use markdown. One sentence only."},
    {"role": "user", "content": "Introduce yourself."}
  ]
}
ENDJSON
)
      resp=$(curl -s --max-time 30 \
        -H "Authorization: Bearer $CLLAMA" \
        -H "Content-Type: application/json" \
        -d "$payload" \
        "http://cllama:8080/v1/chat/completions" 2>/dev/null) || { echo ""; return 0; }

      content=$(printf '%s' "$resp" | jq -r '.choices[0].message.content // empty' 2>/dev/null) || content=""
      printf '%s' "$content"
      return 0
      ;;
    *)
      echo "[discord-responder] unsupported CLLAMA format: $CLLAMA_FORMAT" >&2
      echo ""
      return 0
      ;;
  esac
}

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
      echo "[discord-responder] found trigger (attempt $i), calling LLM for $RUNTIME" >&2

      # Try real LLM call through cllama; fall back to static message
      llm_response=$(call_cllama)
      if [ -n "$llm_response" ]; then
        message="$llm_response"
        echo "[discord-responder] got LLM response via cllama proxy" >&2
      else
        message="I'm running on ${RUNTIME}. Stub runtime reporting for duty! (no cllama)"
        echo "[discord-responder] cllama unavailable, using static fallback" >&2
      fi

      # Escape the message for JSON (handle quotes and backslashes)
      escaped=$(printf '%s' "$message" | jq -Rs '.')
      send_resp=$(curl -s -w "\n%{http_code}" -X POST \
        -H "Authorization: Bot $TOKEN" \
        -H "Content-Type: application/json" \
        -H "User-Agent: $UA" \
        -d "{\"content\":$escaped}" \
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
