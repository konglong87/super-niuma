import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { createMockSessionControlClient } from "../api/mockSessionControlClient";
import type { SessionControlClient } from "../api/sessionControlClient";
import type { SessionDetail } from "../types";
import { WebUIV2App } from "../WebUIV2App";
import { Inspector, type InspectorTab } from "./Inspector";

const detail: SessionDetail = {
  ref: "tenant:alpha",
  source: "tenant",
  title: "Release coordination",
  status: "waiting_permission",
  updatedAt: "2026-09-05T00:00:00.000Z",
  shortID: "alpha",
  messages: [],
  activity: ["Tool requested approval"],
  context: [{ sourceRef: "tenant:beta", title: "Design review", status: "ready" }],
  changes: ["web/src/v2/components/Inspector.tsx"],
  runs: [{ id: "run-alpha", status: "waiting_permission", startedAt: "2026-09-05T00:00:00.000Z" }]
};

const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
const webUIV2Styles = readFileSync(resolve(process.cwd(), "src/v2/styles.css"), "utf8");

function setViewport(mobile: boolean): void {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockImplementation(() => ({
      matches: mobile,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn()
    }))
  });
}

describe("Inspector", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;
  let style: HTMLStyleElement;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map<string, string>();
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
    host.className = "webui2-page";
    document.body.replaceChildren(host);
    style = document.createElement("style");
    style.textContent = webUIV2Styles;
    document.head.append(style);
    root = createRoot(host);
    setViewport(false);
  });

  afterEach(() => {
    act(() => root.unmount());
    style.remove();
    vi.restoreAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("renders four data tabs and keeps a permission request out of the tab list", () => {
    act(() => root.render(<I18nProvider><Inspector detail={detail} onClose={vi.fn()} onTabChange={vi.fn()} open tab="activity" /></I18nProvider>));

    expect(host.querySelector("aside.webui2-inspector")).not.toBeNull();
    expect(Array.from(host.querySelectorAll('[role="tab"]')).map((tab) => tab.textContent)).toEqual(["Activity", "Context", "Changes", "Runs"]);
    expect(host.textContent).toContain("Tool requested approval");
    expect(host.textContent).toContain("Permission request pending");

    const tabs: Array<[InspectorTab, string]> = [["context", "Design review"], ["changes", "Inspector.tsx"], ["runs", "run-alpha"]];
    for (const [tab, content] of tabs) {
      act(() => root.render(<I18nProvider><Inspector detail={detail} onClose={vi.fn()} onTabChange={vi.fn()} open tab={tab} /></I18nProvider>));
      expect(host.textContent).toContain(content);
    }
  });

  it("docks on desktop while keeping the conversation mounted and visible", () => {
    host.dataset.inspectorOpen = "true";
    act(() => root.render(<I18nProvider><div className="webui2-content"><section className="webui2-workspace" data-testid="conversation">Conversation remains visible</section><Inspector detail={detail} onClose={vi.fn()} open tab="activity" /></div></I18nProvider>));

    const content = host.querySelector<HTMLElement>(".webui2-content");
    const conversation = host.querySelector<HTMLElement>('[data-testid="conversation"]');
    const inspector = host.querySelector<HTMLElement>("aside.webui2-inspector");
    expect(getComputedStyle(content!).gridTemplateColumns).toBe("minmax(0, 1fr) minmax(260px, 340px)");
    expect(getComputedStyle(conversation!).display).not.toBe("none");
    expect(getComputedStyle(inspector!).position).not.toBe("fixed");
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(host.querySelector(".webui2-inspector-backdrop")).toBeNull();
  });

  it("keeps the panel reserved while the selected session detail is loading", () => {
    act(() => root.render(<I18nProvider><Inspector detail={undefined} onClose={vi.fn()} open tab="runs" /></I18nProvider>));

    expect(host.querySelector("aside.webui2-inspector")).not.toBeNull();
    expect(host.querySelector(".webui2-inspector")?.getAttribute("aria-busy")).toBe("true");
    expect(host.querySelector(".webui2-inspector-loading")?.textContent).toContain("Loading Inspector");

    setViewport(true);
    act(() => root.render(<I18nProvider key="mobile"><Inspector detail={undefined} onClose={vi.fn()} open tab="runs" /></I18nProvider>));
    expect(host.querySelector('[role="dialog"][aria-modal="true"]')).not.toBeNull();
    expect(host.querySelector(".webui2-inspector-backdrop")).not.toBeNull();
  });

  it("uses a focus-managed mobile dialog that closes from Escape and backdrop", () => {
    setViewport(true);
    function Harness() {
      const [open, setOpen] = useState(true);
      return <><button type="button">Launcher</button><I18nProvider><Inspector detail={detail} onClose={() => setOpen(false)} onTabChange={vi.fn()} open={open} tab="activity" /></I18nProvider></>;
    }
    act(() => root.render(<Harness />));

    const dialog = host.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');
    const close = host.querySelector<HTMLButtonElement>(".webui2-inspector header button");
    expect(dialog).not.toBeNull();
    expect(document.activeElement).toBe(close);
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector('[role="dialog"]')).toBeNull();

    act(() => root.render(<Harness key="reopened" />));
    act(() => host.querySelector<HTMLButtonElement>(".webui2-inspector-backdrop")?.click());
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it("preserves the selected managed session when the mobile Inspector closes", async () => {
    setViewport(true);
    window.history.replaceState({}, "", "/webui/v2/sessions/tenant%3Aalpha");
    const client = createMockSessionControlClient();
    act(() => root.render(<I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={identity} /></QueryClientProvider></I18nProvider>));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    const selectedSession = () => host.querySelector("main.webui2-page")?.getAttribute("data-session-ref");

    expect(selectedSession()).toBe("tenant:alpha");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Open Inspector"]')?.click());
    const dialog = host.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');
    expect(dialog).not.toBeNull();
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(selectedSession()).toBe("tenant:alpha");

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Open Inspector"]')?.click());
    expect(host.querySelector('[role="dialog"][aria-modal="true"]')).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>(".webui2-inspector-backdrop")?.click());
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(selectedSession()).toBe("tenant:alpha");
  });

  it("keeps the Changes disclosure inside the mobile focus loop", () => {
    setViewport(true);
    act(() => root.render(<I18nProvider><Inspector detail={detail} onClose={vi.fn()} open tab="changes" /></I18nProvider>));
    const dialog = host.querySelector<HTMLElement>('[role="dialog"]');
    const summary = host.querySelector<HTMLElement>(".webui2-inspector-changes summary");
    const close = host.querySelector<HTMLButtonElement>(".webui2-inspector header button");

    act(() => summary?.focus());
    expect(document.activeElement).toBe(summary);
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Tab" })));
    expect(document.activeElement).toBe(close);
  });

  it.each<InspectorTab>(["activity", "context", "runs"])("keeps the %s tab inside the mobile focus loop without trailing disclosures", (tab) => {
    setViewport(true);
    const detailWithoutChanges = { ...detail, changes: [] };
    act(() => root.render(<I18nProvider><Inspector detail={detailWithoutChanges} onClose={vi.fn()} open tab={tab} /></I18nProvider>));
    const dialog = host.querySelector<HTMLElement>('[role="dialog"]');
    const selectedTab = host.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]');
    const close = host.querySelector<HTMLButtonElement>(".webui2-inspector header button");

    act(() => selectedTab?.focus());
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Tab" })));
    expect(document.activeElement).toBe(close);

    act(() => close?.focus());
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Tab", shiftKey: true })));
    expect(document.activeElement).toBe(selectedTab);
  });

  it("uses arrow keys to navigate uncontrolled and controlled tabs", () => {
    const onTabChange = vi.fn();
    act(() => root.render(<I18nProvider><Inspector detail={detail} initialTab="activity" onClose={vi.fn()} onTabChange={onTabChange} open /></I18nProvider>));
    const activity = host.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]');
    act(() => activity?.focus());
    act(() => activity?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowRight" })));
    expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe("Context");
    expect(document.activeElement?.textContent).toBe("Context");
    expect(onTabChange).toHaveBeenCalledWith("context");

    function ControlledHarness() {
      const [tab, setTab] = useState<InspectorTab>("runs");
      return <I18nProvider><Inspector detail={detail} onClose={vi.fn()} onTabChange={setTab} open tab={tab} /></I18nProvider>;
    }
    act(() => root.render(<ControlledHarness key="controlled" />));
    const runs = host.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]');
    act(() => runs?.focus());
    act(() => runs?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowRight" })));
    expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe("Activity");
    expect(document.activeElement?.textContent).toBe("Activity");
    const controlledActivity = host.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]');
    act(() => controlledActivity?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowLeft" })));
    expect(host.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe("Runs");
  });

  it("localizes Context and Runs statuses", () => {
    storage.set("niuma-webui.language.v1", "zh");
    act(() => root.render(<I18nProvider><Inspector detail={detail} onClose={vi.fn()} open tab="context" /></I18nProvider>));
    expect(host.querySelector(".webui2-inspector-content")?.textContent).toContain("就绪");
    expect(host.querySelector(".webui2-inspector-content")?.textContent).not.toContain("ready");

    act(() => root.render(<I18nProvider><Inspector detail={detail} onClose={vi.fn()} open tab="runs" /></I18nProvider>));
    expect(host.querySelector(".webui2-inspector-content")?.textContent).toContain("等待授权");
    expect(host.querySelector(".webui2-inspector-content")?.textContent).not.toContain("waiting_permission");
  });

  it("defaults closed until configured and retains its tab across sessions and its preference on remount", async () => {
    window.localStorage.setItem("niuma-webui.v2.inspector", "true");
    window.history.replaceState({}, "", "/webui/v2/sessions/tenant%3Aalpha");
    const client = createMockSessionControlClient();
    const renderApp = (key: string) => root.render(<I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={identity} key={key} /></QueryClientProvider></I18nProvider>);

    act(() => renderApp("first"));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(host.querySelector(".webui2-inspector")).toBeNull();

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Account menu"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="System settings"]')?.click());
    act(() => host.querySelector<HTMLInputElement>('input[aria-label="Inspector"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>(".settings-back")?.click());
    expect(host.querySelector("aside.webui2-inspector")).not.toBeNull();
    act(() => host.querySelector<HTMLButtonElement>('[role="tab"]:last-child')?.click());
    expect(host.querySelector('[role="tab"]:last-child')?.getAttribute("aria-selected")).toBe("true");

    const beta = Array.from(host.querySelectorAll<HTMLButtonElement>(".webui2-session-select")).find((button) => button.textContent?.includes("Design review"));
    act(() => beta?.click());
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(host.querySelector("aside.webui2-inspector")).not.toBeNull();
    expect(host.querySelector('[role="tab"]:last-child')?.getAttribute("aria-selected")).toBe("true");

    act(() => renderApp("second"));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    expect(host.querySelector(".webui2-inspector")).not.toBeNull();
  });

  it("keeps the Inspector mounted on its selected tab while the next session loads", async () => {
    const alpha: SessionDetail = { ...detail, status: "running", runs: [{ id: "run-alpha", status: "running", startedAt: detail.updatedAt }] };
    const beta: SessionDetail = { ...detail, ref: "tenant:beta", shortID: "beta", title: "Design review", status: "completed", runs: [{ id: "run-beta", status: "completed", startedAt: detail.updatedAt, endedAt: detail.updatedAt }] };
    let resolveBeta: (value: SessionDetail) => void = () => undefined;
    const betaDetail = new Promise<SessionDetail>((resolve) => { resolveBeta = resolve; });
    const client: SessionControlClient = {
      list: vi.fn(async () => [alpha, beta]),
      get: vi.fn(async (_identity, ref) => ref === "tenant:beta" ? betaDetail : alpha),
      create: vi.fn(), send: vi.fn(), stop: vi.fn(), archive: vi.fn()
    };
    window.history.replaceState({}, "", "/webui/v2/sessions/tenant%3Aalpha");
    act(() => root.render(<I18nProvider><QueryClientProvider client={new QueryClient()}><WebUIV2App client={client} identity={identity} /></QueryClientProvider></I18nProvider>));
    await act(async () => { await Promise.resolve(); await Promise.resolve(); });
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Account menu"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="menuitem"][aria-label="System settings"]')?.click());
    act(() => host.querySelector<HTMLInputElement>('input[aria-label="Inspector"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="tab"]:last-child')?.click());

    const betaButton = Array.from(host.querySelectorAll<HTMLButtonElement>(".webui2-session-select")).find((button) => button.textContent?.includes("Design review"));
    act(() => betaButton?.click());
    await act(async () => { await Promise.resolve(); });
    expect(host.querySelector("aside.webui2-inspector")).not.toBeNull();
    expect(host.querySelector('[role="tab"]:last-child')?.getAttribute("aria-selected")).toBe("true");
    expect(host.querySelector(".webui2-inspector-loading")).not.toBeNull();

    await act(async () => { resolveBeta(beta); await betaDetail; await new Promise((resolve) => setTimeout(resolve, 0)); });
    await vi.waitFor(() => expect(host.querySelector(".webui2-inspector-content")?.textContent).toContain("run-beta"));
    expect(host.querySelector('[role="tab"]:last-child')?.getAttribute("aria-selected")).toBe("true");
  });
});
