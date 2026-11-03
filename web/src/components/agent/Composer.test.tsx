import { act, createRef, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Composer, resizeComposerTextarea } from "./Composer";
import { agentCopy } from "./copy";
import { I18nProvider } from "../../lib/i18n";
import { listPromptTemplates } from "../../lib/api";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  listPromptTemplates: vi.fn()
}));

function textareaWithScrollHeight(scrollHeight: number): HTMLTextAreaElement {
  const textarea = document.createElement("textarea");
  Object.defineProperty(textarea, "scrollHeight", { configurable: true, value: scrollHeight });
  return textarea;
}

describe("resizeComposerTextarea", () => {
  it("keeps an empty composer at the compact default height", () => {
    const textarea = textareaWithScrollHeight(20);
    resizeComposerTextarea(textarea);
    expect(textarea.style.height).toBe("36px");
    expect(textarea.style.overflowY).toBe("hidden");
  });

  it("grows with multiline content and shrinks after clearing", () => {
    const textarea = textareaWithScrollHeight(96);
    resizeComposerTextarea(textarea);
    expect(textarea.style.height).toBe("96px");
    textarea.style.height = "96px";
    Object.defineProperty(textarea, "scrollHeight", { configurable: true, value: 20 });
    resizeComposerTextarea(textarea);
    expect(textarea.style.height).toBe("36px");
  });

  it("caps long content and enables internal scrolling", () => {
    const textarea = textareaWithScrollHeight(320);
    resizeComposerTextarea(textarea);
    expect(textarea.style.height).toBe("176px");
    expect(textarea.style.overflowY).toBe("auto");
  });
});

