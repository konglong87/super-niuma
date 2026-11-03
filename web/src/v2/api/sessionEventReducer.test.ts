import { describe, expect, it } from "vitest";
import { applyConversationEvents } from "./sessionEventReducer";
import type { ConversationEvent, SessionDetail } from "../types";

const session: SessionDetail = { ref: "tenant:key", source: "tenant", title: "Test", shortID: "key", status: "running", updatedAt: "", messages: [], activity: [], context: [], changes: [], runs: [] };
const event = (id: number, type: string, payload: object, task = 14): ConversationEvent => ({ id, task_id: task, event_type: type, payload_json: JSON.stringify(payload) });

describe("conversation event projection", () => {
  it("preserves stream whitespace across live batches, reconnect overlap and history", () => {
    const chunks = ["The", " user", " ", "said", ":\n\n", "```go\n", "\t", "return", " ", "true\n", "```\n"];
    for (const type of ["thinking_delta", "text_delta"]) {
      const events = chunks.map((content, index) => event(index + 1, type, { content }));
      const first = applyConversationEvents(session, events.slice(0, 5), { live: true });
      const resumed = applyConversationEvents(first, events.slice(3), { live: true });
      const history = applyConversationEvents(session, events);
      expect(resumed.messages[0]?.content).toBe(chunks.join(""));
      expect(history.messages[0]?.content).toBe(resumed.messages[0]?.content);
    }
  });
  it("restores the reported user message and immediate provider failure", () => {
    const result = applyConversationEvents(session, [event(1, "message", { content: "你好" }), event(2, "failed", { error: 'provider type "anthropic" is incompatible with protocol "openai-responses"' })]);
    expect(result.messages.map((m) => m.kind)).toEqual(["message", "error"]);
    expect(result.messages[1]?.content).toContain("incompatible");
    expect(result.runs[0]?.status).toBe("failed");
  });
  it("keeps projected channel roles separate even when event types are shared", () => {
    const result = applyConversationEvents(session, [
      event(1, "message", { content: "用户问题", role: "user" }, 101),
      event(2, "completed", {}, 101),
      event(3, "message", { content: "Agent 回复", role: "assistant" }, 102),
      event(4, "completed", {}, 102)
    ]);
    expect(result.messages.map((message) => ({ role: message.role, content: message.content }))).toEqual([
      { role: "user", content: "用户问题" },
      { role: "assistant", content: "Agent 回复" }
    ]);
  });
  it("deduplicates reconnect and snapshot overlap without duplicating text", () => {
    const delta = event(2, "text_delta", { content: "hello" });
    const first = applyConversationEvents(session, [event(1, "message", { content: "hi" }), delta]);
    const result = applyConversationEvents(first, [delta, event(3, "text_delta", { content: " world" }), event(4, "completed", { response: "hello world" })]);
    expect(result.messages.map((m) => m.content)).toEqual(["hi", "hello world"]);
    expect(result.cursor).toBe("4");
  });
  it("falls back to the measured run span when a terminal duration is unbounded", () => {
    const result = applyConversationEvents(session, [
      { ...event(1, "started", {}), created_at: "2026-09-06T00:00:01Z" },
      { ...event(2, "text_delta", { content: "done" }), created_at: "2026-09-06T00:00:02Z" },
      { ...event(3, "completed", { duration_ms: 63_926_413_815_000 }), created_at: "2026-09-06T00:00:03Z" }
    ]);
    expect(result.messages[0]?.durationMs).toBe(2000);
  });
  it("keeps thinking, tools and different runs separate", () => {
    const result = applyConversationEvents(session, [event(1, "thinking_delta", { content: "thinking" }), event(2, "tool_use", { tool_id: "a", tool_name: "Read" }), event(3, "tool_result", { tool_id: "a", tool_name: "Read", output: "done" }), event(4, "text_delta", { content: "one" }), event(5, "text_delta", { content: "two" }, 15)]);
    expect(result.messages.map((m) => m.kind)).toEqual(["thinking", "tool", "message", "message"]);
    expect(result.messages[1]?.content).toContain("done");
    expect(result.messages.at(-1)?.content).toBe("two");
  });
  it("preserves structured tool input and closes the same tool across image events", () => {
    const result = applyConversationEvents(session, [event(1, "tool_call", { tool_id: "a", tool_name: "Bash", input: { command: "go test ./..." } }), event(2, "image_artifact", { asset_id: "asset-a" }), event(3, "tool_result", { tool_id: "a", output: "failure output", is_error: true, duration_ms: 420, computer_observation: { observation_id: "obs-1", asset_id: "asset-screen", media_type: "image/png", name: "screen.png", size_bytes: 42, sha256: "hash" } })]);
    expect(result.messages).toHaveLength(2);
    expect(result.messages[0]?.tool).toMatchObject({ name: "Bash", command: "go test ./...", output: "failure output", status: "failed", durationMs: 420, computerObservation: { observationID: "obs-1", assetID: "asset-screen", mediaType: "image/png" } });
    expect(result.messages[0]?.tool?.input).toContain("go test ./...");
    expect(result.messages[1]?.artifacts).toEqual([{ asset_id: "asset-a" }]);
  });
  it("keeps text before and after an image artifact in chronological order", () => {
    const result = applyConversationEvents(session, [event(1, "text_delta", { content: "Before image" }), event(2, "image_artifact", { asset_id: "asset-a" }), event(3, "text_delta", { content: "After image" })]);
    expect(result.messages.map((message) => message.content)).toEqual(["Before image", "", "After image"]);
    expect(result.messages[1]?.artifacts?.[0]?.asset_id).toBe("asset-a");
  });
  it("projects attachments and terminal metadata without assigning current provider to history", () => {
    const result = applyConversationEvents({ ...session, provider: "current-provider", model: "fallback-model" }, [event(1, "text_delta", { content: "old" }, 13), event(2, "completed", { model: "old-model" }, 13), event(3, "message", { content: "look", attachments: [{ attachment_id: "attachment-a", type: "image", media_type: "image/png", size_bytes: 10 }] }), event(4, "thinking_delta", { thinking: "plan" }), event(5, "text_delta", { content: "answer" }), event(6, "usage", { input_tokens: 80, output_tokens: 20 }), event(7, "completed", { model: "actual-model", duration_ms: 1200, total_tokens: 100 })]);
    expect(result.messages[0]).toMatchObject({ model: "old-model" });
    expect(result.messages[0]?.provider).toBeUndefined();
    expect(result.messages[1]?.attachments?.[0]).toMatchObject({ attachment_id: "attachment-a", name: "", sha256: "" });
    expect(result.messages[2]?.thinkingStatus).toBe("completed");
    expect(result.messages[3]).toMatchObject({ taskID: 14, model: "actual-model", durationMs: 1200, tokens: 100, status: "completed" });
    expect(result.messages[3]?.provider).toBeUndefined();
  });
  it("marks only unseen SSE text live and preserves progress through heartbeat refolds", () => {
    const history = applyConversationEvents(session, [event(1, "text_delta", { content: "old" })]);
    expect(history.messages[0]?.liveRevision).toBeUndefined();
    const live = applyConversationEvents(history, [event(1, "text_delta", { content: "old" }), event(2, "text_delta", { content: " new" })], { live: true });
    expect(live.messages[0]?.liveRevision).toBe(2);
    const heartbeat = applyConversationEvents(live, [], { live: true });
    expect(heartbeat.messages[0]).toMatchObject({ content: "old new", liveRevision: 2 });
    const refresh = applyConversationEvents(heartbeat, [event(3, "text_delta", { content: " snapshot" })]);
    expect(refresh.messages[0]?.liveRevision).toBe(2);
  });
  it("terminates unfinished thinking and tool activity on cancellation", () => {
    const result = applyConversationEvents(session, [event(1, "thinking_delta", { content: "plan" }), event(2, "tool_call", { tool_id: "a", tool_name: "Bash" }), event(3, "thinking_delta", { content: "followup" }), event(4, "cancelled", {})]);
    expect(result.messages[0]?.thinkingStatus).toBe("completed");
    expect(result.messages[1]?.tool?.status).toBe("stopped");
    expect(result.messages[2]?.thinkingStatus).toBe("stopped");
  });
  it("keeps compact summaries, phases and tool results in durable chronological positions", () => {
    const timed = (id: number, type: string, payload: object, seconds: number) => ({ ...event(id, type, payload), created_at: `2026-09-06T00:00:${String(seconds).padStart(2, "0")}Z` });
    const result = applyConversationEvents(session, [
      timed(1, "turn_start", { turn: 1 }, 0), timed(2, "thinking_delta", { content: "stage one" }, 1),
      timed(3, "tool_call", { tool_id: "read", tool_name: "Read" }, 3), timed(4, "thinking_delta", { content: "stage two" }, 4),
      timed(5, "compact_summary", { summary: "durable summary" }, 7), timed(6, "image_artifact", { asset_id: "image-one" }, 8),
      timed(7, "tool_result", { tool_id: "read", output: "done", duration_ms: 6000 }, 9), timed(8, "text_delta", { content: "answer" }, 10), timed(9, "completed", {}, 12)
    ]);
    expect(result.messages.map((message) => message.kind)).toEqual(["thinking", "tool", "thinking", "compact", "message", "message"]);
    expect(result.messages[0]).toMatchObject({ phase: 1, turn: 1, stageDurationMs: 2000, thinkingStatus: "completed" });
    expect(result.messages[2]).toMatchObject({ phase: 2, stageDurationMs: 3000, thinkingStatus: "completed" });
    expect(result.messages[1]?.tool).toMatchObject({ output: "done", status: "completed", durationMs: 6000 });
    expect(result.messages[3]?.content).toBe("durable summary");
    expect(result.messages.at(-1)?.durationMs).toBe(12000);
  });
  it("does not assign a tool provider or duration to the main reply", () => {
    const result = applyConversationEvents(session, [event(1, "started", { provider: "main-provider", model: "main-model" }), event(2, "tool_call", { tool_id: "image" }), event(3, "tool_result", { tool_id: "image", provider: "image-provider", model: "image-model", duration_ms: 9000 }), event(4, "text_delta", { content: "answer" }), event(5, "completed", {})]);
    expect(result.messages.at(-1)).toMatchObject({ provider: "main-provider", model: "main-model" });
    expect(result.messages.at(-1)?.durationMs).toBeUndefined();
  });
  it("recovers generated images from tool results and deduplicates dedicated image events", () => {
    const asset = { asset_id: "asset-one", media_type: "image/png", url: "/tenant/media/assets/asset-one" };
    const result = applyConversationEvents(session, [
      event(1, "tool_call", { tool_id: "image", tool_name: "GenerateImage" }),
      event(2, "text_delta", { content: "Before image" }),
      event(3, "tool_result", { tool_id: "image", output: JSON.stringify(asset) }),
      event(4, "image_artifact", asset), event(5, "text_delta", { content: "After image" })
    ]);
    expect(result.messages.map((message) => message.kind)).toEqual(["tool", "message", "message", "message"]);
    expect(result.messages.map((message) => message.artifacts?.[0]?.asset_id).filter(Boolean)).toEqual(["asset-one"]);
    expect(result.messages[1]?.content).toBe("Before image");
    expect(result.messages[2]?.artifacts).toEqual([{ asset_id: "asset-one" }]);
    expect(result.messages[0]?.tool?.status).toBe("completed");
    const explicitFirst = applyConversationEvents(session, [event(1, "tool_call", { tool_id: "image", tool_name: "EditImage" }), event(2, "image_artifact", { asset }), event(3, "tool_result", { tool_id: "image", output: JSON.stringify({ asset }) })]);
    expect(explicitFirst.messages.filter((message) => message.artifacts?.length)).toHaveLength(1);
  });
  it("preserves available attachment data while retaining metadata-only history", () => {
    const metadata = { type: "image", media_type: "image/png", name: "clipboard.png", size_bytes: 4 };
    const result = applyConversationEvents(session, [event(1, "message", { content: "look", attachments: [{ ...metadata, inline_data: "cG5n" }, { ...metadata, attachment_id: "asset-one", url: "/tenant/media/assets/asset-one" }, metadata] })]);
    expect(result.messages[0]?.attachments?.[0]?.inline_data).toBe("cG5n");
    expect(result.messages[0]?.attachments?.[1]?.url).toBe("/tenant/media/assets/asset-one");
    expect(result.messages[0]?.attachments?.[2]?.inline_data).toBeUndefined();
  });

  it("rehydrates a pending question and resolves the matching request in place", () => {
    const requested = applyConversationEvents(session, [event(1, "user_question_request", {
      request_id: "question-1", question: "Which release channel?", choices: ["Stable", "Canary"], expires_at: "2026-09-07T01:00:00Z"
    })]);
    expect(requested.runs[0]?.status).toBe("waiting_input");
    expect(requested.messages).toHaveLength(1);
    expect(requested.messages[0]).toMatchObject({ kind: "question", content: "Which release channel?" });
    expect((requested.messages[0] as unknown as { question: unknown }).question).toEqual({
      taskID: 14, requestID: "question-1", choices: ["Stable", "Canary"], expiresAt: "2026-09-07T01:00:00Z", status: "pending"
    });

    const resolved = applyConversationEvents(requested, [event(2, "user_question_resolved", {
      request_id: "question-1", status: "answered", answer: "Canary"
    })]);
    expect(resolved.runs[0]?.status).toBe("running");
    expect((resolved.messages[0] as unknown as { question: unknown }).question).toMatchObject({ status: "answered", answer: "Canary" });
  });

  it("makes pending question actions unavailable when the run terminates", () => {
    const result = applyConversationEvents(session, [
      event(1, "user_question_request", { request_id: "question-1", question: "Continue?", choices: ["Yes", "No"] }),
      event(2, "cancelled", {})
    ]);
    expect((result.messages[0] as unknown as { question: unknown }).question).toMatchObject({ status: "cancelled" });
    expect(result.messages[0]?.status).toBe("stopped");
  });

  it("does not turn the explicitly linked question tool into a duplicate legacy question", () => {
    const result = applyConversationEvents(session, [
      event(1, "tool_call", { tool_id: "ask-1", tool_name: "AskUserQuestion", input: { question: "Continue?", choices: ["Yes", "No"] } }),
      event(2, "user_question_request", { request_id: "question-1", tool_id: "ask-1", question: "Continue?", choices: ["Yes", "No"] }),
      event(3, "user_question_resolved", { request_id: "question-1", status: "expired" }),
      event(4, "tool_result", { tool_id: "ask-1", tool_name: "AskUserQuestion", is_error: true, error: "question expired" })
    ]);

    expect(result.messages.filter((message) => message.kind === "question")).toHaveLength(1);
    expect(result.messages.find((message) => message.kind === "question")?.question).toMatchObject({ status: "expired" });
    expect(result.messages.find((message) => message.kind === "question")?.question?.legacy).toBeUndefined();
    expect(result.messages.find((message) => message.kind === "tool")?.tool).toMatchObject({ id: "tool:14:ask-1", status: "failed" });
  });

  it("associates an old question request with its active tool by prompt", () => {
    const result = applyConversationEvents(session, [
      event(1, "tool_call", { tool_id: "ask-old", tool_name: "AskUserQuestion", input: { question: "Continue?", choices: ["Yes"] } }),
      event(2, "user_question_request", { request_id: "question-old", question: "Continue?", choices: ["Yes"] }),
      event(3, "user_question_resolved", { request_id: "question-old", status: "cancelled" }),
      event(4, "tool_result", { tool_id: "ask-old", tool_name: "AskUserQuestion", is_error: true, error: "question cancelled" })
    ]);

    expect(result.messages.filter((message) => message.kind === "question")).toHaveLength(1);
    expect(result.messages.find((message) => message.kind === "question")?.question).toMatchObject({ status: "cancelled" });
    expect(result.messages.find((message) => message.kind === "question")?.question?.legacy).toBeUndefined();
  });

  it("turns a historical failed AskUserQuestion tool record into a readable non-actionable question", () => {
    const result = applyConversationEvents(session, [
      event(1, "tool_call", { tool_id: "ask-1", tool_name: "AskUserQuestion", input: { questions: [{ question: "Pick a database", options: [{ label: "Postgres" }, { label: "MySQL" }] }] } }),
      event(2, "tool_result", { tool_id: "ask-1", tool_name: "AskUserQuestion", is_error: true, error: "callback unavailable" }),
      event(3, "completed", {})
    ]);
    expect(result.messages).toHaveLength(1);
    expect(result.messages[0]).toMatchObject({ kind: "question", content: "Pick a database" });
    expect((result.messages[0] as unknown as { question: unknown }).question).toMatchObject({ choices: ["Postgres", "MySQL"], status: "unavailable", legacy: true });
  });

  it("preserves native AskUserQuestion choices in historical failed tools", () => {
    const result = applyConversationEvents(session, [
      event(1, "tool_call", { tool_id: "ask", tool_name: "AskUserQuestion", input: { question: "Diagram?", choices: ["Architecture", "Sequence"] } }),
      event(2, "tool_result", { tool_id: "ask", tool_name: "AskUserQuestion", is_error: true }),
      event(3, "completed", {})
    ]);
    expect(result.messages[0].question?.choices).toEqual(["Architecture", "Sequence"]);
  });

  it("keeps a run active until its terminal event after question expiry", () => {
    const result = applyConversationEvents(session, [
      event(1, "user_question_request", { request_id: "q1", question: "Continue?", choices: ["Yes"] }),
      event(2, "user_question_resolved", { request_id: "q1", status: "expired" })
    ]);
    expect(result.messages[0].question?.status).toBe("expired");
    expect(result.runs[0].status).toBe("running");
    expect(result.runs[0].endedAt).toBeUndefined();
  });

  it("marks a terminal error after an image artifact without losing the actual failure", () => {
    const result = applyConversationEvents(session, [
      event(1, "image_artifact", { asset_id: "asset-ready" }),
      event(2, "failed", { code: "provider_error", error: "upstream image download failed" })
    ]);
    expect(result.messages[0]?.artifacts).toEqual([{ asset_id: "asset-ready" }]);
    expect(result.messages[1]).toMatchObject({ kind: "error", content: "upstream image download failed" });
    expect((result.messages[1] as unknown as { error: unknown }).error).toEqual({ code: "provider_error", artifactAvailable: true });
  });
});
