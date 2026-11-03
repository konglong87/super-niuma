import type { AgentUserQuestion } from "../lib/types";

export type SessionSource = "tenant" | "local";
export type SessionStatus = "idle" | "queued" | "running" | "waiting_permission" | "waiting_input" | "blocked" | "completed" | "failed" | "stopped" | "archived";
export type SessionRef = `${SessionSource}:${string}`;
export type SessionMessageKind = "message" | "thinking" | "tool" | "handoff" | "operation" | "error" | "compact" | "question";
export type OperationKind = "create" | "send" | "compact" | "stop" | "attach" | "archive" | "profile_draft";
export type SessionControlErrorCode = "forbidden" | "not_found" | "invalid_request" | "service_unavailable" | "workspace_unavailable" | "provider_route_invalid" | "invalid_state" | "local_read_only" | "idempotency_conflict" | "budget_exceeded" | "network_unavailable" | "stream_disconnected";
export type SessionRunConfig = { provider?: string; model?: string; permissionMode?: string; effort?: string; promptMode?: string };
export type SessionChannel = { provider: string; accountKey?: string };

export type SessionSummary = {
  id?: number;
  activeRunID?: number;
  ref: SessionRef;
  source: SessionSource;
  title: string;
  status: SessionStatus;
  updatedAt: string;
  shortID: string;
  profileLabel?: string;
  model?: string;
  provider?: string;
  channel?: SessionChannel;
  permissionMode?: string;
  effort?: string;
  promptMode?: string;
  cwd?: string;
};

export type ContextChip = { sourceRef: SessionRef; title: string; status: "ready" | "stale" };
export type OperationCard = { id: string; kind: OperationKind; status: "pending" | "completed" | "failed"; title: string; detail: string; createdAt: string; replayed?: boolean };
export type ConversationEvent = { id: number; task_id: number; event_type?: string; payload_json?: string; created_at?: string };
export type ThinkingMode = "full" | "summary" | "hidden";
export type ComputerObservationImage = { observationID: string; assetID: string; mediaType: string; name: string; sizeBytes: number; sha256: string };
export type MessageTool = { id: string; name: string; input: string; command: string; output: string; status: "running" | "completed" | "failed" | "stopped"; durationMs?: number; computerObservation?: ComputerObservationImage };
export type SessionMessage = {
  id: string; role: "user" | "assistant"; kind: SessionMessageKind; content: string; createdAt: string;
  taskID?: number; provider?: string; model?: string; durationMs?: number; tokens?: number;
  stageDurationMs?: number; stageEndedAt?: string; turn?: number; phase?: number;
  status?: SessionStatus; thinkingStatus?: "streaming" | "completed" | "failed" | "stopped";
  liveRevision?: number; tool?: MessageTool; attachments?: PreparedAttachment[]; artifacts?: Array<{ asset_id: string }>;
  permission?: { taskID: number; requestID: string; resolved: boolean }; operation?: OperationCard;
  question?: AgentUserQuestion; error?: { code?: string; artifactAvailable?: boolean };
  handoff?: { packageID: string; hashPrefix: string; stale: boolean; sourceRefs: SessionRef[] };
};
export type SessionDetail = SessionSummary & { historyLoaded?: boolean; events?: ConversationEvent[]; cursor?: string; messages: SessionMessage[]; activity: string[]; context: ContextChip[]; changes: string[]; runs: Array<{ id: string; status: SessionStatus; startedAt: string; endedAt?: string }> };
export type SessionListFilters = { query: string; statuses: SessionStatus[] };
export type CreateSessionInput = SessionRunConfig & { title: string; initialText?: string; cwd?: string; idempotencyKey: string };
export type PreparedAttachment = {
  attachment_id?: string;
  type: string;
  media_type: string;
  name: string;
  size_bytes: number;
  sha256: string;
  url?: string;
  inline_data?: string;
  transcript?: string;
};
export type SendSessionInput = SessionRunConfig & { ref: SessionRef; text: string; attachments: PreparedAttachment[]; sourceRefs: SessionRef[]; idempotencyKey: string };
export type OperationResult = { operation: OperationCard; session: SessionDetail; replayed: boolean };
