import { ApiError, makeHeaders } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import { WORKFLOW_EVENT_TYPES, WORKFLOW_STATUSES, type WorkflowEvent } from "./types";
// Fetch, not EventSource: tenant/device/auth headers are mandatory on every reconnect.
export async function streamWorkflowEvents(identity: IdentityConfig, path: string, onEvent: (event: WorkflowEvent) => void, signal: AbortSignal): Promise<void> {
  const headers = makeHeaders(identity);
  headers.set("Accept", "text/event-stream");
  const response = await fetch(`${identity.apiBase}${path}`, { method: "GET", headers, signal });
  if (!response.ok) throw new ApiError(response.status, await response.text());
  if (!response.body) throw new Error("Workflow event stream has no body");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const deliver = (frame: string) => {
    const data = frame.split(/\r\n|\r|\n/).filter((line) => line.startsWith("data:")).map((line) => line.slice(5).replace(/^ /, "")).join("\n");
    if (!data || signal.aborted) return;
    const event: unknown = JSON.parse(data);
    if (!event || typeof event !== "object" || !("sequence" in event) || !Number.isSafeInteger(event.sequence) || !("type" in event) || typeof event.type !== "string") throw new Error("Invalid Workflow event JSON");
    const raw = event as Record<string, unknown>;
    if (!WORKFLOW_EVENT_TYPES.some((type) => type === raw.type) || typeof raw.created_at !== "string" || (raw.status !== undefined && !WORKFLOW_STATUSES.some((status) => status === raw.status)) || (raw.text !== undefined && typeof raw.text !== "string") || (raw.node_id !== undefined && typeof raw.node_id !== "string") || (raw.attempt_no !== undefined && (!Number.isSafeInteger(raw.attempt_no) || Number(raw.attempt_no) < 0))) throw new Error("Invalid Workflow event JSON");
    onEvent(event as WorkflowEvent);
  };
  try {
    for (;;) {
      const chunk = await reader.read();
      buffer += chunk.done ? decoder.decode() : decoder.decode(chunk.value, { stream: true });
      const separator = /\r\n\r\n|\n\n|\r\r/;
      let boundary = separator.exec(buffer);
      while (boundary) {
        deliver(buffer.slice(0, boundary.index)); buffer = buffer.slice(boundary.index + boundary[0].length);
        boundary = separator.exec(buffer);
      }
      if (chunk.done) { if (buffer.trim()) deliver(buffer); break; }
    }
  } finally { await reader.cancel().catch(() => undefined); reader.releaseLock(); }
}
