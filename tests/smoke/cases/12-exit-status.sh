#!/usr/bin/env bash
# Ported from OpenSSH regress/exit-status.sh. Verifies the remote exit code
# is faithfully propagated for a range of values, both for a normal
# foreground command and for one that closes stdout/stderr before exiting.
set -euo pipefail
. /smoke/lib/common.sh

for s in 0 1 4 5 44 255; do
  set +e
  ssh_cli fake@fake "exit $s"; rc=$?
  set -e
  [ "$rc" = "$s" ] || { echo "exit-status mismatch (foreground): got $rc, want $s" >&2; exit 1; }

  # Same status, but the remote closes stdout/stderr partway through and
  # sleeps a bit before exiting. The exit message must still reach us.
  set +e
  ssh_cli -n fake@fake "sleep 1; exec >/dev/null 2>&1; sleep 1; exit $s"; rc=$?
  set -e
  [ "$rc" = "$s" ] || { echo "exit-status mismatch (post-close): got $rc, want $s" >&2; exit 1; }
done

echo 'exit-status OK (matrix 0/1/4/5/44/255, with and without early-close)'
