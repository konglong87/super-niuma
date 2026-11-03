#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck source=lib/external-repos.sh
source "$ROOT_DIR/scripts/lib/external-repos.sh"
UPSTREAM_DIR="$(default_upstream_dir "$ROOT_DIR")"
TARGET_CWD="$ROOT_DIR"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
WORK_DIR="/tmp/upstream-agent-lifecycle-${TIMESTAMP}"
MODEL="claude-sonnet-4-6"
SCENARIO="completed"
FORCE="false"
AGENT_TEAMS_FLAG="false"
EXPERIMENTAL_AGENT_TEAMS="false"
USER_TYPE_OVERRIDE=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/upstream-agent-lifecycle-capture.sh [flags]

Runs the original Claude Code CLI against a local Anthropic-compatible fetch
stub. The stub captures every /v1/messages request and returns deterministic
SSE responses that force a parent Agent call and a background child completion.

This is a read-only evidence gate: it does not call a real model and writes all
artifacts under --work-dir plus the isolated CLAUDE_CONFIG_DIR.

Flags:
  --upstream-dir <path>  Original Claude Code source/extract dir. Default:
                         <repo-parent>/claude_code_src_2026 (override: NIUMA_UPSTREAM_DIR)
  --target-cwd <path>    Workspace where upstream CLI runs. Default: repo root.
  --work-dir <path>      Output/work directory. Default:
                         /tmp/upstream-agent-lifecycle-<timestamp>
  --model <name>         Model passed to upstream CLI. Default: claude-sonnet-4-6.
  --scenario <name>      Capture scenario: completed, child-error, killed, long-output,
                         final-report,
                         skill-load, skill-compact,
                         send-message-running, send-message-resume,
                         send-message-broadcast, or
                         send-message-structured-shutdown. Default: completed.
  --agent-teams          Pass upstream --agent-teams to test SendMessage/tool-team gating.
  --experimental-agent-teams
                         Set CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 for upstream.
  --user-type <value>    Set USER_TYPE for upstream, e.g. ant.
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
    --target-cwd)
      TARGET_CWD="${2:?missing value for --target-cwd}"
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
    --scenario)
      SCENARIO="${2:?missing value for --scenario}"
      shift 2
      ;;
    --agent-teams)
      AGENT_TEAMS_FLAG="true"
      shift
      ;;
    --experimental-agent-teams)
      EXPERIMENTAL_AGENT_TEAMS="true"
      shift
      ;;
    --user-type)
      USER_TYPE_OVERRIDE="${2:?missing value for --user-type}"
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
  completed|child-error|killed|long-output|final-report|skill-load|skill-compact|send-message-running|send-message-resume|send-message-broadcast|send-message-structured-shutdown)
    ;;
  *)
    echo "unknown scenario: $SCENARIO (want completed, child-error, killed, long-output, final-report, skill-load, skill-compact, send-message-running, send-message-resume, send-message-broadcast, or send-message-structured-shutdown)" >&2
    exit 2
    ;;
esac

CLI_JS="$UPSTREAM_DIR/dist/cli.js"
if [[ ! -f "$CLI_JS" ]]; then
  echo "upstream CLI not found: $CLI_JS" >&2
  exit 2
fi
if [[ ! -d "$TARGET_CWD" ]]; then
  echo "target cwd not found: $TARGET_CWD" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" && "$FORCE" != "true" ]]; then
  echo "work dir already exists: $WORK_DIR (use --force or choose another --work-dir)" >&2
  exit 2
fi
if [[ -e "$WORK_DIR" ]]; then
  rm -rf "$WORK_DIR"
fi
mkdir -p "$WORK_DIR/config" "$WORK_DIR/home"

PRELOAD="$WORK_DIR/capture-fetch.cjs"
CAPTURE_JSONL="$WORK_DIR/capture.jsonl"
RESPONSES_JSONL="$WORK_DIR/stub-responses.jsonl"
REPORT_JSON="$WORK_DIR/report.json"
RUN_LOG="$WORK_DIR/run.log"
RUN_ERR="$WORK_DIR/run.err"
SESSION_ID="44444444-4444-4444-8444-444444444444"

cat > "$PRELOAD" <<'EOF_PRELOAD'
const fs = require("fs");
const { ReadableStream } = require("stream/web");

const capturePath = process.env.UPSTREAM_CAPTURE_JSONL;
const responsesPath = process.env.UPSTREAM_STUB_RESPONSES_JSONL;
const scenario = process.env.UPSTREAM_AGENT_LIFECYCLE_SCENARIO || "completed";
if (!capturePath || !responsesPath) {
  throw new Error("UPSTREAM_CAPTURE_JSONL and UPSTREAM_STUB_RESPONSES_JSONL are required");
}

const encoder = new TextEncoder();
let call = 0;
let parentToolUseIssued = false;
let childFinalIssued = false;
let finalReportChildToolUseIssued = false;
let parentStopIssued = false;
let parentSendMessageIssued = false;
let resumedChildFinalIssued = false;
let skillToolUseIssued = false;

function stubInputTokens() {
  return scenario === "skill-compact" ? 50000 : 10;
}

function longOutputText() {
  return "UPSTREAM_AGENT_LONG_OUTPUT_BODY ".repeat(5000) + "UPSTREAM_AGENT_LONG_OUTPUT_TAIL_MARKER";
}

function textFromRequest(body) {
  const parts = [];
  for (const block of body.system || []) {
    if (block && typeof block.text === "string") parts.push(block.text);
  }
  for (const message of body.messages || []) {
    const content = Array.isArray(message.content) ? message.content : [];
    for (const block of content) {
      if (!block) continue;
      if (typeof block.text === "string") parts.push(block.text);
      if (typeof block.content === "string") parts.push(block.content);
      if (Array.isArray(block.content)) {
        for (const nested of block.content) {
          if (nested && typeof nested.text === "string") parts.push(nested.text);
        }
      }
    }
  }
  return parts.join("\n");
}

function toolNames(body) {
  return (body.tools || []).map((tool) => tool && tool.name).filter(Boolean);
}

function appendJson(path, value) {
  fs.appendFileSync(path, JSON.stringify(value) + "\n");
}

