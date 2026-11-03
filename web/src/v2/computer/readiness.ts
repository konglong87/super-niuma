import type { ComputerCapabilities, ComputerPermissionTarget } from "./types";

export type ComputerPermissionGuideAction = {
  target: ComputerPermissionTarget;
  label: "Accessibility" | "Screen Recording";
  description: string;
};

const COMPUTER_PERMISSION_GUIDE_ACTIONS: readonly ComputerPermissionGuideAction[] = [
  { target: "accessibility", label: "Accessibility", description: "Allow Computer Use to control the desktop." },
  { target: "screen_capture", label: "Screen Recording", description: "Allow Computer Use to read the desktop." }
];

/** Returns only the macOS permissions that are explicitly missing. */
export function computerPermissionGuideActions(available: boolean, caps: ComputerCapabilities | null): ComputerPermissionGuideAction[] {
  if (!available || !caps) return [];
  return COMPUTER_PERMISSION_GUIDE_ACTIONS.filter((action) => {
    if (action.target === "accessibility") return caps.input_readiness === "permission_required";
    return caps.capture_readiness === "permission_required";
  });
}

/** Session approval is separate from OS permission/readiness. Fail closed on unknowns. */
export function computerReadinessError(available: boolean, caps: ComputerCapabilities | null): string | null {
  if (!available) return "Computer Use is unavailable on this host.";
  if (!caps) return "Checking Computer Use capabilities…";
  if (caps.capture_readiness !== "ready") return `Screen capture is not ready: ${caps.capture_readiness}.`;
  if (caps.input_readiness !== "ready") return `Desktop input is not ready: ${caps.input_readiness}.`;
  if (caps.permission_state !== "approved") return `System permission is not approved: ${caps.permission_state}.`;
  if (!caps.image_supported) return "Desktop images are not supported.";
  if (!caps.supports_stop) return "The backend must support Stop before a session can start.";
  return null;
}
