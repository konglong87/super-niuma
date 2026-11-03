import { expect, type Locator, type Page, test } from "@playwright/test";
import { dropSessionContext, installWebUIV2Sessions } from "./fixtures/webuiV2Sessions";

const v2IndexPath = "/webui/v2";
const managedRef = "tenant:alpha";
const completedRef = "tenant:beta";
const localRef = "local:workspace";

async function openSettingsNavigation(page: Page): Promise<void> {
  const launcher = page.getByRole("button", { name: "Open settings navigation", exact: true });
  if (await launcher.isVisible()) await launcher.click();
}

async function serveWindowsDesktopAssets(page: Page, baseURL: string | undefined): Promise<void> {
  await page.route((url) => url.hostname === "wails.localhost" && /^\/(?:webui\/|src\/|@|node_modules\/)/.test(url.pathname), async (route) => {
    const url = new URL(route.request().url());
    return route.fulfill({ response: await route.fetch({ url: new URL(url.pathname + url.search, baseURL).href }) });
  });
}

test("Windows desktop origin waits for late tokens and rechecks restarted credentials", async ({ page, baseURL }, testInfo) => {
  test.skip(process.env.VITE_DESKTOP_UI_VERSION !== "2", "Run with VITE_DESKTOP_UI_VERSION=2 to exercise the desktop-v2 build contract.");
  await serveWindowsDesktopAssets(page, baseURL);
  await page.addInitScript(() => {
    localStorage.setItem("niuma.desktop.onboarding.v1", "done");
    localStorage.setItem("niuma.desktop.identity.v2", JSON.stringify({ apiBase: "http://stale.invalid", apiToken: "stale-process", mobileJwt: "stale-jwt" }));
  });
  await page.route("**/runtime/settings/environments", (route) => route.fulfill({ json: { environments: [
    { id: "current", label: "Desktop", database: "desktop", tenant_key: "web", user_id: "operator", api_path: "", available: true }
  ] } }));
  const probes: string[] = [];
  let finishRestart!: () => void;
  const restart = new Promise<void>((resolve) => { finishRestart = resolve; });
  await page.route(/\/(?:health|readyz)$/, async (route) => {
    const token = route.request().headers().authorization;
    probes.push(token);
    if (token === "Bearer restarted-process") await restart;
    return route.fulfill({ json: { ok: true } });
  });
  await page.goto("http://wails.localhost/webui/v2/settings/general");
  await expect(page.locator(".webui2-desktop-readiness")).toBeVisible();
  expect(probes).toEqual([]);
  for (const token of ["late-process", "restarted-process"]) {
    await page.evaluate((value) => {
      (window as Window & { __NIUMA_DESKTOP_TOKEN__?: string }).__NIUMA_DESKTOP_TOKEN__ = value;
      dispatchEvent(new Event("niuma-desktop-token"));
    }, token);
    if (token === "restarted-process") {
      await expect(page.locator(".webui2-desktop-readiness")).toBeVisible();
      await expect(page.locator(".webui2-settings-center")).toHaveCount(0);
      finishRestart();
    }
    await expect(page.getByRole("heading", { name: "General", exact: true })).toBeVisible();
    await expect(page.locator(".webui2-desktop-readiness")).toHaveCount(0);
    expect(probes.filter((value) => value === `Bearer ${token}`).length).toBeGreaterThanOrEqual(2);
  }
  expect(probes).not.toContain("Bearer stale-process");
  await page.screenshot({ path: testInfo.outputPath("desktop-v2-token-ready.png") });
});

