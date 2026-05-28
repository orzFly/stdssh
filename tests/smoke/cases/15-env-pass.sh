#!/usr/bin/env bash
# Positive companion to 07-env-leak: a non-blocklisted env var sent via
# SetEnv (which the client forwards as "env" channel-requests) must arrive
# at the remote shell unchanged. Mirrors the accept-path checks of OpenSSH
# regress/envpass.sh, scaled down to what stdssh's blocklist policy
# guarantees (we have no AcceptEnv pattern matching — any non-managed name
# passes through).
set -euo pipefail
. /smoke/lib/common.sh

remote=$(
  ssh "${_SSH_BASE_OPTS[@]}" \
    -o SetEnv="_XXX_TEST_A=1 _XXX_TEST_B=hello-world" \
    fake@fake \
    'printf "A=%s\nB=%s\n" "${_XXX_TEST_A:-unset}" "${_XXX_TEST_B:-unset}"'
)

a=$(printf '%s\n' "$remote" | sed -n 's/^A=//p')
b=$(printf '%s\n' "$remote" | sed -n 's/^B=//p')

[ "$a" = '1' ]              || { echo "_XXX_TEST_A = '$a', want '1'" >&2; exit 1; }
[ "$b" = 'hello-world' ]    || { echo "_XXX_TEST_B = '$b', want 'hello-world'" >&2; exit 1; }

# And SHELL must still be the server-resolved one, not whatever the client
# might try to forge. (Defense-in-depth: SHELL is in the env blocklist.)
shell=$(ssh "${_SSH_BASE_OPTS[@]}" -o SetEnv="SHELL=/tmp/forged-shell" fake@fake 'printf "%s" "$SHELL"')
case "$shell" in
  /tmp/forged-shell)
    echo "client forged SHELL=$shell — env blocklist failed" >&2
    exit 1
    ;;
esac

echo "env-pass OK (client SetEnv propagates; SHELL stayed '$shell')"
