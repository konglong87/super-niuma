import { useEffect } from "react";
import type { Dispatch, SetStateAction } from "react";
import { getAgentTask, listAgentTaskEvents } from "../lib/api";
import { TASK_STATUS } from "../lib/constants";
import {
  AGENT_EVENT_PAGE_LIMIT,
  RUNNING_REFRESH_INTERVAL_MS,
  RUNNING_STALE_TIMEOUT_MS,
  mergeEvents,
  upsertTask,
} from "../lib/agentEvents";
import type { AgentTaskEventRecord, AgentTaskRecord, IdentityConfig } from "../lib/types";
import type { AgentTaskStreamHealth } from "./useAgentTaskStream";

// 运行中任务的兜底轮询：SSE 之外定时拉取任务状态与新事件，
// 静默超时时提示并全量刷新（逻辑原样迁自 WebAgentPage）。
export function useTaskPolling(deps: {
  identity: IdentityConfig;
  selectedTaskId: number | null;
  running: boolean;
  streamStaleCopy: string;
  onStatus: (message: string) => void;
  setEvents: Dispatch<SetStateAction<AgentTaskEventRecord[]>>;
  setConversationTimelineEvents: Dispatch<SetStateAction<AgentTaskEventRecord[]>>;
  setTasks: Dispatch<SetStateAction<AgentTaskRecord[]>>;
  refresh: (selectedID?: number) => Promise<void>;
  health: AgentTaskStreamHealth;
}): void {
  const {
    identity,
    selectedTaskId,
    running,
    streamStaleCopy,
    onStatus,
    setEvents,
    setConversationTimelineEvents,
    setTasks,
    refresh,
    health,
  } = deps;
  const { newestEventIDRef, lastEventAtRef, staleNotifiedRef } = health;

  useEffect(() => {
    if (!selectedTaskId || !running) {
      staleNotifiedRef.current = false;
      return;
    }
    lastEventAtRef.current = Date.now();
    const interval = window.setInterval(() => {
      void (async () => {
        try {
          const task = await getAgentTask(identity, selectedTaskId);
          setTasks((current) => upsertTask(current, task));
          const latestEvents = await listAgentTaskEvents(identity, selectedTaskId, {
            afterID: newestEventIDRef.current,
            limit: AGENT_EVENT_PAGE_LIMIT
          });
          if (latestEvents.length > 0) {
            const newest = latestEvents.reduce((max, event) => Math.max(max, event.id), 0);
            if (newest > newestEventIDRef.current) {
              newestEventIDRef.current = newest;
              lastEventAtRef.current = Date.now();
              staleNotifiedRef.current = false;
              setEvents((current) => mergeEvents(current, latestEvents));
              setConversationTimelineEvents((current) => mergeEvents(current, latestEvents));
            }
          }
          if (task.status && task.status !== TASK_STATUS.running) {
            await refresh(selectedTaskId);
            return;
          }
          if (!staleNotifiedRef.current && Date.now() - lastEventAtRef.current > RUNNING_STALE_TIMEOUT_MS) {
            staleNotifiedRef.current = true;
            onStatus(streamStaleCopy);
            await refresh(selectedTaskId);
          }
        } catch (err) {
          onStatus(err instanceof Error ? err.message : String(err));
        }
      })();
    }, RUNNING_REFRESH_INTERVAL_MS);
    return () => window.clearInterval(interval);
  }, [streamStaleCopy, identity, onStatus, running, selectedTaskId]);
}
