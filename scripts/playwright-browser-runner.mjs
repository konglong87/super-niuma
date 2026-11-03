#!/usr/bin/env node
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";

const productEnv = suffix => {
  for (const prefix of ["NIUMA_", "NIUMA_", "NIUMA_"]) {
    const value = process.env[`${prefix}${suffix}`];
    if (value !== undefined) return value;
  }
  return undefined;
};

const DEFAULT_TIMEOUT_MS = Number(productEnv("PLAYWRIGHT_TIMEOUT_MS") || 15000);
const DEFAULT_VIEWPORT = { width: 1280, height: 800 };

async function main() {
  if (process.argv.includes("--self-test")) {
    await selfTest();
    return;
  }

  const request = await readRequest();
  const result = await run(request);
  process.stdout.write(`${JSON.stringify(result)}\n`);
}

async function readRequest() {
  const stdin = await new Promise((resolve, reject) => {
    let data = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", chunk => {
      data += chunk;
    });
    process.stdin.on("end", () => resolve(data));
    process.stdin.on("error", reject);
  });
  const raw = stdin.trim() || process.env.CLAUDE_BROWSER_REQUEST || "{}";
  return JSON.parse(raw);
}

async function run(request) {
  const { chromium } = await import("playwright");
  const stateDir = await ensureStateDir();
  const sessionID = cleanSessionID(request.session_id || "default");
  const statePath = join(stateDir, `${sessionID}.json`);
  const state = await readState(statePath);
  const browser = await chromium.launch({
    headless: true,
    executablePath: browserExecutablePath(),
  });
  try {
    const context = await browser.newContext({
      viewport: DEFAULT_VIEWPORT,
      ignoreHTTPSErrors: productEnv("PLAYWRIGHT_IGNORE_HTTPS_ERRORS") === "true",
    });
    const page = await context.newPage();
    const consoleLogs = [];
    const network = [];
    page.on("console", message => {
      consoleLogs.push(`${message.type()}: ${message.text()}`);
    });
    page.on("response", response => {
      network.push({
        method: response.request().method(),
        url: response.url(),
        status: response.status(),
      });
    });
    page.setDefaultTimeout(DEFAULT_TIMEOUT_MS);

    const action = String(request.action || "").toLowerCase();
    let targetURL = request.url || state.url;
    if ((action === "open" || action === "navigate") && !targetURL) {
      return { is_error: true, content: "url is required for open/navigate" };
    }
    if (targetURL) {
      await page.goto(targetURL, { waitUntil: "networkidle", timeout: DEFAULT_TIMEOUT_MS });
    }

    switch (action) {
      case "open":
      case "navigate":
        break;
      case "text": {
        const text = await page.locator("body").innerText({ timeout: DEFAULT_TIMEOUT_MS }).catch(() => "");
        return { content: limitText(text, request.limit || 4000) };
      }
      case "links":
        return { data: await extractLinks(page, request.limit || 50) };
      case "forms":
        return { data: await extractForms(page, request.limit || 50) };
      case "click":
        await clickTarget(page, request.selector);
        await page.waitForLoadState("networkidle", { timeout: DEFAULT_TIMEOUT_MS }).catch(() => {});
        break;
      case "input":
        await fillTarget(page, request.selector, request.value || "");
        state.inputs = state.inputs || {};
        state.inputs[String(request.selector || "")] = String(request.value || "");
        await writeState(statePath, { ...state, url: page.url(), inputs: state.inputs });
        return { content: JSON.stringify({ session_id: sessionID, selector: request.selector, value_set: true }) };
      case "submit":
        await restoreInputs(page, state.inputs || {});
        await submitTarget(page, request.selector);
        await page.waitForLoadState("networkidle", { timeout: DEFAULT_TIMEOUT_MS }).catch(() => {});
        state.inputs = {};
        break;
      case "screenshot": {
        const bytes = await page.screenshot({ fullPage: true, type: "png" });
        return {
          data: {
            session_id: sessionID,
            url: page.url(),
            mime_type: "image/png",
            data_url: `data:image/png;base64,${bytes.toString("base64")}`,
          },
        };
      }
      default:
        return { is_error: true, content: `unsupported WebBrowser action: ${request.action || ""}` };
    }

    const snapshot = await snapshotPage(page, sessionID, request.limit || 4000, consoleLogs, network);
    await writeState(statePath, { url: snapshot.url, inputs: state.inputs || {} });
    return { page: snapshot };
  } finally {
    await browser.close();
  }
}

async function snapshotPage(page, sessionID, textLimit, consoleLogs = [], network = []) {
  const [title, text, links, forms] = await Promise.all([
    page.title().catch(() => ""),
    page.locator("body").innerText({ timeout: DEFAULT_TIMEOUT_MS }).catch(() => ""),
    extractLinks(page, 50),
    extractForms(page, 50),
  ]);
  return {
    session_id: sessionID,
    url: page.url(),
    status: 200,
    title,
    text: limitText(text, textLimit),
    links,
    forms,
    console: consoleLogs.slice(-50),
    network: network.slice(-50),
  };
}