function sseResponse(events) {
  const chunks = events.map((event) => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`);
  return new Response(
    new ReadableStream({
      start(controller) {
        for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
        controller.close();
      },
    }),
    {
      status: 200,
      headers: {
        "content-type": "text/event-stream",
        "request-id": `req_upstream_agent_lifecycle_${call}`,
      },
    },
  );
}

function apiErrorResponse(message) {
 return new Response(
    JSON.stringify({
      type: "error",
      error: {
        type: "invalid_request_error",
        message,
      },
    }),
    {
      status: 400,
      headers: {
        "content-type": "application/json",
        "request-id": `req_upstream_agent_lifecycle_error_${call}`,
      },
    },
  );
}

function textEvents(text) {
  return [
    {
      type: "message_start",
      message: {
        id: `msg_text_${call}`,
        type: "message",
        role: "assistant",
        model: "claude-sonnet-4-6",
        content: [],
        stop_reason: null,
        stop_sequence: null,
        usage: { input_tokens: stubInputTokens(), output_tokens: 10 },
      },
    },
    { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } },
    { type: "content_block_delta", index: 0, delta: { type: "text_delta", text } },
    { type: "content_block_stop", index: 0 },
    {
      type: "message_delta",
      delta: { stop_reason: "end_turn", stop_sequence: null },
      usage: { output_tokens: 10 },
    },
    { type: "message_stop" },
  ];
}

function childHangResponse(signal) {
  appendJson(responsesPath, {
    timestamp: new Date().toISOString(),
    call,
    classification: "child_agent_hanging",
    partial: "UPSTREAM_AGENT_CHILD_PARTIAL_MARKER: partial result before kill",
  });
  let finished = false;
  const closeAsAborted = (controller) => {
    if (finished) return;
    finished = true;
    controller.enqueue(encoder.encode(`event: content_block_stop\ndata: ${JSON.stringify({ type: "content_block_stop", index: 0 })}\n\n`));
    controller.enqueue(encoder.encode(`event: message_delta\ndata: ${JSON.stringify({ type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 10 } })}\n\n`));
    controller.enqueue(encoder.encode(`event: message_stop\ndata: ${JSON.stringify({ type: "message_stop" })}\n\n`));
    controller.close();
  };
  return new Response(
    new ReadableStream({
      start(controller) {
        const events = [
          {
            type: "message_start",
            message: {
              id: `msg_hanging_${call}`,
              type: "message",
              role: "assistant",
              model: "claude-sonnet-4-6",
              content: [],
              stop_reason: null,
              stop_sequence: null,
              usage: { input_tokens: stubInputTokens(), output_tokens: 10 },
            },
          },
          { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } },
          {
            type: "content_block_delta",
            index: 0,
            delta: { type: "text_delta", text: "UPSTREAM_AGENT_CHILD_PARTIAL_MARKER: partial result before kill" },
          },
        ];
        for (const event of events) {
          controller.enqueue(encoder.encode(`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`));
        }
        if (signal) {
          signal.addEventListener("abort", () => closeAsAborted(controller), { once: true });
        }
      },
    }),
    {
      status: 200,
      headers: {
        "content-type": "text/event-stream",
        "request-id": `req_upstream_agent_lifecycle_hanging_${call}`,
      },
    },
  );
}

function agentToolUseEvents() {
  const input = {
    description: "Lifecycle evidence",
    prompt:
      scenario === "killed" || scenario === "send-message-running"
        ? "UPSTREAM_AGENT_LIFECYCLE_CHILD_PROMPT: Read-only. Start with exactly UPSTREAM_AGENT_CHILD_PARTIAL_MARKER and then keep waiting until killed."
        : scenario === "long-output"
          ? "UPSTREAM_AGENT_LIFECYCLE_CHILD_PROMPT: Read-only. Return a very long result that starts with UPSTREAM_AGENT_LONG_OUTPUT_BODY and ends with UPSTREAM_AGENT_LONG_OUTPUT_TAIL_MARKER. Mention output_file discipline once."
          : "UPSTREAM_AGENT_LIFECYCLE_CHILD_PROMPT: Read-only. Return exactly UPSTREAM_AGENT_CHILD_DONE_MARKER and mention output_file discipline.",
    subagent_type: "general-purpose",
    run_in_background: true,
  };
  const partialJson = JSON.stringify(input);
  return [
    {
      type: "message_start",
      message: {
        id: `msg_agent_tool_${call}`,
        type: "message",
        role: "assistant",
        model: "claude-sonnet-4-6",
        content: [],
        stop_reason: null,
        stop_sequence: null,
        usage: { input_tokens: stubInputTokens(), output_tokens: 10 },
      },
    },
    {
      type: "content_block_start",
      index: 0,
      content_block: {
        type: "tool_use",
        id: "toolu_upstream_agent_lifecycle_1",
        name: "Agent",
        input: {},
      },
    },
    {
      type: "content_block_delta",
      index: 0,
      delta: { type: "input_json_delta", partial_json: partialJson },
    },
    { type: "content_block_stop", index: 0 },
    {
      type: "message_delta",
      delta: { stop_reason: "tool_use", stop_sequence: null },
      usage: { output_tokens: 10 },
    },
    { type: "message_stop" },
  ];
}

function skillLoadToolUseEvents() {
  return multiToolUseEvents(`msg_skill_load_tool_${call}`, [
    {
      id: "toolu_upstream_skill_load_1",
      name: "Skill",
      input: {
        skill: "matrix-skill",
      },
    },
  ]);
}

function compactSummaryText() {
  return `<analysis>
Summarize skill compact lifecycle evidence.
</analysis>

<summary>
1. Primary Request and Intent:
   The user asked to load matrix-skill and continue after compaction.

2. Key Technical Concepts:
   - Skill progressive loading
   - Auto compact preservation

3. Files and Code Sections:
   - .claude/skills/matrix-skill/SKILL.md
      - Contains MATRIX_SKILL_ACTIVE.

4. Errors and fixes:
   - None.

5. Problem Solving:
   The matrix-skill invocation happened before compaction.

6. All user messages:
   - UPSTREAM_SKILL_COMPACT_PROMPT

7. Pending Tasks:
   - Continue after compact and preserve MATRIX_SKILL_ACTIVE.

8. Current Work:
   Verify active skill context survives compaction.

9. Optional Next Step:
   Answer with UPSTREAM_SKILL_COMPACT_FINAL_MARKER and MATRIX_SKILL_ACTIVE.
</summary>`;
}

function taskStopToolUseEvents(taskId) {
  const input = { task_id: taskId };
  const partialJson = JSON.stringify(input);
  return [
    {
      type: "message_start",
      message: {
        id: `msg_task_stop_${call}`,
        type: "message",
        role: "assistant",
        model: "claude-sonnet-4-6",
        content: [],
        stop_reason: null,
        stop_sequence: null,
        usage: { input_tokens: stubInputTokens(), output_tokens: 10 },
      },
    },
    {
      type: "content_block_start",
      index: 0,
      content_block: {
        type: "tool_use",
        id: "toolu_upstream_agent_lifecycle_stop_1",
        name: "TaskStop",
        input: {},
      },
    },
    {
      type: "content_block_delta",
      index: 0,
      delta: { type: "input_json_delta", partial_json: partialJson },
    },
    { type: "content_block_stop", index: 0 },
    {
      type: "message_delta",
      delta: { stop_reason: "tool_use", stop_sequence: null },
      usage: { output_tokens: 10 },
    },
    { type: "message_stop" },
  ];
}

function sendMessageToolUseEvents(agentId) {
  const input = scenario === "send-message-broadcast"
    ? {
        to: "*",
        summary: "broadcast lifecycle update",
        message: "UPSTREAM_SENDMESSAGE_BROADCAST_MARKER: broadcast after completion notification.",
      }
    : scenario === "send-message-structured-shutdown"
      ? {
          to: "reviewer",
          message: {
            type: "shutdown_request",
            reason: "UPSTREAM_SENDMESSAGE_STRUCTURED_SHUTDOWN_MARKER: shutdown evidence request.",
          },
        }
    : {
        to: agentId,
        summary: "continue stopped lifecycle child",
        message: "UPSTREAM_SENDMESSAGE_CONTINUE_MARKER: continue with new evidence after completion notification.",
      };
  const partialJson = JSON.stringify(input);
  return [
    {
      type: "message_start",
      message: {
        id: `msg_send_message_${call}`,
        type: "message",
        role: "assistant",
        model: "claude-sonnet-4-6",
        content: [],
        stop_reason: null,
        stop_sequence: null,
        usage: { input_tokens: stubInputTokens(), output_tokens: 10 },
      },
    },
    {
      type: "content_block_start",
      index: 0,
      content_block: {
        type: "tool_use",
        id: "toolu_upstream_send_message_resume_1",
        name: "SendMessage",
        input: {},
      },
    },
    {
      type: "content_block_delta",
      index: 0,
      delta: { type: "input_json_delta", partial_json: partialJson },
    },
    { type: "content_block_stop", index: 0 },
    {
      type: "message_delta",
      delta: { stop_reason: "tool_use", stop_sequence: null },
      usage: { output_tokens: 10 },
    },
    { type: "message_stop" },
  ];
}

function toolUseBlock(index, id, name, input) {
  return [
    {
      type: "content_block_start",
      index,
      content_block: {
        type: "tool_use",
        id,
        name,
        input: {},
      },
    },
    {
      type: "content_block_delta",
      index,
      delta: { type: "input_json_delta", partial_json: JSON.stringify(input) },
    },
    { type: "content_block_stop", index },
  ];
}

function multiToolUseEvents(messageID, blocks) {
  const events = [
    {
      type: "message_start",
      message: {
        id: messageID,
        type: "message",
        role: "assistant",
        model: "claude-sonnet-4-6",
        content: [],
        stop_reason: null,
        stop_sequence: null,
        usage: { input_tokens: stubInputTokens(), output_tokens: 10 },
      },
    },
  ];
  blocks.forEach((block, index) => {
    events.push(...toolUseBlock(index, block.id, block.name, block.input));
  });
  events.push(
    {
      type: "message_delta",
      delta: { stop_reason: "tool_use", stop_sequence: null },
      usage: { output_tokens: 10 },
    },
    { type: "message_stop" },
  );
  return events;
}

function finalReportParentToolUseEvents() {
  return multiToolUseEvents(`msg_final_report_parent_tools_${call}`, [
    {
      id: "toolu_upstream_final_report_parent_grep_1",
      name: "Grep",
      input: {
        pattern: "runtimeTodoStatus|Background agent tasks",
        path: "internal/query",
        output_mode: "content",
        head_limit: 40,
      },
    },
    {
      id: "toolu_upstream_final_report_parent_read_query_1",
      name: "Read",
      input: {
        file_path: "internal/query/query.go",
        offset: 3350,
        limit: 240,
      },
    },
    {
      id: "toolu_upstream_final_report_parent_read_task_1",
      name: "Read",
      input: {
        file_path: "internal/tools/task/task.go",
        offset: 1,
        limit: 220,
      },
    },
    {
      id: "toolu_upstream_final_report_parent_read_agent_1",
      name: "Read",
      input: {
        file_path: "internal/tools/agent/agent.go",
        offset: 1,
        limit: 260,
      },
    },
    {
      id: "toolu_upstream_final_report_parent_agent_1",
      name: "Agent",
      input: {
        description: "Task Agent evidence",
        prompt:
          "UPSTREAM_FINAL_REPORT_CHILD_PROMPT: Read-only. Analyze Task/Agent tool prompt and lifecycle evidence. Use Grep first, then Read with offset/limit on internal/tools/task/task.go, internal/tools/agent/agent.go, and internal/agentruntime/runtime.go. Return UPSTREAM_FINAL_REPORT_CHILD_RESULT_MARKER plus concise function-level evidence. Do not modify files.",
        subagent_type: "general-purpose",
        run_in_background: true,
      },
    },
  ]);
}

function finalReportChildToolUseEvents() {
  return multiToolUseEvents(`msg_final_report_child_tools_${call}`, [
    {
      id: "toolu_upstream_final_report_child_grep_1",
      name: "Grep",
      input: {
        pattern: "Agent|Task|runtime status|tool planning",
        path: "internal",
        output_mode: "content",
        head_limit: 60,
      },
    },
    {
      id: "toolu_upstream_final_report_child_read_task_1",
      name: "Read",
      input: {
        file_path: "internal/tools/task/task.go",
        offset: 1,
        limit: 220,
      },
    },
    {
      id: "toolu_upstream_final_report_child_read_agent_1",
      name: "Read",
      input: {
        file_path: "internal/tools/agent/agent.go",
        offset: 1,
        limit: 260,
      },
    },
    {
      id: "toolu_upstream_final_report_child_read_runtime_1",
      name: "Read",
      input: {
        file_path: "internal/agentruntime/runtime.go",
        offset: 700,
        limit: 180,
      },
    },
  ]);
}

function agentIdFromText(text) {
  const match = text.match(/agentId:\s*([^\s(]+)/);
  return match ? match[1] : "";
}

function classify(body) {
  const text = textFromRequest(body);
  const tools = toolNames(body);
  const hasAgentTool = tools.includes("Agent");
  const hasInitialPrompt = text.includes("UPSTREAM_AGENT_LIFECYCLE_PARENT_PROMPT");
  const hasChildPrompt = text.includes("UPSTREAM_AGENT_LIFECYCLE_CHILD_PROMPT");
  const hasSkillTool = tools.includes("Skill");
  const hasSkillLoadPrompt = text.includes("UPSTREAM_SKILL_LOAD_PROMPT");
  const hasSkillCompactPrompt = text.includes("UPSTREAM_SKILL_COMPACT_PROMPT");
  const hasSkillActiveMarker = text.includes("MATRIX_SKILL_ACTIVE");
  const hasCompactSummaryPrompt = text.includes("Your task is to create a detailed summary of the conversation so far");
  const hasPostCompactSummary =
    text.includes("This session is being continued from a previous conversation") ||
    text.includes("Summary:") ||
    text.includes("Conversation summary");
  const hasFinalReportParentPrompt = text.includes("UPSTREAM_FINAL_REPORT_PARENT_PROMPT");
  const hasFinalReportChildPrompt = text.includes("UPSTREAM_FINAL_REPORT_CHILD_PROMPT");
  const hasFinalReportChildResultMarker = text.includes("UPSTREAM_FINAL_REPORT_CHILD_RESULT_MARKER");
  const hasFinalReportParentFinalMarker = text.includes("UPSTREAM_FINAL_REPORT_PARENT_FINAL_MARKER");
  const hasAsyncLaunchResult = text.includes("Async agent launched successfully");
  const hasChildDoneMarker = text.includes("UPSTREAM_AGENT_CHILD_DONE_MARKER: child result complete");
  const hasChildPartialMarker = text.includes("UPSTREAM_AGENT_CHILD_PARTIAL_MARKER: partial result before kill");
  const hasLongOutputTailMarker = text.includes("UPSTREAM_AGENT_LONG_OUTPUT_TAIL_MARKER");
  const hasSendMessageContinueMarker = text.includes("UPSTREAM_SENDMESSAGE_CONTINUE_MARKER");
  const hasSendMessageBroadcastMarker = text.includes("UPSTREAM_SENDMESSAGE_BROADCAST_MARKER");
  const hasSendMessageStructuredShutdownMarker = text.includes("UPSTREAM_SENDMESSAGE_STRUCTURED_SHUTDOWN_MARKER");
  const hasSendMessageResumeResult =
    text.includes("resumed it in the background") ||
    text.includes("had no active task; resumed from transcript in the background") ||
    text.includes("Message queued for delivery");
  const hasSendMessageStoppedResumeFailure =
    text.includes("could not be resumed") ||
    text.includes("No transcript found for agent ID") ||
    text.includes("has no transcript to resume");
  const hasSendMessageBroadcastFailure =
    text.includes("Not in a team context") ||
    text.includes("No teammates to broadcast") ||
    text.includes("Message broadcast to") ||
    text.includes("Team \"") ||
    text.includes("does not exist");
  const hasSendMessageStructuredShutdownResult =
    text.includes("Shutdown request sent to reviewer") ||
    text.includes("request_id") ||
    text.includes("shutdown_");
  const hasNotification =
    text.includes("<task-notification") ||
    text.includes("completion notification") ||
    text.includes("Agent task") ||
    text.includes("has completed") ||
    text.includes("<status>killed</status>");

  if (!parentToolUseIssued && hasAgentTool && hasInitialPrompt) {
    parentToolUseIssued = true;
    return "parent_agent_tool_use";
  }
  if ((scenario === "skill-load" || scenario === "skill-compact") && !skillToolUseIssued && hasSkillTool && (hasSkillLoadPrompt || hasSkillCompactPrompt)) {
    skillToolUseIssued = true;
    return "parent_skill_tool_use";
  }
  if (scenario === "skill-compact" && hasCompactSummaryPrompt) {
    return "compact_summary";
  }
  if (scenario === "skill-compact" && hasPostCompactSummary && hasSkillActiveMarker) {
    return "parent_after_skill_compact";
  }
  if (scenario === "skill-load" && hasSkillActiveMarker) {
    return "parent_after_skill_load";
  }
  if (scenario === "final-report" && !parentToolUseIssued && hasAgentTool && hasFinalReportParentPrompt) {
    parentToolUseIssued = true;
    return "parent_final_report_tool_use";
  }
  if (scenario === "final-report" && hasFinalReportChildPrompt && !finalReportChildToolUseIssued) {
    finalReportChildToolUseIssued = true;
    return "child_final_report_tool_use";
  }
  if (scenario === "final-report" && hasFinalReportChildPrompt && !childFinalIssued) {
    childFinalIssued = true;
    return "child_agent_final";
  }
  if (scenario === "final-report" && hasFinalReportChildResultMarker) {
    return "parent_after_agent_notification";
  }
  if (scenario === "final-report" && hasFinalReportParentFinalMarker) {
    return "parent_after_final_report";
  }
  if (scenario === "send-message-resume" && hasSendMessageContinueMarker && hasChildPrompt && !resumedChildFinalIssued) {
    resumedChildFinalIssued = true;
    return "child_agent_resumed_after_send_message";
  }
  if (hasChildPrompt && (scenario === "killed" || scenario === "send-message-running")) {
    return "child_agent_hanging";
  }
  if (hasChildPrompt && scenario === "child-error") {
    return "child_agent_error";
  }
  if (hasChildPrompt && !childFinalIssued) {
    childFinalIssued = true;
    return scenario === "child-error" ? "child_agent_error" : "child_agent_final";
  }
  if (
    scenario === "send-message-resume" &&
    !parentSendMessageIssued &&
    tools.includes("SendMessage") &&
    (hasChildDoneMarker || hasNotification)
  ) {
    parentSendMessageIssued = true;
    return "parent_send_message_tool_use";
  }
  if (
    scenario === "send-message-broadcast" &&
    !parentSendMessageIssued &&
    tools.includes("SendMessage") &&
    (hasChildDoneMarker || hasNotification)
  ) {
    parentSendMessageIssued = true;
    return "parent_send_message_tool_use";
  }
  if (
    scenario === "send-message-structured-shutdown" &&
    !parentSendMessageIssued &&
    tools.includes("SendMessage") &&
    (hasChildDoneMarker || hasNotification)
  ) {
    parentSendMessageIssued = true;
    return "parent_send_message_tool_use";
  }
  if (
    scenario === "send-message-running" &&
    !parentSendMessageIssued &&
    tools.includes("SendMessage") &&
    hasAsyncLaunchResult
  ) {
    parentSendMessageIssued = true;
    return "parent_send_message_tool_use";
  }
  if (scenario === "send-message-resume" && hasSendMessageResumeResult) {
    return "parent_after_send_message_result";
  }
  if (scenario === "send-message-resume" && hasSendMessageStoppedResumeFailure) {
    return "parent_after_send_message_result";
  }
  if (scenario === "send-message-broadcast" && (hasSendMessageBroadcastMarker || hasSendMessageBroadcastFailure)) {
    return "parent_after_send_message_result";
  }
  if (scenario === "send-message-structured-shutdown" && (hasSendMessageStructuredShutdownMarker || hasSendMessageStructuredShutdownResult)) {
    return "parent_after_send_message_result";
  }
  if (scenario === "send-message-running" && hasSendMessageResumeResult && !parentStopIssued) {
    parentStopIssued = true;
    return "parent_task_stop_tool_use";
  }
  if (hasChildDoneMarker || hasLongOutputTailMarker || hasNotification) {
    return "parent_after_agent_notification";
  }
  if (scenario === "killed" && hasAsyncLaunchResult && !parentStopIssued) {
    parentStopIssued = true;
    return "parent_task_stop_tool_use";
  }
  if (scenario === "killed" && text.includes("Successfully stopped task")) {
    return "parent_after_task_stop_result";
  }
  if (hasAsyncLaunchResult && !hasChildDoneMarker) {
    return "parent_after_async_launch";
  }
  return "fallback_text";
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
    call += 1;
    const classification = classify(parsed);
    appendJson(capturePath, {
      timestamp: new Date().toISOString(),
      call,
      url,
      method,
      classification,
      tool_names: toolNames(parsed),
      body: parsed,
    });

    let events;
    if (classification === "parent_final_report_tool_use") {
      events = finalReportParentToolUseEvents();
    } else if (classification === "child_final_report_tool_use") {
      events = finalReportChildToolUseEvents();
    } else if (classification === "parent_skill_tool_use") {
      events = skillLoadToolUseEvents();
    } else if (classification === "compact_summary") {
      events = textEvents(compactSummaryText());
    } else if (classification === "parent_after_skill_load") {
      events = textEvents("UPSTREAM_SKILL_LOAD_FINAL_MARKER: MATRIX_SKILL_ACTIVE reached the post-Skill request context.");
    } else if (classification === "parent_after_skill_compact") {
      events = textEvents("UPSTREAM_SKILL_COMPACT_FINAL_MARKER: MATRIX_SKILL_ACTIVE survived original auto compact.");
    } else if (classification === "parent_agent_tool_use") {
      events = agentToolUseEvents();
    } else if (classification === "child_agent_hanging") {
      return childHangResponse(init.signal);
    } else if (classification === "child_agent_final") {
      events = textEvents(
        scenario === "final-report"
          ? "UPSTREAM_FINAL_REPORT_CHILD_RESULT_MARKER: child gathered Task/Agent lifecycle evidence from internal/tools/task/task.go, internal/tools/agent/agent.go, and internal/agentruntime/runtime.go. subagentToolPlanningStatus is visible in child context."
          : scenario === "long-output"
          ? longOutputText()
          : "UPSTREAM_AGENT_CHILD_DONE_MARKER: child result complete. Do not peek output_file unless asked.",
      );
    } else if (classification === "child_agent_resumed_after_send_message") {
      events = textEvents("UPSTREAM_SENDMESSAGE_RESUMED_CHILD_DONE_MARKER: resumed child saw continuation marker.");
    } else if (classification === "child_agent_error") {
      appendJson(responsesPath, {
        timestamp: new Date().toISOString(),
        call,
        classification,
        error: "UPSTREAM_AGENT_CHILD_FAILED_MARKER: simulated child provider failure",
      });
      return apiErrorResponse("UPSTREAM_AGENT_CHILD_FAILED_MARKER: simulated child provider failure");
    } else if (classification === "parent_after_agent_notification") {
      events = textEvents(
        scenario === "final-report"
          ? "UPSTREAM_FINAL_REPORT_PARENT_FINAL_MARKER: runtimeStatusText runtimeTodoStatus runtimePlanStatus runtimeAgentTaskStatus subagentToolPlanningStatus internal/query/query.go internal/tools/task/task.go internal/tools/agent/agent.go internal/agentruntime/runtime.go"
          : "UPSTREAM_AGENT_LIFECYCLE_PARENT_FINAL_AFTER_NOTIFICATION",
      );
    } else if (classification === "parent_send_message_tool_use") {
      events = sendMessageToolUseEvents(agentIdFromText(textFromRequest(parsed)));
    } else if (classification === "parent_after_send_message_result") {
      events = textEvents("UPSTREAM_SENDMESSAGE_PARENT_AFTER_RESULT");
    } else if (classification === "parent_task_stop_tool_use") {
      events = taskStopToolUseEvents(agentIdFromText(textFromRequest(parsed)));
    } else if (classification === "parent_after_task_stop_result") {
      events = textEvents("UPSTREAM_AGENT_LIFECYCLE_PARENT_AFTER_TASK_STOP_WAITING");
    } else if (classification === "parent_after_async_launch") {
      events = textEvents("UPSTREAM_AGENT_LIFECYCLE_PARENT_ACK_ASYNC_LAUNCH");
    } else {
      events = textEvents("UPSTREAM_AGENT_LIFECYCLE_FALLBACK_OK");
    }
    appendJson(responsesPath, {
      timestamp: new Date().toISOString(),
      call,
      classification,
      events,
    });
    return sseResponse(events);
  }
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
};
EOF_PRELOAD

echo "running upstream Agent lifecycle capture..." >&2
RUN_STATUS=0
TOOL_LIST="Agent,Read,Grep,SendMessage"
if [[ "$SCENARIO" == "killed" || "$SCENARIO" == "send-message-running" ]]; then
  TOOL_LIST="Agent,Read,Grep,SendMessage,TaskStop"
fi
if [[ "$SCENARIO" == "final-report" ]]; then
  TOOL_LIST="Agent,Read,Grep"
fi
if [[ "$SCENARIO" == "skill-load" || "$SCENARIO" == "skill-compact" ]]; then
  TOOL_LIST="Skill,Read,LS"
fi
PROMPT_TEXT="UPSTREAM_AGENT_LIFECYCLE_PARENT_PROMPT：只读验证 Agent 生命周期。必须调用 Agent 并设置 run_in_background=true；不要修改文件。收到后台完成通知后，只输出 UPSTREAM_AGENT_LIFECYCLE_PARENT_FINAL_AFTER_NOTIFICATION。"
if [[ "$SCENARIO" == "final-report" ]]; then
  PROMPT_TEXT="UPSTREAM_FINAL_REPORT_PARENT_PROMPT：真实复杂只读诊断任务。分析本仓库 TodoWrite、Task/Agent、tool_result、plan/progress 上下文如何进入模型请求，并给出文件/函数/行号证据。要求：1. 父线程必须先用 Grep 搜索 runtimeTodoStatus 或 Background agent tasks 相关实现；2. 父线程必须用 Read 读取 internal/query/query.go、internal/tools/task/task.go、internal/tools/agent/agent.go 的相关片段；3. 必须调用 Agent 派发一个只读子任务，让子任务只分析 Task/Agent 工具提示词和生命周期证据，子任务不得修改文件；4. 子任务必须使用 Grep 定位后再用 Read 的 offset/limit 读取 internal/tools/task/task.go、internal/tools/agent/agent.go、internal/agentruntime/runtime.go 的最小必要片段；5. 父线程综合子任务结果后用 P0/P1/P2 总结，最终答案必须包含 runtimeStatusText、runtimeTodoStatus、runtimePlanStatus、runtimeAgentTaskStatus、subagentToolPlanningStatus 以及 Task/Agent 工具实现的文件/函数证据。全程不要修改文件，不要调用写入工具。"
fi
if [[ "$SCENARIO" == "skill-load" ]]; then
  PROMPT_TEXT="UPSTREAM_SKILL_LOAD_PROMPT：必须先调用 Skill 工具加载 matrix-skill。加载后只用一句话回答：MATRIX_SKILL_ACTIVE 已进入上下文。不要修改文件。"
fi
if [[ "$SCENARIO" == "skill-compact" ]]; then
  PROMPT_TEXT="UPSTREAM_SKILL_COMPACT_PROMPT：必须先调用 Skill 工具加载 matrix-skill。加载后继续到 compact 后的下一轮，只用一句话回答：UPSTREAM_SKILL_COMPACT_FINAL_MARKER MATRIX_SKILL_ACTIVE。不要修改文件。"
fi
(
  cd "$TARGET_CWD"
  export HOME="$WORK_DIR/home"
  export CLAUDE_CONFIG_DIR="$WORK_DIR/config"
  export ANTHROPIC_API_KEY="dummy"
  export USE_BUILTIN_RIPGREP=0
  export UPSTREAM_CAPTURE_JSONL="$CAPTURE_JSONL"
  export UPSTREAM_STUB_RESPONSES_JSONL="$RESPONSES_JSONL"
  export UPSTREAM_AGENT_LIFECYCLE_SCENARIO="$SCENARIO"
  export NODE_OPTIONS="--require $PRELOAD"
  if [[ "$SCENARIO" == "skill-compact" ]]; then
    export CLAUDE_CODE_AUTO_COMPACT_WINDOW="${CLAUDE_CODE_AUTO_COMPACT_WINDOW:-50000}"
    export CLAUDE_AUTOCOMPACT_PCT_OVERRIDE="${CLAUDE_AUTOCOMPACT_PCT_OVERRIDE:-90}"
  fi
  if [[ "$EXPERIMENTAL_AGENT_TEAMS" == "true" ]]; then
    export CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
  fi
  if [[ -n "$USER_TYPE_OVERRIDE" ]]; then
    export USER_TYPE="$USER_TYPE_OVERRIDE"
  fi
  if [[ "$AGENT_TEAMS_FLAG" == "true" ]]; then
    node "$CLI_JS" \
      -p "$PROMPT_TEXT" \
      --model "$MODEL" \
      --max-turns 6 \
      --tools "$TOOL_LIST" \
      --session-id "$SESSION_ID" \
      --permission-mode bypassPermissions \
      --agent-teams
  else
    node "$CLI_JS" \
      -p "$PROMPT_TEXT" \
      --model "$MODEL" \
      --max-turns 6 \
      --tools "$TOOL_LIST" \
      --session-id "$SESSION_ID" \
      --permission-mode bypassPermissions
  fi
) >"$RUN_LOG" 2>"$RUN_ERR" || RUN_STATUS=$?

if [[ ! -s "$CAPTURE_JSONL" ]]; then
  echo "upstream Agent lifecycle capture missing; run_status=$RUN_STATUS log=$RUN_LOG err=$RUN_ERR" >&2
  sed -n '1,200p' "$RUN_LOG" >&2 || true
  sed -n '1,200p' "$RUN_ERR" >&2 || true
  exit 1
fi

node - "$CAPTURE_JSONL" "$RESPONSES_JSONL" "$REPORT_JSON" "$RUN_LOG" "$RUN_ERR" "$RUN_STATUS" "$WORK_DIR/config" "$SESSION_ID" "$SCENARIO" "$AGENT_TEAMS_FLAG" "$EXPERIMENTAL_AGENT_TEAMS" "$USER_TYPE_OVERRIDE" <<'EOF_ANALYZE'
const fs = require("fs");
const path = require("path");

const [
  capturePath,
  responsesPath,
  reportPath,
  runLog,
  runErr,
  runStatusRaw,
  configDir,
  sessionId,
  scenario,
  agentTeamsFlag,
  experimentalAgentTeams,
  userTypeOverride,
] = process.argv.slice(2);

function readJsonl(file) {
  if (!fs.existsSync(file)) return [];
  return fs.readFileSync(file, "utf8").trim().split(/\n+/).filter(Boolean).map((line) => JSON.parse(line));
}

function textFromRequest(body) {
  const parts = [];
  for (const block of body.system || []) {
    if (block && typeof block.text === "string") parts.push(block.text);
  }
  for (const message of body.messages || []) {
    const content = Array.isArray(message.content) ? message.content : [];
    for (const block of content) {
      if (!block) continue;
      if (typeof block.text === "string") parts.push(block.text);
      if (typeof block.content === "string") parts.push(block.content);
      if (Array.isArray(block.content)) {
        for (const nested of block.content) {
          if (nested && typeof nested.text === "string") parts.push(nested.text);
        }
      }
    }
  }
  return parts.join("\n");
}

const captures = readJsonl(capturePath);
const responses = readJsonl(responsesPath);
// Discover this run's transcript without depending on a machine-specific project slug.
const projectsDir = path.join(configDir, "projects");
const transcriptCandidates = fs.existsSync(projectsDir)
  ? fs.readdirSync(projectsDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => path.join(projectsDir, entry.name, `${sessionId}.jsonl`))
    .filter((candidate) => fs.existsSync(candidate))
  : [];
if (transcriptCandidates.length > 1) {
  throw new Error("ambiguous transcript for capture session");
}
const transcriptPath = transcriptCandidates[0] || null;
const transcriptExists = transcriptPath !== null;
const requestSummaries = captures.map((entry) => {
  const text = textFromRequest(entry.body || {});
  const raw = JSON.stringify(entry.body || {});
  const messages = entry.body?.messages || [];
  const lastMessage = messages[messages.length - 1];
  return {
    call: entry.call,
    classification: entry.classification,
    tool_names: entry.tool_names || [],
    system_block_count: Array.isArray(entry.body?.system) ? entry.body.system.length : 0,
    message_count: messages.length,
    last_message_role: lastMessage?.role || "",
    request_text_bytes: text.length,
    contains: {
      parent_prompt: text.includes("UPSTREAM_AGENT_LIFECYCLE_PARENT_PROMPT"),
      child_prompt: text.includes("UPSTREAM_AGENT_LIFECYCLE_CHILD_PROMPT"),
      skill_load_prompt: text.includes("UPSTREAM_SKILL_LOAD_PROMPT"),
      skill_compact_prompt: text.includes("UPSTREAM_SKILL_COMPACT_PROMPT"),
      skill_launch_result: text.includes("Launching skill: matrix-skill"),
      skill_active_marker: text.includes("MATRIX_SKILL_ACTIVE"),
      skill_final_marker: text.includes("UPSTREAM_SKILL_LOAD_FINAL_MARKER"),
      skill_compact_final_marker: text.includes("UPSTREAM_SKILL_COMPACT_FINAL_MARKER"),
      compact_summary_prompt: text.includes("Your task is to create a detailed summary of the conversation so far"),
      post_compact_summary: text.includes("This session is being continued from a previous conversation") ||
        text.includes("Summary:") ||
        text.includes("Conversation summary"),
      invoked_skills_attachment: raw.includes('"invoked_skills"') || raw.includes('"type":"invoked_skills"'),
      final_report_parent_prompt: text.includes("UPSTREAM_FINAL_REPORT_PARENT_PROMPT"),
      final_report_child_prompt: text.includes("UPSTREAM_FINAL_REPORT_CHILD_PROMPT"),
      final_report_child_result: text.includes("UPSTREAM_FINAL_REPORT_CHILD_RESULT_MARKER"),
      final_report_parent_final: text.includes("UPSTREAM_FINAL_REPORT_PARENT_FINAL_MARKER"),
      runtime_status_text: text.includes("runtimeStatusText"),
      runtime_todo_status: text.includes("runtimeTodoStatus"),
      runtime_plan_status: text.includes("runtimePlanStatus"),
      runtime_agent_task_status: text.includes("runtimeAgentTaskStatus"),
      subagent_tool_planning_status: text.includes("subagentToolPlanningStatus"),
      async_launch_result: text.includes("Async agent launched successfully"),
      agent_id: text.includes("agentId:"),
      output_file: text.includes("output_file:"),
      send_message: text.includes("SendMessage"),
      child_done_marker: text.includes("UPSTREAM_AGENT_CHILD_DONE_MARKER: child result complete"),
      child_partial_marker: text.includes("UPSTREAM_AGENT_CHILD_PARTIAL_MARKER: partial result before kill"),
      task_notification: text.includes("<task-notification"),
      task_stop_result: text.includes("Successfully stopped task"),
      completion_notification: text.includes("completion notification"),
      completed_status: text.includes("<status>completed</status>") || text.includes("status: completed"),
      failed_status: text.includes("<status>failed</status>") || text.includes("status: failed"),
      killed_status: text.includes("<status>killed</status>") || text.includes("status: killed"),
      failed_marker: text.includes("UPSTREAM_AGENT_CHILD_FAILED_MARKER"),
      long_output_body_marker: text.includes("UPSTREAM_AGENT_LONG_OUTPUT_BODY"),
      long_output_tail_marker: text.includes("UPSTREAM_AGENT_LONG_OUTPUT_TAIL_MARKER"),
      generic_persisted_output: text.includes("<persisted-output>"),
      structured_content_preview: text.includes("content_preview") || text.includes("content_truncated"),
      sendmessage_continue_marker: text.includes("UPSTREAM_SENDMESSAGE_CONTINUE_MARKER"),
      sendmessage_broadcast_marker: text.includes("UPSTREAM_SENDMESSAGE_BROADCAST_MARKER"),
      sendmessage_structured_shutdown_marker: text.includes("UPSTREAM_SENDMESSAGE_STRUCTURED_SHUTDOWN_MARKER"),
      sendmessage_resumed_child_marker: text.includes("UPSTREAM_SENDMESSAGE_RESUMED_CHILD_DONE_MARKER"),
      sendmessage_resume_result: text.includes("resumed it in the background") ||
        text.includes("had no active task; resumed from transcript in the background") ||
        text.includes("Message queued for delivery"),
      sendmessage_stopped_resume_failure: text.includes("could not be resumed") ||
        text.includes("No transcript found for agent ID") ||
        text.includes("has no transcript to resume"),
      sendmessage_broadcast_result: text.includes("Not in a team context") ||
        text.includes("No teammates to broadcast") ||
        text.includes("Message broadcast to") ||
        text.includes("Team \"") ||
        text.includes("does not exist"),
      sendmessage_structured_shutdown_result: text.includes("Shutdown request sent to reviewer") ||
        text.includes("request_id") ||
        text.includes("shutdown_"),
    },
  };
});
const responseClasses = responses.map((entry) => entry.classification);
const findings = {
  parent_saw_agent_tool: requestSummaries.some((r) => r.tool_names.includes("Agent")),
  parent_saw_skill_tool: requestSummaries.some((r) => r.tool_names.includes("Skill")),
  skill_tool_use_captured: responseClasses.includes("parent_skill_tool_use"),
  skill_launch_result_captured: requestSummaries.some((r) => r.contains.skill_launch_result),
  skill_active_context_captured: requestSummaries.some((r) => r.contains.skill_active_marker),
  skill_final_response_captured: responseClasses.includes("parent_after_skill_load"),
  skill_compact_summary_captured: responseClasses.includes("compact_summary"),
  skill_compact_post_context_captured: responseClasses.includes("parent_after_skill_compact"),
  skill_compact_post_summary_captured: requestSummaries.some((r) => r.contains.post_compact_summary),
  skill_compact_invoked_skills_attachment_captured: requestSummaries.some((r) => r.contains.invoked_skills_attachment),
  skill_compact_final_marker_captured: requestSummaries.some((r) => r.contains.skill_compact_final_marker),
  send_message_tool_exposed: requestSummaries.some((r) => r.tool_names.includes("SendMessage")),
  send_message_tool_exposed_calls: requestSummaries
    .filter((r) => r.tool_names.includes("SendMessage"))
    .map((r) => r.call),
  parent_tool_result_contains_async_contract: requestSummaries.some(
    (r) => r.contains.async_launch_result && r.contains.agent_id && r.contains.output_file && r.contains.send_message,
  ),
  child_agent_request_captured: requestSummaries.some((r) => r.contains.child_prompt),
  task_stop_tool_exposed: requestSummaries.some((r) => r.tool_names.includes("TaskStop")),
  task_stop_result_captured: requestSummaries.some((r) => r.contains.task_stop_result),
  notification_turn_captured: requestSummaries.some(
    (r) => r.contains.child_done_marker || r.contains.task_notification || r.contains.completion_notification,
  ),
  provider_error_reported_as_completed: requestSummaries.some(
    (r) => r.contains.task_notification && r.contains.completed_status && r.contains.failed_marker,
  ),
  failed_notification_captured: requestSummaries.some(
    (r) => r.contains.task_notification && r.contains.failed_status,
  ),
  killed_notification_captured: requestSummaries.some(
    (r) => r.contains.task_notification && r.contains.killed_status,
  ),
  long_output_notification_captured: requestSummaries.some(
    (r) => r.contains.task_notification && r.contains.completed_status && r.contains.long_output_tail_marker,
  ),
  long_output_tail_marker_captured: requestSummaries.some((r) => r.contains.long_output_tail_marker),
  long_output_used_generic_persisted_output: requestSummaries.some((r) => r.contains.generic_persisted_output),
  long_output_used_structured_content_preview: requestSummaries.some((r) => r.contains.structured_content_preview),
  sendmessage_tool_use_captured: responseClasses.includes("parent_send_message_tool_use"),
  sendmessage_resume_result_captured: requestSummaries.some((r) => r.contains.sendmessage_resume_result),
  sendmessage_resumed_child_request_captured: responseClasses.includes("child_agent_resumed_after_send_message"),
  sendmessage_resumed_child_prompt_contains_message: requestSummaries.some(
    (r) => r.classification === "child_agent_resumed_after_send_message" && r.contains.sendmessage_continue_marker,
  ),
  sendmessage_resumed_child_notification_captured: requestSummaries.some(
    (r) => r.contains.task_notification && r.contains.completed_status && r.contains.sendmessage_resumed_child_marker,
  ),
  sendmessage_stopped_resume_failure_captured: requestSummaries.some((r) => r.contains.sendmessage_stopped_resume_failure),
  sendmessage_broadcast_result_captured: requestSummaries.some((r) => r.contains.sendmessage_broadcast_result),
  sendmessage_structured_shutdown_result_captured: requestSummaries.some((r) => r.contains.sendmessage_structured_shutdown_result),
  final_report_parent_tool_use_captured: responseClasses.includes("parent_final_report_tool_use"),
  final_report_child_tool_use_captured: responseClasses.includes("child_final_report_tool_use"),
  final_report_child_result_captured: requestSummaries.some((r) => r.contains.final_report_child_result),
  final_report_parent_final_response_captured: responseClasses.includes("parent_after_agent_notification"),
  final_report_required_terms_visible: requestSummaries.some(
    (r) =>
      r.contains.runtime_status_text &&
      r.contains.runtime_todo_status &&
      r.contains.runtime_plan_status &&
      r.contains.runtime_agent_task_status &&
      r.contains.subagent_tool_planning_status,
  ),
  transcript_exists: transcriptExists,
};
const commonOk =
  captures.length >= 2 &&
  responseClasses.includes("parent_agent_tool_use") &&
  findings.parent_tool_result_contains_async_contract &&
  findings.child_agent_request_captured;
const ok = scenario === "child-error"
  ? commonOk &&
    responseClasses.includes("child_agent_error") &&
    findings.provider_error_reported_as_completed &&
    findings.notification_turn_captured
  : scenario === "killed"
    ? captures.length >= 3 &&
      responseClasses.includes("parent_agent_tool_use") &&
      responseClasses.includes("child_agent_hanging") &&
      responseClasses.includes("parent_task_stop_tool_use") &&
      findings.task_stop_tool_exposed &&
      findings.task_stop_result_captured &&
      findings.killed_notification_captured &&
      findings.notification_turn_captured
    : scenario === "long-output"
      ? commonOk &&
        responseClasses.includes("child_agent_final") &&
        findings.notification_turn_captured &&
        findings.long_output_notification_captured &&
        !findings.long_output_used_generic_persisted_output &&
        !findings.long_output_used_structured_content_preview
      : scenario === "skill-load"
        ? captures.length >= 2 &&
          findings.parent_saw_skill_tool &&
          findings.skill_tool_use_captured &&
          findings.skill_launch_result_captured &&
          findings.skill_active_context_captured &&
          findings.skill_final_response_captured
        : scenario === "skill-compact"
          ? captures.length >= 3 &&
            findings.parent_saw_skill_tool &&
            findings.skill_tool_use_captured &&
            findings.skill_launch_result_captured &&
            findings.skill_compact_summary_captured &&
            findings.skill_compact_post_summary_captured &&
            findings.skill_active_context_captured &&
            findings.skill_compact_post_context_captured
        : scenario === "final-report"
          ? captures.length >= 4 &&
            responseClasses.includes("parent_final_report_tool_use") &&
            responseClasses.includes("child_final_report_tool_use") &&
            responseClasses.includes("child_agent_final") &&
            findings.parent_tool_result_contains_async_contract &&
            findings.final_report_child_result_captured &&
            findings.final_report_parent_final_response_captured &&
            findings.final_report_required_terms_visible &&
            findings.transcript_exists
        : scenario === "send-message-resume"
        ? commonOk &&
          findings.send_message_tool_exposed &&
          responseClasses.includes("child_agent_final") &&
          findings.notification_turn_captured &&
          findings.sendmessage_tool_use_captured &&
          findings.sendmessage_stopped_resume_failure_captured &&
          findings.transcript_exists
        : scenario === "send-message-broadcast"
          ? commonOk &&
            findings.send_message_tool_exposed &&
            responseClasses.includes("child_agent_final") &&
            findings.notification_turn_captured &&
            findings.sendmessage_tool_use_captured &&
            findings.sendmessage_broadcast_result_captured &&
            findings.transcript_exists
        : scenario === "send-message-structured-shutdown"
          ? commonOk &&
            findings.send_message_tool_exposed &&
            responseClasses.includes("child_agent_final") &&
            findings.notification_turn_captured &&
            findings.sendmessage_tool_use_captured &&
            findings.sendmessage_structured_shutdown_result_captured &&
            findings.transcript_exists
        : scenario === "send-message-running"
          ? captures.length >= 4 &&
            responseClasses.includes("parent_agent_tool_use") &&
            responseClasses.includes("child_agent_hanging") &&
            findings.send_message_tool_exposed &&
            findings.parent_tool_result_contains_async_contract &&
            findings.sendmessage_tool_use_captured &&
            findings.sendmessage_resume_result_captured &&
            findings.task_stop_tool_exposed &&
            findings.task_stop_result_captured
    : commonOk &&
      responseClasses.includes("child_agent_final") &&
      findings.notification_turn_captured;

const report = {
  ok,
  scenario,
  exposure_mode: {
    agent_teams_flag: agentTeamsFlag === "true",
    experimental_agent_teams: experimentalAgentTeams === "true",
    user_type: userTypeOverride || null,
  },
  run_status: Number(runStatusRaw),
  capture: capturePath,
  stub_responses: responsesPath,
  run_log: runLog,
  run_err: runErr,
  transcript: transcriptExists ? transcriptPath : null,
  request_count: captures.length,
  response_classes: responseClasses,
  request_summaries: requestSummaries,
  findings,
};
fs.writeFileSync(reportPath, JSON.stringify(report, null, 2) + "\n");
console.log(JSON.stringify(report, null, 2));
if (!ok) process.exitCode = 1;
EOF_ANALYZE

echo "report: $REPORT_JSON" >&2
