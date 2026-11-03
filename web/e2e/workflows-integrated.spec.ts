import { resolve } from "node:path";
import { type APIRequestContext, expect, type Page, test } from "@playwright/test";

// No page.route / route.fulfill: every Profile/Workflow/Run request reaches real Go + SQLite.
const enabled = process.env.NIUMA_WORKFLOW_INTEGRATION_E2E === "1";
const backend = process.env.NIUMA_WEBUI_LIVE_API_BASE ?? "";
const provider = process.env.NIUMA_WORKFLOW_OFFLINE_PROVIDER_BASE ?? "";
const auth = { Authorization: "Bearer offline-auth", "X-Tenant-Key": "webui-local", "X-User-Id": "webui-local-user", "X-Device-Id": "workflow-integrated" };
const date = process.env.NIUMA_WORKFLOW_EVIDENCE_DATE ?? new Date().toISOString().slice(0, 10).replaceAll("-", "");
const evidence = (name: string) => resolve(process.cwd(), `../desktop-v2/build/validation/${date}/workflow-browser-integrated-${name}.png`);
type Binding = { ref: string; profile_id?: number; profile_key: string; profile_version: number };
type Template = { id: number; key: string; version: number; name: string; agents: Binding[]; definition: { nodes: { id: string; agent_ref: string; depends_on: string[]; instruction: string; retry: { max_attempts: number } }[] } };
type Attempt = { number: number; status: string; failure?: { code: string; retryable: boolean } };
type Run = { id: string; team_version: number; status: string; revision: number; last_sequence: number; plan: { definition: Template["definition"]; agents: { tools: string[] | null }[] }; nodes: { node_id: string; status: string; attempts: Attempt[]; output?: string }[]; artifacts: { content: string; node_id: string }[]; usage: { input_tokens: number; output_tokens: number } };
type Event = { sequence: number; type: string; node_id?: string; attempt_no?: number; status?: string };
type ProviderState = { service: string; requests: number; inflight: number; max_inflight: number; calls: { mode: string; dependency_count: number; outcome: string }[] };
let agentID = 0;
const pageErrors = new WeakMap<Page, string[]>();

