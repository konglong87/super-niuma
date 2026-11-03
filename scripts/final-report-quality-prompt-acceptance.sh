#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
DUMP_PATH="/tmp/niuma-final-report-quality-${TIMESTAMP}.jsonl"
RUN_LOG="/tmp/niuma-final-report-quality-${TIMESTAMP}.run.log"
SCORE_REPORT=""
MODEL=""
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="16000"
FORCE="false"
VERIFY_ONLY="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/final-report-quality-prompt-acceptance.sh [flags]

Runs the real code-mode diagnostic task and verifies both request shape and
final report quality. This gate is intentionally stricter than prompt-shape
acceptance: the final answer must include function-level Task/Agent/runtime
evidence instead of admitting that the evidence was not collected.

Flags:
  --dump <path>          Prompt dump JSONL path.
  --run-log <path>       Combined stdout/stderr run log path.
  --score-report <path>  Final report evidence score JSON path. Default:
                         <run-log>.score.json.
  --model <name>         Optional model override passed to niuma --model.
  --prompt-profile <name>
                         NIUMA_PROMPT_PROFILE value.
  --max-tokens <n>       Maximum model output tokens per turn. Default: 16000.
  --verify-only          Verify existing dump/run-log and write score report.
  --force                Remove existing dump/log before running.
  -h, --help             Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dump)
      DUMP_PATH="${2:?missing value for --dump}"
      shift 2
      ;;
    --run-log)
      RUN_LOG="${2:?missing value for --run-log}"
      shift 2
      ;;
    --score-report)
      SCORE_REPORT="${2:?missing value for --score-report}"
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
    --verify-only)
      VERIFY_ONLY="true"
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

if [[ "$FORCE" == "true" && "$VERIFY_ONLY" != "true" ]]; then
  rm -f "$DUMP_PATH" "$RUN_LOG"
  if [[ -n "$SCORE_REPORT" ]]; then
    rm -f "$SCORE_REPORT"
  fi
fi
if [[ -z "$SCORE_REPORT" ]]; then
  SCORE_REPORT="${RUN_LOG}.score.json"
fi

cmd=(
  "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh"
  --dump "$DUMP_PATH"
  --run-log "$RUN_LOG"
  --force
  --full
  --prompt-profile "$PROMPT_PROFILE"
  --max-tokens "$MAX_TOKENS"
  --max-turns 6
  --min-turns 3
  --tools "LS,Grep,Read,Task"
  --require-subagent-record
  --no-require-final-tools-disabled
  --no-require-final-turn-budget
  --require-tool-result "Grep,Read,Task"
  --require-tool-use-input-text "Grep=runtimeTodoStatus,Read=internal/query/query.go,Read=3489,Read=internal/tools/task/task.go,Read=internal/tools/agent/agent.go,Task=Task/Agent,Task=subagentToolPlanningStatus"
  --forbid-tool "TodoWrite,Write,Edit,MultiEdit,Bash,AgentCreate"
  --require-request-text "## Sub-agent tool planning reminder,output_mode:\"content\",offset and limit,## Background agent tasks"
  --require-output-text "runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,internal/query/query.go,internal/query/query.go:3491,internal/query/query.go:3505,internal/query/query.go:3681,internal/query/query.go:3730,internal/query/query.go:3746,internal/tools/task/task.go,internal/tools/agent/agent.go,internal/agentruntime/runtime.go,subagentToolPlanningStatus"
  --forbid-output-text "未能提供可靠函数/行号,未完全闭环,尚未完全闭环,没有读取对应测试文件,无法可靠给出函数级,父线程没有成功,没有在父线程读取到,证据强度低于,非 \`Read\` 输出"
  --prompt "真实复杂只读诊断任务：分析本仓库 TodoWrite、Task/Agent、tool_result、plan/progress 上下文如何进入模型请求，并给出文件/函数/行号证据。要求：1. 父线程必须先用 Grep 搜索 runtimeTodoStatus 或 Background agent tasks 相关实现；2. 父线程必须用 Read 读取 internal/query/query.go 的 runtime status 区间，Read input 必须包含 offset=3489，limit 应覆盖到 runtimeAgentTaskStatus；还必须用 Read 读取 internal/tools/task/task.go、internal/tools/agent/agent.go 的相关片段；3. 必须调用 Task 派发一个只读子任务，让子任务只分析 Task/Agent 工具提示词和生命周期证据，子任务不得修改文件；Task 子任务 brief 必须明确包含 subagentToolPlanningStatus；4. 子任务必须使用 Grep 定位后再用 Read 的 offset/limit 读取 internal/tools/task/task.go、internal/tools/agent/agent.go、internal/agentruntime/runtime.go 的最小必要片段；子任务必须专门 Grep subagentToolPlanningStatus，并读取 internal/agentruntime/runtime.go 中 subagentRuntimeStatusText 调用 subagentToolPlanningStatus 以及 subagentToolPlanningStatus 函数定义附近的代码；5. 父线程综合子任务结果后用 P0/P1/P2 总结，最终答案必须包含 runtimeStatusText、runtimeTodoStatus、runtimePlanStatus、runtimeAgentTaskStatus、subagentToolPlanningStatus，以及 internal/query/query.go:3491、internal/query/query.go:3505、internal/query/query.go:3681、internal/query/query.go:3730、internal/query/query.go:3746、internal/tools/task/task.go、internal/tools/agent/agent.go、internal/agentruntime/runtime.go 的文件/函数/行号证据；6. 最终答案必须包含英文标签 Evidence:、Unknowns:、Verification:、Risks:、Next action:；Evidence 和 Verification 必须带文件路径、函数/字段名、行号、命令、测试、dump 或 transcript 锚点；7. 最终答案必须覆盖 Tool.Description、Tool.Run、runSingle、AgentCreate、AgentGet；8. 如果没有完成上述 Read，不要把 Grep 行号包装成已验证证据，应明确失败并让本 gate 失败。全程不要修改文件，不要调用写入工具。"
)

if [[ -n "$MODEL" ]]; then
  cmd+=(--model "$MODEL")
fi
if [[ "$VERIFY_ONLY" == "true" ]]; then
  cmd+=(--verify-only)
fi

"${cmd[@]}"

(cd "$ROOT_DIR" && go run ./scripts/final-report-evidence-score \
  --run-log "$RUN_LOG" \
  --min-score 20) >"$SCORE_REPORT"

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "run_log=$RUN_LOG"
echo "score_report=$SCORE_REPORT"
