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

resolve_env_file() {
  local arg="$1"
  if [[ -f "$arg" ]]; then
    echo "$arg"
    return 0
  fi
  if [[ -f "${BOTS_DIR}/${arg}.env" ]]; then
    echo "${BOTS_DIR}/${arg}.env"
    return 0
  fi
  return 1
}

collect_targets() {
  if [[ $# -eq 0 ]]; then
    shopt -s nullglob
    local files=("${BOTS_DIR}"/*.env)
    shopt -u nullglob
    printf '%s\n' "${files[@]}"
    return 0
  fi

  local arg
  for arg in "$@"; do
    local env_file
    env_file="$(resolve_env_file "$arg")" || {
      echo "Cannot resolve OpenClaw bot env for: $arg" >&2
      exit 1
    }
    printf '%s\n' "$env_file"
  done
}

main() {
  mapfile -t env_files < <(collect_targets "$@")

  if [[ ${#env_files[@]} -eq 0 ]]; then
    echo "No OpenClaw bots to stop."
    exit 0
  fi

  local env_file
  for env_file in "${env_files[@]}"; do
    local bot_id
    bot_id="$(basename "${env_file}" .env)"
    local project
    project="openclaw-$(sanitize "${bot_id}")"

    echo "Stopping OpenClaw ${bot_id} (project=${project})"
    docker compose \
      --project-name "${project}" \
      --file "${COMPOSE_FILE}" \
      --env-file "${env_file}" \
      down
  done
}

main "$@"
