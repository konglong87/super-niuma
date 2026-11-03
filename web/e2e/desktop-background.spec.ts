import { expect, test } from "@playwright/test";
import { desktopOrigin, installDesktopHost } from "./fixtures/desktopHost";

test("imports a local background image, previews it, and restores it after reload", async ({ page, baseURL }, testInfo) => {
  await installDesktopHost(page, baseURL!);
  await page.addInitScript(() => {
    const key = "desktop-background-fixture";
    const read = () => {
      try {
        return JSON.parse(localStorage.getItem(key) || '{"mode":"external","enabled":false}');
      } catch {
        return { mode: "external", enabled: false };
      }
    };
    const write = (value: unknown) => localStorage.setItem(key, JSON.stringify(value));
    const app = window.go?.main?.app ?? { RestartLocalService: async () => undefined };
    window.go = { main: { app } };
    app.GetBackgroundImage = async () => read();
    app.SaveBackgroundImage = async (dataURL, name) => {
      const next = { mode: "local" as const, enabled: true, name, mime_type: "image/png", data_url: dataURL };
      write(next);
      return next;
    };
    app.SetBackgroundMode = async (mode) => {
      const current = read();
      const next = mode === "local" && current.data_url ? { ...current, mode } : { mode, enabled: false };
      write(next);
      return next;
    };
    app.ClearBackgroundImage = async () => write({ mode: "external", enabled: false });
  });

  const settingsDoc = {
    appearance: {
      enabled: true,
      backgroundColor: "#f7f8fa",
      backgroundImage: "",
      overlayOpacity: 0,
      sidebarSurfaceOpacity: 0.46,
      emptyBlur: 0,
      conversationBlur: 0,
      composerBlur: 0,
      bubbleOpacity: 1
    }
  };
  await page.route("**/runtime/settings", (route) => route.fulfill({ json: { doc: settingsDoc, revision: "fixture", path: "/settings.json", exists: true, masked: [] } }));
  await page.goto(`${desktopOrigin}/webui/v2/settings/appearance`);
  await expect(page.getByRole("heading", { name: "Appearance", exact: true })).toBeVisible();
  const fileChooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Local image", exact: true }).click();
  await (await fileChooser).setFiles("../logo.png");
  await expect(page.getByAltText("logo.png")).toBeVisible();
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-visual-background-image").trim())).toContain("data:image/png");
  await page.screenshot({ path: testInfo.outputPath("desktop-background-imported.png"), fullPage: false });

  await page.reload();
  await expect(page.getByAltText("logo.png")).toBeVisible();
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-visual-background-image").trim())).toContain("data:image/png");
  await page.screenshot({ path: testInfo.outputPath("desktop-background-restored.png"), fullPage: false });

  await page.getByRole("button", { name: "Back to chat", exact: true }).click();
  await expect(page).toHaveURL(/\/webui\/v2(?:\/|$)/);
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-visual-background-image").trim())).toContain("data:image/png");
  await expect.poll(() => page.locator(".webui2-workspace").evaluate((node) => getComputedStyle(node).backgroundColor)).toBe("rgba(0, 0, 0, 0)");
  await expect.poll(() => page.locator(".webui2-page").evaluate((node) => getComputedStyle(node).getPropertyValue("--webui2-sidebar-surface-opacity").trim())).toBe("46%");
  await expect.poll(() => page.locator(".webui2-sidebar").evaluate((node) => getComputedStyle(node).backdropFilter)).toBe("blur(12px)");
  await page.screenshot({ path: testInfo.outputPath("desktop-background-chat-restored.png"), fullPage: false });
});
