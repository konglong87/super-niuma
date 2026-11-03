#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"
WORK_DIR="/tmp/niuma-final-diagnostic-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-final-diagnostic-${TIMESTAMP}.jsonl"
MODEL="final-diagnostic-stub"
PROMPT_PROFILE="claude-compatible"
FORCE=0

usage() {
  cat <<'EOF'
Usage:
  scripts/final-diagnostic-prompt-acceptance.sh [flags]

Runs a deterministic read-only code-mode prompt dump acceptance that covers
runtime todo/plan context, tool_result carry-forward, background AgentCreate,
sub-agent prompt dumping, background task status, and AgentGet result summary.

Flags:
  --work-dir <dir>        Temp work dir for isolated fixture/config/provider logs.
  --dump <path>           Prompt dump JSONL path.
  --model <name>          Model name sent to the stub provider.
  --prompt-profile <name> NIUMA_PROMPT_PROFILE value.
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
    --model)
      MODEL="$2"
      shift 2
      ;;
    --prompt-profile)
      PROMPT_PROFILE="$2"
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

mkdir -p "$FIXTURE_DIR/.claude" "$FIXTURE_DIR/internal/query" "$CONFIG_DIR"
cat >"$FIXTURE_DIR/README.md" <<'EOF'
# Final Diagnostic Fixture

FINAL_DIAGNOSTIC_READ_MARKER: this read-only fixture proves Read tool_result
content is carried into the next model request without modifying the repository.
EOF
cat >"$FIXTURE_DIR/internal/query/query.go" <<'EOF'
package query

