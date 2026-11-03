import type { SessionMessage, SessionRef } from "../types";

export type DisplayProgress = { selectedRef: SessionRef; messages?: SessionMessage[]; seen: Map<SessionRef, Map<string, number>>; animating: Set<string> };

// Only live revisions observed after selecting a loaded session can animate.
// Seeding each selection consumes background/snapshot content without replay.
export function advanceDisplayProgress(previous: DisplayProgress, selectedRef: SessionRef, messages?: SessionMessage[]): DisplayProgress {
  if (previous.selectedRef === selectedRef && previous.messages === messages) return previous;
  const seed = previous.selectedRef !== selectedRef || !previous.messages;
  const seen = new Map(previous.seen);
  const revisions = new Map(seen.get(selectedRef));
  const animating = seed ? new Set<string>() : new Set(previous.animating);
  for (const message of messages ?? []) {
    const revision = message.liveRevision ?? 0;
    if (!seed && revision > (revisions.get(message.id) ?? 0) && message.role === "assistant" && message.kind === "message" && !message.permission) animating.add(message.id);
    revisions.set(message.id, revision);
  }
  seen.set(selectedRef, revisions);
  return { selectedRef, messages, seen, animating };
}
