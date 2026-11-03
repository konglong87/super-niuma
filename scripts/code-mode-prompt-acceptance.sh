#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEFAULT_CWD="$ROOT_DIR"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

CWD="$DEFAULT_CWD"
DUMP_PATH="/tmp/niuma-code-mode-acceptance-${TIMESTAMP}.jsonl"
RUN_LOG=""
PROMPT="只读审计 internal/query 中 runtime status、prompt dump、tool_result budget 三条链路如何进入模型请求。请给文件/函数证据。不要修改文件。"
MODEL=""
PROMPT_PROFILE=""
TOOLS="LS,Grep,Read"
ALLOWED_TOOLS=""
MAX_TURNS="3"
MAX_TOKENS=""
MIN_TURNS="3"
REQUIRE_TOOL_NO_RAW_OVER_LIMIT="Grep"
REQUIRE_TOOL_NO_PERSISTED_OUTPUT="Read"
REQUIRE_TOOL_PERSISTED_OUTPUT=""
REQUIRE_TOOL_MAX_BYTES="Grep=20000,Bash=30000,PowerShell=30000"
REQUIRE_TOOL_RESULT=""
REQUIRE_TOOL_USE_INPUT_TEXT=""
FORBID_TOOL=""
REQUIRE_REQUEST_TEXT=""
FORBID_REQUEST_TEXT=""
REQUIRE_OUTPUT_TEXT=""
FORBID_OUTPUT_TEXT=""
REQUIRE_FINAL_TOOLS_DISABLED="true"
REQUIRE_FINAL_TURN_BUDGET="true"
REQUIRE_SUBAGENT_TURN_BUDGET_TOOLS_DISABLED="true"
REQUIRE_SUBAGENT_RECORD="false"
VERIFY_ONLY="false"
ALLOW_RUN_FAILURE="false"
FULL_DUMP="false"
FORCE="false"
RESUME=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/code-mode-prompt-acceptance.sh [flags]

Runs the fixed code-mode diagnostic prompt with prompt dumping enabled, then
verifies the resulting JSONL with scripts/verify-code-mode-prompt-dump.go.

