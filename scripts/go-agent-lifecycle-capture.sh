#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
TARGET_CWD="$ROOT_DIR"
WORK_DIR="/tmp/go-agent-lifecycle-${TIMESTAMP}"
DUMP_PATH="/tmp/go-agent-lifecycle-${TIMESTAMP}.jsonl"
MODEL="go-agent-lifecycle-stub"
PROMPT_PROFILE="claude-compatible-strict"
SCENARIO="completed"
FORCE="false"
AGENT_ISOLATION=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/go-agent-lifecycle-capture.sh [flags]

Runs niuma against a local OpenAI-compatible provider stub and captures the
request sequence for the Claude-compatible Agent tool with run_in_background=true.
The output report is intentionally shaped like upstream-agent-lifecycle-capture
so the two can be compared without relying on a real model.

Flags:
  --target-cwd <path>    Workspace where niuma runs. Default: repo root.
  --work-dir <path>      Output/work directory. Default: /tmp/go-agent-lifecycle-<timestamp>.
  --dump <path>          Prompt dump JSONL. Default: /tmp/go-agent-lifecycle-<timestamp>.jsonl.
  --model <name>         Model name sent to the stub provider. Default: go-agent-lifecycle-stub.
  --prompt-profile <name>
                         Prompt profile. Default: claude-compatible-strict.
  --scenario <name>      Capture scenario: completed, child-error, or cancelled. Default: completed.
  --agent-isolation <mode>
                         Optional Agent isolation mode to request. Currently useful for worktree.
  --force                Remove existing --work-dir/--dump first.
  -h, --help             Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-cwd)
      TARGET_CWD="${2:?missing value for --target-cwd}"
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
    --scenario)
      SCENARIO="${2:?missing value for --scenario}"
      shift 2
      ;;
    --agent-isolation)
      AGENT_ISOLATION="${2:?missing value for --agent-isolation}"
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

case "$SCENARIO" in
  completed|child-error|cancelled)
    ;;
  *)
    echo "unknown scenario: $SCENARIO (want completed, child-error, or cancelled)" >&2
    exit 2
    ;;
esac

if [[ ! -d "$TARGET_CWD" ]]; then
  echo "target cwd not found: $TARGET_CWD" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir already exists: $WORK_DIR (use --force or choose another --work-dir)" >&2
  exit 2
fi
if [[ -e "$DUMP_PATH" && "$FORCE" != "true" ]]; then
  echo "dump path already exists: $DUMP_PATH (use --force or choose another --dump)" >&2
  exit 2
fi
rm -rf "$WORK_DIR"
rm -f "$DUMP_PATH"
mkdir -p "$WORK_DIR/config"

READY_FILE="$WORK_DIR/provider-ready.json"
PROVIDER_LOG="$WORK_DIR/provider-requests.jsonl"
PROVIDER_SCRIPT="$WORK_DIR/provider.mjs"
CLI_LOG="$WORK_DIR/cli.log"
REPORT_JSON="$WORK_DIR/report.json"

cat > "$PROVIDER_SCRIPT" <<'NODE'
#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const readyFile = process.env.GO_AGENT_LIFECYCLE_PROVIDER_READY || "";
const requestLog = process.env.GO_AGENT_LIFECYCLE_PROVIDER_LOG || "";
const scenario = process.env.GO_AGENT_LIFECYCLE_SCENARIO || "completed";
const agentIsolation = process.env.GO_AGENT_LIFECYCLE_AGENT_ISOLATION || "";
if (!readyFile || !requestLog) {
  throw new Error("GO_AGENT_LIFECYCLE_PROVIDER_READY and GO_AGENT_LIFECYCLE_PROVIDER_LOG are required");
}

let call = 0;
let parentAgentIssued = false;
let childFinalIssued = false;
let parentGrepIssued = false;
let parentStopIssued = false;

