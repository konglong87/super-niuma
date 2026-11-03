#!/usr/bin/env bash
# The single entry point for everything in scripts/ that can be checked without
# an API key or a companion checkout. This is what CI runs.
#
# It does NOT run the acceptance scenarios themselves — those need a real model
# and cost money, and are listed as api-key scripts in scripts/README.md. What it
# does is prove the scripts are alive: AUDIT-P0-19 found 69 scripts that nobody
# could tell were working or bit-rotted, because nothing ever executed them. A
# script that no longer parses, whose --help crashes, or that calls a sibling
# that has been renamed away, fails here.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPTS_DIR="$ROOT_DIR/scripts"
source "$SCRIPTS_DIR/lib/product-env.sh"

failures=0
checks=0

maintain_go_cache() {
  if [[ "${NIUMA_GO_CACHE_MAINTENANCE:-1}" == "0" ]]; then
    return 0
  fi
  if ! bash "$SCRIPTS_DIR/go-cache-maintenance.sh" --check; then
    echo "WARNING: Go cache maintenance failed" >&2
  fi
}
trap maintain_go_cache EXIT

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

pass() {
  checks=$((checks + 1))
}

echo "== bash -n (every script parses)"
while IFS= read -r script; do
  if bash -n "$script" 2>/dev/null; then
    pass
  else
    fail "$(basename "$script") does not parse:"
    bash -n "$script" || true
  fi
done < <(find "$SCRIPTS_DIR" -name '*.sh' -type f | sort)

# run_bounded runs a command under a wall-clock cap when `timeout` is available,
# so one script that waits on stdin cannot wedge the whole run.
run_bounded() {
  if command -v timeout >/dev/null 2>&1; then
    timeout -s KILL 30 "$@"
  else
    "$@"
  fi
}

echo "== --help exits 0 (argument parsing is intact)"
# A script with a --help case arm must honour it without touching the network,
# the filesystem, or a companion checkout. This is the cheapest possible proof
# that the script still runs at all.
while IFS= read -r script; do
  name="$(basename "$script")"
  # Skip self: this script has no --help of its own, and invoking it would recurse.
  [[ "$name" == "offline-acceptance.sh" ]] && continue
  grep -qE '^[[:space:]]*(-h\|)?--help\)' "$script" || continue
  if output="$(run_bounded bash "$script" --help 2>&1)"; then
    if [[ -z "$output" ]]; then
      fail "$name --help printed nothing"
    else
      pass
    fi
  else
    fail "$name --help exited $? (output: $(printf '%s' "$output" | head -3 | tr '\n' ' '))"
  fi
done < <(find "$SCRIPTS_DIR" -maxdepth 1 -name '*.sh' -type f | sort)

echo "== sibling script references resolve"
# Scripts chain into each other by path. A rename that misses a call site is
# invisible until someone runs the outer script months later.
while IFS= read -r script; do
  name="$(basename "$script")"
  while IFS= read -r referenced; do
    [[ -n "$referenced" ]] || continue
    if [[ -f "$SCRIPTS_DIR/$referenced" ]]; then
      pass
    else
      fail "$name references scripts/$referenced, which does not exist"
    fi
  done < <(grep -oE '\$(ROOT_DIR|SCRIPTS_DIR)"?/scripts/[A-Za-z0-9_.-]+\.(sh|mjs|py|go)' "$script" 2>/dev/null |
    sed -E 's|.*/scripts/||' | sort -u)
done < <(find "$SCRIPTS_DIR" -maxdepth 1 -name '*.sh' -type f | sort)

echo "== release gate refuses cleanly without its prerequisites"
# AUDIT-P0-19: this gate depends on agent-proving-ground reports that are APG
# *outputs*. It must say so, not die on a bare "no such file". Point it at a
# guaranteed-absent tree and check the guidance is there.
gate_output="$(NIUMA_APG_DIR=/nonexistent/apg NIUMA_UPSTREAM_DIR=/nonexistent/upstream \
  run_bounded bash "$SCRIPTS_DIR/agent-capability-full-release-acceptance.sh" 2>&1 || true)"
gate_status=0
NIUMA_APG_DIR=/nonexistent/apg NIUMA_UPSTREAM_DIR=/nonexistent/upstream \
  run_bounded bash "$SCRIPTS_DIR/agent-capability-full-release-acceptance.sh" >/dev/null 2>&1 || gate_status=$?
