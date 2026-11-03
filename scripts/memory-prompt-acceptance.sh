#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

VERIFY_ONLY="false"
WORKSPACE=""
CONFIG_DIR=""
PASS_ARGS=()

usage() {
  cat <<'USAGE'
Usage:
  scripts/memory-prompt-acceptance.sh [flags]

Creates an isolated workspace plus Claude config dir with a project MEMORY.md,
then verifies the real full prompt dump contains both:
  - compatible auto-memory behavioral rules, and
  - project MEMORY.md recall content as model-visible request context.

Flags accepted by scripts/code-mode-prompt-acceptance.sh are passed through.
If --cwd is supplied, the memory fixture is created for that workspace.
If --config-dir is supplied, it is used as CLAUDE_CONFIG_DIR.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cwd)
      WORKSPACE="${2:?missing value for --cwd}"
      PASS_ARGS+=("$1" "$2")
      shift 2
      ;;
    --config-dir)
      CONFIG_DIR="${2:?missing value for --config-dir}"
      shift 2
      ;;
    --verify-only)
      VERIFY_ONLY="true"
      PASS_ARGS+=("$1")
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      PASS_ARGS+=("$1")
      shift
      ;;
  esac
done

if [[ "$VERIFY_ONLY" != "true" ]]; then
  if [[ -z "$WORKSPACE" ]]; then
    WORKSPACE="$(mktemp -d /tmp/niuma-memory-acceptance-workspace-XXXXXX)"
    PASS_ARGS=(--cwd "$WORKSPACE" "${PASS_ARGS[@]}")
  fi
  if [[ -z "$CONFIG_DIR" ]]; then
    CONFIG_DIR="$(mktemp -d /tmp/niuma-memory-acceptance-config-XXXXXX)"
  fi

  abs_workspace="$(cd "$WORKSPACE" && pwd)"
  slug="${abs_workspace//\//-}"
  slug="${slug//:/}"
  slug="${slug// /-}"
  memory_dir="$CONFIG_DIR/projects/$slug/memory"
  mkdir -p "$memory_dir"
  cat > "$memory_dir/MEMORY.md" <<'MEMORY'
- [Memory acceptance preference](preference.md) - MEMORY_ACCEPTANCE_PROJECT_PREF
MEMORY
  cat > "$memory_dir/preference.md" <<'MEMORY'
---
name: Memory acceptance preference
description: Fixture memory for prompt acceptance.
metadata:
  type: project
---

# Memory acceptance preference

MEMORY_ACCEPTANCE_PROJECT_PREF
MEMORY
fi

if [[ -n "$CONFIG_DIR" ]]; then
  export CLAUDE_CONFIG_DIR="$CONFIG_DIR"
fi

MEMORY_PROMPT="只读检查当前项目记忆：如果上下文中出现 MEMORY_ACCEPTANCE_PROJECT_PREF，只回答 MEMORY_ACCEPTANCE_PROJECT_PREF 已加载；不要修改文件，不要写 memory。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --prompt "$MEMORY_PROMPT" \
  --prompt-profile claude-compatible \
  --tools "Read,Grep" \
  --max-turns 1 \
  --min-turns 1 \
  --full \
  --allow-run-failure \
  --require-request-text "MEMORY_ACCEPTANCE_PROJECT_PREF,# auto memory,For explicitly read-only tasks,Before recommending from memory,PR list or activity summary,surprising or non-obvious" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  "${PASS_ARGS[@]}"
