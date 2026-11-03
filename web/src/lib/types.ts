import type { components } from "./generated/api-types";

type OpenAPISchema<K extends keyof components["schemas"]> = components["schemas"][K];

export type IdentityConfig = {
  apiBase: string;
  apiToken: string;
  mobileJwt: string;
  tenantKey: string;
  userId: string;
  deviceId: string;
  model: string;
  provider?: string;
  role?: string;
};

export type ServerStatus = {
  ok?: boolean;
  workspace?: string;
  cwd?: string;
  provider?: string;
  model?: string;
  baseURL?: string;
  runtime_defaults?: RuntimeDefaults;
};

export type RuntimeDefaults = {
  service?: "ready" | "starting" | "unavailable";
  configured: boolean;
  needs_setup: boolean;
  provider?: string;
  model?: string;
  source?: string;
};

export type PromptTemplate = {
  id: number;
  title: string;
  content: string;
  category: string;
  pinned: boolean;
  sort_order: number;
  created_at?: string;
  updated_at?: string;
};

// Derived from the generated schema rather than hand-written: /v1/providers and
// /v1/models went undocumented for 19 commits and the frontend covered for it
// with inline types that nothing kept in sync (AUDIT-P0-18).
export type ProviderListResponse = OpenAPISchema<"internal_server.SwaggerProviderListResponse">;
export type OpenAIModelsResponse = OpenAPISchema<"internal_server.SwaggerOpenAIModelsResponse">;

export type ProviderOption = {
  name: string;
  model: string;
};

export type ProvisioningHealthCheck = { name: string; status: string; message?: string };
export type ProvisioningWorkerStatus = { state: string; pid?: number; screen?: string; log_path?: string; provider?: string; model?: string; observed_at?: string; message?: string };
export type ProvisioningRecord = { id: number; tenant_id?: number; profile_key: string; account_key: string; credential_ref?: string; supervisor?: string; status: string; worker?: { supervisor?: string; account_key?: string; workspace?: string; settings_ref?: string; provider?: string; model?: string; streaming?: string; reactions?: string; permission_mode?: string; payload_key_ref?: string }; observed_worker?: ProvisioningWorkerStatus; checks?: ProvisioningHealthCheck[]; last_error?: { code: string; message: string; retryable?: boolean }; created_at?: string; updated_at?: string };
export type ProvisioningOverview = { records: ProvisioningRecord[]; workers: ProvisioningWorkerStatus[] };
export type FeishuCLIAvailability = { project_cli: boolean; lark_cli: boolean; message?: string };
export type FeishuOnboardingSession = {
  id: string;
  status: "starting" | "waiting_for_scan" | "created" | "failed" | "canceled" | string;
  profile_key: string;
  account_key: string;
  app_id?: string;
  verification_url?: string;
  verification_qr_code?: string;
  verification_expires_in?: number;
  record?: ProvisioningRecord;
  error?: string;
  created_at?: string;
  updated_at?: string;
};

export type TenantRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.Tenant"> & {
  tenant_key?: string;
  name?: string;
  status?: string;
};

export type TenantUserRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.User"> & {
  user_key?: string;
  display_name?: string;
  email?: string;
  role?: string;
  status?: string;
};

export type TenantSession = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.Session"> & {
  id: number;
  tenant_id?: number;
  user_id?: number;
  metadata_json?: string;
  archived_at?: string;
  created_at?: string;
  updated_at?: string;
};

export type MobileLatestRecap = OpenAPISchema<"internal_server.SwaggerMobileLatestRecap"> & {
  content: string;
};

export type MobileSessionDetail = OpenAPISchema<"internal_server.SwaggerMobileSessionDetailResponse"> &
  TenantSession & {
    latest_recap?: MobileLatestRecap;
  };

export type TenantMessage = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.Message"> & {
  id: number;
  status?: string;
  total_tokens?: number;
  updated_at?: string;
};

export type MobileSessionResponse = {
  id?: number;
  session_id?: number;
  data?: TenantSession[] | TenantSession;
  session?: TenantSession;
};

