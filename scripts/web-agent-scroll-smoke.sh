#!/usr/bin/env bash
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/product-env.sh"


ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="${NIUMA_WEB_AGENT_HOST:-127.0.0.1}"
PORT="${NIUMA_WEB_AGENT_PORT:-18088}"
AUTH_TOKEN="${NIUMA_WEB_AGENT_AUTH_TOKEN:-test-token}"
PROVIDER_PORT="${WEB_AGENT_SCROLL_PROVIDER_PORT:-19087}"
DB_NAME="${NIUMA_WEB_AGENT_DB:-golang_cc_web_agent_scroll_$$}"
TMP_BASE="${TMPDIR:-/tmp}"
WORKSPACE="$(mktemp -d "${TMP_BASE%/}/web-agent-scroll-workspace.XXXXXX")"

cleanup() {
  if [[ -n "${PROVIDER_PID:-}" ]]; then
    kill "${PROVIDER_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
  rm -rf "${WORKSPACE}"
}
trap cleanup EXIT

cd "${ROOT}"
mkdir -p "${WORKSPACE}/config"
printf '%s\n' \
  'model: scroll-stub' \
  'provider: scroll-stub' \
  'fallback:' \
  '  enabled: true' \
  '  providers:' \
  '    - name: scroll-stub' \
  '      type: custom' \
  "      baseURL: http://127.0.0.1:${PROVIDER_PORT}/v1" \
  '      apiKey: scroll-stub-key' \
  '      model: scroll-stub' >"${WORKSPACE}/config/config.local.yaml"
WEB_AGENT_SCROLL_PROVIDER_PORT="${PROVIDER_PORT}" WEB_AGENT_SCROLL_PROVIDER_CHUNK_DELAY_MS="2" node ./scripts/web-agent-scroll-provider.mjs &
PROVIDER_PID="$!"

for _ in {1..40}; do
  if curl -fsS "http://127.0.0.1:${PROVIDER_PORT}/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

export ANTHROPIC_BASE_URL="http://127.0.0.1:${PROVIDER_PORT}/v1"
export ANTHROPIC_API_KEY="scroll-stub-key"
unset NIUMA_PROVIDER CLAUDE_CODE_PROVIDER
export CLAUDE_CODE_MODEL="scroll-stub"
export NIUMA_WEB_AGENT_PORT="${PORT}"
export NIUMA_WEB_AGENT_AUTH_TOKEN="${AUTH_TOKEN}"
export NIUMA_WEB_AGENT_DB="${DB_NAME}"

cat <<EOF
web-agent-scroll-smoke: starting deterministic scroll backend
web-agent-scroll-smoke: provider=http://127.0.0.1:${PROVIDER_PORT}/v1
web-agent-scroll-smoke: url=http://${HOST}:${PORT}/webui/agent?token=${AUTH_TOKEN}
web-agent-scroll-smoke: this is a UI scroll/typewriter stub, not a real model test
EOF

./scripts/web-agent-start.sh e2e &
SERVER_PID="$!"

for _ in {1..120}; do
  if curl -fsS "http://${HOST}:${PORT}/health" -H "Authorization: Bearer ${AUTH_TOKEN}" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    wait "${SERVER_PID}"
  fi
  sleep 0.5
done
curl -fsS "http://${HOST}:${PORT}/health" -H "Authorization: Bearer ${AUTH_TOKEN}" >/dev/null

