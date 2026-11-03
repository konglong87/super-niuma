#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

VERIFY_ONLY="false"
WORKSPACE=""
PASS_ARGS=()

usage() {
  cat <<'USAGE'
Usage:
  scripts/skills-prompt-acceptance.sh [flags]

Creates a temporary workspace with a project-local matrix skill, forces the
model to load it with the Skill tool, then verifies the prompt dump contains
both the Skill tool_result and the loaded skill context message in the full
request.

Flags accepted by scripts/code-mode-prompt-acceptance.sh are passed through.
If --cwd is supplied, the skill fixture is created in that workspace.
Without --cwd, a temporary workspace is created automatically.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cwd)
      WORKSPACE="${2:?missing value for --cwd}"
      PASS_ARGS+=("$1" "$2")
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
    WORKSPACE="$(mktemp -d /tmp/niuma-skills-acceptance-workspace-XXXXXX)"
    PASS_ARGS=(--cwd "$WORKSPACE" "${PASS_ARGS[@]}")
  fi

  mkdir -p "$WORKSPACE/.claude/skills/matrix-skill"
  cat > "$WORKSPACE/.claude/skills/matrix-skill/SKILL.md" <<'SKILL'
---
name: matrix-skill
description: Matrix acceptance skill
---

MATRIX_SKILL_ACTIVE

When this skill is active, answer with the exact marker MATRIX_SKILL_ACTIVE
and do not modify files.
SKILL
fi

SKILLS_PROMPT="必须先调用 Skill 工具加载 matrix-skill。加载后只用一句话回答：MATRIX_SKILL_ACTIVE 已进入上下文。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --prompt "$SKILLS_PROMPT" \
  --tools "Skill,LS,Read" \
  --max-turns 3 \
  --min-turns 2 \
  --full \
  --require-tool-result "Skill" \
  --require-request-text "Skill matrix-skill instructions are now active,MATRIX_SKILL_ACTIVE" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  "${PASS_ARGS[@]}"