export type MobileMessageEvent =
  | {
      type: "message_start";
      session_id?: number;
      message_id?: number;
      message_key?: string;
      status?: string;
    }
  | {
      type: "delta";
      delta?: string;
      message_id?: number;
    }
  | {
      type: "message_stop";
      session_id?: number;
      message_id?: number;
      status?: string;
    }
  | {
      type: "error";
      status?: string;
      error?: string;
      message_id?: number;
    };

export type ChatMessage = {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  status?: string;
  messageId?: number;
  turnIndex?: number;
  createdAt?: string;
};

export type MobileAttachment = OpenAPISchema<"internal_server.SwaggerMobileAttachment"> & {
  type: string;
};

export type AttachmentPresignResponse = OpenAPISchema<"internal_server.SwaggerMobileAttachmentPresignResponse"> & {
  attachment_id: string;
  object_key: string;
  upload_url: string;
  expires_at: string;
  attachment: MobileAttachment;
};

export type MemoryRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.Memory"> & {
  source?: string;
  created_at?: string;
};

export type KnowledgeDocumentRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.KnowledgeDocument">;

export type KnowledgeChunkRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.KnowledgeChunk">;

export type AutoMemoryReviewRequest = OpenAPISchema<"internal_server.SwaggerAutoMemoryReviewRequest">;

export type ProfileRecord = OpenAPISchema<"internal_server.SwaggerTenantProfile">;

export type SkillRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.Skill"> & {
  source?: string;
  package_ref?: string;
  package_sha256?: string;
  manifest_json?: string;
  runtime_ref?: string;
  created_at?: string;
  updated_at?: string;
};

export type LocalSkillRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_skills.Skill"> & {
  name: string;
  path: string;
};

export type SkillRollbackResult = {
  id: number;
  skill_key: string;
  from_version: number;
  version: number;
};

export type SkillPackageFile = {
  path: string;
  sha256: string;
  size: number;
  runtime?: boolean;
  render_order?: number;
};

export type SkillPackageManifest = {
  schema_version: string;
  skill_key: string;
  package_sha256: string;
  files: SkillPackageFile[];
  render?: {
    entrypoint?: string;
    runtime_files?: string[];
    excluded_prefixes?: string[];
  };
};

export type SkillPackageResult = {
  id?: number;
  skill_key: string;
  name?: string;
  version?: number;
  enabled?: boolean;
  package_sha256: string;
  package_ref?: string;
  runtime_ref?: string;
  manifest_ref?: string;
  runtime_md?: string;
  manifest: SkillPackageManifest;
};

export type SkillPackageVerifyResult = {
  ok: boolean;
  trace_id?: string;
  error?: string;
  tenant_runtime?: {
    active?: boolean;
    resolved?: boolean;
    source?: string;
    skill_keys?: string[];
    loaded_keys?: string[];
    versions?: string[];
    package_sha256?: string[];
    package_refs?: string[];
    runtime_refs?: string[];
    bytes?: number;
  };
};

export type SkillOverrideRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.SkillOverride">;

export type DocumentRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.Document"> & {
  created_at?: string;
  updated_at?: string;
};

export type TelemetryRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.TelemetryEvent"> & {
  properties_json?: string;
};

export type TenantQuotaConfig = {
  tenant_id?: number;
  quota_enabled: boolean;
  qps_limit?: number | null;
  daily_token_limit?: number | null;
  daily_message_limit?: number | null;
  max_concurrent_requests?: number | null;
  timezone?: string;
  reserve_output_tokens?: number;
  status?: string;
  updated_by_user_id?: number;
  created_at?: string;
  updated_at?: string;
};

export type TenantUsageDaily = {
  id?: number;
  tenant_id?: number;
  usage_date?: string;
  source?: string;
  model?: string;
  request_count?: number;
  message_count?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_input_tokens?: number;
  cache_creation_input_tokens?: number;
  total_tokens?: number;
  rejected_count?: number;
  updated_at?: string;
};

