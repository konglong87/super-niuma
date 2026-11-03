#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-agent-child-error-side-by-side-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TARGET_CWD="$ROOT_DIR"
MODEL="agent-child-error-stub"
PROMPT_PROFILE="claude-compatible-strict"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-child-error-side-by-side-compare.sh [flags]

Runs the same background Agent child-error lifecycle against niuma and
original Claude Code, then compares request shape and behavior-level evidence.
The gate focuses on terminal failure evidence propagation: the child provider
fails, then the parent request must receive structured failed status/result
context for the next planning step.

Flags:
  --work-dir <path>       Artifact directory. Default:
                          /tmp/niuma-agent-child-error-side-by-side-<timestamp>
  --upstream-dir <path>   Original Claude Code source/extract dir. Default:
                          <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --target-cwd <path>     Workspace for both runs. Default: repo root.
  --model <name>          Model name for both runs. Default: agent-child-error-stub.
  --prompt-profile <name> niuma prompt profile. Default: claude-compatible-strict.
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
GO_DUMP="$WORK_DIR/go-child-error.jsonl"
GO_REPORT="$GO_WORK_DIR/report.json"
GO_PROVIDER_LOG="$GO_WORK_DIR/provider-requests.jsonl"
GO_CLI_LOG="$GO_WORK_DIR/cli.log"
UPSTREAM_WORK_DIR="$WORK_DIR/upstream"
UPSTREAM_CAPTURE="$UPSTREAM_WORK_DIR/capture.jsonl"
UPSTREAM_REPORT="$UPSTREAM_WORK_DIR/report.json"
PROMPT_COMPARE="$WORK_DIR/promptdump-compare.json"
BEHAVIOR_COMPARE="$WORK_DIR/behavior-compare.json"
SUMMARY_JSON="$WORK_DIR/summary.json"

echo "running niuma Agent child-error capture; dump=$GO_DUMP" >&2
"$ROOT_DIR/scripts/go-agent-lifecycle-capture.sh" \
  --target-cwd "$TARGET_CWD" \
  --work-dir "$GO_WORK_DIR" \
  --dump "$GO_DUMP" \
  --scenario child-error \
  --model "$MODEL" \
  --prompt-profile "$PROMPT_PROFILE" \
  --force

echo "running upstream Agent child-error capture; work_dir=$UPSTREAM_WORK_DIR" >&2
"$ROOT_DIR/scripts/upstream-agent-lifecycle-capture.sh" \
  --upstream-dir "$UPSTREAM_DIR" \
  --target-cwd "$TARGET_CWD" \
  --work-dir "$UPSTREAM_WORK_DIR" \
  --scenario child-error \
  --model "$MODEL" \
  --force

echo "comparing prompt dumps..." >&2
(cd "$ROOT_DIR" && go run ./scripts/promptdump-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$PROMPT_COMPARE"

echo "comparing behavior evidence..." >&2
(cd "$ROOT_DIR" && go run ./scripts/behavior-eval-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$BEHAVIOR_COMPARE"

node - "$SUMMARY_JSON" "$GO_DUMP" "$GO_REPORT" "$GO_PROVIDER_LOG" "$GO_CLI_LOG" "$UPSTREAM_REPORT" "$UPSTREAM_CAPTURE" "$PROMPT_COMPARE" "$BEHAVIOR_COMPARE" "$MODEL" "$PROMPT_PROFILE" <<'EOF_SUMMARY'
const fs = require("fs");

const [
  summaryPath,
  goDump,
  goReportPath,
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

const goReport = readJson(goReportPath);
const upstream = readJson(upstreamReportPath);
const promptCompare = readJson(promptComparePath);
const behaviorCompare = readJson(behaviorComparePath);
const summary = {
  ok: Boolean(goReport.ok && upstream.ok && promptCompare.ok && behaviorCompare.ok),
  model,
  prompt_profile: promptProfile,
  artifacts: {
    go_dump: goDump,
    go_report: goReportPath,
    go_provider_log: goProviderLog,
    go_cli_log: goCliLog,
    upstream_report: upstreamReportPath,
    upstream_capture: upstreamCapture,
    prompt_compare: promptComparePath,
    behavior_compare: behaviorComparePath,
  },
  go: {
    ok: goReport.ok,
    request_count: goReport.request_count,
    response_classes: goReport.response_classes,
    findings: goReport.findings,
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
