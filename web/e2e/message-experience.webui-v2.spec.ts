import { expect, test, type Page } from "@playwright/test";

const THINKING_PREFERENCE_KEY = "niuma-webui.v2.thinking-mode";

async function setThinkingMode(page: Page, mode: "full" | "summary" | "hidden") {
  const control = page.getByRole("combobox", { name: "Thinking", exact: true });
  if (await control.count()) {
    await control.selectOption(mode);
    return;
  }
  await page.evaluate(({ key, value }) => {
    window.localStorage.setItem(key, value);
    window.dispatchEvent(new StorageEvent("storage", { key, newValue: value }));
  }, { key: THINKING_PREFERENCE_KEY, value: mode });
}

test("long history preserves clickable folded records and scroll ownership", async ({ page }) => {
  const session = { id: 999999, ref: "tenant:message-experience", source: "tenant", title: "Renderer fixture", status: "completed", updated_at: "2026-09-06T00:00:00Z", short_id: "fixture" };
  const events: Array<{ id: number; task_id: number; event_type: string; payload_json: string; created_at: string }> = [];
  const event = (task: number, type: string, payload: object) => events.push({ id: events.length + 1, task_id: task, event_type: type, payload_json: JSON.stringify(payload), created_at: session.updated_at });
  for (let index = 1; index <= 35; index++) {
    event(index, "message", { content: `Historical request ${index}` });
    event(index, "completed", { response: `Historical response ${index}` });
  }
  event(36, "thinking_delta", { content: "Checking the source before responding." });
  event(36, "tool_call", { tool_id: "read-1", tool_name: "Read", input: { file_path: "/workspace/example.ts" } });
  event(36, "tool_result", { tool_id: "read-1", tool_name: "Read", output: "export const ready = true;" });
  event(36, "completed", { response: "Final response", model: "fixture-model" });
  const data = { schema_version: "niuma.session-conversation.v1", session, events, cursor: String(events.length), has_more: false };
  await page.addInitScript(() => localStorage.setItem("niuma-webui.language.v1", "en"));
  await page.route("**/tenant/session-control/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/conversations/stream")) return route.fulfill({ contentType: "text/event-stream", body: `event: conversation\ndata: ${JSON.stringify(data)}\n\n` });
    if (url.pathname.endsWith("/conversation")) return route.fulfill({ json: { data } });
    return route.fulfill({ json: { data: url.searchParams.get("source") === "local" ? [] : [session] } });
  });
  await page.route("**/tenant/agent-tasks/*/pending-inputs", (route) => route.fulfill({ json: { data: [] } }));
  await page.goto(`/webui/v2/sessions/${encodeURIComponent(session.ref)}?token=test-token`);
  const stream = page.locator(".webui2-conversation-stream");
  await expect(page.getByText("Final response", { exact: true })).toBeInViewport();
  await expect.poll(() => stream.evaluate((node) => node.scrollTop)).toBeGreaterThan(0);
  await stream.evaluate((node) => { node.scrollTop = 0; });
  await expect(page.getByRole("button", { name: "Jump to latest", exact: true })).toBeVisible();
  await page.getByRole("button", { name: /Show earlier messages/ }).click();
  await expect(page.getByText("Historical request 1", { exact: true })).toBeAttached();
  await setThinkingMode(page, "full");
  await expect(page.getByText("Checking the source before responding.", { exact: true })).toBeAttached();
  await setThinkingMode(page, "hidden");
  await setThinkingMode(page, "summary");
  const tool = page.locator(".webui2-tool-message");
  const summary = tool.locator("summary");
  await summary.scrollIntoViewIfNeeded();
  const geometry = await summary.evaluate((node) => {
    const rect = node.getBoundingClientRect();
    const parentRect = node.parentElement!.getBoundingClientRect();
    return { summaryHeight: rect.height, parentHeight: parentRect.height, hit: node.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)) };
  });
  expect(geometry.parentHeight).toBeGreaterThanOrEqual(geometry.summaryHeight);
  expect(geometry.hit).toBe(true);
  await summary.click();
  await expect(tool).toHaveAttribute("open", "");
  await expect(tool.locator("pre").filter({ hasText: "export const ready" })).toBeVisible();
  await summary.click();
  await stream.evaluate((node) => { node.scrollTop = 0; });
  await page.getByRole("button", { name: "Jump to latest", exact: true }).click();
  await expect(page.getByText("Final response", { exact: true })).toBeInViewport();
  await expect.poll(() => stream.evaluate((node) => node.scrollHeight - node.clientHeight - node.scrollTop)).toBeLessThan(2);
});

