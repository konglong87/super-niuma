import type { ProfileDraftIssue, ProfileDraftReport } from "./agentProfiles";
import type { AgentProfileDocument } from "./types";

export const WORKFLOW_PROFILE_SCHEMA_VERSION = 2 as const;
export const WORKFLOW_EXECUTION_KIND = "text" as const;
export const PROFILE_EXECUTION_KINDS = [WORKFLOW_EXECUTION_KIND, "computer", "image"] as const;
export type ProfileExecutionKind = (typeof PROFILE_EXECUTION_KINDS)[number];
export const WORKFLOW_AGENT_ROLE_TYPES = ["planner", "researcher", "coder", "reviewer", "executor", "general"] as const;
export type WorkflowAgentRoleType = (typeof WORKFLOW_AGENT_ROLE_TYPES)[number];

// Local MVP editor choices only: the server registry/policy remains authoritative.
export const WORKFLOW_AGENT_ROLE_DESCRIPTORS = WORKFLOW_AGENT_ROLE_TYPES.map((key) => ({
  key,
  display_name: key[0].toUpperCase() + key.slice(1)
}));

export type AgentProfileV2Document = Omit<AgentProfileDocument, "schema_version" | "identity" | "execution"> & {
  schema_version: typeof WORKFLOW_PROFILE_SCHEMA_VERSION;
  identity: AgentProfileDocument["identity"] & { role_type: WorkflowAgentRoleType };
  execution: AgentProfileDocument["execution"] & { kind: ProfileExecutionKind };
};

export type WorkflowAgentProfileDocument = Omit<AgentProfileV2Document, "execution"> & {
  execution: AgentProfileDocument["execution"] & { kind: typeof WORKFLOW_EXECUTION_KIND };
};

const MAX_PROFILE_JSON_BYTES = 256 * 1024;
const MAX_DISPLAY_NAME_BYTES = 128;
const MAX_DESCRIPTION_BYTES = 4096;
const MAX_PROMPT_BYTES = 16384;
const utf8 = new TextEncoder();

type JSONRecord = Record<string, unknown>;

export function isWorkflowAgentRoleType(value: unknown): value is WorkflowAgentRoleType {
  return typeof value === "string" && WORKFLOW_AGENT_ROLE_TYPES.some((key) => key === value);
}

export function defaultWorkflowAgentProfileDocument(roleType: WorkflowAgentRoleType = "general"): WorkflowAgentProfileDocument {
  if (!isWorkflowAgentRoleType(roleType)) throw new Error("Unknown workflow agent role_type");
  const displayName = WORKFLOW_AGENT_ROLE_DESCRIPTORS.find((role) => role.key === roleType)?.display_name;
  return {
    schema_version: WORKFLOW_PROFILE_SCHEMA_VERSION,
    identity: { role_type: roleType, display_name: displayName },
    prompt: { mode: "chat", language: "zh-CN" },
    capabilities: {
      tools: { allow: [], deny: [] },
      skills: [],
      mcp_servers: [],
      allow_agents: false,
      allow_attachments: false
    },
    execution: { kind: WORKFLOW_EXECUTION_KIND },
    context: {
      workspace: false,
      git: false,
      tenant_memory: false,
      user_memory: false,
      knowledge_base: false,
      session_history: false
    },
    safety: { permission_mode: "ask", sandbox: "required", allow_unsandboxed_commands: false }
  };
}

// This checks untrusted JSON before it is cast or used by the editor. It does
// not grant tool access or replace server catalog/budget/workflow compilation.
export function validateWorkflowAgentProfileDraft(value: unknown): ProfileDraftReport {
  return validateProfileV2Draft(value, true);
}

// Profile editors may preserve deferred kinds; only workflow compilation can
// authorize execution. This validator does not silently coerce them to text.
export function validateAgentProfileV2Draft(value: unknown): ProfileDraftReport {
  return validateProfileV2Draft(value, false);
}

