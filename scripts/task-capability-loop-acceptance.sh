#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/niuma-task-capability-loop-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-task-capability-loop-${TIMESTAMP}.jsonl"
MODEL="task-capability-loop-stub"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="1024"
VERIFY_ONLY="false"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/task-capability-loop-acceptance.sh [flags]

Runs a deterministic real Task sub-agent acceptance. The parent must delegate
with the Task tool, the sub-agent must use real Grep and Read tool calls against
an isolated fixture, and the parent final answer must use the returned
evidence/unknowns/verification/risk/next-action result.

Flags:
  --work-dir <dir>        Temp work dir for isolated fixture/config/provider logs.
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
    --min-turns 2 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-subagent-record=true \
    --require-tool-result "Task,Grep,Read" \
    --require-tool-no-persisted-output "Task,Grep,Read" \
    --require-tool-max-bytes "Task=12000,Grep=8000,Read=8000" \
    --require-tool-use-input-text "Task=TASK_CAPABILITY_SUBAGENT_PROMPT,Grep=TASK_CAPABILITY_FIXTURE_EVIDENCE,Read=internal/capability_fixture.go" \
    --require-request-text "TASK_CAPABILITY_FIXTURE_EVIDENCE,TASK_CAPABILITY_SUBAGENT_FINAL,TASK_CAPABILITY_SUBAGENT_EVIDENCE,TASK_CAPABILITY_SUBAGENT_UNKNOWN,TASK_CAPABILITY_SUBAGENT_VERIFICATION,TASK_CAPABILITY_SUBAGENT_RISK,TASK_CAPABILITY_SUBAGENT_NEXT_ACTION,## Recent agent evidence decision context,These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs,Task result completed: Inspect task capability fixture,capability_loop: evidence: TASK_CAPABILITY_SUBAGENT_EVIDENCE,## Agent capability follow-up gate,pending_follow_up: Task result completed: Inspect task capability fixture,follow_up_id: tool:call_task_capability_task,must_handle_next_action: TASK_CAPABILITY_SUBAGENT_NEXT_ACTION,verification_required: TASK_CAPABILITY_SUBAGENT_VERIFICATION,risk_to_account_for: TASK_CAPABILITY_SUBAGENT_RISK" \
    --forbid-request-text "\"name\":\"Write\",\"name\":\"Edit\",\"name\":\"MultiEdit\",\"name\":\"Bash\",\"name\":\"AgentCreate\"" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
