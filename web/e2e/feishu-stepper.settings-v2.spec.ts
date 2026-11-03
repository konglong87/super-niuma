import { expect, test, type Page } from "@playwright/test";
import { installWebUIV2Sessions } from "./fixtures/webuiV2Sessions";

const languages = {
  en: { select: "Select agent", next: "Continue", save: "Save connection draft" },
  zh: { select: "选择智能体", next: "继续", save: "保存连接草稿" }
} as const;

for (const [language, copy] of Object.entries(languages)) {
  test(`Feishu stepper separates connectors and labels (${language})`, async ({ page }, testInfo) => {
    await installWebUIV2Sessions(page);
    await page.addInitScript((value) => localStorage.setItem("niuma-webui.language.v1", value), language);
    await page.route("**/runtime/settings", (route) => route.fulfill({ json: { doc: { provider: "openai", model: "fixture-model" } } }));
    await page.route("**/runtime/settings/environments", (route) => route.fulfill({ json: {
      environments: [{ id: "current", label: "Web", database: "web", tenant_key: "fixture", user_id: "operator", api_path: "", available: true }]
    } }));
    const record = { id: 7, profile_key: "support-agent", account_key: "support-bot", status: "draft", worker: { provider: "openai", model: "fixture-model" } };
    await page.route("**/tenant/agent-provisionings/overview", (route) => route.fulfill({ json: { records: [record], workers: [] } }));
    await page.route("**/tenant/agent-profiles?limit=100", (route) => route.fulfill({ json: { data: [
      { id: 3, profile_key: record.profile_key, profile_version: 1, status: "published", display_name: "Support agent", config_json: "{}" }
    ] } }));
    await page.route("**/tenant/channel-accounts?limit=100", (route) => route.fulfill({ json: { data: [
      { id: 4, account_key: record.account_key, provider: "feishu", app_id: "cli_fixture", enabled: true, status: "active" }
    ] } }));
    await page.route("**/tenant/agent-provisionings", (route) => route.fulfill({ json: record }));
    await page.goto("/webui/v2/settings/feishu");
    const steps = page.locator(".provisioning-step-button");
    await expect(steps.nth(1)).toHaveAttribute("aria-current", "step");
    await expect(steps.nth(2)).toBeDisabled();
    await assertStepGeometry(page);
    await page.screenshot({ path: testInfo.outputPath(`feishu-stepper-${language}.png`) });

    await steps.getByText(copy.select, { exact: true }).click();
    await expect(steps.nth(0)).toHaveAttribute("aria-current", "step");
    await expect(steps.nth(1)).toBeDisabled();
    await assertStepGeometry(page);
    await page.getByRole("button", { name: copy.next, exact: true }).click();
    await expect(steps.nth(1)).toHaveAttribute("aria-current", "step");
    await page.getByRole("button", { name: copy.save, exact: true }).click();
    await expect(steps.nth(2)).toHaveAttribute("aria-current", "step");
    await expect(page.locator(".provisioning-steps li.complete")).toHaveCount(2);
    await assertStepGeometry(page);
  });
}

async function assertStepGeometry(page: Page): Promise<void> {
  const geometry = await page.locator(".provisioning-steps").evaluate((list) => {
    const items = Array.from(list.querySelectorAll("li"));
    const listBounds = list.getBoundingClientRect();
    return {
      overflow: document.documentElement.scrollWidth > window.innerWidth,
      steps: items.map((item, index) => {
        const marker = item.querySelector(".provisioning-step-marker")!.getBoundingClientRect();
        const labelNode = item.querySelector<HTMLElement>(".provisioning-step-label")!;
        const label = labelNode.getBoundingClientRect();
        const bounds = item.getBoundingClientRect();
        const line = getComputedStyle(item, "::after");
        const next = items[index + 1]?.querySelector(".provisioning-step-marker")?.getBoundingClientRect();
        return {
          labelGap: label.top - marker.bottom,
          widthOffset: Math.abs(bounds.width - listBounds.width / items.length),
          labelFits: label.left >= bounds.left && label.right <= bounds.right && labelNode.scrollWidth <= labelNode.clientWidth,
          centerOffset: Math.abs(marker.x + marker.width / 2 - (bounds.x + bounds.width / 2)),
          connector: next ? {
            startOffset: Math.abs(bounds.left + Number.parseFloat(line.left) - marker.right),
            endOffset: Math.abs(bounds.right - Number.parseFloat(line.right) - next.left),
            centerOffset: Math.abs(bounds.top + Number.parseFloat(line.top) - (marker.top + marker.height / 2)),
            labelGap: label.top - (bounds.top + Number.parseFloat(line.top) + Number.parseFloat(line.height))
          } : null
        };
      })
    };
  });
  expect(geometry.overflow).toBe(false);
  for (const step of geometry.steps) {
    expect(step.labelFits).toBe(true);
    expect(step.widthOffset).toBeLessThan(1);
    expect(step.labelGap).toBeGreaterThanOrEqual(5);
    expect(step.centerOffset).toBeLessThan(1);
    if (!step.connector) continue;
    expect(step.connector.startOffset).toBeLessThan(1);
    expect(step.connector.endOffset).toBeLessThan(1);
    expect(step.connector.centerOffset).toBeLessThan(1);
    expect(step.connector.labelGap).toBeGreaterThan(0);
  }
}
