#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-agent-long-output-side-by-side-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TARGET_CWD="$ROOT_DIR"
MODEL="agent-long-output-stub"
PROMPT_PROFILE="claude-compatible-strict"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-long-output-side-by-side-compare.sh [flags]

Runs the same long-output background Agent lifecycle against niuma and
original Claude Code, then compares request shape and behavior-level evidence.
The gate focuses on context hygiene: niuma must keep a structured
content_preview/content_truncated/content_bytes/output_file summary, while the
upstream capture documents whether the full long output tail reaches the parent
request.

Flags:
  --work-dir <path>       Artifact directory. Default:
                          /tmp/niuma-agent-long-output-side-by-side-<timestamp>
  --upstream-dir <path>   Original Claude Code source/extract dir. Default:
                          <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --target-cwd <path>     Workspace for both runs. Default: repo root.
  --model <name>          Model name for both runs. Default: agent-long-output-stub.
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
GO_DUMP="$WORK_DIR/go-long-output.jsonl"
GO_RUN_OUT="$WORK_DIR/go-run.out"
GO_VERIFY_JSON="$WORK_DIR/go-verify.json"
GO_PROVIDER_LOG="$GO_WORK_DIR/provider-requests.jsonl"
GO_CLI_LOG="$GO_WORK_DIR/cli.log"
UPSTREAM_WORK_DIR="$WORK_DIR/upstream"
UPSTREAM_CAPTURE="$UPSTREAM_WORK_DIR/capture.jsonl"
UPSTREAM_REPORT="$UPSTREAM_WORK_DIR/report.json"
PROMPT_COMPARE="$WORK_DIR/promptdump-compare.json"
BEHAVIOR_COMPARE="$WORK_DIR/behavior-compare.json"
SUMMARY_JSON="$WORK_DIR/summary.json"

echo "running niuma Agent long-output capture; dump=$GO_DUMP" >&2
"$ROOT_DIR/scripts/agent-long-output-resume-acceptance.sh" \
  --cwd "$TARGET_CWD" \
  --work-dir "$GO_WORK_DIR" \
  --dump "$GO_DUMP" \
  --model "$MODEL" \
  --prompt-profile "$PROMPT_PROFILE" \
  --force >"$GO_RUN_OUT"

echo "summarizing niuma long-output prompt dump..." >&2
(cd "$ROOT_DIR" && go run ./scripts/verify-code-mode-prompt-dump.go \
  --min-turns 2 \
  --require-final-tools-disabled=false \
  --require-final-turn-budget=false \
  --require-subagent-turn-budget-tools-disabled=false \
  --require-tool-result "AgentGet" \
  --require-tool-no-persisted-output "AgentGet" \
  --require-tool-max-bytes "AgentGet=10000" \
  --require-request-text "## Background agent tasks,completed,completion notification: result is ready,content_preview,content_truncated,content_bytes,output_file,long-agent.output" \
  --forbid-request-text "AGENT_LONG_OUTPUT_TAIL_MARKER,<persisted-output>" \
  "$GO_DUMP") >"$GO_VERIFY_JSON"

echo "running upstream Agent long-output capture; work_dir=$UPSTREAM_WORK_DIR" >&2
"$ROOT_DIR/scripts/upstream-agent-lifecycle-capture.sh" \
  --upstream-dir "$UPSTREAM_DIR" \
  --target-cwd "$TARGET_CWD" \
  --work-dir "$UPSTREAM_WORK_DIR" \
  --scenario long-output \
  --model "$MODEL" \
  --force

