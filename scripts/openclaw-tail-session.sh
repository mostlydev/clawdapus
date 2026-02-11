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
  echo "Usage: $(basename "$0") <bot-name-or-env-file> [--with-tools]"
  echo
  echo "Streams only new session entries from now onward."
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage
  exit 1
fi

input="$1"
mode="${2:-}"
with_tools=0
if [[ "${mode}" == "--with-tools" ]]; then
  with_tools=1
elif [[ -n "${mode}" ]]; then
  usage
  exit 1
fi

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
  exec -T \
  -e WITH_TOOLS="${with_tools}" \
  openclaw bash <<'EOS'
set -euo pipefail

SESSION_STORE="/state/openclaw/agents/main/sessions/sessions.json"
SESSION_DIR="/state/openclaw/agents/main/sessions"
if [[ ! -f "${SESSION_STORE}" ]]; then
  echo "No session store found at ${SESSION_STORE}" >&2
  exit 1
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
  echo "No active session file found." >&2
  exit 1
fi

# Kill stale tail -F processes on session JSONL files from previous invocations.
# docker exec orphans these when the host-side script is interrupted.
for f in /proc/[0-9]*/cmdline; do
  pid="${f#/proc/}"; pid="${pid%%/*}"
  cmd="$(tr '\0' ' ' < "$f" 2>/dev/null)" || continue
  [[ "$cmd" == *tail*-F*.jsonl* ]] && kill "$pid" 2>/dev/null || true
done

echo "Tailing session: ${SESSION_FILE}"

if [[ "${WITH_TOOLS:-0}" == "1" ]]; then
  JQ_FILTER='
    select(.type=="message")
    | .timestamp as $ts
    | .message.role as $role
    | (
      if $role=="assistant" then
        (.message.content | map(select(.type=="text") | .text) | join("\n"))
      elif $role=="user" then
        (.message.content | map(select(.type=="text") | .text) | join("\n"))
      elif $role=="toolResult" then
        ("toolResult " + (.message.toolName // "") + ": " + ((.message.content | map(select(.type=="text") | .text) | join(" ")) // ""))
      else
        ""
      end
    ) as $body
    | ($body | gsub("^\\s+|\\s+$"; "")) as $trim
    | select(($trim | length) > 0)
    | if $role=="assistant" then
        "---- \($ts) [assistant model=\(.message.model // "unknown")] ----\n\($trim)\n"
      else
        "---- \($ts) [\($role)] ----\n\($trim)\n"
      end
  '
else
  JQ_FILTER='
    select(.type=="message" and (.message.role=="assistant" or .message.role=="user"))
    | .timestamp as $ts
    | .message.role as $role
    | (.message.content | map(select(.type=="text") | .text) | join("\n")) as $body
    | ($body | gsub("^\\s+|\\s+$"; "")) as $trim
    | select(($trim | length) > 0)
    | if $role=="assistant" then
        "---- \($ts) [assistant model=\(.message.model // "unknown")] ----\n\($trim)\n"
      else
        "---- \($ts) [user] ----\n\($trim)\n"
      end
  '
fi

# Start from now to avoid replaying stale history.
tail -n 0 -F "${SESSION_FILE}" | jq --unbuffered -r "${JQ_FILTER}"
EOS
