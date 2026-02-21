#!/bin/sh
set -e

if [ -x /claw/configure.sh ]; then
  /claw/configure.sh
fi

if [ -f /etc/cron.d/claw ] && command -v cron >/dev/null 2>&1; then
  cron
fi

PORT="${OPENCLAW_PORT:-18789}"
BIND="${OPENCLAW_BIND:-loopback}"

exec openclaw gateway --port "${PORT}" --bind "${BIND}"
