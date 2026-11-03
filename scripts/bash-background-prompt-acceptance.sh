#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

BASH_BACKGROUND_PROMPT="这是 Bash 后台能力验收。必须严格按顺序执行：
1. 先调用 Bash，参数必须包含 run_in_background=true，command 必须是：printf NIUMA_BG_READY; printf NIUMA_BG_DONE
2. Bash 返回后，从 tool_result 的 log_path 字段取得日志路径。
3. 再调用 Read 读取这个 log_path。
4. 最后用一句话说明是否读到了 NIUMA_BG_DONE。
不要修改文件。不要用 &。不要跳过工具调用。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --prompt "$BASH_BACKGROUND_PROMPT" \
  --tools "Bash,Read" \
  --allowed-tools "Bash(printf*)" \
  --max-turns 5 \
  --min-turns 3 \
  --full \
  --require-tool-result "Bash,Read" \
  --require-tool-use-input-text "Bash=run_in_background,Bash=NIUMA_BG_DONE,Read=background/logs" \
  --require-request-text "log_path,NIUMA_BG_DONE" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  "$@"
