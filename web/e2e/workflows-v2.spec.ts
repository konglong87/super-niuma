import { expect, test, type Page, type Locator } from "@playwright/test";
import { resolve } from "node:path";
import { installWebUIV2Sessions } from "./fixtures/webuiV2Sessions";

// Kept independent of the app TS project: these are wire-contract fixtures, not app mocks.
type Node = { id: string; agent_ref: string; instruction: string; depends_on: string[]; timeout_seconds: number; retry: { max_attempts: number; backoff_ms: number } };
type TemplateInput = { key?: string; name: string; description: string; definition: { schema_version: number; nodes: Node[]; limits: { max_parallel: number; run_timeout_seconds: number; max_total_tokens: number } }; agents: { ref: string; profile_id?: number; profile_key: string; profile_version: number }[] };
type Template = TemplateInput & { id: number; key: string; version: number };
type Usage = { input_tokens: number; output_tokens: number; cache_read_tokens: number; cache_write_tokens: number };
type Run = { id: string; scope: { tenant_id: number; user_id: number }; team_id: number; team_key: string; team_version: number; name: string; input: string; plan: { definition: Template["definition"]; agents: unknown[]; hash: string }; status: string; revision: number; epoch: number; last_sequence: number; created_at: string; deadline: string; nodes: { node_id: string; status: string; output?: string; attempts: { number: number; epoch: number; task_id: number; status: string; usage: Usage; started_at: string; provider?: string; model?: string; failure?: { code: string; message: string; retryable: boolean } }[] }[]; artifacts: { id: string; node_id: string; attempt_no: number; kind: string; content: string; sha256: string; created_at: string }[]; usage: Usage };
type WorkflowEvent = { sequence: number; type: string; status: string; node_id?: string; attempt_no?: number; created_at: string };
const blankTemplate = (): TemplateInput => ({ name: "", description: "", definition: { schema_version: 2, nodes: [{ id: "step-1", agent_ref: "", instruction: "", depends_on: [], timeout_seconds: 120, retry: { max_attempts: 2, backoff_ms: 1000 } }], limits: { max_parallel: 2, run_timeout_seconds: 600, max_total_tokens: 32000 } }, agents: [] });
const profileDocument = { schema_version: 2, identity: { role_type: "researcher" }, prompt: { mode: "chat" }, capabilities: { tools: { allow: [], deny: [] }, skills: [], mcp_servers: [], allow_agents: false, allow_attachments: false }, execution: { kind: "text" }, context: { workspace: false, git: false }, safety: { permission_mode: "ask", sandbox: "required", allow_unsandboxed_commands: false } };

