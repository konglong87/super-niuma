import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import type { SessionControlClient } from "./sessionControlClient";
import { SessionControlClientProvider, sessionControlQueryKeys } from "./sessionControlClient";
import { useCreateSession } from "./sessionControlQueries";
import type { OperationResult, SessionDetail } from "../types";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "device-a",
  model: "test-model"
};

const session: SessionDetail = {
  ref: "tenant:new-session",
  source: "tenant",
  title: "New session",
  status: "idle",
  updatedAt: "2026-09-23T00:00:00.000Z",
  shortID: "new-session",
  messages: [],
  activity: [],
  context: [],
  changes: [],
  runs: []
};

const result: OperationResult = {
  operation: { id: "operation-create", kind: "create", status: "completed", title: "Created", detail: "", createdAt: session.updatedAt },
  session,
  replayed: false
};

function createClient(): SessionControlClient {
  return {
    list: async () => [],
    get: async () => session,
    create: async () => result,
    send: async () => result,
    stop: async () => result,
    archive: async () => result
  };
}

describe("session creation readback", () => {
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

  it("returns the created session without waiting for the list refresh", async () => {
    let releaseList!: () => void;
    const listReadback = new Promise<void>((resolve) => { releaseList = resolve; });
    const invalidateQueries = vi.spyOn(queryClient, "invalidateQueries").mockReturnValue(listReadback);
    let create: ReturnType<typeof useCreateSession> | undefined;

    function Harness() {
      create = useCreateSession(identity);
      return null;
    }

    act(() => root.render(<QueryClientProvider client={queryClient}><SessionControlClientProvider client={createClient()}><Harness /></SessionControlClientProvider></QueryClientProvider>));
    await expect(create!.mutateAsync({ title: session.title, cwd: "/work/project" })).resolves.toMatchObject({ session: { ref: session.ref } });

    expect(queryClient.getQueryData(sessionControlQueryKeys.detail(identity, session.ref))).toEqual(session);
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ["session-control", "list", identity.tenantKey, identity.userId] });
    releaseList();
  });
});
