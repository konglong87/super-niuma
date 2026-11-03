import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import type { SessionDetail } from "../types";
import { SessionControlClientProvider, sessionControlQueryKeys, type SessionControlClient } from "./sessionControlClient";
import { useSessionConversations } from "./useSessionConversations";

it("keeps bootstrap pages historical, animates only loaded-session increments, and aborts on unmount", async () => {
  const identity: IdentityConfig = { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
  const detail: SessionDetail = { ref: "tenant:alpha", source: "tenant", title: "A", status: "completed", updatedAt: "", shortID: "alpha", messages: [], activity: [], context: [], changes: [], runs: [] };
  let callback: Parameters<NonNullable<SessionControlClient["subscribe"]>>[2] | undefined;
  let streamSignal: AbortSignal | undefined;
  const unsupported = async (): Promise<never> => { throw new Error("unused method"); };
  const client: SessionControlClient = {
    list: unsupported, get: unsupported, create: unsupported, send: unsupported, stop: unsupported, archive: unsupported,
    subscribe: async (_identity, _sessions, onPage, signal) => {
      callback = onPage; streamSignal = signal;
      await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }));
    }
  };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const host = document.createElement("div");
  const root = createRoot(host);
  const environment = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
  environment.IS_REACT_ACT_ENVIRONMENT = true;
  const key = sessionControlQueryKeys.detail(identity, detail.ref);
  const read = () => queryClient.getQueryData<SessionDetail>(key)!;
  function Harness() { const stream = useSessionConversations(identity, [detail], detail.ref); return <span>{stream.state}</span>; }
  try {
    await act(async () => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await vi.waitFor(() => expect(callback).toBeDefined());
    for (const id of [1, 2]) {
      act(() => callback!(detail, [{ id, task_id: 1, event_type: "text_delta", payload_json: JSON.stringify({ content: "history" }) }]));
      expect(read().messages[0].liveRevision).toBeUndefined();
    }
    act(() => queryClient.setQueryData(key, { ...read(), historyLoaded: true }));
    act(() => callback!(detail, [{ id: 3, task_id: 1, event_type: "text_delta", payload_json: '{"content":"new"}' }]));
    expect(read().messages[0].liveRevision).toBe(3);
    act(() => callback!(detail, []));
    expect(read().messages[0].liveRevision).toBe(3);
    expect(read().messages[0].content).toBe("historyhistorynew");
    act(() => callback!({ ...detail, status: "running", activeRunID: 1 }, []));
    expect(read().activeRunID).toBe(1);
    act(() => callback!(detail, []));
    expect(read().status).toBe("completed");
    expect(read().activeRunID).toBeUndefined();
  } finally {
    await act(async () => root.unmount());
    expect(streamSignal?.aborted).toBe(true);
    queryClient.clear();
    delete environment.IS_REACT_ACT_ENVIRONMENT;
  }
});

it("reads the persisted terminal state when the live stream drops", async () => {
  const identity: IdentityConfig = { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
  const detail: SessionDetail = { ref: "tenant:failed", source: "tenant", title: "Failed", status: "running", updatedAt: "", shortID: "failed", messages: [], activity: [], context: [], changes: [], runs: [] };
  const terminal = { ...detail, status: "failed" as const, updatedAt: "2026-09-18T10:00:00Z" };
  const unsupported = async (): Promise<never> => { throw new Error("unused method"); };
  const client: SessionControlClient = {
    list: unsupported, create: unsupported, send: unsupported, stop: unsupported, archive: unsupported,
    get: async () => terminal,
    subscribe: async () => { throw new Error("SSE disconnected"); }
  };
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const host = document.createElement("div");
  const root = createRoot(host);
  const environment = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
  environment.IS_REACT_ACT_ENVIRONMENT = true;
  const key = sessionControlQueryKeys.detail(identity, detail.ref);
  function Harness() { const stream = useSessionConversations(identity, [detail], detail.ref); return <span>{stream.state}</span>; }
  try {
    await act(async () => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await vi.waitFor(() => expect(queryClient.getQueryData<SessionDetail>(key)?.status).toBe("failed"));
    expect(host.textContent).toBe("closed");
  } finally {
    await act(async () => root.unmount());
    queryClient.clear();
    delete environment.IS_REACT_ACT_ENVIRONMENT;
  }
});
