import type {
  IdentityConfig,
  ImageArtifact,
  ImageGenerationRecord,
  ImageGenerationRequest,
  ImageCapabilityEntry,
  AgentTaskSSEEvent,
  AgentTaskCreateRequest,
  AgentTaskEventRecord,
  AgentTaskMessageRequest,
  AgentTaskPermissionResolveRequest,
  AgentTaskRecord,
  AgentSlashCommand,
  AgentTaskUpdateRequest,
  AgentWorkspaceValidation,
  PromptTemplate,
  AgentProfileDocument,
  AgentProfileRecord,
  EffectiveAgentProfile,
  AgentProfileAssignment,
  AgentProfileChannelBinding,
  AgentProfileConversationCatalog,
  AgentTeamPolicy,
  AgentTeamRecord,
  AgentTeamMember,
  AgentTeamBinding,
  AgentTeamRun,
  AgentTeamRunTimeline,
  ChannelAccountRecord,
  AutoMemoryReviewRequest,
  DocumentRecord,
  AttachmentPresignResponse,
  KnowledgeChunkRecord,
  KnowledgeDocumentRecord,
  LocalSkillRecord,
  GoalCreateRequest,
  GoalEvidence,
  GoalEventRecord,
  GoalPlan,
  GoalRecord,
  GoalRunResponse,
  GlobalSettingsResponse,
  GlobalSettingsPromoteResponse,
  GlobalSettingsSaveResponse,
  SettingsDoc,
  MemoryRecord,
  MobileAttachment,
  MobileSessionDetail,
  MobileMessageEvent,
  ProfileRecord,
  OpenAIModelsResponse,
  ProviderListResponse,
  ProviderOption,
  ProvisioningRecord,
  ProvisioningOverview,
  RuntimeBackgroundJob,
  RuntimeBackgroundLogs,
  RuntimeBackgroundStopResponse,
  RuntimeLoopRequest,
  RuntimeRunRecord,
  RuntimeSchedulerEvent,
  ServerStatus,
  SkillOverrideRecord,
  SkillPackageResult,
  SkillPackageVerifyResult,
  SkillRecord,
  SkillRollbackResult,
  TenantRecord,
  TenantQuotaConfig,
  TenantQuotaEvent,
  TenantUsageDaily,
  TenantUsageLedger,
  TenantUserRecord,
  TenantMessage,
  TenantSession,
  TelemetryRecord,
  TraceDetail,
  TraceSessionSummary,
  WebAgentConversation,
  WebAgentConversationDetail,
  PendingInputRecord,
  PendingInputSideChatResponse
} from "./types";

type RequestOptions = {
  method?: string;
  body?: unknown;
  mobile?: boolean;
  signal?: AbortSignal;
  traceId?: string;
  headers?: Record<string, string>;
};

export const DEFAULT_STREAM_TIMEOUT_MS = 120_000;

export class ApiError extends Error {
  status: number;
  body: string;

  constructor(status: number, body: string) {
    super(apiErrorMessage(status, body));
    this.status = status;
    this.body = body;
  }
}

export function apiErrorMessage(status: number, body: string): string {
  const fallback = body || `request failed with status ${status}`;
  if (!body) {
    return fallback;
  }
  try {
    const parsed = JSON.parse(body) as unknown;
    if (parsed && typeof parsed === "object") {
      const raw = parsed as Record<string, unknown>;
      const message = raw.error || raw.message || raw.detail;
      if (typeof message === "string" && message.trim() !== "") {
        return message;
      }
    }
  } catch {
    // Plain text API errors should be shown as-is.
  }
  return fallback;
}

export function makeHeaders(identity: IdentityConfig, mobile = false, traceId = ""): Headers {
  const headers = new Headers();
  headers.set("Content-Type", "application/json");
  headers.set("X-Tenant-Key", identity.tenantKey);
  headers.set("X-User-Id", identity.userId);
  headers.set("X-Device-Id", identity.deviceId);
  headers.set("X-Trace-Id", traceId.trim() || `webui-${Date.now().toString(36)}`);
  if (mobile && identity.mobileJwt.trim() !== "") {
    headers.set("Authorization", `Bearer ${identity.mobileJwt.trim()}`);
  } else if (identity.apiToken.trim() !== "") {
    headers.set("Authorization", `Bearer ${identity.apiToken.trim()}`);
  }
  return headers;
}

