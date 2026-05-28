#!/usr/bin/env bash
# Phase 11 smoke: parent-process env scrubbing.
#
# SSH_AUTH_SOCK and SSH_AGENT_PID in the stdssh parent environment must NOT
# leak through to the remote shell, otherwise a server running with the
# operator's agent in its env would silently expose that agent — even when
# the client did not request agent forwarding or --no-agent-forward is set.
set -euo pipefail
. /smoke/lib/common.sh

fake_sock=/tmp/fake-agent-leak.sock
fake_pid=999999

# Inject the parent-side values via the ProxyCommand's inherited env, then
# ask the remote shell what it sees. The remote must NOT see our fakes.
remote=$(
  SSH_AUTH_SOCK="$fake_sock" \
  SSH_AGENT_PID="$fake_pid" \
  ssh "${_SSH_BASE_OPTS[@]}" fake@fake \
    'printf "SOCK=%s\nPID=%s\n" "${SSH_AUTH_SOCK:-unset}" "${SSH_AGENT_PID:-unset}"'
)

remote_sock=$(printf '%s\n' "$remote" | sed -n 's/^SOCK=//p')
remote_pid=$(printf '%s\n' "$remote" | sed -n 's/^PID=//p')

if [ "$remote_sock" = "$fake_sock" ]; then
  echo "SSH_AUTH_SOCK leaked from parent: $remote_sock" >&2
  exit 1
fi
if [ "$remote_pid" = "$fake_pid" ]; then
  echo "SSH_AGENT_PID leaked from parent: $remote_pid" >&2
  exit 1
fi

echo "env-leak OK (remote SOCK=$remote_sock PID=$remote_pid)"