const subagentRecords = records.filter(record => record.scope === "subagent");
if (subagentRecords.length < 2) {
  console.error("expected subagent prompt dump records");
  process.exit(1);
}
const allText = records.map(record => JSON.stringify(record.request || {})).join("\n");
for (const marker of [
  "TASK_CAPABILITY_SUBAGENT_FINAL",
  "TASK_CAPABILITY_SUBAGENT_EVIDENCE",
  "TASK_CAPABILITY_SUBAGENT_UNKNOWN",
  "TASK_CAPABILITY_SUBAGENT_VERIFICATION",
  "TASK_CAPABILITY_SUBAGENT_RISK",
  "TASK_CAPABILITY_SUBAGENT_NEXT_ACTION",
]) {
  if (!allText.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
if (!subagentRecords.some(record => JSON.stringify(record.request || {}).includes('"name":"Grep"'))) {
  console.error("subagent did not receive or use Grep in prompt dump");
  process.exit(1);
}
if (!subagentRecords.some(record => JSON.stringify(record.request || {}).includes('"name":"Read"'))) {
  console.error("subagent did not receive or use Read in prompt dump");
  process.exit(1);
}
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const finalMain = mainRecords[mainRecords.length - 1];
const finalText = JSON.stringify(finalMain?.request || {});
if (!finalText.includes("TASK_CAPABILITY_SUBAGENT_FINAL") || !finalText.includes("TASK_CAPABILITY_SUBAGENT_EVIDENCE")) {
  console.error("final parent request did not receive Task evidence result");
  process.exit(1);
}
for (const marker of [
  "## Recent agent evidence decision context",
  "These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs",
  "Task result completed: Inspect task capability fixture",
  "capability_loop: evidence: TASK_CAPABILITY_SUBAGENT_EVIDENCE",
  "## Agent capability follow-up gate",
  "pending_follow_up: Task result completed: Inspect task capability fixture",
  "follow_up_id: tool:call_task_capability_task",
  "must_handle_next_action: TASK_CAPABILITY_SUBAGENT_NEXT_ACTION",
  "verification_required: TASK_CAPABILITY_SUBAGENT_VERIFICATION",
  "risk_to_account_for: TASK_CAPABILITY_SUBAGENT_RISK",
  "TASK_CAPABILITY_SUBAGENT_NEXT_ACTION",
]) {
  if (!finalText.includes(marker)) {
    console.error(`final parent request missing Task recent evidence context: ${marker}`);
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

mkdir -p "$FIXTURE_DIR/internal" "$CONFIG_DIR"
cat >"$FIXTURE_DIR/internal/capability_fixture.go" <<'EOF_FIXTURE'
package internal

// TASK_CAPABILITY_FIXTURE_EVIDENCE proves the sub-agent read concrete code.
func CapabilityLoopFixture() string {
	return "TASK_CAPABILITY_FIXTURE_FUNCTION"
}
EOF_FIXTURE
cat >"$FIXTURE_DIR/README.md" <<'EOF_README'
# Task Capability Loop Fixture

The parent must delegate to Task. The sub-agent must inspect
internal/capability_fixture.go before reporting evidence.
EOF_README

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.TASK_CAPABILITY_PROVIDER_READY || "";
const requestLog = process.env.TASK_CAPABILITY_PROVIDER_LOG || "";
let subagentTurns = 0;

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
  const id = `chatcmpl-task-capability-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 96, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 96 + Math.max(1, Math.ceil(text.length / 4)) } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-task-capability-${Date.now()}`;
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
  const model = parsed.model || "task-capability-loop-stub";
  const all = JSON.stringify(parsed);
  const users = userText(parsed);
  const isSubagent = users.includes("TASK_CAPABILITY_SUBAGENT_PROMPT");
  if (isSubagent) subagentTurns += 1;
  writeLog({
    is_subagent: isSubagent,
    subagent_turns: subagentTurns,
    has_grep_result: users.includes("TASK_CAPABILITY_FIXTURE_EVIDENCE"),
    has_read_result: users.includes("TASK_CAPABILITY_FIXTURE_FUNCTION"),
    has_task_result: users.includes("TASK_CAPABILITY_SUBAGENT_FINAL"),
    parent_saw_evidence: !isSubagent && users.includes("TASK_CAPABILITY_SUBAGENT_EVIDENCE"),
    parent_saw_recent_context: !isSubagent
      && all.includes("## Recent agent evidence decision context")
      && all.includes("Task result completed: Inspect task capability fixture")
      && all.includes("capability_loop: evidence: TASK_CAPABILITY_SUBAGENT_EVIDENCE"),
  });

  if (isSubagent && subagentTurns === 1) {
    streamToolCall(res, model, "call_task_capability_grep", "Grep", {
      pattern: "TASK_CAPABILITY_FIXTURE_EVIDENCE",
      path: "internal/capability_fixture.go",
      output_mode: "content",
      "-n": true,
    });
    return;
  }
  if (isSubagent && subagentTurns === 2) {
    streamToolCall(res, model, "call_task_capability_read", "Read", {
      file_path: "internal/capability_fixture.go",
      offset: 1,
      limit: 20,
    });
    return;
  }
  if (isSubagent) {
    streamText(res, model, [
      "TASK_CAPABILITY_SUBAGENT_FINAL",
      "Summary: inspected the delegated fixture and found the implementation marker.",
      "Evidence:",
      "- TASK_CAPABILITY_SUBAGENT_EVIDENCE: internal/capability_fixture.go contains TASK_CAPABILITY_FIXTURE_EVIDENCE and CapabilityLoopFixture returns TASK_CAPABILITY_FIXTURE_FUNCTION.",
      "Assumptions:",
      "- TASK_CAPABILITY_SUBAGENT_ASSUMPTION: isolated fixture represents the minimal Task evidence flow.",
      "Unknowns:",
      "- TASK_CAPABILITY_SUBAGENT_UNKNOWN: no production repository file was modified or inspected.",
      "Verification:",
      "- TASK_CAPABILITY_SUBAGENT_VERIFICATION: Grep and Read tool results were visible before this final sub-agent answer.",
      "Risks:",
      "- TASK_CAPABILITY_SUBAGENT_RISK: this proves request/tool-result flow, not open-ended model quality.",
      "Next action:",
      "- TASK_CAPABILITY_SUBAGENT_NEXT_ACTION: parent should cite the fixture evidence and state the remaining risk."
    ].join("\n"));
    return;
  }

  if (users.includes("TASK_CAPABILITY_SUBAGENT_FINAL") && users.includes("TASK_CAPABILITY_SUBAGENT_EVIDENCE")) {
    streamText(res, model, "TASK_CAPABILITY_PARENT_USED_EVIDENCE TASK_CAPABILITY_PARENT_USED_UNKNOWNS TASK_CAPABILITY_PARENT_USED_VERIFICATION TASK_CAPABILITY_PARENT_NEXT_ACTION");
    return;
  }

  streamToolCall(res, model, "call_task_capability_task", "Task", {
    description: "Inspect task capability fixture",
    prompt: "TASK_CAPABILITY_SUBAGENT_PROMPT: Use Grep to find TASK_CAPABILITY_FIXTURE_EVIDENCE in internal/capability_fixture.go, then Read that file. Return Summary, Evidence, Assumptions, Unknowns, Verification, Risks, and Next action. Include the exact TASK_CAPABILITY_SUBAGENT_* markers.",
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

TASK_CAPABILITY_PROVIDER_READY="$READY_FILE" \
TASK_CAPABILITY_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="task-capability-loop-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 3 \
  --max-tokens "$MAX_TOKENS" \
  --model "$MODEL" \
  --tools "Task,Grep,Read" \
  -p "TASK_CAPABILITY_PARENT_PROMPT: delegate to a Task sub-agent, require it to inspect internal/capability_fixture.go with Grep and Read, then use the returned evidence, unknowns, verification, risk, and next action in the parent final answer. Do not modify files." >"$CLI_LOG" 2>&1

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
if (!events.some(event => event.is_subagent && event.has_grep_result)) {
  console.error("provider never saw sub-agent Grep evidence");
  process.exit(1);
}
if (!events.some(event => event.is_subagent && event.has_read_result)) {
  console.error("provider never saw sub-agent Read evidence");
  process.exit(1);
}
if (!events.some(event => event.parent_saw_evidence)) {
  console.error("provider never saw parent request with Task evidence result");
  process.exit(1);
}
if (!events.some(event => event.parent_saw_recent_context)) {
  console.error("provider never saw parent request with Task recent evidence decision context");
  process.exit(1);
}
NODE

for marker in \
  TASK_CAPABILITY_PARENT_USED_EVIDENCE \
  TASK_CAPABILITY_PARENT_USED_UNKNOWNS \
  TASK_CAPABILITY_PARENT_USED_VERIFICATION \
  TASK_CAPABILITY_PARENT_NEXT_ACTION
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
