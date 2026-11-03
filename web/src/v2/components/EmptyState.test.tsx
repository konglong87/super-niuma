import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { EmptyState } from "./EmptyState";

describe("EmptyState", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key), clear: () => storage.clear() } });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("uses the niuma brand asset with localized Chinese copy and invokes New session", () => {
    const onCreateSession = vi.fn();
    storage.set("niuma-webui.language.v1", "zh");
    act(() => root.render(<I18nProvider><EmptyState onCreateSession={onCreateSession} /></I18nProvider>));
    const image = host.querySelector<HTMLImageElement>("img");
    const button = host.querySelector<HTMLButtonElement>("button");

    expect(image?.getAttribute("src")).toMatch(/^data:image\/svg\+xml/);
    expect(image?.getAttribute("alt")).toBe("Niuma 牛马机器人");
    expect(button?.textContent).toBe("新建会话");
    act(() => button?.click());
    expect(onCreateSession).toHaveBeenCalledTimes(1);
  });
});
