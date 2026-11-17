import { getDesktopServiceBridge, type ControlRun, type SuperPlatformBridge } from "../desktopServiceBridge";

export const STOPPED_CONFIRMATION = "ALL_WORKERS_AND_SENDERS_CONFIRMED_STOPPED";

export const CONTROL_STATUS = {
  queued: "queued", running: "running", verifying: "verifying", awaitingApproval: "awaiting_approval",
  deliveryPending: "delivery_pending", delivered: "delivered", cancelling: "cancelling", cancelled: "cancelled",
  failed: "failed", needsAttention: "needs_attention", unknown: "unknown", succeeded: "succeeded",
} as const;
const TERMINAL_STATUSES = new Set<string>([CONTROL_STATUS.delivered, CONTROL_STATUS.cancelled, CONTROL_STATUS.failed, CONTROL_STATUS.needsAttention]);
// Unrecognized statuses count as active so duplicate submission fails closed.
export const isActiveRun = (run: Pick<ControlRun, "status">): boolean => !TERMINAL_STATUSES.has(run.status);
export const PLATFORM_EVENT_TOPICS = new Set(["super.run.changed", "super.delivery.ready", "work_order.recorded", "work_order.cancelled"]);
export const DELIVERY_STATUS = { unknown: "unknown" } as const;

export type PlatformBridge = Partial<SuperPlatformBridge>;
export function getSuperPlatformBridge(): PlatformBridge | null {
  const bridge = getDesktopServiceBridge();
  return typeof bridge?.GetSuperPlatform === "function" ? bridge : null;
}
export type PlatformError = "unavailable" | "conflict" | "denied" | "invalid" | "capacity" | "resourceBusy";
// Exact matches only. Raw backend errors may contain secrets or filesystem data.
export function platformError(error: unknown): PlatformError {
  const message = typeof error === "string" ? error : error instanceof Error ? error.message : "";
  switch (message) {
    case "invalid Super control request": return "invalid";
    case "Super state revision conflict": return "conflict";
    case "Super authority denied": return "denied";
    case "Super capacity exhausted": return "capacity";
    case "Super workspace occupied": return "resourceBusy";
    default: return "unavailable";
  }
}
