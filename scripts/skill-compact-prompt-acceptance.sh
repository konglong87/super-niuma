#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-skill-compact-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-skill-compact-${TIMESTAMP}.jsonl"
MODEL="skill-compact-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/skill-compact-prompt-acceptance.sh [flags]

Runs a deterministic CLI prompt-dump gate for the real auto-compact path:
Skill tool activation -> active skill context message -> auto compact before
the next model request -> active skill context reinjection.

Flags:
  --work-dir <path>       Output/work directory. Default: /tmp/niuma-skill-compact-<timestamp>.
  --dump <path>           Prompt dump JSONL path. Default: /tmp/niuma-skill-compact-<timestamp>.jsonl.
  --model <name>          Model name sent to the local stub provider.
  --prompt-profile <name> NIUMA_PROMPT_PROFILE value.
  --force                 Remove existing --work-dir/--dump first.
  -h, --help              Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir)
      WORK_DIR="${2:?missing value for --work-dir}"
      shift 2
      ;;
    --dump)
      DUMP_PATH="${2:?missing value for --dump}"
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
if [[ -e "$DUMP_PATH" && "$FORCE" != "true" ]]; then
  echo "dump path already exists: $DUMP_PATH (use --force or choose another --dump)" >&2
  exit 2
fi

rm -rf "$WORK_DIR"
rm -f "$DUMP_PATH"
mkdir -p "$WORK_DIR"

READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
REPORT_JSON="$WORK_DIR/report.json"
CONFIG_DIR="$WORK_DIR/config"
WORKSPACE="$WORK_DIR/workspace"
HOME_DIR="$WORK_DIR/home"
REAL_GOMODCACHE="$(go env GOMODCACHE 2>/dev/null || true)"
REAL_GOCACHE="$(go env GOCACHE 2>/dev/null || true)"

mkdir -p "$CONFIG_DIR" "$WORKSPACE/.claude/skills/matrix-skill" "$HOME_DIR/.niuma"

cat >"$HOME_DIR/.niuma/settings.json" <<'JSON'
{
  "contextLength": 100,
  "autoCompact": {
    "enabled": true,
    "defaultThresholdRatio": 0.01,
    "preserveRecentRounds": 1,
    "maxSummaryTokens": 800,
    "cooldownTurns": 2,
    "modelContext": {
      "skill-compact-stub": 700
    }
  }
}
JSON

cat >"$WORKSPACE/.claude/skills/matrix-skill/SKILL.md" <<'SKILL'
---
name: matrix-skill
description: Matrix compact acceptance skill
---

MATRIX_SKILL_ACTIVE

When this skill is active after compaction, answer with MATRIX_SKILL_ACTIVE
and do not modify files.
SKILL

cat >"$WORKSPACE/compact-input.txt" <<'EOF'
COMPACT_READ_MARKER

This fixture makes the pre-compact conversation large enough to trigger the
auto-compact gate after Skill activation without touching the real repository.
EOF

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.SKILL_COMPACT_PROVIDER_READY || "";
const requestLog = process.env.SKILL_COMPACT_PROVIDER_LOG || "";
let call = 0;

