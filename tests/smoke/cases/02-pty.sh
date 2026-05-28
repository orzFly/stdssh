#!/usr/bin/env bash
# Phase 5 smoke: pty-req — remote sees a controlling terminal with sane size
# and TERM forwarded via pty-req payload.
set -euo pipefail
. /smoke/lib/common.sh

# Under -tt the remote stderr is merged into the pty (stdout). Use labeled
# lines so any tput / shell noise can be filtered out by sed.
# Setting TERM=xterm ensures the remote tput has a terminfo entry to read.
raw=$(TERM=xterm ssh_cli -tt fake@fake \
  'printf "TTY=%s\n"  "$(tty)";
   printf "COLS=%s\n" "$(tput cols)";
   printf "ROWS=%s\n" "$(tput lines)";
   printf "TERM=%s\n" "$TERM"' 2>/dev/null)

clean=$(printf '%s' "$raw" | tr -d '\r')
tty=$( printf '%s\n' "$clean" | sed -n 's/^TTY=//p'  | head -1)
cols=$(printf '%s\n' "$clean" | sed -n 's/^COLS=//p' | head -1)
rows=$(printf '%s\n' "$clean" | sed -n 's/^ROWS=//p' | head -1)
term=$(printf '%s\n' "$clean" | sed -n 's/^TERM=//p' | head -1)

case "$tty" in
  /dev/pts/*|/dev/ptmx) ;;
  *) echo "expected a pty, got tty='$tty'" >&2; exit 1 ;;
esac
case "$cols" in ''|*[!0-9]*) echo "non-numeric cols: '$cols'" >&2; exit 1 ;; esac
case "$rows" in ''|*[!0-9]*) echo "non-numeric rows: '$rows'" >&2; exit 1 ;; esac
[ "$cols" -gt 0 ] && [ "$rows" -gt 0 ]
[ "$term" = "xterm" ] || { echo "TERM forwarding broken: got '$term'" >&2; exit 1; }

echo "pty OK (tty=$tty, ${cols}x${rows}, TERM=$term)"
