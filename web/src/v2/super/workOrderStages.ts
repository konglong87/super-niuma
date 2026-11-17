import type { ControlRun } from "../desktopServiceBridge";
import { CONTROL_STATUS } from "./platformClient";

export type StageState = "notStarted" | "awaitingApproval" | "inProgress" | "done" | "failed" | "cancelled" | "needsAttention";
// Polls rebuild the runs array; keep the previous one when nothing a stage
// depends on changed, so the workspace does not re-render every poll.
export function sameRunsForStages(previous: ControlRun[], next: ControlRun[]): boolean {
  return previous.length === next.length && previous.every((run, index) => run.id === next[index].id && run.version === next[index].version && run.status === next[index].status);
}
export type WorkOrderStages = Record<"execution" | "verification" | "delivery", StageState>;

// Summarize an order from its most recent run; the control panel stays the detailed source.
export function workOrderStages(runs: ControlRun[], orderID: string | undefined): WorkOrderStages {
  const run = runs.filter((item) => orderID !== undefined && item.intake_id === orderID)
    .reduce<ControlRun | undefined>((latest, item) => !latest || item.created_at_ms > latest.created_at_ms ? item : latest, undefined);
  if (!run) return { execution: "notStarted", verification: "notStarted", delivery: "notStarted" };
  const { status, verification: report } = run;
  const jobs = run.jobs ?? [];
  const executed = jobs.length > 0 && jobs.every((job) => job.status === CONTROL_STATUS.succeeded);
  const stopped = status === CONTROL_STATUS.cancelled || status === CONTROL_STATUS.cancelling;
  const execution: StageState = status === CONTROL_STATUS.awaitingApproval ? "awaitingApproval"
    : status === CONTROL_STATUS.queued || status === CONTROL_STATUS.running ? "inProgress"
    : executed || status === CONTROL_STATUS.verifying || status === CONTROL_STATUS.deliveryPending || status === CONTROL_STATUS.delivered ? "done"
    : stopped ? "cancelled" : status === CONTROL_STATUS.failed ? "failed" : "needsAttention";
  // A failed report on a queued/running root is a revision round awaiting
  // re-verification, not a final verification failure.
  const revising = report && !report.passed && (status === CONTROL_STATUS.queued || status === CONTROL_STATUS.running);
  const verification: StageState = status === CONTROL_STATUS.verifying || revising ? "inProgress" : report?.passed ? "done" : report ? "failed" : "notStarted";
  const delivery: StageState = status === CONTROL_STATUS.deliveryPending ? "inProgress" : status === CONTROL_STATUS.delivered ? "done"
    : status === CONTROL_STATUS.needsAttention && (run.notices ?? []).length > 0 ? "needsAttention"
    : stopped && report ? "cancelled" : "notStarted";
  return { execution, verification, delivery };
}
