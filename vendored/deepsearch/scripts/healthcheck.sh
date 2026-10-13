#!/bin/sh
set -eu

# The service listens on HTTP_ADDR, which may come from the environment or from
# a mounted .env file; fall back to the built-in default when neither is set.
addr="${HTTP_ADDR:-${APP_HTTP_ADDR:-}}"
if [ -z "${addr}" ] && [ -r /app/.env ]; then
    line="$(grep -m 1 '^HTTP_ADDR=' /app/.env || true)"
    addr="${line#HTTP_ADDR=}"
    addr="${addr%\"}"
    addr="${addr#\"}"
fi
addr="${addr:-0.0.0.0:8231}"

wget -q -O /dev/null "http://${addr}/healthz"
