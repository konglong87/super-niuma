import { afterEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig } from "./types";
import * as api from "./api";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test-token", mobileJwt: "", tenantKey: "tenant-a", userId: "user-a", deviceId: "device-a", model: "model" };

afterEach(() => vi.unstubAllGlobals());

describe("agent task question API", () => {
  it("PATCHes a trimmed answer to the scoped task request", async () => {
    const resolveQuestion = (api as unknown as { resolveAgentTaskQuestion?: (identity: IdentityConfig, taskID: number, requestID: string, answer: string) => Promise<unknown> }).resolveAgentTaskQuestion;
    expect(typeof resolveQuestion).toBe("function");
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 14, request_id: "question/1", status: "answered", answer: "Canary" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await resolveQuestion!(identity, 14, "question/1", "  Canary  ");

    expect(fetchMock).toHaveBeenCalledWith("/api/tenant/agent-tasks/14/questions/question%2F1", expect.objectContaining({ method: "PATCH", body: '{"answer":"Canary"}' }));
  });
});
