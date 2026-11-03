#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"

CWD="$ROOT_DIR"
WORK_DIR="/tmp/niuma-agent-capability-loop-resume-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-capability-loop-resume-${TIMESTAMP}.jsonl"
MODEL="agent-capability-loop-resume-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/agent-capability-loop-resume-acceptance.sh [flags]

Builds a resume transcript whose background Agent completed before resume with
structured capability_loop evidence. The resumed first request must surface the
capability_loop in runtime status, then AgentGet must return the same structured
fields for the parent model to use.

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

VERIFY_ONLY="false"

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
    --require-no-tool-errors=false \
    --require-tool-result "AgentCreate,AgentGet" \
    --require-tool-no-persisted-output "AgentCreate,AgentGet" \
    --require-tool-max-bytes "AgentCreate=4000,AgentGet=12000" \
    --require-tool-use-input-text "AgentGet=1" \
    --require-request-text "## Background agent tasks,completion notification: result is ready,call AgentGet before using findings,capability_loop,CAPABILITY_RESUME_EVIDENCE,CAPABILITY_RESUME_UNKNOWN,CAPABILITY_RESUME_VERIFICATION,CAPABILITY_RESUME_NEXT_ACTION,\"capability_loop\"" \
    --forbid-request-text "CAPABILITY_RESUME_RAW_SHOULD_NOT_APPEAR" \
    "$DUMP_PATH"
}

