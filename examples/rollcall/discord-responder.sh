#!/bin/sh
# discord-responder.sh — Background process for stub runtimes.
# Polls Discord channel via REST API, responds to mentions of DISCORD_BOT_ID.
# When CLLAMA_TOKEN is set, makes a real LLM call through the cllama proxy.
# The LLM is given a send_message tool and instructed to use it explicitly —
# plain text responses are private (thinking) and never reach Discord.
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
CLLAMA_MODEL="${ROLLCALL_CLLAMA_MODEL:-anthropic/claude-sonnet-4-6}"
REPLY_MODE="${ROLLCALL_REPLY_MODE:-tool_only}"
UA="DiscordBot (https://github.com/mostlydev/clawdapus, 1.0)"

[ -n "$TOKEN" ] && [ -n "$BOT_ID" ] && [ -n "$CHANNEL_ID" ] || {
  echo "[discord-responder] missing env vars (TOKEN=${TOKEN:+set} BOT_ID=$BOT_ID CHANNEL=$CHANNEL_ID), exiting" >&2
  exit 0
}

echo "[discord-responder] polling channel $CHANNEL_ID for mentions of $BOT_ID (runtime=$RUNTIME)" >&2
if [ -n "$CLLAMA" ]; then
  echo "[discord-responder] cllama token present — reply mode=$REPLY_MODE" >&2
else
  echo "[discord-responder] no cllama token — will use static response" >&2
fi

# Call cllama proxy with the send_message tool. The LLM decides whether to call
# it; if it only produces text (thinking), nothing is posted to Discord.
# Returns the message content from the tool call on stdout, or empty string.
call_cllama() {
  prompt="${1:-Introduce yourself.}"
  [ -n "$CLLAMA" ] || return 0

  case "$REPLY_MODE" in
    managed_text)
      SYSTEM_PROMPT="You are a bot named ${RUNTIME} running on the ${RUNTIME} runtime.

Reply with one sentence suitable for Discord.

