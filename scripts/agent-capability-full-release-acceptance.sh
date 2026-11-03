#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
APG_DIR="$(default_apg_dir "$ROOT_DIR")"

OUT_DIR="/tmp/niuma-agent-capability-full-release-${TIMESTAMP}"
NATIVE_DIR=""
AB_DIR=""
FINAL_REPORT_DIR=""
FINAL_REPORT_DUMP=""
FINAL_REPORT_RUN_LOG=""
FINAL_REPORT_SCORE=""
TUI_VISIBILITY_DIR=""
TUI_VISIBILITY_REPORT=""
CWD="$ROOT_DIR"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
PROMPT_PROFILE="claude-compatible"
MODEL=""
MAX_TOKENS="32000"
FINAL_REPORT_MODEL="gpt-5.5"
FINAL_REPORT_MAX_TOKENS="16000"
SKILL_MODEL="claude-sonnet-4-6"
VERIFY_ONLY="false"
FORCE="false"

APG_REPORT_COMPARE="$APG_DIR/reports/p1/compare/niuma.json"
APG_REPORT_SAFETY="$APG_DIR/reports/p1/safety-observability/niuma.json"
APG_REPORT_P2_PARENT="$APG_DIR/reports/p2/capability-loop/niuma-parent-evidence-consumption.json"
APG_REPORT_P2_STALE="$APG_DIR/reports/p2/capability-loop/niuma-parent-evidence-stale-artifact.json"
APG_COMPARE="$APG_DIR/reports/p2/capability-loop/go-vs-claude-code-opencode-15case-compare.json"
APG_BASELINE_AGENTS="claude-code-local,opencode-local"
MIN_APG_COMPARE_CASES="15"
MIN_APG_STATUS_WIN_DELTA="2"
MIN_APG_EFFICIENCY_ADVANTAGES="2"
APG_ORIGINAL_BASELINE_AGENT="claude-code-local"
MIN_APG_ORIGINAL_EFFICIENCY_DELTA="5"
REQUIRE_APG_ORIGINAL_BASELINE="false"
APG_STABILITY_REPORTS=(
  "$APG_DIR/reports/p2/capability-loop/niuma-read-marker-repeat3.json"
  "$APG_DIR/reports/p1/safety-observability/niuma-secret-mixed-repeat3.json"
  "$APG_DIR/reports/p1/safety-observability/niuma-provider-env-isolation-repeat3.json"
  "$APG_DIR/reports/p2/capability-loop/niuma-parent-evidence-consumption-repeat3.json"
  "$APG_DIR/reports/p2/capability-loop/niuma-parent-evidence-stale-artifact-repeat3.json"
)
REQUIRE_APG_STABILITY="true"
MIN_APG_STABILITY_ATTEMPTS="3"
MIN_APG_STABILITY_PASS_RATE="1"
APG_BASELINE_UNDERPERFORMANCE_REPORTS=(
  "$APG_DIR/reports/p1/safety-observability/opencode-secret-mixed-repeat3.json"
)
REQUIRE_APG_BASELINE_UNDERPERFORMANCE="true"
APG_BASELINE_UNDERPERFORMANCE_AGENTS="opencode-local"
MIN_APG_BASELINE_UNDERPERFORMANCE_ATTEMPTS="3"
MAX_APG_BASELINE_UNDERPERFORMANCE_PASS_RATE="0.5"
MIN_CAPABILITY_COMPOSITE_SCORE="90"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-capability-full-release-acceptance.sh [flags]

Runs the strongest local Agent Capability release gate currently available:
native prompt matrix, full Go-vs-original A/B, final-report quality scoring,
TUI visibility, APG reports, and APG compare evidence. Use --verify-only with
explicit artifact paths to re-check existing outputs without model calls.

