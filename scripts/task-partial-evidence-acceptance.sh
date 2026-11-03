#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-task-partial-evidence-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-task-partial-evidence-${TIMESTAMP}.jsonl"
MODEL="task-partial-evidence-stub"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="1024"
VERIFY_ONLY="false"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/task-partial-evidence-acceptance.sh [flags]

Runs a deterministic real CLI prompt-dump acceptance for synchronous Task
partial/error evidence recovery. The parent must call Task, the sub-agent must
emit partial text and then fail the stream, and the next parent request must
contain failed Task capability_loop evidence in Recent agent evidence context.

Flags:
  --work-dir <dir>        Temp work dir for isolated config/provider logs.
  --dump <path>           Prompt dump JSONL path.
  --cwd <path>            Accepted for matrix compatibility; this scenario uses an isolated temp cwd.
  --model <name>          Model name sent to the stub provider.
  --prompt-profile <name> NIUMA_PROMPT_PROFILE value.
  --max-tokens <n>        Max tokens passed to niuma. Default: 1024.
  --verify-only           Verify an existing --dump without running the stub provider.
  --force                 Remove existing work dir/dump before running.
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
    --cwd)
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
    --max-tokens)
      MAX_TOKENS="${2:?missing value for --max-tokens}"
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
    --require-subagent-record=true \
    --require-tool-result "Task" \
    --require-tool-no-persisted-output "Task" \
    --require-tool-max-bytes "Task=12000" \
    --require-tool-use-input-text "Task=PARTIAL_TASK_SUBAGENT_PROMPT" \
    --require-request-text "PARTIAL_TASK_PARENT_PROMPT,PARTIAL_TASK_SUBAGENT_PROMPT,PARTIAL_TASK_STREAM_EVIDENCE,Sub-agent stopped before a clean final answer,<capability_loop>,\"status\": \"failed\",transcript_path,output_file,## Recent agent evidence decision context,Task result failed: Partial stream evidence probe,capability_loop: evidence: Partial sub-agent content before failed:,unknowns: Whether the partial sub-agent result covered all requested evidence before failure.,risks: Partial sub-agent result may be incomplete, stale, or missing later evidence due to the failure.,next_action: Parent agent should inspect the failure, preserve any partial evidence,artifacts: session_id:,transcript_path:,output_file:,## Agent capability follow-up gate,pending_follow_up: Task result failed: Partial stream evidence probe,must_handle_next_action: Parent agent should inspect the failure,verification_required: Inspect partial answer,risk_to_account_for: Partial sub-agent result may be incomplete" \
    --forbid-request-text "Task result completed: Partial stream evidence probe,\"name\":\"Write\",\"name\":\"Edit\",\"name\":\"MultiEdit\",\"name\":\"Bash\",\"name\":\"AgentCreate\"" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
