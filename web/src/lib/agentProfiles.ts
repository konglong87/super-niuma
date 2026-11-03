import type { AgentProfileDocument, AgentTeamPolicy } from "./types";
import { parseAgentProfileV2JSON, validateAgentProfileV2Draft, WORKFLOW_PROFILE_SCHEMA_VERSION } from "./workflowAgentProfiles";

// Keep the shared V1 wire types unchanged while preserving typed metadata in
// the existing advanced editor, copy, and summary paths.
export type AgentProfileEditorDocument = AgentProfileDocument & {
  identity: AgentProfileDocument["identity"] & { role_type?: string };
  execution: AgentProfileDocument["execution"] & { kind?: string };
};

export type ProfileDraftIssue = { code: string; field: string; message: string };

export type ProfileDraftReport = { valid: boolean; issues: ProfileDraftIssue[] };

export function defaultAgentProfileDocument(mode: "chat" | "code" = "chat"): AgentProfileDocument {
  const isCode = mode === "code";
  return {
    schema_version: 1,
    identity: { display_name: isCode ? "Coder" : "Chat Assistant", description: isCode ? "Guarded coding agent" : "General conversation assistant" },
    prompt: { mode, language: "zh-CN", output_style: isCode ? "technical" : "concise" },
    capabilities: {
      tools: { allow: isCode ? ["Bash", "Edit", "Glob", "Grep", "Read"] : [], deny: [] },
      skills: [],
      mcp_servers: [],
      allow_agents: isCode,
      allow_attachments: true
    },
    execution: {
      effort: isCode ? "high" : "medium",
      max_turns: isCode ? 20 : 3,
      max_tokens: isCode ? 8192 : 2048,
      max_parallel_read_only_tools: isCode ? 4 : 0,
      auto_compact: isCode
    },
    context: {
      workspace: isCode,
      git: isCode,
      tenant_memory: true,
      user_memory: true,
      knowledge_base: true,
      session_history: true
    },
    safety: { permission_mode: "ask", sandbox: "required", allow_unsandboxed_commands: false }
  };
}

export function defaultTeamPolicy(): AgentTeamPolicy {
  return {
    orchestration: { mode: "coordinator", coordinator_member: "coordinator", max_rounds: 4, max_parallel_members: 3, run_timeout_seconds: 900, max_total_tokens: 24000 },
    trigger: { require_mention: true, commands: ["/team"], allow_direct_message: true },
    authorization: { require_tenant_member: true, allowed_external_user_ids: [], destructive_action_mode: "ask" },
    output: { final_member: "coordinator", phase_updates: "coordinator_only", post_member_cards: false }
  };
}

export function parseAgentProfileJSON(text: string): AgentProfileEditorDocument {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error) {
    throw new Error(error instanceof Error ? error.message : "Invalid JSON");
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Profile JSON object is required");
  }
  if ((value as { schema_version?: unknown }).schema_version === WORKFLOW_PROFILE_SCHEMA_VERSION) {
    return parseAgentProfileV2JSON(text);
  }
  return value as AgentProfileEditorDocument;
}

export function validateAgentProfileDraft(document: AgentProfileDocument): ProfileDraftReport {
  if (document.schema_version === WORKFLOW_PROFILE_SCHEMA_VERSION) return validateAgentProfileV2Draft(document);
  const issues: ProfileDraftIssue[] = [];
  if (document.schema_version !== 1) issues.push({ code: "schema_version_invalid", field: "schema_version", message: "schema_version must be 1" });
  if (document.prompt.mode !== "chat" && document.prompt.mode !== "code") issues.push({ code: "prompt_mode_invalid", field: "prompt.mode", message: "prompt.mode must be chat or code" });
  if (document.prompt.mode === "chat" && document.context.workspace) issues.push({ code: "chat_workspace_forbidden", field: "context.workspace", message: "chat profiles cannot use workspace context" });
  if (document.prompt.mode === "chat" && document.context.git) issues.push({ code: "chat_git_forbidden", field: "context.git", message: "chat profiles cannot use git context" });
  if (document.safety.permission_mode === "bypassPermissions") issues.push({ code: "bypass_permission_forbidden", field: "safety.permission_mode", message: "bypassPermissions cannot be configured" });
  if (document.safety.allow_unsandboxed_commands) issues.push({ code: "unsandboxed_commands_forbidden", field: "safety.allow_unsandboxed_commands", message: "unsandboxed commands are not configurable" });
  return { valid: issues.length === 0, issues };
}

export function profileCapabilitySummary(document: AgentProfileDocument): { tools: string; skills: number; context: string } {
  const allow = document.capabilities.tools.allow?.length ?? 0;
  const deny = document.capabilities.tools.deny?.length ?? 0;
  const context = [
    document.context.workspace ? "workspace" : "",
    document.context.git ? "git" : "",
    document.context.tenant_memory ? "tenant memory" : "",
    document.context.user_memory ? "user memory" : "",
    document.context.knowledge_base ? "knowledge base" : ""
  ].filter(Boolean).join(", ");
  return { tools: `${allow} allowed / ${deny} denied`, skills: document.capabilities.skills?.length ?? 0, context: context || "none" };
}
