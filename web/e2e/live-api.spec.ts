import { expect, test, type APIRequestContext } from "@playwright/test";

const productEnv = (suffix: string): string | undefined =>
  process.env[`NIUMA_${suffix}`] ?? process.env[`GOLANG_CLAUDE_CODE_${suffix}`];

const liveEnabled = productEnv("WEBUI_LIVE_E2E") === "1";
const serverApiBase = productEnv("WEBUI_LIVE_API_BASE") || "http://127.0.0.1:8080";
const browserApiBase = productEnv("WEBUI_LIVE_BROWSER_API_BASE") || "/api";
const apiToken = productEnv("WEBUI_LIVE_API_TOKEN") || "test-token";
const tenantKey = productEnv("WEBUI_LIVE_TENANT") || "webui-live";
const userId = productEnv("WEBUI_LIVE_USER") || `webui-live-${Date.now()}`;
const deviceId = productEnv("WEBUI_LIVE_DEVICE") || "playwright";
const model = productEnv("WEBUI_LIVE_MODEL") || "gpt-5.5";
const bootstrapTenantKey = productEnv("WEBUI_LIVE_BOOTSTRAP_TENANT") || "yutang";
const bootstrapUserId = productEnv("WEBUI_LIVE_BOOTSTRAP_USER") || "webui-live-bootstrap";
const livePrompt = [
  "Run the WebUI validation scenario.",
  "Do not call tools, commands, tests, shell, browser, or network.",
  "Reply in one short paragraph confirming that long-term memory, short-term document context, profile, skills, trace, telemetry, token usage, and prompt cache are inspectable in the WebUI."
].join("\n");

test.skip(!liveEnabled, "set NIUMA_WEBUI_LIVE_E2E=1 and start a real API server to run live WebUI E2E");

test("drives live mobile chat validation through api server", async ({ page, request }) => {
  test.setTimeout(180_000);
  const headers = {
    Authorization: `Bearer ${apiToken}`,
    "X-Tenant-Key": tenantKey,
    "X-User-Id": userId,
    "X-Device-Id": deviceId
  };
  const health = await request.get(`${serverApiBase}/health`, {
    headers: { Authorization: `Bearer ${apiToken}` }
  });
  expect(health.ok()).toBeTruthy();
  await ensureLiveTenantContext(request);

  await page.addInitScript(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
  });
  await page.goto("/");
  await page.getByRole("navigation", { name: "Primary navigation" }).getByRole("button", { name: /Run Context/ }).click();
  const contextPanel = page.locator(".context-panel");
  await contextPanel.getByLabel("API Base").fill(browserApiBase);
  await contextPanel.getByLabel("API Token").fill(apiToken);
  await contextPanel.getByRole("textbox", { name: "Tenant", exact: true }).fill(tenantKey);
  await contextPanel.getByRole("textbox", { name: "User", exact: true }).fill(userId);
  await contextPanel.getByLabel("Device").fill(deviceId);
  await contextPanel.getByLabel("Model").fill(model);
  await page.getByRole("button", { name: "Save context" }).click();

  await page.getByTitle("Seed validation scenario").click();
  await page.getByRole("button", { name: /niuma/ }).click();
  await expect(page.getByText(/Seeded scenario session/)).toBeVisible({ timeout: 30_000 });
  const statusText = (await page.locator(".welcome-status-card").innerText()).trim();
  const sessionID = Number(statusText.match(/session (\d+)/)?.[1]);
  expect(sessionID).toBeGreaterThan(0);

  await page.getByRole("navigation", { name: "Primary navigation" }).getByRole("button", { name: /Chat Lab/ }).click();
  await page.getByPlaceholder("Send a message through Mobile Chat API...").fill(livePrompt);
  await page.getByRole("button", { name: "Send" }).click();
  await page.getByRole("button", { name: /niuma/ }).click();
  await expect(page.locator(".welcome-status-card")).toContainText("Stream completed", { timeout: 120_000 });
  await page.getByRole("navigation", { name: "Primary navigation" }).getByRole("button", { name: /Chat Lab/ }).click();
  await expect(page.getByText("Run the WebUI validation scenario.").first()).toBeVisible();

  await page.getByRole("navigation", { name: "Secondary navigation" }).getByRole("button", { name: /Validation/ }).click();
  await page.getByTitle("Refresh validation").click();
  await expect(page.getByText(/Mobile session/)).toBeVisible();
  await expect(page.getByText(/Conversation cycle/)).toBeVisible();
  await expect(page.getByText(/Telemetry lifecycle/)).toBeVisible();

  const messages = await request.get(`${serverApiBase}/tenant/messages?session_id=${sessionID}&limit=10`, { headers });
  expect(messages.ok()).toBeTruthy();
  await expectResponseText(messages, "Run the WebUI validation scenario.");

  const memories = await request.get(`${serverApiBase}/tenant/memories?limit=20`, { headers });
  expect(memories.ok()).toBeTruthy();
  await expectResponseText(memories, "webui.validation.preference");

  const profile = await request.get(`${serverApiBase}/tenant/profile`, { headers });
  expect(profile.ok()).toBeTruthy();
  await expectResponseText(profile, "webui-scenario");

  const documents = await request.get(`${serverApiBase}/tenant/documents?history=true&limit=20`, { headers });
  expect(documents.ok()).toBeTruthy();
  await expectResponseText(documents, "WebUI Scenario");

  const skills = await request.get(`${serverApiBase}/tenant/effective-skills?enabled=true&limit=50`, { headers });
  expect(skills.ok()).toBeTruthy();
  await expectResponseText(skills, "webui-validation");

  const telemetry = await request.get(`${serverApiBase}/tenant/telemetry?limit=100&search=${sessionID}`, { headers });
  expect(telemetry.ok()).toBeTruthy();
  await expectResponseText(telemetry, "mobile.chat.stream.finished");

  const trace = await request.get(`${serverApiBase}/trace/api/sessions/${sessionID}?source=tenant&limit=100&trace_limit=100&task_limit=200`, { headers });
  expect(trace.ok()).toBeTruthy();
  await expectResponseText(trace, String(sessionID));
});

