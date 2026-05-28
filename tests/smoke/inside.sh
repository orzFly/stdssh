#!/usr/bin/env bash
# Container entrypoint: iterate every cases/*.sh, report pass/fail, exit non-zero
# if any case failed.
set -uo pipefail

cd /smoke

# Distro provenance for the logs (helps when matrix output is interleaved).
if [ -r /etc/os-release ]; then
  . /etc/os-release
  printf '# host: %s %s (%s)\n' "${ID:-?}" "${VERSION_ID:-?}" "${PRETTY_NAME:-?}"
fi

fails=()
shopt -s nullglob
cases=(cases/*.sh)
if [ ${#cases[@]} -eq 0 ]; then
  echo 'no smoke cases found' >&2
  exit 1
fi

for case in "${cases[@]}"; do
  name=$(basename "$case" .sh)
  printf '\n=== %s ===\n' "$name"
  if bash "$case"; then
    printf 'PASS: %s\n' "$name"
  else
    rc=$?
    printf 'FAIL: %s (exit %d)\n' "$name" "$rc"
    fails+=("$name")
  fi
done

echo
if [ ${#fails[@]} -ne 0 ]; then
  printf '%d failure(s): %s\n' "${#fails[@]}" "${fails[*]}" >&2
  exit 1
fi
echo 'all smoke cases passed'
