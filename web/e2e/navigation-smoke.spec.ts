// Navigation smoke test across every primary section, at desktop and mobile
// viewports, against mocked API responses.
//
// This file was named visual-regression.spec.ts and it never was one: it only
// calls page.screenshot(), which writes a PNG into the run's output directory and
// asserts nothing about it. There is no baseline to compare against, so it could
// never fail on a visual change — the name promised a safety net that did not
// exist (AUDIT-P2-05).
//
// What it does assert is worth keeping: every section is reachable, renders its
// heading, and mounts the dashboard shell without throwing. The screenshots stay
// as debugging artifacts for failed runs.
//
// Turning this into a real visual-regression suite means toHaveScreenshot() plus
// committed baselines, and baselines are render-environment specific: PNGs
// generated on a developer's macOS box do not match ubuntu-latest font
// rasterization. That needs a pinned container to generate and verify baselines,
// tracked as TODO-063 rather than faked here.
import { expect, test } from "@playwright/test";

function isApplicationAPIURL(rawURL: string): boolean {
  return new URL(rawURL).pathname.startsWith("/api/");
}

test("scopes JSON mocks to application API URLs", () => {
  expect(isApplicationAPIURL("http://127.0.0.1:5174/api/tenant/tenants?limit=100")).toBe(true);
  expect(isApplicationAPIURL("http://127.0.0.1:5174/src/v2/api/mockSessionControlClient.ts")).toBe(false);
});

test.beforeEach(async ({ page }) => {
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    if (!isApplicationAPIURL(url.href)) {
      await route.fallback();
      return;
    }
    const path = url.pathname.replace(/^\/api/, "");
    const method = route.request().method();
    const json = (body: unknown) => route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });

    if (path === "/tenant/tenants") {
      await json({ data: [{ tenant_key: "webui-local", name: "WebUI Local", status: "active" }] });
      return;
    }
    if (path === "/tenant/users") {
      await json({ data: [{ user_key: "webui-local-user", display_name: "WebUI Local User", role: "owner", status: "active" }] });
      return;
    }
    if (path === "/mobile/chat/sessions") {
      await json({ data: [{ id: 8, session_key: "visual-seeded", title: "Visual Session", status: "active", model: "gpt-test" }] });
      return;
    }
    if (path === "/mobile/chat/sessions/8/messages") {
      await json({ data: [{ id: 80, role: "user", content: "visual prompt" }, { id: 81, role: "assistant", content: "visual answer", content_json: "{\"mobile\":{\"status\":\"completed\"}}" }] });
      return;
    }
    if (path === "/tenant/memories" || path === "/tenant/team-memory" || path === "/tenant/managed-memory") {
      await json({ data: [{ id: 1, memory_key: "visual.pref", content: "Use compact dashboards" }] });
      return;
    }
    if (path === "/tenant/profile") {
      await json({ id: 1, profile_version: 1, summary: "visual tester", profile_json: "{\"role\":\"tester\"}" });
      return;
    }
    if (path === "/tenant/documents") {
      await json({ data: [{ id: 1, doc_type: "CLAUDE.md", content_md: "# Visual Context" }] });
      return;
    }
    if (path === "/tenant/knowledge/documents") {
      await json({ data: [{ id: 1, title: "Visual KB", content: "Knowledge content", status: "active" }] });
      return;
    }
    if (path === "/tenant/memory-review/candidates") {
      await json({ data: [] });
      return;
    }
    if (path === "/tenant/effective-skills" || path === "/tenant/skills") {
      await json({ data: [{ id: 1, skill_key: "visual-review", name: "Visual Review", version: 1, enabled: true, source: "tenant" }] });
      return;
    }
    if (path === "/tenant/skill-overrides") {
      await json({ data: [] });
      return;
    }
    if (path === "/tenant/goals") {
      await json({ data: [{ id: "goal_visual_1", objective: "Verify WebUI visual completeness", status: "active", turns_used: 1, turn_budget: 4, input_tokens: 100, output_tokens: 50, token_budget: 1000, model: "gpt-test", session_id: "8" }] });
      return;
    }
    if (path === "/tenant/goals/goal_visual_1/events") {
      await json({ data: [{ id: "evt_visual_1", goal_id: "goal_visual_1", type: "goal_started", message: "goal started", status: "active", created_at: "2026-01-02T03:04:01Z" }] });
      return;
    }
    if (path === "/tenant/agent-tasks") {
      await json({ data: [{ id: 42, parent_session_id: 8, agent_name: "visual-agent", description: "Inspect UI", status: "running", model: "gpt-test", trace_id: "trace-visual", metadata_json: "{\"source\":\"visual\"}" }] });
      return;
    }
    if (path === "/tenant/agent-tasks/42/events") {
      await json({ data: [{ id: 100, task_id: 42, event_type: "started", payload_json: "{\"source\":\"visual\"}", trace_id: "trace-visual", created_at: "2026-01-02T03:04:01Z" }] });
      return;
    }
    if (path === "/tenant/telemetry") {
      await json({ data: [{ id: 1, name: "mobile.chat.stream.finished", status: "ok", session_id: 8, trace_id: "trace-visual" }] });
      return;
    }
    if (path === "/trace/api/sessions") {
      await json({ source: "local", data: [] });
      return;
    }
    if (path === "/trace/api/sessions/8") {
      await json({
        source: "tenant",
        session_id: "8",
        summary: { turns: 1, messages: 2, tool_calls: 1, total_tokens: 50, cache_summary: "read 5", skills: ["visual-review"] },
        spans: [{ id: "span-1", trace_id: "trace-visual", type: "model", name: "model.request", model: "gpt-test", status: "ok", duration_ms: 32 }],
        events: [{ type: "telemetry", name: "mobile.chat.stream.finished", trace_id: "trace-visual", status: "ok", duration_ms: 50 }]
      });
      return;
    }
    if (path === "/health") {
      await json({ ok: true, workspace: "/workspace" });
      return;
    }
    if (method === "POST" || method === "PATCH" || method === "DELETE") {
      await json({ id: 1 });
      return;
    }
    await json({ data: [] });
  });
});

test("every primary section is reachable and renders its shell", async ({ page }, testInfo) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "niuma WebUI" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("welcome.png"), fullPage: false });

  const primary = page.getByRole("navigation", { name: "Primary navigation" });
  const targets = [
    { button: /Chat Lab/, heading: "Visual Session", file: "chat.png" },
    { button: /Knowledge/, heading: "Memory", file: "knowledge.png" },
    { button: /Skills/, heading: "Skills", file: "skills.png" },
    { button: /Goals/, heading: "Goals", file: "goals.png" },
    { button: /Observability/, heading: "Agents", file: "observability.png" },
    { button: /Run Context/, heading: "Run Context", file: "context.png" }
  ];

  for (const target of targets) {
    await primary.getByRole("button", { name: target.button }).click();
    await expect(page.getByRole("heading", { name: target.heading }).first()).toBeVisible();
    await expect(page.locator(".dashboard-shell")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(target.file), fullPage: false });
  }
});
