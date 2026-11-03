import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SuperEvent, SuperPlatformSnapshot } from "../desktopServiceBridge";
import type { PlatformBridge } from "./platformClient";
import { PLATFORM_LIMITS } from "./platformSafety";
import { deferred, mockBridge, mockEvent, mockRun, mockSnapshot } from "./platformFixtures.test-support";
import { useSuperPlatform } from "./useSuperPlatform";

// Pure mock bridge concurrency/recovery tests. No actual app or model execution.
describe("useSuperPlatform (mock bridge)", () => {
  let root: Root;
  let model: ReturnType<typeof useSuperPlatform>;
  let mounted: boolean;
  function Harness({ bridge }: { bridge: PlatformBridge | null }) { model = useSuperPlatform(bridge); return null; }
  beforeEach(() => {
    vi.useFakeTimers(); mounted = true;
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    root = createRoot(document.createElement("div"));
  });
  afterEach(() => {
    if (mounted) act(() => root.unmount());
    vi.useRealTimers(); vi.restoreAllMocks(); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });
  async function render(bridge: PlatformBridge | null) { await act(async () => root.render(<Harness bridge={bridge} />)); }
  async function tick(ms: number = PLATFORM_LIMITS.pollMS) { await act(async () => vi.advanceTimersByTimeAsync(ms)); }

  it("does not start overlapping polls and removes timers on unmount", async () => {
    const pending = deferred<SuperPlatformSnapshot>(); const bridge = mockBridge();
    vi.mocked(bridge.GetSuperPlatform).mockReturnValue(pending.promise);
    await render(bridge); await tick(PLATFORM_LIMITS.pollMS * 10);
    expect(bridge.GetSuperPlatform).toHaveBeenCalledTimes(1);
    act(() => { model.refresh(); model.refresh(); }); expect(bridge.GetSuperPlatform).toHaveBeenCalledTimes(1);
    await act(async () => pending.resolve(mockSnapshot()));
    act(() => root.unmount()); mounted = false;
    await tick(PLATFORM_LIMITS.pollMS * 10); expect(bridge.GetSuperPlatform).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });
  it("ignores late snapshot, events and mutations from the old bridge", async () => {
    const oldSnapshot = deferred<SuperPlatformSnapshot>(); const oldEvents = deferred<SuperEvent[]>(); const oldSave = deferred<SuperPlatformSnapshot>();
    const previous = mockBridge();
    vi.mocked(previous.GetSuperPlatform).mockReturnValue(oldSnapshot.promise);
    vi.mocked(previous.GetSuperEvents).mockReturnValue(oldEvents.promise);
    vi.mocked(previous.SaveSuperPlatformConfig).mockReturnValue(oldSave.promise);
    await render(previous); act(() => { void model.save("{}"); });
    const current = mockBridge(mockSnapshot({ workers: 7 })); await render(current);
    await act(async () => { oldSnapshot.resolve(mockSnapshot({ workers: 1 })); oldEvents.resolve([mockEvent(900)]); oldSave.resolve(mockSnapshot({ workers: 3 })); });
    expect(model.snapshot?.workers).toBe(7); expect(model.events).toEqual([]); expect(model.busy).toBe(false);
    expect(previous.GetSuperEvents).toHaveBeenCalledTimes(1);
    expect(current.GetSuperEvents).toHaveBeenCalledWith(0);
  });
  it("rejects a pre-cancellation read and preserves the newer run version", async () => {
    const run = mockRun({ status: "running", version: 4 }); const bridge = mockBridge(mockSnapshot({ runs: [run] }));
    await render(bridge);
    const stale = deferred<SuperPlatformSnapshot>();
    vi.mocked(bridge.GetSuperPlatform).mockReturnValueOnce(stale.promise);
    act(() => model.refresh());
    vi.mocked(bridge.CancelSuperRun).mockResolvedValue(mockRun({ status: "cancelled", version: 5 }));
    await act(async () => { await model.cancel(run); });
    expect(model.snapshot?.runs[0].status).toBe("cancelled");
    await act(async () => stale.resolve(mockSnapshot({ runs: [run] })));
    expect(model.snapshot?.runs[0].version).toBe(5);
    await tick(0); // Even a stale backend snapshot cannot lower a run's revision.
    expect(model.snapshot?.runs[0].status).toBe("cancelled");
  });
  it("keeps last snapshot on a poll error and recovers without error text", async () => {
    const bridge = mockBridge(); await render(bridge);
    vi.mocked(bridge.GetSuperPlatform).mockRejectedValueOnce(new Error("secret-file-path"));
    await tick(); expect(model.pollError).toBe(true); expect(model.snapshot?.workers).toBe(2);
    expect(model.error).toBeNull(); await tick(); expect(model.pollError).toBe(false);
  });
  it("replays backlog in bounded pages then resumes and caps the visible timeline", async () => {
    const bridge = mockBridge();
    vi.mocked(bridge.GetSuperEvents).mockImplementation(async (after) => after >= 350 ? [] : Array.from({ length: 50 }, (_, i) => mockEvent(after + i + 1)));
    await render(bridge);
    expect(bridge.GetSuperEvents).toHaveBeenCalledTimes(PLATFORM_LIMITS.eventPages);
    expect(model.events).toHaveLength(200);
    await tick(); expect(bridge.GetSuperEvents).toHaveBeenLastCalledWith(350);
    expect(model.events).toHaveLength(PLATFORM_LIMITS.events); expect(model.events[0].sequence).toBe(151);
    expect(model.events.at(-1)?.sequence).toBe(350);
  });
  it("does not advance the event cursor when a page fails validation", async () => {
    const bridge = mockBridge();
    vi.mocked(bridge.GetSuperEvents).mockResolvedValueOnce([mockEvent(1)]).mockResolvedValueOnce([mockEvent(Number.MAX_SAFE_INTEGER + 1)]);
    await render(bridge); expect(model.eventError).toBe(true); expect(model.events).toHaveLength(1);
    await tick(); expect(bridge.GetSuperEvents).toHaveBeenLastCalledWith(1); expect(model.eventError).toBe(false);
  });
  it("stops safely after unmount with an outstanding mutation and read", async () => {
    const bridge = mockBridge(); await render(bridge);
    const late = deferred<ReturnType<typeof mockRun>>(); vi.mocked(bridge.StartSuperRun).mockReturnValue(late.promise);
    let result: Promise<boolean>;
    act(() => { result = model.start("mock-order", "mock-profile"); });
    act(() => root.unmount()); mounted = false;
    await act(async () => late.resolve(mockRun()));
    expect(await result!).toBe(false); expect(vi.getTimerCount()).toBe(0);
  });
});
