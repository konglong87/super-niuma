#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-skill-load-side-by-side-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
MODEL="claude-sonnet-4-6"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="32000"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/skill-load-side-by-side-compare.sh [flags]

Runs the same Skill progressive-loading task against original Claude Code and
niuma, then compares request shape and behavior-level evidence. Both sides
use a temporary workspace containing .claude/skills/matrix-skill/SKILL.md.

Flags:
  --work-dir <path>       Artifact directory. Default:
                          /tmp/niuma-skill-load-side-by-side-<timestamp>
  --upstream-dir <path>   Original Claude Code source/extract dir. Default:
                          <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --model <name>          Model name for both runs. Default: claude-sonnet-4-6.
  --prompt-profile <name> niuma prompt profile. Default: claude-compatible.
  --max-tokens <n>        niuma max output tokens. Default: 32000.
  --force                 Remove existing --work-dir before running.
  -h, --help              Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir)
      WORK_DIR="${2:?missing value for --work-dir}"
      shift 2
      ;;
    --upstream-dir)
      UPSTREAM_DIR="${2:?missing value for --upstream-dir}"
      shift 2
      ;;
    --model)
      MODEL="${2:?missing value for --model}"
      shift 2
      ;;
    --prompt-profile)
      PROMPT_PROFILE="${2:?missing value for --prompt-profile}"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="${2:?missing value for --max-tokens}"
      shift 2
      ;;
    --force)
      FORCE="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir already exists: $WORK_DIR (use --force or choose another --work-dir)" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" ]]; then
  rm -rf "$WORK_DIR"
fi
mkdir -p "$WORK_DIR/workspace/.claude/skills/matrix-skill"

WORKSPACE="$WORK_DIR/workspace"
cat > "$WORKSPACE/.claude/skills/matrix-skill/SKILL.md" <<'SKILL'
---
name: matrix-skill
description: Matrix acceptance skill
---

MATRIX_SKILL_ACTIVE

When this skill is active, answer with the exact marker MATRIX_SKILL_ACTIVE
and do not modify files.
SKILL

GO_DUMP="$WORK_DIR/go-skill-load.jsonl"
GO_RUN_LOG="$WORK_DIR/go-skill-load.run.log"
UPSTREAM_WORK_DIR="$WORK_DIR/upstream"
UPSTREAM_CAPTURE="$UPSTREAM_WORK_DIR/capture.jsonl"
UPSTREAM_REPORT="$UPSTREAM_WORK_DIR/report.json"
PROMPT_COMPARE="$WORK_DIR/promptdump-compare.json"
BEHAVIOR_COMPARE="$WORK_DIR/behavior-compare.json"
SUMMARY_JSON="$WORK_DIR/summary.json"

echo "running upstream skill-load capture; work_dir=$UPSTREAM_WORK_DIR" >&2
"$ROOT_DIR/scripts/upstream-agent-lifecycle-capture.sh" \
  --upstream-dir "$UPSTREAM_DIR" \
  --target-cwd "$WORKSPACE" \
  --work-dir "$UPSTREAM_WORK_DIR" \
  --scenario skill-load \
  --model "$MODEL" \
  --force

echo "running niuma skill-load gate; dump=$GO_DUMP" >&2
"$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --cwd "$WORKSPACE" \
  --dump "$GO_DUMP" \
  --run-log "$GO_RUN_LOG" \
  --force \
  --full \
  --prompt-profile "$PROMPT_PROFILE" \
  --model "$MODEL" \
  --max-tokens "$MAX_TOKENS" \
  --max-turns 3 \
  --min-turns 2 \
  --tools "Skill,Read" \
  --require-tool-result "Skill" \
  --require-request-text "Skill matrix-skill instructions are now active,MATRIX_SKILL_ACTIVE" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  --prompt "必须先调用 Skill 工具加载 matrix-skill。加载后只用一句话回答：MATRIX_SKILL_ACTIVE 已进入上下文。不要修改文件。"

echo "comparing prompt dumps..." >&2
(cd "$ROOT_DIR" && go run ./scripts/promptdump-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$PROMPT_COMPARE"

echo "comparing behavior evidence..." >&2
(cd "$ROOT_DIR" && go run ./scripts/behavior-eval-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$BEHAVIOR_COMPARE"

node - "$SUMMARY_JSON" "$GO_DUMP" "$GO_RUN_LOG" "$UPSTREAM_REPORT" "$UPSTREAM_CAPTURE" "$PROMPT_COMPARE" "$BEHAVIOR_COMPARE" "$WORKSPACE" "$MODEL" "$PROMPT_PROFILE" "$MAX_TOKENS" <<'EOF_SUMMARY'
const fs = require("fs");

const [
  summaryPath,
  goDump,
  goRunLog,
  upstreamReportPath,
  upstreamCapture,
  promptComparePath,
  behaviorComparePath,
  workspace,
  model,
  promptProfile,
  maxTokens,
] = process.argv.slice(2);

function readJson(path) {
  return JSON.parse(fs.readFileSync(path, "utf8"));
}

const upstream = readJson(upstreamReportPath);
const promptCompare = readJson(promptComparePath);
const behaviorCompare = readJson(behaviorComparePath);
const summary = {
  ok: Boolean(upstream.ok && promptCompare.ok && behaviorCompare.ok),
  model,
  prompt_profile: promptProfile,
  max_tokens: Number(maxTokens),
  workspace,
  artifacts: {
    go_dump: goDump,
    go_run_log: goRunLog,
    upstream_report: upstreamReportPath,
    upstream_capture: upstreamCapture,
    prompt_compare: promptComparePath,
    behavior_compare: behaviorComparePath,
  },
  upstream: {
    ok: upstream.ok,
    request_count: upstream.request_count,
    response_classes: upstream.response_classes,
    findings: upstream.findings,
  },
  prompt_compare: {
    ok: promptCompare.ok,
    differences: promptCompare.differences || [],
    warnings: promptCompare.warnings || [],
  },
  behavior_compare: {
    ok: behaviorCompare.ok,
    capability_signals: {
      go: behaviorCompare.go && behaviorCompare.go.capability_signals,
      upstream: behaviorCompare.upstream && behaviorCompare.upstream.capability_signals,
      final_go: behaviorCompare.go && behaviorCompare.go.final_main && behaviorCompare.go.final_main.capability_signals,
      final_upstream: behaviorCompare.upstream && behaviorCompare.upstream.final_main && behaviorCompare.upstream.final_main.capability_signals,
    },
    differences: behaviorCompare.differences || [],
    warnings: behaviorCompare.warnings || [],
    info: behaviorCompare.info || [],
  },
};

fs.writeFileSync(summaryPath, JSON.stringify(summary, null, 2) + "\n");
console.log(JSON.stringify(summary, null, 2));
if (!summary.ok) process.exitCode = 1;
EOF_SUMMARY

echo "summary=$SUMMARY_JSON"
