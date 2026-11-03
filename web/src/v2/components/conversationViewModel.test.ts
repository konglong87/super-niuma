import { describe, expect, it } from "vitest";
import type { AgentTaskRecord, WebAgentConversationDetail } from "../../lib/types";
import type { ConversationEvent, SessionDetail } from "../types";
import { buildConversationInspection, buildConversationRuns, collectConversationNextSteps, conversationRuntimeMetrics } from "./conversationViewModel";

const session: SessionDetail = { ref: "tenant:test", id: 7, source: "tenant", title: "Test", shortID: "test", status: "completed", updatedAt: "", messages: [], activity: [], context: [], changes: [], runs: [] };
const event = (id: number, type: string, payload: object, taskID = 1): ConversationEvent => ({ id, task_id: taskID, event_type: type, payload_json: JSON.stringify(payload), created_at: `2026-09-06T00:00:${String(id).padStart(2, "0")}Z` });
const runtime = (tasks: AgentTaskRecord[]): WebAgentConversationDetail => ({ id: "session:7", title: "Test", status: "completed", latest_task: tasks.at(-1) ?? { id: 1 }, tasks, events: [], usage: {} });

describe("conversation view model", () => {
  it("reads actual providers and models per run and lets newer SSE completion win", () => {
    const details = runtime([
      { id: 1, model: "old-model", status: "completed", metadata_json: JSON.stringify({ provider: "old-provider" }), started_at: "2026-09-06T00:00:00Z", finished_at: "2026-09-06T00:00:02Z" },
      { id: 2, model: "new-model", status: "running", metadata_json: JSON.stringify({ provider: "new-provider" }), started_at: "2026-09-06T00:00:03Z" }
    ]);
    const runs = buildConversationRuns({ ...session, provider: "current-setting", model: "future-model", events: [event(4, "usage", { input_tokens: 80, output_tokens: 20 }, 2), event(5, "completed", { duration_ms: 1800 }, 2)] }, details);
    expect(runs[0]).toMatchObject({ provider: "old-provider", model: "old-model", durationMs: 2000 });
    expect(runs[1]).toMatchObject({ provider: "new-provider", model: "new-model", durationMs: 1800, status: "completed", hasUsage: true });
    expect(runs[1]?.usage.totalTokens).toBe(100);
  });

  it("falls back to run timestamps when the reported duration is malformed", () => {
    const details = runtime([
      { id: 1, model: "model", status: "completed", started_at: "2026-09-06T00:00:00Z", finished_at: "2026-09-06T00:00:02Z" }
    ]);
    const runs = buildConversationRuns({ ...session, events: [event(1, "started", {}, 1), event(2, "completed", { duration_ms: 63_926_413_815_000 }, 1)] }, details);
    expect(runs[0]?.durationMs).toBe(2000);
  });

  it("keeps unknown context and cache metrics absent instead of inventing a context limit", () => {
    expect(conversationRuntimeMetrics({ ...session, events: [event(1, "completed", {})] })).toEqual({ contextPercent: undefined, cacheHitPercent: undefined, totalTokens: undefined });
    const metrics = conversationRuntimeMetrics({ ...session, events: [event(1, "usage", { input_tokens: 100, output_tokens: 200, cache_read_input_tokens: 100, context_length: 1000 })] });
    expect(metrics).toEqual({ contextPercent: 20, cacheHitPercent: 50, totalTokens: 400 });
    expect(conversationRuntimeMetrics({ ...session, events: [event(1, "usage", { total_tokens: 123 })] }).contextPercent).toBeUndefined();
  });

  it("only uses real next-step strings from the latest user generation", () => {
    expect(collectConversationNextSteps([event(1, "message", { content: "new" }, 2), event(2, "next_steps", { suggestions: ["old"] }, 1), event(3, "next_steps", { suggestions: ["Real suggestion", { text: "invented" }, ""] }, 2)])).toEqual(["Real suggestion"]);
    expect(collectConversationNextSteps([event(1, "next_steps", { suggestions: ["old"] }), event(2, "message", { content: "next" }, 2)])).toEqual([]);
  });

  it("retains full measured file changes and only shows a diff actually captured by the matching tool", () => {
    const events = [event(1, "tool_result", { tool_id: "edit", output: "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1 +1 @@\n-old\n+new" }), event(2, "file_change", { tool_id: "edit", path: "app.go", access: "edited", change: "modified", content_available: true, before_lines: 1, after_lines: 1, line_delta: 0 }), event(3, "file_change", { path: "external.bin", access: "edited", content_available: false }), event(4, "file_change", { path: "README.md", access: "read" }), ...Array.from({ length: 25 }, (_, index) => event(index + 5, "file_change", { path: `file-${index}`, access: "read" }))];
    const inspection = buildConversationInspection({ ...session, events });
    expect(inspection.activity.readFilePaths).toHaveLength(26);
    expect(inspection.activity.editedFiles[0]?.lineDelta).toBe(0);
    expect(inspection.activity.editedFiles[1]?.lineDelta).toBeNull();
    expect(inspection.fileEvents[0]?.diff).toContain("-old\n+new");
    expect(inspection.fileEvents[1]?.diff).toBeUndefined();
  });

  it("restores actual handoff context, subtask progress and permission decisions without cross-run collisions", () => {
    const inspection = buildConversationInspection({ ...session, events: [
      event(1, "session_handoff", { package_id: "package-one", package_sha256: "abc", package: { source: { ref: "tenant:source", cursor: "event:8" }, stage_summary: "Existing investigation", open_items: ["Run regression"], budget: { estimated_tokens: 200 } } }),
      event(2, "nested_agent_progress", { sub_task_id: 30, sub_event_type: "started", sub_payload: { agent_name: "Reviewer", model: "review-model" } }),
      event(3, "nested_agent_progress", { sub_task_id: 30, sub_event_type: "completed", sub_payload: { turns: 2, tool_calls: 4, duration_ms: 5000 } }),
      event(4, "permission_request", { request_id: "same", tool_name: "Bash" }), event(5, "permission_resolved", { request_id: "same", allowed: false }), event(6, "permission_request", { request_id: "same", tool_name: "Edit" }, 2)
    ] });
    expect(inspection.handoffs[0]).toMatchObject({ sourceRef: "tenant:source", summary: "Existing investigation", openItems: ["Run regression"], estimatedTokens: 200 });
    expect(inspection.subAgents).toEqual([expect.objectContaining({ taskID: "30", status: "done", turn: 2, toolCalls: 4 })]);
    expect(inspection.permissions.map((permission) => [permission.taskID, permission.status])).toEqual([[1, "denied"], [2, "pending"]]);
  });
});