export async function apiRequest<T>(identity: IdentityConfig, path: string, options: RequestOptions = {}): Promise<T> {
  const headers = makeHeaders(identity, options.mobile, options.traceId);
  for (const [key, value] of Object.entries(options.headers || {})) {
    headers.set(key, value);
  }
  const response = await fetch(`${identity.apiBase}${path}`, {
    method: options.method || "GET",
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal
  });
  if (!response.ok) {
    throw new ApiError(response.status, await response.text());
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export async function listPendingInputs(identity: IdentityConfig, taskId: number, signal?: AbortSignal): Promise<PendingInputRecord[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/agent-tasks/${taskId}/pending-inputs`, { signal });
  return unwrapData<PendingInputRecord[]>(value, []);
}

export async function addPendingInput(identity: IdentityConfig, taskId: number, request: { client_input_id: string; content: string; direction?: string; attachments?: AgentTaskMessageRequest["attachments"] }, signal?: AbortSignal): Promise<PendingInputRecord> {
  const value = await apiRequest<{ data: PendingInputRecord }>(identity, `/tenant/agent-tasks/${taskId}/pending-inputs`, {
    method: "POST",
    body: request,
    signal,
    headers: { "Idempotency-Key": request.client_input_id }
  });
  return value.data;
}

export async function updatePendingInput(identity: IdentityConfig, taskId: number, inputId: string, patch: { content?: string; direction?: string }, signal?: AbortSignal): Promise<PendingInputRecord> {
  const value = await apiRequest<{ data: PendingInputRecord }>(identity, `/tenant/agent-tasks/${taskId}/pending-inputs/${encodeURIComponent(inputId)}`, { method: "PATCH", body: patch, signal });
  return value.data;
}

export async function movePendingInputUp(identity: IdentityConfig, taskId: number, inputId: string, signal?: AbortSignal): Promise<PendingInputRecord> {
  const value = await apiRequest<{ data: PendingInputRecord }>(identity, `/tenant/agent-tasks/${taskId}/pending-inputs/${encodeURIComponent(inputId)}/move-up`, { method: "POST", signal });
  return value.data;
}

export async function cancelPendingInput(identity: IdentityConfig, taskId: number, inputId: string, signal?: AbortSignal): Promise<void> {
  await apiRequest(identity, `/tenant/agent-tasks/${taskId}/pending-inputs/${encodeURIComponent(inputId)}`, { method: "DELETE", signal });
}

export async function retryPendingInput(identity: IdentityConfig, taskId: number, inputId: string, signal?: AbortSignal): Promise<PendingInputRecord> {
  const value = await apiRequest<{ data: PendingInputRecord }>(identity, `/tenant/agent-tasks/${taskId}/pending-inputs/${encodeURIComponent(inputId)}/retry`, { method: "POST", signal });
  return value.data;
}

export async function createPendingInputSideChat(identity: IdentityConfig, taskId: number, inputId: string, signal?: AbortSignal): Promise<PendingInputSideChatResponse> {
  return apiRequest(identity, `/tenant/agent-tasks/${taskId}/pending-inputs/${encodeURIComponent(inputId)}/side-chat`, { method: "POST", signal });
}

export async function getPendingInputQueueSettings(identity: IdentityConfig, taskId: number, signal?: AbortSignal): Promise<{ enabled: boolean }> {
  return apiRequest(identity, `/tenant/agent-tasks/${taskId}/pending-input-settings`, { signal });
}

export async function setPendingInputQueueEnabled(identity: IdentityConfig, taskId: number, enabled: boolean, signal?: AbortSignal): Promise<{ enabled: boolean }> {
  return apiRequest(identity, `/tenant/agent-tasks/${taskId}/pending-input-settings`, { method: "PATCH", body: { enabled }, signal });
}

export async function generateImage(
  identity: IdentityConfig,
  sessionId: number,
  request: ImageGenerationRequest,
  signal?: AbortSignal
): Promise<{ asset: ImageArtifact }> {
  return apiRequest<{ asset: ImageArtifact }>(identity, `/tenant/sessions/${sessionId}/images/generations`, {
    method: "POST",
    body: request,
    signal
  });
}

export async function getImageCapabilities(identity: IdentityConfig, signal?: AbortSignal): Promise<ImageCapabilityEntry[]> {
  return unwrapData<ImageCapabilityEntry[]>(await apiRequest<{ data: ImageCapabilityEntry[] }>(identity, "/tenant/images/capabilities", { signal }), []);
}

export type ImageEditRequest = ImageGenerationRequest & {
  image?: File;
  mask?: File;
};

export async function editImage(
  identity: IdentityConfig,
  sessionId: number,
  request: ImageEditRequest,
  signal?: AbortSignal
): Promise<{ asset: ImageArtifact }> {
  const form = new FormData();
  for (const key of ["prompt", "provider", "model", "quality", "size", "resolution", "aspect_ratio", "output_format", "background", "watermark", "source_asset_id", "idempotency_key"] as const) {
    const value = request[key];
    if (value !== undefined && value !== "") form.set(key, String(value));
  }
  if (request.image) form.set("image", request.image, request.image.name);
  if (request.mask) form.set("mask", request.mask, request.mask.name);
  const headers = makeHeaders(identity);
  headers.delete("Content-Type");
  const response = await fetch(`${identity.apiBase}/tenant/sessions/${sessionId}/images/edits`, {
    method: "POST",
    headers,
    body: form,
    signal
  });
  if (!response.ok) throw new ApiError(response.status, await response.text());
  return (await response.json()) as { asset: ImageArtifact };
}

export async function listImageHistory(identity: IdentityConfig, sessionId: number): Promise<ImageGenerationRecord[]> {
  return unwrapData<ImageGenerationRecord[]>(await apiRequest<unknown>(identity, `/tenant/sessions/${sessionId}/images`), []);
}

export async function getImageArtifact(identity: IdentityConfig, assetId: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(`${identity.apiBase}/tenant/media/assets/${encodeURIComponent(assetId)}`, {
    headers: makeHeaders(identity),
    signal
  });
  if (!response.ok) throw new ApiError(response.status, await response.text());
  return response.blob();
}

export function unwrapData<T>(value: unknown, fallback: T): T {
  if (Array.isArray(value)) {
    return value as T;
  }
  if (value && typeof value === "object" && "data" in value) {
    const data = (value as { data: T | null }).data;
    return data === null ? fallback : data;
  }
  return fallback;
}

export async function getStatus(identity: IdentityConfig): Promise<ServerStatus> {
  const status = await apiRequest<ServerStatus>(identity, "/status");
  return { ...status, workspace: status.workspace?.trim() || status.cwd?.trim() };
}

export async function listModels(identity: IdentityConfig): Promise<string[]> {
  const response = await apiRequest<OpenAIModelsResponse>(identity, "/v1/models");
  return (response.data ?? []).map((model) => (model.id ?? "").trim()).filter((id) => id !== "");
}

export async function listProviders(identity: IdentityConfig): Promise<ProviderOption[]> {
  const response = await apiRequest<ProviderListResponse>(identity, "/v1/providers");
  return (response.data ?? [])
    .map((provider) => ({ name: (provider.name ?? "").trim(), model: (provider.model ?? "").trim() }))
    .filter((provider) => provider.name !== "");
}

export async function listAgentSlashCommands(identity: IdentityConfig, cwd: string, prefix: string, limit = 8): Promise<AgentSlashCommand[]> {
  const params = new URLSearchParams();
  if (cwd.trim() !== "") {
    params.set("cwd", cwd.trim());
  }
  params.set("prefix", prefix);
  params.set("limit", String(limit));
  const value = await apiRequest<unknown>(identity, `/agent/slash-commands?${params.toString()}`);
  return unwrapData<AgentSlashCommand[]>(value, []);
}

export async function listTenants(identity: IdentityConfig): Promise<TenantRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/tenants?limit=100");
  return unwrapData<TenantRecord[]>(value, []);
}

export async function listTenantUsers(identity: IdentityConfig): Promise<TenantUserRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/users?limit=100");
  return unwrapData<TenantUserRecord[]>(value, []);
}

export async function saveTenantUser(identity: IdentityConfig, record: TenantUserRecord): Promise<void> {
  await apiRequest(identity, "/tenant/users", {
    method: "POST",
    body: {
      user_key: record.user_key,
      email: record.email,
      display_name: record.display_name,
      role: record.role,
      status: record.status,
      user_info_json: record.user_info_json,
      metadata_json: record.metadata_json
    }
  });
}

export async function listMobileSessions(identity: IdentityConfig): Promise<TenantSession[]> {
  const value = await apiRequest<unknown>(identity, "/mobile/chat/sessions?limit=50", { mobile: true });
  return unwrapData<TenantSession[]>(value, []);
}

export async function listTenantSessions(identity: IdentityConfig, limit = 100): Promise<TenantSession[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/sessions?limit=${limit}`);
  return unwrapData<TenantSession[]>(value, []);
}

export async function listWebAgentConversations(identity: IdentityConfig, limit = 100): Promise<WebAgentConversation[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/web-agent/conversations?limit=${limit}`);
  return unwrapData<WebAgentConversation[]>(value, []);
}

export async function getWebAgentConversation(identity: IdentityConfig, conversationId: string, options: { limit?: number; eventLimit?: number } = {}): Promise<WebAgentConversationDetail> {
  const params = new URLSearchParams();
  params.set("limit", String(options.limit || 100));
  params.set("event_limit", String(options.eventLimit || 500));
  return apiRequest<WebAgentConversationDetail>(identity, `/tenant/web-agent/conversations/${encodeURIComponent(conversationId)}?${params.toString()}`);
}

export async function createTenantSession(
  identity: IdentityConfig,
  request: Pick<TenantSession, "session_key" | "title" | "status" | "model" | "cwd" | "metadata_json">
): Promise<number> {
  const value = await apiRequest<unknown>(identity, "/tenant/sessions", {
    method: "POST",
    body: request
  });
  if (value && typeof value === "object" && typeof (value as Record<string, unknown>).id === "number") {
    return (value as { id: number }).id;
  }
  throw new Error("create tenant session response did not include an id");
}

export async function createMobileSession(identity: IdentityConfig, title: string): Promise<number> {
  const value = await apiRequest<unknown>(identity, "/mobile/chat/sessions", {
    method: "POST",
    mobile: true,
    body: {
      title,
      model: identity.model,
      session_key: `webui-${crypto.randomUUID()}`
    }
  });
  if (typeof value === "number") {
    return value;
  }
  if (value && typeof value === "object") {
    const raw = value as Record<string, unknown>;
    const id = raw.id || raw.session_id || (raw.data && typeof raw.data === "object" ? (raw.data as Record<string, unknown>).id : undefined);
    if (typeof id === "number") {
      return id;
    }
  }
  throw new Error("create session response did not include an id");
}

export async function getMobileSession(identity: IdentityConfig, sessionId: number): Promise<MobileSessionDetail> {
  return apiRequest<MobileSessionDetail>(identity, `/mobile/chat/sessions/${sessionId}`, { mobile: true });
}

export async function updateMobileSession(
  identity: IdentityConfig,
  sessionId: number,
  patch: Pick<TenantSession, "title" | "status" | "model" | "cwd" | "metadata_json">
): Promise<void> {
  await apiRequest(identity, `/mobile/chat/sessions/${sessionId}`, {
    method: "PATCH",
    mobile: true,
    body: patch
  });
}

export async function archiveMobileSession(identity: IdentityConfig, sessionId: number): Promise<void> {
  await apiRequest(identity, `/mobile/chat/sessions/${sessionId}`, {
    method: "DELETE",
    mobile: true
  });
}

export async function listMobileMessages(identity: IdentityConfig, sessionId: number): Promise<TenantMessage[]> {
  const value = await apiRequest<unknown>(identity, `/mobile/chat/sessions/${sessionId}/messages?limit=100`, { mobile: true });
  return unwrapData<TenantMessage[]>(value, []);
}

export async function cancelMobileMessage(identity: IdentityConfig, sessionId: number, messageId: number): Promise<void> {
  await apiRequest(identity, `/mobile/chat/sessions/${sessionId}/messages/${messageId}/cancel`, {
    method: "POST",
    mobile: true,
    body: {}
  });
}

export async function regenerateMobileMessage(
  identity: IdentityConfig,
  sessionId: number,
  messageId: number,
  callbacks: {
    onEvent: (event: MobileMessageEvent) => void;
    onDone: () => void;
  },
  signal?: AbortSignal,
  timeoutMs = DEFAULT_STREAM_TIMEOUT_MS
): Promise<void> {
  await streamMobileEndpoint(
    identity,
    `/mobile/chat/sessions/${sessionId}/messages/${messageId}/regenerate`,
    {
      model: identity.model,
      message_key: `webui-regen-${crypto.randomUUID()}`
    },
    callbacks,
    signal,
    timeoutMs
  );
}

export async function branchMobileSession(identity: IdentityConfig, sessionId: number): Promise<number> {
  const value = await apiRequest<unknown>(identity, `/mobile/chat/sessions/${sessionId}/branch`, {
    method: "POST",
    mobile: true,
    body: { title: `Branch ${new Date().toLocaleString()}` }
  });
  if (value && typeof value === "object") {
    const raw = value as Record<string, unknown>;
    const id = raw.id || raw.session_id;
    if (typeof id === "number") {
      return id;
    }
  }
  throw new Error("branch response did not include an id");
}

export async function streamMobileMessage(
  identity: IdentityConfig,
  sessionId: number,
  content: string,
  attachments: MobileAttachment[],
  callbacks: {
    onEvent: (event: MobileMessageEvent) => void;
    onDone: () => void;
  },
  signal?: AbortSignal,
  timeoutMs = DEFAULT_STREAM_TIMEOUT_MS
): Promise<void> {
  await streamMobileEndpoint(
    identity,
    `/mobile/chat/sessions/${sessionId}/messages/stream`,
    {
      content,
      model: identity.model,
      message_key: `webui-msg-${crypto.randomUUID()}`,
      attachments
    },
    callbacks,
    signal,
    timeoutMs
  );
}

export async function presignMobileAttachment(
  identity: IdentityConfig,
  attachment: Pick<MobileAttachment, "type" | "media_type" | "name" | "size_bytes" | "sha256">,
  signal?: AbortSignal
): Promise<AttachmentPresignResponse> {
  return apiRequest<AttachmentPresignResponse>(identity, "/mobile/chat/attachments/presign", {
    method: "POST",
    mobile: true,
    body: attachment,
    signal
  });
}

export async function uploadAttachmentBinary(uploadURL: string, file: File, signal?: AbortSignal, extraHeaders?: Record<string, string>): Promise<void> {
  const response = await fetch(uploadURL, {
    method: "PUT",
    headers: { ...(file.type ? { "Content-Type": file.type } : {}), ...(extraHeaders || {}) },
    body: file,
    signal
  });
  if (!response.ok) {
    throw new ApiError(response.status, await response.text());
  }
}

async function streamMobileEndpoint(
  identity: IdentityConfig,
  path: string,
  body: unknown,
  callbacks: {
    onEvent: (event: MobileMessageEvent) => void;
    onDone: () => void;
  },
  signal?: AbortSignal,
  timeoutMs = DEFAULT_STREAM_TIMEOUT_MS
): Promise<void> {
  await streamSSEEndpoint<MobileMessageEvent>(
    identity,
    path,
    {
      method: "POST",
      mobile: true,
      body
    },
    callbacks,
    signal,
    timeoutMs
  );
}

export async function streamSSEEndpoint<TEvent>(
  identity: IdentityConfig,
  path: string,
  request: {
    method: string;
    mobile?: boolean;
    body?: unknown;
  },
  callbacks: {
    onEvent: (event: TEvent) => void;
    onDone: () => void;
  },
  signal?: AbortSignal,
  timeoutMs = DEFAULT_STREAM_TIMEOUT_MS
): Promise<void> {
  const timeoutController = new AbortController();
  const timeout = window.setTimeout(() => timeoutController.abort(new DOMException("stream timed out", "TimeoutError")), timeoutMs);
  const combinedSignal = combineSignals(signal, timeoutController.signal);
  try {
    const response = await fetch(`${identity.apiBase}${path}`, {
      method: request.method,
      headers: makeHeaders(identity, request.mobile),
      body: request.body === undefined ? undefined : JSON.stringify(request.body),
      signal: combinedSignal
    });
    if (!response.ok || !response.body) {
      throw new ApiError(response.status, await response.text());
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const frames = buffer.split("\n\n");
      buffer = frames.pop() || "";
      for (const frame of frames) {
        const event = parseSSEFrame<TEvent>(frame);
        if (event) {
          callbacks.onEvent(event);
        }
      }
    }

    const tail = parseSSEFrame<TEvent>(buffer);
    if (tail) {
      callbacks.onEvent(tail);
    }
    callbacks.onDone();
  } catch (err) {
    if (timeoutController.signal.aborted) {
      throw new DOMException("stream timed out", "TimeoutError");
    }
    throw err;
  } finally {
    window.clearTimeout(timeout);
  }
}

function combineSignals(signal: AbortSignal | undefined, timeoutSignal: AbortSignal): AbortSignal {
  if (!signal) {
    return timeoutSignal;
  }
  if (typeof AbortSignal.any === "function") {
    return AbortSignal.any([signal, timeoutSignal]);
  }
  const controller = new AbortController();
  const abort = (event: Event) => {
    const source = event.target as AbortSignal;
    controller.abort(source.reason);
  };
  signal.addEventListener("abort", abort, { once: true });
  timeoutSignal.addEventListener("abort", abort, { once: true });
  return controller.signal;
}

export function parseSSEFrame<TEvent = MobileMessageEvent>(frame: string): TEvent | null {
  const data = frame
    .split(/\r?\n/)
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trim())
    .join("");
  if (!data || data === "[DONE]") {
    return null;
  }
  try {
    const parsed = JSON.parse(data) as unknown;
    const eventName = frame
      .split(/\r?\n/)
      .find((line) => line.startsWith("event:"))
      ?.slice(6)
      .trim();
    if (eventName === "agent_task_event" && parsed && typeof parsed === "object") {
      return { type: "agent_task_event", event: parsed } as TEvent;
    }
    if (eventName === "connected" && parsed && typeof parsed === "object") {
      return { type: "connected", ...(parsed as Record<string, unknown>) } as TEvent;
    }
    if (eventName === "error" && parsed && typeof parsed === "object") {
      return { type: "error", ...(parsed as Record<string, unknown>) } as TEvent;
    }
    return parsed as TEvent;
  } catch {
    return { type: "error", error: data } as TEvent;
  }
}

export async function listMemories(identity: IdentityConfig): Promise<MemoryRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/memories?limit=20");
  return unwrapData<MemoryRecord[]>(value, []);
}

export async function listTeamMemory(identity: IdentityConfig): Promise<MemoryRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/team-memory?limit=20");
  return unwrapData<MemoryRecord[]>(value, []);
}

export async function listManagedMemory(identity: IdentityConfig): Promise<MemoryRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/managed-memory?limit=20");
  return unwrapData<MemoryRecord[]>(value, []);
}

export type SaveMemoryRequest = {
  memory_key: string;
  content: string;
  category?: string;
  importance?: number;
  metadata_json?: string;
  embedding_ref?: string;
  source?: string;
};

export async function saveMemory(identity: IdentityConfig, record: SaveMemoryRequest): Promise<void> {
  await apiRequest(identity, "/tenant/memories", { method: "POST", body: record });
}

export async function saveTeamMemory(identity: IdentityConfig, record: MemoryRecord): Promise<void> {
  await apiRequest(identity, "/tenant/team-memory", { method: "POST", body: record });
}

export async function saveManagedMemory(identity: IdentityConfig, record: MemoryRecord): Promise<void> {
  await apiRequest(identity, "/tenant/managed-memory", { method: "POST", body: record });
}

export async function listAutoMemoryCandidates(identity: IdentityConfig): Promise<MemoryRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/automem/candidates?limit=50");
  return unwrapData<MemoryRecord[]>(value, []);
}

export type MemoryReviewCandidateFilters = {
  candidateType?: string;
  riskStatus?: string;
  sourceSessionId?: number | null;
  limit?: number;
};

export async function listMemoryReviewCandidates(identity: IdentityConfig, filters: MemoryReviewCandidateFilters = {}): Promise<MemoryRecord[]> {
  const params = new URLSearchParams();
  params.set("limit", String(filters.limit || 50));
  if (filters.candidateType && filters.candidateType !== "all") {
    params.set("candidate_type", filters.candidateType);
  }
  if (filters.riskStatus && filters.riskStatus !== "all") {
    params.set("risk_status", filters.riskStatus);
  }
  if (filters.sourceSessionId) {
    params.set("source_session_id", String(filters.sourceSessionId));
  }
  const value = await apiRequest<unknown>(identity, `/tenant/memory-review/candidates?${params.toString()}`);
  return unwrapData<MemoryRecord[]>(value, []);
}

export async function reviewAutoMemory(identity: IdentityConfig, request: AutoMemoryReviewRequest): Promise<void> {
  await apiRequest(identity, "/tenant/automem/review", {
    method: "POST",
    body: request
  });
}

export async function reviewMemoryCandidate(identity: IdentityConfig, request: AutoMemoryReviewRequest): Promise<void> {
  await apiRequest(identity, "/tenant/memory-review/review", {
    method: "POST",
    body: request
  });
}

export async function getProfile(identity: IdentityConfig): Promise<ProfileRecord | null> {
  try {
    const value = await apiRequest<unknown>(identity, "/tenant/profile");
    if (value && typeof value === "object" && "data" in value) {
      return (value as { data: ProfileRecord }).data;
    }
    return value as ProfileRecord;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return null;
    }
    throw err;
  }
}

export async function saveProfile(identity: IdentityConfig, profileJson: string, summary: string, profileVersion?: number): Promise<void> {
  await apiRequest(identity, "/tenant/profile", {
    method: "POST",
    body: {
      profile_json: profileJson,
      summary,
      profile_version: profileVersion
    }
  });
}

export async function listAgentProfiles(identity: IdentityConfig, status = "", limit = 100): Promise<AgentProfileRecord[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (status) params.set("status", status);
  return unwrapData<AgentProfileRecord[]>(await apiRequest<unknown>(identity, `/tenant/agent-profiles?${params.toString()}`), []);
}

export async function getAgentProfile(identity: IdentityConfig, profileKey: string, version?: number): Promise<AgentProfileRecord> {
  const params = version ? `?version=${version}` : "";
  return apiRequest<AgentProfileRecord>(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}${params}`);
}

