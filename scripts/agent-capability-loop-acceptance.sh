#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"
WORK_DIR="/tmp/niuma-agent-capability-loop-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-capability-loop-${TIMESTAMP}.jsonl"
MODEL="agent-capability-loop-stub"
PROMPT_PROFILE="claude-compatible"
MAX_TOKENS="1024"
MIN_TURNS="4"
VERIFY_ONLY=0
FORCE=0

usage() {
  cat <<'EOF'
Usage:
  scripts/agent-capability-loop-acceptance.sh [flags]

Runs a deterministic code-mode acceptance for the Agent Capability Loop.
The main thread must create a real background agent, wait for completion,
call AgentGet, receive structured capability_loop evidence/unknowns/
verification/next_action fields, and produce a final synthesis only after
those fields are visible in the model request.

Flags:
  --work-dir <dir>        Temp work dir for isolated fixture/config/provider logs.
  --dump <path>           Prompt dump JSONL path.
  --cwd <path>            Accepted for matrix compatibility; this scenario uses an isolated fixture.
  --model <name>          Model name sent to the stub provider.
  --prompt-profile <name> NIUMA_PROMPT_PROFILE value.
  --max-tokens <n>        Max tokens passed to niuma. Default: 1024.
  --min-turns <n>         Minimum prompt dump turns for verification. Default: 4.
  --verify-only           Verify an existing --dump without running the stub provider.
  --force                 Remove existing work dir/dump before running.
  -h, --help              Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir)
      WORK_DIR="$2"
      shift 2
      ;;
    --dump)
      DUMP_PATH="$2"
      shift 2
      ;;
    --cwd)
      shift 2
      ;;
    --model)
      MODEL="$2"
      shift 2
      ;;
    --prompt-profile)
      PROMPT_PROFILE="$2"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="$2"
      shift 2
      ;;
    --min-turns)
      MIN_TURNS="$2"
      shift 2
      ;;
    --verify-only)
      VERIFY_ONLY=1
      shift
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

verify_dump() {
  go run ./scripts/verify-code-mode-prompt-dump.go \
    --min-turns "$MIN_TURNS" \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-subagent-record=true \
    --require-tool-result "AgentCreate,Bash,AgentGet" \
    --require-tool-no-persisted-output "AgentCreate,Bash,AgentGet" \
    --require-tool-max-bytes "AgentCreate=4000,Bash=4000,AgentGet=12000" \
    --require-tool-use-input-text "AgentCreate=CAPABILITY_LOOP_SUBAGENT_PROMPT,AgentGet=1,Bash=CAPABILITY_LOOP_WAIT_DONE" \
    --require-request-text "## Background agent tasks,completion notification: result is ready,call AgentGet before using findings,capability_loop,CAPABILITY_LOOP_EVIDENCE,CAPABILITY_LOOP_UNKNOWN,CAPABILITY_LOOP_VERIFICATION,CAPABILITY_LOOP_NEXT_ACTION,\"capability_loop\"" \
    "$DUMP_PATH"
}

