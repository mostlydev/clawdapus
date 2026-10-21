package openclaw

import "github.com/mostlydev/clawdapus/internal/driver"

const UpstreamVersion = "2026.10.5"

const baseImageTag = "openclaw:latest"

// baseImageDockerfile is a self-contained Dockerfile for the openclaw base image.
// It uses a heredoc to bake the entrypoint inline — no COPY needed.
const baseImageDockerfile = `FROM node:24.22.0-bookworm-slim@sha256:a5f4366e834695d5f363b920c104953a535d3a9864d1bdf74566a0ebe01daee5

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates cron curl git jq tini \
    build-essential python3 make g++ cmake \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsS https://registry.npmjs.org/openclaw/-/openclaw-2026.10.5.tgz -o /tmp/openclaw.tgz \
    && echo 'f6b0ae5ccc1ea9529920adca06d19c2f37accfd1baf85990df496fc2899186670d58370eb65068a9d2db3dd8ada0932e4cfe798cfa97a6dd400342fcb52cc51e  /tmp/openclaw.tgz' | sha512sum --check - \
    && npm install --global /tmp/openclaw.tgz \
    && rm /tmp/openclaw.tgz \
    && test "$(node -p 'require("/usr/local/lib/node_modules/openclaw/package.json").version')" = "2026.10.5"

LABEL claw.runner.upstream.version="2026.10.5"

RUN cat > /usr/local/bin/claw-openclaw-wake <<'CLAW_WAKE_EOF'
#!/bin/bash
set -euo pipefail
[ "$#" -eq 5 ] || exit 64
invocation_id=$1
task=$2
timeout_seconds=$3
channel=$4
target=$5
[ -n "$invocation_id" ] && [ -n "$task" ] || exit 64
case "$timeout_seconds" in
    ''|*[!0-9]*|0) exit 64 ;;
esac
[ "$timeout_seconds" -le 900 ] || exit 64
if { [ -n "$channel" ] && [ -z "$target" ]; } ||
   { [ -z "$channel" ] && [ -n "$target" ]; }; then
    exit 64
fi
umask 077
task_dir=$(mktemp -d "/tmp/claw-openclaw-wake.XXXXXX")
trap 'rm -rf "$task_dir"' EXIT
printf '%s' "$task" >"$task_dir/task.txt"
session_id=$(node -e 'console.log(require("node:crypto").randomUUID())')
set -- openclaw agent --agent main --session-id "$session_id" --message-file "$task_dir/task.txt" --timeout "$timeout_seconds" --json
if [ -n "$channel" ]; then
    set -- "$@" --deliver --reply-channel "$channel" --reply-to "$target"
fi
# Keep the task opaque and enforce a process deadline in addition to the
# native Gateway turn timeout. The scheduler allows a separate drain margin.
timeout --kill-after=10 "$timeout_seconds" "$@"
CLAW_WAKE_EOF
RUN chmod +x /usr/local/bin/claw-openclaw-wake

RUN mkdir -p /claw /usr/local/bin && cat > /usr/local/bin/openclaw-entrypoint.sh <<'ENTRYPOINT_EOF'
#!/bin/sh
set -e

# Run any image-level configuration scripts baked in during claw build.
if [ -x /claw/configure.sh ]; then
    /claw/configure.sh
fi

if [ -n "${CLAW_MEMORY_DIR:-}" ]; then
    mkdir -p "$CLAW_MEMORY_DIR"
    touch "$CLAW_MEMORY_DIR/MEMORY.md"
    ln -snf "$CLAW_MEMORY_DIR/MEMORY.md" /claw/MEMORY.md
    ln -snf "$CLAW_MEMORY_DIR/MEMORY.md" /claw/memory.md
fi

# Start gateway as a background process so we can run startup tasks before
# handing off. Using & + wait keeps tini (PID 1) in control of the process.
openclaw gateway --port 18789 --bind loopback &
GATEWAY_PID=$!

# Forward shutdown to the gateway and wait for it to release SQLite ownership.
# Without this, container recreation leaves a fresh owner heartbeat behind.
stop_gateway() {
    trap - TERM INT
    kill -TERM "$GATEWAY_PID" 2>/dev/null || true
    set +e
    wait "$GATEWAY_PID"
    exit "$?"
}
trap stop_gateway TERM INT

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
ENTRYPOINT_EOF
RUN chmod +x /usr/local/bin/openclaw-entrypoint.sh

USER root
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/openclaw-entrypoint.sh"]
`

// BaseImage implements driver.BaseImageProvider for the openclaw driver.
func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}

func (d *Driver) RunnerAlias() string {
	return "openclaw"
}

func (d *Driver) WakeHelper() string { return "/usr/local/bin/claw-openclaw-wake" }
func (d *Driver) InvocationTimeoutSeconds(rc *driver.ResolvedClaw) (int, error) {
	return InvocationTimeoutSeconds(rc)
}
