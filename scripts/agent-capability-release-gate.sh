#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

OUT_DIR="/tmp/niuma-agent-capability-release-gate-${TIMESTAMP}"
NATIVE_DIR=""
AB_DIR=""
FINAL_REPORT_DIR=""
CWD="$ROOT_DIR"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
PROMPT_PROFILE="claude-compatible"
MODEL=""
MAX_TOKENS="32000"
FINAL_REPORT_MODEL="gpt-5.5"
SKILL_MODEL="claude-sonnet-4-6"
FINAL_REPORT_MAX_TOKENS="16000"
VERIFY_ONLY="false"
FORCE="false"
RUN_FINAL_REPORT_QUALITY="false"
REQUIRE_FINAL_REPORT="false"
FINAL_REPORT_SCORE=""
FINAL_REPORT_DUMP=""
FINAL_REPORT_RUN_LOG=""
TUI_VISIBILITY_DIR=""
TUI_VISIBILITY_REPORT=""
RUN_TUI_VISIBILITY="false"
REQUIRE_TUI_VISIBILITY="false"
MIN_AB_SCENARIOS="3"
MIN_AB_ADVANTAGES="2"
AB_PROFILE="quick"
AB_SCENARIOS_SET="false"
MIN_AB_SCENARIOS_SET="false"
MIN_AB_ADVANTAGES_SET="false"
APG_REPORTS=()
REQUIRE_APG="false"
REQUIRE_APG_SUITES=""
MIN_APG_PASS_RATE="1"
APG_COMPARE_REPORTS=()
REQUIRE_APG_COMPARE="false"
APG_TARGET_AGENT="niuma-local"
APG_BASELINE_AGENTS=""
MIN_APG_COMPARE_CASES="0"
MIN_APG_STATUS_WIN_DELTA="0"
MIN_APG_EFFICIENCY_ADVANTAGES="0"
APG_STABILITY_REPORTS=()
REQUIRE_APG_STABILITY="false"
MIN_APG_STABILITY_ATTEMPTS="2"
MIN_APG_STABILITY_PASS_RATE="1"
APG_BASELINE_UNDERPERFORMANCE_REPORTS=()
REQUIRE_APG_BASELINE_UNDERPERFORMANCE="false"
APG_BASELINE_UNDERPERFORMANCE_AGENTS=""
MIN_APG_BASELINE_UNDERPERFORMANCE_ATTEMPTS="2"
MAX_APG_BASELINE_UNDERPERFORMANCE_PASS_RATE="0.5"

NATIVE_SCENARIOS="task-capability-loop,agent-capability-loop,agent-capability-loop-resume,agent-capability-loop-compact,agent-capability-loop-compact-facts,agent-capability-loop-failed-compact,agent-capability-loop-cancelled-compact,agent-long-output-compact-resume,agent-detached-running-resume,agent-message-worktree-resume,resume-replacement,tool-result-heavy,read-tool-result"
QUICK_AB_SCENARIOS="agent-long-output-side-by-side,agent-child-error-side-by-side,agent-cancelled-side-by-side"
EXPANDED_AB_SCENARIOS="${QUICK_AB_SCENARIOS},skill-compact-side-by-side"
FULL_AB_SCENARIOS="${EXPANDED_AB_SCENARIOS},skill-load-side-by-side,final-report-side-by-side,agent-message-resume-basic-side-by-side,agent-message-resume-side-by-side"
AB_SCENARIOS="$QUICK_AB_SCENARIOS"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-capability-release-gate.sh [flags]

Runs the release-level Agent Capability gate:
  1. native prompt acceptance matrix
  2. Go-vs-original Claude Code A/B capability matrix
  3. combined agent-capability-scorecard

Use --verify-only to re-check an existing --out-dir without model calls.

