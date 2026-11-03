import { describe, expect, it } from "vitest";
import {
  defaultAgentProfileDocument,
  defaultTeamPolicy,
  parseAgentProfileJSON,
  profileCapabilitySummary,
  validateAgentProfileDraft
} from "./agentProfiles";
import { defaultWorkflowAgentProfileDocument } from "./workflowAgentProfiles";

describe("agent profile editor helpers", () => {
  it("creates safe builtin-shaped documents for chat and coder profiles", () => {
    const chat = defaultAgentProfileDocument("chat");
    const coder = defaultAgentProfileDocument("code");

    expect(chat.prompt.mode).toBe("chat");
    expect(chat.context.workspace).toBe(false);
    expect(chat.safety.sandbox).toBe("required");
    expect(coder.prompt.mode).toBe("code");
    expect(coder.context.workspace).toBe(true);
    expect(coder.capabilities.tools.allow).toContain("Bash");
  });

  it("rejects malformed and unsafe profile drafts before an API call", () => {
    expect(parseAgentProfileJSON("{\"schema_version\":1}")).toEqual({ schema_version: 1 });
    expect(() => parseAgentProfileJSON("[]")).toThrow("JSON object");

    const report = validateAgentProfileDraft({
      ...defaultAgentProfileDocument("chat"),
      prompt: { ...defaultAgentProfileDocument("chat").prompt, mode: "chat" },
      context: { ...defaultAgentProfileDocument("chat").context, workspace: true },
      safety: { ...defaultAgentProfileDocument("chat").safety, permission_mode: "bypassPermissions" }
    });

    expect(report.valid).toBe(false);
    expect(report.issues.map((issue) => issue.code)).toEqual(expect.arrayContaining(["chat_workspace_forbidden", "bypass_permission_forbidden"]));
  });

  it("summarizes capabilities without leaking raw profile content", () => {
    const summary = profileCapabilitySummary({
      ...defaultAgentProfileDocument("chat"),
      capabilities: {
        ...defaultAgentProfileDocument("chat").capabilities,
        tools: { allow: ["Read", "WebSearch"], deny: ["Bash"] },
        skills: ["copywriting"]
      }
    });

    expect(summary.tools).toBe("2 allowed / 1 denied");
    expect(summary.skills).toBe(1);
    expect(summary.context).toContain("tenant memory");
  });

  it("provides a coordinator-first team policy", () => {
    expect(defaultTeamPolicy()).toMatchObject({
      orchestration: { mode: "coordinator", max_rounds: 4, max_parallel_members: 3 },
      output: { phase_updates: "coordinator_only", post_member_cards: false }
    });
  });
});


describe("versioned profile editor compatibility", () => {
  it("preserves V1 parsing, serialization, summary and edit/copy behavior", () => {
    for (const mode of ["chat", "code"] as const) {
      const original = defaultAgentProfileDocument(mode);
      const before = JSON.stringify(original);
      const summary = profileCapabilitySummary(original);
      const copy = parseAgentProfileJSON(before);
      expect(JSON.stringify(copy)).toBe(before);
      expect(copy.identity.role_type).toBeUndefined();
      expect(copy.execution.kind).toBeUndefined();
      expect(validateAgentProfileDraft(copy)).toEqual({ valid: true, issues: [] });
      expect(profileCapabilitySummary(copy)).toEqual(summary);
      copy.identity.display_name = "Copied profile";
      expect(original.identity.display_name).not.toBe("Copied profile");
    }
    // The old advanced parser intentionally leaves V1 completeness to validation.
    expect(parseAgentProfileJSON('{"schema_version":1}')).toEqual({ schema_version: 1 });
  });

  it("accepts safe V2 documents without dropping role/kind or changing summaries", () => {
    const document = defaultWorkflowAgentProfileDocument("reviewer");
    document.prompt.persona = "Review the supplied artifacts.";
    const copy = parseAgentProfileJSON(JSON.stringify(document));
    expect(copy).toEqual(document);
    expect(copy.identity.role_type).toBe("reviewer");
    expect(copy.execution.kind).toBe("text");
    expect(validateAgentProfileDraft(copy)).toEqual({ valid: true, issues: [] });
    expect(profileCapabilitySummary(copy)).toEqual({ tools: "0 allowed / 0 denied", skills: 0, context: "none" });
    copy.identity.display_name = "Copied reviewer";
    expect(parseAgentProfileJSON(JSON.stringify(copy)).identity.role_type).toBe("reviewer");
    expect(document.identity.display_name).toBe("Reviewer");
  });

  it("preserves recognized future kinds rather than treating them as text", () => {
    for (const kind of ["computer", "image"]) {
      const document = defaultWorkflowAgentProfileDocument();
      const value = { ...document, execution: { ...document.execution, kind } };
      const copy = parseAgentProfileJSON(JSON.stringify(value));
      expect(copy.execution.kind).toBe(kind);
      expect(validateAgentProfileDraft(copy).valid).toBe(true);
    }
  });

  it("uses V2 structural and safety checks in the existing editor", () => {
    const document = defaultWorkflowAgentProfileDocument();
    const unsafe = {
      ...document,
      identity: { ...document.identity, role_type: "admin" },
      execution: { ...document.execution, kind: "shell" },
      safety: { ...document.safety, permission_mode: "bypassPermissions", allow_unsandboxed_commands: true }
    };
    const report = validateAgentProfileDraft(unsafe);
    expect(report.valid).toBe(false);
    expect(report.issues.map((issue) => issue.code)).toEqual(expect.arrayContaining([
      "role_type_invalid", "execution_kind_invalid", "bypass_permission_forbidden", "unsandboxed_commands_forbidden"
    ]));
    expect(() => parseAgentProfileJSON(JSON.stringify(unsafe))).toThrow();
    expect(() => parseAgentProfileJSON('{"schema_version":2}')).toThrow();
    expect(validateAgentProfileDraft({ ...document, schema_version: 3 }).valid).toBe(false);
  });
});
