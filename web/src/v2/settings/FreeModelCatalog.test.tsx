import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FreeModelProvider } from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig, SettingsDoc } from "../../lib/types";
import { FreeModelCatalog, sortFreeProviders, unverifiedReasons, upsertFallbackProvider } from "./FreeModelCatalog";
import { useGlobalSettingsDraft, type GlobalSettingsDraft } from "./globalSettingsDraft";

const openExternalURL = vi.fn<(url: string) => Promise<void>>();
vi.mock("../../lib/openExternal", () => ({ openExternalURL: (url: string) => openExternalURL(url) }));

const identity: IdentityConfig = { apiBase: "/api", apiToken: "t", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "" };
const model = (id: string, tool = "supported") => ({ id, capabilities: ["文本生成"], tool_calling: tool });
const provider = (slug: string, extra: Partial<FreeModelProvider> = {}): FreeModelProvider => ({
  slug, name: `name-${slug}`, base_url: `https://${slug}.example/v1`, openai_compatible: "supported", status: "ok", unverified: false,
  free: { type: "permanent", note: "free-note" }, requirements: { credit_card: "none", real_name: "required", region: "unknown" },
  signup_url: `https://${slug}.example/signup`, recommended_model: model(`${slug}-model`), usage_notes: [`note-${slug}`], ...extra,
});
const catalog = [provider("google-ai-studio"), provider("zhipu-glm"), provider("groq", { status: "pending", unverified: true }), provider("siliconflow")];
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;
let draft: GlobalSettingsDraft;
let settings: SettingsDoc;
let testResponse: unknown;
let calls: Array<{ url: string; method: string; body: unknown }>;

function Harness() {
  draft = useGlobalSettingsDraft(identity, true);
  return <I18nProvider><FreeModelCatalog draft={draft} /></I18nProvider>;
}
async function render(language: "zh" | "en" = "zh") {
  window.localStorage.setItem("niuma-webui.language.v1", language);
  await act(async () => root.render(<Harness />));
  await act(async () => { await Promise.resolve(); });
}
const card = (slug: string) => host.querySelector<HTMLElement>(`[data-slug="${slug}"]`)!;
async function verify(slug: string, key = "sk-secret") {
  const input = card(slug).querySelector<HTMLInputElement>('input[type="password"]')!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, key);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  const button = card(slug).querySelector<HTMLButtonElement>("button.primary")!;
  await act(async () => { button.click(); });
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
}
const putBody = () => calls.find((c) => c.method === "PUT")?.body as SettingsDoc | undefined;

beforeEach(() => {
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", { getItem: (k: string) => storage.get(k) ?? null, setItem: (k: string, v: string) => storage.set(k, v), removeItem: (k: string) => storage.delete(k) });
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  settings = {};
  testResponse = { ok: true, kind: "chat_connection", message: "ok", tool_support: "supported" };
  calls = [];
  openExternalURL.mockReset().mockResolvedValue(undefined);
  vi.stubGlobal("fetch", vi.fn<typeof fetch>(async (input, init) => {
    const url = String(input);
    const method = init?.method ?? "GET";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
    calls.push({ url, method, body });
    if (url.endsWith("/runtime/free-models")) return json({ providers: catalog });
    if (url.endsWith("/runtime/settings/test-provider")) return json(testResponse);
    if (url.endsWith("/runtime/settings/validate")) return json({ valid: true, issues: [] });
    if (method === "PUT") { settings = body as SettingsDoc; return json({ revision: "r2" }); }
    return json({ doc: settings, revision: method === "GET" && calls.some((c) => c.method === "PUT") ? "r2" : "r1", path: "/s.json", exists: true, masked: [] });
  }));
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
});

describe("catalog helpers", () => {
  it("explains each unverified cause", () => {
    expect(unverifiedReasons(provider("a", { status: "pending" }))).toEqual(["注册状态待确认（目录状态为 pending）。"]);
    expect(unverifiedReasons(provider("a", { openai_compatible: "unverified" }))).toEqual(["OpenAI 兼容接口未核实。"]);
    expect(unverifiedReasons(provider("a", { recommended_model: undefined }))).toEqual(["没有可用的推荐模型。"]);
    expect(unverifiedReasons(provider("a", { recommended_model: model("m", "pending") }))).toEqual(["推荐模型的工具调用支持未核实。"]);
  });
  it("orders domestic first in zh and overseas first in en", () => {
    expect(sortFreeProviders(catalog, "zh").map((p) => p.slug)).toEqual(["zhipu-glm", "siliconflow", "google-ai-studio", "groq"]);
    expect(sortFreeProviders(catalog, "en").map((p) => p.slug)).toEqual(["google-ai-studio", "groq", "zhipu-glm", "siliconflow"]);
  });
  it("replaces a provider with the same slug instead of duplicating it", () => {
    const list = upsertFallbackProvider([{ name: "a", model: "old", extra: 1 }, { name: "b" }], { name: "a", model: "new" });
    expect(list).toEqual([{ name: "a", model: "new", extra: 1 }, { name: "b" }]);
  });
});