describe("legacy Composer common prompts", () => {
  let root: Root;
  let props: ComponentProps<typeof Composer>;
  const prompt = { id: 1, title: "Review", content: "Review {{feature}}.", category: "Code", pinned: false, sort_order: 0 };

  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => null, setItem: vi.fn() } });
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function (this: HTMLDialogElement) { this.setAttribute("open", ""); } });
    Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: function (this: HTMLDialogElement) { this.removeAttribute("open"); } });
    vi.mocked(listPromptTemplates).mockResolvedValue([prompt]);
    props = {
      identity: { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "legacy", userId: "owner", deviceId: "desktop", model: "test" },
      copy: agentCopy.en, composerRef: createRef(), composerText: "",
      onComposerTextChange: vi.fn(), onComposerKeyDown: vi.fn(), onComposerPaste: vi.fn(),
      onImageDrop: vi.fn(), pendingImages: [], onAddImages: vi.fn(), onRemoveImage: vi.fn(),
      hasSelectedTask: true, slashCommands: [], slashSelected: 0, slashState: "idle", slashError: "",
      onSlashSelect: vi.fn(), onSlashApply: vi.fn(), nextStepSuggestions: [], onApplyNextStep: vi.fn(),
      pendingInputs: [], pendingInputQueueEnabled: true, onMovePendingInputUp: vi.fn(),
      onEditPendingInput: vi.fn(), onDirectionPendingInput: vi.fn(), onDeletePendingInput: vi.fn(),
      onRetryPendingInput: vi.fn(), onSideChatPendingInput: vi.fn(), onTogglePendingInputQueue: vi.fn(),
      permissionMode: "ask", onPermissionModeChange: vi.fn(), composerState: "ready", contextPercent: 0,
      cacheHitPercent: null, providerOptions: [], provider: "", model: "test", servedModels: ["test"],
      onChooseProvider: vi.fn(), onChooseModel: vi.fn(), effort: "medium", onEffortChange: vi.fn(),
      composerActionIsCancel: false, sendDisabled: true, onCancel: vi.fn(), onSend: vi.fn()
    };
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.restoreAllMocks();
    vi.useRealTimers();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render() {
    act(() => root.render(<I18nProvider><main className="web-agent-page"><Composer {...props} /></main></I18nProvider>));
  }

  async function open() {
    act(() => document.querySelector<HTMLButtonElement>('[aria-label="Common prompts"]')!.click());
    await act(async () => { await vi.advanceTimersByTimeAsync(200); });
  }

  it.each(["", "  existing draft\n  "])("fills or appends without changing existing whitespace or sending (%j)", async (draft) => {
    props.composerText = draft;
    render();
    expect(document.querySelector('[aria-label="Common prompts"]')?.textContent).toBe("");
    await open();
    expect(document.querySelector(".web-agent-page > dialog[open]")).not.toBeNull();
    expect(document.querySelector(".agent-composer dialog")).toBeNull();
    expect(listPromptTemplates).toHaveBeenCalledWith(props.identity, "");
    act(() => document.querySelector<HTMLButtonElement>('[aria-label="Use Review"]')!.click());
    const expected = draft ? `${draft}\n\n${prompt.content}` : prompt.content;
    expect(props.onComposerTextChange).toHaveBeenCalledExactlyOnceWith(expected);
    props.composerText = expected;
    render();
    expect(document.querySelector("textarea")?.value).toBe(expected);
    expect(props.onSend).not.toHaveBeenCalled();
  });

  it("retains the draft when loading fails and supports retry", async () => {
    props.composerText = "Keep me";
    vi.mocked(listPromptTemplates).mockRejectedValueOnce(new Error("offline"));
    render();
    await open();
    expect(document.querySelector('[role="alert"]')?.textContent).toBe("offline");
    expect(document.querySelector("textarea")?.value).toBe("Keep me");
    act(() => Array.from(document.querySelectorAll("button")).find((button) => button.textContent === "Retry")!.click());
    await act(async () => { await vi.advanceTimersByTimeAsync(200); });
    expect(document.querySelector('[aria-label="Use Review"]')).not.toBeNull();
    expect(props.onComposerTextChange).not.toHaveBeenCalled();
    expect(props.onSend).not.toHaveBeenCalled();
  });

  it("keeps management available without a session and enables insertion only after selecting one", async () => {
    props.hasSelectedTask = false;
    render();
    await open();
    const usePrompt = () => document.querySelector<HTMLButtonElement>('[aria-label="Use Review"]')!;
    expect(usePrompt().disabled).toBe(true);
    expect(usePrompt().getAttribute("aria-description")).toBe(props.copy.selectSessionPlaceholder);
    act(() => usePrompt().click());
    expect(props.onComposerTextChange).not.toHaveBeenCalled();
    expect(document.querySelector("dialog[open]")).not.toBeNull();
    act(() => document.querySelector<HTMLButtonElement>('[aria-label="Edit"]')!.click());
    expect(document.querySelector(".prompt-picker-editor")).not.toBeNull();
    act(() => document.querySelector<HTMLButtonElement>('[aria-label="Back to list"]')!.click());

    props.hasSelectedTask = true;
    render();
    expect(usePrompt().disabled).toBe(false);
    expect(usePrompt().hasAttribute("aria-description")).toBe(false);
    act(() => usePrompt().click());
    expect(props.onComposerTextChange).toHaveBeenCalledExactlyOnceWith(prompt.content);
    expect(props.onSend).not.toHaveBeenCalled();
  });

  it("blocks insertion if the selected session disappears while the picker is open", async () => {
    render();
    await open();
    props.hasSelectedTask = false;
    render();
    const usePrompt = document.querySelector<HTMLButtonElement>('[aria-label="Use Review"]')!;
    expect(usePrompt.disabled).toBe(true);
    act(() => usePrompt.click());
    expect(props.onComposerTextChange).not.toHaveBeenCalled();
    expect(props.onSend).not.toHaveBeenCalled();
  });

  it.each(["sending", "cancelling"] as const)("disables selection while %s", async (state) => {
    props.composerState = state;
    render();
    await open();
    expect(document.querySelector("dialog")).toBeNull();
    expect(listPromptTemplates).not.toHaveBeenCalled();
  });

  it("allows preparing the next draft while a run is active", async () => {
    props.composerState = "running";
    render();
    await open();
    expect(document.querySelector('[aria-label="Use Review"]')).not.toBeNull();
  });
});