test("legacy desktop keeps its fixed token without injection or v2 readiness", async ({ page, baseURL }, testInfo) => {
  test.skip(process.env.VITE_DESKTOP_UI_VERSION === "2", "Run without the v2 build flag to exercise legacy desktop.");
  await serveWindowsDesktopAssets(page, baseURL);
  const tokens: string[] = [];
  const probes: string[] = [];
  await page.route("**/runtime/settings/environments", (route) => {
    tokens.push(route.request().headers().authorization);
    return route.fulfill({ json: { environments: [
      { id: "current", label: "Desktop", database: "desktop", tenant_key: "web", user_id: "operator", api_path: "", available: true }
    ] } });
  });
  page.on("request", (request) => {
    if (/\/(?:health|readyz)$/.test(request.url())) probes.push(request.url());
  });
  await page.goto("http://wails.localhost/webui/v2/settings/general");
  await expect(page.getByRole("heading", { name: "General", exact: true })).toBeVisible();
  await expect.poll(() => tokens.length).toBeGreaterThan(0);
  expect(tokens.every((token) => token === "Bearer test-token")).toBe(true);
  expect(await page.evaluate(() => (window as Window & { __NIUMA_DESKTOP_TOKEN__?: string }).__NIUMA_DESKTOP_TOKEN__)).toBeUndefined();
  tokens.length = 0;
  await page.reload();
  await expect(page.getByRole("heading", { name: "General", exact: true })).toBeVisible();
  await expect(page.locator(".webui2-desktop-readiness")).toHaveCount(0);
  await expect.poll(() => tokens.length).toBeGreaterThan(0);
  expect(tokens.every((token) => token === "Bearer test-token")).toBe(true);
  expect(probes).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("legacy-desktop-token-ready.png") });
});

test("isolated settings block unsupported deep links and keep supported navigation", async ({ page }, testInfo) => {
  await page.route("**/runtime/settings/environments", (route) => route.fulfill({ json: { environments: [
    { id: "current", label: "Web", database: "web_db", tenant_key: "web", user_id: "operator", api_path: "", available: true },
    { id: "channel", label: "Channel", database: "channel_db", tenant_key: "target", user_id: "worker", api_path: "/runtime/settings/environments/channel", available: true }
  ] } }));
  const isolatedRequests: string[] = [];
  await page.route("**/runtime/settings/environments/channel/**", (route) => {
    isolatedRequests.push(new URL(route.request().url()).pathname);
    return route.fulfill({ json: { data: [] } });
  });
  await page.goto("/webui/v2/settings/general");
  await openSettingsNavigation(page);
  await page.getByRole("combobox", { name: "Settings environment" }).selectOption("channel");
  await openSettingsNavigation(page);
  const nav = page.getByRole("navigation", { name: "Settings categories" });
  for (const label of ["Common prompts", "Memory", "Skills", "Teams", "Channel workers"]) {
    await expect(nav.getByRole("button", { name: label, exact: true })).toBeDisabled();
  }
  for (const label of ["General", "Agents", "Profiles", "Models", "Settings JSON", "Effective configuration"]) {
    await expect(nav.getByRole("button", { name: label, exact: true })).toBeEnabled();
  }
  for (const section of ["prompts", "memory", "skills", "teams", "provisioning"]) {
    await page.evaluate((value) => {
      history.pushState({}, "", `/webui/v2/settings/${value}`);
      dispatchEvent(new PopStateEvent("popstate"));
    }, section);
    await expect(page.getByRole("alert")).toContainText("This section is unavailable");
  }
  expect(isolatedRequests).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath("isolated-settings.png") });
  await openSettingsNavigation(page);
  await nav.getByRole("button", { name: "Profiles", exact: true }).click();
  await expect.poll(() => isolatedRequests.some((url) => url.endsWith("/tenant/agent-profiles"))).toBe(true);
});

