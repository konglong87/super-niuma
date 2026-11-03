import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DesktopServiceStatus } from "./desktopServiceBridge";
import { DESKTOP_READINESS, useDesktopReadiness } from "./useDesktopReadiness";

describe("useDesktopReadiness", () => {
  let root: Root;
  let result: ReturnType<typeof useDesktopReadiness>;
  let healthy: boolean;
  let hostState: DesktopServiceStatus["state"];
  const onReady = vi.fn();
  const fetchMock = vi.fn();
  const restart = vi.fn();

  function Harness({ token = "desktop-token", enabled = true }: { token?: string; enabled?: boolean }) {
    result = useDesktopReadiness({ enabled, apiBase: "", apiToken: token, onReady });
    return null;
  }

  async function render(token = "desktop-token", enabled = true) {
    await act(async () => root.render(<Harness token={token} enabled={enabled} />));
  }

  async function advance(ms: number) {
    await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
  }

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.useFakeTimers();
    vi.setSystemTime(0);
    healthy = false;
    hostState = "starting";
    onReady.mockReset();
    fetchMock.mockReset().mockImplementation(async () => new Response(null, { status: healthy ? 200 : 503 }));
    restart.mockReset().mockResolvedValue(undefined);
    vi.stubGlobal("fetch", fetchMock);
    window.go = { main: { app: {
      RestartLocalService: restart,
      GetLocalServiceStatus: async () => ({ state: hostState, port: 12345 })
    } } };
    root = createRoot(document.createElement("div"));
  });

  afterEach(() => {
    act(() => root.unmount());
    expect(vi.getTimerCount()).toBe(0);
    delete window.go;
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("treats failed initial probes as preparation and automatically becomes ready", async () => {
    await render();
    expect(result.state).toBe("starting");
    expect(result.ready).toBe(false);
    await advance(3_000);
    expect(result.state).toBe("starting");
    healthy = true;
    await advance(1_000);
    expect(result.state).toBe("ready");
    expect(onReady).toHaveBeenCalledTimes(1);
    await advance(4_000);
    expect(onReady).toHaveBeenCalledTimes(1);
    expect(restart).not.toHaveBeenCalled();
  });

  it("allows a live native startup longer than the fallback grace but bounds a stuck startup", async () => {
    await render();
    await advance(10_000);
    expect(result.state).toBe("starting");
    await advance(DESKTOP_READINESS.startupLimitMs);
    expect(result.state).toBe("failed");
  });

  it("does not mistake the native automatic retry gap for terminal failure", async () => {
    await render();
    await advance(9_000);
    hostState = "failed";
    await advance(1_000);
    expect(result.state).toBe("starting");
    hostState = "starting";
    await advance(2_000);
    healthy = true;
    await advance(2_000);
    expect(result.state).toBe("ready");
  });

  it.each(["failed", "stopped", "ready"] as const)("confirms an unavailable service when native state is %s", async (state) => {
    hostState = state;
    await render();
    await advance(7_900);
    expect(result.state).toBe("starting");
    await advance(2_000);
    expect(result.state).toBe("failed");
    healthy = true;
    await advance(2_000);
    expect(result.state).toBe("ready");
    expect(onReady).toHaveBeenCalledTimes(1);
  });

  it("bounds startup when the native status method is absent", async () => {
    delete window.go;
    await render();
    await advance(10_000);
    expect(result.state).toBe("failed");
  });

  it("bounds hung HTTP and native status requests", async () => {
    fetchMock.mockImplementation(() => new Promise(() => undefined));
    window.go!.main!.app!.GetLocalServiceStatus = () => new Promise(() => undefined);
    await render();
    await advance(12_000);
    expect(result.state).toBe("failed");
  });

  it("waits for identity without HTTP calls and does not wait forever", async () => {
    await render("");
    expect(result.state).toBe("waiting_identity");
    expect(fetchMock).not.toHaveBeenCalled();
    await advance(10_000);
    expect(result.state).toBe("failed");
    healthy = true;
    await render("fresh-token");
    expect(result.state).toBe("ready");
    expect(fetchMock).toHaveBeenCalledWith("/health", expect.objectContaining({
      headers: { Authorization: "Bearer fresh-token" }
    }));
  });

  it("refreshes consumers again after connection recovery", async () => {
    healthy = true;
    await render();
    expect(onReady).toHaveBeenCalledTimes(1);
    healthy = false;
    await advance(2_000);
    expect(result.state).toBe("recovering");
    healthy = true;
    await advance(500);
    expect(result.state).toBe("ready");
    expect(onReady).toHaveBeenCalledTimes(2);
  });

  it("deduplicates restart clicks, pauses polling and probes immediately after restart", async () => {
    hostState = "failed";
    await render();
    await advance(10_000);
    let finishRestart!: () => void;
    restart.mockImplementation(() => new Promise<void>((resolve) => { finishRestart = resolve; }));
    await act(async () => { result.retry(); result.retry(); });
    expect(result.busy).toBe(true);
    expect(result.state).toBe("starting");
    const calls = fetchMock.mock.calls.length;
    await advance(4_000);
    await act(async () => result.retry());
    expect(restart).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(calls);
    healthy = true;
    await act(async () => finishRestart());
    expect(result.state).toBe("ready");
    expect(result.busy).toBe(false);
  });

  it("continues recovery checks even when manual restart fails", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => undefined);
    restart.mockRejectedValue(new Error("native restart failed"));
    await render();
    await act(async () => result.retry());
    expect(result.state).toBe("failed");
    expect(result.busy).toBe(false);
    healthy = true;
    await advance(2_000);
    expect(result.state).toBe("ready");
  });

  it("ignores late readiness from the previous process token", async () => {
    const pending: Array<(response: Response) => void> = [];
    fetchMock.mockImplementation(() => new Promise<Response>((resolve) => pending.push(resolve)));
    await render("old-token");
    fetchMock.mockImplementation(async () => new Response(null, { status: 503 }));
    await render("new-token");
    await act(async () => pending.forEach((resolve) => resolve(new Response(null, { status: 200 }))));
    expect(result.state).toBe("starting");
    expect(onReady).not.toHaveBeenCalled();
  });

  it("does not impose desktop readiness on browser clients", async () => {
    await render("", false);
    expect(result.state).toBe("ready");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(onReady).not.toHaveBeenCalled();
  });

  it("requires both health and application readiness before enabling the UI", async () => {
    fetchMock.mockImplementation(async (url: string) => new Response(null, { status: url === "/health" ? 200 : 503 }));
    await render();
    expect(result.ready).toBe(false);
    expect(onReady).not.toHaveBeenCalled();
  });
});