Flags:
  --cwd <path>          Workspace cwd for niuma. Default: repo root.
  --dump <path>         Prompt dump JSONL path. Default: /tmp/niuma-code-mode-acceptance-<timestamp>.jsonl.
  --run-log <path>      Combined stdout/stderr log path. Default: <dump>.run.log.
  --prompt <text>       Diagnostic prompt.
  --model <name>        Optional model override passed to niuma --model.
  --prompt-profile <name>
                        Optional NIUMA_PROMPT_PROFILE value, e.g. claude-compatible.
  --tools <names>       Comma-separated tools passed to --tools. Default: LS,Grep,Read.
  --no-tools-flag       Do not pass --tools, so the CLI uses its native default tool surface.
  --allowed-tools <patterns>
                        Comma-separated tool allow patterns passed to --allowedTools.
  --max-turns <n>       niuma max turns. Default: 3.
  --max-tokens <n>      Optional maximum model output tokens per turn passed to niuma --max-tokens.
  --min-turns <n>       verifier minimum turns. Default: 3.
  --require-tool-no-raw-over-limit <names>
                        Comma-separated tool names that must have no raw tool_results over configured limit. Default: Grep.
  --require-tool-no-persisted-output <names>
                        Comma-separated tool names that must have no persisted-output tool_results. Default: Read.
  --require-tool-persisted-output <names>
                        Comma-separated tool names that must have at least one persisted-output tool_result.
  --require-tool-max-bytes <specs>
                        Comma-separated Tool=Bytes specs for maximum visible tool_result content bytes. Default: Grep=20000,Bash=30000,PowerShell=30000.
  --require-tool-result <names>
                        Comma-separated tool names that must appear as tool_result blocks.
  --require-tool-use-input-text <specs>
                        Comma-separated Tool=snippet specs that must appear in full prompt dump tool_use inputs.
  --forbid-tool <names>
                        Comma-separated tool names that must not be exposed in full prompt dump requests.
  --require-request-text <snippets>
                        Comma-separated text snippets that must appear in a full prompt dump request.
  --forbid-request-text <snippets>
                        Comma-separated text snippets that must not appear in a full prompt dump request.
  --require-output-text <snippets>
                        Comma-separated text snippets that must appear in the combined run output.
  --forbid-output-text <snippets>
                        Comma-separated text snippets that must not appear in the combined run output.
  --no-require-final-tools-disabled
                        Allow the final main record to expose tools, useful for natural early completion acceptance.
  --no-require-final-turn-budget
                        Allow the final main record to omit turn_budget, useful for natural early completion acceptance.
  --no-require-subagent-turn-budget-tools-disabled
                        Allow sub-agent records with turn_budget runtime status to expose tools.
  --require-subagent-record
                        Require at least one sub-agent scoped prompt dump record.
  --verify-only         Do not run the model; verify the existing --dump file.
  --allow-run-failure   Continue to prompt-dump verification if the model run exits non-zero.
  --full                Set NIUMA_DUMP_PROMPT_FULL=true for local debugging.
  --force               Remove an existing --dump file before running the model.
  --resume <session_id> Resume context from an existing transcript before running.
  -h, --help            Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cwd)
      CWD="${2:?missing value for --cwd}"
      shift 2
      ;;
    --dump)
      DUMP_PATH="${2:?missing value for --dump}"
      shift 2
      ;;
    --run-log)
      RUN_LOG="${2:?missing value for --run-log}"
      shift 2
      ;;
    --prompt)
      PROMPT="${2:?missing value for --prompt}"
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
    --tools)
      TOOLS="${2:?missing value for --tools}"
      shift 2
      ;;
    --no-tools-flag)
      TOOLS=""
      shift
      ;;
    --allowed-tools|--allowedTools)
      ALLOWED_TOOLS="${2:?missing value for --allowed-tools}"
      shift 2
      ;;
    --max-turns)
      MAX_TURNS="${2:?missing value for --max-turns}"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="${2:?missing value for --max-tokens}"
      shift 2
      ;;
    --min-turns)
      MIN_TURNS="${2:?missing value for --min-turns}"
      shift 2
      ;;
    --require-tool-no-raw-over-limit)
      REQUIRE_TOOL_NO_RAW_OVER_LIMIT="${2:?missing value for --require-tool-no-raw-over-limit}"
      shift 2
      ;;
    --require-tool-no-persisted-output)
      REQUIRE_TOOL_NO_PERSISTED_OUTPUT="${2:?missing value for --require-tool-no-persisted-output}"
      shift 2
      ;;
    --require-tool-persisted-output)
      REQUIRE_TOOL_PERSISTED_OUTPUT="${2:?missing value for --require-tool-persisted-output}"
      shift 2
      ;;
    --require-tool-max-bytes)
      REQUIRE_TOOL_MAX_BYTES="${2:?missing value for --require-tool-max-bytes}"
      shift 2
      ;;
    --require-tool-result)
      REQUIRE_TOOL_RESULT="${2:?missing value for --require-tool-result}"
      shift 2
      ;;
    --require-tool-use-input-text)
      REQUIRE_TOOL_USE_INPUT_TEXT="${2:?missing value for --require-tool-use-input-text}"
      shift 2
      ;;
    --forbid-tool)
      FORBID_TOOL="${2:?missing value for --forbid-tool}"
      shift 2
      ;;
    --require-request-text)
      REQUIRE_REQUEST_TEXT="${2:?missing value for --require-request-text}"
      shift 2
      ;;
    --forbid-request-text)
      FORBID_REQUEST_TEXT="${2:?missing value for --forbid-request-text}"
      shift 2
      ;;
    --require-output-text)
      REQUIRE_OUTPUT_TEXT="${2:?missing value for --require-output-text}"
      shift 2
      ;;
    --forbid-output-text)
      FORBID_OUTPUT_TEXT="${2:?missing value for --forbid-output-text}"
      shift 2
      ;;
    --no-require-final-tools-disabled)
      REQUIRE_FINAL_TOOLS_DISABLED="false"
      shift
      ;;
    --no-require-final-turn-budget)
      REQUIRE_FINAL_TURN_BUDGET="false"
      shift
      ;;
    --no-require-subagent-turn-budget-tools-disabled)
      REQUIRE_SUBAGENT_TURN_BUDGET_TOOLS_DISABLED="false"
      shift
      ;;
    --require-subagent-record)
      REQUIRE_SUBAGENT_RECORD="true"
      shift
      ;;
    --verify-only)
      VERIFY_ONLY="true"
      shift
      ;;
    --allow-run-failure)
      ALLOW_RUN_FAILURE="true"
      shift
      ;;
    --full)
      FULL_DUMP="true"
      shift
      ;;
    --force)
      FORCE="true"
      shift
      ;;
    --resume)
      RESUME="${2:?missing value for --resume}"
      shift 2
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

