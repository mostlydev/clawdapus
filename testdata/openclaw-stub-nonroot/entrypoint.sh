#!/bin/sh
# Non-root regression entrypoint. Before exec'ing the gateway, prove that the
# canonical openclaw home is reachable from the container's runtime USER.
#
# This mirrors what the real openclaw gateway does on startup: it walks into
# /root/.openclaw/config to read its config file. The v0.8.8 driver crashed
# here under USER node because /root was mode 0700 and only /root/.openclaw
# was tmpfs-overlaid. We assert each path component is statable and the config
# file is readable, so a future regression that re-introduces the same layout
# trips this stub before the gateway even starts.
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

if [ -f /claw/configure.sh ]; then
  sh /claw/configure.sh
fi

exec openclaw gateway
