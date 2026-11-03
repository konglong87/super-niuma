#!/usr/bin/env bash
# Cross-platform release artifacts: one archive per GOOS/GOARCH plus SHA256SUMS.
#
# Every target is compiled by scripts/build.sh rather than by a `go build` line of
# our own, so there is exactly one place that injects Build Identity. That is the
# whole point: AUDIT-P1-30 asked for an install path, and an
# install path that injects the version differently from a local build is how you
# end up shipping binaries whose `--version` says `dev`.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

DIST_DIR="${DIST_DIR:-dist}"
TARGETS="${TARGETS:-darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64}"

usage() {
  cat <<'EOF'
Usage: scripts/release.sh [-h|--help]

Builds niuma for every target in TARGETS and writes one archive per
target, plus a SHA256SUMS manifest, into DIST_DIR.

Environment:
  VERSION   version to inject (default: git describe --tags --always --dirty)
  TARGETS   space-separated GOOS/GOARCH list (default: darwin/arm64 darwin/amd64
            linux/amd64 linux/arm64 windows/amd64)
  DIST_DIR  output directory, wiped on each run (default: dist)
EOF
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  "") ;;
  *)
    echo "release.sh: unknown argument: $1" >&2
    usage >&2
    exit 2
    ;;
esac

# Resolved once and exported. build.sh would otherwise re-run `git describe` per
# target, and a tag landing mid-run would leave the archives disagreeing about
# which version they contain.
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
export VERSION
REVISION="${REVISION:-$(git rev-parse HEAD 2>/dev/null || true)}"
export REVISION
if [ -z "${DIRTY+x}" ]; then
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if git diff-index --quiet HEAD -- 2>/dev/null; then
      DIRTY=false
    else
      DIRTY=true
    fi
  else
    DIRTY=""
  fi
fi
export DIRTY
# All target builds in one release must share one build time. build.sh owns the
# epoch-to-RFC3339 conversion; release.sh only freezes the input once.
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(date +%s)}"
export SOURCE_DATE_EPOCH
# Release builds should be small by default. Local development builds keep
# their normal symbols unless the caller opts into the same flags explicitly.
LDFLAGS="${LDFLAGS:--s -w}"
export LDFLAGS

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

# Do not ship the packaging host's user names, ACLs, or AppleDouble attributes.
package_tar() {
  case "$(tar --version)" in
    *bsdtar*)
      COPYFILE_DISABLE=1 tar --format=ustar --no-xattrs --no-acls --no-fflags \
        --uid 0 --gid 0 --uname root --gname root -czf "$1" "$2"
      ;;
    *"GNU tar"*)
      tar --format=ustar --owner=0 --group=0 --numeric-owner -czf "$1" "$2"
      ;;
    *)
      echo "release.sh: packaging requires bsdtar or GNU tar" >&2
      return 1
      ;;
  esac
}

# DIST_DIR is wiped below, and it is caller-supplied. Someone reading `DIST_DIR`
# as "where to put dist" could reasonably pass $HOME; refuse the values where
# that mistake is unrecoverable.
case "$DIST_DIR" in
  "" | "/" | "$HOME" | "$HOME/" | "$ROOT_DIR" | "$ROOT_DIR/")
    echo "release.sh: refusing to wipe DIST_DIR=$DIST_DIR" >&2
    exit 2
    ;;
esac

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

archives=()
for target in $TARGETS; do
  goos="${target%%/*}"
  goarch="${target##*/}"

  stage="niuma_${VERSION}_${goos}_${goarch}"
  binary="niuma"
  archive="$stage.tar.gz"
  if [ "$goos" = "windows" ]; then
    binary="niuma.exe"
    archive="$stage.zip"
  fi

  mkdir -p "$DIST_DIR/$stage"
  # CGO off: no package in this module imports "C", and a static binary is what
  # makes the archive usable on a host that does not have this checkout.
  # Defer cache maintenance until every target has been built. Cleaning a
  # shared cache in the middle of a multi-target release forces later targets
  # to compile the same dependency graph again.
  NIUMA_GO_CACHE_MAINTENANCE=0 CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    OUTPUT="$DIST_DIR/$stage/$binary" "$ROOT_DIR/scripts/build.sh" >/dev/null
  cp README.md "$DIST_DIR/$stage/README.md"
  cp LICENSE "$DIST_DIR/$stage/LICENSE"
  mkdir -p "$DIST_DIR/$stage/THIRD_PARTY_LICENSES/termenv"
  cp third_party/termenv/LICENSE "$DIST_DIR/$stage/THIRD_PARTY_LICENSES/termenv/LICENSE"

  (
    cd "$DIST_DIR"
    case "$archive" in
      *.zip) zip -Xqr "$archive" "$stage" ;;
      *) package_tar "$archive" "$stage" ;;
    esac
  )
  rm -rf "$DIST_DIR/$stage"

  archives+=("$archive")
  printf 'packaged %s/%s\n' "$DIST_DIR" "$archive"
done

if [ "${#archives[@]}" -eq 0 ]; then
  echo "release.sh: TARGETS is empty, nothing was built" >&2
  exit 2
fi

(cd "$DIST_DIR" && sha256 "${archives[@]}" >SHA256SUMS)
# One maintenance pass after the complete release is safe: no later target
# needs the cache, and local callers can still opt out with the usual env var.
if [[ "${NIUMA_GO_CACHE_MAINTENANCE:-1}" != "0" ]]; then
  bash "$ROOT_DIR/scripts/go-cache-maintenance.sh" --check
fi
printf 'wrote %s/SHA256SUMS (%s archives, version %s)\n' "$DIST_DIR" "${#archives[@]}" "$VERSION"
