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
  logs -f