// Network fixtures live only in tests. No production mock switch and no model calls.
async function installWorkflows(page: Page) {
  await installWebUIV2Sessions(page);
  const document = profileDocument;
  const records = [{ id: 1, profile_key: "researcher", profile_version: 1, display_name: "Researcher", status: "published", config_json: JSON.stringify(document) }, { id: 0, profile_key: "builtin", profile_version: 1, display_name: "Builtin zero", status: "published", config_json: JSON.stringify(document) }];
  let template: Template = { ...blankTemplate(), id: 1, key: "review", version: 1, name: "Release review", description: "A bounded, no-tool team workflow", agents: [{ ref: "researcher", profile_key: "researcher", profile_version: 1 }] };
  template.definition.nodes[0] = { ...template.definition.nodes[0], agent_ref: "researcher", instruction: "Analyze the supplied release notes" };
  const usage = { input_tokens: 120, output_tokens: 60, cache_read_tokens: 12, cache_write_tokens: 4 };
  let run: Run = { id: "run-fixture", scope: { tenant_id: 1, user_id: 1 }, team_id: 1, team_key: "review", team_version: 1, name: "Release review", input: "Review these release notes", plan: { definition: template.definition, agents: [], hash: "fixture-hash" }, status: "interrupted", revision: 7, epoch: 1, last_sequence: 2, created_at: "2026-10-29T00:00:00Z", deadline: "2026-10-29T00:10:00Z", nodes: [{ node_id: "step-1", status: "interrupted", attempts: [{ number: 1, epoch: 1, task_id: 1, status: "interrupted", usage, started_at: "2026-10-29T00:00:00Z", provider: "configured", model: "configured-model", failure: { code: "process_interrupted", message: "Execution was interrupted", retryable: true } }] }], artifacts: [], usage };
  const calls: { path: string; method: string; body: unknown; idempotency?: string }[] = [];
  await page.route(/\/tenant\/(?:workflows|workflow-runs|agent-profiles)(?:[/?]|$)/, async (route) => {
    const request = route.request(); const url = new URL(request.url()); const path = url.pathname.replace(/^\/api/, ""); const body = request.postData() ? request.postDataJSON() : undefined;
    calls.push({ path, method: request.method(), body, idempotency: request.headers()["idempotency-key"] });
    if (path === "/tenant/agent-profiles" && request.method() === "GET") return route.fulfill({ json: { data: records } });
    if (path === "/tenant/agent-profiles" && request.method() === "POST") {
      const profile = { id: records.length + 1, profile_key: body.profile_key, profile_version: 1, display_name: body.display_name, status: "draft", config_json: JSON.stringify(body.config) }; records.push(profile); return route.fulfill({ json: profile });
    }
    if (path.includes("/agent-profiles/") && path.endsWith("/publish")) { const profile = records.find((item) => path.includes(item.profile_key)); if (profile) profile.status = "published"; return route.fulfill({ json: { status: "published" } }); }
    if (path === "/tenant/workflows/templates") return route.fulfill({ json: { items: [{ key: "research-review", name: "串行审查", description: "研究与审查" }, { key: "parallel-review", name: "并行审查", description: "并行研究与汇总" }] } });
    if (path.startsWith("/tenant/workflows/templates/")) return route.fulfill({ json: { template } });
    if (path === "/tenant/workflows" && request.method() === "POST") { template = { ...(body as TemplateInput), id: 1, key: body.key || "review", version: template.version + 1 }; return route.fulfill({ json: { template } }); }
    if (path === "/tenant/workflows") return route.fulfill({ json: { items: [template] } });
    if (path.endsWith("/runs") && request.method() === "GET") return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/runs") && request.method() === "POST") {
      run = { ...run, input: body.input, plan: { ...run.plan, definition: template.definition }, status: "completed", revision: 8, last_sequence: 3, nodes: template.definition.nodes.map((node) => ({ node_id: node.id, status: "completed", output: "Persisted release analysis", attempts: [{ number: 1, epoch: 1, task_id: 1, status: "completed", started_at: "2026-10-29T00:00:00Z", usage }] })), artifacts: [{ id: "result-1", node_id: "step-1", attempt_no: 1, kind: "text", content: "Persisted release analysis", sha256: "fixture-sha256", created_at: "2026-10-29T00:00:01Z" }] }; return route.fulfill({ json: { run, replayed: false } });
    }
    if (path.endsWith("/recover")) { expect(body).toEqual({ revision: 7 }); run = { ...run, status: "running", revision: 8, epoch: 2 }; return route.fulfill({ json: { run } }); }
    if (path.endsWith("/cancel")) { run = { ...run, status: "cancelled", revision: 9, last_sequence: 3 }; return route.fulfill({ json: { run } }); }
    if (path.endsWith("/events/stream")) return route.fulfill({ status: 503, json: { error: "fixture disconnect" } });
    if (path.endsWith("/events")) {
      const events: WorkflowEvent[] = [{ sequence: 1, type: "run_started", status: "running", created_at: "2026-10-29T00:00:00Z" }, { sequence: 2, type: "agent_started", node_id: "step-1", attempt_no: 1, status: "running", created_at: "2026-10-29T00:00:00Z" }, ...(run.status === "completed" || run.status === "cancelled" ? [{ sequence: 3, type: run.status === "cancelled" ? "run_cancelled" as const : "run_completed" as const, status: run.status, created_at: "2026-10-29T00:00:01Z" }] : [])];
      return route.fulfill({ json: { events: events.filter((event) => event.sequence > Number(url.searchParams.get("after_sequence") || 0)), has_more: false, last_sequence: run.last_sequence } });
    }
    return route.fulfill({ json: { run } });
  });
  return calls;
}
const evidencePath = (name: string) => resolve(process.cwd(), "../desktop-v2/build/validation/20261029", `workflow-browser-mock-${name}.png`);
async function noOverflow(page: Page) { expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true); expect(await page.locator(".workflow-workbench").evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true); }
for (const scenario of [{ width: 360, height: 800, language: "en", theme: "light" }, { width: 768, height: 900, language: "zh", theme: "dark" }, { width: 1024, height: 700, language: "en", theme: "light" }, { width: 1440, height: 960, language: "zh", theme: "dark" }] as const) {
  test(`real HTTP adapter workflow UI ${scenario.width} ${scenario.language} ${scenario.theme}`, async ({ page }) => {
    await page.setViewportSize({ width: scenario.width, height: scenario.height });
    await page.addInitScript(({ language, theme }) => { localStorage.setItem("niuma-webui.language.v1", language); localStorage.setItem("niuma-webui.v2.theme.v1", theme); localStorage.setItem("niuma-webui.v2.inspector-default", "true"); }, scenario);
    const calls = await installWorkflows(page); const zh = scenario.language === "zh";
    await page.goto("/webui/v2/workflows"); await expect(page.locator(".workflow-template")).toHaveCount(2); await noOverflow(page);
    await page.screenshot({ path: evidencePath(`${scenario.width}-${scenario.language}-${scenario.theme}-list`) });
    await page.locator(".workflow-template").first().click(); await expect(page.locator(".workflow-step-list li")).toHaveCount(1);
    await expect(page.locator(".workflow-step-list select option").filter({ hasText: "Builtin zero" })).toHaveCount(0);
    await page.getByRole("button", { name: zh ? "+ 添加步骤" : "+ Add step", exact: true }).click();
    await page.locator(".workflow-step-list li").nth(1).getByRole("combobox").selectOption("1");
    await page.locator(".workflow-step-list li").nth(1).getByRole("textbox").fill("Review the analysis");
    await page.getByRole("combobox", { name: zh ? "步骤" : "Steps", exact: true }).selectOption("sequential");
    await page.getByRole("button", { name: zh ? "保存新版本" : "Save new version", exact: true }).click();
    await expect.poll(() => calls.filter((call) => call.path === "/tenant/workflows" && call.method === "POST").length).toBe(1);
    await expect(page.locator(".workflow-heading-fields small")).toHaveText("review · v2");
    const save = calls.find((call) => call.path === "/tenant/workflows" && call.method === "POST")!.body as TemplateInput;
    expect(save).not.toHaveProperty("id"); expect(save).not.toHaveProperty("version"); expect(save.definition.nodes[1].depends_on).toEqual(["step-1"]); expect(save.agents[0].profile_id).toBe(1);
    await page.locator(".workflow-workbench").evaluate((element) => element.scrollTo(0, 0)); await noOverflow(page);
    await page.screenshot({ path: evidencePath(`${scenario.width}-${scenario.language}-${scenario.theme}-editor`) });
    await page.locator(".workflow-step-list li").first().scrollIntoViewIfNeeded();
    await page.screenshot({ path: evidencePath(`${scenario.width}-${scenario.language}-${scenario.theme}-steps`) });
    await page.getByRole("textbox", { name: zh ? "目标 / 输入" : "Goal / input", exact: true }).fill("Review these release notes");
    await page.getByRole("button", { name: zh ? "启动真实运行" : "Start real run", exact: true }).click();
    await expect(page.locator(".workflow-run")).toContainText("Persisted release analysis"); await expect(page.locator(".workflow-timeline")).toContainText("run_completed");
    await expect(page.locator(".webui2-inspector")).toHaveCount(0); await expect(page.locator(".webui2-conversation")).toHaveCount(0); await noOverflow(page);
    await page.screenshot({ path: evidencePath(`${scenario.width}-${scenario.language}-${scenario.theme}-run`) });
    await page.getByRole("heading", { name: zh ? "产物" : "Artifacts", exact: true }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: evidencePath(`${scenario.width}-${scenario.language}-${scenario.theme}-artifacts`) });
    const start = calls.find((call) => call.path === "/tenant/workflows/review/runs" && call.method === "POST")!;
    expect(start.idempotency).toBeTruthy(); expect(start.body).toEqual({ version: 2, input: "Review these release notes" });
  });
}
test("Agent creation/publish, independent leave guard, interrupted recovery and cancel use actual adapter requests", async ({ page }) => {
  const calls = await installWorkflows(page);
  await page.goto("/webui/v2/workflows/agents");
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("Private researcher");
  await page.getByRole("textbox", { name: "System prompt", exact: true }).fill("Work only with supplied materials");
  await page.getByRole("button", { name: "Workflows", exact: true }).click(); await expect(page.getByRole("dialog")).toBeVisible(); await page.getByRole("button", { name: "Keep editing", exact: true }).click();
  await page.getByRole("button", { name: "Save agent draft", exact: true }).click();
  const card = page.locator("article").filter({ has: page.getByRole("heading", { name: "Private researcher", exact: true }) });
  await card.getByRole("button", { name: "Publish", exact: true }).click(); await expect(card).toContainText("Published");
  await page.screenshot({ path: evidencePath("1440-en-light-agents") });
  const profile = calls.find((call) => call.path === "/tenant/agent-profiles" && call.method === "POST")!.body as { scope: string; config: { execution: { kind: string }; capabilities: { tools: { allow: string[] } } } };
  expect(profile.scope).toBe("user_private"); expect(profile.config.execution.kind).toBe("text"); expect(profile.config.capabilities.tools.allow).toEqual([]);
  await page.goto("/webui/v2/workflows/runs/run-fixture"); await page.getByRole("button", { name: "Recover interrupted run", exact: true }).click();
  await expect(page.getByRole("button", { name: "Cancel run", exact: true })).toBeVisible(); await page.getByRole("button", { name: "Cancel run", exact: true }).click();
  await expect(page.locator(".workflow-run > header .workflow-status")).toHaveText("Cancelled"); await expect(page.locator(".workflow-timeline")).toContainText("run_cancelled");
});
// Compare the actual rendered foreground/background pair, not just the presence of a hover rule.
async function renderedButtonContrast(button: Locator) {
  return button.evaluate((element) => {
    const style = getComputedStyle(element);
    const rgb = (value: string) => {
      const channels = (value.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
      return value.startsWith("color(srgb ") ? channels.map((channel) => channel * 255) : channels;
    };
    const luminance = (value: string) => rgb(value).map((channel) => {
      const normalized = channel / 255;
      return normalized <= .04045 ? normalized / 12.92 : ((normalized + .055) / 1.055) ** 2.4;
    }).reduce((sum, channel, index) => sum + channel * [.2126, .7152, .0722][index], 0);
    const foreground = luminance(style.color);
    const background = luminance(style.backgroundColor);
    return { ratio: (Math.max(foreground, background) + .05) / (Math.min(foreground, background) + .05), background: style.backgroundColor };
  });
}
for (const theme of ["light", "dark"] as const) {
  test(`Team hover controls retain readable text in ${theme} mode`, async ({ page }) => {
    await page.addInitScript((selection) => localStorage.setItem("niuma-webui.v2.theme.v1", selection), theme);
    await installWorkflows(page);
    await page.goto("/webui/v2/workflows/agents");
    const primary = page.locator(".workflow-navigation > .workflow-primary");
    await expect(primary).toBeVisible();
    const normalPrimary = await renderedButtonContrast(primary);
    await primary.hover();
    const hoveredPrimary = await renderedButtonContrast(primary);
    expect(normalPrimary.ratio).toBeGreaterThanOrEqual(4.5);
    expect(hoveredPrimary.ratio).toBeGreaterThanOrEqual(4.5);
    expect(hoveredPrimary.background).not.toBe(normalPrimary.background);

    const activeTab = page.getByRole("button", { name: "Agents", exact: true });
    const activeBackground = (await renderedButtonContrast(activeTab)).background;
    await activeTab.hover();
    expect((await renderedButtonContrast(activeTab)).background).toBe(activeBackground);
    const tab = page.getByRole("button", { name: "Workflows", exact: true });
    await tab.hover();
    expect((await renderedButtonContrast(tab)).ratio).toBeGreaterThanOrEqual(4.5);
    await page.getByRole("textbox", { name: "Name", exact: true }).fill("Unsaved draft");
    await tab.click();
    const dialog = page.getByRole("dialog", { name: "You have unsaved workflow or agent changes." });
    await expect(dialog).toBeVisible();
    for (const name of ["Keep editing", "Discard changes"]) {
      const action = dialog.getByRole("button", { name, exact: true });
      await page.mouse.move(0, 0);
      const normal = await renderedButtonContrast(action);
      await action.hover();
      const hovered = await renderedButtonContrast(action);
      expect(normal.ratio).toBeGreaterThanOrEqual(4.5);
      expect(hovered.ratio).toBeGreaterThanOrEqual(4.5);
      expect(hovered.background).not.toBe(normal.background);
    }
    await dialog.getByRole("button", { name: "Discard changes" }).click();
    const quiet = page.locator(".workflow-workbench .workflow-quiet").first();
    await expect(quiet).toBeVisible();
    await quiet.hover();
    const quietHover = await renderedButtonContrast(quiet);
    expect(quietHover.ratio).toBeGreaterThanOrEqual(4.5);
    expect(quietHover.background).toBe(await page.locator(".workflow-tabs").evaluate((element) => getComputedStyle(element).backgroundColor));
  });
}
test("1024px dark Chinese Agent controls stay aligned and legible", async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 700 });
  await page.addInitScript(() => { localStorage.setItem("niuma-webui.language.v1", "zh"); localStorage.setItem("niuma-webui.v2.theme.v1", "dark"); });
  await installWorkflows(page);
  await page.goto("/webui/v2/workflows/agents");
  await expect(page.locator(".workflow-agent-row")).toHaveCount(2);
  await noOverflow(page);
  const layout = await page.locator(".workflow-agent-directory > .workflow-section-heading").evaluate((heading) => {
    const labelBottom = heading.firstElementChild!.getBoundingClientRect().bottom;
    const buttons = [...heading.querySelectorAll(".workflow-actions button")].map((button) => button.getBoundingClientRect());
    return { labelBottom, buttons: buttons.map(({ top, right, height }) => ({ top, right, height })), directoryRight: heading.parentElement!.getBoundingClientRect().right };
  });
  expect(layout.buttons).toHaveLength(2);
  expect(layout.buttons[0].top).toBeGreaterThanOrEqual(layout.labelBottom - 1);
  expect(layout.buttons[0].top).toBe(layout.buttons[1].top);
  expect(layout.buttons.every((button) => button.height <= 40 && button.right <= layout.directoryRight + 1)).toBe(true);
  const styles = await page.locator(".workflow-agent-form").evaluate((form) => {
    const select = getComputedStyle(form.querySelector("select")!);
    return { appearance: select.appearance, color: select.color, backgroundImage: select.backgroundImage, paddingRight: select.paddingRight,
      textColor: getComputedStyle(form.closest(".workflow-workbench")!).color,
      labelColor: getComputedStyle(form.querySelector("label")!).color,
      mutedColor: getComputedStyle(form.closest(".workflow-workbench")!.querySelector(".workflow-muted")!).color };
  });
  expect(styles).toMatchObject({ appearance: "none", color: styles.textColor, labelColor: styles.mutedColor, paddingRight: "34px" });
  expect(styles.backgroundImage).toContain("svg+xml");
  await page.getByRole("combobox", { name: "角色" }).selectOption("reviewer");
  await expect(page.getByRole("combobox", { name: "角色" })).toHaveValue("reviewer");
  await page.screenshot({ path: evidencePath("1024-zh-dark-agents-review") });
});
