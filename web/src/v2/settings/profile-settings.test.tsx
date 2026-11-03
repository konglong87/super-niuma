import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "../../lib/api";
import { defaultAgentProfileDocument } from "../../lib/agentProfiles";
import { I18nProvider, useI18n } from "../../lib/i18n";
import type { AgentProfileRecord, IdentityConfig } from "../../lib/types";
import { AgentSettingsPanel } from "./AgentSettingsPanel";
import { ProfileSettingsPanel } from "./ProfileSettingsPanel";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  listAgentProfiles: vi.fn(), listChannelAccounts: vi.fn(), getAgentProfileBinding: vi.fn(),
  getAgentProfileAssignment: vi.fn(), saveAgentProfileAssignment: vi.fn(),
  saveAgentProfile: vi.fn(), validateAgentProfile: vi.fn(), publishAgentProfile: vi.fn(),
  rollbackAgentProfile: vi.fn(),
  saveAgentProfileBinding: vi.fn(), archiveAgentProfileBinding: vi.fn(),
}));

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "isolated-test", userId: "test", deviceId: "test", role: "owner", model: "test" };
const published: AgentProfileRecord = { id: 10, profile_key: "test-agent", profile_version: 1, status: "published", scope: "tenant_shared", display_name: "Real Test Agent", description: "Test profile", config_json: JSON.stringify(defaultAgentProfileDocument()) };
const draft: AgentProfileRecord = { ...published, id: 11, profile_version: 2, status: "draft" };

