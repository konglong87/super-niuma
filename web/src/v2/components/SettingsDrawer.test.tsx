import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { SessionSummary } from "../types";
import { SettingsDrawer } from "./SettingsDrawer";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
const managed: SessionSummary = { ref: "tenant:alpha", source: "tenant", title: "Release", status: "running", updatedAt: "2026-09-05T00:00:00.000Z", shortID: "alpha", profileLabel: "Release manager" };

describe("SettingsDrawer", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key), clear: () => storage.clear() } });
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.restoreAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("acts as a modal dialog, traps focus, closes on Escape, and restores launcher focus", () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return <><button onClick={() => setOpen(true)} type="button">Launcher</button><I18nProvider><SettingsDrawer open={open} identity={identity} selectedSession={managed} onClose={() => setOpen(false)} onInspectorChange={vi.fn()} onStop={vi.fn()} onArchive={vi.fn()} /></I18nProvider></>;
    }
    act(() => root.render(<Harness />));
    const launcher = host.querySelector<HTMLButtonElement>("button");
    act(() => launcher?.focus());
    act(() => launcher?.click());
    const dialog = host.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');
    const close = host.querySelector<HTMLButtonElement>('.webui2-settings-drawer button[aria-label="Close settings"]');
    expect(dialog).not.toBeNull();
    expect(document.activeElement).toBe(close);

    const focusables = Array.from(dialog?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), a[href]') ?? []);
    const last = focusables.at(-1);
    act(() => last?.focus());
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true })));
    expect(document.activeElement).toBe(close);
    act(() => dialog?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(launcher);
  });

  it("keeps local session mutations unavailable", () => {
    const local: SessionSummary = { ...managed, ref: "local:alpha", source: "local" };
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={local} onClose={vi.fn()} onInspectorChange={vi.fn()} onStop={vi.fn()} onArchive={vi.fn()} /></I18nProvider>));

    expect(host.textContent).toContain("immutable");
    expect(host.querySelector('button[aria-label="Stop session"]')).toBeNull();
    expect(host.querySelector('button[aria-label="Archive session"]')).toBeNull();
  });

  it("hides stop after a managed session reaches a terminal state", () => {
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={{ ...managed, status: "completed" }} onClose={vi.fn()} onInspectorChange={vi.fn()} onStop={vi.fn()} onArchive={vi.fn()} /></I18nProvider>));

    expect(host.querySelector('button[aria-label="Stop session"]')).toBeNull();
    expect(host.querySelector('button[aria-label="Archive session"]')).not.toBeNull();
  });

  it("does not mutate when a stop or archive confirmation is cancelled", () => {
    const stop = vi.fn();
    const archive = vi.fn();
    vi.spyOn(window, "confirm").mockReturnValue(false);
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={managed} onArchive={archive} onClose={vi.fn()} onInspectorChange={vi.fn()} onStop={stop} /></I18nProvider>));

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Stop session"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Archive session"]')?.click());

    expect(stop).not.toHaveBeenCalled();
    expect(archive).not.toHaveBeenCalled();
  });

  it("reports Inspector state without persisting it", () => {
    const onInspectorChange = vi.fn();
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={managed} onClose={vi.fn()} onInspectorChange={onInspectorChange} onStop={vi.fn()} onArchive={vi.fn()} /></I18nProvider>));
    const inspector = host.querySelector<HTMLInputElement>('input[aria-label="Inspector"]');
    act(() => inspector?.click());

    expect(onInspectorChange).toHaveBeenCalledWith(true);
    expect([...storage.keys()].some((key) => key.toLowerCase().includes("inspector"))).toBe(false);
  });

  it("offers current-session, workspace, UI, and auth controls", () => {
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={managed} onClose={vi.fn()} onInspectorChange={vi.fn()} onStop={vi.fn()} onArchive={vi.fn()} /></I18nProvider>));
    expect(host.textContent).toContain("tenant:alpha");
    expect(host.textContent).toContain("Release manager");
    expect(host.textContent).toContain("Agents");
    expect(host.textContent).toContain("Observability");
    expect(host.querySelector('select[aria-label="Language"]')).not.toBeNull();
    expect(host.querySelector('select[aria-label="Theme"]')).not.toBeNull();
    expect(host.querySelector('input[aria-label="Inspector"]')).not.toBeNull();
  });

  it("renders the complete interface labels in Chinese", () => {
    storage.set("niuma-webui.language.v1", "zh");
    const withoutProfile = { ...managed, profileLabel: undefined };
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={withoutProfile} onClose={vi.fn()} onInspectorChange={vi.fn()} onStop={vi.fn()} onArchive={vi.fn()} /></I18nProvider>));

    expect(host.textContent).toContain("智能体配置");
    expect(host.textContent).toContain("未分配智能体配置");
    expect(host.textContent).toContain("英文");
    expect(host.textContent).toContain("中文");
    expect(host.textContent).not.toContain("Profile");
  });

  it("copies the ref, persists only the v2 theme, and confirms each danger action", async () => {
    const stop = vi.fn();
    const archive = vi.fn();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    act(() => root.render(<I18nProvider><SettingsDrawer open identity={identity} selectedSession={managed} onClose={vi.fn()} onInspectorChange={vi.fn()} onStop={stop} onArchive={archive} /></I18nProvider>));

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Copy session ref"]')?.click());
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("tenant:alpha");

    const theme = host.querySelector<HTMLSelectElement>('select[aria-label="Theme"]');
    act(() => {
      theme!.value = "dark";
      theme?.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(storage.get("niuma-webui.v2.theme.v1")).toBe("dark");

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Stop session"]')?.click());
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Archive session"]')?.click());
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(stop).toHaveBeenCalledWith("tenant:alpha");
    expect(archive).toHaveBeenCalledWith("tenant:alpha");
  });
});
