#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

CWD="$ROOT_DIR"
WORK_DIR="/tmp/niuma-agent-long-output-resume-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-long-output-resume-${TIMESTAMP}.jsonl"
MODEL="agent-long-output-resume-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"
VERIFY_ONLY="false"
AUTO_COMPACT="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-long-output-resume-acceptance.sh [flags]

Builds a resume transcript whose background Agent completed with a very large
result. The resumed request must surface output_file and AgentGet must return a
structured content_preview instead of re-inlining or generic-persisting the full
Agent result.

Flags:
  --cwd <path>             Workspace cwd. Default: repo root.
  --work-dir <path>        Temp work dir for isolated config/provider logs.
  --dump <path>            Prompt dump path.
  --model <name>           Model name sent to niuma.
  --prompt-profile <name>  Prompt profile. Default: claude-compatible.
  --auto-compact           Use an isolated workspace/settings and require real auto compact.
  --verify-only            Verify an existing --dump without running the CLI.
  --force                  Remove existing dump/work paths before running.
  -h, --help               Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cwd)
      CWD="${2:?missing value for --cwd}"
      shift 2
      ;;
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
    --auto-compact)
      AUTO_COMPACT="true"
      shift
      ;;
    --verify-only)
      VERIFY_ONLY="true"
      shift
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

