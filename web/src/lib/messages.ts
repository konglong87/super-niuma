import type { ChatMessage, TenantMessage } from "./types";

export function tenantMessagesToChat(messages: TenantMessage[]): ChatMessage[] {
  return [...messages]
    .sort((a, b) => (a.turn_index || 0) - (b.turn_index || 0) || (a.id || 0) - (b.id || 0))
    .map((message) => ({
      id: String(message.id),
      role: message.role === "assistant" ? "assistant" : message.role === "system" ? "system" : "user",
      content: message.content || contentFromJSON(message.content_json) || "",
      status: message.status || statusFromJSON(message.content_json),
      messageId: message.id,
      turnIndex: message.turn_index,
      createdAt: message.created_at
    }));
}

function statusFromJSON(value?: string): string {
  if (!value) {
    return "";
  }
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    if (typeof parsed.status === "string") {
      return parsed.status;
    }
    if (parsed.mobile && typeof parsed.mobile === "object") {
      const mobile = parsed.mobile as Record<string, unknown>;
      if (typeof mobile.status === "string") {
        return mobile.status;
      }
    }
  } catch {
    return "";
  }
  return "";
}

function contentFromJSON(value?: string): string {
  if (!value) {
    return "";
  }
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    if (typeof parsed.content === "string") {
      return parsed.content;
    }
    if (parsed.mobile && typeof parsed.mobile === "object") {
      const mobile = parsed.mobile as Record<string, unknown>;
      if (typeof mobile.content === "string") {
        return mobile.content;
      }
    }
  } catch {
    return "";
  }
  return "";
}

export function formatTokens(total: number): string {
  if (total >= 1_000_000) {
    return `${(total / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (total >= 1000) {
    return `${(total / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  }
  return String(total);
}

const MILLISECONDS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const MINUTES_PER_HOUR = 60;
const HOURS_PER_DAY = 24;
export const MAX_REASONABLE_REPORTED_DURATION_MS = 7 * HOURS_PER_DAY * MINUTES_PER_HOUR * SECONDS_PER_MINUTE * MILLISECONDS_PER_SECOND;
const MAX_COMPACT_DURATION_MS = MAX_REASONABLE_REPORTED_DURATION_MS;

export function isReasonableReportedDurationMs(value: number | undefined): value is number {
  return value !== undefined && Number.isFinite(value) && value >= 0 && value <= MAX_REASONABLE_REPORTED_DURATION_MS;
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms > MAX_COMPACT_DURATION_MS) return ">7d";
  const seconds = Math.round(ms / MILLISECONDS_PER_SECOND);
  if (seconds < SECONDS_PER_MINUTE) return `${seconds}s`;
  const minutes = Math.floor(seconds / SECONDS_PER_MINUTE);
  const rest = seconds % SECONDS_PER_MINUTE;
  if (minutes < MINUTES_PER_HOUR) return `${minutes}m ${rest}s`;
  const hours = Math.floor(minutes / MINUTES_PER_HOUR);
  const hourRest = minutes % MINUTES_PER_HOUR;
  if (hours < HOURS_PER_DAY) return `${hours}h ${String(hourRest).padStart(2, "0")}m`;
  const days = Math.floor(hours / HOURS_PER_DAY);
  const dayRest = hours % HOURS_PER_DAY;
  return `${days}d ${String(dayRest).padStart(2, "0")}h`;
}
