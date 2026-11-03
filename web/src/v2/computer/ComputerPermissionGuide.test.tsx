import { act } from "react";
import { I18nProvider } from "../../lib/i18n";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ComputerPermissionGuide } from "./ComputerPermissionGuide";
import type { ComputerCapabilities, ComputerPermissionTarget } from "./types";
import { capabilities } from "./testFixtures";

function withReadiness(overrides: Partial<ComputerCapabilities>): ComputerCapabilities {
  return { ...capabilities, ...overrides };
}

describe("ComputerPermissionGuide", () => {
  let root: Root;
  let container: HTMLDivElement;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    storage = new Map([["niuma-webui.language.v1", "en"]]);
    Object.defineProperty(window, "localStorage", { configurable: true, value: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => { storage.set(key, value); },
      removeItem: (key: string) => { storage.delete(key); },
      clear: () => { storage.clear(); },
    } });
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  const render = (nextCapabilities: ComputerCapabilities | null, options?: {
    available?: boolean;
    openPermissionSettings?: (target: ComputerPermissionTarget) => Promise<void>;
    onRecheck?: () => void | Promise<void>;
  }) => {
    const openPermissionSettings = options?.openPermissionSettings ?? vi.fn<(target: ComputerPermissionTarget) => Promise<void>>().mockResolvedValue(undefined);
    const onRecheck = options?.onRecheck ?? vi.fn<() => Promise<void>>().mockResolvedValue(undefined);
    act(() => root.render(<I18nProvider><ComputerPermissionGuide
      available={options?.available ?? true}
      capabilities={nextCapabilities}
      client={{ openPermissionSettings }}
      onRecheck={onRecheck}
    /></I18nProvider>));
    return { openPermissionSettings, onRecheck };
  };

  it("defaults Computer Use copy to Chinese without a language preference", () => {
    storage.delete("niuma-webui.language.v1");
    render(withReadiness({ input_readiness: "permission_required", capture_readiness: "permission_required" }));
    expect(container.textContent).toContain("电脑操作需要系统权限");
    expect(container.textContent).toContain("打开辅助功能设置");
    expect(container.textContent).not.toContain("Computer Use needs system permissions");
  });

  it("shows separate Accessibility and Screen Recording actions from readiness", () => {
    render(withReadiness({ input_readiness: "permission_required", capture_readiness: "permission_required" }));
    expect(container.textContent).toContain("Accessibility");
    expect(container.textContent).toContain("Screen Recording");
    expect(container.querySelectorAll(".webui2-computer-permission-action")).toHaveLength(2);
    expect(container.textContent).toContain("Open Accessibility settings");
    expect(container.textContent).toContain("Open Screen Recording settings");
  });

  it("only shows the action for the permission that is missing", () => {
    render(withReadiness({ input_readiness: "permission_required", capture_readiness: "ready" }));
    expect(container.textContent).toContain("Accessibility");
    expect(container.textContent).not.toContain("Screen Recording");
  });

  it("stays hidden when the host is unavailable or permissions are ready", () => {
    render(withReadiness({ input_readiness: "ready", capture_readiness: "ready" }));
    expect(container.innerHTML).toBe("");
    render(withReadiness({ input_readiness: "permission_required" }), { available: false });
    expect(container.innerHTML).toBe("");
  });

  it("calls the matching client action and the recheck callback", async () => {
    const { openPermissionSettings, onRecheck } = render(withReadiness({ input_readiness: "permission_required", capture_readiness: "permission_required" }));
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>("button"));
    await act(async () => { buttons.find((button) => button.textContent === "Open Accessibility settings")?.click(); });
    await act(async () => { buttons.find((button) => button.textContent === "Recheck permissions")?.click(); });
    expect(openPermissionSettings).toHaveBeenCalledWith("accessibility");
    expect(onRecheck).toHaveBeenCalledTimes(1);
  });

  it("surfaces settings and recheck errors", async () => {
    const openPermissionSettings = vi.fn().mockRejectedValue(new Error("settings unavailable"));
    const onRecheck = vi.fn().mockRejectedValue(new Error("capability check failed"));
    render(withReadiness({ input_readiness: "permission_required" }), { openPermissionSettings, onRecheck });
    await act(async () => { container.querySelector<HTMLButtonElement>('button')?.click(); });
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("settings unavailable");
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "Recheck permissions")?.click(); });
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("capability check failed");
  });
});
