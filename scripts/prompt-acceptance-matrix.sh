#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

OUT_DIR="/tmp/niuma-prompt-acceptance-matrix-${TIMESTAMP}"
CWD="$ROOT_DIR"
MODEL=""
PROMPT_PROFILE=""
MAX_TOKENS=""
VERIFY_ONLY="false"
FORCE="false"
DEFAULT_SCENARIOS="code,task-agent,task-capability-loop,task-partial-evidence,skills,skill-compact,skill-resume,resume-replacement,tool-result-heavy,background-agent,agent-capability-loop,agent-capability-loop-resume,agent-capability-loop-compact,agent-capability-loop-compact-facts,agent-capability-loop-failed-compact,agent-capability-loop-cancelled-compact,agent-long-output-compact-resume,agent-detached-running-resume,agent-message-worktree-resume,read-tool-result,bash-background"
SCENARIOS="$DEFAULT_SCENARIOS"

usage() {
  cat <<'USAGE'
Usage:
  scripts/prompt-acceptance-matrix.sh [flags]

Runs a small suite of prompt dump acceptance scenarios and writes a matrix
report. By default this calls the model. Use --verify-only with existing dumps
to validate scripts and gates without a model call.

Flags:
  --cwd <path>              Workspace cwd for niuma. Default: repo root.
  --out-dir <path>          Directory for dumps/reports. Default: /tmp/niuma-prompt-acceptance-matrix-<timestamp>.
  --model <name>            Optional model override passed to scenario scripts.
  --prompt-profile <name>   Optional prompt profile passed to scenario scripts, e.g. claude-compatible.
  --max-tokens <n>          Optional maximum model output tokens per turn passed to scenario scripts.
  --scenarios <names>       Comma-separated scenarios. Supported: code,task-agent,
                             task-capability-loop,task-partial-evidence,skills,
                             skill-compact,skill-resume,resume-replacement,tool-result-heavy,
                             background-agent,agent-capability-loop,agent-capability-loop-resume,
                             agent-capability-loop-compact,agent-capability-loop-compact-facts,
                             agent-capability-loop-failed-compact,
                             agent-capability-loop-cancelled-compact,agent-long-output-compact-resume,
                             agent-detached-running-resume,agent-failed-resume,
                             agent-message-worktree-resume,
                             read-tool-result,bash-background,
                             final-report-side-by-side,skill-load-side-by-side,
                             skill-compact-side-by-side,agent-child-error-side-by-side,
                             agent-cancelled-side-by-side,agent-long-output-side-by-side.
                             Default: code,task-agent,task-capability-loop,task-partial-evidence,
                             skills,skill-compact,skill-resume,resume-replacement,tool-result-heavy,
                             background-agent,agent-capability-loop,agent-capability-loop-resume,
                             agent-capability-loop-compact,agent-capability-loop-compact-facts,
                             agent-capability-loop-failed-compact,
                             agent-capability-loop-cancelled-compact,agent-long-output-compact-resume,
                             agent-detached-running-resume,agent-message-worktree-resume,
                             read-tool-result,bash-background.
  --verify-only             Verify existing dumps in --out-dir instead of running the model.
  --force                   Remove existing scenario dumps before running.
  -h, --help                Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cwd)
      CWD="${2:?missing value for --cwd}"
      shift 2
      ;;
    --out-dir)
      OUT_DIR="${2:?missing value for --out-dir}"
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
    --scenarios)
      SCENARIOS="${2:?missing value for --scenarios}"
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

mkdir -p "$OUT_DIR"
REPORT_JSON="$OUT_DIR/matrix-report.json"
SUMMARY_JSONL="$OUT_DIR/matrix-summary.jsonl"
: > "$SUMMARY_JSONL"

matrix_status="ok"
scenario_count=0

json_string() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '"%s"' "$value"
}

