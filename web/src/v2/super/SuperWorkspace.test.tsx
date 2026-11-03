import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { SuperWorkspace } from "./SuperWorkspace";
import { getSuperClient, superError, type SuperClient, type WorkOrder, type WorkOrderInput } from "./client";

const input: WorkOrderInput = { title: "桌面验收", objective: "在桌面登记任务", acceptance_criteria: ["不创建会话", "不调用模型"], workspace: "" };
const order: WorkOrder = { ...input, id: "wo-test", status: "recorded", execution_status: "not_started", verification_status: "not_started", delivery_status: "not_started", created_at: "2026-11-03T00:00:00Z", updated_at: "2026-11-03T00:00:00Z" };
describe("Super Desktop workspace", () => {
  let host: HTMLDivElement;
  let root: Root;
  let client: SuperClient;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => "zh", setItem: vi.fn() } });
    host = document.createElement("div"); document.body.replaceChildren(host); root = createRoot(host);
    client = { list: vi.fn().mockResolvedValue([]), create: vi.fn().mockResolvedValue(order), get: vi.fn().mockResolvedValue(order), cancel: vi.fn().mockResolvedValue({ ...order, status: "cancelled" }) };
  });
  afterEach(() => { act(() => root.unmount()); delete window.go; vi.restoreAllMocks(); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT; });
  async function render(value: SuperClient | null = client) {
    await act(async () => root.render(<I18nProvider><SuperWorkspace client={value} onBack={vi.fn()} /></I18nProvider>));
  }
  function fill(id: string, value: string) {
    const field = host.querySelector<HTMLInputElement | HTMLTextAreaElement>(`#${id}`)!;
    act(() => { Object.getOwnPropertyDescriptor(field instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(field, value); field.dispatchEvent(new Event("input", { bubbles: true })); });
  }
  function fillForm() { fill("super-title", input.title); fill("super-objective", input.objective); fill("super-criteria", "不创建会话\n不调用模型"); }
  async function submit() { await act(async () => host.querySelector<HTMLButtonElement>('.super-primary')!.click()); }

  it("records, inspects and cancels without HTTP or a browser window", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch"); const open = vi.spyOn(window, "open");
    await render(); expect(host.textContent).toContain("还没有工单"); fillForm(); await submit();
    expect(client.create).toHaveBeenCalledWith(expect.any(String), input);
    expect(host.querySelector(".super-detail")?.textContent).toContain(input.title);
    expect(host.querySelectorAll(".super-stages strong")).toHaveLength(3);
    expect(host.textContent).toContain("尚未执行");
    await act(async () => host.querySelector<HTMLButtonElement>(".super-cancel")!.click());
    expect(client.cancel).toHaveBeenCalledWith(order.id); expect(host.textContent).toContain("已取消");
    expect(host.querySelector<HTMLButtonElement>(".super-cancel")?.disabled).toBe(true);
    expect(fetchSpy).not.toHaveBeenCalled(); expect(open).not.toHaveBeenCalled();
  });
  it("does not invoke intake for whitespace-only fields even when form validation is bypassed", async () => {
    await render(); fillForm(); fill("super-title", "   ");
    expect(host.querySelector<HTMLButtonElement>(".super-primary")?.disabled).toBe(true);
    await act(async () => host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(client.create).not.toHaveBeenCalled();
  });
  it("reuses an idempotency key across ambiguous failures and double submits", async () => {
    vi.mocked(client.create).mockRejectedValueOnce("secret path /private/user/database");
    await render(); fillForm(); await submit();
    expect(host.textContent).toContain("暂时不可用"); expect(host.textContent).not.toContain("/private/user");
    const key = vi.mocked(client.create).mock.calls[0][0]; await submit(); await submit();
    expect(vi.mocked(client.create).mock.calls.map((call) => call[0])).toEqual([key, key, key]);
    fill("super-title", "另一项任务"); await submit(); expect(vi.mocked(client.create).mock.calls[3][0]).not.toBe(key);
  });
  it("prevents overlapping requests and refresh cannot undo cancellation", async () => {
    vi.mocked(client.list).mockResolvedValue([order]); await render();
    let finish!: (order: WorkOrder) => void;
    vi.mocked(client.cancel).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    act(() => { host.querySelector<HTMLButtonElement>(".super-cancel")!.click(); host.querySelector<HTMLButtonElement>(".super-cancel")!.click(); });
    expect(client.cancel).toHaveBeenCalledTimes(1);
    expect([...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === "刷新工单")?.disabled).toBe(true);
    await act(async () => finish({ ...order, status: "cancelled" })); expect(host.querySelector(".super-badge")?.textContent).toBe("已取消");
  });
  it("ignores a stale list response after the bridge changes", async () => {
    let finish!: (orders: WorkOrder[]) => void;
    vi.mocked(client.list).mockImplementation(() => new Promise((resolve) => { finish = resolve; })); await render();
    const next = { ...client, list: vi.fn().mockResolvedValue([{ ...order, id: "new", title: "新桥接工单" }]) };
    await render(next); await act(async () => finish([order]));
    expect(host.querySelector(".super-order-list")?.textContent).toContain("新桥接工单");
    expect(host.querySelector(".super-order-list")?.textContent).not.toContain(input.title);
  });
  it("fails closed when Desktop bridge methods are absent", async () => {
    await render(null); expect(host.textContent).toContain("桥接尚未就绪");
    expect(host.querySelector<HTMLButtonElement>('.super-primary')?.disabled).toBe(true);
    expect(getSuperClient()).toBeNull();
    window.go = { main: { app: { RestartLocalService: vi.fn(), ListSuperWorkOrders: vi.fn() } } };
    expect(getSuperClient()).toBeNull();
  });
  it("classifies only known safe failures", () => {
    expect(superError("invalid work-order input")).toBe("invalid");
    expect(superError(new Error("idempotency key conflicts with an existing intake"))).toBe("conflict");
    expect(superError("work order not found")).toBe("notFound");
    expect(superError({ secret: "value" })).toBe("unavailable");
  });
});
