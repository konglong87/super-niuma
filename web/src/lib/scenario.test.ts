import { describe, expect, it, vi } from "vitest";
import { seedValidationScenario, scenarioPrompt } from "./scenario";
import type { IdentityConfig } from "./types";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "api-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "browser-a",
  model: "model-a"
};

describe("validation scenario", () => {
  it("seeds memory, profile, documents, skills, overrides, and a mobile session", async () => {
    const fetchMock = vi.fn(async (url: string) => {
      if (url.endsWith("/mobile/chat/sessions")) {
        return new Response(JSON.stringify({ id: 42 }), { status: 200 });
      }
      return new Response(JSON.stringify({ id: 1 }), { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await seedValidationScenario(identity);
    const urls = fetchMock.mock.calls.map((call) => call[0]);

    expect(result).toEqual({ sessionId: 42, prompt: scenarioPrompt });
    expect(urls).toEqual([
      "/api/tenant/users",
      "/api/tenant/users",
      "/api/tenant/users",
      "/api/tenant/memories",
      "/api/tenant/profile",
      "/api/tenant/documents",
      "/api/tenant/skills",
      "/api/tenant/skill-overrides",
      "/api/mobile/chat/sessions"
    ]);
  });
});
