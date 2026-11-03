import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import type { SessionControlClient } from "./sessionControlClient";
import { SessionControlClientProvider, SessionControlError, sessionControlErrorCode, sessionControlQueryKeys } from "./sessionControlClient";
import { useArchiveSession, useCreateSession, useRenameSession, useSendSession, useSessionDetail, useSessionList, useStopSession } from "./sessionControlQueries";
import type { OperationKind, OperationResult, SessionDetail, SessionSummary } from "../types";
import { applyConversationEvents } from "./sessionEventReducer";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "device-a",
  model: "test-model"
};

const summary: SessionSummary = {
  ref: "tenant:alpha",
  source: "tenant",
  title: "Release coordination",
  status: "running",
  updatedAt: "2026-09-05T00:00:00.000Z",
  shortID: "alpha"
};

const detail: SessionDetail = { ...summary, messages: [], activity: [], context: [], changes: [], runs: [{ id: "run-1", status: "running", startedAt: summary.updatedAt }] };
const sentDetail: SessionDetail = {
  ...detail,
  messages: [{ id: "message-1", role: "user", kind: "message", content: "Queue this", createdAt: "2026-09-05T00:00:01.000Z" }]
};

function fakeClient(overrides: Partial<SessionControlClient> = {}): SessionControlClient {
  return {
    list: async () => [summary],
    get: async () => detail,
    create: async () => ({ operation: { id: "operation-create", kind: "create", status: "completed", title: "Created", detail: "", createdAt: detail.updatedAt }, session: detail, replayed: false }),
    send: async () => ({ operation: { id: "operation-send", kind: "send", status: "completed", title: "Queued", detail: "", createdAt: sentDetail.updatedAt }, session: sentDetail, replayed: false }),
    stop: async () => ({ operation: { id: "operation-stop", kind: "stop", status: "completed", title: "Stopped", detail: "", createdAt: detail.updatedAt }, session: detail, replayed: false }),
    archive: async () => ({ operation: { id: "operation-archive", kind: "archive", status: "completed", title: "Archived", detail: "", createdAt: detail.updatedAt }, session: detail, replayed: false }),
    ...overrides
  };
}