export type TenantUsageLedger = {
  id?: number;
  request_id?: string;
  tenant_id?: number;
  user_id?: number;
  session_id?: number;
  trace_id?: string;
  source?: string;
  route?: string;
  model?: string;
  status?: string;
  estimated?: boolean;
  reserved_input_tokens?: number;
  reserved_output_tokens?: number;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  error_code?: string;
  error_message?: string;
  started_at?: string;
  finished_at?: string;
};

export type TenantQuotaEvent = {
  id?: number;
  tenant_id?: number;
  user_id?: number;
  request_id?: string;
  event_type?: string;
  limit_type?: string;
  limit_value?: number;
  current_value?: number;
  source?: string;
  route?: string;
  model?: string;
  trace_id?: string;
  metadata_json?: string;
  created_at?: string;
};

export type AgentTaskRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.AgentTask"> & {
  id: number;
};

export type PendingInputRecord = {
  id: string;
  tenant_id?: number;
  user_id?: number;
  session_id: string;
  base_task_id?: number;
  client_input_id: string;
  content: string;
  direction?: string;
  attempt?: number;
  attachments?: AgentTaskAttachment[];
  sequence: number;
  status: "queued" | "running" | "sent" | "failed" | "cancelled";
  dispatched_task_id?: number;
  error_code?: string;
  error_message?: string;
  created_at?: string;
  updated_at?: string;
};

export type PendingInputSideChatResponse = {
  session_id: number;
  task_id: number;
  source_pending_input_id: string;
};

export type WebAgentConversation = {
  id: string;
  session_id?: number;
  session_key?: string;
  title: string;
  cwd?: string;
  workspace_name?: string;
  status: string;
  updated_at?: string;
  session?: TenantSession;
  latest_task: AgentTaskRecord;
  tasks: AgentTaskRecord[];
};

export type WebAgentConversationUsage = {
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  context_length?: number;
  context_percent?: number;
  tool_calls?: number;
  completed_runs?: number;
  failed_runs?: number;
  cancelled_runs?: number;
  timeout_runs?: number;
  running_runs?: number;
  total_runs?: number;
  total_duration_ms?: number;
};

export type WebAgentConversationDetail = WebAgentConversation & {
  events: AgentTaskEventRecord[];
  messages?: TenantMessage[];
  usage: WebAgentConversationUsage;
};

export type AgentSlashCommand = {
  name: string;
  description?: string;
  source?: string;
};

export type AgentWorkspaceValidation = {
  cwd: string;
  workspace_name: string;
  exists: boolean;
  is_dir: boolean;
  git_root?: string;
  is_git_repo: boolean;
};

export type AgentTaskEventRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_storage_mysql.AgentTaskEvent"> & {
  id: number;
  task_id: number;
};

export type AgentTaskSSEEvent =
  | {
      type: "connected";
      task_id?: number;
    }
  | {
      type: "agent_task_event";
      event: AgentTaskEventRecord;
    }
  | {
      type: "error";
      error?: string;
    };

export type AgentTaskCreateRequest = Omit<OpenAPISchema<"internal_server.SwaggerCreateAgentTaskRequest">, "metadata_json" | "result_json"> & {
  metadata_json?: unknown;
  result_json?: unknown;
};

export type AgentTaskUpdateRequest = Omit<OpenAPISchema<"internal_server.SwaggerUpdateAgentTaskRequest">, "metadata_json" | "result_json"> & {
  metadata_json?: unknown;
  result_json?: unknown;
};

export type AgentTaskAttachment = {
  attachment_id?: string;
  type: "image";
  media_type: string;
  name?: string;
  url?: string;
  size_bytes: number;
  sha256?: string;
  inline_data?: string;
};

export type ImageArtifact = {
  asset_id: string;
  generation_id: string;
  operation: "generate" | "edit";
  media_type: string;
  width?: number;
  height?: number;
  size_bytes?: number;
  sha256?: string;
  url: string;
  session_id: number;
};

export type ImageGenerationRecord = ImageArtifact & {
  prompt?: string;
  model?: string;
  quality?: string;
  size?: string;
  output_format?: string;
  background?: string;
  source_asset_id?: string;
  status?: string;
  error?: string;
  created_at?: string;
};

