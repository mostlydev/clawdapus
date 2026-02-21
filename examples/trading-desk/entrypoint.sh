#!/bin/sh
set -e

# Run any image-level configuration scripts baked in during claw build.
if [ -x /claw/configure.sh ]; then
    /claw/configure.sh
fi

exec openclaw gateway --port 18789 --bind loopback
