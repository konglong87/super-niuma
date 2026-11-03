#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-task-capability-resolution-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-task-capability-resolution-${TIMESTAMP}.jsonl"
MODEL="task-capability-resolution-stub"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="1024"
VERIFY_ONLY="false"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/task-capability-loop-resolution-acceptance.sh [flags]

Runs a deterministic two-Task parent loop:
1. Task #1 returns a capability_loop next_action that creates a parent follow-up gate.
2. The parent launches Task #2, whose capability_loop resolves and supersedes Task #1.
3. The final parent request must retain resolution evidence while no longer carrying the old pending gate.

Flags:
  --work-dir <dir>        Temp work dir for isolated config/provider logs.
  --dump <path>           Prompt dump JSONL path.
  --cwd <path>            Accepted for matrix compatibility; this scenario uses an isolated fixture.
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
    --min-turns 3 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-subagent-record=true \
    --require-tool-result "Task" \
    --require-tool-no-persisted-output "Task" \
    --require-tool-max-bytes "Task=16000" \
    --require-tool-use-input-text "Task=TASK_RESOLUTION_PENDING_PROMPT" \
    --require-request-text "TASK_RESOLUTION_PENDING_FINAL,TASK_RESOLUTION_PENDING_EVIDENCE,TASK_RESOLUTION_PENDING_NEXT_ACTION,## Agent capability follow-up gate,follow_up_id: tool:call_task_resolution_pending,TASK_RESOLUTION_RESOLVER_FINAL,TASK_RESOLUTION_DONE_EVIDENCE,resolved_follow_up: TASK_RESOLUTION_DONE,supersedes_evidence_id: tool:call_task_resolution_pending,task:1" \
    --forbid-request-text "\"name\":\"Write\",\"name\":\"Edit\",\"name\":\"MultiEdit\",\"name\":\"Bash\"" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
