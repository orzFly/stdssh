#!/usr/bin/env bash
# Phase 7 smoke: -L / direct-tcpip.
set -euo pipefail
. /smoke/lib/common.sh

BACKEND_PORT=18080
LOCAL_PORT=28080

cleanup() {
  [ -n "${SSH_PID:-}" ]     && kill "$SSH_PID"     2>/dev/null || true
  [ -n "${BACKEND_PID:-}" ] && kill "$BACKEND_PID" 2>/dev/null || true
}
trap cleanup EXIT

python3 -m http.server "$BACKEND_PORT" --bind 127.0.0.1 >/dev/null 2>&1 &
BACKEND_PID=$!
wait_for_port 127.0.0.1 "$BACKEND_PORT" 5 || { echo 'backend never came up' >&2; exit 1; }

ssh_cli -N -L "$LOCAL_PORT:127.0.0.1:$BACKEND_PORT" fake@fake &
SSH_PID=$!
wait_for_port 127.0.0.1 "$LOCAL_PORT" 5 || { echo '-L listener never came up' >&2; exit 1; }

# fetch through the forward; backend returns a directory listing
curl -fsS "http://127.0.0.1:$LOCAL_PORT/" | grep -q 'Directory listing'

echo '-L forward OK'
