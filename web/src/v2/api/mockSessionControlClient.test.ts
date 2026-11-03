import { describe, expect, it } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import { createMockSessionControlClient } from "./mockSessionControlClient";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "device-a",
  model: "test-model"
};

describe("mock session control client", () => {
  it("persists only the requested title and keeps local sessions read-only", async () => {
    const client = createMockSessionControlClient();
    const before = await client.get(identity, "tenant:beta");
    await client.rename!(identity, { ref: before.ref, id: 2, title: "  New title  " });
    expect(await client.get(identity, before.ref)).toEqual({ ...before, title: "New title" });
    await expect(client.rename!(identity, { ref: "local:workspace", id: 1, title: "New" })).rejects.toMatchObject({ code: "local_read_only" });
    await expect(client.rename!(identity, { ref: before.ref, id: 0, title: "New" })).rejects.toMatchObject({ code: "invalid_request" });
  });

  it("lists managed and read-only local sessions through the same summary contract", async () => {
    const client = createMockSessionControlClient();

    await expect(client.list(identity, { query: "", statuses: [] })).resolves.toEqual([
      expect.objectContaining({ ref: "tenant:alpha", source: "tenant", status: "running", title: "Release coordination" }),
      expect.objectContaining({ ref: "tenant:beta", source: "tenant", status: "completed", title: "Design review" }),
      expect.objectContaining({ ref: "local:workspace", source: "local", status: "idle", title: "Local workspace" })
    ]);
  });

  it("provides one deterministic populated fixture while preserving empty managed and local states", async () => {
    const client = createMockSessionControlClient();

    const populated = await client.get(identity, "tenant:beta");
    const emptyManaged = await client.get(identity, "tenant:alpha");
    const emptyLocal = await client.get(identity, "local:workspace");

    expect(populated.messages).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "fixture-beta-user", role: "user", kind: "message" }),
      expect.objectContaining({ id: "fixture-beta-assistant", role: "assistant", kind: "message" }),
      expect.objectContaining({ id: "fixture-beta-thinking", kind: "thinking" }),
      expect.objectContaining({ id: "fixture-beta-tool", kind: "tool" }),
      expect.objectContaining({ id: "fixture-beta-handoff", kind: "handoff", handoff: expect.objectContaining({ sourceRefs: ["tenant:alpha"], stale: false }) }),
      expect.objectContaining({ id: "fixture-beta-profile-operation", kind: "operation", operation: expect.objectContaining({ kind: "profile_draft", status: "completed", replayed: false }) })
    ]));
    expect(new Set(populated.messages.map((message) => message.id)).size).toBe(populated.messages.length);
    expect(emptyManaged.messages).toEqual([]);
    expect(emptyLocal.messages).toEqual([]);
  });

  it("queues a pending input and immutable source handoff only when a running managed session receives text", async () => {
    const client = createMockSessionControlClient();
    const sourceRefs: Array<"tenant:alpha" | "tenant:beta"> = ["tenant:beta"];
    const input = {
      ref: "tenant:alpha" as const,
      text: "Please review the rollout.",
      attachments: [],
      sourceRefs,
      idempotencyKey: "send-alpha-1"
    };

    const result = await client.send(identity, input);

    expect(result).toMatchObject({ replayed: false, operation: { kind: "send", status: "completed" } });
    expect(result.session.runs[0]).toMatchObject({ status: "running" });
    expect(result.session.messages).toEqual(expect.arrayContaining([
      expect.objectContaining({ role: "user", kind: "message", content: "Please review the rollout." }),
      expect.objectContaining({ kind: "handoff", handoff: expect.objectContaining({ sourceRefs: ["tenant:beta"], stale: false }) }),
      expect.objectContaining({ kind: "operation", operation: expect.objectContaining({ kind: "send", status: "completed" }) })
    ]));
    sourceRefs[0] = "tenant:alpha";
    await expect(client.get(identity, "tenant:alpha")).resolves.toMatchObject({
      messages: expect.arrayContaining([expect.objectContaining({ kind: "handoff", handoff: expect.objectContaining({ sourceRefs: ["tenant:beta"] }) })])
    });
  });

  it("does not attach source refs or create a pending input for blank sends", async () => {
    const client = createMockSessionControlClient();

    await expect(client.send(identity, {
      ref: "tenant:alpha",
      text: "   ",
      attachments: [],
      sourceRefs: ["tenant:beta"],
      idempotencyKey: "blank-send-1"
    })).rejects.toMatchObject({ code: "invalid_state" });
    await expect(client.get(identity, "tenant:alpha")).resolves.toMatchObject({ messages: [], activity: ["Run is active"] });
  });

  it("accepts attachment-only sends using fixture-safe metadata", async () => {
    const client = createMockSessionControlClient();
    const result = await client.send(identity, {
      ref: "tenant:alpha",
      text: "",
      attachments: [{ attachment_id: "att-1", type: "image", media_type: "image/png", name: "screen.png", size_bytes: 5, sha256: "hash" }],
      sourceRefs: [],
      idempotencyKey: "attachment-only-1"
    });

    expect(result.operation.kind).toBe("send");
    expect(result.session.messages.filter((message) => message.operation?.kind === "send")).toHaveLength(1);
  });

  it("replays an idempotent send without adding another pending input", async () => {
    const client = createMockSessionControlClient();
    const input = {
      ref: "tenant:alpha" as const,
      text: "Please review the rollout.",
      attachments: [],
      sourceRefs: [],
      idempotencyKey: "send-alpha-replay"
    };

    const first = await client.send(identity, input);
    const replay = await client.send(identity, input);

    expect(replay.replayed).toBe(true);
    expect(replay.session.messages.filter((message) => message.content === "Please review the rollout.")).toHaveLength(1);
    expect(replay.operation.id).toBe(first.operation.id);
  });

  it("returns an idempotency conflict before validating changed or blank send payloads", async () => {
    const client = createMockSessionControlClient();
    const idempotencyKey = "send-conflict-priority";

    await client.send(identity, {
      ref: "tenant:alpha",
      text: "Original pending input",
      attachments: [],
      sourceRefs: [],
      idempotencyKey
    });

    await expect(client.send(identity, {
      ref: "tenant:alpha",
      text: "Changed pending input",
      attachments: [],
      sourceRefs: [],
      idempotencyKey
    })).rejects.toMatchObject({ code: "idempotency_conflict" });
    await expect(client.send(identity, {
      ref: "tenant:alpha",
      text: "   ",
      attachments: [],
      sourceRefs: [],
      idempotencyKey
    })).rejects.toMatchObject({ code: "idempotency_conflict" });

    const readback = await client.get(identity, "tenant:alpha");
    expect(readback.messages.filter((message) => message.role === "user")).toEqual([
      expect.objectContaining({ content: "Original pending input" })
    ]);
  });

  it("stops the active run and returns the stopped session readback", async () => {
    const client = createMockSessionControlClient();

    const result = await client.stop(identity, { ref: "tenant:alpha", idempotencyKey: "stop-alpha-1" });

    expect(result).toMatchObject({ replayed: false, session: { status: "stopped", runs: [expect.objectContaining({ status: "stopped" })] } });
  });

  it("creates a managed session from an idempotent request", async () => {
    const client = createMockSessionControlClient();

    const first = await client.create(identity, { title: "Migration plan", initialText: "Start here", idempotencyKey: "create-1" });
    const replay = await client.create(identity, { title: "Migration plan", initialText: "Start here", idempotencyKey: "create-1" });

    expect(first.session).toMatchObject({ ref: "tenant:session-001", title: "Migration plan", status: "idle" });
    expect(first.session.messages).toEqual(expect.arrayContaining([expect.objectContaining({ content: "Start here" })]));
    expect(replay).toMatchObject({ replayed: true, session: { ref: "tenant:session-001" } });
  });

  it("returns an idempotency conflict before validating changed or blank create payloads", async () => {
    const client = createMockSessionControlClient();
    const idempotencyKey = "create-conflict-priority";

    await client.create(identity, { title: "Original session", initialText: "Original text", idempotencyKey });

    await expect(client.create(identity, { title: "Changed session", initialText: "Original text", idempotencyKey })).rejects.toMatchObject({
      code: "idempotency_conflict"
    });
    await expect(client.create(identity, { title: "   ", initialText: "Original text", idempotencyKey })).rejects.toMatchObject({
      code: "idempotency_conflict"
    });

    const sessions = await client.list(identity, { query: "", statuses: [] });
    expect(sessions.filter((session) => session.ref.startsWith("tenant:session-"))).toEqual([
      expect.objectContaining({ title: "Original session" })
    ]);
  });

  it("archives a managed session with readback", async () => {
    const client = createMockSessionControlClient();

    await expect(client.archive(identity, { ref: "tenant:beta", idempotencyKey: "archive-beta-1" })).resolves.toMatchObject({
      session: { ref: "tenant:beta", status: "archived" },
      operation: { kind: "archive", status: "completed" }
    });
  });

  it("rejects writes to local sessions with the stable read-only code", async () => {
    const client = createMockSessionControlClient();

    await expect(client.send(identity, {
      ref: "local:workspace",
      text: "Cannot write here",
      attachments: [],
      sourceRefs: [],
      idempotencyKey: "local-send-1"
    })).rejects.toMatchObject({ code: "local_read_only" });
    await expect(client.stop(identity, { ref: "local:workspace", idempotencyKey: "local-stop-1" })).rejects.toMatchObject({ code: "local_read_only" });
    await expect(client.archive(identity, { ref: "local:workspace", idempotencyKey: "local-archive-1" })).rejects.toMatchObject({ code: "local_read_only" });
  });

  it("keeps fixture state isolated per client instance", async () => {
    const firstClient = createMockSessionControlClient();
    const secondClient = createMockSessionControlClient();

    await firstClient.stop(identity, { ref: "tenant:alpha", idempotencyKey: "stop-first-client" });

    await expect(secondClient.get(identity, "tenant:alpha")).resolves.toMatchObject({ status: "running", runs: [expect.objectContaining({ status: "running" })] });
  });
});
