#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-final-report-side-by-side-${TIMESTAMP}"
# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TARGET_CWD="$ROOT_DIR"
MODEL="gpt-5.5"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="32000"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/final-report-side-by-side-compare.sh [flags]

Runs the same final-report diagnostic against original Claude Code and
niuma, then compares request shape and behavior-level evidence. The
upstream side uses the local fetch stub in upstream-agent-lifecycle-capture;
the niuma side uses a real code-mode prompt dump with Agent,Grep,Read.

Flags:
  --work-dir <path>       Artifact directory. Default:
                          /tmp/niuma-final-report-side-by-side-<timestamp>
  --upstream-dir <path>   Original Claude Code source/extract dir. Default:
                          <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --target-cwd <path>     Workspace for both runs. Default: repo root.
  --model <name>          Model name for both runs. Default: gpt-5.5.
  --prompt-profile <name> niuma prompt profile. Default: claude-compatible.
  --max-tokens <n>        niuma max output tokens. Default: 32000.
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

if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir already exists: $WORK_DIR (use --force or choose another --work-dir)" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" ]]; then
  rm -rf "$WORK_DIR"
fi
mkdir -p "$WORK_DIR"

GO_DUMP="$WORK_DIR/go-final-report.jsonl"
GO_RUN_LOG="$WORK_DIR/go-final-report.run.log"
UPSTREAM_WORK_DIR="$WORK_DIR/upstream"
UPSTREAM_CAPTURE="$UPSTREAM_WORK_DIR/capture.jsonl"
UPSTREAM_REPORT="$UPSTREAM_WORK_DIR/report.json"
PROMPT_COMPARE="$WORK_DIR/promptdump-compare.json"
BEHAVIOR_COMPARE="$WORK_DIR/behavior-compare.json"
SUMMARY_JSON="$WORK_DIR/summary.json"

echo "running upstream final-report capture; work_dir=$UPSTREAM_WORK_DIR" >&2
"$ROOT_DIR/scripts/upstream-agent-lifecycle-capture.sh" \
  --upstream-dir "$UPSTREAM_DIR" \
  --target-cwd "$TARGET_CWD" \
  --work-dir "$UPSTREAM_WORK_DIR" \
  --scenario final-report \
  --model "$MODEL" \
  --force