async function extractLinks(page, limit) {
  const links = await page.$$eval("a[href]", anchors =>
    anchors.map(anchor => ({
      text: (anchor.innerText || anchor.textContent || "").trim().replace(/\s+/g, " "),
      href: anchor.href,
    }))
  );
  return links.slice(0, limit);
}

async function extractForms(page, limit) {
  const forms = await page.$$eval("form", nodes =>
    nodes.map(form => ({
      action: form.action,
      method: (form.method || "GET").toUpperCase(),
      inputs: Array.from(form.querySelectorAll("input, textarea, select"))
        .map(input => input.getAttribute("name") || input.id || input.getAttribute("type") || "")
        .filter(Boolean),
    }))
  );
  return forms.slice(0, limit);
}

async function clickTarget(page, selector) {
  const target = String(selector || "").trim();
  if (!target) {
    await page.locator("a,button,[role=button],input[type=submit]").first().click();
    return;
  }
  const locator = locatorFor(page, target);
  await locator.click();
}

async function fillTarget(page, selector, value) {
  const target = String(selector || "").trim();
  if (!target) {
    throw new Error("selector is required for input");
  }
  await locatorFor(page, target).fill(String(value));
}

async function restoreInputs(page, inputs) {
  for (const [selector, value] of Object.entries(inputs || {})) {
    await fillTarget(page, selector, value).catch(() => {});
  }
}

async function submitTarget(page, selector) {
  const target = String(selector || "").trim();
  if (target) {
    const form = page.locator(`form${cssMaybeID(target)}, form[action*="${cssString(target)}"]`).first();
    if (await form.count()) {
      const navigation = page.waitForNavigation({ waitUntil: "networkidle", timeout: DEFAULT_TIMEOUT_MS }).catch(() => {});
      await form.evaluate(node => node.requestSubmit ? node.requestSubmit() : node.submit());
      await navigation;
      return;
    }
  }
  const form = page.locator("form").first();
  if (await form.count()) {
    const navigation = page.waitForNavigation({ waitUntil: "networkidle", timeout: DEFAULT_TIMEOUT_MS }).catch(() => {});
    await form.evaluate(node => node.requestSubmit ? node.requestSubmit() : node.submit());
    await navigation;
    return;
  }
  await page.keyboard.press("Enter");
}

function locatorFor(page, target) {
  if (target.startsWith("#") || target.startsWith(".") || target.includes("[") || target.includes("=")) {
    return page.locator(target).first();
  }
  return page.locator(`#${cssIdent(target)}, [name="${cssString(target)}"], a:has-text("${textSelector(target)}"), button:has-text("${textSelector(target)}")`).first();
}

function cssMaybeID(target) {
  if (!target) {
    return "";
  }
  return target.startsWith("#") ? target : `#${cssIdent(target)}`;
}

function cssIdent(value) {
  return String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
}

function cssString(value) {
  return String(value).replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function textSelector(value) {
  return cssString(value);
}

function limitText(text, limit) {
  text = String(text || "").trim().replace(/\s+\n/g, "\n");
  if (!limit || text.length <= limit) {
    return text;
  }
  return `${text.slice(0, limit)}\n[truncated after ${limit} chars]`;
}

async function ensureStateDir() {
  const configured = productEnv("PLAYWRIGHT_STATE_DIR");
  if (configured) {
    await mkdir(configured, { recursive: true });
    return configured;
  }
  const dir = join(tmpdir(), "niuma-webbrowser");
  await mkdir(dir, { recursive: true });
  return dir;
}

async function readState(path) {
  if (!existsSync(path)) {
    return {};
  }
  try {
    return JSON.parse(await readFile(path, "utf8"));
  } catch {
    return {};
  }
}

async function writeState(path, state) {
  await mkdir(dirname(path), { recursive: true }).catch(() => {});
  await writeFile(path, JSON.stringify(state), "utf8");
}

function cleanSessionID(value) {
  return String(value || "default").replace(/[^a-zA-Z0-9_.-]/g, "_");
}

async function selfTest() {
  const tmp = await mkdtemp(join(tmpdir(), "gcc-playwright-self-test-"));
  process.env.NIUMA_PLAYWRIGHT_STATE_DIR = tmp;
  try {
    const { chromium } = await import("playwright");
    const browser = await chromium.launch({
      headless: true,
      executablePath: browserExecutablePath(),
    });
    await browser.close();
    process.stdout.write("ok\n");
  } finally {
    await rm(tmp, { recursive: true, force: true });
  }
}

function browserExecutablePath() {
  const configured = productEnv("PLAYWRIGHT_CHROMIUM_PATH");
  if (configured) {
    return configured;
  }
  const candidates = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ];
  for (const candidate of candidates) {
    if (existsSync(candidate)) {
      return candidate;
    }
  }
  return undefined;
}

main().catch(error => {
  process.stderr.write(`${error && error.stack ? error.stack : error}\n`);
  process.exit(1);
});
