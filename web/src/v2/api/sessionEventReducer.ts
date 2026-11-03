import { mergeEvents } from "../../lib/agentEvents";
import { isReasonableReportedDurationMs } from "../../lib/messages";
import { claimUserQuestionToolID, parseUserQuestionInput, questionChoices, type UserQuestionToolCandidate } from "../../lib/userQuestion";
import type { ComputerObservationImage, ConversationEvent, PreparedAttachment, SessionDetail, SessionMessage, SessionStatus } from "../types";

export const CONVERSATION_EVENT = {
  user: "message", text: "text_delta", thinking: "thinking_delta", tool: "tool_use", toolCall: "tool_call", toolResult: "tool_result",
  usage: "usage", messageStop: "message_stop", artifact: "image_artifact", started: "started", turnStart: "turn_start", compact: "compact_summary", handoff: "session_handoff",
  permission: "permission_request", permissionResolved: "permission_resolved", question: "user_question_request", questionResolved: "user_question_resolved",
  completed: "completed", failed: "failed", cancelled: "cancelled", timeout: "timeout"
} as const;
const IMAGE_TOOLS = new Set(["GenerateImage", "EditImage"]);

export function applyConversationEvents(detail: SessionDetail, incoming: ConversationEvent[], options: { live?: boolean } = {}): SessionDetail {
  const events = mergeEvents(detail.events ?? [], incoming);
  const previousMessages = new Map(detail.messages.map((message) => [message.id, message]));
  const previousIDs = new Set((detail.events ?? []).map((event) => event.id));
  const liveIDs = new Set(options.live ? incoming.filter((event) => !previousIDs.has(event.id)).map((event) => event.id) : []);
  const messages: SessionMessage[] = [];
  const activity: string[] = [];
  const runs = new Map<string, SessionDetail["runs"][number]>();
  const metadata = new Map<number, { provider?: string; model?: string; durationMs?: number; tokens?: number }>();
  const tools = new Map<string, SessionMessage>();
  const questions = new Map<string, SessionMessage>();
  const questionTools: UserQuestionToolCandidate[] = [];
  const claimedQuestionToolIDs = new Set<string>();
  const phases = new Map<number, number>();
  const turns = new Map<number, number>();
  const seenArtifacts = new Set<string>();
  const appendArtifact = (assetID: string, event: ConversationEvent) => {
    const key = `${event.task_id}:${assetID}`;
    if (!assetID || seenArtifacts.has(key)) return;
    seenArtifacts.add(key);
    messages.push({ id: `image:${event.id}:${assetID}`, taskID: event.task_id, role: "assistant", kind: "message", content: "", createdAt: event.created_at ?? "", artifacts: [{ asset_id: assetID }] });
  };
  let buffer: SessionMessage | undefined;
  let bufferTask = 0;
  for (const event of events) {
    const payload = eventPayload(event.payload_json);
    const id = `${event.task_id}:${event.id}`;
    const time = event.created_at ?? "";
    const runID = String(event.task_id);
    const run = runs.get(runID) ?? { id: runID, status: "running" as SessionStatus, startedAt: time };
    runs.set(runID, run);
    const type = event.event_type;
    const meta = metadata.get(event.task_id) ?? {};
    const usage = object(payload.usage);
    if (type === CONVERSATION_EVENT.started || type === CONVERSATION_EVENT.completed) {
      meta.provider = text(payload.provider) || meta.provider;
      meta.model = text(payload.model) || meta.model;
    }
    if (type === CONVERSATION_EVENT.usage || type === CONVERSATION_EVENT.messageStop || type === CONVERSATION_EVENT.completed) {
      const source = { ...payload, ...usage };
      const tokens = number(source.total_tokens) || number(source.input_tokens) + number(source.output_tokens) + number(source.cache_read_input_tokens) + number(source.cache_creation_input_tokens);
      meta.tokens = Math.max(meta.tokens ?? 0, tokens) || undefined;
    }
    metadata.set(event.task_id, meta);
    const terminal = type === CONVERSATION_EVENT.completed || type === CONVERSATION_EVENT.failed || type === CONVERSATION_EVENT.cancelled || type === CONVERSATION_EVENT.timeout;
    if (terminal && typeof payload.duration_ms === "number" && isReasonableReportedDurationMs(payload.duration_ms)) meta.durationMs = payload.duration_ms;
    if (type === CONVERSATION_EVENT.turnStart) turns.set(event.task_id, number(payload.turn));
    if (buffer?.kind === "thinking" && (bufferTask !== event.task_id || (type !== CONVERSATION_EVENT.thinking && type !== CONVERSATION_EVENT.usage && type !== "nested_agent_progress"))) {
      buffer.stageEndedAt = time;
      buffer.stageDurationMs = elapsed(buffer.createdAt, time);
      buffer.thinkingStatus = terminal && bufferTask === event.task_id ? (type === CONVERSATION_EVENT.completed ? "completed" : type === CONVERSATION_EVENT.cancelled ? "stopped" : "failed") : "completed";
      buffer = undefined;
    }
    const eventRole = conversationEventRole(type, payload);
    if (type === CONVERSATION_EVENT.text || type === CONVERSATION_EVENT.thinking || (type === CONVERSATION_EVENT.user && eventRole === "assistant")) {
      const kind = type === CONVERSATION_EVENT.thinking ? "thinking" : "message";
      const content = text(payload.content) || text(payload.text) || text(payload.thinking);
      if (!buffer || bufferTask !== event.task_id || buffer.kind !== kind) {
        const phase = kind === "thinking" ? (phases.get(event.task_id) ?? 0) + 1 : undefined;
        if (phase) phases.set(event.task_id, phase);
        buffer = { id, taskID: event.task_id, role: "assistant", kind, content: "", createdAt: time, thinkingStatus: kind === "thinking" ? "streaming" : undefined, turn: turns.get(event.task_id), phase };
        bufferTask = event.task_id;
        messages.push(buffer);
      }
      buffer.content += content;
      if (liveIDs.has(event.id)) buffer.liveRevision = event.id;
      continue;
    }
    if (type === CONVERSATION_EVENT.user && eventRole === "user") {
      buffer = undefined;
      messages.push({ id, taskID: event.task_id, role: "user", kind: "message", content: text(payload.content), createdAt: time, attachments: attachments(payload.attachments) });
    } else if (type === CONVERSATION_EVENT.tool || type === CONVERSATION_EVENT.toolCall || type === CONVERSATION_EVENT.toolResult) {
      buffer = undefined;
      const toolID = toolMessageID(event.task_id, text(payload.tool_id) || text(payload.id) || String(event.id));
      const existing = tools.get(toolID);
      const input = printable(payload.input) || existing?.tool?.input || "";
      const tool = { id: toolID, name: text(payload.tool_name) || text(payload.name) || existing?.tool?.name || "Tool", input, command: toolCommand(input), output: printable(payload.output) || text(payload.preview) || text(payload.error) || existing?.tool?.output || "", status: type === CONVERSATION_EVENT.toolResult ? (payload.is_error === true || payload.error ? "failed" : "completed") as "failed" | "completed" : "running" as const, durationMs: number(payload.duration_ms) || number(payload.elapsed_ms) || elapsed(existing?.createdAt, time) || undefined, computerObservation: computerObservation(payload.computer_observation) ?? existing?.tool?.computerObservation };
      const content = [tool.name, tool.input, tool.output].filter(Boolean).join("\n\n");
      if (existing) { existing.content = content; existing.tool = tool; }
      else {
        const message: SessionMessage = { id: toolID, taskID: event.task_id, role: "assistant", kind: "tool", content, tool, createdAt: time };
        messages.push(message);
        tools.set(toolID, message);
      }
      if (type !== CONVERSATION_EVENT.toolResult && tool.name === "AskUserQuestion" && !questionTools.some((candidate) => candidate.toolID === toolID)) {
        const parsed = parseUserQuestionInput(tool.input);
        questionTools.push({ active: true, prompt: parsed?.question ?? "", taskID: event.task_id, toolID });
      }
      activity.push(`${type}: ${text(payload.tool_name)}`);
      if (type === CONVERSATION_EVENT.toolResult && tool.status === "completed" && IMAGE_TOOLS.has(tool.name)) {
        const result = typeof payload.output === "string" ? eventPayload(payload.output) : object(payload.output);
        appendArtifact(imageAssetID(result), event);
      }
      if (type === CONVERSATION_EVENT.toolResult && tool.status === "failed" && tool.name === "AskUserQuestion" && existing && !claimedQuestionToolIDs.has(toolID)) {
        const legacy = parseUserQuestionInput(tool.input);
        if (legacy) {
          existing.kind = "question";
          existing.content = legacy.question;
          existing.tool = undefined;
          existing.question = { taskID: event.task_id, choices: legacy.choices, status: "unavailable", legacy: true };
        }
      }
      if (type === CONVERSATION_EVENT.toolResult) {
        const candidate = [...questionTools].reverse().find((item) => item.toolID === toolID);
        if (candidate) candidate.active = false;
      }
    } else if (type === CONVERSATION_EVENT.compact) {
      buffer = undefined;
      const content = text(payload.summary) || text(payload.content);
      if (content) messages.push({ id, taskID: event.task_id, role: "assistant", kind: "compact", content, createdAt: time });
    } else if (type === CONVERSATION_EVENT.handoff) {
      buffer = undefined;
      const snapshot = object(payload.package);
      const source = object(snapshot.source);
      const sourceRef = text(source.ref);
      if (/^(tenant|local):.+/.test(sourceRef)) messages.push({ id, taskID: event.task_id, role: "assistant", kind: "handoff", content: text(snapshot.stage_summary), createdAt: time, handoff: { packageID: text(payload.package_id), hashPrefix: text(payload.package_sha256).slice(0, 12), stale: payload.stale === true, sourceRefs: [sourceRef as `tenant:${string}` | `local:${string}`] } });
    } else if (type === CONVERSATION_EVENT.artifact) {
      buffer = undefined;
      appendArtifact(imageAssetID(payload), event);
    } else if (type === CONVERSATION_EVENT.permission) {
      buffer = undefined;
      run.status = "waiting_permission";
      messages.push({ id, taskID: event.task_id, role: "assistant", kind: "message", content: [text(payload.tool_name), text(payload.reason), printable(payload.input)].filter(Boolean).join("\n\n"), createdAt: time, permission: { taskID: event.task_id, requestID: text(payload.request_id), resolved: false } });
      activity.push("permission_request");
    } else if (type === CONVERSATION_EVENT.permissionResolved) {
      run.status = "running";
      for (const message of messages) if (message.permission?.taskID === event.task_id && message.permission.requestID === text(payload.request_id)) message.permission.resolved = true;
    } else if (type === CONVERSATION_EVENT.question) {
      buffer = undefined;
      const requestID = text(payload.request_id);
      const question = text(payload.question);
      if (requestID && question) {
        const explicitToolID = text(payload.tool_id);
        const claimedToolID = claimUserQuestionToolID(questionTools, event.task_id, question, explicitToolID ? toolMessageID(event.task_id, explicitToolID) : "");
        if (claimedToolID) claimedQuestionToolIDs.add(claimedToolID);
        run.status = "waiting_input";
        const message: SessionMessage = {
          id, taskID: event.task_id, role: "assistant", kind: "question", content: question, createdAt: time,
          question: { taskID: event.task_id, requestID, choices: questionChoices(payload.choices), expiresAt: text(payload.expires_at) || undefined, status: "pending" }
        };
        messages.push(message);
        questions.set(`${event.task_id}:${requestID}`, message);
      }
    } else if (type === CONVERSATION_EVENT.questionResolved) {
      const requestID = text(payload.request_id);
      const message = questions.get(`${event.task_id}:${requestID}`);
      const status = questionResolutionStatus(payload.status);
      if (message?.question && status) {
        message.question.status = status;
        message.question.answer = text(payload.answer) || undefined;
      }
      if (message?.question && status && run.status === "waiting_input") {
        run.status = [...questions.values()].some((item) => item.taskID === event.task_id && item.question?.status === "pending") ? "waiting_input" : "running";
      }
    } else if (type === CONVERSATION_EVENT.failed || type === CONVERSATION_EVENT.timeout || type === CONVERSATION_EVENT.cancelled || type === CONVERSATION_EVENT.completed) {
      buffer = undefined;
      run.status = type === CONVERSATION_EVENT.completed ? "completed" : type === CONVERSATION_EVENT.cancelled ? "stopped" : "failed";
      run.endedAt = time;
      activity.push(`Run ${runID}: ${type}`);
      if (type === CONVERSATION_EVENT.failed || type === CONVERSATION_EVENT.timeout) {
        const artifactAvailable = messages.some((message) => message.taskID === event.task_id && Boolean(message.artifacts?.length));
        const nestedError = object(payload.error);
        const errorCode = text(payload.code) || text(payload.error_code) || text(nestedError.code) || undefined;
        const errorMessage = text(payload.error) || text(payload.error_message) || text(nestedError.message) || type;
        messages.push({ id, taskID: event.task_id, role: "assistant", kind: "error", content: errorMessage, createdAt: time, error: { code: errorCode, artifactAvailable } });
      } else if (type === CONVERSATION_EVENT.completed && !messages.some((message) => message.taskID === event.task_id && message.role === "assistant" && message.kind === "message" && !message.permission && message.content)) {
        const response = text(payload.response);
        if (response) messages.push({ id, taskID: event.task_id, role: "assistant", kind: "message", content: response, createdAt: time, liveRevision: liveIDs.has(event.id) ? event.id : undefined });
      }
      for (const message of messages) if (message.permission?.taskID === event.task_id) message.permission.resolved = true;
      for (const message of messages) {
        if (message.question?.taskID !== event.task_id || message.question.status !== "pending") continue;
        message.question.status = type === CONVERSATION_EVENT.cancelled ? "cancelled" : type === CONVERSATION_EVENT.timeout ? "expired" : "unavailable";
      }
    }
  }
  if (["completed", "failed", "stopped", "archived"].includes(detail.status)) {
    for (const message of messages) if (message.question?.status === "pending") message.question.status = "unavailable";
  }
  for (const message of messages) {
    const run = runs.get(String(message.taskID));
    const prior = previousMessages.get(message.id);
    message.liveRevision = Math.max(message.liveRevision ?? 0, prior?.liveRevision ?? 0) || undefined;
    message.status = run?.status;
    if (message.role !== "assistant") continue;
    const meta = metadata.get(message.taskID ?? 0);
    Object.assign(message, meta);
    if (run && (run.status === "completed" || run.status === "failed" || run.status === "stopped")) {
      const measuredDurationMs = elapsed(run.startedAt, run.endedAt);
      if (message.durationMs === undefined || !isReasonableReportedDurationMs(message.durationMs)) message.durationMs = measuredDurationMs;
      if (message.thinkingStatus === "streaming") message.thinkingStatus = run.status === "completed" ? "completed" : run.status === "stopped" ? "stopped" : "failed";
      if (message.tool?.status === "running") message.tool.status = run.status === "completed" ? "completed" : run.status === "stopped" ? "stopped" : "failed";
    }
  }
  return { ...detail, events, cursor: String(events.at(-1)?.id ?? 0), messages, activity, runs: [...runs.values()] };
}