function literalLoopback(value: string): URL {
  if (!/^http:\/\/(?:127\.0\.0\.1|\[::1\]):[0-9]+(?:\/v1)?\/?$/.test(value)) throw new Error("Integrated acceptance refuses any non-literal-loopback endpoint");
  const url = new URL(value);
  if (url.username || url.password || url.search || url.hash || !url.port) throw new Error("Unsafe loopback endpoint");
  return url;
}
async function get<T>(request: APIRequestContext, path: string): Promise<T> {
  const response = await request.get(`${backend}${path}`, { headers: auth, maxRedirects: 0 });
  expect(response.ok(), `Real backend GET ${path}: HTTP ${response.status()}`).toBe(true);
  return await response.json() as T;
}
async function providerState(request: APIRequestContext): Promise<ProviderState> {
  const response = await request.get(`${literalLoopback(provider).origin}/__control/state`, { maxRedirects: 0 });
  expect(response.ok()).toBe(true); return await response.json() as ProviderState;
}
async function setMode(request: APIRequestContext, mode: "fast" | "slow" | "fail-3-times" | "always-fail") {
  await expect.poll(async () => (await providerState(request)).inflight, { timeout: 10000 }).toBe(0);
  const response = await request.post(`${literalLoopback(provider).origin}/__control/reset`, { data: { mode, slow_seconds: 45 }, maxRedirects: 0 });
  expect(response.ok()).toBe(true);
}
async function runReadback(request: APIRequestContext, id: string): Promise<Run> { return (await get<{ run: Run }>(request, `/tenant/workflow-runs/${encodeURIComponent(id)}`)).run; }
async function waitRun(request: APIRequestContext, id: string, expected: string): Promise<Run> {
  await expect.poll(async () => (await runReadback(request, id)).status, { timeout: 45000, intervals: [100, 250, 500] }).toBe(expected);
  return runReadback(request, id);
}
async function events(request: APIRequestContext, id: string): Promise<Event[]> {
  const result: Event[] = []; let after = 0;
  for (let page = 0; page < 20; page++) {
    const value = await get<{ events: Event[]; has_more: boolean }>(request, `/tenant/workflow-runs/${id}/events?after_sequence=${after}`);
    for (const event of value.events ?? []) { expect(event.sequence).toBeGreaterThan(after); after = event.sequence; result.push(event); }
    if (!value.has_more) return result;
  }
  throw new Error("Integrated event readback exceeded its bounded page budget");
}
function canonicalDependencies(template: Template["definition"]) { for (const node of template.nodes) expect(Array.isArray(node.depends_on), `Real JSON depends_on must be [] not null for ${node.id}`).toBe(true); }
async function save(page: Page, existing = false): Promise<Template> {
  const response = page.waitForResponse((value) => new URL(value.url()).pathname.endsWith("/tenant/workflows") && value.request().method() === "POST");
  await page.getByRole("button", { name: existing ? "Save new version" : "Save workflow", exact: true }).click();
  const actual = await response; expect(actual.ok(), `Save real template HTTP ${actual.status()}`).toBe(true);
  const { template } = await actual.json() as { template: Template };
  canonicalDependencies(template.definition);
  expect(template.agents.every((binding) => Number(binding.profile_id) > 0)).toBe(true);
  await expect(page.locator(".workflow-heading-fields small")).toContainText(`v${template.version}`);
  return template;
}
async function start(page: Page, request: APIRequestContext, template: Template, expected = "completed"): Promise<Run> {
  await page.getByRole("textbox", { name: "Goal / input", exact: true }).fill("Offline integration acceptance. Use only supplied fixture input.");
  const response = page.waitForResponse((value) => new URL(value.url()).pathname.endsWith(`/tenant/workflows/${template.key}/runs`) && value.request().method() === "POST");
  await page.getByRole("button", { name: "Start real run", exact: true }).click();
  const actual = await response; expect(actual.ok(), `Start real run HTTP ${actual.status()}`).toBe(true);
  expect(actual.request().headers()["idempotency-key"]).toBeTruthy();
  const { run } = await actual.json() as { run: Run };
  canonicalDependencies(run.plan.definition);
  expect(run.plan.agents.every((agent) => !(agent.tools ?? []).length)).toBe(true);
  const result = await waitRun(request, run.id, expected);
  await expect(page.locator(".workflow-run > header .workflow-status")).toHaveText(expected === "completed" ? "Completed" : expected === "failed" ? "Failed" : "Running", { timeout: 10000 });
  return result;
}
async function newSingleStep(page: Page, title: string, attempts = 2): Promise<Template> {
  await page.goto("/webui/v2/workflows/new");
  await page.getByRole("textbox", { name: "Name", exact: true }).fill(title);
  const step = page.locator(".workflow-step-list li").first();
  await step.getByRole("combobox").selectOption(String(agentID));
  await step.getByRole("textbox").fill("Return a concise offline fixture result.");
  if (attempts !== 2) { await step.locator("summary").click(); await step.getByRole("spinbutton", { name: "Maximum attempts", exact: true }).fill(String(attempts)); }
  return save(page);
}

test.skip(!enabled, "Manual opt-in only: NIUMA_WORKFLOW_INTEGRATION_E2E=1 plus isolated loopback backend/provider");
test.describe.configure({ mode: "serial" });
test.setTimeout(90000);

