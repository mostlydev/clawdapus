#!/bin/sh
set -e

# Load environment variables from .env file if present
if [ -f "${HERMES_HOME:-/root/.hermes}/.env" ]; then
    set -a
    . "${HERMES_HOME:-/root/.hermes}/.env"
    set +a
fi

if [ -z "${CLLAMA_CONSUMER_SESSION_EPOCH:-}" ]; then
    export CLLAMA_CONSUMER_SESSION_EPOCH="$(cat /proc/sys/kernel/random/uuid)"
fi

exec hermes gateway run
