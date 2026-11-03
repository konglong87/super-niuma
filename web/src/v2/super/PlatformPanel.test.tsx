import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { SuperPlatformBridge } from "../desktopServiceBridge";
import { PlatformPanel } from "./PlatformPanel";
import { SuperWorkspace } from "./SuperWorkspace";
import { getSuperPlatformBridge, platformError, STOPPED_CONFIRMATION } from "./platformClient";
import { PLATFORM_COPY } from "./platformCopy";
import { configSafety, displayHash, displayText } from "./platformSafety";
import { MOCK_ARTIFACT_HASH, MOCK_HASH, deferred, mockBridge, mockClaim, mockEvent, mockOrder, mockRun, mockSnapshot } from "./platformFixtures.test-support";

// UI mock tests only: no model/provider/app is launched.
describe("Super control panel (mock UI)", () => {
  let host: HTMLDivElement;
  let root: Root;
  let bridge: SuperPlatformBridge;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => "zh", setItem: vi.fn() } });
    host = document.createElement("div"); document.body.replaceChildren(host); root = createRoot(host);
    bridge = mockBridge();
  });
  afterEach(() => { act(() => root.unmount()); delete window.go; vi.restoreAllMocks(); vi.useRealTimers(); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT; });
  async function render(value: Partial<SuperPlatformBridge> | null = bridge) {
    await act(async () => root.render(<I18nProvider><PlatformPanel bridge={value} orders={[mockOrder()]} /></I18nProvider>));
  }
  const button = (text: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text)!;
  async function click(text: string) { await act(async () => button(text).click()); }
  function change(id: string, value: string) {
    const input = host.querySelector<HTMLSelectElement | HTMLTextAreaElement>(`#${id}`)!;
    act(() => {
      Object.getOwnPropertyDescriptor(input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLTextAreaElement.prototype, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event(input instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
    });
  }
  function check(selector: string) { act(() => host.querySelector<HTMLInputElement>(selector)!.click()); }
  function selectExecution() { change("super-platform-order", "mock-order"); change("super-platform-profile", "mock-profile"); }

  it("keeps intake operational when the platform bridge is missing", async () => {
    const client = { list: vi.fn().mockResolvedValue([mockOrder()]), create: vi.fn(), get: vi.fn(), cancel: vi.fn().mockResolvedValue({ ...mockOrder(), status: "cancelled" }) };
    await act(async () => root.render(<I18nProvider><SuperWorkspace client={client} onBack={vi.fn()} /></I18nProvider>));
    expect(host.textContent).toContain("此 Desktop 尚未提供控制面 API");
    expect(host.querySelector(".super-order-list")?.textContent).toContain("Mock 工单");
    await act(async () => host.querySelector<HTMLButtonElement>(".super-cancel")!.click());
    expect(client.cancel).toHaveBeenCalledWith("mock-order");
    expect(getSuperPlatformBridge()).toBeNull();
    window.go = { main: { app: { RestartLocalService: vi.fn(), GetSuperPlatform: bridge.GetSuperPlatform } } };
    expect(getSuperPlatformBridge()?.GetSuperPlatform).toBe(bridge.GetSuperPlatform);
  });
  it("renders status without automatically saving or starting work", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch"); const openSpy = vi.spyOn(window, "open");
    await render();
    expect(host.textContent).toContain("Worker 数: 2");
    expect(host.querySelector(".super-platform-config")?.hasAttribute("open")).toBe(false);
    expect(button("提交所选工单").disabled).toBe(true);
    expect(bridge.StartSuperRun).not.toHaveBeenCalled(); expect(bridge.SaveSuperPlatformConfig).not.toHaveBeenCalled();
    expect(fetchSpy).not.toHaveBeenCalled(); expect(openSpy).not.toHaveBeenCalled();
  });
  it.each(["zh", "en"])("treats missing configuration as normal onboarding in %s", async (language) => {
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => language, setItem: vi.fn() } });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ configured: false, running: false, workers: 0, profiles: [], safe_code: "configuration_required" }));
    await render();
    expect(host.querySelector(".super-platform-onboarding")).not.toBeNull();
    expect(host.querySelectorAll("[role=alert]")).toHaveLength(0);
    expect(host.textContent).not.toContain(language === "zh" ? "服务端报告需要关注" : "The backend reported an issue");
    expect(host.querySelector(".super-platform-config")?.hasAttribute("open")).toBe(false);
    await click(language === "zh" ? "设置执行环境" : "Set up execution");
    expect(host.querySelector(".super-platform-config")?.hasAttribute("open")).toBe(true);
    expect(bridge.StartSuperRun).not.toHaveBeenCalled(); expect(bridge.SaveSuperPlatformConfig).not.toHaveBeenCalled();
  });
  it("keeps invalid saved configuration actionable instead of hiding a real backend issue", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ configured: false, safe_code: "configuration_invalid" }));
    await render(); expect(host.querySelector(".super-platform-onboarding")).toBeNull();
    expect(host.querySelector("[role=alert]")?.textContent).toContain("已有配置未通过校验");
  });
  it("treats transport startup and webhook address as ingress, not product or live acceptance", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch"); const openSpy = vi.spyOn(window, "open");
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ webhook_address: "127.0.0.1:61585", channel_code: "channels_started" }));
    await render();
    const status = host.querySelector(".super-platform-reliability");
    expect(status?.textContent).toContain("127.0.0.1:61585");
    expect(status?.textContent).toContain("不是独立产品入口");
    expect(status?.textContent).toContain("尚未完成真实渠道验收");
    expect(status?.querySelector("a")).toBeNull();
    expect(fetchSpy).not.toHaveBeenCalled(); expect(openSpy).not.toHaveBeenCalled();
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ channel_code: "transports_started_not_live_acceptance" }));
    await click("刷新控制台"); expect(host.querySelector(".super-platform-reliability")?.textContent).toContain("尚未完成真实渠道验收");
  });
  it("projects only safe DLQ code/topic/attempts, never event payloads or tokens", async () => {
    const letter = { code: "consumer_failed", attempts: 10, event: { ...mockEvent(1), token: "do-not-render-event-token" },
      subscriber: "do-not-render-subscriber", scope: "do-not-render-scope", token: "do-not-render-claim-token" };
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ config_json: "{}", dead_letters: [letter] }));
    await render(); const dlq = host.querySelector(".super-platform-deadletters");
    expect(dlq?.textContent).toContain("consumer_failed"); expect(dlq?.textContent).toContain("super.run.changed");
    expect(dlq?.textContent).toContain("尝试次数10"); expect(host.textContent).toContain("不会自动重放或重发");
    expect(host.innerHTML).not.toContain("do-not-render");
    expect(bridge.RetrySuperRun).not.toHaveBeenCalled();
  });
  it("hides unknown channel/DLQ strings and credential-bearing endpoint values", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ config_json: "{}", channel_code: "do-not-render-channel-token", webhook_address: "http://user:do-not-render-password@example.com/?token=secret",
      dead_letters: [{ code: "do-not-render-code", attempts: 4, event: { ...mockEvent(2), topic: "do-not-render-topic" } }] }));
    await render();
    expect(host.textContent).toContain("未识别的通道状态"); expect(host.textContent).toContain("地址格式无法安全展示");
    expect(host.textContent).toContain("未识别的安全代码"); expect(host.innerHTML).not.toContain("do-not-render");
    expect(host.querySelector(".super-platform-endpoint")).toBeNull();
  });
  it("explains the recent-run limit without failing the panel or inventing pagination", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ has_more_runs: true, runs: [mockRun()] }));
    await render(); expect(host.querySelector(".super-platform-history")?.textContent).toContain("最近 100 条运行记录");
    expect(host.querySelector(".super-platform-history")?.textContent).toContain("更早历史仍持久保留");
    expect(host.querySelector(".super-platform-run")).not.toBeNull(); expect(button("批准此版本")).toBeDefined();
    expect(host.querySelectorAll("[role=alert]")).toHaveLength(0);
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot()); await click("刷新控制台");
    expect(host.querySelector(".super-platform-history")).toBeNull(); expect(host.querySelector(".super-platform-reliability")).toBeNull();
  });
  it("shows profile workspace/budget and requires explicit start consent", async () => {
    await render(); selectExecution();
    expect(host.querySelector(".super-platform-submit")?.textContent).toContain("/mock/workspace");
    expect(host.querySelector(".super-platform-submit")?.textContent).toContain("8,000");
    expect(button("提交所选工单").disabled).toBe(true);
    check(".super-platform-submit input[type=checkbox]");
    await click("提交所选工单");
    expect(bridge.StartSuperRun).toHaveBeenCalledWith("mock-order", "mock-profile", expect.any(String));
    change("super-platform-profile", ""); expect(button("提交所选工单").disabled).toBe(true);
  });
  it("fails closed on absent grant budgets, disabled profiles and stopped runtime", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ config_json: "{}" }));
    await render(); selectExecution(); expect(host.textContent).toContain("无法读取此 Profile 的授权预算");
    expect(button("提交所选工单").disabled).toBe(true);
    const next = mockBridge(mockSnapshot({ running: false })); await render(next); selectExecution(); check(".super-platform-submit input[type=checkbox]");
    expect(button("提交所选工单").disabled).toBe(true);
  });
  it("does not carry start consent across a changed configuration or bridge", async () => {
    await render(); selectExecution(); check(".super-platform-submit input[type=checkbox]");
    expect(button("提交所选工单").disabled).toBe(false);
    const changed = mockSnapshot();
    const config = JSON.parse(changed.config_json); config.profiles[0].grant.max_tokens = 9000;
    changed.config_json = JSON.stringify(config);
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(changed);
    await click("刷新控制台"); expect(button("提交所选工单").disabled).toBe(true);
    check(".super-platform-submit input[type=checkbox]"); expect(button("提交所选工单").disabled).toBe(false);
    await render(mockBridge(changed)); expect(button("提交所选工单").disabled).toBe(true);
  });
  it("lists disabled profiles but never permits selecting them for execution", async () => {
    const snapshot = mockSnapshot(); snapshot.profiles[0].enabled = false;
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(snapshot); await render(); selectExecution();
    expect(host.querySelector<HTMLOptionElement>('#super-platform-profile option[value="mock-profile"]')?.disabled).toBe(true);
    expect(button("提交所选工单").disabled).toBe(true);
    expect(bridge.StartSuperRun).not.toHaveBeenCalled();
  });
  it("sends unchanged configuration only on Save and retains dirty drafts across polls", async () => {
    await render();
    const raw = '  { "version": 1, "workers": 3, "profiles": [] }\n';
    change("super-platform-config", raw);
    await click("刷新控制台");
    expect(host.querySelector<HTMLTextAreaElement>("#super-platform-config")?.value).toBe(raw);
    vi.mocked(bridge.SaveSuperPlatformConfig).mockResolvedValue(mockSnapshot({ config_json: raw, workers: 3 }));
    await click("保存配置"); expect(bridge.SaveSuperPlatformConfig).toHaveBeenCalledWith(raw);
    expect(bridge.StartSuperRun).not.toHaveBeenCalled();
  });
  it("hides secret-bearing server configuration and rejects credentials in the editor", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ config_json: '{"api_key":"do-not-render-secret"}' }));
    await render();
    expect(host.querySelector<HTMLTextAreaElement>("#super-platform-config")?.value).toBe("");
    expect(host.innerHTML).not.toContain("do-not-render-secret");
    expect(host.textContent).toContain("已隐藏");
    change("super-platform-config", '{"password":"local-test-only"}'); expect(button("保存配置").disabled).toBe(true);
    change("super-platform-config", '{"api_key_env":"MOCK_API_KEY"}'); expect(button("保存配置").disabled).toBe(false);
  });
  it.each(["zh", "en"] as const)("shows existing scope and owner without inferring source identity in %s", async (language) => {
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => language, setItem: vi.fn() } });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [mockRun({ scope: "feishu-account-a", owner: "authorized-owner-a" })] }));
    await render(); const source = host.querySelector(".super-platform-run-source")!;
    expect(source.textContent).toContain(PLATFORM_COPY[language].runScope); expect(source.textContent).toContain("feishu-account-a");
    expect(source.textContent).toContain(PLATFORM_COPY[language].runOwner); expect(source.textContent).toContain("authorized-owner-a");
  });
  it("uses exact grant hash/version for approval and clears consent on revision change", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [mockRun()] }));
    await render(); expect(button("批准此版本").disabled).toBe(true);
    check(".super-platform-run input[type=checkbox]");
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [mockRun({ version: 2 })] }));
    await click("刷新控制台"); expect(button("批准此版本").disabled).toBe(true);
    check(".super-platform-run input[type=checkbox]"); await click("批准此版本");
    expect(bridge.ApproveSuperRun).toHaveBeenCalledWith("mock-run", 2, MOCK_HASH);
  });
  it.each(["zh", "en"] as const)("shows native cancellation as a normal lifecycle state in %s", async (language) => {
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => language, setItem: vi.fn() } });
    const run = mockRun({ status: "cancelled", safe_code: "cancelled" }); run.jobs[0].status = "cancelled";
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    const card = host.querySelector(".super-platform-run")!;
    expect(card.querySelector(".super-platform-run-message")?.getAttribute("role")).toBe("status");
    expect(card.textContent).toContain(PLATFORM_COPY[language].runCancelled);
    expect(card.textContent).not.toContain(PLATFORM_COPY[language].dataWarning);
    expect(card.querySelector("[role=alert]")).toBeNull();
  });
  it("keeps unknown external outcomes visible after an otherwise normal cancellation", async () => {
    const run = mockRun({ status: "cancelled", safe_code: "cancelled", notices: [{ id: "mock-notice", target: { id: "mock-target", provider: "mock" }, status: "unknown", attempts: 1, claim: mockClaim }] });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    const card = host.querySelector(".super-platform-run")!;
    expect(card.textContent).toContain(PLATFORM_COPY.zh.runCancelled);
    expect(card.querySelector("[role=alert]")).not.toBeNull(); expect(card.textContent).toContain("交付结果未知");
  });
  it("cancels with CAS and retries only reconciled failed runs with explicit consent", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [mockRun({ status: "running", version: 4 })] }));
    await render(); await click("取消执行"); expect(bridge.CancelSuperRun).toHaveBeenCalledWith("mock-run", 4);
    const failed = mockRun({ status: "failed", version: 5 });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [failed] }));
    await click("刷新控制台"); expect(button("重试失败任务").disabled).toBe(true);
    check(".super-platform-run input[type=checkbox]"); await click("重试失败任务");
    expect(bridge.RetrySuperRun).toHaveBeenCalledWith("mock-run", 5);
  });
  it("reports settled and unsettled budgets without releasing unknown usage", async () => {
    const run = mockRun({ reservations: [
      { id: "mock-reservation-1", job_id: "mock-node", attempt_id: "mock-attempt-1", input: 100, output: 50, actual: { input_tokens: 70, output_tokens: 30 }, settled: true },
      { id: "mock-reservation-2", job_id: "mock-node", attempt_id: "mock-attempt-2", input: 300, output: 200, actual: { input_tokens: 0, output_tokens: 0 }, settled: false },
    ] });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    const budget = host.querySelector(".super-platform-run > .super-platform-metrics");
    expect(budget?.textContent).toContain("已结算用量100"); expect(budget?.textContent).toContain("未结算预留500");
    expect(host.textContent).toContain("预留尚未释放");
  });
  it("suppresses retry when job outcomes or delivery receipts are unknown", async () => {
    const run = mockRun({ status: "failed" }); run.jobs[0].status = "unknown";
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    expect(button("重试失败任务")).toBeUndefined();
    run.jobs[0].status = "failed"; run.jobs[0].attempts = 3;
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [{ ...run, version: 2 }] })); await click("刷新控制台");
    expect(button("重试失败任务")).toBeUndefined();
  });
  it("does not permit approval of expired grants", async () => {
    const run = mockRun(); run.grant.expires_at_ms = 1;
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    expect(button("批准此版本").disabled).toBe(true); expect(host.textContent).toContain("授权已过期");
  });
  it("highlights uncertain delivery, evidence hashes and no blind retry", async () => {
    const run = mockRun({ status: "needs_attention", verification: { passed: true, policy_hash: MOCK_HASH, verified_at_ms: 1793664000000,
      artifacts: [{ path: "output.txt", sha256: MOCK_ARTIFACT_HASH, bytes: 24 }], checks: [{ path: "output.txt", kind: "exists", passed: true, code: "ok" }] },
      notices: [{ id: "mock-notice", target: { id: "mock-target", provider: "mock" }, status: "unknown", attempts: 1, claim: mockClaim }] });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    expect(host.textContent).toContain(MOCK_ARTIFACT_HASH); expect(host.textContent).toContain("禁止盲目重发");
    expect(button("重试失败任务")).toBeUndefined();
    expect(host.querySelector(".super-platform-run")?.textContent).not.toContain("do-not-render");
  });
  it("keeps manual abandonment hidden without the optional bridge", async () => {
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [mockRun({ status: "needs_attention" })] })); await render();
    expect(host.querySelector(".super-platform-manual")).toBeNull();
  });
  it("requires explicit stop confirmation before abandoning, without retry or delivery claims", async () => {
    const run = mockRun({ status: "needs_attention", version: 7, notices: [{ id: "mock-notice", target: { id: "mock-target", provider: "mock" }, status: "unknown", attempts: 1, claim: mockClaim }] });
    bridge.AbandonSuperRun = vi.fn().mockResolvedValue({ ...run, version: 8, status: "cancelled" });
    vi.mocked(bridge.GetSuperPlatform).mockResolvedValue(mockSnapshot({ runs: [run] })); await render();
    const manual = host.querySelector<HTMLDetailsElement>(".super-platform-manual")!;
    expect(manual.open).toBe(false); expect(manual.querySelector<HTMLInputElement>("input")?.checked).toBe(false);
    expect(button("确认停机后放弃(不重试)").disabled).toBe(true); expect(bridge.AbandonSuperRun).not.toHaveBeenCalled();
    act(() => manual.querySelector<HTMLElement>("summary")!.click());
    expect(manual.textContent).toContain("我已确认旧Worker和发送进程全部停止，未知外部结果仍需核对");
    check(".super-platform-manual input"); await click("确认停机后放弃(不重试)");
    expect(bridge.AbandonSuperRun).toHaveBeenCalledWith(run.id, 7, STOPPED_CONFIRMATION);
    expect(STOPPED_CONFIRMATION).toBe("ALL_WORKERS_AND_SENDERS_CONFIRMED_STOPPED");
    expect(bridge.RetrySuperRun).not.toHaveBeenCalled(); expect(bridge.StartSuperRun).not.toHaveBeenCalled();
    expect(host.querySelector(".super-platform-run")?.textContent).toContain("已取消");
    expect(host.querySelector(".super-platform-run")?.textContent).toContain("交付结果未知");
  });
  it("retains event cursor, deduplicates pages, and never renders raw payloads", async () => {
    vi.mocked(bridge.GetSuperEvents).mockResolvedValueOnce([mockEvent(2), mockEvent(1)]).mockResolvedValueOnce([mockEvent(2), mockEvent(3)]).mockRejectedValueOnce("api_key=do-not-render-secret");
    await render();
    expect(vi.mocked(bridge.GetSuperEvents).mock.calls.map(([after]) => after)).toEqual([0, 2, 3]);
    expect(host.querySelectorAll(".super-platform-events li")).toHaveLength(3);
    expect(host.textContent).toContain("从上次游标补读"); expect(host.innerHTML).not.toContain("do-not-render-event-payload");
    expect(host.innerHTML).not.toContain("do-not-render-secret");
    await click("刷新控制台"); expect(bridge.GetSuperEvents).toHaveBeenLastCalledWith(3);
  });
  it("supports partial bridges without breaking reads or unrelated intake", async () => {
    await render({ GetSuperPlatform: bridge.GetSuperPlatform });
    expect(host.textContent).toContain("此桥接未提供事件 API");
    expect(host.textContent).toContain("当前桥接不支持提交执行");
    expect(button("保存配置").disabled).toBe(true);
  });
  it("shows safe failures instead of exception objects or unknown codes", async () => {
    vi.mocked(bridge.StartSuperRun).mockRejectedValue(new Error("secret token /private/customer"));
    await render(); selectExecution(); check(".super-platform-submit input[type=checkbox]"); await click("提交所选工单");
    expect(host.textContent).toContain("控制面暂时不可用"); expect(host.innerHTML).not.toContain("/private/customer");
    expect(platformError({ message: "secret" })).toBe("unavailable");
    expect(platformError("Super state revision conflict")).toBe("conflict");
    expect(displayText("Bearer mock-secret-123")).toBe("[redacted]"); expect(displayHash("not-a-hash")).toBe("—");
    expect(configSafety('{"api_key_env":"MOCK_API_KEY"}')).toBe("safe");
  });
  it("blocks double mutation and reuses start keys after ambiguous failure", async () => {
    const request = deferred<ReturnType<typeof mockRun>>();
    vi.mocked(bridge.StartSuperRun).mockReturnValueOnce(request.promise).mockRejectedValueOnce("unavailable");
    await render(); selectExecution(); check(".super-platform-submit input[type=checkbox]");
    act(() => { button("提交所选工单").click(); button("提交所选工单").click(); });
    expect(bridge.StartSuperRun).toHaveBeenCalledTimes(1);
    await act(async () => request.reject(new Error("ambiguous")));
    await click("提交所选工单");
    const calls = vi.mocked(bridge.StartSuperRun).mock.calls; expect(calls[0][2]).toBe(calls[1][2]);
  });
});