export async function saveAgentProfile(identity: IdentityConfig, request: { profile_key: string; scope: string; display_name: string; description?: string; profile_version?: number; status?: string; config: AgentProfileDocument }, profileKey?: string): Promise<AgentProfileRecord> {
  return apiRequest<AgentProfileRecord>(identity, `/tenant/agent-profiles${profileKey ? `/${encodeURIComponent(profileKey)}` : ""}`, {
    method: profileKey ? "PATCH" : "POST",
    body: request
  });
}

export async function validateAgentProfile(identity: IdentityConfig, request: { profile_key?: string; display_name?: string; config: AgentProfileDocument }, profileKey?: string): Promise<{ valid: boolean; issues?: Array<{ code: string; field: string; message: string; severity?: string }> }> {
  return apiRequest(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey || request.profile_key || "_draft")}/validate`, { method: "POST", body: request });
}

export async function getAgentProfileEffective(identity: IdentityConfig, profileKey: string, options: { version?: number; surface?: string; language?: string; output_style?: string; effort?: string; max_turns?: number; max_tokens?: number } = {}): Promise<EffectiveAgentProfile> {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(options)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return apiRequest<EffectiveAgentProfile>(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/effective${suffix}`);
}

export async function publishAgentProfile(identity: IdentityConfig, profileKey: string, version: number): Promise<{ profile_key: string; profile_version: number; status: string }> {
  return apiRequest(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/publish?version=${version}`, { method: "POST" });
}

export async function archiveAgentProfile(identity: IdentityConfig, profileKey: string, version: number): Promise<void> {
  await apiRequest(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/archive?version=${version}`, { method: "POST" });
}