test.beforeAll(async ({ request, baseURL }) => {
  if (!enabled) return;
  literalLoopback(backend); literalLoopback(provider); literalLoopback(baseURL ?? "");
  if (!/^[0-9]{8}$/.test(date)) throw new Error("Unsafe evidence date");
  expect((await providerState(request)).service).toBe("niuma-workflow-offline-provider");
  const settings = await get<{ file_resolved: { doc: Record<string, unknown> }; process_snapshot: { available: boolean; doc: Record<string, unknown> } }>(request, "/runtime/settings/effective");
  expect(settings.process_snapshot.available, "A known isolated startup route is required").toBe(true);
  for (const doc of [settings.file_resolved.doc, settings.process_snapshot.doc]) {
    expect(doc.provider).toBe("openai"); expect(doc.providerProtocol).toBe("openai-chat-completions");
    expect(doc.baseURL).toBe(`${literalLoopback(provider).origin}/v1`); expect(doc.model).toBe("fast");
  }
  {
    const doc = settings.file_resolved.doc;
    expect(doc.fallback).toMatchObject({ enabled: false });
    expect((doc.fallback as { providers?: unknown[] }).providers ?? []).toHaveLength(0);
    expect(doc.update).toMatchObject({ enabled: false });
    expect(Object.keys((doc.hooks as object | undefined) ?? {})).toHaveLength(0);
    expect(Object.keys((doc.mcpServers as object | undefined) ?? {})).toHaveLength(0);
  }
  expect((await get<{ items: Template[] }>(request, "/tenant/workflows")).items ?? [], "Refuse to mutate an existing non-isolated workflow database").toHaveLength(0);
  const profiles = await get<{ data: { id: number }[] }>(request, "/tenant/agent-profiles");
  expect((profiles.data ?? []).filter((profile) => profile.id > 0), "Refuse an existing customer Profile database").toHaveLength(0);
});
test.beforeEach(async ({ page }) => {
  const errors: string[] = []; pageErrors.set(page, errors); page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (["http:", "https:"].includes(url.protocol) && !["127.0.0.1", "[::1]"].includes(url.hostname)) errors.push("Integrated browser attempted a non-loopback request");
    if (/\/tenant\/(?:workflows|workflow-runs|agent-profiles)/.test(url.pathname)) {
      const headers = request.headers();
      if (headers.authorization !== auth.Authorization || headers["x-tenant-key"] !== auth["X-Tenant-Key"] || headers["x-user-id"] !== auth["X-User-Id"] || headers["x-device-id"] !== auth["X-Device-Id"]) errors.push("Real browser tenant identity headers do not match the isolated backend");
    }
  });
  await page.addInitScript(() => {
    localStorage.setItem("niuma-webui.identity.v1", JSON.stringify({ apiBase: "/api", apiToken: "offline-auth", mobileJwt: "", tenantKey: "webui-local", userId: "webui-local-user", deviceId: "workflow-integrated", model: "" }));
    localStorage.setItem("niuma-webui.language.v1", "en"); localStorage.setItem("niuma-webui.v2.theme.v1", "light");
  });
});
test.afterEach(async ({ page }) => { expect(pageErrors.get(page) ?? [], "Unexpected UI exception (report wire-contract bugs, do not hide them)").toEqual([]); });