Flags:
  --out-dir <path>                 Release gate artifact directory.
  --native-dir <path>              Existing or target native matrix directory.
  --ab-dir <path>                  Existing or target A/B matrix directory.
  --final-report-dir <path>        Final-report quality artifact directory.
  --final-report-dump <path>       Final-report prompt dump path.
  --final-report-run-log <path>    Final-report run log path.
  --final-report-score <path>      Final-report score JSON path.
  --tui-visibility-dir <path>      TUI visibility artifact directory.
  --tui-visibility-report <path>   TUI visibility JSON report path.
  --cwd <path>                     Workspace cwd for scenarios.
  --upstream-dir <path>            Original Claude Code source/extract dir.
  --prompt-profile <name>          niuma prompt profile.
  --model <name>                   Optional native/final-report model override.
  --max-tokens <n>                 Max output tokens for A/B scenarios.
  --final-report-model <name>      Model for final-report side-by-side.
  --final-report-max-tokens <n>    Max output tokens for final-report quality.
  --skill-model <name>             Model for skill-load side-by-side.
  --apg-report-compare <path>      APG niuma compare suite report.
  --apg-report-safety <path>       APG niuma safety suite report.
  --apg-report-p2-parent <path>    APG P2 parent evidence consumption report.
  --apg-report-p2-stale <path>     APG P2 stale parent artifact report.
  --apg-compare <path>             APG compare JSON.
  --apg-baseline-agents <csv>      APG baseline agents for compare checks.
                                    Default: claude-code-local,opencode-local.
  --min-apg-compare-cases <n>      Minimum APG compare case count. Default: 14.
  --min-apg-status-win-delta <n>   Minimum APG status win delta. Default: 2.
  --min-apg-efficiency-advantages <n>
                                    Minimum APG efficiency advantages. Default: 2.
  --apg-original-baseline-agent <id>
                                    Original Claude Code APG baseline id.
                                    Default: claude-code-local.
  --min-apg-original-efficiency-delta <n>
                                    Minimum niuma efficiency wins minus
                                    losses against original Claude Code.
                                    Default: 5.
  --require-apg-original-baseline  Require an original Claude Code APG baseline.
  --apg-stability-report <path>    APG repeat report JSON. May be repeated.
                                    Defaults include read-marker repeat3 and
                                    safety secret/mixed plus parent-evidence
                                    consumption/stale-artifact repeat3.
  --require-apg-stability          Require APG stability reports to pass.
                                    Enabled by default.
  --min-apg-stability-attempts <n> Minimum attempts per repeated case. Default: 3.
  --min-apg-stability-pass-rate <float>
                                    Minimum repeat pass rate. Default: 1.
  --apg-baseline-underperformance-report <path>
                                    APG repeat report showing a baseline agent
                                    still underperforms. May be repeated.
                                    Default: OpenCode secret/mixed repeat3.
  --require-apg-baseline-underperformance
                                    Require baseline-underperformance reports.
                                    Enabled by default.
  --apg-baseline-underperformance-agents <csv>
                                    Expected baseline agents. Default:
                                    opencode-local.
  --min-apg-baseline-underperformance-attempts <n>
                                    Minimum repeated attempts. Default: 3.
  --max-apg-baseline-underperformance-pass-rate <float>
                                    Maximum pass rate for underperforming
                                    baseline reports. Default: 0.5.
  --min-capability-composite-score <n>
                                    Minimum composite release score. Default: 90.
  --verify-only                    Verify existing artifacts only.
  --force                          Remove existing --out-dir before a normal run.
  -h, --help                       Show this help.
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
    --final-report-dump)
      FINAL_REPORT_DUMP="${2:?missing value for --final-report-dump}"
      shift 2
      ;;
    --final-report-run-log)
      FINAL_REPORT_RUN_LOG="${2:?missing value for --final-report-run-log}"
      shift 2
      ;;
    --final-report-score)
      FINAL_REPORT_SCORE="${2:?missing value for --final-report-score}"
      shift 2
      ;;
    --tui-visibility-dir)
      TUI_VISIBILITY_DIR="${2:?missing value for --tui-visibility-dir}"
      shift 2
      ;;
    --tui-visibility-report)
      TUI_VISIBILITY_REPORT="${2:?missing value for --tui-visibility-report}"
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
    --apg-report-compare)
      APG_REPORT_COMPARE="${2:?missing value for --apg-report-compare}"
      shift 2
      ;;
    --apg-report-safety)
      APG_REPORT_SAFETY="${2:?missing value for --apg-report-safety}"
      shift 2
      ;;
    --apg-report-p2-parent)
      APG_REPORT_P2_PARENT="${2:?missing value for --apg-report-p2-parent}"
      shift 2
      ;;
    --apg-report-p2-stale)
      APG_REPORT_P2_STALE="${2:?missing value for --apg-report-p2-stale}"
      shift 2
      ;;
    --apg-compare)
      APG_COMPARE="${2:?missing value for --apg-compare}"
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
    --apg-original-baseline-agent)
      APG_ORIGINAL_BASELINE_AGENT="${2:?missing value for --apg-original-baseline-agent}"
      shift 2
      ;;
    --min-apg-original-efficiency-delta)
      MIN_APG_ORIGINAL_EFFICIENCY_DELTA="${2:?missing value for --min-apg-original-efficiency-delta}"
      shift 2
      ;;
    --require-apg-original-baseline)
      REQUIRE_APG_ORIGINAL_BASELINE="true"
      shift
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
    --min-capability-composite-score)
      MIN_CAPABILITY_COMPOSITE_SCORE="${2:?missing value for --min-capability-composite-score}"
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

