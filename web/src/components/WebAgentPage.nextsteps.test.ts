import { describe, expect, it } from "vitest";
import type { AgentTaskEventRecord } from "../lib/types";
import { collectNextStepSuggestions, computeVisibleNextStepSuggestions } from "./WebAgentPage";

function event(id: number, eventType: string, payload: Record<string, unknown>, taskID = 1): AgentTaskEventRecord {
  return {
    id,
    task_id: taskID,
    event_type: eventType,
    payload_json: JSON.stringify(payload),
    created_at: "2026-07-27T00:00:00Z",
    trace_id: ""
  };
}

describe("collectNextStepSuggestions", () => {
  it("reads the suggestions out of a next_steps event", () => {
    const got = collectNextStepSuggestions([
      event(1, "message", { from_agent: "webui", content: "改代码" }),
      event(2, "next_steps", { source: "runner", suggestions: ["跑一遍测试", "补单元测试"] })
    ]);
    expect(got).toEqual(["跑一遍测试", "补单元测试"]);
  });

  // 新一轮开始就作废上一轮的建议，否则用户会看到过期引导。
  it("drops suggestions once a new user message arrives", () => {
    const got = collectNextStepSuggestions([
      event(1, "next_steps", { suggestions: ["上一轮的建议"] }),
      event(2, "message", { from_agent: "webui", content: "新的请求" })
    ]);
    expect(got).toEqual([]);
  });

  it("keeps only the latest next_steps event", () => {
    const got = collectNextStepSuggestions([
      event(1, "next_steps", { suggestions: ["旧的"] }),
      event(2, "next_steps", { suggestions: ["新的"] })
    ]);
    expect(got).toEqual(["新的"]);
  });

  it("ignores delayed suggestions from an older task generation", () => {
    const got = collectNextStepSuggestions([
      event(1, "message", { from_agent: "webui", content: "第一轮" }, 1),
      event(2, "message", { from_agent: "webui", content: "第二轮" }, 2),
      event(3, "next_steps", { suggestions: ["第二轮建议"] }, 2),
      event(4, "next_steps", { suggestions: ["第一轮延迟建议"] }, 1)
    ]);
    expect(got).toEqual(["第二轮建议"]);
  });

  it("ignores unusable payloads", () => {
    expect(collectNextStepSuggestions([event(1, "next_steps", {})])).toEqual([]);
    expect(collectNextStepSuggestions([event(1, "next_steps", { suggestions: "not an array" })])).toEqual([]);
    expect(collectNextStepSuggestions([event(1, "next_steps", { suggestions: ["", "   "] })])).toEqual([]);
    expect(collectNextStepSuggestions([])).toEqual([]);
  });
});

describe("computeVisibleNextStepSuggestions", () => {
  const suggestions = ["跑一遍测试", "补单元测试"];

  it("is visible when the composer is empty and nothing else is active", () => {
    expect(computeVisibleNextStepSuggestions(suggestions, "", false, false)).toEqual(suggestions);
  });

  it("hides while the composer has text", () => {
    expect(computeVisibleNextStepSuggestions(suggestions, "还没打完", false, false)).toEqual([]);
  });

  it("becomes visible again once the composer is cleared", () => {
    expect(computeVisibleNextStepSuggestions(suggestions, "还没打完", false, false)).toEqual([]);
    expect(computeVisibleNextStepSuggestions(suggestions, "", false, false)).toEqual(suggestions);
  });

  it("hides while a slash panel is active, even with an empty composer", () => {
    expect(computeVisibleNextStepSuggestions(suggestions, "", true, false)).toEqual([]);
  });

  it("hides while a turn is running, even with an empty composer", () => {
    expect(computeVisibleNextStepSuggestions(suggestions, "", false, true)).toEqual([]);
  });
});
