import { describe, expect, it } from "vitest";
import { buildSubAgentProgress } from "./WebAgentPage";
import type { AgentTaskEventRecord } from "../lib/types";

function nestedEvent(
  id: number,
  subTaskID: number,
  subType: string,
  subPayload: Record<string, unknown>
): AgentTaskEventRecord {
  return {
    id,
    task_id: 1,
    event_type: "nested_agent_progress",
    payload_json: JSON.stringify({
      source: "runner",
      sub_task_id: subTaskID,
      sub_event_type: subType,
      sub_payload: subPayload
    }),
    created_at: "2026-07-16T00:00:00Z",
    trace_id: ""
  };
}

describe("buildSubAgentProgress", () => {
  it("aggregates nested events into one card per sub-task", () => {
    const events = [
      nestedEvent(1, 99, "started", { agent_name: "explorer", model: "claude", description: "find X" }),
      nestedEvent(2, 99, "tool_call", { tool_name: "Grep" }),
      nestedEvent(3, 99, "completed", { turns: 3, tool_calls: 5, duration_ms: 1200 })
    ];
    const cards = buildSubAgentProgress(events);
    expect(cards).toHaveLength(1);
    expect(cards[0].agent).toBe("explorer");
    expect(cards[0].model).toBe("claude");
    expect(cards[0].status).toBe("done");
    expect(cards[0].turn).toBe(3);
    expect(cards[0].toolCalls).toBe(5);
    expect(cards[0].lastTool).toBe("Grep");
    expect(cards[0].durationMS).toBe(1200);
  });

  it("keeps separate cards for different sub-tasks in insertion order", () => {
    const events = [
      nestedEvent(1, 10, "started", { agent_name: "a" }),
      nestedEvent(2, 20, "started", { agent_name: "b" }),
      nestedEvent(3, 10, "tool_call", { tool_name: "Read" })
    ];
    const cards = buildSubAgentProgress(events);
    expect(cards).toHaveLength(2);
    expect(cards[0].agent).toBe("a");
    expect(cards[1].agent).toBe("b");
    expect(cards[0].lastTool).toBe("Read");
  });

  it("ignores non-nested events", () => {
    const events: AgentTaskEventRecord[] = [
      { id: 1, task_id: 1, event_type: "tool_call", payload_json: "{}", created_at: "", trace_id: "" }
    ];
    expect(buildSubAgentProgress(events)).toHaveLength(0);
  });

  it("marks failed and cancelled sub-agents", () => {
    const failed = buildSubAgentProgress([
      nestedEvent(1, 5, "started", { agent_name: "a" }),
      nestedEvent(2, 5, "failed", { error: "boom", duration_ms: 500 })
    ]);
    expect(failed[0].status).toBe("error");
    expect(failed[0].detail).toBe("boom");

    const cancelled = buildSubAgentProgress([
      nestedEvent(1, 6, "started", { agent_name: "b" }),
      nestedEvent(2, 6, "cancelled", { duration_ms: 100 })
    ]);
    expect(cancelled[0].status).toBe("cancelled");
  });
});
