import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { ComputerExecutionProgress } from "./ComputerExecutionProgress";
import type { ComputerActionReceipt } from "./types";

const languageStorageKey = "niuma-webui.language.v1";

const receipt = (id: string, summary: string, outcome: ComputerActionReceipt["outcome"] = "executed"): ComputerActionReceipt => ({
  action_id: id,
  session_id: "s1",
  platform: "macos",
  backend: "native_host",
  outcome,
  verification: outcome === "executed" ? "passed" : "failed",
  focus_before: "focused",
  focus_after: "focused",
  redacted_action_summary: summary,
  completed_at: "2026-09-24T08:30:00Z",
});

describe("ComputerExecutionProgress", () => {
  let root: Root;
  let container: HTMLDivElement;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", { configurable: true, value: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
      clear: () => storage.clear(),
    } });
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render(props: Partial<Parameters<typeof ComputerExecutionProgress>[0]> = {}, language: "en" | "zh" = "en"): void {
    window.localStorage.setItem(languageStorageKey, language);
    act(() => root.render(<I18nProvider><ComputerExecutionProgress state="ready" receipts={[]} {...props} /></I18nProvider>));
  }

  it("renders the English progress copy and English pluralization", () => {
    render({
      loading: true,
      receipts: [receipt("a1", "Open settings")],
    });

    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).not.toBeNull();
    expect(document.body.textContent).toContain("LIVE EXECUTION");
    expect(document.body.textContent).toContain("Computer Use progress");
    expect(document.body.textContent).toContain("Working");
    expect(document.body.textContent).toContain("Running the current Computer Use step…");
    expect(document.body.textContent).toContain("1 step completed");
    expect(document.body.textContent).toContain("Recent activity");
    expect(document.body.textContent).toContain("executed · passed");
    expect(document.body.querySelector('[data-drag-handle="execution-progress"]')?.getAttribute("aria-label")).toBe("Drag Computer Use execution progress");
    expect(document.body.querySelector('button[title="Collapse"]')).not.toBeNull();
  });

  it("uses Chinese copy from the zh language context for labels, status, aria text, and pluralization", () => {
    render({
      loading: true,
      receipts: [receipt("a1", "打开设置"), receipt("a2", "点击权限开关")],
    }, "zh");

    expect(document.body.querySelector('[aria-label="电脑操作进度"]')).not.toBeNull();
    expect(document.body.textContent).toContain("实时执行");
    expect(document.body.textContent).toContain("电脑操作进度");
    expect(document.body.textContent).toContain("执行中");
    expect(document.body.textContent).toContain("正在执行当前电脑操作步骤…");
    expect(document.body.textContent).toContain("已完成 2 个步骤");
    expect(document.body.textContent).toContain("最近活动");
    expect(document.body.textContent).toContain("已执行 · 通过");
    expect(document.body.querySelector('[data-drag-handle="execution-progress"]')?.getAttribute("aria-label")).toBe("拖动电脑操作执行进度");
    expect(document.body.querySelector('button[title="收起"]')).not.toBeNull();
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).toBeNull();
  });

  it("localizes the empty state and non-success receipt statuses in Chinese", () => {
    render({
      state: "failed",
      receipts: [receipt("a1", "", "rejected")],
      error: null,
    }, "zh");

    expect(document.body.textContent).toContain("需要注意");
    expect(document.body.textContent).toContain("电脑操作会话失败。");
    expect(document.body.textContent).toContain("电脑操作");
    expect(document.body.textContent).toContain("已拒绝 · 失败");

    render({}, "zh");
    expect(document.body.textContent).toContain("尚未记录操作步骤。");
    expect(document.body.textContent).not.toContain("No action steps have been recorded yet.");
  });

  it("toggles between expanded and collapsed states and reports the change", () => {
    const onCollapsedChange = vi.fn();
    render({ onCollapsedChange });

    const collapse = document.body.querySelector<HTMLButtonElement>('[aria-label="Close Computer Use progress"]');
    expect(collapse).not.toBeNull();
    act(() => collapse?.click());
    expect(document.body.querySelector('[aria-label="Open Computer Use progress"]')).not.toBeNull();
    expect(onCollapsedChange).toHaveBeenCalledWith(true);

    const launcher = document.body.querySelector<HTMLButtonElement>('[aria-label="Open Computer Use progress"]');
    act(() => launcher?.click());
    expect(document.body.querySelector('[aria-label="Computer Use progress"]')).not.toBeNull();
    expect(onCollapsedChange).toHaveBeenLastCalledWith(false);
  });

  it("keeps the drag handle keyboard reachable and moves the floating panel with pointer input", () => {
    render({ receipts: [receipt("a1", "Move cursor")] });
    const panel = document.body.querySelector<HTMLElement>('[aria-label="Computer Use progress"]');
    const handle = document.body.querySelector<HTMLElement>('[data-drag-handle="execution-progress"]');
    expect(panel).not.toBeNull();
    expect(handle?.tagName).toBe("BUTTON");
    expect((handle as HTMLButtonElement | null)?.tabIndex).toBe(0);

    Object.defineProperty(panel, "getBoundingClientRect", {
      configurable: true,
      value: () => ({ left: 100, top: 100, width: 340, height: 220, right: 440, bottom: 320 }),
    });
    const pointer = (type: string, values: Record<string, number>): Event => {
      const event = new Event(type, { bubbles: true });
      for (const [key, value] of Object.entries(values)) Object.defineProperty(event, key, { configurable: true, value });
      return event;
    };

    act(() => handle?.dispatchEvent(pointer("pointerdown", { button: 0, pointerId: 7, clientX: 120, clientY: 120 })));
    act(() => handle?.dispatchEvent(pointer("pointermove", { pointerId: 7, clientX: 180, clientY: 155 })));
    expect(panel?.style.left).toBe("160px");
    expect(panel?.style.top).toBe("135px");
    act(() => handle?.dispatchEvent(pointer("pointerup", { pointerId: 7, clientX: 180, clientY: 155 })));
    expect(panel?.getAttribute("data-dragging")).toBe("false");
  });
});