export async function rollbackAgentProfile(identity: IdentityConfig, profileKey: string, version: number): Promise<AgentProfileRecord> {
  return apiRequest<AgentProfileRecord>(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/rollback?version=${version}`, { method: "POST" });
}

export async function getAgentProfileBinding(identity: IdentityConfig, profileKey: string, version: number): Promise<AgentProfileChannelBinding> {
  return apiRequest<AgentProfileChannelBinding>(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/bot-binding?version=${version}`);
}

export async function getAgentProfileConversations(identity: IdentityConfig, profileKey: string, version?: number, limit = 50): Promise<AgentProfileConversationCatalog> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (version) params.set("version", String(version));
  return apiRequest<AgentProfileConversationCatalog>(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/conversations?${params.toString()}`);
}

export async function saveAgentProfileBinding(identity: IdentityConfig, profileKey: string, version: number, request: { account_id: number; provider: string; binding_key: string }): Promise<AgentProfileChannelBinding> {
  return apiRequest<AgentProfileChannelBinding>(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/bot-binding?version=${version}`, { method: "PUT", body: { ...request, profile_version: version } });
}

export async function archiveAgentProfileBinding(identity: IdentityConfig, profileKey: string, version: number): Promise<void> {
  await apiRequest(identity, `/tenant/agent-profiles/${encodeURIComponent(profileKey)}/bot-binding?version=${version}`, { method: "DELETE" });
}

