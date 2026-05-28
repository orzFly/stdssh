#!/usr/bin/env bash
# Ported from OpenSSH regress/broken-pipe.sh. If the local consumer closes
# its stdin (here, `true` exits before reading anything), ssh's stdout write
# fails with SIGPIPE — but ssh must not surface that as a non-zero exit
# code, and the remote shell must clean up normally. Repeated runs catch
# any per-connection state leak.
set -euo pipefail
. /smoke/lib/common.sh

for i in 1 2 3 4; do
  set +e
  ssh_cli fake@fake "echo $i" 2>/dev/null | true; rc=$?
  set -e
  [ "$rc" = 0 ] || { echo "broken-pipe iter $i: rc=$rc, want 0" >&2; exit 1; }
done

echo 'broken-pipe OK (4 iterations)'
