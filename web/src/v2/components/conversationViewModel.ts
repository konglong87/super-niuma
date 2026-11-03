import { buildPermissionRequests, buildSubAgentProgress, collectNextStepSuggestions, summarizeActivity, summarizeUsage } from "../../components/WebAgentPage";
import { mergeEvents } from "../../lib/agentEvents";
import { isReasonableReportedDurationMs } from "../../lib/messages";
import type { AgentTaskRecord, IdentityConfig, WebAgentConversationDetail } from "../../lib/types";
import type { ConversationEvent, SessionDetail, SessionMessage, SessionRef, SessionStatus } from "../types";

const TERMINAL_STATUSES = new Set<SessionStatus>(["completed", "failed", "stopped", "archived"]);
const USAGE_KEYS = ["total_tokens", "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"];

export type ConversationRunView = {
  id: string; status: SessionStatus; startedAt: string; endedAt?: string;
  provider?: string; model?: string; durationMs?: number; traceID?: string; permissionMode?: string;
  usage: ReturnType<typeof summarizeUsage>; hasUsage: boolean; hasContext: boolean;
  usageFields: string[];
};

export function conversationEvents(detail: SessionDetail, runtimeDetails?: WebAgentConversationDetail): ConversationEvent[] {
  return mergeEvents(runtimeDetails?.events ?? [], detail.events ?? []);
}

export function collectConversationNextSteps(events: ConversationEvent[]): string[] {
  return collectNextStepSuggestions(events.map((event) => {
    if (event.event_type !== "message" && event.event_type !== "next_steps") return event;
    const payload = parseEventPayload(event.payload_json);
    if (event.event_type === "message" && !payload.from_agent) payload.from_agent = "user";
    if (event.event_type === "next_steps") payload.suggestions = stringList(payload.suggestions);
    return { ...event, payload_json: JSON.stringify(payload) };
  }));
}

// Task snapshots are authoritative for historical model identity. SSE events
// may be newer than that snapshot, so terminal state and usage fold afterward.
export function buildConversationRuns(detail: SessionDetail, runtimeDetails?: WebAgentConversationDetail): ConversationRunView[] {
  const eventsByTask = new Map<string, ConversationEvent[]>();
  for (const event of conversationEvents(detail, runtimeDetails)) {
    const id = String(event.task_id);
    const entries = eventsByTask.get(id) ?? [];
    entries.push(event);
    eventsByTask.set(id, entries);
  }
  const tasks = new Map((runtimeDetails?.tasks ?? []).map((task) => [String(task.id), task]));
  const projections = new Map(detail.runs.map((run) => [run.id, run]));
  const ids = new Set([...projections.keys(), ...tasks.keys(), ...eventsByTask.keys()]);
  return [...ids].map((id) => buildRun(id, tasks.get(id), projections.get(id), eventsByTask.get(id) ?? [])).sort((a, b) => (Date.parse(a.startedAt) || 0) - (Date.parse(b.startedAt) || 0) || (Number(a.id) || 0) - (Number(b.id) || 0));
}

