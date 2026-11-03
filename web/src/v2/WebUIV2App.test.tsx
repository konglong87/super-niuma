import niumaMark from "./assets/niuma-mark.svg";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { I18nProvider } from "../lib/i18n";
import * as api from "../lib/api";
import type { IdentityConfig } from "../lib/types";
import type { SessionControlClient } from "./api/sessionControlClient";
import { createMockSessionControlClient } from "./api/mockSessionControlClient";
import type { SessionDetail } from "./types";
import { WebUIV2App } from "./WebUIV2App";
import { GLOBAL_SETTINGS_SAVED_EVENT } from "./settings/globalSettingsDraft";
import { DESKTOP_READINESS } from "./useDesktopReadiness";
import { capabilities, observationResponse, snapshot } from "./computer/testFixtures";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "test-tenant",
  userId: "test-user",
  deviceId: "test-device",
  model: "test-model"
};

describe("WebUIV2App", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
        clear: () => storage.clear()
      }
    });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    delete window.go;
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
    vi.useRealTimers();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function renderAt(pathname: string, language?: "en" | "zh", client: SessionControlClient = createMockSessionControlClient()): void {
    window.history.replaceState({}, "", pathname);
    if (language) {
      storage.set("niuma-webui.language.v1", language);
    }
    act(() => {
      root.render(
        <I18nProvider>
          <QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={identity} /></QueryClientProvider>
        </I18nProvider>
      );
    });
  }

  it("uses the production HTTP client by default while unit tests can inject a deterministic mock", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ data: [] }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    window.history.replaceState({}, "", "/webui/v2");
    act(() => {
      root.render(<I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App identity={identity} /></QueryClientProvider></I18nProvider>);
    });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());

    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    expect(calls.map(([url]) => url)).toEqual([
      "/api/tenant/session-control/sessions?source=tenant",
      "/api/tenant/session-control/sessions?source=local",
      "/api/runtime/settings"
    ]);

    act(() => root.render(<I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={createMockSessionControlClient()} identity={identity} /></QueryClientProvider></I18nProvider>));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    vi.unstubAllGlobals();
  });

  function enterSessionRef(input: HTMLInputElement, value: string): void {
    const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    act(() => {
      setValue?.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("uses the deep-linked session as its initial selection", () => {
    renderAt("/webui/v2/sessions/tenant%3Aseed-1");

    expect(host.querySelector('main[aria-label="Session workspace"]')?.getAttribute("data-session-ref")).toBe("tenant:seed-1");
  });

  it("opens and restores the desktop root as the session index without masking invalid links", async () => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    storage.set("niuma.desktop.onboarding.v1", "done");
    renderAt("/");
    await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });
    expect(host.querySelector(".webui2-route-error")).toBeNull();
    expect(host.querySelector('img[alt="Niuma mascot"]')).not.toBeNull();

    window.history.pushState({}, "", "/webui/v2/sessions/tenant%3Aseed-1");
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    expect(host.querySelector("main")?.getAttribute("data-session-ref")).toBe("tenant:seed-1");

    window.history.replaceState({}, "", "/");
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    expect(host.querySelector(".webui2-route-error")).toBeNull();
    expect(host.querySelector('img[alt="Niuma mascot"]')).not.toBeNull();

    window.history.replaceState({}, "", "/webui/v2/sessions/not-a-ref");
    act(() => window.dispatchEvent(new PopStateEvent("popstate")));
    expect(host.querySelector(".webui2-route-error")?.textContent).toContain("This session link is invalid.");
  });

  it("restores selection after browser history navigation", () => {
    renderAt("/webui/v2/sessions/tenant%3Aseed-1");
    window.history.replaceState({}, "", "/webui/v2");

    act(() => window.dispatchEvent(new PopStateEvent("popstate")));

    expect(host.querySelector('main[aria-label="Session workspace"]')?.getAttribute("data-session-ref")).toBeNull();
  });

  it("renders a recoverable state for an invalid deep link", async () => {
    renderAt("/webui/v2/sessions/not-a-ref");

    expect(host.querySelector('main[aria-label="Session workspace"]')).not.toBeNull();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("This session link is invalid.");

    const recoverButton = Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "Back to sessions");
    expect(recoverButton).toBeDefined();
    act(() => recoverButton?.click());
	await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    expect(window.location.pathname).toBe("/webui/v2");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    await vi.waitFor(async () => {
      await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });
      expect(host.querySelector(".webui2-empty-state")).not.toBeNull();
    });
  });

  it("renders the animated logo on the default index after sessions load", async () => {
	renderAt("/webui/v2");
	await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });

	expect(host.querySelector('img[alt="Niuma mascot"]')).not.toBeNull();
	expect(host.textContent).toContain("Create a session");
  });

  it("opens a dedicated search page with recent sessions and searches the full session list", async () => {
    renderAt("/webui/v2");
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Search sessions"]')?.click());
    await act(async () => { await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });
    expect(host.querySelector(".webui2-session-search-dialog")).not.toBeNull();
    expect(host.querySelector(".webui2-session-search-results")?.querySelectorAll(".webui2-session-search-result")).toHaveLength(3);

    const input = host.querySelector<HTMLInputElement>('.webui2-session-search-dialog input[aria-label="Search sessions"]');
    expect(document.activeElement).toBe(input);
    enterSessionRef(input!, "beta");

    expect(window.location.pathname).toBe("/webui/v2");
    const searchDialog = host.querySelector<HTMLElement>(".webui2-session-search-dialog");
    expect(searchDialog?.querySelectorAll(".webui2-session-search-result")).toHaveLength(1);
    expect(searchDialog?.textContent).toContain("Design review");
    expect(searchDialog?.textContent).not.toContain("Release coordination");
    expect(searchDialog?.textContent).not.toContain("Local workspace");

    act(() => host.querySelector<HTMLButtonElement>(".webui2-session-search-result")?.click());
    expect(window.location.pathname).toBe("/webui/v2/sessions/tenant%3Abeta");
    expect(host.querySelector(".webui2-session-search-dialog")).toBeNull();
    expect(host.querySelector('main[aria-label="Session workspace"]')?.getAttribute("data-session-ref")).toBe("tenant:beta");
  });

  it("closes the dedicated search page with Escape", async () => {
    renderAt("/webui/v2");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Search sessions"]')?.click());
    const input = host.querySelector<HTMLInputElement>('.webui2-session-search-dialog input[aria-label="Search sessions"]');
    act(() => input?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector(".webui2-session-search-dialog")).toBeNull();
  });

  it("hides and restores the session sidebar while keeping the workspace mounted", () => {
    renderAt("/webui/v2/sessions/tenant%3Aalpha");
    const page = host.querySelector<HTMLElement>(".webui2-page");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Hide sidebar"]')?.click());
    expect(page?.getAttribute("data-sidebar-hidden")).toBe("true");
    expect(host.querySelector(".webui2-sidebar")).not.toBeNull();
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Show sidebar"]')).not.toBeNull();

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Show sidebar"]')?.click());
    expect(page?.getAttribute("data-sidebar-hidden")).toBe("false");
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Hide sidebar"]')).not.toBeNull();
  });

  it("opens full settings without unmounting the current composer and returns to the same session", async () => {
    renderAt("/webui/v2/sessions/tenant%3Aalpha");
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
    const composer = host.querySelector<HTMLTextAreaElement>(".webui2-composer textarea");
    const textSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
    act(() => { textSetter?.call(composer, "Keep my draft"); composer?.dispatchEvent(new Event("input", { bubbles: true })); });
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Account menu"]')?.click());
    const settings = host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="System settings"]');
    act(() => settings?.click());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(window.location.pathname).toBe("/webui/v2/settings/general");
    expect(host.querySelector(".webui2-settings-center")).not.toBeNull();
    expect(host.querySelector<HTMLElement>(".webui2-chat-shell")?.hidden).toBe(true);
    expect(host.querySelector(".webui2-composer textarea")).toBe(composer);
    act(() => host.querySelector<HTMLButtonElement>(".settings-back")?.click());
    expect(window.location.pathname).toBe("/webui/v2/sessions/tenant%3Aalpha");
    expect(host.querySelector(".webui2-composer textarea")).toBe(composer);
    expect(composer?.value).toBe("Keep my draft");
    expect(host.querySelector<HTMLElement>(".webui2-chat-shell")?.hidden).toBe(false);
  });

  it("has no top toolbar and exposes a mobile session navigation control", () => {
    renderAt("/webui/v2");
    expect(host.querySelector('[role="toolbar"]')).toBeNull();
    const openSessions = host.querySelector<HTMLButtonElement>('button[aria-label="Open sessions"]');
    expect(openSessions).not.toBeNull();
    act(() => openSessions?.click());
    expect(host.querySelector(".webui2-page")?.getAttribute("data-sidebar-open")).toBe("true");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Account menu"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="System settings"]')?.click());
    expect(host.querySelector(".webui2-page")?.getAttribute("data-sidebar-open")).toBe("false");
    expect(host.querySelector(".webui2-settings-center")).not.toBeNull();
  });

  it("keeps invalid settings drafts across routes and guards browser back without closing the conversation stream", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ doc: { provider: "custom", model: "test-model" }, revision: "r1", path: "/settings.json", exists: true, masked: [] }), { headers: { "Content-Type": "application/json" } })));
    const client = createMockSessionControlClient();
    const subscriptionSignals: AbortSignal[] = [];
    client.subscribe = vi.fn(async (_identity, _sessions, _onPage, signal) => new Promise<void>((resolve) => { subscriptionSignals.push(signal); signal.addEventListener("abort", () => resolve(), { once: true }); }));
    renderAt("/webui/v2/sessions/tenant%3Aalpha", "zh", client);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="账户菜单"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="系统设置"]')?.click());
    const nav = (label: string) => Array.from(host.querySelectorAll<HTMLButtonElement>(".settings-navigation nav button")).find((button) => button.textContent?.includes(label));
    act(() => nav("全局 Settings JSON")?.click());
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
    const editor = host.querySelector<HTMLTextAreaElement>(".global-settings-json textarea");
    expect(editor).not.toBeNull();
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
    act(() => { setter?.call(editor, '{"model":'); editor?.dispatchEvent(new Event("input", { bubbles: true })); });
    act(() => nav("大模型设置")?.click());
    expect(host.querySelector(".settings-content")?.textContent).toContain("JSON");
    act(() => nav("全局 Settings JSON")?.click());
    expect(host.querySelector<HTMLTextAreaElement>(".global-settings-json textarea")?.value).toBe('{"model":');
    act(() => host.querySelector<HTMLButtonElement>(".settings-back")?.click());
    expect(host.querySelector('[role="alertdialog"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>(".webui2-unsaved-changes-cancel")?.click());
    expect(window.location.pathname).toBe("/webui/v2/settings/json");
    act(() => { window.history.replaceState({}, "", "/webui/v2/sessions/tenant%3Aalpha"); window.dispatchEvent(new PopStateEvent("popstate")); });
    expect(window.location.pathname).toBe("/webui/v2/settings/json");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-unsaved-changes-cancel")?.click());
    expect(subscriptionSignals.length).toBeGreaterThan(0);
    expect(subscriptionSignals.some((signal) => !signal.aborted)).toBe(true);
    act(() => host.querySelector<HTMLButtonElement>(".settings-back")?.click());
    act(() => host.querySelector<HTMLButtonElement>(".webui2-unsaved-changes-discard")?.click());
    expect(window.location.pathname).toBe("/webui/v2/sessions/tenant%3Aalpha");
    expect(host.querySelector(".webui2-settings-center")).toBeNull();
  });

  it("switches settings databases with discard protection while preserving the chat draft and stream", async () => {
    const prefix = "/api/runtime/settings/environments";
    const fetchMock = vi.fn(async (url: string) => {
      const data = url === prefix ? { environments: [
        { id: "current", label: "Web", database: "web_db", tenant_key: "test-tenant", user_id: "test-user", api_path: "", available: true },
        { id: "channel", label: "Channel", database: "channel_db", tenant_key: "target-tenant", user_id: "worker", api_path: "/runtime/settings/environments/channel", available: true }
      ], global_settings_path: "/shared/settings.json", global_settings_shared: true }
        : url.includes("/runtime/settings") && !url.includes("/environments/")
          ? { doc: { provider: "custom", model: "test-model" }, revision: "r1", path: "/shared/settings.json", exists: true, masked: [] }
          : { data: [] };
      return new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = createMockSessionControlClient();
    const signals: AbortSignal[] = [];
    client.subscribe = vi.fn(async (_identity, _sessions, _onPage, signal) => new Promise<void>((resolve) => { signals.push(signal); signal.addEventListener("abort", () => resolve(), { once: true }); }));
    renderAt("/webui/v2/sessions/tenant%3Aalpha", "en", client);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    const composer = host.querySelector<HTMLTextAreaElement>(".webui2-composer textarea")!;
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
    act(() => { setter.call(composer, "Keep this chat draft"); composer.dispatchEvent(new Event("input", { bubbles: true })); });
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Account menu"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="System settings"]')?.click());
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    const switchTo = (id: string) => {
      act(() => { host.querySelector<HTMLButtonElement>('button[aria-label="Settings environment"]')?.click(); });
      act(() => { host.querySelector<HTMLButtonElement>(`[role="option"][data-value="${id}"]`)?.click(); });
    };
    const nav = (label: string) => act(() => Array.from(host.querySelectorAll<HTMLButtonElement>(".settings-navigation nav button")).find((button) => button.textContent === label)?.click());
    switchTo("channel");
    nav("Agent definitions");
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    const profileCall = calls.find(([url]) => url.startsWith(`${prefix}/channel/tenant/agent-profiles`));
    expect(profileCall).toBeDefined();
    expect(new Headers(profileCall![1].headers).get("X-Tenant-Key")).toBe("test-tenant");
    expect(new Headers(profileCall![1].headers).get("X-User-Id")).toBe("test-user");
    expect(host.querySelector(".settings-environment-selector")?.textContent).toContain("channel_db");
    nav("Settings JSON");
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    expect(calls.some(([url]) => url === "/api/runtime/settings")).toBe(true);
    expect(calls.some(([url]) => url.includes("/channel/runtime/settings"))).toBe(false);
    expect(host.textContent).toContain("Global settings shared by Web and channel workers");
    const editor = host.querySelector<HTMLTextAreaElement>(".global-settings-json textarea")!;
    act(() => { setter.call(editor, '{"model":'); editor.dispatchEvent(new Event("input", { bubbles: true })); });
    switchTo("current");
    expect(host.querySelector('[role="alertdialog"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>(".webui2-unsaved-changes-cancel")?.click());
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Settings environment"]')?.dataset.value).toBe("channel");
    expect(editor.value).toBe('{"model":');
    switchTo("current");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-unsaved-changes-discard")?.click());
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Settings environment"]')?.dataset.value).toBe("current");
    expect(host.querySelector(".webui2-composer textarea")).toBe(composer);
    expect(composer.value).toBe("Keep this chat draft");
    expect(signals.length).toBeGreaterThan(0);
    expect(signals.every((signal) => !signal.aborted)).toBe(true);
    expect(vi.mocked(client.subscribe).mock.calls.every(([value]) => value === identity)).toBe(true);
  });

  it("gates unsupported isolated settings navigation and browser deep links without issuing API requests", async () => {
    const prefix = "/api/runtime/settings/environments";
    const fetchMock = vi.fn(async (url: string) => {
      const data = url === prefix ? { environments: [
        { id: "current", label: "Web", database: "web_db", tenant_key: "test-tenant", user_id: "test-user", api_path: "", available: true },
        { id: "channel", label: "Channel", database: "channel_db", tenant_key: "target", user_id: "worker", api_path: "/runtime/settings/environments/channel", available: true }
      ] } : { data: [] };
      return new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    renderAt("/webui/v2/settings/general", "en");
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
    act(() => { host.querySelector<HTMLButtonElement>('button[aria-label="Settings environment"]')?.click(); });
    act(() => { host.querySelector<HTMLButtonElement>('[role="option"][data-value="channel"]')?.click(); });
    const unsupported = ["Common prompts", "Memory", "Skills", "Teams", "Worker runtime"];
    for (const button of host.querySelectorAll<HTMLButtonElement>(".settings-navigation nav button")) {
      expect(button.disabled).toBe(unsupported.includes(button.textContent!));
    }
    fetchMock.mockClear();
    for (const section of ["prompts", "memory", "skills", "teams", "provisioning"]) {
      await act(async () => {
        window.history.pushState({}, "", `/webui/v2/settings/${section}`);
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      expect(host.querySelector(".settings-content [role=alert]")?.textContent).toContain("This section is unavailable");
      expect(host.querySelector(".p2-management-panel")).toBeNull();
      expect(fetchMock).not.toHaveBeenCalled();
    }
      await act(async () => {
        window.history.pushState({}, "", "/webui/v2/settings/memory");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      act(() => { host.querySelector<HTMLButtonElement>('button[aria-label="Settings environment"]')?.click(); });
      act(() => { host.querySelector<HTMLButtonElement>('[role="option"][data-value="current"]')?.click(); });
    expect(host.querySelector(".p2-management-panel")).not.toBeNull();
    expect(fetchMock.mock.calls.some(([url]) => url === "/api/tenant/memories?limit=20")).toBe(true);
    expect(fetchMock.mock.calls.some(([url]) => url.startsWith(`${prefix}/channel/`))).toBe(false);
  });

  function setDesktopOrigin(origin: string, pathname = "/webui/v2/settings/general") {
    window.history.replaceState({}, "", pathname);
    const location = new URL(`${origin}${pathname}`);
    vi.stubGlobal("window", new Proxy(window, {
      get(target, key) {
        if (key === "location") return location;
        const value = Reflect.get(target, key, target);
        return ["addEventListener", "removeEventListener", "dispatchEvent"].includes(String(key)) ? value.bind(target) : value;
      }
    }));
  }

  it.each(["wails://wails", "http://wails.localhost"])("renders the desktop Computer Use workspace in the WebView without native overlay calls on %s", async (origin) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    setDesktopOrigin(origin, "/webui/v2");
    storage.set("niuma.computer-workspace.v1", JSON.stringify({ displayMode: "expanded", collapsed: false, position: null }));
    storage.set("niuma-webui.language.v1", "en");
    const overlayProbe = vi.fn().mockResolvedValue(true);
    const showOverlay = vi.fn().mockResolvedValue(undefined);
    const updateOverlay = vi.fn().mockResolvedValue(undefined);
    const bridge = {
      RestartLocalService: vi.fn().mockResolvedValue(undefined),
      GetComputerCapabilities: vi.fn().mockResolvedValue({ available: true, capabilities }),
      StartComputerSession: vi.fn().mockResolvedValue(snapshot()),
      GetComputerSession: vi.fn().mockResolvedValue(snapshot()),
      ObserveComputerSession: vi.fn().mockResolvedValue(observationResponse),
      PauseComputerSession: vi.fn().mockResolvedValue(snapshot("paused")),
      ResumeComputerSession: vi.fn().mockResolvedValue(snapshot()),
      StopComputerSession: vi.fn().mockResolvedValue(snapshot("stopped")),
      GetComputerActionReceipt: vi.fn(),
      IsComputerOverlayAvailable: overlayProbe,
      ShowComputerOverlay: showOverlay,
      UpdateComputerOverlay: updateOverlay,
    };
    Object.defineProperty(window, "go", { configurable: true, value: { main: { app: bridge } } });
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { headers: { "Content-Type": "application/json" } })));
    const client = createMockSessionControlClient();
    client.list = vi.fn(async () => []);

    await act(async () => root.render(
      <I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={{ ...identity, apiBase: "", apiToken: "desktop-process" }} /></QueryClientProvider></I18nProvider>
    ));
    await vi.waitFor(() => expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull());

    expect(overlayProbe).not.toHaveBeenCalled();
    expect(showOverlay).not.toHaveBeenCalled();
    expect(updateOverlay).not.toHaveBeenCalled();

    act(() => document.body.querySelector<HTMLButtonElement>('button[aria-label="Collapse Computer Use workspace"]')?.click());
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).toBeNull();
    act(() => document.body.querySelector<HTMLButtonElement>('button[aria-label="Open Computer Use workspace"]')?.click());
    expect(document.body.querySelector('[aria-label="Computer workspace"]')).not.toBeNull();
  });

  it.each(["wails://wails", "http://wails.localhost"])("waits for the current token's readiness in desktop-v2 on %s", async (origin) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    setDesktopOrigin(origin);
    const fetchMock = vi.fn(async () => new Response("{}", { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createMockSessionControlClient();
    client.list = vi.fn(async () => []);
    const queryClient = new QueryClient();
    const renderToken = async (apiToken: string) => act(async () => root.render(
      <I18nProvider><QueryClientProvider client={queryClient}><WebUIV2App client={client} identity={{ ...identity, apiBase: "", apiToken }} /></QueryClientProvider></I18nProvider>
    ));
    await renderToken("");
    expect(host.querySelector(".webui2-desktop-readiness")).not.toBeNull();
    expect(host.querySelector(".webui2-settings-center")).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(client.list).not.toHaveBeenCalled();
    await renderToken("first-process");
    expect(host.querySelector(".webui2-desktop-readiness")).toBeNull();
    let finishHealth!: (response: Response) => void;
    fetchMock.mockImplementationOnce(() => new Promise((resolve) => { finishHealth = resolve; }));
    await renderToken("restarted-process");
    expect(host.querySelector(".webui2-desktop-readiness")).not.toBeNull();
    expect(host.querySelector(".webui2-settings-center")).toBeNull();
    await act(async () => finishHealth(new Response("{}")));
    expect(host.querySelector(".webui2-desktop-readiness")).toBeNull();
    const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit]>;
    for (const token of ["first-process", "restarted-process"]) {
      for (const path of ["/health", "/readyz"]) {
        expect(calls.some(([url, init]) => url === path && new Headers(init.headers).get("Authorization") === `Bearer ${token}`)).toBe(true);
      }
    }
  });

  it.each(["wails://wails", "http://wails.localhost"])("keeps the desktop shell visible and can restart the local service on %s", async (origin) => {
    vi.useFakeTimers();
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    storage.set("niuma.desktop.onboarding.v1", "done");
    setDesktopOrigin(origin, "/webui/v2");
    let serviceReady = false;
    const fetchMock = vi.fn(async () => {
      if (!serviceReady) {
        throw new DOMException("local service timeout", "AbortError");
      }
      return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
    });
    const restartLocalService = vi.fn(async () => {
      serviceReady = true;
    });
    Object.defineProperty(window, "go", {
      configurable: true,
      value: { main: { app: { RestartLocalService: restartLocalService } } }
    });
    vi.stubGlobal("fetch", fetchMock);
    const client = createMockSessionControlClient();
    client.list = vi.fn(async () => []);

    await act(async () => root.render(
      <I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={{ ...identity, apiBase: "", apiToken: "desktop-process" }} /></QueryClientProvider></I18nProvider>
    ));
    expect(host.querySelector(".webui2-desktop-readiness")?.getAttribute("data-state")).toBe("starting");
    expect(host.querySelector(".webui2-chat-shell")).not.toBeNull();
    expect(host.querySelector(".webui2-sidebar-avatar img")?.getAttribute("src")).toBe(niumaMark);
    expect(host.querySelector(".webui2-desktop-readiness")?.textContent).toContain("Preparing your workspace");
    expect(host.querySelector(".webui2-desktop-readiness button")).toBeNull();
    expect(host.querySelector<HTMLButtonElement>(".webui2-new-session")?.disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>(".webui2-empty-state button")?.disabled).toBe(true);
    expect(client.list).not.toHaveBeenCalled();

    await act(async () => { await vi.advanceTimersByTimeAsync(DESKTOP_READINESS.graceMs + DESKTOP_READINESS.pollMs); });
    expect(host.querySelector(".webui2-desktop-readiness")?.getAttribute("data-state")).toBe("failed");
    expect(host.querySelectorAll(".webui2-desktop-readiness button")).toHaveLength(1);
    const restartButton = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Prepare again"));
    expect(restartButton).toBeDefined();
    await act(async () => restartButton?.click());
    expect(restartLocalService).toHaveBeenCalledTimes(1);
    expect(host.querySelector(".webui2-desktop-readiness")).toBeNull();
    expect(host.querySelector<HTMLButtonElement>(".webui2-new-session")?.disabled).toBe(false);
    expect(client.list).toHaveBeenCalled();
  });

  it.each(["wails://wails", "http://wails.localhost"])("opens the new-session dialog before model verification in desktop-v2 on %s", async (origin) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    setDesktopOrigin(origin, "/");
    window.localStorage.setItem("niuma.desktop.onboarding.v1", "done");
    const fetchMock = vi.fn(async () => new Response("{}", { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createMockSessionControlClient();
    client.list = vi.fn(async () => []);
    await act(async () => root.render(
      <I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={{ ...identity, apiBase: "", apiToken: "desktop-process" }} /></QueryClientProvider></I18nProvider>
    ));
    await vi.waitFor(() => expect(host.querySelector(".webui2-desktop-readiness")).toBeNull());

    act(() => host.querySelector<HTMLButtonElement>(".webui2-new-session")?.click());

    expect(host.querySelector(".webui2-new-session-dialog")).not.toBeNull();
    expect(host.querySelector(".webui2-settings-center")).toBeNull();
  });

  it.each(["wails://wails", "http://wails.localhost"])("does not impose desktop-v2 readiness on legacy desktop at %s", async (origin) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "");
    setDesktopOrigin(origin);
    const fetchMock = vi.fn(async (_url: string) => new Response("{}", { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createMockSessionControlClient();
    client.list = vi.fn(async () => []);
    const legacyIdentity = { ...identity, apiBase: "", apiToken: "test-token" };
    await act(async () => root.render(
      <I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={legacyIdentity} /></QueryClientProvider></I18nProvider>
    ));
    expect(host.querySelector(".webui2-desktop-readiness")).toBeNull();
    expect(host.querySelector(".webui2-settings-center")).not.toBeNull();
    expect(client.list).toHaveBeenCalledWith(legacyIdentity, expect.anything(), expect.anything());
    expect(fetchMock.mock.calls.some(([url]) => url === "/health" || url === "/readyz")).toBe(false);
  });

  it("renders the route shell in Chinese from the saved language", () => {
    renderAt("/webui/v2", "zh");

    expect(host.querySelector('main[aria-label="会话工作区"]')).not.toBeNull();
    expect(host.querySelector('button[aria-label="搜索会话"]')).not.toBeNull();
    expect(host.querySelector('input[aria-label="搜索会话"]')).toBeNull();
    expect(host.textContent).toContain("新建会话");
    expect(host.textContent).not.toContain("webui2.");
  });

  it("refreshes runtime defaults and catalogs after model settings are saved", () => {
    const invalidateQueries = vi.spyOn(QueryClient.prototype, "invalidateQueries");
    renderAt("/webui/v2");

    act(() => window.dispatchEvent(new Event(GLOBAL_SETTINGS_SAVED_EVENT)));

    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ["webui2-server-status"] });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ["webui2-providers"] });
    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: ["webui2-models"] });
  });

  it("creates one session from a focused dialog, selects its readback, and leaves legacy DOM absent", async () => {
    vi.spyOn(api, "getStatus").mockResolvedValue({ workspace: "/work/project" });
    vi.spyOn(api, "validateAgentWorkspace").mockResolvedValue({ cwd: "/work/project", workspace_name: "project", exists: true, is_dir: true, is_git_repo: false });
    const created: SessionDetail = { ref: "tenant:created", source: "tenant", title: "Launch plan", status: "idle", updatedAt: "2026-09-05T01:00:00.000Z", shortID: "created", messages: [{ id: "operation-create", role: "assistant", kind: "operation", content: "", createdAt: "2026-09-05T01:00:00.000Z", operation: { id: "operation-create", kind: "create", status: "completed", title: "", detail: "", createdAt: "2026-09-05T01:00:00.000Z" } }], activity: [], context: [], changes: [], runs: [] };
    const client: SessionControlClient = {
      list: vi.fn(async () => [created]), get: vi.fn(async () => created),
      create: vi.fn(async () => ({ operation: created.messages[0].operation!, session: created, replayed: false })),
      send: vi.fn(), stop: vi.fn(), archive: vi.fn()
    };
    renderAt("/webui/v2", undefined, client);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const newSession = host.querySelector<HTMLButtonElement>(".webui2-new-session");
    expect(newSession).not.toBeNull();
    act(() => newSession?.click());
    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    await vi.waitFor(() => expect(host.querySelector(".webui2-workspace-picker-location")?.textContent).toBe("/work/project"));
    const title = host.querySelector<HTMLInputElement>('[role="dialog"] input');
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    act(() => { setter?.call(title, "Launch plan"); title?.dispatchEvent(new Event("input", { bubbles: true })); });
    await act(async () => host.querySelector<HTMLButtonElement>('button[type="submit"]')?.click());
    await vi.waitFor(() => expect(client.create).toHaveBeenCalledTimes(1));
    await act(async () => { await Promise.resolve(); await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve())); });

    expect(client.create).toHaveBeenCalledTimes(1);
    expect(window.location.pathname).toBe("/webui/v2/sessions/tenant%3Acreated");
    expect(host.querySelector('main[aria-label="Session workspace"]')?.getAttribute("data-session-ref")).toBe("tenant:created");
    expect(host.querySelectorAll(".webui2-session-row")).toHaveLength(1);
    expect(host.querySelector(".webui2-operation-card")?.textContent).toContain("Create session");
    expect(document.activeElement).toBe(host.querySelector(".webui2-composer textarea"));
    expect(host.querySelector(".dashboard-shell")).toBeNull();
    expect(host.querySelector('[aria-label="Web Agent workspaces and sessions"]')).toBeNull();
  });

  it("creates directly from a workspace shortcut without opening the workspace dialog", async () => {
    vi.spyOn(api, "validateAgentWorkspace").mockResolvedValue({ cwd: "/work/project", workspace_name: "project", exists: true, is_dir: true, is_git_repo: false });
    const created: SessionDetail = {
      ref: "tenant:shortcut-created",
      source: "tenant",
      title: "New session",
      status: "idle",
      updatedAt: "2026-09-05T02:00:00.000Z",
      shortID: "shortcut-created",
      cwd: "/work/project",
      messages: [],
      activity: [],
      context: [],
      changes: [],
      runs: []
    };
    const client: SessionControlClient = {
      list: vi.fn(async () => [created]),
      get: vi.fn(async () => created),
      create: vi.fn(async () => ({
        operation: { id: "operation-shortcut-create", kind: "create" as const, status: "completed" as const, title: "", detail: "", createdAt: created.updatedAt },
        session: created,
        replayed: false
      })),
      send: vi.fn(),
      stop: vi.fn(),
      archive: vi.fn()
    };

    renderAt("/webui/v2", undefined, client);
    await vi.waitFor(() => expect(host.querySelector('[aria-label="New session in project"]')).not.toBeNull());

    act(() => host.querySelector<HTMLButtonElement>('[aria-label="New session in project"]')?.click());

    expect(host.querySelector(".webui2-new-session-dialog")).toBeNull();
    await vi.waitFor(() => expect(client.create).toHaveBeenCalledTimes(1));
    expect(client.create).toHaveBeenCalledWith(identity, expect.objectContaining({
      title: "New session",
      cwd: "/work/project",
      promptMode: "code",
      idempotencyKey: expect.any(String)
    }));
    await vi.waitFor(() => expect(window.location.pathname).toBe("/webui/v2/sessions/tenant%3Ashortcut-created"));
    expect(host.querySelector('main[aria-label="Session workspace"]')?.getAttribute("data-session-ref")).toBe("tenant:shortcut-created");
  });

  it("keeps created v2 state and transient controls isolated from legacy application DOM", async () => {
    vi.spyOn(api, "getStatus").mockResolvedValue({ workspace: "/work/project" });
    vi.spyOn(api, "validateAgentWorkspace").mockResolvedValue({ cwd: "/work/project", workspace_name: "project", exists: true, is_dir: true, is_git_repo: false });
    renderAt("/webui/v2", undefined, createMockSessionControlClient());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    act(() => host.querySelector<HTMLButtonElement>(".webui2-new-session")?.click());
    await vi.waitFor(() => expect(host.querySelector(".webui2-workspace-picker-location")?.textContent).toBe("/work/project"));
    const title = host.querySelector<HTMLInputElement>('[role="dialog"] input');
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    act(() => { setter?.call(title, "Isolated session"); title?.dispatchEvent(new Event("input", { bubbles: true })); });
    await act(async () => host.querySelector<HTMLButtonElement>('button[type="submit"]')?.click());
    await vi.waitFor(() => expect(host.querySelector("main.webui2-page")?.getAttribute("data-session-ref") ?? "").toMatch(/^tenant:session-/));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Account menu"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="System settings"]')?.click());
    expect(host.querySelector(".webui2-settings-center")).not.toBeNull();
    const theme = host.querySelector<HTMLButtonElement>(".settings-theme-options button:last-child");
    act(() => theme?.click());
    act(() => { host.querySelector<HTMLButtonElement>('button[aria-label="Language"]')?.click(); });
    act(() => { host.querySelector<HTMLButtonElement>('[role="option"][data-value="zh"]')?.click(); });
    act(() => host.querySelector<HTMLInputElement>(".settings-preference-row input")?.click());

    expect(host.querySelector("main.webui2-page")?.getAttribute("data-session-ref") ?? "").toMatch(/^tenant:session-/);
    expect(host.querySelector("main.webui2-page")?.getAttribute("data-inspector-open")).toBe("true");
    expect(host.querySelector(".webui2-settings-drawer")).toBeNull();
    expect(host.querySelector(".dashboard-shell")).toBeNull();
    expect(host.querySelector('[aria-label="Web Agent workspaces and sessions"]')).toBeNull();
    expect(storage.get("niuma-webui.v2.theme.v1")).toBe("dark");
    expect(storage.get("niuma-webui.language.v1")).toBe("zh");
    expect(storage.get("niuma-webui.v2.inspector-default")).toBe("true");
  });

  it.each([
    ["/webui/v2", 'main[aria-label="Session workspace"]'],
    ["/webui/v2/sessions/tenant%3Aseed-1", 'main[aria-label="Session workspace"]'],
    ["/webui/", ".dashboard-shell"],
    ["/webui/agent", '[aria-label="Web Agent workspaces and sessions"]']
  ])("keeps the expected application branch for %s", (pathname, selector) => {
    window.history.replaceState({}, "", pathname);
    act(() => {
      root.render(
        <I18nProvider>
          <App />
        </I18nProvider>
      );
    });

    expect(host.querySelector(selector)).not.toBeNull();
  });
});
