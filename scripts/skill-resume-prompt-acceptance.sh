#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WORKSPACE="$(mktemp -d /tmp/niuma-skill-resume-acceptance-workspace-XXXXXX)"
CONFIG_DIR="$(mktemp -d /tmp/niuma-skill-resume-acceptance-config-XXXXXX)"
SESSION_ID="34343434-3434-4343-8434-343434343434"
SKILL_MARKER="RESUME_SKILL_ACTIVE_CONTEXT_MARKER"

abs_workspace="$(cd "$WORKSPACE" && pwd -P)"
slug="${abs_workspace#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
transcript_path="$transcript_dir/$SESSION_ID.jsonl"

mkdir -p "$transcript_dir"
node - "$transcript_path" "$SKILL_MARKER" <<'NODE'
const fs = require("fs");
const path = process.argv[2];
const marker = process.argv[3];
const skillContext = `<system-reminder>
Skill resume-skill instructions are now active. Follow them for subsequent work unless they conflict with higher-priority instructions.

${marker}
</system-reminder>`;
const entries = [
  { type: "message", role: "user", content: "load resume skill before compaction" },
  { type: "tool_call", tool_id: "toolu_resume_skill", tool_name: "Skill", content: "{\"name\":\"resume-skill\"}" },
  { type: "tool_result", tool_id: "toolu_resume_skill", tool_name: "Skill", content: "Skill resume-skill loaded. Its instructions have been added to the conversation context." },
  { type: "message", role: "user", content: skillContext },
  { type: "compact_summary", content: "## Current Goal\nContinue compacted skill-guided work.\n## Open Tasks\nVerify resumed active skill context.\n## Important Raw Facts\nThe skill was loaded before compaction." }
];
fs.writeFileSync(path, entries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

export NIUMA_CONFIG_DIR="$CONFIG_DIR"

RESUME_PROMPT="继续上一轮，只用一句话说明 resume skill context 是否仍在请求上下文。不要修改文件。"

exec "$ROOT_DIR/scripts/code-mode-prompt-acceptance.sh" \
  --cwd "$WORKSPACE" \
  --resume "$SESSION_ID" \
  --prompt "$RESUME_PROMPT" \
  --max-turns 1 \
  --min-turns 1 \
  --full \
  --require-request-text "Conversation summary so far,$SKILL_MARKER" \
  --no-require-final-tools-disabled \
  --no-require-final-turn-budget \
  "$@"
