import { expect, test } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { installWebUIV2Sessions } from "./fixtures/webuiV2Sessions";

const settingsDoc = {
  provider: "custom",
  providerProtocol: "openai-chat-completions",
  baseURL: "https://primary.example/v1",
  model: "fixture-model",
  fallback: {
    enabled: true,
    providers: [{
      name: "glm",
      type: "openai-compatible",
      protocol: "openai-chat-completions",
      baseURL: "https://glm.example/v1",
      model: "glm-5.2"
    }]
  },
  appearance: {
    enabled: false,
    backgroundColor: "#f7f8fa",
    backgroundImage: "",
    overlayOpacity: 0,
    sidebarSurfaceOpacity: 0.46,
    emptyBlur: 0,
    conversationBlur: 0,
    composerBlur: 0,
    bubbleOpacity: 1
  },
  pet: {
    enabled: false,
    visible: true,
    model: "go-companion",
    scale: 1,
    right: 24,
    bottom: 20,
    animation: "idle",
    statusBubble: true,
    fontScale: 1
  }
};

test.beforeEach(async ({ page }) => {
  let document = structuredClone(settingsDoc);
  let revision = "r1";
  await installWebUIV2Sessions(page);
  await page.route("**/runtime/settings/environments", (route) => route.fulfill({ json: {
    environments: [{ id: "current", label: "Web", database: "web", tenant_key: "fixture", user_id: "operator", api_path: "", available: true }],
    global_settings_path: "/settings.json",
    global_settings_shared: true
  } }));
  await page.route("**/runtime/settings/validate", (route) => route.fulfill({ json: { valid: true, issues: [] } }));
  await page.route("**/runtime/settings", async (route) => {
    if (route.request().method() === "PUT") {
      document = route.request().postDataJSON();
      revision = "r2";
      return route.fulfill({ json: { saved: true, revision } });
    }
    return route.fulfill({ json: { doc: document, revision, path: "~/.niuma/settings.json", exists: true, masked: [] } });
  });
  await page.route("**/runtime/settings/promote-provider", async (route) => {
    const request = route.request().postDataJSON() as { doc: typeof settingsDoc; provider_index: number };
    const provider = request.doc.fallback.providers[request.provider_index];
    const promoted = {
      ...request.doc,
      provider: provider.type,
      providerProtocol: provider.protocol,
      baseURL: provider.baseURL,
      model: provider.model
    };
    return route.fulfill({ json: { doc: promoted, revision, masked: [] } });
  });
  await page.route("**/tenant/telemetry*", (route) => route.fulfill({ json: { data: [{ id: 1, name: "model.request.finished", status: "ok", created_at: "2026-09-17T01:02:03Z" }] } }));
  await page.route("**/tenant/usage/daily*", (route) => route.fulfill({ json: { data: [{ id: 1, usage_date: "2026-09-17", model: "fixture-model", request_count: 2, total_tokens: 128 }] } }));
  await page.route("**/tenant/usage/ledger*", (route) => route.fulfill({ json: { data: [] } }));
  await page.route((url) => /\/status$/.test(url.pathname), (route) => route.fulfill({ json: { ok: true, workspace: "/workspace/project", model: "fixture-model" } }));
  await page.goto("/webui/v2/settings/appearance");
});

