import type { Page } from "@playwright/test";
import { installWebUIV2Sessions } from "./webuiV2Sessions";

export const desktopOrigin = "http://wails.localhost";

export async function installDesktopHost(page: Page, baseURL: string, language = "en") {
  await page.route(`${desktopOrigin}/**`, async (route) => {
    const url = new URL(route.request().url());
    await route.fulfill({ response: await route.fetch({ url: `${baseURL}${url.pathname}${url.search}` }) });
  });
  await installWebUIV2Sessions(page);
  await page.route("**/runtime/settings**", (route) => route.fulfill({ json: { doc: {} } }));
  await page.route(/\/(?:health|readyz)$/, (route) => route.fulfill({ json: { ok: true } }));
  await page.route("**/status", (route) => route.fulfill({
    json: { model: "fixture-model", workspace: "/workspace/project", runtime_defaults: { needs_setup: false, provider: "openai", model: "fixture-model", default_workspace: "/workspace/project" } }
  }));
  await page.addInitScript((locale) => {
    localStorage.setItem("niuma-webui.language.v1", locale);
    window.__NIUMA_DESKTOP_TOKEN__ = "fixture-desktop-token";
    window.go = { main: { app: {
      GetLocalServiceStatus: async () => ({ state: "ready", port: 12345 }),
      RestartLocalService: async () => undefined
    } } };
  }, language);
}
