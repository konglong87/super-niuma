import { expect, type Locator, type Page, type Route, test } from "@playwright/test";

async function expectLocatorWithinViewport(locator: Locator) {
  const box = await locator.boundingBox();
  expect(box).not.toBeNull();
  if (!box) {
    return;
  }
  const viewport = locator.page().viewportSize();
  expect(viewport).not.toBeNull();
  if (!viewport) {
    return;
  }
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(viewport.width);
  expect(box.y + box.height).toBeLessThanOrEqual(viewport.height);
}

async function mockEmptyWebAgentApi(page: Page) {
  await page.route("**/tenant/agent-tasks?limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) });
  });
  await page.route("**/api/tenant/agent-tasks?limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) });
  });
  await page.route("**/tenant/sessions?limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) });
  });
  await page.route("**/api/tenant/sessions?limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) });
  });
  await page.route("**/agent/workspaces/validate", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        cwd: "/Users/example/GolandProjects/niuma",
        workspace_name: "niuma",
        exists: true,
        is_dir: true,
        git_root: "/Users/example/GolandProjects/niuma",
        is_git_repo: true
      })
    });
  });
}

test.beforeEach(async ({ page }) => {
  await page.route("**/api/tenant/tenants?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 4, tenant_key: "webui-local", name: "WebUI Local", status: "active" },
          { id: 5, tenant_key: "webui-alt", name: "WebUI Alt", status: "active" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/users?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 6, user_key: "webui-local-user", display_name: "WebUI Local User", role: "owner", status: "active" },
          { id: 7, user_key: "webui-alt-user", display_name: "WebUI Alt User", role: "member", status: "active" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/users", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 9 }) });
  });
  await page.route("**/api/runtime/background?kind=loop&tail=4000&limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "bg_loop_1",
            schedule_id: "sched_loop_1",
            prompt: "drink water",
            cwd: "/Users/example/GolandProjects/niuma",
            kind: "loop",
            status: "running",
            enabled: true,
            interval_seconds: 600,
            run_count: 4,
            log_tail: "remember to drink water"
          }
        ]
      })
    });
  });
  await page.route("**/api/runtime/background/bg_loop_1/logs?tail=12000", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ id: "bg_loop_1", logs: "remember to drink water", log_tail: "remember to drink water" })
    });
  });
  await page.route("**/api/runtime/background/sched_loop_1/runs?limit=50", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: "run_loop_1",
            schedule_id: "sched_loop_1",
            background_id: "bg_loop_1",
            prompt: "drink water",
            cwd: "/Users/example/GolandProjects/niuma",
            status: "completed",
            finished_at: "2026-06-24T10:00:00Z",
            log_bytes: 256
          }
        ]
      })
    });
  });
  await page.route("**/api/runtime/background", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        id: "bg_loop_created",
        schedule_id: "sched_loop_created",
        prompt: "stand up",
        cwd: "/Users/example/GolandProjects/niuma",
        kind: "loop",
        status: "queued",
        enabled: true,
        interval_seconds: 900
      })
    });
  });
  await page.route("**/api/runtime/background/sched_loop_1", async (route) => {
    if (route.request().method() !== "PATCH") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        id: "bg_loop_1",
        schedule_id: "sched_loop_1",
        prompt: "drink more water",
        cwd: "/Users/example/GolandProjects/niuma",
        kind: "loop",
        status: "queued",
        enabled: true,
        interval_seconds: 900
      })
    });
  });
  await page.route("**/api/runtime/background/sched_loop_1/run", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ id: "bg_loop_1", schedule_id: "sched_loop_1", prompt: "drink water", run_count: 5 })
    });
  });
  await page.route("**/api/runtime/background/sched_loop_1/stop", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ id: "bg_loop_1", schedule_id: "sched_loop_1", stopped: true, disabled: true })
    });
  });
  await page.route("**/api/mobile/chat/sessions?limit=50", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 8, session_key: "webui-seeded", title: "Seeded Scenario", status: "active", model: "gpt-test" },
          { id: 7, session_key: "webui-smoke", title: "Smoke Session", status: "active", model: "gpt-test" }
        ]
      })
    });
  });
  await page.route("**/api/mobile/chat/sessions", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ id: 8, session_key: "webui-seeded", title: "Seeded Scenario", status: "active", model: "gpt-test" })
    });
  });
  await page.route("**/api/mobile/chat/sessions/7/messages?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 70, session_id: 7, turn_index: 1, role: "user", content: "hello" },
          { id: 71, session_id: 7, turn_index: 2, role: "assistant", content: "answer", content_json: "{\"mobile\":{\"status\":\"completed\"}}" }
        ]
      })
    });
  });
  await page.route("**/api/mobile/chat/sessions/8/messages?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 80, session_id: 8, turn_index: 1, role: "user", content: "seeded hello" },
          { id: 81, session_id: 8, turn_index: 2, role: "assistant", content: "seeded answer", content_json: "{\"mobile\":{\"status\":\"completed\"}}" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/memories?limit=20", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 1, memory_key: "pref", content: "vim" }] }) });
  });
  await page.route("**/api/tenant/team-memory?limit=20", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 2, memory_key: "team.pref", content: "Prefer tests" }] }) });
  });
  await page.route("**/api/tenant/managed-memory?limit=20", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 3, memory_key: "managed.pref", content: "Use policy" }] }) });
  });
  await page.route("**/api/tenant/memory-review/candidates?limit=50", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) });
  });
  await page.route("**/api/tenant/effective-skills?enabled=true&limit=50", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 1, skill_key: "review", name: "Review", version: 1 }] }) });
  });
  await page.route("**/api/tenant/skills?limit=50", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 2, skill_key: "review", name: "Review", version: 1 }] }) });
  });
  await page.route("**/api/tenant/skill-overrides?limit=50", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) });
  });
  await page.route("**/api/tenant/documents?history=true&limit=20", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 1, doc_type: "CLAUDE.md", content_md: "# Memory" }] }) });
  });
  await page.route("**/api/tenant/knowledge/documents?limit=20", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ id: 1, title: "KB", content: "knowledge", status: "active" }] }) });
  });
  await page.route("**/api/tenant/telemetry?limit=30", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 1, name: "mobile.chat.stream.finished", status: "ok" },
          { id: 2, name: "model.request.finished", status: "ok" },
          { id: 3, name: "query.autocompact.success", status: "ok" },
          { id: 4, name: "tool.execution.finished", status: "ok", properties_json: "{\"active_skill\":\"review\"}" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/telemetry?limit=50&search=**", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 10, name: "mobile.chat.stream.finished", status: "ok", session_id: 8, trace_id: "trace-session-8" },
          { id: 11, name: "model.request.finished", status: "ok", session_id: 8, trace_id: "trace-session-8" },
          { id: 12, name: "query.run.finished", status: "ok", session_id: 8, trace_id: "trace-session-8" }
        ]
      })
    });
  });
  await page.route("**/api/trace/api/sessions?source=local&limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ source: "local", data: [] }) });
  });
  await page.route("**/api/health", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, workspace: "/Users/example/GolandProjects/niuma" }) });
  });
  await page.route("**/api/tenant/agent-tasks?limit=50", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 42, parent_session_id: 8, agent_name: "coder", description: "Build background task", status: "running", model: "gpt-test", trace_id: "trace-agent-42" },
          { id: 41, parent_session_id: 7, agent_name: "reviewer", description: "Review implementation", status: "completed", model: "gpt-test", result_json: "{\"ok\":true}" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/agent-tasks", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 43 }) });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/tenant/agent-tasks/42/events?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: 100, task_id: 42, event_type: "started", payload_json: "{\"source\":\"api\"}", trace_id: "trace-agent-42", created_at: "2026-01-02T03:04:01Z" },
          { id: 101, task_id: 42, event_type: "message", payload_json: "{\"content\":\"continue\"}", trace_id: "trace-agent-42", created_at: "2026-01-02T03:04:02Z" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/agent-tasks/41/events?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ data: [{ id: 90, task_id: 41, event_type: "completed", payload_json: "{\"ok\":true}", created_at: "2026-01-02T03:04:03Z" }] })
    });
  });
  await page.route("**/api/tenant/agent-tasks/42/message", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 102, task_id: 42 }) });
  });
  await page.route("**/api/tenant/agent-tasks/42", async (route) => {
    if (route.request().method() === "PATCH") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 42, updated: true }) });
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 42, agent_name: "coder", status: "running" }) });
  });
  await page.route("**/api/tenant/agent-tasks/42/cancel", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 42, cancelled: true }) });
  });
  await page.route("**/api/tenant/goals?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: "goal_webui_1", objective: "Complete WebUI production readiness", status: "active", turns_used: 2, turn_budget: 5, input_tokens: 1200, output_tokens: 320, token_budget: 20000, model: "gpt-test", session_id: "8", last_next_action: "continue validation" },
          { id: "goal_webui_2", objective: "Review agent task hardening", status: "blocked", turns_used: 3, turn_budget: 5, input_tokens: 2000, output_tokens: 600, token_budget: 20000, model: "gpt-test", last_reason: "waiting on evidence" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/goals", async (route) => {
    if (route.request().method() === "POST") {
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ id: "goal_webui_3", objective: "Created from WebUI", status: "active", turn_budget: 5, token_budget: 20000 })
      });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/tenant/goals/goal_webui_1/events?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          { id: "evt_goal_1", goal_id: "goal_webui_1", type: "goal_started", message: "goal started", status: "active", created_at: "2026-01-02T03:04:01Z" },
          { id: "evt_goal_2", goal_id: "goal_webui_1", type: "turn_finished", message: "turn finished", status: "active", next_action: "continue validation", created_at: "2026-01-02T03:05:01Z" }
        ]
      })
    });
  });
  await page.route("**/api/tenant/goals/goal_webui_2/events?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ data: [{ id: "evt_goal_3", goal_id: "goal_webui_2", type: "status_changed", message: "blocked", status: "blocked", created_at: "2026-01-02T03:06:01Z" }] })
    });
  });
  await page.route("**/api/tenant/goals/goal_webui_1/stop", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: "goal_webui_1", status: "stopped" }) });
  });
  await page.route("**/api/tenant/goals/goal_webui_1/resume?force=true", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: "goal_webui_1", status: "active" }) });
  });
  await page.route("**/api/tenant/goals/goal_webui_1/run?evaluator=deterministic", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ goal: { id: "goal_webui_1", status: "active", turns_used: 3 }, event: { id: "evt_goal_run", type: "turn_finished" } }) });
  });
  await page.route("**/api/tenant/profile", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 1, profile_version: 1, summary: "tester", profile_json: "{\"role\":\"tester\"}" }) });
  });
  await page.route("**/api/tenant/memories", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 1 }) });
  });
  await page.route("**/api/tenant/documents", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 1 }) });
  });
  await page.route("**/api/tenant/skills", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 1 }) });
  });
  await page.route("**/api/tenant/skill-overrides", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 1 }) });
  });
  await page.route("**/api/trace/api/sessions/7?source=tenant&limit=100&trace_limit=100&task_limit=200", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        source: "tenant",
        session_id: "7",
        summary: { turns: 2, messages: 2, tool_calls: 1, total_tokens: 12, cache_summary: "read 4", skills: ["review"] },
        spans: [
          { id: "api-1", trace_id: "trace-session-7a", type: "api", name: "api.request", status: "ok", start: "2026-01-02T03:04:00.900Z", end: "2026-01-02T03:04:01.080Z", duration_ms: 180 },
          { id: "model-1", trace_id: "trace-session-7a", type: "model", name: "model.request", model: "gpt-test", status: "ok", start: "2026-01-02T03:04:01Z", end: "2026-01-02T03:04:01.041Z", duration_ms: 41 },
          { id: "tool-1", trace_id: "trace-session-7a", type: "tool", name: "Read", tool_name: "Read", status: "ok", start: "2026-01-02T03:04:01.050Z", end: "2026-01-02T03:04:01.062Z", duration_ms: 12 },
          { id: "model-2", trace_id: "trace-session-7b", type: "model", name: "model.request", model: "gpt-test", status: "ok", start: "2026-01-02T03:05:01Z", end: "2026-01-02T03:05:01.031Z", duration_ms: 31 }
        ],
        events: [
          { type: "compact_summary", trace_id: "trace-session-7a", time: "2026-01-02T03:04:00.900Z" },
          { type: "telemetry", name: "mobile.chat.stream.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.080Z", duration_ms: 80 },
          { type: "telemetry", name: "mobile.phase.session.load.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:00.920Z", duration_ms: 8 },
          { type: "telemetry", name: "mobile.phase.query.run.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.070Z", duration_ms: 70 },
          { type: "telemetry", name: "mobile.phase.assistant_message.persist.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.078Z", duration_ms: 5 },
          { type: "telemetry", name: "model.phase.http.request_send.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:00.985Z", duration_ms: 5 },
          { type: "telemetry", name: "model.phase.http.write_request.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:00.985Z", duration_ms: 1 },
          { type: "telemetry", name: "model.phase.http.wait_first_response_byte.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.010Z", duration_ms: 25 },
          { type: "telemetry", name: "model.phase.http.stream_ready.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.011Z", duration_ms: 1 },
          { type: "telemetry", name: "model.phase.http_round_trip.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.010Z", duration_ms: 30 },
          { type: "telemetry", name: "model.phase.stream.create.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.011Z", duration_ms: 31 },
          { type: "telemetry", name: "model.phase.stream.first_delta.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.020Z", duration_ms: 20 },
          { type: "telemetry", name: "model.phase.stream.read.finished", trace_id: "trace-session-7a", status: "ok", time: "2026-01-02T03:04:01.041Z", duration_ms: 40 },
          { type: "telemetry", name: "tool.execution.finished", trace_id: "trace-session-7a", skill: "review", time: "2026-01-02T03:04:01.062Z", duration_ms: 12 },
          { type: "telemetry", name: "mobile.chat.stream.finished", trace_id: "trace-session-7b", status: "ok", time: "2026-01-02T03:05:01.040Z", duration_ms: 40 }
        ]
      })
    });
  });
  await page.route("**/api/trace/api/sessions/8?source=tenant&limit=100&trace_limit=100&task_limit=200", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        source: "tenant",
        session_id: "8",
        summary: { turns: 2, messages: 2, tool_calls: 1, total_tokens: 18, cache_summary: "read 6", skills: ["webui-validation"] },
        spans: [
          { id: "model-1", trace_id: "trace-session-8", type: "model", name: "model.request", model: "gpt-test", status: "ok", start: "2026-01-02T03:04:01Z", end: "2026-01-02T03:04:01.041Z", duration_ms: 41 },
          { id: "tool-1", trace_id: "trace-session-8", type: "tool", name: "Read", tool_name: "Read", status: "ok", start: "2026-01-02T03:04:01.050Z", end: "2026-01-02T03:04:01.062Z", duration_ms: 12 }
        ],
        events: [
          { type: "compact_summary", trace_id: "trace-session-8", time: "2026-01-02T03:04:00.900Z" },
          { type: "telemetry", name: "mobile.chat.stream.finished", trace_id: "trace-session-8", status: "ok", time: "2026-01-02T03:04:01.080Z", duration_ms: 80 },
          { type: "telemetry", name: "tool.execution.finished", trace_id: "trace-session-8", skill: "webui-validation", time: "2026-01-02T03:04:01.062Z", duration_ms: 12 }
        ]
      })
    });
  });
});

