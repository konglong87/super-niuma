#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/upstream-guidance-source-${TIMESTAMP}"
FORCE="false"
MODEL="claude-sonnet-4-6"

usage() {
  cat <<'USAGE'
Usage:
  scripts/upstream-workspace-guidance-source-capture.sh [flags]

Runs the original Claude Code CLI with an isolated HOME/CLAUDE_CONFIG_DIR and a
local Node fetch preload that records /v1/messages request bodies, then returns a
minimal stub SSE response. This captures upstream workspace guidance source
behavior without calling a real model.

Flags:
  --upstream-dir <path>  Original Claude Code source/extract dir. Default:
                         <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --work-dir <path>      Output/work directory. Default: /tmp/upstream-guidance-source-<timestamp>.
  --model <name>         Model passed to upstream CLI. Default: claude-sonnet-4-6.
  --force                Remove existing --work-dir first.
  -h, --help             Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --upstream-dir)
      UPSTREAM_DIR="${2:?missing value for --upstream-dir}"
      shift 2
      ;;
    --work-dir)
      WORK_DIR="${2:?missing value for --work-dir}"
      shift 2
      ;;
    --model)
      MODEL="${2:?missing value for --model}"
      shift 2
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

CLI_JS="$UPSTREAM_DIR/dist/cli.js"
if [[ ! -f "$CLI_JS" ]]; then
  echo "upstream CLI not found: $CLI_JS" >&2
  exit 2
fi

if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir already exists: $WORK_DIR (use --force or choose another --work-dir)" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" ]]; then
  rm -rf "$WORK_DIR"
fi
mkdir -p "$WORK_DIR"

PRELOAD="$WORK_DIR/capture-fetch.cjs"
REPORT_JSONL="$WORK_DIR/report.jsonl"
REPORT_JSON="$WORK_DIR/report.json"

cat > "$PRELOAD" <<'EOF_PRELOAD'
const fs = require("fs");
const { ReadableStream } = require("stream/web");

const capturePath = process.env.UPSTREAM_CAPTURE_JSONL;
if (!capturePath) {
  throw new Error("UPSTREAM_CAPTURE_JSONL is required");
}

let call = 0;
const encoder = new TextEncoder();

