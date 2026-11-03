import { act, StrictMode } from "react";
import { I18nProvider } from "../../lib/i18n";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DesktopServiceBridge } from "../desktopServiceBridge";
import { COMPUTER_SESSION_POLL_INTERVAL_MS } from "./useComputerSession";
import { COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT, saveComputerWorkspacePreferences } from "./computerWorkspacePreferences";
import type { SessionRef } from "../routes";
import { ComputerWorkspace } from "./ComputerWorkspace";
import { ComputerApprovalDialog } from "./ComputerApprovalDialog";
import type { ComputerCapabilities, ComputerObservationResponse, ComputerSessionSnapshot } from "./types";
import { capabilities, createTestClient, deferred, observationResponse, snapshot } from "./testFixtures";

describe("ComputerWorkspace", () => {
  let root: Root;
  let container: HTMLDivElement;
  let storage: Record<string, string>;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    storage = { "niuma.computer-workspace.v1": JSON.stringify({ displayMode: "expanded", collapsed: false, position: null }) };
    Object.defineProperty(window, "localStorage", { configurable: true, value: {
      getItem: (key: string) => storage[key] ?? null,
      setItem: (key: string, value: string) => { storage[key] = value; },
      removeItem: (key: string) => { delete storage[key]; },
      clear: () => { storage = {}; },
    } });
    root = createRoot(container);
    storage["niuma-webui.language.v1"] = "en";
  });
  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    delete window.go;
    vi.useRealTimers();
    storage = {};
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });
  function button(label: string): HTMLButtonElement {
    const found = Array.from(document.body.querySelectorAll("button")).find((element) => element.textContent === label);
    if (!found) throw new Error(`Missing button: ${label}`);
    return found;
  }
  async function click(label: string) { await act(async () => { button(label).click(); }); }
  async function clickAria(label: string) {
    await act(async () => {
      const target = document.body.querySelector<HTMLButtonElement>(`[aria-label="${label}"]`);
      if (!target) throw new Error(`Missing aria button: ${label}`);
      target.click();
    });
  }

  function nativeBridge(overrides: Partial<DesktopServiceBridge> = {}) {
    const bridge = {
      RestartLocalService: vi.fn().mockResolvedValue(undefined),
      IsComputerOverlayAvailable: vi.fn().mockResolvedValue(true),
      ShowComputerOverlay: vi.fn().mockResolvedValue(undefined),
      UpdateComputerOverlay: vi.fn().mockResolvedValue(undefined),
      ...overrides,
    };
    window.go = { main: { app: bridge } };
    return bridge;
  }

  it("has no panel with a non-desktop / unavailable bridge", async () => {
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={null} /></I18nProvider>));
    expect(container.innerHTML).toBe("");
  });

  it("renders the DOM workspace by default without calling native overlay methods", async () => {
    const bridge = nativeBridge();
    const client = createTestClient();

    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));

    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
    expect(bridge.IsComputerOverlayAvailable).not.toHaveBeenCalled();
    expect(bridge.UpdateComputerOverlay).not.toHaveBeenCalled();
    expect(bridge.ShowComputerOverlay).not.toHaveBeenCalled();

    await clickAria("Collapse Computer Use workspace");
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    await clickAria("Open Computer Use workspace");
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
  });

  it("opens from the explicit workspace event and expands into a draggable workspace", async () => {
    storage = { "niuma.computer-workspace.v1": JSON.stringify({ displayMode: "auto", collapsed: true, position: null }), "niuma-webui.language.v1": "en" };
    const client = createTestClient();
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    await act(async () => { window.dispatchEvent(new Event("niuma:computer-workspace-open")); });
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
    expect(document.body.querySelector('[aria-label="Collapse Computer Use workspace"]')).not.toBeNull();
  });

  it("auto mode keeps idle compact, expands for a session, and collapses after Stop", async () => {
    storage = { "niuma.computer-workspace.v1": JSON.stringify({ displayMode: "auto", collapsed: false, position: null }), "niuma-webui.language.v1": "en" };
    const client = createTestClient();
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    expect(document.body.querySelector('[aria-label="Open Computer Use workspace"]')).not.toBeNull();
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();

    await act(async () => { window.dispatchEvent(new Event("niuma:computer-workspace-open")); });
    await click("Start session");
    await click("Approve session");
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).toBeNull();

    await click("Stop");
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    expect(document.body.querySelector('[aria-label="Open Computer Use workspace"]')).not.toBeNull();
  });

  it.each(["compact", "expanded"] as const)("honors the %s display mode while preserving manual toggles", async (mode) => {
    storage = { "niuma.computer-workspace.v1": JSON.stringify({ displayMode: mode, collapsed: mode === "compact", position: null }), "niuma-webui.language.v1": "en" };
    const client = createTestClient();
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    const initiallyExpanded = mode === "expanded";
    expect(document.body.querySelector('[aria-label="Computer workspace"]') !== null).toBe(initiallyExpanded);

    const toggleLabel = initiallyExpanded ? "Collapse Computer Use workspace" : "Open Computer Use workspace";
    await clickAria(toggleLabel);
    expect(document.body.querySelector('[aria-label="Computer workspace"]') !== null).toBe(!initiallyExpanded);
  });

  it("keeps model-managed sessions out of the main WebView when native overlay owns them", async () => {
    const bridge = nativeBridge();
    const client = createTestClient();
    client.start = vi.fn().mockResolvedValue({ ...snapshot(), owner_kind: "managed_conversation" });
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).toBeNull();
    expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).not.toBeNull();
    expect(bridge.IsComputerOverlayAvailable).toHaveBeenCalledOnce();
    expect(bridge.UpdateComputerOverlay).toHaveBeenLastCalledWith(expect.objectContaining({
      visible: true, session_id: "s1", can_stop: true, can_pause: false, can_resume: false,
    }));
    expect(client.observe).not.toHaveBeenCalled();
  });

  it.each(["false", "absent", "rejected"] as const)("retains DOM controls when the native capability probe is %s", async (probe) => {
    const bridge = nativeBridge({ IsComputerOverlayAvailable: probe === "absent" ? undefined
      : probe === "false" ? vi.fn().mockResolvedValue(false) : vi.fn().mockRejectedValue(new Error("unsupported")) });
    const client = createTestClient();
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    expect(button("Stop").disabled).toBe(false);
    expect(button("Pause").disabled).toBe(false);
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
    expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).toBeNull();
    expect(bridge.UpdateComputerOverlay).not.toHaveBeenCalled();
    expect(client.observe).toHaveBeenCalledWith("s1");
  });

  it("keeps DOM controls until the asynchronous capability probe actually succeeds", async () => {
    const probe = deferred<boolean>();
    nativeBridge({ IsComputerOverlayAvailable: vi.fn().mockReturnValue(probe.promise) });
    const client = createTestClient();
    vi.useFakeTimers();
    client.getActiveSession = vi.fn().mockResolvedValue(snapshot());
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    expect(button("Stop").disabled).toBe(false);
    expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).toBeNull();
    await act(async () => probe.resolve(true));
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).not.toBeNull();
    expect(client.observe).not.toHaveBeenCalled();
  });

  it.each(["UpdateComputerOverlay", "ShowComputerOverlay"] as const)("retains DOM controls when %s is missing even if the probe exists", async (method) => {
    nativeBridge({ [method]: undefined });
    const client = createTestClient();
    vi.useFakeTimers();
    client.getActiveSession = vi.fn().mockResolvedValue(snapshot());
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    expect(button("Stop").disabled).toBe(false);
    expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).toBeNull();
  });

  it("keeps the reopen launcher through host Hide and state polls, without Observe or automatic Show", async () => {
    vi.useFakeTimers();
    const bridge = nativeBridge();
    const client = createTestClient();
    client.getSession = vi.fn().mockResolvedValue(snapshot("paused"));
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    expect(client.observe).not.toHaveBeenCalled();
    expect(bridge.ShowComputerOverlay).not.toHaveBeenCalled();
    // Hide is host-owned and has no frontend event. Its reopen launcher must
    // remain usable even as subsequent status snapshots change session state.
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    expect(bridge.UpdateComputerOverlay).toHaveBeenLastCalledWith(expect.objectContaining({
      expanded: false, display_mode: "expanded", session_id: "s1", detail: "paused", can_resume: true,
    }));
    expect(bridge.ShowComputerOverlay).not.toHaveBeenCalled();
    await clickAria("Show Computer Use panel");
    expect(bridge.ShowComputerOverlay).toHaveBeenCalledOnce();
    await act(async () => { window.dispatchEvent(new Event("niuma:computer-workspace-open")); });
    expect(bridge.ShowComputerOverlay).toHaveBeenCalledTimes(2);
    expect(client.observe).not.toHaveBeenCalled();
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    client.getSession = vi.fn().mockResolvedValue(snapshot("stopped"));
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).toBeNull();
    expect(bridge.UpdateComputerOverlay).toHaveBeenLastCalledWith(expect.objectContaining({ visible: false, session_id: "" }));
    expect(button("Start session").disabled).toBe(false);
  });

  it("sends the display preference on every snapshot, without Show on preference changes", async () => {
    const bridge = nativeBridge();
    const client = createTestClient();
    vi.useFakeTimers();
    client.getActiveSession = vi.fn().mockResolvedValue(snapshot());
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    for (const displayMode of ["auto", "compact", "expanded"] as const) {
      await act(async () => {
        saveComputerWorkspacePreferences({ displayMode, collapsed: displayMode !== "expanded", position: null });
        window.dispatchEvent(new Event(COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT));
      });
      expect(bridge.UpdateComputerOverlay).toHaveBeenLastCalledWith(expect.objectContaining({ display_mode: displayMode, expanded: false }));
      expect(document.body.querySelector('[aria-label="Show Computer Use panel"]')).not.toBeNull();
    }
    expect(vi.mocked(bridge.UpdateComputerOverlay!).mock.calls.every(([value]) => value.display_mode !== undefined)).toBe(true);
    expect(bridge.ShowComputerOverlay).not.toHaveBeenCalled();
    expect(client.observe).not.toHaveBeenCalled();
  });

  it.each([
    ["en", "Computer Use", "ready", "Show Computer Use panel"],
    ["zh", "电脑操作", "已就绪", "显示电脑操作面板"],
  ])("uses friendly %s copy in the native snapshot and tiny launcher", async (language, title, detail, label) => {
    storage["niuma-webui.language.v1"] = language;
    const bridge = nativeBridge();
    const client = createTestClient();
    vi.useFakeTimers();
    client.getActiveSession = vi.fn().mockResolvedValue(snapshot());
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    expect(bridge.UpdateComputerOverlay).toHaveBeenLastCalledWith(expect.objectContaining({ title, detail, language }));
    expect(document.body.textContent).not.toContain("native_host");
    expect(document.body.querySelector(`[aria-label="${label}"]`)?.textContent).toBe(title);
    await clickAria(label);
    expect(bridge.ShowComputerOverlay).toHaveBeenCalledOnce();
  });

  it.each(["ready", "paused"] as const)("keeps model-managed %s sessions Stop-only in native and DOM fallback", async (state) => {
    const bridge = nativeBridge();
    const client = createTestClient();
    vi.useFakeTimers();
    client.getActiveSession = vi.fn().mockResolvedValue({ ...snapshot(state), owner_kind: "managed_conversation" });
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} nativeOverlay /></I18nProvider>));
    await act(async () => vi.advanceTimersByTimeAsync(COMPUTER_SESSION_POLL_INTERVAL_MS));
    expect(bridge.UpdateComputerOverlay).toHaveBeenLastCalledWith(expect.objectContaining({ can_stop: true, can_pause: false, can_resume: false }));
    // A failed reopen falls back safely instead of leaving users without Stop.
    vi.mocked(bridge.ShowComputerOverlay!).mockRejectedValue(new Error("panel unavailable"));
    await clickAria("Show Computer Use panel");
    expect(button("Stop").disabled).toBe(false);
    const controls = Array.from(document.body.querySelectorAll("button"), (element) => element.textContent);
    expect(controls).not.toContain("Refresh screenshot");
    expect(controls).not.toContain("Pause");
    expect(controls).not.toContain("Resume");
    await click("Stop");
    expect(client.stop).toHaveBeenCalledWith("s1");
    expect(client.pause).not.toHaveBeenCalled();
    expect(client.resume).not.toHaveBeenCalled();
    expect(client.observe).not.toHaveBeenCalled();
  });

  it("opens the matching macOS permission page and refreshes readiness after returning", async () => {
    const client = createTestClient();
    client.getCapabilities = vi.fn()
      .mockResolvedValueOnce({ available: true, capabilities: { ...capabilities, input_readiness: "permission_required", permission_state: "required" } })
      .mockResolvedValue({ available: true, capabilities });
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    await act(async () => { window.dispatchEvent(new Event("niuma:computer-workspace-open")); });
    expect(document.body.textContent).toContain("Open Accessibility settings");
    await click("Open Accessibility settings");
    expect(client.openPermissionSettings).toHaveBeenCalledWith("accessibility");
    await act(async () => { window.dispatchEvent(new Event("focus")); });
    expect(client.getCapabilities).toHaveBeenCalledTimes(2);
    expect(document.body.textContent).not.toContain("Open Accessibility settings");
    expect(button("Start session").disabled).toBe(false);
  });

  it("uses the visible DOM workspace as the single progress surface under StrictMode", async () => {
    const client = createTestClient();
    const render = () => <I18nProvider><StrictMode><ComputerWorkspace client={client} /></StrictMode></I18nProvider>;
    await act(async () => root.render(render()));
    expect(button("Start session").disabled).toBe(false);
    await click("Start session");
    expect(client.start).not.toHaveBeenCalled();
    await click("Approve session");
    expect(client.start).toHaveBeenCalledWith({ approved: true });
    expect(client.observe).toHaveBeenCalledWith("s1");
    expect(document.body.querySelector("img")?.getAttribute("src")).toBe(`data:image/png;base64,${observationResponse.image_data}`);
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).toBeNull();
    expect(button("Stop").disabled).toBe(false);
    await click("Pause");
    expect(client.pause).toHaveBeenCalledWith("s1");
    expect(button("Resume").disabled).toBe(false);
    await act(async () => root.render(render()));
    expect(client.start).toHaveBeenCalledTimes(1);
    expect(document.body.querySelector("img")).not.toBeNull();
  });

  it("keeps separate progress available when the DOM workspace is collapsed", async () => {
    const client = createTestClient();
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).toBeNull();

    await clickAria("Collapse Computer Use workspace");

    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).not.toBeNull();
  });

  it("captures the displayed managed conversation even if selection changes before approval", async () => {
    const client = createTestClient();
    const render = (ref: SessionRef | null) => root.render(<I18nProvider><ComputerWorkspace client={client} selectedConversationRef={ref} /></I18nProvider>);
    await act(async () => render("tenant:alpha"));
    await click("Start session");
    const dialog = () => document.body.querySelector('[role="dialog"]');
    expect(dialog()?.textContent).toContain("tenant:alpha");
    expect(dialog()?.textContent).toContain("access to observe and operate your desktop");
    expect(dialog()?.textContent).toContain("does not confirm that autonomous agent execution is connected");
    await act(async () => render("tenant:beta"));
    expect(dialog()?.textContent).toContain("tenant:alpha");
    expect(dialog()?.textContent).not.toContain("tenant:beta");
    await click("Approve session");
    expect(client.start).toHaveBeenCalledExactlyOnceWith({ approved: true, conversation_ref: "tenant:alpha" });
    expect(document.body.textContent).toContain("Desktop access granted to conversation: tenant:alpha");
    expect(document.body.textContent).not.toContain("tenant:beta");
    await click("Pause");
    await act(async () => render(null));
    expect(document.body.textContent).toContain("Desktop access granted to conversation: tenant:alpha");
    expect(button("Stop").disabled).toBe(false);
    await click("Resume");
    expect(document.body.textContent).toContain("Desktop access granted to conversation: tenant:alpha");
    await click("Stop");
    expect(client.stop).toHaveBeenCalledWith("s1");
    expect(document.body.textContent).not.toContain("Desktop access granted to conversation");
  });

  it("retains the approved ref while Start is pending and when collapsed", async () => {
    const client = createTestClient();
    const pending = deferred<ComputerSessionSnapshot>();
    client.start = vi.fn().mockReturnValue(pending.promise);
    const render = (ref: SessionRef) => root.render(<I18nProvider><ComputerWorkspace client={client} selectedConversationRef={ref} /></I18nProvider>);
    await act(async () => render("tenant:alpha"));
    await click("Start session");
    await click("Approve session");
    await act(async () => render("tenant:beta"));
    expect(document.body.textContent).not.toContain("Desktop access granted to conversation");
    await act(async () => pending.resolve(snapshot()));
    expect(document.body.textContent).toContain("Desktop access granted to conversation: tenant:alpha");
    await act(async () => { (document.body.querySelector('[aria-label="Collapse Computer Use workspace"]') as HTMLButtonElement).click(); });
    expect(document.body.querySelector('[aria-label="Open Computer Use workspace"]')?.textContent).toContain("tenant:alpha");
    expect(document.body.textContent).not.toContain("tenant:beta");
  });

  it.each<SessionRef | null>([null, "local:workspace", "tenant:", "tenant:bad/ref", "tenant:bad ref", "tenant:bad:key"])("keeps %s explicitly local and never upgrades an open preview approval", async (ref) => {
    const client = createTestClient();
    const render = (selectedConversationRef: SessionRef | null) => root.render(<I18nProvider><ComputerWorkspace client={client} selectedConversationRef={selectedConversationRef} /></I18nProvider>);
    await act(async () => render(ref));
    await click("Start session");
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain("Allow local desktop preview?");
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain("does NOT allow any agent or conversation");
    await act(async () => render("tenant:alpha"));
    expect(document.body.querySelector('[role="dialog"]')?.textContent).not.toContain("tenant:alpha");
    await click("Approve session");
    expect(client.start).toHaveBeenCalledExactlyOnceWith({ approved: true });
    expect(document.body.textContent).toContain("Local preview only — no agent access");
    expect(document.body.textContent).not.toContain("Desktop access granted to conversation");
    expect(button("Stop").disabled).toBe(false);
  });

  it("uses a fresh approval after cancellation and Stop, including managed channel refs", async () => {
    const client = createTestClient();
    const render = (ref: SessionRef) => root.render(<I18nProvider><ComputerWorkspace client={client} selectedConversationRef={ref} /></I18nProvider>);
    await act(async () => render("tenant:alpha"));
    await click("Start session");
    await click("Cancel");
    expect(client.start).not.toHaveBeenCalled();
    await act(async () => render("tenant:channel:beta"));
    await click("Start session");
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain("tenant:channel:beta");
    await click("Approve session");
    expect(client.start).toHaveBeenLastCalledWith({ approved: true, conversation_ref: "tenant:channel:beta" });
    await click("Stop");
    await act(async () => render("local:workspace"));
    await click("Start session");
    await click("Approve session");
    expect(client.start).toHaveBeenLastCalledWith({ approved: true });
    expect(document.body.textContent).toContain("Local preview only — no agent access");
    expect(document.body.textContent).not.toContain("tenant:channel:beta");
  });

  it("does not claim a grant or silently fall back to preview after a rejected Start", async () => {
    const client = createTestClient();
    client.start = vi.fn().mockRejectedValue(new Error("conversation cannot authorize computer use"));
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} selectedConversationRef="tenant:alpha" /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    expect(client.start).toHaveBeenCalledExactlyOnceWith({ approved: true, conversation_ref: "tenant:alpha" });
    expect(client.observe).not.toHaveBeenCalled();
    expect(document.body.querySelector('[role="alert"]')?.textContent).toContain("conversation cannot authorize computer use");
    expect(document.body.textContent).not.toContain("Desktop access granted to conversation");
    expect(button("Start session").disabled).toBe(false);
  });

  it("shows a bridge fault recovery guide and blocks unsafe actions while preserving Stop", async () => {
    const client = createTestClient();
    client.getRuntimeStatus = vi.fn().mockResolvedValue({
        state: "faulted", active_session: true, session_id: "s1", available: false, error_code: "bridge_disconnected",
        fault: { state: "faulted", error_code: "bridge_disconnected", recoverable: false },
      });
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
    expect(document.body.textContent).toContain("runtime connection was lost");
    expect(button("Stop").disabled).toBe(false);
    expect(button("Refresh screenshot").disabled).toBe(true);
    expect(button("Pause").disabled).toBe(true);
    expect(button("Refresh runtime status")).toBeTruthy();
    await click("Stop runtime");
    expect(client.stopRuntime).toHaveBeenCalled();
  });

  it("keeps Pause and Stop clickable during capture and ignores late control/capture responses", async () => {
    const client = createTestClient();
    const capture = deferred<ComputerObservationResponse>();
    const pause = deferred<ComputerSessionSnapshot>();
    client.observe = vi.fn().mockReturnValue(capture.promise);
    client.pause = vi.fn().mockReturnValue(pause.promise);
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    await click("Start session");
    await click("Approve session");
    expect(button("Refresh screenshot").disabled).toBe(true);
    expect(button("Pause").disabled).toBe(false);
    expect(button("Stop").disabled).toBe(false);
    await click("Pause");
    expect(client.pause).toHaveBeenCalledWith("s1");
    expect(button("Stop").disabled).toBe(false);
    await click("Stop");
    expect(client.stop).toHaveBeenCalledWith("s1");
    await act(async () => { pause.resolve(snapshot("paused")); capture.resolve(observationResponse); });
    expect(document.body.querySelector(".webui2-computer-state")?.textContent).toBe("stopped");
    expect(document.body.querySelector("img")).toBeNull();
    expect(button("Start session").disabled).toBe(false);
  });

  it("surfaces capability rejection without an unhandled effect promise", async () => {
    const client = createTestClient();
    client.getCapabilities = vi.fn().mockRejectedValue(new Error("native helper unavailable"));
    await act(async () => root.render(<I18nProvider><StrictMode><ComputerWorkspace client={client} /></StrictMode></I18nProvider>));
    expect(document.body.querySelector('[role="alert"]')).toBeNull();
    expect(button("Start session").disabled).toBe(true);
  });

  it("honors available=false even when capabilities look ready", async () => {
    const client = createTestClient();
    client.getCapabilities = vi.fn().mockResolvedValue({ available: false, capabilities, error_code: "disabled", error_message: "Computer Use disabled by host" });
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    expect(button("Start session").disabled).toBe(true);
    expect(document.body.querySelector('[role="alert"]')).toBeNull();
  });

  const readinessCases: Partial<ComputerCapabilities>[] = [
    { capture_readiness: "permission_required" }, { capture_readiness: "unknown" },
    { input_readiness: "failed" }, { input_readiness: "unavailable" },
    { permission_state: "required" }, { permission_state: "denied" },
    { permission_state: "unknown" }, { image_supported: false }, { supports_stop: false }
  ];
  it.each(readinessCases)("gates both Start and approval for %j", async (overrides) => {
    const blocked = { ...capabilities, ...overrides };
    const client = createTestClient();
    client.getCapabilities = vi.fn().mockResolvedValue({ available: true, capabilities: blocked });
    await act(async () => root.render(<I18nProvider><ComputerWorkspace client={client} /></I18nProvider>));
    expect(button("Start session").disabled).toBe(true);
    expect(document.body.querySelector('[role="status"]')).toBeNull();
    const approve = vi.fn();
    await act(async () => root.render(<I18nProvider><ComputerApprovalDialog capabilities={blocked} available busy={false} onApprove={approve} onCancel={vi.fn()} /></I18nProvider>));
    expect(button("Approve session").disabled).toBe(true);
    await click("Approve session");
    expect(approve).not.toHaveBeenCalled();
  });
});
