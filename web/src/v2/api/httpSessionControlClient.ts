import { makeHeaders, streamSSEEndpoint } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import type { OperationKind, OperationResult, SendSessionInput, SessionChannel, SessionDetail, SessionListFilters, SessionRef, SessionRunConfig, SessionSource, SessionStatus, SessionSummary } from "../types";
import { SessionControlError, type SessionControlClient } from "./sessionControlClient";
import { applyConversationEvents } from "./sessionEventReducer";

const sessionControlPath = "/tenant/session-control/sessions";

type SessionWire = {
  id?: number;
  active_run_id?: number;
  ref: string;
  source?: string;
  title?: string;
  status?: string;
  updated_at?: string;
  short_id?: string;
  model?: string;
  provider?: string;
  channel?: { provider?: string; account_key?: string };
  permission_mode?: string;
  effort?: string;
  prompt_mode?: string;
  cwd?: string;
};

type ConversationPage = { session: SessionWire; events: NonNullable<SessionDetail["events"]>; cursor: string; has_more: boolean };

type OperationWire = {
  operation_id?: string;
  replayed?: boolean;
  session?: SessionWire;
};

type RequestOptions = {
  method?: "GET" | "POST" | "PATCH";
  body?: unknown;
  idempotencyKey?: string;
  signal?: AbortSignal;
  dataEnvelope?: boolean;
};

export function createHTTPSessionControlClient(): SessionControlClient {
  return {
    async list(identity, filters, signal) {
      const [tenant, local] = await Promise.all([
        request<unknown>(identity, `${sessionControlPath}?source=tenant`, { signal }),
        request<unknown>(identity, `${sessionControlPath}?source=local`, { signal })
      ]);
      if (!Array.isArray(tenant) || !Array.isArray(local)) throw unavailable();
      return filterSessions([...tenant, ...local].map((value) => sessionSummary(value)), filters);
    },

    async get(identity, ref, signal) {
      if (ref.startsWith("local:")) return sessionDetail(await request<unknown>(identity, sessionPath(ref), { signal }));
      let detail: SessionDetail | undefined;
      let cursor = "0";
      for (;;) {
        const page = await request<ConversationPage>(identity, `${sessionPath(ref)}/conversation?cursor=${cursor}`, { signal });
        validateConversationPage(page);
        detail = applyConversationEvents({ ...sessionDetail(page.session), events: detail?.events }, page.events);
        if (!page.has_more) break;
        if (BigInt(page.cursor) <= BigInt(cursor)) throw unavailable();
        cursor = page.cursor;
      }
      return { ...detail, historyLoaded: true };
    },

    async subscribe(identity, sessions, onPage, signal) {
      await streamSSEEndpoint<ConversationPage | { type: "error" }>(identity, "/tenant/session-control/conversations/stream", { method: "POST", body: { sessions } }, {
        onEvent(page) {
          if ("type" in page) throw unavailable();
          validateConversationPage(page);
          onPage(sessionDetail(page.session), page.events);
        }, onDone() {}
      }, signal);
    },

    async create(identity, input) {
      const data = await request<unknown>(identity, sessionControlPath, {
        method: "POST",
        body: {
          title: input.title.trim(),
          ...(input.model?.trim() ? { model: input.model.trim() } : {}),
          ...(input.provider?.trim() ? { provider: input.provider.trim() } : {}),
          ...(input.cwd?.trim() ? { cwd: input.cwd.trim() } : {}),
          ...runtimeOptions(input),
          ...(input.initialText?.trim() ? { initial_text: input.initialText.trim() } : {})
        },
        idempotencyKey: input.idempotencyKey
      });
      return operationResult(data, "create");
    },

    async send(identity, input) {
      const data = await request<unknown>(identity, `${sessionPath(input.ref)}/messages`, {
        method: "POST",
        body: {
          content: input.text.trim(),
          attachments: input.attachments.map(sessionAttachment),
          source_refs: [...input.sourceRefs],
          ...runtimeOptions(input)
        },
        idempotencyKey: input.idempotencyKey
      });
      return operationResult(data, "send");
    },

    async compact(identity, input) {
      const data = await request<unknown>(identity, `${sessionPath(input.ref)}/compact`, {
        method: "POST",
        body: {},
        idempotencyKey: input.idempotencyKey
      });
      return operationResult(data, "compact");
    },

    async stop(identity, input) {
      const data = await request<unknown>(identity, `${sessionPath(input.ref)}/stop`, {
        method: "POST",
        body: {},
        idempotencyKey: input.idempotencyKey
      });
      return operationResult(data, "stop");
    },

    async rename(identity, input) {
      if (!input.ref.startsWith("tenant:")) throw new SessionControlError("local_read_only");
      if (!Number.isSafeInteger(input.id) || input.id <= 0 || !input.title.trim()) throw new SessionControlError("invalid_request");
      const result = await request<unknown>(identity, `/tenant/sessions/${input.id}`, {
        method: "PATCH",
        body: { title: input.title.trim() },
        dataEnvelope: false
      });
      if (objectValue(result)?.id !== input.id) throw unavailable();
    },

    async archive() {
      // The isolated Session Control API intentionally has no archive route.
      throw new SessionControlError("invalid_state");
    }
  };
}