// FINAL_DIAGNOSTIC_QUERY_MARKER proves the fixture can look like a small repo.
func RuntimeStatusFixture() string { return "runtime status fixture" }
EOF
cat >"$FIXTURE_DIR/.claude/todos.json" <<'EOF'
[
  {
    "id": "todo-final-1",
    "content": "Analyze prompt context parity",
    "activeForm": "Analyzing prompt context parity",
    "status": "in_progress",
    "priority": "high"
  },
  {
    "id": "todo-final-2",
    "content": "Collect final diagnostic dump evidence",
    "status": "pending",
    "priority": "medium"
  },
  {
    "id": "todo-final-3",
    "content": "Old completed diagnostic item",
    "status": "completed",
    "priority": "low"
  }
]
EOF
cat >"$FIXTURE_DIR/.claude/plan_mode.json" <<'EOF'
{
  "active": true,
  "plan": "Keep final diagnostic acceptance read-only while verifying runtime context, tool_result, background agent, and AgentGet evidence."
}
EOF

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.FINAL_DIAGNOSTIC_PROVIDER_READY || "";
const requestLog = process.env.FINAL_DIAGNOSTIC_PROVIDER_LOG || "";

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
  const id = `chatcmpl-final-diagnostic-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 64, completion_tokens: text.length, total_tokens: text.length + 64 },
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
  const completionID = `chatcmpl-final-diagnostic-${Date.now()}`;
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
    usage: { prompt_tokens: 96, completion_tokens: 16, total_tokens: 112 },
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
  const model = parsed.model || "final-diagnostic-stub";
  const all = JSON.stringify(parsed);
  const users = userText(parsed);
  writeLog({
    is_subagent: users.includes("FINAL_DIAGNOSTIC_SUBAGENT_PROMPT"),
    has_active_todos: all.includes("## Active todos") && all.includes("Analyzing prompt context parity"),
    has_plan_mode: all.includes("## Plan mode") && all.includes("Keep final diagnostic acceptance read-only"),
    has_read_result: all.includes("FINAL_DIAGNOSTIC_READ_MARKER"),
    has_background_status: all.includes("## Background agent tasks"),
    has_agent_notification: all.includes("completion notification: result is ready"),
    has_agent_result: all.includes("FINAL_DIAGNOSTIC_AGENT_RESULT"),
  });

  if (users.includes("FINAL_DIAGNOSTIC_SUBAGENT_PROMPT")) {
    streamText(res, model, "FINAL_DIAGNOSTIC_AGENT_RESULT: subagent saw an isolated read-only task prompt.");
    return;
  }
  const hasAgentGetResult = users.includes("\"progress_summary\"")
    || (users.includes("\"task\"") && users.includes("\"result\"") && users.includes("FINAL_DIAGNOSTIC_AGENT_RESULT"));
  if (hasAgentGetResult) {
    streamText(res, model, "FINAL_DIAGNOSTIC_FINAL_OK: todos plan read result background agent and AgentGet were visible.");
    return;
  }
  if (all.includes("FINAL_DIAGNOSTIC_BASH_DONE")) {
    streamToolCall(res, model, "call_final_agent_get", "AgentGet", { task_id: 1 });
    return;
  }
  if (all.includes("task_id") && all.includes("output_file")) {
    streamToolCall(res, model, "call_final_bash_wait", "Bash", {
      command: "sleep 1 && printf FINAL_DIAGNOSTIC_BASH_DONE",
      description: "wait for final diagnostic background agent",
    });
    return;
  }
  if (all.includes("FINAL_DIAGNOSTIC_READ_MARKER")) {
    streamToolCall(res, model, "call_final_agent_create", "AgentCreate", {
      description: "FINAL_DIAGNOSTIC_AGENT_TASK",
      prompt: "FINAL_DIAGNOSTIC_SUBAGENT_PROMPT: inspect the delegated read-only context and answer with FINAL_DIAGNOSTIC_AGENT_RESULT.",
    });
    return;
  }
  streamToolCall(res, model, "call_final_read", "Read", { file_path: "README.md" });
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

FINAL_DIAGNOSTIC_PROVIDER_READY="$READY_FILE" \
FINAL_DIAGNOSTIC_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="final-diagnostic-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 5 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "Read,AgentCreate,AgentGet,Bash" \
  --allowedTools "Bash(sleep *)" \
  -p "FINAL_DIAGNOSTIC_MAIN_PROMPT: read-only analyze how TodoWrite runtime todos, plan/progress, tool_result, Task/Agent/SubAgent status enter model requests. Do not modify files." >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

go run ./scripts/verify-code-mode-prompt-dump.go \
  --min-turns 5 \
  --require-final-tools-disabled=false \
  --require-final-turn-budget=false \
  --require-subagent-turn-budget-tools-disabled=false \
  --require-subagent-record=true \
  --require-tool-result "Read,AgentCreate,Bash,AgentGet" \
  --require-tool-no-persisted-output "Read,AgentCreate,Bash,AgentGet" \
  --require-tool-max-bytes "Read=4000,AgentCreate=4000,Bash=4000,AgentGet=8000" \
  --require-tool-use-input-text "Read=README.md,AgentCreate=FINAL_DIAGNOSTIC_SUBAGENT_PROMPT,AgentGet=1,Bash=FINAL_DIAGNOSTIC_BASH_DONE" \
  --require-request-text "## Active todos,Analyzing prompt context parity,completed: 1 hidden,## Plan mode,Keep final diagnostic acceptance read-only,FINAL_DIAGNOSTIC_READ_MARKER,## Background agent tasks,completion notification: result is ready,call AgentGet before using findings,output_file,FINAL_DIAGNOSTIC_AGENT_RESULT" \
  --forbid-request-text "\"name\":\"TodoWrite\",\"name\":\"EnterPlanMode\",\"name\":\"ExitPlanMode\"" \
  "$DUMP_PATH" >&2

node - "$DUMP_PATH" <<'NODE'
const fs = require("fs");
const records = fs.readFileSync(process.argv[2], "utf8").trim().split(/\n+/).map(line => JSON.parse(line));
const mainRecords = records.filter(record => !record.scope || record.scope === "main");
const final = mainRecords[mainRecords.length - 1];
const finalText = JSON.stringify(final.request || {});
if (finalText.includes("completion notification: result is ready")) {
  console.error("final request repeated an acknowledged completed background-task notification");
  process.exit(1);
}
if (!records.some(record => record.scope === "subagent")) {
  console.error("missing subagent-scoped prompt dump record");
  process.exit(1);
}
if (!records.some(record => (record.runtime_status?.sections || []).includes("active_todos") && (record.runtime_status?.sections || []).includes("plan_mode"))) {
  console.error("missing runtime status sections for active_todos and plan_mode");
  process.exit(1);
}
NODE

if ! grep -q "FINAL_DIAGNOSTIC_FINAL_OK" "$CLI_LOG"; then
  echo "CLI log missing final answer marker FINAL_DIAGNOSTIC_FINAL_OK" >&2
  exit 1
fi

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "workspace=$FIXTURE_DIR"
echo "provider_log=$PROVIDER_LOG"
