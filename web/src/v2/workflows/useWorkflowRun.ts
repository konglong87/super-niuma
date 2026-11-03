import { useEffect, useReducer, useRef, useState } from "react";
import type { WorkflowClient } from "./client";
import { emptyWorkflowState, workflowReducer } from "./reducer";
import { TERMINAL_STATUSES, WORKFLOW, type Run } from "./types";
export type ConnectionState = "connecting" | "live" | "readback" | "offline" | "settled";
export function useWorkflowRun(client: WorkflowClient, runID: string) {
  const [state, dispatch] = useReducer(workflowReducer, runID, emptyWorkflowState);
  const [connection, setConnection] = useState<ConnectionState>("connecting");
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const stateRef = useRef(state);
  stateRef.current = state;
  // biome-ignore lint/correctness/useExhaustiveDependencies: refresh is the explicit user-requested readback generation.
  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    if (stateRef.current.runID !== runID) dispatch({ type: "reset", runID });
    setConnection("connecting"); setError("");
    let timer: ReturnType<typeof setTimeout> | undefined;
    let poll: ReturnType<typeof setInterval> | undefined;
    let reading = false;
    let settled = false;
    let historyCursor = 0;
    let streamController: AbortController | undefined;
    // Both SSE and periodic readback share this authority boundary. Old effect callbacks are ignored.
    const snapshot = async (): Promise<Run> => {
      const run = await client.get(runID, signal);
      if (!signal.aborted) {
        dispatch({ type: "snapshot", run });
      }
      return run;
    };
    const backfill = async (after: number): Promise<void> => {
      let cursor = after;
      for (;;) {
        const page = await client.events(runID, cursor, signal);
        if (signal.aborted) return;
        for (const event of page.events ?? []) dispatch({ type: "event", runID, event });
        const end = Math.max(cursor, ...(page.events ?? []).map((event) => event.sequence));
        historyCursor = Math.max(historyCursor, end);
        if (!page.has_more) return;
        if (end <= cursor) throw new Error("Workflow event page did not advance");
        cursor = end;
      }
    };
    const finishIfTerminal = (run: Run) => {
      if (!TERMINAL_STATUSES.has(run.status)) return false;
      settled = true; setConnection("settled"); setError(""); clearInterval(poll); streamController?.abort(); return true;
    };
    const readback = async () => {
      if (reading || signal.aborted) return;
      reading = true;
      try { await backfill(stateRef.current.runID === runID ? stateRef.current.cursor : 0); finishIfTerminal(await snapshot()); }
      catch (reason) { if (!signal.aborted) { setConnection("offline"); setError(message(reason)); } }
      finally { reading = false; }
    };
    const connect = async () => {
      try {
        setConnection("readback");
        const run = await snapshot();
        // Backfill all retained history once, then continue from the last historical cursor.
        await backfill(historyCursor);
        if (signal.aborted) return;
        if (finishIfTerminal(run)) return;
        setConnection("live"); setError("");
        streamController = new AbortController();
        const abortStream = () => streamController?.abort();
        signal.addEventListener("abort", abortStream, { once: true });
        try { await client.stream(runID, run.last_sequence, (event) => {
          if (signal.aborted || streamController?.signal.aborted) return;
          dispatch({ type: "event", runID, event });
          setConnection("live"); setError("");
        }, streamController.signal); } finally { signal.removeEventListener("abort", abortStream); }
      } catch (reason) {
        if (!signal.aborted && !streamController?.signal.aborted) { setConnection("offline"); setError(message(reason)); }
      }
      if (!signal.aborted) {
        await readback();
        if (settled) return;
        timer = setTimeout(() => { void connect(); }, WORKFLOW.reconnectMS);
      }
    };
    void connect();
    poll = setInterval(() => { void readback(); }, WORKFLOW.readbackMS);
    return () => { controller.abort(); clearTimeout(timer); clearInterval(poll); };
  }, [client, runID, refresh]);
  return { state, connection, error, reload: () => setRefresh((value) => value + 1), accept: (run: Run) => dispatch({ type: "snapshot", run }) };
}
export const message = (reason: unknown): string => reason instanceof Error ? reason.message : String(reason);
