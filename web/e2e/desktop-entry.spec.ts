import { expect, test } from "@playwright/test";
import { desktopOrigin, installDesktopHost } from "./fixtures/desktopHost";

test.beforeEach(async ({ page, baseURL }) => {
  await installDesktopHost(page, baseURL!);
});

test("first launch requires model setup and enters the workspace once configuration exists", async ({ page }, testInfo) => {
  let needsSetup = true;
  await page.route("**/status", (route) => route.fulfill({
    json: { model: needsSetup ? "" : "fixture-model", runtime_defaults: { needs_setup: needsSetup, provider: needsSetup ? "" : "openai", model: needsSetup ? "" : "fixture-model", default_workspace: "/workspace/project" } }
  }));
  await page.goto(desktopOrigin);
  await expect(page.getByRole("dialog", { name: "Configure your model", exact: true })).toBeVisible();
  await expect(page.locator(".webui2-route-error")).toHaveCount(0);
  await page.getByRole("button", { name: "Configure model", exact: true }).click();
  await expect(page).toHaveURL(/\/webui\/v2\/settings\/models$/);
  await page.goBack();
  await expect(page.getByRole("dialog", { name: "Configure your model", exact: true })).toBeVisible();
  // Emulate configuration saved by the settings backend, not a dismissible
  // first-run flag: reopening must use the actual runtime configuration.
  needsSetup = false;
  await page.reload();
  await expect(page.locator(".webui2-empty-state")).toBeVisible();
  await expect(page.locator(".webui2-route-error")).toHaveCount(0);
  await page.reload();
  await expect(page.locator(".webui2-empty-state")).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Configure your model", exact: true })).toHaveCount(0);
  await expect(page.locator(".webui2-route-error")).toHaveCount(0);
  await expect(page.locator(".webui2-empty-state img")).toBeVisible();
  expect(await page.locator(".webui2-empty-state img").evaluate((image: HTMLImageElement) => image.naturalWidth)).toBeGreaterThan(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
  await page.screenshot({ path: testInfo.outputPath("desktop-root.png") });
  await page.locator(".webui2-empty-state").getByRole("button", { name: "New session" }).click();
  await expect(page.locator(".webui2-new-session-dialog")).toBeVisible();
});

test("existing sessions and invalid deep links retain their navigation semantics", async ({ page, isMobile }) => {
  await page.goto(desktopOrigin);
  await expect(page.locator(".webui2-empty-state")).toBeVisible();
  const launcher = page.getByRole("button", { name: "Open sessions" });
  if (await launcher.isVisible()) await launcher.click();
  const session = page.locator(".webui2-session-select").filter({ hasText: "Design review" });
  if (isMobile) await session.tap();
  else await session.click();
  await expect(page).toHaveURL(/\/sessions\/tenant%3Abeta$/);
  await expect(page.locator(".webui2-conversation-message--assistant")).toContainText("The responsive review is complete.");
  await page.goBack();
  await expect(page.locator(".webui2-empty-state")).toBeVisible();
  await expect(page.locator(".webui2-route-error")).toHaveCount(0);
  await page.goto(`${desktopOrigin}/webui/v2/sessions/not-a-ref`);
  await expect(page.locator(".webui2-route-error")).toContainText("This session link is invalid.");
  await page.getByRole("button", { name: "Back to sessions" }).click();
  await expect(page).toHaveURL(/\/webui\/v2$/);
  await expect(page.locator(".webui2-empty-state")).toBeVisible();
});