describe("FreeModelCatalog", () => {
  it("renders cards in zh order with unverified reasons and the Google region warning", async () => {
    await render("zh");
    const slugs = [...host.querySelectorAll<HTMLElement>("article")].map((a) => a.dataset.slug);
    expect(slugs).toEqual(["zhipu-glm", "siliconflow", "google-ai-studio", "groq"]);
    expect(card("groq").textContent).toContain("未核实");
    expect(card("groq").textContent).toContain("注册状态待确认");
    expect(card("zhipu-glm").textContent).not.toContain("未核实");
    expect(card("google-ai-studio").textContent).toContain("中国大陆不支持");
    expect(card("zhipu-glm").textContent).toContain("note-zhipu-glm");
    expect(card("zhipu-glm").textContent).toContain("zhipu-glm-model");
  });

  it("puts overseas providers first in the English UI", async () => {
    await render("en");
    expect([...host.querySelectorAll<HTMLElement>("article")][0].dataset.slug).toBe("google-ai-studio");
    expect(host.textContent).toContain("Free models");
  });

  it("opens the sign-up page and reports a failure", async () => {
    await render();
    const open = () => [...card("zhipu-glm").querySelectorAll("button")].find((b) => b.textContent?.includes("打开注册页"))!;
    await act(async () => { open().click(); });
    expect(openExternalURL).toHaveBeenCalledWith("https://zhipu-glm.example/signup");
    expect(card("zhipu-glm").querySelector('[role="alert"]')).toBeNull();
    openExternalURL.mockRejectedValueOnce(new Error("blocked"));
    await act(async () => { open().click(); });
    expect(card("zhipu-glm").querySelector('[role="alert"]')?.textContent).toContain("打开注册页失败");
  });

  it.each([
    ["key_invalid", "Key 无效"],
    ["quota_or_rate_limit", "额度已用完"],
    ["model_not_found", "找不到推荐模型"],
    ["network", "网络不通"],
    ["chat_failed", "对话测试失败"],
    ["catalog_failed", "无法读取该服务的模型目录"],
  ])("shows a clear hint for reason %s and saves nothing", async (reason, text) => {
    testResponse = { ok: false, kind: "chat_connection", message: "raw-secret-text", reason };
    await render();
    await verify("zhipu-glm");
    expect(card("zhipu-glm").textContent).toContain(text);
    expect(card("zhipu-glm").textContent).not.toContain("raw-secret-text");
    expect(putBody()).toBeUndefined();
  });

  it("writes the primary model when none is configured, and masks nothing in logs", async () => {
    const log = vi.spyOn(console, "log");
    const error = vi.spyOn(console, "error");
    await render();
    await verify("zhipu-glm", "sk-secret");
    expect(calls.find((c) => c.url.endsWith("test-provider"))?.body).toMatchObject({ doc: { provider: "openai-compatible", model: "zhipu-glm-model", baseURL: "https://zhipu-glm.example/v1" } });
    expect(putBody()).toMatchObject({ provider: "openai-compatible", baseURL: "https://zhipu-glm.example/v1", model: "zhipu-glm-model", apiKey: "sk-secret" });
    expect(putBody()?.fallback).toBeUndefined();
    expect(card("zhipu-glm").textContent).toContain("已设为主模型并保存");
    expect(card("zhipu-glm").textContent).toContain("已配置为主模型");
    expect(JSON.stringify([...log.mock.calls, ...error.mock.calls])).not.toContain("sk-secret");
    expect((card("zhipu-glm").querySelector('input[type="password"]') as HTMLInputElement).value).toBe("");
  });

  it("appends to the fallback chain when a primary model exists", async () => {
    settings = { provider: "custom", model: "main", baseURL: "https://main.example", fallback: { enabled: false, providers: [{ name: "keep", type: "custom" }] } };
    await render();
    await verify("siliconflow", "sk-secret");
    const body = putBody()!;
    expect(body.model).toBe("main");
    expect(body.fallback).toEqual({ enabled: true, providers: [{ name: "keep", type: "custom" }, { name: "siliconflow", type: "openai-compatible", baseURL: "https://siliconflow.example/v1", model: "siliconflow-model", apiKey: "sk-secret" }] });
    expect(calls.find((c) => c.url.endsWith("test-provider"))?.body).toMatchObject({ provider: "siliconflow" });
    expect(card("siliconflow").textContent).toContain("已加入备用链并保存");
  });

  it("updates an existing fallback entry with the same slug", async () => {
    settings = { provider: "custom", model: "main", baseURL: "https://main.example", fallback: { enabled: true, providers: [{ name: "siliconflow", type: "openai-compatible", model: "old", apiKey: "••••••" }] } };
    await render();
    expect(card("siliconflow").textContent).toContain("已在备用链中");
    await verify("siliconflow", "sk-new");
    const providers = (putBody()!.fallback as { providers: Array<Record<string, unknown>> }).providers;
    expect(providers).toHaveLength(1);
    expect(providers[0]).toMatchObject({ name: "siliconflow", model: "siliconflow-model", apiKey: "sk-new" });
  });

  it("warns when tools are unsupported but still saves", async () => {
    testResponse = { ok: true, kind: "chat_connection", message: "ok", tool_support: "unsupported" };
    await render();
    await verify("zhipu-glm");
    expect(card("zhipu-glm").textContent).toContain("不支持工具调用");
    expect(putBody()?.model).toBe("zhipu-glm-model");
  });
});
