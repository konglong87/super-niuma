import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { createMockSessionControlClient } from "../api/mockSessionControlClient";
import { SessionControlError } from "../api/sessionControlClient";
import type { OperationResult, PreparedAttachment, SendSessionInput, SessionDetail, SessionSummary } from "../types";
import { applySelectedSkill, Composer, prepareSendInput } from "./Composer";
import { readComposerDraft } from "./composerDraftStorage";
import type { ComposerRuntimeControls } from "./composerRuntimeControls";
import { SESSION_REF_MIME_TYPE } from "./sessionContextDrag";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test-token", mobileJwt: "mobile-token", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };

const target: SessionDetail = {
  ref: "tenant:alpha", source: "tenant", title: "Release coordination", status: "running", updatedAt: "2026-09-05T00:00:00.000Z", shortID: "alpha",
  messages: [], activity: [], context: [], changes: [], runs: []
};
const sources: SessionSummary[] = [
  { ref: "tenant:beta", source: "tenant", title: "Design review", status: "completed", updatedAt: "2026-09-05T00:00:00.000Z", shortID: "beta" },
  { ref: "local:workspace", source: "local", title: "Local workspace", status: "idle", updatedAt: "2026-09-05T00:00:00.000Z", shortID: "workspace" }
];

describe("Composer", () => {
  let host: HTMLDivElement;
  let root: Root;
  const storage = new Map<string, string>();

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    storage.clear();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key) } });
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.unstubAllGlobals();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render(overrides: Partial<React.ComponentProps<typeof Composer>> = {}) {
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockResolvedValue({
      operation: { id: "operation-1", kind: "send", status: "completed", title: "Pending input queued", detail: "", createdAt: "2026-09-05T00:00:00.000Z" },
      session: target,
      replayed: false
    });
    const onSent = vi.fn();
    const onStopRequested = vi.fn();
    act(() => root.render(<I18nProvider><Composer availableSources={sources} identity={identity} onSent={onSent} onSend={send} onStopRequested={onStopRequested} sessionStatus="idle" targetRef="tenant:alpha" {...overrides} /></I18nProvider>));
    return { send, onSent, onStopRequested };
  }

  function setText(value: string): void {
    const textarea = host.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]');
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
    act(() => {
      setter?.call(textarea, value);
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  function dropRef(ref: string): void {
    const drop = host.querySelector<HTMLElement>(".webui2-composer-dropzone");
    const event = new Event("drop", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "dataTransfer", { value: { getData: (type: string) => type === "application/x-niuma-session-ref" ? ref : "" } });
    act(() => drop?.dispatchEvent(event));
  }

  function keyDown(key: string, options: KeyboardEventInit = {}): KeyboardEvent {
    const event = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, key, ...options });
    act(() => host.querySelector("textarea")?.dispatchEvent(event));
    return event;
  }

  function transferEvent(type: string, data: object): Event {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.defineProperty(event, "dataTransfer", { value: { getData: () => "", ...data } });
    act(() => host.querySelector("textarea")?.dispatchEvent(event));
    return event;
  }

  it.each(["idle", "running"] as const)("sends on Enter while preserving Shift+Enter and composition input when %s", async (sessionStatus) => {
    const { send } = render({ sessionStatus });
    setText("A message");
    expect(keyDown("Enter", { shiftKey: true }).defaultPrevented).toBe(false);
    expect(keyDown("Enter", { isComposing: true }).defaultPrevented).toBe(false);
    act(() => host.querySelector("textarea")?.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true })));
    expect(keyDown("Enter").defaultPrevented).toBe(false);
    act(() => host.querySelector("textarea")?.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true })));
    expect(send).not.toHaveBeenCalled();
    await act(async () => { keyDown("Enter"); });
    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][0].text).toBe("A message");
  });

  it("keeps busy context drafts until idle and never sends automatically", async () => {
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockResolvedValue({ operation: { id: "op", kind: "send", status: "completed", title: "", detail: "", createdAt: "" }, session: target, replayed: false });
    render({ onSend: send, sessionStatus: "running" });
    setText("Use the design");
    dropRef("tenant:beta");
    expect(host.querySelector(".webui2-composer-send")).toBeNull();
    expect(host.querySelector<HTMLButtonElement>(".webui2-composer-stop")?.disabled).toBe(false);
    expect(host.textContent).toContain("Your draft is preserved");
    await act(async () => { keyDown("Enter"); });
    expect(send).not.toHaveBeenCalled();
    render({ onSend: send, sessionStatus: "idle" });
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("Use the design");
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(1);
    expect(host.querySelector<HTMLButtonElement>(".webui2-composer-send")?.disabled).toBe(false);
    expect(send).not.toHaveBeenCalled();
    await act(async () => { keyDown("Enter"); });
    expect(send.mock.calls[0][0].sourceRefs).toEqual(["tenant:beta"]);
  });

  it("renders an optional queue panel while the single action remains stop", () => {
    render({ sessionStatus: "running", queuePanel: <div data-testid="queue">One pending message</div> });
    expect(host.querySelector('[data-testid="queue"]')?.textContent).toBe("One pending message");
    expect(host.querySelector(".webui2-composer-send")).toBeNull();
    expect(host.querySelector<HTMLButtonElement>(".webui2-composer-stop")?.getAttribute("aria-label")).toBe("Stop session");
    expect(host.textContent).not.toContain("This input will be queued");
  });

  it("pastes and drops image files with deduplication and releases previews", () => {
    const createObjectURL = vi.fn().mockReturnValue("blob:preview");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", class extends URL { static createObjectURL = createObjectURL; static revokeObjectURL = revokeObjectURL; });
    render();
    const file = new File(["image"], "paste.png", { type: "image/png", lastModified: 3 });
    const paste = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(paste, "clipboardData", { value: { items: [{ kind: "file", type: file.type, getAsFile: () => file }] } });
    act(() => host.querySelector("textarea")?.dispatchEvent(paste));
    expect(paste.defaultPrevented).toBe(true);
    expect(host.querySelector("img")?.getAttribute("src")).toBe("blob:preview");
    transferEvent("drop", { files: [file], getData: (type: string) => type === SESSION_REF_MIME_TYPE ? "tenant:beta" : "" });
    expect(host.querySelectorAll(".webui2-file-chip")).toHaveLength(1);
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(1);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Remove paste.png"]')?.click());
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview");
    transferEvent("drop", { files: [file] });
    act(() => root.render(<div />));
    expect(revokeObjectURL).toHaveBeenCalledTimes(2);
  });

  it("highlights only supported drops and respects the disabled composer", () => {
    render();
    expect(transferEvent("dragover", { types: ["text/plain"] }).defaultPrevented).toBe(false);
    expect(host.querySelector(".is-drag-over")).toBeNull();
    expect(transferEvent("dragover", { types: ["Files"], items: [{ kind: "file", type: "application/pdf" }] }).defaultPrevented).toBe(false);
    expect(transferEvent("dragover", { types: [SESSION_REF_MIME_TYPE] }).defaultPrevented).toBe(true);
    expect(host.querySelector(".is-drag-over")).not.toBeNull();
    transferEvent("dragleave", {});
    expect(host.querySelector(".is-drag-over")).toBeNull();
    render({ disabled: true });
    const image = new File(["image"], "image.png", { type: "image/png" });
    expect(transferEvent("dragover", { types: [SESSION_REF_MIME_TYPE] }).defaultPrevented).toBe(false);
    transferEvent("drop", { files: [image], getData: () => "tenant:beta" });
    expect(host.querySelectorAll(".webui2-file-chip, .webui2-context-chip")).toHaveLength(0);
  });

  it("looks up slash commands in the workspace and selects with keyboard or mouse", async () => {
    const commands = [{ name: "help", description: "Help" }, { name: "history", description: "History" }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(commands), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const { send } = render({ cwd: "/work/project" });
    await act(async () => { setText("/h"); });
    expect(fetchMock.mock.calls[0][0]).toContain("cwd=%2Fwork%2Fproject&prefix=h&limit=8");
    expect(host.querySelectorAll('[role="option"]')).toHaveLength(2);
    keyDown("ArrowDown");
    expect(host.querySelector('[aria-selected="true"]')?.textContent).toContain("/history");
    keyDown("Tab");
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("/history ");
    expect(send).not.toHaveBeenCalled();
    await act(async () => { keyDown("Enter"); });
    expect(send.mock.calls[0][0].text).toBe("/history");
    fetchMock.mockResolvedValue(new Response(JSON.stringify(commands), { status: 200 }));
    await act(async () => { setText("/h"); });
    act(() => host.querySelector<HTMLButtonElement>('[role="option"]')?.click());
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("/help ");
  });

  it("sends an exact slash command and lets Escape dismiss completion", async () => {
    vi.stubGlobal("fetch", vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify([{ name: "help" }]), { status: 200 }))));
    const { send } = render();
    await act(async () => { setText("/help"); });
    await act(async () => { keyDown("Enter"); });
    expect(send.mock.calls[0][0].text).toBe("/help");
    await act(async () => { setText("/h"); });
    keyDown("Escape");
    expect(host.querySelector('[role="listbox"]')).toBeNull();
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("/h");
  });

  it("selects a Skill from the composer and sends the same slash invocation semantics", async () => {
    const commands = [
      { name: "新会话", description: "Create a focused new session", source: "skill" },
      { name: "help", description: "Help", source: "builtin" }
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(commands), { status: 200 })));
    const { send } = render({ cwd: "/work/project" });

    const skillTrigger = host.querySelector<HTMLButtonElement>('button[aria-label="Select Skill"]');
    expect(skillTrigger?.textContent).toContain("Select Skill");
    expect(skillTrigger?.querySelectorAll("svg")).toHaveLength(1);
    act(() => skillTrigger?.click());
    await act(async () => { await Promise.resolve(); });

    const menu = host.querySelector<HTMLElement>(".webui2-skill-picker-menu");
    expect(menu?.textContent).toContain("新会话");
    expect(menu?.textContent).not.toContain("help");
    act(() => Array.from(menu?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? []).find((button) => button.textContent?.includes("新会话"))?.click());
    setText("请帮我创建一个新的会话");

    await act(async () => { host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click(); });
    expect(send.mock.calls[0][0].text).toBe("/新会话\n\n请帮我创建一个新的会话");
  });

  it("selects the first filtered Skill and keeps a manual selection for the current query", async () => {
    const commands = [
      { name: "新会话", description: "Create a focused new session", source: "skill" },
      { name: "新建项目", description: "Start a new project", source: "skill" },
      { name: "新建文档", description: "Start a new document", source: "skill" },
      { name: "新建任务", description: "Start a new task", source: "skill" },
      { name: "新建报告", description: "Start a new report", source: "skill" },
      { name: "新建页面", description: "Start a new page", source: "skill" },
      { name: "新建流程", description: "Start a new workflow", source: "skill" },
      { name: "新建看板", description: "Start a new board", source: "skill" },
      { name: "新建目录", description: "Start a new directory", source: "skill" }
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(commands), { status: 200 })));
    render({ cwd: "/work/project" });

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Select Skill"]')?.click());
    await act(async () => { await Promise.resolve(); });

    const search = host.querySelector<HTMLInputElement>('input[aria-label="Search Skill"]');
    expect(search).not.toBeNull();
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    act(() => {
      setter?.call(search, "新");
      search?.dispatchEvent(new Event("input", { bubbles: true }));
    });

    expect(host.querySelector<HTMLButtonElement>('[role="option"][aria-selected="true"]')?.textContent).toContain("新会话");
    const defaultOption = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]')).find((button) => button.textContent?.includes("No Skill") || button.textContent?.includes("不指定 Skill"));
    expect(defaultOption?.getAttribute("aria-selected")).toBe("false");

    act(() => Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]')).find((button) => button.textContent?.includes("新建项目"))?.click());
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Select Skill"]')?.textContent).toContain("新建项目");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Select Skill"]')?.click());
    await act(async () => { await Promise.resolve(); });
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Select Skill"]')?.textContent).toContain("新建项目");
  });

  it("does not duplicate a manually typed selected Skill command", () => {
    expect(applySelectedSkill("/新会话\n\n继续刚才的工作", "新会话")).toBe("/新会话\n\n继续刚才的工作");
    expect(applySelectedSkill("继续刚才的工作", "新会话")).toBe("/新会话\n\n继续刚才的工作");
  });

  it("keeps dropped context in memory until send and removes it without mutations", () => {
    const { send } = render();
    dropRef("tenant:beta");
    dropRef("tenant:beta");

    expect(host.textContent).toContain("Design review");
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(1);
    expect(send).not.toHaveBeenCalled();

    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Remove Design review"]')?.click());
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(0);
    expect(send).not.toHaveBeenCalled();
  });

  it("sends an idle draft with deduplicated source refs and clears a successful draft", async () => {
    const { send, onSent } = render({ sessionStatus: "idle" });
    setText("  Queue this  ");
    dropRef("tenant:beta");
    dropRef("local:workspace");
    dropRef("tenant:beta");

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());

    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][0]).toMatchObject({ ref: "tenant:alpha", text: "Queue this", attachments: [], sourceRefs: ["tenant:beta", "local:workspace"] });
    expect(send.mock.calls[0][0].idempotencyKey).toEqual(expect.any(String));
    expect(onSent).toHaveBeenCalledWith(target);
    expect(host.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]')?.value).toBe("");
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(0);
  });

  it("disables a second send while the first send is pending", async () => {
    let resolve: ((value: OperationResult) => void) | undefined;
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockImplementation(() => new Promise((done) => { resolve = done; }));
    render({ onSend: send });
    setText("Queue this");
    const sendButton = host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]');

    act(() => sendButton?.click());
    expect(sendButton?.disabled).toBe(true);
    await act(async () => { await Promise.resolve(); });
    act(() => sendButton?.click());
    expect(send).toHaveBeenCalledTimes(1);
    await act(async () => resolve?.({ operation: { id: "operation-1", kind: "send", status: "completed", title: "", detail: "", createdAt: "" }, session: target, replayed: false }));
  });

  it("deduplicates a rapid double Enter before the first send settles", async () => {
    let resolve: ((value: OperationResult) => void) | undefined;
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockImplementation(() => new Promise((done) => { resolve = done; }));
    render({ onSend: send, sessionStatus: "idle" });
    setText("001test");

    await act(async () => {
      keyDown("Enter");
      keyDown("Enter");
      await Promise.resolve();
    });

    expect(send).toHaveBeenCalledTimes(1);
    expect(send.mock.calls[0][0].text).toBe("001test");
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("001test");

    await act(async () => resolve?.({ operation: { id: "operation-1", kind: "send", status: "completed", title: "", detail: "", createdAt: "" }, session: target, replayed: false }));
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
  });

  it.each(["idle", "running"] as const)("keeps a %s local composer read only without invoking send", (sessionStatus) => {
    const { send } = render({ sessionStatus, targetRef: "local:workspace" });
    const textarea = host.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]');
    const sendButton = host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]');

    expect(textarea?.getAttribute("aria-readonly")).toBe("true");
    expect(textarea?.disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Add attachments and options"]')?.disabled).toBe(true);
    expect(host.querySelector('select[aria-label="Add session context"]')).toBeNull();
    expect(sendButton?.disabled).toBe(true);
    act(() => sendButton?.click());
    expect(send).not.toHaveBeenCalled();
  });

  it.each(["running", "queued", "waiting_input", "waiting_permission"] as const)("switches one action from send to stop and back for %s", async (sessionStatus) => {
    render();
    const action = host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]');
    const { onStopRequested } = render({ sessionStatus });
    expect(host.querySelector('button[aria-label="Stop session"]')).toBe(action);
    expect(host.querySelector('button[aria-label="Send message"]')).toBeNull();
    await act(async () => action?.click());
    expect(onStopRequested).toHaveBeenCalledTimes(1);
    expect(host.querySelector('button[aria-label="Confirm stop session"]')).toBeNull();
    render({ sessionStatus: "completed" });
    expect(host.querySelector('button[aria-label="Send message"]')).toBe(action);
    expect(host.querySelector('button[aria-label="Stop session"]')).toBeNull();
  });

  it("reserves waiting_input for the explicit question card while keeping stop available", async () => {
    const { send } = render({ sessionStatus: "waiting_input" as never });
    const textarea = host.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message"]');
    expect(textarea?.disabled).toBe(true);
    expect(host.querySelector('button[aria-label="Stop session"]')).not.toBeNull();
    setText("Canary");
    await act(async () => { keyDown("Enter"); });
    expect(send).not.toHaveBeenCalled();
  });

  it("never offers or invokes stop for a running local session", () => {
    const { onStopRequested } = render({ sessionStatus: "running", targetRef: "local:workspace" });

    expect(host.querySelector('button[aria-label="Stop session"]')).toBeNull();
    expect(host.querySelector('button[aria-label="Confirm stop session"]')).toBeNull();
    expect(onStopRequested).not.toHaveBeenCalled();
  });

  it("presigns then uploads selected images and sends metadata without the private upload URL", async () => {
    const file = new File(["image-data"], "screen.png", { type: "image/png", lastModified: 7 });
    const calls: string[] = [];
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url === "/api/mobile/chat/attachments/presign") {
        calls.push("presign");
        return Promise.resolve(new Response(JSON.stringify({
          attachment_id: "att-1",
          object_key: "tenant/user/att-1.png",
          upload_url: "https://private-upload.test/att-1",
          expires_at: "2026-09-05T01:00:00Z",
          headers: { "x-upload-token": "secret" },
          attachment: { attachment_id: "att-1", type: "image", url: "https://public.test/att-1", upload_url: "https://must-not-leak.test" }
        }), { status: 200, headers: { "content-type": "application/json" } }));
      }
      calls.push("upload");
      expect(init?.body).toBe(file);
      return Promise.resolve(new Response("", { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const signal = new AbortController().signal;
    const input = await prepareSendInput({ identity, ref: "tenant:alpha", text: "  inspect  ", files: [file], sourceRefs: [], idempotencyKey: "send-1", signal });

    expect(calls).toEqual(["presign", "upload"]);
    expect(fetchMock.mock.calls[0][1]?.signal).toBe(signal);
    expect(fetchMock.mock.calls[1][1]?.signal).toBe(signal);
    expect(input.attachments).toEqual([{
      attachment_id: "att-1", type: "image", media_type: "image/png", name: "screen.png", size_bytes: 10,
      sha256: "2b700b7786d5a3f0cb487c3afaccb889fae829504a0ad1b70881e4643360f344", url: "https://public.test/att-1"
    }]);
    expect("upload_url" in input.attachments[0]).toBe(false);
    expect(JSON.stringify(input)).not.toContain("private-upload.test");
  });

  it("passes cancellation through the attachment preparer", async () => {
    const controller = new AbortController();
    const file = new File(["image-data"], "screen.png", { type: "image/png" });
    const attachmentPreparer = vi.fn((_identity: IdentityConfig, _file: File, signal: AbortSignal) => new Promise<PreparedAttachment>((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
    }));

    const preparing = prepareSendInput({ identity, ref: "tenant:alpha", text: "", files: [file], sourceRefs: [], idempotencyKey: "send-1", signal: controller.signal, attachmentPreparer });
    controller.abort();

    await expect(preparing).rejects.toMatchObject({ name: "AbortError" });
    expect(attachmentPreparer).toHaveBeenCalledWith(identity, file, controller.signal);
  });

  it("announces attachment progress without a default pending-input warning", () => {
    render();
    const input = host.querySelector<HTMLInputElement>('input[type="file"]');
    Object.defineProperty(input, "files", { configurable: true, value: [new File(["image"], "screen.png", { type: "image/png" })] });
    act(() => input?.dispatchEvent(new Event("change", { bubbles: true })));

    const liveRegion = host.querySelector<HTMLElement>('[aria-live="polite"]');
    expect(liveRegion?.textContent).not.toContain("queued for the active run");
    expect(liveRegion?.textContent).toContain("1 attachment(s) ready");
  });

  it("cancels an in-flight upload while preserving the file draft", async () => {
    const attachmentPreparer = vi.fn((_identity: IdentityConfig, _file: File, signal: AbortSignal) => new Promise<PreparedAttachment>((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
    }));
    const { send } = render({ attachmentPreparer });
    const input = host.querySelector<HTMLInputElement>('input[type="file"]');
    Object.defineProperty(input, "files", { configurable: true, value: [new File(["image"], "screen.png", { type: "image/png" })] });
    act(() => input?.dispatchEvent(new Event("change", { bubbles: true })));
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());
    await act(async () => { await Promise.resolve(); });

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Cancel upload"]')?.click());

    expect(send).not.toHaveBeenCalled();
    expect(host.textContent).toContain("screen.png");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(host.querySelector('[aria-live="polite"]')?.textContent).toBe("");
  });

  it("keeps reporting non-abort upload failures while preserving the draft", async () => {
    const attachmentPreparer = vi.fn().mockRejectedValue(new SessionControlError("network_unavailable"));
    const { send } = render({ attachmentPreparer });
    const input = host.querySelector<HTMLInputElement>('input[type="file"]');
    Object.defineProperty(input, "files", { configurable: true, value: [new File(["image"], "screen.png", { type: "image/png" })] });
    act(() => input?.dispatchEvent(new Event("change", { bubbles: true })));

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());

    expect(send).not.toHaveBeenCalled();
    expect(host.textContent).toContain("screen.png");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("session service is unavailable");
  });

  it("aborts attachment preparation when the composer unmounts", async () => {
    let uploadSignal: AbortSignal | undefined;
    const attachmentPreparer = vi.fn((_identity: IdentityConfig, _file: File, signal: AbortSignal) => new Promise<PreparedAttachment>((_resolve, reject) => {
      uploadSignal = signal;
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
    }));
    render({ attachmentPreparer });
    const input = host.querySelector<HTMLInputElement>('input[type="file"]');
    Object.defineProperty(input, "files", { configurable: true, value: [new File(["image"], "screen.png", { type: "image/png" })] });
    act(() => input?.dispatchEvent(new Event("change", { bubbles: true })));
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());
    await act(async () => { await Promise.resolve(); });

    await act(async () => { root.render(<div />); await Promise.resolve(); });

    expect(uploadSignal?.aborted).toBe(true);
  });

  it("reuses an idempotency key for an unchanged ambiguous retry", async () => {
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>()
      .mockRejectedValueOnce(new SessionControlError("network_unavailable"))
      .mockResolvedValueOnce({ operation: { id: "op-1", kind: "send", status: "completed", title: "", detail: "", createdAt: "" }, session: target, replayed: true });
    render({ onSend: send });
    setText("Retry me");

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());

    expect(send).toHaveBeenCalledTimes(2);
    expect(send.mock.calls[1][0].idempotencyKey).toBe(send.mock.calls[0][0].idempotencyKey);
  });

  it("generates a new idempotency key after any draft mutation", async () => {
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockRejectedValue(new SessionControlError("network_unavailable"));
    render({ onSend: send });
    setText("First draft");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());
    setText("Changed draft");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());

    expect(send.mock.calls[1][0].idempotencyKey).not.toBe(send.mock.calls[0][0].idempotencyKey);
  });

  it("allows a file-only draft and sends prepared attachment metadata", async () => {
    const attachment: PreparedAttachment = { attachment_id: "att-1", type: "image", media_type: "image/png", name: "screen.png", size_bytes: 5, sha256: "hash" };
    const attachmentPreparer = vi.fn().mockResolvedValue(attachment);
    const { send } = render({ attachmentPreparer });
    const input = host.querySelector<HTMLInputElement>('input[type="file"]');
    const file = new File(["image"], "screen.png", { type: "image/png" });
    Object.defineProperty(input, "files", { configurable: true, value: [file] });
    act(() => input?.dispatchEvent(new Event("change", { bubbles: true })));

    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());

    expect(send.mock.calls[0][0]).toMatchObject({ text: "", attachments: [attachment] });
  });

  it("rejects the target session when it is dropped as its own context", () => {
    const { send } = render();
    dropRef("tenant:alpha");
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(0);
    expect(send).not.toHaveBeenCalled();
  });

  it("reads back one immutable handoff and its send operation after submit", async () => {
    const client = createMockSessionControlClient();
    const onSent = vi.fn();
    render({ onSend: (input) => client.send(identity, input), onSent, sessionStatus: "idle" });
    setText("Use this context");
    dropRef("tenant:beta");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Send message"]')?.click());

    const readback = onSent.mock.calls[0][0] as SessionDetail;
    expect(readback.messages.filter((message) => message.kind === "handoff")).toHaveLength(1);
    expect(readback.messages.filter((message) => message.operation?.kind === "send")).toHaveLength(1);
  });

  it("restores text and source references after refresh without claiming file uploads", () => {
    render({ cwd: "/work/a" });
    setText("Continue with context");
    dropRef("tenant:beta");
    const file = new File(["image"], "unsaved.png", { type: "image/png" });
    transferEvent("drop", { files: [file] });
    expect(readComposerDraft({ identity, targetRef: "tenant:alpha", cwd: "/work/a" })).toEqual({ text: "Continue with context", sourceRefs: ["tenant:beta"] });
    expect([...storage.values()].join("")).not.toContain("unsaved.png");
    act(() => root.render(<div />));
    render({ cwd: "/work/a", sessionStatus: "running" });
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("Continue with context");
    expect(host.querySelectorAll(".webui2-context-chip")).toHaveLength(1);
    expect(host.querySelectorAll(".webui2-file-chip")).toHaveLength(0);
    expect(host.querySelector(".webui2-composer-send")).toBeNull();
    expect(host.querySelector<HTMLButtonElement>(".webui2-composer-stop")?.disabled).toBe(false);
  });

  it("isolates persisted drafts by user, API, session and workspace", () => {
    render({ cwd: "/work/a" });
    setText("Private draft");
    const scopes = [
      { cwd: "/work/b" },
      { identity: { ...identity, userId: "other-user" }, cwd: "/work/a" },
      { identity: { ...identity, apiBase: "/other-api" }, cwd: "/work/a" },
      { targetRef: "tenant:beta" as const, cwd: "/work/a" }
    ];
    for (const scope of scopes) {
      render(scope);
      expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
    }
    render({ cwd: "/work/a" });
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("Private draft");
  });

  it("clears persisted text and context only after successful delivery", async () => {
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockRejectedValueOnce(new SessionControlError("network_unavailable")).mockResolvedValueOnce({ operation: { id: "op", kind: "send", status: "completed", title: "", detail: "", createdAt: "" }, session: target, replayed: false });
    render({ onSend: send, sessionStatus: "idle" });
    setText("Keep until accepted");
    dropRef("tenant:beta");
    await act(async () => keyDown("Enter"));
    expect(readComposerDraft({ identity, targetRef: "tenant:alpha", cwd: "" })?.text).toBe("Keep until accepted");
    await act(async () => keyDown("Enter"));
    expect(readComposerDraft({ identity, targetRef: "tenant:alpha", cwd: "" })).toBeNull();
  });

  it("keeps all controls in one shell and opens the attachment menu with focus recovery", () => {
    render({ queueSettings: <button role="menuitem" type="button">Resume queue</button> });
    expect(host.querySelector(".webui2-composer-shell .webui2-composer-controls")).not.toBeNull();
    expect(host.querySelector(".webui2-context-picker")).toBeNull();
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-label="Add attachments and options"]')!;
    act(() => trigger.click());
    expect(document.activeElement?.getAttribute("aria-label")).toBe("Attach images");
    expect(host.querySelector('[role="menu"]')?.textContent).toContain("Resume queue");
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    expect(document.activeElement?.textContent).toBe("Resume queue");
    act(() => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("edits a full runtime snapshot, displays unknown facts and disables locked controls", () => {
    const onChange = vi.fn();
    const runtimeControls: ComposerRuntimeControls = { value: { provider: "provider-a", model: "model-a", permissionMode: "", effort: "", promptMode: "code" }, providerOptions: [{ value: "provider-a", label: "Provider A" }], modelOptions: [{ value: "model-a", label: "Model A" }, { value: "model-b", label: "Model B" }], locked: false, onChange, contextPercent: null, cacheHitPercent: null };
    render({ sessionStatus: "idle", runtimeControls });
    expect(host.textContent).toContain("Context -");
    expect(host.textContent).toContain("Hit -");
    expect(host.querySelector('button[aria-label="Permission mode"]')?.textContent).toContain("Default");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="Model"]')?.click());
    act(() => [...host.querySelectorAll<HTMLElement>('[role="option"]')].find((option) => option.textContent === "Model B")?.click());
    expect(onChange).toHaveBeenCalledWith({ ...runtimeControls.value, model: "model-b" });
    render({ runtimeControls: { ...runtimeControls, locked: true } });
    for (const label of ["Permission mode", "Provider", "Model", "Effort"]) expect(host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)?.disabled).toBe(true);
  });

  it("applies next-step suggestions as editable drafts without sending", () => {
    const { send } = render({ sessionStatus: "idle", nextStepSuggestions: ["Review the changes"] });
    act(() => host.querySelector<HTMLButtonElement>(".webui2-next-step-suggestions button")?.click());
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("Review the changes");
    expect(host.querySelector(".webui2-next-step-suggestions")).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });

  it("keeps retry configuration immutable and rotates the key when runtime configuration changes", async () => {
    const send = vi.fn<(input: SendSessionInput) => Promise<OperationResult>>().mockRejectedValue(new SessionControlError("network_unavailable"));
    const runtimeControls: ComposerRuntimeControls = { value: { provider: "provider-a", model: "model-a", permissionMode: "ask", effort: "high", promptMode: "code" }, providerOptions: [], modelOptions: [], locked: false, onChange: vi.fn(), contextPercent: null, cacheHitPercent: null };
    render({ onSend: send, sessionStatus: "idle", runtimeControls });
    setText("Retry this exact input");
    await act(async () => keyDown("Enter"));
    render({ onSend: send, sessionStatus: "idle", runtimeControls: { ...runtimeControls, value: { ...runtimeControls.value } } });
    await act(async () => keyDown("Enter"));
    expect(send.mock.calls[1][0]).toBe(send.mock.calls[0][0]);
    expect(send.mock.calls[0][0]).toMatchObject(runtimeControls.value);
    render({ onSend: send, sessionStatus: "idle", runtimeControls: { ...runtimeControls, value: { ...runtimeControls.value, model: "model-b" } } });
    await act(async () => keyDown("Enter"));
    expect(send.mock.calls[2][0].idempotencyKey).not.toBe(send.mock.calls[0][0].idempotencyKey);
    expect(send.mock.calls[2][0].model).toBe("model-b");
    expect(send.mock.calls[0][0].model).toBe("model-a");
  });

  it.each([400, 401, 403, 413])("never falls back to inline data for presign HTTP %s", async (status) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("Attachment rejected", { status }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(prepareSendInput({ identity, ref: "tenant:alpha", text: "", files: [new File(["image"], "screen.png", { type: "image/png" })], sourceRefs: [], idempotencyKey: "test", signal: new AbortController().signal })).rejects.toMatchObject({ status });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it.each(["missing-jwt", "unavailable-route", "placeholder-url"])("uses inline data for %s", async (condition) => {
    vi.stubGlobal("FileReader", class {
      result = "data:image/png;base64,aW1hZ2U=";
      onload?: () => void;
      readAsDataURL() { this.onload?.(); }
    });
    const fetchMock = vi.fn().mockResolvedValue(condition === "placeholder-url" ? new Response(JSON.stringify({ upload_url: "attachment://local-placeholder", attachment: { url: "attachment://local-placeholder" } }), { status: 200 }) : new Response("Not found", { status: 404 }));
    vi.stubGlobal("fetch", fetchMock);
    const input = await prepareSendInput({ identity: condition === "missing-jwt" ? { ...identity, mobileJwt: "" } : identity, ref: "tenant:alpha", text: "", files: [new File(["image"], "screen.png", { type: "image/png" })], sourceRefs: [], idempotencyKey: "test", signal: new AbortController().signal });
    expect(input.attachments[0]).toMatchObject({ type: "image", inline_data: "aW1hZ2U=", name: "screen.png" });
    expect(input.attachments[0].attachment_id).toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(condition === "missing-jwt" ? 0 : 1);
  });
});