function buildRun(id: string, task: AgentTaskRecord | undefined, projection: SessionDetail["runs"][number] | undefined, events: ConversationEvent[]): ConversationRunView {
  const metadata = parseEventPayload(task?.metadata_json);
  const result = parseEventPayload(task?.result_json);
  const startedEvent = events.find((event) => event.event_type === "started");
  const started = parseEventPayload(startedEvent?.payload_json);
  const terminal = [...events].reverse().find((event) => ["completed", "failed", "cancelled", "timeout"].includes(event.event_type ?? ""));
  const terminalPayload = parseEventPayload(terminal?.payload_json);
  const records = [metadata, result, ...events.map((event) => parseEventPayload(event.payload_json))];
  const normalizedEvents = events.map((event) => ({ ...event, payload_json: JSON.stringify(usageRecord(parseEventPayload(event.payload_json))) }));
  const normalizedTask = task ? { ...task, metadata_json: JSON.stringify(usageRecord(metadata)), result_json: JSON.stringify(usageRecord(result)) } : null;
  const usage = summarizeUsage(normalizedTask, normalizedEvents);
  const measured = Object.assign({}, ...records.map(usageRecord)) as Record<string, unknown>;
  const hasUsage = records.some((record) => USAGE_KEYS.some((key) => typeof usageRecord(record)[key] === "number"));
  const hasInputUsage = records.some((record) => ["input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"].some((key) => typeof usageRecord(record)[key] === "number"));
  const hasContext = typeof measured.context_percent === "number" || ((numeric(measured.context_length) ?? 0) > 0 && hasInputUsage);
  const contextTokens = usage.inputTokens + usage.cacheReadTokens + usage.cacheCreationTokens;
  usage.contextPercent = typeof measured.context_percent === "number" ? Math.max(0, Math.min(100, measured.context_percent)) : measured.context_length && hasInputUsage ? Math.min(100, Math.ceil(contextTokens * 100 / Number(measured.context_length))) : 0;
  const calls = new Set(events.filter((event) => event.event_type === "tool_call" || event.event_type === "tool_use").map((event) => stringValue(parseEventPayload(event.payload_json).tool_id) || String(event.id)));
  usage.toolCalls = Math.max(calls.size, numeric(measured.tool_calls) ?? 0);
  const startedAt = task?.started_at || startedEvent?.created_at || projection?.startedAt || events[0]?.created_at || "";
  const endedAt = terminal?.created_at || task?.finished_at || projection?.endedAt;
  const status = terminal ? statusValue(terminal.event_type) : projection?.status ?? statusValue(task?.status);
  const measuredDurationMs = isTerminalStatus(status) ? elapsedMilliseconds(startedAt, endedAt) : undefined;
  const reportedDurationMs = [numeric(terminalPayload.duration_ms), numeric(result.duration_ms)].find((value): value is number => isReasonableReportedDurationMs(value));
  const durationMs = reportedDurationMs ?? measuredDurationMs;
  return {
    id, status, startedAt, endedAt, durationMs, usage, hasUsage, hasContext, usageFields: Object.keys(measured),
    provider: stringValue(terminalPayload.provider) || stringValue(started.provider) || stringValue(metadata.provider) || undefined,
    model: stringValue(terminalPayload.model) || stringValue(started.model) || task?.model || stringValue(metadata.model) || undefined,
    permissionMode: stringValue(metadata.permission_mode) || stringValue(started.permission_mode) || undefined,
    traceID: task?.trace_id || stringValue(started.trace_id) || undefined
  };
}

export function messageWithRuntime(message: SessionMessage, run?: ConversationRunView): SessionMessage {
  if (!run) return message;
  return { ...message, status: run.status, provider: run.provider ?? message.provider, model: run.model ?? message.model, durationMs: run.durationMs ?? message.durationMs, tokens: run.hasUsage ? run.usage.totalTokens : message.tokens };
}

export function conversationRuntimeMetrics(detail: SessionDetail, runtimeDetails?: WebAgentConversationDetail) {
  const runs = buildConversationRuns(detail, runtimeDetails);
  const latest = runs.find((run) => run.id === String(detail.activeRunID)) ?? runs.at(-1);
  if (!latest) return {};
  const usage = latest.usage;
  const cacheInput = usage.inputTokens + usage.cacheReadTokens + usage.cacheCreationTokens;
  return { contextPercent: latest.hasContext ? usage.contextPercent : undefined, cacheHitPercent: latest.hasUsage && cacheInput > 0 ? Math.round(usage.cacheReadTokens * 100 / cacheInput) : undefined, totalTokens: latest.hasUsage ? usage.totalTokens : undefined };
}

