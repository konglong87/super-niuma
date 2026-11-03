import type { SessionDetail, SessionStatus, SessionSummary } from "../types";
import { applyConversationEvents } from "./sessionEventReducer";

const TERMINAL_STATUSES = new Set<SessionStatus>(["completed", "failed", "stopped", "archived"]);

// HTTP readback and SSE arrive independently. Their durable event/task IDs order
// snapshots even when timestamp precision cannot distinguish two transitions.
export function mergeSessionDetail(previous: SessionDetail | undefined, next: SessionDetail): SessionDetail {
  if (!previous?.events || !next.events) return next;
  const summary = isOlderSummary(previous, next) ? summaryFields(previous) : summaryFields(next);
  const merged = applyConversationEvents({ ...next, ...summary, historyLoaded: previous.historyLoaded || next.historyLoaded || undefined }, previous.events);
  const liveRevisions = new Map(previous.messages.map((message) => [message.id, message.liveRevision ?? 0]));
  return { ...merged, messages: merged.messages.map((message) => ({ ...message, liveRevision: Math.max(message.liveRevision ?? 0, liveRevisions.get(message.id) ?? 0) || undefined })) };
}

function isOlderSummary(previous: SessionDetail, next: SessionDetail): boolean {
  const previousRun = latestRunID(previous);
  const nextRun = latestRunID(next);
  if (previousRun !== nextRun) return previousRun > nextRun;
  const previousCursor = latestEventID(previous);
  const nextCursor = latestEventID(next);
  if (previousCursor !== nextCursor) return previousCursor > nextCursor;
  const previousTime = Date.parse(previous.updatedAt) || 0;
  const nextTime = Date.parse(next.updatedAt) || 0;
  if (previousTime !== nextTime) return previousTime > nextTime;
  if (previous.status === "archived" && next.status !== "archived") return true;
  return TERMINAL_STATUSES.has(previous.status) && !TERMINAL_STATUSES.has(next.status);
}

function latestRunID(detail: SessionDetail): number {
  return (detail.events ?? []).reduce((latest, event) => Math.max(latest, event.task_id), detail.activeRunID ?? 0);
}

function latestEventID(detail: SessionDetail): number {
  return (detail.events ?? []).reduce((latest, event) => Math.max(latest, event.id), 0);
}

function summaryFields(detail: SessionDetail): SessionSummary {
  return {
    ref: detail.ref, source: detail.source, title: detail.title, status: detail.status, updatedAt: detail.updatedAt,
    shortID: detail.shortID, id: detail.id, activeRunID: detail.activeRunID, profileLabel: detail.profileLabel,
    model: detail.model, provider: detail.provider, cwd: detail.cwd, permissionMode: detail.permissionMode, effort: detail.effort, promptMode: detail.promptMode
  };
}
