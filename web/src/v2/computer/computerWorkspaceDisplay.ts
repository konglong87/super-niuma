import type { ComputerSessionState } from "./types";
import type { ComputerWorkspaceDisplayMode } from "./computerWorkspacePreferences";

export type ComputerWorkspaceDisplayAction =
  | { type: "sync"; mode: ComputerWorkspaceDisplayMode; sessionID: string | null; sessionState: ComputerSessionState | "idle"; hasError: boolean }
  | { type: "open" }
  | { type: "attempt" }
  | { type: "toggle" };

export type ComputerWorkspaceDisplayState = {
  mode: ComputerWorkspaceDisplayMode;
  sessionID: string | null;
  sessionState: ComputerSessionState | "idle";
  hasError: boolean;
  attempted: boolean;
  opened: boolean;
  manualOverride: "collapsed" | "expanded" | null;
};

export type ComputerWorkspaceDisplayProjection = {
  visible: boolean;
  expanded: boolean;
  showError: boolean;
  showFeedback: boolean;
};

const AUTO_REVEAL_STATES = new Set<ComputerSessionState>(["pending_approval", "ready", "paused", "needs_observation", "failed"]);

function isStopped(state: ComputerSessionState | "idle"): boolean {
  return state === "stopped";
}

function isActive(state: ComputerSessionState | "idle"): boolean {
  return state !== "idle" && state !== "stopped";
}

function isAutoRevealTransition(previous: ComputerWorkspaceDisplayState, next: ComputerWorkspaceDisplayState): boolean {
  return (next.sessionID !== previous.sessionID && next.sessionID !== null)
    || (next.hasError && !previous.hasError && (next.sessionID !== null || next.attempted))
    || (AUTO_REVEAL_STATES.has(next.sessionState as ComputerSessionState) && next.sessionState !== previous.sessionState);
}

function applySync(previous: ComputerWorkspaceDisplayState, action: Extract<ComputerWorkspaceDisplayAction, { type: "sync" }>): ComputerWorkspaceDisplayState {
  const modeChanged = action.mode !== previous.mode;
  const sessionChanged = action.sessionID !== previous.sessionID;
  const stopped = isStopped(action.sessionState);
  const next: ComputerWorkspaceDisplayState = {
    mode: action.mode,
    sessionID: action.sessionID,
    sessionState: action.sessionState,
    hasError: action.hasError,
    attempted: sessionChanged && action.sessionID !== null ? true : stopped ? false : previous.attempted,
    opened: modeChanged || sessionChanged ? false : previous.opened,
    manualOverride: modeChanged || sessionChanged || stopped ? null : previous.manualOverride,
  };

  if (action.mode === "auto" && !stopped && isAutoRevealTransition(previous, next)) {
    return { ...next, opened: true, manualOverride: null };
  }
  return next;
}

export function createComputerWorkspaceDisplayState(mode: ComputerWorkspaceDisplayMode): ComputerWorkspaceDisplayState {
  return { mode, sessionID: null, sessionState: "idle", hasError: false, attempted: false, opened: false, manualOverride: null };
}

export function reduceComputerWorkspaceDisplay(
  state: ComputerWorkspaceDisplayState,
  action: ComputerWorkspaceDisplayAction,
): ComputerWorkspaceDisplayState {
  switch (action.type) {
    case "sync":
      return applySync(state, action);
    case "open":
      return { ...state, opened: true, manualOverride: "expanded" };
    case "attempt":
      return { ...state, attempted: true, opened: true, manualOverride: "expanded" };
    case "toggle": {
      const projection = projectComputerWorkspaceDisplay(state);
      return {
        ...state,
        opened: true,
        manualOverride: projection.expanded ? "collapsed" : "expanded",
      };
    }
  }
}

export function projectComputerWorkspaceDisplay(state: ComputerWorkspaceDisplayState): ComputerWorkspaceDisplayProjection {
  const stopped = isStopped(state.sessionState);
  const active = isActive(state.sessionState);
  const showError = state.hasError && (state.attempted || active);
  const overrideCollapsed = state.manualOverride === "collapsed";
  const overrideExpanded = state.manualOverride === "expanded";

  if (state.mode === "auto") {
    // Keep a small launcher available while idle/stopped so a user can start a
    // local preview; only the detailed surface is automatically hidden.
    const launcher = !overrideCollapsed && !active && !showError;
    const visible = launcher || active || showError || state.opened;
    return { visible, expanded: visible && (showError || (active && !overrideCollapsed) || overrideExpanded), showError, showFeedback: state.opened || state.attempted || active || showError };
  }

  const visible = true;
  const expandedByMode = state.mode === "expanded";
  return { visible, expanded: visible && (overrideExpanded || (expandedByMode && !overrideCollapsed)), showError, showFeedback: state.opened || state.attempted || active || showError };
}
