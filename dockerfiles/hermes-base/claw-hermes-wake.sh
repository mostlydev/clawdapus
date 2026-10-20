#!/bin/bash
# claw-hermes-wake <invocation-id> <task> <timeout-seconds> <delivery-channel> <delivery-target>
#
# Runs one bounded, fresh-session Hermes turn for a claw-api scheduled wake.
#
# - The task is passed to Hermes on stdin via `hermes chat --query-file -`; it
#   is never interpolated into a shell command line or evaluated.
# - Each invocation is a new Hermes process with no --resume/--continue, so
#   Hermes allocates a fresh session id. A fresh cllama consumer session epoch
#   is minted per invocation for the same reason.
# - `chat -Q --oneshot` is the non-interactive single-query mode. It does NOT
#   bypass approvals (unlike `hermes -z`, which forces HERMES_YOLO_MODE=1);
#   dangerous commands follow config.yaml approvals.single_query_mode
#   (default deny).
# - The whole turn plus delivery is bounded by <timeout-seconds>.
# - Empty <delivery-channel> and <delivery-target> mean no explicit delivery:
#   the agent may still post through its own send_message tool. Otherwise the
#   final response is sent with `hermes send --to <channel>:<target>`, which
#   uses the platform bot token directly and does not need the gateway.
#
# Exit status: 64 usage error; otherwise nonzero when the turn fails, times
# out (124), or a requested delivery fails.
set -euo pipefail

if [ "$#" -ne 5 ]; then
    echo "usage: claw-hermes-wake <invocation-id> <task> <timeout-seconds> <delivery-channel> <delivery-target>" >&2
    exit 64
fi

invocation_id=$1
task=$2
timeout_seconds=$3
delivery_channel=$4
delivery_target=$5

if [ -z "$invocation_id" ]; then
    echo "claw-hermes-wake: invocation-id must not be empty" >&2
    exit 64
fi
if [ -z "${task//[[:space:]]/}" ]; then
    echo "claw-hermes-wake: task must not be empty" >&2
    exit 64
fi
case "$timeout_seconds" in
    ''|*[!0-9]*|0)
        echo "claw-hermes-wake: timeout-seconds must be a positive integer" >&2
        exit 64
        ;;
esac
if { [ -n "$delivery_channel" ] && [ -z "$delivery_target" ]; } ||
   { [ -z "$delivery_channel" ] && [ -n "$delivery_target" ]; }; then
    echo "claw-hermes-wake: delivery-channel and delivery-target must both be set or both be empty" >&2
    exit 64
fi
case "$delivery_channel" in
    *:*) echo "claw-hermes-wake: delivery-channel must be a platform name (e.g. discord)" >&2; exit 64 ;;
esac

deadline=$(( $(date +%s) + timeout_seconds ))
remaining() {
    local left=$(( deadline - $(date +%s) ))
    if [ "$left" -lt 1 ]; then left=1; fi
    echo "$left"
}

workdir=$(mktemp -d "${TMPDIR:-/tmp}/claw-hermes-wake.XXXXXX")
trap 'rm -rf "$workdir"' EXIT
response_file="$workdir/response.txt"

export CLAW_INVOCATION_ID="$invocation_id"
export CLLAMA_CONSUMER_SESSION_EPOCH="$(cat /proc/sys/kernel/random/uuid)"

echo "claw-hermes-wake: invocation=${invocation_id} timeout=${timeout_seconds}s" >&2

turn_status=0
printf '%s' "$task" | timeout --kill-after=10 "$(remaining)" \
    hermes chat --query-file - --oneshot -Q >"$response_file" || turn_status=$?

if [ "$turn_status" -ne 0 ]; then
    if [ "$turn_status" -eq 124 ] || [ "$turn_status" -eq 137 ]; then
        echo "claw-hermes-wake: invocation ${invocation_id} timed out after ${timeout_seconds}s" >&2
    else
        echo "claw-hermes-wake: invocation ${invocation_id} turn failed (exit ${turn_status})" >&2
    fi
    exit "$turn_status"
fi

cat "$response_file"

if [ -z "$delivery_channel" ]; then
    exit 0
fi

if [ -z "$(tr -d '[:space:]' <"$response_file")" ]; then
    echo "claw-hermes-wake: invocation ${invocation_id} produced no final text; nothing to deliver" >&2
    exit 0
fi

delivery_status=0
timeout --kill-after=5 "$(remaining)" \
    hermes send --to "${delivery_channel}:${delivery_target}" --file "$response_file" --quiet || delivery_status=$?
if [ "$delivery_status" -ne 0 ]; then
    echo "claw-hermes-wake: invocation ${invocation_id} delivery to ${delivery_channel}:${delivery_target} failed (exit ${delivery_status})" >&2
    exit "$delivery_status"
fi
