#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


TIMESTAMP="$(date +%Y%m%d%H%M%S)"
WORK_DIR="/tmp/niuma-agent-message-resume-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-agent-message-resume-${TIMESTAMP}.jsonl"
MODEL="agent-message-resume-stub"
PROMPT_PROFILE="claude-compatible"
CWD="$(pwd)"
FORCE=0
WORKTREE_RESUME=0

usage() {
  cat <<'EOF'
Usage:
  scripts/agent-message-resume-acceptance.sh [flags]

Builds a resume transcript with a terminal AgentCreate result and a readable
sub-agent transcript. The model is forced to call AgentMessage. The gate proves
AgentMessage starts a resumed successor agent and sends transcript history plus
the new message into the sub-agent provider request.

Flags:
  --work-dir <dir>
  --dump <path>
  --model <name>
  --prompt-profile <name>
  --cwd <path>
  --worktree-resume  Include a retained worktree path in terminal task state and
                     require the resumed successor request to use it as cwd.
  --force
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
    --model)
      MODEL="$2"
      shift 2
      ;;
    --prompt-profile)
      PROMPT_PROFILE="$2"
      shift 2
      ;;
    --cwd)
      CWD="$2"
      shift 2
      ;;
    --worktree-resume)
      WORKTREE_RESUME=1
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

if [[ -e "$DUMP_PATH" && "$FORCE" != "1" ]]; then
  echo "dump exists, pass --force: $DUMP_PATH" >&2
  exit 1
fi

rm -rf "$WORK_DIR"
mkdir -p "$WORK_DIR"
rm -f "$DUMP_PATH"

READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
SESSION_ID="56565656-5656-4656-8656-565656565656"
SUBSESSION_ID="67676767-6767-4676-8676-676767676767"
CONFIG_DIR="$WORK_DIR/config"

abs_cwd="$(cd "$CWD" && pwd -P)"
slug="${abs_cwd#/}"
slug="${slug//\//-}"
slug="${slug//:/}"
slug="${slug// /-}"
transcript_dir="$CONFIG_DIR/projects/$slug"
main_transcript_path="$transcript_dir/$SESSION_ID.jsonl"
subagent_transcript_path="$transcript_dir/$SUBSESSION_ID.jsonl"
output_file="$transcript_dir/resumable-agent.output"
retained_worktree="$WORK_DIR/retained-worktree"
mkdir -p "$transcript_dir"
if [[ "$WORKTREE_RESUME" == "1" ]]; then
  mkdir -p "$retained_worktree"
fi
printf 'AGENT_MESSAGE_RESUME_COMPLETED_MARKER\n' > "$output_file"
node - "$output_file.state.json" "$output_file" "$subagent_transcript_path" "$SUBSESSION_ID" "$retained_worktree" "$WORKTREE_RESUME" <<'NODE'
const fs = require("fs");
const [statePath, outputFile, transcriptPath, sessionID, retainedWorktree, worktreeResume] = process.argv.slice(2);
const result = {
  content: "AGENT_MESSAGE_RESUME_COMPLETED_MARKER",
  status: "completed",
  output_file: outputFile,
  transcript_path: transcriptPath,
  session_id: sessionID,
};
if (worktreeResume === "1") {
  result.worktree_path = retainedWorktree;
  result.worktree_branch = "worktree-agent-message-resume";
}
fs.writeFileSync(statePath, JSON.stringify({ status: "completed", result }) + "\n");
NODE