function validateConversationPage(page: ConversationPage): void {
  if (!page?.session || !Array.isArray(page.events) || !/^\d+$/.test(page.cursor) || typeof page.has_more !== "boolean") throw unavailable();
}

function sessionAttachment(attachment: SendSessionInput["attachments"][number]): Record<string, unknown> {
  return {
    ...(attachment.attachment_id ? { attachment_id: attachment.attachment_id } : {}),
    type: attachment.type,
    media_type: attachment.media_type,
    name: attachment.name,
    size_bytes: attachment.size_bytes,
    sha256: attachment.sha256,
    ...(attachment.url ? { url: attachment.url } : {}),
    ...(!attachment.attachment_id && !attachment.url && attachment.inline_data ? { inline_data: attachment.inline_data } : {})
  };
}

async function request<T>(identity: IdentityConfig, path: string, options: RequestOptions): Promise<T> {
  const headers = makeHeaders(identity);
  if (options.idempotencyKey) headers.set("Idempotency-Key", options.idempotencyKey);
  let response: Response;
  try {
    response = await fetch(`${identity.apiBase}${path}`, {
      method: options.method ?? "GET",
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal
    });
  } catch (error) {
    if (isAbortError(error)) throw error;
    throw unavailable();
  }
  const body = await responseText(response);
  if (!response.ok) throw errorFromBody(body);
  const value = parseJSON(body);
  if (options.dataEnvelope === false) return value as T;
  if (!isDataEnvelope(value)) throw unavailable();
  return value.data as T;
}

function sessionPath(ref: SessionRef): string {
  const separator = ref.indexOf(":");
  const source = ref.slice(0, separator);
  const id = ref.slice(separator + 1);
  if ((source !== "tenant" && source !== "local") || !id) throw new SessionControlError("invalid_state");
  return `${sessionControlPath}/${encodeURIComponent(source)}/${encodeURIComponent(id)}`;
}

function sessionSummary(value: unknown): SessionSummary {
  const wire = sessionWire(value);
  const { source, id } = sessionRef(wire);
  const channel = sessionChannel(wire.channel);
  return {
    ref: `${source}:${id}`,
    source,
    title: stringValue(wire.title),
    status: sessionStatus(wire.status),
    updatedAt: stringValue(wire.updated_at),
    shortID: stringValue(wire.short_id) || id,
    ...(wire.id ? { id: wire.id } : {}),
    ...(wire.active_run_id ? { activeRunID: wire.active_run_id } : {}),
    permissionMode: wire.permission_mode, effort: wire.effort, promptMode: wire.prompt_mode,
    ...(wire.model ? { model: wire.model } : {}), ...(wire.provider ? { provider: wire.provider } : {}), ...(wire.cwd ? { cwd: wire.cwd } : {}),
    ...(channel ? { channel } : {})
  };
}

