#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

CWD="$ROOT_DIR"
WORK_DIR="/tmp/niuma-agent-detached-running-resume-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-detached-running-resume-${TIMESTAMP}.jsonl"
MODEL="agent-detached-running-resume-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"
VERIFY_ONLY="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-detached-running-resume-acceptance.sh [flags]

Builds a resume transcript whose AgentCreate result is still running but has no
runner attached to the resumed CLI process. The resumed request and AgentGet
tool_result must surface process_attachment so the model does not wait blindly.

Flags:
  --cwd <path>             Workspace cwd. Default: repo root.
  --work-dir <path>        Temp work dir for isolated config/provider logs.
  --dump <path>            Prompt dump path.
  --model <name>           Model name sent to niuma.
  --prompt-profile <name>  Prompt profile. Default: claude-compatible.
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
  go run ./scripts/verify-code-mode-prompt-dump.go \
    --min-turns 2 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-tool-result "AgentGet" \
    --require-request-text "## Background agent tasks,#1 general-purpose running,process_attachment: not_attached_to_current_process,the task may be running in another process or interrupted,do not wait indefinitely" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dump = process.argv[2];
const records = fs.readFileSync(dump, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
let foundDetachedAgentGet = false;
for (const record of records) {
  for (const message of record.request?.messages || []) {
    for (const block of message.content || []) {
      if (block.type !== "tool_result" || block.tool_use_id !== "call_agent_get_detached_running_resume") continue;
      const raw = typeof block.content === "string" ? block.content : JSON.stringify(block.content);
      const content = typeof block.content === "string" ? JSON.parse(block.content) : block.content;
      if (
        content?.task?.status === "running" &&
        content?.task?.process_attachment === "not_attached_to_current_process" &&
        raw.includes("process_attachment_note")
      ) {
        foundDetachedAgentGet = true;
      }
    }
  }
}
if (!foundDetachedAgentGet) {
  console.error("AgentGet result did not include detached running process_attachment");
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
READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
SESSION_ID="45454545-4545-4454-8454-454545454545"
CONFIG_DIR="$WORK_DIR/config"

abs_cwd="$(cd "$CWD" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
transcript_path="$transcript_dir/$SESSION_ID.jsonl"
mkdir -p "$transcript_dir"

node - "$transcript_path" <<'NODE'
const fs = require("fs");
const transcript = process.argv[2];
const entries = [
  { type: "message", role: "user", content: "create a background agent and resume from another process" },
  { type: "tool_call", tool_id: "toolu_detached_agent_create", tool_name: "AgentCreate", content: "{\"prompt\":\"keep working\",\"description\":\"detached running resume probe\"}" },
  { type: "tool_result", tool_id: "toolu_detached_agent_create", tool_name: "AgentCreate", content: JSON.stringify({ agent_name: "general-purpose", model: "model", session_id: "detached-running-subagent", status: "running", task_id: 1 }) }
];
fs.writeFileSync(transcript, entries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.AGENT_DETACHED_RUNNING_PROVIDER_READY || "";
const requestLog = process.env.AGENT_DETACHED_RUNNING_PROVIDER_LOG || "";

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
  const id = `chatcmpl-agent-detached-running-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 32, completion_tokens: text.length, total_tokens: text.length + 32 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-agent-detached-running-${Date.now()}`;
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] } }] });
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 48, completion_tokens: 12, total_tokens: 60 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-detached-running-resume-stub";
  const text = JSON.stringify(parsed);
  writeLog({
    has_background_tasks: text.includes("## Background agent tasks"),
    has_running_status: text.includes("#1 general-purpose running"),
    has_detached_status: text.includes("process_attachment: not_attached_to_current_process"),
    has_agentget_result: text.includes("call_agent_get_detached_running_resume"),
  });
  if (text.includes("DETACHED_RUNNING_RESUME_PROMPT") && text.includes("process_attachment: not_attached_to_current_process") && !text.includes("call_agent_get_detached_running_resume")) {
    streamToolCall(res, model, "call_agent_get_detached_running_resume", "AgentGet", { task_id: 1 });
    return;
  }
  if (text.includes("\"process_attachment\":\"not_attached_to_current_process\"") || text.includes("\"process_attachment\": \"not_attached_to_current_process\"")) {
    streamText(res, model, "DETACHED_RUNNING_RESUME_FINAL_OK");
    return;
  }
  streamText(res, model, "DETACHED_RUNNING_RESUME_UNEXPECTED");
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

AGENT_DETACHED_RUNNING_PROVIDER_READY="$READY_FILE" \
AGENT_DETACHED_RUNNING_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="agent-detached-running-test-key"
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
  -p "DETACHED_RUNNING_RESUME_PROMPT: inspect resumed background agent status. If a running task is not attached to this process, call AgentGet before answering." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  exit 1
fi

verify_dump >&2
verify_markers

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "transcript=$transcript_path"
echo "provider_log=$PROVIDER_LOG"