BASE_URL="http://${HOST}:${PORT}" AUTH_TOKEN="${AUTH_TOKEN}" WORKSPACE="${WORKSPACE}" node <<'NODE'
const { chromium } = require("./web/node_modules/playwright");

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
  const issues = [];
  page.on("console", message => {
    if (["error", "warning"].includes(message.type())) issues.push(`${message.type()}: ${message.text()}`);
  });
  page.on("pageerror", error => issues.push(`pageerror: ${error.message}`));
  await page.addInitScript(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("go-claude-webui.identity.v1", JSON.stringify({
      apiBase: "",
      apiToken: "test-token",
      mobileJwt: "",
      tenantKey: "webui-local",
      userId: "webui-local-user",
      deviceId: "scroll-smoke",
      model: "scroll-stub"
    }));
  });

  const url = `${process.env.BASE_URL}/webui/agent?token=${process.env.AUTH_TOKEN}`;
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: 30_000 });
  await page.getByRole("button", { name: /New Session/ }).first().waitFor({ timeout: 30_000 });
  await page.getByRole("button", { name: /New Session/ }).first().click();
  const dialog = page.getByRole("dialog", { name: "Start in current workspace" });
  await dialog.getByLabel("Workspace cwd").fill(process.env.WORKSPACE);
  await page.getByText(`Ready: ${process.env.WORKSPACE}`).waitFor({ timeout: 15_000 });
  await dialog.getByLabel("First prompt").fill("Return the deterministic long Markdown scroll response.");
  await dialog.getByRole("button", { name: "Create Session" }).click();

  const assistant = page.locator(".agent-message.assistant.typewriter").last();
  const scroll = page.getByTestId("agent-conversation-scroll");
  await assistant.waitFor({ timeout: 30_000 });
  await page.evaluate(() => {
    const container = document.querySelector('[data-testid="agent-conversation-scroll"]');
    const latest = document.querySelector(".agent-message.assistant.typewriter:last-of-type");
    window.__webAgentResizeSamples = [];
    window.__webAgentResizeObserver = new ResizeObserver(() => {
      const markdown = latest?.querySelector(".agent-markdown");
      window.__webAgentResizeSamples.push({
        visibleLength: markdown?.textContent?.length || 0,
        bottomDistance: Math.max(0, container.scrollHeight - container.scrollTop - container.clientHeight)
      });
    });
    window.__webAgentResizeObserver.observe(latest);
  });
  const samples = [];
  let closedAtLength = null;
  let sawGrowthAfterClosed = false;
  let previousLength = 0;
  const deadline = Date.now() + 45_000;
  while (Date.now() < deadline) {
    const sample = await scroll.evaluate(async (element) => {
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const latest = element.querySelector(".agent-message.assistant.typewriter:last-of-type .agent-markdown");
      const response = await fetch("/api/tenant/agent-tasks?limit=100", {
        headers: {
          Authorization: "Bearer test-token",
          "X-Tenant-Key": "webui-local",
          "X-User-Id": "webui-local-user",
          "X-Device-Id": "scroll-smoke"
        }
      });
      const payload = await response.json();
      const latestTask = (payload.data || []).reduce((latest, task) => !latest || task.id > latest.id ? task : latest, null);
      return {
        visibleLength: latest?.textContent?.length || 0,
        bottomDistance: Math.max(0, element.scrollHeight - element.scrollTop - element.clientHeight),
        taskStatus: latestTask?.status || ""
      };
    });
    const state = (await page.locator(".composer-state").textContent())?.trim() || "";
    samples.push({ ...sample, state });
    if (sample.taskStatus === "completed" && closedAtLength === null) closedAtLength = sample.visibleLength;
    if (closedAtLength !== null && sample.visibleLength > closedAtLength) sawGrowthAfterClosed = true;
    previousLength = sample.visibleLength;
    if (sample.visibleLength > 0 && (await assistant.textContent()).includes("scroll stub: completed") && state === "ready") break;
    await page.waitForTimeout(100);
  }

  const finalText = await assistant.locator(".agent-markdown").textContent();
  if (!finalText?.includes("scroll stub: completed")) throw new Error("typewriter response did not finish");
  if (closedAtLength === null || !sawGrowthAfterClosed) throw new Error(`no visible typewriter growth after SSE closed: closed=${closedAtLength}, final=${finalText.length}`);
  const resizeSamples = await page.evaluate(() => window.__webAgentResizeSamples || []);
  const maxResizeBottomDistance = Math.max(0, ...resizeSamples.filter((sample, index) => index > 0 && sample.visibleLength > resizeSamples[index - 1].visibleLength).map(sample => sample.bottomDistance));
  if (maxResizeBottomDistance > 2) throw new Error(`lost bottom follow after rendered resize: max=${maxResizeBottomDistance}`);

  await scroll.evaluate(element => {
    element.scrollTop = Math.max(0, element.scrollHeight - element.clientHeight - 320);
    element.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  const pausedTop = await scroll.evaluate(element => element.scrollTop);
  await page.waitForTimeout(300);
  const pausedTopAfter = await scroll.evaluate(element => element.scrollTop);
  if (Math.abs(pausedTopAfter - pausedTop) > 2) throw new Error(`intentional upward scroll was overridden: ${pausedTop} -> ${pausedTopAfter}`);
  await page.getByRole("button", { name: "Jump to latest" }).click();
  await page.waitForFunction(() => {
    const element = document.querySelector('[data-testid="agent-conversation-scroll"]');
    return element && element.scrollHeight - element.scrollTop - element.clientHeight <= 2;
  });

  const taskResult = await page.evaluate(async () => {
    const response = await fetch("/api/tenant/agent-tasks?limit=100", {
      headers: {
        Authorization: "Bearer test-token",
        "X-Tenant-Key": "webui-local",
        "X-User-Id": "webui-local-user",
        "X-Device-Id": "scroll-smoke"
      }
    });
    const payload = await response.json();
    const task = (payload.data || []).reduce((latest, item) => !latest || item.id > latest.id ? item : latest, null);
    return JSON.parse(task?.result_json || "{}").response || "";
  });
  const expectedLines = taskResult.split("\n").filter(line => /^line \d+:/.test(line));
  const missingLines = expectedLines.filter(line => !finalText.includes(line));
  if (expectedLines.length !== 72 || missingLines.length > 0 || !taskResult.includes("scroll stub: completed")) {
    throw new Error(`visible response is incomplete: expectedLines=${expectedLines.length}, missingLines=${missingLines.length}`);
  }
  if (issues.length > 0) throw new Error(`browser console issues: ${issues.join(" | ")}`);

  console.log(JSON.stringify({
    ok: true,
    url,
    samples: samples.length,
    firstVisibleLength: samples[0]?.visibleLength || 0,
    closedAtLength,
    finalVisibleLength: finalText.length,
    maxGrowingBottomDistance: maxResizeBottomDistance,
    sawGrowthAfterClosed,
    pausedTop,
    pausedTopAfter
  }));
  await browser.close();
})().catch(error => {
  console.error(error);
  process.exit(1);
});
NODE