if [[ -z "$RUN_LOG" ]]; then
  RUN_LOG="${DUMP_PATH}.run.log"
fi

if [[ "$VERIFY_ONLY" != "true" ]]; then
  if [[ -e "$DUMP_PATH" && "$FORCE" != "true" ]]; then
    echo "dump path already exists: $DUMP_PATH (use --force or choose a new --dump)" >&2
    exit 2
  fi
  if [[ -e "$DUMP_PATH" ]]; then
    rm -f "$DUMP_PATH"
  fi
  rm -f "$RUN_LOG"

  export NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH"
  if [[ -n "$PROMPT_PROFILE" ]]; then
    export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
  else
    unset NIUMA_PROMPT_PROFILE || true
  fi
  if [[ "$FULL_DUMP" == "true" ]]; then
    export NIUMA_DUMP_PROMPT_FULL="true"
  else
    unset NIUMA_DUMP_PROMPT_FULL || true
  fi

  cmd=(go run ./cmd/niuma --cwd "$CWD" --max-turns "$MAX_TURNS")
  if [[ -n "$MODEL" ]]; then
    cmd+=(--model "$MODEL")
  fi
  if [[ -n "$MAX_TOKENS" ]]; then
    cmd+=(--max-tokens "$MAX_TOKENS")
  fi
  if [[ -n "$RESUME" ]]; then
    cmd+=(--resume "$RESUME")
  fi
  if [[ -n "$TOOLS" ]]; then
    cmd+=(--tools "$TOOLS")
  fi
  if [[ -n "$ALLOWED_TOOLS" ]]; then
    cmd+=(--allowedTools "$ALLOWED_TOOLS")
  fi
  cmd+=(-p "$PROMPT")

  echo "running diagnostic prompt; dump=$DUMP_PATH" >&2
  run_status=0
  (cd "$ROOT_DIR" && "${cmd[@]}") > >(tee "$RUN_LOG" >&2) 2> >(tee -a "$RUN_LOG" >&2) || run_status=$?
  if [[ "$run_status" -ne 0 && "$ALLOW_RUN_FAILURE" != "true" ]]; then
    exit "$run_status"
  fi
  if [[ "$run_status" -ne 0 ]]; then
    echo "model run exited with status $run_status; continuing because --allow-run-failure was set" >&2
  fi
fi

if [[ -n "$REQUIRE_OUTPUT_TEXT$FORBID_OUTPUT_TEXT" && ! -s "$RUN_LOG" ]]; then
  echo "run log is missing or empty: $RUN_LOG" >&2
  exit 2