test("renders the compact settings registry and previews appearance changes with reset", async ({ page }, testInfo) => {
  await expect(page.getByRole("heading", { name: "Appearance · Skin", exact: true })).toBeVisible();
  const openNavigation = page.getByRole("button", { name: "Open settings navigation", exact: true });
  if (await openNavigation.isVisible()) await openNavigation.click();
  await expect(page.getByRole("navigation", { name: "Settings categories" }).getByRole("button", { name: "General", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Appearance · Skin", exact: true })).toHaveAttribute("aria-current", "page");
  const closeNavigation = page.getByRole("complementary").getByRole("button", { name: "Close settings navigation", exact: true });
  if (await closeNavigation.isVisible()) await closeNavigation.click();

  await page.getByLabel("Enable appearance").check();
  await page.getByLabel("Background color").fill("#123456");
  await expect(page.locator(".webui2-page")).toHaveAttribute("data-appearance-enabled", "true");
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-visual-background-color").trim())).toBe("#123456");
  const sidebarSurfaceOpacity = page.getByLabel("Sidebar surface opacity");
  await expect(sidebarSurfaceOpacity).toHaveValue("0.46");
  await sidebarSurfaceOpacity.fill("0.76");
  await expect(page.locator("output").filter({ hasText: "76%" })).toBeVisible();
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-sidebar-surface-opacity").trim())).toBe("76%");

  page.once("dialog", (dialog) => void dialog.accept());
  await page.getByRole("button", { name: "Reset", exact: true }).click();
  await expect(page.locator(".webui2-page")).toHaveAttribute("data-appearance-enabled", "false");
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-visual-background-color").trim())).toBe("#f7f8fa");
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("settings-v2-appearance.png"), fullPage: false });
});

test("persists a custom sidebar surface opacity and restores it after reload", async ({ page }) => {
  await page.getByLabel("Enable appearance").check();
  const sidebarSurfaceOpacity = page.getByLabel("Sidebar surface opacity");
  await sidebarSurfaceOpacity.fill("0.76");
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-sidebar-surface-opacity").trim())).toBe("76%");

  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Saved and confirmed by readback." })).toBeVisible();

  await page.reload();
  await expect(page.getByLabel("Sidebar surface opacity")).toHaveValue("0.76");
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-sidebar-surface-opacity").trim())).toBe("76%");
});

test("renders pet preview and reads actual observability records", async ({ page }, testInfo) => {
  const openNavigation = page.getByRole("button", { name: "Open settings navigation", exact: true });
  if (await openNavigation.isVisible()) await openNavigation.click();
  await page.getByRole("button", { name: "Pet", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Pet", exact: true })).toBeVisible();
  await page.getByLabel("Enable pet").check();
  await chooseSettingsSelect(page, "Model asset", "Chinese Dragon");
  const preview = page.locator(".pet-settings-preview");
  const canvas = preview.locator("canvas");
  const scene = preview.locator("[data-render-state]");
  await expect(scene).toHaveAttribute("data-render-state", "ready");
  await expect(scene).toHaveAttribute("data-asset", "/assets/pets/chinese-dragon.glb");
  await expect(canvas).toBeVisible();
  const dragonPixels = await canvasColorRatio(canvas);
  expect(dragonPixels).toBeGreaterThan(0.1);
  expect(dragonPixels).toBeLessThan(0.8);
  const before = await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL());
  await expect.poll(() => canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL())).not.toBe(before);
  await chooseSettingsSelect(page, "Model asset", "Niuma");
  await expect(scene).toHaveAttribute("data-render-state", "ready");
  await expect(scene).toHaveAttribute("data-asset", "/assets/pets/niuma.glb");
  const niumaPixels = await canvasColorRatio(canvas);
  expect(niumaPixels).toBeGreaterThan(0.1);
  expect(niumaPixels).toBeLessThan(0.8);
  const niumaFrame = await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL());
  await expect.poll(() => canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL())).not.toBe(niumaFrame);
  await chooseSettingsSelect(page, "Animation", "Focus");
  // Focus mode must be stable, not a second independently moving joint loop.
  await expect.poll(async () => {
    const first = await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL());
    await page.waitForTimeout(100);
    return first === await canvas.evaluate((node) => (node as HTMLCanvasElement).toDataURL());
  }).toBe(true);
  await chooseSettingsSelect(page, "Model asset", "Niuma Classic");
  await expect(scene).toHaveAttribute("data-render-state", "ready");
  await expect(scene).toHaveAttribute("data-asset", "/assets/pets/niuma-classic.glb");
  expect(await canvasColorRatio(canvas)).toBeGreaterThan(0.1);
  await chooseSettingsSelect(page, "Model asset", "Go Companion");
  await expect(scene).toHaveAttribute("data-render-state", "ready");
  await expect(scene).toHaveAttribute("data-asset", "/assets/pets/go-companion.glb");
  const robotPixels = await canvasColorRatio(canvas);
  expect(robotPixels).toBeGreaterThan(0.1);
  await chooseSettingsSelect(page, "Animation", "Focus");
  await expect(scene).toHaveAttribute("data-render-state", "ready");
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("settings-v2-pet.png"), fullPage: false });
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Saved and confirmed by readback." })).toBeVisible();

  const reopenNavigation = page.getByRole("button", { name: "Open settings navigation", exact: true });
  if (await reopenNavigation.isVisible()) await reopenNavigation.click();
  await page.getByRole("button", { name: "Observability", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Observability", exact: true })).toBeVisible();
  await expect(page.getByText("model.request.finished", { exact: true })).toBeVisible();
  await expect(page.getByText("128", { exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("settings-v2-observability.png"), fullPage: false });
});

test("drags the desktop pet, persists position, and closes with per-device recovery", async ({ page }, testInfo) => {
  await page.goto("/webui/v2/settings/pet");
  await expect(page.getByRole("heading", { name: "Pet", exact: true })).toBeVisible();
  await page.getByLabel("Enable pet").check();
  await chooseSettingsSelect(page, "Model asset", "Chinese Dragon");
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Saved and confirmed by readback." })).toBeVisible();

  await page.goto("/webui/v2");
  const pet = page.locator(".webui2-desktop-pet");
  await expect(pet).toBeVisible();
  const scene = pet.locator("[data-render-state]");
  await expect(scene).toHaveAttribute("data-render-state", "ready");
  await expect(scene).toHaveAttribute("data-asset", "/assets/pets/chinese-dragon.glb");
  expect(await canvasColorRatio(pet.locator("canvas"))).toBeGreaterThan(0.1);

  const origin = await pet.boundingBox();
  if (!origin) throw new Error("pet has no bounding box");
  const target = { x: Math.max(40, origin.x - 500), y: Math.max(40, origin.y - 350) };
  await page.mouse.move(origin.x + origin.width / 2, origin.y + origin.height / 2);
  await page.mouse.down();
  await page.mouse.move(target.x, target.y, { steps: 12 });
  await page.mouse.up();
  const dragged = await pet.boundingBox();
  if (!dragged) throw new Error("pet has no bounding box");
  expect(dragged.x).toBeLessThan(origin.x - 100);
  expect(dragged.y).toBeLessThan(origin.y - 100);
  await page.screenshot({ path: testInfo.outputPath("desktop-pet-dragged.png"), fullPage: false });

  await page.reload();
  await expect(pet).toBeVisible();
  const restored = await pet.boundingBox();
  if (!restored) throw new Error("pet has no bounding box");
  expect(Math.abs(restored.x - dragged.x)).toBeLessThan(6);
  expect(Math.abs(restored.y - dragged.y)).toBeLessThan(6);

  await pet.click();
  const menu = page.getByRole("menu", { name: "Pet options" });
  await expect(menu).toBeVisible();
  await expect(menu.getByRole("menuitem")).toHaveCount(3);
  const menuBox = await menu.boundingBox();
  const petBox = await pet.boundingBox();
  if (!menuBox || !petBox) throw new Error("pet menu or pet has no bounding box");
  const menuBelow = menuBox.y >= petBox.y + petBox.height;
  const menuGap = menuBelow
    ? menuBox.y - (petBox.y + petBox.height)
    : petBox.y - (menuBox.y + menuBox.height);
  expect(menuGap).toBeGreaterThanOrEqual(6);
  expect(menuGap).toBeLessThan(28);
  expect(menuBox.x).toBeGreaterThanOrEqual(0);
  expect(menuBox.y).toBeGreaterThanOrEqual(0);
  expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(1440);
  expect(menuBox.y + menuBox.height).toBeLessThanOrEqual(960);
  await page.screenshot({ path: testInfo.outputPath("desktop-pet-menu.png"), fullPage: false });
  await menu.getByRole("menuitem", { name: "Close pet" }).click();
  await expect(page.locator(".webui2-desktop-pet")).toHaveCount(0);

  await page.goto("/webui/v2/settings/pet");
  await expect(page.getByText("The pet is closed on this device.")).toBeVisible();
  await page.getByRole("button", { name: "Show on this device" }).click();
  await page.goto("/webui/v2");
  await expect(page.locator(".webui2-desktop-pet")).toBeVisible();
});

test("shows the beginner model setup first and keeps the full editor behind advanced setup", async ({ page }, testInfo) => {
  await page.goto("/webui/v2/settings/models");
  await expect(page.getByRole("heading", { name: "Models", exact: true })).toBeVisible();
  const quick = page.getByRole("tab", { name: "Quick setup", exact: true });
  await expect(quick).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { name: "Quickly configure the primary model", exact: true })).toBeVisible();
  await expect(page.getByLabel("API protocol", { exact: true })).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Save changes", exact: true })).toHaveCount(1);
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("models-quick.png"), fullPage: true });
  await quick.focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Advanced setup", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { name: "Advanced model settings", exact: true })).toBeVisible();
  await expect(page.getByLabel("API protocol", { exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("models-advanced.png"), fullPage: true });
});

