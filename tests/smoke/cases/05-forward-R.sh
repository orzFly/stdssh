#!/usr/bin/env bash
# Phase 8 smoke: -R / tcpip-forward.
set -euo pipefail
. /smoke/lib/common.sh

CLIENT_PORT=19999   # listener on the "client side" (also this container)
REMOTE_PORT=29999   # ssh server (stdssh) listens here

cleanup() {
  [ -n "${SSH_PID:-}"   ] && kill "$SSH_PID"   2>/dev/null || true
  [ -n "${SOCAT_PID:-}" ] && kill "$SOCAT_PID" 2>/dev/null || true
}
trap cleanup EXIT

# client-side responder: any TCP connect to :$CLIENT_PORT gets "pong"
socat TCP-LISTEN:"$CLIENT_PORT",reuseaddr,bind=127.0.0.1,fork SYSTEM:'echo pong' \
  >/dev/null 2>&1 &
SOCAT_PID=$!
wait_for_port 127.0.0.1 "$CLIENT_PORT" 5 || { echo 'socat never came up' >&2; exit 1; }

ssh_cli -N -R "$REMOTE_PORT:127.0.0.1:$CLIENT_PORT" fake@fake &
SSH_PID=$!
wait_for_port 127.0.0.1 "$REMOTE_PORT" 5 || { echo '-R listener never came up' >&2; exit 1; }

reply=$(timeout 3 bash -c "exec 3<>/dev/tcp/127.0.0.1/$REMOTE_PORT; cat <&3")
echo "$reply" | tr -d '\r' | grep -qx pong

echo '-R forward OK'
