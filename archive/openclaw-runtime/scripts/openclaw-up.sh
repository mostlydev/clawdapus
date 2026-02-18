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
    local all=("${BOTS_DIR}"/*.env)
    shopt -u nullglob
    local files=()
    for f in "${all[@]}"; do
      [[ "$(basename "$f")" == "example.env" ]] && continue
      files+=("$f")
    done
    if [[ ${#files[@]} -eq 0 ]]; then
      echo "No OpenClaw bot env files found in ${BOTS_DIR}" >&2
      echo "Copy openclaw/bots/example.env to openclaw/bots/<name>.env first." >&2
      exit 1
    fi
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

# Read a value from a .env file.  Returns the raw value (no quotes).
env_val() {
  local file="$1" key="$2" default="${3:-}"
  local val
  val="$(grep -E "^${key}=" "${file}" 2>/dev/null | tail -1 | cut -d= -f2-)" || true
  # Strip surrounding quotes
  val="${val#\"}" ; val="${val%\"}"
  val="${val#\'}" ; val="${val%\'}"
  if [[ -z "${val}" ]]; then
    echo "${default}"
  else
    echo "${val}"
  fi
}

# Generate openclaw.json on the host so it can be bind-mounted read-only.
# The bot cannot modify its own heartbeat frequency, model, or scheduling.
generate_config() {
  local env_file="$1" config_path="$2"

  local workspace="/workspace"
  local hb_every;  hb_every="$(env_val  "${env_file}" OPENCLAW_HEARTBEAT_EVERY   30m)"
  local hb_target; hb_target="$(env_val "${env_file}" OPENCLAW_HEARTBEAT_TARGET  none)"
  local hb_prompt; hb_prompt="$(env_val "${env_file}" OPENCLAW_HEARTBEAT_PROMPT  "")"
  local model;     model="$(env_val     "${env_file}" OPENCLAW_MODEL_PRIMARY     "")"

  mkdir -p "$(dirname "${config_path}")"

  local prompt_line=""
  if [[ -n "${hb_prompt}" ]]; then
    prompt_line="        prompt: \"${hb_prompt}\","
  fi
  local model_line=""
  if [[ -n "${model}" ]]; then
    model_line="      model: { primary: \"${model}\" },"
  fi

  cat > "${config_path}" <<EOF
{
  agents: {
    defaults: {
      workspace: "${workspace}",
      heartbeat: {
        every: "${hb_every}",
        target: "${hb_target}",
${prompt_line}
      },
${model_line}
    },
  },
  session: {
    dmScope: "main",
  },
}
EOF
  echo "Generated config: ${config_path}"
}

main() {
  mapfile -t env_files < <(collect_targets "$@")

  local env_file
  for env_file in "${env_files[@]}"; do
    local bot_id
    bot_id="$(basename "${env_file}" .env)"
    local slug
    slug="$(sanitize "${bot_id}")"
    local project
    project="openclaw-${slug}"
    local state_dir
    state_dir="${OPENCLAW_DIR}/runtime/${slug}"
    mkdir -p "${state_dir}"

    # Generate openclaw.json on the host — mounted read-only into the container.
    generate_config "${env_file}" "${state_dir}/openclaw/openclaw.json"

    echo "Starting OpenClaw ${bot_id} (project=${project})"
    BOT_STATE_PATH="${state_dir}" docker compose \
      --project-name "${project}" \
      --file "${COMPOSE_FILE}" \
      --env-file "${env_file}" \
      up -d --build

    ready=0
    for _ in $(seq 1 30); do
      if docker compose \
        --project-name "${project}" \
        --file "${COMPOSE_FILE}" \
        --env-file "${env_file}" \
        exec -T openclaw bash -lc 'if [[ -f /state/openclaw/gateway.token ]]; then export OPENCLAW_GATEWAY_TOKEN="$(cat /state/openclaw/gateway.token)"; fi; openclaw health --json >/dev/null 2>&1' >/dev/null 2>&1; then
        ready=1
        break
      fi
      sleep 1
    done

    if [[ "${ready}" -eq 1 ]]; then
      echo "OpenClaw ${bot_id} is healthy."
    else
      echo "OpenClaw ${bot_id} started but health check timed out (retry logs/cmd in a few seconds)." >&2
    fi
  done
}

main "$@"
