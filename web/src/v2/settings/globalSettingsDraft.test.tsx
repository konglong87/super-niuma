import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IdentityConfig, SettingsDoc } from "../../lib/types";
import { I18nProvider } from "../../lib/i18n";
import { SettingsDocumentPanel } from "./SettingsDocumentPanel";
import { SETTINGS_SECRET_SENTINEL, parseSettingsDraft, updateSettingsValue, useGlobalSettingsDraft, type GlobalSettingsDraft } from "./globalSettingsDraft";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test-token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "" };
const fixture: SettingsDoc = {
  provider: "custom", providerProtocol: "openai-responses", model: "old-model",
  responses: { stateMode: "stateless", store: false, future: true },
  env: { ANTHROPIC_API_KEY: SETTINGS_SECRET_SENTINEL, CUSTOM_SECRET: SETTINGS_SECRET_SENTINEL, CUSTOM_OPTION: "keep" },
  fallback: { enabled: false, providers: [{ name: "backup", type: "custom", apiKey: SETTINGS_SECRET_SENTINEL, future: { value: 5 } }] },
  imageGeneration: { provider: "backup", worker: { maxAttempts: 7 }, futureOption: "keep" },
  future: { nested: ["untouched"] },
};
const snapshot = (doc = fixture, revision = "revision-1") => ({ doc, revision, path: "/isolated/settings.json", exists: true, masked: ["env.ANTHROPIC_API_KEY"] });
const jsonResponse = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
let host: HTMLDivElement;
let root: Root;
let draft: GlobalSettingsDraft;
let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;

function Observer({ view, currentIdentity = identity, enabled = true }: { view?: "models" | "json"; currentIdentity?: IdentityConfig; enabled?: boolean }) {
  draft = useGlobalSettingsDraft(currentIdentity, enabled);
  return view ? <I18nProvider><SettingsDocumentPanel draft={draft} view={view} /></I18nProvider> : null;
}

async function render(view?: "models" | "json", currentIdentity = identity) {
  await act(async () => root.render(<Observer view={view} currentIdentity={currentIdentity} />));
}
function openAdvancedModelEditor(): void {
  const tabs = host.querySelectorAll<HTMLButtonElement>('[role="tab"]');
  expect(tabs[1]).toBeDefined();
  act(() => tabs[1].click());
}

beforeEach(() => {
  const storage = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
    removeItem: (key: string) => storage.delete(key),
  });
  window.localStorage.setItem("niuma-webui.language.v1", "zh");
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse(snapshot()));
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
});