test("Memory and Skills keep independent drafts through pending save completion", async ({ page }, testInfo) => {
  let finishSave!: () => void;
  const pendingSave = new Promise<void>((resolve) => { finishSave = resolve; });
  let posted: unknown;
  await page.route("**/tenant/memories*", async (route) => {
    if (route.request().method() === "POST") {
      posted = route.request().postDataJSON();
      await pendingSave;
    }
    return route.fulfill({ json: { data: [] } });
  });
  await page.goto("/webui/v2/settings/memory");
  const memory = page.locator(".p2-management-panel").filter({ hasText: "Persistent memory" });
  const skills = page.locator(".p2-management-panel").filter({ hasText: "Available skills" });
  await memory.getByRole("textbox", { name: "Content", exact: true }).fill("Memory draft only");
  const nav = page.getByRole("navigation", { name: "Settings categories" });
  await openSettingsNavigation(page);
  await nav.getByRole("button", { name: "Skills", exact: true }).click();
  await expect(skills.getByRole("textbox", { name: "Markdown" })).toHaveValue("");
  await skills.getByRole("textbox", { name: "Markdown" }).fill("# Skill draft only");
  await openSettingsNavigation(page);
  await nav.getByRole("button", { name: "Memory", exact: true }).click();
  await expect(memory.getByRole("textbox", { name: "Content", exact: true })).toHaveValue("Memory draft only");
  await memory.getByRole("button", { name: "Save", exact: true }).click();
  await expect.poll(() => posted).toMatchObject({ content: "Memory draft only" });
  await openSettingsNavigation(page);
  await nav.getByRole("button", { name: "Skills", exact: true }).click();
  finishSave();
  await expect(memory.getByRole("textbox", { name: "Content", exact: true, includeHidden: true })).toHaveValue("");
  await expect(skills.getByRole("textbox", { name: "Markdown" })).toHaveValue("# Skill draft only");
  await expect(skills.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath("skills-draft-isolation.png") });
});

test("prompt picker keeps compact controls and supports CRUD without sending", async ({ page }, testInfo) => {
  type Prompt = { id: number; title: string; content: string; category: string; pinned: boolean; sort_order: number };
  let prompts: Prompt[] = [];
  let sends = 0;
  page.on("request", (request) => { if (request.method() === "POST" && request.url().includes("/messages")) sends++; });
  await page.route("**/tenant/prompt-templates**", async (route) => {
    const request = route.request();
    if (request.method() === "DELETE") prompts = [];
    else if (request.method() === "POST" || request.method() === "PATCH") {
      prompts = [{ ...request.postDataJSON(), id: 1 }];
      return route.fulfill({ json: prompts[0] });
    }
    return route.fulfill({ json: prompts });
  });
  await page.goto(sessionPath(completedRef));
  const message = page.getByRole("textbox", { name: "Message", exact: true });
  await message.fill("Existing draft");
  const launcher = page.getByRole("button", { name: "Common prompts", exact: true });
  await expect(launcher).toHaveText("");
  expect((await launcher.boundingBox())?.width).toBe(32);
  await launcher.click();
  let dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("status")).toContainText("No common prompts");
  await expect(dialog.getByRole("searchbox")).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath("prompt-picker-empty.png") });
  await dialog.getByRole("button", { name: "Add prompt", exact: true }).click();
  await expect(dialog.getByLabel("Title", { exact: true })).toBeFocused();
  await expect(dialog.getByRole("searchbox")).toHaveCount(0);
  await dialog.getByLabel("Title", { exact: true }).fill("Feature implementation");
  await dialog.getByLabel("Prompt content", { exact: true }).fill("Implement {{feature}} for {{requirements}}.\n1. Add regression tests.\n2. Keep existing behavior.");
  await dialog.getByLabel("Category", { exact: true }).fill("Development");
  await dialog.getByLabel("Sort order", { exact: true }).fill("3");
  const pin = dialog.getByRole("checkbox", { name: "Pin" });
  await pin.check();
  const pinBox = await pin.boundingBox();
  expect(pinBox?.width).toBe(16);
  expect(pinBox?.height).toBe(16);
  const contentBox = await dialog.getByLabel("Prompt content").boundingBox();
  expect(contentBox?.height).toBeGreaterThanOrEqual(150);
  const footer = dialog.locator(".prompt-picker-footer");
  await expect(footer).toBeInViewport();
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("prompt-picker-editor.png") });
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  const select = dialog.getByRole("button", { name: "Use Feature implementation" });
  await expect(select).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Pin", exact: true })).toHaveAttribute("aria-pressed", "true");
  await page.screenshot({ path: testInfo.outputPath("prompt-picker-list.png") });
  await dialog.getByRole("searchbox").fill("Feature");
  await expect(select).toBeVisible();
  await select.click();
  await expect(dialog).toHaveCount(0);
  await expect(message).toHaveValue(`Existing draft\n\n${prompts[0].content}`);
  expect(sends).toBe(0);
  await launcher.click();
  dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Pin", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "Pin", exact: true })).toHaveAttribute("aria-pressed", "false");
  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  await dialog.getByLabel("Title", { exact: true }).fill("Updated feature");
  await dialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "Use Updated feature" })).toBeVisible();
  await dialog.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
  await page.screenshot({ path: testInfo.outputPath("prompt-picker-delete.png") });
  await dialog.getByRole("button", { name: "Delete prompt", exact: true }).click();
  await expect(dialog.getByRole("status")).toContainText("No common prompts");
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(launcher).toBeFocused();
  expect(sends).toBe(0);
  await page.getByRole("button", { name: "Add attachments and options" }).click();
  const chooserPromise = page.waitForEvent("filechooser");
  await page.getByRole("menuitem", { name: "Attach images", exact: true }).click();
  const chooser = await chooserPromise;
  expect(chooser.isMultiple()).toBe(true);
});

