import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ProfileConversationsDialog } from "./ProfileConversationsDialog";
import { I18nProvider } from "../lib/i18n";
import { apiRequest, getAgentProfileConversations, listMobileMessages } from "../lib/api";
import type { AgentProfileConversationCatalog, AgentProfileRecord, IdentityConfig, TenantMessage } from "../lib/types";

vi.mock("../lib/api", () => ({
  getAgentProfileConversations: vi.fn(),
  apiRequest: vi.fn(),
  listMobileMessages: vi.fn()
}));

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "yutang",
  userId: "webui-local-user",
  deviceId: "test-device",
  role: "owner",
  model: "gpt-5.6-sol"
};

const profile: AgentProfileRecord = {
  id: 7,
  profile_key: "copywriter",
  display_name: "Copywriter",
  description: "Marketing content assistant",
  profile_version: 2,
  status: "published",
  scope: "tenant_shared",
  config_json: "{}"
};

function catalog(): AgentProfileConversationCatalog {
  return {
    profile,
    message_count: 3,
    run_count: 2,
    teams: [{ team_id: 9, team_key: "content-team", team_version: 1, team_display_name: "Content Team", member_key: "copywriter", role: "coordinator", account_id: 3, account_key: "copywriter-feishu", external_chat_id: "oc_group", trigger_policy: "mention", status: "active" }],
    conversations: [
      { conversation_id: 21, session_id: 101, account_id: 3, account_key: "copywriter-feishu", external_chat_id: "oc_group", chat_type: "group", conversation_status: "active", title: "Campaign review", model: "gpt-5.6-sol", message_count: 2, run_count: 1, latest_run_status: "completed", last_message_preview: "Draft the launch announcement", last_message_at: "2026-08-27T01:02:03Z" },
      { conversation_id: 22, session_id: 102, account_id: 3, account_key: "copywriter-feishu", external_chat_id: "ou_user", chat_type: "p2p", conversation_status: "active", title: "Direct brief", model: "gpt-5.6-sol", message_count: 1, run_count: 1, latest_run_status: "completed", last_message_preview: "Need a concise version", last_message_at: "2026-08-26T01:02:03Z" }
    ]
  };
}

function message(id: number, sessionID: number, role: string, content: string): TenantMessage {
  return { id, session_id: sessionID, turn_index: id, role, content };
}

