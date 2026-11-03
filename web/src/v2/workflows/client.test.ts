import { afterEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import { createHTTPWorkflowClient } from "./client";
import { eventFixture, runFixture, templateFixture } from "./testFixtures";
const identity: IdentityConfig = { apiBase: "/prefix/api", tenantKey: "team", userId: "user", deviceId: "device", apiToken: "private-token", mobileJwt: "", model: "default" };
afterEach(() => vi.unstubAllGlobals());
describe("Workflow HTTP adapter", () => {
  it("uses existing tenant identity headers and API prefix; keeps start idempotency and version exact", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ run: runFixture(), replayed: false }), { status: 200 })); vi.stubGlobal("fetch", fetch);
    await createHTTPWorkflowClient(identity).start("key/a", 3, "goal", "stable-id");
    const [url, options] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/prefix/api/tenant/workflows/key%2Fa/runs");
    expect(new Headers(options.headers).get("Authorization")).toBe("Bearer private-token");
    expect(new Headers(options.headers).get("X-Tenant-Key")).toBe("team");
    expect(new Headers(options.headers).get("X-Device-Id")).toBe("device");
    expect(new Headers(options.headers).get("Idempotency-Key")).toBe("stable-id");
    expect(JSON.parse(String(options.body))).toEqual({ version: 3, input: "goal" });
    expect(String(options.body)).not.toContain("private-token");
  });
  it("unwraps response envelopes and covers every published endpoint", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ items: [templateFixture()], template: templateFixture(), run: runFixture(), events: [], has_more: false, last_sequence: 7 }), { status: 200 })); vi.stubGlobal("fetch", fetch);
    const client = createHTTPWorkflowClient(identity); const signal = new AbortController().signal;
    expect(await client.list(signal)).toHaveLength(1); await client.builtins(); await client.createBuiltin("parallel/join"); await client.save(templateFixture()); await client.runs("review", 2); await client.get("run/a"); await client.events("run/a", 7); await client.cancel("run/a"); await client.recover("run/a", 42);
    const calls = fetch.mock.calls as unknown as [string, RequestInit][];
    expect(calls.map(([url]) => url.replace(identity.apiBase, ""))).toEqual(["/tenant/workflows", "/tenant/workflows/templates", "/tenant/workflows/templates/parallel%2Fjoin/create", "/tenant/workflows", "/tenant/workflows/review/runs?version=2", "/tenant/workflow-runs/run%2Fa", "/tenant/workflow-runs/run%2Fa/events?after_sequence=7", "/tenant/workflow-runs/run%2Fa/cancel", "/tenant/workflow-runs/run%2Fa/recover"]);
    expect(JSON.parse(String(calls[3][1].body))).not.toHaveProperty("id"); expect(JSON.parse(String(calls[3][1].body))).not.toHaveProperty("version");
    expect(calls[0][1].signal).toBe(signal); expect(JSON.parse(String(calls[8][1].body))).toEqual({ revision: 42 });
  });
  it("delivers pure SSE JSON across LF/CRLF and split UTF8 chunks without legacy envelope guessing", async () => {
    const expected = [eventFixture(8, { text: "你好", type: "agent_output_delta" }), eventFixture(9, { status: "completed", type: "run_completed" })];
    const encoded = new TextEncoder().encode(`: heartbeat\r\n\r\nevent: workflow\r\ndata: ${JSON.stringify(expected[0])}\r\n\r\ndata: ${JSON.stringify(expected[1])}\n\n`);
    const fetch = vi.fn(async () => new Response(new ReadableStream({ start(controller) { for (let i = 0; i < encoded.length; i += 3) controller.enqueue(encoded.slice(i, i + 3)); controller.close(); } }))); vi.stubGlobal("fetch", fetch);
    const onEvent = vi.fn(); await createHTTPWorkflowClient(identity).stream("run/a", 7, onEvent, new AbortController().signal);
    expect(onEvent.mock.calls.map(([event]) => event)).toEqual(expected);
    const [url, options] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain("run%2Fa/events/stream?after_sequence=7"); expect(new Headers(options.headers).get("Authorization")).toBe("Bearer private-token");
  });
  it("surfaces real server conflict and invalid stream errors", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"error":"revision conflict"}', { status: 409 })));
    await expect(createHTTPWorkflowClient(identity).recover("run", 1)).rejects.toThrow("revision conflict");
    vi.stubGlobal("fetch", vi.fn(async () => new Response("data: {\"data\":{}}\n\n")));
    await expect(createHTTPWorkflowClient(identity).stream("run", 0, vi.fn(), new AbortController().signal)).rejects.toThrow("Invalid Workflow event JSON");
  });
});
