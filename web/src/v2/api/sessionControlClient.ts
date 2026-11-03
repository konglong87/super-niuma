import { createContext, createElement, useContext, type ReactNode } from "react";
import type { IdentityConfig } from "../../lib/types";
import type { CreateSessionInput, OperationResult, SendSessionInput, SessionControlErrorCode, SessionDetail, SessionListFilters, SessionRef, SessionSummary } from "../types";

export type RenameSessionInput = { ref: SessionRef; id: number; title: string };

export type SessionControlClient = {
  subscribe?(identity: IdentityConfig, sessions: Array<{ref: SessionRef; cursor: string}>, onPage: (summary: SessionDetail, events: NonNullable<SessionDetail["events"]>) => void, signal: AbortSignal): Promise<void>;
  list(identity: IdentityConfig, filters: SessionListFilters, signal?: AbortSignal): Promise<SessionSummary[]>;
  get(identity: IdentityConfig, ref: SessionRef, signal?: AbortSignal): Promise<SessionDetail>;
  create(identity: IdentityConfig, input: CreateSessionInput): Promise<OperationResult>;
  send(identity: IdentityConfig, input: SendSessionInput): Promise<OperationResult>;
  compact?(identity: IdentityConfig, input: { ref: SessionRef; idempotencyKey: string }): Promise<OperationResult>;
  stop(identity: IdentityConfig, input: { ref: SessionRef; idempotencyKey: string }): Promise<OperationResult>;
  archive(identity: IdentityConfig, input: { ref: SessionRef; idempotencyKey: string }): Promise<OperationResult>;
  rename?(identity: IdentityConfig, input: RenameSessionInput): Promise<void>;
};

export class SessionControlError extends Error {
  readonly code: SessionControlErrorCode;

  constructor(code: SessionControlErrorCode, message: string = code) {
    super(message);
    this.name = "SessionControlError";
    this.code = code;
  }
}

export function sessionControlErrorCode(error: unknown): SessionControlErrorCode {
  return error instanceof SessionControlError ? error.code : "network_unavailable";
}

function normalizedFilters(filters: SessionListFilters): SessionListFilters {
  return {
    query: filters.query.trim().toLowerCase(),
    statuses: Array.from(new Set(filters.statuses)).sort()
  };
}

export const sessionControlQueryKeys = {
  list(identity: IdentityConfig, filters: SessionListFilters) {
    return ["session-control", "list", identity.tenantKey, identity.userId, normalizedFilters(filters), identity.apiBase] as const;
  },
  detail(identity: IdentityConfig, ref: SessionRef) {
    return ["session-control", "detail", identity.tenantKey, identity.userId, ref, identity.apiBase] as const;
  }
};

const SessionControlClientContext = createContext<SessionControlClient | null>(null);

export function SessionControlClientProvider({ client, children }: { client: SessionControlClient; children: ReactNode }) {
  return createElement(SessionControlClientContext.Provider, { value: client }, children);
}

export function useSessionControlClient(): SessionControlClient {
  const client = useContext(SessionControlClientContext);
  if (!client) {
    throw new Error("SessionControlClientProvider is required");
  }
  return client;
}