test("keeps Chinese quick setup readable in light and dark themes", async ({ page }, testInfo) => {
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme => {
      localStorage.setItem("niuma-webui.language.v1", "zh");
      localStorage.setItem("niuma-webui.v2.theme.v1", theme);
    }, theme);
    await page.goto("/webui/v2/settings/models");
    await expect(page.getByRole("tab", { name: "快速配置", exact: true })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByLabel("模型名称", { exact: true })).toHaveValue("fixture-model");
    await expect(page.getByRole("tabpanel", { name: "快速配置", exact: true }).getByLabel("API Key", { exact: true })).toHaveAttribute("type", "password");
    await expectNoHorizontalOverflow(page);
    await page.getByLabel("模型名称", { exact: true }).scrollIntoViewIfNeeded();
    await page.screenshot({ path: testInfo.outputPath(`models-quick-zh-${theme}.png`), fullPage: false });
  }
});

test("quick setup saves only edited values and preserves hidden credentials and advanced fields", async ({ page }) => {
  const original = { ...structuredClone(settingsDoc), apiKey: "••••••", authToken: "••••••", future: { keep: true }, imageGeneration: { enabled: false, worker: { maxAttempts: 7 } }, responses: { store: false } };
  let document = structuredClone(original);
  let writes = 0;
  let tests = 0;
  await page.route("**/runtime/settings", async route => {
    if (route.request().method() === "PUT") { document = route.request().postDataJSON(); writes++; return route.fulfill({ json: { saved: true, revision: "r2" } }); }
    return route.fulfill({ json: { doc: document, revision: writes ? "r2" : "r1", exists: true, masked: ["apiKey", "authToken"] } });
  });
  await page.route("**/runtime/settings/test-provider", async route => {
    tests++;
    expect(route.request().postDataJSON().doc.model).toBe("edited-model");
    return route.fulfill({ json: { ok: true, kind: "catalog_connection", message: "Fixture catalog reachable." } });
  });
  await page.goto("/webui/v2/settings/models");
  await page.getByLabel("Model name", { exact: true }).fill("edited-model");
  await page.getByRole("tab", { name: "Image generation", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Image generation", exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Quick setup", exact: true }).click();
  await expect(page.getByLabel("Model name", { exact: true })).toHaveValue("edited-model");
  expect(writes).toBe(0);
  expect(tests).toBe(0);
  await page.getByRole("button", { name: "Test connection", exact: true }).click();
  await expect(page.locator('.model-status')).toContainText("Model catalog reachable");
  await expect(page.getByRole("tabpanel", { name: "Quick setup", exact: true }).getByText("This test checks only the model catalog, not chat, tool or vision capabilities.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Saved and verified by readback." })).toBeVisible();
  expect(document).toEqual({ ...original, model: "edited-model" });
  expect(writes).toBe(1);
  await page.reload();
  await expect(page.getByLabel("Model name", { exact: true })).toHaveValue("edited-model");
});

