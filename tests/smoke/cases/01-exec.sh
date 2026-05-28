#!/usr/bin/env bash
# Phase 4 smoke: exec channel — stdout, stdin, exit status.
set -euo pipefail
. /smoke/lib/common.sh

# stdout
out=$(ssh_cli fake@fake 'echo hello; uname -s')
echo "$out" | grep -qx hello
echo "$out" | grep -qx Linux

# stdin -> remote cat
echo 'roundtrip' | ssh_cli fake@fake 'cat' | grep -qx roundtrip

# exit status propagation
set +e
ssh_cli fake@fake 'exit 42'; rc=$?
set -e
[ "$rc" = 42 ] || { echo "expected exit 42, got $rc" >&2; exit 1; }

echo 'exec OK'
