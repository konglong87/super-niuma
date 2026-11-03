import { beforeEach, describe, expect, it } from "vitest";
import {
  PET_DEVICE_PREFERENCES_KEY,
  clampPetTopLeft,
  denormalizePetPosition,
  loadPetDevicePreferences,
  normalizePetPosition,
  savePetDevicePreferences
} from "./petDevicePreferences";

describe("pet device preferences", () => {
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

  it("round trips a normalized position and hidden state", () => {
    savePetDevicePreferences({ hidden: true, position: { x: 0.25, y: 0.75 } });
    expect(loadPetDevicePreferences()).toEqual({ hidden: true, position: { x: 0.25, y: 0.75 } });
  });

  it("rejects malformed storage without throwing", () => {
    localStorage.setItem(PET_DEVICE_PREFERENCES_KEY, "not json");
    expect(loadPetDevicePreferences()).toEqual({ hidden: false, position: null });
    localStorage.setItem(PET_DEVICE_PREFERENCES_KEY, JSON.stringify({ hidden: "yes", position: { x: -4, y: 8 } }));
    expect(loadPetDevicePreferences()).toEqual({ hidden: false, position: null });
  });

  it("converts positions across viewport sizes and clamps the pet inside the visible area", () => {
    const viewport = { width: 1000, height: 700 };
    const pet = { width: 150, height: 170 };
    expect(clampPetTopLeft({ x: -20, y: 900 }, viewport, pet)).toEqual({ x: 8, y: 522 });
    const normalized = normalizePetPosition({ x: 425, y: 265 }, viewport, pet);
    expect(normalized).toEqual({ x: 0.5, y: 0.5 });
    expect(denormalizePetPosition(normalized, { width: 1440, height: 900 }, pet)).toEqual({ x: 645, y: 365 });
  });
});
