import { defineConfig, devices } from "@playwright/test";

const port = process.env.NIUMA_WEBUI_E2E_PORT || "5176";
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "./e2e",
  testMatch: /desktop-(?:background|entry|startup)\.spec\.ts/,
  fullyParallel: true,
  reporter: "list",
  use: { baseURL, trace: "retain-on-failure" },
  webServer: {
    command: `npm run dev -- --port ${port} --strictPort`,
    env: { VITE_DESKTOP_UI_VERSION: "2" },
    url: baseURL,
    reuseExistingServer: false
  },
  projects: [
    { name: "desktop-entry", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
    { name: "desktop-entry-minimum", use: { ...devices["Desktop Chrome"], viewport: { width: 1024, height: 700 } } },
    { name: "desktop-entry-mobile", use: { ...devices["Pixel 5"] } }
  ]
});
