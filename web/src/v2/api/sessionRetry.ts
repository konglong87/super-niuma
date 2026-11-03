import { parseWebUIV2Route, webUIV2SessionPath } from "../routes";
import type { SendSessionInput, SessionDetail, SessionMessage, SessionRef } from "../types";
import { SessionControlError } from "./sessionControlClient";

const HANDOFF_EVENT_TYPE = "session_handoff";
const HANDOFF_EVENT_SCHEMA = "niuma.session-handoff-event.v1";
const HANDOFF_PACKAGE_SCHEMA = "niuma.session-handoff.v1";

// Regeneration follows the existing Web Agent behavior: append a new run for
// the original user request, keeping the previous answer available for review.
export function sessionRetryInput(detail: SessionDetail, message: SessionMessage, idempotencyKey: string): SendSessionInput | null {
  if (detail.source !== "tenant" || ["running", "queued", "waiting_permission", "waiting_input", "archived"].includes(detail.status)) return null;
  const index = detail.messages.findIndex((item) => item.id === message.id);
  if (index < 0 || message.role !== "assistant") return null;
  const original = detail.messages.slice(0, index).reverse().find((item) => item.role === "user" && (!message.taskID || item.taskID === message.taskID));
  if (!original || (!original.content.trim() && !original.attachments?.length)) return null;
  return {
    ref: detail.ref,
    text: original.content,
    attachments: (original.attachments ?? []).map((item) => ({ ...item, name: item.name ?? "", sha256: item.sha256 ?? "" })),
    sourceRefs: retrySourceRefs(detail, original.taskID ?? message.taskID),
    idempotencyKey
  };
}

function retrySourceRefs(detail: SessionDetail, taskID?: number): SessionRef[] {
  const handoffs = (detail.events ?? []).filter((event) => event.event_type === HANDOFF_EVENT_TYPE);
  if (handoffs.length > 0 && !taskID) throw invalidHandoff();
  const sources = new Set<SessionRef>();
  for (const event of handoffs) {
    if (event.task_id !== taskID) continue;
    let payload: unknown;
    try { payload = JSON.parse(event.payload_json ?? ""); } catch { throw invalidHandoff(); }
    const envelope = handoffObject(payload);
    const snapshot = handoffObject(envelope.package);
    const source = handoffObject(snapshot.source);
    const target = handoffObject(snapshot.target);
    if (envelope.schema !== HANDOFF_EVENT_SCHEMA || envelope.target_task_id !== taskID || snapshot.schema !== HANDOFF_PACKAGE_SCHEMA || target.ref !== detail.ref) throw invalidHandoff();
    if (typeof envelope.package_id !== "string" || !envelope.package_id.startsWith("handoff:") || envelope.package_id !== snapshot.package_id || typeof envelope.package_sha256 !== "string" || !/^[a-f0-9]{64}$/.test(envelope.package_sha256) || envelope.package_sha256 !== snapshot.package_sha256) throw invalidHandoff();
    const ref = handoffSourceRef(source.ref);
    if (ref !== detail.ref) sources.add(ref);
  }
  // Resend references so the backend reauthorizes sources and captures their
  // current snapshots. Retrying does not replay the old immutable package bytes.
  return [...sources];
}

function handoffObject(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw invalidHandoff();
  return value as Record<string, unknown>;
}

function handoffSourceRef(value: unknown): SessionRef {
  if (typeof value !== "string" || value !== value.trim()) throw invalidHandoff();
  try {
    const route = parseWebUIV2Route(webUIV2SessionPath(value as SessionRef));
    if (route.kind === "session") return route.ref;
  } catch { throw invalidHandoff(); }
  throw invalidHandoff();
}

function invalidHandoff(): SessionControlError {
  return new SessionControlError("invalid_state", "Original session context is invalid");
}
