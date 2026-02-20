#!/bin/sh
set -e

if [ -x /claw/configure.sh ]; then
    /claw/configure.sh
fi

if [ -f /etc/cron.d/claw ] && command -v cron >/dev/null 2>&1; then
    cron
fi

exec openclaw gateway --port 18789 --bind loopback