verify_dump_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map(line => JSON.parse(line));
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const fullText = mainRecords.map(record => JSON.stringify(record.request || {})).join("\n");
for (const marker of [
  "CAPABILITY_LOOP_EVIDENCE",
  "CAPABILITY_LOOP_UNKNOWN",
  "CAPABILITY_LOOP_VERIFICATION",
  "CAPABILITY_LOOP_RISK",
  "CAPABILITY_LOOP_NEXT_ACTION",
]) {
  if (!fullText.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
NODE
}

if [[ "$VERIFY_ONLY" == "1" ]]; then
  if [[ ! -s "$DUMP_PATH" ]]; then
    echo "prompt dump missing for verify-only: $DUMP_PATH" >&2
    exit 2
  fi
  verify_dump >&2
  verify_dump_markers
  echo "ok=true"
  echo "dump=$DUMP_PATH"
  exit 0
fi

if [[ -e "$WORK_DIR" && "$FORCE" != "1" ]]; then
  echo "work dir exists, pass --force: $WORK_DIR" >&2
  exit 1
fi
if [[ -e "$DUMP_PATH" && "$FORCE" != "1" ]]; then
  echo "dump exists, pass --force: $DUMP_PATH" >&2
  exit 1
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
cat >"$FIXTURE_DIR/README.md" <<'EOF'
# Agent Capability Loop Fixture

CAPABILITY_LOOP_WORKSPACE_MARKER: this isolated workspace is read-only for
the deterministic capability-loop acceptance.
EOF

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.CAPABILITY_LOOP_PROVIDER_READY || "";
const requestLog = process.env.CAPABILITY_LOOP_PROVIDER_LOG || "";

function writeLog(entry) {
  if (requestLog) {
    fs.appendFileSync(requestLog, `${JSON.stringify({ time: new Date().toISOString(), ...entry })}\n`);
  }
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
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const id = `chatcmpl-agent-capability-loop-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 96, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 96 + Math.max(1, Math.ceil(text.length / 4)) },
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
  const completionID = `chatcmpl-agent-capability-loop-${Date.now()}`;
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
    usage: { prompt_tokens: 128, completion_tokens: 24, total_tokens: 152 },
  });
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

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-capability-loop-stub";
  const all = JSON.stringify(parsed);
  const users = userText(parsed);
  writeLog({
    is_subagent: users.includes("CAPABILITY_LOOP_SUBAGENT_PROMPT"),
    has_background_status: all.includes("## Background agent tasks"),
    has_runtime_capability_loop: all.includes("capability_loop") && all.includes("CAPABILITY_LOOP_EVIDENCE"),
    has_agent_get_result: users.includes("\"capability_loop\"") && users.includes("CAPABILITY_LOOP_VERIFICATION"),
  });

  if (users.includes("CAPABILITY_LOOP_SUBAGENT_PROMPT")) {
    streamText(res, model, [
      "Summary:",
      "- CAPABILITY_LOOP_SUMMARY: inspected the delegated capability-loop task.",
      "",
      "Evidence:",
      "- CAPABILITY_LOOP_EVIDENCE: internal/agentruntime/runtime.go finishTask persists capability_loop.",
      "",
      "Assumptions:",
      "- CAPABILITY_LOOP_ASSUMPTION: parent will verify the structured fields before final synthesis.",
      "",
      "Unknowns:",
      "- CAPABILITY_LOOP_UNKNOWN: no external production transcript was inspected in this deterministic stub.",
      "",
      "Verification:",
      "- CAPABILITY_LOOP_VERIFICATION: scripts/agent-capability-loop-acceptance.sh drove AgentCreate and AgentGet.",
      "",
      "Risks:",
      "- CAPABILITY_LOOP_RISK: field presence alone does not prove real-world reasoning quality.",
      "",
      "Next action:",
      "- CAPABILITY_LOOP_NEXT_ACTION: parent should cite evidence, unknowns, verification, and risk before deciding next work."
    ].join("\n"));
    return;
  }

  const hasAgentGetCapabilityLoop = users.includes("\"capability_loop\"")
    && users.includes("CAPABILITY_LOOP_EVIDENCE")
    && users.includes("CAPABILITY_LOOP_UNKNOWN")
    && users.includes("CAPABILITY_LOOP_VERIFICATION")
    && users.includes("CAPABILITY_LOOP_NEXT_ACTION");
  if (hasAgentGetCapabilityLoop) {
    streamText(res, model, "CAPABILITY_LOOP_PARENT_USED_EVIDENCE CAPABILITY_LOOP_PARENT_USED_UNKNOWNS CAPABILITY_LOOP_PARENT_USED_VERIFICATION CAPABILITY_LOOP_PARENT_NEXT_ACTION");
    return;
  }
  if (all.includes("CAPABILITY_LOOP_WAIT_DONE")) {
    streamToolCall(res, model, "call_capability_agent_get", "AgentGet", { task_id: 1 });
    return;
  }
  if (all.includes("\"task_id\"") && all.includes("output_file")) {
    streamToolCall(res, model, "call_capability_wait", "Bash", {
      command: "sleep 1 && printf CAPABILITY_LOOP_WAIT_DONE",
      description: "wait for capability loop background agent",
    });
    return;
  }
  streamToolCall(res, model, "call_capability_agent_create", "AgentCreate", {
    description: "CAPABILITY_LOOP_AGENT_TASK",
    prompt: "CAPABILITY_LOOP_SUBAGENT_PROMPT: Do not use tools. Return exact sections Summary, Evidence, Assumptions, Unknowns, Verification, Risks, Next action with the provided markers.",
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

CAPABILITY_LOOP_PROVIDER_READY="$READY_FILE" \
CAPABILITY_LOOP_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="agent-capability-loop-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 5 \
  --max-tokens "$MAX_TOKENS" \
  --model "$MODEL" \
  --tools "AgentCreate,AgentGet,Bash" \
  --allowedTools "Bash(sleep *)" \
  -p "CAPABILITY_LOOP_MAIN_PROMPT: create one read-only sub-agent, wait for it, call AgentGet, and synthesize only after seeing evidence, unknowns, verification, risks, and next_action. Do not modify files." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

verify_dump >&2

node - "$DUMP_PATH" "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const providerLogPath = process.argv[3];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map(line => JSON.parse(line));
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const fullText = mainRecords.map(record => JSON.stringify(record.request || {})).join("\n");
for (const marker of [
  "CAPABILITY_LOOP_EVIDENCE",
  "CAPABILITY_LOOP_UNKNOWN",
  "CAPABILITY_LOOP_VERIFICATION",
  "CAPABILITY_LOOP_RISK",
  "CAPABILITY_LOOP_NEXT_ACTION",
]) {
  if (!fullText.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
const providerEvents = fs.readFileSync(providerLogPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!providerEvents.some(event => event.has_runtime_capability_loop)) {
  console.error("provider never saw runtime capability_loop status before AgentGet");
  process.exit(1);
}
if (!providerEvents.some(event => event.has_agent_get_result)) {
  console.error("provider never saw AgentGet capability_loop result");
  process.exit(1);
}
NODE

for marker in \
  CAPABILITY_LOOP_PARENT_USED_EVIDENCE \
  CAPABILITY_LOOP_PARENT_USED_UNKNOWNS \
  CAPABILITY_LOOP_PARENT_USED_VERIFICATION \
  CAPABILITY_LOOP_PARENT_NEXT_ACTION
do
  if ! grep -q "$marker" "$CLI_LOG"; then
    echo "CLI log missing final answer marker $marker" >&2
    exit 1
  fi
done

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "workspace=$FIXTURE_DIR"