run_scenario() {
  local scenario="$1"
  local script_path
  local min_turns
  if [[ "$scenario" == "final-report-side-by-side" ]]; then
    run_final_report_side_by_side "$scenario"
    return
  fi
  if [[ "$scenario" == "skill-load-side-by-side" ]]; then
    run_skill_load_side_by_side "$scenario"
    return
  fi
  if [[ "$scenario" == "skill-compact-side-by-side" ]]; then
    run_skill_compact_side_by_side "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-child-error-side-by-side" ]]; then
    run_agent_child_error_side_by_side "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-cancelled-side-by-side" ]]; then
    run_agent_cancelled_side_by_side "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-long-output-side-by-side" ]]; then
    run_agent_long_output_side_by_side "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-message-worktree-resume" ]]; then
    run_agent_message_worktree_resume "$scenario"
    return
  fi
  if [[ "$scenario" == "task-capability-loop" ]]; then
    run_task_capability_loop "$scenario"
    return
  fi
  if [[ "$scenario" == "task-partial-evidence" ]]; then
    run_task_partial_evidence "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-capability-loop-resume" ]]; then
    run_agent_capability_loop_resume "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-capability-loop-compact" ]]; then
    run_agent_capability_loop_compact "$scenario" "completed"
    return
  fi
  if [[ "$scenario" == "agent-capability-loop-compact-facts" ]]; then
    run_agent_capability_loop_compact_facts "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-capability-loop-failed-compact" ]]; then
    run_agent_capability_loop_compact "$scenario" "failed"
    return
  fi
  if [[ "$scenario" == "agent-capability-loop-cancelled-compact" ]]; then
    run_agent_capability_loop_compact "$scenario" "cancelled"
    return
  fi
  if [[ "$scenario" == "agent-long-output-compact-resume" ]]; then
    run_agent_long_output_compact_resume "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-detached-running-resume" ]]; then
    run_agent_detached_running_resume "$scenario"
    return
  fi
  if [[ "$scenario" == "agent-failed-resume" ]]; then
    run_agent_failed_resume "$scenario"
    return
  fi
  if [[ "$scenario" == "skill-compact" ]]; then
    run_skill_compact "$scenario"
    return
  fi
  case "$scenario" in
    code)
      script_path="$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh"
      min_turns="3"
      ;;
    task-agent)
      script_path="$ROOT_DIR/scripts/task-agent-prompt-acceptance.sh"
      min_turns="3"
      ;;
    skills)
      script_path="$ROOT_DIR/scripts/skills-prompt-acceptance.sh"
      min_turns="2"
      ;;
    skill-resume)
      script_path="$ROOT_DIR/scripts/skill-resume-prompt-acceptance.sh"
      min_turns="1"
      ;;
    resume-replacement)
      script_path="$ROOT_DIR/scripts/resume-replacement-prompt-acceptance.sh"
      min_turns="1"
      ;;
    tool-result-heavy)
      script_path="$ROOT_DIR/scripts/tool-result-heavy-prompt-acceptance.sh"
      min_turns="2"
      ;;
    background-agent)
      script_path="$ROOT_DIR/scripts/background-agent-prompt-acceptance.sh"
      min_turns="4"
      ;;
    agent-capability-loop)
      script_path="$ROOT_DIR/scripts/agent-capability-loop-acceptance.sh"
      min_turns="4"
      ;;
    read-tool-result)
      script_path="$ROOT_DIR/scripts/read-tool-result-prompt-acceptance.sh"
      min_turns="2"
      ;;
    bash-background)
      script_path="$ROOT_DIR/scripts/bash-background-prompt-acceptance.sh"
      min_turns="3"
      ;;
    *)
      echo "unsupported scenario: $scenario" >&2
      return 2
      ;;
  esac

  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$script_path" --dump "$dump_path" --min-turns "$min_turns")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ -n "$MAX_TOKENS" ]]; then
    cmd+=(--max-tokens "$MAX_TOKENS")
  fi
  if [[ "$scenario" != "skills" && "$scenario" != "skill-resume" && "$scenario" != "resume-replacement" && "$scenario" != "tool-result-heavy" ]]; then
    cmd+=(--cwd "$CWD")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_final_report_side_by_side() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/summary.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing side-by-side summary for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/final-report-side-by-side-compare.sh" --work-dir "$work_dir" --target-cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ -n "$MAX_TOKENS" ]]; then
    cmd+=(--max-tokens "$MAX_TOKENS")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_skill_load_side_by_side() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/summary.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing side-by-side summary for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/skill-load-side-by-side-compare.sh" --work-dir "$work_dir")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ -n "$MAX_TOKENS" ]]; then
    cmd+=(--max-tokens "$MAX_TOKENS")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_skill_compact_side_by_side() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/summary.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing side-by-side summary for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/skill-compact-side-by-side-compare.sh" --work-dir "$work_dir")
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_child_error_side_by_side() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/summary.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing side-by-side summary for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/agent-child-error-side-by-side-compare.sh" --work-dir "$work_dir" --target-cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_cancelled_side_by_side() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/summary.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing side-by-side summary for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/agent-cancelled-side-by-side-compare.sh" --work-dir "$work_dir" --target-cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_long_output_side_by_side() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/summary.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing side-by-side summary for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/agent-long-output-side-by-side-compare.sh" --work-dir "$work_dir" --target-cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_message_worktree_resume() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local retained_worktree="$work_dir/retained-worktree"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$dump_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing prompt dump for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if go run "$ROOT_DIR/scripts/verify-code-mode-prompt-dump.go" \
      --min-turns 2 \
      --require-final-tools-disabled=false \
      --require-final-turn-budget=false \
      --require-subagent-turn-budget-tools-disabled=false \
      --require-no-tool-errors=false \
      --require-tool-result "AgentMessage" \
      --require-request-text "## Background agent tasks,completed,completion notification: result is ready,previous finding,<persisted-output>,resume replacement preview,Agent message from coordinator,please continue with new evidence,resumed_from_task_id,source_transcript,Current working directory: $retained_worktree,$retained_worktree,worktree_resume,retained,worktree_path,worktree-agent-message-resume" \
      --forbid-request-text "AgentMessage can only send to running sub-agent tasks,RAW_FULL_TOOL_RESULT_MARKER" \
      "$dump_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/agent-message-resume-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD" --worktree-resume)
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_task_capability_loop() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/task-capability-loop-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ -n "$MAX_TOKENS" ]]; then
    cmd+=(--max-tokens "$MAX_TOKENS")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_task_partial_evidence() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/task-partial-evidence-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ -n "$MAX_TOKENS" ]]; then
    cmd+=(--max-tokens "$MAX_TOKENS")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_capability_loop_resume() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/agent-capability-loop-resume-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_capability_loop_compact() {
  local scenario="$1"
  local terminal_status="${2:-completed}"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/agent-capability-loop-compact-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD" --terminal-status "$terminal_status")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_capability_loop_compact_facts() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/agent-capability-loop-compact-facts-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_long_output_compact_resume() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/agent-long-output-resume-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD" --auto-compact)
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_detached_running_resume() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/agent-detached-running-resume-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path" --cwd "$CWD")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_agent_failed_resume() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local first_dump="$OUT_DIR/${scenario}-first.jsonl"
  local second_dump="$OUT_DIR/${scenario}-second.jsonl"
  local third_dump="$OUT_DIR/${scenario}-third.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local -a cmd=("$ROOT_DIR/scripts/failed-agent-resume-acceptance.sh" --work-dir "$work_dir" --cwd "$CWD" --first-dump "$first_dump" --second-dump "$second_dump" --third-dump "$third_dump")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$VERIFY_ONLY" == "true" ]]; then
    cmd+=(--verify-only)
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir second_dump=$second_dump third_dump=$third_dump" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$second_dump")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$second_dump")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

