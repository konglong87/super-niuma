import { describe, expect, it, vi } from "vitest";
import {
  ApiError,
  apiErrorMessage,
  cancelAgentTask,
  createRuntimeLoop,
  createAgentTask,
  createTenantSession,
  createGoal,
  getEffectiveSkill,
  getAgentTask,
  getWebAgentConversation,
  getGoal,
  getGoalPlan,
  getLocalTrace,
  getRuntimeTraceArtifact,
  getStatus,
  getRuntimeBackgroundLogs,
  getMobileSession,
  getTenantSkill,
  listGoalEvents,
  listGoalEvidence,
  listGoals,
  listAgentTaskEvents,
  listAgentTasks,
  listLocalTraceSessions,
  listRuntimeBackground,
  listRuntimeEvents,
  listRuntimeRuns,
  listMemoryReviewCandidates,
  listMobileMessages,
  makeHeaders,
  parseSSEFrame,
  presignMobileAttachment,
  reviewMemoryCandidate,
  reviewAutoMemory,
  renderTenantSkillPackage,
  rollbackTenantSkill,
  runRuntimeBackground,
  runGoalOnce,
  saveKnowledgeDocument,
  searchKnowledge,
  saveTenantUser,
  saveTeamMemory,
  listEffectiveSkills,
  listLocalSkills,
  listSkillOverrides,
  listTenantSkills,
  listTenantSessions,
  listWebAgentConversations,
  saveSkillOverride,
  saveTenantSkill,
  publishTenantSkillPackage,
  verifyTenantSkillPackageRuntime,
  resolveAgentTaskPermission,
  sendAgentTaskMessage,
  streamAgentTaskEvents,
  listTelemetry,
  streamMobileMessage,
  stopRuntimeBackground,
  stopGoal,
  resumeGoal,
  updateRuntimeLoop,
  updateAgentTask,
  uploadAttachmentBinary,
  unwrapData
  ,generateImage
  ,editImage
  ,listImageHistory
  ,getImageArtifact
  ,getImageCapabilities
  ,listAgentProfiles
  ,saveAgentProfile
  ,publishAgentProfile
  ,listAgentTeams
  ,saveAgentTeamMembers
  ,listChannelAccounts
  ,getAgentTeamRunTimeline
} from "./api";
import type { IdentityConfig } from "./types";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "api-token",
  mobileJwt: "mobile-token",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "browser-a",
  model: "model-a"
};