test("prompt picker bounds long Chinese content and retains theme with short viewports", async ({ page }, testInfo) => {
  await page.addInitScript(() => {
    localStorage.setItem("niuma-webui.language.v1", "zh");
    localStorage.setItem("niuma-webui.v2.theme.v1", "dark");
  });
  await page.route("**/tenant/prompt-templates**", (route) => route.fulfill({ json: [
    { id: 1, title: "功能开发需求", content: "根据 {{需求}} 完成 {{功能}}，要求：\n1. 遵守项目架构\n2. 补充单元测试\n3. 保持原有行为", category: "开发", pinned: true, sort_order: 0 },
    { id: 2, title: "检查较长标题在移动端的显示效果与换行行为".repeat(3), content: "x".repeat(400), category: "very-long-category".repeat(5), pinned: false, sort_order: 1 },
    { id: 3, title: "代码审查", content: "检查潜在回归、权限边界和测试缺口。", category: "审查", pinned: false, sort_order: 2 }
  ] }));
  await page.goto(sessionPath(completedRef));
  await page.getByRole("button", { name: "常用提示词", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "使用 功能开发需求" })).toBeVisible();
  expect(await dialog.evaluate((element) => getComputedStyle(element).backgroundColor)).toBe("rgb(32, 36, 42)");
  await expectNoHorizontalOverflow(page);
  await expect(dialog).toBeInViewport();
  const overflow = await dialog.evaluate((element) => element.scrollWidth > element.clientWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("prompt-picker-chinese-dark.png") });
  await dialog.getByRole("button", { name: "编辑", exact: true }).first().click();
  await page.setViewportSize({ width: 375, height: 480 });
  await expect(dialog.locator(".prompt-picker-footer")).toBeInViewport();
  await expect(dialog.getByRole("button", { name: "保存", exact: true })).toBeInViewport();
  await expectNoHorizontalOverflow(page);
  await page.screenshot({ path: testInfo.outputPath("prompt-picker-short-viewport.png") });
  await page.keyboard.press("Escape");
  await expect(dialog.getByRole("searchbox")).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  await expect(dialog.getByRole("button", { name: "关闭", exact: true })).toBeFocused();
  await page.keyboard.press("Shift+Tab");
  expect(await dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
});

test.beforeEach(async ({ page }) => { await installWebUIV2Sessions(page); });

function sessionPath(ref: string): string {
  return `${v2IndexPath}/sessions/${encodeURIComponent(ref)}`;
}

async function expectNoHorizontalOverflow(page: Page): Promise<void> {
  const widths = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth
  }));
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
}

async function expectVisibleButtonsWithinViewport(page: Page): Promise<void> {
  const buttons = page.locator("button:visible");
  const count = await buttons.count();
  const viewport = page.viewportSize();
  expect(viewport).not.toBeNull();
  if (!viewport) return;

  for (let index = 0; index < count; index += 1) {
    const box = await buttons.nth(index).boundingBox();
    expect(box, `visible button ${index} has no bounding box`).not.toBeNull();
    if (!box) continue;
    expect(box.x, `visible button ${index} starts outside the viewport`).toBeGreaterThanOrEqual(0);
    expect(box.y, `visible button ${index} starts above the viewport`).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, `visible button ${index} overflows horizontally`).toBeLessThanOrEqual(viewport.width);
    expect(box.y + box.height, `visible button ${index} overflows vertically`).toBeLessThanOrEqual(viewport.height);
  }
}

