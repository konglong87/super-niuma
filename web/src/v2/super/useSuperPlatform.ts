import { useCallback, useEffect, useRef, useState } from "react";
import type { ControlRun, SuperEvent, SuperPlatformSnapshot } from "../desktopServiceBridge";
import { STOPPED_CONFIRMATION, platformError, type PlatformBridge, type PlatformError } from "./platformClient";
import { PLATFORM_LIMITS } from "./platformSafety";

type Session = { active: boolean; mutating: boolean; revision: number; refresh: () => void };
function mergeSnapshot(next: SuperPlatformSnapshot, previous: SuperPlatformSnapshot | null): SuperPlatformSnapshot {
  const versions = new Map(previous?.runs?.map((run) => [run.id, run]) ?? []);
  return { ...next, profiles: next.profiles ?? [], runs: (next.runs ?? []).map((run) => {
    const current = versions.get(run.id);
    return current && current.version > run.version ? current : run;
  }) };
}
function validateSnapshot(value: SuperPlatformSnapshot): SuperPlatformSnapshot {
  if (!value || typeof value.configured !== "boolean" || typeof value.running !== "boolean"
    || typeof value.config_json !== "string" || (value.runs != null && !Array.isArray(value.runs))
    || (value.profiles != null && !Array.isArray(value.profiles))) throw new Error("invalid response");
  return value;
}

export function useSuperPlatform(bridge: PlatformBridge | null) {
  const [snapshot, setSnapshot] = useState<SuperPlatformSnapshot | null>(null);
  const [events, setEvents] = useState<SuperEvent[]>([]);
  const [error, setError] = useState<PlatformError | null>(null);
  const [pollError, setPollError] = useState(false);
  const [eventError, setEventError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const session = useRef<Session | null>(null);
  const startKeys = useRef(new Map<string, string>());

  useEffect(() => {
    const current: Session = { active: true, mutating: false, revision: 0, refresh: () => {} };
    session.current = current;
    startKeys.current.clear();
    setSnapshot(null); setEvents([]); setError(null); setPollError(false); setEventError(false); setBusy(false);
    setLoading(typeof bridge?.GetSuperPlatform === "function");
    let timer: ReturnType<typeof setTimeout> | undefined;
    let reading = false;
    let queued = false;
    let cursor = 0;
    const live = () => current.active && session.current === current;
    async function readEvents() {
      if (typeof bridge?.GetSuperEvents !== "function") return;
      try {
        // Drain bounded pages immediately. Resume from the last accepted cursor
        // on the next poll; failures never advance it, and payloads stay opaque.
        for (let page = 0; page < PLATFORM_LIMITS.eventPages && live(); page++) {
          const result = await bridge.GetSuperEvents(cursor);
          if (!live()) return;
          if (!Array.isArray(result) || result.some((event) => !event || !Number.isSafeInteger(event.sequence) || event.sequence < 1)) throw new Error("invalid events");
          const fresh = result.filter((event) => event.sequence > cursor).sort((a, b) => a.sequence - b.sequence);
          setEventError(false);
          if (!fresh.length) break;
          cursor = fresh[fresh.length - 1].sequence;
          setEvents((previous) => [...new Map([...previous, ...fresh].map((event) => [event.sequence, event])).values()]
            .sort((a, b) => a.sequence - b.sequence).slice(-PLATFORM_LIMITS.events));
        }
      } catch { if (live()) setEventError(true); }
    }
    async function readSnapshot() {
      if (typeof bridge?.GetSuperPlatform !== "function") return;
      const revision = current.revision;
      try {
        const next = validateSnapshot(await bridge.GetSuperPlatform());
        if (live() && revision === current.revision && !current.mutating) {
          setSnapshot((previous) => mergeSnapshot(next, previous)); setPollError(false);
        }
      } catch { if (live() && revision === current.revision) setPollError(true); }
      finally { if (live()) setLoading(false); }
    }
    async function poll() {
      if (!live() || typeof bridge?.GetSuperPlatform !== "function") return;
      if (reading || current.mutating) { queued = true; return; }
      if (timer) clearTimeout(timer);
      reading = true; queued = false;
      await Promise.all([readSnapshot(), readEvents()]);
      reading = false;
      if (live()) {
        // Wails promises cannot be aborted; generation/revision checks fence
        // responses from a previous bridge, unmount or pre-mutation read.
        timer = setTimeout(() => void poll(), queued && !current.mutating ? 0 : PLATFORM_LIMITS.pollMS);
      }
    }
    current.refresh = () => { void poll(); };
    void poll();
    return () => { current.active = false; if (timer) clearTimeout(timer); };
  }, [bridge]);

  const mutate = useCallback(async (action: () => Promise<ControlRun | SuperPlatformSnapshot>, isConfig = false) => {
    const current = session.current;
    if (!current?.active || current.mutating) return false;
    current.mutating = true; current.revision++;
    setBusy(true); setError(null);
    try {
      const result = await action();
      if (!current.active || session.current !== current) return false;
      if (isConfig) {
        const next = validateSnapshot(result as SuperPlatformSnapshot);
        setSnapshot((previous) => mergeSnapshot(next, previous));
      } else {
        const run = result as ControlRun;
        if (!run || typeof run.id !== "string" || !Number.isSafeInteger(run.version)) throw new Error("invalid response");
        setSnapshot((previous) => previous ? { ...previous, runs: [run, ...previous.runs.filter((item) => item.id !== run.id)] } : previous);
      }
      setPollError(false);
      return true;
    } catch (failure) {
      if (current.active && session.current === current) setError(platformError(failure));
      return false;
    } finally {
      current.mutating = false; current.revision++;
      if (current.active && session.current === current) { setBusy(false); current.refresh(); }
    }
  }, []);

  const start = useCallback((intakeID: string, profileID: string) => {
    if (typeof bridge?.StartSuperRun !== "function") return Promise.resolve(false);
    const identity = JSON.stringify([intakeID, profileID]);
    let key = startKeys.current.get(identity);
    if (!key) { key = crypto.randomUUID(); startKeys.current.set(identity, key); }
    return mutate(() => bridge.StartSuperRun!(intakeID, profileID, key!));
  }, [bridge, mutate]);
  const save = useCallback((json: string) => typeof bridge?.SaveSuperPlatformConfig === "function"
    ? mutate(() => bridge.SaveSuperPlatformConfig!(json), true) : Promise.resolve(false), [bridge, mutate]);
  const approve = useCallback((run: ControlRun) => typeof bridge?.ApproveSuperRun === "function"
    ? mutate(() => bridge.ApproveSuperRun!(run.id, run.version, run.grant_hash)) : Promise.resolve(false), [bridge, mutate]);
  const cancel = useCallback((run: ControlRun) => typeof bridge?.CancelSuperRun === "function"
    ? mutate(() => bridge.CancelSuperRun!(run.id, run.version)) : Promise.resolve(false), [bridge, mutate]);
  const retry = useCallback((run: ControlRun) => typeof bridge?.RetrySuperRun === "function"
    ? mutate(() => bridge.RetrySuperRun!(run.id, run.version)) : Promise.resolve(false), [bridge, mutate]);
  const abandon = useCallback((run: ControlRun) => typeof bridge?.AbandonSuperRun === "function"
    ? mutate(() => bridge.AbandonSuperRun!(run.id, run.version, STOPPED_CONFIRMATION)) : Promise.resolve(false), [bridge, mutate]);
  const refresh = useCallback(() => session.current?.refresh(), []);
  return { snapshot, events, error, pollError, eventError, busy, loading, start, save, approve, cancel, retry, abandon, refresh };
}
