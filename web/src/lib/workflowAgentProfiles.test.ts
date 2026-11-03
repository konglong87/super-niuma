import { describe, expect, it } from "vitest";
import {
  defaultWorkflowAgentProfileDocument,
  isWorkflowAgentRoleType,
  parseAgentProfileV2JSON,
  parseWorkflowAgentProfileJSON,
  PROFILE_EXECUTION_KINDS,
  validateAgentProfileV2Draft,
  validateWorkflowAgentProfileDraft,
  WORKFLOW_AGENT_ROLE_DESCRIPTORS,
  WORKFLOW_AGENT_ROLE_TYPES
} from "./workflowAgentProfiles";

function clone(value: unknown): Record<string, any> {
  return JSON.parse(JSON.stringify(value));
}

describe("workflow V2 profile helpers", () => {
  it("creates independent general/text/noTools documents with no implicit context or subagents", () => {
    const document = defaultWorkflowAgentProfileDocument();
    expect(document.schema_version).toBe(2);
    expect(document.identity).toEqual({ role_type: "general", display_name: "General" });
    expect(document.execution).toEqual({ kind: "text" });
    expect(document.capabilities).toEqual({
      tools: { allow: [], deny: [] }, skills: [], mcp_servers: [], allow_agents: false, allow_attachments: false
    });
    expect(Object.values(document.context).every((value) => value === false)).toBe(true);
    expect(document.safety).toEqual({ permission_mode: "ask", sandbox: "required", allow_unsandboxed_commands: false });
    expect(validateWorkflowAgentProfileDraft(document)).toEqual({ valid: true, issues: [] });
    document.capabilities.tools.allow?.push("Bash");
    expect(defaultWorkflowAgentProfileDocument().capabilities.tools.allow).toEqual([]);
  });

  it("recognizes the six metadata-only roles without granting tools", () => {
    expect(WORKFLOW_AGENT_ROLE_TYPES).toEqual(["planner", "researcher", "coder", "reviewer", "executor", "general"]);
    expect(WORKFLOW_AGENT_ROLE_DESCRIPTORS.map((role) => role.display_name)).toEqual(["Planner", "Researcher", "Coder", "Reviewer", "Executor", "General"]);
    for (const role of WORKFLOW_AGENT_ROLE_TYPES) {
      const document = defaultWorkflowAgentProfileDocument(role);
      expect(isWorkflowAgentRoleType(role)).toBe(true);
      expect(document.identity.role_type).toBe(role);
      expect(document.capabilities.tools.allow).toEqual([]);
      expect(document.context.workspace).toBe(false);
    }
    expect(isWorkflowAgentRoleType("admin")).toBe(false);
    expect(isWorkflowAgentRoleType(null)).toBe(false);
    expect(() => defaultWorkflowAgentProfileDocument("admin" as never)).toThrow("role_type");
  });

  it("round trips V2 fields and explicit model refs without stripping or mutating them", () => {
    const document = defaultWorkflowAgentProfileDocument("planner");
    document.identity.description = "A planning assistant";
    document.prompt.persona = "Plan, do not execute.";
    document.execution.provider = "existing-provider";
    document.execution.model = "existing-model";
    const encoded = JSON.stringify(document);
    expect(parseWorkflowAgentProfileJSON(encoded)).toEqual(document);
    expect(JSON.stringify(document)).toBe(encoded);
  });

  it("distinguishes recognized deferred kinds from MVP workflow support", () => {
    expect(PROFILE_EXECUTION_KINDS).toEqual(["text", "computer", "image"]);
    for (const kind of ["computer", "image"]) {
      const document = clone(defaultWorkflowAgentProfileDocument());
      document.execution.kind = kind;
      expect(validateAgentProfileV2Draft(document).valid).toBe(true);
      expect(parseAgentProfileV2JSON(JSON.stringify(document)).execution.kind).toBe(kind);
      expect(validateWorkflowAgentProfileDraft(document).issues).toContainEqual(expect.objectContaining({ code: "execution_kind_unsupported", field: "execution.kind" }));
      expect(() => parseWorkflowAgentProfileJSON(JSON.stringify(document))).toThrow("text execution only");
    }
  });

  it.each([
    ["unknown role", (d: Record<string, any>) => { d.identity.role_type = "admin"; }, "role_type_invalid"],
    ["missing role", (d: Record<string, any>) => { delete d.identity.role_type; }, "role_type_invalid"],
    ["unknown kind", (d: Record<string, any>) => { d.execution.kind = "shell"; }, "execution_kind_invalid"],
    ["missing kind", (d: Record<string, any>) => { delete d.execution.kind; }, "execution_kind_invalid"],
    ["V1 version", (d: Record<string, any>) => { d.schema_version = 1; }, "schema_version_invalid"],
    ["unknown version", (d: Record<string, any>) => { d.schema_version = 3; }, "schema_version_invalid"],
    ["bypass", (d: Record<string, any>) => { d.safety.permission_mode = "bypassPermissions"; }, "bypass_permission_forbidden"],
    ["unsandboxed", (d: Record<string, any>) => { d.safety.allow_unsandboxed_commands = true; }, "unsandboxed_commands_forbidden"],
    ["disabled sandbox", (d: Record<string, any>) => { d.safety.sandbox = "disabled"; }, "sandbox_required"],
    ["unknown permission", (d: Record<string, any>) => { d.safety.permission_mode = "admin"; }, "permission_mode_invalid"],
    ["chat workspace", (d: Record<string, any>) => { d.context.workspace = true; }, "chat_workspace_forbidden"],
    ["chat git", (d: Record<string, any>) => { d.context.git = true; }, "chat_git_forbidden"],
    ["unknown field", (d: Record<string, any>) => { d.execution.api_key = "not-a-real-key"; }, "profile_field_unknown"],
    ["bad tools", (d: Record<string, any>) => { d.capabilities.tools.allow = ["Read", 42]; }, "profile_field_type_invalid"],
    ["bad boolean", (d: Record<string, any>) => { d.context.workspace = "false"; }, "profile_field_type_invalid"],
    ["bad budget", (d: Record<string, any>) => { d.execution.max_turns = -1; }, "profile_budget_invalid"],
    ["fractional budget", (d: Record<string, any>) => { d.execution.max_tokens = 1.5; }, "profile_budget_invalid"],
    ["oversized UTF-8 name", (d: Record<string, any>) => { d.identity.display_name = "中".repeat(43); }, "display_name_too_large"],
    ["oversized persona", (d: Record<string, any>) => { d.prompt.persona = "x".repeat(16385); }, "persona_too_large"]
  ])("rejects %s", (_name, mutate, code) => {
    const document = clone(defaultWorkflowAgentProfileDocument());
    mutate(document);
    const report = validateWorkflowAgentProfileDraft(document);
    expect(report.valid).toBe(false);
    expect(report.issues.map((issue) => issue.code)).toContain(code);
    expect(() => parseWorkflowAgentProfileJSON(JSON.stringify(document))).toThrow();
  });

  it("rejects malformed JSON/shapes/oversized input instead of throwing inside shape validation", () => {
    for (const value of [null, [], true, "string", {}, { schema_version: 2, prompt: null, capabilities: [] }]) {
      expect(validateWorkflowAgentProfileDraft(value).valid).toBe(false);
      expect(() => parseWorkflowAgentProfileJSON(JSON.stringify(value))).toThrow();
    }
    expect(() => parseWorkflowAgentProfileJSON("{")).toThrow();
    expect(() => parseWorkflowAgentProfileJSON(`${JSON.stringify(defaultWorkflowAgentProfileDocument())} {}`)).toThrow();
    expect(() => parseWorkflowAgentProfileJSON(" ".repeat(256 * 1024 + 1))).toThrow("maximum size");
    const prototypeKey = JSON.stringify(defaultWorkflowAgentProfileDocument()).replace('"schema_version":2', '"schema_version":2,"__proto__":{"admin":true}');
    expect(() => parseWorkflowAgentProfileJSON(prototypeKey)).toThrow("Unknown profile field");
    expect(({} as { admin?: boolean }).admin).toBeUndefined();
  });
});
