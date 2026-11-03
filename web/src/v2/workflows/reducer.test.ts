import { describe, expect, it } from "vitest";
import { emptyWorkflowState, workflowReducer } from "./reducer";
import { eventFixture, runFixture } from "./testFixtures";
describe("Workflow reducer", () => {
  const initial = () => workflowReducer(emptyWorkflowState("run-1"), { type: "snapshot", run: runFixture() });
  it("holds gaps, drains in sequence and deduplicates delta delivery", () => {
    const delta = eventFixture(2, { type: "agent_output_delta", node_id: "step-1", attempt_no: 1, text: "hello" });
    let state = workflowReducer(initial(), { type: "event", runID: "run-1", event: delta });
    expect(state.cursor).toBe(0);
    state = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(1, { node_id: "step-1", attempt_no: 1, status: "running" }) });
    expect(state.cursor).toBe(2);
    expect(state.nodes["step-1"].attempts[1].text).toBe("hello");
    expect(workflowReducer(state, { type: "event", runID: "run-1", event: delta })).toBe(state);
  });
  it("does not guess state from old or current event names without status", () => {
    const state = workflowReducer(initial(), { type: "event", runID: "run-1", event: eventFixture(1, { type: "run_completed" }) });
    expect(state.status).toBe("running");
    const node = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(2, { type: "agent_completed", node_id: "step-1", attempt_no: 1 }) });
    expect(node.nodes["step-1"].status).toBe("pending");
  });
  it("isolates runs, resets and late older attempts", () => {
    let state = initial();
    expect(workflowReducer(state, { type: "snapshot", run: runFixture({ id: "other" }) })).toBe(state);
    expect(workflowReducer(state, { type: "event", runID: "other", event: eventFixture(1) })).toBe(state);
    state = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(1, { node_id: "step-1", attempt_no: 2, status: "running" }) });
    state = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(2, { node_id: "step-1", attempt_no: 1, status: "failed", type: "agent_output_delta", text: "stale" }) });
    expect(state.nodes["step-1"].status).toBe("running"); expect(state.nodes["step-1"].attempts[1]).toBeUndefined();
    expect(workflowReducer(state, { type: "reset", runID: "other" })).toEqual(emptyWorkflowState("other"));
  });
  it("uses snapshot cursor as authority while retaining historical events only for timeline", () => {
    let state = workflowReducer(initial(), { type: "snapshot", run: runFixture({ last_sequence: 3, revision: 2, status: "completed", artifacts: [{ id: "a", node_id: "step-1", attempt_no: 1, kind: "text", content: "real", sha256: "sha", created_at: "now" }] }) });
    state = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(2, { status: "running" }) });
    expect(state.status).toBe("completed"); expect(state.events).toHaveLength(1); expect(state.run?.artifacts[0].content).toBe("real");
    expect(workflowReducer(state, { type: "snapshot", run: runFixture({ revision: 1 }) })).toBe(state);
  });
  it("distinguishes cancel requested from final run_cancelled using their explicit statuses", () => {
    let state = workflowReducer(initial(), { type: "event", runID: "run-1", event: eventFixture(1, { type: "run_cancel_requested", status: "cancel_requested" }) });
    expect(state.status).toBe("cancel_requested");
    state = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(2, { type: "run_cancelled", status: "cancelled" }) });
    expect(state.status).toBe("cancelled");
  });
  it("retains live deltas through partial readback but clears them on a new epoch", () => {
    const attempt = { number: 1, epoch: 1, task_id: 1, status: "running" as const, started_at: "now", usage: runFixture().usage };
    let state = workflowReducer(emptyWorkflowState("run-1"), { type: "snapshot", run: runFixture({ nodes: [{ node_id: "step-1", status: "running", attempts: [attempt] }] }) });
    state = workflowReducer(state, { type: "event", runID: "run-1", event: eventFixture(1, { type: "agent_output_delta", node_id: "step-1", attempt_no: 1, text: "Live output" }) });
    state = workflowReducer(state, { type: "snapshot", run: runFixture({ revision: 2, last_sequence: 1, nodes: [{ node_id: "step-1", status: "running", attempts: [attempt] }] }) });
    expect(state.nodes["step-1"].attempts[1].text).toBe("Live output");
    state = workflowReducer(state, { type: "snapshot", run: runFixture({ revision: 3, last_sequence: 1, epoch: 2, nodes: [{ node_id: "step-1", status: "running", attempts: [{ ...attempt, epoch: 2 }] }] }) });
    expect(state.nodes["step-1"].attempts[1].text).toBe("");
  });
  it("holds events before a snapshot and never mutates its input", () => {
    const seed = emptyWorkflowState("run-1");
    let state = workflowReducer(seed, { type: "event", runID: "run-1", event: eventFixture(1, { status: "queued" }) });
    expect(state.cursor).toBe(0);
    state = workflowReducer(state, { type: "snapshot", run: runFixture() });
    expect(state.cursor).toBe(1); expect(state.status).toBe("queued"); expect(seed.pending).toEqual({});
  });
});
