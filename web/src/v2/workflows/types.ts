// JSON contract: internal/agentteam/workflow/model.go. Independent of legacy Team/task events.
export const WORKFLOW = {
  schemaVersion: 2, primaryProvider: "primary", maxNodes: 16, defaultParallel: 2, maxParallel: 4,
  runSeconds: 600, maxRunSeconds: 1800, nodeSeconds: 120, maxNodeSeconds: 300,
  attempts: 2, maxAttempts: 3, backoffMS: 1000, maxBackoffMS: 10000, tokens: 32000, maxTokens: 128000,
  inputBytes: 32768, reconnectMS: 1500, readbackMS: 3000,
} as const;
export const WORKFLOW_STATUSES = ["queued", "pending", "ready", "running", "retry_wait", "completed", "failed", "timed_out", "cancel_requested", "cancelled", "interrupted", "blocked"] as const;
export type WorkflowStatus = typeof WORKFLOW_STATUSES[number];
export const TERMINAL_STATUSES = new Set<WorkflowStatus>(["completed", "failed", "timed_out", "cancelled"]);
export const WORKFLOW_FAILURE_CODES = { cancelled: "cancelled" } as const;
export const WORKFLOW_EVENT_TYPES = ["run_queued", "run_started", "task_created", "task_waiting_dependency", "agent_started", "agent_output_delta", "agent_phase", "agent_completed", "agent_failed", "task_retry_scheduled", "run_cancel_requested", "run_cancelled", "task_cancelled", "run_completed", "run_failed", "run_timed_out", "run_interrupted", "run_recovered"] as const;
export type WorkflowEventType = typeof WORKFLOW_EVENT_TYPES[number];
export type RetryPolicy = { max_attempts: number; backoff_ms: number };
export type WorkflowNode = { id: string; agent_ref: string; instruction: string; depends_on: string[]; timeout_seconds: number; retry: RetryPolicy };
export type Definition = { schema_version: number; nodes: WorkflowNode[]; limits: { max_parallel: number; run_timeout_seconds: number; max_total_tokens: number } };
export type Binding = { ref: string; profile_id?: number; profile_key: string; profile_version: number };
export type Template = { id: number; key: string; version: number; name: string; description: string; definition: Definition; agents: Binding[] };
export type TemplateInput = Omit<Template, "id" | "version" | "key"> & { key?: string };
export type Builtin = { key: string; name: string; description: string };
export type WorkflowAgent = { ref: string; profile_id: number; profile_key: string; profile_version: number; profile_hash: string; name: string; role_type: string; kind: string; system_prompt: string; provider?: string; model?: string; effort?: string; max_output_tokens: number; tools: string[] };
export type Usage = { input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_write_tokens: number };
export type Failure = { code: string; message: string; retryable: boolean };
export type Attempt = { number: number; epoch: number; task_id: number; status: WorkflowStatus; started_at: string; finished_at?: string; usage: Usage; usage_known?: boolean; budget_reserved?: number; failure?: Failure; provider?: string; model?: string };
export type NodeRun = { node_id: string; status: WorkflowStatus; attempts: Attempt[]; output?: string; artifact_id?: string; next_retry_at?: string; failure?: Failure };
export type Artifact = { id: string; node_id: string; attempt_no: number; kind: string; content: string; sha256: string; created_at: string };
export type Run = {
  id: string; scope: { tenant_id: number; user_id: number }; team_id: number; team_key: string; team_version: number;
  name: string; input: string; plan: { definition: Definition; agents: WorkflowAgent[]; hash: string };
  status: WorkflowStatus; revision: number; epoch: number; last_sequence: number; created_at: string; started_at?: string; finished_at?: string;
  deadline: string; cancel_requested_at?: string; nodes: NodeRun[]; artifacts: Artifact[]; failure?: Failure; usage: Usage; uncertain_tokens?: number;
};
// Event JSON intentionally has no run_id or epoch. Stream ownership must supply runID.
export type WorkflowEvent = { sequence: number; type: WorkflowEventType; node_id?: string; attempt_no?: number; status?: WorkflowStatus; text?: string; failure?: Failure; created_at: string };
export type EventPage = { events: WorkflowEvent[]; has_more: boolean; last_sequence: number };
