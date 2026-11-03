import { useCallback, useEffect, useRef, useState } from "react";
import type { ComputerClient } from "./client";
import { computerReadinessError } from "./readiness";
import type { ComputerActionReceipt, ComputerCapabilities, ComputerObservation, ComputerRuntimeStatus, ComputerSessionSnapshot, StartComputerSessionInput } from "./types";

type ComputerState = {
  available: boolean;
  capabilities: ComputerCapabilities | null;
  session: ComputerSessionSnapshot | null;
  // The native DTO has no conversation label. Retain only a successful start's
  // approved ref, never the currently selected conversation or persisted UI prefs.
  approvedConversationRef: string | null;
  observation: ComputerObservation | null;
  receipts: ComputerActionReceipt[];
  loading: boolean;
  error: string | null;
  controlIntent: "pause" | "stop" | null;
  runtimeStatus: ComputerRuntimeStatus | null;
};
export const COMPUTER_SESSION_POLL_INTERVAL_MS = 1000;
const isTerminal = (session: ComputerSessionSnapshot) => session.state === "stopped" || session.state === "failed";
const canDiscoverSession = (state: ComputerState) => !state.loading && !state.controlIntent
  && (!state.session || isTerminal(state.session));

const initialState: ComputerState = { available: false, capabilities: null, session: null, approvedConversationRef: null, observation: null, receipts: [], loading: false, error: null, controlIntent: null, runtimeStatus: null };

function mergeSnapshot(current: ComputerState, snapshot: ComputerSessionSnapshot): ComputerState {
  const receipt = snapshot.last_receipt;
  const receipts = receipt ? [...current.receipts.filter((item) => item.action_id !== receipt.action_id), receipt] : current.receipts;
  // A snapshot contains metadata only. Preserve image bytes only for the same observation.
  const stopped = snapshot.state === "stopped";
  const observation = stopped
    ? null
    : snapshot.observation?.id === current.observation?.id
      ? current.observation : snapshot.observation ?? current.observation;
  const session = stopped ? { ...snapshot, observation: undefined } : snapshot;
  return { ...current, session, capabilities: snapshot.capabilities, observation, receipts };
}

