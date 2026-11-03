import { useEffect, useRef } from "react";
import type { Dispatch, RefObject, SetStateAction } from "react";
import { listAgentTaskEvents, streamAgentTaskEvents } from "../lib/api";
import { SSE_EVENT, TASK_STATUS } from "../lib/constants";
import { AGENT_EVENT_PAGE_LIMIT, applyTaskEventStatus, mergeEvents, terminalStatuses } from "../lib/agentEvents";
import type { AgentTaskEventRecord, AgentTaskRecord, IdentityConfig } from "../lib/types";

export type StreamState = "idle" | "connecting" | "live" | "closed" | "error";

const LATE_EVENT_READBACK_DEADLINE_MS = 6_000;
const LATE_EVENT_READBACK_BACKOFF_MS = [250, 500, 1_000, 2_000] as const;

// SSE 流健康度的协调状态，由 useAgentTaskStream 拥有、useTaskPolling 共享。
export interface AgentTaskStreamHealth {
  newestEventIDRef: RefObject<number>;
  lastEventAtRef: RefObject<number>;
  staleNotifiedRef: RefObject<boolean>;
}

// 选中任务的 SSE 连接与事件分发（逻辑原样迁自 WebAgentPage）。
export function useAgentTaskStream(deps: {
  identity: IdentityConfig;
  selectedTaskId: number | null;
  selectedTaskStatus: string | undefined;
  selectedTaskResultJSON?: string;
  loadError: string;
  events: AgentTaskEventRecord[];
  streamErrorCopy: string;
  onStatus: (message: string) => void;
  setStreamState: Dispatch<SetStateAction<StreamState>>;
  setEvents: Dispatch<SetStateAction<AgentTaskEventRecord[]>>;
  setConversationTimelineEvents: Dispatch<SetStateAction<AgentTaskEventRecord[]>>;
  setTasks: Dispatch<SetStateAction<AgentTaskRecord[]>>;
  markTypewriterTask: (taskID: number) => void;
  refresh: (selectedID?: number) => Promise<void>;
}): AgentTaskStreamHealth {
  const {
    identity,
    selectedTaskId,
    selectedTaskStatus,
    selectedTaskResultJSON,
    loadError,
    events,
    streamErrorCopy,
    onStatus,
    setStreamState,
    setEvents,
    setConversationTimelineEvents,
    setTasks,
    markTypewriterTask,
    refresh,
  } = deps;
  const lastEventAtRef = useRef(Date.now());
  const staleNotifiedRef = useRef(false);
  const newestEventIDRef = useRef(0);
  const readbackRef = useRef<{ taskId: number; eventID: number; identityKey: string; cancel: () => void } | null>(null);
  const terminalReadbackRef = useRef<((taskID: number, eventID: number) => void) | null>(null);
  // 连接所有权放在 ref 里、由 effect 主体自己接管,而不是挂在 effect cleanup 上:
  // 任务进入终态会触发 effect 重跑,但已建立的连接仍交给服务端正常关闭,
  // onDone 再统一置 closed 并 refresh。completed 携带 pending 标记时,尾部建议
  // 由独立的短时 after_id readback 补齐,不让 composer 等待它。
  const connRef = useRef<{ taskId: number; controller: AbortController; identityKey: string } | null>(null);

  useEffect(() => {
    newestEventIDRef.current = events.reduce((max, event) => Math.max(max, event.id), 0);
  }, [events]);

  useEffect(() => {
    const identityKey = makeIdentityKey(identity);
    const abortConn = () => {
      connRef.current?.controller.abort();
      connRef.current = null;
    };
    const cancelReadback = () => {
      readbackRef.current?.cancel();
      readbackRef.current = null;
    };
    terminalReadbackRef.current = null;
    if (!selectedTaskId || loadError) {
      abortConn();
      cancelReadback();
      return;
    }
    const startLateEventReadback = (taskID: number, afterID: number) => {
      const activeReadback = readbackRef.current;
      if (activeReadback && activeReadback.taskId === taskID && activeReadback.eventID === afterID && activeReadback.identityKey === identityKey) {
        return;
      }
      readbackRef.current?.cancel();
      let cancelled = false;
      let releaseDelay: (() => void) | null = null;
      let delayTimer: number | null = null;
      let deadlineTimer: number | null = null;
      const readbackController = new AbortController();
      const cleanup = () => {
        if (delayTimer !== null) {
          window.clearTimeout(delayTimer);
          delayTimer = null;
        }
        if (deadlineTimer !== null) {
          window.clearTimeout(deadlineTimer);
          deadlineTimer = null;
        }
        releaseDelay?.();
        releaseDelay = null;
      };
      const cancel = () => {
        cancelled = true;
        readbackController.abort();
        cleanup();
      };
      readbackRef.current = { taskId: taskID, eventID: afterID, identityKey, cancel };
      deadlineTimer = window.setTimeout(cancel, LATE_EVENT_READBACK_DEADLINE_MS);
      void (async () => {
        const startedAt = Date.now();
        let cursor = afterID;
        let attempt = 0;
        while (!cancelled && Date.now() - startedAt < LATE_EVENT_READBACK_DEADLINE_MS) {
          const elapsed = Date.now() - startedAt;
          const delay = Math.min(
            LATE_EVENT_READBACK_BACKOFF_MS[Math.min(attempt, LATE_EVENT_READBACK_BACKOFF_MS.length - 1)],
            Math.max(0, LATE_EVENT_READBACK_DEADLINE_MS - elapsed)
          );
          await new Promise<void>((resolve) => {
            releaseDelay = resolve;
            delayTimer = window.setTimeout(() => {
              delayTimer = null;
              releaseDelay = null;
              resolve();
            }, delay);
          });
          if (cancelled || Date.now() - startedAt >= LATE_EVENT_READBACK_DEADLINE_MS) {
            return;
          }
          let latestEvents: AgentTaskEventRecord[];
          try {
            latestEvents = await listAgentTaskEvents(identity, taskID, {
              afterID: cursor,
              limit: AGENT_EVENT_PAGE_LIMIT,
              signal: readbackController.signal
            });
          } catch {
            if (cancelled || Date.now() - startedAt >= LATE_EVENT_READBACK_DEADLINE_MS) {
              return;
            }
            attempt += 1;
            continue;
          }
          if (cancelled) {
            return;
          }
          if (latestEvents.length > 0) {
            const newest = latestEvents.reduce((max, event) => Math.max(max, event.id), cursor);
            cursor = Math.max(cursor, newest);
            newestEventIDRef.current = Math.max(newestEventIDRef.current, newest);
            setEvents((current) => mergeEvents(current, latestEvents));
            setConversationTimelineEvents((current) => mergeEvents(current, latestEvents));
            if (latestEvents.some((event) => event.task_id === taskID && event.event_type === "next_steps")) {
              return;
            }
          }
          attempt += 1;
        }
      })().finally(() => {
        cleanup();
        if (readbackRef.current?.cancel === cancel) {
          readbackRef.current = null;
        }
      });
    };
    terminalReadbackRef.current = startLateEventReadback;
    if (selectedTaskStatus !== undefined && terminalStatuses.has(selectedTaskStatus || "")) {
      if (connRef.current?.taskId === selectedTaskId && connRef.current.identityKey === identityKey) {
        // 同一任务刚流到终态:让服务端自行关流,onDone 再统一 refresh。
        return;
      }
      // 切到一个本来就是终态的任务:不开新的实时连接。
      abortConn();
      if (readbackRef.current?.taskId !== selectedTaskId || readbackRef.current.identityKey !== identityKey) {
        cancelReadback();
      }
      const pendingCompleted = [...events].reverse().find((event) => event.task_id === selectedTaskId && event.event_type === TASK_STATUS.completed && hasPendingNextSteps(event.payload_json));
      const hasExistingNextSteps = hasMatchingNextSteps(events, selectedTaskId, pendingCompleted?.id);
      if ((pendingCompleted || hasPendingNextSteps(selectedTaskResultJSON)) && !hasExistingNextSteps) {
        startLateEventReadback(selectedTaskId, pendingCompleted?.id || newestEventIDRef.current);
      }
      setStreamState("closed");
      return;
    }
    // 非终态(含 ready→running 的翻转):替换掉旧连接重新订阅。服务端对非
    // 非 running 任务会立即返回并关流,所以 running 翻转必须在这里重连才能拿到
    // 本轮的实时事件。
    abortConn();
    cancelReadback();
    const controller = new AbortController();
    connRef.current = { taskId: selectedTaskId, controller, identityKey };
    const ownsConnection = () => connRef.current?.controller === controller && connRef.current.taskId === selectedTaskId && connRef.current.identityKey === identityKey;
    const releaseConn = () => {
      if (connRef.current?.controller === controller) {
        connRef.current = null;
      }
    };
    setStreamState("connecting");
    const streamAfterID = newestEventIDRef.current;
    void streamAgentTaskEvents(
      identity,
      selectedTaskId,
      {
        onEvent: (event) => {
          if (!ownsConnection()) {
            return;
          }
          if (event.type === SSE_EVENT.connected) {
            setStreamState("live");
            if (selectedTaskStatus === TASK_STATUS.running) {
              markTypewriterTask(selectedTaskId);
            }
            return;
          }
          if (event.type === SSE_EVENT.agentTaskEvent) {
            lastEventAtRef.current = Date.now();
            staleNotifiedRef.current = false;
            if (
              event.event.task_id === selectedTaskId &&
              event.event.event_type === "next_steps" &&
              readbackRef.current?.taskId === selectedTaskId &&
              readbackRef.current.identityKey === identityKey
            ) {
              readbackRef.current.cancel();
              readbackRef.current = null;
            }
            setEvents((current) => mergeEvents(current, [event.event]));
            setConversationTimelineEvents((current) => mergeEvents(current, [event.event]));
            setTasks((current) => applyTaskEventStatus(current, event.event));
            if (terminalStatuses.has(event.event.event_type || "")) {
              setStreamState("closed");
            }
            if (
              event.event.task_id === selectedTaskId &&
              event.event.event_type === TASK_STATUS.completed &&
              hasPendingNextSteps(event.event.payload_json)
            ) {
              startLateEventReadback(selectedTaskId, event.event.id);
            }
            return;
          }
          if (event.type === SSE_EVENT.error) {
            setStreamState("error");
            onStatus(event.error || streamErrorCopy);
          }
        },
        onDone: () => {
          if (!ownsConnection()) {
            return;
          }
          releaseConn();
          setStreamState((current) => (current === "error" ? current : "closed"));
          void refresh(selectedTaskId);
        }
      },
      controller.signal,
      streamAfterID
    ).catch((err) => {
      const owned = ownsConnection();
      releaseConn();
      if (controller.signal.aborted || !owned) {
        return;
      }
      setStreamState("error");
      onStatus(err instanceof Error ? err.message : String(err));
    });
  }, [streamErrorCopy, identity.apiBase, identity.apiToken, identity.deviceId, identity.mobileJwt, identity.tenantKey, identity.userId, loadError, onStatus, selectedTaskResultJSON, selectedTaskStatus, selectedTaskId]);

  useEffect(() => {
    if (!selectedTaskId || !terminalStatuses.has(selectedTaskStatus || "")) {
      return;
    }
    const pendingCompleted = [...events].reverse().find((event) => event.task_id === selectedTaskId && event.event_type === TASK_STATUS.completed && hasPendingNextSteps(event.payload_json));
    const hasExistingNextSteps = hasMatchingNextSteps(events, selectedTaskId, pendingCompleted?.id);
    if ((pendingCompleted || hasPendingNextSteps(selectedTaskResultJSON)) && !hasExistingNextSteps) {
      terminalReadbackRef.current?.(selectedTaskId, pendingCompleted?.id || newestEventIDRef.current);
    }
  }, [events, identity.apiBase, identity.apiToken, identity.deviceId, identity.mobileJwt, identity.tenantKey, identity.userId, selectedTaskId, selectedTaskResultJSON, selectedTaskStatus]);

  // 只在卸载时兜底断开。运行中的连接替换由上面的 effect 主体管理,不能写成它的
  // cleanup:终态触发的重跑必须保留连接。
  useEffect(
    () => () => {
      connRef.current?.controller.abort();
      connRef.current = null;
      readbackRef.current?.cancel();
      readbackRef.current = null;
    },
    []
  );

  return { newestEventIDRef, lastEventAtRef, staleNotifiedRef };
}

function hasPendingNextSteps(payloadJSON: string | undefined): boolean {
  if (!payloadJSON) {
    return false;
  }
  try {
    const payload = JSON.parse(payloadJSON) as unknown;
    return Boolean(payload && typeof payload === "object" && (payload as Record<string, unknown>).next_steps_status === "pending");
  } catch {
    return false;
  }
}

function hasMatchingNextSteps(events: AgentTaskEventRecord[], taskID: number, completedID?: number): boolean {
  return events.some((event) => event.task_id === taskID && event.event_type === "next_steps" && (completedID === undefined || event.id >= completedID));
}

function makeIdentityKey(identity: IdentityConfig): string {
  return [identity.apiBase, identity.tenantKey, identity.userId, identity.deviceId, identity.apiToken, identity.mobileJwt].join("\u0000");
}