test("capability links open the requested panel without granting screenshot routes or making requests", async ({ page }) => {
  let requests = 0;
  page.on("request", request => { if (request.method() !== "GET" && request.url().includes('/runtime/settings')) requests++; });
  await page.goto("/webui/v2/settings/models");
  await page.locator('.model-capability-list button').filter({ hasText: "Computer use" }).click();
  await expect(page.getByRole("tab", { name: "Computer use", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { name: "Computer Use desktop operations", exact: true })).toBeVisible();
  for (const route of await page.locator('.global-settings-computer-route input').all()) await expect(route).not.toBeChecked();
  await expect(page.getByRole("button", { name: "Save changes", exact: true })).toBeDisabled();
  expect(requests).toBe(0);
  await page.getByRole("tab", { name: "Quick setup", exact: true }).click();
  await page.locator('.model-capability-list button').filter({ hasText: "Image generation" }).click();
  await expect(page.getByRole("tab", { name: "Image generation", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("Worker maximum concurrency", { exact: true })).not.toBeVisible();
  await page.getByText("Image task worker", { exact: true }).click();
  await expect(page.getByLabel("Worker maximum concurrency", { exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page);
});

test("promotes a selected fallback model, saves it, and confirms the new primary route", async ({ page }, testInfo) => {
  await page.goto("/webui/v2/settings/models");
  await expect(page.getByRole("heading", { name: "Models", exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Advanced setup", exact: true }).click();
  await page.getByRole("button", { name: /glm-5\.2/ }).click();
  await expect(page.getByRole("button", { name: "Set as primary model", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Set as primary model", exact: true }).click();
  await expect(page.locator(".global-settings-provider-actions .global-settings-badge")).toBeVisible();
  await expect(page.locator(".global-settings-footer-status")).toContainText("Unsaved changes");
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "Saved and verified by readback." })).toBeVisible();
  await expect(page.locator(".global-settings-provider-list").getByText("glm-5.2", { exact: true }).first()).toBeVisible();
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("settings-v2-promote-primary.png"), fullPage: false });
});

test("connects a Feishu draft and operates it from the separate Worker runtime page", async ({ page }, testInfo) => {
  const worker = {
    id: 7,
    profile_key: "support-agent",
    account_key: "support-bot",
    supervisor: "screen",
    status: "running",
    worker: { supervisor: "screen", account_key: "support-bot", provider: "openai", model: "fixture-model", streaming: "on", reactions: "on" },
    observed_worker: { state: "running", pid: 2341, screen: "support-agent", observed_at: "2026-09-23T02:15:00Z" },
    checks: [{ name: "Feishu credentials", status: "passed", message: "Credential is available." }]
  };
  const otherWorker = { ...worker, id: 6, profile_key: "ops-agent", account_key: "ops-bot", worker: { ...worker.worker, account_key: "ops-bot" }, observed_worker: { ...worker.observed_worker, screen: "ops-agent" } };
  const records = [worker, otherWorker];
  await page.route("**/tenant/agent-provisionings/overview", (route) => route.fulfill({ json: { records, workers: records.map((record) => record.observed_worker) } }));
  await page.route("**/tenant/agent-profiles?limit=100", (route) => route.fulfill({ json: { data: [{ id: 3, profile_key: "support-agent", profile_version: 1, status: "published", scope: "tenant_shared", display_name: "Support agent", description: "Support", config_json: "{}" }] } }));
  await page.route("**/tenant/channel-accounts?limit=100", (route) => route.fulfill({ json: { data: [{ id: 4, account_key: "support-bot", provider: "feishu", app_id: "cli_fixture", mode: "websocket", enabled: true, status: "active" }] } }));
  await page.route("**/tenant/agent-provisionings", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const draft = { ...worker, id: 8, status: "draft", observed_worker: { state: "stopped", pid: 0, screen: "support-agent", observed_at: "2026-09-23T02:15:00Z" }, checks: [] };
    records.unshift(draft);
    return route.fulfill({ json: draft });
  });
  await page.route(/\/tenant\/agent-provisionings\/\d+\/(preflight|start|restart|stop|status)$/, async (route) => {
    const action = new URL(route.request().url()).pathname.split("/").at(-1)!;
    const id = Number(new URL(route.request().url()).pathname.split("/").at(-2));
    const record = records.find((item) => item.id === id);
    if (!record) return route.fulfill({ status: 404, json: { error: "not found" } });
    const state = action === "start" || action === "restart" ? "running" : action === "preflight" ? "preflight" : action === "stop" ? "stopped" : record.observed_worker.state;
    Object.assign(record, { status: state, observed_worker: { ...record.observed_worker, state } });
    return route.fulfill({ json: record });
  });

  await page.evaluate(() => localStorage.setItem("niuma-webui.language.v1", "zh"));
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/webui/v2/settings/feishu");
  const selectedNavItem = page.locator('.settings-navigation nav button[aria-current="page"]');
  await selectedNavItem.focus();
  await expect(selectedNavItem).toHaveCSS("outline-style", "none");
  await expect(selectedNavItem).toHaveCSS("box-shadow", "none");
  await expect(page.locator(".connection-mode button.is-selected")).toHaveCSS("border-top-color", "rgba(0, 0, 0, 0)");
  await expect(page.locator(".worker-settings-account")).toBeVisible();
  await expect(page.locator("h1")).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "飞书连接", exact: true })).toBeVisible();
  await expect(page.locator(".settings-page-heading")).toHaveCount(0);
  await expect(page.locator(".provisioning-hero, .provisioning-scope-note")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "保存连接草稿", exact: true })).toBeVisible();
  await expectNoHorizontalOverflow(page);

  const saveDesktopEvidence = testInfo.project.name === "chromium-webui-v2-desktop";
  const evidenceDir = resolve(process.cwd(), "../desktop-v2/build/validation/20260923");
  if (saveDesktopEvidence) {
    await mkdir(evidenceDir, { recursive: true });
    await page.screenshot({ path: resolve(evidenceDir, "feishu-connection.png"), fullPage: false });
  }
  await page.getByRole("button", { name: "保存连接草稿", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("连接草稿已保存");
  await page.getByRole("button", { name: "前往 Worker 运行", exact: true }).click();

  await expect(page.getByRole("heading", { name: "Worker 运行", exact: true })).toBeVisible();
  await expect(page.locator("h1")).toHaveCount(1);
  await expect(page.locator(".settings-page-heading")).toHaveCount(0);
  await expect(page.locator(".worker-selector-trigger")).toBeVisible();
  await expect(page.locator(".runtime-detail")).toBeVisible();
  await expect(page.getByText("support-agent", { exact: true }).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "运行预检", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "启动", exact: true })).toBeEnabled();
  await expect(page.getByRole("button", { name: "停止", exact: true })).toBeDisabled();
  await expectNoHorizontalOverflow(page);
  if (saveDesktopEvidence) await page.screenshot({ path: resolve(evidenceDir, "worker-runtime.png"), fullPage: false });

  await page.locator(".worker-selector-trigger").click();
  await page.getByRole("searchbox", { name: "筛选 Worker" }).fill("ops-agent");
  await expectNoHorizontalOverflow(page);
  if (saveDesktopEvidence) await page.screenshot({ path: resolve(evidenceDir, "worker-runtime-filter.png"), fullPage: false });
  await page.getByRole("option", { name: /ops-agent/ }).click();
  await expect(page.locator(".runtime-detail-head h2")).toHaveText("ops-agent");
  await expect(page.locator(".runtime-state-large")).toContainText("running");
  await expect(page.getByRole("button", { name: "停止", exact: true })).toBeEnabled();

  await page.locator(".worker-selector-trigger").click();
  await page.getByRole("searchbox", { name: "筛选 Worker" }).fill("support-agent");
  await page.getByRole("option", { name: /support-agent.*stopped/i }).click();
  await expect(page.locator(".runtime-detail-head h2")).toHaveText("support-agent");
  await expect(page.getByRole("button", { name: "停止", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "启动", exact: true }).click();
  await expect(page.locator(".runtime-state-large")).toContainText("running");
  await expect(page.getByRole("button", { name: "停止", exact: true })).toBeEnabled();
  await page.setViewportSize({ width: 1024, height: 700 });
  await expectNoHorizontalOverflow(page);
});

async function expectNoHorizontalOverflow(page: import("@playwright/test").Page): Promise<void> {
  const widths = await page.evaluate(() => ({ body: document.body.scrollWidth, document: document.documentElement.scrollWidth, viewport: window.innerWidth }));
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
}

async function chooseSettingsSelect(page: import("@playwright/test").Page, label: string, option: string): Promise<void> {
  await page.getByRole("button", { name: label, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

async function canvasColorRatio(canvas: import("@playwright/test").Locator): Promise<number> {
  return canvas.evaluate((node) => {
    const source = node as HTMLCanvasElement;
    const copy = document.createElement("canvas");
    copy.width = source.width;
    copy.height = source.height;
    const context = copy.getContext("2d")!;
    context.drawImage(source, 0, 0);
    const data = context.getImageData(0, 0, copy.width, copy.height).data;
    let colored = 0;
    for (let i = 3; i < data.length; i += 4) if (data[i] > 0) colored++;
    return colored / (copy.width * copy.height);
  });
}