export function useComputerSession(client: ComputerClient | null) {
  const [state, setState] = useState<ComputerState>(initialState);
  const current = useRef(initialState);
  const mounted = useRef(false);
  // Every request has one generation; safety controls supersede all pending work.
  const generation = useRef(0);
  const update = useCallback((next: ComputerState) => {
    current.current = next;
    if (mounted.current) setState(next);
  }, []);

  useEffect(() => {
    mounted.current = true;
    ++generation.current;
    update(initialState);
    return () => { mounted.current = false; ++generation.current; };
  }, [client, update]);

  // Share the in-flight guard across effect lifetimes too: switching clients or
  // sessions cannot overlap a still-pending read from the previous lifecycle.
  const snapshotPending = useRef(false);
  const sessionID = state.session?.session_id;
  const polling = !!state.session && !isTerminal(state.session);

  // Discover model-created replacements after a terminal session too. Status
  // discovery grants no approval, never observes, and cannot revive a stopped ID.
  useEffect(() => {
    if (!client?.getActiveSession || polling) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      if (!disposed) timer = setTimeout(() => { void discover(); }, COMPUTER_SESSION_POLL_INTERVAL_MS);
    };
    const discover = async () => {
      if (disposed) return;
      if (!canDiscoverSession(current.current)) { schedule(); return; }
      const version = generation.current;
      try {
        const snapshot = await client.getActiveSession!();
        const latest = current.current;
        if (disposed || !mounted.current || version !== generation.current || !canDiscoverSession(latest)
          || !snapshot.session_id || isTerminal(snapshot) || snapshot.session_id === latest.session?.session_id) return;
        // Reset all old-session evidence and user control/approval state. The
        // returned host snapshot supplies metadata, never old preview authority.
        update({ ...mergeSnapshot({ ...initialState, available: latest.available }, snapshot), approvedConversationRef: null });
      } catch {
        // Idle with no active session is expected; do not surface polling errors.
      } finally {
        if (!disposed && (!current.current.session || isTerminal(current.current.session))) schedule();
      }
    };
    void discover();
    return () => { disposed = true; clearTimeout(timer); };
  }, [client, sessionID, polling, update]);
  useEffect(() => {
    if (!client || !sessionID || !polling) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      if (!disposed) timer = setTimeout(() => { void poll(); }, COMPUTER_SESSION_POLL_INTERVAL_MS);
    };
    const poll = async () => {
      const before = current.current;
      if (disposed || before.session?.session_id !== sessionID || isTerminal(before.session)) return;
      if (snapshotPending.current || before.loading) { schedule(); return; }
      snapshotPending.current = true;
      // A read never increments generation or supersedes a user control.
      const version = generation.current;
      try {
        const snapshot = await client.getSession(sessionID);
        const latest = current.current;
        if (disposed || !mounted.current || version !== generation.current || latest.loading
          || latest.session?.session_id !== sessionID || snapshot.session_id !== sessionID
          || isTerminal(latest.session)) return;
        // Unconfirmed Stop/Pause remains authoritative even after a failed RPC.
        const confirmedIntent = isTerminal(snapshot)
          || (latest.controlIntent === "pause" && snapshot.state === "paused");
        if (latest.controlIntent && !confirmedIntent) return;
        update({
          ...mergeSnapshot(latest, snapshot),
          // Snapshot polling is status-only: never capture or replace preview bytes.
          observation: latest.observation,
          controlIntent: confirmedIntent ? null : latest.controlIntent,
        });
      } catch {
        // Transient reads must not overwrite actionable control errors. Retry at
        // the bounded cadence, never via an Observe fallback.
      } finally {
        snapshotPending.current = false;
        if (current.current.session && !isTerminal(current.current.session)) schedule();
      }
    };
    schedule();
    return () => { disposed = true; clearTimeout(timer); };
  }, [client, sessionID, polling, update]);

  const fail = useCallback((message: string): never => {
    update({ ...current.current, error: message });
    throw new Error(message);
  }, [update]);

  const run = useCallback(async <T,>(operation: () => Promise<T>, apply: (value: T, state: ComputerState) => ComputerState): Promise<T | null> => {
    const version = ++generation.current;
    update({ ...current.current, loading: true, error: null });
    try {
      const value = await operation();
      if (!mounted.current || version !== generation.current) return null;
      update({ ...apply(value, current.current), loading: false });
      return value;
    } catch (error) {
      if (!mounted.current || version !== generation.current) return null;
      update({ ...current.current, loading: false, error: error instanceof Error ? error.message : String(error) });
      throw error;
    }
  }, [update]);

  const loadCapabilities = useCallback(async () => {
    if (!client) return null;
    // A readiness refresh is allowed while a session is active so TCC revoke /
    // restore transitions become visible without an Observe or input fallback.
    if (current.current.loading || current.current.controlIntent) return null;
    return run(() => client.getCapabilities(), (value, state) => ({
      ...state, available: value.available, capabilities: value.capabilities,
      error: value.error_message || value.error_code || (!value.available ? "Computer Use is unavailable on this host." : null)
    }));
  }, [client, run]);

  const runtimeStatusPending = useRef(false);
  const loadRuntimeStatus = useCallback(async () => {
    if (!client?.getRuntimeStatus || runtimeStatusPending.current) return null;
    runtimeStatusPending.current = true;
    try {
      const status = await client.getRuntimeStatus();
      if (mounted.current) update({ ...current.current, runtimeStatus: status });
      return status;
    } catch {
      const unavailable: ComputerRuntimeStatus = {
        state: "faulted", active_session: Boolean(current.current.session),
        session_id: current.current.session?.session_id, available: false,
        error_code: "runtime_status_unavailable",
        fault: { state: "faulted", error_code: "runtime_status_unavailable", recoverable: false, session_id: current.current.session?.session_id },
      };
      if (mounted.current) update({ ...current.current, runtimeStatus: unavailable });
      return null;
    } finally {
      runtimeStatusPending.current = false;
    }
  }, [client, update]);

  useEffect(() => {
    if (!client?.getRuntimeStatus) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      if (disposed) return;
      await loadRuntimeStatus();
      if (!disposed) timer = setTimeout(() => { void poll(); }, 2000);
    };
    void poll();
    return () => { disposed = true; clearTimeout(timer); };
  }, [client, loadRuntimeStatus]);

  useEffect(() => {
    const refresh = (): void => {
      if (document.visibilityState === "hidden") return;
      void loadCapabilities().catch(() => undefined);
    };
    window.addEventListener("focus", refresh);
    window.addEventListener("niuma:computer-permission-recheck", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      window.removeEventListener("focus", refresh);
      window.removeEventListener("niuma:computer-permission-recheck", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [loadCapabilities]);

  const start = useCallback(async (input: StartComputerSessionInput = { approved: false }) => {
    const state = current.current;
    if (!client) return fail("Computer Use is unavailable");
    if (!input.approved) return fail("Explicit session approval is required");
    const readiness = computerReadinessError(state.available, state.capabilities);
    if (readiness) return fail(readiness);
    if (state.loading || (state.session && state.session.state !== "stopped")) return fail("Stop the current computer session before starting another");
    const approvedInput = { ...input };
    update({ ...state, session: null, approvedConversationRef: null, observation: null, receipts: [], controlIntent: null });
    return run(() => client.start(approvedInput), (snapshot, state) => ({
      ...mergeSnapshot(state, snapshot), approvedConversationRef: approvedInput.conversation_ref ?? null
    }));
  }, [client, fail, run, update]);

  const stopRuntime = useCallback(async () => {
    if (!client?.stopRuntime) return fail("Computer runtime stop is unavailable");
    return run(() => client.stopRuntime!(), (status, state) => ({
      ...state,
      runtimeStatus: status,
      session: status.state === "stopped" && state.session ? { ...state.session, state: "stopped", observation: undefined } : state.session,
      observation: status.state === "stopped" ? null : state.observation,
      controlIntent: null,
    }));
  }, [client, fail, run]);

  const observe = useCallback(async () => {
    const state = current.current;
    const session = state.session;
    if (!client || !session) return fail("No computer session is active");
    if (state.controlIntent || !["ready", "needs_observation"].includes(session.state)) return fail("The computer session cannot observe until it is resumed");
    if (state.runtimeStatus?.fault && state.runtimeStatus.fault.error_code !== "permission_revoked") return fail("The Computer Use runtime fault requires Stop before retrying");
    if (state.loading) return fail("A computer request is already pending");
    return run(() => client.observe(session.session_id), (value, state) => {
      if (value.observation.session_id !== session.session_id) throw new Error("Observation session mismatch");
      const observation = { ...value.observation, image_data: value.image_data, media_type: value.media_type };
      return { ...state, observation, capabilities: observation.capabilities, session: { ...session, state: "ready", observation, capabilities: observation.capabilities } };
    });
  }, [client, fail, run]);

  const control = useCallback(async (kind: "pause" | "resume" | "stop") => {
    const state = current.current;
    const session = state.session;
    if (!client || !session) return fail("No computer session is active");
    if (session.state === "stopped") return fail("The computer session is stopped");
    if (kind !== "stop" && state.controlIntent === "stop") return fail("Stop requested; retry Stop if it failed before continuing");
    if (kind === "resume" && (state.loading || session.state !== "paused")) return fail("Only a confirmed paused session can resume");
    if (kind !== "stop" && state.runtimeStatus?.fault && state.runtimeStatus.fault.error_code !== "permission_revoked") return fail("The Computer Use runtime fault requires Stop before retrying");
    if (kind === "resume") {
      const readiness = computerReadinessError(state.available, state.capabilities);
      if (readiness) return fail(readiness);
    }
    // Retain the safety intent on rejection: outcome is not confirmed; allow Stop retry.
    update({ ...state, controlIntent: kind === "resume" ? state.controlIntent : kind });
    return run(() => client[kind](session.session_id), (snapshot, state) => {
      if (snapshot.session_id !== session.session_id) throw new Error("Control session mismatch");
      if (kind === "stop" && snapshot.state !== "stopped") throw new Error("Backend did not confirm Stop");
      if (kind === "pause" && snapshot.state !== "paused") throw new Error("Backend did not confirm Pause");
      return { ...mergeSnapshot(state, snapshot), controlIntent: null };
    });
  }, [client, fail, run, update]);
  const pause = useCallback(() => control("pause"), [control]);
  const resume = useCallback(() => control("resume"), [control]);
  const stop = useCallback(() => control("stop"), [control]);

  const getReceipt = useCallback(async (actionID: string) => {
    const state = current.current;
    if (!client || !state.session) return fail("No computer session is active");
    if (state.loading || state.controlIntent) return fail("A computer control request is pending");
    const sessionID = state.session.session_id;
    return run(() => client.getReceipt(sessionID, actionID), (receipt, state) => {
      if (receipt.session_id !== sessionID || receipt.action_id !== actionID) throw new Error("Receipt ownership mismatch");
      return { ...state, receipts: [...state.receipts.filter((item) => item.action_id !== receipt.action_id), receipt] };
    });
  }, [client, fail, run]);

  return { ...state, loadCapabilities, loadRuntimeStatus, start, observe, pause, resume, stop, stopRuntime, getReceipt };
}
