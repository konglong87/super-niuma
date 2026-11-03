import type { IdentityConfig } from "../../lib/types";
import type { OperationCard, OperationKind, OperationResult, SendSessionInput, SessionDetail, SessionListFilters, SessionMessage, SessionRef, SessionStatus, SessionSummary } from "../types";
import { SessionControlError, type SessionControlClient } from "./sessionControlClient";

type StoredOperation = { fingerprint: string; result: OperationResult };

const fixtureTime = "2026-09-05T00:00:00.000Z";

function cloneSession(session: SessionDetail): SessionDetail {
  return {
    ...session,
    messages: session.messages.map((message) => ({
      ...message,
      operation: message.operation ? { ...message.operation } : undefined,
      handoff: message.handoff ? { ...message.handoff, sourceRefs: [...message.handoff.sourceRefs] } : undefined
    })),
    activity: [...session.activity],
    context: session.context.map((chip) => ({ ...chip })),
    changes: [...session.changes],
    runs: session.runs.map((run) => ({ ...run }))
  };
}

function cloneResult(result: OperationResult, replayed: boolean): OperationResult {
  return { operation: { ...result.operation }, session: cloneSession(result.session), replayed };
}

function summary(session: SessionDetail): SessionSummary {
  const { messages: _messages, activity: _activity, context: _context, changes: _changes, runs: _runs, ...value } = session;
  return value;
}

function initialFixtures(): Map<SessionRef, SessionDetail> {
  return new Map<SessionRef, SessionDetail>([
    ["tenant:alpha", {
      ref: "tenant:alpha",
      source: "tenant",
      title: "Release coordination",
      status: "running",
      updatedAt: fixtureTime,
      shortID: "alpha",
      profileLabel: "Release manager",
      messages: [],
      activity: ["Run is active"],
      context: [],
      changes: [],
      runs: [{ id: "run-alpha", status: "running", startedAt: fixtureTime }]
    }],
    ["tenant:beta", {
      ref: "tenant:beta",
      source: "tenant",
      title: "Design review",
      status: "completed",
      updatedAt: "2026-09-04T16:00:00.000Z",
      shortID: "beta",
      profileLabel: "Design reviewer",
      messages: [
        { id: "fixture-beta-user", role: "user", kind: "message", content: "Please review the navigation states.", createdAt: "2026-09-04T15:01:00.000Z" },
        { id: "fixture-beta-assistant", role: "assistant", kind: "message", content: "The responsive review is complete.", createdAt: "2026-09-04T15:02:00.000Z" },
        { id: "fixture-beta-thinking", role: "assistant", kind: "thinking", content: "Compared desktop and mobile constraints.", createdAt: "2026-09-04T15:03:00.000Z" },
        { id: "fixture-beta-tool", role: "assistant", kind: "tool", content: "Viewport checks completed without overflow.", createdAt: "2026-09-04T15:04:00.000Z" },
        {
          id: "fixture-beta-handoff", role: "assistant", kind: "handoff", content: "Fixture handoff payload", createdAt: "2026-09-04T15:05:00.000Z",
          handoff: { packageID: "fixture-beta-package", hashPrefix: "beta123", stale: false, sourceRefs: ["tenant:alpha"] }
        },
        {
          id: "fixture-beta-profile-operation", role: "assistant", kind: "operation", content: "Fixture profile operation", createdAt: "2026-09-04T15:06:00.000Z",
          operation: { id: "fixture-beta-profile-draft", kind: "profile_draft", status: "completed", title: "Fixture profile title", detail: "Fixture profile detail", createdAt: "2026-09-04T15:06:00.000Z", replayed: false }
        }
      ],
      activity: ["Review completed"],
      context: [],
      changes: [],
      runs: [{ id: "run-beta", status: "completed", startedAt: "2026-09-04T15:00:00.000Z", endedAt: "2026-09-04T16:00:00.000Z" }]
    }],
    ["local:workspace", {
      ref: "local:workspace",
      source: "local",
      title: "Local workspace",
      status: "idle",
      updatedAt: "2026-09-04T12:00:00.000Z",
      shortID: "workspace",
      messages: [],
      activity: ["Read-only local session"],
      context: [],
      changes: [],
      runs: []
    }]
  ]);
}

function requestFingerprint(value: unknown): string {
  return JSON.stringify(value);
}