export async function getAgentProfileAssignment(identity: IdentityConfig, surface: string): Promise<AgentProfileAssignment> {
  return apiRequest<AgentProfileAssignment>(identity, `/tenant/agent-profile-assignment?surface=${encodeURIComponent(surface)}`);
}

export async function saveAgentProfileAssignment(identity: IdentityConfig, request: { surface: string; profile_id: number; user_id?: number }): Promise<AgentProfileAssignment> {
  return apiRequest<AgentProfileAssignment>(identity, "/tenant/agent-profile-assignment", { method: "PUT", body: request });
}

export async function listAgentTeams(identity: IdentityConfig, status = "", limit = 100): Promise<AgentTeamRecord[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (status) params.set("status", status);
  return unwrapData<AgentTeamRecord[]>(await apiRequest<unknown>(identity, `/tenant/agent-teams?${params.toString()}`), []);
}

export async function getAgentTeam(identity: IdentityConfig, teamKey: string, version?: number): Promise<AgentTeamRecord> {
  const params = version ? `?version=${version}` : "";
  return apiRequest<AgentTeamRecord>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}${params}`);
}

export async function saveAgentTeam(identity: IdentityConfig, request: { team_key: string; scope: string; display_name: string; description?: string; team_version?: number; status?: string; schema_version: number; policy: AgentTeamPolicy }, teamKey?: string): Promise<AgentTeamRecord> {
  return apiRequest<AgentTeamRecord>(identity, `/tenant/agent-teams${teamKey ? `/${encodeURIComponent(teamKey)}` : ""}`, { method: teamKey ? "PATCH" : "POST", body: request });
}

export async function validateAgentTeam(identity: IdentityConfig, teamKey: string, request: { policy: AgentTeamPolicy; members: AgentTeamMember[]; bindings: AgentTeamBinding[] }): Promise<{ valid: boolean; issues?: Array<{ code: string; field?: string; message: string }> }> {
  return apiRequest(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/validate`, { method: "POST", body: request });
}

export async function publishAgentTeam(identity: IdentityConfig, teamKey: string, version: number): Promise<void> {
  await apiRequest(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/publish?version=${version}`, { method: "POST" });
}

export async function archiveAgentTeam(identity: IdentityConfig, teamKey: string, version: number): Promise<void> {
  await apiRequest(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/archive?version=${version}`, { method: "POST" });
}

export async function rollbackAgentTeam(identity: IdentityConfig, teamKey: string, version: number): Promise<AgentTeamRecord> {
  return apiRequest<AgentTeamRecord>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/rollback?version=${version}`, { method: "POST" });
}

export async function listAgentTeamMembers(identity: IdentityConfig, teamKey: string, version: number): Promise<AgentTeamMember[]> {
  return unwrapData<AgentTeamMember[]>(await apiRequest<unknown>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/members?version=${version}`), []);
}

export async function saveAgentTeamMembers(identity: IdentityConfig, teamKey: string, version: number, members: AgentTeamMember[]): Promise<AgentTeamMember[]> {
  return unwrapData<AgentTeamMember[]>(await apiRequest<unknown>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/members?version=${version}`, { method: "PUT", body: members }), []);
}

export async function listAgentTeamBindings(identity: IdentityConfig, teamKey: string, version: number): Promise<AgentTeamBinding[]> {
  return unwrapData<AgentTeamBinding[]>(await apiRequest<unknown>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/bindings?version=${version}`), []);
}

export async function saveAgentTeamBindings(identity: IdentityConfig, teamKey: string, version: number, bindings: AgentTeamBinding[]): Promise<AgentTeamBinding[]> {
  return unwrapData<AgentTeamBinding[]>(await apiRequest<unknown>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/bindings?version=${version}`, { method: "PUT", body: bindings }), []);
}

export async function listAgentTeamRuns(identity: IdentityConfig, teamKey: string, version: number): Promise<AgentTeamRun[]> {
  return unwrapData<AgentTeamRun[]>(await apiRequest<unknown>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/runs?version=${version}`), []);
}

export async function getAgentTeamRun(identity: IdentityConfig, teamKey: string, teamVersion: number, runId: string): Promise<AgentTeamRun> {
  return apiRequest<AgentTeamRun>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/runs/${encodeURIComponent(runId)}?version=${teamVersion}`);
}

export async function cancelAgentTeamRun(identity: IdentityConfig, teamKey: string, teamVersion: number, runId: string): Promise<void> {
  await apiRequest(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/runs/${encodeURIComponent(runId)}/cancel?version=${teamVersion}`, { method: "POST" });
}

export async function getAgentTeamRunTimeline(identity: IdentityConfig, teamKey: string, teamVersion: number, runId: string, limit = 500): Promise<AgentTeamRunTimeline> {
  return apiRequest<AgentTeamRunTimeline>(identity, `/tenant/agent-teams/${encodeURIComponent(teamKey)}/runs/${encodeURIComponent(runId)}/events?version=${teamVersion}&limit=${limit}`);
}

export async function listChannelAccounts(identity: IdentityConfig): Promise<ChannelAccountRecord[]> {
  return unwrapData<ChannelAccountRecord[]>(await apiRequest<unknown>(identity, "/tenant/channel-accounts?limit=100"), []);
}

export async function listProvisionings(identity: IdentityConfig): Promise<ProvisioningRecord[]> {
  return unwrapData<ProvisioningRecord[]>(await apiRequest<unknown>(identity, "/tenant/agent-provisionings?limit=100"), []);
}
export async function getProvisioningOverview(identity: IdentityConfig): Promise<ProvisioningOverview> {
  return apiRequest<ProvisioningOverview>(identity, "/tenant/agent-provisionings/overview");
}
export async function createProvisioning(identity: IdentityConfig, body: unknown): Promise<ProvisioningRecord> {
  return apiRequest<ProvisioningRecord>(identity, "/tenant/agent-provisionings", { method: "POST", body });
}
export async function preflightProvisioning(identity: IdentityConfig, id: number): Promise<ProvisioningRecord> {
  return apiRequest<ProvisioningRecord>(identity, `/tenant/agent-provisionings/${id}/preflight`, { method: "POST" });
}
export async function workerProvisioningAction(identity: IdentityConfig, id: number, action: "start" | "restart" | "stop" | "status"): Promise<ProvisioningRecord> {
  return apiRequest<ProvisioningRecord>(identity, `/tenant/agent-provisionings/${id}/${action}`, { method: "POST" });
}
export async function getProvisioningLogs(identity: IdentityConfig, id: number, tail = 100): Promise<string> {
  return unwrapData<string>(await apiRequest<unknown>(identity, `/tenant/agent-provisionings/${id}/logs?tail=${tail}`), "");
}

