import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { getWebAgentConversation } from "../../lib/api";
import type { IdentityConfig, WebAgentConversationDetail } from "../../lib/types";
import type { SessionDetail } from "../types";
import { sessionRuntimeConfig, useSessionRuntimeDetails } from "./useSessionRuntime";

vi.mock("../../lib/api", () => ({ getWebAgentConversation: vi.fn(async () => ({ tasks: [], events: [] })) }));
const identity: IdentityConfig = { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "global" };
const detail: SessionDetail = { id: 7, ref: "tenant:session", source: "tenant", title: "Test", shortID: "session", status: "running", activeRunID: 42, updatedAt: "", messages: [], events: [], activity: [], context: [], changes: [], runs: [] };

describe("Session runtime metadata", () => {
  it("prefers persisted session defaults and recovers legacy values from task metadata", () => {
    const runtime = { latest_task: { model: "legacy-model", metadata_json: JSON.stringify({ provider: "legacy", permission_mode: "ask", effort: "low", prompt_mode: "chat" }) }, tasks: [] } as unknown as WebAgentConversationDetail;
    expect(sessionRuntimeConfig(detail, runtime)).toEqual({ model: "legacy-model", provider: "legacy", permissionMode: "ask", effort: "low", promptMode: "chat" });
    expect(sessionRuntimeConfig({ ...detail, model: "new-model", provider: "new", permissionMode: "auto", effort: "high", promptMode: "code" }, runtime)).toEqual({ model: "new-model", provider: "new", permissionMode: "auto", effort: "high", promptMode: "code" });
  });

  it("does not turn unknown legacy settings into explicit overrides during loading", () => {
    expect(sessionRuntimeConfig(detail)).toEqual({ provider: "", model: "", permissionMode: "", effort: "", promptMode: "" });
  });

  it.each(["null", "[]", '"legacy"', "malformed"])("handles legacy metadata %s without breaking the conversation", (metadata_json) => {
    const runtime = { latest_task: { model: "saved-model", metadata_json }, tasks: [] } as unknown as WebAgentConversationDetail;
    expect(sessionRuntimeConfig(detail, runtime)).toEqual({ provider: "", model: "saved-model", permissionMode: "", effort: "", promptMode: "" });
  });

  it("shares a lifecycle fetch between observers without fetching on text deltas", async () => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.mocked(getWebAgentConversation).mockClear();
    const host = document.createElement("div");
    const root = createRoot(host);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    function Observer({ value }: { value: SessionDetail }) { useSessionRuntimeDetails(identity, value); return null; }
    async function render(value: SessionDetail) { await act(async () => root.render(<QueryClientProvider client={queryClient}><Observer value={value} /><Observer value={value} /></QueryClientProvider>)); }
    try {
      await render(detail);
      expect(getWebAgentConversation).toHaveBeenCalledTimes(1);
      expect(getWebAgentConversation).toHaveBeenCalledWith(identity, "session:7", { limit: 100, eventLimit: 1 });
      await render({ ...detail, cursor: "11", events: [{ id: 11, task_id: 42, event_type: "text_delta", payload_json: '{"content":"hello"}' }] });
      expect(getWebAgentConversation).toHaveBeenCalledTimes(1);
      await render({ ...detail, status: "completed" });
      expect(getWebAgentConversation).toHaveBeenCalledTimes(2);
    } finally {
      act(() => root.unmount());
      queryClient.clear();
      delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
    }
  });
});
