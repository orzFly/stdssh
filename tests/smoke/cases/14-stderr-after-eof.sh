#!/usr/bin/env bash
# Ported from OpenSSH regress/stderr-after-eof.sh. The remote command
# closes stdout, sleeps, then writes a known payload to stderr. The
# extended-data channel must flush completely after stdout-eof — i.e. our
# session channel half-closes correctly without dropping stderr in flight.
set -euo pipefail
. /smoke/lib/common.sh

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
DATA="$tmpdir/in"
COPY="$tmpdir/err"

# ~6 KB of deterministic content (something `cmp` can verify exactly).
for i in 1 2 3 4 5 6 7 8; do
  printf 'line %d: the quick brown fox jumps over the lazy dog %s\n' \
    "$i" "$(printf 'X%.0s' $(seq 1 200))"
done > "$DATA"

# Remote command runs in the same container, so $DATA is the same path on
# both sides. After exec >/dev/null, stdout is closed on the channel; the
# server must keep the extended-data path open until cat finishes writing.
ssh_cli fake@fake "exec >/dev/null; sleep 1; cat '$DATA' 1>&2" 2> "$COPY"

cmp "$DATA" "$COPY" || { echo 'stderr payload differs from input' >&2; exit 1; }
echo "stderr-after-eof OK ($(wc -c < "$DATA") bytes via stderr after stdout EOF)"
