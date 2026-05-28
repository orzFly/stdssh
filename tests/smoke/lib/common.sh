# Shared helpers sourced by every smoke case.
# Keeps the ProxyCommand and ssh-client options in one place.

PROXY_CMD="${PROXY_CMD:-/usr/local/bin/stdssh --hostkey-seed smoke-seed --log-level=warn}"

_SSH_BASE_OPTS=(
  -o BatchMode=yes
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o LogLevel=ERROR
  -o "ProxyCommand=$PROXY_CMD"
)

ssh_cli()  { ssh  "${_SSH_BASE_OPTS[@]}" "$@"; }
sftp_cli() { sftp "${_SSH_BASE_OPTS[@]}" "$@"; }
scp_cli()  { scp  "${_SSH_BASE_OPTS[@]}" "$@"; }

# wait_for_port host port [timeout_seconds]
#   spins until a TCP connect succeeds or the timeout elapses
wait_for_port() {
  local host="$1" port="$2" timeout="${3:-5}"
  local deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if (exec 3<>/dev/tcp/"$host"/"$port") 2>/dev/null; then
      exec 3>&- 3<&-
      return 0
    fi
    sleep 0.1
  done
  return 1
}
