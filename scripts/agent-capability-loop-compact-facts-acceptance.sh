#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"

WORK_DIR="/tmp/niuma-agent-capability-loop-compact-facts-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-capability-loop-compact-facts-${TIMESTAMP}.jsonl"
MODEL="agent-capability-loop-compact-facts-stub"
PROMPT_PROFILE="claude-compatible"
VERIFY_ONLY="false"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-capability-loop-compact-facts-acceptance.sh [flags]

Builds a resume transcript where AgentGet has already returned structured
capability_loop JSON, then forces real auto-compact and verifies the next model
request carries capability_loop as compact hard facts.

Flags:
  --cwd <path>             Accepted for matrix compatibility; this scenario uses an isolated fixture.
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

verify_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map(line => JSON.parse(line));
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const rawRequests = mainRecords.map(record => JSON.stringify(record.request || {}));
const compacted = rawRequests.filter(text => text.includes("Conversation summary so far:"));
if (compacted.length === 0) {
  console.error("no compacted main request found");
  process.exit(1);
}
const hardFacts = compacted.find(text =>
  text.includes("## Runtime Extracted Facts") &&
  text.includes("Capability Loop Evidence") &&
  text.includes("CAPABILITY_COMPACT_FACT_EVIDENCE") &&
  text.includes("child-reports/readiness.json") &&
  text.includes("child-reports/risk.json") &&
  text.includes("Capability Loop Verification") &&
  text.includes("CAPABILITY_COMPACT_FACT_VERIFICATION") &&
  text.includes("Capability Loop Unknowns") &&
  text.includes("CAPABILITY_COMPACT_FACT_UNKNOWN") &&
  text.includes("Capability Loop Risks") &&
  text.includes("CAPABILITY_COMPACT_FACT_RISK") &&
  text.includes("CHILD_BETA_RISK_BLOCKER") &&
    text.includes("Capability Loop Next Actions") &&
    text.includes("CAPABILITY_COMPACT_FACT_NEXT_ACTION") &&
    text.includes("RELEASE_GAMMA_HOLD") &&
    text.includes("Capability Loop Follow Up IDs") &&
    text.includes("task:1") &&
    text.includes("Capability Loop Resolved Follow Ups") &&
    text.includes("CAPABILITY_COMPACT_FACT_RESOLVED") &&
    text.includes("Capability Loop Supersedes") &&
    text.includes("Capability Loop Artifacts") &&
    text.includes("93939393-9393-4393-9393-939393939393") &&
    text.includes("capability-loop-compact-facts-agent.output") &&
    text.includes("capability-loop-compact-facts-worktree") &&
    text.includes("agent/compact-facts")
);
if (!hardFacts) {
  console.error("compacted request missing capability_loop hard facts");
  process.exit(1);
}
for (const marker of [
  "CAPABILITY_COMPACT_FACT_PARENT_USED_EVIDENCE",
  "CAPABILITY_COMPACT_FACT_PARENT_USED_CHILD_PATHS",
  "CAPABILITY_COMPACT_FACT_PARENT_USED_RISK_BLOCKER",
  "CAPABILITY_COMPACT_FACT_PARENT_USED_HOLD_DECISION",
  "CAPABILITY_COMPACT_FACT_PARENT_USED_UNKNOWN",
  "CAPABILITY_COMPACT_FACT_PARENT_USED_VERIFICATION",
  "CAPABILITY_COMPACT_FACT_PARENT_USED_NEXT_ACTION",
]) {
  const saw = rawRequests.some(text => text.includes(marker));
  if (saw) {
    console.error(`final answer marker unexpectedly appeared inside request: ${marker}`);
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
SESSION_ID="92929292-9292-4292-9292-929292929292"
SUBSESSION_ID="93939393-9393-4393-9393-939393939393"
CONFIG_DIR="$WORK_DIR/config"
HOME_DIR="$WORK_DIR/home"
WORKSPACE="$WORK_DIR/workspace"
REAL_GOMODCACHE="$(go env GOMODCACHE 2>/dev/null || true)"
REAL_GOCACHE="$(go env GOCACHE 2>/dev/null || true)"

mkdir -p "$WORKSPACE"
cat >"$WORKSPACE/README.md" <<'EOF'
# Agent Capability Loop Compact Facts Fixture

This fixture verifies AgentGet capability_loop JSON survives auto compact as
runtime extracted facts.
EOF

abs_cwd="$(cd "$WORKSPACE" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
main_transcript_path="$transcript_dir/$SESSION_ID.jsonl"
subagent_transcript_path="$transcript_dir/$SUBSESSION_ID.jsonl"
output_file="$transcript_dir/capability-loop-compact-facts-agent.output"
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

node - "$main_transcript_path" "$subagent_transcript_path" "$output_file" "$SUBSESSION_ID" <<'NODE'
const fs = require("fs");
const [mainTranscript, subagentTranscript, outputFile, subSessionID] = process.argv.slice(2);
const largeContext = "CAPABILITY_COMPACT_FACT_HISTORY_PADDING ".repeat(1500);
const result = {
  task: {
    id: 1,
    status: "completed",
    agent_name: "general-purpose",
    description: "capability compact hard facts probe",
    session_id: subSessionID,
  },
  result: {
    content: "CAPABILITY_COMPACT_FACT_SUMMARY: AgentGet returned before compact.",
    status: "completed",
    agent_name: "general-purpose",
    session_id: subSessionID,
    transcript_path: subagentTranscript,
    output_file: outputFile,
    worktree_path: `${outputFile}.capability-loop-compact-facts-worktree`,
    worktree_branch: "agent/compact-facts",
    turns: 2,
    task_id: 1,
    capability_loop: {
      evidence: [
        "CAPABILITY_COMPACT_FACT_EVIDENCE: internal/compact/facts.go extracts nested AgentGet capability_loop JSON.",
        "PARENT_COMPACT_FACT_SOURCE_PATHS: child-reports/readiness.json and child-reports/risk.json were consumed before compact.",
        "CHILD_ALPHA_EVIDENCE_OK: child-reports/readiness.json supports partial readiness."
      ],
      assumptions: ["CAPABILITY_COMPACT_FACT_ASSUMPTION: resume transcript includes AgentGet tool_result before compaction."],
      unknowns: ["CAPABILITY_COMPACT_FACT_UNKNOWN: this deterministic gate does not measure real-model task win rate."],
      verification: [
        "CAPABILITY_COMPACT_FACT_VERIFICATION: scripts/agent-capability-loop-compact-facts-acceptance.sh checks compacted prompt dump.",
        "PARENT_COMPACT_FACT_VERIFIED_PATH_RETENTION: compacted request still contains child-reports/readiness.json and child-reports/risk.json."
      ],
      risks: [
        "CAPABILITY_COMPACT_FACT_RISK: prompt dump proof is narrower than broad APG open-task evaluation.",
        "CHILD_BETA_RISK_BLOCKER: child-reports/risk.json blocks release approval."
      ],
      next_action: "CAPABILITY_COMPACT_FACT_NEXT_ACTION: parent should hold RELEASE_GAMMA_HOLD and cite child source paths before final synthesis.",
      follow_up_id: "task:1",
      resolved_follow_up: "CAPABILITY_COMPACT_FACT_RESOLVED: previous task:1 next_action was verified before compact.",
      supersedes_evidence_id: "task:1"
    }
  },
  progress_summary: {
    status: "completed",
    turns: 2,
    messages: 1,
    tool_calls: 1,
    tool_results: 1
  }
};
fs.writeFileSync(outputFile, result.result.content + "\n");
const mainEntries = [
  { type: "message", role: "user", content: `create an agent and inspect the completed AgentGet result before compact\n${largeContext}` },
  { type: "message", role: "assistant", content: "I will inspect the completed sub-agent result before continuing." },
  { type: "tool_call", tool_id: "toolu_capability_compact_facts_agent_get", tool_name: "AgentGet", content: "{\"task_id\":1}" },
  { type: "tool_result", tool_id: "toolu_capability_compact_facts_agent_get", tool_name: "AgentGet", content: JSON.stringify(result) },
  { type: "message", role: "assistant", content: "I saw AgentGet capability_loop evidence and will preserve it." }
];
const subEntries = [
  { type: "message", role: "user", content: "collect compact hard facts evidence" },
  { type: "message", role: "assistant", content: result.result.content }
];
fs.writeFileSync(mainTranscript, mainEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
fs.writeFileSync(subagentTranscript, subEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.CAPABILITY_COMPACT_FACTS_PROVIDER_READY || "";
const requestLog = process.env.CAPABILITY_COMPACT_FACTS_PROVIDER_LOG || "";

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
  const id = `chatcmpl-agent-capability-compact-facts-${Date.now()}`;
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
  const absolutePaths = (source.match(/\/tmp\/[^\s"')]+/g) || []).map(path => path.replace(/[.,;:]+$/, ""));
  const relativePaths = (source.match(/[A-Za-z0-9_.-]+(?:\/[A-Za-z0-9_.@+-]+)+/g) || []).map(path => path.replace(/[.,;:]+$/, ""));
  const paths = Array.from(new Set([...absolutePaths, ...relativePaths]));
  const fileLines = paths.length > 0 ? paths.map(path => `- ${path}`) : ["- No fixture paths extracted."];
  return [
    "## Current Goal",
    "Verify AgentGet result survives auto compact via runtime hard facts.",
    "## User Preferences / Constraints",
    "Do not modify files.",
    "## Decisions Made",
    "Use deterministic AgentGet-before-compact gate.",
    "## Files / Code Changed",
    ...fileLines,
    "## Commands / Test Results",
    "Auto compact summary request was triggered.",
    "## Open Tasks",
    "Synthesize from compact hard facts.",
    "## Known Issues / Risks",
    "SUMMARY_MODEL_SHOULD_NOT_CREATE_CAPABILITY_MARKERS.",
    "## Important Raw Facts",
    "The summary model intentionally omits capability loop markers."
  ].join("\n");
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-capability-loop-compact-facts-stub";
  const raw = JSON.stringify(parsed);
  const users = userText(parsed);
  const isSummaryRequest = users.includes("Summarize the conversation history below for continuing an agent session.");
  const hasHardFacts = raw.includes("Conversation summary so far:")
    && raw.includes("## Runtime Extracted Facts")
    && raw.includes("Capability Loop Evidence")
    && raw.includes("CAPABILITY_COMPACT_FACT_EVIDENCE")
    && raw.includes("child-reports/readiness.json")
    && raw.includes("child-reports/risk.json")
    && raw.includes("Capability Loop Unknowns")
    && raw.includes("CAPABILITY_COMPACT_FACT_UNKNOWN")
    && raw.includes("Capability Loop Verification")
    && raw.includes("CAPABILITY_COMPACT_FACT_VERIFICATION")
    && raw.includes("Capability Loop Risks")
    && raw.includes("CAPABILITY_COMPACT_FACT_RISK")
    && raw.includes("CHILD_BETA_RISK_BLOCKER")
    && raw.includes("Capability Loop Next Actions")
    && raw.includes("CAPABILITY_COMPACT_FACT_NEXT_ACTION")
    && raw.includes("RELEASE_GAMMA_HOLD")
    && raw.includes("Capability Loop Follow Up IDs")
    && raw.includes("task:1")
    && raw.includes("Capability Loop Resolved Follow Ups")
    && raw.includes("CAPABILITY_COMPACT_FACT_RESOLVED")
    && raw.includes("Capability Loop Supersedes")
    && raw.includes("Capability Loop Artifacts")
    && raw.includes("93939393-9393-4393-9393-939393939393")
    && raw.includes("capability-loop-compact-facts-agent.output")
    && raw.includes("capability-loop-compact-facts-worktree")
    && raw.includes("agent/compact-facts");
  writeLog({
    is_summary_request: isSummaryRequest,
    has_compacted_capability_hard_facts: hasHardFacts,
  });
  if (isSummaryRequest) {
    streamText(res, model, compactSummary(users));
    return;
  }
  if (hasHardFacts) {
    streamText(res, model, "CAPABILITY_COMPACT_FACT_PARENT_USED_EVIDENCE CAPABILITY_COMPACT_FACT_PARENT_USED_CHILD_PATHS CAPABILITY_COMPACT_FACT_PARENT_USED_RISK_BLOCKER CAPABILITY_COMPACT_FACT_PARENT_USED_HOLD_DECISION CAPABILITY_COMPACT_FACT_PARENT_USED_UNKNOWN CAPABILITY_COMPACT_FACT_PARENT_USED_VERIFICATION CAPABILITY_COMPACT_FACT_PARENT_USED_NEXT_ACTION");
    return;
  }
  streamText(res, model, "CAPABILITY_COMPACT_FACT_UNEXPECTED");
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

CAPABILITY_COMPACT_FACTS_PROVIDER_READY="$READY_FILE" \
CAPABILITY_COMPACT_FACTS_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="agent-capability-loop-compact-facts-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$WORKSPACE" \
  --resume "$SESSION_ID" \
  --max-turns 1 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "" \
  -p "CAPABILITY_LOOP_COMPACT_FACTS_PROMPT: synthesize only from compact-preserved capability loop hard facts. Do not modify files." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

verify_markers

node - "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const providerLog = process.argv[2];
const events = fs.readFileSync(providerLog, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!events.some(event => event.is_summary_request)) {
  console.error("provider never saw auto compact summary request");
  process.exit(1);
}
if (!events.some(event => event.has_compacted_capability_hard_facts)) {
  console.error("provider never saw compacted request with capability_loop hard facts");
  process.exit(1);
}
NODE

for marker in \
  CAPABILITY_COMPACT_FACT_PARENT_USED_EVIDENCE \
  CAPABILITY_COMPACT_FACT_PARENT_USED_CHILD_PATHS \
  CAPABILITY_COMPACT_FACT_PARENT_USED_RISK_BLOCKER \
  CAPABILITY_COMPACT_FACT_PARENT_USED_HOLD_DECISION \
  CAPABILITY_COMPACT_FACT_PARENT_USED_UNKNOWN \
  CAPABILITY_COMPACT_FACT_PARENT_USED_VERIFICATION \
  CAPABILITY_COMPACT_FACT_PARENT_USED_NEXT_ACTION
do
  if ! grep -q "$marker" "$CLI_LOG"; then
    echo "CLI log missing final answer marker $marker" >&2
    exit 1
  fi
done

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "workspace=$WORKSPACE"
echo "main_transcript=$main_transcript_path"
echo "subagent_transcript=$subagent_transcript_path"
echo "output_file=$output_file"
echo "provider_log=$PROVIDER_LOG"