describe("v2 profile settings API workflows", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listAgentProfiles).mockResolvedValue([published, draft]);
    vi.mocked(api.listChannelAccounts).mockResolvedValue([]);
    vi.mocked(api.getAgentProfileBinding).mockRejectedValue(new api.ApiError(404, "not found"));
    vi.mocked(api.getAgentProfileAssignment).mockImplementation(async (_identity, surface) => ({ surface, profile_id: 10 }));
    vi.mocked(api.validateAgentProfile).mockResolvedValue({ valid: true });
    vi.mocked(api.publishAgentProfile).mockResolvedValue({ profile_key: draft.profile_key, profile_version: 2, status: "published" });
    vi.mocked(api.saveAgentProfile).mockImplementation(async (_identity, request) => ({ ...draft, display_name: request.display_name, config_json: JSON.stringify(request.config) }));
    host = document.createElement("div"); document.body.replaceChildren(host); root = createRoot(host);
  });
  afterEach(() => { act(() => root.unmount()); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
  async function mount(kind: "profiles" | "agent", onDirtyChange = vi.fn()) {
    await act(async () => { root.render(<I18nProvider>{kind === "profiles" ? <ProfileSettingsPanel identity={identity} onDirtyChange={onDirtyChange} /> : <AgentSettingsPanel identity={identity} onDirtyChange={onDirtyChange} />}</I18nProvider>); });
    return onDirtyChange;
  }
  const button = (label: string) => Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find((element) => element.textContent?.trim() === label)!;
  async function click(label: string) { await act(async () => button(label).click()); }
  async function change(element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
    await act(async () => {
      const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(element, value);
      element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
    });
  }

  it("uses only server catalog and protects metadata drafts during switching", async () => {
    const dirty = await mount("profiles");
    expect(api.listAgentProfiles).toHaveBeenCalledWith(identity);
    expect(host.querySelector(".settings-profile-catalog")?.textContent).toContain("Real Test Agent");
    expect(host.querySelector(".settings-profile-catalog")?.textContent).not.toContain("Copywriter");
    await click("editor");
    const name = Array.from(host.querySelectorAll("label")).find((element) => element.textContent === "Display name")!.querySelector("input")!;
    await change(name, "Changed name");
    expect(dirty).toHaveBeenLastCalledWith(true);
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await click("New profile");
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(name.value).toBe("Changed name");
    await click("Save draft");
    expect(api.saveAgentProfile).toHaveBeenCalledWith(identity, expect.objectContaining({ display_name: "Changed name", config: expect.objectContaining({ identity: expect.objectContaining({ display_name: "Changed name" }) }) }), "test-agent");
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it("blocks invalid JSON and requires saving before publish", async () => {
    await mount("profiles"); await click("editor");
    await change(host.querySelector<HTMLTextAreaElement>('[aria-label="Profile JSON"]')!, "{}");
    expect(host.textContent).toContain("Profile sections are required");
    await click("Validate");
    expect(api.validateAgentProfile).not.toHaveBeenCalled();
    await click("preview");
    expect(button("Publish").disabled).toBe(true);
    expect(api.publishAgentProfile).not.toHaveBeenCalled();
  });

  it("keeps incomplete stored profiles visible and preserves their original JSON for repair", async () => {
    const raw = '{"schema_version":1,"identity":{"display_name":"Incomplete"}}';
    vi.mocked(api.listAgentProfiles).mockResolvedValue([{ ...draft, config_json: raw }]);
    await mount("profiles");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Profile configuration is incomplete");
    await click("editor");
    expect(host.querySelector<HTMLTextAreaElement>('[aria-label="Profile JSON"]')?.value).toBe(raw);
    await click("Save draft");
    expect(api.saveAgentProfile).not.toHaveBeenCalled();
    await change(host.querySelector<HTMLTextAreaElement>('[aria-label="Profile JSON"]')!, JSON.stringify(defaultAgentProfileDocument()));
    await click("Save draft");
    expect(api.saveAgentProfile).toHaveBeenCalledTimes(1);
  });

  it("keeps environment switching busy until a bot binding write completes", async () => {
    const binding = { account_id: 7, provider: "feishu", binding_key: "test-bot", status: "active" };
    vi.mocked(api.listChannelAccounts).mockResolvedValue([{ id: 7, account_key: "test-bot", provider: "feishu", app_id: "test", mode: "websocket", enabled: true, status: "active" }]);
    vi.mocked(api.getAgentProfileBinding).mockResolvedValue(binding);
    let finish!: (value: typeof binding) => void;
    vi.mocked(api.saveAgentProfileBinding).mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const busy = vi.fn();
    await act(async () => { root.render(<I18nProvider><ProfileSettingsPanel identity={identity} onBusyChange={busy} /></I18nProvider>); });
    await click("binding"); await click("Bind bot");
    expect(busy).toHaveBeenLastCalledWith(true);
    expect(button("Bind bot").disabled).toBe(true);
    expect(button("Archive binding").disabled).toBe(true);
    await act(async () => finish(binding));
    expect(busy).toHaveBeenLastCalledWith(false);
    expect(button("Bind bot").disabled).toBe(false);
  });

  it("validates the saved version before publishing and reloads real catalog", async () => {
    await mount("profiles"); await click("preview"); await click("Publish");
    expect(api.validateAgentProfile).toHaveBeenCalledWith(identity, expect.objectContaining({ profile_key: "test-agent" }), "test-agent");
    expect(api.publishAgentProfile).toHaveBeenCalledWith(identity, "test-agent", 2);
    expect(api.listAgentProfiles).toHaveBeenCalledTimes(2);
  });

  it("copies a template into a new unsaved profile and creates it through POST contract", async () => {
    const dirty = await mount("profiles"); await click("Copy");
    expect(dirty).toHaveBeenLastCalledWith(true);
    expect(host.querySelector<HTMLInputElement>(".profile-form-grid input")?.value).toMatch(/^test-agent-copy-/);
    await click("Save draft");
    expect(api.saveAgentProfile).toHaveBeenCalledWith(identity, expect.objectContaining({ scope: "user_private", status: "draft", profile_version: 1, display_name: "Real Test Agent copy" }), undefined);
  });

  it("creates rollback drafts through the existing version endpoint", async () => {
    vi.mocked(api.rollbackAgentProfile).mockResolvedValue({ ...draft, id: 12, profile_version: 3 });
    await mount("profiles"); await click("versions"); await click("Rollback");
    expect(api.rollbackAgentProfile).toHaveBeenCalledWith(identity, "test-agent", 2);
    expect(host.textContent).toContain("Profile editor");
    expect(host.textContent).toContain("Rollback draft created");
  });

  it("assigns a published version id and confirms writes with readback", async () => {
    const alternative = { ...published, id: 20, profile_key: "other-agent", display_name: "Other Agent" };
    vi.mocked(api.listAgentProfiles).mockResolvedValue([published, draft, alternative]);
    const dirty = await mount("agent");
    const select = host.querySelector<HTMLSelectElement>("#assignment-web_chat")!;
    expect(Array.from(select.options).map((option) => option.value)).toEqual(["0", "10", "20"]);
    await change(select, "20"); expect(dirty).toHaveBeenLastCalledWith(true);
    vi.mocked(api.getAgentProfileAssignment).mockResolvedValueOnce({ surface: "web_chat", profile_id: 20 });
    await click("Save assignments");
    expect(api.saveAgentProfileAssignment).toHaveBeenCalledWith(identity, { surface: "web_chat", profile_id: 20 });
    expect(api.getAgentProfileAssignment).toHaveBeenLastCalledWith(identity, "web_chat");
    expect(dirty).toHaveBeenLastCalledWith(false);
    expect(host.querySelector(".settings-agent-overview")?.textContent).toContain("Other Agent");
  });

  it("keeps assignment errors distinct from unassigned surfaces", async () => {
    vi.mocked(api.getAgentProfileAssignment).mockImplementation(async (_identity, surface) => { throw new api.ApiError(surface === "web_chat" ? 403 : 404, "unavailable"); });
    await mount("agent");
    expect(host.querySelector<HTMLSelectElement>("#assignment-web_chat")?.disabled).toBe(true);
    expect(host.querySelector<HTMLSelectElement>("#assignment-mobile_chat")?.disabled).toBe(false);
    expect(host.textContent).toContain("Load failed");
    expect(host.textContent).toContain("Not assigned");
    expect(api.saveAgentProfileAssignment).not.toHaveBeenCalled();
  });

  it("retains only pending assignment edits after a partial write failure", async () => {
    const alternative = { ...published, id: 20, profile_key: "other-agent", display_name: "Other Agent" };
    vi.mocked(api.listAgentProfiles).mockResolvedValue([published, alternative]);
    const dirty = await mount("agent");
    await change(host.querySelector<HTMLSelectElement>("#assignment-web_chat")!, "20");
    await change(host.querySelector<HTMLSelectElement>("#assignment-mobile_chat")!, "20");
    vi.mocked(api.getAgentProfileAssignment).mockResolvedValueOnce({ surface: "web_chat", profile_id: 20 });
    vi.mocked(api.saveAgentProfileAssignment).mockResolvedValueOnce({ surface: "web_chat", profile_id: 20 }).mockRejectedValueOnce(new Error("write unavailable"));
    await click("Save assignments");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("write unavailable");
    expect(host.querySelectorAll(".settings-assignment-state")[0].textContent).toBe("Assigned");
    expect(host.querySelectorAll(".settings-assignment-state")[1].textContent).toBe("Unsaved");
    expect(dirty).toHaveBeenLastCalledWith(true);
    vi.mocked(api.saveAgentProfileAssignment).mockResolvedValue({ surface: "mobile_chat", profile_id: 20 });
    vi.mocked(api.getAgentProfileAssignment).mockResolvedValueOnce({ surface: "mobile_chat", profile_id: 20 });
    await click("Save assignments");
    expect(api.saveAgentProfileAssignment).toHaveBeenCalledTimes(3);
    expect(api.saveAgentProfileAssignment).toHaveBeenLastCalledWith(identity, { surface: "mobile_chat", profile_id: 20 });
    expect(dirty).toHaveBeenLastCalledWith(false);
  });

  it("explains that assignments choose an entry point and do not affect managed sessions", async () => {
    await mount("agent");
    expect(host.textContent).toContain("This page only chooses which published agent definition an entry point uses.");
    expect(host.textContent).toContain("WebUI v2 managed sessions currently do not consume these assignments");
  });

  it("refreshes the assignment catalog when profile publication changes", async () => {
    const render = async (refreshVersion: number) => act(async () => { root.render(<I18nProvider><AgentSettingsPanel identity={identity} refreshVersion={refreshVersion} /></I18nProvider>); });
    await render(0);
    vi.mocked(api.listAgentProfiles).mockResolvedValue([published, { ...draft, status: "published" }]);
    await render(1);
    expect(api.listAgentProfiles).toHaveBeenCalledTimes(2);
    expect(Array.from(host.querySelector<HTMLSelectElement>("#assignment-web_chat")!.options).map((option) => option.value)).toContain("11");
  });

  it("preserves assignment drafts when another profile is published until confirmed refresh", async () => {
    const alternative = { ...published, id: 20, profile_key: "other-agent", display_name: "Other Agent" };
    vi.mocked(api.listAgentProfiles).mockResolvedValue([published, alternative]);
    const render = async (refreshVersion: number) => act(async () => { root.render(<I18nProvider><AgentSettingsPanel identity={identity} refreshVersion={refreshVersion} /></I18nProvider>); });
    await render(0);
    await change(host.querySelector<HTMLSelectElement>("#assignment-web_chat")!, "20");
    await render(1);
    expect(api.listAgentProfiles).toHaveBeenCalledTimes(1);
    expect(host.querySelector<HTMLSelectElement>("#assignment-web_chat")!.value).toBe("20");
    expect(host.textContent).toContain("Unsaved assignments are preserved");
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Refresh assignments"]')!.click());
    expect(api.listAgentProfiles).toHaveBeenCalledTimes(1);
    confirm.mockReturnValue(true);
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Refresh assignments"]')!.click());
    expect(api.listAgentProfiles).toHaveBeenCalledTimes(2);
    expect(host.querySelector<HTMLSelectElement>("#assignment-web_chat")!.value).toBe("10");
  });

  it("notifies the settings shell after a profile publish", async () => {
    const onDataChanged = vi.fn();
    await act(async () => { root.render(<I18nProvider><ProfileSettingsPanel identity={identity} onDataChanged={onDataChanged} /></I18nProvider>); });
    await click("preview"); await click("Publish");
    expect(onDataChanged).toHaveBeenCalledTimes(1);
  });

  it("keeps archived history visible but disables unsupported rollback with localized guidance", async () => {
    vi.stubGlobal("localStorage", { getItem: () => null, setItem: vi.fn() });
    vi.mocked(api.listAgentProfiles).mockResolvedValue([{ ...published, status: "archived" }]);
    function LanguageControl() { const { setLanguage } = useI18n(); return <button type="button" onClick={() => setLanguage("zh")}>Chinese</button>; }
    await act(async () => { root.render(<I18nProvider><ProfileSettingsPanel identity={identity} /><LanguageControl /></I18nProvider>); });
    expect(host.querySelector(".settings-profile-catalog")?.textContent).toContain("archived");
    await click("versions");
    expect(button("Rollback").disabled).toBe(true);
    expect(button("Rollback").title).toContain("copy this profile");
    await click("Rollback");
    expect(api.rollbackAgentProfile).not.toHaveBeenCalled();
    await click("Chinese");
    expect(button("回滚").title).toBe("归档版本无法回滚，可复制为新 Profile。");
  });

  it("disables every rollback action while a rollback draft is being created", async () => {
    let resolveRollback!: (value: AgentProfileRecord) => void;
    vi.mocked(api.rollbackAgentProfile).mockImplementationOnce(() => new Promise((resolve) => { resolveRollback = resolve; }));
    await mount("profiles"); await click("versions"); await click("Rollback");
    const actions = Array.from(host.querySelectorAll<HTMLButtonElement>(".version-row button"));
    expect(actions).toHaveLength(2);
    expect(actions.every((action) => action.disabled)).toBe(true);
    await act(async () => actions[1].click());
    expect(api.rollbackAgentProfile).toHaveBeenCalledTimes(1);
    await act(async () => resolveRollback({ ...draft, id: 12, profile_version: 3 }));
    expect(host.textContent).toContain("Rollback draft created");
  });
});
