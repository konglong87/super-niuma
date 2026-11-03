import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { COMPUTER_SESSION_POLL_INTERVAL_MS, useComputerSession } from "./useComputerSession";
import type { ComputerClient } from "./client";
import type { ComputerObservationResponse, ComputerSessionSnapshot } from "./types";
import { capabilities, createTestClient, deferred, observationResponse, snapshot } from "./testFixtures";

describe("useComputerSession", () => {
  let root: Root;
  let result: ReturnType<typeof useComputerSession>;
  let client: ComputerClient;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    root = createRoot(document.createElement("div"));
    client = createTestClient();
  });
  afterEach(() => { act(() => root.unmount()); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT; });
  function Harness() { result = useComputerSession(client); return null; }
  async function mount() {
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    await act(async () => { await result.loadCapabilities(); });
  }
  async function start() { await mount(); await act(async () => { await result.start({ approved: true }); }); }

  it("discovers a model-created session through read-only status and binds exact Stop control", async () => {
    const modelSession = { ...snapshot(), owner: { tenant_id: 9, user_id: 8, session_id: 7 }, owner_kind: "managed_conversation" as const };
    client.getActiveSession = vi.fn().mockResolvedValue(modelSession);
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(result.session?.session_id).toBe(modelSession.session_id);
    expect(result.session?.owner_kind).toBe("managed_conversation");
    expect(client.getActiveSession).toHaveBeenCalled();
    await act(async () => { await result.stop(); });
    expect(client.stop).toHaveBeenCalledWith(modelSession.session_id);
  });

  it("survives StrictMode and observes immediately after awaiting start with the same closure", async () => {
    await mount();
    const actions = result;
    await act(async () => { await actions.start({ approved: true }); await actions.observe(); });
    expect(client.observe).toHaveBeenCalledWith("s1");
    expect(result.observation).toEqual({ ...observationResponse.observation, image_data: observationResponse.image_data, media_type: "image/png" });
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    expect(result.session?.session_id).toBe("s1");
  });

  it("ignores a late observe after Stop and does not let it clear a newer busy flag", async () => {
    await start();
    const capture = deferred<ComputerObservationResponse>();
    const stop = deferred<ComputerSessionSnapshot>();
    client.observe = vi.fn().mockReturnValue(capture.promise);
    client.stop = vi.fn().mockReturnValue(stop.promise);
    let pendingObserve!: ReturnType<typeof result.observe>;
    let pendingStop!: ReturnType<typeof result.stop>;
    await act(async () => { pendingObserve = result.observe(); });
    await act(async () => { pendingStop = result.stop(); });
    await act(async () => { capture.resolve(observationResponse); await pendingObserve; });
    expect(result.loading).toBe(true);
    expect(result.observation).toBeNull();
    await act(async () => { stop.resolve(snapshot("stopped")); await pendingStop; });
    expect(result.session?.state).toBe("stopped");
  });

  it.each(["pause", "resume"] as const)("ignores late %s success after Stop", async (kind) => {
    await start();
    if (kind === "resume") await act(async () => { await result.pause(); });
    const delayed = deferred<ComputerSessionSnapshot>();
    client[kind] = vi.fn().mockReturnValue(delayed.promise);
    let pending!: Promise<ComputerSessionSnapshot | null>;
    await act(async () => { pending = result[kind](); });
    await act(async () => { await result.stop(); });
    await act(async () => { delayed.resolve(snapshot(kind === "pause" ? "paused" : "ready")); await pending; });
    expect(result.session?.state).toBe("stopped");
    expect(result.loading).toBe(false);
  });

  it("ignores late errors after Stop", async () => {
    await start();
    const delayed = deferred<ComputerObservationResponse>();
    client.observe = vi.fn().mockReturnValue(delayed.promise);
    let pending!: ReturnType<typeof result.observe>;
    await act(async () => { pending = result.observe(); });
    await act(async () => { await result.stop(); });
    await act(async () => { delayed.reject(new Error("stale capture failure")); await pending; });
    expect(result.error).toBeNull();
    expect(result.session?.state).toBe("stopped");
  });

  it("Pause interrupts observe; stale image cannot mutate paused state", async () => {
    await start();
    const delayed = deferred<ComputerObservationResponse>();
    client.observe = vi.fn().mockReturnValue(delayed.promise);
    let pending!: ReturnType<typeof result.observe>;
    await act(async () => { pending = result.observe(); });
    await act(async () => { await result.pause(); });
    await act(async () => { delayed.resolve(observationResponse); await pending; });
    expect(result.session?.state).toBe("paused");
    expect(result.observation).toBeNull();
  });

  it("clears the last preview when Stop is confirmed", async () => {
    await start();
    await act(async () => { await result.observe(); });
    expect(result.observation).not.toBeNull();
    await act(async () => { await result.stop(); });
    expect(result.session?.state).toBe("stopped");
    expect(result.observation).toBeNull();
    expect(result.session?.observation).toBeUndefined();
  });

  it("shows Stop failures without claiming stopped; blocks resume and allows Stop retry", async () => {
    await start();
    client.stop = vi.fn().mockRejectedValueOnce(new Error("Stop outcome unknown")).mockResolvedValue(snapshot("stopped"));
    await act(async () => { await expect(result.stop()).rejects.toThrow("Stop outcome unknown"); });
    expect(result.error).toBe("Stop outcome unknown");
    expect(result.session?.state).toBe("ready");
    expect(result.controlIntent).toBe("stop");
    await act(async () => { await expect(result.resume()).rejects.toThrow("Stop requested"); });
    expect(client.resume).not.toHaveBeenCalled();
    await act(async () => { await result.stop(); });
    expect(result.session?.state).toBe("stopped");
  });

  it("uses the new ID after Stop → Start even from a retained callback", async () => {
    await start();
    const oldObserve = result.observe;
    await act(async () => { await result.stop(); });
    client.start = vi.fn().mockResolvedValue(snapshot("ready", "s2"));
    client.observe = vi.fn().mockResolvedValue({ ...observationResponse, observation: { ...observationResponse.observation, session_id: "s2" } });
    await act(async () => { await result.start({ approved: true }); await oldObserve(); });
    expect(client.observe).toHaveBeenLastCalledWith("s2");
    expect(result.session?.session_id).toBe("s2");
  });

  it("ignores a previous client's pending start after replacement", async () => {
    await mount();
    const delayed = deferred<ComputerSessionSnapshot>();
    client.start = vi.fn().mockReturnValue(delayed.promise);
    let pending!: ReturnType<typeof result.start>;
    await act(async () => { pending = result.start({ approved: true }); });
    client = createTestClient();
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    await act(async () => { delayed.resolve(snapshot()); await pending; });
    expect(result.session).toBeNull();
    expect(result.loading).toBe(false);
  });

  it("requires explicit approval and backend availability even with ready capabilities", async () => {
    await mount();
    await act(async () => { await expect(result.start()).rejects.toThrow("Explicit session approval"); });
    client.getCapabilities = vi.fn().mockResolvedValue({ available: false, capabilities, error_code: "not_available", error_message: "helper missing" });
    await act(async () => { await result.loadCapabilities(); });
    expect(result.error).toBe("helper missing");
    await act(async () => { await expect(result.start({ approved: true })).rejects.toThrow("unavailable"); });
    expect(client.start).not.toHaveBeenCalled();
  });

  it("accepts an observation envelope without image bytes", async () => {
    await start();
    client.observe = vi.fn().mockResolvedValue({ observation: observationResponse.observation });
    await act(async () => { await result.observe(); });
    expect(result.observation?.id).toBe("o1");
    expect(result.observation?.image_data).toBeUndefined();
  });

  it("keeps Stop intent when the backend fails to confirm a stopped snapshot", async () => {
    await start();
    client.stop = vi.fn().mockResolvedValue(snapshot("ready"));
    await act(async () => { await expect(result.stop()).rejects.toThrow("Backend did not confirm Stop"); });
    expect(result.session?.state).toBe("ready");
    expect(result.controlIntent).toBe("stop");
  });

  it("merges last_receipt from real session snapshots without duplicate timeline entries", async () => {
    await start();
    const receipt = {
      action_id: "a1", session_id: "s1", platform: "macos", backend: "native_host",
      outcome: "executed", verification: "passed", focus_before: "focused", focus_after: "focused",
      redacted_action_summary: "click", duration: 1000, completed_at: "2026-09-24T00:00:00Z"
    } satisfies import("./types").ComputerActionReceipt;
    client.pause = vi.fn().mockResolvedValue({ ...snapshot("paused"), last_receipt: receipt });
    client.stop = vi.fn().mockResolvedValue({ ...snapshot("stopped"), last_receipt: receipt });
    await act(async () => { await result.pause(); await result.stop(); });
    expect(result.receipts).toEqual([receipt]);
  });


  it("moves needs_observation to ready only after a successful observe following Resume", async () => {
    await start();
    await act(async () => { await result.pause(); });
    client.resume = vi.fn().mockResolvedValue(snapshot("needs_observation"));
    await act(async () => { await result.resume(); });
    expect(result.session?.state).toBe("needs_observation");
    await act(async () => { await result.observe(); });
    expect(result.session?.state).toBe("ready");
  });

});