async function openInspector(page: Page): Promise<Locator> {
  await page.getByRole("button", { name: "Open Inspector" }).click();
  const inspector = page.locator(".webui2-inspector");
  await expect(inspector).toBeVisible();
  return inspector;
}

async function closeInspector(page: Page): Promise<void> {
  const inspector = page.locator(".webui2-inspector");
  await inspector.getByRole("button", { name: /Close Inspector|关闭检查器/ }).click();
  await expect(inspector).toHaveCount(0);
}

async function selectSession(page: Page, title: string): Promise<void> {
  const launcher = page.getByRole("button", { name: "Open sessions" });
  if (await launcher.isVisible()) await launcher.click();
  await page.locator(".webui2-session-select").filter({ hasText: title }).click();
}

async function toggleThemeThroughSettings(page: Page, theme: "light" | "dark", language: "en" | "zh" = "en"): Promise<void> {
  const mobileLauncher = page.getByRole("button", { name: language === "zh" ? "打开会话列表" : "Open sessions" });
  if (await mobileLauncher.isVisible()) await mobileLauncher.click();
  await page.getByRole("button", { name: language === "zh" ? "设置" : "Settings", exact: true }).click();
  const settings = page.getByRole("region", { name: language === "zh" ? "设置中心" : "Settings center" });
  await expect(settings.getByRole("heading", { name: language === "zh" ? "通用设置" : "General", exact: true })).toBeVisible();
  await settings.locator(".settings-theme-options button").filter({ hasText: theme === "dark" ? (language === "zh" ? "深色" : "Dark") : (language === "zh" ? "浅色" : "Light") }).click();
  await expect(page.locator(".webui2-page")).toHaveAttribute("data-theme", theme);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem("niuma-webui.v2.theme.v1"))).toBe(theme);
  const navigation = settings.getByRole("button", { name: language === "zh" ? "打开设置导航" : "Open settings navigation", exact: true });
  if (await navigation.isVisible()) await navigation.click();
  await settings.getByRole("button", { name: language === "zh" ? "返回会话" : "Back to chat", exact: true }).click();
}

async function expectRichFixturePresentation(page: Page): Promise<void> {
  const user = page.locator(".webui2-conversation-message--user");
  const assistant = page.locator(".webui2-conversation-message--assistant");
  await expect(user).toContainText("Please review the navigation states.");
  await expect(assistant).toContainText("The responsive review is complete.");
  await expect(user.locator(".webui2-user-surface")).toHaveCount(1);
  await expect(assistant.locator(".webui2-user-surface")).toHaveCount(0);
  const [userBackground, assistantBackground] = await Promise.all([
    user.locator(".webui2-user-surface").evaluate((element) => getComputedStyle(element).backgroundColor),
    assistant.evaluate((element) => getComputedStyle(element).backgroundColor)
  ]);
  expect(userBackground).not.toBe(assistantBackground);

  await expect(page.locator(".webui2-thinking-message")).toHaveCount(1);
  await expect(page.locator(".webui2-tool-message")).toHaveCount(1);
  await expect(page.locator(".webui2-handoff-record")).toHaveCount(1);
  await expect(page.locator(".webui2-folded-record[open]")).toHaveCount(0);
  await expect(page.locator(".webui2-folded-message .agent-markdown")).toHaveCount(0);
}

async function expectExpandedFoldDoesNotCoverComposer(page: Page): Promise<void> {
  const folded = page.locator(".webui2-folded-record").first();
  await expect(folded).toBeVisible();
  const summary = folded.locator("summary");
  await summary.click();
  await expect(folded).toHaveAttribute("open", "");
  const composer = page.locator(".webui2-composer");
  const [foldedBox, composerBox] = await Promise.all([folded.boundingBox(), composer.boundingBox()]);
  expect(foldedBox).not.toBeNull();
  expect(composerBox).not.toBeNull();
  if (foldedBox && composerBox) expect(foldedBox.y + foldedBox.height).toBeLessThanOrEqual(composerBox.y);
  await summary.click();
  await expect(folded).not.toHaveAttribute("open");
}