test("create a primary-model Agent without named providers, save sequential dependencies, refresh SQLite readback", async ({ page, request }) => {
  await setMode(request, "fast");
  await page.goto("/webui/v2/workflows/agents");
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("Integrated offline researcher");
  await page.getByRole("textbox", { name: "System prompt", exact: true }).fill("Summarize only the synthetic supplied material; use no tools.");
  expect((await get<{ data: { name: string }[] }>(request, "/v1/providers")).data).toEqual([]);
  await page.locator('select[aria-label="Model"]').selectOption("custom");
  await page.getByRole("combobox", { name: "Provider", exact: true }).selectOption("primary");
  await page.locator('input[list="workflow-agent-models"]').fill("slow");
  const created = page.waitForResponse((value) => new URL(value.url()).pathname.endsWith("/tenant/agent-profiles") && value.request().method() === "POST");
  await page.getByRole("button", { name: "Save agent draft", exact: true }).click();
  const profileResponse = await created; expect(profileResponse.ok()).toBe(true);
  const profile = await profileResponse.json() as { id: number; scope: string; profile_key: string; profile_version: number; config_json: string };
  agentID = profile.id; expect(agentID).toBeGreaterThan(0); expect(profile.scope).toBe("user_private");
  const config = JSON.parse(profile.config_json); expect(config.execution.kind).toBe("text"); expect(config.execution.provider).toBe("primary"); expect(config.execution.model).toBe("slow"); expect(config.capabilities.tools.allow ?? []).toEqual([]);
  const card = page.locator("article").filter({ has: page.getByRole("heading", { name: "Integrated offline researcher", exact: true }) });
  await card.getByRole("button", { name: "Publish", exact: true }).click(); await expect(card).toContainText("published");
  await page.screenshot({ path: evidence("agent-published") });
  await page.goto("/webui/v2/workflows/new");
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("Integrated custom chain");
  await page.getByRole("button", { name: "+ Add step", exact: true }).click();
  for (const [index, step] of (await page.locator(".workflow-step-list li").all()).entries()) { await step.getByRole("combobox").selectOption(String(agentID)); await step.getByRole("textbox").fill(`Offline sequential step ${index + 1}`); }
  await page.getByRole("combobox", { name: "Steps", exact: true }).selectOption("sequential");
  const template = await save(page); expect(template.definition.nodes.map((node) => node.depends_on)).toEqual([[], ["step-1"]]);
  const run = await start(page, request, template); expect(run.nodes.flatMap((node) => node.attempts).every((attempt) => (attempt as Attempt & { model?: string }).model === "slow")).toBe(true); expect(run.artifacts).toHaveLength(2); expect(run.usage.input_tokens).toBeGreaterThan(0);
  await page.reload(); await expect(page.locator(".workflow-run > header .workflow-status")).toHaveText("Completed"); await expect(page.locator(".workflow-timeline")).toContainText("run_completed");
  expect((await runReadback(request, run.id)).artifacts).toEqual(run.artifacts);
  await page.getByRole("heading", { name: "Artifacts", exact: true }).scrollIntoViewIfNeeded(); await page.screenshot({ path: evidence("sequential-sqlite-readback") });
  // Saving a new immutable Team version must not bury the previous Run.
  await page.goto(`/webui/v2/workflows/edit/${encodeURIComponent(template.key)}`);
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("Integrated custom chain v2");
  const nextVersion = await save(page, true);
  expect(nextVersion.version).toBe(template.version + 1);
  const history = page.getByRole("heading", { name: "Run history · all versions" });
  await expect(history).toBeVisible();
  const oldRun = page.getByRole("button", { name: /Integrated custom chain · v1 · Completed/ });
  await expect(oldRun).toBeVisible(); await oldRun.click();
  await expect(page.locator(".workflow-run > header .workflow-status")).toHaveText("Completed");
  expect((await runReadback(request, run.id)).team_version).toBe(template.version);
});