describe("read-only session synchronization", () => {
  let root: Root;
  let result: ReturnType<typeof useComputerSession>;
  let client: ComputerClient;
  let unmounted: boolean;
  const receipt = {
    action_id: "model-action", session_id: "s1", platform: "macos", backend: "native_host",
    outcome: "executed", verification: "passed", focus_before: "focused", focus_after: "focused",
    redacted_action_summary: "click", completed_at: "2026-09-24T00:00:00Z"
  } satisfies import("./types").ComputerActionReceipt;
  function Harness() { result = useComputerSession(client); return null; }
  async function tick(count = 1) {
    await act(async () => { await vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS * count); });
  }
  beforeEach(async () => {
    vi.useFakeTimers();
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    root = createRoot(document.createElement("div"));
    unmounted = false;
    client = createTestClient();
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    await act(async () => { await result.loadCapabilities(); });
  });
  afterEach(() => {
    if (!unmounted) act(() => root.unmount());
    expect(vi.getTimerCount()).toBe(0);
    vi.useRealTimers();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });
  async function start() {
    await act(async () => { await result.start({ approved: true, conversation_ref: "approved-conversation" }); });
  }

  it("polls only active sessions, dedupes model receipts and synchronizes model Stop without Observe", async () => {
    await tick(3);
    expect(client.getSession).not.toHaveBeenCalled();
    await start();
    client.getSession = vi.fn().mockResolvedValue({ ...snapshot("needs_observation"), last_receipt: receipt });
    await tick(2);
    expect(result.session?.state).toBe("needs_observation");
    expect(result.receipts).toEqual([receipt]);
    expect(result.approvedConversationRef).toBe("approved-conversation");
    expect(result.loading).toBe(false);
    client.getSession = vi.fn().mockResolvedValue({ ...snapshot("stopped"), last_receipt: receipt });
    await tick(5);
    expect(client.getSession).toHaveBeenCalledExactlyOnceWith("s1");
    expect(result.session?.state).toBe("stopped");
    expect(result.receipts).toEqual([receipt]);
    expect(client.observe).not.toHaveBeenCalled();
  });

  it("rediscovers a replacement after Stop without carrying old preview, receipts or approval", async () => {
    client.getActiveSession = vi.fn().mockRejectedValue(new Error("No active session"));
    await start();
    client.getReceipt = vi.fn().mockResolvedValue(receipt);
    await act(async () => { await result.observe(); await result.getReceipt(receipt.action_id); });
    await act(async () => { await result.stop(); });
    const replacement = { ...snapshot("ready", "model-replacement"), owner_kind: "managed_conversation" as const };
    client.getActiveSession = vi.fn().mockResolvedValue(replacement);
    await tick();
    expect(result.session?.session_id).toBe(replacement.session_id);
    expect(result.approvedConversationRef).toBeNull();
    expect(result.observation).toBeNull();
    expect(result.receipts).toEqual([]);
    expect(result.controlIntent).toBeNull();
    expect(client.start).toHaveBeenCalledTimes(1);
    expect(client.observe).toHaveBeenCalledTimes(1);
    client.stop = vi.fn().mockResolvedValue(snapshot("stopped", replacement.session_id));
    await act(async () => { await result.stop(); });
    expect(client.stop).toHaveBeenLastCalledWith(replacement.session_id);
  });

  it("rediscovers after failure without resuming the failed session", async () => {
    client.getActiveSession = vi.fn().mockRejectedValue(new Error("No active session"));
    await start();
    client.getSession = vi.fn().mockResolvedValue(snapshot("failed"));
    await tick();
    const replacement = snapshot("ready", "replacement-after-failure");
    client.getActiveSession = vi.fn().mockResolvedValue(replacement);
    await tick();
    expect(result.session?.session_id).toBe(replacement.session_id);
    expect(client.resume).not.toHaveBeenCalled();
    expect(client.observe).not.toHaveBeenCalled();
  });

  it("ignores a terminal-session discovery that returns after a newer local start", async () => {
    client.getActiveSession = vi.fn().mockRejectedValue(new Error("No active session"));
    await start();
    await act(async () => { await result.stop(); });
    const read = deferred<ComputerSessionSnapshot>();
    client.getActiveSession = vi.fn().mockReturnValue(read.promise);
    await tick();
    client.start = vi.fn().mockResolvedValue(snapshot("ready", "local-replacement"));
    await act(async () => { await result.start({ approved: true }); });
    await act(async () => { read.resolve(snapshot("ready", "obsolete-discovery")); });
    expect(result.session?.session_id).toBe("local-replacement");
    expect(result.controlIntent).toBeNull();
  });

  it("never revives a terminal session from stale same-ID active discovery", async () => {
    client.getActiveSession = vi.fn().mockRejectedValue(new Error("No active session"));
    await start();
    await act(async () => { await result.stop(); });
    client.getActiveSession = vi.fn().mockResolvedValue(snapshot("ready"));
    await tick(2);
    expect(client.getActiveSession).toHaveBeenCalled();
    expect(result.session?.state).toBe("stopped");
    expect(client.observe).not.toHaveBeenCalled();
  });

  it("preserves existing preview bytes while model observation metadata changes", async () => {
    await start();
    await act(async () => { await result.observe(); });
    const preview = result.observation;
    client.getSession = vi.fn().mockResolvedValue({ ...snapshot(), observation: { ...observationResponse.observation, id: "model-observation" } });
    await tick();
    expect(result.observation).toBe(preview);
    expect(client.observe).toHaveBeenCalledTimes(1);
  });

  it("never overlaps reads and retries rejected snapshots at a bounded cadence", async () => {
    await start();
    const delayed = deferred<ComputerSessionSnapshot>();
    client.getSession = vi.fn().mockReturnValueOnce(delayed.promise).mockResolvedValue(snapshot());
    await tick(10);
    expect(client.getSession).toHaveBeenCalledTimes(1);
    await act(async () => { delayed.reject(new Error("transient read failure")); });
    expect(result.error).toBeNull();
    await tick();
    expect(client.getSession).toHaveBeenCalledTimes(2);
    expect(client.observe).not.toHaveBeenCalled();
  });

  it.each(["pause", "stop"] as const)("discards a late ready snapshot during pending %s", async (kind) => {
    await start();
    const read = deferred<ComputerSessionSnapshot>();
    const control = deferred<ComputerSessionSnapshot>();
    client.getSession = vi.fn().mockReturnValue(read.promise);
    client[kind] = vi.fn().mockReturnValue(control.promise);
    await tick();
    let pending!: ReturnType<typeof result.stop>;
    await act(async () => { pending = result[kind](); });
    await act(async () => { read.resolve({ ...snapshot(), last_receipt: receipt }); });
    expect(result.loading).toBe(true);
    expect(result.controlIntent).toBe(kind);
    expect(result.receipts).toEqual([]);
    await tick(3);
    expect(client.getSession).toHaveBeenCalledTimes(1);
    await act(async () => { control.resolve(snapshot(kind === "pause" ? "paused" : "stopped")); await pending; });
    expect(result.session?.state).toBe(kind === "pause" ? "paused" : "stopped");
  });

  it.each(["pause", "stop"] as const)("retains unconfirmed %s intent until a safe snapshot confirms it", async (kind) => {
    await start();
    client[kind] = vi.fn().mockRejectedValue(new Error("control outcome unknown"));
    await act(async () => { await expect(result[kind]()).rejects.toThrow("control outcome unknown"); });
    await tick();
    expect(result.controlIntent).toBe(kind);
    expect(result.error).toBe("control outcome unknown");
    client.getSession = vi.fn().mockResolvedValue(snapshot(kind === "pause" ? "paused" : "stopped"));
    await tick();
    expect(result.controlIntent).toBeNull();
    expect(result.session?.state).toBe(kind === "pause" ? "paused" : "stopped");
  });

  it("ignores late reads after Stop then Start and resumes polling the new session", async () => {
    await start();
    const read = deferred<ComputerSessionSnapshot>();
    client.getSession = vi.fn().mockReturnValueOnce(read.promise).mockResolvedValue(snapshot("paused", "s2"));
    await tick();
    await act(async () => { await result.stop(); });
    client.start = vi.fn().mockResolvedValue(snapshot("ready", "s2"));
    await act(async () => { await result.start({ approved: true, conversation_ref: "new-conversation" }); });
    await tick(2);
    expect(client.getSession).toHaveBeenCalledTimes(1);
    await act(async () => { read.resolve({ ...snapshot("stopped"), last_receipt: receipt }); });
    expect(result.session?.session_id).toBe("s2");
    expect(result.receipts).toEqual([]);
    expect(result.approvedConversationRef).toBe("new-conversation");
    await tick();
    expect(client.getSession).toHaveBeenLastCalledWith("s2");
    expect(result.session?.state).toBe("paused");
  });

  it("discards a previous client's read even when the new client uses the same session ID", async () => {
    await start();
    const read = deferred<ComputerSessionSnapshot>();
    const oldClient = client;
    oldClient.getSession = vi.fn().mockReturnValue(read.promise);
    await tick();
    client = createTestClient();
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>));
    await act(async () => { await result.loadCapabilities(); });
    await start();
    await tick(2);
    expect(client.getSession).not.toHaveBeenCalled();
    await act(async () => { read.resolve({ ...snapshot("stopped"), last_receipt: receipt }); });
    expect(result.session?.state).toBe("ready");
    expect(result.receipts).toEqual([]);
    await tick();
    expect(client.getSession).toHaveBeenCalledTimes(1);
    expect(oldClient.getSession).toHaveBeenCalledTimes(1);
  });

  it.each(["resolve", "reject"] as const)("cleans up timers and ignores late %s after unmount", async (outcome) => {
    await start();
    const read = deferred<ComputerSessionSnapshot>();
    client.getSession = vi.fn().mockReturnValue(read.promise);
    await tick();
    act(() => root.unmount());
    unmounted = true;
    const previous = result;
    await act(async () => {
      if (outcome === "resolve") read.resolve(snapshot("stopped"));
      else read.reject(new Error("late error"));
    });
    await tick(3);
    expect(result).toBe(previous);
    expect(client.getSession).toHaveBeenCalledTimes(1);
  });

  it("ignores mismatched snapshots and stops polling failed sessions", async () => {
    await start();
    client.getSession = vi.fn().mockResolvedValueOnce(snapshot("stopped", "other-session")).mockResolvedValue(snapshot("failed"));
    await tick();
    expect(result.session?.state).toBe("ready");
    await tick(4);
    expect(result.session?.state).toBe("failed");
    expect(client.getSession).toHaveBeenCalledTimes(2);
  });
});
