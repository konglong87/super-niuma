import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { ComputerPreview } from "./ComputerPreview";
import { capabilities, observationResponse } from "./testFixtures";
import type { ComputerObservation } from "./types";

const languageStorageKey = "niuma-webui.language.v1";

const observation = (activeWindow: ComputerObservation["active_window"]): ComputerObservation => ({
  ...observationResponse.observation,
  active_window: activeWindow,
  image_data: observationResponse.image_data,
  media_type: observationResponse.media_type,
});

describe("ComputerPreview", () => {
  let root: Root;
  let container: HTMLDivElement;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map([[languageStorageKey, "en"]]);
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

  function render(nextObservation: ComputerObservation | null, language: "en" | "zh" = "en"): void {
    storage.set(languageStorageKey, language);
    act(() => root.render(<I18nProvider><ComputerPreview observation={nextObservation} capabilities={capabilities} /></I18nProvider>));
  }

  it.each([
    ["the niuma bundle id", { bundle_id: "com.wails.niuma" }],
    ["the exact niuma title", { title: "niuma" }],
  ])("hides image data for controller window observations identified by %s", (_match, activeWindow) => {
    render(observation(activeWindow));

    expect(container.querySelector("img")).toBeNull();
    expect(container.textContent).toContain("Controller window preview hidden");
    expect(container.textContent).toContain("The controller window preview is hidden to avoid a recursive overlay.");
    expect(container.innerHTML).not.toContain(observationResponse.image_data!);
  });

  it("keeps image data visible for non-controller windows and non-exact titles", () => {
    render(observation({ bundle_id: "com.example.other", title: "niuma preview" }));

    expect(container.querySelector("img")?.getAttribute("src")).toBe(`data:image/png;base64,${observationResponse.image_data}`);
    expect(container.textContent).not.toContain("Controller window preview hidden");
  });

  it("localizes the recursive-overlay notice", () => {
    render(observation({ title: "niuma" }), "zh");

    expect(container.textContent).toContain("已隐藏控制器窗口预览");
    expect(container.textContent).toContain("为避免递归叠加，已隐藏控制器窗口预览。");
    expect(container.textContent).not.toContain("Controller window preview hidden");
  });
});
