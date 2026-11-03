import { expect, type Page, test } from "@playwright/test";

const SESSION_REF = "tenant:composer-main-chat";
const SOURCE_REF = "tenant:composer-source";
const SESSION_PATH = `/webui/v2/sessions/${encodeURIComponent(SESSION_REF)}?token=test-token`;
const PNG_PIXEL = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aZ1sAAAAASUVORK5CYII=";

async function mockComposerSession(page: Page, includeImage = false) {
  const session = { id: 1001, ref: SESSION_REF, source: "tenant", title: "Composer review", cwd: "/workspace/project", status: "completed", model: "gpt-6-astra", provider: "openai", permission_mode: "ask", effort: "high", prompt_mode: "code", updated_at: "2026-09-06T00:00:00Z" };
  const source = { ...session, id: 1002, ref: SOURCE_REF, title: "Design context" };
  const events = [
    { id: 1, task_id: 11, event_type: "message", payload_json: JSON.stringify({ content: "Inspect the input controls." }), created_at: session.updated_at },
    { id: 2, task_id: 11, event_type: "completed", payload_json: JSON.stringify({ response: "The composer is ready for the next request." }), created_at: session.updated_at }
  ];
  if (includeImage) events.push({ id: 3, task_id: 11, event_type: "image_artifact", payload_json: JSON.stringify({ asset_id: "preview-image" }), created_at: session.updated_at });
  const conversation = { schema_version: "niuma.session-conversation.v1", session, events, cursor: String(events.length), has_more: false };
  await page.addInitScript(() => localStorage.setItem("niuma-webui.language.v1", "en"));
  await page.route((url) => /^(?:\/api)?\/(?:tenant|v1|agent|health)(?:\/|$)/.test(url.pathname), async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/conversations/stream")) return route.fulfill({ contentType: "text/event-stream", body: `event: conversation\ndata: ${JSON.stringify(conversation)}\n\n` });
    if (url.pathname.endsWith("/conversation")) return route.fulfill({ json: { data: conversation } });
    if (url.pathname.includes("/session-control/")) return route.fulfill({ json: { data: url.searchParams.get("source") === "local" ? [] : [session, source] } });
    if (url.pathname.endsWith("/v1/providers")) return route.fulfill({ json: { data: [{ name: "openai", model: "gpt-6-astra" }] } });
    if (url.pathname.endsWith("/v1/models")) return route.fulfill({ json: { data: [{ id: "gpt-6-astra" }, { id: "gpt-6-medium" }] } });
    if (url.pathname.endsWith("/health")) return route.fulfill({ json: { model: "gpt-6-astra", workspace: "/workspace/project" } });
    if (url.pathname.includes("/agent/slash-commands")) return route.fulfill({ json: { data: [{ name: "help", description: "Available commands", source: "builtin" }, { name: "history", description: "Conversation history", source: "builtin" }, { name: "新会话", description: "Create a focused new session", source: "skill" }] } });
    if (url.pathname.endsWith("/pending-input-settings")) return route.fulfill({ json: { enabled: false } });
    if (url.pathname.endsWith("/pending-inputs")) return route.fulfill({ json: { data: [] } });
    if (url.pathname.endsWith("/tenant/media/assets/preview-image")) {
      expect(route.request().headers().authorization).toBe("Bearer test-token");
      return route.fulfill({ contentType: "image/png", body: Buffer.from(PNG_PIXEL, "base64") });
    }
    if (url.pathname.includes("/web-agent/conversations/")) return route.fulfill({ json: { tasks: [{ id: 11, model: "gpt-6-astra", status: "completed" }], latest_task: { id: 11, model: "gpt-6-astra", status: "completed" }, events: [] } });
    return route.fulfill({ json: { data: [] } });
  });
  return { session, conversation, events };
}