export function buildConversationInspection(detail: SessionDetail, runtimeDetails?: WebAgentConversationDetail) {
  const events = conversationEvents(detail, runtimeDetails);
  const activity = summarizeActivity(null, events, Number.POSITIVE_INFINITY);
  const outputs = new Map<string, string>();
  for (const event of events) {
    if (event.event_type !== "tool_result") continue;
    const payload = parseEventPayload(event.payload_json);
    const output = stringValue(payload.output);
    if (/^diff --git |^--- .+\n\+\+\+ /m.test(output)) outputs.set(`${event.task_id}:${stringValue(payload.tool_id)}`, output);
  }
  const fileEvents = events.flatMap((event) => {
    if (event.event_type !== "file_change") return [];
    const payload = parseEventPayload(event.payload_json);
    const path = stringValue(payload.path);
    if (!path) return [];
    return [{ id: event.id, taskID: event.task_id, createdAt: event.created_at, path, access: stringValue(payload.access), beforeLines: numeric(payload.before_lines), afterLines: numeric(payload.after_lines), diff: outputs.get(`${event.task_id}:${stringValue(payload.tool_id)}`) }];
  });
  const grouped = new Map<number, ConversationEvent[]>();
  for (const event of events) {
    const taskEvents = grouped.get(event.task_id) ?? [];
    taskEvents.push(event);
    grouped.set(event.task_id, taskEvents);
  }
  const runs = buildConversationRuns(detail, runtimeDetails);
  const runStatuses = new Map(runs.map((run) => [run.id, run.status]));
  return {
    activity, fileEvents, subAgents: buildSubAgentProgress(events),
    permissions: [...grouped.values()].flatMap(buildPermissionRequests).map((permission) => ({ ...permission, active: !isTerminalStatus(runStatuses.get(String(permission.taskID))) })),
    handoffs: buildConversationHandoffs(events), runs
  };
}

function buildConversationHandoffs(events: ConversationEvent[]) {
  return events.flatMap((event) => {
    if (event.event_type !== "session_handoff") return [];
    const payload = parseEventPayload(event.payload_json);
    const snapshot = objectValue(payload.package);
    const source = objectValue(snapshot.source);
    const sourceRef = stringValue(source.ref);
    if (!/^(tenant|local):.+/.test(sourceRef)) return [];
    return [{ id: event.id, taskID: event.task_id, sourceRef: sourceRef as SessionRef, capturedAt: stringValue(source.captured_at), cursor: stringValue(source.cursor), hash: stringValue(payload.package_sha256), packageID: stringValue(payload.package_id), objective: stringValue(snapshot.objective), summary: stringValue(snapshot.stage_summary), completed: stringList(snapshot.completed), openItems: stringList(snapshot.open_items), risks: stringList(snapshot.risks), nextActions: stringList(snapshot.next_actions), estimatedTokens: numeric(objectValue(snapshot.budget).estimated_tokens) }];
  });
}

export function conversationTraceURL(identity: IdentityConfig, detail: SessionDetail): string | undefined {
  const sessionID = detail.id ? String(detail.id) : detail.source === "local" ? detail.ref.slice("local:".length) : "";
  if (!sessionID) return undefined;
  const params = new URLSearchParams({ token: identity.apiToken, source: detail.source, session_id: sessionID, tenant_key: identity.tenantKey, user_id: identity.userId });
  if (identity.deviceId) params.set("device_id", identity.deviceId);
  return `${identity.apiBase.replace(/\/api$/, "")}/trace?${params.toString()}`;
}

export function isTerminalStatus(status?: SessionStatus): boolean { return status ? TERMINAL_STATUSES.has(status) : false; }
export function elapsedMilliseconds(start?: string, end?: string | number): number | undefined {
  if (!start || end === undefined) return undefined;
  const startMs = Date.parse(start);
  const endMs = typeof end === "number" ? end : Date.parse(end);
  return Number.isFinite(startMs) && Number.isFinite(endMs) ? Math.max(0, endMs - startMs) : undefined;
}
export function parseEventPayload(raw?: string): Record<string, unknown> { try { return objectValue(JSON.parse(raw || "{}")); } catch { return {}; } }
function objectValue(value: unknown): Record<string, unknown> { return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}; }
function stringValue(value: unknown): string { return typeof value === "string" ? value : ""; }
function stringList(value: unknown): string[] { return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string").map((item) => item.trim()).filter(Boolean) : []; }
function numeric(value: unknown): number | undefined { return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined; }
function usageRecord(record: Record<string, unknown>): Record<string, unknown> { return { ...record, ...objectValue(record.usage) }; }
function statusValue(value?: string): SessionStatus {
  if (value === "cancelled") return "stopped";
  if (value === "timeout") return "failed";
  return (["idle", "queued", "running", "waiting_permission", "waiting_input", "blocked", "completed", "failed", "stopped", "archived"] as const).find((status) => status === value) ?? "idle";
}