describe("shared global settings draft", () => {

  it("shows a localized quick form, one save action and no invented health claims", async () => {
    window.localStorage.setItem("niuma-webui.language.v1", "en");
    await render("models");
    expect(host.textContent).not.toMatch(/[\u3400-\u9fff]/);
    const quick = host.querySelector('[role="tabpanel"]:not([hidden])')!;
    expect(quick.querySelectorAll("input")).toHaveLength(3);
    expect(quick.textContent).toContain("Catalog connection not tested");
    expect(quick.textContent).not.toContain("Needs setup"); // Env credentials may be present.
    expect(quick.querySelector('.model-status-icon')?.getAttribute("data-result")).toBe("unknown");
    expect([...host.querySelectorAll("button")].filter(b => b.textContent === "Save changes")).toHaveLength(1);
    expect(draft.doc).toEqual(fixture);
    expect(draft.dirty).toBe(false);
  });

  it("does not show a fallback catalog result as a healthy primary model", async () => {
    await render("models");
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true, kind: "catalog_connection", message: "backup ok" }));
    await act(async () => { await draft.testProvider("backup"); });
    expect(host.querySelector('.model-status')?.textContent).toContain("尚未测试目录连接");
    expect(host.querySelector('.model-status')?.textContent).not.toContain("backup ok");
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true, kind: "catalog_connection", message: "catalog only" }));
    await act(async () => { await draft.testProvider(); });
    expect(host.querySelector('.model-status')?.textContent).toContain("模型目录可访问");
    expect(host.querySelector('.model-connection-test')?.textContent).toContain("不验证对话、工具或看图能力");
    act(() => draft.setField(["model"], "new-draft-model"));
    expect(host.querySelector('.model-status')?.textContent).toContain("尚未测试目录连接");
  });

  it("supports Auth Token-only and unknown providers without mutating hidden settings", async () => {
    const doc = { ...fixture, provider: "custom-provider", authToken: SETTINGS_SECRET_SENTINEL };
    fetchMock.mockResolvedValueOnce(jsonResponse(snapshot(doc)));
    await render("models");
    const quick = host.querySelector('[role="tabpanel"]:not([hidden])')!;
    expect(quick.querySelector<HTMLInputElement>('input[aria-label="Auth Token"]')?.type).toBe("password");
    expect(quick.querySelector('input[aria-label="API Key"]')).toBeNull();
    expect(quick.textContent).toContain("custom-provider");
    expect(draft.doc).toEqual(doc);
    expect(draft.dirty).toBe(false);
    act(() => draft.setField(["model"], "edited-model"));
    openAdvancedModelEditor();
    act(() => host.querySelector<HTMLButtonElement>('[role="tab"][data-section="images"]')!.click());
    act(() => host.querySelector<HTMLButtonElement>('[role="tab"][data-section="quick"]')!.click());
    expect(draft.doc).toEqual({ ...doc, model: "edited-model" });
  });

  it("returns quick-form protocol actions to the primary provider, not the last fallback", async () => {
    await render("models");
    openAdvancedModelEditor();
    act(() => host.querySelectorAll<HTMLButtonElement>(".global-settings-provider-list button")[1].click());
    expect(host.querySelector(".global-settings-provider-heading")?.textContent).toContain("backup");
    act(() => host.querySelector<HTMLButtonElement>('[role="tab"][data-section="quick"]')!.click());
    act(() => host.querySelector<HTMLButtonElement>(".model-advanced-hint > button")!.click());
    expect(host.querySelector(".global-settings-provider-heading")?.textContent).toContain("全局主模型");
    expect(draft.dirty).toBe(false);
  });

  it("opens the requested capability without enabling it and supports keyboard tabs", async () => {
    await render("models");
    const quick = host.querySelector('[role="tabpanel"]:not([hidden])')!;
    const computer = [...quick.querySelectorAll<HTMLButtonElement>('.model-capability-list button')][1];
    act(() => computer.click());
    const selected = host.querySelector<HTMLButtonElement>('[role="tab"][aria-selected="true"]')!;
    expect(selected.dataset.section).toBe("computer");
    expect(document.activeElement).toBe(selected);
    act(() => selected.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(host.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.dataset.section).toBe("images");
    expect(draft.doc).toEqual(fixture);
    expect(draft.dirty).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("updates form and JSON in both directions while retaining unknown fields and masked env keys", async () => {
    await render("models");
    openAdvancedModelEditor();
    act(() => draft.setField(["model"], "form-model"));
    await render("json");
    expect((host.querySelector("textarea") as HTMLTextAreaElement).value).toContain('"model": "form-model"');
    const fromJSON = { ...JSON.parse(draft.raw), model: "json-model", brandNew: { nested: true } };
    act(() => draft.setRaw(JSON.stringify(fromJSON)));
    await render("models");
    openAdvancedModelEditor();
    expect((host.querySelector('input[aria-label="默认模型"]') as HTMLInputElement).value).toBe("json-model");
    expect(draft.doc?.env).toEqual(fixture.env);
    expect(draft.doc?.future).toEqual(fixture.future);
    expect(draft.doc?.brandNew).toEqual({ nested: true });
    expect(draft.dirty).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("retains invalid JSON across tabs and blocks form mutation, validation, save and connection test", async () => {
    await render("json");
    const raw = '{"model":"unfinished",';
    act(() => draft.setRaw(raw));
    await render("models");
    expect(host.textContent).toContain("JSON 语法错误");
    expect((host.querySelector("fieldset") as HTMLFieldSetElement).disabled).toBe(true);
    act(() => draft.setField(["model"], "should-not-replace"));
    await act(async () => {
      expect(await draft.validate()).toBe(false);
      expect(await draft.save()).toBe(false);
      expect(await draft.testProvider()).toBe(false);
    });
    await render("json");
    expect((host.querySelector("textarea") as HTMLTextAreaElement).value).toBe(raw);
    expect(draft.dirty).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("retains a draft while document tabs are inactive and fetches lazily", async () => {
    await act(async () => root.render(<Observer enabled={false} />));
    expect(fetchMock).not.toHaveBeenCalled();
    await render();
    act(() => draft.setRaw("incomplete JSON"));
    await act(async () => root.render(<Observer enabled={false} />));
    expect(draft.raw).toBe("incomplete JSON");
    await render("json");
    expect(draft.raw).toBe("incomplete JSON");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("sends validation then conditional save then readback, keeping only the returned masked document", async () => {
    const storage = vi.spyOn(window.localStorage, "setItem");
    await render();
    act(() => draft.setField(["env", "ANTHROPIC_API_KEY"], "new-secret"));
    fetchMock.mockResolvedValueOnce(jsonResponse({ valid: true, issues: [] }))
      .mockResolvedValueOnce(jsonResponse({ saved: true, revision: "revision-2", requires_restart: true }))
      .mockResolvedValueOnce(jsonResponse(snapshot(fixture, "revision-2")));
    await act(async () => { expect(await draft.save()).toBe(true); });
    const calls = fetchMock.mock.calls.slice(1);
    expect(calls.map(([url, init]) => [url, init?.method])).toEqual([
      ["/api/runtime/settings/validate", "POST"], ["/api/runtime/settings", "PUT"], ["/api/runtime/settings", "GET"],
    ]);
    expect(new Headers(calls[1][1]?.headers).get("If-Match")).toBe("revision-1");
    const savedDoc = JSON.parse(calls[1][1]?.body as string);
    expect(savedDoc.env.ANTHROPIC_API_KEY).toBe("new-secret");
    expect(savedDoc.env.CUSTOM_SECRET).toBe(SETTINGS_SECRET_SENTINEL);
    expect(savedDoc.future).toEqual(fixture.future);
    expect(draft.raw).not.toContain("new-secret");
    expect(draft.dirty).toBe(false);
    expect(draft.requiresRestart).toBe(true);
    expect(draft.revision).toBe("revision-2");
    expect(storage).not.toHaveBeenCalled();
  });

  it("preserves a conflicting draft and never retries an unconditional overwrite", async () => {
    await render();
    act(() => draft.setField(["model"], "my-model"));
    fetchMock.mockResolvedValueOnce(jsonResponse({ valid: true, issues: [] }))
      .mockResolvedValueOnce(jsonResponse({ error: "settings_conflict", revision: "revision-2" }, 409));
    await act(async () => { expect(await draft.save()).toBe(false); });
    expect(draft.conflict).toBe(true);
    expect(draft.doc?.model).toBe("my-model");
    expect(draft.dirty).toBe(true);
    act(() => draft.setField(["model"], "another-draft-model"));
    expect(draft.error).toContain("服务端配置已变更");
    await act(async () => { expect(await draft.save()).toBe(false); });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    act(() => draft.reset());
    expect(draft.conflict).toBe(true);
  });

  it("keeps the draft when validation fails without issuing PUT", async () => {
    await render();
    act(() => draft.setField(["providerProtocol"], "invalid"));
    fetchMock.mockResolvedValueOnce(jsonResponse({ valid: false, issues: [{ field: "providerProtocol", code: "invalid_protocol", message: "unsupported protocol" }] }));
    await act(async () => { expect(await draft.save()).toBe(false); });
    expect(draft.validation?.valid).toBe(false);
    expect(draft.doc?.providerProtocol).toBe("invalid");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("shows validation progress and the result in the model settings footer", async () => {
    let finish!: (response: Response) => void;
    await render("models");
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { finish = resolve; }));

    act(() => (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("校验")) as HTMLButtonElement)?.click());
    expect(host.querySelector(".global-settings-footer-status")?.textContent).toBe("校验中…");
    expect((host.querySelector('button[aria-busy="true"]') as HTMLButtonElement).disabled).toBe(true);

    await act(async () => finish(jsonResponse({ valid: true, issues: [] })));
    await vi.waitFor(() => expect(host.querySelector(".global-settings-footer-status")?.textContent).toContain("配置校验通过"));
    expect(host.querySelector(".global-settings-footer-status")?.textContent).toBe("配置校验通过");
  });

  it("does not claim success or clear the draft when save readback fails", async () => {
    await render();
    act(() => draft.setField(["model"], "new-model"));
    fetchMock.mockResolvedValueOnce(jsonResponse({ valid: true, issues: [] }))
      .mockResolvedValueOnce(jsonResponse({ saved: true, revision: "revision-2" }))
      .mockRejectedValueOnce(new TypeError("network failed"));
    await act(async () => { expect(await draft.save()).toBe(false); });
    expect(draft.saved).toBe(false);
    expect(draft.error).toContain("读取确认失败");
    expect(draft.doc?.model).toBe("new-model");
    expect(draft.dirty).toBe(true);
  });

  it("detects another writer between save and readback", async () => {
    await render();
    act(() => draft.setField(["model"], "new-model"));
    fetchMock.mockResolvedValueOnce(jsonResponse({ valid: true, issues: [] }))
      .mockResolvedValueOnce(jsonResponse({ saved: true, revision: "revision-2" }))
      .mockResolvedValueOnce(jsonResponse(snapshot({ model: "other-writer" }, "revision-3")));
    await act(async () => { expect(await draft.save()).toBe(false); });
    expect(draft.conflict).toBe(true);
    expect(draft.doc?.model).toBe("new-model");
    expect(draft.saved).toBe(false);
  });

  it("tests the selected named provider with the unsaved document and invalidates stale results on edits", async () => {
    await render();
    act(() => draft.setField(["fallback", "providers", 0, "model"], "draft-backup"));
    fetchMock.mockResolvedValueOnce(jsonResponse({ ok: true, kind: "catalog_connection", status_code: 200, message: "catalog available" }));
    await act(async () => { expect(await draft.testProvider("backup")).toBe(true); });
    expect(fetchMock.mock.calls[1][0]).toBe("/api/runtime/settings/test-provider");
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string)).toMatchObject({ provider: "backup", doc: { fallback: { providers: [{ model: "draft-backup" }] } } });
    expect(draft.connection?.ok).toBe(true);
    expect(draft.dirty).toBe(true);
    act(() => draft.setField(["model"], "another-model"));
    expect(draft.connection).toBeNull();
  });

  it("promotes a fallback provider into the primary route without saving", async () => {
    const document: SettingsDoc = {
      provider: "custom",
      providerProtocol: "openai-responses",
      baseURL: "https://primary.example/v1",
      apiKey: SETTINGS_SECRET_SENTINEL,
      model: "old-model",
      fallback: {
        providers: [{
          name: "glm",
          type: "openai-compatible",
          protocol: "openai-chat-completions",
          baseURL: "https://glm.example/v1",
          apiKey: SETTINGS_SECRET_SENTINEL,
          model: "glm-5.2",
          future: { keep: true },
        }],
      },
      futureRoot: { keep: "yes" },
    };
    fetchMock.mockResolvedValueOnce(jsonResponse(snapshot(document)));
    await render("models");
    openAdvancedModelEditor();
    act(() => (host.querySelectorAll(".global-settings-provider-list button")[1] as HTMLButtonElement).click());
    expect(host.querySelector(".global-settings-promote")).not.toBeNull();
    fetchMock.mockResolvedValueOnce(jsonResponse({
      doc: {
        ...document,
        provider: "openai-compatible",
        providerProtocol: "openai-chat-completions",
        baseURL: "https://glm.example/v1",
        apiKey: SETTINGS_SECRET_SENTINEL,
        model: "glm-5.2",
      },
      masked: ["apiKey", "fallback.providers.0.apiKey"],
      revision: "revision-1",
    }));
    await act(async () => { expect(await draft.promoteProvider(0)).toBe(true); });
    const call = fetchMock.mock.calls[1];
    expect(call[0]).toBe("/api/runtime/settings/promote-provider");
    expect(JSON.parse(call[1]?.body as string)).toMatchObject({ provider_index: 0, doc: { fallback: { providers: [{ model: "glm-5.2" }] } } });
    expect(draft.doc?.model).toBe("glm-5.2");
    expect(draft.doc?.futureRoot).toEqual({ keep: "yes" });
    expect(host.querySelector(".global-settings-promote")).toBeNull();
    expect(host.textContent).toContain("已设为主模型");
    expect(draft.dirty).toBe(true);
    fetchMock.mockResolvedValueOnce(jsonResponse({ valid: true, issues: [] }))
      .mockResolvedValueOnce(jsonResponse({ saved: true, revision: "revision-2", requires_restart: true }))
      .mockResolvedValueOnce(jsonResponse(snapshot({ ...draft.doc!, model: "glm-5.2" }, "revision-2")));
    await act(async () => { expect(await draft.save()).toBe(true); });
    expect(new Headers(fetchMock.mock.calls[2][1]?.headers).get("X-Settings-Promoted-Provider-Index")).toBe("0");
    expect(new Headers(fetchMock.mock.calls[3][1]?.headers).get("X-Settings-Promoted-Provider-Index")).toBe("0");
  });

  it("does not probe the primary model for an unnamed fallback provider", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(snapshot({ fallback: { providers: [{ type: "custom", model: "unnamed" }] } })));
    await render("models");
    openAdvancedModelEditor();
    const providerButtons = host.querySelectorAll(".global-settings-provider-list button");
    act(() => (providerButtons[1] as HTMLButtonElement).click());
    expect(host.textContent).toContain("请先填写供应商名称");
    expect((host.querySelector(".global-settings-connection button") as HTMLButtonElement).disabled).toBe(true);
    await act(async () => {
      expect(await draft.testProvider("")).toBe(false);
      expect(await draft.testProvider("   ")).toBe(false);
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("keeps draft credentials out of React duplicate-key warnings", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    const provider = { type: "custom", apiKey: "private-draft-credential" };
    fetchMock.mockResolvedValueOnce(jsonResponse(snapshot({ fallback: { providers: [provider, provider] } })));
    await render("models");
    openAdvancedModelEditor();
    expect(errors.mock.calls.flat().join(" ")).not.toContain("private-draft-credential");
    expect(errors.mock.calls.flat().join(" ")).not.toContain("same key");
  });

  it("keeps standard fallback separate and lets users opt providers into Computer Use screenshots", async () => {
    const doc = {
      ...fixture,
      model: "primary-vision",
      fallback: {
        enabled: true,
        providers: [
          { name: "backup-vision", type: "custom", model: "backup-vision" },
          { name: "backup-text", type: "custom", model: "backup-text" },
        ],
      },
      computerUse: { imageInputRoutes: [{ provider: "primary", model: "primary-vision" }] },
    };
    fetchMock.mockResolvedValueOnce(jsonResponse(snapshot(doc)));
    await render("models");
    openAdvancedModelEditor();
    expect(host.textContent).toContain("普通模型 Fallback 链");
    expect(host.textContent).toContain("Computer Use 桌面操作");
    expect(host.textContent).toContain("普通请求和 Computer Use 分开配置");
    act(() => host.querySelector<HTMLButtonElement>('[role="tab"][data-section="computer"]')!.click());
    const routes = host.querySelectorAll<HTMLInputElement>(".global-settings-computer-route input");
    expect(routes).toHaveLength(3);
    expect(routes[0].checked).toBe(true);
    expect(routes[1].checked).toBe(false);
    expect(routes[2].checked).toBe(false);
    act(() => routes[1].click());
    expect(draft.doc?.computerUse).toEqual({ imageInputRoutes: [
      { provider: "primary", model: "primary-vision" },
      { provider: "backup-vision", model: "backup-vision" },
    ] });
    act(() => routes[0].click());
    expect(draft.doc?.computerUse).toEqual({ imageInputRoutes: [{ provider: "backup-vision", model: "backup-vision" }] });
    expect(draft.doc?.fallback).toEqual(doc.fallback);
  });

  it("renders English model fields, JSON errors and confirmation dialogs", async () => {
    window.localStorage.setItem("niuma-webui.language.v1", "en");
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await render("models");
    openAdvancedModelEditor();
    expect(host.textContent).toContain("Primary model");
    expect(host.textContent).toContain("Image generation");
    expect(host.textContent).not.toMatch(/[\u3400-\u9fff]/);
    act(() => draft.setRaw("invalid JSON"));
    await render("json");
    expect(host.textContent).toContain("JSON syntax error");
    expect(host.textContent).not.toMatch(/[\u3400-\u9fff]/);
    act(() => (host.querySelector('button[title="Reset draft"]') as HTMLButtonElement).click());
    expect(confirm).toHaveBeenCalledWith("Discard unsaved changes and restore the last loaded configuration?");
  });

  it("loads a new identity without carrying credentials or draft changes from the previous identity", async () => {
    await render();
    act(() => draft.setField(["env", "ANTHROPIC_API_KEY"], "private-draft"));
    fetchMock.mockResolvedValueOnce(jsonResponse(snapshot({ model: "other-tenant" })));
    await render(undefined, { ...identity, tenantKey: "other" });
    expect(draft.raw).not.toContain("private-draft");
    expect(draft.doc?.model).toBe("other-tenant");
    expect(draft.dirty).toBe(false);
  });

  it("requires confirmation before resetting and supports cancel", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await render("json");
    act(() => draft.setRaw("invalid draft"));
    const reset = host.querySelector('button[title="重置草稿"]') as HTMLButtonElement;
    act(() => reset.click());
    expect(draft.raw).toBe("invalid draft");
    confirm.mockReturnValue(true);
    act(() => reset.click());
    expect(draft.dirty).toBe(false);
    expect(draft.doc).toEqual(fixture);
  });
});

describe("document updates", () => {
  it.each(["null", "[]", "true", '"text"'])("rejects non-object JSON %s", (raw) => {
    expect(parseSettingsDraft(raw).doc).toBeNull();
    expect(parseSettingsDraft(raw).syntaxError).toContain("根节点");
  });

  it("does not expose secret fragments from syntax errors", () => {
    const parsed = parseSettingsDraft('{"apiKey": "private-secret" invalid }');
    expect(parsed.syntaxError).toContain("JSON 语法错误");
    expect(parsed.syntaxError).not.toContain("private-secret");
  });

  it("updates array entries without replacing siblings and removes only the selected optional field", () => {
    const changed = updateSettingsValue(fixture, ["fallback", "providers", 0, "model"], "new");
    expect(changed.fallback).toMatchObject({ providers: [{ name: "backup", model: "new", apiKey: SETTINGS_SECRET_SENTINEL, future: { value: 5 } }] });
    expect(changed.imageGeneration).toEqual(fixture.imageGeneration);
    const removed = updateSettingsValue(changed, ["responses", "store"], undefined);
    expect(removed.responses).toEqual({ stateMode: "stateless", future: true });
    expect(fixture.responses).toEqual({ stateMode: "stateless", store: false, future: true });
  });
});
