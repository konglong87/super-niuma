#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

CWD="$ROOT_DIR"
WORK_DIR="/tmp/niuma-agent-empty-output-resume-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-empty-output-resume-${TIMESTAMP}.jsonl"
MODEL="agent-empty-output-resume-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-empty-output-resume-acceptance.sh [flags]

Builds a resume transcript whose AgentCreate result points at an empty output_file.
The resumed request must surface the task as failed/interrupted, not running.

Flags:
  --cwd <path>             Workspace cwd. Default: repo root.
  --work-dir <path>        Temp work dir for isolated config/provider logs.
  --dump <path>            Prompt dump path.
  --model <name>           Model name sent to niuma.
  --prompt-profile <name>  Prompt profile. Default: claude-compatible.
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

if [[ "$FORCE" == "true" ]]; then
  rm -rf "$WORK_DIR" "$DUMP_PATH"
fi
if [[ -e "$WORK_DIR" || -e "$DUMP_PATH" ]]; then
  echo "work or dump path already exists; use --force or choose another path" >&2
  exit 2
fi

mkdir -p "$WORK_DIR"
READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
SESSION_ID="12121212-1212-4212-8212-121212121212"
CONFIG_DIR="$WORK_DIR/config"

abs_cwd="$(cd "$CWD" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
transcript_path="$transcript_dir/$SESSION_ID.jsonl"
output_file="$transcript_dir/empty-agent.output"
mkdir -p "$transcript_dir"
: > "$output_file"

node - "$transcript_path" "$output_file" <<'NODE'
const fs = require("fs");
const transcript = process.argv[2];
const outputFile = process.argv[3];
const entries = [
  { type: "message", role: "user", content: "create an agent and exit before it writes output" },
  { type: "tool_call", tool_id: "toolu_empty_agent_create", tool_name: "AgentCreate", content: "{\"prompt\":\"write later\",\"description\":\"empty output resume probe\"}" },
  { type: "tool_result", tool_id: "toolu_empty_agent_create", tool_name: "AgentCreate", content: JSON.stringify({ agent_name: "general-purpose", model: "model", output_file: outputFile, session_id: "empty-output-subagent", status: "running", task_id: 1 }) }
];
fs.writeFileSync(transcript, entries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.AGENT_EMPTY_OUTPUT_PROVIDER_READY || "";
const requestLog = process.env.AGENT_EMPTY_OUTPUT_PROVIDER_LOG || "";

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
  const id = `chatcmpl-agent-empty-output-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 32, completion_tokens: text.length, total_tokens: text.length + 32 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-agent-empty-output-${Date.now()}`;
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] } }] });
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 48, completion_tokens: 12, total_tokens: 60 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-empty-output-resume-stub";
  const text = JSON.stringify(parsed);
  writeLog({
    has_failure_notification: text.includes("failure notification"),
    has_running_status: text.includes("#1 general-purpose running"),
    has_interrupted_result: text.includes("output_file is empty") || text.includes("treated as interrupted"),
    has_agentget_result: text.includes("call_agent_get_empty_output_resume"),
  });
  if (text.includes("EMPTY_OUTPUT_RESUME_PROMPT") && text.includes("failure notification") && !text.includes("call_agent_get_empty_output_resume")) {
    streamToolCall(res, model, "call_agent_get_empty_output_resume", "AgentGet", { task_id: 1 });
    return;
  }
  if (text.includes("output_file is empty") || text.includes("treated as interrupted")) {
    streamText(res, model, "EMPTY_OUTPUT_RESUME_FINAL_OK");
    return;
  }
  streamText(res, model, "EMPTY_OUTPUT_RESUME_IDLE");
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

AGENT_EMPTY_OUTPUT_PROVIDER_READY="$READY_FILE" \
AGENT_EMPTY_OUTPUT_PROVIDER_LOG="$PROVIDER_LOG" \
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
export NIUMA_PROVIDER="custom"
export ANTHROPIC_BASE_URL="$BASE_URL/v1"
export ANTHROPIC_API_KEY="agent-empty-output-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$CWD" \
  --resume "$SESSION_ID" \
  --max-turns 2 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentGet" \
  -p "EMPTY_OUTPUT_RESUME_PROMPT: inspect resumed background agent status. If there is a failed task notification, call AgentGet before answering." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  exit 1
fi

go run ./scripts/verify-code-mode-prompt-dump.go \
  --min-turns 2 \
  --require-final-tools-disabled=false \
  --require-final-turn-budget=false \
  --require-subagent-turn-budget-tools-disabled=false \
  --require-tool-result "AgentGet" \
  --require-request-text "## Background agent tasks,failed,failure notification: call AgentGet before explaining failure details,output_file is empty,treated as interrupted" \
  --forbid-request-text "#1 general-purpose running" \
  "$DUMP_PATH" >&2

node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dump = process.argv[2];
const records = fs.readFileSync(dump, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
let foundFailedAgentGet = false;
for (const record of records) {
  for (const message of record.request?.messages || []) {
    for (const block of message.content || []) {
      if (block.type !== "tool_result" || block.tool_use_id !== "call_agent_get_empty_output_resume") continue;
      const content = typeof block.content === "string" ? JSON.parse(block.content) : block.content;
      if (content?.task?.status === "failed" && content?.result?.status === "failed") {
        foundFailedAgentGet = true;
      }
    }
  }
}
if (!foundFailedAgentGet) {
  console.error("AgentGet result did not include failed task/result status");
  process.exit(1);
}
if (JSON.stringify(records.map(record => record.request || {})).includes("#1 general-purpose running")) {
  console.error("resumed empty output task was still shown as running");
  process.exit(1);
}
NODE

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "transcript=$transcript_path"
echo "output_file=$output_file"
echo "provider_log=$PROVIDER_LOG"
