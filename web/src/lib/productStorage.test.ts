import { beforeEach, describe, expect, it } from "vitest";
import { readProductStorage } from "./productStorage";

describe("product storage isolation", () => {
  beforeEach(() => {
    const storage = new Map<string, string>();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
        clear: () => storage.clear()
      }
    });
  });

  it("reads the canonical Niuma key", () => {
    window.localStorage.setItem("niuma-webui.language.v1", "zh");
    expect(readProductStorage("niuma-webui.language.v1")).toBe("zh");
  });

  it("does not import another product's browser data", () => {
    window.localStorage.setItem("go-claude-webui.language.v1", "zh");
    expect(readProductStorage("niuma-webui.language.v1")).toBeNull();
    expect(window.localStorage.getItem("niuma-webui.language.v1")).toBeNull();
  });
});