export async function getFeishuCLIAvailability(identity: IdentityConfig): Promise<import("./types").FeishuCLIAvailability> {
  return apiRequest(identity, "/tenant/feishu/onboarding/cli");
}

export async function installFeishuCLI(identity: IdentityConfig): Promise<import("./types").FeishuCLIAvailability> {
  return apiRequest(identity, "/tenant/feishu/onboarding/cli/install", { method: "POST" });
}

export async function startFeishuOnboarding(identity: IdentityConfig, body: { profile_key: string; account_key: string; app_name?: string; app_description?: string; provider?: string; model?: string; streaming?: string; reactions?: string }): Promise<import("./types").FeishuOnboardingSession> {
  return apiRequest(identity, "/tenant/feishu/onboarding", { method: "POST", body });
}

export async function getFeishuOnboarding(identity: IdentityConfig, id: string): Promise<import("./types").FeishuOnboardingSession> {
  return apiRequest(identity, `/tenant/feishu/onboarding/${encodeURIComponent(id)}`);
}

export async function cancelFeishuOnboarding(identity: IdentityConfig, id: string): Promise<void> {
  await apiRequest(identity, `/tenant/feishu/onboarding/${encodeURIComponent(id)}/cancel`, { method: "POST" });
}

export async function listEffectiveSkills(identity: IdentityConfig): Promise<SkillRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/effective-skills?enabled=true&limit=50");
  return unwrapData<SkillRecord[]>(value, []);
}

export async function listLocalSkills(identity: IdentityConfig): Promise<LocalSkillRecord[]> {
  const value = await apiRequest<unknown>(identity, "/local/skills");
  return unwrapData<LocalSkillRecord[]>(value, []);
}

export async function getLocalSkill(identity: IdentityConfig, name: string): Promise<LocalSkillRecord> {
  return apiRequest<LocalSkillRecord>(identity, `/local/skills?name=${encodeURIComponent(name)}`);
}

export async function saveSkill(identity: IdentityConfig, request: { skill_key: string; name: string; content_md: string; version?: number; enabled?: boolean }): Promise<void> {
  await apiRequest(identity, "/tenant/skills", { method: "POST", body: request });
}

export async function getEffectiveSkill(identity: IdentityConfig, skillKey: string, version?: number): Promise<SkillRecord> {
  const query = new URLSearchParams({ skill_key: skillKey });
  if (version && version > 0) {
    query.set("version", String(version));
  }
  return apiRequest<SkillRecord>(identity, `/tenant/effective-skills?${query.toString()}`);
}

export async function listTenantSkills(identity: IdentityConfig): Promise<SkillRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/skills?limit=50");
  return unwrapData<SkillRecord[]>(value, []);
}

export async function getTenantSkill(identity: IdentityConfig, skillKey: string, version?: number): Promise<SkillRecord> {
  const query = new URLSearchParams({ skill_key: skillKey });
  if (version && version > 0) {
    query.set("version", String(version));
  }
  return apiRequest<SkillRecord>(identity, `/tenant/skills?${query.toString()}`);
}

export async function saveTenantSkill(identity: IdentityConfig, record: SkillRecord): Promise<void> {
  await apiRequest(identity, "/tenant/skills", {
    method: "POST",
    body: {
      skill_key: record.skill_key,
      name: record.name,
      version: record.version,
      enabled: record.enabled,
      content_md: record.content_md,
      config_json: record.config_json
    }
  });
}

export async function rollbackTenantSkill(identity: IdentityConfig, skillKey: string, version: number): Promise<SkillRollbackResult> {
  return apiRequest<SkillRollbackResult>(identity, "/tenant/skills/rollback", {
    method: "POST",
    body: {
      skill_key: skillKey,
      version
    }
  });
}

export async function renderTenantSkillPackage(identity: IdentityConfig, request: {
  skill_key: string;
  name?: string;
  source_path?: string;
  content_base64?: string;
}): Promise<SkillPackageResult> {
  return apiRequest<SkillPackageResult>(identity, "/tenant/skill-packages/render", {
    method: "POST",
    body: request
  });
}

export async function publishTenantSkillPackage(identity: IdentityConfig, request: {
  skill_key: string;
  name?: string;
  source_path?: string;
  content_base64?: string;
  version?: number;
  enabled?: boolean;
}): Promise<SkillPackageResult> {
  return apiRequest<SkillPackageResult>(identity, "/tenant/skill-packages/publish", {
    method: "POST",
    body: request
  });
}

export async function verifyTenantSkillPackageRuntime(identity: IdentityConfig, request: {
  skill_key: string;
  schema_name: string;
  expected_package_sha256?: string;
  expected_version?: number;
  model?: string;
}): Promise<SkillPackageVerifyResult> {
  return apiRequest<SkillPackageVerifyResult>(identity, "/tenant/skill-packages/verify-runtime", {
    method: "POST",
    body: request
  });
}

export async function listSkillOverrides(identity: IdentityConfig): Promise<SkillOverrideRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/skill-overrides?limit=50");
  return unwrapData<SkillOverrideRecord[]>(value, []);
}

export async function saveSkillOverride(identity: IdentityConfig, record: SkillOverrideRecord): Promise<void> {
  await apiRequest(identity, "/tenant/skill-overrides", {
    method: "POST",
    body: record
  });
}

export async function listDocuments(identity: IdentityConfig, docType = ""): Promise<DocumentRecord[]> {
  const query = docType ? `?type=${encodeURIComponent(docType)}&history=true&limit=20` : "?history=true&limit=20";
  const value = await apiRequest<unknown>(identity, `/tenant/documents${query}`);
  return unwrapData<DocumentRecord[]>(value, []);
}

export async function saveDocument(identity: IdentityConfig, record: DocumentRecord): Promise<void> {
  await apiRequest(identity, "/tenant/documents", {
    method: "POST",
    body: record
  });
}

export async function listKnowledgeDocuments(identity: IdentityConfig): Promise<KnowledgeDocumentRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/knowledge/documents?limit=20");
  return unwrapData<KnowledgeDocumentRecord[]>(value, []);
}

export async function saveKnowledgeDocument(identity: IdentityConfig, record: KnowledgeDocumentRecord): Promise<void> {
  await apiRequest(identity, "/tenant/knowledge/documents", {
    method: "POST",
    body: record
  });
}

export async function searchKnowledge(identity: IdentityConfig, query: string, limit = 10): Promise<KnowledgeChunkRecord[]> {
  const value = await apiRequest<unknown>(identity, "/tenant/knowledge/search", {
    method: "POST",
    body: {
      query,
      limit
    }
  });
  return unwrapData<KnowledgeChunkRecord[]>(value, []);
}