function sessionChannel(value: SessionWire["channel"]): SessionChannel | undefined {
  if (!value || typeof value.provider !== "string" || value.provider.trim() === "") return undefined;
  return {
    provider: value.provider.trim(),
    ...(typeof value.account_key === "string" && value.account_key.trim() !== "" ? { accountKey: value.account_key.trim() } : {})
  };
}

function runtimeOptions(input: SessionRunConfig): Record<string, string> {
  const fields = { provider: input.provider, model: input.model, permission_mode: input.permissionMode, effort: input.effort, prompt_mode: input.promptMode };
  return Object.fromEntries(Object.entries(fields).filter((entry): entry is [string, string] => typeof entry[1] === "string" && entry[1].trim() !== "").map(([key, value]) => [key, value.trim()]));
}

function sessionDetail(value: unknown): SessionDetail {
  return { ...sessionSummary(value), messages: [], activity: [], context: [], changes: [], runs: [] };
}

function operationResult(value: unknown, kind: OperationKind): OperationResult {
  const wire = objectValue(value) as OperationWire | null;
  if (!wire?.session || typeof wire.operation_id !== "string" || wire.operation_id.trim() === "") throw unavailable();
  const session = sessionDetail(wire.session);
  return {
    operation: {
      id: wire.operation_id,
      kind,
      status: "completed",
      title: "",
      detail: "",
      createdAt: session.updatedAt,
      replayed: wire.replayed === true
    },
    session,
    replayed: wire.replayed === true
  };
}

function sessionWire(value: unknown): SessionWire {
  const wire = objectValue(value) as SessionWire | null;
  if (!wire || typeof wire.ref !== "string") throw unavailable();
  return wire;
}

function sessionRef(wire: SessionWire): { source: SessionSource; id: string } {
  const separator = wire.ref.indexOf(":");
  const source = wire.source ?? wire.ref.slice(0, separator);
  const id = wire.ref.slice(separator + 1);
  if ((source !== "tenant" && source !== "local") || !id) throw unavailable();
  return { source, id };
}

function sessionStatus(value: unknown): SessionStatus {
  const statuses: SessionStatus[] = ["idle", "queued", "running", "waiting_permission", "waiting_input", "blocked", "completed", "failed", "stopped", "archived"];
  return typeof value === "string" && statuses.includes(value as SessionStatus) ? value as SessionStatus : "idle";
}

function filterSessions(sessions: SessionSummary[], filters: SessionListFilters): SessionSummary[] {
  const query = filters.query.trim().toLowerCase();
  const statuses = new Set(filters.statuses);
  return sessions
    .filter((session) => statuses.size === 0 || statuses.has(session.status))
    .filter((session) => query === "" || session.title.toLowerCase().includes(query) || session.ref.toLowerCase().includes(query) || session.cwd?.toLowerCase().includes(query));
}

function errorFromBody(body: string): SessionControlError {
  const parsed = parseJSON(body);
  const record = objectValue(parsed);
  const nested = record ? objectValue(record.error) : null;
  const code = stringValue(record?.code) || stringValue(record?.error) || stringValue(nested?.code);
  const known = ["forbidden", "not_found", "invalid_request", "service_unavailable", "workspace_unavailable", "provider_route_invalid", "invalid_state", "local_read_only", "idempotency_conflict", "budget_exceeded"] as const;
  const safeCode = known.includes(code as typeof known[number]) ? code as typeof known[number] : "network_unavailable";
  const message = safeCode === "budget_exceeded" ? stringValue(nested?.message) || stringValue(record?.message) || safeCode : safeCode;
  return new SessionControlError(safeCode, message);
}

function isDataEnvelope(value: unknown): value is { data: unknown } {
  const record = objectValue(value);
  return record !== null && Object.hasOwn(record, "data");
}

function objectValue(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function parseJSON(body: string): unknown {
  try {
    return JSON.parse(body) as unknown;
  } catch {
    return null;
  }
}

async function responseText(response: Response): Promise<string> {
  try {
    return await response.text();
  } catch {
    return "";
  }
}

function unavailable(): SessionControlError {
  return new SessionControlError("network_unavailable");
}

function isAbortError(error: unknown): boolean {
  return typeof error === "object" && error !== null && "name" in error && error.name === "AbortError";
}
