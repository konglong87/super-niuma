import { expect, test } from "@playwright/test";
import { desktopOrigin, installDesktopHost } from "./fixtures/desktopHost";

// Exercise the desktop origin and bridge contract using the actual desktop UI.
// Native packaging and process startup are verified separately on macOS.
test("slow native preparation stays neutral, then enters the workspace automatically", async ({ page, baseURL }, testInfo) => {
  await installDesktopHost(page, baseURL!, "zh");
  let healthy = false;
  await page.route(/\/(?:health|readyz)$/, (route) => route.fulfill({
    status: healthy ? 200 : 503,
    json: { ok: healthy, runtime_defaults: { needs_setup: false } }
  }));
  await page.addInitScript(() => {
    window.go = { main: { app: {
      GetLocalServiceStatus: async () => ({ state: "starting", port: 12345 }),
      RestartLocalService: async () => { throw new Error("Unexpected manual restart during preparation"); }
    } } };
  });
  await page.goto(desktopOrigin);
  const banner = page.locator(".webui2-desktop-readiness");
  await expect(banner).toHaveAttribute("data-state", "starting");
  await expect(banner).toContainText("正在准备工作区");
  await expect(banner.locator("button")).toHaveCount(0);
  await expect(page.locator(".webui2-new-session")).toBeDisabled();
  await page.clock.install();
  await page.clock.runFor(10_000);
  await expect(banner).toHaveAttribute("data-state", "starting");
  const box = await banner.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
  await page.screenshot({ path: testInfo.outputPath("desktop-preparing.png") });
  healthy = true;
  await page.clock.runFor(3_000);
  await expect(banner).toHaveCount(0);
  await expect(page.locator(".webui2-new-session")).toBeEnabled();
  await expect(page.locator(".webui2-session-list")).toContainText("Release coordination");
  await expect(page.locator(".webui2-empty-state img")).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("desktop-ready.png") });
});

test("confirmed failure offers one retry and recovers without a page reload", async ({ page, baseURL }, testInfo) => {
  await installDesktopHost(page, baseURL!, "zh");
  let healthy = false;
  let restarts = 0;
  await page.exposeFunction("restartFixtureService", () => { restarts++; healthy = true; });
  await page.route(/\/(?:health|readyz)$/, (route) => route.fulfill({
    status: healthy ? 200 : 503,
    json: { ok: healthy, runtime_defaults: { needs_setup: false } }
  }));
  await page.addInitScript(() => {
    window.go = { main: { app: {
      GetLocalServiceStatus: async () => ({ state: "failed", port: 12345, error: "Internal failure details" }),
      RestartLocalService: () => (window as unknown as { restartFixtureService: () => Promise<void> }).restartFixtureService()
    } } };
  });
  await page.goto(desktopOrigin);
  const banner = page.locator(".webui2-desktop-readiness");
  await expect(banner).toHaveAttribute("data-state", "starting");
  await page.clock.install();
  await page.clock.runFor(12_000);
  await expect(banner).toHaveAttribute("data-state", "failed");
  await expect(banner.locator("button")).toHaveCount(1);
  await expect(banner).not.toContainText("Internal failure details");
  await page.screenshot({ path: testInfo.outputPath("desktop-retry.png") });
  await banner.getByRole("button", { name: "重新准备" }).click();
  await expect(banner).toHaveCount(0);
  expect(restarts).toBe(1);
  await expect(page.locator(".webui2-new-session")).toBeEnabled();
});
