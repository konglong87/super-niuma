import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { createMockSessionControlClient } from "../api/mockSessionControlClient";
import { WebUIV2App } from "../WebUIV2App";
import { agentClientFixture, workflowClientFixture } from "./testFixtures";
const identity: IdentityConfig = { apiBase: "/api", apiToken: "", mobileJwt: "", tenantKey: "t", userId: "u", deviceId: "d", model: "" };
let host: HTMLDivElement; let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div"); document.body.replaceChildren(host); root = createRoot(host);
  const storage = new Map([["niuma-webui.language.v1", "en"], ["niuma-webui.v2.inspector-default", "true"]]);
  Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key) } });
  vi.stubGlobal("fetch", vi.fn(async () => new Response('{"data":[]}', { status: 200 })));
});
afterEach(() => { act(() => root.unmount()); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
const settle = async () => { await act(async () => { await new Promise((resolve) => setTimeout(resolve, 12)); }); };
const button = (name: string) => [...host.querySelectorAll("button")].find((element) => element.textContent === name)!;
describe("Workflow route shell", () => {
  it("keeps chat subscription active but makes workflow body and Inspector mutually exclusive", async () => {
    const client = createMockSessionControlClient();
    let subscriptionSignal: AbortSignal | undefined;
    client.subscribe = vi.fn((_identity, _refs, _onPage, signal) => { subscriptionSignal = signal; return new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true })); });
    window.history.replaceState({}, "", "/webui/v2/sessions/tenant%3Aalpha");
    act(() => root.render(<I18nProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><WebUIV2App identity={identity} client={client} workflowClient={workflowClientFixture()} workflowAgentClient={agentClientFixture()} /></QueryClientProvider></I18nProvider>)); await settle();
    expect(client.subscribe).toHaveBeenCalled(); const signal = subscriptionSignal;
    act(() => button("Team workflows").click()); await settle();
    expect(host.querySelector(".workflow-workbench")).not.toBeNull(); expect(host.querySelector(".webui2-conversation")).toBeNull(); expect(host.querySelector(".webui2-inspector")).toBeNull(); expect(host.querySelector("main")?.dataset.inspectorOpen).toBe("false"); expect(host.querySelector("main")?.dataset.sessionRef).toBe("tenant:alpha"); expect(signal?.aborted).toBe(false);
  });
  it("has independent dirty confirmation for workflow routing and browser back", async () => {
    window.history.replaceState({}, "", "/webui/v2/workflows/new");
    act(() => root.render(<I18nProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><WebUIV2App identity={identity} client={createMockSessionControlClient()} workflowClient={workflowClientFixture()} workflowAgentClient={agentClientFixture()} /></QueryClientProvider></I18nProvider>)); await settle();
    const input = host.querySelector<HTMLInputElement>(".workflow-heading-fields input")!;
    act(() => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Unsaved"); input.dispatchEvent(new Event("input", { bubbles: true })); });
    act(() => button("Agents").click()); await settle(); expect(host.querySelector(".workflow-leave-dialog")).not.toBeNull(); expect(window.location.pathname).toBe("/webui/v2/workflows/new");
    act(() => button("Keep editing").click()); await settle(); expect(input.value).toBe("Unsaved");
    act(() => { window.history.replaceState({}, "", "/webui/v2"); window.dispatchEvent(new PopStateEvent("popstate")); }); await settle(); expect(host.querySelector(".workflow-leave-dialog")).not.toBeNull();
    act(() => button("Discard changes").click()); await settle(); expect(window.location.pathname).toBe("/webui/v2"); expect(host.querySelector(".workflow-workbench")).toBeNull();
  });
});
