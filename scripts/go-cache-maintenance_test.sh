#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/go-cache-maintenance.sh"
TEST_ROOT="$(mktemp -d)"
FAKE_BIN="$TEST_ROOT/bin"
FAKE_CACHE="$TEST_ROOT/cache"
CLEAN_MARKER="$TEST_ROOT/cleaned"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

mkdir -p "$FAKE_BIN" "$FAKE_CACHE"

cat >"$FAKE_BIN/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-} ${2:-}" in
  "env GOCACHE")
    printf '%s\n' "$FAKE_CACHE"
    ;;
  "clean -cache")
    : >"$CLEAN_MARKER"
    ;;
  *)
    echo "unexpected fake go invocation: $*" >&2
    exit 1
    ;;
esac
EOF

cat >"$FAKE_BIN/du" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\t%s\n' "${FAKE_DU_SIZE_KIB}" "${2:-}"
EOF

chmod +x "$FAKE_BIN/go" "$FAKE_BIN/du"

run_maintenance() {
  PATH="$FAKE_BIN:/usr/bin:/bin" \
    FAKE_CACHE="$FAKE_CACHE" \
    CLEAN_MARKER="$CLEAN_MARKER" \
    FAKE_DU_SIZE_KIB="$1" \
    NIUMA_GO_CACHE_MAX_GB="${3:-6}" \
    bash "$SCRIPT" --check >/dev/null
}

# Exactly 6 decimal GB is still within the limit.
run_maintenance 5859375 6
[[ ! -e "$CLEAN_MARKER" ]]

# One KiB above 6 decimal GB triggers a clean.
run_maintenance 5859376 6
[[ -e "$CLEAN_MARKER" ]]

rm -f "$CLEAN_MARKER"

# The environment override changes the decision without editing the script.
run_maintenance 976564 1
[[ -e "$CLEAN_MARKER" ]]

echo "go-cache-maintenance tests ok"