const subagentRecords = records.filter(record => record.scope === "subagent");
if (subagentRecords.length < 1) {
  console.error("expected at least one subagent prompt dump record");
  process.exit(1);
}
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const finalMain = mainRecords[mainRecords.length - 1];
const finalText = JSON.stringify(finalMain?.request || {});
for (const marker of [
  "## Recent agent evidence decision context",
  "Task result failed: Partial stream evidence probe",
  "PARTIAL_TASK_STREAM_EVIDENCE",
  "capability_loop: evidence: Partial sub-agent content before failed:",
  "unknowns: Whether the partial sub-agent result covered all requested evidence before failure.",
  "verification: Inspect partial answer, completed tool calls, transcript_path, or rerun a narrower verification before relying on findings.",
  "risks: Partial sub-agent result may be incomplete, stale, or missing later evidence due to the failure.",
  "next_action: Parent agent should inspect the failure, preserve any partial evidence",
  "artifacts: session_id:",
  "transcript_path:",
  "output_file:",
  "## Agent capability follow-up gate",
  "pending_follow_up: Task result failed: Partial stream evidence probe",
  "must_handle_next_action: Parent agent should inspect the failure",
  "verification_required: Inspect partial answer",
  "risk_to_account_for: Partial sub-agent result may be incomplete",
]) {
  if (!finalText.includes(marker)) {
    console.error(`final parent request missing partial Task recovery context: ${marker}`);
    process.exit(1);
  }
}
if (finalText.includes("Task result completed: Partial stream evidence probe")) {
  console.error("partial Task evidence was mislabeled completed");
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

if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir exists, pass --force: $WORK_DIR" >&2
  exit 2
fi
if [[ -e "$DUMP_PATH" && "$FORCE" != "true" ]]; then
  echo "dump exists, pass --force: $DUMP_PATH" >&2
  exit 2
fi

rm -rf "$WORK_DIR"
rm -f "$DUMP_PATH"
mkdir -p "$WORK_DIR"

READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
CONFIG_DIR="$WORK_DIR/config"
FIXTURE_DIR="$WORK_DIR/workspace"

mkdir -p "$FIXTURE_DIR" "$CONFIG_DIR"
cat >"$FIXTURE_DIR/README.md" <<'EOF_README'
# Partial Task Evidence Fixture

This workspace is intentionally tiny. The acceptance focuses on a Task
sub-agent that emits partial evidence and then fails the stream.
EOF_README

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.TASK_PARTIAL_PROVIDER_READY || "";
const requestLog = process.env.TASK_PARTIAL_PROVIDER_LOG || "";
let subagentCalls = 0;

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
  const id = `chatcmpl-task-partial-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 80, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 80 + Math.max(1, Math.ceil(text.length / 4)) } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-task-partial-${Date.now()}`;
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] } }] });
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 120, completion_tokens: 24, total_tokens: 144 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamPartialThenConnectionError(res, model, text) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const id = `chatcmpl-task-partial-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  res.write('data: {"id":"chatcmpl_task_partial_broken","object":"chat.completion.chunk"\n\n');
  res.end();
}

function userText(parsed) {
  const parts = [];
  for (const message of parsed.messages || []) {
    if (message.role !== "user" && message.role !== "tool") continue;
    const content = message.content;
    if (typeof content === "string") {
      parts.push(content);
      continue;
    }
    if (!Array.isArray(content)) continue;
    for (const block of content) {
      if (typeof block === "string") parts.push(block);
      else if (block && typeof block.text === "string") parts.push(block.text);
      else if (block && typeof block.content === "string") parts.push(block.content);
    }
  }
  return parts.join("\n");
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "task-partial-evidence-stub";
  const all = JSON.stringify(parsed);
  const users = userText(parsed);
  const isSubagent = users.includes("PARTIAL_TASK_SUBAGENT_PROMPT");
  if (isSubagent) subagentCalls += 1;
  writeLog({
    is_subagent: isSubagent,
    subagent_calls: subagentCalls,
    parent_saw_task_result: !isSubagent && users.includes("PARTIAL_TASK_STREAM_EVIDENCE") && users.includes("<capability_loop>"),
    parent_saw_failed_recent_context: !isSubagent
      && all.includes("## Recent agent evidence decision context")
      && all.includes("Task result failed: Partial stream evidence probe")
      && all.includes("PARTIAL_TASK_STREAM_EVIDENCE")
      && all.includes("Partial sub-agent result may be incomplete")
      && all.includes("artifacts: session_id:")
      && all.includes("transcript_path:")
      && all.includes("output_file:")
      && all.includes("## Agent capability follow-up gate")
      && all.includes("must_handle_next_action: Parent agent should inspect the failure"),
    parent_mislabeled_completed: !isSubagent && all.includes("Task result completed: Partial stream evidence probe"),
  });

  if (isSubagent) {
    streamPartialThenConnectionError(res, model, "PARTIAL_TASK_STREAM_EVIDENCE: child found evidence before stream failure.");
    return;
  }

  if (users.includes("PARTIAL_TASK_STREAM_EVIDENCE") && users.includes("Task result failed: Partial stream evidence probe")) {
    streamText(res, model, "PARTIAL_TASK_PARENT_USED_FAILED_EVIDENCE PARTIAL_TASK_PARENT_USED_RECOVERY_RISK PARTIAL_TASK_PARENT_NEXT_ACTION");
    return;
  }

  streamToolCall(res, model, "call_partial_task", "Task", {
    description: "Partial stream evidence probe",
    prompt: "PARTIAL_TASK_SUBAGENT_PROMPT: Report PARTIAL_TASK_STREAM_EVIDENCE if any evidence is found. This stub will fail the stream after partial text.",
  });
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

TASK_PARTIAL_PROVIDER_READY="$READY_FILE" \
TASK_PARTIAL_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="task-partial-evidence-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 3 \
  --max-tokens "$MAX_TOKENS" \
  --model "$MODEL" \
  --tools "Task" \
  -p "PARTIAL_TASK_PARENT_PROMPT: delegate once to a Task sub-agent, then continue only after seeing failed partial evidence, unknowns, verification, risks, and next_action. Do not modify files." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

verify_dump >&2
verify_markers

node - "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const events = fs.readFileSync(process.argv[2], "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!events.some(event => event.is_subagent)) {
  console.error("provider never saw sub-agent request");
  process.exit(1);
}
if (!events.some(event => event.parent_saw_task_result)) {
  console.error("provider never saw parent request with partial Task result");
  process.exit(1);
}
if (!events.some(event => event.parent_saw_failed_recent_context)) {
  console.error("provider never saw parent request with failed Task recent evidence context");
  process.exit(1);
}
if (events.some(event => event.parent_mislabeled_completed)) {
  console.error("provider saw partial Task evidence mislabeled as completed");
  process.exit(1);
}
NODE

for marker in \
  PARTIAL_TASK_PARENT_USED_FAILED_EVIDENCE \
  PARTIAL_TASK_PARENT_USED_RECOVERY_RISK \
  PARTIAL_TASK_PARENT_NEXT_ACTION
do
  if ! grep -q "$marker" "$CLI_LOG"; then
    echo "CLI log missing parent final marker $marker" >&2
    exit 1
  fi
done

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "workspace=$FIXTURE_DIR"
echo "provider_log=$PROVIDER_LOG"
