#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d%H%M%S)"
WORK_DIR="/tmp/niuma-closure-gate-${TIMESTAMP}"
DUMP_PATH="/tmp/niuma-closure-gate-${TIMESTAMP}.jsonl"
MODEL="closure-gate-stub"
PROMPT_PROFILE="claude-compatible"
FORCE=0

usage() {
  cat <<'EOF'
Usage:
  scripts/closure-gate-acceptance.sh [flags]

Runs a deterministic real CLI acceptance against a local OpenAI-compatible
stub provider. The scenario verifies that a premature final answer is blocked
first by Read Scope Gate, then by Final Claim Gate, and only accepted after the
model gathers Read evidence plus a successful verification command.

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

mkdir -p "$FIXTURE_DIR" "$CONFIG_DIR"
cat >"$FIXTURE_DIR/README.md" <<'EOF_README'
# Closure Gate Fixture

CLOSURE_GATE_READ_MARKER: this fixture proves the Read Scope Gate has real
file evidence before a summary is accepted.
EOF_README

git -C "$FIXTURE_DIR" init -q
git -C "$FIXTURE_DIR" config user.email closure-gate@example.invalid
git -C "$FIXTURE_DIR" config user.name "Closure Gate Acceptance"
git -C "$FIXTURE_DIR" add README.md
git -C "$FIXTURE_DIR" commit -q -m "initial fixture"

cat >"$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.CLOSURE_GATE_PROVIDER_READY || "";
const requestLog = process.env.CLOSURE_GATE_PROVIDER_LOG || "";

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
  const id = `chatcmpl-closure-gate-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "stop" }], usage: { prompt_tokens: 64, completion_tokens: Math.max(1, Math.ceil(text.length / 4)), total_tokens: 64 + Math.max(1, Math.ceil(text.length / 4)) } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, { "content-type": "text/event-stream; charset=utf-8", "cache-control": "no-cache", connection: "keep-alive" });
  const completionID = `chatcmpl-closure-gate-${Date.now()}`;
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
  writeSSE(res, { id: completionID, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }], usage: { prompt_tokens: 96, completion_tokens: 16, total_tokens: 112 } });
  res.write("data: [DONE]\n\n");
  res.end();
}

function userAndToolText(parsed) {
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
  const model = parsed.model || "closure-gate-stub";
  const all = JSON.stringify(parsed);
  const observed = userAndToolText(parsed);
  writeLog({
    has_read_scope_gate: all.includes("Read Scope Gate"),
    has_final_claim_gate: all.includes("Final Claim Gate"),
    has_read_marker: all.includes("CLOSURE_GATE_READ_MARKER"),
    has_diff_check_result: all.includes("CLOSURE_GATE_DIFF_CHECK_OK"),
  });

  if (observed.includes("CLOSURE_GATE_DIFF_CHECK_OK")) {
    streamText(res, model, "CLOSURE_GATE_FINAL_OK: README.md summary is based on CLOSURE_GATE_READ_MARKER, and checks passed after git diff --check.");
    return;
  }
  if (all.includes("Final Claim Gate")) {
    streamToolCall(res, model, "call_closure_diff_check", "Bash", { command: "git diff --check && printf CLOSURE_GATE_DIFF_CHECK_OK" });
    return;
  }
  if (observed.includes("CLOSURE_GATE_READ_MARKER")) {
    streamText(res, model, "README.md summary: CLOSURE_GATE_READ_MARKER. Checks passed.");
    return;
  }
  if (all.includes("Read Scope Gate")) {
    streamToolCall(res, model, "call_closure_read", "Read", { file_path: "README.md" });
    return;
  }
  streamText(res, model, "CLOSURE_GATE_PREMATURE: I read README.md and summarized it. Checks passed.");
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

CLOSURE_GATE_PROVIDER_READY="$READY_FILE" \
CLOSURE_GATE_PROVIDER_LOG="$PROVIDER_LOG" \
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
export ANTHROPIC_API_KEY="closure-gate-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH" \
go run ./cmd/niuma \
  --cwd "$FIXTURE_DIR" \
  --max-turns 5 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "Read,Bash" \
  --allowedTools "Bash(git diff --check*)" \
  -p "CLOSURE_GATE_MAIN_PROMPT: 读取 README.md 并总结，然后说明 checks 是否通过。不要修改文件。" >"$CLI_LOG" 2>&1

if [[ ! -s "$DUMP_PATH" ]]; then
  echo "prompt dump missing: $DUMP_PATH" >&2
  cat "$CLI_LOG" >&2 || true
  exit 1
fi

go run ./scripts/verify-code-mode-prompt-dump.go \
  --min-turns 5 \
  --require-final-tools-disabled=false \
  --require-final-turn-budget=false \
  --require-tool-result "Read,Bash" \
  --require-tool-no-persisted-output "Read,Bash" \
  --require-tool-max-bytes "Read=4000,Bash=4000" \
  --require-tool-use-input-text "Read=README.md,Bash=git diff --check" \
  --require-request-text "Read Scope Gate,Final Claim Gate,CLOSURE_GATE_READ_MARKER,CLOSURE_GATE_DIFF_CHECK_OK" \
  "$DUMP_PATH" >&2

node - "$DUMP_PATH" "$CLI_LOG" "$PROVIDER_LOG" <<'NODE'
const fs = require("fs");
const dumpPath = process.argv[2];
const cliLogPath = process.argv[3];
const providerLogPath = process.argv[4];
const records = fs.readFileSync(dumpPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
const text = records.map(record => JSON.stringify(record.request || record)).join("\n");
for (const marker of ["Read Scope Gate", "Final Claim Gate", "CLOSURE_GATE_READ_MARKER", "CLOSURE_GATE_DIFF_CHECK_OK"]) {
  if (!text.includes(marker)) {
    console.error(`prompt dump missing ${marker}`);
    process.exit(1);
  }
}
const cliLog = fs.readFileSync(cliLogPath, "utf8");
if (cliLog.includes("CLOSURE_GATE_PREMATURE")) {
  console.error("premature blocked final leaked to CLI output");
  process.exit(1);
}
if (!cliLog.includes("CLOSURE_GATE_FINAL_OK")) {
  console.error("CLI output missing accepted final marker");
  process.exit(1);
}
const providerLog = fs.readFileSync(providerLogPath, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
if (!providerLog.some(record => record.has_read_scope_gate) || !providerLog.some(record => record.has_final_claim_gate)) {
  console.error("provider did not observe both gate reminders");
  process.exit(1);
}
NODE

echo "ok=true"
echo "dump=$DUMP_PATH"
echo "work_dir=$WORK_DIR"
echo "workspace=$FIXTURE_DIR"
echo "provider_log=$PROVIDER_LOG"