test("both real builtins create private Profiles; parallel join passes two declared artifacts and overlaps calls", async ({ page, request }) => {
  for (const key of ["research-review", "parallel-review"]) {
    await setMode(request, "fast"); await page.goto("/webui/v2/workflows");
    const created = page.waitForResponse((value) => new URL(value.url()).pathname.endsWith(`/tenant/workflows/templates/${key}/create`) && value.request().method() === "POST");
    await page.locator(".workflow-template").nth(key === "research-review" ? 0 : 1).click();
    const response = await created; expect(response.ok()).toBe(true); let { template } = await response.json() as { template: Template };
    canonicalDependencies(template.definition); expect(template.agents.every((binding) => Number(binding.profile_id) > 0)).toBe(true);
    if (key === "parallel-review") {
      expect(template.definition.nodes).toHaveLength(4);
      expect(template.agents).toHaveLength(4);
      const branchRefs = template.definition.nodes.filter((node) => ["research_a", "research_b"].includes(node.id)).map((node) => node.agent_ref);
      expect(new Set(branchRefs).size).toBe(2);
      expect(new Set(branchRefs.map((ref) => template.agents.find((binding) => binding.ref === ref)?.profile_id)).size).toBe(2);
      const review = page.locator(".workflow-step-list li").last();
      await review.getByRole("textbox").fill("Join and review the two independent offline analyses.");
      template = await save(page, true);
      expect(template.definition.nodes.find((node) => node.id === "review")?.depends_on.sort()).toEqual(["research_a", "research_b"]);
      await review.scrollIntoViewIfNeeded(); await page.screenshot({ path: evidence("parallel-custom-dag") });
    }
    const run = await start(page, request, template); expect(run.artifacts).toHaveLength(key === "research-review" ? 3 : 4);
    const state = await providerState(request);
    expect(state.max_inflight).toBe(key === "research-review" ? 1 : 2);
    if (key === "parallel-review") expect(state.calls.some((call) => call.dependency_count === 2 && call.outcome === "completed")).toBe(true);
    await page.reload(); await expect(page.locator(".workflow-timeline")).toContainText("run_completed");
    await page.getByRole("heading", { name: "Artifacts", exact: true }).scrollIntoViewIfNeeded(); await page.screenshot({ path: evidence(key) });
  }
});

test("three real 503 responses exhaust internal retries, then persisted node retry succeeds", async ({ page, request }) => {
  await setMode(request, "fail-3-times"); const template = await newSingleStep(page, "Integrated node retry");
  const run = await start(page, request, template); expect(run.nodes[0].attempts.map((attempt) => attempt.status)).toEqual(["failed", "completed"]); expect(run.nodes[0].attempts[0].failure?.retryable).toBe(true);
  const history = await events(request, run.id); expect(history.some((event) => event.type === "task_retry_scheduled")).toBe(true);
  const state = await providerState(request); expect(state.calls.map((call) => call.outcome)).toEqual(["503", "503", "503", "completed"]);
  await page.reload(); await expect(page.locator(".workflow-timeline")).toContainText("task_retry_scheduled"); await page.screenshot({ path: evidence("retry-after-three-503") });
});

test("terminal failure is persisted without production mocks", async ({ page, request }) => {
  await setMode(request, "always-fail"); const template = await newSingleStep(page, "Integrated terminal failure", 1);
  const run = await start(page, request, template, "failed"); expect(run.nodes[0].attempts).toHaveLength(1); expect(run.nodes[0].attempts[0].status).toBe("failed"); expect(run.artifacts ?? []).toHaveLength(0);
  expect((await providerState(request)).calls.map((call) => call.outcome)).toEqual(["503", "503", "503"]);
  await page.reload(); await expect(page.locator(".workflow-run > header .workflow-status")).toHaveText("Failed"); await expect(page.locator(".workflow-timeline")).toContainText("run_failed"); await page.screenshot({ path: evidence("failed-readback") });
});

test("cancel an actually active slow SSE request, then refresh persisted cancelled Run", async ({ page, request }) => {
  await setMode(request, "slow"); const template = await newSingleStep(page, "Integrated active cancellation");
  const run = await start(page, request, template, "running");
  await expect.poll(async () => (await providerState(request)).inflight, { timeout: 10000 }).toBe(1);
  await page.getByRole("button", { name: "Cancel run", exact: true }).click();
  const cancelled = await waitRun(request, run.id, "cancelled"); expect(cancelled.artifacts ?? []).toHaveLength(0);
  await expect.poll(async () => (await providerState(request)).inflight, { timeout: 10000 }).toBe(0);
  await page.reload(); await expect(page.locator(".workflow-run > header .workflow-status")).toHaveText("Cancelled"); await expect(page.locator(".workflow-timeline")).toContainText("run_cancelled");
  expect((await providerState(request)).calls.some((call) => call.outcome === "disconnected")).toBe(true);
  await page.screenshot({ path: evidence("active-cancel-sqlite-readback") });
});
