import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { createMockSessionControlClient } from "../api/mockSessionControlClient";
import { WebUIV2App } from "../WebUIV2App";

const identity: IdentityConfig = { apiBase: "", apiToken: "desktop-test", mobileJwt: "", tenantKey: "test", userId: "test", deviceId: "test", model: "test" };
describe("Desktop single-entry navigation", () => {
  let host: HTMLDivElement; let root: Root;
  const list = vi.fn().mockResolvedValue([]);
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const data = new Map([["niuma-webui.language.v1", "zh"]]);
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => data.set(key, value), removeItem: (key: string) => data.delete(key) } });
    host = document.createElement("div"); document.body.replaceChildren(host); root = createRoot(host);
    list.mockClear();
    window.go = { main: { app: { RestartLocalService: vi.fn(), ListSuperWorkOrders: list, CreateSuperWorkOrder: vi.fn(), GetSuperWorkOrder: vi.fn(), CancelSuperWorkOrder: vi.fn() } } };
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { headers: { "Content-Type": "application/json" } })));
  });
  afterEach(() => { act(() => root.unmount()); delete window.go; vi.unstubAllGlobals(); vi.unstubAllEnvs(); vi.restoreAllMocks(); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT; });
  async function render(desktop = true) {
    window.history.replaceState({}, "", "/webui/v2/sessions/tenant%3Aalpha");
    if (desktop) {
      vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
      const location = new URL("wails://wails/webui/v2/sessions/tenant%3Aalpha");
      vi.stubGlobal("window", new Proxy(window, { get(target, key) { if (key === "location") return location; const value = Reflect.get(target, key, target); return ["addEventListener", "removeEventListener", "dispatchEvent"].includes(String(key)) ? value.bind(target) : value; } }));
    }
    const client = createMockSessionControlClient(); const create = vi.spyOn(client, "create");
    await act(async () => root.render(<I18nProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><WebUIV2App client={client} identity={identity} /></QueryClientProvider></I18nProvider>));
    await vi.waitFor(() => expect(host.querySelector(".webui2-desktop-readiness")).toBeNull());
    return { client, create };
  }
  it("adds Super next to New Session; switching neither creates a session nor opens a browser", async () => {
    const { create } = await render(); const open = vi.spyOn(window, "open");
    expect(list).not.toHaveBeenCalled();
    const entry = host.querySelector<HTMLButtonElement>(".webui2-super-entry")!;
    expect(entry.textContent).toBe("超级牛马干活"); expect(entry.previousElementSibling?.textContent).toBe("新建会话");
    const conversation = host.querySelector(".webui2-workspace");
    await act(async () => entry.click());
    expect(host.querySelector(".webui2-super-surface")?.hasAttribute("hidden")).toBe(false);
    expect(host.querySelector(".webui2-conversation-surfaces")?.hasAttribute("hidden")).toBe(true);
    expect(host.querySelector(".webui2-workspace")).toBe(conversation);
    expect(list).toHaveBeenCalledTimes(1); expect(create).not.toHaveBeenCalled(); expect(open).not.toHaveBeenCalled();
    act(() => host.querySelector<HTMLButtonElement>(".webui2-new-session:not(.webui2-super-entry)")!.click());
    expect(host.querySelector(".webui2-super-surface")?.hasAttribute("hidden")).toBe(true);
    expect(host.querySelector(".webui2-new-session-dialog")).not.toBeNull(); expect(create).not.toHaveBeenCalled();
  });
  it("returns to the same selected conversation and keeps a Super draft", async () => {
    await render(); const before = host.querySelector("main")?.getAttribute("data-session-ref"); expect(before).toBe("tenant:alpha");
    await act(async () => host.querySelector<HTMLButtonElement>(".webui2-super-entry")!.click());
    const field = host.querySelector<HTMLInputElement>("#super-title")!;
    act(() => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "保留草稿"); field.dispatchEvent(new Event("input", { bubbles: true })); });
    act(() => [...host.querySelectorAll<HTMLButtonElement>(".super-actions button")].find((button) => button.textContent === "返回会话")!.click());
    expect(host.querySelector("main")?.getAttribute("data-session-ref")).toBe(before);
    await act(async () => host.querySelector<HTMLButtonElement>(".webui2-super-entry")!.click());
    expect(host.querySelector<HTMLInputElement>("#super-title")?.value).toBe("保留草稿");
    expect(list).toHaveBeenCalledTimes(1);
  });
  it("existing conversation selection leaves Super without creating a session", async () => {
    const { create } = await render();
    await act(async () => host.querySelector<HTMLButtonElement>(".webui2-super-entry")!.click());
    expect(host.querySelector(".webui2-session-select[aria-pressed='true']")).toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>(".webui2-session-select")!.click());
    expect(host.querySelector(".webui2-super-surface")?.hasAttribute("hidden")).toBe(true);
    expect(host.querySelector(".webui2-conversation-surfaces")?.hasAttribute("hidden")).toBe(false);
    expect(host.querySelector("main")?.getAttribute("data-session-ref")).toMatch(/^tenant:/);
    expect(create).not.toHaveBeenCalled();
  });
  it("does not expose an unusable Desktop-only entry on the old Web UI", async () => {
    await render(false); expect(host.querySelector(".webui2-super-entry")).toBeNull(); expect(list).not.toHaveBeenCalled();
  });
});