Flags:
  --out-dir <path>             Gate artifact directory. Default:
                               /tmp/niuma-agent-capability-release-gate-<timestamp>.
  --native-dir <path>          Native matrix artifact directory. Default: <out-dir>/native.
  --ab-dir <path>              A/B matrix artifact directory. Default: <out-dir>/ab.
  --final-report-dir <path>    Final-report quality artifact directory. Default:
                               <out-dir>/final-report-quality.
  --cwd <path>                 Workspace cwd for niuma scenarios. Default: repo root.
  --upstream-dir <path>        Original Claude Code source/extract dir.
  --prompt-profile <name>      niuma prompt profile. Default: claude-compatible.
  --model <name>               Optional native matrix model override.
  --max-tokens <n>             niuma max output tokens for A/B scenarios. Default: 32000.
  --final-report-model <name>  Model for final-report side-by-side if included.
  --final-report-max-tokens <n>
                               Max output tokens for --run-final-report-quality.
                               Default: 16000.
  --skill-model <name>         Model for skill-load side-by-side if included.
  --native-scenarios <csv>     Native matrix scenarios.
  --ab-profile <quick|expanded|full>
                               A/B scenario profile. quick is the default Agent
                               triad. expanded also requires skill-compact.
                               full also requires skill-load, final-report,
                               AgentMessage basic resume, and AgentMessage
                               worktree resume side-by-side scenarios.
  --ab-scenarios <csv>         A/B matrix scenarios.
  --min-ab-scenarios <n>       Minimum A/B scenario count. Default: 3.
  --min-ab-advantages <n>      Minimum capability/context advantage count. Default: 2.
  --run-final-report-quality   Run or verify final-report quality acceptance and
                               feed its score into the combined scorecard.
  --final-report-score <path>  Optional final-report evidence score JSON.
  --final-report-dump <path>   Prompt dump path for --run-final-report-quality.
  --final-report-run-log <path>
                               Run log path for --run-final-report-quality.
  --require-final-report       Require --final-report-score to be present and ok.
  --run-tui-visibility         Run or verify the TUI visibility acceptance gate.
  --tui-visibility-dir <path>  TUI visibility artifact directory. Default:
                               <out-dir>/tui-visibility.
  --tui-visibility-report <path>
                               TUI visibility JSON report path.
  --require-tui-visibility     Require --tui-visibility-report to be present and ok.
  --apg-report <path>          Agent Proving Ground report JSON to include in the
                               release gate. May be passed multiple times.
  --require-apg                Fail if no APG reports are provided or accepted.
  --require-apg-suites <csv>   Required APG suite_id values.
  --min-apg-pass-rate <float>  Minimum APG aggregate pass rate. Default: 1.
  --apg-compare <path>         Agent Proving Ground compare JSON to include in
                               the release gate. May be passed multiple times.
  --require-apg-compare        Fail if no APG compare reports are provided.
  --apg-target-agent <id>      Target agent id in APG compare reports. Default:
                               niuma-local.
  --apg-baseline-agents <csv>  Baseline agent ids. Default: all non-target agents.
  --min-apg-compare-cases <n>  Minimum APG compare case count. Default: 0.
  --min-apg-status-win-delta <n>
                               Minimum target status wins minus losses. Default: 0.
  --min-apg-efficiency-advantages <n>
                               Minimum cases where target and baseline both pass
                               and target duration is lower. Default: 0.
  --apg-stability-report <path>
                               APG repeat report JSON to include in stability
                               checks. May be passed multiple times.
  --require-apg-stability      Fail if no APG stability reports are provided.
  --min-apg-stability-attempts <n>
                               Minimum attempts per repeated case. Default: 2.
  --min-apg-stability-pass-rate <float>
                               Minimum aggregate repeat pass rate. Default: 1.
  --apg-baseline-underperformance-report <path>
                               APG repeat report proving a baseline agent still
                               underperforms. May be passed multiple times.
  --require-apg-baseline-underperformance
                               Fail if no baseline-underperformance reports are
                               provided.
  --apg-baseline-underperformance-agents <csv>
                               Expected baseline agent ids for underperformance
                               reports. Default: any baseline agent.
  --min-apg-baseline-underperformance-attempts <n>
                               Minimum attempts per repeated baseline case.
                               Default: 2.
  --max-apg-baseline-underperformance-pass-rate <float>
                               Maximum aggregate pass rate for underperforming
                               baseline reports. Default: 0.5.
  --verify-only                Verify existing artifacts under --out-dir without rerunning scenarios.
  --force                      Remove existing --out-dir before a normal run.
  -h, --help                   Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      OUT_DIR="${2:?missing value for --out-dir}"
      shift 2
      ;;
    --native-dir)
      NATIVE_DIR="${2:?missing value for --native-dir}"
      shift 2
      ;;
    --ab-dir)
      AB_DIR="${2:?missing value for --ab-dir}"
      shift 2
      ;;
    --final-report-dir)
      FINAL_REPORT_DIR="${2:?missing value for --final-report-dir}"
      shift 2
      ;;
    --cwd)
      CWD="${2:?missing value for --cwd}"
      shift 2
      ;;
    --upstream-dir)
      UPSTREAM_DIR="${2:?missing value for --upstream-dir}"
      shift 2
      ;;
    --prompt-profile)
      PROMPT_PROFILE="${2:?missing value for --prompt-profile}"
      shift 2
      ;;
    --model)
      MODEL="${2:?missing value for --model}"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="${2:?missing value for --max-tokens}"
      shift 2
      ;;
    --final-report-model)
      FINAL_REPORT_MODEL="${2:?missing value for --final-report-model}"
      shift 2
      ;;
    --final-report-max-tokens)
      FINAL_REPORT_MAX_TOKENS="${2:?missing value for --final-report-max-tokens}"
      shift 2
      ;;
    --skill-model)
      SKILL_MODEL="${2:?missing value for --skill-model}"
      shift 2
      ;;
    --native-scenarios)
      NATIVE_SCENARIOS="${2:?missing value for --native-scenarios}"
      shift 2
      ;;
    --ab-profile)
      AB_PROFILE="${2:?missing value for --ab-profile}"
      shift 2
      ;;
    --ab-scenarios)
      AB_SCENARIOS="${2:?missing value for --ab-scenarios}"
      AB_SCENARIOS_SET="true"
      shift 2
      ;;
    --min-ab-scenarios)
      MIN_AB_SCENARIOS="${2:?missing value for --min-ab-scenarios}"
      MIN_AB_SCENARIOS_SET="true"
      shift 2
      ;;
    --min-ab-advantages)
      MIN_AB_ADVANTAGES="${2:?missing value for --min-ab-advantages}"
      MIN_AB_ADVANTAGES_SET="true"
      shift 2
      ;;
    --run-final-report-quality)
      RUN_FINAL_REPORT_QUALITY="true"
      REQUIRE_FINAL_REPORT="true"
      shift
      ;;
    --final-report-score)
      FINAL_REPORT_SCORE="${2:?missing value for --final-report-score}"
      shift 2
      ;;
    --final-report-dump)
      FINAL_REPORT_DUMP="${2:?missing value for --final-report-dump}"
      shift 2
      ;;
    --final-report-run-log)
      FINAL_REPORT_RUN_LOG="${2:?missing value for --final-report-run-log}"
      shift 2
      ;;
    --require-final-report)
      REQUIRE_FINAL_REPORT="true"
      shift
      ;;
    --run-tui-visibility)
      RUN_TUI_VISIBILITY="true"
      REQUIRE_TUI_VISIBILITY="true"
      shift
      ;;
    --tui-visibility-dir)
      TUI_VISIBILITY_DIR="${2:?missing value for --tui-visibility-dir}"
      shift 2
      ;;
    --tui-visibility-report)
      TUI_VISIBILITY_REPORT="${2:?missing value for --tui-visibility-report}"
      shift 2
      ;;
    --require-tui-visibility)
      REQUIRE_TUI_VISIBILITY="true"
      shift
      ;;
    --apg-report)
      APG_REPORTS+=("${2:?missing value for --apg-report}")
      shift 2
      ;;
    --require-apg)
      REQUIRE_APG="true"
      shift
      ;;
    --require-apg-suites)
      REQUIRE_APG_SUITES="${2:?missing value for --require-apg-suites}"
      shift 2
      ;;
    --min-apg-pass-rate)
      MIN_APG_PASS_RATE="${2:?missing value for --min-apg-pass-rate}"
      shift 2
      ;;
    --apg-compare)
      APG_COMPARE_REPORTS+=("${2:?missing value for --apg-compare}")
      shift 2
      ;;
    --require-apg-compare)
      REQUIRE_APG_COMPARE="true"
      shift
      ;;
    --apg-target-agent)
      APG_TARGET_AGENT="${2:?missing value for --apg-target-agent}"
      shift 2
      ;;
    --apg-baseline-agents)
      APG_BASELINE_AGENTS="${2:?missing value for --apg-baseline-agents}"
      shift 2
      ;;
    --min-apg-compare-cases)
      MIN_APG_COMPARE_CASES="${2:?missing value for --min-apg-compare-cases}"
      shift 2
      ;;
    --min-apg-status-win-delta)
      MIN_APG_STATUS_WIN_DELTA="${2:?missing value for --min-apg-status-win-delta}"
      shift 2
      ;;
    --min-apg-efficiency-advantages)
      MIN_APG_EFFICIENCY_ADVANTAGES="${2:?missing value for --min-apg-efficiency-advantages}"
      shift 2
      ;;
    --apg-stability-report)
      APG_STABILITY_REPORTS+=("${2:?missing value for --apg-stability-report}")
      shift 2
      ;;
    --require-apg-stability)
      REQUIRE_APG_STABILITY="true"
      shift
      ;;
    --min-apg-stability-attempts)
      MIN_APG_STABILITY_ATTEMPTS="${2:?missing value for --min-apg-stability-attempts}"
      shift 2
      ;;
    --min-apg-stability-pass-rate)
      MIN_APG_STABILITY_PASS_RATE="${2:?missing value for --min-apg-stability-pass-rate}"
      shift 2
      ;;
    --apg-baseline-underperformance-report)
      APG_BASELINE_UNDERPERFORMANCE_REPORTS+=("${2:?missing value for --apg-baseline-underperformance-report}")
      shift 2
      ;;
    --require-apg-baseline-underperformance)
      REQUIRE_APG_BASELINE_UNDERPERFORMANCE="true"
      shift
      ;;
    --apg-baseline-underperformance-agents)
      APG_BASELINE_UNDERPERFORMANCE_AGENTS="${2:?missing value for --apg-baseline-underperformance-agents}"
      shift 2
      ;;
    --min-apg-baseline-underperformance-attempts)
      MIN_APG_BASELINE_UNDERPERFORMANCE_ATTEMPTS="${2:?missing value for --min-apg-baseline-underperformance-attempts}"
      shift 2
      ;;
    --max-apg-baseline-underperformance-pass-rate)
      MAX_APG_BASELINE_UNDERPERFORMANCE_PASS_RATE="${2:?missing value for --max-apg-baseline-underperformance-pass-rate}"
      shift 2
      ;;
    --verify-only)
      VERIFY_ONLY="true"
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

if [[ "$VERIFY_ONLY" == "true" && "$FORCE" == "true" ]]; then
  echo "--verify-only and --force cannot be combined" >&2
  exit 2
fi

case "$AB_PROFILE" in
  quick)
    if [[ "$AB_SCENARIOS_SET" != "true" ]]; then
      AB_SCENARIOS="$QUICK_AB_SCENARIOS"
    fi
    ;;
  expanded)
    if [[ "$AB_SCENARIOS_SET" != "true" ]]; then
      AB_SCENARIOS="$EXPANDED_AB_SCENARIOS"
    fi
    if [[ "$MIN_AB_SCENARIOS_SET" != "true" ]]; then
      MIN_AB_SCENARIOS="4"
    fi
    if [[ "$MIN_AB_ADVANTAGES_SET" != "true" ]]; then
      MIN_AB_ADVANTAGES="3"
    fi
    ;;
  full)
    if [[ "$AB_SCENARIOS_SET" != "true" ]]; then
      AB_SCENARIOS="$FULL_AB_SCENARIOS"
    fi
    if [[ "$MIN_AB_SCENARIOS_SET" != "true" ]]; then
      MIN_AB_SCENARIOS="8"
    fi
    if [[ "$MIN_AB_ADVANTAGES_SET" != "true" ]]; then
      MIN_AB_ADVANTAGES="4"
    fi
    ;;
  *)
    echo "unknown --ab-profile: $AB_PROFILE (supported: quick, expanded, full)" >&2
    exit 2
    ;;
esac

if [[ -e "$OUT_DIR" && "$VERIFY_ONLY" != "true" && "$FORCE" != "true" ]]; then
  echo "out dir already exists: $OUT_DIR (use --force or choose another --out-dir)" >&2
  exit 2
fi
if [[ -e "$OUT_DIR" && "$VERIFY_ONLY" != "true" && "$FORCE" == "true" ]]; then
  rm -rf "$OUT_DIR"
fi

if [[ -z "$NATIVE_DIR" ]]; then
  NATIVE_DIR="$OUT_DIR/native"
fi
if [[ -z "$AB_DIR" ]]; then
  AB_DIR="$OUT_DIR/ab"
fi
if [[ -z "$FINAL_REPORT_DIR" ]]; then
  FINAL_REPORT_DIR="$OUT_DIR/final-report-quality"