test("a run started by another client owns queued message configuration", async ({ page }) => {
  const fixture = await mockComposerSession(page);
  let sent: Record<string, unknown> | undefined;
  await page.route("**/session-control/sessions/tenant/*/messages", async (route) => {
    sent = route.request().postDataJSON();
    await route.fulfill({ json: { data: { operation_id: "queued", session: fixture.session, run_id: 12 } } });
  });
  await page.goto(SESSION_PATH);
  await page.getByRole("button", { name: "Model", exact: true }).click();
  await page.getByRole("option", { name: "gpt-6-medium", exact: true }).click();
  await expect(page.getByRole("button", { name: "Model", exact: true })).toContainText("gpt-6-medium");
  Object.assign(fixture.session, { status: "running", active_run_id: 12 });
  fixture.events.push({ id: 3, task_id: 12, event_type: "started", payload_json: JSON.stringify({ model: "gpt-6-astra", provider: "openai" }), created_at: fixture.session.updated_at });
  fixture.conversation.cursor = "3";
  await expect(page.getByRole("button", { name: "Model", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Model", exact: true })).toContainText("gpt-6-astra");
  const input = page.getByRole("textbox", { name: "Message", exact: true });
  await input.fill("Append to the active run");
  await input.press("Enter");
  await expect.poll(() => sent).toMatchObject({ content: "Append to the active run", model: "gpt-6-astra", provider: "openai" });
});

test("composer keeps a single focus shell and floats command menus on desktop and mobile", async ({ page }, testInfo) => {
  await mockComposerSession(page);
  await page.goto(SESSION_PATH);
  const textarea = page.getByRole("textbox", { name: "Message", exact: true });
  const shell = page.locator(".webui2-composer-shell");
  await expect(textarea).toBeVisible();
  await expect(page.locator(".webui2-pending-queue")).toHaveCount(0);
  await expect(page.getByLabel("Add session context")).toHaveCount(0);
  await textarea.focus();
  const focused = await textarea.evaluate((element) => ({ shadow: getComputedStyle(element).boxShadow, outline: getComputedStyle(element).outlineStyle, border: getComputedStyle(element).borderWidth }));
  expect(focused).toEqual({ shadow: "none", outline: "none", border: "0px" });
  const shellBox = await shell.boundingBox();
  expect(shellBox).not.toBeNull();
  for (const button of await shell.locator("button:visible").all()) {
    const box = await button.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(shellBox!.x);
    expect(box!.x + box!.width).toBeLessThanOrEqual(shellBox!.x + shellBox!.width + 1);
    expect(box!.y + box!.height).toBeLessThanOrEqual(shellBox!.y + shellBox!.height + 1);
  }
  await page.screenshot({ path: testInfo.outputPath("composer-focused.png") });
  await textarea.fill("/h");
  const commands = page.getByRole("listbox", { name: "Slash commands" });
  await expect(commands).toBeVisible();
  const commandBox = await commands.boundingBox();
  const slashShellBox = await shell.boundingBox();
  expect(commandBox!.y + commandBox!.height).toBeLessThan(slashShellBox!.y);
  expect(Math.abs(slashShellBox!.height - shellBox!.height)).toBeLessThanOrEqual(1);
  await textarea.press("ArrowDown");
  await expect(page.getByRole("option", { selected: true })).toContainText("/history");
  await page.screenshot({ path: testInfo.outputPath("composer-slash.png") });
  await textarea.press("Tab");
  await expect(textarea).toHaveValue("/history ");
  await page.getByRole("button", { name: "Add attachments and options" }).click();
  await expect(page.getByRole("menuitem", { name: "Attach images" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Resume queue" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  expect(overflow).toBe(false);
  await page.screenshot({ path: testInfo.outputPath("composer-menu.png") });
});

test("skill picker uses the same slash invocation as manual Skill selection", async ({ page }, testInfo) => {
  const fixture = await mockComposerSession(page);
  let sent: Record<string, unknown> | undefined;
  await page.route("**/session-control/sessions/tenant/*/messages", async (route) => {
    sent = route.request().postDataJSON();
    await route.fulfill({ json: { data: { operation_id: "skill-operation", session: fixture.session, run_id: 14 } } });
  });
  await page.goto(SESSION_PATH);

  await page.getByRole("button", { name: "Select Skill", exact: true }).click();
  const menu = page.getByRole("listbox", { name: "Available Skills", exact: true });
  await expect(menu).toBeVisible();
  await expect(menu).toContainText("新会话");
  await expect(menu).not.toContainText("help");
  await page.screenshot({ path: testInfo.outputPath("composer-skill-picker.png") });
  await menu.getByRole("option", { name: /新会话/ }).click();

  const input = page.getByRole("textbox", { name: "Message", exact: true });
  await input.fill("请帮我创建一个新的会话");
  await input.press("Enter");
  await expect.poll(() => sent).toMatchObject({ content: "/新会话\n\n请帮我创建一个新的会话" });
});

test("composer refresh restores source drafts while browser files stay transient", async ({ page }) => {
  await mockComposerSession(page);
  await page.goto(SESSION_PATH);
  const textarea = page.getByRole("textbox", { name: "Message", exact: true });
  await textarea.fill("Use the design context after refresh.");
  await textarea.evaluate((node, ref) => {
    const dataTransfer = new DataTransfer();
    dataTransfer.setData("application/x-niuma-session-ref", ref);
    node.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer }));
  }, SOURCE_REF);
  await page.locator('input[type="file"]').setInputFiles({ name: "draft-image.png", mimeType: "image/png", buffer: Buffer.from(PNG_PIXEL, "base64") });
  await expect(page.locator(".webui2-context-chip")).toContainText("Design context");
  await expect(page.locator(".webui2-file-chip img")).toBeVisible();
  await expect.poll(() => page.locator(".webui2-file-chip img").evaluate((node) => (node as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
  await page.reload();
  await expect(textarea).toHaveValue("Use the design context after refresh.");
  await expect(page.locator(".webui2-context-chip")).toHaveCount(1);
  await expect(page.locator(".webui2-file-chip")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Send message" })).toBeEnabled();
});

test("generated image modal contains focus and returns it after Escape or backdrop dismissal", async ({ page }) => {
  await mockComposerSession(page, true);
  await page.goto(SESSION_PATH);
  const trigger = page.getByRole("button", { name: "Open generated asset", exact: true });
  await trigger.click();
  const preview = page.getByRole("dialog", { name: "Generated asset preview", exact: true });
  await expect(preview).toBeVisible();
  await expect(preview.getByRole("button", { name: "Close preview", exact: true })).toBeFocused();
  await expect.poll(() => preview.locator("img").evaluate((node) => (node as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
  await page.keyboard.press("Shift+Tab");
  await expect(preview.getByRole("link", { name: "Download generated asset", exact: true })).toBeFocused();
  await trigger.evaluate((node) => node.focus());
  await expect(preview.getByRole("link", { name: "Download generated asset", exact: true })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(preview).toHaveCount(0);
  await expect(trigger).toBeFocused();
  await trigger.click();
  await preview.getByRole("button", { name: "Close image preview", exact: true }).click({ position: { x: 5, y: 5 } });
  await expect(preview).toHaveCount(0);
  await expect(trigger).toBeFocused();
});