function sseStream() {
  const events = [
    {
      type: "message_start",
      message: {
        id: "msg_stub",
        type: "message",
        role: "assistant",
        model: "claude-sonnet-4-6",
        content: [],
        stop_reason: null,
        stop_sequence: null,
        usage: { input_tokens: 1, output_tokens: 1 },
      },
    },
    { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } },
    { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "UPSTREAM_STUB_OK" } },
    { type: "content_block_stop", index: 0 },
    {
      type: "message_delta",
      delta: { stop_reason: "end_turn", stop_sequence: null },
      usage: { output_tokens: 1 },
    },
    { type: "message_stop" },
  ];
  const chunks = events.map((event) => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

globalThis.fetch = async function captureFetch(input, init = {}) {
  const url = typeof input === "string" ? input : input && input.url ? input.url : String(input);
  const method = (init.method || "GET").toUpperCase();
  if (url.includes("/v1/messages") && method === "POST") {
    let body = init.body;
    if (body && typeof body !== "string") {
      body = Buffer.from(body).toString("utf8");
    }
    const parsed = body ? JSON.parse(body) : null;
    fs.appendFileSync(
      capturePath,
      JSON.stringify({
        timestamp: new Date().toISOString(),
        call: ++call,
        url,
        method,
        body: parsed,
      }) + "\n",
    );
    return new Response(sseStream(), {
      status: 200,
      headers: {
        "content-type": "text/event-stream",
        "request-id": `req_stub_${call}`,
      },
    });
  }
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
};
EOF_PRELOAD

request_text() {
  local capture="$1"
  jq -r '
    [
      .body.system[]?.text,
      (.body.messages[]?.content[]? | select(.type == "text") | .text)
    ] | join("\n")
  ' "$capture"
}

check_contains() {
  local text_file="$1"
  local want="$2"
  if ! grep -Fq -- "$want" "$text_file"; then
    echo "missing required upstream request text: $want" >&2
    return 1
  fi
}

check_absent() {
  local text_file="$1"
  local forbidden="$2"
  if grep -Fq -- "$forbidden" "$text_file"; then
    echo "upstream request contained forbidden text: $forbidden" >&2
    return 1
  fi
}

run_case() {
  local name="$1"
  local required_csv="$2"
  local forbidden_csv="$3"
  local case_dir="$WORK_DIR/$name"
  local home="$case_dir/home"
  local config="$case_dir/config"
  local workspace="$case_dir/workspace"
  local capture="$case_dir/capture.jsonl"
  local log="$case_dir/run.log"
  local err="$case_dir/run.err"
  local text="$case_dir/request.txt"
  mkdir -p "$home" "$config" "$workspace"
  printf 'USER_GUIDANCE_MARKER\n' > "$config/CLAUDE.md"

  case "$name" in
    user_only)
      ;;
    project_claude_preempts_agents)
      printf 'PROJECT_CLAUDE_MARKER\n' > "$workspace/CLAUDE.md"
      printf 'PROJECT_AGENTS_SHOULD_NOT_LOAD\n' > "$workspace/AGENTS.md"
      ;;
    project_agents_fallback)
      printf 'PROJECT_AGENTS_MARKER\n' > "$workspace/AGENTS.md"
      ;;
    project_and_dot_claude_preempt_agents)
      printf 'PROJECT_CLAUDE_MARKER\n' > "$workspace/CLAUDE.md"
      mkdir -p "$workspace/.claude"
      printf 'PROJECT_DOT_CLAUDE_MARKER\n' > "$workspace/.claude/CLAUDE.md"
      printf 'PROJECT_AGENTS_SHOULD_NOT_LOAD\n' > "$workspace/AGENTS.md"
      ;;
    *)
      echo "unknown case: $name" >&2
      return 2
      ;;
  esac

  echo "running upstream case: $name" >&2
  local run_status=0
  (
    cd "$workspace"
    export HOME="$home"
    export CLAUDE_CONFIG_DIR="$config"
    export ANTHROPIC_API_KEY="dummy"
    export UPSTREAM_CAPTURE_JSONL="$capture"
    export NODE_OPTIONS="--require $PRELOAD"
    node "$CLI_JS" -p "只回答一句话：UPSTREAM_GUIDANCE_SOURCE_CHECK。不要调用工具。" \
      --model "$MODEL" \
      --tools Read \
      --session-id "33333333-3333-4333-8333-333333333333" \
      --no-session-persistence \
      --permission-mode bypassPermissions
  ) >"$log" 2>"$err" || run_status=$?

  if [[ ! -s "$capture" ]]; then
    echo "upstream capture missing for $name; run_status=$run_status log=$log err=$err" >&2
    sed -n '1,160p' "$log" >&2 || true
    sed -n '1,160p' "$err" >&2 || true
    return 1
  fi

  request_text "$capture" > "$text"
  IFS=',' read -ra required <<<"$required_csv"
  for item in "${required[@]}"; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    [[ -z "$item" ]] && continue
    check_contains "$text" "$item"
  done
  IFS=',' read -ra forbidden <<<"$forbidden_csv"
  for item in "${forbidden[@]}"; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    [[ -z "$item" ]] && continue
    check_absent "$text" "$item"
  done

  jq -c --arg name "$name" --arg capture "$capture" --arg log "$log" --arg err "$err" --argjson run_status "$run_status" '
    ([.body.system[]?.text] | join("\n")) as $system_text |
    ([.body.messages[]?.content[]? | select(.type == "text") | .text] | join("\n")) as $user_text |
    ($system_text + "\n" + $user_text) as $all_text |
    {
      case: $name,
      ok: true,
      run_status: $run_status,
      capture: $capture,
      log: $log,
      err: $err,
      system_block_count: (.body.system | length),
      system_bytes: ($system_text | length),
      user_text_bytes: ($user_text | length),
      contains: {
        user_guidance: ($all_text | contains("USER_GUIDANCE_MARKER")),
        project_claude: ($all_text | contains("PROJECT_CLAUDE_MARKER")),
        project_agents: ($all_text | contains("PROJECT_AGENTS_MARKER")),
        project_agents_forbidden: ($all_text | contains("PROJECT_AGENTS_SHOULD_NOT_LOAD")),
        project_dot_claude: ($all_text | contains("PROJECT_DOT_CLAUDE_MARKER"))
      }
    }
  ' "$capture" >> "$REPORT_JSONL"
}

run_case "user_only" \
  "USER_GUIDANCE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_CLAUDE_MARKER,PROJECT_AGENTS_MARKER,PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_DOT_CLAUDE_MARKER"

run_case "project_claude_preempts_agents" \
  "USER_GUIDANCE_MARKER,PROJECT_CLAUDE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_AGENTS_MARKER,PROJECT_DOT_CLAUDE_MARKER"

run_case "project_agents_fallback" \
  "USER_GUIDANCE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_AGENTS_MARKER,PROJECT_CLAUDE_MARKER,PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_DOT_CLAUDE_MARKER"

run_case "project_and_dot_claude_preempt_agents" \
  "USER_GUIDANCE_MARKER,PROJECT_CLAUDE_MARKER,PROJECT_DOT_CLAUDE_MARKER,# claudeMd,# currentDate" \
  "PROJECT_AGENTS_SHOULD_NOT_LOAD,PROJECT_AGENTS_MARKER"

jq -s '{ok: (all(.[]; .ok == true)), cases: .}' "$REPORT_JSONL" > "$REPORT_JSON"
cat "$REPORT_JSON"
echo "report: $REPORT_JSON" >&2
