import { getDesktopServiceBridge, type SuperPlatformBridge } from "../desktopServiceBridge";

export const STOPPED_CONFIRMATION = "ALL_WORKERS_AND_SENDERS_CONFIRMED_STOPPED";

export const CONTROL_STATUS = {
  queued: "queued", running: "running", verifying: "verifying", awaitingApproval: "awaiting_approval",
  deliveryPending: "delivery_pending", delivered: "delivered", cancelling: "cancelling", cancelled: "cancelled",
  failed: "failed", needsAttention: "needs_attention", unknown: "unknown",
} as const;
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
