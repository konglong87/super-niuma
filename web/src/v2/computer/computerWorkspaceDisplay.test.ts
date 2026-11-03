import { describe, expect, it } from "vitest";
import { COMPUTER_WORKSPACE_DISPLAY_MODES } from "./computerWorkspacePreferences";
import { createComputerWorkspaceDisplayState, projectComputerWorkspaceDisplay, reduceComputerWorkspaceDisplay } from "./computerWorkspaceDisplay";

const sync = (state: ReturnType<typeof createComputerWorkspaceDisplayState>, input: Partial<Parameters<typeof reduceComputerWorkspaceDisplay>[1]> & { type: "sync" }) => reduceComputerWorkspaceDisplay(state, input as Parameters<typeof reduceComputerWorkspaceDisplay>[1]);

describe("computerWorkspaceDisplay", () => {
  it("keeps only a compact launcher while auto mode is idle", () => {
    const state = sync(createComputerWorkspaceDisplayState(COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO), { type: "sync", mode: "auto", sessionID: null, sessionState: "idle", hasError: true });
    expect(projectComputerWorkspaceDisplay(state)).toEqual({ visible: true, expanded: false, showError: false, showFeedback: false });
    const opened = reduceComputerWorkspaceDisplay(state, { type: "open" });
    expect(projectComputerWorkspaceDisplay(opened)).toEqual({ visible: true, expanded: true, showError: false, showFeedback: true });
  });

  it("reveals each new session and re-reveals a collapsed session on error/pause", () => {
    let state = createComputerWorkspaceDisplayState(COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO);
    state = sync(state, { type: "sync", mode: "auto", sessionID: "s1", sessionState: "ready", hasError: false });
    state = reduceComputerWorkspaceDisplay(state, { type: "toggle" });
    expect(projectComputerWorkspaceDisplay(state).visible).toBe(true);
    expect(projectComputerWorkspaceDisplay(state).expanded).toBe(false);
    state = sync(state, { type: "sync", mode: "auto", sessionID: "s1", sessionState: "paused", hasError: false });
    expect(projectComputerWorkspaceDisplay(state)).toEqual({ visible: true, expanded: true, showError: false, showFeedback: true });
    state = reduceComputerWorkspaceDisplay(state, { type: "toggle" });
    state = sync(state, { type: "sync", mode: "auto", sessionID: "s1", sessionState: "paused", hasError: true });
    expect(projectComputerWorkspaceDisplay(state)).toEqual({ visible: true, expanded: true, showError: true, showFeedback: true });
    state = sync(state, { type: "sync", mode: "auto", sessionID: "s1", sessionState: "stopped", hasError: false });
    expect(projectComputerWorkspaceDisplay(state).visible).toBe(true);
    expect(projectComputerWorkspaceDisplay(state).expanded).toBe(false);
  });

  it("resets temporary overrides when mode or session identity changes", () => {
    let state = createComputerWorkspaceDisplayState(COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT);
    state = reduceComputerWorkspaceDisplay(state, { type: "toggle" });
    expect(projectComputerWorkspaceDisplay(state).expanded).toBe(true);
    state = sync(state, { type: "sync", mode: "expanded", sessionID: null, sessionState: "idle", hasError: false });
    expect(projectComputerWorkspaceDisplay(state)).toEqual({ visible: true, expanded: true, showError: false, showFeedback: false });
    state = sync(state, { type: "sync", mode: "expanded", sessionID: "s2", sessionState: "ready", hasError: false });
    expect(projectComputerWorkspaceDisplay(state).expanded).toBe(true);
  });
});
