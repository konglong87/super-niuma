#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

CWD="$ROOT_DIR"
WORK_DIR="/tmp/niuma-failed-agent-resume-${TIMESTAMP}"
FIRST_DUMP="/tmp/niuma-failed-agent-resume-first-${TIMESTAMP}.jsonl"
SECOND_DUMP="/tmp/niuma-failed-agent-resume-second-${TIMESTAMP}.jsonl"
THIRD_DUMP="/tmp/niuma-failed-agent-resume-third-${TIMESTAMP}.jsonl"
MODEL="failed-agent-resume-stub"
PROMPT_PROFILE="claude-compatible"
FORCE="false"
VERIFY_ONLY="false"

usage() {
  cat <<'USAGE'
Usage:
  scripts/failed-agent-resume-acceptance.sh [flags]

Runs a deterministic OpenAI-compatible stub provider to verify that a failed
background Agent task is restored as failed across CLI --resume and is visible
in the next model request.

Flags:
  --cwd <path>             Workspace cwd. Default: repo root.
  --work-dir <path>        Temp work dir for isolated config/provider logs.
  --first-dump <path>      First-run prompt dump path.
  --second-dump <path>     Resume-run prompt dump path.
  --third-dump <path>      Resume-after-AgentGet prompt dump path.
  --model <name>           Model name sent to niuma.
  --prompt-profile <name>  Prompt profile. Default: claude-compatible.
  --verify-only            Verify existing --second-dump and --third-dump without running the provider.
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
    --first-dump)
      FIRST_DUMP="${2:?missing value for --first-dump}"
      shift 2
      ;;
    --second-dump)
      SECOND_DUMP="${2:?missing value for --second-dump}"
      shift 2
      ;;
    --third-dump)
      THIRD_DUMP="${2:?missing value for --third-dump}"
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

