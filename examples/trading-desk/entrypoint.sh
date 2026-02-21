#!/bin/sh
set -e

# Run any image-level configuration scripts baked in during claw build.
if [ -x /claw/configure.sh ]; then
    /claw/configure.sh
fi

# Start gateway as a background process so we can run startup tasks before
# handing off. Using & + wait keeps tini (PID 1) in control of the process.
openclaw gateway --port 18789 --bind loopback &
GATEWAY_PID=$!

# If a startup greeting is configured, wait for the gateway to be healthy
# (Discord connected), then send the message.
if [ -n "${CLAW_GREETING_CHANNEL:-}" ] && [ -n "${CLAW_GREETING_MESSAGE:-}" ]; then
    # Poll until gateway is healthy (up to 60s)
    echo "[entrypoint] waiting for gateway health..."
    i=0
    while [ "$i" -lt 60 ]; do
        if openclaw health >/dev/null 2>&1; then
            echo "[entrypoint] gateway healthy after ${i}s"
            break
        fi
        sleep 1
        i=$((i + 1))
    done

    # Retry a few times in case Discord connection is still establishing
    sent=0
    j=0
    while [ "$j" -lt 5 ] && [ "$sent" -eq 0 ]; do
        echo "[entrypoint] sending greeting (attempt $((j + 1))): $CLAW_GREETING_MESSAGE"
        if openclaw message send \
            --channel discord \
            --target "channel:${CLAW_GREETING_CHANNEL}" \
            -m "${CLAW_GREETING_MESSAGE}"; then
            echo "[entrypoint] greeting sent"
            sent=1
        else
            echo "[entrypoint] send failed, retrying in 3s..."
            j=$((j + 1))
            sleep 3
        fi
    done
fi

# Hand off — wait for the gateway process to exit.
wait "$GATEWAY_PID"
