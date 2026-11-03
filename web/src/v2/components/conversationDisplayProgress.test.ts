import { describe, expect, it } from "vitest";
import type { SessionMessage } from "../types";
import { advanceDisplayProgress, type DisplayProgress } from "./conversationDisplayProgress";

const message = (id: string, liveRevision?: number): SessionMessage => ({ id, role: "assistant", kind: "message", content: "hello", createdAt: "", liveRevision });
const initial = (): DisplayProgress => ({ selectedRef: "tenant:a", seen: new Map(), animating: new Set() });

describe("conversation display progress", () => {
  it("seeds history immediately and animates only subsequently observed live deltas", () => {
    const history = advanceDisplayProgress(initial(), "tenant:a", [message("old", 1)]);
    expect(history.animating.size).toBe(0);
    const snapshot = advanceDisplayProgress(history, "tenant:a", [message("old", 1), message("snapshot")]);
    expect(snapshot.animating.size).toBe(0);
    const live = advanceDisplayProgress(snapshot, "tenant:a", [message("old", 1), message("snapshot", 3), message("live", 4)]);
    expect([...live.animating]).toEqual(["snapshot", "live"]);
  });
  it("does not replay background updates on switching back to a session", () => {
    const first = advanceDisplayProgress(initial(), "tenant:a", []);
    const live = advanceDisplayProgress(first, "tenant:a", [message("answer", 2)]);
    expect(live.animating.has("answer")).toBe(true);
    const switched = advanceDisplayProgress(live, "tenant:b", [message("background", 5)]);
    expect(switched.animating.size).toBe(0);
    const returned = advanceDisplayProgress(switched, "tenant:a", [message("answer", 8)]);
    expect(returned.animating.size).toBe(0);
    expect(returned.seen.get("tenant:a")?.get("answer")).toBe(8);
    expect(returned.seen.get("tenant:b")?.get("background")).toBe(5);
  });
  it("does not replay completed display after refresh or duplicate SSE revisions", () => {
    const seeded = advanceDisplayProgress(initial(), "tenant:a", []);
    const live = advanceDisplayProgress(seeded, "tenant:a", [message("answer", 2)]);
    const done = { ...live, animating: new Set<string>() };
    const refreshed = advanceDisplayProgress(done, "tenant:a", [message("answer", 2)]);
    expect(refreshed.animating.size).toBe(0);
    expect(advanceDisplayProgress(refreshed, "tenant:a", refreshed.messages)).toBe(refreshed);
  });
});
