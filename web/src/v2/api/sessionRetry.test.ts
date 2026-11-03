import { describe, expect, it } from "vitest";
import type { ConversationEvent, SessionDetail, SessionMessage } from "../types";
import { SessionControlError } from "./sessionControlClient";
import { sessionRetryInput } from "./sessionRetry";

const message: SessionMessage = { id: "2:3", taskID: 2, role: "assistant", kind: "message", content: "reply", createdAt: "" };
const detail: SessionDetail = {
  ref: "tenant:alpha", source: "tenant", title: "A", shortID: "alpha", status: "completed", updatedAt: "",
  messages: [{ id: "2:2", taskID: 2, role: "user", kind: "message", content: "original", createdAt: "", attachments: [{ type: "image", attachment_id: "image-1", media_type: "image/png", size_bytes: 3, name: "image.png", sha256: "" }] }, message],
  runs: [], activity: [], context: [], changes: []
};

function handoff(sourceRef: string, taskID = 2, id = 1): ConversationEvent {
  const packageID = `handoff:${"a".repeat(64)}`;
  const packageSHA256 = "b".repeat(64);
  return {
    id, task_id: taskID, event_type: "session_handoff",
    payload_json: JSON.stringify({
      schema: "niuma.session-handoff-event.v1", target_task_id: taskID,
      package_id: packageID, package_sha256: packageSHA256,
      package: {
        schema: "niuma.session-handoff.v1", package_id: packageID, package_sha256: packageSHA256,
        source: { ref: sourceRef, cursor: "task_event:1", content_sha256: "c".repeat(64), captured_at: "2026-09-06T00:00:00Z" },
        target: { ref: detail.ref }, objective: "Original objective", constraints: [], stage_summary: "Old snapshot",
        completed: [], open_items: [], risks: [], next_actions: [], evidence: [], budget: { estimated_tokens: 20, limit_tokens: 2048 }
      }
    })
  };
}

describe("session regeneration", () => {
  it("retains original input, image references, session and retry identity", () => {
    expect(sessionRetryInput(detail, message, "retry-key")).toMatchObject({ ref: "tenant:alpha", text: "original", idempotencyKey: "retry-key", attachments: [{ attachment_id: "image-1" }] });
  });
  it("does not regenerate while busy, from local history, or from an unrelated run", () => {
    expect(sessionRetryInput({ ...detail, status: "waiting_permission" }, message, "key")).toBeNull();
    expect(sessionRetryInput({ ...detail, source: "local", ref: "local:alpha" }, message, "key")).toBeNull();
    expect(sessionRetryInput(detail, { ...message, taskID: 99 }, "key")).toBeNull();
  });

  it("recovers original run source refs from durable handoffs and deduplicates them without replaying old snapshots", () => {
    const withContext = { ...detail, events: [handoff("tenant:beta"), handoff("local:workspace", 2, 2), handoff("tenant:beta", 2, 3), handoff(detail.ref, 2, 4)] };
    const input = sessionRetryInput(withContext, message, "retry-context");
    expect(input).toEqual({
      ref: detail.ref, text: "original", attachments: detail.messages[0].attachments,
      sourceRefs: ["tenant:beta", "local:workspace"], idempotencyKey: "retry-context"
    });
    expect(JSON.stringify(input)).not.toContain("Old snapshot");
  });

  it("ignores other runs' handoffs including malformed payloads", () => {
    const withOtherRun = { ...detail, events: [handoff("tenant:other", 3), { ...handoff("tenant:ignored", 4), payload_json: "broken" }, handoff("tenant:original", 2, 3)] };
    expect(sessionRetryInput(withOtherRun, message, "key")?.sourceRefs).toEqual(["tenant:original"]);
    expect(sessionRetryInput({ ...detail, events: [handoff("tenant:other", 3)] }, message, "key")?.sourceRefs).toEqual([]);
  });

  it.each(["", "not json", "null", "[]", "{}"])("rejects a malformed original handoff payload %j explicitly", (payload_json) => {
    expect(() => sessionRetryInput({ ...detail, events: [{ ...handoff("tenant:beta"), payload_json }] }, message, "key"))
      .toThrowError(expect.objectContaining({ name: "SessionControlError", code: "invalid_state" }));
  });

  it.each([
    { name: "event schema", mutate: (value: Record<string, any>) => { value.schema = "unsupported"; } },
    { name: "target task", mutate: (value: Record<string, any>) => { value.target_task_id = 99; } },
    { name: "package schema", mutate: (value: Record<string, any>) => { value.package.schema = "unsupported"; } },
    { name: "target session", mutate: (value: Record<string, any>) => { value.package.target.ref = "tenant:other"; } },
    { name: "missing source", mutate: (value: Record<string, any>) => { delete value.package.source; } },
    { name: "invalid source", mutate: (value: Record<string, any>) => { value.package.source.ref = "foreign:secret"; } },
    { name: "empty source", mutate: (value: Record<string, any>) => { value.package.source.ref = "tenant:"; } },
    { name: "padded source", mutate: (value: Record<string, any>) => { value.package.source.ref = " tenant:beta "; } },
    { name: "malformed unicode source", mutate: (value: Record<string, any>) => { value.package.source.ref = "tenant:\ud800"; } },
    { name: "package identity", mutate: (value: Record<string, any>) => { value.package.package_id = "handoff:other"; } },
    { name: "package hash", mutate: (value: Record<string, any>) => { value.package.package_sha256 = "wrong"; } }
  ])("rejects invalid $name instead of silently stripping context", ({ mutate }) => {
    const event = handoff("tenant:beta");
    const payload = JSON.parse(event.payload_json!);
    mutate(payload);
    const events = [{ ...event, payload_json: JSON.stringify(payload) }];
    expect(() => sessionRetryInput({ ...detail, events }, message, "key")).toThrowError(expect.objectContaining({ name: SessionControlError.name, code: "invalid_state" }));
  });

  it("rejects ambiguous handoff provenance when the original messages have no task identity", () => {
    const messages = detail.messages.map((item) => ({ ...item, taskID: undefined }));
    expect(() => sessionRetryInput({ ...detail, messages, events: [handoff("tenant:beta")] }, messages[1], "key"))
      .toThrowError(expect.objectContaining({ code: "invalid_state" }));
  });
});