verify_second_dump() {
  if [[ ! -s "$SECOND_DUMP" ]]; then
    echo "resume prompt dump missing: $SECOND_DUMP" >&2
    exit 1
  fi

  go run ./scripts/verify-code-mode-prompt-dump.go \
    --min-turns 2 \
    --require-final-tools-disabled=false \
    --require-final-turn-budget=false \
    --require-subagent-turn-budget-tools-disabled=false \
    --require-tool-result "AgentGet" \
    --require-request-text "## Background agent tasks,failed,failure notification: call AgentGet before explaining failure details,FAILED_AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER,FAILED_AGENT_RESUME_PARTIAL_EVIDENCE,FAILED_AGENT_RESUME_NEXT_ACTION" \
    "$SECOND_DUMP" >&2

  node -e '
const fs = require("fs");
const dump = process.argv[1];
let found = false;
for (const line of fs.readFileSync(dump, "utf8").trim().split(/\n+/)) {
  if (!line) continue;
  const record = JSON.parse(line);
  for (const message of record.request?.messages || []) {
    for (const block of message.content || []) {
      if (block.type !== "tool_result" || block.tool_use_id !== "call_agent_get_failed_resume") continue;
      const content = typeof block.content === "string" ? JSON.parse(block.content) : block.content;
      if (content?.task?.status === "failed" && content?.result?.status === "failed") {
        found = true;
      }
    }
  }
}
if (!found) {
  console.error("AgentGet failed task/result status not found in resume dump");
  process.exit(1);
}
const raw = fs.readFileSync(dump, "utf8");
if (raw.includes("#1 general-purpose completed: failed resume probe")) {
  console.error("resume runtime status misclassified failed task as completed");
  process.exit(1);
}
const records = fs.readFileSync(dump, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
const finalRequest = JSON.stringify(records[records.length - 1]?.request || {});
if (finalRequest.includes("failure notification: call AgentGet before explaining failure details")) {
  console.error("final resume request repeated acknowledged failed task notification");
  process.exit(1);
}
' "$SECOND_DUMP"
}

verify_third_dump() {
  if [[ ! -s "$THIRD_DUMP" ]]; then
    echo "post-AgentGet resume prompt dump missing: $THIRD_DUMP" >&2
    exit 1
  fi

  node -e '
const fs = require("fs");
const dump = process.argv[1];
const records = fs.readFileSync(dump, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
const firstRequest = JSON.stringify(records[0]?.request || {});
if (firstRequest.includes("failure notification: call AgentGet before explaining failure details")) {
  console.error("post-AgentGet resume repeated acknowledged failed task notification");
  process.exit(1);
}
if (firstRequest.includes("#1 general-purpose failed: failed resume probe; failure notification")) {
  console.error("post-AgentGet resume repeated failed task notification line");
  process.exit(1);
}
for (const want of [
  "## Recent agent evidence decision context",
  "These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs",
  "FAILED_AGENT_RESUME_PARTIAL_EVIDENCE",
  "FAILED_AGENT_RESUME_UNKNOWN",
  "FAILED_AGENT_RESUME_VERIFICATION",
  "FAILED_AGENT_RESUME_RISK",
  "FAILED_AGENT_RESUME_NEXT_ACTION"
]) {
  if (!firstRequest.includes(want)) {
    console.error(`post-AgentGet resume missing recent evidence context: ${want}`);
    process.exit(1);
  }
}
' "$THIRD_DUMP"
}

if [[ "$VERIFY_ONLY" == "true" ]]; then
  verify_second_dump
  verify_third_dump
  echo "ok=true"
  echo "second_dump=$SECOND_DUMP"
  echo "third_dump=$THIRD_DUMP"
  exit 0
fi

if [[ "$FORCE" == "true" ]]; then
  rm -rf "$WORK_DIR" "$FIRST_DUMP" "$SECOND_DUMP" "$THIRD_DUMP"
fi
if [[ -e "$WORK_DIR" || -e "$FIRST_DUMP" || -e "$SECOND_DUMP" || -e "$THIRD_DUMP" ]]; then
  echo "work or dump path already exists; use --force or choose another path" >&2
  exit 2
fi

mkdir -p "$WORK_DIR"
READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
CLI_FIRST_LOG="$WORK_DIR/first-run.log"
CLI_SECOND_LOG="$WORK_DIR/second-run.log"
CLI_THIRD_LOG="$WORK_DIR/third-run.log"
SESSION_ID="11111111-2222-4333-8444-555555555555"
CONFIG_DIR="$WORK_DIR/config"

provider_pid=""
cleanup() {
  if [[ -n "$provider_pid" ]]; then
    kill "$provider_pid" >/dev/null 2>&1 || true
    wait "$provider_pid" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

FAILED_AGENT_RESUME_PROVIDER_READY="$READY_FILE" \
FAILED_AGENT_RESUME_PROVIDER_LOG="$PROVIDER_LOG" \
node "$ROOT_DIR/scripts/failed-agent-resume-provider.mjs" --port 0 >"$WORK_DIR/provider.log" 2>&1 &
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
BASE_URL="$(node -e 'const fs=require("fs"); const path=process.argv[1]; process.stdout.write(JSON.parse(fs.readFileSync(path, "utf8")).base_url)' "$READY_FILE")"

export NIUMA_CONFIG_DIR="$CONFIG_DIR"
export NIUMA_PROVIDER="custom"
export ANTHROPIC_BASE_URL="$BASE_URL/v1"
export ANTHROPIC_API_KEY="failed-agent-resume-test-key"
export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
export NIUMA_DUMP_PROMPT_FULL="true"

FIRST_PROMPT="FAILED_AGENT_RESUME_FIRST_PROMPT: call AgentCreate once with the delegated failure prompt. After AgentCreate returns, answer briefly. Do not call AgentGet in this first run."
SECOND_PROMPT="FAILED_AGENT_RESUME_RESUME_PROMPT: inspect resumed background agent status. If there is a failed task notification, call AgentGet for that task before answering."

echo "running first CLI turn; dump=$FIRST_DUMP" >&2
NIUMA_DUMP_PROMPT_JSON="$FIRST_DUMP" \
go run ./cmd/niuma \
  --cwd "$CWD" \
  --session-id "$SESSION_ID" \
  --max-turns 2 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentCreate,AgentGet" \
  -p "$FIRST_PROMPT" >"$CLI_FIRST_LOG" 2>&1

TRANSCRIPT_PATH="$(find "$CONFIG_DIR/projects" -name "${SESSION_ID}.jsonl" -print -quit)"
if [[ -z "$TRANSCRIPT_PATH" || ! -s "$TRANSCRIPT_PATH" ]]; then
  echo "first transcript missing for session $SESSION_ID" >&2
  exit 1
fi

OUTPUT_FILE="$(node -e '
const fs = require("fs");
const path = process.argv[1];
for (const line of fs.readFileSync(path, "utf8").trim().split(/\n+/)) {
  if (!line) continue;
  const entry = JSON.parse(line);
  if (entry.type === "tool_result" && entry.tool_name === "AgentCreate") {
    const content = JSON.parse(entry.content);
    if (content.output_file) {
      process.stdout.write(content.output_file);
      process.exit(0);
    }
  }
}
process.exit(1);
' "$TRANSCRIPT_PATH")"

STATE_FILE="${OUTPUT_FILE}.state.json"
for _ in {1..100}; do
  if [[ -s "$OUTPUT_FILE" ]] && grep -q "FAILED_AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER" "$OUTPUT_FILE" \
    && [[ -s "$STATE_FILE" ]] && grep -q '"status":"failed"' "$STATE_FILE"; then
    break
  fi
  sleep 0.05
done
if [[ ! -s "$OUTPUT_FILE" ]] || ! grep -q "FAILED_AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER" "$OUTPUT_FILE" \
  || [[ ! -s "$STATE_FILE" ]] || ! grep -q '"status":"failed"' "$STATE_FILE"; then
  echo "background agent failure output/state was not written: $OUTPUT_FILE" >&2
  cat "$CLI_FIRST_LOG" >&2 || true
  cat "$WORK_DIR/provider.log" >&2 || true
  exit 1
fi

echo "running resume CLI turn; dump=$SECOND_DUMP" >&2
NIUMA_DUMP_PROMPT_JSON="$SECOND_DUMP" \
go run ./cmd/niuma \
  --cwd "$CWD" \
  --resume "$SESSION_ID" \
  --max-turns 3 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentGet" \
  -p "$SECOND_PROMPT" >"$CLI_SECOND_LOG" 2>&1

verify_second_dump

echo "running post-AgentGet resume CLI turn; dump=$THIRD_DUMP" >&2
NIUMA_DUMP_PROMPT_JSON="$THIRD_DUMP" \
go run ./cmd/niuma \
  --cwd "$CWD" \
  --resume latest \
  --max-turns 1 \
  --max-tokens 1024 \
  --model "$MODEL" \
  --tools "AgentGet" \
  -p "FAILED_AGENT_RESUME_POST_AGENTGET_PROMPT: this run resumes after AgentGet was already called. Do not call tools; answer ok." >"$CLI_THIRD_LOG" 2>&1

verify_third_dump

echo "ok=true"
echo "first_dump=$FIRST_DUMP"
echo "second_dump=$SECOND_DUMP"
echo "third_dump=$THIRD_DUMP"
echo "transcript=$TRANSCRIPT_PATH"
echo "output_file=$OUTPUT_FILE"
echo "state_file=$STATE_FILE"
echo "provider_log=$PROVIDER_LOG"
