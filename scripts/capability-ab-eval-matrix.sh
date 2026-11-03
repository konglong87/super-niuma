#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="/tmp/niuma-capability-ab-eval-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TARGET_CWD="$ROOT_DIR"
SCENARIOS="final-report-side-by-side,skill-load-side-by-side,skill-compact-side-by-side,agent-child-error-side-by-side,agent-cancelled-side-by-side,agent-long-output-side-by-side,agent-message-resume-basic-side-by-side,agent-message-resume-side-by-side"
FINAL_REPORT_MODEL="gpt-5.5"
SKILL_MODEL="claude-sonnet-4-6"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="32000"
VERIFY_ONLY="false"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/capability-ab-eval-matrix.sh [flags]

Runs or verifies a multi-scenario Go-vs-original-Claude-Code capability
scorecard. Each scenario reuses an existing side-by-side summary.json and the
underlying prompt/behavior compare artifacts.

Flags:
  --out-dir <path>              Artifact directory. Default:
                                /tmp/niuma-capability-ab-eval-<timestamp>
  --scenarios <csv>             Scenario list. Default:
                                final-report-side-by-side,skill-load-side-by-side,
                                skill-compact-side-by-side,agent-child-error-side-by-side,
                                agent-cancelled-side-by-side,agent-long-output-side-by-side,
                                agent-message-resume-basic-side-by-side,
                                agent-message-resume-side-by-side
  --verify-only                 Do not run scenarios; read existing
                                <out-dir>/<scenario>/summary.json files.
  --upstream-dir <path>         Original Claude Code source/extract dir.
  --target-cwd <path>           Workspace for final-report scenario.
  --final-report-model <name>   Model for final-report side-by-side.
  --skill-model <name>          Model for skill-load side-by-side.
  --prompt-profile <name>       niuma prompt profile.
  --max-tokens <n>              niuma max output tokens.
  --force                       Remove existing --out-dir before running.
  -h, --help                    Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      OUT_DIR="${2:?missing value for --out-dir}"
      shift 2
      ;;
    --scenarios)
      SCENARIOS="${2:?missing value for --scenarios}"
      shift 2
      ;;
    --verify-only)
      VERIFY_ONLY="true"
      shift
      ;;
    --upstream-dir)
      UPSTREAM_DIR="${2:?missing value for --upstream-dir}"
      shift 2
      ;;
    --target-cwd)
      TARGET_CWD="${2:?missing value for --target-cwd}"
      shift 2
      ;;
    --final-report-model)
      FINAL_REPORT_MODEL="${2:?missing value for --final-report-model}"
      shift 2
      ;;
    --skill-model)
      SKILL_MODEL="${2:?missing value for --skill-model}"
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

if [[ -e "$OUT_DIR" && "$VERIFY_ONLY" != "true" && "$FORCE" != "true" ]]; then
  echo "out dir already exists: $OUT_DIR (use --force or choose another --out-dir)" >&2
  exit 2
fi
if [[ -e "$OUT_DIR" && "$VERIFY_ONLY" != "true" && "$FORCE" == "true" ]]; then
  rm -rf "$OUT_DIR"
fi
mkdir -p "$OUT_DIR"

IFS=',' read -r -a SCENARIO_LIST <<<"$SCENARIOS"
SCENARIO_ARGS=()

run_scenario() {
  local scenario="$1"
  local work_dir="$OUT_DIR/$scenario"
  SCENARIO_ARGS+=("$scenario=$work_dir")

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -f "$work_dir/summary.json" ]]; then
      echo "missing summary for verify-only scenario $scenario: $work_dir/summary.json" >&2
      return 2
    fi
    return 0
  fi

  case "$scenario" in
    final-report-side-by-side)
      "$ROOT_DIR/scripts/final-report-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --target-cwd "$TARGET_CWD" \
        --model "$FINAL_REPORT_MODEL" \
        --prompt-profile "$PROMPT_PROFILE" \
        --max-tokens "$MAX_TOKENS" \
        --force
      ;;
    skill-load-side-by-side)
      "$ROOT_DIR/scripts/skill-load-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --model "$SKILL_MODEL" \
        --prompt-profile "$PROMPT_PROFILE" \
        --max-tokens "$MAX_TOKENS" \
        --force
      ;;
    skill-compact-side-by-side)
      "$ROOT_DIR/scripts/skill-compact-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --prompt-profile "$PROMPT_PROFILE" \
        --force
      ;;
    agent-child-error-side-by-side)
      "$ROOT_DIR/scripts/agent-child-error-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --target-cwd "$TARGET_CWD" \
        --force
      ;;
    agent-cancelled-side-by-side)
      "$ROOT_DIR/scripts/agent-cancelled-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --target-cwd "$TARGET_CWD" \
        --force
      ;;
    agent-long-output-side-by-side)
      "$ROOT_DIR/scripts/agent-long-output-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --target-cwd "$TARGET_CWD" \
        --force
      ;;
    agent-message-resume-basic-side-by-side)
      "$ROOT_DIR/scripts/agent-message-resume-basic-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --target-cwd "$TARGET_CWD" \
        --prompt-profile "$PROMPT_PROFILE" \
        --force
      ;;
    agent-message-resume-side-by-side)
      "$ROOT_DIR/scripts/agent-message-resume-side-by-side-compare.sh" \
        --work-dir "$work_dir" \
        --upstream-dir "$UPSTREAM_DIR" \
        --target-cwd "$TARGET_CWD" \
        --prompt-profile "$PROMPT_PROFILE" \
        --force
      ;;
    *)
      echo "unknown scenario: $scenario" >&2
      return 2
      ;;
  esac
}

