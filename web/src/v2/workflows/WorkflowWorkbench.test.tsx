import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { WorkflowRoute } from "../routes";
import { agentClientFixture, agentFixture, eventFixture, runFixture, templateFixture, workflowClientFixture } from "./testFixtures";
import type { WorkflowEvent } from "./types";
import { WorkflowRunDetail } from "./WorkflowRunDetail";
import { WorkflowWorkbench } from "./WorkflowWorkbench";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "", mobileJwt: "", tenantKey: "t", userId: "u", deviceId: "d", model: "" };
let host: HTMLDivElement; let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div"); document.body.replaceChildren(host); root = createRoot(host);
  const storage = new Map([["niuma-webui.language.v1", "en"]]);
  Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key) } });
});
afterEach(() => { act(() => root.unmount()); vi.useRealTimers(); vi.restoreAllMocks(); });
const settle = async () => { await act(async () => { await Promise.resolve(); await Promise.resolve(); }); };
const click = async (label: string) => { const button = [...host.querySelectorAll("button")].find((item) => item.textContent === label); expect(button, label).toBeDefined(); act(() => button!.click()); await settle(); };
function setValue(element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
  const prototype = element instanceof HTMLInputElement ? HTMLInputElement.prototype : element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLSelectElement.prototype;
  act(() => { Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(element, value); element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? "change" : "input", { bubbles: true })); });
}
function mount(route: WorkflowRoute) {
  const client = workflowClientFixture(); const agentClient = agentClientFixture(); const onNavigate = vi.fn(); const onDirtyChange = vi.fn();
  const render = () => act(() => root.render(<I18nProvider><WorkflowWorkbench identity={identity} route={route} client={client} agentClient={agentClient} onNavigate={onNavigate} onDirtyChange={onDirtyChange} onRequestNavigation={(action) => action()} /></I18nProvider>));
  return { client, agentClient, onNavigate, onDirtyChange, render };
}
describe("Workflow workbench", () => {
  it("creates either builtin without requiring any existing agents", async () => {
    const context = mount({ kind: "workflows", page: "list" }); vi.mocked(context.agentClient.list).mockResolvedValue([]); context.render(); await settle();
    await click("ChainPlan → execute → review"); expect(context.client.createBuiltin).toHaveBeenCalledWith("chain"); expect(context.onNavigate).toHaveBeenCalledWith("/webui/v2/workflows/edit/review"); expect(context.client.start).not.toHaveBeenCalled();
  });
  it("keeps dependencies secondary and blocks starting an unsaved edit", async () => {
    const context = mount({ kind: "workflows", page: "edit", key: "review" }); context.render(); await settle();
    expect(host.querySelector(".workflow-editor .workflow-dependency-disclosure")?.hasAttribute("open")).toBe(false);
    expect(host.querySelector(".workflow-editor .workflow-launch")).not.toBeNull();
    setValue(host.querySelector<HTMLInputElement>('input:not([type="number"])')!, "New name");
    expect(host.querySelector<HTMLButtonElement>(".workflow-launch .workflow-primary")?.disabled).toBe(true);
    expect(host.textContent).toContain("Save changes before starting a run.");
  });
  it("saves exact binding contract as a new version, without historical id/version fields", async () => {
    const context = mount({ kind: "workflows", page: "edit", key: "review" }); context.render(); await settle();
    setValue(host.querySelector<HTMLInputElement>('input:not([type="number"])')!, "New review");
    expect(context.onDirtyChange).toHaveBeenCalledWith(true);
    await act(async () => { host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); }); await settle();
    expect(context.client.save).toHaveBeenCalledWith(expect.objectContaining({ key: "review", name: "New review", agents: templateFixture().agents.map((binding) => ({ ...binding, profile_id: 1 })) }));
    const saved = vi.mocked(context.client.save).mock.calls[0][0]; expect(saved).not.toHaveProperty("id"); expect(saved).not.toHaveProperty("version"); expect(context.onDirtyChange).toHaveBeenCalledWith(false); expect(context.client.start).not.toHaveBeenCalled();
  });
  it("shows prior-version runs after saving a new Team version, with version labels", async () => {
    const context = mount({ kind: "workflows", page: "edit", key: "review" });
    vi.mocked(context.client.list).mockResolvedValue([{ ...templateFixture(), version: 2 }]);
    vi.mocked(context.client.runs).mockResolvedValue([runFixture({ id: "old-run", team_version: 1, name: "Old result", status: "completed" })]);
    context.render(); await settle();
    expect(context.client.runs).toHaveBeenCalledWith("review", 0);
    expect(host.textContent).toContain("Run history · all versions");
    expect(host.textContent).toContain("Old result · v1 · Completed");
    const historicalButton = [...host.querySelectorAll("button")].find((button) => button.textContent?.startsWith("Old result · v1 · Completed"));
    expect(historicalButton).toBeDefined(); act(() => historicalButton!.click()); await settle();
    expect(context.onNavigate).toHaveBeenCalledWith("/webui/v2/workflows/runs/old-run");
  });
  it("never offers ID=0 or draft profiles as step bindings", async () => {
    const context = mount({ kind: "workflows", page: "new" }); vi.mocked(context.agentClient.list).mockResolvedValue([{ ...agentFixture(), record: { ...agentFixture().record, id: 0, display_name: "Builtin zero" } }, { ...agentFixture(), record: { ...agentFixture().record, id: 2, status: "draft", display_name: "Draft" } }]); context.render(); await settle();
    expect(host.textContent).toContain("No published database V2 text agents"); expect([...host.querySelectorAll("option")].some((option) => /Builtin zero|Draft/.test(option.textContent ?? ""))).toBe(false);
  });
  it("selects shared/private profiles by unique database ID and persists the chosen exact identity", async () => {
    const context = mount({ kind: "workflows", page: "edit", key: "review" });
    vi.mocked(context.agentClient.list).mockResolvedValue([
      { ...agentFixture(), record: { ...agentFixture().record, id: 11, scope: "tenant_shared" } },
      { ...agentFixture(), record: { ...agentFixture().record, id: 22, scope: "user_private" } },
    ]);
    context.render(); await settle();
    const selector = host.querySelector<HTMLSelectElement>(".workflow-step-list select")!;
    expect(selector.value).toBe("22");
    expect([...selector.options].map((option) => option.value)).toEqual(["", "11", "22"]);
    setValue(selector, "11");
    await act(async () => { host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); }); await settle();
    expect(vi.mocked(context.client.save).mock.calls[0][0].agents).toEqual([{ ref: "a-11-v1", profile_id: 11, profile_key: "writer", profile_version: 1 }]);
  });
  it("reuses start Idempotency-Key after ambiguous network failure and shows real run route", async () => {
    const context = mount({ kind: "workflows", page: "edit", key: "review" }); vi.mocked(context.client.start).mockRejectedValueOnce(new Error("Connection lost")).mockResolvedValueOnce({ run: runFixture(), replayed: true }); context.render(); await settle();
    const input = [...host.querySelectorAll("textarea")].at(-1)!; setValue(input, "Review this release"); await click("Start real run"); expect(host.textContent).toContain("Connection lost"); await click("Start real run");
    const calls = vi.mocked(context.client.start).mock.calls; expect(calls[0]).toEqual(calls[1]); expect(calls[0][1]).toBe(1); expect(context.onNavigate).toHaveBeenCalledWith("/webui/v2/workflows/runs/run-1");
  });
  it("does not navigate after a pending save resolves on an unmounted editor", async () => {
    const context = mount({ kind: "workflows", page: "edit", key: "review" });
    let resolveSave!: (value: ReturnType<typeof templateFixture>) => void;
    vi.mocked(context.client.save).mockImplementation(() => new Promise((resolve) => { resolveSave = resolve; }));
    context.render(); await settle();
    act(() => host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    act(() => root.render(<div>Elsewhere</div>));
    await act(async () => resolveSave(templateFixture()));
    expect(context.onNavigate).not.toHaveBeenCalled(); expect(context.onDirtyChange).not.toHaveBeenCalledWith(false);
  });
  it("keeps agent editing visible beside the directory and confirms draft state", async () => {
    const context = mount({ kind: "workflows", page: "agents" }); context.render(); await settle();
    expect(host.querySelectorAll(".workflow-agent-row")).toHaveLength(1);
    expect(host.querySelector(".workflow-agent-row .workflow-status")?.textContent).toBe("Published");
    await click("Edit");
    expect(host.querySelector(".workflow-agent-row")?.getAttribute("data-active")).toBe("true");
    expect(host.querySelector<HTMLInputElement>(".workflow-agent-form input")?.value).toBe("Release reviewer");
    await act(async () => { host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); }); await settle();
    expect(host.querySelector('[role="status"]')?.textContent).toContain("Draft saved");
    expect(context.agentClient.save).toHaveBeenCalledWith(expect.objectContaining({ name: "Release reviewer" }), agentFixture().record);
    setValue(host.querySelectorAll<HTMLSelectElement>(".workflow-agent-form select")[1], "custom");
    expect(host.querySelector(".workflow-success")).toBeNull();
    expect(context.onDirtyChange).toHaveBeenLastCalledWith(true);
  });
  it("creates agent draft with role and inherited model, then offers publish separately", async () => {
    const context = mount({ kind: "workflows", page: "agents" }); context.render(); await settle(); setValue(host.querySelector<HTMLInputElement>("form input")!, "My researcher"); setValue(host.querySelector<HTMLTextAreaElement>("form textarea")!, "Analyze supplied text");
    await act(async () => { host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); }); await settle(); expect(context.agentClient.save).toHaveBeenCalledWith({ name: "My researcher", role: "general", prompt: "Analyze supplied text", provider: "", model: "" }, undefined); expect(context.agentClient.publish).not.toHaveBeenCalled(); expect(host.textContent).toContain("Tools: none");
  });
});
describe("Run detail / hierarchy", () => {
  it("distinguishes cancellation from an error alert", async () => {
    const client = workflowClientFixture();
    vi.mocked(client.get).mockResolvedValue(runFixture({ status: "cancelled", failure: { code: "cancelled", message: "Cancelled by the user", retryable: false }, nodes: [{ node_id: "step-1", status: "cancelled", attempts: [] }] }));
    act(() => root.render(<I18nProvider><WorkflowRunDetail client={client} runID="run-1" /></I18nProvider>)); await settle();
    expect(host.querySelector(".workflow-run > header .workflow-status")?.textContent).toBe("Cancelled");
    expect(host.querySelector(".workflow-cancelled")?.getAttribute("role")).toBe("status");
    expect(host.querySelector(".workflow-cancelled[role=alert]")).toBeNull();
  });
  it("shows progress first while artifacts and raw events stay available on demand", async () => {
    const client = workflowClientFixture();
    vi.mocked(client.get).mockResolvedValue(runFixture({ status: "completed", nodes: [{ node_id: "step-1", status: "completed", output: "A concise answer", attempts: [] }], artifacts: [{ id: "artifact", node_id: "step-1", attempt_no: 1, kind: "text", content: "A concise answer", sha256: "sha256", created_at: "2026-10-29T00:00:00Z" }] }));
    vi.mocked(client.events).mockResolvedValue({ events: [eventFixture(1, { type: "run_completed", status: "completed" })], has_more: false, last_sequence: 1 });
    act(() => root.render(<I18nProvider><WorkflowRunDetail client={client} runID="run-1" /></I18nProvider>)); await settle();
    expect(host.querySelector("[role=progressbar]")?.getAttribute("aria-valuenow")).toBe("1");
    expect(host.textContent).toContain("1 of 1 steps completed");
    expect(host.querySelector(".workflow-artifact")?.hasAttribute("open")).toBe(false);
    expect(host.querySelector(".workflow-event-panel")?.hasAttribute("open")).toBe(false);
    expect(host.textContent).toContain("Run completed");
    expect(host.textContent).toContain("run_completed");
  });
});
describe("Run detail / readback", () => {
  it("shows readback artifacts, usage, failures, attempts and historical event timeline", async () => {
    const client = workflowClientFixture(); const run = runFixture({ status: "completed", last_sequence: 2, artifacts: [{ id: "artifact", node_id: "step-1", attempt_no: 2, kind: "text", content: "Persisted result", sha256: "sha256", created_at: "2026-10-29T00:00:00Z" }], nodes: [{ node_id: "step-1", status: "completed", output: "Persisted result", attempts: [{ number: 1, epoch: 1, task_id: 1, status: "failed", started_at: "now", usage: runFixture().usage, failure: { code: "rate_limit", message: "Please wait", retryable: true } }, { number: 2, epoch: 1, task_id: 2, status: "completed", started_at: "now", usage: runFixture().usage }] }] });
    vi.mocked(client.get).mockResolvedValue(run); vi.mocked(client.events).mockResolvedValue({ events: [eventFixture(1, { type: "task_retry_scheduled", node_id: "step-1", attempt_no: 1, status: "retry_wait" }), eventFixture(2, { type: "run_completed", status: "completed" })], has_more: false, last_sequence: 2 });
    act(() => root.render(<I18nProvider><WorkflowRunDetail client={client} runID="run-1" /></I18nProvider>)); await settle();
    expect(host.textContent).toContain("Persisted result"); expect(host.textContent).toContain("Cache write"); expect(host.textContent).toContain("rate_limit"); expect(host.textContent).toContain("task_retry_scheduled"); expect(host.textContent).toContain("Attempt 2"); expect(client.stream).not.toHaveBeenCalled(); expect(host.textContent).not.toContain("Cancel run");
  });
  it("separates unknown usage reservations from actual reported tokens without inventing cost", async () => {
    window.localStorage.setItem("niuma-webui.language.v1", "zh");
    const client = workflowClientFixture();
    vi.mocked(client.get).mockResolvedValue(runFixture({ status: "completed", uncertain_tokens: 4096, nodes: [{ node_id: "step-1", status: "completed", attempts: [{ number: 1, epoch: 1, task_id: 1, status: "completed", started_at: "now", usage_known: false, budget_reserved: 4096, usage: { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 } }] }] }));
    act(() => root.render(<I18nProvider><WorkflowRunDetail client={client} runID="run-1" /></I18nProvider>)); await settle();
    expect(host.textContent).toContain("仍有未知用量，保守预留 4096 tokens，不代表真实消耗/费用");
    expect(host.textContent).toContain("用量未知");
    expect([...host.querySelectorAll(".workflow-run > header .workflow-usage dd")].map((value) => value.textContent)).toEqual(["7", "11", "2", "3"]);
    expect([...host.querySelectorAll(".workflow-step-list .workflow-usage dd")].map((value) => value.textContent)).toEqual(["未知", "未知", "未知", "未知"]);
    expect(host.textContent).not.toMatch(/0元|￥0|\$0/);
  });
  it("recovers only interrupted runs with authoritative revision and never re-starts", async () => {
    const client = workflowClientFixture(); vi.mocked(client.get).mockResolvedValue(runFixture({ status: "interrupted", revision: 17 }));
    act(() => root.render(<I18nProvider><WorkflowRunDetail client={client} runID="run-1" /></I18nProvider>)); await settle(); await click("Recover interrupted run"); expect(client.recover).toHaveBeenCalledWith("run-1", 17); expect(client.start).not.toHaveBeenCalled();
  });
  it("reads back on stream disconnect and ignores a stale callback after unmount", async () => {
    const client = workflowClientFixture(); let callback: ((event: WorkflowEvent) => void) | undefined;
    vi.mocked(client.stream).mockImplementation(async (_id, _after, event) => { callback = event; throw new Error("offline"); });
    vi.mocked(client.get).mockResolvedValueOnce(runFixture()).mockResolvedValue(runFixture({ status: "completed", revision: 2, last_sequence: 3 }));
    act(() => root.render(<I18nProvider><WorkflowRunDetail client={client} runID="run-1" /></I18nProvider>)); await settle(); await settle();
    expect(client.get).toHaveBeenCalledTimes(2); expect(host.textContent).toContain("Completed"); expect(client.start).not.toHaveBeenCalled();
    act(() => root.render(<div>other</div>)); act(() => callback?.(eventFixture(4, { status: "failed" }))); expect(host.textContent).toBe("other");
  });
});