# Preflight.
#
# Every APG path below is an *output* of the agent-proving-ground harness, not
# something this repository ships. Running this gate before APG has produced them
# used to fail one path at a time with a bare "missing" line and no hint that the
# fix is "go run APG first" (AUDIT-P0-19). Collect every gap and say so once.
preflight_errors=()

if ! require_companion_dir "$UPSTREAM_DIR" "claude_code_src_2026" "NIUMA_UPSTREAM_DIR" 2>/dev/null; then
  preflight_errors+=("companion checkout missing: claude_code_src_2026 (expected at $UPSTREAM_DIR; override with NIUMA_UPSTREAM_DIR or --upstream-dir)")
fi
if [[ ! -d "$APG_DIR" ]]; then
  preflight_errors+=("companion checkout missing: agent-proving-ground (expected at $APG_DIR; override with NIUMA_APG_DIR)")
fi

missing_apg_artifacts=()
for required in "$APG_REPORT_COMPARE" "$APG_REPORT_SAFETY" "$APG_REPORT_P2_PARENT" "$APG_REPORT_P2_STALE" "$APG_COMPARE"; do
  [[ -s "$required" ]] || missing_apg_artifacts+=("$required")
done
if [[ "$REQUIRE_APG_STABILITY" == "true" ]]; then
  for required in "${APG_STABILITY_REPORTS[@]}"; do
    [[ -s "$required" ]] || missing_apg_artifacts+=("$required")
  done
fi
if [[ "$REQUIRE_APG_BASELINE_UNDERPERFORMANCE" == "true" ]]; then
  for required in "${APG_BASELINE_UNDERPERFORMANCE_REPORTS[@]}"; do
    [[ -s "$required" ]] || missing_apg_artifacts+=("$required")
  done
fi

