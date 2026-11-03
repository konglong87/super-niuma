import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { DEFAULT_PET } from "../settings/globalVisualSettings";
import { DesktopPet } from "./DesktopPet";
import { PET_DEVICE_PREFERENCES_KEY } from "./petDevicePreferences";

vi.mock("./PetScene", () => ({
  PetScene: () => <div data-testid="pet-scene" />
}));

const settings = { ...DEFAULT_PET, enabled: true, visible: true };

function pointerEvent(type: string, x: number, y: number): Event {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clientX", { value: x });
  Object.defineProperty(event, "clientY", { value: y });
  Object.defineProperty(event, "button", { value: 0 });
  Object.defineProperty(event, "pointerId", { value: 1 });
  return event;
}

describe("DesktopPet", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
        clear: () => storage.clear()
      }
    });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
      width: 150, height: 170, left: 1000, top: 560, right: 1150, bottom: 730, x: 1000, y: 560,
      toJSON: () => ({})
    } as DOMRect);
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.restoreAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render(onOpenSettings = vi.fn()): void {
    act(() => {
      root.render(<I18nProvider><DesktopPet settings={settings} status="idle" onOpenSettings={onOpenSettings} /></I18nProvider>);
    });
  }

  function pet(): HTMLElement {
    const element = host.querySelector<HTMLElement>(".webui2-desktop-pet");
    expect(element).not.toBeNull();
    return element!;
  }

  function menu(): HTMLElement {
    const element = document.body.querySelector<HTMLElement>("[role='menu']");
    expect(element).not.toBeNull();
    return element!;
  }

  function click(element: HTMLElement): void {
    act(() => {
      element.dispatchEvent(pointerEvent("pointerdown", 1050, 600));
      element.dispatchEvent(pointerEvent("pointerup", 1050, 600));
    });
  }

  it("opens the menu on click and closes the pet on this device", () => {
    render();
    click(pet());
    const menuElement = menu();
    expect(menuElement.querySelectorAll("[role='menuitem']")).toHaveLength(3);
    const close = [...menuElement.querySelectorAll<HTMLElement>("[role='menuitem']")].find((item) => item.textContent === "Close pet")!;
    act(() => close.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(host.querySelector(".webui2-desktop-pet")).toBeNull();
    expect(JSON.parse(storage.get(PET_DEVICE_PREFERENCES_KEY)!)).toMatchObject({ hidden: true });
  });

  it("drags freely, clamps to the viewport, and persists a normalized position", () => {
    render();
    const element = pet();
    act(() => {
      element.dispatchEvent(pointerEvent("pointerdown", 1050, 600));
      element.dispatchEvent(pointerEvent("pointermove", 700, 300));
      element.dispatchEvent(pointerEvent("pointerup", 700, 300));
    });
    expect(parseFloat(element.style.left)).toBeCloseTo(650, 0);
    expect(parseFloat(element.style.top)).toBeCloseTo(260, 0);
    expect(host.querySelector("[role='menu']")).toBeNull();
    expect(JSON.parse(storage.get(PET_DEVICE_PREFERENCES_KEY)!)).toEqual({
      hidden: false,
      position: { x: 0.748, y: 0.433 }
    });
  });

  it("ignores tiny pointer jitter as a click, not a drag", () => {
    render();
    const element = pet();
    act(() => {
      element.dispatchEvent(pointerEvent("pointerdown", 1050, 600));
      element.dispatchEvent(pointerEvent("pointermove", 1052, 601));
      element.dispatchEvent(pointerEvent("pointerup", 1052, 601));
    });
    expect(storage.get(PET_DEVICE_PREFERENCES_KEY)).toBeUndefined();
    expect(document.body.querySelector("[role='menu']")).not.toBeNull();
  });

  it("resets to the default position and opens pet settings from the menu", () => {
    storage.set(PET_DEVICE_PREFERENCES_KEY, JSON.stringify({ hidden: false, position: { x: 0.1, y: 0.2 } }));
    const onOpenSettings = vi.fn();
    render(onOpenSettings);
    click(pet());
    const items = [...document.body.querySelectorAll<HTMLElement>("[role='menuitem']")];
    act(() => items.find((item) => item.textContent === "Reset position")!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(JSON.parse(storage.get(PET_DEVICE_PREFERENCES_KEY)!)).toEqual({ hidden: false, position: null });
    click(pet());
    const reopened = [...document.body.querySelectorAll<HTMLElement>("[role='menuitem']")];
    act(() => reopened.find((item) => item.textContent === "Pet settings")!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(onOpenSettings).toHaveBeenCalledTimes(1);
    expect(document.body.querySelector("[role='menu']")).toBeNull();
  });

  it("stays hidden when the device preference hides it", () => {
    storage.set(PET_DEVICE_PREFERENCES_KEY, JSON.stringify({ hidden: true, position: null }));
    render();
    expect(host.querySelector(".webui2-desktop-pet")).toBeNull();
  });
});