const subagentRecords = records.filter(record => record.scope === "subagent");
if (subagentRecords.length < 2) {
  console.error("expected at least two subagent prompt dump records");
  process.exit(1);
}
const allText = records.map(record => JSON.stringify(record.request || {})).join("\n");
for (const marker of [
  "TASK_RESOLUTION_PENDING_FINAL",
  "TASK_RESOLUTION_PENDING_EVIDENCE",
  "TASK_RESOLUTION_PENDING_NEXT_ACTION",
  "TASK_RESOLUTION_RESOLVER_FINAL",
  "TASK_RESOLUTION_DONE_EVIDENCE",
  "TASK_RESOLUTION_DONE",
  "supersedes_evidence_id: tool:call_task_resolution_pending",
  "task:1",
]) {
  if (!allText.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const gateRecord = mainRecords.find(record => {
  const text = JSON.stringify(record.request || {});
  return text.includes("## Agent capability follow-up gate")
    && text.includes("pending_follow_up: Task result completed: Produce pending capability follow-up")
    && text.includes("follow_up_id: tool:call_task_resolution_pending")
    && text.includes("must_handle_next_action: TASK_RESOLUTION_PENDING_NEXT_ACTION");
});
if (!gateRecord) {
  console.error("parent request never carried the first Task pending follow-up gate");
  process.exit(1);
}
const finalMain = mainRecords[mainRecords.length - 1];
const finalText = JSON.stringify(finalMain?.request || {});
for (const marker of [
  "## Recent agent evidence decision context",
  "Task result completed: Resolve pending capability follow-up",
  "capability_loop: evidence: TASK_RESOLUTION_DONE_EVIDENCE",
  "verification: TASK_RESOLUTION_DONE_VERIFICATION",
  "resolved_follow_up: TASK_RESOLUTION_DONE",
  "supersedes_evidence_id: tool:call_task_resolution_pending",
  "task:1",
]) {
  if (!finalText.includes(marker)) {
    console.error(`final parent request missing resolution context: ${marker}`);
    process.exit(1);
  }
}
for (const marker of [
  "pending_follow_up: Task result completed: Produce pending capability follow-up",
  "must_handle_next_action: TASK_RESOLUTION_PENDING_NEXT_ACTION",
  "verification_required: TASK_RESOLUTION_PENDING_VERIFICATION",
  "risk_to_account_for: TASK_RESOLUTION_PENDING_RISK",
]) {
  if (finalText.includes(marker)) {
    console.error(`final parent request resurrected superseded pending gate: ${marker}`);
    process.exit(1);
  }
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
# Task Capability Resolution Fixture

The parent should run a pending Task, then run a second Task that resolves the
first Task's follow-up through resolved_follow_up and supersedes_evidence_id.
EOF_README

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.TASK_RESOLUTION_PROVIDER_READY || "";
const requestLog = process.env.TASK_RESOLUTION_PROVIDER_LOG || "";

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
  const id = `chatcmpl-task-resolution-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 96, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 96 + Math.max(1, Math.ceil(text.length / 4)) } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-task-resolution-${Date.now()}`;
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] } }] });
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 128, completion_tokens: 24, total_tokens: 152 } });
  res.write("data: [DONE]\n\n");
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
  const model = parsed.model || "task-capability-resolution-stub";
  const all = JSON.stringify(parsed);
  const users = userText(parsed);
  const isPendingSubagent = users.includes("TASK_RESOLUTION_PENDING_PROMPT");
  const isResolverSubagent = users.includes("TASK_RESOLUTION_RESOLVER_PROMPT");
  const parentHasPendingGate = !isPendingSubagent && !isResolverSubagent
    && all.includes("## Agent capability follow-up gate")
    && all.includes("pending_follow_up: Task result completed: Produce pending capability follow-up")
    && all.includes("follow_up_id: tool:call_task_resolution_pending")
    && all.includes("must_handle_next_action: TASK_RESOLUTION_PENDING_NEXT_ACTION");
  const parentHasResolution = !isPendingSubagent && !isResolverSubagent
    && all.includes("TASK_RESOLUTION_RESOLVER_FINAL")
    && all.includes("resolved_follow_up: TASK_RESOLUTION_DONE")
    && all.includes("supersedes_evidence_id: tool:call_task_resolution_pending");
  writeLog({
    is_pending_subagent: isPendingSubagent,
    is_resolver_subagent: isResolverSubagent,
    parent_has_pending_gate: parentHasPendingGate,
    parent_has_resolution: parentHasResolution,
    parent_final_resurrects_pending: parentHasResolution && all.includes("pending_follow_up: Task result completed: Produce pending capability follow-up"),
  });

  if (isPendingSubagent) {
    streamText(res, model, [
      "TASK_RESOLUTION_PENDING_FINAL",
      "Summary:",
      "- TASK_RESOLUTION_PENDING_SUMMARY: identified a follow-up that must be resolved before final synthesis.",
      "Evidence:",
      "- TASK_RESOLUTION_PENDING_EVIDENCE: first Task produced concrete evidence but needs one more verification pass.",
      "Assumptions:",
      "- TASK_RESOLUTION_PENDING_ASSUMPTION: parent can launch a second Task to resolve the follow-up.",
      "Unknowns:",
      "- TASK_RESOLUTION_PENDING_UNKNOWN: second-pass verification has not run yet.",
      "Verification:",
      "- TASK_RESOLUTION_PENDING_VERIFICATION: first-pass deterministic evidence was captured in this Task result.",
      "Risks:",
      "- TASK_RESOLUTION_PENDING_RISK: final answer before the second Task would be premature.",
      "Next action:",
      "- TASK_RESOLUTION_PENDING_NEXT_ACTION: parent must run the resolver Task before final answer."
    ].join("\n"));
    return;
  }

  if (isResolverSubagent) {
    streamText(res, model, [
      "TASK_RESOLUTION_RESOLVER_FINAL",
      "Summary:",
      "- TASK_RESOLUTION_RESOLVER_SUMMARY: resolved the prior Task follow-up.",
      "Evidence:",
      "- TASK_RESOLUTION_DONE_EVIDENCE: resolver Task checked the pending next action and found it satisfied.",
      "Verification:",
      "- TASK_RESOLUTION_DONE_VERIFICATION: resolver Task explicitly verified the old pending next action.",
      "Resolved follow-up:",
      "- TASK_RESOLUTION_DONE: handled TASK_RESOLUTION_PENDING_NEXT_ACTION.",
      "Supersedes evidence id:",
      "- tool:call_task_resolution_pending",
      "- task:1"
    ].join("\n"));
    return;
  }

  if (users.includes("TASK_RESOLUTION_RESOLVER_FINAL") && users.includes("TASK_RESOLUTION_DONE_EVIDENCE")) {
    streamText(res, model, "TASK_RESOLUTION_PARENT_FINAL_USED_RESOLUTION TASK_RESOLUTION_PARENT_FINAL_NO_OLD_GATE");
    return;
  }

  if (users.includes("TASK_RESOLUTION_PENDING_FINAL") && users.includes("TASK_RESOLUTION_PENDING_NEXT_ACTION")) {
    streamToolCall(res, model, "call_task_resolution_resolver", "Task", {
      description: "Resolve pending capability follow-up",
      prompt: "TASK_RESOLUTION_RESOLVER_PROMPT: Resolve follow_up_id tool:call_task_resolution_pending. Return Summary, Evidence, Verification, Resolved follow-up, and Supersedes evidence id. Include TASK_RESOLUTION_DONE markers and supersedes_evidence_id tool:call_task_resolution_pending."
    });
    return;
  }

  streamToolCall(res, model, "call_task_resolution_pending", "Task", {
    description: "Produce pending capability follow-up",
    prompt: "TASK_RESOLUTION_PENDING_PROMPT: Return Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action. Include the exact TASK_RESOLUTION_PENDING_* markers."
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

TASK_RESOLUTION_PROVIDER_READY="$READY_FILE" \
TASK_RESOLUTION_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="task-capability-resolution-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 4 \
  --max-tokens "$MAX_TOKENS" \
  --model "$MODEL" \
  --tools "Task" \
  -p "TASK_RESOLUTION_PARENT_PROMPT: run a first Task that reports a capability follow-up, then run a second Task to resolve that follow-up before final answer. Do not modify files." >"$CLI_LOG" 2>&1

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
if (!events.some(event => event.parent_has_pending_gate)) {
  console.error("provider never saw parent request with first Task pending gate");
  process.exit(1);
}
if (!events.some(event => event.parent_has_resolution && !event.parent_final_resurrects_pending)) {
  console.error("provider never saw parent resolution request without resurrected pending gate");
  process.exit(1);
}
NODE

for marker in \
  TASK_RESOLUTION_PARENT_FINAL_USED_RESOLUTION \
  TASK_RESOLUTION_PARENT_FINAL_NO_OLD_GATE
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
