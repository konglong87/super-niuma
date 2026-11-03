#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

BACKGROUND_PROMPT="必须先调用 AgentCreate 创建一个后台子 agent。子 agent 不要使用任何工具，只回答：done。主线程拿到 handle 后，必须调用 Bash 执行 sleep 5，然后调用 AgentGet 获取结果，最后一句话总结是否看到了 completed 状态。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --prompt "$BACKGROUND_PROMPT" \
  --tools "AgentCreate,AgentGet,Bash" \
  --allowed-tools "Bash(sleep *)" \
  --max-turns 5 \
  --min-turns 4 \
  --full \
  --require-tool-result "AgentCreate,Bash,AgentGet" \
  --require-request-text "completion notification: result is ready,call AgentGet before using findings,output_file,capability_loop,evidence:,unknowns:,verification:,next_action:" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  "$@"