fi
if [[ -n "$REQUIRE_OUTPUT_TEXT" ]]; then
  IFS=',' read -ra required_output_parts <<<"$REQUIRE_OUTPUT_TEXT"
  for required_output in "${required_output_parts[@]}"; do
    required_output="${required_output#"${required_output%%[![:space:]]*}"}"
    required_output="${required_output%"${required_output##*[![:space:]]}"}"
    [[ -z "$required_output" ]] && continue
    if ! grep -Fq -- "$required_output" "$RUN_LOG"; then
      echo "run log missing required text: $required_output" >&2
      echo "run_log=$RUN_LOG" >&2
      exit 1
    fi
  done
fi
if [[ -n "$FORBID_OUTPUT_TEXT" ]]; then
  IFS=',' read -ra forbidden_output_parts <<<"$FORBID_OUTPUT_TEXT"
  for forbidden_output in "${forbidden_output_parts[@]}"; do
    forbidden_output="${forbidden_output#"${forbidden_output%%[![:space:]]*}"}"
    forbidden_output="${forbidden_output%"${forbidden_output##*[![:space:]]}"}"
    [[ -z "$forbidden_output" ]] && continue
    if grep -Fq -- "$forbidden_output" "$RUN_LOG"; then
      echo "run log contained forbidden text: $forbidden_output" >&2
      echo "run_log=$RUN_LOG" >&2
      exit 1
    fi
  done
fi

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump is missing or empty: $DUMP_PATH" >&2
  exit 2
fi

echo "verifying prompt dump: $DUMP_PATH" >&2
verify_cmd=(go run ./scripts/verify-code-mode-prompt-dump.go --min-turns "$MIN_TURNS")
verify_cmd+=(--require-final-tools-disabled="$REQUIRE_FINAL_TOOLS_DISABLED")
verify_cmd+=(--require-final-turn-budget="$REQUIRE_FINAL_TURN_BUDGET")
verify_cmd+=(--require-subagent-turn-budget-tools-disabled="$REQUIRE_SUBAGENT_TURN_BUDGET_TOOLS_DISABLED")
verify_cmd+=(--require-subagent-record="$REQUIRE_SUBAGENT_RECORD")
if [[ -n "$REQUIRE_TOOL_NO_RAW_OVER_LIMIT" ]]; then
  verify_cmd+=(--require-tool-no-raw-over-limit "$REQUIRE_TOOL_NO_RAW_OVER_LIMIT")
fi
if [[ -n "$REQUIRE_TOOL_NO_PERSISTED_OUTPUT" ]]; then
  verify_cmd+=(--require-tool-no-persisted-output "$REQUIRE_TOOL_NO_PERSISTED_OUTPUT")
fi
if [[ -n "$REQUIRE_TOOL_PERSISTED_OUTPUT" ]]; then
  verify_cmd+=(--require-tool-persisted-output "$REQUIRE_TOOL_PERSISTED_OUTPUT")
fi
if [[ -n "$REQUIRE_TOOL_MAX_BYTES" ]]; then
  verify_cmd+=(--require-tool-max-bytes "$REQUIRE_TOOL_MAX_BYTES")
fi
if [[ -n "$REQUIRE_TOOL_RESULT" ]]; then
  verify_cmd+=(--require-tool-result "$REQUIRE_TOOL_RESULT")
fi
if [[ -n "$REQUIRE_TOOL_USE_INPUT_TEXT" ]]; then
  verify_cmd+=(--require-tool-use-input-text "$REQUIRE_TOOL_USE_INPUT_TEXT")
fi
if [[ -n "$FORBID_TOOL" ]]; then
  verify_cmd+=(--forbid-tool "$FORBID_TOOL")
fi
if [[ -n "$REQUIRE_REQUEST_TEXT" ]]; then
  verify_cmd+=(--require-request-text "$REQUIRE_REQUEST_TEXT")
fi
if [[ -n "$FORBID_REQUEST_TEXT" ]]; then
  verify_cmd+=(--forbid-request-text "$FORBID_REQUEST_TEXT")
fi
verify_cmd+=("$DUMP_PATH")
(cd "$ROOT_DIR" && "${verify_cmd[@]}")
