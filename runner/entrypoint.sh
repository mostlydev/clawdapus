#!/usr/bin/env bash
set -u

BOT_NAME="${BOT_NAME:-bot}"
BOT_WORKDIR="${BOT_WORKDIR:-/workspace}"
BOT_COMMAND="${BOT_COMMAND:-}"
BOT_INTERVAL_SECONDS="${BOT_INTERVAL_SECONDS:-600}"
BOT_JITTER_SECONDS="${BOT_JITTER_SECONDS:-0}"
BOT_TIMEOUT_SECONDS="${BOT_TIMEOUT_SECONDS:-540}"
BOT_FAIL_DELAY_SECONDS="${BOT_FAIL_DELAY_SECONDS:-30}"
STATE_DIR="/state"
LOG_DIR="${STATE_DIR}/logs"
RUNS_LOG="${STATE_DIR}/runs.log"

mkdir -p "${LOG_DIR}"

now() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

log() {
  printf '[%s] [%s] %s\n' "$(now)" "${BOT_NAME}" "$*"
}

is_positive_int() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

if [[ ! -d "${BOT_WORKDIR}" ]]; then
  log "BOT_WORKDIR does not exist: ${BOT_WORKDIR}"
  exit 1
fi

if [[ -z "${BOT_COMMAND}" ]]; then
  log "BOT_COMMAND is empty. Set BOT_COMMAND in your bot env file."
  exit 1
fi

for value in "${BOT_INTERVAL_SECONDS}" "${BOT_JITTER_SECONDS}" "${BOT_TIMEOUT_SECONDS}" "${BOT_FAIL_DELAY_SECONDS}"; do
  if ! is_positive_int "${value}"; then
    log "Timing env vars must be non-negative integers."
    exit 1
  fi
done

cd "${BOT_WORKDIR}"

log "loop starting"
log "workdir=${BOT_WORKDIR} interval=${BOT_INTERVAL_SECONDS}s timeout=${BOT_TIMEOUT_SECONDS}s"

while true; do
  run_id="$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM"
  run_log="${LOG_DIR}/${run_id}.log"
  started_at="$(now)"

  if (( BOT_JITTER_SECONDS > 0 )); then
    jitter=$(( RANDOM % (BOT_JITTER_SECONDS + 1) ))
    log "jitter sleep ${jitter}s"
    sleep "${jitter}"
  fi

  log "run start id=${run_id}"

  set +e
  timeout "${BOT_TIMEOUT_SECONDS}" bash -lc "${BOT_COMMAND}" >"${run_log}" 2>&1
  status=$?
  set -e

  finished_at="$(now)"

  if [[ ${status} -eq 0 ]]; then
    log "run ok id=${run_id}"
  elif [[ ${status} -eq 124 ]]; then
    log "run timeout id=${run_id} timeout=${BOT_TIMEOUT_SECONDS}s"
  else
    log "run failed id=${run_id} status=${status}"
  fi

  printf '{"runId":"%s","startedAt":"%s","finishedAt":"%s","status":%d,"logFile":"%s"}\n' \
    "${run_id}" "${started_at}" "${finished_at}" "${status}" "${run_log}" >> "${RUNS_LOG}"

  if [[ ${status} -ne 0 && ${BOT_FAIL_DELAY_SECONDS} -gt 0 ]]; then
    sleep "${BOT_FAIL_DELAY_SECONDS}"
  fi

  sleep "${BOT_INTERVAL_SECONDS}"
done