test.describe("WebUI v2 desktop layout", () => {
  test("keeps the session workspace bounded at desktop and narrow-desktop widths", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name === "chromium-webui-v2-mobile", "desktop layout is covered by desktop and narrow projects");
    await page.goto(sessionPath(managedRef));
    const workspace = page.getByRole("main", { name: "Session workspace" });
    await expect(workspace).toHaveAttribute("data-session-ref", managedRef);
    await expect(page.getByRole("toolbar")).toHaveCount(0);
    await expect(page.getByRole("complementary", { name: "Sessions", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Settings" })).toBeVisible();
    await expect(page.locator(".webui2-inspector")).toHaveCount(0);

    await expect(page.getByRole("status").filter({ hasText: "Waiting for a response" })).toBeVisible();
    let inspector = await openInspector(page);
    await expect(inspector).not.toHaveAttribute("role", "dialog");
    await closeInspector(page);

    await selectSession(page, "Design review");
    await expectRichFixturePresentation(page);
    await page.screenshot({ path: testInfo.outputPath("webui-v2-populated-light.png"), fullPage: false });
    await expectExpandedFoldDoesNotCoverComposer(page);
    await toggleThemeThroughSettings(page, "dark");
    await expectRichFixturePresentation(page);
    inspector = await openInspector(page);
    await expect(inspector).not.toHaveAttribute("role", "dialog");
    await page.screenshot({ path: testInfo.outputPath("webui-v2-populated-dark-inspector.png"), fullPage: false });

    await closeInspector(page);
    await selectSession(page, "Release coordination");
    await expect(page.getByRole("status").filter({ hasText: "Waiting for a response" })).toBeVisible();
    inspector = await openInspector(page);
    await expect(inspector).not.toHaveAttribute("role", "dialog");
    await expectNoHorizontalOverflow(page);
    await expectVisibleButtonsWithinViewport(page);
    await page.screenshot({ path: testInfo.outputPath("webui-v2-desktop-empty-dark-inspector.png"), fullPage: false });
  });
});

test.describe("WebUI v2 mobile and preference flows", () => {
  test.use({ viewport: { width: 393, height: 851 } });

  test("keeps Local sessions read-only and uses an Inspector dialog on mobile", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "chromium-webui-v2-mobile", "mobile interaction is covered by the Pixel 5 project");
    await page.goto(sessionPath(localRef));
    const workspace = page.getByRole("main", { name: "Session workspace" });
    await expect(workspace).toHaveAttribute("data-session-ref", localRef);
    await expect(page.getByRole("textbox", { name: "Message", exact: true })).toBeDisabled();
    await expect(page.getByLabel("Add session context")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Add attachments and options" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Send message" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Stop session" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Archive session" })).toHaveCount(0);

    const inspector = await openInspector(page);
    await expect(inspector).toHaveAttribute("role", "dialog");
    await expect(inspector).toHaveAttribute("aria-modal", "true");
    await expect(page.locator(".webui2-inspector-backdrop")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath("webui-v2-mobile-local-light.png"), fullPage: false });
    await inspector.getByRole("button", { name: "Close Inspector" }).click();
    await expect(inspector).toHaveCount(0);
    await expectNoHorizontalOverflow(page);
  });

  test("persists Chinese and scoped dark mode across v2 loads", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "chromium-webui-v2-mobile", "mobile interaction is covered by the Pixel 5 project");
    await page.addInitScript(() => {
      window.localStorage.setItem("niuma-webui.language.v1", "zh");
    });
    await page.goto(sessionPath(completedRef));
    await expect(page.getByRole("main", { name: "会话工作区" })).toBeVisible();
    await expect(page.getByRole("button", { name: "打开会话列表" })).toBeVisible();
    await expect(page.locator(".webui2-page")).toHaveAttribute("data-theme", "light");
    await expectRichFixturePresentation(page);
    await toggleThemeThroughSettings(page, "dark", "zh");
    await page.getByRole("button", { name: "打开检查器" }).click();
    await expect(page.locator(".webui2-inspector")).toHaveAttribute("role", "dialog");
    await expectNoHorizontalOverflow(page);
    await page.screenshot({ path: testInfo.outputPath("webui-v2-mobile-completed-dark-zh.png"), fullPage: false });
  });

  test("adds dragged session context to an idle draft and removes or sends it with keyboard controls", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "chromium-webui-v2-mobile", "keyboard context interaction is covered by the Pixel 5 project");
    await page.goto(sessionPath(completedRef));
    await dropSessionContext(page, localRef);

    const chips = page.getByRole("list", { name: "Session context", exact: true });
    await expect(chips.getByRole("listitem")).toHaveCount(1);
    const remove = chips.getByRole("button", { name: /Remove / });
    await remove.focus();
    await page.keyboard.press("Enter");
    await expect(chips).toHaveCount(0);

    await dropSessionContext(page, localRef);
    await expect(chips.getByRole("listitem")).toHaveCount(1);
    await page.getByRole("textbox", { name: "Message", exact: true }).fill("Review the keyboard-attached context.");
    const request = page.waitForRequest((item) => item.method() === "POST" && item.url().endsWith("/tenant/beta/messages"));
    const send = page.getByRole("button", { name: "Send message" });
    await send.focus();
    await page.keyboard.press("Enter");

    expect((await request).postDataJSON()).toMatchObject({ content: "Review the keyboard-attached context.", source_refs: [localRef] });
    await expect(chips).toHaveCount(0);
    await expect(page.locator(".webui2-handoff-record")).toHaveCount(2);
    await expect(page.locator(".webui2-handoff-record").last()).not.toHaveAttribute("open");
  });

  test("keeps a source draft during a running session until it is idle and manually submitted", async ({ page }) => {
    await page.goto(sessionPath(managedRef));
    await dropSessionContext(page, completedRef);
    const textarea = page.getByRole("textbox", { name: "Message", exact: true });
    await textarea.fill("Keep this context until the run stops.");
    const sends: object[] = [];
    page.on("request", (request) => { if (request.method() === "POST" && request.url().endsWith("/tenant/alpha/messages")) sends.push(request.postDataJSON()); });
    await expect(page.getByRole("button", { name: "Send message" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Stop session", exact: true })).toBeEnabled();
    await textarea.press("Enter");
    await expect(textarea).toHaveValue("Keep this context until the run stops.");
    expect(sends).toHaveLength(0);
    await page.getByRole("button", { name: "Stop session", exact: true }).click();
    await expect(page.getByRole("button", { name: "Send message" })).toBeEnabled();
    expect(sends).toHaveLength(0);
    await textarea.press("Enter");
    await expect(textarea).toHaveValue("");
    expect(sends).toHaveLength(1);
    expect(sends[0]).toMatchObject({ source_refs: [completedRef] });
  });
});

test.describe("WebUI v2 navigation and legacy routes", () => {
  test("keeps deep links, sidebar selection, and history synchronized", async ({ page }) => {
    await page.goto(v2IndexPath);
    await expect(page.getByRole("main", { name: "Session workspace" })).not.toHaveAttribute("data-session-ref");

    await page.goto(sessionPath(localRef));
    await page.reload();
    await expect(page.getByRole("main", { name: "Session workspace" })).toHaveAttribute("data-session-ref", localRef);
    await expect(page.locator(".webui2-session-select[aria-pressed=\"true\"]")).toContainText("Local workspace");

    await selectSession(page, "Design review");
    await expect(page).toHaveURL(sessionPath(completedRef));
    await expect(page.locator(".webui2-session-select[aria-pressed=\"true\"]")).toContainText("Design review");
    await page.goBack();
    await expect(page).toHaveURL(sessionPath(localRef));
    await expect(page.locator(".webui2-session-select[aria-pressed=\"true\"]")).toContainText("Local workspace");
    await page.goForward();
    await expect(page).toHaveURL(sessionPath(completedRef));
  });

  test("retains legacy WebUI and Web Agent landmarks", async ({ page }) => {
    await page.goto("/webui/");
    await expect(page.locator(".dashboard-shell")).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Primary navigation" })).toBeVisible();
    await page.goto("/webui/agent");
    await expect(page.getByLabel("Web Agent workspaces and sessions")).toHaveCount(1);
  });
});
