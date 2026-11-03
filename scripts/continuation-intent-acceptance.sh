#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"
WORK_DIR="/tmp/niuma-continuation-intent-${TIMESTAMP}"
MODEL="continuation-intent-stub"
FORCE=0

usage() {
  cat <<'EOF'
Usage:
  scripts/continuation-intent-acceptance.sh [flags]

Runs a deterministic real CLI acceptance against a local OpenAI-compatible
stub provider. The scenario creates a real transcript where the assistant
proposes a pending action, resumes that transcript, sends "好", and verifies
that niuma continues the pending action instead of answering with an idle
acknowledgement.

Flags:
  --work-dir <dir>  Temp work dir for fixture/config/provider logs.
  --model <name>    Model name sent to the stub provider.
  --force           Remove existing work dir before running.
  -h, --help        Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir)
      WORK_DIR="${2:?missing value for --work-dir}"
      shift 2
      ;;
    --model)
      MODEL="${2:?missing value for --model}"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -e "$WORK_DIR" && "$FORCE" != "1" ]]; then
  echo "work dir exists, pass --force: $WORK_DIR" >&2
  exit 1
fi

rm -rf "$WORK_DIR"
mkdir -p "$WORK_DIR"

READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CONFIG_DIR="$WORK_DIR/config"
FIXTURE_DIR="$WORK_DIR/workspace"
FIRST_LOG="$WORK_DIR/first.log"
SECOND_LOG="$WORK_DIR/second.log"

mkdir -p "$CONFIG_DIR" "$FIXTURE_DIR"
cat >"$FIXTURE_DIR/README.md" <<'EOF_README'
# Continuation Intent Fixture

CONTINUATION_ACCEPTANCE_MARKER: this fixture proves the confirmed pending
action used a real tool after the user said "好".
EOF_README

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.CONTINUATION_PROVIDER_READY || "";
const requestLog = process.env.CONTINUATION_PROVIDER_LOG || "";

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
  const id = `chatcmpl-continuation-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 64, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 64 + Math.max(1, Math.ceil(text.length / 4)) } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-continuation-${Date.now()}`;
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
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 96, completion_tokens: 16, total_tokens: 112 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function joinedMessages(parsed) {
  return JSON.stringify(parsed.messages || []);
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "continuation-intent-stub";
  const all = joinedMessages(parsed);
  const hasContinuationReminder = all.includes("Continuation intent detected");
  const hasContinuationGate = all.includes("confirmed a pending action");
  const hasToolResult = all.includes("CONTINUATION_ACCEPTANCE_MARKER");
  writeLog({ has_continuation_reminder: hasContinuationReminder, has_continuation_gate: hasContinuationGate, has_tool_result: hasToolResult });

  if (hasToolResult) {
    streamText(res, model, "CONTINUATION_ACCEPTANCE_DONE: continued after user confirmed the pending action.");
    return;
  }
  if (hasContinuationGate) {
    streamToolCall(res, model, "call_continuation_read", "Read", { file_path: "README.md" });
    return;
  }
  if (hasContinuationReminder) {
    streamText(res, model, "收到。有什么需要做的随时说。");
    return;
  }
  streamText(res, model, [
    "如果继续推进，优先级建议：",
    "1. 补强 3-ai-agents — 这是最大缺口",
    "2. 扩充 prompts",
    "",
    "你想聊哪个方向？",
  ].join("\n"));
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true }));
    return;
  }
  if (req.method === "POST" && req.url === "/v1/chat/completions") {
    void handleChat(req, res).catch(err => {
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: { message: String(err && err.stack || err) } }));
    });
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

CONTINUATION_PROVIDER_READY="$READY_FILE" \
CONTINUATION_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="continuation-intent-test-key"

go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 1 \
  --max-tokens 1024 \
  --model "$MODEL" \
  -p "先分析下一步优先级，暂时不要执行。" >"$FIRST_LOG" 2>&1

SESSION_PATH="$(find "$CONFIG_DIR/projects" -name '*.jsonl' -type f | head -n 1)"
if [[ -z "$SESSION_PATH" || ! -s "$SESSION_PATH" ]]; then
  echo "first run transcript missing under $CONFIG_DIR/projects" >&2
  cat "$FIRST_LOG" >&2 || true
  exit 1
fi
SESSION_ID="$(basename "$SESSION_PATH" .jsonl)"

go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --resume "$SESSION_ID" \
  --max-turns 4 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "Read" \
  -p "好" >"$SECOND_LOG" 2>&1

node - "$SESSION_PATH" "$SECOND_LOG" "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const transcriptPath = process.argv[2];
const secondLogPath = process.argv[3];
const providerLogPath = process.argv[4];
const transcript = fs.readFileSync(transcriptPath, "utf8");
const secondLog = fs.readFileSync(secondLogPath, "utf8");
const providerLog = fs.readFileSync(providerLogPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));

for (const marker of [
  '"type":"continuation_intent"',
  '补强 3-ai-agents',
  '"type":"completion_gate"',
  'confirmed_pending_action_requires_progress',
  '"type":"tool_call"',
  '"tool_name":"Read"',
  'CONTINUATION_ACCEPTANCE_MARKER',
]) {
  if (!transcript.includes(marker)) {
    console.error(`transcript missing ${marker}`);
    process.exit(1);
  }
}
if (secondLog.includes("有什么需要做的随时说")) {
  console.error("idle acknowledgement leaked to CLI output");
  process.exit(1);
}
if (!secondLog.includes("CONTINUATION_ACCEPTANCE_DONE")) {
  console.error("CLI output missing accepted continuation final marker");
  process.exit(1);
}
if (!providerLog.some(record => record.has_continuation_reminder) || !providerLog.some(record => record.has_continuation_gate) || !providerLog.some(record => record.has_tool_result)) {
  console.error("provider did not observe continuation reminder, gate, and tool result");
  process.exit(1);
}
NODE

echo "ok=true"
echo "session_id=$SESSION_ID"
echo "transcript=$SESSION_PATH"
echo "work_dir=$WORK_DIR"
echo "provider_log=$PROVIDER_LOG"