for scenario in "${SCENARIO_LIST[@]}"; do
  scenario="$(echo "$scenario" | xargs)"
  if [[ -z "$scenario" ]]; then
    continue
  fi
  echo "capability A/B scenario: $scenario" >&2
  run_scenario "$scenario"
done

SCORECARD_JSON="$OUT_DIR/scorecard.json"

node - "$SCORECARD_JSON" "${SCENARIO_ARGS[@]}" <<'EOF_SCORECARD'
const fs = require("fs");
const path = require("path");

const [scorecardPath, ...scenarioArgs] = process.argv.slice(2);

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function countSeverities(items) {
  const counts = { error: 0, warning: 0, info: 0 };
  for (const item of items || []) {
    const severity = String(item && item.severity || "info");
    if (severity === "error") counts.error++;
    else if (severity === "warning" || severity === "warn") counts.warning++;
    else counts.info++;
  }
  return counts;
}

function addCounts(a, b) {
  return {
    error: a.error + b.error,
    warning: a.warning + b.warning,
    info: a.info + b.info,
  };
}

function score(signals) {
  if (signals && typeof signals.anchored_score === "number") return signals.anchored_score;
  if (!signals || typeof signals.score !== "number") return 0;
  return signals.score;
}

function rawScore(signals) {
  if (!signals || typeof signals.score !== "number") return 0;
  return signals.score;
}

function markers(signals) {
  if (signals && typeof signals.anchored_score === "number") {
    return Array.isArray(signals.anchored_markers) ? signals.anchored_markers : [];
  }
  return Array.isArray(signals && signals.markers) ? signals.markers : [];
}

function loadCapabilitySignals(summary) {
  const direct = summary.behavior_compare && summary.behavior_compare.capability_signals;
  if (direct) return direct;

  const behaviorPath = summary.artifacts && summary.artifacts.behavior_compare;
  if (behaviorPath && fs.existsSync(behaviorPath)) {
    const behavior = readJson(behaviorPath);
    return {
      go: behavior.go && behavior.go.capability_signals,
      upstream: behavior.upstream && behavior.upstream.capability_signals,
      final_go: behavior.go && behavior.go.final_main && behavior.go.final_main.capability_signals,
      final_upstream: behavior.upstream && behavior.upstream.final_main && behavior.upstream.final_main.capability_signals,
    };
  }
  return {};
}

function hasLongOutputContextAdvantage(summary) {
  const evidence = summary.long_output_evidence;
  if (!evidence || !evidence.go || !evidence.upstream) return false;
  const go = evidence.go;
  const upstream = evidence.upstream;
  return Boolean(
    go.has_content_preview &&
      go.has_content_truncated &&
      go.has_content_bytes &&
      go.has_output_file &&
      !go.has_tail_marker &&
      !go.has_generic_persisted_output &&
      Number(go.agent_get_persisted_output || 0) === 0 &&
      upstream.long_output_tail_marker_captured &&
      !upstream.used_structured_content_preview
  );
}