echo "running niuma same-shape final-report gate; dump=$GO_DUMP" >&2
"$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --cwd "$TARGET_CWD" \
  --dump "$GO_DUMP" \
  --run-log "$GO_RUN_LOG" \
  --force \
  --full \
  --prompt-profile "$PROMPT_PROFILE" \
  --model "$MODEL" \
  --max-tokens "$MAX_TOKENS" \
  --max-turns 6 \
  --min-turns 3 \
  --tools "Agent,Grep,Read" \
  --require-subagent-record \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  --no-require-subagent-turn-budget-tools-disabled \
  --require-tool-result "Grep,Read,Agent" \
  --require-tool-use-input-text "Grep=runtimeTodoStatus,Read=internal/query/query.go,Read=3489,Read=internal/tools/task/task.go,Read=internal/tools/agent/agent.go,Agent=Task/Agent,Agent=general-purpose,Agent=sonnet,Agent=subagentToolPlanningStatus" \
  --forbid-tool "TodoWrite,Write,Edit,MultiEdit,Bash,AgentCreate,Task,LS,Glob" \
  --require-request-text "## Sub-agent tool planning reminder,output_mode:\"content\",offset and limit,## Background agent tasks" \
  --require-output-text "runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,internal/query/query.go,internal/query/query.go:3491,internal/query/query.go:3505,internal/query/query.go:3681,internal/query/query.go:3730,internal/query/query.go:3746,internal/tools/task/task.go,internal/tools/task/task.go:91,internal/tools/task/task.go:138,internal/tools/agent/agent.go,internal/tools/agent/agent.go:67,internal/agentruntime/runtime.go,subagentToolPlanningStatus" \
  --forbid-output-text "未能提供可靠函数/行号,未完全闭环,尚未完全闭环,没有读取对应测试文件,无法可靠给出函数级,父线程没有成功,没有在父线程读取到,证据强度低于,非 \`Read\` 输出,gate 未完全通过,本次 gate 失败,父线程没有直接 \`Read internal/tools/task/task.go\`,父线程没有直接 \`Read internal/tools/agent/agent.go\`" \
  --prompt "真实复杂只读诊断任务：分析本仓库 TodoWrite、Task/Agent、tool_result、plan/progress 上下文如何进入模型请求，并给出文件/函数/行号证据。要求：1. 父线程必须先用 Grep 搜索 runtimeTodoStatus 或 Background agent tasks 相关实现；2. 父线程必须用 Read 读取 internal/query/query.go 的 runtime status 区间，Read input 必须包含 offset=3489，limit 应覆盖到 runtimeAgentTaskStatus；还必须用 Read 读取 internal/tools/task/task.go、internal/tools/agent/agent.go 的相关片段；3. 必须调用 Agent 派发一个只读子任务，让子任务只分析 Task/Agent 工具提示词和生命周期证据，子任务不得修改文件；调用 Agent 时必须使用 subagent_type=\"general-purpose\" 且 model=\"sonnet\"，不要使用 Explore、haiku 或其它未确认可用的子模型；4. 子任务必须使用 Grep 定位后再用 Read 的 offset/limit 读取 internal/tools/task/task.go、internal/tools/agent/agent.go、internal/agentruntime/runtime.go 的最小必要片段；子任务必须专门 Grep subagentToolPlanningStatus，并读取 internal/agentruntime/runtime.go 中 subagentRuntimeStatusText 调用 subagentToolPlanningStatus 以及 subagentToolPlanningStatus 函数定义附近的代码；5. 父线程综合子任务结果后用 P0/P1/P2 总结，最终答案必须包含 runtimeStatusText、runtimeTodoStatus、runtimePlanStatus、runtimeAgentTaskStatus、subagentToolPlanningStatus，以及 internal/query/query.go:3491、internal/query/query.go:3505、internal/query/query.go:3681、internal/query/query.go:3730、internal/query/query.go:3746、internal/tools/task/task.go、internal/tools/agent/agent.go、internal/agentruntime/runtime.go 的文件/函数/行号证据；6. 如果没有完成上述 Read，不要把 Grep 行号包装成已验证证据，应明确失败并让本 gate 失败。全程不要修改文件，不要调用写入工具。"

echo "comparing prompt dumps..." >&2
(cd "$ROOT_DIR" && go run ./scripts/promptdump-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$PROMPT_COMPARE"

echo "comparing behavior evidence..." >&2
(cd "$ROOT_DIR" && go run ./scripts/behavior-eval-compare --go "$GO_DUMP" --upstream "$UPSTREAM_CAPTURE") >"$BEHAVIOR_COMPARE"

node - "$SUMMARY_JSON" "$GO_DUMP" "$GO_RUN_LOG" "$UPSTREAM_REPORT" "$UPSTREAM_CAPTURE" "$PROMPT_COMPARE" "$BEHAVIOR_COMPARE" "$MODEL" "$PROMPT_PROFILE" "$MAX_TOKENS" <<'EOF_SUMMARY'
const fs = require("fs");

const [
  summaryPath,
  goDump,
  goRunLog,
  upstreamReportPath,
  upstreamCapture,
  promptComparePath,
  behaviorComparePath,
  model,
  promptProfile,
  maxTokens,
] = process.argv.slice(2);

function readJson(path) {
  return JSON.parse(fs.readFileSync(path, "utf8"));
}

const upstream = readJson(upstreamReportPath);
const promptCompare = readJson(promptComparePath);
const behaviorCompare = readJson(behaviorComparePath);
const summary = {
  ok: Boolean(upstream.ok && promptCompare.ok && behaviorCompare.ok),
  model,
  prompt_profile: promptProfile,
  max_tokens: Number(maxTokens),
  artifacts: {
    go_dump: goDump,
    go_run_log: goRunLog,
    upstream_report: upstreamReportPath,
    upstream_capture: upstreamCapture,
    prompt_compare: promptComparePath,
    behavior_compare: behaviorComparePath,
  },
  upstream: {
    ok: upstream.ok,
    request_count: upstream.request_count,
    response_classes: upstream.response_classes,
    final_report_required_terms_visible: upstream.findings && upstream.findings.final_report_required_terms_visible,
    transcript: upstream.transcript,
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
