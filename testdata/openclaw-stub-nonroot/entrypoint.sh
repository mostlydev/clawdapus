#!/bin/sh
# Non-root regression entrypoint. Before exec'ing the gateway, prove that the
# canonical openclaw home is reachable from the container's runtime USER.
#
# This mirrors what the real openclaw gateway does on startup: it walks into
# /root/.openclaw/config to read its config file, then creates state under
# ~/.openclaw/agents. The first canonical-home regression failed on the parent
# /root traversal; the follow-up failed because Docker left /root/.openclaw at
# 0755 root:root when mounting the nested config directory. We assert both the
# config read path and the first state write path, so either regression trips
# this stub before the gateway even starts.
set -e

uid="$(id -u)"
echo "openclaw-stub-nonroot: entrypoint running as uid=$uid" >&2

if [ "$uid" = "0" ]; then
  echo "openclaw-stub-nonroot: this fixture must run as a non-root user (uid != 0); refusing to give a false-pass" >&2
  exit 64
fi

for path in /root /root/.openclaw /root/.openclaw/config; do
  if ! stat "$path" >/dev/null 2>&1; then
    echo "openclaw-stub-nonroot: cannot stat $path as uid $uid (EACCES on parent traversal?). The /root tmpfs is missing or mounted at the wrong path." >&2
    exit 65
  fi
done

if ! cat /root/.openclaw/config/openclaw.json >/dev/null 2>&1; then
  echo "openclaw-stub-nonroot: cannot read /root/.openclaw/config/openclaw.json as uid $uid" >&2
  exit 66
fi

if ! mkdir -p /root/.openclaw/agents/bootstrap-probe >/dev/null 2>&1; then
  echo "openclaw-stub-nonroot: cannot create ~/.openclaw/agents as uid $uid (the ~/.openclaw state root is not writable)" >&2
  exit 67
fi

if [ -f /claw/configure.sh ]; then
  sh /claw/configure.sh
fi

exec openclaw gateway
