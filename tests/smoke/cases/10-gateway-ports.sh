#!/usr/bin/env bash
# tcpip-forward bind address is forced to loopback by default (matches
# OpenSSH GatewayPorts=no), and --gateway-ports opens it back up to wildcard.
# Inspects /proc/net/tcp{,6} to see which IP the kernel actually bound.
set -euo pipefail
. /smoke/lib/common.sh

PORT=18080
PORT_HEX=$(printf '%04X' "$PORT")

# /proc/net/tcp{,6}: column 2 is local_address as "<hex-IP-LE>:<hex-port>",
# column 4 is the TCP state (0A == TCP_LISTEN). Go's net.Listen sometimes
# returns a v6 dual-stack socket for v4 wildcard literals, so we check both
# families and tag the result.
listening_addr() {
  local got
  got=$(awk -v p=":$PORT_HEX" '$4 == "0A" && index($2, p) {print "v4:" $2; exit}' /proc/net/tcp)
  if [ -z "$got" ] && [ -r /proc/net/tcp6 ]; then
    got=$(awk -v p=":$PORT_HEX" '$4 == "0A" && index($2, p) {print "v6:" $2; exit}' /proc/net/tcp6)
  fi
  printf '%s\n' "$got"
}

# Wait briefly for the kernel to release a port between scenarios.
wait_for_port_gone() {
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ -z "$(listening_addr)" ] && return 0
    sleep 0.1
  done
  return 1
}

cleanup() { [ -n "${SSH_PID:-}" ] && kill "$SSH_PID" 2>/dev/null || true; }
trap cleanup EXIT

# Scenario 1: default GatewayPorts=no. Client requests "0.0.0.0"; server
# must rewrite to 127.0.0.1 so the port isn't network-reachable.
ssh "${_SSH_BASE_OPTS[@]}" -N -R "0.0.0.0:$PORT:127.0.0.1:22" fake@fake &
SSH_PID=$!
wait_for_port 127.0.0.1 "$PORT" 5 || { echo 'default: listener never came up' >&2; exit 1; }

bind=$(listening_addr)
[ -n "$bind" ] || { echo 'default: no listening socket' >&2; exit 1; }
case "$bind" in
  v4:0100007F:*) ;;  # 127.0.0.1 — good
  *) echo "default: bound to $bind, want v4:0100007F:* (127.0.0.1)" >&2; exit 1 ;;
esac

kill "$SSH_PID" 2>/dev/null || true
wait "$SSH_PID" 2>/dev/null || true
SSH_PID=
wait_for_port_gone || { echo 'port not released between scenarios' >&2; exit 1; }

# Scenario 2: --gateway-ports server flag passes BindAddr through verbatim.
GW_OPTS=(
  -o BatchMode=yes
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o LogLevel=ERROR
  -o "ProxyCommand=/usr/local/bin/stdssh --hostkey-seed smoke-seed --log-level=warn --gateway-ports"
)
ssh "${GW_OPTS[@]}" -N -R "0.0.0.0:$PORT:127.0.0.1:22" fake@fake &
SSH_PID=$!
wait_for_port 127.0.0.1 "$PORT" 5 || { echo '--gateway-ports: listener never came up' >&2; exit 1; }

bind=$(listening_addr)
[ -n "$bind" ] || { echo '--gateway-ports: no listening socket' >&2; exit 1; }
case "$bind" in
  v4:00000000:*|v6:00000000000000000000000000000000:*) ;;  # wildcard — good
  *) echo "--gateway-ports: bound to $bind, want wildcard (0.0.0.0 or [::])" >&2; exit 1 ;;
esac

echo 'gateway-ports OK (default=loopback, --gateway-ports=wildcard)'