export function createMockSessionControlClient(): SessionControlClient {
  const sessions = initialFixtures();
  const idempotency = new Map<string, StoredOperation>();
  let sequence = 0;

  function nextID(prefix: string): string {
    sequence += 1;
    return `${prefix}-${String(sequence).padStart(3, "0")}`;
  }

  function nextTimestamp(): string {
    return new Date(Date.parse(fixtureTime) + sequence * 1_000).toISOString();
  }

  function sessionFor(ref: SessionRef): SessionDetail {
    const session = sessions.get(ref);
    if (!session) {
      throw new SessionControlError("not_found");
    }
    return session;
  }

  function mutableSession(ref: SessionRef): SessionDetail {
    const session = sessionFor(ref);
    if (session.source === "local") {
      throw new SessionControlError("local_read_only");
    }
    return session;
  }

  function operation(kind: OperationKind, title: string, detail: string): OperationCard {
    const id = nextID("operation");
    return { id, kind, status: "completed", title, detail, createdAt: nextTimestamp() };
  }

  function message(kind: SessionMessage["kind"], role: SessionMessage["role"], content: string, data: Pick<SessionMessage, "operation" | "handoff"> = {}): SessionMessage {
    const id = nextID("message");
    return { id, kind, role, content, createdAt: nextTimestamp(), ...data };
  }

  function resultFor(operationCard: OperationCard, session: SessionDetail): OperationResult {
    return { operation: { ...operationCard }, session: cloneSession(session), replayed: false };
  }

  function replayOrRun(operationName: string, idempotencyKey: string, fingerprint: string, run: () => OperationResult): OperationResult {
    const key = `${operationName}:${idempotencyKey}`;
    const existing = idempotency.get(key);
    if (existing) {
      if (existing.fingerprint !== fingerprint) {
        throw new SessionControlError("idempotency_conflict");
      }
      return cloneResult(existing.result, true);
    }
    const result = run();
    idempotency.set(key, { fingerprint, result: cloneResult(result, false) });
    return cloneResult(result, false);
  }

  return {
    async list(_identity: IdentityConfig, filters: SessionListFilters): Promise<SessionSummary[]> {
      const query = filters.query.trim().toLowerCase();
      const statuses = new Set(filters.statuses);
      return Array.from(sessions.values())
        .filter((session) => statuses.size === 0 || statuses.has(session.status))
        .filter((session) => query === "" || session.title.toLowerCase().includes(query) || session.ref.toLowerCase().includes(query))
        .map((session) => summary(cloneSession(session)));
    },

    async get(_identity: IdentityConfig, ref: SessionRef): Promise<SessionDetail> {
      return cloneSession(sessionFor(ref));
    },

    async create(_identity: IdentityConfig, input): Promise<OperationResult> {
      const title = input.title.trim();
      return replayOrRun("create", input.idempotencyKey, requestFingerprint({ title, initialText: input.initialText?.trim() ?? "" }), () => {
        if (!title) {
          throw new SessionControlError("invalid_state");
        }
        const ref = `tenant:${nextID("session")}` as SessionRef;
        const createdAt = nextTimestamp();
        const session: SessionDetail = {
          ref,
          source: "tenant",
          title,
          status: "idle",
          updatedAt: createdAt,
          shortID: ref.slice("tenant:".length),
          messages: [],
          activity: ["Session created"],
          context: [],
          changes: [],
          runs: []
        };
        const initialText = input.initialText?.trim();
        if (initialText) {
          session.messages.push(message("message", "user", initialText));
        }
        const operationCard = operation("create", "Session created", "Managed session is ready");
        session.messages.push(message("operation", "assistant", "Session created", { operation: operationCard }));
        session.updatedAt = operationCard.createdAt;
        sessions.set(ref, session);
        return resultFor(operationCard, session);
      });
    },

    async send(_identity: IdentityConfig, input: SendSessionInput): Promise<OperationResult> {
      const text = input.text.trim();
      const sourceRefs = [...input.sourceRefs];
      const attachments = input.attachments.map((attachment) => ({ name: attachment.name, size: attachment.size_bytes, type: attachment.type }));
      return replayOrRun("send", input.idempotencyKey, requestFingerprint({ ref: input.ref, text, sourceRefs, attachments }), () => {
        if (!text && attachments.length === 0) {
          throw new SessionControlError("invalid_state");
        }
        const session = mutableSession(input.ref);
        session.messages.push(message("message", "user", text));
        if (sourceRefs.length > 0) {
          session.messages.push(message("handoff", "assistant", "Context attached at send time", {
            handoff: {
              packageID: nextID("handoff"),
              hashPrefix: `mock-${sourceRefs.length}`,
              stale: false,
              sourceRefs: [...sourceRefs]
            }
          }));
        }
        const operationCard = operation("send", "Pending input queued", "Input was queued for the active run");
        session.messages.push(message("operation", "assistant", "Pending input queued", { operation: operationCard }));
        session.activity.push("Pending input queued");
        session.updatedAt = operationCard.createdAt;
        return resultFor(operationCard, session);
      });
    },

    async stop(_identity: IdentityConfig, input): Promise<OperationResult> {
      return replayOrRun("stop", input.idempotencyKey, requestFingerprint({ ref: input.ref }), () => {
        const session = mutableSession(input.ref);
        const stoppedAt = nextTimestamp();
        session.runs = session.runs.map((run) => run.status === "running" || run.status === "queued" || run.status === "waiting_input" ? { ...run, status: "stopped" as SessionStatus, endedAt: stoppedAt } : run);
        session.status = "stopped";
        const operationCard = operation("stop", "Run stopped", "Active run was stopped");
        session.messages.push(message("operation", "assistant", "Run stopped", { operation: operationCard }));
        session.activity.push("Run stopped");
        session.updatedAt = operationCard.createdAt;
        return resultFor(operationCard, session);
      });
    },

    async archive(_identity: IdentityConfig, input): Promise<OperationResult> {
      return replayOrRun("archive", input.idempotencyKey, requestFingerprint({ ref: input.ref }), () => {
        const session = mutableSession(input.ref);
        session.status = "archived";
        const operationCard = operation("archive", "Session archived", "Managed session was archived");
        session.messages.push(message("operation", "assistant", "Session archived", { operation: operationCard }));
        session.activity.push("Session archived");
        session.updatedAt = operationCard.createdAt;
        return resultFor(operationCard, session);
      });
    },

    async rename(_identity: IdentityConfig, input): Promise<void> {
      const session = mutableSession(input.ref);
      const title = input.title.trim();
      if (!title) throw new SessionControlError("invalid_request");
      if (!Number.isSafeInteger(input.id) || input.id <= 0) throw new SessionControlError("invalid_request");
      session.title = title;
    }
  };
}