fi
if [[ -z "$TUI_VISIBILITY_DIR" ]]; then
  TUI_VISIBILITY_DIR="$OUT_DIR/tui-visibility"
fi
if [[ "$RUN_FINAL_REPORT_QUALITY" == "true" ]]; then
  if [[ -z "$FINAL_REPORT_DUMP" ]]; then
    FINAL_REPORT_DUMP="$FINAL_REPORT_DIR/prompt-dump.jsonl"
  fi
  if [[ -z "$FINAL_REPORT_RUN_LOG" ]]; then
    FINAL_REPORT_RUN_LOG="$FINAL_REPORT_DIR/run.log"
  fi
  if [[ -z "$FINAL_REPORT_SCORE" ]]; then
    FINAL_REPORT_SCORE="$FINAL_REPORT_DIR/score.json"
  fi
fi
if [[ "$RUN_TUI_VISIBILITY" == "true" || "$REQUIRE_TUI_VISIBILITY" == "true" ]]; then
  if [[ -z "$TUI_VISIBILITY_REPORT" ]]; then
    TUI_VISIBILITY_REPORT="$TUI_VISIBILITY_DIR/tui-acceptance-report.json"
  fi
fi
SCORECARD_JSON="$OUT_DIR/agent-capability-scorecard.json"
REPORT_JSON="$OUT_DIR/release-gate-report.json"

mkdir -p "$OUT_DIR"

run_native_matrix() {
  local -a cmd=(
    "$ROOT_DIR/scripts/prompt-acceptance-matrix.sh"
    --scenarios "$NATIVE_SCENARIOS"
    --out-dir "$NATIVE_DIR"
    --cwd "$CWD"
    --prompt-profile "$PROMPT_PROFILE"
  )
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "release gate: native prompt matrix -> $NATIVE_DIR" >&2
  "${cmd[@]}"
}

run_ab_matrix() {
  local -a cmd=(
    "$ROOT_DIR/scripts/capability-ab-eval-matrix.sh"
    --scenarios "$AB_SCENARIOS"
    --out-dir "$AB_DIR"
    --upstream-dir "$UPSTREAM_DIR"
    --target-cwd "$CWD"
    --prompt-profile "$PROMPT_PROFILE"
    --max-tokens "$MAX_TOKENS"
    --final-report-model "$FINAL_REPORT_MODEL"
    --skill-model "$SKILL_MODEL"
  )
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "release gate: A/B capability matrix -> $AB_DIR" >&2
  "${cmd[@]}"
}

run_final_report_quality() {
  mkdir -p "$FINAL_REPORT_DIR"
  local -a cmd=(
    "$ROOT_DIR/scripts/final-report-quality-prompt-acceptance.sh"
    --dump "$FINAL_REPORT_DUMP"
    --run-log "$FINAL_REPORT_RUN_LOG"
    --score-report "$FINAL_REPORT_SCORE"
    --prompt-profile "$PROMPT_PROFILE"
    --max-tokens "$FINAL_REPORT_MAX_TOKENS"
  )
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "release gate: final-report quality -> $FINAL_REPORT_SCORE" >&2
  "${cmd[@]}"
}

run_tui_visibility() {
  mkdir -p "$TUI_VISIBILITY_DIR"
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$TUI_VISIBILITY_REPORT" ]]; then
      echo "TUI visibility report missing: $TUI_VISIBILITY_REPORT" >&2
      exit 1
    fi
    return
  fi
  echo "release gate: TUI visibility -> $TUI_VISIBILITY_REPORT" >&2
  TUI_TOOL_PROGRESS_ACCEPTANCE_REPORT_DIR="$TUI_VISIBILITY_DIR" \
  TUI_TOOL_PROGRESS_ACCEPTANCE_REPORT_JSON="$TUI_VISIBILITY_REPORT" \
  TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=prepare \
  TUI_TOOL_PROGRESS_ACCEPTANCE_TESTS=1 \
  TUI_TOOL_PROGRESS_ACCEPTANCE_FULL_TESTS=0 \
    "$ROOT_DIR/scripts/tui-tool-progress-acceptance.sh"
}

run_scorecard() {
  local -a cmd=(
    go run ./scripts/agent-capability-scorecard
    --prompt-matrix "$NATIVE_DIR/matrix-report.json"
    --ab-scorecard "$AB_DIR/scorecard.json"
    --require-ab
    --require-ab-scenarios "$AB_SCENARIOS"
    --min-ab-scenarios "$MIN_AB_SCENARIOS"
    --min-ab-advantages "$MIN_AB_ADVANTAGES"
    --out "$SCORECARD_JSON"
  )
  if [[ -n "$FINAL_REPORT_SCORE" ]]; then
    cmd+=(--final-report-score "$FINAL_REPORT_SCORE")
  fi
  if [[ "$REQUIRE_FINAL_REPORT" == "true" ]]; then
    cmd+=(--require-final-report)
  fi

  echo "release gate: combined scorecard -> $SCORECARD_JSON" >&2
  "${cmd[@]}"
}

join_csv() {
  local IFS=,
  echo "$*"
}

