import { afterEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import { createWorkflowAgentClient, emptyAgentDraft, isWorkflowBindableAgent } from "./agentClient";
import { agentFixture } from "./testFixtures";

const identity: IdentityConfig = { apiBase: "/api", tenantKey: "team", userId: "user", deviceId: "device", apiToken: "secret", mobileJwt: "", model: "default" };
afterEach(() => vi.unstubAllGlobals());
describe("Workflow Agent profile adapter", () => {
  it("selects only published database text profiles with explicit no-tool capabilities", () => {
    const agent = agentFixture(); expect(isWorkflowBindableAgent(agent)).toBe(true);
    expect(isWorkflowBindableAgent({ ...agent, record: { ...agent.record, id: 0 } })).toBe(false);
    expect(isWorkflowBindableAgent({ ...agent, record: { ...agent.record, status: "draft" } })).toBe(false);
    agent.document.capabilities.tools.allow = ["Shell"]; expect(isWorkflowBindableAgent(agent)).toBe(false);
  });
  it("creates a private V2 text draft with explicit no tools and no credential fields", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(agentFixture().record))); vi.stubGlobal("fetch", fetch);
    await createWorkflowAgentClient(identity).save({ ...emptyAgentDraft(), name: "Reviewer", prompt: "Be careful", role: "reviewer" });
    const [url, options] = fetch.mock.calls[0] as unknown as [string, RequestInit]; const body = JSON.parse(String(options.body));
    expect(url).toBe("/api/tenant/agent-profiles"); expect(body.scope).toBe("user_private"); expect(body.config.identity.role_type).toBe("reviewer"); expect(body.config.execution).toEqual({ kind: "text" }); expect(body.config.capabilities.tools.allow).toEqual([]); expect(body.config.capabilities.allow_agents).toBe(false); expect(body.config.capabilities.mcp_servers).toEqual([]); expect(String(options.body)).not.toContain("secret");
  });
  it.each([{ id: 0, scope: "builtin" }, { id: 11, scope: "tenant_shared" }])("never patches catalog/shared profiles when creating a personal copy: %o", async (source) => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(agentFixture().record))); vi.stubGlobal("fetch", fetch);
    await createWorkflowAgentClient(identity).save({ ...emptyAgentDraft(), name: "Personal" }, { ...agentFixture().record, ...source, profile_key: "builtin" });
    const [url, options] = fetch.mock.calls[0] as unknown as [string, RequestInit]; expect(url).toBe("/api/tenant/agent-profiles"); expect(options.method).toBe("POST"); expect(JSON.parse(String(options.body)).profile_key).not.toBe("builtin");
  });
});

describe("Workflow primary model route", () => {
  it("offers primary when the existing named-provider catalog is empty", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ data: [] })));
    vi.stubGlobal("fetch", fetch);
    expect(await createWorkflowAgentClient(identity).providers()).toEqual([{ name: "primary", model: "" }]);
    const [url] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/providers");
  });
  it("keeps configured named routes and never invents a stale primary model", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ data: [{ name: "review", model: "pinned" }] }))));
    expect(await createWorkflowAgentClient(identity).providers()).toEqual([{ name: "primary", model: "" }, { name: "review", model: "pinned" }]);
  });
  it("does not duplicate an explicitly supplied primary entry", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ data: [{ name: "primary", model: "current" }] }))));
    expect(await createWorkflowAgentClient(identity).providers()).toEqual([{ name: "primary", model: "current" }]);
  });
});