verify_markers() {
  node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).map(line => JSON.parse(line));
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const text = mainRecords.map(record => JSON.stringify(record.request || {})).join("\n");
for (const marker of [
  "CAPABILITY_RESUME_EVIDENCE",
  "CAPABILITY_RESUME_ASSUMPTION",
  "CAPABILITY_RESUME_UNKNOWN",
  "CAPABILITY_RESUME_VERIFICATION",
  "CAPABILITY_RESUME_RISK",
  "CAPABILITY_RESUME_NEXT_ACTION",
]) {
  if (!text.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
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
SESSION_ID="78787878-7878-4878-8878-787878787878"
SUBSESSION_ID="89898989-8989-4898-8898-898989898989"
CONFIG_DIR="$WORK_DIR/config"

abs_cwd="$(cd "$CWD" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
main_transcript_path="$transcript_dir/$SESSION_ID.jsonl"
subagent_transcript_path="$transcript_dir/$SUBSESSION_ID.jsonl"
output_file="$transcript_dir/capability-loop-agent.output"
mkdir -p "$transcript_dir"

node - "$main_transcript_path" "$subagent_transcript_path" "$output_file" "$SUBSESSION_ID" <<'NODE'
const fs = require("fs");
const [mainTranscript, subagentTranscript, outputFile, subSessionID] = process.argv.slice(2);
const result = {
  content: "CAPABILITY_RESUME_SUMMARY: completed before resume with structured evidence.",
  status: "completed",
  agent_name: "general-purpose",
  model: "model",
  session_id: subSessionID,
  transcript_path: subagentTranscript,
  output_file: outputFile,
  turns: 2,
  task_id: 1,
  capability_loop: {
    evidence: ["CAPABILITY_RESUME_EVIDENCE: internal/cli/cli.go seedResumeAgentTasksFromEntries restores ResultJSON."],
    assumptions: ["CAPABILITY_RESUME_ASSUMPTION: output_file.state.json is readable during resume."],
    unknowns: ["CAPABILITY_RESUME_UNKNOWN: no production transcript was sampled in this deterministic gate."],
    verification: ["CAPABILITY_RESUME_VERIFICATION: scripts/agent-capability-loop-resume-acceptance.sh restored the task and called AgentGet."],
    risks: ["CAPABILITY_RESUME_RISK: field visibility alone does not prove open-task win rate."],
    next_action: "CAPABILITY_RESUME_NEXT_ACTION: parent should cite restored evidence before final synthesis."
  }
};
fs.writeFileSync(outputFile, result.content + "\n");
fs.writeFileSync(`${outputFile}.state.json`, JSON.stringify({ status: "completed", result }) + "\n");
const mainEntries = [
  { type: "message", role: "user", content: "create an agent that completes before resume" },
  { type: "tool_call", tool_id: "toolu_capability_resume_agent_create", tool_name: "AgentCreate", content: "{\"prompt\":\"collect capability evidence\",\"description\":\"capability resume probe\"}" },
  { type: "tool_result", tool_id: "toolu_capability_resume_agent_create", tool_name: "AgentCreate", content: JSON.stringify({ agent_name: "general-purpose", model: "model", output_file: outputFile, session_id: subSessionID, status: "running", task_id: 1 }) }
];
const subEntries = [
  { type: "message", role: "user", content: "collect capability evidence" },
  { type: "message", role: "assistant", content: result.content }
];
fs.writeFileSync(mainTranscript, mainEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
fs.writeFileSync(subagentTranscript, subEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.CAPABILITY_RESUME_PROVIDER_READY || "";
const requestLog = process.env.CAPABILITY_RESUME_PROVIDER_LOG || "";

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
  const id = `chatcmpl-agent-capability-resume-${Date.now()}`;
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
  const completionID = `chatcmpl-agent-capability-resume-${Date.now()}`;
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

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-capability-loop-resume-stub";
  const text = JSON.stringify(parsed);
  const users = userText(parsed);
  const hasRuntimeCapabilityLoop = text.includes("## Background agent tasks")
    && text.includes("completion notification")
    && text.includes("capability_loop")
    && text.includes("CAPABILITY_RESUME_EVIDENCE")
    && text.includes("CAPABILITY_RESUME_UNKNOWN")
    && text.includes("CAPABILITY_RESUME_VERIFICATION");
  const hasAgentGetCapabilityLoop = users.includes("\"capability_loop\"")
    && users.includes("CAPABILITY_RESUME_EVIDENCE")
    && users.includes("CAPABILITY_RESUME_ASSUMPTION")
    && users.includes("CAPABILITY_RESUME_UNKNOWN")
    && users.includes("CAPABILITY_RESUME_VERIFICATION")
    && users.includes("CAPABILITY_RESUME_RISK")
    && users.includes("CAPABILITY_RESUME_NEXT_ACTION");
  writeLog({
    has_runtime_capability_loop: hasRuntimeCapabilityLoop,
    has_agent_get_capability_loop: hasAgentGetCapabilityLoop,
  });

  if (hasAgentGetCapabilityLoop) {
    streamText(res, model, "CAPABILITY_RESUME_PARENT_USED_EVIDENCE CAPABILITY_RESUME_PARENT_USED_UNKNOWN CAPABILITY_RESUME_PARENT_USED_VERIFICATION CAPABILITY_RESUME_PARENT_NEXT_ACTION");
    return;
  }
  if (text.includes("CAPABILITY_LOOP_RESUME_PROMPT") && hasRuntimeCapabilityLoop && !text.includes("call_capability_resume_agent_get")) {
    streamToolCall(res, model, "call_capability_resume_agent_get", "AgentGet", { task_id: 1 });
    return;
  }
  streamText(res, model, "CAPABILITY_RESUME_UNEXPECTED");
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

CAPABILITY_RESUME_PROVIDER_READY="$READY_FILE" \
CAPABILITY_RESUME_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="agent-capability-loop-resume-test-key"
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
  -p "CAPABILITY_LOOP_RESUME_PROMPT: resume the completed sub-agent, call AgentGet, and synthesize only after seeing evidence, assumptions, unknowns, verification, risks, and next_action. Do not modify files." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

verify_dump >&2
verify_markers

node - "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const providerLog = process.argv[2];
const events = fs.readFileSync(providerLog, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!events.some(event => event.has_runtime_capability_loop)) {
  console.error("provider never saw resume runtime capability_loop");
  process.exit(1);
}
if (!events.some(event => event.has_agent_get_capability_loop)) {
  console.error("provider never saw AgentGet capability_loop after resume");
  process.exit(1);
}
NODE

for marker in \
  CAPABILITY_RESUME_PARENT_USED_EVIDENCE \
  CAPABILITY_RESUME_PARENT_USED_UNKNOWN \
  CAPABILITY_RESUME_PARENT_USED_VERIFICATION \
  CAPABILITY_RESUME_PARENT_NEXT_ACTION
do
  if ! grep -q "$marker" "$CLI_LOG"; then
    echo "CLI log missing final answer marker $marker" >&2
    exit 1
  fi
done

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "main_transcript=$main_transcript_path"
echo "subagent_transcript=$subagent_transcript_path"
echo "output_file=$output_file"
echo "provider_log=$PROVIDER_LOG"
