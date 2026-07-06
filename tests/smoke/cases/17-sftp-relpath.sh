#!/usr/bin/env bash
# Phase 6 smoke: SFTP relative-path resolution matches OpenSSH.
#
# sshd chdir()s to the user's home before exec'ing sftp-server (session.c),
# so a bare relative path like ".bashrc" resolves against ~, not the server
# process's CWD. stdssh runs in a container whose CWD (WORKDIR) is typically
# not home, so the SFTP subsystem must set its working directory to home too.
# This case also pins the ".." behaviour: OpenSSH lets ".." walk the real
# filesystem up from home un-clamped (so "../<basename home>/file" lands back
# in home, and "../../file" escapes it) — pkg/sftp's path.Join reproduces that
# for non-symlinked homes.
set -euo pipefail
. /smoke/lib/common.sh

tmpdir=$(mktemp -d)
probe="$HOME/.sftp-relpath-probe"
subdir="$HOME/.sftp-relpath-sub"
subprobe="$subdir/.sftp-relpath-probe"
mkdir -p "$subdir"
printf 'home\n' > "$probe"
printf 'sub\n'  > "$subprobe"
trap 'rm -f "$probe" "$subprobe"; rmdir "$subdir" 2>/dev/null || true; rm -rf "$tmpdir"' EXIT

# 1. Bare relative path resolves to $HOME, not the server's CWD (/smoke, which
#    has no such file — so a successful fetch proves home-relative resolution).
scp_cli fake@fake:.sftp-relpath-probe "$tmpdir/a"
[ "$(cat "$tmpdir/a")" = home ]

# 2. Relative path into a subdirectory of $HOME.
scp_cli fake@fake:.sftp-relpath-sub/.sftp-relpath-probe "$tmpdir/b"
[ "$(cat "$tmpdir/b")" = sub ]

# 3. ".." walks up from $HOME and back in: ../<basename $HOME>/.sftp-relpath-probe
#    == $HOME/.sftp-relpath-probe (mirrors OpenSSH; uses $HOME so no username
#    is baked into the test).
home_base=$(basename "$HOME")
scp_cli "fake@fake:../$home_base/.sftp-relpath-probe" "$tmpdir/c"
[ "$(cat "$tmpdir/c")" = home ]

echo 'sftp relative-path resolution OK'
