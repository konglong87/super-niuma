// SSE 事件名，与后端 internal/server 的 SSE 输出对齐，值不可修改。
export const SSE_EVENT = {
  connected: "connected",
  agentTaskEvent: "agent_task_event",
  error: "error",
} as const;

// Agent 任务状态，与后端 agenttasks 状态机对齐。
export const TASK_STATUS = {
  running: "running",
  completed: "completed",
  failed: "failed",
  cancelled: "cancelled",
  timeout: "timeout",
} as const;

export type TaskStatus = (typeof TASK_STATUS)[keyof typeof TASK_STATUS];

export const TERMINAL_TASK_STATUSES: readonly TaskStatus[] = [
  TASK_STATUS.completed,
  TASK_STATUS.failed,
  TASK_STATUS.cancelled,
  TASK_STATUS.timeout,
];