verify_dump() {
  local required_text="## Background agent tasks,completed,completion notification: result is ready,content_preview,content_truncated,content_bytes,output_file,long-agent.output"
  if [[ "$AUTO_COMPACT" == "true" ]]; then
    required_text="Conversation summary so far,$required_text"
  fi
  go run ./scripts/verify-code-mode-prompt-dump.go \
    --min-turns 2 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-tool-result "AgentGet" \
    --require-tool-no-persisted-output "AgentGet" \
    --require-tool-max-bytes "AgentGet=10000" \
    --require-request-text "$required_text" \
    --forbid-request-text "AGENT_LONG_OUTPUT_TAIL_MARKER,<persisted-output>" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" "$AUTO_COMPACT" <<'NODE'
const fs = require("fs");
const dump = process.argv[2];
const autoCompact = process.argv[3] === "true";
const records = fs.readFileSync(dump, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
let foundStructuredAgentGet = false;
let foundCompactedRuntimeStatus = false;
for (const record of records) {
  const rawRequest = JSON.stringify(record.request || {});
  if (autoCompact && rawRequest.includes("Conversation summary so far") && rawRequest.includes("## Background agent tasks") && rawRequest.includes("long-agent.output")) {
    foundCompactedRuntimeStatus = true;
  }
  for (const message of record.request?.messages || []) {
    for (const block of message.content || []) {
      if (block.type !== "tool_result" || block.tool_use_id !== "call_agent_get_long_output_resume") continue;
      const content = typeof block.content === "string" ? JSON.parse(block.content) : block.content;
      const result = content?.result || {};
      if (content?.task?.status === "completed" && result.content_preview && result.content_truncated === true && result.content_bytes > 50000 && String(result.output_file || "").endsWith("long-agent.output")) {
        foundStructuredAgentGet = true;
      }
    }
  }
}
if (!foundStructuredAgentGet) {
  console.error("AgentGet result did not include structured long-result summary and output_file");
  process.exit(1);
}
if (autoCompact && !foundCompactedRuntimeStatus) {
  console.error("auto-compact dump did not include compacted runtime status with output_file");
  process.exit(1);
}
const requestText = JSON.stringify(records.map(record => record.request || {}));
if (requestText.includes("AGENT_LONG_OUTPUT_TAIL_MARKER") || requestText.includes("<persisted-output>")) {
  console.error("long Agent result leaked tail marker or generic persisted-output into request");
  process.exit(1);
}
NODE
}

if [[ "$VERIFY_ONLY" == "true" ]]; then
  if [[ ! -s "$DUMP_PATH" ]]; then
    echo "prompt dump missing for verify-only: $DUMP_PATH" >&2
    exit 2
  fi
  verify_dump >&2
  verify_markers
  echo "ok=true"
  echo "auto_compact=$AUTO_COMPACT"
  echo "dump=$DUMP_PATH"
  exit 0
fi

if [[ "$FORCE" == "true" ]]; then
  rm -rf "$WORK_DIR" "$DUMP_PATH"
fi
if [[ -e "$WORK_DIR" || -e "$DUMP_PATH" ]]; then
  echo "work or dump path already exists; use --force or choose another path" >&2
  exit 2
fi

mkdir -p "$WORK_DIR"
if [[ "$AUTO_COMPACT" == "true" ]]; then
  CWD="$WORK_DIR/workspace"
  mkdir -p "$CWD"
  printf '# Agent Long Output Compact Fixture\n' > "$CWD/README.md"
fi
READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
SESSION_ID="23232323-2323-4232-8232-232323232323"
CONFIG_DIR="$WORK_DIR/config"
HOME_DIR="$WORK_DIR/home"
REAL_GOMODCACHE="$(go env GOMODCACHE 2>/dev/null || true)"
REAL_GOCACHE="$(go env GOCACHE 2>/dev/null || true)"

abs_cwd="$(cd "$CWD" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
transcript_path="$transcript_dir/$SESSION_ID.jsonl"
output_file="$transcript_dir/long-agent.output"
mkdir -p "$transcript_dir"

node - "$transcript_path" "$output_file" "$AUTO_COMPACT" <<'NODE'
const fs = require("fs");
const transcript = process.argv[2];
const outputFile = process.argv[3];
const autoCompact = process.argv[4] === "true";
const longContent = "AGENT_LONG_OUTPUT_BODY ".repeat(5000) + "AGENT_LONG_OUTPUT_TAIL_MARKER";
const historyPadding = autoCompact ? "\n" + "AGENT_LONG_OUTPUT_HISTORY_PADDING ".repeat(1500) : "";
fs.writeFileSync(outputFile, longContent);
const result = {
  content: longContent,
  status: "completed",
  agent_name: "general-purpose",
  model: "model",
  session_id: "long-output-subagent",
  transcript_path: outputFile.replace(/\.output$/, ".jsonl"),
  output_file: outputFile,
  turns: 4,
  task_id: 1
};
fs.writeFileSync(`${outputFile}.state.json`, JSON.stringify({ status: "completed", result }));
const entries = [
  { type: "message", role: "user", content: "create a long-output agent and exit before reading it" + historyPadding },
  { type: "tool_call", tool_id: "toolu_long_agent_create", tool_name: "AgentCreate", content: "{\"prompt\":\"produce long output\",\"description\":\"long output resume probe\"}" },
  { type: "tool_result", tool_id: "toolu_long_agent_create", tool_name: "AgentCreate", content: JSON.stringify({ agent_name: "general-purpose", model: "model", output_file: outputFile, session_id: "long-output-subagent", status: "running", task_id: 1 }) }
];
fs.writeFileSync(transcript, entries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.AGENT_LONG_OUTPUT_PROVIDER_READY || "";
const requestLog = process.env.AGENT_LONG_OUTPUT_PROVIDER_LOG || "";

function writeLog(entry) {
  if (requestLog) fs.appendFileSync(requestLog, `${JSON.stringify({ time: new Date().toISOString(), ...entry })}\n`);
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

function writeSSE(res, payload) {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

function streamText(res, model, text) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const id = `chatcmpl-agent-long-output-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 32, completion_tokens: text.length, total_tokens: text.length + 32 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-agent-long-output-${Date.now()}`;
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] } }] });
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 48, completion_tokens: 12, total_tokens: 60 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function compactSummary(source) {
  const paths = Array.from(new Set((source.match(/\/tmp\/[^\s"')]+/g) || []).map(path => path.replace(/[.,;:]+$/, ""))));
  const fileLines = paths.length > 0 ? paths.map(path => `- ${path}`) : ["- long-agent.output"];
  return [
    "## Current Goal",
    "Verify long Agent output_file survives compact and AgentGet returns a preview.",
    "## User Preferences / Constraints",
    "Do not modify files.",
    "## Decisions Made",
    "Use deterministic long-output compact-resume gate.",
    "## Files / Code Changed",
    ...fileLines,
    "## Commands / Test Results",
    "Auto compact summary request was triggered.",
    "## Open Tasks",
    "Call AgentGet and use content_preview/output_file instead of full long content.",
    "## Known Issues / Risks",
    "This gate proves request structure, not real-model open-task win rate.",
    "## Important Raw Facts",
    "long-agent.output must remain visible after compact and the long-result tail marker must not enter model request."
  ].join("\n");
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-long-output-resume-stub";
  const text = JSON.stringify(parsed);
  const isSummaryRequest = text.includes("Summarize the conversation history below for continuing an agent session.");
  writeLog({
    is_summary_request: isSummaryRequest,
    has_conversation_summary: text.includes("Conversation summary so far"),
    has_completion_notification: text.includes("completion notification"),
    has_output_file: text.includes("long-agent.output"),
    has_content_preview: text.includes("content_preview"),
    has_tail_marker: text.includes("AGENT_LONG_OUTPUT_TAIL_MARKER"),
    has_persisted_output: text.includes("<persisted-output>"),
  });
  if (isSummaryRequest) {
    streamText(res, model, compactSummary(text));
    return;
  }
  if (text.includes("AGENT_LONG_OUTPUT_RESUME_PROMPT") && text.includes("completion notification") && !text.includes("call_agent_get_long_output_resume")) {
    streamToolCall(res, model, "call_agent_get_long_output_resume", "AgentGet", { task_id: 1 });
    return;
  }
  if (text.includes("content_preview") && text.includes("long-agent.output") && !text.includes("AGENT_LONG_OUTPUT_TAIL_MARKER") && !text.includes("<persisted-output>")) {
    streamText(res, model, "AGENT_LONG_OUTPUT_RESUME_FINAL_OK");
    return;
  }
  streamText(res, model, "AGENT_LONG_OUTPUT_RESUME_UNEXPECTED");
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true }));
    return;
  }
  if (req.method === "POST" && req.url === "/v1/chat/completions") {
    void handleChat(req, res);
    return;
  }
  res.writeHead(404, { "content-type": "application/json" });
  res.end(JSON.stringify({ error: { message: "not found" } }));
});