echo "comparing prompt dumps..." >&2
(cd "$ROOT_DIR" && go run ./scripts/promptdump-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$PROMPT_COMPARE"

echo "comparing behavior evidence..." >&2
(cd "$ROOT_DIR" && go run ./scripts/behavior-eval-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$BEHAVIOR_COMPARE"

node - "$SUMMARY_JSON" "$GO_DUMP" "$GO_RUN_OUT" "$GO_VERIFY_JSON" "$GO_PROVIDER_LOG" "$GO_CLI_LOG" "$UPSTREAM_REPORT" "$UPSTREAM_CAPTURE" "$PROMPT_COMPARE" "$BEHAVIOR_COMPARE" "$MODEL" "$PROMPT_PROFILE" <<'EOF_SUMMARY'
const fs = require("fs");

const [
  summaryPath,
  goDump,
  goRunOut,
  goVerifyPath,
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

function parseGoMarkers(dumpPath) {
  const records = fs.readFileSync(dumpPath, "utf8")
    .trim()
    .split(/\n+/)
    .filter(Boolean)
    .map(line => JSON.parse(line));
  const text = JSON.stringify(records.map(record => record.request || {}));
  let agentGetMaxBytes = 0;
  let agentGetPersistedOutput = 0;
  for (const record of records) {
    const byTool = record.summary?.tool_results_by_tool || {};
    const agentGet = byTool.AgentGet;
    if (!agentGet) continue;
    agentGetMaxBytes = Math.max(agentGetMaxBytes, Number(agentGet.max_bytes || 0));
    agentGetPersistedOutput += Number(agentGet.persisted_output || 0);
  }
  return {
    request_count: records.length,
    has_content_preview: text.includes("content_preview"),
    has_content_truncated: text.includes("content_truncated"),
    has_content_bytes: text.includes("content_bytes"),
    has_output_file: text.includes("long-agent.output"),
    has_tail_marker: text.includes("AGENT_LONG_OUTPUT_TAIL_MARKER"),
    has_generic_persisted_output: text.includes("<persisted-output>"),
    agent_get_max_bytes: agentGetMaxBytes,
    agent_get_persisted_output: agentGetPersistedOutput,
  };
}

const goVerify = readJson(goVerifyPath);
const upstream = readJson(upstreamReportPath);
const promptCompare = readJson(promptComparePath);
const behaviorCompare = readJson(behaviorComparePath);
const goRunText = fs.readFileSync(goRunOut, "utf8");
const goMarkers = parseGoMarkers(goDump);
const agentGetSummary = goVerify.final?.tool_results_by_tool?.AgentGet;
if (agentGetSummary) {
  goMarkers.agent_get_max_bytes = Number(agentGetSummary.max_bytes || goMarkers.agent_get_max_bytes || 0);
  goMarkers.agent_get_persisted_output = Number(agentGetSummary.persisted_output || 0);
}
const upstreamFindings = upstream.findings || {};
const upstreamFinal = Array.isArray(upstream.request_summaries)
  ? upstream.request_summaries[upstream.request_summaries.length - 1]
  : null;
const longOutputEvidence = {
  go: {
    ok: goRunText.includes("ok=true") && Boolean(goVerify.ok),
    ...goMarkers,
  },
  upstream: {
    ok: Boolean(upstream.ok),
    request_count: upstream.request_count,
    final_request_text_bytes: upstreamFinal && upstreamFinal.request_text_bytes,
    long_output_notification_captured: Boolean(upstreamFindings.long_output_notification_captured),
    long_output_tail_marker_captured: Boolean(upstreamFindings.long_output_tail_marker_captured),
    used_generic_persisted_output: Boolean(upstreamFindings.long_output_used_generic_persisted_output),
    used_structured_content_preview: Boolean(upstreamFindings.long_output_used_structured_content_preview),
  },
};
const summary = {
  ok: Boolean(
    longOutputEvidence.go.ok &&
      longOutputEvidence.go.has_content_preview &&
      longOutputEvidence.go.has_content_truncated &&
      longOutputEvidence.go.has_content_bytes &&
      longOutputEvidence.go.has_output_file &&
      !longOutputEvidence.go.has_tail_marker &&
      !longOutputEvidence.go.has_generic_persisted_output &&
      longOutputEvidence.go.agent_get_max_bytes > 0 &&
      longOutputEvidence.go.agent_get_max_bytes <= 10000 &&
      longOutputEvidence.go.agent_get_persisted_output === 0 &&
      upstream.ok &&
      promptCompare.ok &&
      behaviorCompare.ok
  ),
  model,
  prompt_profile: promptProfile,
  artifacts: {
    go_dump: goDump,
    go_run: goRunOut,
    go_verify: goVerifyPath,
    go_provider_log: goProviderLog,
    go_cli_log: goCliLog,
    upstream_report: upstreamReportPath,
    upstream_capture: upstreamCapture,
    prompt_compare: promptComparePath,
    behavior_compare: behaviorComparePath,
  },
  long_output_evidence: longOutputEvidence,
  go: {
    ok: longOutputEvidence.go.ok,
    request_count: longOutputEvidence.go.request_count,
    final: goVerify.final,
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
