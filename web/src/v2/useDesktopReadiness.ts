import { useCallback, useEffect, useRef, useState } from "react";
import { getDesktopServiceBridge } from "./desktopServiceBridge";

export const DESKTOP_READINESS = {
  graceMs: 8_000,
  startupLimitMs: 50_000,
  requestTimeoutMs: 1_500,
  failureThreshold: 3,
  pollMs: 2_000,
  startupPollMs: [300, 500, 1_000, 2_000]
} as const;

export type DesktopReadinessState = "waiting_identity" | "starting" | "ready" | "recovering" | "failed";
type Snapshot = { key: string; state: DesktopReadinessState; busy: boolean };
type Options = { enabled: boolean; apiBase: string; apiToken: string; onReady: () => void };

export function useDesktopReadiness({ enabled, apiBase, apiToken, onReady }: Options) {
  const key = JSON.stringify([apiBase, apiToken]);
  const [snapshot, setSnapshot] = useState<Snapshot>({ key, state: "waiting_identity", busy: false });
  const [retryVersion, setRetryVersion] = useState(0);
  const restartRequested = useRef(false);
  const retryLock = useRef(false);

  const retry = useCallback(() => {
    if (retryLock.current) return;
    retryLock.current = true;
    restartRequested.current = true;
    setRetryVersion((version) => version + 1);
  }, []);

  useEffect(() => {
    if (!enabled) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    let failures = 0;
    let actionableFailures = 0;
    let outageStarted = Date.now();
    let wasReady = false;
    let hasBeenReady = false;
    let failed = false;
    const shouldRestart = restartRequested.current;
    restartRequested.current = false;
    retryLock.current = shouldRestart;

    const publish = (state: DesktopReadinessState, busy = false) => {
      if (active) setSnapshot({ key, state, busy });
    };

    const check = async (): Promise<void> => {
      const request = new AbortController();
      controller = request;
      const timeout = setTimeout(() => request.abort(), DESKTOP_READINESS.requestTimeoutMs);
      const bridge = getDesktopServiceBridge();
      try {
        const headers = { Authorization: `Bearer ${apiToken}` };
        const [healthy, hostStatus] = await Promise.all([
          apiToken ? untilAborted(Promise.all(["/health", "/readyz"].map(async (path) => {
            const response = await fetch(`${apiBase}${path}`, { headers, signal: request.signal });
            return response.ok;
          })), request.signal).then((results) => results.every(Boolean), () => false) : false,
          bridge?.GetLocalServiceStatus
            ? untilAborted(Promise.resolve().then(() => bridge.GetLocalServiceStatus!()), request.signal).catch(() => undefined)
            : undefined
        ]);
        if (!active) return;
        if (healthy) {
          failures = 0;
          actionableFailures = 0;
          failed = false;
          publish("ready");
          if (!wasReady) onReady();
          wasReady = true;
          hasBeenReady = true;
        } else {
          if (wasReady) outageStarted = Date.now();
          wasReady = false;
          failures++;
          const elapsed = Date.now() - outageStarted;
          // A live host may legitimately need longer than the fallback grace
          // period (migrations, automatic retries). Still bound a stuck startup.
          const hostStarting = Boolean(apiToken) && hostStatus?.state === "starting";
          actionableFailures = hostStarting ? 0 : actionableFailures + 1;
          failed ||= failures >= DESKTOP_READINESS.failureThreshold
            && elapsed >= DESKTOP_READINESS.graceMs
            && (actionableFailures >= DESKTOP_READINESS.failureThreshold || elapsed >= DESKTOP_READINESS.startupLimitMs);
          publish(failed ? "failed" : !apiToken ? "waiting_identity" : hasBeenReady ? "recovering" : "starting");
        }
      } finally {
        clearTimeout(timeout);
        if (active) {
          const delay = wasReady || failed ? DESKTOP_READINESS.pollMs
            : DESKTOP_READINESS.startupPollMs[Math.min(failures - 1, DESKTOP_READINESS.startupPollMs.length - 1)];
          timer = setTimeout(() => void check(), delay);
        }
      }
    };

    const start = async () => {
      publish(apiToken ? "starting" : "waiting_identity", shouldRestart);
      if (shouldRestart) {
        const bridge = getDesktopServiceBridge();
        if (bridge) {
          controller = new AbortController();
          const timeout = setTimeout(() => controller?.abort(), DESKTOP_READINESS.startupLimitMs);
          try {
            await untilAborted(Promise.resolve().then(() => bridge.RestartLocalService()), controller.signal);
          } catch (error) {
            if (!active) return;
            console.warn("Desktop preparation retry failed", error);
            failed = true;
          } finally {
            clearTimeout(timeout);
          }
        }
      }
      if (!active) return;
      retryLock.current = false;
      publish(failed ? "failed" : apiToken ? "starting" : "waiting_identity");
      outageStarted = Date.now();
      await check();
    };
    void start();
    return () => {
      active = false;
      clearTimeout(timer);
      controller?.abort();
      retryLock.current = false;
    };
  }, [enabled, apiBase, apiToken, key, onReady, retryVersion]);

  const state = !enabled ? "ready" : snapshot.key === key ? snapshot.state : apiToken ? "starting" : "waiting_identity";
  return { state, ready: state === "ready", busy: snapshot.key === key && snapshot.busy, retry };
}

function untilAborted<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const abort = () => reject(new DOMException("Desktop readiness request cancelled", "AbortError"));
    if (signal.aborted) {
      void promise.catch(() => undefined);
      abort();
      return;
    }
    signal.addEventListener("abort", abort, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}
