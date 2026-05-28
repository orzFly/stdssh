#!/usr/bin/env bash
# Phase 6 smoke: sftp subsystem + scp (which rides exec).
set -euo pipefail
. /smoke/lib/common.sh

# sftp ls
out=$(sftp_cli -b - fake@fake <<'EOF'
ls /etc
EOF
)
echo "$out" | grep -q hostname || {
  echo 'sftp ls /etc did not include hostname' >&2
  printf '%s\n' "$out" >&2
  exit 1
}

# sftp put/get roundtrip
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
echo 'sftp-payload' > "$tmpdir/up"
sftp_cli -b - fake@fake <<EOF
put $tmpdir/up $tmpdir/down
EOF
diff -q "$tmpdir/up" "$tmpdir/down"

# scp (exec channel running scp -t)
scp_cli fake@fake:/etc/hostname "$tmpdir/host"
[ -s "$tmpdir/host" ]

echo 'sftp + scp OK'
