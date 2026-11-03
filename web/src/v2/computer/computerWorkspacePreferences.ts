export const COMPUTER_WORKSPACE_PREFERENCES_KEY = "niuma.computer-workspace.v1";
export const COMPUTER_WORKSPACE_VIEWPORT_MARGIN = 12;
export const COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT = "niuma:computer-workspace-preferences-changed";

export const COMPUTER_WORKSPACE_DISPLAY_MODES = {
  AUTO: "auto",
  COMPACT: "compact",
  EXPANDED: "expanded",
} as const;

export type ComputerWorkspaceDisplayMode =
  (typeof COMPUTER_WORKSPACE_DISPLAY_MODES)[keyof typeof COMPUTER_WORKSPACE_DISPLAY_MODES];

export type ComputerWorkspacePoint = { x: number; y: number };
export type ComputerWorkspacePreferences = {
  displayMode: ComputerWorkspaceDisplayMode;
  /** @deprecated Use displayMode. Kept as a compatibility projection for existing consumers. */
  collapsed: boolean;
  position: ComputerWorkspacePoint | null;
};

export const DEFAULT_COMPUTER_WORKSPACE_DISPLAY_MODE: ComputerWorkspaceDisplayMode = COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO;

const DEFAULT_PREFERENCES: ComputerWorkspacePreferences = {
  displayMode: DEFAULT_COMPUTER_WORKSPACE_DISPLAY_MODE,
  // The legacy UI treats `collapsed` as the compact/minimized representation.
  collapsed: true,
  position: null,
};

export function isComputerWorkspaceDisplayMode(value: unknown): value is ComputerWorkspaceDisplayMode {
  return value === COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO
    || value === COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT
    || value === COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED;
}

export function computerWorkspaceDisplayModeFromCollapsed(collapsed: boolean): ComputerWorkspaceDisplayMode {
  return collapsed
    ? COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT
    : COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED;
}

/**
 * Projects the new preference into the old boolean shape until UI consumers migrate.
 * `auto` uses the compact projection so the legacy workspace stays unobtrusive by default.
 */
export function computerWorkspaceCollapsedFromDisplayMode(displayMode: ComputerWorkspaceDisplayMode): boolean {
  return displayMode !== COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED;
}

function finitePoint(value: unknown): value is ComputerWorkspacePoint {
  if (!value || typeof value !== "object") return false;
  const point = value as Record<string, unknown>;
  return typeof point.x === "number" && Number.isFinite(point.x)
    && typeof point.y === "number" && Number.isFinite(point.y);
}

function clonePoint(value: ComputerWorkspacePoint | null): ComputerWorkspacePoint | null {
  return value ? { x: value.x, y: value.y } : null;
}

/**
 * Normalizes persisted data without touching browser APIs. Old records that only
 * contain `collapsed` are migrated to the equivalent explicit display mode.
 */
export function normalizeComputerWorkspacePreferences(value: unknown): ComputerWorkspacePreferences {
  if (!value || typeof value !== "object") return { ...DEFAULT_PREFERENCES };
  const record = value as Record<string, unknown>;
  const hasDisplayMode = Object.prototype.hasOwnProperty.call(record, "displayMode");
  const displayMode = hasDisplayMode
    ? isComputerWorkspaceDisplayMode(record.displayMode)
      ? record.displayMode
      : DEFAULT_COMPUTER_WORKSPACE_DISPLAY_MODE
    : typeof record.collapsed === "boolean"
      ? computerWorkspaceDisplayModeFromCollapsed(record.collapsed)
      : DEFAULT_COMPUTER_WORKSPACE_DISPLAY_MODE;

  return {
    displayMode,
    collapsed: computerWorkspaceCollapsedFromDisplayMode(displayMode),
    position: finitePoint(record.position) ? clonePoint(record.position) : null,
  };
}

export function parseComputerWorkspacePreferences(raw: string | null): ComputerWorkspacePreferences {
  if (!raw) return { ...DEFAULT_PREFERENCES };
  try {
    return normalizeComputerWorkspacePreferences(JSON.parse(raw));
  } catch {
    return { ...DEFAULT_PREFERENCES };
  }
}

export function clampComputerWorkspacePosition(
  point: ComputerWorkspacePoint,
  viewport: ComputerWorkspacePoint,
  size: ComputerWorkspacePoint,
): ComputerWorkspacePoint {
  const maxX = Math.max(COMPUTER_WORKSPACE_VIEWPORT_MARGIN, viewport.x - size.x - COMPUTER_WORKSPACE_VIEWPORT_MARGIN);
  const maxY = Math.max(COMPUTER_WORKSPACE_VIEWPORT_MARGIN, viewport.y - size.y - COMPUTER_WORKSPACE_VIEWPORT_MARGIN);
  return {
    x: Math.min(maxX, Math.max(COMPUTER_WORKSPACE_VIEWPORT_MARGIN, point.x)),
    y: Math.min(maxY, Math.max(COMPUTER_WORKSPACE_VIEWPORT_MARGIN, point.y)),
  };
}

export function loadComputerWorkspacePreferences(): ComputerWorkspacePreferences {
  try {
    return parseComputerWorkspacePreferences(window.localStorage.getItem(COMPUTER_WORKSPACE_PREFERENCES_KEY));
  } catch {
    return { ...DEFAULT_PREFERENCES };
  }
}

export function saveComputerWorkspacePreferences(preferences: ComputerWorkspacePreferences): void {
  try {
    const normalized = normalizeComputerWorkspacePreferences(preferences);
    window.localStorage.setItem(COMPUTER_WORKSPACE_PREFERENCES_KEY, JSON.stringify(normalized));
  } catch {
    // Floating controls remain usable when storage is unavailable.
  }
}
