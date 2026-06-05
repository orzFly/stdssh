#!/usr/bin/env bash
# Host entrypoint: build the smoke image for each distro and run all cases inside.
#
# Usage:
#   tests/smoke/run.sh                              # every distro in DEFAULT_DISTROS
#   DISTRO=alpine tests/smoke/run.sh                # single distro
#   DISTROS='debian alpine' tests/smoke/run.sh      # subset
#   ENGINE=docker tests/smoke/run.sh                # force engine (default: podman > docker)
#   IMAGE_PREFIX=foo tests/smoke/run.sh             # override tag prefix
#   SKIP_PACKAGE_BUILD=1 tests/smoke/run.sh         # reuse an existing dist/ (CI builds it upstream)
set -euo pipefail

cd "$(dirname "$0")/../.."  # repo root

DEFAULT_DISTROS='debian ubuntu alpine arch fedora'

ENGINE="${ENGINE:-}"
if [ -z "$ENGINE" ]; then
  if command -v podman >/dev/null 2>&1; then
    ENGINE=podman
  elif command -v docker >/dev/null 2>&1; then
    ENGINE=docker
  else
    echo 'Neither podman nor docker found in PATH' >&2
    exit 1
  fi
fi

if [ -n "${DISTRO:-}" ]; then
  DISTROS="$DISTRO"
fi
DISTROS="${DISTROS:-$DEFAULT_DISTROS}"

IMAGE_PREFIX="${IMAGE_PREFIX:-stdssh-smoke}"

# The runtime images install stdssh from the GoReleaser packages in dist/. Build
# them with a snapshot release unless dist/ already holds them (e.g. CI ran
# goreleaser upstream and set SKIP_PACKAGE_BUILD=1).
if [ "${SKIP_PACKAGE_BUILD:-0}" != 1 ]; then
  if ! command -v goreleaser >/dev/null 2>&1; then
    echo 'goreleaser not found in PATH (needed to build the dist/ packages).' >&2
    echo 'Install it, or set SKIP_PACKAGE_BUILD=1 after producing dist/ yourself.' >&2
    exit 1
  fi
  echo '## building release packages (goreleaser --snapshot)'
  goreleaser release --snapshot --clean
fi
if ! ls dist/stdssh_*_linux_*.deb >/dev/null 2>&1; then
  echo 'dist/ has no stdssh packages; run goreleaser first or unset SKIP_PACKAGE_BUILD.' >&2
  exit 1
fi

fails=()
for d in $DISTROS; do
  echo
  echo "##################################################"
  echo "## distro: $d"
  echo "##################################################"
  tag="${IMAGE_PREFIX}:${d}"
  if ! "$ENGINE" build --build-arg "DISTRO=$d" -f tests/smoke/Dockerfile -t "$tag" .; then
    echo "## BUILD FAILED: $d" >&2
    fails+=("$d:build")
    continue
  fi
  if ! "$ENGINE" run --rm "$tag"; then
    echo "## RUN FAILED: $d" >&2
    fails+=("$d:run")
  fi
done

echo
if [ ${#fails[@]} -ne 0 ]; then
  printf '%d failure(s): %s\n' "${#fails[@]}" "${fails[*]}" >&2
  exit 1
fi
echo "all distros passed: $DISTROS"
