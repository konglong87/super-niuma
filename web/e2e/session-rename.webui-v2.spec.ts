import { expect, test, type Page } from "@playwright/test";
import { installWebUIV2Sessions } from "./fixtures/webuiV2Sessions";

const SESSION_ACTIONS = "Actions for tenant:beta";

async function showSidebar(page: Page) {
  const opener = page.getByRole("button", { name: "Open sessions", exact: true });
  if (await opener.isVisible()) await opener.click();
}

test.beforeEach(async ({ page }) => {
  await installWebUIV2Sessions(page);
  await page.goto("/webui/v2/sessions/tenant%3Abeta?token=test-token");
  await showSidebar(page);
});

test("both menus rename the same session, retain row dimensions and persist across reload", async ({ page }, testInfo) => {
  const row = page.locator(".webui2-session-row").filter({ has: page.getByRole("button", { name: SESSION_ACTIONS, exact: true }) });
  const originalHeight = (await row.boundingBox())!.height;
  await row.getByRole("button", { name: SESSION_ACTIONS, exact: true }).click();
  await expect(page.getByRole("menuitem", { name: "Rename session" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("rename-overflow.png") });
  await page.getByRole("menuitem", { name: "Rename session" }).click();
  let editor = page.getByRole("textbox", { name: "Rename session", exact: true });
  await expect(editor).toBeFocused();
  await editor.fill("  Release plan  ");
  await page.screenshot({ path: testInfo.outputPath("rename-inline.png") });
  await editor.press("Enter");
  await expect(page.locator(".webui2-session-title").filter({ hasText: /^Release plan$/ })).toBeVisible();
  expect((await row.boundingBox())!.height).toBe(originalHeight);
  await page.reload();
  await showSidebar(page);
  await expect(row.locator(".webui2-session-title")).toHaveText("Release plan");

  await row.click({ button: "right" });
  const contextMenu = page.locator(".webui2-session-context-menu");
  await expect(contextMenu).toBeVisible();
  await expect(contextMenu).toHaveCSS("background-image", "none");
  const menuBox = (await contextMenu.boundingBox())!;
  expect(menuBox.x).toBeGreaterThanOrEqual(0);
  expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  expect(menuBox.y + menuBox.height).toBeLessThanOrEqual(page.viewportSize()!.height);
  await page.screenshot({ path: testInfo.outputPath("rename-context.png") });
  await contextMenu.getByRole("menuitem", { name: "Rename session" }).click();
  editor = page.getByRole("textbox", { name: "Rename session", exact: true });
  await expect(editor).toHaveValue("Release plan");
  await editor.fill("Final release plan");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(row.locator(".webui2-session-title")).toHaveText("Final release plan");
  await row.locator(".webui2-session-select").focus();
  await expect(page.getByRole("tooltip")).toContainText("Final release plan");
  await page.keyboard.press("Escape");
  await page.screenshot({ path: testInfo.outputPath("rename-saved.png") });
});

test("pure-color editor fits a compact sidebar and cancellation or errors never change the saved title", async ({ page }, testInfo) => {
  await page.locator(".webui2-sidebar").evaluate((node) => { (node as HTMLElement).style.width = "232px"; });
  const row = page.locator(".webui2-session-row").filter({ has: page.getByRole("button", { name: SESSION_ACTIONS, exact: true }) });
  const originalHeight = (await row.boundingBox())!.height;
  for (const theme of ["light", "dark"]) {
    await page.locator(".webui2-page").evaluate((node, value) => node.setAttribute("data-theme", value), theme);
    await row.getByRole("button", { name: SESSION_ACTIONS, exact: true }).click();
    await page.getByRole("menuitem", { name: "Rename session" }).click();
    const editor = page.getByRole("textbox", { name: "Rename session", exact: true });
    await editor.fill("A very long title for testing compact inline controls and cancellation");
    await expect(editor).toHaveCSS("background-image", "none");
    const geometry = await page.locator(".webui2-session-editing").evaluate((node) => {
      const [input, save, cancel] = [...node.querySelectorAll("input, button")].map((item) => item.getBoundingClientRect());
      const bounds = node.getBoundingClientRect();
      return { fits: node.scrollWidth <= node.clientWidth, nonoverlap: input.right <= save.left && save.right <= cancel.left, height: bounds.height, inputHeight: input.height };
    });
    expect(geometry.fits).toBe(true);
    expect(geometry.nonoverlap).toBe(true);
    expect(geometry.inputHeight).toBeLessThanOrEqual(originalHeight);
    expect(geometry.height).toBeLessThanOrEqual(originalHeight);
    await page.screenshot({ path: testInfo.outputPath(`rename-compact-${theme}.png`) });
    await editor.press("Escape");
    await expect(row.locator(".webui2-session-title")).toHaveText("Design review");
  }
  await page.route("**/tenant/sessions/2", (route) => route.fulfill({ status: 503, json: { code: "service_unavailable" } }));
  await row.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Rename session" }).click();
  const editor = page.getByRole("textbox", { name: "Rename session", exact: true });
  await editor.fill(" ");
  await editor.press("Enter");
  await expect(page.getByText("Enter a session title.", { exact: true })).toBeVisible();
  await editor.fill("Unsaved name");
  await editor.press("Enter");
  await expect(page.getByText("Title could not be saved. Try again.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(row.locator(".webui2-session-title")).toHaveText("Design review");
  await expect(page.locator(".webui2-conversation-message")).not.toHaveCount(0);
});
