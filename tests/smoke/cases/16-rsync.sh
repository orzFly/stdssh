#!/usr/bin/env bash
# rsync over the stdssh ProxyCommand: push + pull roundtrip. rsync rides the
# exec channel (like scp) but speaks its own protocol over the spawned remote
# `rsync --server`, so it exercises a path sftp/scp don't.
set -euo pipefail
. /smoke/lib/common.sh

command -v rsync >/dev/null 2>&1 || { echo 'rsync SKIP (rsync not installed)'; exit 0; }

# rsync's -e/RSYNC_RSH is split on whitespace with no shell quoting, so the
# ProxyCommand value (which contains spaces) can't survive an inline -e string.
# Stage a wrapper that re-sources common.sh and execs ssh with the opts array
# intact; rsync then invokes it as `<wrapper> fake@fake rsync --server ...`.
rsh=$(mktemp)
tmpdir=$(mktemp -d)
trap 'rm -f "$rsh"; rm -rf "$tmpdir"' EXIT
cat > "$rsh" <<'RSH'
#!/usr/bin/env bash
. /smoke/lib/common.sh
exec ssh "${_SSH_BASE_OPTS[@]}" "$@"
RSH
chmod +x "$rsh"

# A tree with a nested file and a binary blob, to exercise recursion and verify
# byte-exact transfer (not just text).
mkdir -p "$tmpdir/src/sub"
echo 'rsync-payload' > "$tmpdir/src/file"
head -c 4096 /dev/urandom > "$tmpdir/src/sub/blob"

# push: local -> remote
rsync -e "$rsh" -a "$tmpdir/src/" fake@fake:"$tmpdir/dst/"
diff -r "$tmpdir/src" "$tmpdir/dst"

# pull: remote -> local
rsync -e "$rsh" -a fake@fake:"$tmpdir/dst/" "$tmpdir/back/"
diff -r "$tmpdir/dst" "$tmpdir/back"

echo 'rsync push + pull OK'
