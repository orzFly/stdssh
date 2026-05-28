#!/usr/bin/env bash
# Phase 11 smoke: child processes are reaped when the SSH client disconnects.
#
# Before the strong-cleanup fix, the server sent a single SIGHUP to the
# direct child and walked away. A backgrounded grandchild (e.g. a shell job)
# would inherit init and outlive the connection. This case asserts the whole
# remote process tree is torn down when the ssh client dies.
set -euo pipefail
. /smoke/lib/common.sh

marker_parent=/tmp/cleanup-parent.$$
marker_child=/tmp/cleanup-child.$$
trap 'rm -f "$marker_parent" "$marker_child"' EXIT

# Launch a remote shell that records its own pid (the direct child) AND
# starts a backgrounded sleep grandchild, recording the grandchild's pid too.
# Then both wait, so disconnect is what tears them down.
#
# NOTE: invoke ssh directly via `exec` in a subshell, not through the
# ssh_cli helper. With `helper &`, $! is the bash subshell PID, not ssh's,
# so kill $! would leave the actual ssh client (and the remote tree) alive.
( exec ssh "${_SSH_BASE_OPTS[@]}" fake@fake "
  echo \$\$ > $marker_parent
  sleep 60 &
  echo \$! > $marker_child
  wait
" >/dev/null 2>&1 ) &
ssh_pid=$!

# Wait for both pid markers to appear.
deadline=$(( $(date +%s) + 5 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if [ -s "$marker_parent" ] && [ -s "$marker_child" ]; then
    break
  fi
  sleep 0.1
done
[ -s "$marker_parent" ] || { echo 'remote parent never recorded its pid' >&2; kill $ssh_pid 2>/dev/null || true; exit 1; }
[ -s "$marker_child" ]  || { echo 'remote grandchild never recorded its pid' >&2; kill $ssh_pid 2>/dev/null || true; exit 1; }

parent_pid=$(cat "$marker_parent")
child_pid=$(cat "$marker_child")
echo "remote tree: parent=$parent_pid grandchild=$child_pid"

# Sanity-check they're actually alive before we disconnect.
kill -0 "$parent_pid" 2>/dev/null || { echo "remote parent $parent_pid not alive pre-disconnect" >&2; exit 1; }
kill -0 "$child_pid"  2>/dev/null || { echo "remote grandchild $child_pid not alive pre-disconnect" >&2; exit 1; }

# Kill the ssh client. stdssh sees stdio EOF, the server-side context
# cancels, session cleanup SIGHUPs the process group, and (after the grace
# window) SIGKILLs anything still alive.
kill "$ssh_pid" 2>/dev/null || true
wait "$ssh_pid" 2>/dev/null || true

# Allow the SIGHUP→SIGKILL escalation to complete (grace + reap).
deadline=$(( $(date +%s) + 6 ))
parent_alive=1
child_alive=1
while [ "$(date +%s)" -lt "$deadline" ]; do
  kill -0 "$parent_pid" 2>/dev/null && parent_alive=1 || parent_alive=0
  kill -0 "$child_pid"  2>/dev/null && child_alive=1  || child_alive=0
  if [ "$parent_alive" = 0 ] && [ "$child_alive" = 0 ]; then
    break
  fi
  sleep 0.2
done

if [ "$parent_alive" != 0 ]; then
  echo "remote parent $parent_pid still alive after disconnect" >&2
  kill -KILL "$parent_pid" 2>/dev/null || true
  exit 1
fi
if [ "$child_alive" != 0 ]; then
  echo "remote grandchild $child_pid still alive after disconnect" >&2
  kill -KILL "$child_pid" 2>/dev/null || true
  exit 1
fi

echo "cleanup-on-disconnect OK"
