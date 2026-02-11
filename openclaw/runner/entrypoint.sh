#!/usr/bin/env bash
set -euo pipefail

OPENCLAW_STATE_DIR="${OPENCLAW_STATE_DIR:-/state/openclaw}"
OPENCLAW_CONFIG_PATH="${OPENCLAW_CONFIG_PATH:-/state/openclaw/openclaw.json}"
OPENCLAW_WORKSPACE_DIR="${OPENCLAW_WORKSPACE_DIR:-/workspace}"
OPENCLAW_AUTO_SETUP="${OPENCLAW_AUTO_SETUP:-1}"
OPENCLAW_HEARTBEAT_EVERY="${OPENCLAW_HEARTBEAT_EVERY:-30m}"
OPENCLAW_HEARTBEAT_TARGET="${OPENCLAW_HEARTBEAT_TARGET:-none}"
OPENCLAW_HEARTBEAT_PROMPT="${OPENCLAW_HEARTBEAT_PROMPT:-}"
OPENCLAW_MODEL_PRIMARY="${OPENCLAW_MODEL_PRIMARY:-}"
OPENCLAW_GATEWAY_PORT="${OPENCLAW_GATEWAY_PORT:-18789}"
OPENCLAW_GATEWAY_BIND_MODE="${OPENCLAW_GATEWAY_BIND_MODE:-loopback}"
OPENCLAW_GATEWAY_AUTH_TOKEN="${OPENCLAW_GATEWAY_AUTH_TOKEN:-}"
OPENCLAW_GATEWAY_TOKEN_FILE="${OPENCLAW_STATE_DIR}/gateway.token"

mkdir -p "${OPENCLAW_STATE_DIR}" "${OPENCLAW_WORKSPACE_DIR}"

# Config is mounted read-only by Docker — skip generation and config set.
# openclaw-up.sh generates openclaw.json on the host before starting the
# container, and compose.yml mounts it at OPENCLAW_CONFIG_PATH as read-only.

if [[ "${OPENCLAW_AUTO_SETUP}" == "1" || "${OPENCLAW_AUTO_SETUP}" == "true" ]]; then
  # Seed workspace bootstrap files (AGENTS/SOUL/HEARTBEAT/memory) if missing.
  openclaw setup --non-interactive --workspace "${OPENCLAW_WORKSPACE_DIR}" >/dev/null 2>&1 || true
fi

if [[ -z "${OPENCLAW_GATEWAY_AUTH_TOKEN}" ]]; then
  if [[ -f "${OPENCLAW_GATEWAY_TOKEN_FILE}" ]]; then
    OPENCLAW_GATEWAY_AUTH_TOKEN="$(cat "${OPENCLAW_GATEWAY_TOKEN_FILE}")"
  else
    OPENCLAW_GATEWAY_AUTH_TOKEN="$(cat /proc/sys/kernel/random/uuid)"
    printf '%s\n' "${OPENCLAW_GATEWAY_AUTH_TOKEN}" > "${OPENCLAW_GATEWAY_TOKEN_FILE}"
    chmod 600 "${OPENCLAW_GATEWAY_TOKEN_FILE}"
  fi
fi

# Export gateway token so cron jobs and openclaw CLI can authenticate.
export OPENCLAW_GATEWAY_TOKEN="${OPENCLAW_GATEWAY_AUTH_TOKEN}"

# Dump env to a sourceable profile so cron jobs inherit all vars.
node -e "
const fs = require('fs');
const lines = Object.entries(process.env)
  .map(([k, v]) => 'export ' + k + '=' + JSON.stringify(v))
  .join('\n');
fs.writeFileSync('/etc/bot-env.sh', lines + '\n');
"
chmod 600 /etc/bot-env.sh

# ── System heartbeat override cron ───────────────────────────────────
# Fires heartbeats at the operator-configured frequency via the gateway
# wake RPC.  Lives in /etc/cron.d/ — the bot cannot disable or modify it.
heartbeat_cron=""
if [[ "${OPENCLAW_HEARTBEAT_EVERY}" =~ ^([0-9]+)m$ ]]; then
  m="${BASH_REMATCH[1]}"
  if (( m > 0 && m <= 59 )); then
    heartbeat_cron="*/${m} * * * *"
  fi
elif [[ "${OPENCLAW_HEARTBEAT_EVERY}" =~ ^([0-9]+)h$ ]]; then
  h="${BASH_REMATCH[1]}"
  if (( h > 0 )); then
    heartbeat_cron="0 */${h} * * *"
  fi
fi

if [[ -n "${heartbeat_cron}" ]]; then
  mkdir -p /var/log
  cat > /etc/cron.d/heartbeat-override <<CRON
${heartbeat_cron} root . /etc/bot-env.sh && openclaw gateway call wake --params '{"mode":"now","text":"system heartbeat"}' >> /var/log/heartbeat-override.log 2>&1
CRON
  chmod 644 /etc/cron.d/heartbeat-override
  echo "Installed system heartbeat override: ${OPENCLAW_HEARTBEAT_EVERY} (${heartbeat_cron})"
fi

# Install workspace crontab if present (bot-managed, bot-editable).
if [[ -f "${OPENCLAW_WORKSPACE_DIR}/crontab" ]]; then
  crontab "${OPENCLAW_WORKSPACE_DIR}/crontab"
  echo "Installed crontab from ${OPENCLAW_WORKSPACE_DIR}/crontab"
fi

# Start cron daemon in background.
cron

cmd=(openclaw gateway --allow-unconfigured --port "${OPENCLAW_GATEWAY_PORT}" --bind "${OPENCLAW_GATEWAY_BIND_MODE}" --auth token --token "${OPENCLAW_GATEWAY_AUTH_TOKEN}")

exec "${cmd[@]}"
