#!/usr/bin/env bash
# Phase 11 smoke: signal-killed processes are reported via SSH "exit-signal".
#
# Regression guard for a bug where signaled exits were marshalled as
# exit-status = uint32(-1) (= 4294967295) instead of as RFC 4254 §6.10
# exit-signal. The fix is most easily observed via ssh -v: the client
# logs "rtype exit-signal" for signaled remote deaths.
set -euo pipefail
. /smoke/lib/common.sh

# Normal exit: must still flow as exit-status, not exit-signal.
normal_log=$(ssh -v "${_SSH_BASE_OPTS[@]}" fake@fake 'exit 7' 2>&1 || true)
echo "$normal_log" | grep -q 'rtype exit-status'   || { echo 'no exit-status for normal exit' >&2; exit 1; }
echo "$normal_log" | grep -q 'rtype exit-signal'   && { echo 'spurious exit-signal for normal exit' >&2; exit 1; }

# Signal-killed exit: must produce exit-signal.
sig_log=$(ssh -v "${_SSH_BASE_OPTS[@]}" fake@fake 'kill -TERM $$' 2>&1 || true)
echo "$sig_log" | grep -q 'rtype exit-signal'      || { echo 'no exit-signal for signaled exit' >&2; exit 1; }

echo "exit-signal OK"