test("conversation cards and composer share one content width without decorative rules", async ({ page }, testInfo) => {
  const session = { id: 1000000, ref: "tenant:message-width", source: "tenant", title: "Width fixture", status: "completed", updated_at: "2026-09-06T00:00:00Z", short_id: "width" };
  const events = [
    { id: 1, task_id: 1, event_type: "message", payload_json: JSON.stringify({ content: "Check the shared content width." }), created_at: session.updated_at },
    { id: 2, task_id: 1, event_type: "thinking_delta", payload_json: JSON.stringify({ content: "Thinking content stays inside the same column." }), created_at: session.updated_at },
    { id: 3, task_id: 1, event_type: "completed", payload_json: JSON.stringify({ response: "The width is aligned." }), created_at: session.updated_at }
  ];
  const data = { schema_version: "niuma.session-conversation.v1", session, events, cursor: String(events.length), has_more: false };
  await page.addInitScript(() => localStorage.setItem("niuma-webui.language.v1", "en"));
  await page.route("**/tenant/session-control/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/conversations/stream")) return route.fulfill({ contentType: "text/event-stream", body: `event: conversation\ndata: ${JSON.stringify(data)}\n\n` });
    if (url.pathname.endsWith("/conversation")) return route.fulfill({ json: { data } });
    return route.fulfill({ json: { data: url.searchParams.get("source") === "local" ? [] : [session] } });
  });
  await page.route("**/tenant/agent-tasks/*/pending-inputs", (route) => route.fulfill({ json: { data: [] } }));
  await page.goto(`/webui/v2/sessions/${encodeURIComponent(session.ref)}?token=test-token`);
  await setThinkingMode(page, "summary");
  await expect(page.locator(".webui2-thinking-surface")).toBeVisible();

  const geometry = await page.evaluate(() => {
    const rect = (selector: string) => {
      const element = document.querySelector<HTMLElement>(selector);
      if (!element) throw new Error(`missing ${selector}`);
      const box = element.getBoundingClientRect();
      const styles = getComputedStyle(element);
      return { left: box.left, right: box.right, width: box.width, borderTopStyle: styles.borderTopStyle };
    };
    return {
      thinking: rect(".webui2-thinking-surface"),
      composer: rect(".webui2-composer"),
      shell: rect(".webui2-composer-shell"),
      footer: rect(".webui2-message-footer")
    };
  });

  expect(Math.abs(geometry.thinking.left - geometry.composer.left)).toBeLessThanOrEqual(1);
  expect(Math.abs(geometry.thinking.right - geometry.composer.right)).toBeLessThanOrEqual(1);
  expect(Math.abs(geometry.composer.left - geometry.shell.left)).toBeLessThanOrEqual(1);
  expect(Math.abs(geometry.composer.right - geometry.shell.right)).toBeLessThanOrEqual(1);
  expect(geometry.composer.borderTopStyle).toBe("none");
  expect(geometry.footer.borderTopStyle).toBe("none");
  await page.screenshot({ path: testInfo.outputPath("conversation-width-aligned.png"), fullPage: false });
});

