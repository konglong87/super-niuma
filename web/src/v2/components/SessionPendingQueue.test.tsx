import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig, PendingInputRecord } from "../../lib/types";
import { pendingQueueQueryKey } from "../api/useSessionPendingQueue";
import { PendingQueueSettingsButton, SessionPendingQueue, type SessionPendingQueueProps } from "./SessionPendingQueue";

vi.mock("../../lib/api", () => ({ listPendingInputs: vi.fn(), getPendingInputQueueSettings: vi.fn(), updatePendingInput: vi.fn(), movePendingInputUp: vi.fn(), cancelPendingInput: vi.fn(), retryPendingInput: vi.fn(), setPendingInputQueueEnabled: vi.fn(), createPendingInputSideChat: vi.fn() }));

const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant-a", userId: "user-a", deviceId: "device", model: "model" };
const scope: SessionPendingQueueProps = { identity, sessionRef: "tenant:alpha", taskID: 12 };
function item(id: string, sequence: number, status: PendingInputRecord["status"] = "queued"): PendingInputRecord {
  return { id, sequence, status, content: `Message ${id}`, client_input_id: `client-${id}`, session_id: "alpha" };
}

describe("SessionPendingQueue", () => {
  let host: HTMLDivElement;
  let root: Root;
  let queryClient: QueryClient;
  let records: PendingInputRecord[];
  let enabled: boolean;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.resetAllMocks();
    records = [];
    enabled = true;
    vi.mocked(api.listPendingInputs).mockImplementation(async () => [...records]);
    vi.mocked(api.getPendingInputQueueSettings).mockImplementation(async () => ({ enabled }));
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  });

  afterEach(() => {
    act(() => root.unmount());
    queryClient.clear();
    vi.useRealTimers();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  async function flush(): Promise<void> {
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 15)); });
  }

  async function render(props: Partial<SessionPendingQueueProps> = {}): Promise<void> {
    act(() => root.render(<QueryClientProvider client={queryClient}><I18nProvider><SessionPendingQueue {...scope} {...props} /></I18nProvider></QueryClientProvider>));
    await flush();
  }

  function button(label: string, index = 0): HTMLButtonElement {
    const found = host.querySelectorAll<HTMLButtonElement>(`button[aria-label="${label}"]`)[index];
    expect(found).toBeDefined();
    return found;
  }

  async function click(label: string, index = 0): Promise<void> {
    await act(async () => button(label, index).click());
    await flush();
  }

  function setText(label: string, value: string): void {
    const textarea = host.querySelector<HTMLTextAreaElement>(`textarea[aria-label="${label}"]`);
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
    act(() => {
      setter?.call(textarea, value);
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("renders no queue entry or wrapper for zero pending inputs", async () => {
    await render();
    expect(host.innerHTML).toBe("");
    expect(api.getPendingInputQueueSettings).not.toHaveBeenCalled();
  });

  it.each([{ items: [item("one", 1)] }, { items: [item("running", 1, "running"), item("one", 2)] }])("shows a count starting at one queued input", async ({ items }) => {
    records = items;
    await render();
    expect(host.textContent).toBe("1 queued");
    expect(button("Manage pending inputs").getAttribute("aria-expanded")).toBe("false");
    expect(api.getPendingInputQueueSettings).not.toHaveBeenCalled();
  });

  it("summarizes only queued records and expands the server order without executing or completed records", async () => {
    records = [item("second", 4), item("sent", 2, "sent"), item("first", 3), item("running", 1, "running"), item("cancelled", 6, "cancelled"), item("failed", 5, "failed")];
    await render();
    expect(host.querySelector(".webui2-pending-queue-toggle")?.textContent).toBe("2 queued");
    expect(host.querySelector(".webui2-pending-failure-toggle")?.textContent).toBe("Failed 1");
    await click("Manage pending inputs");
    const rows = Array.from(host.querySelectorAll("li"));
    expect(rows.map((row) => row.querySelector("p")?.textContent)).toEqual(["Message first", "Message second", "Message failed"]);
    expect(button("Move up").disabled).toBe(true);
    expect(button("Move up", 1).disabled).toBe(false);
    expect(button("Move up", 2).disabled).toBe(true);
    expect(host.querySelector('button[aria-label="Open side chat"]')).toBeNull();
  });

  it("edits content and direction inline and keeps attachments untouched", async () => {
    records = [{ ...item("one", 1), attachments: [{ type: "image", attachment_id: "image-1", media_type: "image/png", size_bytes: 100 }] }];
    vi.mocked(api.updatePendingInput).mockImplementation(async (_identity, _task, id, patch) => {
      records = records.map((record) => record.id === id ? { ...record, ...patch } : record);
      return records[0];
    });
    await render();
    await click("Manage pending inputs");
    await click("Edit pending input");
    setText("Message", "Updated message");
    setText("Direction", "Focus on tests");
    await click("Save changes");
    expect(api.updatePendingInput).toHaveBeenCalledWith(identity, 12, "one", { content: "Updated message", direction: "Focus on tests" });
    expect(host.textContent).toContain("Updated message");
    expect(host.textContent).toContain("Direction: Focus on tests");
    expect(host.textContent).toContain("1 attachment(s)");
    expect(host.querySelector("textarea")).toBeNull();
    expect(api.listPendingInputs).toHaveBeenCalledTimes(2);
  });

  it("reads authoritative reordered positions then removes a deleted item", async () => {
    records = [item("one", 1), item("two", 2)];
    vi.mocked(api.movePendingInputUp).mockImplementation(async () => {
      records = [item("two", 1), item("one", 2)];
      return records[0];
    });
    vi.mocked(api.cancelPendingInput).mockImplementation(async (_identity, _task, id) => { records = records.filter((record) => record.id !== id); });
    await render();
    await click("Manage pending inputs");
    await click("Move up", 1);
    expect(api.movePendingInputUp).toHaveBeenCalledWith(identity, 12, "two");
    expect(host.querySelector("li p")?.textContent).toBe("Message two");
    await click("Delete pending input");
    expect(api.cancelPendingInput).toHaveBeenCalledWith(identity, 12, "two");
    expect(host.textContent).not.toContain("Message two");
    expect(host.querySelector(".webui2-pending-queue-summary")?.textContent).toBe("1 queued");
  });

  it("keeps failed-only queue accessible and retries only after user action", async () => {
    records = [item("failed", 1, "failed")];
    vi.mocked(api.retryPendingInput).mockImplementation(async () => { records = [item("failed", 1)]; return records[0]; });
    await render();
    expect(host.textContent).toBe("Failed 1");
    expect(host.querySelector('[aria-label="Manage pending inputs"]')).toBeNull();
    expect(api.retryPendingInput).not.toHaveBeenCalled();
    await click("Manage failed inputs");
    await click("Retry pending input");
    expect(api.retryPendingInput).toHaveBeenCalledWith(identity, 12, "failed");
    expect(host.querySelector('button[aria-label="Retry pending input"]')).toBeNull();
  });

  it("preserves an edit after conflict, reads back and reports a safe failure", async () => {
    records = [item("one", 1)];
    vi.mocked(api.updatePendingInput).mockRejectedValue(new Error("private raw backend detail"));
    await render();
    await click("Manage pending inputs");
    await click("Edit pending input");
    setText("Message", "Keep this draft");
    await click("Save changes");
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("Keep this draft");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("could not be updated");
    expect(host.textContent).not.toContain("private raw backend detail");
    expect(api.listPendingInputs).toHaveBeenCalledTimes(2);
  });

  it("reads queue enablement and updates it without deleting queued records", async () => {
    records = [item("one", 1)];
    vi.mocked(api.setPendingInputQueueEnabled).mockImplementation(async (_identity, _task, value) => { enabled = value; return { enabled }; });
    await render();
    await click("Manage pending inputs");
    const toggle = host.querySelector<HTMLInputElement>('[role="switch"]')!;
    expect(toggle.checked).toBe(true);
    await act(async () => toggle.click());
    await flush();
    expect(api.setPendingInputQueueEnabled).toHaveBeenCalledWith(identity, 12, false);
    expect(toggle.checked).toBe(false);
    expect(host.textContent).toContain("Message one");
    expect(api.cancelPendingInput).not.toHaveBeenCalled();
  });

  it("provides the side-chat readback to the optional navigation callback", async () => {
    records = [item("one", 1)];
    const result = { session_id: 71, task_id: 72, source_pending_input_id: "one" };
    const onOpenSession = vi.fn();
    vi.mocked(api.createPendingInputSideChat).mockResolvedValue(result);
    await render({ onOpenSession });
    await click("Manage pending inputs");
    await click("Open side chat");
    expect(onOpenSession).toHaveBeenCalledWith(result);
    expect(api.createPendingInputSideChat).toHaveBeenCalledWith(identity, 12, "one");
  });

  it("isolates cache and transient editor state across sessions, users and tasks", async () => {
    records = [item("private-a", 1)];
    await render();
    await click("Manage pending inputs");
    await click("Edit pending input");
    setText("Message", "Unsaved private draft");
    records = [item("other-b", 1)];
    const nextScope: SessionPendingQueueProps = { identity: { ...identity, tenantKey: "tenant-b", userId: "user-b" }, sessionRef: "tenant:beta", taskID: 15 };
    await render(nextScope);
    expect(host.textContent).toBe("1 queued");
    expect(host.querySelector("textarea")).toBeNull();
    await click("Manage pending inputs");
    expect(host.textContent).toContain("Message other-b");
    expect(host.textContent).not.toContain("private");
    expect(queryClient.getQueryData([...pendingQueueQueryKey(scope), "items"])).toEqual([item("private-a", 1)]);
    expect(queryClient.getQueryData([...pendingQueueQueryKey(nextScope), "items"])).toEqual([item("other-b", 1)]);
    records = [item("task-16", 1)];
    await render({ ...nextScope, taskID: 16 });
    await click("Manage pending inputs");
    expect(host.textContent).toContain("Message task-16");
    expect(host.textContent).not.toContain("Message other-b");
  });

  it("invalidates queue data when the conversation revision changes", async () => {
    await render();
    records = [item("one", 1), item("two", 2)];
    await render({ revision: "event-2" });
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 350)); });
    await flush();
    expect(host.textContent).toBe("2 queued");
    expect(api.listPendingInputs).toHaveBeenCalledTimes(2);
  });

  it("does not request local sessions or missing task IDs", async () => {
    await render({ sessionRef: "local:workspace" });
    await render({ taskID: undefined });
    expect(host.textContent).toBe("");
    expect(api.listPendingInputs).not.toHaveBeenCalled();
    expect(api.getPendingInputQueueSettings).not.toHaveBeenCalled();
  });

  it("stops active-session polling on unmount", async () => {
    vi.useFakeTimers();
    act(() => root.render(<QueryClientProvider client={queryClient}><I18nProvider><SessionPendingQueue {...scope} /></I18nProvider></QueryClientProvider>));
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(api.listPendingInputs).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
    expect(api.listPendingInputs).toHaveBeenCalledTimes(2);
    act(() => root.render(<div />));
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    expect(api.listPendingInputs).toHaveBeenCalledTimes(2);
  });

  it("closes a drained queue and does not reopen when another input arrives", async () => {
    records = [item("one", 1)];
    await render();
    await click("Manage pending inputs");
    records = [];
    await act(async () => { await queryClient.invalidateQueries({ queryKey: pendingQueueQueryKey(scope) }); });
    await flush();
    expect(host.innerHTML).toBe("");
    records = [item("two", 2)];
    await act(async () => { await queryClient.invalidateQueries({ queryKey: pendingQueueQueryKey(scope) }); });
    await flush();
    expect(button("Manage pending inputs").getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector(".webui2-pending-queue-panel")).toBeNull();
  });

  it("reports queued and running records as busy and releases the lock after readback", async () => {
    const onBusyChange = vi.fn();
    records = [item("running", 1, "running")];
    await render({ onBusyChange });
    expect(onBusyChange).toHaveBeenLastCalledWith(true);
    expect(host.innerHTML).toBe("");
    records = [item("failed", 1, "failed")];
    await act(async () => { await queryClient.invalidateQueries({ queryKey: pendingQueueQueryKey(scope) }); });
    await flush();
    expect(onBusyChange).toHaveBeenLastCalledWith(false);
  });

  it("shows queue read failures separately and keeps the runtime busy until recovery", async () => {
    const onBusyChange = vi.fn();
    vi.mocked(api.listPendingInputs).mockRejectedValueOnce(new Error("temporary queue failure"));
    await render({ onBusyChange });
    expect(host.querySelector(".webui2-pending-failure-toggle")?.textContent).toContain("queue");
    expect(onBusyChange).toHaveBeenLastCalledWith(true);
    await act(async () => host.querySelector<HTMLButtonElement>(".webui2-pending-failure-toggle")?.click());
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("queue");
  });

  it("recovers an empty paused queue through the composer settings menu without item polling", async () => {
    enabled = false;
    vi.mocked(api.setPendingInputQueueEnabled).mockImplementation(async (_identity, _task, value) => { enabled = value; return { enabled }; });
    act(() => root.render(<QueryClientProvider client={queryClient}><I18nProvider><PendingQueueSettingsButton {...scope} /></I18nProvider></QueryClientProvider>));
    await flush();
    expect(api.listPendingInputs).not.toHaveBeenCalled();
    expect(host.querySelector('[role="menuitem"]')?.textContent).toBe("Resume queue");
    await click("Resume queue");
    expect(api.setPendingInputQueueEnabled).toHaveBeenCalledWith(identity, 12, true);
    expect(host.querySelector('[role="menuitem"]')?.textContent).toBe("Pause queue");
    expect(api.cancelPendingInput).not.toHaveBeenCalled();
  });
});
