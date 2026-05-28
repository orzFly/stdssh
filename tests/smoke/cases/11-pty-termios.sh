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

# script(1) is in util-linux on most distros; Alpine's BusyBox is missing it.
# Skip if absent — there's no portable way to fake a tty otherwise.
if ! command -v script >/dev/null 2>&1; then
  echo 'pty-termios SKIP (no script(1) available)'
  exit 0
fi

# script(1) only takes a single command string and runs it via /bin/sh,
# which loses our array's element boundaries (the ProxyCommand value
# contains spaces). Stage the real work in a wrapper that re-sources
# common.sh inside the pty so the "${_SSH_BASE_OPTS[@]}" expansion stays
# intact.
inner=$(mktemp)
trap 'rm -f "$inner"' EXIT
cat > "$inner" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
. /smoke/lib/common.sh
stty intr ^A erase ^G -icrnl
ssh "${_SSH_BASE_OPTS[@]}" -tt fake@fake 'stty -a; exit'
INNER
chmod +x "$inner"

out=$(script -qfc "$inner" /dev/null 2>&1 | tr -d '\r')

intr=$(printf '%s\n'  "$out" | sed -n 's/.*intr = \([^;]*\);.*/\1/p'  | head -1)
erase=$(printf '%s\n' "$out" | sed -n 's/.*erase = \([^;]*\);.*/\1/p' | head -1)
icrnl=$(printf '%s\n' "$out" | grep -o '\-\?icrnl' | head -1)

if [ "$intr" != '^A' ] || [ "$erase" != '^G' ] || [ "$icrnl" != '-icrnl' ]; then
  echo "--- script output (debug) ---" >&2
  printf '%s\n' "$out" >&2
  echo "--- end ---" >&2
  [ "$intr" = '^A' ]      || { echo "remote intr  = '$intr', want '^A'  (modelist not applied)" >&2; exit 1; }
  [ "$erase" = '^G' ]     || { echo "remote erase = '$erase', want '^G' (modelist not applied)" >&2; exit 1; }
  [ "$icrnl" = '-icrnl' ] || { echo "remote icrnl = '$icrnl', want '-icrnl'" >&2; exit 1; }
fi

echo 'pty-termios OK (intr=^A erase=^G -icrnl propagated)'