for (const viewport of [{ width: 1440, height: 960 }, { width: 1024, height: 768 }, { width: 375, height: 480 }]) {
  test(`legacy prompt picker supports CRUD and draft insertion at ${viewport.width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize(viewport);
    await mockEmptyWebAgentApi(page);
    type Prompt = { id: number; title: string; content: string; category: string; pinned: boolean; sort_order: number };
    let prompts: Prompt[] = [];
    let sends = 0;
    let taskCreates = 0;
    const sessions: Record<string, unknown>[] = [];
    const tasks: Record<string, unknown>[] = [];
    page.on("request", (request) => {
      if (request.method() !== "POST") return;
      if (/\/messages?(\?|$)/.test(request.url())) sends++;
      if (/\/agent-tasks(\?|$)/.test(request.url()) && request.postDataJSON()?.prompt?.trim()) sends++;
    });
    await page.route("**/tenant/sessions?limit=100", (route) => route.fulfill({ json: { data: sessions } }));
    await page.route("**/tenant/agent-tasks?limit=100", (route) => route.fulfill({ json: { data: tasks } }));
    await page.route("**/tenant/sessions", async (route) => {
      expect(route.request().method()).toBe("POST");
      sessions.push({ ...route.request().postDataJSON(), id: 9501 });
      await route.fulfill({ json: { id: 9501 } });
    });
    await page.route("**/tenant/agent-tasks", async (route) => {
      expect(route.request().method()).toBe("POST");
      const body = route.request().postDataJSON();
      expect(body.parent_session_id).toBe(9501);
      expect(body.prompt).toBe("");
      taskCreates++;
      tasks.push({ ...body, id: 9101, metadata_json: JSON.stringify(body.metadata_json) });
      await route.fulfill({ json: { id: 9101 } });
    });
    await page.route("**/tenant/agent-tasks/9101", (route) => route.fulfill({ json: tasks[0] }));
    await page.route("**/tenant/agent-tasks/9101/events?**", (route) => route.fulfill({ json: { data: [] } }));
    await page.route("**/tenant/sessions/9501/messages?**", (route) => route.fulfill({ json: { data: [] } }));
    await page.route("**/tenant/prompt-templates**", async (route) => {
      const request = route.request();
      if (request.method() === "DELETE") prompts = [];
      else if (request.method() === "POST" || request.method() === "PATCH") {
        prompts = [{ ...request.postDataJSON(), id: 1 }];
        return route.fulfill({ json: prompts[0] });
      }
      return route.fulfill({ json: prompts });
    });
    await page.goto("/webui/agent?token=test-token");
    if (viewport.width === 1024) await page.getByRole("button", { name: "Toggle left rail" }).click();
    const composer = page.getByLabel("Message composer");
    const launcher = page.getByRole("button", { name: "Common prompts", exact: true });
    await expect(launcher).toHaveText("");
    expect((await launcher.boundingBox())?.width).toBe(32);
    await launcher.click();
    const dialog = page.getByRole("dialog", { name: "Common prompts", exact: true });
    await expect(dialog.getByRole("status")).toContainText("No common prompts");
    await dialog.getByRole("button", { name: "Add prompt", exact: true }).click();
    const editor = page.getByRole("dialog", { name: "New prompt", exact: true });
    await editor.getByLabel("Title", { exact: true }).fill("Feature implementation");
    await editor.getByLabel("Prompt content", { exact: true }).fill("Implement {{feature}}.\n1. Add tests.");
    await editor.getByLabel("Category", { exact: true }).fill("Development");
    await editor.getByLabel("Sort order", { exact: true }).fill("2");
    await editor.getByRole("checkbox", { name: "Pin", exact: true }).check();
    await expectLocatorWithinViewport(editor);
    await expectLocatorWithinViewport(editor.getByRole("button", { name: "Save", exact: true }));
    await page.screenshot({ path: testInfo.outputPath("legacy-picker-editor.png") });
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    const usePrompt = dialog.getByRole("button", { name: "Use Feature implementation" });
    await expect(usePrompt).toBeDisabled();
    await expect(usePrompt).toHaveAttribute("aria-description", "Create or select a session first...");
    await expect(composer).toHaveValue("");
    await page.keyboard.press("Escape");

    await page.getByTestId("agent-conversation-scroll").getByRole("button", { name: "New Session", exact: true }).click();
    const newSession = page.getByRole("dialog", { name: "Start in current workspace" });
    await newSession.getByPlaceholder("/absolute/path/to/project").fill("/Users/example/GolandProjects/niuma");
    await newSession.getByPlaceholder("Optional session title").fill("Prompt draft regression");
    await newSession.getByRole("button", { name: "Create Session", exact: true }).click();
    await expect(newSession).toBeHidden();
    await expect(page.getByRole("heading", { name: "Prompt draft regression", exact: true })).toBeVisible();
    expect(taskCreates).toBe(1);
    await launcher.click();
    await expect(usePrompt).toBeEnabled();
    await dialog.getByRole("button", { name: "Use Feature implementation" }).click();
    await expect(composer).toHaveValue(prompts[0].content);
    await composer.fill("  Existing draft\n  ");
    await launcher.click();
    await dialog.getByRole("searchbox").fill("Feature");
    await dialog.getByRole("button", { name: "Use Feature implementation" }).click();
    await expect(composer).toHaveValue(`  Existing draft\n  \n\n${prompts[0].content}`);
    await launcher.click();
    await dialog.getByRole("button", { name: "Edit", exact: true }).click();
    const editing = page.getByRole("dialog", { name: "Edit prompt", exact: true });
    await editing.getByLabel("Title", { exact: true }).fill("Updated feature");
    await editing.getByRole("button", { name: "Save", exact: true }).click();
    await expect(dialog.getByRole("button", { name: "Use Updated feature" })).toBeVisible();
    // Switch the existing page theme tokens without introducing picker-specific theme state.
    await page.locator(".web-agent-page").evaluate((element) => element.setAttribute("data-agent-theme", "dark"));
    expect(await dialog.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe("rgb(19, 25, 34)");
    expect(await dialog.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(false);
    await page.screenshot({ path: testInfo.outputPath("legacy-picker-dark.png") });
    await dialog.getByRole("button", { name: "Delete", exact: true }).click();
    const deleting = page.getByRole("dialog", { name: "Delete prompt", exact: true });
    await expect(deleting.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
    await deleting.getByRole("button", { name: "Delete prompt", exact: true }).click();
    await expect(dialog.getByRole("status")).toContainText("No common prompts");
    await page.keyboard.press("Escape");
    await expect(launcher).toBeFocused();
    expect(sends).toBe(0);
    await expect(composer).toHaveValue("  Existing draft\n  \n\nImplement {{feature}}.\n1. Add tests.");
    await composer.fill("Implement reviewed feature.\n1. Add tests.");
    await expect.poll(() => page.evaluate(() => Object.values(localStorage).some(
      (value) => value.includes("Implement reviewed feature.")
    ))).toBe(true);
    await page.reload();
    await expect(page.getByLabel("Message composer")).toHaveValue("Implement reviewed feature.\n1. Add tests.");
    expect(sends).toBe(0);
  });
}

test("renders chat and validation workspaces with mocked api", async ({ page }) => {
  await page.goto("/");
  const primary = page.getByRole("navigation", { name: "Primary navigation" });

  await expect(page.getByRole("heading", { name: "niuma WebUI" })).toBeVisible();
  await expect(page.getByText("Full-cycle test console for chat, memory, skills, trace, and telemetry.")).toBeVisible();
  await expect(page.locator(".sidebar").getByRole("button", { name: /Settings/ })).toBeVisible();
  await expect(page.locator(".sidebar").getByRole("button", { name: "EN", exact: true })).toHaveCount(0);
  await expect(page.locator(".welcome-page").getByRole("button", { name: "EN", exact: true })).toHaveCount(0);
  await expect(page.locator(".welcome-status-card").getByText("Status")).toBeVisible();
  await expect(page.locator(".welcome-status-card").getByText("Ready")).toBeVisible();
  await expect(page.getByLabel("Active run context").getByText("/api", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Active run context").getByText("webui-local", { exact: true })).toBeVisible();
  await page.locator(".sidebar").getByRole("button", { name: /Settings/ }).click();
  const settingsDialog = page.getByRole("dialog", { name: "Identity and language" });
  await expect(settingsDialog).toBeVisible();
  const tenantSelect = settingsDialog.getByLabel("Tenant");
  await expect(tenantSelect).toHaveValue("webui-local");
  await tenantSelect.selectOption("webui-alt");
  await expect(tenantSelect).toHaveValue("webui-alt");
  await expect(page.getByLabel("Active run context").getByText("webui-alt", { exact: true })).toBeVisible();
  const userSelect = settingsDialog.getByLabel("User");
  await expect(userSelect).toHaveValue("webui-local-user");
  await userSelect.selectOption("webui-alt-user");
  await expect(userSelect).toHaveValue("webui-alt-user");
  await expect(page.getByLabel("Active run context").getByText("webui-alt-user", { exact: true })).toBeVisible();
  await settingsDialog.getByRole("button", { name: "中", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "一级导航" }).getByRole("button", { name: /聊天实验室/ })).toBeVisible();
  await expect(page.getByText("用于验证聊天、记忆、技能、Trace 和 Telemetry 的全链路测试控制台。")).toBeVisible();
  await page.getByRole("dialog", { name: "身份和语言" }).getByRole("button", { name: "EN", exact: true }).click();
  await expect(primary.getByRole("button", { name: /Chat Lab/ })).toBeVisible();
  await page.getByRole("dialog", { name: "Identity and language" }).getByRole("button", { name: /Close settings/ }).click();
  await expect(page.getByRole("dialog", { name: "Identity and language" })).toHaveCount(0);
  await primary.getByRole("button", { name: /Chat Lab/ }).click();
  const secondary = page.getByRole("navigation", { name: "Secondary navigation" });
  await expect(secondary.getByRole("button", { name: /Conversation/ })).toBeVisible();
  await expect(page.getByLabel("Chat context").getByText("webui-alt", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Chat context").getByText("webui-alt-user", { exact: true })).toBeVisible();
  await page.getByLabel("Search sessions").fill("smoke");
  await expect(page.getByRole("button", { name: /Smoke Session/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Seeded Scenario/ })).toHaveCount(0);
  await page.getByLabel("Search sessions").fill("");
  await page.getByLabel("Filter status").selectOption("paused");
  await expect(page.getByText("No sessions match the current filter.")).toBeVisible();
  await page.getByLabel("Filter status").selectOption("all");
  await expect(page.locator(".app-header").getByText("Mobile Chat API Server")).toHaveCount(0);
  await expect(page.locator(".app-header").getByRole("heading", { name: "niuma WebUI" })).toHaveCount(0);
  await expect(page.locator("main").getByRole("button", { name: "EN", exact: true })).toHaveCount(0);
  await expect(page.locator("main").getByText("Ready", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Active run context")).toHaveCount(0);

  await page.getByRole("button", { name: /niuma/ }).click();
  await expect(page.getByRole("heading", { name: "niuma WebUI" })).toBeVisible();
  await primary.getByRole("button", { name: /Chat Lab/ }).click();

  const resizeHandle = page.getByRole("separator", { name: "Resize sidebar" });
  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "284");
  await resizeHandle.focus();
  await page.keyboard.press("ArrowRight");
  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "300");

  await secondary.getByRole("button", { name: /Validation/ }).click();
  await expect(page.getByRole("heading", { name: "Lifecycle Validation" })).toBeVisible();
  await expect(page.getByText("10/10 passed")).toBeVisible();
  await expect(page.getByText("Capability Evidence")).toBeVisible();
  await expect(page.getByText("Auto compression")).toBeVisible();

  await primary.getByRole("button", { name: /Run Context/ }).click();
  await expect(page.getByRole("heading", { name: "Tenant users" })).toBeVisible();
  await page.getByLabel("User key").fill("webui-created-user");
  await page.getByLabel("Display name").fill("WebUI Created User");
  await page.getByLabel("Email").fill("created@example.test");
  await page.getByLabel("Role").selectOption("member");
  await page.getByRole("button", { name: /Save user/ }).click();
  await page.getByRole("button", { name: /niuma/ }).click();
  await expect(page.getByText("Saved user webui-created-user")).toBeVisible();
  await primary.getByRole("button", { name: /Run Context/ }).click();
  await page.getByTitle("Seed validation scenario").click();
  await primary.getByRole("button", { name: /Chat Lab/ }).click();
  await expect(page.getByRole("heading", { name: "Seeded Scenario" })).toBeVisible();
  await expect(page.getByText("seeded answer")).toBeVisible();
  const userBox = await page.locator(".message.user").first().boundingBox();
  const assistantBox = await page.locator(".message.assistant").first().boundingBox();
  expect(userBox?.x ?? 0).toBeGreaterThan(assistantBox?.x ?? Number.POSITIVE_INFINITY);

  await primary.getByRole("button", { name: /Knowledge/ }).click();
  await secondary.getByRole("button", { name: "Memory Long-term facts" }).click();
  await expect(page.getByRole("heading", { name: "Memory" })).toBeVisible();
  await expect(page.getByText("pref")).toBeVisible();

  await primary.getByRole("button", { name: /Skills/ }).click();
  await expect(page.getByRole("heading", { name: "Skills", exact: true })).toBeVisible();
  await expect(page.getByText("review").first()).toBeVisible();

  await primary.getByRole("button", { name: /Goals/ }).click();
  await expect(page.getByRole("heading", { name: "Goals" })).toBeVisible();
  await expect(page.getByText("Complete WebUI production readiness").first()).toBeVisible();
  await expect(page.getByLabel("Goal summary").getByText("Active")).toBeVisible();
  await expect(page.getByLabel("Events").getByText("turn_finished")).toBeVisible();
  await page.getByPlaceholder("Search goals").fill("agent");
  await expect(page.getByText("Review agent task hardening")).toBeVisible();
  await page.getByPlaceholder("Search goals").fill("");

  await primary.getByRole("button", { name: /Observability/ }).click();
  await secondary.getByRole("button", { name: /Agents/ }).click();
  await expect(page.getByRole("heading", { name: "Agents" })).toBeVisible();
  await expect(page.getByText("Build background task")).toBeVisible();
  await expect(page.getByLabel("Agent summary").getByText("Running")).toBeVisible();
  await expect(page.locator(".agent-event-row").getByText("started")).toBeVisible();
  await page.locator(".agent-message-box textarea").fill("continue from webui");
  await expect(page.locator(".agent-message-box textarea")).toHaveValue("continue from webui");
  await expect(page.getByRole("button", { name: /Send message/ })).toBeEnabled();
  const messageRequest = page.waitForRequest((request) => request.url().includes("/api/tenant/agent-tasks/42/message") && request.method() === "POST");
  await page.getByRole("button", { name: /Send message/ }).click();
  expect((await messageRequest).postDataJSON()).toMatchObject({ from_agent: "webui", content: "continue from webui" });
  await expect(page.locator(".agent-message-box textarea")).toHaveValue("");
  await secondary.getByRole("button", { name: /Loops/ }).click();
  await expect(page.getByRole("heading", { name: "Loops" })).toBeVisible();
  await expect(page.getByText("drink water").first()).toBeVisible();
  await expect(page.getByText("remember to drink water")).toBeVisible();
  await expect(page.getByText("Run history")).toBeVisible();
  await expect(page.getByText("completed").first()).toBeVisible();
  await page.getByLabel("Prompt").fill("drink more water");
  await page.getByLabel("Interval").fill("15m");
  const saveLoopRequest = page.waitForRequest((request) => request.url().includes("/api/runtime/background/sched_loop_1") && request.method() === "PATCH");
  await page.getByRole("button", { name: /Save loop/ }).click();
  expect((await saveLoopRequest).postDataJSON()).toMatchObject({ prompt: "drink more water", interval: "15m" });
  const runLoopRequest = page.waitForRequest((request) => request.url().includes("/api/runtime/background/sched_loop_1/run") && request.method() === "POST");
  await page.getByRole("button", { name: /Run now/ }).click();
  await runLoopRequest;
  await page.getByRole("button", { name: /New loop/ }).click();
  await page.getByLabel("Prompt").fill("stand up");
  await page.getByLabel("Interval").fill("15m");
  const createLoopRequest = page.waitForRequest((request) => request.url().endsWith("/api/runtime/background") && request.method() === "POST");
  await page.getByRole("button", { name: /Create loop/ }).click();
  expect((await createLoopRequest).postDataJSON()).toMatchObject({ prompt: "stand up", interval: "15m" });
  const stopLoopRequest = page.waitForRequest((request) => request.url().includes("/api/runtime/background/sched_loop_1/stop") && request.method() === "POST");
  await page.getByText("drink water").first().click();
  await page.getByRole("button", { name: /Stop loop/ }).click();
  await stopLoopRequest;
  await secondary.getByRole("button", { name: "Trace Timeline and spans" }).click();
  await expect(page.getByRole("heading", { name: "Trace" })).toBeVisible();
  await expect(page.getByLabel("Trace session")).toHaveValue("8");
  await page.getByLabel("Trace session").selectOption("7");
  await expect(page.getByLabel("Trace session")).toHaveValue("7");
  await expect(page.getByText("Runtime timeline")).toBeVisible();
  await expect(page.getByText("Trace IDs")).toBeVisible();
  await expect(page.getByText("Telemetry events")).toBeVisible();
  await expect(page.getByText("Model requests")).toBeVisible();
  await expect(page.getByText("Tool calls")).toBeVisible();
  await expect(page.getByText("Token usage", { exact: true })).toBeVisible();
  await expect(page.getByText("mobile.chat.stream.finished").first()).toBeVisible();
  await page.getByRole("tab", { name: "Sequence timeline" }).click();
  await expect(page.getByLabel("Trace / request")).toHaveValue("trace-session-7a");
  await expect(page.getByText("Total duration")).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("gpt-test").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("Read").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("41ms").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("session load").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("query run").first()).toBeVisible();
  await expect(page.getByLabel("Duration attribution")).toBeVisible();
  await expect(page.getByLabel("Duration attribution").getByText("Bottleneck")).toBeVisible();
  await expect(page.getByLabel("Duration attribution").getByText("Stream create", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Slow path")).toBeVisible();
  await expect(page.getByLabel("Slow path").getByText("Remote first byte")).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("http wait first response byte").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByTitle("Time waiting after request write until the provider returns the first response byte. Long time here usually means remote API queueing, routing, prefill, or first-token preparation.").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("stream first delta").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("stream read").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByTitle("Time spent creating the provider stream and waiting before stream reads can begin.").first()).toBeVisible();
  await expect(page.locator(".trace-collapse-button").first()).toBeVisible();
  await page.locator(".trace-collapse-button").first().click();
  await expect(page.locator(".trace-waterfall").getByText("Inside").first()).toBeVisible();
  await expect(page.locator(".trace-waterfall").getByText("Unexplained").first()).toBeVisible();
  await page.getByLabel("Trace / request").selectOption("trace-session-7b");
  await expect(page.getByLabel("Trace / request")).toHaveValue("trace-session-7b");
  await secondary.getByRole("button", { name: /Telemetry/ }).click();
  await expect(page.getByLabel("Trace session")).toHaveValue("7");
  await expect(page.getByRole("heading", { name: "Telemetry" })).toBeVisible();
  await expect(page.getByText("model.request.finished").first()).toBeVisible();
});

test("moves Profile secondary navigation into the sidebar", async ({ page }) => {
  await page.route("**/api/tenant/agent-profiles?limit=100", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await page.route("**/api/tenant/channel-accounts?limit=100", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await page.route("**/api/tenant/agent-provisionings/overview", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ records: [], workers: [] }) }));
  await page.route("**/api/v1/providers", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await page.goto("/");
  const primary = page.getByRole("navigation", { name: "Primary navigation" });

  await primary.getByRole("button", { name: /Agent Profiles/ }).click();

  const profileNavigation = page.getByRole("group", { name: /Agent Profiles.*Secondary navigation/ });
  await expect(profileNavigation.getByRole("button", { name: /Agent Profiles/ })).toBeVisible();
  await expect(profileNavigation.getByRole("button", { name: /Provisioning/ })).toBeVisible();
  await expect(page.locator(".workspace-toolbar")).toHaveCount(0);
  await expect(page.locator(".app-shell")).toHaveClass(/profile-app-shell/);
  const profileSelector = page.getByLabel("Select profile");
  await expect(profileSelector).toBeVisible();
  await expect(page.locator(".agent-catalog-list")).toHaveCount(0);
  await profileSelector.selectOption("coder");
  await expect(page.getByRole("heading", { name: "Coder", exact: true })).toBeVisible();

  await profileNavigation.getByRole("button", { name: /Provisioning/ }).click();
  await expect(page.getByRole("heading", { name: "Provision an agent with confidence." })).toBeVisible();
});

test("maximizes the Team workspace with sidebar navigation and a header selector", async ({ page }) => {
  const teams = [
    { id: 1, team_key: "alpha", team_version: 1, display_name: "Alpha Team", description: "Alpha collaboration", scope: "tenant_shared", status: "published", schema_version: 1, policy_json: JSON.stringify({ orchestration: { mode: "coordinator" } }) },
    { id: 2, team_key: "beta", team_version: 1, display_name: "Beta Team", description: "Beta collaboration", scope: "tenant_shared", status: "published", schema_version: 1, policy_json: JSON.stringify({ orchestration: { mode: "coordinator" } }) }
  ];
  await page.route("**/api/tenant/agent-teams?limit=100", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: teams }) }));
  await page.route(/\/api\/tenant\/agent-teams\/[^/]+\/(members|bindings|runs)\?version=1$/, (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await page.route("**/api/tenant/agent-profiles?limit=100", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await page.route("**/api/tenant/channel-accounts?limit=100", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [] }) }));
  await page.goto("/");

  await page.getByRole("navigation", { name: "Primary navigation" }).getByRole("button", { name: /Agent Teams/ }).click();

  const teamNavigation = page.getByRole("group", { name: /Agent Teams.*Secondary navigation/ });
  await expect(teamNavigation.getByRole("button", { name: /Team workspace/ })).toBeVisible();
  await expect(teamNavigation.getByRole("button", { name: /Runs & replay/ })).toBeVisible();
  await expect(page.locator(".workspace-toolbar")).toHaveCount(0);
  await expect(page.locator(".app-shell")).toHaveClass(/team-app-shell/);
  const teamSelector = page.getByLabel("Select team");
  await expect(teamSelector).toBeVisible();
  await expect(page.locator(".agent-catalog-list")).toHaveCount(0);
  await teamSelector.selectOption("beta");
  await expect(page.getByRole("heading", { name: "Beta Team", exact: true })).toBeVisible();

  await teamNavigation.getByRole("button", { name: /Runs & replay/ }).click();
  await expect(page.getByRole("heading", { name: "Run monitor" })).toBeVisible();
});

test("renders settings as a global dialog instead of a sidebar panel", async ({ page }) => {
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.goto("/");
  await page.evaluate(() => window.localStorage.setItem("niuma-webui.language.v1", "zh"));
  await page.reload();

  await page.locator(".sidebar").getByRole("button", { name: /设置/ }).click();
  const dialog = page.getByRole("dialog", { name: "身份和语言" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("租户")).toHaveValue("webui-local");
  await expect(dialog.getByLabel("用户")).toHaveValue("webui-local-user");
  await expect(dialog).toHaveJSProperty("parentElement.className", "dashboard-shell");

  await expectLocatorWithinViewport(dialog);
  await expectLocatorWithinViewport(dialog.getByRole("button", { name: /关闭设置/ }));
  await expectLocatorWithinViewport(dialog.getByLabel("租户"));
  await expectLocatorWithinViewport(dialog.getByLabel("用户"));
  await expect(page.locator(".settings-backdrop")).toBeVisible();

  const box = await dialog.boundingBox();
  const viewport = page.viewportSize();
  expect(box).not.toBeNull();
  expect(viewport).not.toBeNull();
  if (box && viewport) {
    expect(Math.abs(box.x + box.width / 2 - viewport.width / 2)).toBeLessThanOrEqual(2);
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await expectLocatorWithinViewport(dialog);
});

test("shows identity load errors with local reset action", async ({ page }) => {
  await page.unroute("**/api/tenant/tenants?limit=100");
  await page.unroute("**/api/tenant/users?limit=100");
  await page.route("**/api/tenant/tenants?limit=100", async (route) => {
    await route.fulfill({
      status: 403,
      contentType: "application/json",
      body: JSON.stringify({ error: "tenant access forbidden" })
    });
  });
  await page.route("**/api/tenant/users?limit=100", async (route) => {
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "get tenant webui-alt: mysql storage: not found" })
    });
  });
  await page.goto("/");

  await page.locator(".sidebar").getByRole("button", { name: /Settings/ }).click();
  await expect(page.getByText("Tenant list unavailable")).toBeVisible();
  await expect(page.getByText("tenant access forbidden")).toBeVisible();
  await expect(page.getByText("User list unavailable")).toBeVisible();
  await expect(page.getByText("get tenant webui-alt: mysql storage: not found")).toBeVisible();

  await page.getByRole("button", { name: "Reset to local identity" }).click();
  const dialog = page.getByRole("dialog", { name: "Identity and language" });
  await expect(dialog.getByLabel("Tenant")).toHaveValue("webui-local");
  await expect(dialog.getByLabel("User")).toHaveValue("webui-local-user");
});

test("shows mobile auth mode errors instead of an empty session list", async ({ page }) => {
  await page.unroute("**/api/mobile/chat/sessions?limit=50");
  await page.route("**/api/mobile/chat/sessions?limit=50", async (route) => {
    await route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({ error: "invalid mobile token" })
    });
  });
  await page.goto("/");

  await page.getByRole("navigation", { name: "Primary navigation" }).getByRole("button", { name: /Chat Lab/ }).click();
  await expect(page.getByRole("alert").getByText("Session list unavailable")).toBeVisible();
  await expect(page.getByRole("alert").getByText("invalid mobile token")).toBeVisible();
  await expect(page.getByRole("alert").getByText("NIUMA_MOBILE_DEV_AUTH=true")).toBeVisible();
});

test("opens web agent from the main webui navigation in Chinese", async ({ page }) => {
  await mockEmptyWebAgentApi(page);
  await page.goto("/webui/?token=test-token");

  await page.locator(".sidebar").getByRole("button", { name: /Settings/ }).click();
  await page.getByRole("dialog", { name: "Identity and language" }).getByRole("button", { name: "中", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "一级导航" }).getByRole("button", { name: /Web Agent/ })).toBeVisible();
  await page.getByRole("dialog", { name: "身份和语言" }).getByRole("button", { name: /关闭设置/ }).click();
  await expect(page.getByRole("dialog", { name: "身份和语言" })).toHaveCount(0);

  await page.getByRole("navigation", { name: "一级导航" }).getByRole("button", { name: /Web Agent/ }).click();

  await expect(page).toHaveURL(/\/webui\/agent\?token=test-token$/);
  await expect(page.getByRole("button", { name: "新会话" }).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "主 WebUI" })).toBeVisible();
  await expect(page.getByText("当前租户还没有 Web Agent 会话。")).toBeVisible();
});

test("keeps Niuma browser storage isolated in a real browser", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("go-claude-webui.language.v1", "zh");
    window.localStorage.setItem("go-claude-webui.sidebar-width.v1", "320");
  });

  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Niuma WebUI" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Primary navigation" })).toBeVisible();
  const imported = await page.evaluate(() => ({
    language: window.localStorage.getItem("niuma-webui.language.v1"),
    sidebarWidth: window.localStorage.getItem("niuma-webui.sidebar-width.v1")
  }));
  expect(imported).toEqual({ language: null, sidebarWidth: null });
});

test("renders codex-style web agent route with real API state only", async ({ page }) => {
  let cancelled = false;
  let sentMessage = "";
  let continuationCreated = false;
  await page.route("**/tenant/tenants?limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ tenant_key: "webui-local", name: "WebUI Local" }] }) });
  });
  await page.route("**/tenant/users?limit=100", async (route) => {
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: [{ user_key: "webui-local-user", display_name: "WebUI Local User" }] }) });
  });
  const fulfillWebAgentSessions = async (route: Route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: 501,
            session_key: "web-agent-session-layout",
            title: "Implement Web Agent layout",
            status: "active",
            model: "gpt-test",
            cwd: "/Users/example/GolandProjects/niuma",
            metadata_json: JSON.stringify({
              source: "webui-agent",
              workspace_name: "niuma"
            }),
            started_at: "2026-06-30T03:00:00Z",
            last_message_at: "2026-06-30T03:00:04Z"
          }
        ]
      })
    });
  };
  await page.route("**/tenant/sessions?limit=100", fulfillWebAgentSessions);
  await page.route("**/api/tenant/sessions?limit=100", fulfillWebAgentSessions);
  await page.route("**/tenant/agent-tasks?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: 101,
            agent_name: "web-agent",
            description: "Implement Web Agent layout",
            status: cancelled ? "cancelled" : "running",
            model: "gpt-test",
            trace_id: "trace-web-agent",
            parent_session_id: 501,
            started_at: "2026-06-30T03:00:00Z",
            finished_at: cancelled ? "2026-06-30T03:00:02Z" : undefined,
            metadata_json: JSON.stringify({
              source: "webui-agent",
              cwd: "/Users/example/GolandProjects/niuma",
              workspace_name: "niuma",
              context_percent: 41
            })
          },
          ...(continuationCreated ? [{
            id: 102,
            agent_name: "web-agent",
            description: "Implement Web Agent layout",
            status: "running",
            model: "gpt-test",
            trace_id: "trace-web-agent-continuation",
            parent_session_id: 501,
            started_at: "2026-06-30T03:00:04Z",
            metadata_json: JSON.stringify({
              source: "webui-agent",
              cwd: "/Users/example/GolandProjects/niuma",
              workspace_name: "niuma",
              context_percent: 41,
              continuation_of_task_id: 101
            })
          }] : [])
        ]
      })
    });
  });
  await page.route(/.*\/(?:api\/)?tenant\/agent-tasks\/101\/events\?.*/, async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: [
          {
            id: 1,
            task_id: 101,
            event_type: "started",
            payload_json: JSON.stringify({ source: "api", status: "running", agent_name: "web-agent" }),
            trace_id: "trace-web-agent",
            created_at: "2026-06-30T03:00:00Z"
          },
          ...Array.from({ length: 36 }, (_, index) => ({
          id: index + 2,
          task_id: 101,
          event_type: index % 6 === 0 ? "tool_call" : "text_delta",
          payload_json: JSON.stringify(index % 6 === 0 ? { tool_name: "Read", file: `/repo/file-${index}.go`, insertions: 2, deletions: 1 } : { text: `Progress event ${index}` }),
          trace_id: "trace-web-agent",
          created_at: "2026-06-30T03:00:00Z"
          })),
          {
            id: 38,
            task_id: 101,
            event_type: "message",
            payload_json: JSON.stringify({ content: "Streaming follow-up event" }),
            trace_id: "trace-web-agent",
            created_at: "2026-06-30T03:00:01Z"
          },
          ...(cancelled ? [{
            id: 50,
            task_id: 101,
            event_type: "cancelled",
            payload_json: JSON.stringify({ source: "api", cancelled: true }),
            trace_id: "trace-web-agent",
            created_at: "2026-06-30T03:00:02Z"
          }] : []),
        ]
      })
    });
  });
  await page.route(/.*\/(?:api\/)?tenant\/agent-tasks\/101\/events\/stream\?.*/, async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: [
        'event: connected\ndata: {"task_id":101}',
        'event: agent_task_event\ndata: {"id":37,"task_id":101,"event_type":"message","payload_json":"{\\"content\\":\\"Streaming follow-up event\\"}","trace_id":"trace-web-agent","created_at":"2026-06-30T03:00:01Z"}'
      ].join("\n\n") + "\n\n"
    });
  });
  await page.route("**/tenant/agent-tasks/101/cancel", async (route) => {
    cancelled = true;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 101, cancelled: true, in_process: false }) });
  });
  await page.route("**/tenant/agent-tasks/101/message", async (route) => {
    const body = await route.request().postDataJSON();
    sentMessage = body.content;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 99, task_id: 101 }) });
  });
  await page.route("**/tenant/agent-tasks/102", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        id: 102,
        agent_name: "web-agent",
        description: "Implement Web Agent layout",
        status: "completed",
        model: "gpt-test",
        parent_session_id: 501,
        started_at: "2026-06-30T03:00:04Z",
        finished_at: "2026-06-30T03:00:06Z",
        metadata_json: JSON.stringify({
          source: "webui-agent",
          cwd: "/Users/example/GolandProjects/niuma",
          workspace_name: "niuma",
          context_percent: 41,
          continuation_of_task_id: 101
        })
      })
    });
  });
  await page.route(/.*\/(?:api\/)?tenant\/agent-tasks\/102\/events\?.*/, async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        data: sentMessage ? [
          {
            id: 61,
            task_id: 102,
            event_type: "message",
            payload_json: JSON.stringify({ from_agent: "webui", content: sentMessage }),
            trace_id: "trace-web-agent-continuation",
            created_at: "2026-06-30T03:00:05Z"
          },
          {
            id: 62,
            task_id: 102,
            event_type: "text_delta",
            payload_json: JSON.stringify({ content: "continued reply" }),
            trace_id: "trace-web-agent-continuation",
            created_at: "2026-06-30T03:00:06Z"
          }
        ] : []
      })
    });
  });
  await page.route(/.*\/(?:api\/)?tenant\/agent-tasks\/102\/events\/stream\?.*/, async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: 'event: connected\ndata: {"task_id":102}\n\n'
    });
  });
  await page.route("**/tenant/agent-tasks/102/message", async (route) => {
    const body = await route.request().postDataJSON();
    sentMessage = body.content;
    await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 100, task_id: 102 }) });
  });
  await page.route("**/tenant/agent-tasks", async (route) => {
    if (route.request().method() === "POST") {
      const body = await route.request().postDataJSON();
      expect(body.parent_session_id).toBe(501);
      continuationCreated = true;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ id: 102 }) });
      return;
    }
    await route.fallback();
  });
  await page.route("**/agent/workspaces/validate", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        cwd: "/Users/example/GolandProjects/niuma",
        workspace_name: "niuma",
        exists: true,
        is_dir: true,
        git_root: "/Users/example/GolandProjects/niuma",
        is_git_repo: true
      })
    });
  });

  await page.goto("/webui/agent?token=test-token");

  await expect(page.getByRole("heading", { name: "Implement Web Agent layout" })).toBeVisible();
  await expect(page.getByText("Progress event 35")).toBeVisible();
  await expect(page.getByText("Streaming follow-up event")).toBeVisible();
  await expect(page.locator(".agent-message.typewriter").filter({ hasText: "Streaming follow-up event" })).toBeVisible();
  await expect(page.getByText("Context 41%")).toBeVisible();
  await expect(page.getByRole("region", { name: "Activity timeline" })).toBeVisible();
  await expect(page.locator(".composer-state")).toHaveText("running");
  await expect(page.getByText("Workspace Detail")).toBeVisible();
  await expect(page.getByText("web-agent-seed")).toHaveCount(0);
  await expect(page.locator(".agent-conversation-scroll .agent-message", { hasText: '"source": "api"' })).toHaveCount(0);
  await expectLocatorWithinViewport(page.locator(".agent-send-button.danger"));

  await page.getByTitle("Hide navigation").click();
  await expect(page.locator(".web-agent-page.left-collapsed.right-open")).toBeVisible();
  const collapsedLayout = await page.evaluate(() => {
    const left = document.querySelector(".web-agent-left")?.getBoundingClientRect();
    const center = document.querySelector(".web-agent-center")?.getBoundingClientRect();
    const right = document.querySelector(".web-agent-right")?.getBoundingClientRect();
    return {
      viewportWidth: window.innerWidth,
      bodyScrollWidth: document.documentElement.scrollWidth,
      left: left ? { x: left.x, width: left.width } : null,
      center: center ? { x: center.x, width: center.width, right: center.right } : null,
      right: right ? { x: right.x, width: right.width } : null
    };
  });
  expect(collapsedLayout.left?.width).toBeGreaterThanOrEqual(52);
  expect(collapsedLayout.left?.width).toBeLessThanOrEqual(64);
  expect(collapsedLayout.center?.x).toBeGreaterThanOrEqual(52);
  expect(collapsedLayout.center?.width).toBeGreaterThan(760);
  expect(collapsedLayout.right?.width).toBeGreaterThan(330);
  expect(collapsedLayout.center?.right).toBeLessThanOrEqual((collapsedLayout.right?.x || collapsedLayout.viewportWidth) + 1);
  expect(collapsedLayout.bodyScrollWidth).toBeLessThanOrEqual(collapsedLayout.viewportWidth);
  await expect(page.getByText("Context 41%")).toBeVisible();
  await page.getByTitle("Show navigation").click();
  await expect(page.locator(".web-agent-page.left-open.right-open")).toBeVisible();

  const scroll = page.getByTestId("agent-conversation-scroll");
  await scroll.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
  });
  await expect.poll(() => scroll.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);

  await page.locator(".agent-send-button.danger").click();
  await expect(page.locator(".composer-state")).toHaveText("ready");
  await expect(page.getByRole("button", { name: "Send" })).toBeVisible();
  await page.getByLabel("Message composer").fill("hello from enter");
  await page.getByLabel("Message composer").press("Shift+Enter");
  await expect.poll(() => sentMessage).toBe("");
  await page.getByLabel("Message composer").press("Enter");
  await expect.poll(() => sentMessage).toBe("hello from enter");
  await expect(page.locator(".agent-message.user").filter({ hasText: "hello from enter" })).toBeVisible();

  await page.getByTitle("Toggle left rail").click();
  await page.getByTitle("Toggle right panel").click();
  await expect(page.locator(".web-agent-page.center-only")).toBeVisible();
  await expect(page.locator(".web-agent-left")).toBeHidden();
  await expect(page.locator(".web-agent-right")).toBeHidden();
  await expect.poll(() => page.locator(".web-agent-center").evaluate((element) => Math.round(element.getBoundingClientRect().left))).toBe(0);

  await page.getByTitle("Toggle left rail").click();
  await page.getByRole("button", { name: /New Session/ }).first().click();
  await expect(page.getByRole("dialog", { name: "Start in current workspace" })).toBeVisible();
});
