import { afterEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import { createHTTPSessionControlClient } from "./httpSessionControlClient";
import { sessionControlErrorCode } from "./sessionControlClient";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "device-a",
  model: "test-model"
};

const summary = {
  ref: "tenant:release" as const,
  source: "tenant" as const,
  title: "Release coordination",
  status: "running" as const,
  updated_at: "2026-09-05T00:00:00.000Z",
  short_id: "release"
};

const detail = { ...summary, messages: [], activity: [], context: [], changes: [], runs: [] };
const operation = { operation_id: "operation-1", replayed: false, session: detail, run_id: 42 };
const conversation = { session: summary, events: [], cursor: "0", has_more: false };

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("HTTP session control client", () => {
  it("renames through the existing tenant PATCH route with only the trimmed title", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: 7 }));
    vi.stubGlobal("fetch", fetchMock);
    await createHTTPSessionControlClient().rename!(identity, { ref: "tenant:release", id: 7, title: "  Updated title  " });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, request] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/tenant/sessions/7");
    expect(request.method).toBe("PATCH");
    expect(JSON.parse(request.body)).toEqual({ title: "Updated title" });
    const headers = new Headers(request.headers);
    expect(headers.get("Authorization")).toBe("Bearer test-token");
    expect(headers.get("X-Tenant-Key")).toBe("tenant-a");
    expect(headers.get("X-User-ID")).toBe("user-a");
  });

  it("rejects invalid or read-only rename targets before sending a request", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const rename = createHTTPSessionControlClient().rename!;
    await expect(rename(identity, { ref: "local:workspace", id: 7, title: "Changed" })).rejects.toMatchObject({ code: "local_read_only" });
    await expect(rename(identity, { ref: "tenant:release", id: 0, title: "Changed" })).rejects.toMatchObject({ code: "invalid_request" });
    await expect(rename(identity, { ref: "tenant:release", id: 7, title: "  " })).rejects.toMatchObject({ code: "invalid_request" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("does not report rename success on malformed acknowledgments or failed writes", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(jsonResponse({ id: 8 }))
      .mockResolvedValueOnce(jsonResponse({ error: "forbidden" }, 403))
      .mockRejectedValueOnce(new TypeError("offline")));
    const rename = createHTTPSessionControlClient().rename!;
    const input = { ref: "tenant:release" as const, id: 7, title: "Changed" };
    await expect(rename(identity, input)).rejects.toMatchObject({ code: "network_unavailable" });
    await expect(rename(identity, input)).rejects.toMatchObject({ code: "forbidden" });
    await expect(rename(identity, input)).rejects.toMatchObject({ code: "network_unavailable" });
  });

  it("round trips per-run configuration and inline images without copying upload credentials", async () => {
    const config = { provider: "custom", model: "sol", permission_mode: "ask", effort: "high", prompt_mode: "chat" };
    const fetchMock = vi.fn(async () => jsonResponse({ data: { ...operation, session: { ...summary, ...config } } }));
    vi.stubGlobal("fetch", fetchMock);
    const attachment = { type: "image" as const, name: "image.png", size_bytes: 3, sha256: "hash", media_type: "image/png", inline_data: "cG5n" };
    const result = await createHTTPSessionControlClient().send(identity, { ref: "tenant:release", text: "Look", sourceRefs: [], attachments: [attachment], provider: "custom", model: "sol", permissionMode: "ask", effort: "high", promptMode: "chat", idempotencyKey: "configured" });
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(JSON.parse(String(calls[0][1].body))).toEqual({ content: "Look", source_refs: [], attachments: [attachment], ...config });
    expect(result.session).toMatchObject({ provider: "custom", model: "sol", permissionMode: "ask", effort: "high", promptMode: "chat" });
  });

  it("retains server session and active run IDs for scoped queue reads", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ data: { ...conversation, session: { ...summary, id: 7, active_run_id: 42 } } })));
    expect(await createHTTPSessionControlClient().get(identity, "tenant:release")).toMatchObject({ id: 7, activeRunID: 42 });
  });
  it("reads key-based conversation pages and restores failed runs without timeline", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ data: { ...conversation, events: [{ id: 43, task_id: 14, event_type: "message", payload_json: '{"content":"hello"}' }], cursor: "43", has_more: true } }))
      .mockResolvedValueOnce(jsonResponse({ data: { ...conversation, events: [{ id: 44, task_id: 14, event_type: "failed", payload_json: '{"error":"protocol conflict"}' }], cursor: "44" } }));
    vi.stubGlobal("fetch", fetchMock);
    const result = await createHTTPSessionControlClient().get(identity, "tenant:sc-long-key");
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(["/api/tenant/session-control/sessions/tenant/sc-long-key/conversation?cursor=0", "/api/tenant/session-control/sessions/tenant/sc-long-key/conversation?cursor=43"]);
    expect(result.messages.map((message) => message.content)).toEqual(["hello", "protocol conflict"]);
    expect(result.messages[1]?.kind).toBe("error");
  });

  it("does not swallow a failed conversation read", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ code: "forbidden" }, 403)));
    await expect(createHTTPSessionControlClient().get(identity, "tenant:sc-long-key")).rejects.toMatchObject({ code: "forbidden" });
  });
  it("reads tenant and local sources through the isolated namespace without an idempotency key", async () => {
    const localSummary = { ...summary, ref: "local:workspace", source: "local", title: "Local workspace", short_id: "workspace" };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ data: [summary] }))
      .mockResolvedValueOnce(jsonResponse({ data: [localSummary] }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await createHTTPSessionControlClient().list(identity, { query: "  Release ", statuses: ["running"] });

    expect(result).toEqual([{
      ref: "tenant:release",
      source: "tenant",
      title: "Release coordination",
      status: "running",
      updatedAt: summary.updated_at,
      shortID: "release"
    }]);
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      "/api/tenant/session-control/sessions?source=tenant",
      "/api/tenant/session-control/sessions?source=local"
    ]);
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    const request = calls[0]?.[1] as RequestInit;
    const headers = new Headers(request.headers);
    expect(headers.get("Authorization")).toBe("Bearer test-token");
    expect(headers.get("X-Tenant-Key")).toBe("tenant-a");
    expect(headers.has("Idempotency-Key")).toBe(false);
  });

  it("parses the optional channel association on session summaries", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(jsonResponse({ data: [{ ...summary, channel: { provider: "feishu", account_key: "feishu-primary" } }] }))
      .mockResolvedValueOnce(jsonResponse({ data: [] })));

    const sessions = await createHTTPSessionControlClient().list(identity, { query: "", statuses: [] });

    expect(sessions[0]?.channel).toEqual({ provider: "feishu", accountKey: "feishu-primary" });
  });

  it("encodes source and ID path segments independently", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: conversation }));
    vi.stubGlobal("fetch", fetchMock);

    await createHTTPSessionControlClient().get(identity, "tenant:release/a" as "tenant:release/a");

    expect(fetchMock).toHaveBeenCalledWith("/api/tenant/session-control/sessions/tenant/release%2Fa/conversation?cursor=0", expect.objectContaining({ method: "GET" }));
  });

  it("sends initial text with create in one idempotent request", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: operation }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await createHTTPSessionControlClient().create(identity, { title: "Release coordination", initialText: "Plan the release", idempotencyKey: "create-1" });

    expect(result.operation).toMatchObject({ kind: "create", id: "operation-1", status: "completed", replayed: false });
    const [url, request] = (fetchMock.mock.calls as unknown as Array<[string, RequestInit]>)[0] as [string, RequestInit];
    expect(url).toBe("/api/tenant/session-control/sessions");
    expect(request.method).toBe("POST");
    expect(new Headers(request.headers).get("Idempotency-Key")).toBe("create-1");
    expect(JSON.parse(String(request.body))).toEqual({ title: "Release coordination", initial_text: "Plan the release" });
  });

  it("keeps an explicit runtime route override when supplied", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: operation }));
    vi.stubGlobal("fetch", fetchMock);

    await createHTTPSessionControlClient().create(identity, {
      title: "Use alternate route",
      provider: "provider-b",
      model: "model-b",
      idempotencyKey: "create-route"
    });

    const [, request] = (fetchMock.mock.calls as unknown as Array<[string, RequestInit]>)[0] as [string, RequestInit];
    expect(JSON.parse(String(request.body))).toMatchObject({ title: "Use alternate route", provider: "provider-b", model: "model-b" });
  });

  it("sends message content, references, and sanitized attachment metadata in one idempotent request", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: operation }));
    vi.stubGlobal("fetch", fetchMock);

    await createHTTPSessionControlClient().send(identity, {
      ref: "tenant:release",
      text: "Queue this",
      sourceRefs: ["tenant:source-a", "local:workspace"],
      attachments: [{
        attachment_id: "att-1",
        type: "image",
        media_type: "image/png",
        name: "screen.png",
        size_bytes: 3,
        sha256: "abc",
        url: "https://cdn.example/screen.png",
        transcript: "private transcript",
        upload_url: "https://uploads.example/private",
        inline_data: "private inline bytes"
      } as never],
      idempotencyKey: "send-1"
    });

    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls.map(([url]) => url)).toEqual([
      "/api/tenant/session-control/sessions/tenant/release/messages"
    ]);
    expect(JSON.parse(String(calls[0]?.[1]?.body))).toEqual({
      content: "Queue this",
      source_refs: ["tenant:source-a", "local:workspace"],
      attachments: [{
        attachment_id: "att-1",
        type: "image",
        media_type: "image/png",
        name: "screen.png",
        size_bytes: 3,
        sha256: "abc",
        url: "https://cdn.example/screen.png"
      }]
    });
    expect(new Headers(calls[0]?.[1]?.headers).get("Idempotency-Key")).toBe("send-1");
  });

  it("maps structured backend failures to stable UI-safe error codes", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ error: "forbidden", message: "raw backend policy detail" }, 403));
    vi.stubGlobal("fetch", fetchMock);

    const error = await createHTTPSessionControlClient().get(identity, "tenant:release").catch((reason: unknown) => reason);

    expect(sessionControlErrorCode(error)).toBe("forbidden");
    expect(error).not.toMatchObject({ message: expect.stringContaining("raw backend policy detail") });
  });

  it("preserves budget exhaustion as a specific translated error code", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: { code: "budget_exceeded", message: "Protected evidence needs 6546 tokens" } }, 400)));

    const error = await createHTTPSessionControlClient().get(identity, "tenant:release").catch((reason: unknown) => reason);

    expect(sessionControlErrorCode(error)).toBe("budget_exceeded");
    expect(error).toMatchObject({ message: "Protected evidence needs 6546 tokens" });
  });

  it("preserves invalid_request instead of degrading it to network_unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "session cwd is not allowed", code: "invalid_request" }, 400)));

    const error = await createHTTPSessionControlClient().create(identity, { title: "Folder test", cwd: "/Users/test-user/GolandProjects/huyu", idempotencyKey: "create-folder" }).catch((reason: unknown) => reason);

    expect(sessionControlErrorCode(error)).toBe("invalid_request");
  });

  it("accepts waiting_input session summaries without collapsing them to idle", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(jsonResponse({ data: [{ ...summary, status: "waiting_input" }] }))
      .mockResolvedValueOnce(jsonResponse({ data: [] })));

    const sessions = await createHTTPSessionControlClient().list(identity, { query: "", statuses: [] });

    expect(sessions[0]?.status).toBe("waiting_input");
  });

  it("maps malformed successful responses and transport failures to network_unavailable", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ session: detail }))
      .mockRejectedValueOnce(new TypeError("connection refused"));
    vi.stubGlobal("fetch", fetchMock);
    const client = createHTTPSessionControlClient();

    const malformed = await client.get(identity, "tenant:release").catch((reason: unknown) => reason);
    const unavailable = await client.get(identity, "tenant:release").catch((reason: unknown) => reason);

    expect(sessionControlErrorCode(malformed)).toBe("network_unavailable");
    expect(sessionControlErrorCode(unavailable)).toBe("network_unavailable");
  });

  it("forwards a query cancellation signal to fetch", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ data: conversation }));
    vi.stubGlobal("fetch", fetchMock);
    const controller = new AbortController();

    await createHTTPSessionControlClient().get(identity, "tenant:release", controller.signal);

    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls[0]?.[1]).toMatchObject({ signal: controller.signal });
  });

  it("keeps archive disabled until its HTTP contract is approved", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const error = await createHTTPSessionControlClient().archive(identity, { ref: "tenant:release", idempotencyKey: "archive-1" }).catch((reason: unknown) => reason);

    expect(sessionControlErrorCode(error)).toBe("invalid_state");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