run_skill_compact() {
  local scenario="$1"
  local work_dir="$OUT_DIR/${scenario}"
  local dump_path="$OUT_DIR/${scenario}.jsonl"
  local report_path="$OUT_DIR/${scenario}-report.json"
  local log_path="$OUT_DIR/${scenario}.log"
  local summary_path="$work_dir/report.json"

  if [[ "$VERIFY_ONLY" == "true" ]]; then
    if [[ ! -s "$summary_path" ]]; then
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":2,"dump":%s,"report":%s,"log":%s,"error":"missing skill compact report for verify-only"}\n' "$(json_string "$scenario")" "$(json_string "$summary_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
      return
    fi
    if node -e 'const fs=require("fs"); const p=process.argv[1]; const s=JSON.parse(fs.readFileSync(p,"utf8")); if (!s.ok) process.exit(1);' "$summary_path" >"$report_path" 2>"$log_path"; then
      printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    else
      local exit_code=$?
      matrix_status="failed"
      printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
    fi
    return
  fi

  local -a cmd=("$ROOT_DIR/scripts/skill-compact-prompt-acceptance.sh" --work-dir "$work_dir" --dump "$dump_path")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$PROMPT_PROFILE" ]]; then
    cmd+=(--prompt-profile "$PROMPT_PROFILE")
  fi
  if [[ "$FORCE" == "true" ]]; then
    cmd+=(--force)
  fi

  echo "running scenario: $scenario work_dir=$work_dir dump=$dump_path" >&2
  if "${cmd[@]}" >"$report_path" 2>"$log_path"; then
    printf '{"scenario":%s,"status":"ok","dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  else
    local exit_code=$?
    matrix_status="failed"
    printf '{"scenario":%s,"status":"failed","exit_code":%d,"dump":%s,"report":%s,"log":%s}\n' "$(json_string "$scenario")" "$exit_code" "$(json_string "$dump_path")" "$(json_string "$report_path")" "$(json_string "$log_path")" >> "$SUMMARY_JSONL"
  fi
}

IFS=',' read -r -a scenario_items <<< "$SCENARIOS"
for raw_scenario in "${scenario_items[@]}"; do
  scenario="$(printf '%s' "$raw_scenario" | xargs)"
  if [[ -z "$scenario" ]]; then
    continue
  fi
  scenario_count=$((scenario_count + 1))
  run_scenario "$scenario"
done

{
  printf '{\n'
  printf '  "ok": %s,\n' "$(if [[ "$matrix_status" == "ok" ]]; then echo true; else echo false; fi)"
  printf '  "status": %s,\n' "$(json_string "$matrix_status")"
  printf '  "out_dir": %s,\n' "$(json_string "$OUT_DIR")"
  printf '  "scenario_count": %d,\n' "$scenario_count"
  printf '  "summary_jsonl": %s,\n' "$(json_string "$SUMMARY_JSONL")"
  printf '  "scenarios": [\n'
  local_index=0
  while IFS= read -r line; do
    if [[ $local_index -gt 0 ]]; then
      printf ',\n'
    fi
    printf '    %s' "$line"
    local_index=$((local_index + 1))
  done < "$SUMMARY_JSONL"
  printf '\n  ]\n'
  printf '}\n'
} > "$REPORT_JSON"

cat "$REPORT_JSON"
if [[ "$matrix_status" != "ok" ]]; then
  exit 1
fi