function eventPayload(raw?: string): Record<string, unknown> {
  try { const value: unknown = JSON.parse(raw || "{}"); return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}; } catch { return {}; }
}
function conversationEventRole(type: string | undefined, payload: Record<string, unknown>): "user" | "assistant" {
  if (payload.role === "user" || payload.role === "assistant") return payload.role;
  return type === CONVERSATION_EVENT.user ? "user" : "assistant";
}
function text(value: unknown): string { return typeof value === "string" ? value : ""; }
function object(value: unknown): Record<string, unknown> { return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}; }
function number(value: unknown): number { return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : 0; }
function printable(value: unknown): string { return typeof value === "string" ? value : value && typeof value === "object" ? JSON.stringify(value, null, 2) : ""; }
function elapsed(start?: string, end?: string): number | undefined {
  if (!start || !end) return undefined;
  const delta = Date.parse(end) - Date.parse(start);
  return Number.isFinite(delta) ? Math.max(0, delta) : undefined;
}
function toolCommand(input: string): string {
  const parsed = eventPayload(input);
  return text(parsed.command) || text(parsed.file_path) || text(parsed.path) || text(parsed.pattern) || text(parsed.query) || input;
}
function toolMessageID(taskID: number, toolID: string): string { return `tool:${taskID}:${toolID}`; }
function imageAssetID(payload: Record<string, unknown>): string { return text(object(payload.asset).asset_id) || text(payload.asset_id); }
function questionResolutionStatus(value: unknown): "answered" | "cancelled" | "expired" | undefined {
  return value === "answered" || value === "cancelled" || value === "expired" ? value : undefined;
}
function computerObservation(value: unknown): ComputerObservationImage | undefined {
  const raw = object(value);
  const observationID = text(raw.observation_id);
  const assetID = text(raw.asset_id);
  if (!observationID || !assetID || !text(raw.media_type)) return undefined;
  return { observationID, assetID, mediaType: text(raw.media_type), name: text(raw.name), sizeBytes: number(raw.size_bytes), sha256: text(raw.sha256) };
}

function attachments(value: unknown): PreparedAttachment[] | undefined {
  if (!Array.isArray(value)) return undefined;
  return value.flatMap((item) => {
    const raw = object(item);
    if (!text(raw.type) || !text(raw.media_type)) return [];
    return [{ attachment_id: text(raw.attachment_id) || undefined, type: text(raw.type), media_type: text(raw.media_type), name: text(raw.name), size_bytes: number(raw.size_bytes), sha256: text(raw.sha256), url: text(raw.url) || undefined, inline_data: text(raw.inline_data) || text(raw.data) || undefined, transcript: text(raw.transcript) || undefined }];
  });
}
