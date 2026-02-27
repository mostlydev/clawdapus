#!/bin/sh
set -e

send_greeting() {
    if [ -z "${DISCORD_BOT_TOKEN:-}" ] || [ -z "${CLAW_GREETING_CHANNEL:-}" ] || [ -z "${CLAW_GREETING_MESSAGE:-}" ]; then
        return 0
    fi

    payload="$(jq -n --arg content "${CLAW_GREETING_MESSAGE}" '{content: $content}')"
    i=0
    while [ "$i" -lt 5 ]; do
        if curl -fsS -X POST "https://discord.com/api/v10/channels/${CLAW_GREETING_CHANNEL}/messages" \
            -H "Authorization: Bot ${DISCORD_BOT_TOKEN}" \
            -H "Content-Type: application/json" \
            -d "$payload" >/dev/null; then
            echo "[microclaw-entrypoint] greeting sent"
            return 0
        fi
        i=$((i + 1))
        sleep 3
    done

    echo "[microclaw-entrypoint] warning: failed to send greeting after retries"
}

send_greeting
exec microclaw run
