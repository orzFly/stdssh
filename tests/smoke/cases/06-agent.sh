#!/usr/bin/env bash
# Phase 9 smoke: agent forwarding (SSH_AUTH_SOCK bridge).
set -euo pipefail
. /smoke/lib/common.sh

keyfile=/root/.ssh/id_smoke
[ -f "$keyfile" ] || ssh-keygen -t ed25519 -f "$keyfile" -N '' -q -C smoke

eval "$(ssh-agent -s)" >/dev/null
trap 'ssh-agent -k >/dev/null 2>&1 || true' EXIT
ssh-add "$keyfile" 2>/dev/null

# With -A: remote can list the forwarded key AND the remote SSH_AUTH_SOCK
# is a stdssh-allocated path, not the local agent's socket leaked through env.
# (We can't usefully assert "without -A → no agent", because the smoke runner
# and stdssh share a process tree, so the local SSH_AUTH_SOCK env naturally
# leaks into the remote shell.)
#
# Keep a generous timeout as a safety net so a regression in connection
# teardown can't hang CI, but the session should exit promptly.
out=$(timeout 8 ssh "${_SSH_BASE_OPTS[@]}" -A fake@fake \
  'printf "REMOTE_SOCK=%s\n" "$SSH_AUTH_SOCK"; ssh-add -l' 2>&1)

echo "$out" | grep -qi 'ed25519' || {
  echo 'agent forwarding did not list the key:' >&2
  printf '%s\n' "$out" >&2
  exit 1
}

remote_sock=$(printf '%s\n' "$out" | tr -d '\r' | sed -n 's/^REMOTE_SOCK=//p' | head -1)
[ -n "$remote_sock" ] || { echo 'remote sock unset under -A' >&2; exit 1; }
[ "$remote_sock" != "$SSH_AUTH_SOCK" ] || {
  echo "remote SSH_AUTH_SOCK should be stdssh-allocated, not the local agent's ($remote_sock)" >&2
  exit 1
}

echo "agent forwarding OK (remote sock=$remote_sock)"