function validateProfileV2Draft(value: unknown, workflowOnly: boolean): ProfileDraftReport {
  const issues: ProfileDraftIssue[] = [];
  const add = (code: string, field: string, message: string) => issues.push({ code, field, message });
  const object = (input: unknown, field: string, keys: readonly string[]): JSONRecord => {
    if (!input || typeof input !== "object" || Array.isArray(input)) {
      add("profile_object_required", field, `${field || "Profile"} must be a JSON object`);
      return {};
    }
    for (const key of Object.keys(input)) {
      if (!keys.includes(key)) add("profile_field_unknown", field ? `${field}.${key}` : key, "Unknown profile field");
    }
    return input as JSONRecord;
  };
  const strings = (input: JSONRecord, prefix: string, keys: readonly string[]) => {
    for (const key of keys) {
      if (input[key] !== undefined && typeof input[key] !== "string") add("profile_field_type_invalid", `${prefix}.${key}`, "Expected a string");
    }
  };
  const booleans = (input: JSONRecord, prefix: string, keys: readonly string[]) => {
    for (const key of keys) {
      if (input[key] !== undefined && typeof input[key] !== "boolean") add("profile_field_type_invalid", `${prefix}.${key}`, "Expected a boolean");
    }
  };
  const lists = (input: JSONRecord, prefix: string, keys: readonly string[]) => {
    for (const key of keys) {
      const list = input[key];
      if (list !== undefined && (!Array.isArray(list) || !list.every((item) => typeof item === "string"))) {
        add("profile_field_type_invalid", `${prefix}.${key}`, "Expected an array of strings");
      }
    }
  };
  const size = (input: unknown, limit: number, field: string, code: string) => {
    if (typeof input === "string" && utf8.encode(input.trim()).length > limit) add(code, field, "Field exceeds maximum UTF-8 size");
  };

  const document = object(value, "", ["schema_version", "identity", "prompt", "capabilities", "execution", "context", "safety"]);
  if (document.schema_version !== WORKFLOW_PROFILE_SCHEMA_VERSION) add("schema_version_invalid", "schema_version", "schema_version must be 2");
  const identity = object(document.identity, "identity", ["role_type", "display_name", "description"]);
  strings(identity, "identity", ["role_type", "display_name", "description"]);
  if (!isWorkflowAgentRoleType(identity.role_type)) add("role_type_invalid", "identity.role_type", "Unknown workflow agent role_type");
  size(identity.display_name, MAX_DISPLAY_NAME_BYTES, "identity.display_name", "display_name_too_large");
  size(identity.description, MAX_DESCRIPTION_BYTES, "identity.description", "description_too_large");

  const prompt = object(document.prompt, "prompt", ["mode", "persona", "output_style", "language", "system_addendum"]);
  strings(prompt, "prompt", ["mode", "persona", "output_style", "language", "system_addendum"]);
  if (prompt.mode !== "chat" && prompt.mode !== "code") add("prompt_mode_invalid", "prompt.mode", "prompt.mode must be chat or code");
  size(prompt.persona, MAX_PROMPT_BYTES, "prompt.persona", "persona_too_large");
  size(prompt.system_addendum, MAX_PROMPT_BYTES, "prompt.system_addendum", "system_addendum_too_large");

  const capabilities = object(document.capabilities, "capabilities", ["tools", "skills", "mcp_servers", "allow_agents", "allow_attachments"]);
  const tools = object(capabilities.tools, "capabilities.tools", ["allow", "deny"]);
  lists(tools, "capabilities.tools", ["allow", "deny"]);
  lists(capabilities, "capabilities", ["skills", "mcp_servers"]);
  booleans(capabilities, "capabilities", ["allow_agents", "allow_attachments"]);

  const execution = object(document.execution, "execution", ["kind", "model", "provider", "runtime_profile", "effort", "max_turns", "max_tokens", "max_parallel_read_only_tools", "auto_compact"]);
  strings(execution, "execution", ["kind", "model", "provider", "runtime_profile", "effort"]);
  if (!PROFILE_EXECUTION_KINDS.some((kind) => kind === execution.kind)) add("execution_kind_invalid", "execution.kind", "Unknown execution kind");
  else if (workflowOnly && execution.kind !== WORKFLOW_EXECUTION_KIND) add("execution_kind_unsupported", "execution.kind", "MVP workflows support text execution only");
  booleans(execution, "execution", ["auto_compact"]);
  for (const key of ["max_turns", "max_tokens", "max_parallel_read_only_tools"]) {
    const budget = execution[key];
    if (budget !== undefined && (typeof budget !== "number" || !Number.isSafeInteger(budget) || budget < 0)) {
      add("profile_budget_invalid", `execution.${key}`, "Expected a non-negative safe integer");
    }
  }

  const contextKeys = ["workspace", "git", "tenant_memory", "user_memory", "knowledge_base", "session_history"];
  const context = object(document.context, "context", contextKeys);
  booleans(context, "context", contextKeys);
  if (prompt.mode === "chat" && context.workspace === true) add("chat_workspace_forbidden", "context.workspace", "chat profiles cannot use workspace context");
  if (prompt.mode === "chat" && context.git === true) add("chat_git_forbidden", "context.git", "chat profiles cannot use git context");

  const safety = object(document.safety, "safety", ["permission_mode", "sandbox", "allow_unsandboxed_commands"]);
  strings(safety, "safety", ["permission_mode", "sandbox"]);
  booleans(safety, "safety", ["allow_unsandboxed_commands"]);
  if (safety.permission_mode === "bypassPermissions") add("bypass_permission_forbidden", "safety.permission_mode", "bypassPermissions cannot be configured");
  else if (!["ask", "deny", "allow"].includes(safety.permission_mode as string)) add("permission_mode_invalid", "safety.permission_mode", "Explicit permission mode required");
  if (!["required", "optional", "disabled"].includes(safety.sandbox as string)) add("sandbox_mode_invalid", "safety.sandbox", "Known sandbox mode required");
  else if (workflowOnly && safety.sandbox !== "required") add("sandbox_required", "safety.sandbox", "Workflow editor requires sandboxing");
  if (safety.allow_unsandboxed_commands === true) add("unsandboxed_commands_forbidden", "safety.allow_unsandboxed_commands", "Unsandboxed commands cannot be configured");
  return { valid: issues.length === 0, issues };
}

export function parseWorkflowAgentProfileJSON(text: string): WorkflowAgentProfileDocument {
  return parseProfileV2JSON(text, true) as WorkflowAgentProfileDocument;
}

export function parseAgentProfileV2JSON(text: string): AgentProfileV2Document {
  return parseProfileV2JSON(text, false);
}

function parseProfileV2JSON(text: string, workflowOnly: boolean): AgentProfileV2Document {
  if (utf8.encode(text).length > MAX_PROFILE_JSON_BYTES) throw new Error("Profile JSON exceeds maximum size");
  const value: unknown = JSON.parse(text);
  const report = validateProfileV2Draft(value, workflowOnly);
  if (!report.valid) throw new Error(report.issues.map((issue) => `${issue.field}: ${issue.message}`).join("; "));
  return value as AgentProfileV2Document;
}