describe("ProfileConversationsDialog", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.mocked(apiRequest).mockReset().mockResolvedValue({ data: [message(501, 101, "assistant", "Channel reply")] });
    vi.mocked(getAgentProfileConversations).mockReset().mockResolvedValue(catalog());
    vi.mocked(listMobileMessages).mockReset().mockImplementation(async (_identity, sessionID) => [message(sessionID, sessionID, "assistant", `reply-${sessionID}`)]);
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    document.body.style.overflow = "auto";
  });

  afterEach(() => {
    act(() => root.unmount());
    document.body.style.overflow = "";
  });

  async function renderDialog(onClose = vi.fn(), onOpenSession = vi.fn(), profileRecord = profile) {
    await act(async () => {
      root.render(<I18nProvider><ProfileConversationsDialog identity={identity} profile={profileRecord} onClose={onClose} onOpenSession={onOpenSession} /></I18nProvider>);
      await flushPromises();
    });
    return { onClose, onOpenSession };
  }

  it("shows guidance without querying MySQL for a builtin profile", async () => {
    const builtin = { ...profile, id: 0, scope: "builtin", source_kind: "builtin" };

    await renderDialog(vi.fn(), vi.fn(), builtin);

    expect(getAgentProfileConversations).not.toHaveBeenCalled();
    expect(host.textContent).toContain("Builtin profiles do not store conversations");
    expect(host.textContent).not.toContain("mysql storage");
  });

  it("renders a spacious catalog and loads the selected session messages", async () => {
    await renderDialog();

    expect(host.querySelector('[role="dialog"]')).not.toBeNull();
    expect(host.textContent).toContain("Copywriter · Conversations");
    expect(host.textContent).toContain("Campaign review");
    expect(host.textContent).toContain("Direct brief");
    expect(host.textContent).toContain("reply-101");
    expect(host.querySelector(".profile-conversation-header .profile-conversation-summary")).not.toBeNull();
    expect(host.querySelector(".profile-team-links")).toBeNull();
    expect(document.activeElement).toBe(host.querySelector("button.icon-button[aria-label=\"Close conversations\"]"));
    expect(host.querySelector<HTMLAnchorElement>("a[href*='source=tenant']")?.getAttribute("href")).toContain("tenant_key=yutang");
    expect(host.querySelector<HTMLAnchorElement>("a[href*='source=tenant']")?.getAttribute("href")).toContain("session_id=101");
    expect(listMobileMessages).toHaveBeenCalledWith(identity, 101);
  });

  it("switches sessions and opens the selected session in Chat Lab", async () => {
    const { onOpenSession } = await renderDialog();
    const rows = host.querySelectorAll<HTMLButtonElement>(".profile-conversation-row");

    await act(async () => {
      rows[1].click();
      await flushPromises();
    });

    expect(host.textContent).toContain("reply-102");
    expect(listMobileMessages).toHaveBeenLastCalledWith(identity, 102);
    const openButton = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Open in Chat Lab"));
    expect(openButton).toBeDefined();
    await act(async () => openButton?.click());
    expect(onOpenSession).toHaveBeenCalledWith(102);
  });

  it("reads channel messages with the selected tenant gateway without offering a wrong-environment chat or trace link", async () => {
    const target = { ...identity, apiBase: "/api/runtime/settings/environments/channel" };
    await act(async () => {
      root.render(<I18nProvider><ProfileConversationsDialog identity={target} profile={profile} onClose={vi.fn()} tenantMessages showTrace={false} /></I18nProvider>);
      await flushPromises();
    });
    expect(apiRequest).toHaveBeenCalledWith(target, "/tenant/messages?session_id=101&limit=100");
    expect(listMobileMessages).not.toHaveBeenCalled();
    expect(host.textContent).toContain("Channel reply");
    expect(host.querySelector("a[href*='session_id']")).toBeNull();
    expect(host.textContent).not.toContain("Open in Chat Lab");
  });

  it("shows tenant message failures instead of silently hiding them", async () => {
    vi.mocked(apiRequest).mockRejectedValueOnce(new Error("database unavailable"));
    await act(async () => {
      root.render(<I18nProvider><ProfileConversationsDialog identity={identity} profile={profile} onClose={vi.fn()} tenantMessages showTrace={false} /></I18nProvider>);
      await flushPromises();
    });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Unable to load messages in this environment");
    expect(host.textContent).not.toContain("database unavailable");
  });

  it("supports empty and error states and closes on Escape", async () => {
    const onClose = vi.fn();
    vi.mocked(getAgentProfileConversations).mockResolvedValueOnce({ ...catalog(), conversations: [], teams: [], message_count: 0, run_count: 0 });
    await renderDialog(onClose);
    expect(host.textContent).toContain("No conversations yet");

    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => root.unmount());
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    vi.mocked(getAgentProfileConversations).mockRejectedValueOnce(new Error("catalog unavailable"));
    await act(async () => {
      root.render(<I18nProvider><ProfileConversationsDialog identity={identity} profile={profile} onClose={onClose} /></I18nProvider>);
      await flushPromises();
    });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Unable to load conversations");
    expect(host.textContent).not.toContain("catalog unavailable");
    const retryButton = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Try again"));
    expect(retryButton).toBeDefined();
    await act(async () => {
      retryButton?.click();
      await flushPromises();
    });
    expect(host.textContent).toContain("Campaign review");
  });
});

function flushPromises(): Promise<void> {
  return new Promise((resolve) => queueMicrotask(resolve));
}
