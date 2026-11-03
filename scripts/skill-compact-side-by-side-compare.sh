#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-skill-compact-side-by-side-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
MODEL="skill-compact-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/skill-compact-side-by-side-compare.sh [flags]

Runs the same Skill + auto-compact preservation task against niuma and
original Claude Code, then compares request shape and behavior-level evidence.
The niuma side uses the existing deterministic skill-compact acceptance
gate; the upstream side reuses the generated temporary workspace.

Flags:
  --work-dir <path>       Artifact directory. Default:
                          /tmp/niuma-skill-compact-side-by-side-<timestamp>
  --upstream-dir <path>   Original Claude Code source/extract dir. Default:
                          <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --model <name>          Model name for both runs. Default: skill-compact-stub.
  --prompt-profile <name> niuma prompt profile. Default: claude-compatible.
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
GO_DUMP="$WORK_DIR/go-skill-compact.jsonl"
GO_REPORT="$GO_WORK_DIR/report.json"
GO_PROVIDER_LOG="$GO_WORK_DIR/provider-requests.jsonl"
GO_CLI_LOG="$GO_WORK_DIR/cli.log"
WORKSPACE="$GO_WORK_DIR/workspace"
UPSTREAM_WORK_DIR="$WORK_DIR/upstream"
UPSTREAM_CAPTURE="$UPSTREAM_WORK_DIR/capture.jsonl"
UPSTREAM_REPORT="$UPSTREAM_WORK_DIR/report.json"
PROMPT_COMPARE="$WORK_DIR/promptdump-compare.json"
BEHAVIOR_COMPARE="$WORK_DIR/behavior-compare.json"
SUMMARY_JSON="$WORK_DIR/summary.json"

echo "running niuma skill-compact gate; dump=$GO_DUMP" >&2
"$ROOT_DIR/scripts/skill-compact-prompt-acceptance.sh" \
  --work-dir "$GO_WORK_DIR" \
  --dump "$GO_DUMP" \
  --model "$MODEL" \
  --prompt-profile "$PROMPT_PROFILE" \
  --force

if [[ ! -d "$WORKSPACE" ]]; then
  echo "niuma skill-compact workspace missing: $WORKSPACE" >&2
  exit 1
fi

echo "running upstream skill-compact capture; work_dir=$UPSTREAM_WORK_DIR" >&2
"$ROOT_DIR/scripts/upstream-agent-lifecycle-capture.sh" \
  --upstream-dir "$UPSTREAM_DIR" \
  --target-cwd "$WORKSPACE" \
  --work-dir "$UPSTREAM_WORK_DIR" \
  --scenario skill-compact \
  --model "$MODEL" \
  --force

echo "comparing prompt dumps..." >&2
(cd "$ROOT_DIR" && go run ./scripts/promptdump-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$PROMPT_COMPARE"

echo "comparing behavior evidence..." >&2
(cd "$ROOT_DIR" && go run ./scripts/behavior-eval-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$BEHAVIOR_COMPARE"

node - "$SUMMARY_JSON" "$GO_DUMP" "$GO_REPORT" "$GO_PROVIDER_LOG" "$GO_CLI_LOG" "$UPSTREAM_REPORT" "$UPSTREAM_CAPTURE" "$PROMPT_COMPARE" "$BEHAVIOR_COMPARE" "$WORKSPACE" "$MODEL" "$PROMPT_PROFILE" <<'EOF_SUMMARY'
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
  workspace,
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
  workspace,
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
    has_summary_request: goReport.has_summary_request,
    has_main_compacted_provider_request: goReport.has_main_compacted_provider_request,
    compacted_request_count: goReport.compacted_request_count,
    compacted_with_skill_count: goReport.compacted_with_skill_count,
    max_active_skill_reminder_count: goReport.max_active_skill_reminder_count,
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