export async function listTelemetry(identity: IdentityConfig, search = ""): Promise<TelemetryRecord[]> {
  const query = search ? `?limit=50&search=${encodeURIComponent(search)}` : "?limit=30";
  const value = await apiRequest<unknown>(identity, `/tenant/telemetry${query}`);
  return unwrapData<TelemetryRecord[]>(value, []);
}

export async function getTenantQuotaConfig(identity: IdentityConfig): Promise<TenantQuotaConfig> {
  return apiRequest<TenantQuotaConfig>(identity, "/tenant/quota/config");
}

export async function updateTenantQuotaConfig(identity: IdentityConfig, config: TenantQuotaConfig): Promise<TenantQuotaConfig> {
  return apiRequest<TenantQuotaConfig>(identity, "/tenant/quota/config", {
    method: "PUT",
    body: config
  });
}

export async function listTenantUsageDaily(identity: IdentityConfig, limit = 30): Promise<TenantUsageDaily[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/usage/daily?limit=${limit}`);
  return unwrapData<TenantUsageDaily[]>(value, []);
}

export async function listTenantUsageLedger(identity: IdentityConfig, limit = 50): Promise<TenantUsageLedger[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/usage/ledger?limit=${limit}`);
  return unwrapData<TenantUsageLedger[]>(value, []);
}

export async function listTenantQuotaEvents(identity: IdentityConfig, limit = 50): Promise<TenantQuotaEvent[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/quota/events?limit=${limit}`);
  return unwrapData<TenantQuotaEvent[]>(value, []);
}

export async function listAgentTasks(identity: IdentityConfig, limit = 50): Promise<AgentTaskRecord[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/agent-tasks?limit=${limit}`);
  return unwrapData<AgentTaskRecord[]>(value, []);
}

export async function validateAgentWorkspace(identity: IdentityConfig, cwd: string): Promise<AgentWorkspaceValidation> {
  return apiRequest<AgentWorkspaceValidation>(identity, "/agent/workspaces/validate", {
    method: "POST",
    body: { cwd }
  });
}

export async function getAgentTask(identity: IdentityConfig, taskId: number): Promise<AgentTaskRecord> {
  return apiRequest<AgentTaskRecord>(identity, `/tenant/agent-tasks/${taskId}`);
}

export async function createAgentTask(identity: IdentityConfig, request: AgentTaskCreateRequest): Promise<number> {
  const value = await apiRequest<unknown>(identity, "/tenant/agent-tasks", {
    method: "POST",
    body: request,
    traceId: request.trace_id
  });
  if (value && typeof value === "object" && typeof (value as Record<string, unknown>).id === "number") {
    return (value as { id: number }).id;
  }
  throw new Error("create agent task response did not include an id");
}

export async function updateAgentTask(identity: IdentityConfig, taskId: number, request: AgentTaskUpdateRequest): Promise<void> {
  await apiRequest(identity, `/tenant/agent-tasks/${taskId}`, {
    method: "PATCH",
    body: request
  });
}

export async function cancelAgentTask(identity: IdentityConfig, taskId: number): Promise<{ id: number; cancelled: boolean; in_process?: boolean }> {
  return apiRequest<{ id: number; cancelled: boolean; in_process?: boolean }>(identity, `/tenant/agent-tasks/${taskId}/cancel`, {
    method: "POST"
  });
}

export type AgentTaskEventsOptions = {
  limit?: number;
  afterID?: number;
  signal?: AbortSignal;
};

export async function listAgentTaskEvents(identity: IdentityConfig, taskId: number, options: number | AgentTaskEventsOptions = 100): Promise<AgentTaskEventRecord[]> {
  const params = new URLSearchParams();
  if (typeof options === "number") {
    params.set("limit", String(options));
  } else {
    params.set("limit", String(options.limit || 100));
    if (options.afterID && options.afterID > 0) {
      params.set("after_id", String(options.afterID));
    }
  }
  const value = await apiRequest<unknown>(identity, `/tenant/agent-tasks/${taskId}/events?${params.toString()}`, {
    signal: typeof options === "object" ? options.signal : undefined
  });
  return unwrapData<AgentTaskEventRecord[]>(value, []);
}

export async function streamAgentTaskEvents(
  identity: IdentityConfig,
  taskId: number,
  callbacks: {
    onEvent: (event: AgentTaskSSEEvent) => void;
    onDone: () => void;
  },
  signal?: AbortSignal,
  afterID = 0
): Promise<void> {
  const params = new URLSearchParams({ limit: "500" });
  if (afterID > 0) {
    params.set("after_id", String(afterID));
  }
  await streamSSEEndpoint<AgentTaskSSEEvent>(
    identity,
    `/tenant/agent-tasks/${taskId}/events/stream?${params.toString()}`,
    {
      method: "GET"
    },
    callbacks,
    signal,
    24 * 60 * 60 * 1000
  );
}

export async function sendAgentTaskMessage(identity: IdentityConfig, taskId: number, request: AgentTaskMessageRequest): Promise<number> {
  const value = await apiRequest<unknown>(identity, `/tenant/agent-tasks/${taskId}/message`, {
    method: "POST",
    body: request,
    traceId: request.trace_id
  });
  if (value && typeof value === "object" && typeof (value as Record<string, unknown>).id === "number") {
    return (value as { id: number }).id;
  }
  throw new Error("agent message response did not include an id");
}

export async function resolveAgentTaskPermission(
  identity: IdentityConfig,
  taskId: number,
  requestId: string,
  request: AgentTaskPermissionResolveRequest
): Promise<{ id: number; request_id: string; allowed: boolean }> {
  return apiRequest<{ id: number; request_id: string; allowed: boolean }>(
    identity,
    `/tenant/agent-tasks/${taskId}/permissions/${encodeURIComponent(requestId)}`,
    {
      method: "PATCH",
      body: request
    }
  );
}

export async function resolveAgentTaskQuestion(
  identity: IdentityConfig,
  taskId: number,
  requestId: string,
  answer: string
): Promise<{ id: number; request_id: string; status: "answered"; answer: string }> {
  return apiRequest(identity, `/tenant/agent-tasks/${taskId}/questions/${encodeURIComponent(requestId)}`, {
    method: "PATCH",
    body: { answer: answer.trim() }
  });
}

export async function listGoals(
  identity: IdentityConfig,
  filters: { active?: boolean; status?: string; limit?: number } = {}
): Promise<GoalRecord[]> {
  const params = new URLSearchParams();
  params.set("limit", String(filters.limit || 50));
  if (filters.active !== undefined) {
    params.set("active", String(filters.active));
  }
  if (filters.status && filters.status !== "all") {
    params.set("status", filters.status);
  }
  const value = await apiRequest<unknown>(identity, `/tenant/goals?${params.toString()}`);
  return unwrapData<GoalRecord[]>(value, []);
}

export async function getGoal(identity: IdentityConfig, goalId: string): Promise<GoalRecord> {
  return apiRequest<GoalRecord>(identity, `/tenant/goals/${encodeURIComponent(goalId)}`);
}

export async function createGoal(identity: IdentityConfig, request: GoalCreateRequest): Promise<GoalRecord> {
  return apiRequest<GoalRecord>(identity, "/tenant/goals", {
    method: "POST",
    body: request
  });
}

