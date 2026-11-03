#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="$(mktemp -d /tmp/niuma-heavy-acceptance-workspace-XXXXXX)"

awk 'BEGIN {
  line = "HEAVY_ACCEPTANCE_MARKER alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega"
  for (i = 1; i <= 3500; i++) print line
}' > "$WORKSPACE/heavy.txt"

HEAVY_PROMPT="必须先调用 Grep 工具在 heavy.txt 中搜索 HEAVY_ACCEPTANCE_MARKER。拿到结果后用一句话说明是否搜索成功。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --cwd "$WORKSPACE" \
  --prompt "$HEAVY_PROMPT" \
  --tools "Grep,LS,Read" \
  --max-turns 2 \
  --min-turns 2 \
  --require-tool-result "Grep" \
  --require-tool-persisted-output "Grep" \
  "$@"