describe("session control query hooks", () => {
  let host: HTMLDivElement;
  let root: Root;
  let queryClient: QueryClient;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  });

  afterEach(() => {
    act(() => root.unmount());
    queryClient.clear();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("uses tenant-scoped list and detail queries from the injected client", async () => {
    const client = fakeClient();
    let listData: SessionSummary[] | undefined;
    let detailData: SessionDetail | undefined;

    function Harness() {
      const list = useSessionList(identity, { query: "", statuses: [] });
      const selected = useSessionDetail(identity, "tenant:alpha");
      listData = list.data;
      detailData = selected.data;
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await vi.waitFor(() => expect(listData).toEqual([summary]));

    expect(listData).toEqual([summary]);
    expect(detailData).toEqual(detail);
    expect(queryClient.getQueryData(sessionControlQueryKeys.list(identity, { query: "", statuses: [] }))).toEqual([summary]);
    expect(queryClient.getQueryData(sessionControlQueryKeys.detail(identity, "tenant:alpha"))).toEqual(detail);
  });

  it("updates only the renamed title in matching list and detail caches, preserving streamed content and other identities", async () => {
    const client = fakeClient({ rename: vi.fn().mockResolvedValue(undefined) });
    const listKey = sessionControlQueryKeys.list(identity, { query: "", statuses: [] });
    const detailKey = sessionControlQueryKeys.detail(identity, summary.ref);
    const otherBaseKey = sessionControlQueryKeys.list({ ...identity, apiBase: "/other" }, { query: "", statuses: [] });
    const otherTenantKey = sessionControlQueryKeys.list({ ...identity, tenantKey: "other" }, { query: "", statuses: [] });
    queryClient.setQueryData(listKey, [summary]);
    queryClient.setQueryData(detailKey, sentDetail);
    queryClient.setQueryData(otherBaseKey, [summary]);
    queryClient.setQueryData(otherTenantKey, [summary]);
    let rename!: ReturnType<typeof useRenameSession>;
    function Harness() { rename = useRenameSession(identity); return null; }
    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await act(async () => { await rename.mutateAsync({ ref: summary.ref, id: 7, title: "  Release plan  " }); });
    expect(client.rename).toHaveBeenCalledWith(identity, { ref: summary.ref, id: 7, title: "Release plan" });
    expect(queryClient.getQueryData(listKey)).toEqual([{ ...summary, title: "Release plan" }]);
    expect(queryClient.getQueryData(detailKey)).toEqual({ ...sentDetail, title: "Release plan" });
    expect(queryClient.getQueryData(otherBaseKey)).toEqual([summary]);
    expect(queryClient.getQueryData(otherTenantKey)).toEqual([summary]);
    expect(queryClient.getQueryState(otherBaseKey)?.isInvalidated).toBe(false);
    expect(queryClient.getQueryState(listKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(detailKey)?.isInvalidated).toBe(true);
  });

  it("does not change cached titles when rename fails", async () => {
    const client = fakeClient({ rename: vi.fn().mockRejectedValue(new SessionControlError("network_unavailable")) });
    const key = sessionControlQueryKeys.list(identity, { query: "", statuses: [] });
    queryClient.setQueryData(key, [summary]);
    let rename!: ReturnType<typeof useRenameSession>;
    function Harness() { rename = useRenameSession(identity); return null; }
    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await act(async () => { await expect(rename.mutateAsync({ ref: summary.ref, id: 7, title: "Changed" })).rejects.toMatchObject({ code: "network_unavailable" }); });
    expect(queryClient.getQueryData(key)).toEqual([summary]);
  });

  it("forwards React Query cancellation signals to read-only client calls", async () => {
    let listSignal: AbortSignal | undefined;
    let detailSignal: AbortSignal | undefined;
    const client = fakeClient({
      list: async (_identity, _filters, signal) => {
        listSignal = signal;
        return [summary];
      },
      get: async (_identity, _ref, signal) => {
        detailSignal = signal;
        return detail;
      }
    });

    function Harness() {
      useSessionList(identity, { query: "", statuses: [] });
      useSessionDetail(identity, "tenant:alpha");
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await vi.waitFor(() => expect(listSignal).toBeInstanceOf(AbortSignal));
    expect(detailSignal).toBeInstanceOf(AbortSignal);
  });

  it("keeps newer same-second SSE completion when an earlier HTTP detail request resolves late", async () => {
    const snapshot = applyConversationEvents({ ...detail, activeRunID: 14 }, [{ id: 1, task_id: 14, event_type: "text_delta", payload_json: '{"content":"start"}' }]);
    const key = sessionControlQueryKeys.detail(identity, snapshot.ref);
    queryClient.setQueryData(key, snapshot);
    let resolveGet!: (value: SessionDetail) => void;
    const get = vi.fn(() => new Promise<SessionDetail>((resolve) => { resolveGet = resolve; }));
    const client = fakeClient({ get });
    function Harness() { useSessionDetail(identity, snapshot.ref); return null; }
    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await vi.waitFor(() => expect(get).toHaveBeenCalledOnce());
    act(() => queryClient.setQueryData<SessionDetail>(key, (current) => applyConversationEvents({ ...current!, status: "completed", activeRunID: undefined }, [{ id: 2, task_id: 14, event_type: "text_delta", payload_json: '{"content":" done"}' }, { id: 3, task_id: 14, event_type: "completed" }], { live: true })));
    await act(async () => resolveGet({ ...snapshot, historyLoaded: true }));
    const cached = queryClient.getQueryData<SessionDetail>(key)!;
    expect(cached).toMatchObject({ status: "completed", activeRunID: undefined, cursor: "3", historyLoaded: true });
    expect(cached.messages[0]).toMatchObject({ content: "start done", liveRevision: 2 });
    expect(cached.runs[0].status).toBe("completed");
  });

  it("adopts successful send readback and invalidates the target and tenant list caches", async () => {
    const client = fakeClient();
    const invalidateQueries = vi.spyOn(queryClient, "invalidateQueries");
    let send: ReturnType<typeof useSendSession> | undefined;

    function Harness() {
      send = useSendSession(identity);
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await act(async () => {
      await send?.mutateAsync({ ref: "tenant:alpha", text: "Queue this", attachments: [], sourceRefs: [] });
    });

    expect(queryClient.getQueryData(sessionControlQueryKeys.detail(identity, "tenant:alpha"))).toEqual(sentDetail);
    await vi.waitFor(() => {
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: sessionControlQueryKeys.detail(identity, "tenant:alpha") });
      expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ["session-control", "list", "tenant-a", "user-a"] });
    });
  });

  it("resolves an accepted send before a slow readback finishes", async () => {
    const client = fakeClient();
    let releaseDetail!: () => void;
    const detailReadback = new Promise<void>((resolve) => { releaseDetail = resolve; });
    let invalidationCount = 0;
    const invalidateQueries = vi.spyOn(queryClient, "invalidateQueries").mockImplementation(() => {
      invalidationCount += 1;
      return invalidationCount === 1 ? detailReadback : Promise.resolve();
    });
    let send: ReturnType<typeof useSendSession> | undefined;

    function Harness() {
      send = useSendSession(identity);
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    const accepted = send!.mutateAsync({ ref: "tenant:alpha", text: "Immediate clear", attachments: [], sourceRefs: [] });
    await vi.waitFor(() => expect(invalidationCount).toBe(1));
    await expect(accepted).resolves.toMatchObject({ operation: { id: "operation-send" } });
    expect(invalidationCount).toBe(1);

    releaseDetail();
    await vi.waitFor(() => expect(invalidationCount).toBe(2));
  });

  it("preserves replay metadata in create, send, stop, and archive readbacks", async () => {
    function replayResult(kind: OperationKind): OperationResult {
      const operation = { id: `operation-${kind}`, kind, status: "completed" as const, title: "raw", detail: "", createdAt: detail.updatedAt };
      return {
        operation,
        replayed: true,
        session: { ...detail, messages: [{ id: `message-${kind}`, role: "assistant", kind: "operation", content: "", createdAt: detail.updatedAt, operation }] }
      };
    }
    const client = fakeClient({
      create: async () => replayResult("create"),
      send: async () => replayResult("send"),
      stop: async () => replayResult("stop"),
      archive: async () => replayResult("archive")
    });
    let mutations: {
      create: ReturnType<typeof useCreateSession>;
      send: ReturnType<typeof useSendSession>;
      stop: ReturnType<typeof useStopSession>;
      archive: ReturnType<typeof useArchiveSession>;
    } | undefined;

    function Harness() {
      useSessionDetail(identity, "tenant:alpha");
      mutations = {
        create: useCreateSession(identity),
        send: useSendSession(identity),
        stop: useStopSession(identity),
        archive: useArchiveSession(identity)
      };
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    const actions: Array<[OperationKind, () => Promise<OperationResult>]> = [
      ["create", () => mutations!.create.mutateAsync({ title: "Replay" })],
      ["send", () => mutations!.send.mutateAsync({ ref: "tenant:alpha", text: "Replay", attachments: [], sourceRefs: [] })],
      ["stop", () => mutations!.stop.mutateAsync({ ref: "tenant:alpha" })],
      ["archive", () => mutations!.archive.mutateAsync({ ref: "tenant:alpha" })]
    ];

    for (const [kind, mutate] of actions) {
      await act(async () => { await mutate(); });
      const cached = queryClient.getQueryData<SessionDetail>(sessionControlQueryKeys.detail(identity, "tenant:alpha"));
      expect(cached?.messages[0].operation).toMatchObject({ kind, replayed: true });
    }
  });

  it("overlays replay metadata without replacing a newer authoritative detail refetch", async () => {
    const operation = { id: "operation-send", kind: "send" as const, status: "completed" as const, title: "Queued", detail: "", createdAt: "2026-09-05T00:00:01.000Z" };
    const mutationSession: SessionDetail = {
      ...detail,
      activity: ["Mutation response"],
      messages: [{ id: "message-operation", role: "assistant", kind: "operation", content: "", createdAt: operation.createdAt, operation }]
    };
    const authoritativeDetail: SessionDetail = {
      ...mutationSession,
      status: "completed",
      updatedAt: "2026-09-05T00:00:03.000Z",
      activity: ["Mutation response", "Run completed on server"],
      changes: ["server/newer.go"],
      messages: [...mutationSession.messages, { id: "message-newer", role: "assistant", kind: "message", content: "Authoritative completion", createdAt: "2026-09-05T00:00:03.000Z" }],
      runs: [{ id: "run-1", status: "completed", startedAt: summary.updatedAt, endedAt: "2026-09-05T00:00:03.000Z" }]
    };
    const client = fakeClient({
      get: async () => authoritativeDetail,
      send: async () => ({ operation, session: mutationSession, replayed: true })
    });
    let send: ReturnType<typeof useSendSession> | undefined;
    let selected: SessionDetail | undefined;

    function Harness() {
      selected = useSessionDetail(identity, "tenant:alpha").data;
      send = useSendSession(identity);
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await vi.waitFor(() => expect(selected?.status).toBe("completed"));
    await act(async () => { await send?.mutateAsync({ ref: "tenant:alpha", text: "Replay", attachments: [], sourceRefs: [] }); });

    const cached = queryClient.getQueryData<SessionDetail>(sessionControlQueryKeys.detail(identity, "tenant:alpha"));
    expect(cached).toMatchObject({
      status: "completed",
      updatedAt: authoritativeDetail.updatedAt,
      activity: authoritativeDetail.activity,
      changes: authoritativeDetail.changes,
      runs: authoritativeDetail.runs
    });
    expect(cached?.messages).toHaveLength(2);
    expect(cached?.messages[1].content).toBe("Authoritative completion");
    expect(cached?.messages[0].operation).toMatchObject({ id: operation.id, replayed: true });
  });

  it("exposes a stable mutation error code without rendering an arbitrary error message", async () => {
    const client = fakeClient({ send: async () => Promise.reject(new SessionControlError("local_read_only", "raw service detail must stay hidden")) });
    let send: ReturnType<typeof useSendSession> | undefined;

    function Harness() {
      send = useSendSession(identity);
      return <output>{sessionControlErrorCode(send.error)}</output>;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={client}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await act(async () => {
      await expect(send?.mutateAsync({ ref: "tenant:alpha", text: "Queue this", attachments: [], sourceRefs: [] })).rejects.toMatchObject({ code: "local_read_only" });
    });
    await vi.waitFor(() => expect(host.textContent).toBe("local_read_only"));

    expect(host.textContent).toBe("local_read_only");
    expect(host.textContent).not.toContain("raw service detail");
  });
});