export async function stopGoal(identity: IdentityConfig, goalId: string): Promise<GoalRecord> {
  return apiRequest<GoalRecord>(identity, `/tenant/goals/${encodeURIComponent(goalId)}/stop`, {
    method: "POST"
  });
}

export async function resumeGoal(identity: IdentityConfig, goalId: string, force = false): Promise<GoalRecord> {
  const suffix = force ? "?force=true" : "";
  return apiRequest<GoalRecord>(identity, `/tenant/goals/${encodeURIComponent(goalId)}/resume${suffix}`, {
    method: "POST"
  });
}

export async function runGoalOnce(identity: IdentityConfig, goalId: string, evaluator = "deterministic"): Promise<GoalRunResponse> {
  const params = new URLSearchParams();
  if (evaluator) {
    params.set("evaluator", evaluator);
  }
  return apiRequest<GoalRunResponse>(identity, `/tenant/goals/${encodeURIComponent(goalId)}/run?${params.toString()}`, {
    method: "POST"
  });
}

export async function listGoalEvents(identity: IdentityConfig, goalId: string, limit = 100): Promise<GoalEventRecord[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/goals/${encodeURIComponent(goalId)}/events?limit=${limit}`);
  return unwrapData<GoalEventRecord[]>(value, []);
}

export async function getGoalPlan(identity: IdentityConfig, goalId: string): Promise<GoalPlan> {
  return apiRequest<GoalPlan>(identity, `/tenant/goals/${encodeURIComponent(goalId)}/plan`);
}

export async function listGoalEvidence(identity: IdentityConfig, goalId: string, limit = 20): Promise<GoalEvidence[]> {
  const value = await apiRequest<unknown>(identity, `/tenant/goals/${encodeURIComponent(goalId)}/evidence?limit=${limit}`);
  return unwrapData<GoalEvidence[]>(value, []);
}

export async function listRuntimeBackground(identity: IdentityConfig, kind = "loop"): Promise<RuntimeBackgroundJob[]> {
  const params = new URLSearchParams();
  if (kind) {
    params.set("kind", kind);
  }
  params.set("tail", "4000");
  params.set("limit", "100");
  const value = await apiRequest<unknown>(identity, `/runtime/background?${params.toString()}`);
  return unwrapData<RuntimeBackgroundJob[]>(value, []);
}

export async function createRuntimeLoop(identity: IdentityConfig, request: RuntimeLoopRequest): Promise<RuntimeBackgroundJob> {
  return apiRequest<RuntimeBackgroundJob>(identity, "/runtime/background", { method: "POST", body: request });
}

export async function updateRuntimeLoop(identity: IdentityConfig, id: string, request: RuntimeLoopRequest): Promise<RuntimeBackgroundJob> {
  return apiRequest<RuntimeBackgroundJob>(identity, `/runtime/background/${encodeURIComponent(id)}`, { method: "PATCH", body: request });
}

export async function runRuntimeBackground(identity: IdentityConfig, id: string): Promise<RuntimeBackgroundJob> {
  return apiRequest<RuntimeBackgroundJob>(identity, `/runtime/background/${encodeURIComponent(id)}/run`, { method: "POST", body: {} });
}

export async function getRuntimeBackgroundLogs(identity: IdentityConfig, id: string, tail = 12000): Promise<RuntimeBackgroundLogs> {
  return apiRequest<RuntimeBackgroundLogs>(identity, `/runtime/background/${encodeURIComponent(id)}/logs?tail=${tail}`);
}

export async function stopRuntimeBackground(identity: IdentityConfig, id: string): Promise<RuntimeBackgroundStopResponse> {
  return apiRequest<RuntimeBackgroundStopResponse>(identity, `/runtime/background/${encodeURIComponent(id)}/stop`, { method: "POST", body: {} });
}

export async function listRuntimeRuns(identity: IdentityConfig, id: string, limit = 50): Promise<RuntimeRunRecord[]> {
  const value = await apiRequest<unknown>(identity, `/runtime/background/${encodeURIComponent(id)}/runs?limit=${limit}`);
  return unwrapData<RuntimeRunRecord[]>(value, []);
}

export async function listRuntimeEvents(identity: IdentityConfig, offset = 0, limit = 100): Promise<{ data: RuntimeSchedulerEvent[]; next_offset: number }> {
  return apiRequest<{ data: RuntimeSchedulerEvent[]; next_offset: number }>(identity, `/runtime/background/events?offset=${offset}&limit=${limit}`);
}

export async function getTrace(identity: IdentityConfig, sessionId: number): Promise<TraceDetail> {
  return apiRequest<TraceDetail>(identity, `/trace/api/sessions/${sessionId}?source=tenant&limit=100&trace_limit=100&task_limit=200`);
}

export async function listLocalTraceSessions(identity: IdentityConfig): Promise<TraceSessionSummary[]> {
  const value = await apiRequest<unknown>(identity, "/trace/api/sessions?source=local&limit=1000");
  return unwrapData<TraceSessionSummary[]>(value, []);
}

export async function getLocalTrace(identity: IdentityConfig, sessionId: string): Promise<TraceDetail> {
  return apiRequest<TraceDetail>(identity, `/trace/api/sessions/${encodeURIComponent(sessionId)}?source=local`);
}

export async function getRuntimeTraceArtifact(identity: IdentityConfig, sessionId: string, source: "local" | "tenant"): Promise<Blob> {
  const path = `/trace/api/sessions/${encodeURIComponent(sessionId)}/export?source=${source}&schema=runtime-trace-v1`;
  const response = await fetch(`${identity.apiBase}${path}`, { headers: makeHeaders(identity) });
  if (!response.ok) {
    throw new ApiError(response.status, await response.text());
  }
  return response.blob();
}

export async function getGlobalSettings(identity: IdentityConfig): Promise<GlobalSettingsResponse> {
  return apiRequest<GlobalSettingsResponse>(identity, "/runtime/settings");
}

export async function listPromptTemplates(identity: IdentityConfig, search = ""): Promise<PromptTemplate[]> {
  return apiRequest<PromptTemplate[]>(identity, `/tenant/prompt-templates${search.trim() ? `?search=${encodeURIComponent(search.trim())}` : ""}`);
}

export async function savePromptTemplate(identity: IdentityConfig, record: Partial<PromptTemplate> & Pick<PromptTemplate, "title" | "content">): Promise<PromptTemplate> {
  return apiRequest<PromptTemplate>(identity, "/tenant/prompt-templates", { method: record.id ? "PATCH" : "POST", body: record });
}

export async function deletePromptTemplate(identity: IdentityConfig, id: number): Promise<void> {
  await apiRequest<void>(identity, `/tenant/prompt-templates/${id}`, { method: "DELETE" });
}

export async function saveGlobalSettings(identity: IdentityConfig, doc: SettingsDoc): Promise<GlobalSettingsSaveResponse> {
  return apiRequest<GlobalSettingsSaveResponse>(identity, "/runtime/settings", { method: "PUT", body: doc });
}

export async function promoteGlobalSettingsProvider(identity: IdentityConfig, doc: SettingsDoc, providerIndex: number, signal?: AbortSignal): Promise<GlobalSettingsPromoteResponse> {
  return apiRequest<GlobalSettingsPromoteResponse>(identity, "/runtime/settings/promote-provider", {
    method: "POST",
    body: { doc, provider_index: providerIndex },
    signal,
  });
}
