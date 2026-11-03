#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TASK_PROMPT="必须先调用 Task 工具派发一个只读子任务。子任务只需要用 LS 查看当前目录，并基于 LS 结果用不超过 3 个条目回答。父线程拿到 Task 结果后，用一句话说明是否成功收到了子 agent 结果。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --prompt "$TASK_PROMPT" \
  --tools "Task,LS,Read" \
  --max-turns 4 \
  --min-turns 3 \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  --require-subagent-record \
  "$@"
