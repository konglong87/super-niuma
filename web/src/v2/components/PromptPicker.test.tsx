import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { deletePromptTemplate, listPromptTemplates, savePromptTemplate } from "../../lib/api";
import type { IdentityConfig, PromptTemplate } from "../../lib/types";
import { Composer } from "./Composer";
import { PromptPicker } from "./PromptPicker";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  listPromptTemplates: vi.fn(), savePromptTemplate: vi.fn(), deletePromptTemplate: vi.fn()
}));

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
const template: PromptTemplate = { id: 1, title: "Review", content: "Review {{feature}} carefully.", category: "Code", pinned: false, sort_order: 2 };
const empty = { ...template, id: 0, title: "", content: "", category: "", pinned: false, sort_order: 0 };

describe("PromptPicker", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => null, setItem: vi.fn(), removeItem: vi.fn() } });
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function (this: HTMLDialogElement) { this.setAttribute("open", ""); } });
    Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: function (this: HTMLDialogElement) { this.removeAttribute("open"); } });
    vi.mocked(listPromptTemplates).mockResolvedValue([template]);
    vi.mocked(savePromptTemplate).mockResolvedValue(template);
    vi.mocked(deletePromptTemplate).mockResolvedValue();
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.restoreAllMocks();
    vi.useRealTimers();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render(props: Partial<React.ComponentProps<typeof PromptPicker>> = {}) {
    const onSelect = vi.fn();
    act(() => root.render(<I18nProvider><main className="webui2-page"><div className="webui2-composer"><PromptPicker identity={identity} onSelect={onSelect} {...props} /></div></main></I18nProvider>));
    return onSelect;
  }

  function button(name: string): HTMLButtonElement {
    return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((item) => (item.getAttribute("aria-label") ?? item.textContent) === name)!;
  }

  async function click(name: string) {
    await act(async () => { button(name).click(); });
  }

  async function settle() {
    await act(async () => { await vi.advanceTimersByTimeAsync(200); });
  }

  function field(label: string): HTMLInputElement | HTMLTextAreaElement {
    return Array.from(document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input,textarea")).find((item) => item.getAttribute("aria-label") === label || item.closest("label")?.textContent === label)!;
  }

  function fill(label: string, value: string) {
    const input = field(label);
    act(() => {
      Object.getOwnPropertyDescriptor(input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  async function open() {
    await click("Common prompts");
    await settle();
  }

  it("renders only the accessible star and portals the dialog outside Composer", async () => {
    const onSelect = render();
    const trigger = button("Common prompts");
    expect(trigger.textContent).toBe("");
    expect(trigger.querySelector("svg")).not.toBeNull();
    expect(trigger.title).toBe("Common prompts");
    await open();
    expect(document.querySelector(".webui2-composer dialog")).toBeNull();
    expect(document.querySelector(".webui2-page > dialog[open]")).not.toBeNull();
    expect(document.activeElement).toBe(field("Search title, content or category"));
    await click("Use Review");
    expect(onSelect).toHaveBeenCalledExactlyOnceWith(template.content);
    expect(document.querySelector("dialog")).toBeNull();
  });

  it("does not fetch or open from a disabled launcher", async () => {
    render({ disabled: true });
    await click("Common prompts");
    expect(document.querySelector("dialog")).toBeNull();
    expect(listPromptTemplates).not.toHaveBeenCalled();
  });

  it("shows loading, failure with retry, then empty state and separate editor", async () => {
    vi.mocked(listPromptTemplates).mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
    render();
    await click("Common prompts");
    expect(document.querySelector('[role="status"]')?.textContent).toContain("Loading");
    await settle();
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("offline");
    await click("Retry");
    await settle();
    expect(document.querySelector('[role="status"]')?.textContent).toContain("No common prompts");
    await click("Add prompt");
    expect(document.querySelector(".prompt-picker-toolbar")).toBeNull();
    expect(document.activeElement).toBe(field("Title"));
    expect(button("Save").disabled).toBe(true);
  });

  it("ignores old search responses and distinguishes no matches from empty catalog", async () => {
    let resolveOld!: (value: PromptTemplate[]) => void;
    vi.mocked(listPromptTemplates).mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; })).mockResolvedValue([]);
    render();
    await open();
    fill("Search title, content or category", "missing");
    await settle();
    expect(listPromptTemplates).toHaveBeenLastCalledWith(identity, "missing");
    await act(async () => resolveOld([template]));
    expect(document.querySelector('[role="status"]')?.textContent).toContain("No matching prompts");
    expect(document.querySelector(".prompt-picker-item")).toBeNull();
  });

  it("creates a template with category, ordering and pin without selecting it", async () => {
    const onSelect = render();
    await open();
    await click("New");
    fill("Title", "New review");
    fill("Prompt content", "Check {{feature}}");
    fill("Category", "Work");
    fill("Sort order", "7");
    await act(async () => field("Pin").click());
    await click("Save");
    expect(savePromptTemplate).toHaveBeenCalledExactlyOnceWith(identity, { ...empty, title: "New review", content: "Check {{feature}}", category: "Work", pinned: true, sort_order: 7 });
    expect(document.querySelector(".prompt-picker-editor")).toBeNull();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("keeps edits on failure, blocks duplicate writes and supports retry", async () => {
    let rejectSave!: (error: Error) => void;
    vi.mocked(savePromptTemplate).mockImplementationOnce(() => new Promise((_, reject) => { rejectSave = reject; })).mockResolvedValue(template);
    render();
    await open();
    await click("Edit");
    fill("Title", "Updated review");
    await click("Save");
    expect(button("Saving…").disabled).toBe(true);
    expect(button("Close").disabled).toBe(true);
    await act(async () => document.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(savePromptTemplate).toHaveBeenCalledTimes(1);
    await act(async () => rejectSave(new Error("save failed")));
    expect(field("Title").value).toBe("Updated review");
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("save failed");
    await click("Save");
    expect(savePromptTemplate).toHaveBeenLastCalledWith(identity, { ...template, title: "Updated review" });
    expect(document.querySelector(".prompt-picker-editor")).toBeNull();
  });

  it("confirms deletion in the dialog, preserves the row on failure, and retries", async () => {
    vi.mocked(deletePromptTemplate).mockRejectedValueOnce(new Error("delete failed")).mockResolvedValue();
    render();
    await open();
    await click("Delete");
    expect(document.activeElement).toBe(button("Cancel"));
    expect(deletePromptTemplate).not.toHaveBeenCalled();
    await click("Cancel");
    expect(document.querySelector(".prompt-picker-item")).not.toBeNull();
    await click("Delete");
    await click("Delete prompt");
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("delete failed");
    vi.mocked(listPromptTemplates).mockResolvedValue([]);
    await click("Delete prompt");
    await settle();
    expect(deletePromptTemplate).toHaveBeenLastCalledWith(identity, 1);
    expect(document.querySelector(".prompt-picker-item")).toBeNull();
  });

  it("toggles pin through the existing save API", async () => {
    render();
    await open();
    await click("Pin");
    expect(savePromptTemplate).toHaveBeenCalledExactlyOnceWith(identity, { ...template, pinned: true });
  });

  it("protects dirty edits and closes a clean dialog through native cancel", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    render();
    await open();
    await click("New");
    fill("Title", "Keep me");
    await act(async () => document.querySelector("dialog")!.dispatchEvent(new Event("cancel", { cancelable: true })));
    expect(confirm).toHaveBeenCalled();
    expect(field("Title").value).toBe("Keep me");
    confirm.mockReturnValue(true);
    await click("Cancel");
    await act(async () => document.querySelector("dialog")!.dispatchEvent(new Event("cancel", { cancelable: true })));
    expect(document.querySelector("dialog")).toBeNull();
  });

  it("remounts private dialog state when identity changes", async () => {
    render();
    await open();
    await click("Edit");
    vi.mocked(listPromptTemplates).mockResolvedValue([]);
    render({ identity: { ...identity, userId: "other-user" } });
    await settle();
    expect(document.querySelector(".prompt-picker-editor")).toBeNull();
    expect(document.querySelector(".prompt-picker-item")).toBeNull();
  });

  it.each(["", "Existing draft"])("fills Composer without sending and preserves '%s'", async (draft) => {
    const onSend = vi.fn();
    act(() => root.render(<I18nProvider><main className="webui2-page"><Composer identity={identity} targetRef="tenant:prompt-test" sessionStatus="idle" availableSources={[]} onSend={onSend} onSent={vi.fn()} onStopRequested={vi.fn()} /></main></I18nProvider>));
    fill("Message", draft);
    await open();
    await click("Use Review");
    expect(field("Message").value).toBe(draft ? `${draft}\n\n${template.content}` : template.content);
    expect(onSend).not.toHaveBeenCalled();
  });
});
