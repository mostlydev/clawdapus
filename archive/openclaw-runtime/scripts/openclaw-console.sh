#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STACK_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
OPENCLAW_DIR="${STACK_DIR}/openclaw"
BOTS_DIR="${OPENCLAW_DIR}/bots"
COMPOSE_FILE="${OPENCLAW_DIR}/compose.yml"

sanitize() {
  local cleaned
  cleaned="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '-' | sed -E 's/^-+//; s/-+$//; s/-+/-/g')"
  if [[ -z "${cleaned}" ]]; then
    cleaned="bot"
  fi
  echo "${cleaned}"
}

usage() {
  echo "Usage: $(basename "$0") <bot-name-or-env-file> [--once]"
  echo
  echo "  default: stream live conversation/session output"
  echo "  --once : print a snapshot only (no follow)"
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage
  exit 1
fi

input="$1"
mode="${2:-}"

if [[ -f "${input}" ]]; then
  env_file="${input}"
elif [[ -f "${BOTS_DIR}/${input}.env" ]]; then
  env_file="${BOTS_DIR}/${input}.env"
else
  echo "Cannot resolve OpenClaw env file: ${input}" >&2
  exit 1
fi

follow=1
if [[ "${mode}" == "--once" ]]; then
  follow=0
elif [[ -n "${mode}" ]]; then
  usage
  exit 1
fi

bot_id="$(basename "${env_file}" .env)"
project="openclaw-$(sanitize "${bot_id}")"
tail_lines="${OPENCLAW_CONSOLE_TAIL_LINES:-40}"

docker compose \
  --project-name "${project}" \
  --file "${COMPOSE_FILE}" \
  --env-file "${env_file}" \
  exec -T \
  -e FOLLOW="${follow}" \
  -e TAIL_LINES="${tail_lines}" \
  openclaw bash <<'EOS'
set -euo pipefail

if [[ -f /state/openclaw/gateway.token ]]; then
  export OPENCLAW_GATEWAY_TOKEN="$(cat /state/openclaw/gateway.token)"
fi

echo "== health =="
openclaw health --json || true
echo

echo "== heartbeat:last =="
openclaw system heartbeat last --json || true
echo

echo "== sessions =="
openclaw sessions --json || true
echo

SESSION_STORE="/state/openclaw/agents/main/sessions/sessions.json"
SESSION_DIR="/state/openclaw/agents/main/sessions"
if [[ ! -f "${SESSION_STORE}" ]]; then
  echo "No session store found at ${SESSION_STORE}"
  exit 0
fi

SESSION_FILE="$(jq -r --arg dir "${SESSION_DIR}" '
  if has("agent:main:main") and (.["agent:main:main"].sessionId // "") != "" then
    (.["agent:main:main"].sessionFile // ($dir + "/" + .["agent:main:main"].sessionId + ".jsonl"))
  else
    to_entries
    | sort_by(.value.updatedAt)
    | if length == 0 then
        ""
      else
        (last.value.sessionFile // ($dir + "/" + (last.value.sessionId // "") + ".jsonl"))
      end
  end
' "${SESSION_STORE}" 2>/dev/null || true)"
if [[ -z "${SESSION_FILE}" || ! -f "${SESSION_FILE}" ]]; then
  echo "No session file found."
  exit 0
fi

echo "== conversation stream: ${SESSION_FILE} =="
echo

JQ_FILTER='
  select(.type=="message")
  | .timestamp as $ts
  | .message.role as $role
  | (
      if $role=="assistant" or $role=="user" then
        (.message.content | map(select(.type=="text") | .text) | join("\n"))
      elif $role=="toolResult" then
        ("toolResult " + (.message.toolName // "") + ": " + ((.message.content | map(select(.type=="text") | .text) | join(" ")) // ""))
      else
        ""
      end
    ) as $body
  | ($body | gsub("^\\s+|\\s+$"; "")) as $trim
  | select(($trim | length) > 0)
  | "---- \($ts) [\($role)] ----\n\($trim)\n"
'

if [[ "${FOLLOW:-1}" == "1" ]]; then
  tail -n "${TAIL_LINES:-120}" -F "${SESSION_FILE}" | jq --unbuffered -r "${JQ_FILTER}"
else
  tail -n "${TAIL_LINES:-120}" "${SESSION_FILE}" | jq -r "${JQ_FILTER}"
fi
EOS
