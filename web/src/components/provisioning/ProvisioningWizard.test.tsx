import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig, ProvisioningRecord } from "../../lib/types";
import { ProvisioningWizard } from "./ProvisioningWizard";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(),
  getProvisioningOverview: vi.fn(),
  listAgentProfiles: vi.fn(),
  listChannelAccounts: vi.fn(),
  listProviders: vi.fn(),
  createProvisioning: vi.fn(),
  preflightProvisioning: vi.fn(),
  workerProvisioningAction: vi.fn()
}));

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", role: "owner", model: "test" };
const record: ProvisioningRecord = {
  id: 7,
  profile_key: "support-agent",
  account_key: "support-bot",
  supervisor: "screen",
  status: "draft",
  worker: { supervisor: "screen", account_key: "support-bot", provider: "openai", model: "gpt-5" },
  observed_worker: { state: "stopped" }
};

describe("ProvisioningWizard views", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getProvisioningOverview).mockResolvedValue({ records: [record], workers: [] });
    vi.mocked(api.listAgentProfiles).mockResolvedValue([{ id: 3, profile_key: "support-agent", profile_version: 1, status: "published", scope: "tenant_shared", display_name: "Support agent", description: "Support", config_json: "{}" }]);
    vi.mocked(api.listChannelAccounts).mockResolvedValue([{ id: 4, account_key: "support-bot", provider: "feishu", app_id: "cli_test", mode: "websocket", enabled: true, status: "active" }]);
    vi.mocked(api.listProviders).mockResolvedValue([{ name: "openai", model: "gpt-5" }]);
    vi.mocked(api.preflightProvisioning).mockResolvedValue({ ...record, status: "preflight", observed_worker: { state: "preflight" } });
    vi.mocked(api.createProvisioning).mockResolvedValue(record);
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.restoreAllMocks();
  });

  async function mount(view: "account" | "lifecycle", onNavigate = vi.fn()) {
    await act(async () => {
      root.render(<I18nProvider><ProvisioningWizard identity={identity} onStatus={vi.fn()} onNavigate={onNavigate} view={view} /></I18nProvider>);
    });
    return onNavigate;
  }

  async function click(label: string) {
    const button = Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent?.trim() === label);
    expect(button).toBeDefined();
    await act(async () => button?.click());
  }

  it("keeps Feishu setup focused and routes the saved draft to Worker runtime", async () => {
    const onNavigate = await mount("account");
    expect(host.textContent).toContain("Connect Feishu");
    expect(host.textContent).toContain("Saved account");
    expect(host.textContent).not.toContain("Start Worker");
    expect(host.textContent).not.toContain("Run preflight");
    expect(host.querySelector('input[type="password"]')).toBeNull();

    await click("Save connection draft");
    expect(host.textContent).toContain("Connection draft saved");
    expect(host.textContent).toContain("Worker has not been started");
    await click("Go to Worker runtime");
    expect(onNavigate).toHaveBeenCalledWith("lifecycle");
  });

  it("shows Worker selection, state and lifecycle actions together", async () => {
    await mount("lifecycle");
    expect(host.querySelector(".worker-selector-trigger")).not.toBeNull();
    expect(host.textContent).toContain("support-agent");
    expect(host.textContent).toContain("Preflight");
    expect(host.textContent).toContain("Start");
    expect(host.textContent).toContain("Restart");
    expect(host.textContent).toContain("Stop");
    expect(host.textContent).not.toContain("Preflight settings");
    expect(host.textContent).not.toContain("App Secret");
    expect(host.textContent).not.toContain("Connect a new bot");

    const trigger = host.querySelector<HTMLButtonElement>(".worker-selector-trigger");
    await act(async () => trigger?.click());
    expect(host.querySelector(".worker-selector-popover")).not.toBeNull();
    expect(host.querySelector('[role="option"]')?.textContent).toContain("support-agent");
    const option = host.querySelector<HTMLButtonElement>('[role="option"]');
    await act(async () => option?.click());

    await click("Preflight");
    expect(api.preflightProvisioning).toHaveBeenCalledWith(identity, record.id);
  });

  it("offers a direct Feishu connection entry when no Worker exists", async () => {
    vi.mocked(api.getProvisioningOverview).mockResolvedValue({ records: [], workers: [] });
    const onNavigate = vi.fn();
    await mount("lifecycle", onNavigate);
    await click("Connect Feishu");
    expect(onNavigate).toHaveBeenCalledWith("account");
  });
});
