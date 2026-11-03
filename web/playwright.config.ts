import { defineConfig, devices } from "@playwright/test";

const productEnv = (suffix: string): string | undefined =>
  process.env[`NIUMA_${suffix}`] ?? process.env[`NIUMA_${suffix}`] ?? process.env[`GOLANG_CLAUDE_CODE_${suffix}`];

const workflowIntegrationEnabled = productEnv("WORKFLOW_INTEGRATION_E2E") === "1";
const liveApiTarget = productEnv("WEBUI_LIVE_API_BASE");
function requireLiteralLoopback(value: string | undefined, name: string): void {
  if (!value || !/^http:\/\/(?:127\.0\.0\.1|\[::1\]):[0-9]+(?:\/v1)?\/?$/.test(value)) throw new Error(`${name} must be an explicit literal-loopback HTTP URL with a port`);
  const url = new URL(value);
  if (!url.port || url.username || url.password || url.search || url.hash) throw new Error(`${name} is not a safe loopback fixture URL`);
}
if (workflowIntegrationEnabled) {
  requireLiteralLoopback(liveApiTarget, "NIUMA_WEBUI_LIVE_API_BASE");
  requireLiteralLoopback(productEnv("WORKFLOW_OFFLINE_PROVIDER_BASE"), "NIUMA_WORKFLOW_OFFLINE_PROVIDER_BASE");
}
const viteEnv = liveApiTarget ? `VITE_NIUMA_API_TARGET=${liveApiTarget} ` : "";
const webPort = productEnv("WEBUI_E2E_PORT") || "5174";
if (workflowIntegrationEnabled && (!/^[0-9]+$/.test(webPort) || Number(webPort) < 1 || Number(webPort) > 65535)) throw new Error("Integration web port must be numeric and bounded");
const webURL = `http://127.0.0.1:${webPort}`;
const reuseExistingServer = productEnv("WEBUI_E2E_REUSE_SERVER") === "1";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  reporter: "list",
  use: {
    baseURL: webURL,
    trace: "retain-on-failure"
  },
  webServer: {
    env: workflowIntegrationEnabled ? { NPM_CONFIG_OFFLINE: "true", NPM_CONFIG_UPDATE_NOTIFIER: "false", npm_config_offline: "true", npm_config_update_notifier: "false" } : undefined,
    command: `${viteEnv}npm run dev -- --host 127.0.0.1 --port ${webPort} --strictPort`,
    url: webURL,
    reuseExistingServer: workflowIntegrationEnabled ? false : reuseExistingServer,
    timeout: 120_000
  },
  projects: [
    ...(workflowIntegrationEnabled ? [{
      name: "chromium-workflow-integrated",
      testMatch: /workflows-integrated\.spec\.ts/,
      fullyParallel: false,
      workers: 1,
      retries: 0,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 960 }, trace: "off" as const }
    }] : []),
    {
      name: "chromium-mock",
      testMatch: /webui\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] }
    },
    {
      name: "chromium-webui-v2-desktop",
      testMatch: /(?:webui-v2|settings-v2|desktop-background|workflows-v2)\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 960 } }
    },
    {
      name: "chromium-webui-v2-narrow",
      testMatch: /(?:webui-v2|settings-v2|workflows-v2)\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1024, height: 768 } }
    },
    {
      name: "chromium-webui-v2-mobile",
      testMatch: /(?:webui-v2|settings-v2|workflows-v2)\.spec\.ts/,
      use: { ...devices["Pixel 5"] }
    },
    {
      name: "chromium-smoke-desktop",
      testMatch: /navigation-smoke\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 960 } }
    },
    {
      name: "chromium-smoke-mobile",
      testMatch: /navigation-smoke\.spec\.ts/,
      use: { ...devices["Pixel 5"] }
    },
    {
      name: "chromium-live",
      testMatch: /live-api\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] }
    }
  ]
});