async function expectResponseText(response: { text: () => Promise<string> }, expected: string) {
  const text = await response.text();
  expect(text).toContain(expected);
}

async function ensureLiveTenantContext(request: APIRequestContext) {
  const bootstrapHeaders = {
    Authorization: `Bearer ${apiToken}`,
    "X-Tenant-Key": bootstrapTenantKey,
    "X-User-Id": bootstrapUserId,
    "X-Device-Id": deviceId
  };
  const bootstrapUser = await request.patch(`${serverApiBase}/tenant/user`, {
    headers: bootstrapHeaders,
    data: {
      email: `${bootstrapUserId}@example.test`,
      display_name: "WebUI Live Bootstrap",
      role: "owner",
      status: "active",
      user_info_json: JSON.stringify({ source: "webui-live-e2e", bootstrap: true })
    }
  });
  expect(bootstrapUser.ok()).toBeTruthy();

  const tenant = await request.post(`${serverApiBase}/tenant/tenants`, {
    headers: bootstrapHeaders,
    data: {
      tenant_key: tenantKey,
      name: "WebUI Live E2E",
      status: "active",
      settings_json: JSON.stringify({ source: "webui-live-e2e" })
    }
  });
  expect(tenant.ok()).toBeTruthy();

  const targetUser = await request.patch(`${serverApiBase}/tenant/user`, {
    headers: {
      Authorization: `Bearer ${apiToken}`,
      "X-Tenant-Key": tenantKey,
      "X-User-Id": userId,
      "X-Device-Id": deviceId
    },
    data: {
      email: `${userId}@example.test`,
      display_name: "WebUI Live User",
      role: "owner",
      status: "active",
      user_info_json: JSON.stringify({ source: "webui-live-e2e" })
    }
  });
  expect(targetUser.ok()).toBeTruthy();
}
