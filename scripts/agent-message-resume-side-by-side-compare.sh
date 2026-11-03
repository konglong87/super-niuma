#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-agent-message-resume-side-by-side-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TARGET_CWD="$ROOT_DIR"
MODEL="agent-message-resume-stub"
PROMPT_PROFILE="claude-compatible"
WORKTREE_RESUME="true"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-message-resume-side-by-side-compare.sh [flags]

Runs niuma AgentMessage resume/worktree evidence against original Claude
Code's SendMessage resume capture, then compares prompt shape and behavior
signals. Upstream SendMessage currently needs the experimental agent-teams env
switch; the deprecated --agent-teams CLI flag is intentionally not used.

Flags:
  --work-dir <path>       Artifact directory. Default:
                          /tmp/niuma-agent-message-resume-side-by-side-<timestamp>
  --upstream-dir <path>   Original Claude Code source/extract dir. Default:
                          <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --target-cwd <path>     Workspace for both runs. Default: repo root.
  --model <name>          Model name for both runs. Default: agent-message-resume-stub.
  --prompt-profile <name> niuma prompt profile. Default: claude-compatible.
  --no-worktree-resume    Do not include a retained worktree in the niuma
                          terminal task state.
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
    --target-cwd)
      TARGET_CWD="${2:?missing value for --target-cwd}"
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
    --no-worktree-resume)
      WORKTREE_RESUME="false"
      shift
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
mkdir -p "$WORK_DIR"

GO_WORK_DIR="$WORK_DIR/go"
GO_DUMP="$WORK_DIR/go-agent-message-resume.jsonl"
GO_RUN_OUT="$WORK_DIR/go-run.out"
GO_PROVIDER_LOG="$GO_WORK_DIR/provider-requests.jsonl"
GO_CLI_LOG="$GO_WORK_DIR/cli.log"
UPSTREAM_WORK_DIR="$WORK_DIR/upstream"
UPSTREAM_CAPTURE="$UPSTREAM_WORK_DIR/capture.jsonl"
UPSTREAM_REPORT="$UPSTREAM_WORK_DIR/report.json"
PROMPT_COMPARE="$WORK_DIR/promptdump-compare.json"
BEHAVIOR_COMPARE="$WORK_DIR/behavior-compare.json"
SUMMARY_JSON="$WORK_DIR/summary.json"

echo "running niuma AgentMessage resume capture; dump=$GO_DUMP" >&2
go_cmd=(
  "$ROOT_DIR/scripts/agent-message-resume-acceptance.sh"
  --cwd "$TARGET_CWD" \
  --work-dir "$GO_WORK_DIR" \
  --dump "$GO_DUMP" \
  --model "$MODEL" \
  --prompt-profile "$PROMPT_PROFILE" \
  --force
)
if [[ "$WORKTREE_RESUME" == "true" ]]; then
  go_cmd+=(--worktree-resume)
fi
"${go_cmd[@]}" >"$GO_RUN_OUT"

echo "running upstream SendMessage resume capture; work_dir=$UPSTREAM_WORK_DIR" >&2
"$ROOT_DIR/scripts/upstream-agent-lifecycle-capture.sh" \
  --upstream-dir "$UPSTREAM_DIR" \
  --target-cwd "$TARGET_CWD" \
  --work-dir "$UPSTREAM_WORK_DIR" \
  --scenario send-message-resume \
  --model "$MODEL" \
  --experimental-agent-teams \
  --user-type ant \
  --force

echo "comparing prompt dumps..." >&2
(cd "$ROOT_DIR" && go run ./scripts/promptdump-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$PROMPT_COMPARE"

echo "comparing behavior evidence..." >&2
(cd "$ROOT_DIR" && go run ./scripts/behavior-eval-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$BEHAVIOR_COMPARE"

node - "$SUMMARY_JSON" "$GO_DUMP" "$GO_RUN_OUT" "$GO_PROVIDER_LOG" "$GO_CLI_LOG" "$UPSTREAM_REPORT" "$UPSTREAM_CAPTURE" "$PROMPT_COMPARE" "$BEHAVIOR_COMPARE" "$MODEL" "$PROMPT_PROFILE" <<'EOF_SUMMARY'
const fs = require("fs");

const [
  summaryPath,
  goDump,
  goRunOut,
  goProviderLog,
  goCliLog,
  upstreamReportPath,
  upstreamCapture,
  promptComparePath,
  behaviorComparePath,
  model,
  promptProfile,
] = process.argv.slice(2);

function readJson(path) {
  return JSON.parse(fs.readFileSync(path, "utf8"));
}

const goRunText = fs.readFileSync(goRunOut, "utf8");
const upstream = readJson(upstreamReportPath);
const promptCompare = readJson(promptComparePath);
const behaviorCompare = readJson(behaviorComparePath);
const upstreamFindings = upstream.findings || {};
const summary = {
  ok: Boolean(
    goRunText.includes("ok=true") &&
      upstream.ok &&
      upstreamFindings.send_message_tool_exposed &&
      upstreamFindings.sendmessage_tool_use_captured &&
      upstreamFindings.sendmessage_stopped_resume_failure_captured &&
      promptCompare.ok &&
      behaviorCompare.ok
  ),
  model,
  prompt_profile: promptProfile,
  artifacts: {
    go_dump: goDump,
    go_run: goRunOut,
    go_provider_log: goProviderLog,
    go_cli_log: goCliLog,
    upstream_report: upstreamReportPath,
    upstream_capture: upstreamCapture,
    prompt_compare: promptComparePath,
    behavior_compare: behaviorComparePath,
  },
  go: {
    ok: goRunText.includes("ok=true"),
    worktree_resume: goRunText.includes("retained_worktree="),
  },
  upstream: {
    ok: upstream.ok,
    request_count: upstream.request_count,
    response_classes: upstream.response_classes,
    findings: upstream.findings,
    exposure_mode: upstream.exposure_mode,
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