export type ImageGenerationRequest = {
  prompt: string;
  provider?: string;
  model?: string;
  quality?: string;
  size?: string;
  resolution?: string;
  aspect_ratio?: string;
  output_format?: string;
  background?: string;
  watermark?: boolean;
  source_asset_id?: string;
  idempotency_key?: string;
};

export type ImageModelCapability = {
  operations?: string[];
  resolutions?: string[];
  aspectRatios?: string[];
  sizes?: string[];
  qualityOptions?: string[];
  outputFormats?: string[];
  responseModes?: string[];
  supportsMask?: boolean;
  supportsWatermark?: boolean;
  maxInputImages?: number;
};

export type ImageCapabilityEntry = {
  provider: string;
  model: string;
  label?: string;
  image_protocol?: string;
  capability: ImageModelCapability;
};

export type AgentTaskMessageRequest = OpenAPISchema<"internal_server.SwaggerAgentTaskMessageRequest"> & {
  attachments?: AgentTaskAttachment[];
};

export type AgentTaskPermissionResolveRequest = OpenAPISchema<"internal_server.SwaggerAgentTaskPermissionRequest">;

export type AgentUserQuestion = {
  taskID: number;
  requestID?: string;
  choices: string[];
  expiresAt?: string;
  status: "pending" | "answered" | "cancelled" | "expired" | "unavailable";
  answer?: string;
  legacy?: boolean;
};

export type GoalRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_goal.Goal"> & {
  id: string;
};

export type GoalEventRecord = OpenAPISchema<"github_com_konglong87_niuma_internal_goal.Event"> & {
  id: string;
  goal_id: string;
};

export type GoalPlan = OpenAPISchema<"internal_server.SwaggerGoalPlanResponse">;

export type GoalEvidence = OpenAPISchema<"github_com_konglong87_niuma_internal_goal.GoalEvidence"> & {
  id: string;
  goal_id: string;
};

export type GoalCreateRequest = OpenAPISchema<"internal_server.SwaggerCreateGoalRequest">;

export type GoalRunResponse = OpenAPISchema<"internal_server.SwaggerRunGoalResponse">;

export type RuntimeBackgroundJob = {
  id: string;
  schedule_id?: string;
  prompt?: string;
  cwd?: string;
  kind?: string;
  status?: string;
  pid?: number;
  interval_seconds?: number;
  spec?: string;
  enabled?: boolean;
  run_count?: number;
  last_run_at?: string;
  next_run_at?: string;
  last_error?: string;
  log_path?: string;
  log_tail?: string;
  created_at?: string;
  updated_at?: string;
};

export type RuntimeBackgroundLogs = {
  id: string;
  logs: string;
  log_tail: string;
};

export type RuntimeBackgroundStopResponse = {
  id: string;
  schedule_id?: string;
  stopped: boolean;
  disabled: boolean;
};

export type RuntimeLoopRequest = {
  prompt: string;
  cwd?: string;
  interval_seconds?: number;
  interval?: string;
  model?: string;
  max_turns?: number;
  max_tokens?: number;
};

export type RuntimeSchedulerEvent = {
  offset?: number;
  id: string;
  type: string;
  schedule_id?: string;
  background_id?: string;
  prompt?: string;
  cwd?: string;
  status?: string;
  run_count?: number;
  error?: string;
  created_at?: string;
};

export type RuntimeRunRecord = {
  id: string;
  schedule_id: string;
  background_id: string;
  prompt: string;
  cwd: string;
  status: string;
  error?: string;
  started_at?: string;
  finished_at?: string;
  log_bytes?: number;
};

export type TraceSummary = OpenAPISchema<"internal_server.traceSummary">;

export type TraceQuality = {
  tests_run: boolean;
  tests_passed: boolean;
  test_attempts?: number;
  failed_test_attempts?: number;
  test_failure_recovered?: boolean;
  completion_verified: boolean;
  final_verification_passed?: boolean;
  tool_errors: number;
  recoveries: number;
  gate_blocks: number;
  todo_writes: number;
};

export type TraceDiagnostic = {
  code: string;
  severity: string;
  message: string;
  turn_index?: number;
  tool_name?: string;
  count?: number;
  estimated_savings_ms?: number;
  span_ids?: string[];
  fingerprint?: string;
};

