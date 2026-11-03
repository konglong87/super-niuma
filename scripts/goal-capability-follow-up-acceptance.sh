#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"
WORK_DIR="/tmp/niuma-goal-follow-up-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-goal-follow-up-${TIMESTAMP}.jsonl"
RESOLUTION_DUMP_PATH="/tmp/niuma-goal-follow-up-${TIMESTAMP}-resolved.jsonl"
MODEL="goal-follow-up-stub"
MAX_TOKENS="512"
VERIFY_ONLY=0
FORCE=0

usage() {
  cat <<'EOF'
Usage:
  scripts/goal-capability-follow-up-acceptance.sh [flags]

Runs a deterministic Goal Mode prompt-dump acceptance. The script creates an
isolated goal, injects structured capability_loop evidence, runs
`goal run --once` against a local stub provider, and verifies that the real
model request contains `## Goal capability follow-up gate` with next-action,
verification, unknown, risk, provenance, and source-action constraints.

Flags:
  --work-dir <dir>   Temp work dir for isolated config, fixture, provider logs.
  --dump <path>      Prompt dump JSONL path.
  --model <name>     Model name sent to the stub provider.
  --max-tokens <n>   Max tokens passed to niuma. Default: 512.
  --verify-only      Verify an existing --dump without running the stub provider.
  --force            Remove existing work dir/dump before running.
  -h, --help         Show this help.
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
      RESOLUTION_DUMP_PATH="${DUMP_PATH%.jsonl}-resolved.jsonl"
      shift 2
      ;;
    --model)
      MODEL="$2"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="$2"
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
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
if (!fs.existsSync(dumpPath) || fs.statSync(dumpPath).size === 0) {
  console.error(`prompt dump missing or empty: ${dumpPath}`);
  process.exit(1);
}
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map((line, index) => {
  try {
    return JSON.parse(line);
  } catch (error) {
    console.error(`invalid prompt dump JSON at line ${index + 1}: ${error.message}`);
    process.exit(1);
  }
});
const fullText = records.map(record => JSON.stringify(record.request || record)).join("\n");
const gateIndex = fullText.indexOf("## Goal capability follow-up gate");
if (gateIndex < 0) {
  console.error("prompt dump missing ## Goal capability follow-up gate");
  process.exit(1);
}
const gateEnd = fullText.indexOf("\\n\\nRules:", gateIndex);
const gateText = fullText.slice(gateIndex, gateEnd >= 0 ? gateEnd : gateIndex + 4000);
for (const marker of [
  "## Goal capability follow-up gate",
  "pending_goal_follow_up:",
  "evidence_source: terminal_agent_task_store",
  "source_action: This is fallback terminal task-store evidence",
  "must_handle_next_action: GOAL_GATE_NEXT_ACTION: inspect child output before completion",
  "verification_required: GOAL_GATE_VERIFICATION: rerun focused goal acceptance",
  "unknown_to_resolve_or_disclose: GOAL_GATE_UNKNOWN: whether follow-up entered the live request",
  "risk_to_account_for: GOAL_GATE_RISK: goal may complete before verification",
]) {
  if (!fullText.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
for (const forbidden of [
  "None observed",
  "verification_required: None observed",
  "unknown_to_resolve_or_disclose: None observed",
  "risk_to_account_for: None observed",
]) {
  if (gateText.includes(forbidden)) {
    console.error(`Goal follow-up gate contains forbidden placeholder ${forbidden}`);
    process.exit(1);
  }
}
NODE
}

verify_resolution_dump() {
  node - "$RESOLUTION_DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
if (!fs.existsSync(dumpPath) || fs.statSync(dumpPath).size === 0) {
  console.error(`resolution prompt dump missing or empty: ${dumpPath}`);
  process.exit(1);
}
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map((line, index) => {
  try {
    return JSON.parse(line);
  } catch (error) {
    console.error(`invalid resolution prompt dump JSON at line ${index + 1}: ${error.message}`);
    process.exit(1);
  }
});
const request = records[records.length - 1].request || records[records.length - 1];
const messages = Array.isArray(request.messages) ? request.messages : [];
const lastUser = [...messages].reverse().find(message => message.role === "user");
const currentPromptText = JSON.stringify(lastUser || request);
for (const marker of [
  "capability_follow_up_resolution:",
  "resolved_follow_up=GOAL_GATE_RESOLVED: handled pending follow-up",
  "supersedes_evidence_id=ev_goal_follow_up_gate",
  "GOAL_GATE_RESOLUTION_EVIDENCE: inspected child output",
  "GOAL_GATE_RESOLUTION_VERIFICATION: focused acceptance passed",
]) {
  if (!currentPromptText.includes(marker)) {
    console.error(`resolution prompt dump missing ${marker}`);
    process.exit(1);
  }
}
if (currentPromptText.includes("pending_goal_follow_up: ev_goal_follow_up_gate")) {
  console.error("resolution prompt dump still contains superseded pending follow-up gate");
  process.exit(1);
}
NODE
}

if [[ "$VERIFY_ONLY" -eq 1 ]]; then
  verify_dump
  echo "ok=true dump=$DUMP_PATH"
  exit 0
fi

if [[ "$FORCE" -eq 1 ]]; then
  rm -rf "$WORK_DIR" "$DUMP_PATH" "$RESOLUTION_DUMP_PATH"
fi
if [[ -e "$WORK_DIR" || -e "$DUMP_PATH" || -e "$RESOLUTION_DUMP_PATH" ]]; then
  echo "work dir or dump already exists; use --force or pass unique paths" >&2
  exit 2
fi

CONFIG_DIR="$WORK_DIR/config"
FIXTURE_DIR="$WORK_DIR/fixture"
PROVIDER_SCRIPT="$WORK_DIR/provider.js"
PROVIDER_READY="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-events.jsonl"
CLI_LOG="$WORK_DIR/goal-run.log"
RESOLUTION_CLI_LOG="$WORK_DIR/goal-run-resolved.log"
GOAL_START_JSON="$WORK_DIR/goal-start.json"

mkdir -p "$CONFIG_DIR" "$FIXTURE_DIR"

cat >"$FIXTURE_DIR/README.md" <<'EOF'
# Goal follow-up acceptance fixture

This workspace is intentionally tiny. The acceptance verifies that existing
Goal evidence enters the next model request as a completion gate.
EOF

cat >"$PROVIDER_SCRIPT" <<'NODE'
const fs = require("fs");
const http = require("http");

const readyFile = process.env.GOAL_FOLLOW_UP_PROVIDER_READY;
const logFile = process.env.GOAL_FOLLOW_UP_PROVIDER_LOG;

function readBody(req) {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", chunk => { body += chunk; });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

function writeLog(event) {
  fs.appendFileSync(logFile, JSON.stringify(event) + "\n");
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
  const id = `chatcmpl-goal-follow-up-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 96, completion_tokens: 24, total_tokens: 120 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "goal-follow-up-stub";
  const requestText = JSON.stringify(parsed);
  writeLog({
    has_goal_follow_up_gate: requestText.includes("## Goal capability follow-up gate"),
    has_next_action: requestText.includes("GOAL_GATE_NEXT_ACTION"),
    has_verification: requestText.includes("GOAL_GATE_VERIFICATION"),
    has_unknown: requestText.includes("GOAL_GATE_UNKNOWN"),
    has_risk: requestText.includes("GOAL_GATE_RISK"),
  });
  streamText(res, model, [
    "Observed Goal capability follow-up gate in the request, but attempting premature completion.",
    "GOAL_STATUS: complete"
  ].join("\n"));
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true }));
    return;
  }
  if (req.method === "POST" && req.url === "/v1/chat/completions") {
    void handleChat(req, res).catch(error => {
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: { message: String(error && error.stack || error) } }));
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

GOAL_FOLLOW_UP_PROVIDER_READY="$PROVIDER_READY" \
GOAL_FOLLOW_UP_PROVIDER_LOG="$PROVIDER_LOG" \
node "$PROVIDER_SCRIPT" >"$WORK_DIR/provider.log" 2>&1 &
provider_pid=$!

for _ in {1..100}; do
  if [[ -s "$PROVIDER_READY" ]]; then
    break
  fi
  sleep 0.05
done
if [[ ! -s "$PROVIDER_READY" ]]; then
  echo "provider did not become ready" >&2
  cat "$WORK_DIR/provider.log" >&2 || true
  exit 1
fi
BASE_URL="$(node -e 'const fs=require("fs"); process.stdout.write(JSON.parse(fs.readFileSync(process.argv[1], "utf8")).base_url)' "$PROVIDER_READY")"

export CLAUDE_CONFIG_DIR="$CONFIG_DIR"
export NIUMA_CONFIG_DIR="$CONFIG_DIR"
export NIUMA_PROVIDER="custom"
export ANTHROPIC_BASE_URL="$BASE_URL/v1"
export ANTHROPIC_API_KEY="goal-follow-up-test-key"
export NIUMA_DUMP_PROMPT_FULL="true"

go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --model "$MODEL" \
  goal start --json \
  --cwd "$FIXTURE_DIR" \
  --model "$MODEL" \
  --turn-budget 3 \
  --token-budget 2000 \
  "Verify that Goal capability evidence gates completion." >"$GOAL_START_JSON"

GOAL_ID="$(node -e 'const fs=require("fs"); process.stdout.write(JSON.parse(fs.readFileSync(process.argv[1], "utf8")).id)' "$GOAL_START_JSON")"

node - "$CONFIG_DIR" "$GOAL_ID" <<'NODE'
const fs = require("fs");
const path = require("path");
const root = process.argv[2];
const goalID = process.argv[3];
const evidenceDir = path.join(root, "goals", "evidence");
fs.mkdirSync(evidenceDir, { recursive: true });
const file = path.join(evidenceDir, `goal_${goalID.replace(/^goal_/, "")}.jsonl`);
const evidence = {
  id: "ev_goal_follow_up_gate",
  goal_id: goalID,
  type: "manual",
  summary: "TaskStore failed partial evidence",
  passed: false,
  payload: {
    evidence_source: "terminal_agent_task_store",
    agent_task_id: 77,
    agent_status: "failed",
    partial_evidence: true,
    capability_loop: {
      evidence: ["GOAL_GATE_EVIDENCE: child found incomplete verification"],
      assumptions: ["None observed"],
      unknowns: ["GOAL_GATE_UNKNOWN: whether follow-up entered the live request"],
      verification: ["GOAL_GATE_VERIFICATION: rerun focused goal acceptance"],
      risks: ["GOAL_GATE_RISK: goal may complete before verification"],
      next_action: "GOAL_GATE_NEXT_ACTION: inspect child output before completion"
    }
  },
  created_at: new Date().toISOString()
};
fs.appendFileSync(file, JSON.stringify(evidence) + "\n");
NODE

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 1 \
  --max-tokens "$MAX_TOKENS" \
  --model "$MODEL" \
  goal run --once "$GOAL_ID" --json >"$CLI_LOG" 2>&1

verify_dump

node - "$CLI_LOG" <<'NODE'
const fs = require("fs");
const runPath = process.argv[2];
const text = fs.readFileSync(runPath, "utf8");
const start = text.lastIndexOf('{\n  "Goal"');
if (start < 0) {
  console.error(`goal run output did not contain RunResult JSON:\n${text}`);
  process.exit(1);
}
let parsed;
try {
  parsed = JSON.parse(text.slice(start));
} catch (error) {
  console.error(`goal run output is not JSON: ${error.message}\n${text}`);
  process.exit(1);
}
const goal = parsed.goal || parsed.Goal;
const decision = parsed.decision || parsed.Decision;
if (!goal || goal.status !== "active") {
  console.error(`expected goal.status active after premature complete, got ${JSON.stringify(goal)}`);
  process.exit(1);
}
if (!decision || decision.status !== "continue") {
  console.error(`expected decision.status continue after premature complete, got ${JSON.stringify(decision)}`);
  process.exit(1);
}
if (!String(decision.reason || "").includes("pending capability follow-up")) {
  console.error(`decision reason did not mention pending capability follow-up: ${JSON.stringify(decision)}`);
  process.exit(1);
}
if (!String(decision.next_action || "").includes("GOAL_GATE_NEXT_ACTION")) {
  console.error(`decision next_action did not carry capability follow-up: ${JSON.stringify(decision)}`);
  process.exit(1);
}
NODE

node - "$CONFIG_DIR" "$GOAL_ID" <<'NODE'
const fs = require("fs");
const path = require("path");
const root = process.argv[2];
const goalID = process.argv[3];
const evidenceDir = path.join(root, "goals", "evidence");
const file = path.join(evidenceDir, `goal_${goalID.replace(/^goal_/, "")}.jsonl`);
const evidence = {
  id: "ev_goal_follow_up_resolution",
  goal_id: goalID,
  type: "manual",
  summary: "Resolved pending Goal capability follow-up",
  passed: true,
  payload: {
    evidence_source: "manual_resolution",
    resolved_follow_up: "GOAL_GATE_RESOLVED: handled pending follow-up",
    supersedes_evidence_id: "ev_goal_follow_up_gate",
    capability_loop: {
      evidence: ["GOAL_GATE_RESOLUTION_EVIDENCE: inspected child output"],
      verification: ["GOAL_GATE_RESOLUTION_VERIFICATION: focused acceptance passed"]
    }
  },
  created_at: new Date().toISOString()
};
fs.appendFileSync(file, JSON.stringify(evidence) + "\n");
NODE

NIUMA_DUMP_PROMPT_JSON="$RESOLUTION_DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 1 \
  --max-tokens "$MAX_TOKENS" \
  --model "$MODEL" \
  goal run --once "$GOAL_ID" --json >"$RESOLUTION_CLI_LOG" 2>&1

verify_resolution_dump

node - "$RESOLUTION_CLI_LOG" <<'NODE'
const fs = require("fs");
const runPath = process.argv[2];
const text = fs.readFileSync(runPath, "utf8");
const start = text.lastIndexOf('{\n  "Goal"');
if (start < 0) {
  console.error(`resolved goal run output did not contain RunResult JSON:\n${text}`);
  process.exit(1);
}
let parsed;
try {
  parsed = JSON.parse(text.slice(start));
} catch (error) {
  console.error(`resolved goal run output is not JSON: ${error.message}\n${text}`);
  process.exit(1);
}
const goal = parsed.goal || parsed.Goal;
const decision = parsed.decision || parsed.Decision;
if (!goal || !decision) {
  console.error(`expected goal and decision after follow-up resolution, got goal=${JSON.stringify(goal)} decision=${JSON.stringify(decision)}`);
  process.exit(1);
}
if (String(decision.reason || "").includes("pending capability follow-up")) {
  console.error(`follow-up resolution should clear capability hard gate, got ${JSON.stringify(decision)}`);
  process.exit(1);
}
if (String(decision.next_action || "").includes("GOAL_GATE_NEXT_ACTION")) {
  console.error(`follow-up resolution should not carry superseded next_action, got ${JSON.stringify(decision)}`);
  process.exit(1);
}
NODE

node - "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const logPath = process.argv[2];
const events = fs.readFileSync(logPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!events.some(event => event.has_goal_follow_up_gate && event.has_next_action && event.has_verification && event.has_unknown && event.has_risk)) {
  console.error("provider never saw complete Goal capability follow-up gate");
  process.exit(1);
}
NODE

echo "ok=true dump=$DUMP_PATH resolution_dump=$RESOLUTION_DUMP_PATH work_dir=$WORK_DIR goal_id=$GOAL_ID"
