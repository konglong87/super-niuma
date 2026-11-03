import { expect, test } from "@playwright/test";
import { desktopOrigin, installDesktopHost } from "./fixtures/desktopHost";

test("desktop-v2 task-first home shows the default workspace and starts from the composer", async ({ page, baseURL }, testInfo) => {
  test.skip(process.env.VITE_DESKTOP_UI_VERSION !== "2", "Run with VITE_DESKTOP_UI_VERSION=2 to exercise the desktop-v2 home.");
  await installDesktopHost(page, baseURL!, "zh");

  let createPayload: Record<string, unknown> | undefined;
  await page.route(`${desktopOrigin}/tenant/session-control/sessions`, async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    createPayload = route.request().postDataJSON() as Record<string, unknown>;
    await route.fulfill({
      json: {
        operation_id: "operation-welcome-create",
        session: {
          id: 99,
          ref: "tenant:welcome-99",
          source: "tenant",
          title: String(createPayload.title || "写代码"),
          status: "idle",
          updated_at: "2026-09-20T09:00:00.000Z",
          short_id: "welcome-99",
          cwd: "/workspace/project",
          prompt_mode: "code"
        },
        replayed: false
      }
    });
  });

  await page.goto(`${desktopOrigin}/`);
  const brandAnimation = page.getByRole("img", { name: "niuma" });
  await expect(brandAnimation).toBeVisible();
  await expect(brandAnimation).toHaveAttribute("width", "240");
  await expect(brandAnimation).toHaveAttribute("height", "96");
  await expect(page.getByRole("heading", { name: "今天想让我帮你做什么？", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "选择工作区" })).toContainText("默认工作区 · project");
  await expect(page.getByRole("button", { name: "写代码" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("button", { name: "文件处理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "火花想法" })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("task-first-home.png"), fullPage: false });

  await page.getByRole("textbox", { name: "消息" }).fill("帮我检查这个项目的启动流程");
  await page.getByRole("button", { name: "发送消息" }).click();
  await expect.poll(() => createPayload).toMatchObject({
    cwd: "/workspace/project",
    initial_text: "帮我检查这个项目的启动流程",
    prompt_mode: "code"
  });
});
