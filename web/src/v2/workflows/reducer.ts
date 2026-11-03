import type { Failure, Run, WorkflowEvent, WorkflowStatus } from "./types";
export type AttemptProjection = { status?: WorkflowStatus; text: string; failure?: Failure };
export type NodeProjection = { status: WorkflowStatus; attempt: number; attempts: Record<number, AttemptProjection> };
export type WorkflowState = { runID: string; run: Run | null; status?: WorkflowStatus; cursor: number; nodes: Record<string, NodeProjection>; events: WorkflowEvent[]; pending: Record<number, WorkflowEvent> };
export type WorkflowAction = { type: "snapshot"; run: Run } | { type: "event"; runID: string; event: WorkflowEvent } | { type: "reset"; runID: string };
export const emptyWorkflowState = (runID: string): WorkflowState => ({ runID, run: null, cursor: 0, nodes: {}, events: [], pending: {} });

export function workflowReducer(state: WorkflowState, action: WorkflowAction): WorkflowState {
  if (action.type === "reset") return emptyWorkflowState(action.runID);
  if (action.type === "snapshot") {
    const run = action.run;
    if (run.id !== state.runID || (state.run && (run.revision < state.run.revision || run.last_sequence < state.cursor))) return state;
    const nodes: WorkflowState["nodes"] = {};
    for (const node of run.nodes ?? []) {
      const attempts: NodeProjection["attempts"] = {};
      for (const attempt of node.attempts ?? []) attempts[attempt.number] = { status: attempt.status, text: "", failure: attempt.failure };
      const latest = Math.max(0, ...(node.attempts ?? []).map((item) => item.number));
      if (latest) attempts[latest] = { ...attempts[latest], text: node.output ?? (state.run?.epoch === run.epoch ? state.nodes[node.node_id]?.attempts[latest]?.text ?? "" : "") };
      nodes[node.node_id] = { status: node.status, attempt: latest, attempts };
    }
    return drain({ ...state, run, status: run.status, nodes, cursor: run.last_sequence, pending: Object.fromEntries(Object.entries(state.pending).filter(([seq]) => Number(seq) > run.last_sequence)) });
  }
  const { event } = action;
  if (action.runID !== state.runID || !Number.isSafeInteger(event.sequence) || event.sequence < 1) return state;
  if (state.events.some((item) => item.sequence === event.sequence)) return state;
  const next = { ...state, events: [...state.events, event].sort((a, b) => a.sequence - b.sequence) };
  if (event.sequence <= state.cursor) return next; // Historical timeline only; never replay over a snapshot.
  return drain({ ...next, pending: { ...state.pending, [event.sequence]: event } });
}

function drain(state: WorkflowState): WorkflowState {
  let next = state;
  if (!next.run) return next;
  while (next.pending[next.cursor + 1]) {
    const event = next.pending[next.cursor + 1];
    const pending = { ...next.pending };
    delete pending[event.sequence];
    next = { ...next, cursor: event.sequence, pending };
    // Status is authoritative JSON, NEVER inferred from event names (including legacy events).
    if (!event.node_id) {
      if (event.status) next.status = event.status;
      continue;
    }
    const node = next.nodes[event.node_id];
    if (!node) continue; // Unknown node awaits authoritative readback.
    const number = event.attempt_no ?? 0;
    if (number && number < node.attempt) continue;
    const projection = { ...node, attempts: { ...node.attempts } };
    if (event.status) projection.status = event.status;
    if (number) {
      projection.attempt = number;
      const attempt = projection.attempts[number] ?? { text: "" };
      projection.attempts[number] = {
        ...attempt, ...(event.status ? { status: event.status } : {}), ...(event.failure ? { failure: event.failure } : {}),
        text: attempt.text + (event.type === "agent_output_delta" ? event.text ?? "" : ""),
      };
    }
    next.nodes = { ...next.nodes, [event.node_id]: projection };
  }
  return next;
}
