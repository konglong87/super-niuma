import { beforeEach, describe, expect, it } from "vitest";
import {
  COMPUTER_WORKSPACE_DISPLAY_MODES,
  COMPUTER_WORKSPACE_PREFERENCES_KEY,
  computerWorkspaceCollapsedFromDisplayMode,
  computerWorkspaceDisplayModeFromCollapsed,
  loadComputerWorkspacePreferences,
  normalizeComputerWorkspacePreferences,
  parseComputerWorkspacePreferences,
  saveComputerWorkspacePreferences,
} from "./computerWorkspacePreferences";

describe("computerWorkspacePreferences", () => {
  let storage: Record<string, string>;

  beforeEach(() => {
    storage = {};
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage[key] ?? null,
        setItem: (key: string, value: string) => { storage[key] = value; },
      },
    });
  });

  it("defaults to auto with a compact legacy projection", () => {
    expect(loadComputerWorkspacePreferences()).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO,
      collapsed: true,
      position: null,
    });
  });

  it("migrates the legacy collapsed field to an explicit display mode", () => {
    expect(normalizeComputerWorkspacePreferences({ collapsed: false, position: { x: 10, y: 20 } })).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED,
      collapsed: false,
      position: { x: 10, y: 20 },
    });
    expect(parseComputerWorkspacePreferences(JSON.stringify({ collapsed: true, position: null }))).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT,
      collapsed: true,
      position: null,
    });
  });

  it("falls back for invalid display modes and malformed persisted data", () => {
    expect(normalizeComputerWorkspacePreferences({ displayMode: "fullscreen", collapsed: false })).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO,
      collapsed: true,
      position: null,
    });
    expect(parseComputerWorkspacePreferences(JSON.stringify({ displayMode: "invalid" }))).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO,
      collapsed: true,
      position: null,
    });
    expect(parseComputerWorkspacePreferences("not-json")).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO,
      collapsed: true,
      position: null,
    });
  });

  it("saves and reads the canonical display mode preference", () => {
    saveComputerWorkspacePreferences({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED,
      collapsed: true,
      position: { x: 100, y: 200 },
    });

    expect(JSON.parse(storage[COMPUTER_WORKSPACE_PREFERENCES_KEY])).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED,
      collapsed: false,
      position: { x: 100, y: 200 },
    });
    expect(loadComputerWorkspacePreferences()).toEqual({
      displayMode: COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED,
      collapsed: false,
      position: { x: 100, y: 200 },
    });
  });

  it("keeps the pure display-mode compatibility conversions deterministic", () => {
    expect(computerWorkspaceDisplayModeFromCollapsed(true)).toBe(COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT);
    expect(computerWorkspaceDisplayModeFromCollapsed(false)).toBe(COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED);
    expect(computerWorkspaceCollapsedFromDisplayMode(COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO)).toBe(true);
    expect(computerWorkspaceCollapsedFromDisplayMode(COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT)).toBe(true);
    expect(computerWorkspaceCollapsedFromDisplayMode(COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED)).toBe(false);
  });
});
