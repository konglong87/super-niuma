#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"

CWD="$ROOT_DIR"
WORK_DIR="/tmp/niuma-agent-capability-loop-compact-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-capability-loop-compact-${TIMESTAMP}.jsonl"
MODEL="agent-capability-loop-compact-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"
VERIFY_ONLY="false"
TERMINAL_STATUS="completed"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-capability-loop-compact-acceptance.sh [flags]

Builds a resume transcript whose background Agent completed with structured
capability_loop evidence, enables the real auto-compact path, and verifies the
compacted next request still carries capability_loop evidence in runtime status
and AgentGet.

Flags:
  --cwd <path>             Accepted for matrix compatibility; this scenario uses an isolated fixture.
  --work-dir <path>        Temp work dir for isolated config/provider logs.
  --dump <path>            Prompt dump path.
  --model <name>           Model name sent to niuma.
  --prompt-profile <name>  Prompt profile. Default: claude-compatible.
  --terminal-status <name> Completed terminal status to fixture: completed, failed, cancelled.
                           Default: completed.
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
    --terminal-status)
      TERMINAL_STATUS="${2:?missing value for --terminal-status}"
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

case "$TERMINAL_STATUS" in
  completed)
    notification_text="completion notification: result is ready"
    prompt_action="call AgentGet and synthesize only after seeing evidence, assumptions, unknowns, verification, risks, and next_action"
    ;;
  failed)
    notification_text="failure notification: call AgentGet before explaining failure details"
    prompt_action="call AgentGet and explain failure only after seeing evidence, assumptions, unknowns, verification, risks, and next_action"
    ;;
  cancelled)
    notification_text="cancellation notification: call AgentGet before explaining cancellation details"
    prompt_action="call AgentGet and explain cancellation only after seeing evidence, assumptions, unknowns, verification, risks, and next_action"
    ;;
  *)
    echo "invalid --terminal-status: $TERMINAL_STATUS (want completed, failed, or cancelled)" >&2
    exit 2
    ;;
esac

