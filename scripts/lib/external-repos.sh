#!/usr/bin/env bash
# Resolves the two companion checkouts the side-by-side and release-gate scripts
# depend on. Both used to be hardcoded as absolute paths under one developer's
# home directory, which made every such script unrunnable for anyone else and
# indistinguishable from a dead script (AUDIT-P0-19).
#
#   claude_code_src_2026   original Claude Code source, used as the A/B baseline
#   agent-proving-ground   the agent test harness whose reports/ the release
#                          gate consumes
#
# Default: a sibling of this repository's main checkout. Override with
# NIUMA_UPSTREAM_DIR / NIUMA_APG_DIR, or NIUMA_COMPANION_ROOT to
# move both at once.

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/product-env.sh"

# companion_root prints the directory the companion checkouts live in.
# Resolved from the main checkout, so this works from a git worktree too.
companion_root() {
  local override="${NIUMA_COMPANION_ROOT:-}}"
  if [[ -n "$override" ]]; then
    printf '%s\n' "$override"
    return
  fi
  local repo_root="${1:-$PWD}" common_dir main_checkout
  if common_dir="$(git -C "$repo_root" rev-parse --git-common-dir 2>/dev/null)"; then
    # --git-common-dir is the main checkout's .git even from a linked worktree.
    main_checkout="$(cd "$(dirname "$(cd "$common_dir" && pwd)")" && pwd)"
  else
    main_checkout="$repo_root"
  fi
  dirname "$main_checkout"
}

# default_upstream_dir prints the original Claude Code checkout to compare against.
default_upstream_dir() {
  local override="${NIUMA_UPSTREAM_DIR:-}}"
  if [[ -n "$override" ]]; then
    printf '%s\n' "$override"
    return
  fi
  printf '%s/claude_code_src_2026\n' "$(companion_root "${1:-$PWD}")"
}

# default_apg_dir prints the agent-proving-ground checkout.
default_apg_dir() {
  local override="${NIUMA_APG_DIR:-}}"
  if [[ -n "$override" ]]; then
    printf '%s\n' "$override"
    return
  fi
  printf '%s/agent-proving-ground\n' "$(companion_root "${1:-$PWD}")"
}

# require_companion_dir explains what is missing instead of letting the caller
# fail later with a bare "no such file".
require_companion_dir() {
  local dir="$1" name="$2" env_var="$3"
  if [[ -d "$dir" ]]; then
    return 0
  fi
  cat >&2 <<EOF
missing companion checkout: $name
  expected at: $dir
  This script compares against / consumes artifacts from $name, which is a
  separate repository and is not vendored here.
  Clone it next to this repository, or point $env_var at your checkout.
EOF
  return 1
}
