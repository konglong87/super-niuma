import { expect, test } from "@playwright/test";
import { installWebUIV2Sessions } from "./fixtures/webuiV2Sessions";

const COMPACT_ROW_HEIGHT = 40;

test.beforeEach(async ({ page }) => {
  await installWebUIV2Sessions(page);
  await page.goto("/webui/v2/sessions/tenant%3Abeta?token=test-token");
  const opener = page.getByRole("button", { name: "Open sessions", exact: true });
  if (await opener.isVisible()) await opener.click();
});

test("session rows share one surface and show metadata only in an accessible preview", async ({ page }, testInfo) => {
  const row = page.locator(".webui2-session-row").filter({ hasText: "Design review" });
  const select = row.locator(".webui2-session-select");
  await expect(row).toHaveAttribute("data-selected", "true");
  await expect(row.locator("time")).toHaveCount(0);
  await expect(row.locator("code")).toHaveCount(0);
  await expect(select).toHaveText("Design review");
  const geometry = await row.evaluate((node) => {
    const button = node.querySelector(".webui2-session-select")!;
    const actions = node.querySelector(".webui2-session-actions")!;
    return {
      background: getComputedStyle(node).backgroundColor,
      bottomBorder: getComputedStyle(node).borderBottomWidth,
      leftBorder: getComputedStyle(button).borderLeftWidth,
      buttonBackground: getComputedStyle(button).backgroundColor,
      actionsBackground: getComputedStyle(actions).backgroundColor
    };
  });
  expect(geometry.background).not.toBe("rgba(0, 0, 0, 0)");
  expect(geometry.bottomBorder).toBe("0px");
  expect(geometry.leftBorder).toBe("0px");
  expect(geometry.buttonBackground).toBe("rgba(0, 0, 0, 0)");
  expect(geometry.actionsBackground).toBe("rgba(0, 0, 0, 0)");

  await select.focus();
  const preview = page.getByRole("tooltip");
  await expect(preview).toBeVisible();
  await expect(preview).toContainText("Updated");
  await expect(preview.locator("code")).toHaveText("tenant:beta");
  await expect(preview.locator("time")).toHaveAttribute("datetime", "2026-09-05T00:00:00.000Z");
  const box = await preview.boundingBox();
  const viewport = page.viewportSize()!;
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width);
  expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height);
  await page.screenshot({ path: testInfo.outputPath("sidebar-preview-light.png") });
  await page.keyboard.press("Escape");
  await expect(preview).toHaveCount(0);
  await expect(select).toBeFocused();

  await row.locator(".webui2-session-actions > button").click();
  await page.getByRole("menuitem", { name: "Session details" }).click();
  await expect(preview).toBeVisible();
  await expect(row).toHaveAttribute("data-selected", "true");
  await page.keyboard.press("Escape");
  await expect(preview).toHaveCount(0);

  if (!testInfo.project.name.includes("mobile")) {
    await page.mouse.move(700, 500);
    await select.hover();
    await expect(preview).toBeVisible();
    await preview.hover();
    await expect(preview).toBeVisible();
    await page.waitForTimeout(600);
    await expect(preview).toBeVisible();
    await page.mouse.move(700, 500);
    await expect(preview).toHaveCount(0);
  }
});

test("status motion respects preferences and compact sidebar widths in both themes", async ({ page }, testInfo) => {
  const active = page.locator('.webui2-session-row [data-status="running"] svg');
  await expect(active).toHaveCSS("animation-name", "session-status-spin");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect(active).toHaveCSS("animation-name", "none");
  await expect(page.locator('.webui2-session-row [data-status="completed"] svg')).toHaveCSS("animation-name", "none");

  // Stress truncation without changing the shared session fixtures.
  await page.locator(".webui2-session-title").first().evaluate((node) => {
    node.textContent = "A long session title that must leave room for its status and action menu";
  });
  for (const width of [232, 288, 380]) {
    // Set the existing resize preference, then exercise the rendered geometry.
    await page.locator(".webui2-sidebar").evaluate((node, size) => { (node as HTMLElement).style.width = `${size}px`; }, width);
    for (const theme of ["light", "dark"]) {
      await page.locator(".webui2-page").evaluate((node, value) => node.setAttribute("data-theme", value), theme);
      const rows = page.locator(".webui2-session-row");
      for (const row of await rows.all()) {
        expect(await row.evaluate((node) => node.scrollWidth <= node.clientWidth)).toBe(true);
        const layout = await row.evaluate((node) => {
          const title = node.querySelector(".webui2-session-title")!;
          const status = node.querySelector(".webui2-session-status-icon")!;
          const actions = node.querySelector(".webui2-session-actions")!;
          const titleBox = title.getBoundingClientRect();
          const statusBox = status.getBoundingClientRect();
          return {
            height: node.getBoundingClientRect().height,
            titleHeight: titleBox.height,
            statusWidth: statusBox.width,
            gap: statusBox.left - titleBox.right,
            centerOffset: Math.abs(titleBox.top + titleBox.height / 2 - statusBox.top - statusBox.height / 2),
            actionsOverlap: statusBox.right > actions.getBoundingClientRect().left,
            ellipsis: getComputedStyle(title).textOverflow
          };
        });
        expect(layout.height).toBe(COMPACT_ROW_HEIGHT);
        expect(layout.titleHeight).toBe(20);
        expect(layout.statusWidth).toBe(16);
        expect(layout.gap).toBeCloseTo(6);
        expect(layout.centerOffset).toBeLessThan(1);
        expect(layout.actionsOverlap).toBe(false);
        expect(layout.ellipsis).toBe("ellipsis");
      }
      await page.screenshot({ path: testInfo.outputPath(`sidebar-${width}-${theme}.png`) });
    }
  }
});

test("account footer replaces the fixed settings button and opens settings from the menu", async ({ page }) => {
  const sidebar = page.locator(".webui2-sidebar");
  await expect(sidebar.locator(".webui2-settings-launcher")).toHaveCount(0);
  const account = sidebar.locator(".webui2-account-trigger");
  await expect(account).toContainText("webui-local");
  await account.click();
  await expect(sidebar.getByRole("menu")).toBeVisible();
  await expect(sidebar.getByRole("menuitem", { name: "System settings" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sidebar.getByRole("menu")).toHaveCount(0);
});

test("workspace plus opens a pre-routed new session dialog", async ({ page }) => {
  const plus = page.locator('.webui2-workspace-new[aria-label="New session in project"]');
  await expect(plus).toBeVisible();
  await plus.click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "cwd" })).toHaveValue("/workspace/project");
  await expect(page.getByRole("textbox", { name: "Session title" })).toHaveAttribute("placeholder", "New session");
});
