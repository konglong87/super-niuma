#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

READ_PROMPT="必须先调用 Read 读取 internal/query/query.go 的前 80 行，然后用一句话说明是否看到了 Options 结构体。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --prompt "$READ_PROMPT" \
  --tools "Read" \
  --max-turns 3 \
  --min-turns 2 \
  --full \
  --require-tool-result "Read" \
  --require-request-text "Whenever you read a file,Options struct" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  "$@"
