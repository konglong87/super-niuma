import { describe, expect, it } from "vitest";
import { buildCapabilityChecks, buildValidationChecks, validationScore } from "./validation";
import type { ValidationSnapshot } from "./validation";

const baseSnapshot: ValidationSnapshot = {
  selectedSessionId: null,
  messageCount: 0,
  memories: [],
  skills: [],
  tenantSkills: [],
  skillOverrides: [],
  documents: [],
  telemetry: [],
  profile: null,
  trace: null
};

describe("validation checks", () => {
  it("marks missing lifecycle data as failing or warning", () => {
    const checks = buildValidationChecks(baseSnapshot);
    const score = validationScore(checks);

    expect(checks.find((item) => item.key === "session")?.status).toBe("fail");
    expect(checks.find((item) => item.key === "memory")?.status).toBe("warn");
    expect(score.total).toBe(10);
    expect(score.failing).toBeGreaterThan(0);
  });

  it("passes when mobile lifecycle data is present", () => {
    const checks = buildValidationChecks({
      ...baseSnapshot,
      selectedSessionId: 7,
      messageCount: 2,
      memories: [{ memory_key: "pref", content: "vim" }],
      profile: { profile_version: 1, summary: "tester" },
      documents: [{ doc_type: "CLAUDE.md", content_md: "# Memory" }],
      skills: [{ skill_key: "review", name: "Review", version: 1 }],
      tenantSkills: [{ skill_key: "review", name: "Review", version: 1 }],
      skillOverrides: [{ skill_key: "review", version: 1, enabled: true }],
      telemetry: [
        { name: "mobile.chat.stream.finished", status: "ok" },
        { name: "model.request.finished", status: "ok" }
      ],
      trace: {
        summary: {
          messages: 2,
          tool_calls: 1,
          total_tokens: 12,
          cache_summary: "read 4",
          skills: ["review"]
        }
      }
    });

    expect(validationScore(checks)).toEqual({ passed: 10, warning: 0, failing: 0, total: 10 });
  });

  it("extracts capability evidence from trace and telemetry", () => {
    const checks = buildCapabilityChecks({
      ...baseSnapshot,
      selectedSessionId: 7,
      messageCount: 8,
      memories: [{ memory_key: "pref", content: "vim" }],
      profile: { profile_version: 1, summary: "tester" },
      documents: [{ doc_type: "CLAUDE.md", content_md: "# Memory" }],
      skills: [{ skill_key: "review", name: "Review", version: 1 }],
      telemetry: [
        { name: "mobile.chat.stream.finished", status: "ok" },
        { name: "query.autocompact.success", status: "ok" },
        { name: "tool.execution.finished", status: "ok", properties_json: "{\"active_skill\":\"review\"}" }
      ],
      trace: {
        summary: {
          messages: 8,
          cache_summary: "read 4",
          skills: ["review"]
        },
        events: [{ type: "compact_summary" }, { name: "tool.execution.finished", skill: "review" }]
      }
    });

    expect(checks.find((item) => item.key === "auto-compact")?.status).toBe("pass");
    expect(checks.find((item) => item.key === "skills-progressive")?.detail).toContain("review");
    expect(validationScore(checks).passed).toBe(7);
  });
});