When the user explicitly asks you to call a managed service tool, you MUST call that tool before you reply.
Do not invent tool results from prior knowledge."

      case "$CLLAMA_FORMAT" in
        anthropic)
          payload=$(printf '%s\n%s\n%s' \
            "{\"model\":\"$CLLAMA_MODEL\",\"max_tokens\":200," \
            "\"system\":$(printf '%s' "$SYSTEM_PROMPT" | jq -Rs '.')," \
            "\"messages\":[{\"role\":\"user\",\"content\":$(printf '%s' "$prompt" | jq -Rs '.')}]}")

          resp=$(curl -s --max-time 30 \
            -H "Authorization: Bearer $CLLAMA" \
            -H "Anthropic-Version: 2023-06-01" \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://cllama:8080/v1/messages" 2>/dev/null) || { echo ""; return 0; }

          echo "[discord-responder] anthropic raw response: $(printf '%s' "$resp" | head -c 400)" >&2

          content=$(printf '%s' "$resp" | jq -r '
            [.content[]? | select(.type == "text") | .text] | join(" ")
          ' 2>/dev/null | head -1) || content=""
          printf '%s' "$content"
          return 0
          ;;
        openai)
          payload=$(printf '%s\n%s\n%s\n%s' \
            "{\"model\":\"$CLLAMA_MODEL\",\"max_tokens\":200," \
            "\"messages\":[" \
            "{\"role\":\"system\",\"content\":$(printf '%s' "$SYSTEM_PROMPT" | jq -Rs '.')}," \
            "{\"role\":\"user\",\"content\":$(printf '%s' "$prompt" | jq -Rs '.')}]}")

          resp=$(curl -s --max-time 30 \
            -H "Authorization: Bearer $CLLAMA" \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://cllama:8080/v1/chat/completions" 2>/dev/null) || { echo ""; return 0; }

          echo "[discord-responder] openai raw response: $(printf '%s' "$resp" | head -c 400)" >&2

          content=$(printf '%s' "$resp" | jq -r '
            .choices[0].message.content // ""
          ' 2>/dev/null | head -1) || content=""
          printf '%s' "$content"
          return 0
          ;;
        *)
          echo "[discord-responder] unsupported CLLAMA format: $CLLAMA_FORMAT" >&2
          echo ""
          return 0
          ;;
      esac
      ;;
    *)
      SYSTEM_PROMPT="You are a bot named ${RUNTIME} running on the ${RUNTIME} runtime.

## CRITICAL: Tool-only mode

Plain text responses are private thinking. They are NEVER sent to Discord.

The ONLY way to communicate is by calling the send_message tool. When a message arrives, call send_message with one sentence stating your name and runtime. If you generate text without calling send_message, nobody sees it.

Example: call send_message with message=\"I am ${RUNTIME}, running on the ${RUNTIME} runtime.\""

      case "$CLLAMA_FORMAT" in
        anthropic)
          payload=$(printf '%s\n%s\n%s\n%s' \
            "{\"model\":\"$CLLAMA_MODEL\",\"max_tokens\":200," \
            "\"system\":$(printf '%s' "$SYSTEM_PROMPT" | jq -Rs '.')," \
            "\"tools\":[{\"name\":\"send_message\",\"description\":\"Post a message to Discord. This is the ONLY way to communicate. Call this to deliver your response.\",\"input_schema\":{\"type\":\"object\",\"properties\":{\"message\":{\"type\":\"string\"}},\"required\":[\"message\"]}}]," \
            "\"messages\":[{\"role\":\"user\",\"content\":$(printf '%s' "$prompt" | jq -Rs '.')}]}")

          resp=$(curl -s --max-time 30 \
            -H "Authorization: Bearer $CLLAMA" \
            -H "Anthropic-Version: 2023-06-01" \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://cllama:8080/v1/messages" 2>/dev/null) || { echo ""; return 0; }

          echo "[discord-responder] anthropic raw response: $(printf '%s' "$resp" | head -c 400)" >&2

          content=$(printf '%s' "$resp" | jq -r '
            .content[]?
            | select(.type == "tool_use" and .name == "send_message")
            | .input.message // ""
          ' 2>/dev/null | head -1) || content=""
          printf '%s' "$content"
          return 0
          ;;
        openai)
          payload=$(printf '%s\n%s\n%s\n%s\n%s' \
            "{\"model\":\"$CLLAMA_MODEL\",\"max_tokens\":200," \
            "\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"send_message\",\"description\":\"Post a message to Discord. This is the ONLY way to communicate. Call this to deliver your response.\",\"parameters\":{\"type\":\"object\",\"properties\":{\"message\":{\"type\":\"string\"}},\"required\":[\"message\"]}}}]," \
            "\"messages\":[" \
            "{\"role\":\"system\",\"content\":$(printf '%s' "$SYSTEM_PROMPT" | jq -Rs '.')}," \
            "{\"role\":\"user\",\"content\":$(printf '%s' "$prompt" | jq -Rs '.')}]}")

          resp=$(curl -s --max-time 30 \
            -H "Authorization: Bearer $CLLAMA" \
            -H "Content-Type: application/json" \
            -d "$payload" \
            "http://cllama:8080/v1/chat/completions" 2>/dev/null) || { echo ""; return 0; }

          echo "[discord-responder] openai raw response: $(printf '%s' "$resp" | head -c 400)" >&2

          content=$(printf '%s' "$resp" | jq -r '
            .choices[0].message.tool_calls[]?
            | select(.function.name == "send_message")
            | .function.arguments | fromjson | .message // ""
          ' 2>/dev/null | head -1) || content=""
          printf '%s' "$content"
          return 0
          ;;
        *)
          echo "[discord-responder] unsupported CLLAMA format: $CLLAMA_FORMAT" >&2
          echo ""
          return 0
          ;;
      esac
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
      --arg bid "$BOT_ID" \
      '[.[] | select(.author.id == $bid) | select(.content | ascii_downcase | test($rt | ascii_downcase))] | length' \
      2>/dev/null) || already=0

    if [ "$already" -eq 0 ]; then
      echo "[discord-responder] found trigger (attempt $i), calling LLM for $RUNTIME" >&2
      trigger_prompt=$(printf '%s' "$clean" | jq -r \
        --arg bid "$BOT_ID" \
        '[.[] | select(.author.id != $bid) | select(.content | test($bid))] | .[0].content // ""' \
        2>/dev/null) || trigger_prompt=""
      trigger_prompt=$(printf '%s' "$trigger_prompt" | sed "s#<@${BOT_ID}>##g; s#<@!${BOT_ID}>##g" | tr '\n' ' ' | sed 's/[[:space:]]\+/ /g; s/^ //; s/ $//')
      if [ -z "$trigger_prompt" ]; then
        trigger_prompt="Introduce yourself."
      fi
      echo "[discord-responder] trigger prompt: $trigger_prompt" >&2

      # LLM call: agent must call send_message tool to communicate.
      # If the LLM only generates text (private thinking), llm_response is empty
      # and nothing is posted — silence is correct behavior.
      llm_response=$(call_cllama "$trigger_prompt")
      if [ -n "$llm_response" ]; then
        message="$llm_response"
        echo "[discord-responder] LLM produced response via cllama proxy" >&2
      else
        message="I'm running on ${RUNTIME}. Stub runtime reporting for duty! (no cllama)"
        echo "[discord-responder] cllama unavailable or no tool call, using static fallback" >&2
      fi

      # Post the message to Discord.
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
