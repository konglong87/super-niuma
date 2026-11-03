import type { PendingInputRecord } from "./types";

export function sortPendingInputs(items: PendingInputRecord[]): PendingInputRecord[] {
  return [...items].sort((a, b) => a.sequence - b.sequence || a.id.localeCompare(b.id));
}

export function replacePendingInput(items: PendingInputRecord[], item: PendingInputRecord): PendingInputRecord[] {
  const next = items.some((current) => current.id === item.id) ? items.map((current) => (current.id === item.id ? item : current)) : [...items, item];
  return sortPendingInputs(next);
}

export function removePendingInput(items: PendingInputRecord[], id: string): PendingInputRecord[] {
  return items.filter((item) => item.id !== id);
}

export function movePendingInputUp(items: PendingInputRecord[], id: string): PendingInputRecord[] {
  const next = sortPendingInputs(items);
  const index = next.findIndex((item) => item.id === id);
  if (index <= 0) return next;
  const previous = next[index - 1];
  const current = next[index];
  next[index - 1] = { ...current, sequence: previous.sequence };
  next[index] = { ...previous, sequence: current.sequence };
  return sortPendingInputs(next);
}

export function pendingInputDisplayContent(item: PendingInputRecord): string {
  const direction = item.direction?.trim() || "";
  return direction ? `[方向]\n${direction}\n\n[原消息]\n${item.content}` : item.content;
}