write_release_report() {
  local apg_stability_csv=""
  if [[ ${#APG_STABILITY_REPORTS[@]} -gt 0 ]]; then
    apg_stability_csv="$(join_csv "${APG_STABILITY_REPORTS[@]}")"
  fi
  local apg_baseline_underperformance_csv=""
  if [[ ${#APG_BASELINE_UNDERPERFORMANCE_REPORTS[@]} -gt 0 ]]; then
    apg_baseline_underperformance_csv="$(join_csv "${APG_BASELINE_UNDERPERFORMANCE_REPORTS[@]}")"
  fi
  local -a cmd=(
    node -
    "$REPORT_JSON"
    "$OUT_DIR"
    "$NATIVE_DIR/matrix-report.json"
    "$AB_DIR/scorecard.json"
    "$SCORECARD_JSON"
    "$VERIFY_ONLY"
    "$FINAL_REPORT_SCORE"
    "$AB_PROFILE"
    "$AB_SCENARIOS"
    "$MIN_AB_SCENARIOS"
    "$MIN_AB_ADVANTAGES"
    "$REQUIRE_APG"
    "$REQUIRE_APG_SUITES"
    "$MIN_APG_PASS_RATE"
    "$REQUIRE_APG_COMPARE"
    "$APG_TARGET_AGENT"
    "$APG_BASELINE_AGENTS"
    "$MIN_APG_COMPARE_CASES"
    "$MIN_APG_STATUS_WIN_DELTA"
    "$MIN_APG_EFFICIENCY_ADVANTAGES"
    "$REQUIRE_TUI_VISIBILITY"
    "$TUI_VISIBILITY_REPORT"
    "$REQUIRE_APG_STABILITY"
    "$MIN_APG_STABILITY_ATTEMPTS"
    "$MIN_APG_STABILITY_PASS_RATE"
    "$apg_stability_csv"
    "$REQUIRE_APG_BASELINE_UNDERPERFORMANCE"
    "$APG_BASELINE_UNDERPERFORMANCE_AGENTS"
    "$MIN_APG_BASELINE_UNDERPERFORMANCE_ATTEMPTS"
    "$MAX_APG_BASELINE_UNDERPERFORMANCE_PASS_RATE"
    "$apg_baseline_underperformance_csv"
  )
  if [[ ${#APG_REPORTS[@]} -gt 0 ]]; then
    cmd+=("${APG_REPORTS[@]}")
  fi
  cmd+=("--")
  if [[ ${#APG_COMPARE_REPORTS[@]} -gt 0 ]]; then
    cmd+=("${APG_COMPARE_REPORTS[@]}")
  fi

  "${cmd[@]}" <<'EOF_REPORT'
const fs = require("fs");
const [
  reportPath,
  outDir,
  nativeMatrix,
  abScorecard,
  combinedScorecard,
  verifyOnly,
  finalReportScore,
  abProfile,
  abScenarios,
  minAbScenarios,
  minAbAdvantages,
  requireApg,
  requireApgSuites,
  minApgPassRate,
  requireApgCompare,
  apgTargetAgent,
  apgBaselineAgents,
  minApgCompareCases,
  minApgStatusWinDelta,
  minApgEfficiencyAdvantages,
  requireTuiVisibility,
  tuiVisibilityReport,
  requireApgStability,
  minApgStabilityAttempts,
  minApgStabilityPassRate,
  apgStabilityReportsCSV,
  requireApgBaselineUnderperformance,
  apgBaselineUnderperformanceAgents,
  minApgBaselineUnderperformanceAttempts,
  maxApgBaselineUnderperformancePassRate,
  apgBaselineUnderperformanceReportsCSV,
  ...rawApgPaths
] = process.argv.slice(2);

function readJson(path) {
  return JSON.parse(fs.readFileSync(path, "utf8"));
}

function toRequiredSet(csv) {
  return new Set(String(csv || "").split(",").map(s => s.trim()).filter(Boolean));
}

const firstSep = rawApgPaths.indexOf("--");
const apgReportPaths = firstSep >= 0 ? rawApgPaths.slice(0, firstSep) : rawApgPaths;
const apgComparePaths = firstSep >= 0 ? rawApgPaths.slice(firstSep + 1) : [];
const apgStabilityPaths = String(apgStabilityReportsCSV || "").split(",").map(s => s.trim()).filter(Boolean);
const apgBaselineUnderperformancePaths = String(apgBaselineUnderperformanceReportsCSV || "").split(",").map(s => s.trim()).filter(Boolean);

function summarizeApgReports(paths) {
  const requiredSuites = toRequiredSet(requireApgSuites);
  const minPassRate = Number(minApgPassRate);
  const failures = [];
  const warnings = [];
  const reports = [];
  let total = 0;
  let passed = 0;
  let failed = 0;
  let skipped = 0;

  if (requireApg === "true" && paths.length === 0) {
    failures.push("apg_required_but_no_reports");
  }

  for (const reportPath of paths) {
    let report;
    try {
      report = readJson(reportPath);
    } catch (error) {
      failures.push(`apg_report_unreadable:${reportPath}:${error.message}`);
      continue;
    }

    const suiteId = report.suite_id || "";
    const status = report.status || "";
    const agent = report.agent_profile || {};
    const item = {
      path: reportPath,
      schema_version: report.schema_version || "",
      suite_id: suiteId,
      run_id: report.run_id || "",
      status,
      agent_id: agent.id || "",
      adapter: agent.adapter || "",
      total: Number(report.total || 0),
      passed: Number(report.passed || 0),
      failed: Number(report.failed || 0),
      skipped: Number(report.skipped || 0),
    };
    reports.push(item);

    if (item.schema_version !== "agent-proving-ground/report/v1") {
      failures.push(`apg_schema_mismatch:${reportPath}:${item.schema_version || "missing"}`);
    }
    if (item.adapter && item.adapter !== "niuma") {
      failures.push(`apg_report_not_golang_cc_adapter:${reportPath}:${item.adapter}`);
    }
    if (item.agent_id && !String(item.agent_id).includes("niuma")) {
      warnings.push(`apg_agent_id_not_golang_cc:${reportPath}:${item.agent_id}`);
    }
    if (status !== "passed") {
      failures.push(`apg_report_status_not_passed:${reportPath}:${status || "missing"}`);
    }
    if (item.failed > 0) {
      failures.push(`apg_report_has_failures:${reportPath}:${item.failed}`);
    }

    total += item.total;
    passed += item.passed;
    failed += item.failed;
    skipped += item.skipped;
  }

  const seenSuites = new Set(reports.map(r => r.suite_id).filter(Boolean));
  const missingRequiredSuites = Array.from(requiredSuites).filter(suite => !seenSuites.has(suite));
  for (const suite of missingRequiredSuites) {
    failures.push(`apg_required_suite_missing:${suite}`);
  }

  const passRate = total > 0 ? passed / total : 0;
  if ((paths.length > 0 || requireApg === "true") && Number.isFinite(minPassRate) && passRate < minPassRate) {
    failures.push(`apg_pass_rate_below_min:${passRate.toFixed(4)}<${minPassRate}`);
  }

  return {
    ok: failures.length === 0,
    required: requireApg === "true",
    required_suites: Array.from(requiredSuites),
    min_pass_rate: Number.isFinite(minPassRate) ? minPassRate : null,
    report_count: reports.length,
    total,
    passed,
    failed,
    skipped,
    pass_rate: passRate,
    reports,
    missing_required_suites: missingRequiredSuites,
    warnings,
    failures,
  };
}

function statusRank(status) {
  switch (status) {
    case "passed":
      return 3;
    case "skipped":
    case "skipped_unsupported":
      return 2;
    case "failed":
      return 1;
    default:
      return 0;
  }
}

function resultTokens(result) {
  return Number(result.input_tokens || 0) + Number(result.output_tokens || 0);
}

function hasOwnField(value, field) {
  return Boolean(value) && Object.prototype.hasOwnProperty.call(value, field);
}

function summarizeApgCompares(paths) {
  const baselineFilter = toRequiredSet(apgBaselineAgents);
  const minCases = Number(minApgCompareCases);
  const minWinDelta = Number(minApgStatusWinDelta);
  const minEfficiencyAdvantages = Number(minApgEfficiencyAdvantages);
  const failures = [];
  const warnings = [];
  const reports = [];
  const comparisons = [];
  let caseCount = 0;
  let statusWins = 0;
  let statusLosses = 0;
  let statusTies = 0;
  let efficiencyAdvantages = 0;
  let efficiencyLosses = 0;
  let targetCaseFailures = 0;
  let baselineCaseFailures = 0;
  const baselineSummaries = new Map();
  const usageConfidence = {
    total_report_summaries: 0,
    total_case_results: 0,
    missing_report_cost_status: 0,
    missing_report_usage_confidence: 0,
    missing_case_cost_status: 0,
    missing_case_usage_confidence: 0,
    missing_case_model: 0,
    cost_status_counts: {},
    usage_confidence_counts: {},
    model_counts: {},
    case_model_counts: {},
    unknown_model_reports: 0,
    unknown_model_case_results: 0,
  };

  if (requireApgCompare === "true" && paths.length === 0) {
    failures.push("apg_compare_required_but_no_reports");
  }

  for (const comparePath of paths) {
    let compare;
    try {
      compare = readJson(comparePath);
    } catch (error) {
      failures.push(`apg_compare_unreadable:${comparePath}:${error.message}`);
      continue;
    }

    const item = {
      path: comparePath,
      schema_version: compare.schema_version || "",
      status: compare.status || "",
      report_count: Array.isArray(compare.reports) ? compare.reports.length : 0,
      case_count: Array.isArray(compare.cases) ? compare.cases.length : 0,
      target_agent: apgTargetAgent,
      baseline_agents: [],
      target_case_failures: 0,
      baseline_case_failures: 0,
    };
    reports.push(item);

    if (item.schema_version !== "agent-proving-ground/compare/v1") {
      failures.push(`apg_compare_schema_mismatch:${comparePath}:${item.schema_version || "missing"}`);
    }
    if (item.status && item.status !== "passed") {
      warnings.push(`apg_compare_report_status_not_passed:${comparePath}:${item.status}`);
    }
    for (const summary of compare.reports || []) {
      usageConfidence.total_report_summaries += 1;
      if (!hasOwnField(summary, "cost_status")) {
        usageConfidence.missing_report_cost_status += 1;
      }
      if (!hasOwnField(summary, "usage_confidence")) {
        usageConfidence.missing_report_usage_confidence += 1;
      }
      const costStatus = summary.cost_status || "missing";
      const confidence = summary.usage_confidence || "missing";
      const model = summary.model || "missing";
      usageConfidence.cost_status_counts[costStatus] = (usageConfidence.cost_status_counts[costStatus] || 0) + 1;
      usageConfidence.usage_confidence_counts[confidence] = (usageConfidence.usage_confidence_counts[confidence] || 0) + 1;
      usageConfidence.model_counts[model] = (usageConfidence.model_counts[model] || 0) + 1;
      if (model === "missing" || model === "configured-provider") {
        usageConfidence.unknown_model_reports += 1;
      }
    }

    const reportAgents = new Set((compare.reports || []).map(r => r.agent_id).filter(Boolean));
    if (!reportAgents.has(apgTargetAgent)) {
      failures.push(`apg_compare_target_agent_missing:${comparePath}:${apgTargetAgent}`);
      continue;
    }

    const baselines = Array.from(reportAgents).filter(agent => {
      if (agent === apgTargetAgent) return false;
      return baselineFilter.size === 0 || baselineFilter.has(agent);
    });
    item.baseline_agents = baselines;
    if (baselines.length === 0) {
      failures.push(`apg_compare_baseline_agent_missing:${comparePath}`);
      continue;
    }

    for (const testCase of compare.cases || []) {
      const target = (testCase.results || []).find(result => result.agent_id === apgTargetAgent);
      if (!target) {
        failures.push(`apg_compare_case_target_missing:${comparePath}:${testCase.track_id || ""}/${testCase.id || ""}`);
        continue;
      }
      if (target.status !== "passed") {
        targetCaseFailures += 1;
        item.target_case_failures += 1;
        failures.push(`apg_compare_target_case_not_passed:${comparePath}:${testCase.track_id || ""}/${testCase.id || ""}:${apgTargetAgent}=${target.status || "missing"}`);
      }

      for (const baselineAgent of baselines) {
        const baseline = (testCase.results || []).find(result => result.agent_id === baselineAgent);
        if (!baseline) {
          warnings.push(`apg_compare_case_baseline_missing:${comparePath}:${testCase.track_id || ""}/${testCase.id || ""}:${baselineAgent}`);
          continue;
        }
        for (const result of [target, baseline]) {
          usageConfidence.total_case_results += 1;
          if (!hasOwnField(result, "cost_status")) {
            usageConfidence.missing_case_cost_status += 1;
          }
          if (!hasOwnField(result, "usage_confidence")) {
            usageConfidence.missing_case_usage_confidence += 1;
          }
          if (!hasOwnField(result, "model")) {
            usageConfidence.missing_case_model += 1;
          }
          const costStatus = result.cost_status || "missing";
          const confidence = result.usage_confidence || "missing";
          const resultModel = result.model || "missing";
          usageConfidence.cost_status_counts[costStatus] = (usageConfidence.cost_status_counts[costStatus] || 0) + 1;
          usageConfidence.usage_confidence_counts[confidence] = (usageConfidence.usage_confidence_counts[confidence] || 0) + 1;
          usageConfidence.case_model_counts[resultModel] = (usageConfidence.case_model_counts[resultModel] || 0) + 1;
          if (resultModel === "missing" || resultModel === "configured-provider") {
            usageConfidence.unknown_model_case_results += 1;
          }
        }
        if (!baselineSummaries.has(baselineAgent)) {
          baselineSummaries.set(baselineAgent, {
            baseline_agent: baselineAgent,
            case_count: 0,
            status_wins: 0,
            status_losses: 0,
            status_ties: 0,
            status_win_delta: 0,
            efficiency_advantages: 0,
            efficiency_losses: 0,
            efficiency_delta: 0,
            target_case_failures: 0,
            baseline_case_failures: 0,
          });
        }
        const baselineSummary = baselineSummaries.get(baselineAgent);
        if (baseline.status !== "passed") {
          baselineCaseFailures += 1;
          item.baseline_case_failures += 1;
          baselineSummary.baseline_case_failures += 1;
        }

        caseCount += 1;
        baselineSummary.case_count += 1;
        if (target.status !== "passed") {
          baselineSummary.target_case_failures += 1;
        }
        const targetRank = statusRank(target.status);
        const baselineRank = statusRank(baseline.status);
        let status_delta = 0;
        if (targetRank > baselineRank) {
          statusWins += 1;
          baselineSummary.status_wins += 1;
          status_delta = 1;
        } else if (targetRank < baselineRank) {
          statusLosses += 1;
          baselineSummary.status_losses += 1;
          status_delta = -1;
          failures.push(`apg_compare_status_underperform:${comparePath}:${testCase.track_id || ""}/${testCase.id || ""}:${apgTargetAgent}=${target.status || "missing"}<${baselineAgent}=${baseline.status || "missing"}`);
        } else {
          statusTies += 1;
          baselineSummary.status_ties += 1;
        }
        baselineSummary.status_win_delta = baselineSummary.status_wins - baselineSummary.status_losses;

        const targetDuration = Number(target.duration_ms || 0);
        const baselineDuration = Number(baseline.duration_ms || 0);
        const bothPassed = target.status === "passed" && baseline.status === "passed";
        let efficiency_delta = 0;
        if (bothPassed && targetDuration > 0 && baselineDuration > 0) {
          if (targetDuration < baselineDuration) {
            efficiencyAdvantages += 1;
            baselineSummary.efficiency_advantages += 1;
            efficiency_delta = 1;
          } else if (targetDuration > baselineDuration) {
            efficiencyLosses += 1;
            baselineSummary.efficiency_losses += 1;
            efficiency_delta = -1;
          }
        }
        baselineSummary.efficiency_delta = baselineSummary.efficiency_advantages - baselineSummary.efficiency_losses;

        comparisons.push({
          path: comparePath,
          case_id: testCase.id || "",
          track_id: testCase.track_id || "",
          target_agent: apgTargetAgent,
          baseline_agent: baselineAgent,
          target_status: target.status || "",
          baseline_status: baseline.status || "",
          status_delta,
          target_duration_ms: targetDuration,
          baseline_duration_ms: baselineDuration,
          efficiency_delta,
          target_tokens: resultTokens(target),
          baseline_tokens: resultTokens(baseline),
          target_cost_status: target.cost_status || "",
          baseline_cost_status: baseline.cost_status || "",
          target_usage_confidence: target.usage_confidence || "",
          baseline_usage_confidence: baseline.usage_confidence || "",
          target_turns: Number(target.turns || 0),
          baseline_turns: Number(baseline.turns || 0),
          target_tool_calls: Number(target.tool_calls || 0),
          baseline_tool_calls: Number(baseline.tool_calls || 0),
        });
      }
    }
  }

  const statusWinDelta = statusWins - statusLosses;
  if ((paths.length > 0 || requireApgCompare === "true") && Number.isFinite(minCases) && caseCount < minCases) {
    failures.push(`apg_compare_case_count_below_min:${caseCount}<${minCases}`);
  }
  if ((paths.length > 0 || requireApgCompare === "true") && Number.isFinite(minWinDelta) && statusWinDelta < minWinDelta) {
    failures.push(`apg_compare_status_win_delta_below_min:${statusWinDelta}<${minWinDelta}`);
  }
  if ((paths.length > 0 || requireApgCompare === "true") && Number.isFinite(minEfficiencyAdvantages) && efficiencyAdvantages < minEfficiencyAdvantages) {
    failures.push(`apg_compare_efficiency_advantages_below_min:${efficiencyAdvantages}<${minEfficiencyAdvantages}`);
  }
  if (paths.length > 0 || requireApgCompare === "true") {
    if (usageConfidence.missing_report_cost_status > 0) {
      failures.push(`apg_compare_missing_report_cost_status:${usageConfidence.missing_report_cost_status}`);
    }
    if (usageConfidence.missing_report_usage_confidence > 0) {
      failures.push(`apg_compare_missing_report_usage_confidence:${usageConfidence.missing_report_usage_confidence}`);
    }
    if (usageConfidence.missing_case_cost_status > 0) {
      failures.push(`apg_compare_missing_case_cost_status:${usageConfidence.missing_case_cost_status}`);
    }
    if (usageConfidence.missing_case_usage_confidence > 0) {
      failures.push(`apg_compare_missing_case_usage_confidence:${usageConfidence.missing_case_usage_confidence}`);
    }
    if (usageConfidence.missing_case_model > 0) {
      failures.push(`apg_compare_missing_case_model:${usageConfidence.missing_case_model}`);
    }
  }

  return {
    ok: failures.length === 0,
    required: requireApgCompare === "true",
    target_agent: apgTargetAgent,
    baseline_agents: Array.from(baselineFilter),
    min_case_count: Number.isFinite(minCases) ? minCases : null,
    min_status_win_delta: Number.isFinite(minWinDelta) ? minWinDelta : null,
    min_efficiency_advantages: Number.isFinite(minEfficiencyAdvantages) ? minEfficiencyAdvantages : null,
    report_count: reports.length,
    case_count: caseCount,
    status_wins: statusWins,
    status_losses: statusLosses,
    status_ties: statusTies,
    status_win_delta: statusWinDelta,
    efficiency_advantages: efficiencyAdvantages,
    efficiency_losses: efficiencyLosses,
    target_case_failures: targetCaseFailures,
    baseline_case_failures: baselineCaseFailures,
    baseline_summaries: Array.from(baselineSummaries.values()).sort((a, b) => a.baseline_agent.localeCompare(b.baseline_agent)),
    usage_confidence: usageConfidence,
    reports,
    comparisons,
    warnings,
    failures,
  };
}

const scorecard = readJson(combinedScorecard);
const apg = summarizeApgReports(apgReportPaths);
const apg_compare = summarizeApgCompares(apgComparePaths);
function baseCaseID(id) {
  return String(id || "").replace(/#attempt-\d+$/, "");
}
function attemptNumber(id) {
  const match = String(id || "").match(/#attempt-(\d+)$/);
  return match ? Number(match[1]) : 0;
}
function summarizeApgStabilityReports(paths) {
  const required = requireApgStability === "true";
  const minAttempts = Number(minApgStabilityAttempts);
  const minPassRate = Number(minApgStabilityPassRate);
  const failures = [];
  const warnings = [];
  const reports = [];
  const cases = [];
  let total = 0;
  let passed = 0;
  let failed = 0;
  let skipped = 0;

  if (required && paths.length === 0) {
    failures.push("apg_stability_required_but_no_reports");
  }

  for (const reportPath of paths) {
    let report;
    try {
      report = readJson(reportPath);
    } catch (error) {
      failures.push(`apg_stability_unreadable:${reportPath}:${error.message}`);
      continue;
    }
    const item = {
      path: reportPath,
      schema_version: report.schema_version || "",
      suite_id: report.suite_id || "",
      run_id: report.run_id || "",
      status: report.status || "",
      agent_id: report.agent_profile?.id || "",
      total: Number(report.total || 0),
      passed: Number(report.passed || 0),
      failed: Number(report.failed || 0),
      skipped: Number(report.skipped || 0),
    };
    reports.push(item);
    if (item.schema_version !== "agent-proving-ground/report/v1") {
      failures.push(`apg_stability_schema_mismatch:${reportPath}:${item.schema_version || "missing"}`);
    }
    if (item.status !== "passed") {
      failures.push(`apg_stability_status_not_passed:${reportPath}:${item.status || "missing"}`);
    }
    total += item.total;
    passed += item.passed;
    failed += item.failed;
    skipped += item.skipped;

    const grouped = new Map();
    for (const testCase of report.cases || []) {
      const base = baseCaseID(testCase.id);
      const attempt = attemptNumber(testCase.id);
      if (!attempt) {
        warnings.push(`apg_stability_case_without_attempt:${reportPath}:${testCase.track_id || ""}/${testCase.id || ""}`);
      }
      if (!grouped.has(base)) {
        grouped.set(base, { id: base, track_id: testCase.track_id || "", attempts: 0, passed: 0, failed: 0, skipped: 0, max_attempt: 0 });
      }
      const current = grouped.get(base);
      current.attempts += 1;
      current.max_attempt = Math.max(current.max_attempt, attempt);
      switch (testCase.status) {
        case "passed":
          current.passed += 1;
          break;
        case "skipped_unsupported":
          current.skipped += 1;
          break;
        default:
          current.failed += 1;
          break;
      }
    }
    for (const current of grouped.values()) {
      cases.push({ report: reportPath, ...current });
      if (Number.isFinite(minAttempts) && current.attempts < minAttempts) {
        failures.push(`apg_stability_attempts_below_min:${reportPath}:${current.track_id}/${current.id}:${current.attempts}<${minAttempts}`);
      }
      if (current.failed > 0) {
        failures.push(`apg_stability_case_failed:${reportPath}:${current.track_id}/${current.id}:${current.failed}`);
      }
    }
  }

  const passRate = total > 0 ? passed / total : 0;
  if ((paths.length > 0 || required) && Number.isFinite(minPassRate) && passRate < minPassRate) {
    failures.push(`apg_stability_pass_rate_below_min:${passRate.toFixed(4)}<${minPassRate}`);
  }

  return {
    ok: failures.length === 0,
    required,
    min_attempts: Number.isFinite(minAttempts) ? minAttempts : null,
    min_pass_rate: Number.isFinite(minPassRate) ? minPassRate : null,
    report_count: reports.length,
    total,
    passed,
    failed,
    skipped,
    pass_rate: passRate,
    reports,
    cases,
    warnings,
    failures,
  };
}

function summarizeApgBaselineUnderperformanceReports(paths) {
  const required = requireApgBaselineUnderperformance === "true";
  const expectedAgents = toRequiredSet(apgBaselineUnderperformanceAgents);
  const minAttempts = Number(minApgBaselineUnderperformanceAttempts);
  const maxPassRate = Number(maxApgBaselineUnderperformancePassRate);
  const failures = [];
  const warnings = [];
  const reports = [];
  const cases = [];
  let total = 0;
  let passed = 0;
  let failed = 0;
  let skipped = 0;

  if (required && paths.length === 0) {
    failures.push("apg_baseline_underperformance_required_but_no_reports");
  }

  for (const reportPath of paths) {
    let report;
    try {
      report = readJson(reportPath);
    } catch (error) {
      failures.push(`apg_baseline_underperformance_unreadable:${reportPath}:${error.message}`);
      continue;
    }
    const item = {
      path: reportPath,
      schema_version: report.schema_version || "",
      suite_id: report.suite_id || "",
      run_id: report.run_id || "",
      status: report.status || "",
      agent_id: report.agent_profile?.id || "",
      total: Number(report.total || 0),
      passed: Number(report.passed || 0),
      failed: Number(report.failed || 0),
      skipped: Number(report.skipped || 0),
    };
    reports.push(item);
    if (item.schema_version !== "agent-proving-ground/report/v1") {
      failures.push(`apg_baseline_underperformance_schema_mismatch:${reportPath}:${item.schema_version || "missing"}`);
    }
    if (expectedAgents.size > 0 && !expectedAgents.has(item.agent_id)) {
      failures.push(`apg_baseline_underperformance_agent_unexpected:${reportPath}:${item.agent_id || "missing"}`);
    }
    if (item.failed <= 0) {
      failures.push(`apg_baseline_underperformance_no_failures:${reportPath}`);
    }
    total += item.total;
    passed += item.passed;
    failed += item.failed;
    skipped += item.skipped;

    const grouped = new Map();
    for (const testCase of report.cases || []) {
      const base = baseCaseID(testCase.id);
      const attempt = attemptNumber(testCase.id);
      if (!attempt) {
        warnings.push(`apg_baseline_underperformance_case_without_attempt:${reportPath}:${testCase.track_id || ""}/${testCase.id || ""}`);
      }
      if (!grouped.has(base)) {
        grouped.set(base, { id: base, track_id: testCase.track_id || "", attempts: 0, passed: 0, failed: 0, skipped: 0, max_attempt: 0 });
      }
      const current = grouped.get(base);
      current.attempts += 1;
      current.max_attempt = Math.max(current.max_attempt, attempt);
      switch (testCase.status) {
        case "passed":
          current.passed += 1;
          break;
        case "skipped_unsupported":
          current.skipped += 1;
          break;
        default:
          current.failed += 1;
          break;
      }
    }
    for (const current of grouped.values()) {
      cases.push({ report: reportPath, agent_id: item.agent_id, ...current });
      if (Number.isFinite(minAttempts) && current.attempts < minAttempts) {
        failures.push(`apg_baseline_underperformance_attempts_below_min:${reportPath}:${current.track_id}/${current.id}:${current.attempts}<${minAttempts}`);
      }
      if (current.failed <= 0) {
        failures.push(`apg_baseline_underperformance_case_no_failures:${reportPath}:${current.track_id}/${current.id}`);
      }
    }
  }

  const passRate = total > 0 ? passed / total : 0;
  if ((paths.length > 0 || required) && total <= 0) {
    failures.push("apg_baseline_underperformance_no_cases");
  }
  if ((paths.length > 0 || required) && Number.isFinite(maxPassRate) && passRate > maxPassRate) {
    failures.push(`apg_baseline_underperformance_pass_rate_above_max:${passRate.toFixed(4)}>${maxPassRate}`);
  }

  return {
    ok: failures.length === 0,
    required,
    expected_agents: Array.from(expectedAgents),
    min_attempts: Number.isFinite(minAttempts) ? minAttempts : null,
    max_pass_rate: Number.isFinite(maxPassRate) ? maxPassRate : null,
    report_count: reports.length,
    total,
    passed,
    failed,
    skipped,
    pass_rate: passRate,
    reports,
    cases,
    warnings,
    failures,
  };
}
function summarizeTuiVisibility(reportPath) {
  const required = requireTuiVisibility === "true";
  const failures = [];
  if (!reportPath) {
    if (required) failures.push("tui_visibility_required_but_no_report");
    return { ok: failures.length === 0, required, report: "", failures };
  }
  let report = null;
  try {
    report = readJson(reportPath);
  } catch (error) {
    failures.push(`tui_visibility_unreadable:${reportPath}:${error.message}`);
    return { ok: false, required, report: reportPath, failures };
  }
  if (report.schema_version !== "niuma/tui-tool-progress-acceptance/v1") {
    failures.push(`tui_visibility_schema_mismatch:${report.schema_version || "missing"}`);
  }
  if (report.ok !== true) {
    failures.push("tui_visibility_not_ok");
  }
  const covered = new Set(Array.isArray(report.covered_signals) ? report.covered_signals : []);
  for (const signal of [
    "tool_result_capability_loop_inline_summary",
    "sub_agent_progress_capability_loop_terminal_summary",
    "sub_agent_output_transcript_worktree_terminal_paths",
    "runtime_context_goal_cwd_session_agent_counts_status_bar",
  ]) {
    if (!covered.has(signal)) failures.push(`tui_visibility_missing_signal:${signal}`);
  }
  return {
    ok: failures.length === 0,
    required,
    report: reportPath,
    schema_version: report.schema_version || "",
    covered_signals: Array.from(covered),
    automated_checks: report.automated_checks || {},
    failures,
  };
}
const tui_visibility = summarizeTuiVisibility(tuiVisibilityReport);
const apg_stability = summarizeApgStabilityReports(apgStabilityPaths);
const apg_baseline_underperformance = summarizeApgBaselineUnderperformanceReports(apgBaselineUnderperformancePaths);
function requiredTuiSignalsCovered(tuiVisibility) {
  const covered = new Set(tuiVisibility.covered_signals || []);
  return [
    "tool_result_capability_loop_inline_summary",
    "sub_agent_progress_capability_loop_terminal_summary",
    "sub_agent_output_transcript_worktree_terminal_paths",
    "runtime_context_goal_cwd_session_agent_counts_status_bar",
  ].every(signal => covered.has(signal));
}
function firstBaselineSummary(apgCompare, pattern) {
  return (apgCompare.baseline_summaries || []).find(item => pattern.test(item.baseline_agent || ""));
}
function summarizeCapabilityComposite(scorecard, apg, apgCompare, tuiVisibility, apgStability, apgBaselineUnderperformance) {
  const nativeTotals = scorecard.totals || {};
  const finalReport = scorecard.final_report || {};
  const originalBaseline = firstBaselineSummary(apgCompare, /claude-code|original-claude|upstream-claude/i);
  const openCodeBaseline = firstBaselineSummary(apgCompare, /opencode/i);
  const finalReportSections = new Set(finalReport.sections || []);
  const requiredFinalSections = ["evidence", "unknowns", "verification", "risks", "next_action"];
  const components = [];
  const unscoredGaps = [];
  const qualityOK = Number(nativeTotals.required_scenario_count || 0) >= 13 &&
    Number(nativeTotals.missing_scenario_count || 0) === 0 &&
    Number(nativeTotals.failed_scenario_count || 0) === 0 &&
    finalReport.ok === true &&
    Number(finalReport.score || 0) >= Number(finalReport.required_score || 1) &&
    requiredFinalSections.every(section => finalReportSections.has(section)) &&
    apg.ok === true &&
    Number(apg.pass_rate || 0) >= Number(apg.min_pass_rate || 1) &&
    apgCompare.ok === true &&
    Number(apgCompare.status_losses || 0) === 0 &&
    Number(apgCompare.target_case_failures || 0) === 0;
  components.push({ name: "quality_closure", ok: qualityOK, score: qualityOK ? 35 : 0, max_score: 35 });

  const stabilityOK = apgStability.ok === true &&
    apgStability.required === true &&
    Number(apgStability.total || 0) >= 15 &&
    Number(apgStability.pass_rate || 0) >= 1;
  components.push({ name: "target_repeat_stability", ok: stabilityOK, score: stabilityOK ? 20 : 0, max_score: 20 });

  const originalEfficiencyOK = Boolean(originalBaseline) &&
    Number(originalBaseline.status_losses || 0) === 0 &&
    Number(originalBaseline.efficiency_delta || 0) >= 5;
  components.push({
    name: "original_baseline_efficiency",
    ok: originalEfficiencyOK,
    score: originalEfficiencyOK ? 15 : 0,
    max_score: 15,
    baseline_agent: originalBaseline?.baseline_agent || "",
    efficiency_delta: originalBaseline ? Number(originalBaseline.efficiency_delta || 0) : null,
  });

  const openCodeOK = Boolean(openCodeBaseline) &&
    Number(openCodeBaseline.status_losses || 0) === 0 &&
    Number(openCodeBaseline.status_win_delta || 0) >= 2 &&
    apgBaselineUnderperformance.ok === true &&
    apgBaselineUnderperformance.required === true &&
    Number(apgBaselineUnderperformance.failed || 0) > 0 &&
    Number(apgBaselineUnderperformance.pass_rate || 0) <= Number(apgBaselineUnderperformance.max_pass_rate ?? 1);
  components.push({
    name: "opencode_status_and_repeat_advantage",
    ok: openCodeOK,
    score: openCodeOK ? 15 : 0,
    max_score: 15,
    baseline_agent: openCodeBaseline?.baseline_agent || "",
    status_win_delta: openCodeBaseline ? Number(openCodeBaseline.status_win_delta || 0) : null,
    baseline_underperformance_pass_rate: Number(apgBaselineUnderperformance.pass_rate || 0),
  });

  const visibilityOK = tuiVisibility.ok === true && requiredTuiSignalsCovered(tuiVisibility);
  components.push({ name: "user_visible_capability_loop", ok: visibilityOK, score: visibilityOK ? 10 : 0, max_score: 10 });

  const usageConfidence = apgCompare.usage_confidence || {};
  const usageCostConfidenceOK = apgCompare.ok === true &&
    Number(usageConfidence.total_report_summaries || 0) > 0 &&
    Number(usageConfidence.total_case_results || 0) > 0 &&
    Number(usageConfidence.missing_report_cost_status || 0) === 0 &&
    Number(usageConfidence.missing_report_usage_confidence || 0) === 0 &&
    Number(usageConfidence.missing_case_cost_status || 0) === 0 &&
    Number(usageConfidence.missing_case_usage_confidence || 0) === 0 &&
    Number(usageConfidence.cost_status_counts?.unknown_model_pricing || 0) > 0 &&
    Number(usageConfidence.cost_status_counts?.not_reported || 0) > 0;
  components.push({ name: "usage_cost_confidence_audit", ok: usageCostConfidenceOK, score: usageCostConfidenceOK ? 5 : 0, max_score: 5 });

  const originalUsageMissing = (apgCompare.comparisons || []).some(item =>
    /claude-code|original-claude|upstream-claude/i.test(item.baseline_agent || "") &&
    Number(item.baseline_tokens || 0) === 0
  );
  if (originalUsageMissing) {
    unscoredGaps.push("original_claude_code_usage_or_cost_missing");
  }
  if (Number(usageConfidence.cost_status_counts?.unknown_model_pricing || 0) > 0) {
    unscoredGaps.push("provider_model_pricing_table_missing_for_reported_token_usage");
  }
  if (Number(usageConfidence.unknown_model_reports || 0) > 0 || Number(usageConfidence.unknown_model_case_results || 0) > 0) {
    unscoredGaps.push("apg_report_effective_model_missing_for_some_baselines");
  }
  unscoredGaps.push("cost_normalization_not_scored_until_cross_agent_usage_is_complete");

  const score = components.reduce((sum, item) => sum + Number(item.score || 0), 0);
  const maxScore = components.reduce((sum, item) => sum + Number(item.max_score || 0), 0);
  return {
    schema_version: "niuma/capability-composite/v1",
    score,
    max_score: maxScore,
    normalized_score: maxScore > 0 ? score / maxScore : 0,
    ok: score >= 90 && components.every(item => item.ok),
    components,
    unscored_gaps: unscoredGaps,
  };
}
const capability_composite = summarizeCapabilityComposite(scorecard, apg, apg_compare, tui_visibility, apg_stability, apg_baseline_underperformance);
function summarizeSuperiorityEvidence(scorecard, apg, apgCompare, tuiVisibility, apgStability, apgBaselineUnderperformance, capabilityComposite) {
  const ab = scorecard.ab || {};
  const finalReport = scorecard.final_report || {};
  const nativeTotals = scorecard.totals || {};
  const satisfiedClaims = [];
  const evidenceGaps = [];

  if (Number(nativeTotals.required_scenario_count || 0) >= 13 &&
      Number(nativeTotals.missing_scenario_count || 0) === 0 &&
      Number(nativeTotals.failed_scenario_count || 0) === 0) {
    satisfiedClaims.push("native_agent_capability_matrix_complete");
  }
  if (ab.ok === true &&
      Number(ab.scenario_count || 0) >= Number(minAbScenarios) &&
      Number(ab.capability_advantage_count || 0) >= Number(minAbAdvantages) &&
      Number(ab.capability_underperform_count || 0) === 0) {
    satisfiedClaims.push("go_vs_original_bounded_capability_advantage");
  }
  if (finalReport.ok === true &&
      Number(finalReport.score || 0) >= Number(finalReport.required_score || 1) &&
      ["evidence", "unknowns", "verification", "risks", "next_action"].every(section => (finalReport.sections || []).includes(section))) {
    satisfiedClaims.push("final_report_evidence_loop_quality");
  }
  if (tuiVisibility.ok === true) {
    satisfiedClaims.push("agent_capability_loop_user_visible");
  }
  if (apg.ok === true && Number(apg.total || 0) > 0 && Number(apg.pass_rate || 0) >= Number(apg.min_pass_rate || 1)) {
    satisfiedClaims.push("apg_real_task_reports_pass");
  }
  if (apgCompare.ok === true &&
      Number(apgCompare.case_count || 0) >= Number(apgCompare.min_case_count || 0) &&
      Number(apgCompare.status_losses || 0) === 0 &&
      Number(apgCompare.efficiency_advantages || 0) >= Number(apgCompare.min_efficiency_advantages || 0)) {
    satisfiedClaims.push("apg_baseline_compare_no_status_regression");
  }
  const originalBaselineSummary = (apgCompare.baseline_summaries || []).find(item => /claude-code|original-claude|upstream-claude/i.test(item.baseline_agent || ""));
  if (originalBaselineSummary &&
      Number(originalBaselineSummary.status_losses || 0) === 0 &&
      Number(originalBaselineSummary.efficiency_delta || 0) > 0) {
    satisfiedClaims.push("apg_original_efficiency_advantage");
  }
  if (apgStability.ok === true && apgStability.required === true && Number(apgStability.total || 0) > 0) {
    satisfiedClaims.push("apg_repeat_stability_pass");
  }
  if (apgBaselineUnderperformance.ok === true &&
      apgBaselineUnderperformance.required === true &&
      Number(apgBaselineUnderperformance.failed || 0) > 0 &&
      Number(apgBaselineUnderperformance.pass_rate || 0) <= Number(apgBaselineUnderperformance.max_pass_rate ?? 1)) {
    satisfiedClaims.push("apg_baseline_underperformance_repeat_stability");
  }
  if (capabilityComposite.ok === true) {
    satisfiedClaims.push("capability_composite_release_score_pass");
  }

  const baselineAgents = new Set();
  for (const report of apgCompare.reports || []) {
    for (const agent of report.baseline_agents || []) baselineAgents.add(agent);
  }
  const hasOriginalClaudeCodeApgBaseline = Array.from(baselineAgents).some(agent => /claude-code|original-claude|upstream-claude/i.test(agent));
  const apgCaseCount = Number(apgCompare.case_count || 0);
  const apgReportTotal = Number(apg.total || 0);
  if (!hasOriginalClaudeCodeApgBaseline) {
    evidenceGaps.push("apg_compare_original_claude_code_baseline_missing");
  }
  if (apgCaseCount < 10) {
    evidenceGaps.push(`apg_compare_case_count_low:${apgCaseCount}<10`);
  }
  if (apgReportTotal < 10) {
    evidenceGaps.push(`apg_real_task_total_low:${apgReportTotal}<10`);
  }
  if (Number(ab.scenario_count || 0) < 8) {
    evidenceGaps.push(`go_vs_original_ab_scenario_count_low:${Number(ab.scenario_count || 0)}<8`);
  }

  const boundedClaimOk = [
    "native_agent_capability_matrix_complete",
    "go_vs_original_bounded_capability_advantage",
    "final_report_evidence_loop_quality",
    "agent_capability_loop_user_visible",
    "apg_real_task_reports_pass",
    "apg_baseline_compare_no_status_regression",
  ].every(claim => satisfiedClaims.includes(claim));

  const boundedClaimComplete = boundedClaimOk && evidenceGaps.length === 0;
  const nextEvidence = [];
  if (!hasOriginalClaudeCodeApgBaseline) {
    nextEvidence.push("add APG original-Claude-Code baseline adapter/report and compare it against niuma");
  }
  if (apgCaseCount < 10) {
    nextEvidence.push("raise APG baseline compare coverage to at least 10 shared cases");
  }
  if (apgReportTotal < 10) {
    nextEvidence.push("expand niuma APG real-task passing evidence to at least 10 cases");
  }
  if (Number(ab.scenario_count || 0) < 8) {
    nextEvidence.push("raise Go-vs-original A/B scenario count beyond 8 with more real multi-step task families");
  }
  if (!(apgStability.ok === true && apgStability.required === true && Number(apgStability.total || 0) > 0)) {
    nextEvidence.push("add required APG repeat/stability evidence for core shared suites");
  }
  if (!(apgBaselineUnderperformance.ok === true && apgBaselineUnderperformance.required === true && Number(apgBaselineUnderperformance.failed || 0) > 0)) {
    nextEvidence.push("add required repeat/stability evidence that baseline underperformance is not a one-off run");
  }
  nextEvidence.push("expand beyond release-gate coverage with broader engineering task families and stronger quality/cost/stability analysis before claiming open-world superiority");

  return {
    schema_version: "niuma/superiority-evidence-audit/v1",
    bounded_claim_ok: boundedClaimOk,
    bounded_release_gate_complete: boundedClaimComplete,
    open_world_superiority_proven: false,
    claim_level: boundedClaimComplete ? "bounded_release_gate_complete" : "bounded_release_gate_advantage",
    satisfied_claims: satisfiedClaims,
    evidence_gaps: evidenceGaps,
    apg_original_claude_code_baseline_present: hasOriginalClaudeCodeApgBaseline,
    apg_compare_case_count: apgCaseCount,
    apg_real_task_total: apgReportTotal,
    go_vs_original_ab_scenario_count: Number(ab.scenario_count || 0),
    apg_original_efficiency_delta: originalBaselineSummary ? Number(originalBaselineSummary.efficiency_delta || 0) : null,
    next_evidence: nextEvidence,
  };
}
const superiority_evidence = summarizeSuperiorityEvidence(scorecard, apg, apg_compare, tui_visibility, apg_stability, apg_baseline_underperformance, capability_composite);
const failures = [
  ...(scorecard.failures || []),
  ...apg.failures,
  ...apg_compare.failures,
  ...tui_visibility.failures,
  ...apg_stability.failures,
  ...apg_baseline_underperformance.failures,
];
const report = {
  ok: Boolean(scorecard.ok) && apg.ok && apg_compare.ok && tui_visibility.ok && apg_stability.ok && apg_baseline_underperformance.ok,
  generated_at: new Date().toISOString(),
  mode: verifyOnly === "true" ? "verify-only" : "run",
  out_dir: outDir,
  gate_config: {
    ab_profile: abProfile,
    ab_scenarios: abScenarios.split(",").map(s => s.trim()).filter(Boolean),
    min_ab_scenarios: Number(minAbScenarios),
    min_ab_advantages: Number(minAbAdvantages),
  },
  artifacts: {
    native_matrix: nativeMatrix,
    ab_scorecard: abScorecard,
    combined_scorecard: combinedScorecard,
    final_report_score: finalReportScore || undefined,
    tui_visibility_report: tuiVisibilityReport || undefined,
    apg_reports: apgReportPaths,
    apg_compares: apgComparePaths,
    apg_stability_reports: apgStabilityPaths,
    apg_baseline_underperformance_reports: apgBaselineUnderperformancePaths,
  },
  totals: scorecard.totals,
  ab: scorecard.ab || null,
  final_report: scorecard.final_report || null,
  tui_visibility,
  apg,
  apg_compare,
  apg_stability,
  apg_baseline_underperformance,
  capability_composite,
  superiority_evidence,
  failures,
};

fs.writeFileSync(reportPath, JSON.stringify(report, null, 2) + "\n");
console.log(JSON.stringify(report, null, 2));
if (!report.ok) process.exitCode = 1;
EOF_REPORT
}

run_native_matrix >/dev/null
run_ab_matrix >/dev/null
if [[ "$RUN_FINAL_REPORT_QUALITY" == "true" ]]; then
  run_final_report_quality >/dev/null
fi
if [[ "$RUN_TUI_VISIBILITY" == "true" || "$REQUIRE_TUI_VISIBILITY" == "true" ]]; then
  run_tui_visibility >/dev/null
fi
run_scorecard >/dev/null
write_release_report

echo "native_matrix=$NATIVE_DIR/matrix-report.json"
echo "ab_scorecard=$AB_DIR/scorecard.json"
if [[ -n "$FINAL_REPORT_SCORE" ]]; then
  echo "final_report_score=$FINAL_REPORT_SCORE"
fi
if [[ -n "$TUI_VISIBILITY_REPORT" ]]; then
  echo "tui_visibility_report=$TUI_VISIBILITY_REPORT"
fi
echo "combined_scorecard=$SCORECARD_JSON"
echo "release_report=$REPORT_JSON"
