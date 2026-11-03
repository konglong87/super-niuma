import type { AgentTaskEventRecord, AgentTaskRecord } from "./types";
import { TASK_STATUS, TERMINAL_TASK_STATUSES } from "./constants";

// Agent 任务事件流的分页与轮询节奏常量（迁自 WebAgentPage）。
export const RUNNING_REFRESH_INTERVAL_MS = 2_000;
export const RUNNING_STALE_TIMEOUT_MS = 90_000;
export const AGENT_EVENT_PAGE_LIMIT = 500;

export const terminalStatuses = new Set<string>(TERMINAL_TASK_STATUSES);

export function mergeEvents(current: AgentTaskEventRecord[], incoming: AgentTaskEventRecord[]) {
  const byID = new Map<number, AgentTaskEventRecord>();
  for (const event of current) {
    byID.set(event.id, event);
  }
  for (const event of incoming) {
    byID.set(event.id, event);
  }
  return Array.from(byID.values()).sort((a, b) => a.id - b.id);
}

export function applyTaskEventStatus(tasks: AgentTaskRecord[], event: AgentTaskEventRecord) {
  if (!event.task_id) {
    return tasks;
  }
  if (event.event_type === "started") {
    return tasks.map((task) => (task.id === event.task_id ? { ...task, status: TASK_STATUS.running } : task));
  }
  if (!terminalStatuses.has(event.event_type || "")) {
    return tasks;
  }
  return tasks.map((task) => (
    task.id === event.task_id
      ? {
        ...task,
        status: event.event_type || task.status,
        result_json: event.payload_json || task.result_json,
        finished_at: event.created_at || task.finished_at
      }
      : task
  ));
}

export function upsertTask(tasks: AgentTaskRecord[], nextTask: AgentTaskRecord) {
  if (tasks.some((task) => task.id === nextTask.id)) {
    return tasks.map((task) => (task.id === nextTask.id ? nextTask : task));
  }
  return [nextTask, ...tasks];
}