server.listen(0, "127.0.0.1", () => {
  const address = server.address();
  fs.writeFileSync(readyFile, JSON.stringify({ base_url: `http://127.0.0.1:${address.port}` }));
});
NODE

provider_pid=""
cleanup() {
  if [[ -n "$provider_pid" ]]; then
    kill "$provider_pid" >/dev/null 2>&1 || true
    wait "$provider_pid" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

AGENT_LONG_OUTPUT_PROVIDER_READY="$READY_FILE" \
AGENT_LONG_OUTPUT_PROVIDER_LOG="$PROVIDER_LOG" \
node "$PROVIDER_SCRIPT" >"$WORK_DIR/provider.log" 2>&1 &
provider_pid=$!

for _ in {1..100}; do
  if [[ -s "$READY_FILE" ]]; then
    break
  fi
  sleep 0.05
done
if [[ ! -s "$READY_FILE" ]]; then
  echo "provider did not become ready" >&2
  cat "$WORK_DIR/provider.log" >&2 || true
  exit 1
fi
BASE_URL="$(node -e 'const fs=require("fs"); process.stdout.write(JSON.parse(fs.readFileSync(process.argv[1], "utf8")).base_url)' "$READY_FILE")"

export NIUMA_CONFIG_DIR="$CONFIG_DIR"
# Resolve the canonical provider route from the isolated settings file. Keep
# the legacy environment variables below for compatibility, but do not depend
# on ambient user configuration.
node - "$CONFIG_DIR/settings.json" "$BASE_URL" "$MODEL" "$AUTO_COMPACT" <<'NODE'
const fs = require("fs");
const settingsPath = process.argv[2];
const baseURL = process.argv[3];
const model = process.argv[4];
const autoCompact = process.argv[5] === "true";
const settings = {
  provider: "custom",
  providerProtocol: "openai-chat-completions",
  baseURL: `${baseURL}/v1`,
  apiKey: "agent-long-output-test-key",
  model,
};
if (autoCompact) {
  settings.contextLength = 100;
  settings.autoCompact = {
    enabled: true,
    defaultThresholdRatio: 0.01,
    preserveRecentRounds: 1,
    maxSummaryTokens: 800,
    cooldownTurns: 2,
    modelContext: {
      [model]: 700,
    },
  };
}
fs.writeFileSync(settingsPath, JSON.stringify(settings, null, 2) + "\n", { mode: 0o600 });
NODE
export NIUMA_PROVIDER="custom"
export ANTHROPIC_BASE_URL="$BASE_URL/v1"
export ANTHROPIC_API_KEY="agent-long-output-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"
if [[ "$AUTO_COMPACT" == "true" ]]; then
  export HOME="$HOME_DIR"
  if [[ -n "$REAL_GOMODCACHE" ]]; then
    export GOMODCACHE="$REAL_GOMODCACHE"
  fi
  if [[ -n "$REAL_GOCACHE" ]]; then
    export GOCACHE="$REAL_GOCACHE"
  fi
fi

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$CWD" \
  --resume "$SESSION_ID" \
  --max-turns 2 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentGet" \
  -p "AGENT_LONG_OUTPUT_RESUME_PROMPT: inspect resumed background agent status. If there is a completed task notification, call AgentGet before answering." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  exit 1
fi

verify_dump >&2
verify_markers

echo "ok=true"
echo "auto_compact=$AUTO_COMPACT"
echo "dump=$DUMP_PATH"
echo "transcript=$transcript_path"
echo "output_file=$output_file"
echo "provider_log=$PROVIDER_LOG"