describe("api helpers", () => {
  it("builds tenant and dev-device headers", () => {
    vi.spyOn(Date, "now").mockReturnValue(123);
    const headers = makeHeaders(identity);

    expect(headers.get("X-Tenant-Key")).toBe("tenant-a");
    expect(headers.get("X-User-Id")).toBe("user-a");
    expect(headers.get("X-Device-Id")).toBe("browser-a");
    expect(headers.get("X-Trace-Id")).toBe("webui-3f");
    expect(headers.get("Authorization")).toBe("Bearer api-token");
  });

  it("allows durable trace ids for correlated web agent requests", () => {
    const headers = makeHeaders(identity, false, "web-agent-trace-1");

    expect(headers.get("X-Trace-Id")).toBe("web-agent-trace-1");
  });

  it("prefers mobile jwt for mobile requests", () => {
    const headers = makeHeaders(identity, true);

    expect(headers.get("Authorization")).toBe("Bearer mobile-token");
  });

  it("unwraps data envelopes", () => {
    expect(unwrapData({ data: [1, 2] }, [])).toEqual([1, 2]);
    expect(unwrapData({ data: null }, ["fallback"])).toEqual(["fallback"]);
    expect(unwrapData([3], [])).toEqual([3]);
    expect(unwrapData({ ok: true }, ["fallback"])).toEqual(["fallback"]);
  });

  it("loads server status for workspace and primary-model defaults", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true, cwd: "/tmp/work", model: "gpt-5.6-sol" }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await getStatus(identity);

    expect(result.workspace).toBe("/tmp/work");
    expect(result.model).toBe("gpt-5.6-sol");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/status");
  });

  it("normalizes json api errors for status display", () => {
    expect(apiErrorMessage(403, '{"error":"tenant access forbidden"}')).toBe("tenant access forbidden");
    expect(new ApiError(403, '{"message":"forbidden"}').message).toBe("forbidden");
    expect(apiErrorMessage(500, "plain failure")).toBe("plain failure");
  });

  it("uses tenant profile and team routes with versioned payloads", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ profile_key: "copywriter", profile_version: 1 }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 9, profile_key: "writer", profile_version: 1, status: "draft" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: "published" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ id: 3, team_key: "launch", team_version: 1 }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ member_key: "editor" }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ id: 4, provider: "feishu", account_key: "bot-a" }] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const profile = await listAgentProfiles(identity);
    await saveAgentProfile(identity, { profile_key: "writer", scope: "tenant_shared", display_name: "Writer", config: {} as never });
    await publishAgentProfile(identity, "writer", 1);
    const teams = await listAgentTeams(identity);
    await saveAgentTeamMembers(identity, "launch", 1, [{ member_key: "editor", profile_id: 9, role: "coordinator" }]);
    const accounts = await listChannelAccounts(identity);

    expect(profile[0].profile_key).toBe("copywriter");
    expect(teams[0].team_key).toBe("launch");
    expect(accounts[0].account_key).toBe("bot-a");
    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      "/api/tenant/agent-profiles?limit=100",
      "/api/tenant/agent-profiles",
      "/api/tenant/agent-profiles/writer/publish?version=1",
      "/api/tenant/agent-teams?limit=100",
      "/api/tenant/agent-teams/launch/members?version=1",
      "/api/tenant/channel-accounts?limit=100"
    ]);
  });

  it("loads the unified TeamRun event and mailbox timeline", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ run_id: "run-1", events: [{ id: 1, sequence_no: 1, event_type: "member_started", created_at: "2026-10-10T00:00:00Z" }], mailbox: [] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const timeline = await getAgentTeamRunTimeline(identity, "launch", 2, "run-1");

    expect(timeline.events).toHaveLength(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/agent-teams/launch/runs/run-1/events?version=2&limit=500");
  });

  it("parses mobile sse frames", () => {
    expect(parseSSEFrame('event: delta\ndata: {"type":"delta","delta":"hi"}\n')).toEqual({
      type: "delta",
      delta: "hi"
    });
    expect(parseSSEFrame("data: [DONE]")).toBeNull();
    expect(parseSSEFrame("data: plain text")).toEqual({ type: "error", error: "plain text" });
  });

  it("parses agent task sse frames with event names", () => {
    expect(parseSSEFrame('event: connected\ndata: {"task_id":41}\n')).toEqual({ type: "connected", task_id: 41 });
    expect(parseSSEFrame('event: agent_task_event\ndata: {"id":7,"task_id":41,"event_type":"message"}\n')).toEqual({
      type: "agent_task_event",
      event: { id: 7, task_id: 41, event_type: "message" }
    });
  });

  it("loads local trace sessions through trace source local", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          source: "local",
          data: [{ source: "local", session_id: "session-a", title: "CLI run" }]
        }),
        { status: 200, headers: { "content-type": "application/json" } }
      )
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await listLocalTraceSessions(identity);

    expect(result[0].session_id).toBe("session-a");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/trace/api/sessions?source=local&limit=1000");
  });

  it("loads local trace detail by encoded session id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ source: "local", session_id: "session/a", summary: { messages: 1 } }), {
        status: 200,
        headers: { "content-type": "application/json" }
      })
    );
    vi.stubGlobal("fetch", fetchMock);

    await getLocalTrace(identity, "session/a");

    expect(fetchMock.mock.calls[0][0]).toBe("/api/trace/api/sessions/session%2Fa?source=local");
  });

  it("downloads an authenticated runtime trace artifact", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const artifact = await getRuntimeTraceArtifact(identity, "session/a", "local");

    expect(artifact.type).toBe("application/json");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/trace/api/sessions/session%2Fa/export?source=local&schema=runtime-trace-v1");
    expect((fetchMock.mock.calls[0][1].headers as Headers).get("Authorization")).toBe(`Bearer ${identity.apiToken}`);
  });

  it("calls image generation and edit endpoints with tenant headers", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ asset: { asset_id: "a1" } }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ asset: { asset_id: "a2" } }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ asset_id: "a1" }] }), { status: 200 }))
      .mockResolvedValueOnce(new Response("image-bytes", { status: 200, headers: { "content-type": "image/png" } }));
    vi.stubGlobal("fetch", fetchMock);

    await generateImage(identity, 7, { prompt: "bridge" });
    await editImage(identity, 7, { prompt: "blue bridge", source_asset_id: "a1", image: new File(["x"], "source.png", { type: "image/png" }) });
    expect(await listImageHistory(identity, 7)).toHaveLength(1);
    expect((await getImageArtifact(identity, "a1")).type).toBe("image/png");

    expect(fetchMock.mock.calls.map((call) => call[0])).toEqual([
      "/api/tenant/sessions/7/images/generations",
      "/api/tenant/sessions/7/images/edits",
      "/api/tenant/sessions/7/images",
      "/api/tenant/media/assets/a1"
    ]);
    expect((fetchMock.mock.calls[0][1].headers as Headers).get("X-Tenant-Key")).toBe("tenant-a");
    expect((fetchMock.mock.calls[1][1].body as FormData).get("source_asset_id")).toBe("a1");
    expect((fetchMock.mock.calls[1][1].headers as Headers).get("Content-Type")).toBeNull();
  });

  it("loads sanitized image capabilities", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [{ provider: "agnes", model: "agnes-image-2.5-flash", capability: { resolutions: ["2K"] } }] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const capabilities = await getImageCapabilities(identity);
    expect(capabilities[0].model).toBe("agnes-image-2.5-flash");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/images/capabilities");
  });

  it("manages runtime background loops", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ data: [{ id: "bg_1", schedule_id: "sched_1", kind: "loop", prompt: "drink water" }] }), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: "bg_1", logs: "full log", log_tail: "tail" }), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: "bg_1", schedule_id: "sched_1", stopped: true, disabled: true }), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: "bg_2", schedule_id: "sched_2", prompt: "stretch" }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: "bg_1", schedule_id: "sched_1", prompt: "drink more water" }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: "bg_1", schedule_id: "sched_1", run_count: 5 }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ id: "run_1", status: "completed" }] }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ data: [{ id: "evt_1", type: "run_finished" }], next_offset: 42 }), {
          status: 200,
          headers: { "content-type": "application/json" }
        })
      );
    vi.stubGlobal("fetch", fetchMock);

    const jobs = await listRuntimeBackground(identity, "loop");
    const logs = await getRuntimeBackgroundLogs(identity, "bg_1", 500);
    const stopped = await stopRuntimeBackground(identity, "sched_1");
    const created = await createRuntimeLoop(identity, { prompt: "stretch", interval: "15m" });
    const updated = await updateRuntimeLoop(identity, "sched_1", { prompt: "drink more water", interval_seconds: 1200 });
    const ran = await runRuntimeBackground(identity, "sched_1");
    const runs = await listRuntimeRuns(identity, "sched_1");
    const events = await listRuntimeEvents(identity, 0, 10);

    expect(jobs[0]).toMatchObject({ id: "bg_1", schedule_id: "sched_1", prompt: "drink water" });
    expect(logs.log_tail).toBe("tail");
    expect(stopped).toMatchObject({ id: "bg_1", disabled: true });
    expect(created.prompt).toBe("stretch");
    expect(updated.prompt).toBe("drink more water");
    expect(ran.run_count).toBe(5);
    expect(runs[0].status).toBe("completed");
    expect(events.next_offset).toBe(42);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/runtime/background?kind=loop&tail=4000&limit=100");
    expect(fetchMock.mock.calls[1][0]).toBe("/api/runtime/background/bg_1/logs?tail=500");
    expect(fetchMock.mock.calls[2][0]).toBe("/api/runtime/background/sched_1/stop");
    expect(fetchMock.mock.calls[3][0]).toBe("/api/runtime/background");
    expect(fetchMock.mock.calls[4][0]).toBe("/api/runtime/background/sched_1");
    expect(fetchMock.mock.calls[5][0]).toBe("/api/runtime/background/sched_1/run");
    expect(fetchMock.mock.calls[6][0]).toBe("/api/runtime/background/sched_1/runs?limit=50");
    expect(fetchMock.mock.calls[7][0]).toBe("/api/runtime/background/events?offset=0&limit=10");
  });

  it("sends attachments through mobile stream", async () => {
    const body = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('data: {"type":"message_stop","status":"completed"}\n\n'));
        controller.close();
      }
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(body, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await streamMobileMessage(identity, 7, "hello", [{ type: "image", url: "https://cdn.test/image.png" }], {
      onEvent: vi.fn(),
      onDone: vi.fn()
    });

    const requestBody = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/mobile/chat/sessions/7/messages/stream");
    expect(requestBody.attachments).toEqual([{ type: "image", url: "https://cdn.test/image.png" }]);
  });

  it("times out mobile streams", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => {
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    try {
      const result = streamMobileMessage(
        identity,
        7,
        "hello",
        [],
        {
          onEvent: vi.fn(),
          onDone: vi.fn()
        },
        undefined,
        10
      );
      const assertion = expect(result).rejects.toMatchObject({ name: "TimeoutError" });
      await vi.advanceTimersByTimeAsync(10);
      await assertion;
    } finally {
      vi.useRealTimers();
    }
  });

  it("presigns mobile attachments", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          attachment_id: "att_1",
          object_key: "tenant/user/att_1.png",
          upload_url: "https://uploads.test/att_1.png",
          expires_at: "2026-06-16T00:00:00Z",
          attachment: { attachment_id: "att_1", type: "image", url: "https://uploads.test/att_1.png" }
        }),
        { status: 200, headers: { "content-type": "application/json" } }
      )
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await presignMobileAttachment(identity, { type: "image", media_type: "image/png", name: "a.png" });

    expect(result.attachment_id).toBe("att_1");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/mobile/chat/attachments/presign");
  });

  it("uploads attachment binaries to presigned urls", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const file = new File(["hello"], "hello.txt", { type: "text/plain" });
    await uploadAttachmentBinary("https://uploads.test/hello.txt", file);

    expect(fetchMock.mock.calls[0][0]).toBe("https://uploads.test/hello.txt");
    expect(fetchMock.mock.calls[0][1]).toMatchObject({
      method: "PUT",
      body: file
    });
    expect((fetchMock.mock.calls[0][1].headers as Record<string, string>)["Content-Type"]).toBe("text/plain");
  });

  it("saves tenant users through admin api", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 9 }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await saveTenantUser(identity, {
      user_key: "user-b",
      display_name: "User B",
      email: "user-b@example.test",
      role: "member",
      status: "active"
    });

    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/users");
    expect(fetchMock.mock.calls[0][1].method).toBe("POST");
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toMatchObject({
      user_key: "user-b",
      display_name: "User B",
      role: "member",
      status: "active"
    });
  });

  it("filters telemetry by selected session search", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await listTelemetry(identity, "42");

    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/telemetry?limit=50&search=42");
  });

  it("manages tenant agent tasks", async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url === "/api/tenant/agent-tasks?limit=50") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: 41, agent_name: "reviewer", status: "running" }] }), { status: 200 }));
      }
      if (url === "/api/tenant/agent-tasks/41" && init?.method !== "PATCH") {
        return Promise.resolve(new Response(JSON.stringify({ id: 41, agent_name: "reviewer", status: "running" }), { status: 200 }));
      }
      if (url === "/api/tenant/agent-tasks/41/events?limit=100") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: 7, task_id: 41, event_type: "started" }] }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ id: 99 }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await listAgentTasks(identity);
    const task = await getAgentTask(identity, 41);
    const created = await createAgentTask(identity, { agent_name: "coder", prompt: "build", status: "running" });
    await updateAgentTask(identity, 41, { status: "completed", result_json: { ok: true } });
    const messageId = await sendAgentTaskMessage(identity, 41, { from_agent: "coordinator", content: "continue" });
    await resolveAgentTaskPermission(identity, 41, "perm-1", { allowed: true, reason: "ok" });
    await cancelAgentTask(identity, 41);
    const events = await listAgentTaskEvents(identity, 41);

    expect(tasks[0].agent_name).toBe("reviewer");
    expect(task.id).toBe(41);
    expect(created).toBe(99);
    expect(messageId).toBe(99);
    expect(events[0].event_type).toBe("started");
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/agent-tasks/41/message", "POST"]);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/agent-tasks/41/permissions/perm-1", "PATCH"]);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/agent-tasks/41/events?limit=100", "GET"]);
    const patchCall = fetchMock.mock.calls.find((call) => call[0] === "/api/tenant/agent-tasks/41" && call[1]?.method === "PATCH");
    expect(JSON.parse(patchCall?.[1]?.body as string)).toEqual({ status: "completed", result_json: { ok: true } });
    const permissionCall = fetchMock.mock.calls.find((call) => call[0] === "/api/tenant/agent-tasks/41/permissions/perm-1");
    expect(JSON.parse(permissionCall?.[1]?.body as string)).toEqual({ allowed: true, reason: "ok" });
  });

  it("manages tenant sessions for web agent conversations", async () => {
    const fetchMock = vi.fn((url: string, _init?: RequestInit) => {
      if (url === "/api/tenant/sessions?limit=100") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: 17, session_key: "web-agent-session", title: "yu001" }] }), { status: 200 }));
      }
      if (url === "/api/tenant/web-agent/conversations?limit=100") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: "session:17", session_id: 17, title: "yu001", latest_task: { id: 41 }, tasks: [{ id: 41 }] }] }), { status: 200 }));
      }
      if (url === "/api/tenant/web-agent/conversations/session%3A17?limit=100&event_limit=500") {
        return Promise.resolve(new Response(JSON.stringify({ id: "session:17", session_id: 17, title: "yu001", latest_task: { id: 41 }, tasks: [{ id: 41 }], events: [], usage: { total_runs: 1 } }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ id: 17 }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const sessions = await listTenantSessions(identity, 100);
    const conversations = await listWebAgentConversations(identity, 100);
    const detail = await getWebAgentConversation(identity, "session:17", { limit: 100, eventLimit: 500 });
    const sessionID = await createTenantSession(identity, {
      session_key: "web-agent-session",
      title: "yu001",
      status: "active",
      model: "model-a",
      cwd: "/repo",
      metadata_json: "{\"source\":\"webui-agent\"}"
    });

    expect(sessions[0].id).toBe(17);
    expect(conversations[0].session_id).toBe(17);
    expect(detail.id).toBe("session:17");
    expect(sessionID).toBe(17);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/sessions?limit=100", "GET"]);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/web-agent/conversations?limit=100", "GET"]);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/web-agent/conversations/session%3A17?limit=100&event_limit=500", "GET"]);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/sessions", "POST"]);
  });

  it("lists tenant agent task events after a cursor", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [{ id: 8, task_id: 41, event_type: "completed" }] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const signal = new AbortController().signal;

    const events = await listAgentTaskEvents(identity, 41, { afterID: 7, limit: 500, signal });

    expect(events[0].id).toBe(8);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/agent-tasks/41/events?limit=500&after_id=7");
    expect(fetchMock.mock.calls[0][1]?.signal).toBe(signal);
  });

  it("uses agent task trace id as the HTTP trace header for create and message", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ id: 99 }), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);

    await createAgentTask(identity, { agent_name: "web-agent", prompt: "build", trace_id: "web-agent-trace-2" });
    await sendAgentTaskMessage(identity, 99, { from_agent: "webui", content: "continue", trace_id: "web-agent-trace-2" });

    for (const call of fetchMock.mock.calls) {
      const headers = call[1]?.headers as Headers;
      expect(headers.get("X-Trace-Id")).toBe("web-agent-trace-2");
    }
  });

  it("streams tenant agent task events with api auth headers", async () => {
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('event: connected\ndata: {"task_id":41}\n\n'));
        controller.enqueue(new TextEncoder().encode('event: agent_task_event\ndata: {"id":7,"task_id":41,"event_type":"message"}\n\n'));
        controller.close();
      }
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream, { status: 200, headers: { "content-type": "text/event-stream" } }));
    vi.stubGlobal("fetch", fetchMock);
    const events: unknown[] = [];

    await streamAgentTaskEvents(identity, 41, {
      onEvent: (event) => events.push(event),
      onDone: () => events.push("done")
    }, undefined, 7);

    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/agent-tasks/41/events/stream?limit=500&after_id=7");
    const headers = fetchMock.mock.calls[0][1].headers as Headers;
    expect(headers.get("Authorization")).toBe("Bearer api-token");
    expect(headers.get("X-Tenant-Key")).toBe("tenant-a");
    expect(events).toContainEqual({ type: "connected", task_id: 41 });
    expect(events).toContainEqual({ type: "agent_task_event", event: { id: 7, task_id: 41, event_type: "message" } });
    expect(events).toContain("done");
  });

  it("manages tenant goals", async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url === "/api/tenant/goals?limit=50") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: "goal_1", objective: "ship", status: "active" }] }), { status: 200 }));
      }
      if (url === "/api/tenant/goals/goal_1") {
        return Promise.resolve(new Response(JSON.stringify({ id: "goal_1", objective: "ship", status: "active" }), { status: 200 }));
      }
      if (url === "/api/tenant/goals/goal_1/events?limit=100") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: "evt_1", goal_id: "goal_1", type: "goal_started" }] }), { status: 200 }));
      }
      if (url === "/api/tenant/goals/goal_1/plan") {
        return Promise.resolve(new Response(JSON.stringify({ goal_id: "goal_1", current_step_id: "step_verify" }), { status: 200 }));
      }
      if (url === "/api/tenant/goals/goal_1/evidence?limit=20") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ id: "ev_1", goal_id: "goal_1", summary: "verified" }] }), { status: 200 }));
      }
      if (url === "/api/tenant/goals/goal_1/run?evaluator=deterministic") {
        return Promise.resolve(new Response(JSON.stringify({ goal: { id: "goal_1", status: "active" } }), { status: 200 }));
      }
      if (url === "/api/tenant/goals") {
        return Promise.resolve(new Response(JSON.stringify({ id: "goal_2", objective: "new", status: "active" }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ id: "goal_1", status: init?.method === "POST" ? "active" : "stopped" }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const goals = await listGoals(identity);
    const goal = await getGoal(identity, "goal_1");
    const created = await createGoal(identity, { objective: "new", model: "model-a" });
    const stopped = await stopGoal(identity, "goal_1");
    const resumed = await resumeGoal(identity, "goal_1", true);
    const run = await runGoalOnce(identity, "goal_1", "deterministic");
    const events = await listGoalEvents(identity, "goal_1");
    const plan = await getGoalPlan(identity, "goal_1");
    const evidence = await listGoalEvidence(identity, "goal_1");

    expect(goals[0].id).toBe("goal_1");
    expect(goal.objective).toBe("ship");
    expect(created.id).toBe("goal_2");
    expect(stopped.id).toBe("goal_1");
    expect(resumed.id).toBe("goal_1");
    expect(run.goal?.id).toBe("goal_1");
    expect(events[0].type).toBe("goal_started");
    expect(plan.current_step_id).toBe("step_verify");
    expect(evidence[0].summary).toBe("verified");
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/goals/goal_1/stop", "POST"]);
    expect(fetchMock.mock.calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/goals/goal_1/resume?force=true", "POST"]);
  });

  it("searches tenant knowledge", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [{ content: "matched", search_mode: "fulltext" }] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await searchKnowledge(identity, "project rule", 5);

    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/knowledge/search");
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ query: "project rule", limit: 5 });
    expect(result[0].search_mode).toBe("fulltext");
  });

  it("manages tenant skills and overrides", async () => {
    const fetchMock = vi.fn((url: string, _init?: RequestInit) => {
      if (url === "/api/tenant/effective-skills?enabled=true&limit=50") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ skill_key: "review", source: "tenant" }] }), { status: 200 }));
      }
      if (url === "/api/local/skills") {
        return Promise.resolve(new Response(JSON.stringify({
          workspace: "/workspace/project",
          data: [{ name: "anysearch", path: "/Users/test/.claude/skills/anysearch/SKILL.md", source: "user" }]
        }), { status: 200 }));
      }
      if (url === "/api/tenant/effective-skills?skill_key=review&version=2") {
        return Promise.resolve(new Response(JSON.stringify({ skill_key: "review", source: "tenant", version: 2 }), { status: 200 }));
      }
      if (url === "/api/tenant/skills?limit=50") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ skill_key: "review", name: "Review" }] }), { status: 200 }));
      }
      if (url === "/api/tenant/skills?skill_key=review&version=3") {
        return Promise.resolve(new Response(JSON.stringify({ skill_key: "review", name: "Review", version: 3, content_md: "# Old" }), { status: 200 }));
      }
      if (url === "/api/tenant/skills/rollback") {
        return Promise.resolve(new Response(JSON.stringify({ id: 1, skill_key: "review", from_version: 1, version: 3 }), { status: 200 }));
      }
      if (url === "/api/tenant/skill-packages/render") {
        return Promise.resolve(new Response(JSON.stringify({ skill_key: "review", package_sha256: "sha-render", runtime_md: "# Runtime", manifest: { files: [] } }), { status: 200 }));
      }
      if (url === "/api/tenant/skill-packages/publish") {
        return Promise.resolve(new Response(JSON.stringify({ id: 9, skill_key: "review", version: 4, package_sha256: "sha-publish", manifest: { files: [] } }), { status: 200 }));
      }
      if (url === "/api/tenant/skill-packages/verify-runtime") {
        return Promise.resolve(new Response(JSON.stringify({ ok: true, trace_id: "trace-verify", tenant_runtime: { loaded_keys: ["review"], versions: ["4"], bytes: 20 } }), { status: 200 }));
      }
      if (url === "/api/tenant/skill-overrides?limit=50") {
        return Promise.resolve(new Response(JSON.stringify({ data: [{ skill_key: "review", enabled: true }] }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify({ id: 1 }), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    const effective = await listEffectiveSkills(identity);
    const local = await listLocalSkills(identity);
    const tenant = await listTenantSkills(identity);
    const overrides = await listSkillOverrides(identity);
    await saveTenantSkill(identity, { skill_key: "review", name: "Review", version: 2, enabled: true, content_md: "# Review" });
    await saveSkillOverride(identity, { skill_key: "review", version: 2, enabled: false, config_json: '{"source":"test"}' });
    const effectiveDetail = await getEffectiveSkill(identity, "review", 2);
    const rollback = await rollbackTenantSkill(identity, "review", 1);
    const rendered = await renderTenantSkillPackage(identity, { skill_key: "review", name: "Review", source_path: "/tmp/review" });
    const published = await publishTenantSkillPackage(identity, { skill_key: "review", name: "Review", content_base64: "emlw", enabled: true });
    const verify = await verifyTenantSkillPackageRuntime(identity, { skill_key: "review", schema_name: "review_decision_v1", expected_package_sha256: "sha-publish", expected_version: 4 });
    const rolledBackSkill = await getTenantSkill(identity, "review", 3);

    expect(effective[0].source).toBe("tenant");
    expect(local[0].name).toBe("anysearch");
    expect(effectiveDetail.skill_key).toBe("review");
    expect(rollback.id).toBe(1);
    expect(rendered.package_sha256).toBe("sha-render");
    expect(published.version).toBe(4);
    expect(verify.ok).toBe(true);
    expect(rolledBackSkill.content_md).toBe("# Old");
    expect(tenant[0].name).toBe("Review");
    expect(overrides[0].enabled).toBe(true);
    const calls = fetchMock.mock.calls as Array<[string, RequestInit?]>;
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skills", "POST"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skill-overrides", "POST"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/effective-skills?skill_key=review&version=2", "GET"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skills/rollback", "POST"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skill-packages/render", "POST"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skill-packages/publish", "POST"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skill-packages/verify-runtime", "POST"]);
    expect(calls.map((call) => [call[0], call[1]?.method || "GET"])).toContainEqual(["/api/tenant/skills?skill_key=review&version=3", "GET"]);
    expect(JSON.parse(calls[4][1]?.body as string)).toMatchObject({ skill_key: "review", version: 2, enabled: true });
    expect(JSON.parse(calls[5][1]?.body as string)).toMatchObject({ skill_key: "review", enabled: false, config_json: '{"source":"test"}' });
    expect(JSON.parse(calls[7][1]?.body as string)).toEqual({ skill_key: "review", version: 1 });
    expect(JSON.parse(calls[8][1]?.body as string)).toMatchObject({ skill_key: "review", source_path: "/tmp/review" });
    expect(JSON.parse(calls[9][1]?.body as string)).toMatchObject({ skill_key: "review", enabled: true, content_base64: "emlw" });
    expect(JSON.parse(calls[10][1]?.body as string)).toMatchObject({ skill_key: "review", schema_name: "review_decision_v1", expected_version: 4 });
  });

  it("saves knowledge documents and scoped memory", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ id: 1 }), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);

    await saveKnowledgeDocument(identity, { title: "Runbook", content: "Use code mode", source_type: "webui" });
    await saveTeamMemory(identity, { memory_key: "team.rule", content: "Prefer tests" });

    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/knowledge/documents");
    expect(fetchMock.mock.calls[0][1].method).toBe("POST");
    expect(fetchMock.mock.calls[1][0]).toBe("/api/tenant/team-memory");
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toMatchObject({ memory_key: "team.rule" });
  });

  it("reviews automem candidates", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 7 }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await reviewAutoMemory(identity, { memory_key: "auto.pending.pref", action: "approve" });

    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/automem/review");
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ memory_key: "auto.pending.pref", action: "approve" });
  });

  it("lists and reviews unified memory candidates", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ data: [{ memory_key: "explicit.pending.pref" }] }), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);

    const candidates = await listMemoryReviewCandidates(identity, {
      candidateType: "explicit_remember",
      riskStatus: "low_risk",
      sourceSessionId: 42
    });
    await reviewMemoryCandidate(identity, { memory_key: "explicit.pending.pref", action: "reject" });

    expect(candidates).toEqual([{ memory_key: "explicit.pending.pref" }]);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/tenant/memory-review/candidates?limit=50&candidate_type=explicit_remember&risk_status=low_risk&source_session_id=42");
    expect(fetchMock.mock.calls[1][0]).toBe("/api/tenant/memory-review/review");
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ memory_key: "explicit.pending.pref", action: "reject" });
  });

  it("loads mobile source messages with mobile auth", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ data: [{ id: 9, role: "user", content: "remember that I prefer Go examples" }] }), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);

    const messages = await listMobileMessages(identity, 3);

    expect(messages).toEqual([{ id: 9, role: "user", content: "remember that I prefer Go examples" }]);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/mobile/chat/sessions/3/messages?limit=100");
    expect(fetchMock.mock.calls[0][1].headers.get("Authorization")).toBe("Bearer mobile-token");
  });

  it("loads mobile session detail with latest recap", async () => {
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            id: 3,
            session_key: "s3",
            status: "active",
            latest_recap: { message_id: 12, content: "本次会话目标：验证 recap", model: "recap-small" }
          }),
          { status: 200 }
        )
      )
    );
    vi.stubGlobal("fetch", fetchMock);

    const session = await getMobileSession(identity, 3);

    expect(session.latest_recap?.content).toContain("验证 recap");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/mobile/chat/sessions/3");
    expect(fetchMock.mock.calls[0][1].headers.get("Authorization")).toBe("Bearer mobile-token");
  });
});
