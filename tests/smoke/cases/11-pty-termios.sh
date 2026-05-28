#!/usr/bin/env bash
# PTY mode-list (RFC 4254 §8) must be applied to the slave termios before the
# child shell runs. Pick a few non-default settings on the local side via
# `script` (which gives us a real pty so ssh can read termios), then assert
# the remote stty -a reports the same values.
#
# Pre-modelist-fix this case fails: remote intr/erase come from the kernel
# defaults regardless of the client's negotiated values.
set -euo pipefail
. /smoke/lib/common.sh

# script(1) is in util-linux on most distros; bsdmainutils on some Debian
# variants. Skip if absent — there's no portable way to fake a tty otherwise.
if ! command -v script >/dev/null 2>&1; then
  echo 'pty-termios SKIP (no script(1) available)'
  exit 0
fi

out=$(
  script -qfc "
    stty intr ^A erase ^G -icrnl
    ssh ${_SSH_BASE_OPTS[*]} -tt fake@fake 'stty -a; exit'
  " /dev/null 2>&1 | tr -d '\r'
)

# Extract the first stty-a output: the lines start with 'speed' and
# 'intr =' so we match those for robustness against shell prologue noise.
intr=$(printf '%s\n' "$out" | sed -n 's/.*intr = \([^;]*\);.*/\1/p'  | head -1)
erase=$(printf '%s\n' "$out" | sed -n 's/.*erase = \([^;]*\);.*/\1/p' | head -1)
icrnl=$(printf '%s\n' "$out" | grep -o '\-\?icrnl' | head -1)

[ "$intr" = '^A' ]    || { echo "remote intr = '$intr', want '^A' (modelist not applied)" >&2; exit 1; }
[ "$erase" = '^G' ]   || { echo "remote erase = '$erase', want '^G'" >&2; exit 1; }
[ "$icrnl" = '-icrnl' ] || { echo "remote icrnl = '$icrnl', want '-icrnl'" >&2; exit 1; }

echo 'pty-termios OK (intr=^A erase=^G -icrnl propagated)'
