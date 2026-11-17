import { describe, expect, it } from "vitest";
import type { ControlRun } from "../desktopServiceBridge";
import { MOCK_HASH, mockClaim, mockRun } from "./platformFixtures.test-support";
import { workOrderStages } from "./workOrderStages";

const passed = { passed: true, policy_hash: MOCK_HASH, verified_at_ms: 1793664000000, artifacts: [], checks: [] };
const notice = { id: "mock-notice", target: { id: "mock-target", provider: "mock" }, status: "unknown", attempts: 1, claim: mockClaim };
function run(status: string, overrides: Partial<ControlRun> = {}, jobStatus = "queued"): ControlRun {
  const value = mockRun({ status, ...overrides }); value.jobs = value.jobs.map((job) => ({ ...job, status: jobStatus })); return value;
}
const stages = (value: ControlRun) => workOrderStages([value], "mock-order");

describe("work-order stages", () => {
  it("stays not started without a run for the selected order", () => {
    const none = { execution: "notStarted", verification: "notStarted", delivery: "notStarted" };
    expect(workOrderStages([], "mock-order")).toEqual(none);
    expect(workOrderStages([run("delivered", { intake_id: "other-order" })], "mock-order")).toEqual(none);
    expect(workOrderStages([run("delivered")], undefined)).toEqual(none);
  });
  it("follows the most recent run for the order", () => {
    const older = run("delivered", { id: "old", created_at_ms: 1, verification: passed }, "succeeded");
    const newer = run("running", { id: "new", created_at_ms: 2 });
    expect(workOrderStages([older, newer], "mock-order").execution).toBe("inProgress");
    expect(workOrderStages([newer, older], "mock-order").execution).toBe("inProgress");
  });
  it("maps the run lifecycle onto execution, verification and delivery", () => {
    expect(stages(run("awaiting_approval"))).toEqual({ execution: "awaitingApproval", verification: "notStarted", delivery: "notStarted" });
    expect(stages(run("queued"))).toEqual({ execution: "inProgress", verification: "notStarted", delivery: "notStarted" });
    expect(stages(run("verifying", {}, "succeeded"))).toEqual({ execution: "done", verification: "inProgress", delivery: "notStarted" });
    expect(stages(run("delivery_pending", { verification: passed }, "succeeded"))).toEqual({ execution: "done", verification: "done", delivery: "inProgress" });
    expect(stages(run("delivered", { verification: passed }, "succeeded"))).toEqual({ execution: "done", verification: "done", delivery: "done" });
  });
  it("reports failures, cancellation and attention without claiming progress", () => {
    expect(stages(run("failed", {}, "failed"))).toEqual({ execution: "failed", verification: "notStarted", delivery: "notStarted" });
    expect(stages(run("failed", { verification: { ...passed, passed: false } }, "succeeded"))).toEqual({ execution: "done", verification: "failed", delivery: "notStarted" });
    expect(stages(run("cancelled"))).toEqual({ execution: "cancelled", verification: "notStarted", delivery: "notStarted" });
    expect(stages(run("cancelled", { verification: passed }, "succeeded"))).toEqual({ execution: "done", verification: "done", delivery: "cancelled" });
    expect(stages(run("needs_attention", {}, "unknown"))).toEqual({ execution: "needsAttention", verification: "notStarted", delivery: "notStarted" });
    expect(stages(run("needs_attention", { verification: passed, notices: [notice] }, "succeeded"))).toEqual({ execution: "done", verification: "done", delivery: "needsAttention" });
  });
});