node - "$main_transcript_path" "$subagent_transcript_path" "$output_file" "$SUBSESSION_ID" <<'NODE'
const fs = require("fs");
const mainTranscript = process.argv[2];
const subagentTranscript = process.argv[3];
const outputFile = process.argv[4];
const subSessionID = process.argv[5];
const mainEntries = [
  { type: "message", role: "user", content: "create an agent that completed before resume" },
  { type: "tool_call", tool_id: "toolu_resumable_agent_create", tool_name: "AgentCreate", content: "{\"prompt\":\"collect evidence\",\"description\":\"resumable message probe\"}" },
  { type: "tool_result", tool_id: "toolu_resumable_agent_create", tool_name: "AgentCreate", content: JSON.stringify({ agent_name: "general-purpose", model: "model", output_file: outputFile, session_id: subSessionID, status: "running", task_id: 1 }) }
];
const subEntries = [
  { type: "message", role: "user", content: "original brief" },
  { type: "message", role: "assistant", content: "previous finding" },
  { type: "tool_call", tool_id: "toolu_resume_large", tool_name: "Echo", content: "{\"text\":\"large\"}" },
  { type: "tool_result", tool_id: "toolu_resume_large", tool_name: "Echo", content: "RAW_FULL_TOOL_RESULT_MARKER_" + "R".repeat(9000) },
  { type: "content_replacement", replacements: [{ kind: "tool-result", tool_use_id: "toolu_resume_large", replacement: "<persisted-output>\nresume replacement preview\n</persisted-output>" }] }
];
fs.writeFileSync(mainTranscript, mainEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
fs.writeFileSync(subagentTranscript, subEntries.map(entry => JSON.stringify(entry)).join("\n") + "\n");
NODE

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.AGENT_MESSAGE_RESUME_PROVIDER_READY || "";
const requestLog = process.env.AGENT_MESSAGE_RESUME_PROVIDER_LOG || "";

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
  const id = `chatcmpl-agent-message-resume-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 32, completion_tokens: text.length, total_tokens: text.length + 32 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-agent-message-resume-${Date.now()}`;
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id, type: "function", function: { name, arguments: JSON.stringify(args) } }] } }] });
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 48, completion_tokens: 12, total_tokens: 60 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

async function handleChat(req, res) {
  const parsed = JSON.parse(await readBody(req));
  const model = parsed.model || "agent-message-resume-stub";
  const text = JSON.stringify(parsed);
  writeLog({
    has_terminal_status: text.includes("resumable message probe") && text.includes("completed"),
    has_resume_history: text.includes("previous finding") && text.includes("please continue with new evidence"),
    has_resumed_result: text.includes('"resumed": true'),
    has_running_only_error: text.includes("only send to running"),
  });
  if (text.includes("previous finding") && text.includes("Agent message from coordinator") && text.includes("please continue with new evidence")) {
    streamText(res, model, "RESUMED_AGENT_MESSAGE_DONE");
    return;
  }
  if (text.includes("AGENT_MESSAGE_RESUME_PROMPT") && text.includes("completion notification") && !text.includes("call_agent_message_resume")) {
    streamToolCall(res, model, "call_agent_message_resume", "AgentMessage", { task_id: 1, from_agent: "coordinator", content: "please continue with new evidence" });
    return;
  }
  streamText(res, model, "AGENT_MESSAGE_RESUME_FINAL_OK");
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

AGENT_MESSAGE_RESUME_PROVIDER_READY="$READY_FILE" \
AGENT_MESSAGE_RESUME_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="agent-message-resume-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$CWD" \
  --resume "$SESSION_ID" \
  --max-turns 2 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentMessage" \
  -p "AGENT_MESSAGE_RESUME_PROMPT: send AgentMessage to task 1 and continue the resumed sub-agent." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

go run ./scripts/verify-code-mode-prompt-dump.go \
  --min-turns 2 \
  --require-final-tools-disabled=false \
  --require-final-turn-budget=false \
  --require-subagent-turn-budget-tools-disabled=false \
  --require-no-tool-errors=false \
  --require-tool-result "AgentMessage" \
  --require-request-text "## Background agent tasks,completed,completion notification: result is ready,previous finding,<persisted-output>,resume replacement preview,Agent message from coordinator,please continue with new evidence,resumed_from_task_id,source_transcript" \
  --forbid-request-text "AgentMessage can only send to running sub-agent tasks,RAW_FULL_TOOL_RESULT_MARKER" \
  "$DUMP_PATH" >&2

if [[ "$WORKTREE_RESUME" == "1" ]]; then
  go run ./scripts/verify-code-mode-prompt-dump.go \
    --min-turns 2 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-no-tool-errors=false \
    --require-request-text "Current working directory: $retained_worktree,$retained_worktree,previous finding,Agent message from coordinator,worktree_resume,retained,worktree_path,worktree-agent-message-resume" \
    "$DUMP_PATH" >&2
fi

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "main_transcript=$main_transcript_path"
echo "subagent_transcript=$subagent_transcript_path"
echo "output_file=$output_file"
if [[ "$WORKTREE_RESUME" == "1" ]]; then
  echo "retained_worktree=$retained_worktree"
fi
echo "provider_log=$PROVIDER_LOG"