function append(entry) {
  fs.appendFileSync(requestLog, JSON.stringify({ time: new Date().toISOString(), ...entry }) + "\n");
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", chunk => { body += chunk; });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

function requestText(parsed) {
  const parts = [];
  for (const message of parsed.messages || []) {
    const content = message.content;
    if (typeof content === "string") {
      parts.push(content);
      continue;
    }
    if (!Array.isArray(content)) continue;
    for (const block of content) {
      if (typeof block === "string") {
        parts.push(block);
      } else if (block && typeof block.text === "string") {
        parts.push(block.text);
      } else if (block && typeof block.content === "string") {
        parts.push(block.content);
      }
    }
  }
  return parts.join("\n");
}

function writeSSE(res, payload) {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

function streamText(res, model, text) {
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const id = `chatcmpl-skill-compact-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 64, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 96 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const completionID = `chatcmpl-skill-compact-${Date.now()}`;
  writeSSE(res, {
    id: completionID,
    object: "chat.completion.chunk",
    model,
    choices: [{
      index: 0,
      delta: {
        role: "assistant",
        tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }],
      },
    }],
  });
  writeSSE(res, {
    id: completionID,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }],
    usage: { prompt_tokens: 96, completion_tokens: 16, total_tokens: 112 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

function compactSummary() {
  return [
    "## Current Goal",
    "Verify active skill context survives auto compaction.",
    "## User Preferences / Constraints",
    "Do not modify files.",
    "## Decisions Made",
    "Use a deterministic stub provider and temporary workspace.",
    "## Files / Code Changed",
    "compact-input.txt",
    "## Commands / Test Results",
    "Skill matrix-skill was loaded before compaction.",
    "## Open Tasks",
    "Confirm MATRIX_SKILL_ACTIVE appears after compact summary.",
    "## Known Issues / Risks",
    "None.",
    "## Important Raw Facts",
    "MATRIX_SKILL_ACTIVE was observed before compaction."
  ].join("\n");
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  call += 1;
  const model = parsed.model || "skill-compact-stub";
  const text = requestText(parsed);
  const raw = JSON.stringify(parsed);
  const isSummaryRequest = text.includes("Summarize the conversation history below for continuing an agent session.");
  append({
    call,
    is_summary_request: isSummaryRequest,
    has_compact_summary: raw.includes("Conversation summary so far:"),
    has_skill_marker: raw.includes("MATRIX_SKILL_ACTIVE"),
    skill_marker_count: (raw.match(/MATRIX_SKILL_ACTIVE/g) || []).length,
    active_skill_reminder_count: (raw.match(/Skill matrix-skill instructions are now active/g) || []).length,
    has_raw_read_marker: raw.includes("COMPACT_READ_MARKER"),
    tool_names: (parsed.tools || []).map(tool => tool?.function?.name || tool?.name || "").filter(Boolean),
  });

  if (isSummaryRequest) {
    streamText(res, model, compactSummary());
    return;
  }
  if (!raw.includes("Launching skill: matrix-skill") && !raw.includes("Skill matrix-skill instructions are now active")) {
    streamToolCall(res, model, "call_skill_compact_skill", "Skill", { skill: "matrix-skill" });
    return;
  }
  if (!raw.includes("COMPACT_READ_MARKER")) {
    streamToolCall(res, model, "call_skill_compact_read", "Read", { file_path: "compact-input.txt" });
    return;
  }
  streamText(res, model, "SKILL_COMPACT_FINAL_OK MATRIX_SKILL_ACTIVE");
}

const server = http.createServer(async (req, res) => {
  try {
    if (req.method === "POST" && (req.url === "/v1/chat/completions" || req.url === "/chat/completions")) {
      await handleChat(req, res);
      return;
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: { message: `not found: ${req.method} ${req.url}` } }));
  } catch (error) {
    res.writeHead(500, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: { message: String(error?.stack || error) } }));
  }
});

server.listen(0, "127.0.0.1", () => {
  const address = server.address();
  fs.writeFileSync(readyFile, JSON.stringify({ port: address.port }) + "\n");
});
NODE

SKILL_COMPACT_PROVIDER_READY="$READY_FILE" \
SKILL_COMPACT_PROVIDER_LOG="$PROVIDER_LOG" \
node "$PROVIDER_SCRIPT" &
provider_pid=$!
cleanup() {
  kill "$provider_pid" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for _ in $(seq 1 100); do
  if [[ -s "$READY_FILE" ]]; then
    break
  fi
  sleep 0.05
done
if [[ ! -s "$READY_FILE" ]]; then
  echo "provider did not become ready" >&2
  exit 1
fi
provider_port="$(node -e 'const fs=require("fs"); const p=JSON.parse(fs.readFileSync(process.argv[1],"utf8")); console.log(p.port)' "$READY_FILE")"

export NIUMA_CONFIG_DIR="$CONFIG_DIR"
export NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH"
export NIUMA_DUMP_PROMPT_FULL="true"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_PROVIDER="custom"
export HOME="$HOME_DIR"
if [[ -n "$REAL_GOMODCACHE" ]]; then
  export GOMODCACHE="$REAL_GOMODCACHE"
fi
if [[ -n "$REAL_GOCACHE" ]]; then
  export GOCACHE="$REAL_GOCACHE"
fi
export ANTHROPIC_API_KEY="skill-compact-test-key"
export ANTHROPIC_BASE_URL="http://127.0.0.1:${provider_port}/v1"
export CLAUDE_CODE_MODEL="$MODEL"

set +e
(
  cd "$ROOT_DIR"
  go run ./cmd/niuma \
    --cwd "$WORKSPACE" \
    --max-turns 3 \
    --model "$MODEL" \
    --tools "Skill,Read" \
    -p "SKILL_COMPACT_PROMPT: load matrix-skill, read compact-input.txt, then after any compacted continuation answer SKILL_COMPACT_FINAL_OK. Do not modify files."
) >"$CLI_LOG" 2>&1
run_status=$?
set -e

if [[ "$run_status" -ne 0 ]]; then
  echo "niuma run failed with status $run_status" >&2
  echo "cli log: $CLI_LOG" >&2
  tail -120 "$CLI_LOG" >&2 || true
  exit "$run_status"
fi
if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  exit 1
fi

go run "$ROOT_DIR/scripts/verify-code-mode-prompt-dump.go" \
  --min-turns 3 \
  --require-final-tools-disabled=false \
  --require-final-turn-budget=false \
  --require-tool-no-raw-over-limit "" \
  --require-tool-no-persisted-output "" \
  --require-tool-max-bytes "" \
  --require-tool-result "Read" \
  --require-request-text "Conversation summary so far,MATRIX_SKILL_ACTIVE" \
  "$DUMP_PATH"

node - "$PROVIDER_LOG" "$DUMP_PATH" "$CLI_LOG" "$REPORT_JSON" <<'NODE'
const fs = require("fs");
const [providerLogPath, dumpPath, cliLogPath, reportPath] = process.argv.slice(2);

function jsonl(path) {
  return fs.readFileSync(path, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
}

const providerRecords = jsonl(providerLogPath);
const dumpRecords = jsonl(dumpPath);
const fullRequests = dumpRecords.map(record => JSON.stringify(record));
const compactedRequests = fullRequests.filter(text => text.includes("Conversation summary so far:"));
const compactedWithSkill = compactedRequests.filter(text => text.includes("MATRIX_SKILL_ACTIVE"));
const cliLog = fs.readFileSync(cliLogPath, "utf8");
const hasFinalOutput = cliLog.includes("SKILL_COMPACT_FINAL_OK");
const maxSkillMarkerCount = Math.max(0, ...fullRequests.map(text => (text.match(/MATRIX_SKILL_ACTIVE/g) || []).length));
const hasSummaryRequest = providerRecords.some(record => record.is_summary_request);
const hasMainCompactedProviderRequest = providerRecords.some(record => !record.is_summary_request && record.has_compact_summary && record.has_skill_marker);
const sawSkillToolSurface = providerRecords.some(record => record.tool_names.includes("Skill"));
const maxActiveSkillReminderCount = Math.max(0, ...providerRecords.map(record => record.active_skill_reminder_count || 0));

const ok = Boolean(
  hasSummaryRequest &&
  hasMainCompactedProviderRequest &&
  sawSkillToolSurface &&
  compactedWithSkill.length > 0 &&
  hasFinalOutput &&
  maxActiveSkillReminderCount === 1
);
const report = {
  ok,
  has_summary_request: hasSummaryRequest,
  has_main_compacted_provider_request: hasMainCompactedProviderRequest,
  saw_skill_tool_surface: sawSkillToolSurface,
  compacted_request_count: compactedRequests.length,
  compacted_with_skill_count: compactedWithSkill.length,
  has_final_output: hasFinalOutput,
  max_skill_marker_count: maxSkillMarkerCount,
  max_active_skill_reminder_count: maxActiveSkillReminderCount,
  provider_log: providerLogPath,
  dump: dumpPath,
};
fs.writeFileSync(reportPath, JSON.stringify(report, null, 2) + "\n");
if (!ok) {
  console.error(JSON.stringify(report, null, 2));
  process.exit(1);
}
console.log(JSON.stringify(report, null, 2));
NODE

echo "skill compact prompt acceptance passed"
echo "work_dir=$WORK_DIR"
echo "dump=$DUMP_PATH"