function evaluateScenario(arg) {
  const eq = arg.indexOf("=");
  if (eq < 1) throw new Error(`invalid scenario argument: ${arg}`);
  const name = arg.slice(0, eq);
  const dir = arg.slice(eq + 1);
  const summaryPath = path.join(dir, "summary.json");
  const summary = readJson(summaryPath);
  const promptCompare = summary.prompt_compare || {};
  const behaviorCompare = summary.behavior_compare || {};
  const promptCounts = countSeverities(promptCompare.differences || []);
  const behaviorCounts = countSeverities(behaviorCompare.differences || []);
  const diffCounts = addCounts(promptCounts, behaviorCounts);
  const capabilitySignals = loadCapabilitySignals(summary);
  const goScore = score(capabilitySignals.go);
  const upstreamScore = score(capabilitySignals.upstream);
  const finalGoScore = score(capabilitySignals.final_go);
  const finalUpstreamScore = score(capabilitySignals.final_upstream);
  const rawScoreDelta = rawScore(capabilitySignals.go) - rawScore(capabilitySignals.upstream);
  const finalRawScoreDelta = rawScore(capabilitySignals.final_go) - rawScore(capabilitySignals.final_upstream);
  const scoreDelta = goScore - upstreamScore;
  const finalScoreDelta = finalGoScore - finalUpstreamScore;
  const longOutputContextAdvantage = hasLongOutputContextAdvantage(summary);
  const hasCapabilitySignals = Boolean(
    capabilitySignals.go || capabilitySignals.upstream ||
      capabilitySignals.final_go || capabilitySignals.final_upstream
  );
  const failures = [];

  if (!summary.ok) failures.push("scenario_summary_not_ok");
  if (promptCompare.ok === false) failures.push("prompt_compare_not_ok");
  if (behaviorCompare.ok === false) failures.push("behavior_compare_not_ok");
  if (diffCounts.error > 0) failures.push("compare_error_differences");
  if (hasCapabilitySignals && scoreDelta < 0) failures.push("capability_score_below_upstream");
  if (hasCapabilitySignals && finalScoreDelta < 0) failures.push("final_capability_score_below_upstream");

  return {
    scenario: name,
    ok: failures.length === 0,
    failures,
    summary: summaryPath,
    artifacts: summary.artifacts || {},
    compare: {
      prompt_ok: promptCompare.ok,
      behavior_ok: behaviorCompare.ok,
      differences: diffCounts,
      prompt_warnings: Array.isArray(promptCompare.warnings) ? promptCompare.warnings.length : 0,
      behavior_warnings: Array.isArray(behaviorCompare.warnings) ? behaviorCompare.warnings.length : 0,
    },
    capability_signals: {
      present: hasCapabilitySignals,
      score_delta: scoreDelta,
      final_score_delta: finalScoreDelta,
      raw_score_delta: rawScoreDelta,
      final_raw_score_delta: finalRawScoreDelta,
      go: capabilitySignals.go,
      upstream: capabilitySignals.upstream,
      final_go: capabilitySignals.final_go,
      final_upstream: capabilitySignals.final_upstream,
      go_markers: markers(capabilitySignals.go),
      upstream_markers: markers(capabilitySignals.upstream),
    },
    context_hygiene: {
      long_output_advantage: longOutputContextAdvantage,
      long_output_evidence: summary.long_output_evidence || null,
    },
  };
}

const scenarios = scenarioArgs.map(evaluateScenario);
const totals = scenarios.reduce((acc, scenario) => {
  acc.scenario_count++;
  if (scenario.ok) acc.ok_count++;
  if (
    scenario.capability_signals.score_delta > 0 ||
      scenario.capability_signals.final_score_delta > 0 ||
      scenario.capability_signals.raw_score_delta > 0 ||
      scenario.capability_signals.final_raw_score_delta > 0 ||
      scenario.context_hygiene.long_output_advantage
  ) {
    acc.capability_advantage_count++;
  }
  if (scenario.context_hygiene.long_output_advantage) {
    acc.context_hygiene_advantage_count++;
  }
  if (scenario.capability_signals.score_delta < 0 || scenario.capability_signals.final_score_delta < 0) {
    acc.capability_underperform_count++;
  }
  acc.compare_errors += scenario.compare.differences.error;
  acc.compare_warnings += scenario.compare.differences.warning;
  acc.compare_info += scenario.compare.differences.info;
  return acc;
}, {
  scenario_count: 0,
  ok_count: 0,
  capability_advantage_count: 0,
  capability_underperform_count: 0,
  compare_errors: 0,
  compare_warnings: 0,
  compare_info: 0,
  context_hygiene_advantage_count: 0,
});

const report = {
  ok: scenarios.every(s => s.ok),
  generated_at: new Date().toISOString(),
  totals,
  scenarios,
};

fs.writeFileSync(scorecardPath, JSON.stringify(report, null, 2) + "\n");
console.log(JSON.stringify(report, null, 2));
if (!report.ok) process.exitCode = 1;
EOF_SCORECARD

echo "scorecard=$SCORECARD_JSON"