if [[ "$gate_status" -ne 2 ]]; then
  fail "release gate exited $gate_status without prerequisites, want 2"
else
  pass
fi
for expected in "preflight failed" "agent-proving-ground" "no artifacts were written"; do
  if printf '%s' "$gate_output" | grep -qF "$expected"; then
    pass
  else
    fail "release gate preflight output does not mention '$expected'"
  fi
done

echo "== no hardcoded home directories"
# Absolute paths under one developer's home are what made the side-by-side
# scripts look dead to everyone else (AUDIT-P0-19).
if hits="$(grep -rn '/Users/[a-z]' "$SCRIPTS_DIR" --include='*.sh' 2>/dev/null | grep -v 'offline-acceptance.sh:')"; then
  fail "hardcoded home directory paths remain:"
  printf '%s\n' "$hits" >&2
else
  pass
fi

echo "== release packaging and capture lookup regressions"
if output="$(node "$SCRIPTS_DIR/test-release-assets.mjs" 2>&1)"; then
  pass
else
  fail "release asset manifest regression:"
  printf '%s\n' "$output" >&2
fi
if output="$(node "$SCRIPTS_DIR/test-desktop-v2-windows-packaging.mjs" 2>&1)"; then
  pass
else
  fail "Windows desktop packaging regression:"
  printf '%s\n' "$output" >&2
fi
if output="$(python3 "$SCRIPTS_DIR/release_test.py" 2>&1)"; then
  pass
else
  fail "release archive regression:"
  printf '%s\n' "$output" >&2
fi
if output="$(PYTHONDONTWRITEBYTECODE=1 python3 "$SCRIPTS_DIR/open_source_snapshot_test.py" 2>&1)"; then
  pass
else
  fail "history-free source snapshot regression:"
  printf '%s\n' "$output" >&2
fi
# Deterministic clocks and mocked process tables only: this must never run the
# opt-in real-input safety driver or require macOS permissions in CI.
if output="$(PYTHONDONTWRITEBYTECODE=1 python3 "$SCRIPTS_DIR/computer_acceptance_safety_test.py" 2>&1)"; then
  pass
else
  fail "Computer Use safety harness regression:"
  printf '%s\n' "$output" >&2
fi
if output="$(node --test "$SCRIPTS_DIR/upstream-agent-lifecycle-capture.test.cjs" 2>&1)"; then
  pass
else
  fail "capture lookup regression:"
  printf '%s\n' "$output" >&2
fi

# Go cache maintenance is a developer-machine hygiene helper, not a product
# or release invariant. It remains available through its dedicated script and
# the best-effort EXIT hook above, but is intentionally not part of CI.

# Scenarios that run end to end with no API key and no companion checkout: each
# builds its own deterministic TUI harness. Verified to pass offline; the rest of
# scripts/ is classified in scripts/README.md.
#
# Deliberately excluded, with reasons:
#   tui-semantic-terminal-acceptance, tui-recap-terminal-acceptance
#     need macOS `screencapture` (a GUI capability CI does not have)
#   tui-render-budget-acceptance
#     currently fails its own "last=Bash" assertion — a real pre-existing defect,
#     not an environment problem; see docs/todo.md before adding it back
OFFLINE_SCENARIOS=(
  tui-display-order-acceptance.sh
  tui-ten-turn-acceptance.sh
  tui-tool-progress-acceptance.sh
  # Gates on the in-repo fixture transcripts, so it no longer depends on which
  # sessions happen to sit under ~/.niuma/projects. On a machine that has
  # recorded sessions it also replays them, but only as an advisory extra pass.
  tui-session-replay-acceptance.sh
)

if [[ "${1:-}" == "--static-only" ]]; then
  echo "== offline scenarios (skipped: --static-only)"
else
  echo "== offline scenarios (deterministic harness, no API key)"
  for scenario in "${OFFLINE_SCENARIOS[@]}"; do
    printf '   %s ... ' "$scenario"
    if output="$(env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
      bash "$SCRIPTS_DIR/$scenario" 2>&1)"; then
      echo "ok"
      pass
    else
      echo "FAILED"
      fail "$scenario exited non-zero:"
      printf '%s\n' "$output" | tail -20 >&2
    fi
  done
fi

echo
if [[ "$failures" -gt 0 ]]; then
  echo "offline acceptance FAILED: $failures failure(s), $checks check(s) passed" >&2
  exit 1
fi
echo "offline acceptance ok: $checks checks passed"