export type TraceSessionSummary = {
  source: string;
  session_id: string;
  title?: string;
  path?: string;
  status?: string;
  model?: string;
  cwd?: string;
  started_at?: string;
  updated_at?: string;
  message_hint?: string;
};

export type TraceRecap = {
  id?: string;
  content?: string;
  model?: string;
  source?: string;
  status?: string;
  time?: string;
  duration_ms?: number;
  summarizes_entry_id?: string;
  properties?: Record<string, unknown>;
};

export type TraceDetail = OpenAPISchema<"internal_server.SwaggerTraceDetailResponse"> & {
  source?: string;
  session_id?: string;
  title?: string;
  trace_ids?: string[];
  summary?: TraceSummary;
  quality?: TraceQuality;
  diagnostics?: TraceDiagnostic[];
  latest_recap?: TraceRecap;
  recaps?: TraceRecap[];
  events?: Array<Record<string, unknown>>;
  spans?: Array<Record<string, unknown>>;
  span_tree?: Array<{
    span?: OpenAPISchema<"internal_server.traceSpan">;
    children?: unknown[];
  }>;
  telemetry_events?: TelemetryRecord[];
};

/** A decoded global settings.json document. Kept loose so fields the backend
 * struct does not model still round-trip untouched. */
export type SettingsDoc = Record<string, unknown>;

export type GlobalSettingsResponse = {
  path: string;
  exists: boolean;
  doc: SettingsDoc;
  /** Dotted paths of credential values the server replaced with a placeholder. */
  masked: string[];
};

export type GlobalSettingsSaveResponse = {
  path: string;
  saved: boolean;
};

export type GlobalSettingsPromoteResponse = {
  doc: SettingsDoc;
  masked: string[];
  revision?: string;
};

export type AgentProfileDocument = {
  schema_version: number;
  identity: { display_name?: string; description?: string };
  prompt: {
    mode: "chat" | "code" | string;
    persona?: string;
    output_style?: string;
    language?: string;
    system_addendum?: string;
  };
  capabilities: {
    tools: { allow?: string[]; deny?: string[] };
    skills?: string[];
    mcp_servers?: string[];
    allow_agents?: boolean;
    allow_attachments?: boolean;
  };
  execution: {
    model?: string;
    provider?: string;
    runtime_profile?: string;
    effort?: string;
    max_turns?: number;
    max_tokens?: number;
    max_parallel_read_only_tools?: number;
    auto_compact?: boolean;
  };
  context: {
    workspace?: boolean;
    git?: boolean;
    tenant_memory?: boolean;
    user_memory?: boolean;
    knowledge_base?: boolean;
    session_history?: boolean;
  };
  safety: {
    permission_mode?: string;
    sandbox?: string;
    allow_unsandboxed_commands?: boolean;
  };
};

export type AgentProfileRecord = {
  id: number;
  tenant_id?: number;
  owner_user_id?: number;
  owner_key?: string;
  profile_key: string;
  scope?: string;
  display_name: string;
  description?: string;
  profile_version: number;
  status: string;
  config_json: string;
  requested_hash?: string;
  effective_hash?: string;
  validation_json?: string;
  created_at?: string;
  updated_at?: string;
  published_at?: string;
  source_kind?: "builtin" | "database" | "file" | "generated" | string;
  source_ref?: string;
  source_path?: string;
};

export type AgentProfileConversationSummary = {
  conversation_id: number;
  session_id?: number;
  account_id: number;
  account_key: string;
  external_chat_id: string;
  external_thread_id?: string;
  chat_type: string;
  conversation_status: string;
  title?: string;
  model?: string;
  last_message_at?: string;
  last_inbound_at?: string;
  last_outbound_at?: string;
  message_count: number;
  run_count: number;
  latest_run_status?: string;
  last_message_role?: string;
  last_message_preview?: string;
};

export type AgentProfileTeamLink = {
  team_id: number;
  team_key: string;
  team_version: number;
  team_display_name: string;
  member_key: string;
  role: string;
  account_id?: number;
  account_key?: string;
  external_chat_id?: string;
  trigger_policy?: string;
  status: string;
};

