import { describe, expect, it } from "vitest";
import { parseWebUIV2Route, settingsReturnSession, SETTINGS_SECTIONS, webUIV2WorkflowPath, webUIV2SessionPath, webUIV2SettingsPath } from "./routes";

describe("WebUI v2 routes", () => {
  it("recognizes the v2 index with and without a trailing slash", () => {
    expect(parseWebUIV2Route("/webui/v2")).toEqual({ kind: "index" });
    expect(parseWebUIV2Route("/webui/v2/")).toEqual({ kind: "index" });
  });

  it("accepts the root only for the desktop v2 entrypoint", () => {
    expect(parseWebUIV2Route("/", true)).toEqual({ kind: "index" });
    expect(parseWebUIV2Route("/")).toEqual({ kind: "invalid" });
    expect(parseWebUIV2Route("/webui/agent", true)).toEqual({ kind: "invalid" });
    expect(parseWebUIV2Route("/webui/v2/sessions/not-a-ref", true)).toEqual({ kind: "invalid" });
    expect(parseWebUIV2Route("/webui/v2/settings/models", true)).toEqual({ kind: "settings", section: "models" });
  });

  it("round-trips an encoded session reference", () => {
    const ref = "tenant:release/a" as const;

    expect(webUIV2SessionPath(ref)).toBe("/webui/v2/sessions/tenant%3Arelease%2Fa");
    expect(parseWebUIV2Route(webUIV2SessionPath(ref))).toEqual({ kind: "session", ref });
  });

  it("recognizes all settings pages and preserves a return session without allowing external redirects", () => {
    expect(parseWebUIV2Route("/webui/v2/settings")).toEqual({ kind: "settings", section: "general" });
    for (const section of SETTINGS_SECTIONS) {
      const url = new URL(webUIV2SettingsPath(section, "tenant:release/a"), "http://localhost");
      expect(parseWebUIV2Route(url.pathname)).toEqual({ kind: "settings", section });
      expect(settingsReturnSession(url.search)).toBe("tenant:release/a");
    }
    expect(parseWebUIV2Route("/webui/v2/settings/unknown")).toEqual({ kind: "invalid" });
    expect(settingsReturnSession("?session=https://example.com")).toBeNull();
  });

  it("rejects incomplete, malformed, and unrelated paths", () => {
    expect(parseWebUIV2Route("/webui/v2/sessions/")).toEqual({ kind: "invalid" });
    expect(parseWebUIV2Route("/webui/v2/sessions/not-a-ref")).toEqual({ kind: "invalid" });
    expect(parseWebUIV2Route("/webui/agent")).toEqual({ kind: "invalid" });
  });
});

describe("Workflow routes", () => {
  it("round trips list, agent, new, edit and run deep links", () => {
    for (const route of [
      { kind: "workflows", page: "list" }, { kind: "workflows", page: "agents" }, { kind: "workflows", page: "new" },
      { kind: "workflows", page: "edit", key: "my/workflow" }, { kind: "workflows", page: "run", runID: "run:one/a" },
    ] as const) expect(parseWebUIV2Route(webUIV2WorkflowPath(route))).toEqual(route);
  });
  it("rejects malformed workflow routes rather than treating them as sessions or settings", () => {
    for (const path of ["/webui/v2/workflows/unknown", "/webui/v2/workflows/runs/", "/webui/v2/workflows/edit/%ZZ", "/webui/v2/workflows/edit/%20", "/webui/v2/workflows/runs/a/b"]) expect(parseWebUIV2Route(path)).toEqual({ kind: "invalid" });
  });
});