test("Computer Use thumbnails stay compact and proportional without shrinking the original preview", async ({ page }) => {
  const thumbnailSize = 160;
  const session = { id: 1000001, ref: "tenant:compact-observations", source: "tenant", title: "Compact observations", status: "completed", updated_at: "2026-10-01T00:00:00Z", short_id: "compact" };
  const dimensions = [
    { asset: "landscape", width: 1280, height: 720 },
    { asset: "portrait", width: 720, height: 1280 },
    { asset: "square", width: 900, height: 900 }
  ];
  const events = dimensions.flatMap(({ asset }, index) => [
    { id: index * 2 + 1, task_id: 1, event_type: "tool_call", payload_json: JSON.stringify({ tool_id: asset, tool_name: "ComputerUse", input: { action: "observe" } }), created_at: session.updated_at },
    { id: index * 2 + 2, task_id: 1, event_type: "tool_result", payload_json: JSON.stringify({ tool_id: asset, output: "Observed", computer_observation: { observation_id: `obs-${asset}`, asset_id: asset, media_type: "image/svg+xml" } }), created_at: session.updated_at }
  ]);
  events.push(
    { id: 7, task_id: 1, event_type: "image_artifact", payload_json: JSON.stringify({ asset_id: "ordinary" }), created_at: session.updated_at },
    { id: 8, task_id: 1, event_type: "completed", payload_json: JSON.stringify({ response: "Screenshots ready." }), created_at: session.updated_at }
  );
  const data = { schema_version: "niuma.session-conversation.v1", session, events, cursor: String(events.length), has_more: false };
  await page.addInitScript(() => localStorage.setItem("niuma-webui.language.v1", "en"));
  await page.route("**/tenant/session-control/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/conversations/stream")) return route.fulfill({ contentType: "text/event-stream", body: `event: conversation\ndata: ${JSON.stringify(data)}\n\n` });
    if (url.pathname.endsWith("/conversation")) return route.fulfill({ json: { data } });
    return route.fulfill({ json: { data: url.searchParams.get("source") === "local" ? [] : [session] } });
  });
  await page.route("**/tenant/agent-tasks/*/pending-inputs", (route) => route.fulfill({ json: { data: [] } }));
  await page.route("**/tenant/media/assets/*", (route) => {
    const asset = new URL(route.request().url()).pathname.split("/").at(-1);
    const { width, height } = dimensions.find((image) => image.asset === asset) ?? dimensions[0];
    return route.fulfill({ contentType: "image/svg+xml", body: `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#237c57"/></svg>` });
  });
  await page.goto(`/webui/v2/sessions/${encodeURIComponent(session.ref)}?token=test-token`);
  const observations = page.locator(".webui2-tool-observation");
  await expect(observations).toHaveCount(dimensions.length);
  for (const [index, { width, height }] of dimensions.entries()) {
    const observation = observations.nth(index);
    const image = observation.locator("img");
    await expect.poll(() => image.evaluate((node: HTMLImageElement) => node.naturalWidth)).toBe(width);
    const geometry = await image.evaluate((node) => {
      const imageBox = node.getBoundingClientRect();
      const wrapper = node.closest(".agent-generated-image-wrap")?.getBoundingClientRect();
      return { width: imageBox.width, height: imageBox.height, wrapperHeight: wrapper?.height };
    });
    expect(Math.max(geometry.width, geometry.height)).toBeCloseTo(thumbnailSize, 0);
    expect(geometry.width / geometry.height).toBeCloseTo(width / height, 2);
    expect(geometry.wrapperHeight).toBeCloseTo(geometry.height, 0);
    const summary = observation.locator("..").locator("details");
    await expect(summary).not.toHaveAttribute("open", "");
  }

  const thumbnail = observations.first().getByRole("button", { name: "Open generated asset" });
  const originalSource = await thumbnail.locator("img").getAttribute("src");
  await thumbnail.click();
  const preview = page.getByRole("dialog", { name: "Generated asset preview" });
  await expect(preview).toBeVisible();
  const previewImage = preview.getByRole("img");
  await expect(previewImage).toHaveAttribute("src", originalSource ?? "");
  await expect.poll(() => previewImage.evaluate((node: HTMLImageElement) => node.naturalWidth)).toBe(dimensions[0].width);
  const previewBox = await previewImage.boundingBox();
  expect(previewBox?.width).toBeGreaterThan(thumbnailSize);
  expect((previewBox?.width ?? 0) / (previewBox?.height ?? 1)).toBeCloseTo(dimensions[0].width / dimensions[0].height, 2);
  await page.keyboard.press("Escape");
  await expect(preview).not.toBeVisible();
  await expect(thumbnail).toBeFocused();

  const ordinaryImage = page.locator(".agent-message-artifacts img");
  await expect.poll(() => ordinaryImage.evaluate((node: HTMLImageElement) => node.naturalWidth)).toBe(dimensions[0].width);
  expect((await ordinaryImage.boundingBox())?.width).toBeGreaterThan(thumbnailSize);
  for (const divisor of [6, 8]) {
    await observations.first().evaluate((node, value) => (node as HTMLElement).style.setProperty("--computer-observation-thumbnail-divisor", String(value)), divisor);
    const box = await thumbnail.locator("img").boundingBox();
    expect(box?.width).toBeCloseTo(thumbnailSize * 4 / divisor, 0);
    expect((box?.width ?? 0) / (box?.height ?? 1)).toBeCloseTo(dimensions[0].width / dimensions[0].height, 2);
  }
});
