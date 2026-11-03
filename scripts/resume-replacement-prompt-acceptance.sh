#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WORKSPACE="$(mktemp -d /tmp/niuma-resume-acceptance-workspace-XXXXXX)"
CONFIG_DIR="$(mktemp -d /tmp/niuma-resume-acceptance-config-XXXXXX)"
SESSION_ID="24242424-2424-4242-8424-242424242424"
RAW_MARKER="RAW_RESUME_ACCEPTANCE_MARKER"
REPLACEMENT_TEXT="<persisted-output>\\nresume acceptance preview\\n</persisted-output>"

abs_workspace="$(cd "$WORKSPACE" && pwd -P)"
slug="${abs_workspace#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
transcript_path="$transcript_dir/$SESSION_ID.jsonl"

mkdir -p "$transcript_dir"
raw_content="${RAW_MARKER}_$(printf '%08000d' 0)"
cat > "$transcript_path" <<JSONL
{"type":"message","role":"user","content":"resume acceptance previous user message"}
{"type":"tool_call","tool_id":"toolu_resume_acceptance","tool_name":"Echo","content":"{\"text\":\"large\"}"}
{"type":"tool_result","tool_id":"toolu_resume_acceptance","tool_name":"Echo","content":"$raw_content"}
{"type":"content_replacement","replacements":[{"kind":"tool-result","tool_use_id":"toolu_resume_acceptance","replacement":"$REPLACEMENT_TEXT"}]}
JSONL

export NIUMA_CONFIG_DIR="$CONFIG_DIR"

RESUME_PROMPT="继续上一轮，只用一句话说明 resume replacement 已生效。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --cwd "$WORKSPACE" \
  --resume "$SESSION_ID" \
  --prompt "$RESUME_PROMPT" \
  --max-turns 1 \
  --min-turns 1 \
  --full \
  --require-request-text "<persisted-output>,resume acceptance preview,Continue from where you left off." \
  --forbid-request-text "$RAW_MARKER" \
  "$@"