function appendJson(path, value) {
  fs.appendFileSync(path, JSON.stringify(value) + "\n");
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
  const id = `chatcmpl-go-agent-lifecycle-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 48, completion_tokens: text.length, total_tokens: text.length + 48 },
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
  const completionID = `chatcmpl-go-agent-lifecycle-${Date.now()}`;
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

function streamHangingPartial(req, res, model) {
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  res.flushHeaders?.();
  const id = `chatcmpl-go-agent-lifecycle-hang-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: "GO_AGENT_CHILD_PARTIAL_MARKER: partial result before cancellation" } }] });
  res.flush?.();
  req.on("close", () => {
    appendJson(requestLog, {
      time: new Date().toISOString(),
      call,
      classification: "child_agent_hanging_closed",
    });
  });
}

function errorResponse(res, message) {
  res.writeHead(400, { "content-type": "application/json" });
  res.end(JSON.stringify({ error: { message, type: "invalid_request_error" } }));
}

function requestText(body) {
  const parts = [];
  for (const message of body.messages || []) {
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

function toolNames(body) {
  return (body.tools || []).map(tool => tool?.function?.name || tool?.name).filter(Boolean);
}

function classify(body) {
  const text = requestText(body);
  const all = JSON.stringify(body);
  const tools = toolNames(body);
  if (!parentAgentIssued && tools.includes("Agent") && text.includes("GO_AGENT_LIFECYCLE_PARENT_PROMPT")) {
    parentAgentIssued = true;
    return "parent_agent_tool_use";
  }
  if (text.includes("GO_AGENT_LIFECYCLE_CHILD_PROMPT") && scenario === "cancelled") {
    return "child_agent_hanging";
  }
  if (!childFinalIssued && text.includes("GO_AGENT_LIFECYCLE_CHILD_PROMPT")) {
    childFinalIssued = true;
    return scenario === "child-error" ? "child_agent_error" : "child_agent_final";
  }
  if (scenario === "cancelled" && !parentStopIssued && all.includes("task_id") && all.includes("output_file") && tools.includes("AgentStop")) {
    parentStopIssued = true;
    return "parent_agent_stop_tool_use";
  }
  if (scenario === "cancelled" && (text.includes("\"cancelled\": true") || all.includes("\"cancelled\": true"))) {
    return "parent_after_agent_stop_result";
  }
  if (!parentGrepIssued && all.includes("task_id") && all.includes("output_file")) {
    parentGrepIssued = true;
    return "parent_after_agent_tool_result";
  }
  if (all.includes("runtimeAgentTaskStatus") || all.includes("GO_AGENT_CHILD_DONE_MARKER")) {
    return "parent_after_followup_tool";
  }
  return "fallback_text";
}

async function handleChat(req, res) {
  const body = JSON.parse(await readBody(req));
  call += 1;
  const classification = classify(body);
  const text = requestText(body);
  const all = JSON.stringify(body);
  const tools = toolNames(body);
  appendJson(requestLog, {
    time: new Date().toISOString(),
    call,
    classification,
    tool_names: tools,
    message_count: (body.messages || []).length,
    contains: {
      parent_prompt: text.includes("GO_AGENT_LIFECYCLE_PARENT_PROMPT"),
      child_prompt: text.includes("GO_AGENT_LIFECYCLE_CHILD_PROMPT"),
      agent_tool_result_json: all.includes("task_id") && all.includes("background"),
      task_id: all.includes("task_id"),
      output_file: all.includes("output_file"),
      background_agent_tasks: all.includes("## Background agent tasks"),
      completion_notification: all.includes("completion notification: result is ready"),
      call_agent_get_instruction: all.includes("call AgentGet before using findings"),
      agent_get_tool_exposed: tools.includes("AgentGet"),
      child_done_marker: all.includes("GO_AGENT_CHILD_DONE_MARKER"),
      result_preview_done_marker: all.includes("result preview: GO_AGENT_CHILD_DONE_MARKER"),
      task_notification_xml: all.includes("<task-notification"),
      failed_status: all.includes("failed"),
      failed_marker: all.includes("GO_AGENT_CHILD_FAILED_MARKER"),
      result_preview_failed_marker: all.includes("result preview:") && all.includes("GO_AGENT_CHILD_FAILED_MARKER"),
      cancelled_status: all.includes("cancelled"),
      cancellation_notification: all.includes("cancellation notification"),
      partial_marker: all.includes("GO_AGENT_CHILD_PARTIAL_MARKER"),
      result_preview_partial_marker: all.includes("result preview:") && all.includes("GO_AGENT_CHILD_PARTIAL_MARKER"),
      capability_loop: all.includes("capability_loop"),
      worktree_notice: all.includes("isolated git worktree"),
      worktree_path: all.includes("worktree_path") || all.includes("worktreePath"),
    },
    body,
  });

  const model = body.model || "go-agent-lifecycle-stub";
  if (classification === "parent_agent_tool_use") {
    const args = {
      description: "Lifecycle evidence",
      prompt: scenario === "cancelled"
        ? "GO_AGENT_LIFECYCLE_CHILD_PROMPT: Read-only. Start with exactly GO_AGENT_CHILD_PARTIAL_MARKER and then keep waiting until cancelled."
        : "GO_AGENT_LIFECYCLE_CHILD_PROMPT: Read-only. Return exactly GO_AGENT_CHILD_DONE_MARKER and mention output_file discipline.",
      subagent_type: "general-purpose",
      run_in_background: true,
    };
    if (agentIsolation) {
      args.isolation = agentIsolation;
    }
    streamToolCall(res, model, "call_go_agent_lifecycle_1", "Agent", args);
    return;
  }
  if (classification === "child_agent_hanging") {
    streamHangingPartial(req, res, model);
    return;
  }
  if (classification === "child_agent_final") {
    streamText(res, model, "GO_AGENT_CHILD_DONE_MARKER: child result complete. Do not peek output_file unless asked.");
    return;
  }
  if (classification === "child_agent_error") {
    errorResponse(res, "GO_AGENT_CHILD_FAILED_MARKER: simulated child provider failure");
    return;
  }
  if (classification === "parent_agent_stop_tool_use") {
    const match = JSON.stringify(body).match(/"task_id"\s*:\s*([0-9]+)/);
    const taskID = match ? Number(match[1]) : 1;
    await new Promise(resolve => setTimeout(resolve, 100));
    streamToolCall(res, model, "call_go_agent_lifecycle_stop", "AgentStop", {
      task_id: taskID,
      reason: "cancel lifecycle evidence",
    });
    return;
  }
  if (classification === "parent_after_agent_stop_result") {
    streamText(res, model, "GO_AGENT_LIFECYCLE_PARENT_AFTER_AGENT_STOP_WAITING");
    return;
  }
  if (classification === "parent_after_agent_tool_result") {
    if (scenario === "child-error" || agentIsolation === "worktree") {
      await new Promise(resolve => setTimeout(resolve, 500));
    }
    streamToolCall(res, model, "call_go_agent_lifecycle_grep", "Grep", {
      pattern: "func \\(s \\*Session\\) runtimeAgentTaskStatus",
      path: "internal/query/query.go",
      output_mode: "content",
    });
    return;
  }
  streamText(res, model, "GO_AGENT_LIFECYCLE_PARENT_FINAL_AFTER_STATUS");
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
      res.end(JSON.stringify({ error: { message: String(err?.stack || err) } }));
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

GO_AGENT_LIFECYCLE_PROVIDER_READY="$READY_FILE" \
GO_AGENT_LIFECYCLE_PROVIDER_LOG="$PROVIDER_LOG" \
GO_AGENT_LIFECYCLE_SCENARIO="$SCENARIO" \
GO_AGENT_LIFECYCLE_AGENT_ISOLATION="$AGENT_ISOLATION" \
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

echo "running go Agent lifecycle capture..." >&2
RUN_STATUS=0
TOOL_LIST="Agent,Grep,Read"
if [[ "$SCENARIO" == "cancelled" ]]; then
  TOOL_LIST="Agent,AgentStop,Grep,Read"
fi
(
  cd "$ROOT_DIR"
  export NIUMA_CONFIG_DIR="$WORK_DIR/config"
  export NIUMA_PROVIDER="custom"
  export ANTHROPIC_BASE_URL="$BASE_URL/v1"
  export ANTHROPIC_API_KEY="go-agent-lifecycle-test-key"
  export NIUMA_PROMPT_PROFILE="$PROMPT_PROFILE"
  export NIUMA_DUMP_PROMPT_FULL="true"
  export NIUMA_DUMP_PROMPT_JSON="$DUMP_PATH"
  go run ./cmd/niuma \
    --cwd "$TARGET_CWD" \
    --max-turns 4 \
    --max-tokens 1024 \
    --model "$MODEL" \
    --tools "$TOOL_LIST" \
    --permission-mode bypassPermissions \
    -p "GO_AGENT_LIFECYCLE_PARENT_PROMPT: read-only verify Agent lifecycle. Must call Agent with run_in_background=true; do not modify files. After the background agent status/result is visible, output GO_AGENT_LIFECYCLE_PARENT_FINAL_AFTER_STATUS."
) >"$CLI_LOG" 2>&1 || RUN_STATUS=$?

if [[ "$RUN_STATUS" -ne 0 ]]; then
  echo "go Agent lifecycle capture run failed; run_status=$RUN_STATUS log=$CLI_LOG" >&2
  sed -n '1,240p' "$CLI_LOG" >&2 || true
  exit "$RUN_STATUS"
fi
if [[ ! -s "$PROVIDER_LOG" || ! -s "$DUMP_PATH" ]]; then
  echo "go Agent lifecycle capture artifacts missing; provider_log=$PROVIDER_LOG dump=$DUMP_PATH" >&2
  sed -n '1,240p' "$CLI_LOG" >&2 || true
  exit 1
fi

node - "$PROVIDER_LOG" "$DUMP_PATH" "$REPORT_JSON" "$CLI_LOG" "$RUN_STATUS" "$SCENARIO" "$AGENT_ISOLATION" <<'NODE'
const fs = require("fs");
const [providerLog, dumpPath, reportPath, cliLog, runStatusRaw, scenario, agentIsolation] = process.argv.slice(2);

function readJsonl(path) {
  return fs.readFileSync(path, "utf8").trim().split(/\n+/).filter(Boolean).map(line => JSON.parse(line));
}

const providerRecords = readJsonl(providerLog);
const dumpRecords = readJsonl(dumpPath);
const summaries = providerRecords.map(record => ({
  call: record.call,
  classification: record.classification,
  tool_names: record.tool_names,
  message_count: record.message_count,
  contains: record.contains,
}));
const responseClasses = providerRecords.map(record => record.classification);
const mainDumpRecords = dumpRecords.filter(record => !record.scope || record.scope === "main");
const subagentDumpRecords = dumpRecords.filter(record => record.scope === "subagent");
const finalMain = mainDumpRecords[mainDumpRecords.length - 1] || {};
const finalText = JSON.stringify(finalMain.request || {});
const findings = {
  parent_saw_agent_tool: summaries.some(r => r.tool_names.includes("Agent")),
  child_agent_request_captured: summaries.some(r => r.contains.child_prompt) || subagentDumpRecords.length > 0,
  parent_tool_result_contains_go_task_handle: summaries.some(r => r.contains.agent_tool_result_json && r.contains.task_id && r.contains.output_file),
  parent_runtime_status_captured: summaries.some(r => r.contains.background_agent_tasks),
  parent_runtime_status_contains_completed_result: summaries.some(r => r.contains.background_agent_tasks && r.contains.result_preview_done_marker),
  task_notification_xml_absent: !summaries.some(r => r.contains.task_notification_xml),
  final_repeats_completion_instruction: finalText.includes("completion notification: result is ready"),
  runtime_mentions_agentget_without_tool: summaries.some(
    r => r.contains.call_agent_get_instruction && !r.contains.agent_get_tool_exposed,
  ),
  failed_status_captured: summaries.some(r => r.contains.background_agent_tasks && r.contains.failed_status),
  failed_marker_captured: summaries.some(r => r.contains.background_agent_tasks && r.contains.result_preview_failed_marker),
  agent_stop_tool_exposed: summaries.some(r => r.tool_names.includes("AgentStop")),
  agent_stop_result_captured: summaries.some(r => r.classification === "parent_after_agent_stop_result"),
  cancelled_status_captured: summaries.some(r => r.contains.background_agent_tasks && r.contains.cancelled_status),
  cancellation_notification_captured: summaries.some(r => r.contains.background_agent_tasks && r.contains.cancellation_notification),
  partial_marker_captured: summaries.some(r => r.contains.background_agent_tasks && r.contains.result_preview_partial_marker),
  cancelled_capability_loop_captured: summaries.some(r => r.contains.background_agent_tasks && r.contains.cancelled_status && r.contains.capability_loop),
  worktree_notice_captured: summaries.some(r => r.contains.worktree_notice),
  worktree_path_captured: summaries.some(r => r.contains.worktree_path),
  subagent_dump_records: subagentDumpRecords.length,
};
const isolationOk = agentIsolation === "worktree" ? findings.worktree_notice_captured : true;
const commonOk =
  providerRecords.length >= 3 &&
  responseClasses.includes("parent_agent_tool_use") &&
  findings.parent_tool_result_contains_go_task_handle &&
  findings.parent_runtime_status_captured &&
  findings.child_agent_request_captured &&
  findings.task_notification_xml_absent &&
  !findings.runtime_mentions_agentget_without_tool &&
  isolationOk;
const ok = scenario === "child-error"
  ? commonOk &&
    responseClasses.includes("child_agent_error") &&
    findings.failed_status_captured &&
    findings.failed_marker_captured
  : scenario === "cancelled"
    ? providerRecords.length >= 3 &&
      responseClasses.includes("parent_agent_tool_use") &&
      responseClasses.includes("child_agent_hanging") &&
      responseClasses.includes("parent_agent_stop_tool_use") &&
      findings.agent_stop_tool_exposed &&
      findings.agent_stop_result_captured &&
      findings.cancelled_status_captured &&
      findings.cancellation_notification_captured &&
      findings.partial_marker_captured &&
      findings.cancelled_capability_loop_captured &&
      findings.task_notification_xml_absent &&
      !findings.runtime_mentions_agentget_without_tool
  : commonOk &&
    responseClasses.includes("child_agent_final") &&
    findings.parent_runtime_status_contains_completed_result;

const report = {
  ok,
  scenario,
  agent_isolation: agentIsolation,
  run_status: Number(runStatusRaw),
  provider_requests: providerLog,
  prompt_dump: dumpPath,
  run_log: cliLog,
  request_count: providerRecords.length,
  response_classes: responseClasses,
  request_summaries: summaries,
  prompt_dump_summary: {
    records: dumpRecords.length,
    main_records: mainDumpRecords.length,
    subagent_records: subagentDumpRecords.length,
    final_main_runtime_sections: finalMain.runtime_status?.sections || [],
  },
  findings,
};
fs.writeFileSync(reportPath, JSON.stringify(report, null, 2) + "\n");
console.log(JSON.stringify(report, null, 2));
if (!ok) process.exitCode = 1;
NODE

echo "report: $REPORT_JSON" >&2
