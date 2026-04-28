#!/bin/sh
set -e

# Load environment variables from .env file if present
if [ -f "${HERMES_HOME:-/root/.hermes}/.env" ]; then
    set -a
    . "${HERMES_HOME:-/root/.hermes}/.env"
    set +a
fi

exec hermes gateway run
