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

if [[ $# -ne 1 ]]; then
  echo "Usage: $(basename "$0") <bot-name-or-env-file>"
  exit 1
fi

input="$1"
if [[ -f "${input}" ]]; then
  env_file="${input}"
elif [[ -f "${BOTS_DIR}/${input}.env" ]]; then
  env_file="${BOTS_DIR}/${input}.env"
else
  echo "Cannot resolve OpenClaw env file: ${input}" >&2
  exit 1
fi

bot_id="$(basename "${env_file}" .env)"
project="openclaw-$(sanitize "${bot_id}")"

docker compose \
  --project-name "${project}" \
  --file "${COMPOSE_FILE}" \
  --env-file "${env_file}" \
  exec -T openclaw bash <<'EOS'
set -euo pipefail

if [[ -f /state/openclaw/gateway.token ]]; then
  export OPENCLAW_GATEWAY_TOKEN="$(cat /state/openclaw/gateway.token)"
fi

echo "== health =="
openclaw health --json || true
echo

echo "== live balance =="
if [[ -f /workspace/state/balance.json ]]; then
  cat /workspace/state/balance.json
else
  echo "missing: /workspace/state/balance.json"
fi
echo

SESSION_STORE="/state/openclaw/agents/main/sessions/sessions.json"
SESSION_DIR="/state/openclaw/agents/main/sessions"
if [[ ! -f "${SESSION_STORE}" ]]; then
  echo "No session store found."
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

echo "== latest assistant message =="
jq -s -r '
  map(
    select(.type=="message" and .message.role=="assistant")
    | .timestamp as $ts
    | .message.model as $model
    | (.message.content | map(select(.type=="text") | .text) | join("\n")) as $body
    | ($body | gsub("^\\s+|\\s+$"; "")) as $trim
    | select(($trim | length) > 0)
    | { ts: $ts, model: $model, body: $trim }
  )
  | if length == 0 then
      "No assistant text messages found in latest session."
    else
      .[-1] | "timestamp: \(.ts)\nmodel: \(.model)\n\n\(.body)\n"
    end
' "${SESSION_FILE}"
EOS