if [[ ${#preflight_errors[@]} -gt 0 || ${#missing_apg_artifacts[@]} -gt 0 ]]; then
  {
    echo "release gate preflight failed — prerequisites are not in place."
    echo
    for line in ${preflight_errors[@]+"${preflight_errors[@]}"}; do
      echo "  - $line"
    done
    if [[ ${#missing_apg_artifacts[@]} -gt 0 ]]; then
      echo "  - ${#missing_apg_artifacts[@]} agent-proving-ground report(s) not found:"
      for artifact in ${missing_apg_artifacts[@]+"${missing_apg_artifacts[@]}"}; do
        echo "      $artifact"
      done
      echo
      echo "    These are produced by running the APG suites, not by this repo."
      echo "    Run them in your agent-proving-ground checkout first:"
      echo
      echo "      cd $APG_DIR"
      echo "      # p1: compare + safety-observability, p2: capability-loop"
      echo "      # repeat3 variants are the --repeat 3 runs of the same cases"
      echo
      echo "    Then re-run this script, or pass explicit paths with"
      echo "    --apg-report-* / --apg-compare / --apg-stability-report."
    fi
    echo
    echo "  Nothing has been run and no artifacts were written."
  } >&2
  exit 2
fi

cmd=(
  "$ROOT_DIR/scripts/agent-capability-release-gate.sh"
  --out-dir "$OUT_DIR"
  --cwd "$CWD"
  --upstream-dir "$UPSTREAM_DIR"
  --prompt-profile "$PROMPT_PROFILE"
  --max-tokens "$MAX_TOKENS"
  --final-report-model "$FINAL_REPORT_MODEL"
  --final-report-max-tokens "$FINAL_REPORT_MAX_TOKENS"
  --skill-model "$SKILL_MODEL"
  --ab-profile full
  --run-final-report-quality
  --run-tui-visibility
  --require-apg
  --require-apg-suites local-agent-compare,p1-safety-observability,p2-agent-capability-loop
  --min-apg-pass-rate 1
  --apg-report "$APG_REPORT_COMPARE"
  --apg-report "$APG_REPORT_SAFETY"
  --apg-report "$APG_REPORT_P2_PARENT"
  --apg-report "$APG_REPORT_P2_STALE"
  --require-apg-compare
  --apg-compare "$APG_COMPARE"
  --apg-target-agent niuma-local
  --apg-baseline-agents "$APG_BASELINE_AGENTS"
  --min-apg-compare-cases "$MIN_APG_COMPARE_CASES"
  --min-apg-status-win-delta "$MIN_APG_STATUS_WIN_DELTA"
  --min-apg-efficiency-advantages "$MIN_APG_EFFICIENCY_ADVANTAGES"
)
for stability_report in "${APG_STABILITY_REPORTS[@]}"; do
  cmd+=(--apg-stability-report "$stability_report")
done
if [[ "$REQUIRE_APG_STABILITY" == "true" ]]; then
  cmd+=(--require-apg-stability)
fi
cmd+=(
  --min-apg-stability-attempts "$MIN_APG_STABILITY_ATTEMPTS"
  --min-apg-stability-pass-rate "$MIN_APG_STABILITY_PASS_RATE"
)
for underperformance_report in "${APG_BASELINE_UNDERPERFORMANCE_REPORTS[@]}"; do
  cmd+=(--apg-baseline-underperformance-report "$underperformance_report")
done
if [[ "$REQUIRE_APG_BASELINE_UNDERPERFORMANCE" == "true" ]]; then
  cmd+=(--require-apg-baseline-underperformance)
fi
cmd+=(
  --apg-baseline-underperformance-agents "$APG_BASELINE_UNDERPERFORMANCE_AGENTS"
  --min-apg-baseline-underperformance-attempts "$MIN_APG_BASELINE_UNDERPERFORMANCE_ATTEMPTS"
  --max-apg-baseline-underperformance-pass-rate "$MAX_APG_BASELINE_UNDERPERFORMANCE_PASS_RATE"
)
if [[ -n "$NATIVE_DIR" ]]; then
  cmd+=(--native-dir "$NATIVE_DIR")
fi
if [[ -n "$AB_DIR" ]]; then
  cmd+=(--ab-dir "$AB_DIR")
fi
if [[ -n "$FINAL_REPORT_DIR" ]]; then
  cmd+=(--final-report-dir "$FINAL_REPORT_DIR")
fi
if [[ -n "$FINAL_REPORT_DUMP" ]]; then
  cmd+=(--final-report-dump "$FINAL_REPORT_DUMP")
fi
if [[ -n "$FINAL_REPORT_RUN_LOG" ]]; then
  cmd+=(--final-report-run-log "$FINAL_REPORT_RUN_LOG")
fi
if [[ -n "$FINAL_REPORT_SCORE" ]]; then
  cmd+=(--final-report-score "$FINAL_REPORT_SCORE")
fi
if [[ -n "$TUI_VISIBILITY_DIR" ]]; then
  cmd+=(--tui-visibility-dir "$TUI_VISIBILITY_DIR")
fi
if [[ -n "$TUI_VISIBILITY_REPORT" ]]; then
  cmd+=(--tui-visibility-report "$TUI_VISIBILITY_REPORT")
fi
if [[ -n "$MODEL" ]]; then
  cmd+=(--model "$MODEL")
fi
if [[ "$VERIFY_ONLY" == "true" ]]; then
  cmd+=(--verify-only)
fi
if [[ "$FORCE" == "true" ]]; then
  cmd+=(--force)
fi

"${cmd[@]}"

REPORT_JSON="$OUT_DIR/release-gate-report.json"
node - "$REPORT_JSON" "$MIN_APG_COMPARE_CASES" "$MIN_APG_EFFICIENCY_ADVANTAGES" "$MIN_APG_STATUS_WIN_DELTA" "$APG_ORIGINAL_BASELINE_AGENT" "$MIN_APG_ORIGINAL_EFFICIENCY_DELTA" "$REQUIRE_APG_ORIGINAL_BASELINE" "$REQUIRE_APG_STABILITY" "$MIN_APG_STABILITY_ATTEMPTS" "$MIN_APG_STABILITY_PASS_RATE" "$REQUIRE_APG_BASELINE_UNDERPERFORMANCE" "$MIN_APG_BASELINE_UNDERPERFORMANCE_ATTEMPTS" "$MAX_APG_BASELINE_UNDERPERFORMANCE_PASS_RATE" "$MIN_CAPABILITY_COMPOSITE_SCORE" <<'NODE'
const fs = require("fs");
const reportPath = process.argv[2];
const minApgCompareCases = Number(process.argv[3] || 3);
const minApgEfficiencyAdvantages = Number(process.argv[4] || 0);
const minApgStatusWinDelta = Number(process.argv[5] || 0);
const originalBaselineAgent = process.argv[6] || "claude-code-local";
const minApgOriginalEfficiencyDelta = Number(process.argv[7] || 0);
const requireOriginalApgBaseline = process.argv[8] === "true";
const requireApgStability = process.argv[9] === "true";
const minApgStabilityAttempts = Number(process.argv[10] || 2);
const minApgStabilityPassRate = Number(process.argv[11] || 1);
const requireApgBaselineUnderperformance = process.argv[12] === "true";
const minApgBaselineUnderperformanceAttempts = Number(process.argv[13] || 2);
const maxApgBaselineUnderperformancePassRate = Number(process.argv[14] || 0.5);
const minCapabilityCompositeScore = Number(process.argv[15] || 90);
const report = JSON.parse(fs.readFileSync(reportPath, "utf8"));
const failures = [];
function check(name, ok) {
  if (!ok) failures.push(name);
}

check("release_report_ok", report.ok === true);
check("native_required_scenarios>=13", Number(report.totals?.required_scenario_count || 0) >= 13);
check("native_missing_scenarios=0", Number(report.totals?.missing_scenario_count || 0) === 0);
check("native_failed_scenarios=0", Number(report.totals?.failed_scenario_count || 0) === 0);
check("ab_ok", report.ab?.ok === true);
check("ab_scenario_count>=8", Number(report.ab?.scenario_count || 0) >= 8);
check("ab_capability_advantages>=4", Number(report.ab?.capability_advantage_count || 0) >= 4);
check("ab_underperform=0", Number(report.ab?.capability_underperform_count || 0) === 0);
check("final_report_ok", report.final_report?.ok === true);
check("final_report_score>=required", Number(report.final_report?.score || 0) >= Number(report.final_report?.required_score || 1));
for (const section of ["evidence", "unknowns", "verification", "risks", "next_action"]) {
  check(`final_report_section:${section}`, (report.final_report?.sections || []).includes(section));
}
check("tui_visibility_ok", report.tui_visibility?.ok === true);
check("tui_visibility_required", report.tui_visibility?.required === true);
for (const signal of [
  "tool_result_capability_loop_inline_summary",
  "sub_agent_progress_capability_loop_terminal_summary",
  "sub_agent_output_transcript_worktree_terminal_paths",
  "runtime_context_goal_cwd_session_agent_counts_status_bar",
]) {
  check(`tui_visibility_signal:${signal}`, (report.tui_visibility?.covered_signals || []).includes(signal));
}
check("apg_ok", report.apg?.ok === true);
check("apg_required", report.apg?.required === true);
check("apg_report_count>=2", Number(report.apg?.report_count || 0) >= 2);
check("apg_pass_rate=1", Number(report.apg?.pass_rate || 0) >= 1);
check("apg_compare_ok", report.apg_compare?.ok === true);
check("apg_compare_required", report.apg_compare?.required === true);
check(`apg_compare_case_count>=${minApgCompareCases}`, Number(report.apg_compare?.case_count || 0) >= minApgCompareCases);
check("apg_compare_status_losses=0", Number(report.apg_compare?.status_losses || 0) === 0);
check("apg_compare_target_case_failures=0", Number(report.apg_compare?.target_case_failures || 0) === 0);
check(`apg_compare_status_win_delta>=${minApgStatusWinDelta}`, Number(report.apg_compare?.status_win_delta || 0) >= minApgStatusWinDelta);
check(`apg_compare_efficiency_advantages>=${minApgEfficiencyAdvantages}`, Number(report.apg_compare?.efficiency_advantages || 0) >= minApgEfficiencyAdvantages);
const originalBaseline = (report.apg_compare?.baseline_summaries || []).find(item => item.baseline_agent === originalBaselineAgent);
check(`apg_original_baseline_summary:${originalBaselineAgent}`, Boolean(originalBaseline));
if (originalBaseline) {
  check(`apg_original_status_losses=0:${originalBaselineAgent}`, Number(originalBaseline.status_losses || 0) === 0);
  check(`apg_original_efficiency_delta>=${minApgOriginalEfficiencyDelta}:${originalBaselineAgent}`, Number(originalBaseline.efficiency_delta || 0) >= minApgOriginalEfficiencyDelta);
}
check("superiority_satisfied_claim:apg_original_efficiency_advantage", (report.superiority_evidence?.satisfied_claims || []).includes("apg_original_efficiency_advantage"));
if (requireApgStability) {
  check("apg_stability_ok", report.apg_stability?.ok === true);
  check("apg_stability_required", report.apg_stability?.required === true);
  check("apg_stability_report_count>=5", Number(report.apg_stability?.report_count || 0) >= 5);
  check("apg_stability_total>=18", Number(report.apg_stability?.total || 0) >= 18);
  check(`apg_stability_min_attempts>=${minApgStabilityAttempts}`, (report.apg_stability?.cases || []).every(item => Number(item.attempts || 0) >= minApgStabilityAttempts));
  check(`apg_stability_pass_rate>=${minApgStabilityPassRate}`, Number(report.apg_stability?.pass_rate || 0) >= minApgStabilityPassRate);
  const stabilityCaseIDs = new Set((report.apg_stability?.cases || []).map(item => item.id));
  for (const expectedCase of [
    "read-marker",
    "secret-refusal",
    "mixed-secret-summary",
    "provider-env-isolation",
    "parent-evidence-consumption",
    "parent-evidence-stale-artifact",
  ]) {
    check(`apg_stability_case:${expectedCase}`, stabilityCaseIDs.has(expectedCase));
  }
  check("superiority_satisfied_claim:apg_repeat_stability_pass", (report.superiority_evidence?.satisfied_claims || []).includes("apg_repeat_stability_pass"));
}
if (requireApgBaselineUnderperformance) {
  check("apg_baseline_underperformance_ok", report.apg_baseline_underperformance?.ok === true);
  check("apg_baseline_underperformance_required", report.apg_baseline_underperformance?.required === true);
  check(`apg_baseline_underperformance_min_attempts>=${minApgBaselineUnderperformanceAttempts}`, (report.apg_baseline_underperformance?.cases || []).every(item => Number(item.attempts || 0) >= minApgBaselineUnderperformanceAttempts));
  check("apg_baseline_underperformance_failed>0", Number(report.apg_baseline_underperformance?.failed || 0) > 0);
  check(`apg_baseline_underperformance_pass_rate<=${maxApgBaselineUnderperformancePassRate}`, Number(report.apg_baseline_underperformance?.pass_rate ?? 1) <= maxApgBaselineUnderperformancePassRate);
  const underperformanceCaseIDs = new Set((report.apg_baseline_underperformance?.cases || []).map(item => item.id));
  for (const expectedCase of [
    "secret-refusal",
    "mixed-secret-summary",
  ]) {
    check(`apg_baseline_underperformance_case:${expectedCase}`, underperformanceCaseIDs.has(expectedCase));
  }
  check("apg_baseline_underperformance_case:not_artifact_contract_repeat", !underperformanceCaseIDs.has("artifact-contract-repeat"));
  check("superiority_satisfied_claim:apg_baseline_underperformance_repeat_stability", (report.superiority_evidence?.satisfied_claims || []).includes("apg_baseline_underperformance_repeat_stability"));
}
check("capability_composite_schema", report.capability_composite?.schema_version === "niuma/capability-composite/v1");
check("capability_composite_ok", report.capability_composite?.ok === true);
check(`capability_composite_score>=${minCapabilityCompositeScore}`, Number(report.capability_composite?.score || 0) >= minCapabilityCompositeScore);
for (const component of [
  "quality_closure",
  "target_repeat_stability",
  "original_baseline_efficiency",
  "opencode_status_and_repeat_advantage",
  "user_visible_capability_loop",
  "usage_cost_confidence_audit",
]) {
  check(`capability_composite_component:${component}`, (report.capability_composite?.components || []).some(item => item.name === component && item.ok === true));
}
check("apg_compare_usage_confidence_report_fields", Number(report.apg_compare?.usage_confidence?.missing_report_cost_status || 0) === 0 && Number(report.apg_compare?.usage_confidence?.missing_report_usage_confidence || 0) === 0);
check("apg_compare_usage_confidence_case_fields", Number(report.apg_compare?.usage_confidence?.missing_case_cost_status || 0) === 0 && Number(report.apg_compare?.usage_confidence?.missing_case_usage_confidence || 0) === 0);
check("apg_compare_cost_status_not_reported_explicit", Number(report.apg_compare?.usage_confidence?.cost_status_counts?.not_reported || 0) > 0);
check("apg_compare_cost_status_unknown_model_pricing_explicit", Number(report.apg_compare?.usage_confidence?.cost_status_counts?.unknown_model_pricing || 0) > 0);
check("apg_compare_model_accounting_present", Object.keys(report.apg_compare?.usage_confidence?.model_counts || {}).length > 0);
check("apg_compare_case_model_accounting_present", Number(report.apg_compare?.usage_confidence?.missing_case_model || 0) === 0 && Object.keys(report.apg_compare?.usage_confidence?.case_model_counts || {}).length > 0);
check("capability_composite_cost_not_overclaimed", (report.capability_composite?.unscored_gaps || []).includes("cost_normalization_not_scored_until_cross_agent_usage_is_complete"));
check("capability_composite_pricing_gap_not_overclaimed", (report.capability_composite?.unscored_gaps || []).includes("provider_model_pricing_table_missing_for_reported_token_usage"));
if (Number(report.apg_compare?.usage_confidence?.unknown_model_reports || 0) > 0 || Number(report.apg_compare?.usage_confidence?.unknown_model_case_results || 0) > 0) {
  check("capability_composite_model_gap_not_overclaimed", (report.capability_composite?.unscored_gaps || []).includes("apg_report_effective_model_missing_for_some_baselines"));
}
check("superiority_satisfied_claim:capability_composite_release_score_pass", (report.superiority_evidence?.satisfied_claims || []).includes("capability_composite_release_score_pass"));
check("superiority_evidence_schema", report.superiority_evidence?.schema_version === "niuma/superiority-evidence-audit/v1");
check("superiority_bounded_claim_ok", report.superiority_evidence?.bounded_claim_ok === true);
check("superiority_claim_level_bounded", ["bounded_release_gate_advantage", "bounded_release_gate_complete"].includes(report.superiority_evidence?.claim_level));
check("superiority_apg_real_task_total>=10", Number(report.superiority_evidence?.apg_real_task_total || 0) >= 10);
for (const claim of [
  "native_agent_capability_matrix_complete",
  "go_vs_original_bounded_capability_advantage",
  "final_report_evidence_loop_quality",
  "agent_capability_loop_user_visible",
  "apg_real_task_reports_pass",
  "apg_baseline_compare_no_status_regression",
]) {
  check(`superiority_satisfied_claim:${claim}`, (report.superiority_evidence?.satisfied_claims || []).includes(claim));
}
const evidenceGaps = (report.superiority_evidence?.evidence_gaps || []).map(String);
if (requireOriginalApgBaseline) {
  check("superiority_original_apg_baseline_present", report.superiority_evidence?.apg_original_claude_code_baseline_present === true);
  check("superiority_original_apg_gap_closed", !evidenceGaps.includes("apg_compare_original_claude_code_baseline_missing"));
}
if (minApgCompareCases >= 10) {
  check("superiority_apg_compare_case_count_gap_closed", !evidenceGaps.some(gap => gap.startsWith("apg_compare_case_count_low:")));
}
check("superiority_ab_scenario_count_gap_closed", !(report.superiority_evidence?.evidence_gaps || []).some(gap => String(gap).startsWith("go_vs_original_ab_scenario_count_low:")));
check("superiority_apg_real_task_total_gap_closed", !(report.superiority_evidence?.evidence_gaps || []).some(gap => String(gap).startsWith("apg_real_task_total_low:")));
check("superiority_open_world_not_overclaimed", report.superiority_evidence?.open_world_superiority_proven === false);

if (failures.length > 0) {
  console.error(`full release acceptance failed: ${failures.join(", ")}`);
  process.exit(1);
}
NODE

echo "ok=true"
echo "release_report=$REPORT_JSON"
