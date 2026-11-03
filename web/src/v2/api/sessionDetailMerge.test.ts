import { describe, expect, it } from "vitest";
import type { SessionDetail } from "../types";
import { mergeSessionDetail } from "./sessionDetailMerge";
import { applyConversationEvents } from "./sessionEventReducer";

const detail: SessionDetail = { ref: "tenant:a", source: "tenant", title: "A", shortID: "a", status: "running", activeRunID: 1, updatedAt: "2026-09-06T00:00:00Z", messages: [], events: [], activity: [], context: [], changes: [], runs: [] };
const snapshot = applyConversationEvents(detail, [{ id: 1, task_id: 1, event_type: "text_delta", payload_json: '{"content":"start"}' }]);

describe("session summary freshness", () => {
  it("keeps same-second completed SSE metadata, live revision and history through stale HTTP readback", () => {
    const current = applyConversationEvents({ ...snapshot, status: "completed", activeRunID: undefined }, [{ id: 2, task_id: 1, event_type: "text_delta", payload_json: '{"content":" done"}' }, { id: 3, task_id: 1, event_type: "completed" }], { live: true });
    const merged = mergeSessionDetail(current, { ...snapshot, historyLoaded: true });
    expect(merged).toMatchObject({ status: "completed", activeRunID: undefined, cursor: "3", historyLoaded: true });
    expect(merged.messages[0]).toMatchObject({ content: "start done", liveRevision: 2 });
    expect(merged.events).toHaveLength(3);
  });
  it("does not discard a newer queued run that has not emitted its first event", () => {
    const current = { ...snapshot, status: "queued" as const, activeRunID: 2, model: "new-model", historyLoaded: true };
    const merged = mergeSessionDetail(current, { ...snapshot, status: "completed", activeRunID: undefined });
    expect(merged).toMatchObject({ status: "queued", activeRunID: 2, model: "new-model", historyLoaded: true });
    expect(mergeSessionDetail(snapshot, current)).toMatchObject({ status: "queued", activeRunID: 2 });
  });
  it("preserves terminal progression at equal event frontier while accepting newer lifecycle updates", () => {
    const completed = { ...snapshot, status: "completed" as const, activeRunID: undefined };
    expect(mergeSessionDetail(completed, snapshot).status).toBe("completed");
    expect(mergeSessionDetail(snapshot, completed).activeRunID).toBeUndefined();
    expect(mergeSessionDetail({ ...completed, status: "archived" }, completed).status).toBe("archived");
    expect(mergeSessionDetail(completed, { ...completed, status: "archived", updatedAt: "2026-09-06T00:00:01Z" }).status).toBe("archived");
  });
});