export type AgentProfileConversationCatalog = {
  profile: AgentProfileRecord;
  conversations: AgentProfileConversationSummary[];
  teams: AgentProfileTeamLink[];
  message_count: number;
  run_count: number;
};

export type EffectiveAgentProfile = {
  profile: AgentProfileRecord;
  requested: AgentProfileDocument;
  config: AgentProfileDocument;
  source: string;
  profile_key: string;
  profile_version: number;
  requested_hash: string;
  effective_hash: string;
  blocked_overrides?: Array<{ field: string; reason: string }>;
};

export type AgentProfileAssignment = {
  id?: number;
  tenant_id?: number;
  user_id?: number;
  surface: string;
  profile_id: number;
  assigned_by_user_id?: number;
};

export type AgentProfileChannelBinding = {
  id?: number;
  profile_id?: number;
  account_id: number;
  provider: string;
  binding_key: string;
  status: string;
};

export type AgentTeamPolicy = {
  orchestration: {
    mode: "coordinator" | "parallel_review" | string;
    coordinator_member?: string;
    max_rounds?: number;
    max_parallel_members?: number;
    run_timeout_seconds?: number;
    max_total_tokens?: number;
  };
  trigger: {
    require_mention?: boolean;
    commands?: string[];
    allow_direct_message?: boolean;
  };
  authorization: {
    require_tenant_member?: boolean;
    allowed_external_user_ids?: string[];
    destructive_action_mode?: string;
  };
  output: {
    final_member?: string;
    phase_updates?: string;
    post_member_cards?: boolean;
  };
};

export type AgentTeamRecord = {
  id: number;
  team_key: string;
  team_version: number;
  display_name: string;
  description?: string;
  scope?: string;
  status: string;
  schema_version?: number;
  policy_json: string;
  requested_hash?: string;
  effective_hash?: string;
  created_at?: string;
  updated_at?: string;
  published_at?: string;
};

export type AgentTeamMember = {
  id?: number;
  team_id?: number;
  member_key: string;
  profile_id: number;
  role: string;
  account_id?: number;
  tool_policy_json?: string;
  workspace_policy_json?: string;
  status?: string;
};

export type AgentTeamBinding = {
  id?: number;
  team_id?: number;
  provider: string;
  account_id: number;
  external_chat_id: string;
  external_thread_id?: string;
  trigger_policy: string;
  status?: string;
};

export type AgentTeamRunEvent = {
  id: number;
  tenant_id?: number;
  team_run_id: string;
  sequence_no: number;
  event_type: string;
  member_key?: string;
  from_member_key?: string;
  to_member_key?: string;
  status?: string;
  summary?: string;
  payload_json?: string;
  artifact_ref?: string;
  created_at: string;
};

export type AgentTeamMailboxMessage = {
  id: number;
  tenant_id?: number;
  team_run_id: string;
  from_member_key: string;
  to_member_key: string;
  message_kind: string;
  sequence_no: number;
  idempotency_key?: string;
  payload_ref?: string;
  evidence_ref?: string;
  status: string;
  created_at: string;
  updated_at?: string;
};

export type AgentTeamRunTimeline = {
  run_id: string;
  events: AgentTeamRunEvent[];
  mailbox: AgentTeamMailboxMessage[];
};

export type AgentTeamRun = {
  id: string;
  team_id?: number;
  source_account_id?: number;
  source_kind?: string;
  conversation_id?: number;
  status: string;
  coordinator_member_key?: string;
  member_count?: number;
  max_rounds?: number;
  max_parallel_members?: number;
  max_total_tokens?: number;
  used_tokens?: number;
  used_turns?: number;
  result_json?: string;
  error_code?: string;
  error_message?: string;
  created_at?: string;
  started_at?: string;
  finished_at?: string;
};

export type ChannelAccountRecord = {
  id: number;
  provider: string;
  account_key: string;
  app_id?: string;
  mode?: string;
  enabled?: boolean;
  status?: string;
};