verify_dump() {
  go run ./scripts/verify-code-mode-prompt-dump.go \
    --min-turns 2 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-no-tool-errors=false \
    --require-tool-result "AgentGet" \
    --require-tool-no-persisted-output "AgentGet" \
    --require-tool-max-bytes "AgentGet=12000" \
    --require-tool-use-input-text "AgentGet=1" \
    --require-request-text "Conversation summary so far,## Background agent tasks,$notification_text,capability_loop,CAPABILITY_COMPACT_EVIDENCE,CAPABILITY_COMPACT_UNKNOWN,CAPABILITY_COMPACT_VERIFICATION,CAPABILITY_COMPACT_NEXT_ACTION,\"capability_loop\",\"status\",## Agent capability follow-up gate,must_handle_next_action: CAPABILITY_COMPACT_NEXT_ACTION,verification_required: CAPABILITY_COMPACT_VERIFICATION,risk_to_account_for: CAPABILITY_COMPACT_RISK" \
    --forbid-request-text "CAPABILITY_COMPACT_RAW_SHOULD_NOT_APPEAR" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" "$TERMINAL_STATUS" "$notification_text" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const terminalStatus = process.argv[3];
const notificationText = process.argv[4];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map(line => JSON.parse(line));
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const text = mainRecords.map(record => JSON.stringify(record.request || {})).join("\n");
for (const marker of [
  "Conversation summary so far:",
  "CAPABILITY_COMPACT_EVIDENCE",
  "CAPABILITY_COMPACT_ASSUMPTION",
  "CAPABILITY_COMPACT_UNKNOWN",
  "CAPABILITY_COMPACT_VERIFICATION",
  "CAPABILITY_COMPACT_RISK",
  "CAPABILITY_COMPACT_NEXT_ACTION",
  "## Agent capability follow-up gate",
  "must_handle_next_action: CAPABILITY_COMPACT_NEXT_ACTION",
  "verification_required: CAPABILITY_COMPACT_VERIFICATION",
  "risk_to_account_for: CAPABILITY_COMPACT_RISK",
]) {
  if (!text.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
if (!text.includes(notificationText)) {
  console.error(`prompt dump missing ${notificationText}`);
  process.exit(1);
}
if (!text.includes(terminalStatus)) {
  console.error(`prompt dump missing terminal status ${terminalStatus}`);
  process.exit(1);
}
const compactedMain = mainRecords.filter(record => JSON.stringify(record.request || {}).includes("Conversation summary so far:"));
if (!compactedMain.some(record => {
  const raw = JSON.stringify(record.request || {});
  return raw.includes("## Background agent tasks") && raw.includes("capability_loop") && raw.includes("CAPABILITY_COMPACT_EVIDENCE") && raw.includes(terminalStatus);
})) {
  console.error("no compacted main request carried capability_loop runtime status");
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
SESSION_ID="90909090-9090-4090-9090-909090909090"
SUBSESSION_ID="91919191-9191-4191-9191-919191919191"
CONFIG_DIR="$WORK_DIR/config"
HOME_DIR="$WORK_DIR/home"
WORKSPACE="$WORK_DIR/workspace"
REAL_GOMODCACHE="$(go env GOMODCACHE 2>/dev/null || true)"
REAL_GOCACHE="$(go env GOCACHE 2>/dev/null || true)"

mkdir -p "$WORKSPACE"
cat >"$WORKSPACE/README.md" <<'EOF'
# Agent Capability Loop Compact Fixture

This isolated workspace prevents repository project config from disabling the
auto-compact settings required by the deterministic acceptance gate.
EOF

abs_cwd="$(cd "$WORKSPACE" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
main_transcript_path="$transcript_dir/$SESSION_ID.jsonl"
subagent_transcript_path="$transcript_dir/$SUBSESSION_ID.jsonl"
output_file="$transcript_dir/capability-loop-compact-agent.output"
mkdir -p "$transcript_dir" "$HOME_DIR/.niuma"

cat >"$HOME_DIR/.niuma/settings.json" <<JSON
{
  "contextLength": 100,
  "autoCompact": {
    "enabled": true,
    "defaultThresholdRatio": 0.01,
    "preserveRecentRounds": 1,
    "maxSummaryTokens": 800,
    "cooldownTurns": 2,
    "modelContext": {
      "$MODEL": 700
    }
  }
}
JSON

node - "$main_transcript_path" "$subagent_transcript_path" "$output_file" "$SUBSESSION_ID" "$TERMINAL_STATUS" <<'NODE'
const fs = require("fs");
const [mainTranscript, subagentTranscript, outputFile, subSessionID, terminalStatus] = process.argv.slice(2);
const largeContext = "CAPABILITY_COMPACT_HISTORY_PADDING ".repeat(1500);
const result = {
  content: `CAPABILITY_COMPACT_SUMMARY: ${terminalStatus} before compact with structured evidence.`,
  status: terminalStatus,
  agent_name: "general-purpose",
  model: "model",
  session_id: subSessionID,
  transcript_path: subagentTranscript,
  output_file: outputFile,
  turns: 2,
  task_id: 1,
  capability_loop: {
    evidence: ["CAPABILITY_COMPACT_EVIDENCE: internal/query/query.go injects runtime status after MaybeCompact."],
    assumptions: ["CAPABILITY_COMPACT_ASSUMPTION: output_file.state.json is readable during compact-resume gate."],
    unknowns: ["CAPABILITY_COMPACT_UNKNOWN: no production compact transcript was sampled in this deterministic gate."],
    verification: ["CAPABILITY_COMPACT_VERIFICATION: scripts/agent-capability-loop-compact-acceptance.sh forced auto compact and AgentGet."],
    risks: ["CAPABILITY_COMPACT_RISK: compact marker visibility is not a real-model win-rate proof."],
    next_action: "CAPABILITY_COMPACT_NEXT_ACTION: parent should cite compact-preserved evidence before final synthesis."
  }
};
fs.writeFileSync(outputFile, result.content + "\n");
fs.writeFileSync(`${outputFile}.state.json`, JSON.stringify({ status: terminalStatus, result }) + "\n");
const mainEntries = [
  { type: "message", role: "user", content: `create an agent that becomes ${terminalStatus} before compact\n` + largeContext },
  { type: "message", role: "assistant", content: "I will delegate and preserve capability loop evidence." },
  { type: "tool_call", tool_id: "toolu_capability_compact_agent_create", tool_name: "AgentCreate", content: "{\"prompt\":\"collect compact capability evidence\",\"description\":\"capability compact probe\"}" },
  { type: "tool_result", tool_id: "toolu_capability_compact_agent_create", tool_name: "AgentCreate", content: JSON.stringify({ agent_name: "general-purpose", model: "model", output_file: outputFile, session_id: subSessionID, status: "running", task_id: 1 }) }
];
const subEntries = [
  { type: "message", role: "user", content: "collect compact capability evidence" },
  { type: "message", role: "assistant", content: result.content }
];
fs.writeFileSync(mainTranscript, mainEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
fs.writeFileSync(subagentTranscript, subEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.CAPABILITY_COMPACT_PROVIDER_READY || "";
const requestLog = process.env.CAPABILITY_COMPACT_PROVIDER_LOG || "";

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
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const id = `chatcmpl-agent-capability-compact-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 64, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 64 + Math.max(1, Math.ceil(text.length / 4)) },
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
  const completionID = `chatcmpl-agent-capability-compact-${Date.now()}`;
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
    usage: { prompt_tokens: 96, completion_tokens: 12, total_tokens: 108 },
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

function compactSummary(source) {
  const paths = Array.from(new Set((source.match(/\/tmp\/[^\s"')]+/g) || []).map(path => path.replace(/[.,;:]+$/, ""))));
  const fileLines = paths.length > 0 ? paths.map(path => `- ${path}`) : ["- scripts/agent-capability-loop-compact-acceptance.sh"];
  return [
    "## Current Goal",
    "Verify capability_loop survives auto compact.",
    "## User Preferences / Constraints",
    "Do not modify files.",
    "## Decisions Made",
    "Use deterministic compact-resume gate.",
    "## Files / Code Changed",
    ...fileLines,
    "## Commands / Test Results",
    "Auto compact summary request was triggered.",
    "## Open Tasks",
    "Call AgentGet after compact before final synthesis.",
    "## Known Issues / Risks",
    "Compact marker visibility is not open-task win-rate proof.",
    "## Important Raw Facts",
    "CAPABILITY_COMPACT_EVIDENCE and CAPABILITY_COMPACT_UNKNOWN must survive via runtime status."
  ].join("\n");
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-capability-loop-compact-stub";
  const terminalStatus = process.env.CAPABILITY_COMPACT_TERMINAL_STATUS || "completed";
  const notificationText = terminalStatus === "failed"
    ? "failure notification: call AgentGet before explaining failure details"
    : terminalStatus === "cancelled"
      ? "cancellation notification: call AgentGet before explaining cancellation details"
      : "completion notification: result is ready";
  const text = JSON.stringify(parsed);
  const users = userText(parsed);
  const isSummaryRequest = users.includes("Summarize the conversation history below for continuing an agent session.");
  const hasCompactedCapabilityLoop = text.includes("Conversation summary so far:")
    && text.includes("## Background agent tasks")
    && text.includes(notificationText)
    && text.includes("capability_loop")
    && text.includes("CAPABILITY_COMPACT_EVIDENCE")
    && text.includes("CAPABILITY_COMPACT_UNKNOWN")
    && text.includes("CAPABILITY_COMPACT_VERIFICATION")
    && text.includes(terminalStatus);
  const hasAgentGetCapabilityLoop = users.includes("\"capability_loop\"")
    && users.includes("CAPABILITY_COMPACT_EVIDENCE")
    && users.includes("CAPABILITY_COMPACT_ASSUMPTION")
    && users.includes("CAPABILITY_COMPACT_UNKNOWN")
    && users.includes("CAPABILITY_COMPACT_VERIFICATION")
    && users.includes("CAPABILITY_COMPACT_RISK")
    && users.includes("CAPABILITY_COMPACT_NEXT_ACTION")
    && users.includes(`"status": "${terminalStatus}"`);
  writeLog({
    is_summary_request: isSummaryRequest,
    has_compacted_capability_loop: hasCompactedCapabilityLoop,
    has_agent_get_capability_loop: hasAgentGetCapabilityLoop,
    terminal_status: terminalStatus,
  });

  if (isSummaryRequest) {
    streamText(res, model, compactSummary(users));
    return;
  }
  if (hasAgentGetCapabilityLoop) {
    streamText(res, model, "CAPABILITY_COMPACT_PARENT_USED_EVIDENCE CAPABILITY_COMPACT_PARENT_USED_UNKNOWN CAPABILITY_COMPACT_PARENT_USED_VERIFICATION CAPABILITY_COMPACT_PARENT_NEXT_ACTION");
    return;
  }
  if (text.includes("CAPABILITY_LOOP_COMPACT_PROMPT") && hasCompactedCapabilityLoop && !text.includes("call_capability_compact_agent_get")) {
    streamToolCall(res, model, "call_capability_compact_agent_get", "AgentGet", { task_id: 1 });
    return;
  }
  streamText(res, model, "CAPABILITY_COMPACT_UNEXPECTED");
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

CAPABILITY_COMPACT_PROVIDER_READY="$READY_FILE" \
CAPABILITY_COMPACT_PROVIDER_LOG="$PROVIDER_LOG" \
CAPABILITY_COMPACT_TERMINAL_STATUS="$TERMINAL_STATUS" \
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

export HOME="$HOME_DIR"
if [[ -n "$REAL_GOMODCACHE" ]]; then
  export GOMODCACHE="$REAL_GOMODCACHE"
fi
if [[ -n "$REAL_GOCACHE" ]]; then
  export GOCACHE="$REAL_GOCACHE"
fi
export NIUMA_CONFIG_DIR="$CONFIG_DIR"
export NIUMA_PROVIDER="custom"
export ANTHROPIC_BASE_URL="$BASE_URL/v1"
export ANTHROPIC_API_KEY="agent-capability-loop-compact-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$WORKSPACE" \
  --resume "$SESSION_ID" \
  --max-turns 2 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentGet" \
  -p "CAPABILITY_LOOP_COMPACT_PROMPT: after compacted resume, $prompt_action. Do not modify files." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

verify_dump >&2
verify_markers

node - "$PROVIDER_LOG" "$TERMINAL_STATUS" <<'NODE'
const fs = require("fs");
const providerLog = process.argv[2];
const terminalStatus = process.argv[3];
const events = fs.readFileSync(providerLog, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!events.some(event => event.is_summary_request)) {
  console.error("provider never saw auto compact summary request");
  process.exit(1);
}
if (!events.some(event => event.has_compacted_capability_loop)) {
  console.error("provider never saw compacted request with capability_loop");
  process.exit(1);
}
if (!events.some(event => event.has_agent_get_capability_loop)) {
  console.error(`provider never saw AgentGet ${terminalStatus} capability_loop after compact`);
  process.exit(1);
}
NODE

for marker in \
  CAPABILITY_COMPACT_PARENT_USED_EVIDENCE \
  CAPABILITY_COMPACT_PARENT_USED_UNKNOWN \
  CAPABILITY_COMPACT_PARENT_USED_VERIFICATION \
  CAPABILITY_COMPACT_PARENT_NEXT_ACTION
do
  if ! grep -q "$marker" "$CLI_LOG"; then
    echo "CLI log missing final answer marker $marker" >&2
    exit 1
  fi
done

echo "ok=true"
echo "terminal_status=$TERMINAL_STATUS"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "workspace=$WORKSPACE"
echo "main_transcript=$main_transcript_path"
echo "subagent_transcript=$subagent_transcript_path"
echo "output_file=$output_file"
echo "provider_log=$PROVIDER_LOG"
