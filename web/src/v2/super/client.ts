export type { WorkOrder, WorkOrderInput } from "../desktopServiceBridge";
import { getDesktopServiceBridge, type SuperWorkOrderBridge } from "../desktopServiceBridge";

export const WORK_ORDER_STATUS = { recorded: "recorded", cancelled: "cancelled" } as const;
export const WORK_ORDER_LIMIT = 100;
// Mirrors domain limits; backend UTF-8 byte validation remains authoritative.
export const WORK_ORDER_LIMITS = { title: 200, objective: 8000, criteria: 20000, workspace: 4096 } as const;
export type SuperClient = {
  create: SuperWorkOrderBridge["CreateSuperWorkOrder"];
  list: SuperWorkOrderBridge["ListSuperWorkOrders"];
  get: SuperWorkOrderBridge["GetSuperWorkOrder"];
  cancel: SuperWorkOrderBridge["CancelSuperWorkOrder"];
};

// Capability detection only: normal Desktop startup never opens the Super store.
export function getSuperClient(): SuperClient | null {
  const bridge = getDesktopServiceBridge();
  if (!bridge?.CreateSuperWorkOrder || !bridge.ListSuperWorkOrders || !bridge.GetSuperWorkOrder || !bridge.CancelSuperWorkOrder) return null;
  return {
    create: (key, input) => bridge.CreateSuperWorkOrder!(key, input),
    list: () => bridge.ListSuperWorkOrders!(),
    get: (id) => bridge.GetSuperWorkOrder!(id),
    cancel: (id) => bridge.CancelSuperWorkOrder!(id),
  };
}

export type SuperError = "invalid" | "conflict" | "notFound" | "unavailable";
export function superError(error: unknown): SuperError {
  const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
  if (message.includes("invalid work-order input")) return "invalid";
  if (message.includes("idempotency key conflicts")) return "conflict";
  if (message.includes("work order not found")) return "notFound";
  return "unavailable";
}
