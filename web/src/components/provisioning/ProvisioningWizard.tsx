import { ArrowRight, Bot, CheckCircle2, CircleDashed, KeyRound, Play, RefreshCw, ShieldCheck, Square, WandSparkles, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { cancelFeishuOnboarding, createProvisioning, getFeishuCLIAvailability, getFeishuOnboarding, getProvisioningOverview, installFeishuCLI, listAgentProfiles, listChannelAccounts, listProviders, preflightProvisioning, startFeishuOnboarding, workerProvisioningAction } from "../../lib/api";
import { useI18n, type Language } from "../../lib/i18n";
import type { AgentProfileRecord, ChannelAccountRecord, FeishuCLIAvailability, FeishuOnboardingSession, IdentityConfig, ProviderOption, ProvisioningRecord } from "../../lib/types";
import { ProvisioningSteps } from "./ProvisioningSteps";
import { WorkerSelector } from "./WorkerSelector";
import "./provisioning.css";

export type ProvisioningView = "account" | "lifecycle";

const labels: Record<ProvisioningView, Record<Language, string[]>> = {
  account: { en: ["Select agent", "Connect Feishu", "Connection draft"], zh: ["选择智能体", "连接飞书", "连接草稿"] },
  lifecycle: { en: ["Select Worker", "Preflight settings", "Worker lifecycle", "Test and refresh"], zh: ["选择 Worker", "运行预检", "Worker 生命周期", "测试与刷新"] }
};

type Props = { identity: IdentityConfig; onStatus: (message: string) => void; onNavigate?: (view: ProvisioningView) => void; view?: ProvisioningView };

export function ProvisioningWizard({ identity, onStatus: _onStatus, onNavigate, view = "account" }: Props) {
  const { language } = useI18n();
  const zh = language === "zh";
  const tr = (en: string, zhText: string): string => zh ? zhText : en;
  const stepLabels = labels[view][language];
  const [records, setRecords] = useState<ProvisioningRecord[]>([]);
  const [profiles, setProfiles] = useState<AgentProfileRecord[]>([]);
  const [accounts, setAccounts] = useState<ChannelAccountRecord[]>([]);
  const [providers, setProviders] = useState<ProviderOption[]>([]);
  const [cli, setCLI] = useState<FeishuCLIAvailability | null>(null);
  const [onboarding, setOnboarding] = useState<FeishuOnboardingSession | null>(null);
  const [selected, setSelected] = useState<ProvisioningRecord | null>(null);
  const [step, setStep] = useState(0);
  const [profileKey, setProfileKey] = useState("");
  const [accountKey, setAccountKey] = useState("");
  const [appId, setAppId] = useState("");
  const [appSecret, setAppSecret] = useState("");
  const [usingExistingAccount, setUsingExistingAccount] = useState(true);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [statusMessage, setStatusMessage] = useState("");

  const publishedProfiles = useMemo(() => profiles.filter((item) => item.status === "published"), [profiles]);
  const selectedProfile = useMemo(() => profiles.find((item) => item.profile_key === profileKey), [profiles, profileKey]);
  const selectedState = selected?.observed_worker?.state || selected?.status || "unknown";

  useEffect(() => {
    let cancelled = false;
    Promise.all([
      getProvisioningOverview(identity).catch(() => ({ records: [], workers: [] })),
      listAgentProfiles(identity).catch(() => []),
      listChannelAccounts(identity).catch(() => []),
      listProviders(identity).catch(() => []),
      getFeishuCLIAvailability(identity).catch(() => null)
    ]).then(([overview, nextProfiles, nextAccounts, nextProviders, nextCLI]) => {
      if (cancelled) return;
      setRecords(overview.records);
      setProfiles(nextProfiles);
      setAccounts(nextAccounts);
      setProviders(nextProviders);
      setCLI(nextCLI);
      const first = overview.records[0];
      if (!first) return;
      setSelected(first);
      setProfileKey(first.profile_key);
      setAccountKey(first.account_key);
      setUsingExistingAccount(true);
      setProvider(first.worker?.provider || "");
      setModel(first.worker?.model || "");
      setStep(view === "account" ? 1 : 0);
    }).catch((reason) => {
      if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason));
    });
    return () => { cancelled = true; };
  }, [identity, view]);

  useEffect(() => {
    if (!onboarding || (onboarding.status !== "starting" && onboarding.status !== "waiting_for_scan")) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      void getFeishuOnboarding(identity, onboarding.id).then((next) => {
        if (cancelled) return;
        setOnboarding(next);
        if (next.record) {
          setSelected(next.record);
          setRecords((current) => [next.record as ProvisioningRecord, ...current.filter((record) => record.id !== next.record?.id)]);
        }
        if (next.status === "created") {
          const message = language === "zh" ? "飞书机器人已创建，连接草稿已保存。" : "Feishu bot created and connection draft saved.";
          setStatusMessage(message);
      }
      }).catch((reason) => {
        if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason));
      });
    }, 700);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [identity, onboarding, language]);

  async function run(action: () => Promise<void>): Promise<void> {
    setBusy(true);
    setError("");
    setStatusMessage("");
    try { await action(); } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)); } finally { setBusy(false); }
  }

  async function startAutomaticOnboarding(): Promise<void> {
    await run(async () => {
      if (!profileKey || !accountKey) throw new Error(tr("Select an agent and enter an account key first.", "请先选择智能体并填写账号 Key。"));
      let nextCLI = cli;
      if (!nextCLI?.lark_cli) { nextCLI = await installFeishuCLI(identity); setCLI(nextCLI); }
      const session = await startFeishuOnboarding(identity, { profile_key: profileKey, account_key: accountKey, app_name: accountKey, provider, model, streaming: "on", reactions: "on" });
      setOnboarding(session);
      setStep(2);
      const message = tr("lark-cli is ready. Scan the Feishu QR code to create the bot.", "lark-cli 已就绪，请扫描飞书二维码创建机器人。");
      setStatusMessage(message);
    });
  }

  async function createConnectionDraft(): Promise<void> {
    await run(async () => {
      if (!profileKey || !accountKey) throw new Error(tr("Select a published agent and enter an account key.", "请选择已发布智能体并填写账号 Key。"));
      const item = await createProvisioning(identity, {
        profile_key: profileKey,
        account_key: accountKey,
        credential: { id: accountKey, provider: "feishu", app_id: appId, secret_value: appSecret },
        worker: { supervisor: "screen", account_key: accountKey, provider, model, settings_ref: "global-settings", streaming: "on", reactions: "on", permission_mode: "ask" }
      });
      setSelected(item);
      setRecords((current) => [item, ...current.filter((record) => record.id !== item.id)]);
      const message = tr("Feishu connection draft saved; Worker is not started.", "飞书连接草稿已保存；Worker 尚未启动。");
      setStatusMessage(message);
      setStep(2);
    });
  }

  async function action(kind: "preflight" | "start" | "restart" | "stop" | "status"): Promise<void> {
    if (!selected) { setError(tr("Select a Worker first.", "请先选择 Worker。")); return; }
    await run(async () => {
      const item = kind === "preflight" ? await preflightProvisioning(identity, selected.id) : await workerProvisioningAction(identity, selected.id, kind);
      setSelected(item);
      setRecords((current) => current.map((record) => record.id === item.id ? item : record));
      setStatusMessage(`${kind} ${tr("completed", "完成")}`);
    });
  }

  async function cancelOnboarding(): Promise<void> {
    if (!onboarding) return;
    await run(async () => {
      await cancelFeishuOnboarding(identity, onboarding.id);
      setOnboarding({ ...onboarding, status: "canceled" });
    });
  }

  function selectRecord(record: ProvisioningRecord): void {
    if (busy) return;
    setSelected(record);
    setProfileKey(record.profile_key);
    setAccountKey(record.account_key);
    setProvider(record.worker?.provider || "");
    setModel(record.worker?.model || "");
  }

  function stateClass(state: string): string { return state.replace(/[^a-z0-9_-]/gi, "-").toLowerCase(); }

  if (view === "lifecycle") {
    return <div className="worker-settings"><div className="worker-settings-runtime">
      <div className="runtime-heading">
        <div><span className="runtime-eyebrow">{tr("Operations", "运行管理")}</span><h1>{tr("Worker runtime", "Worker 运行")}</h1><p>{tr("Operate saved channel workers without changing Feishu credentials.", "管理已保存的渠道 Worker，不在这里修改飞书凭据。")}</p></div>
        <div className="runtime-heading-tools">
          <WorkerSelector records={records} selected={selected} disabled={busy} language={language} onSelect={selectRecord} />
          <button className="worker-button-secondary" type="button" onClick={() => onNavigate?.("account")}><Bot size={15} />{tr("Connect Feishu", "连接飞书")}</button>
        </div>
      </div>
      {error ? <div className="worker-settings-alert" role="alert"><span>{error}</span><button aria-label={tr("Dismiss error", "关闭错误")} type="button" onClick={() => setError("")}><X size={15} /></button></div> : null}
      {statusMessage ? <div className="worker-settings-alert worker-settings-alert-success" role="status">{statusMessage}</div> : null}
      {selected ? <section className="runtime-detail" aria-label={tr("Worker details", "Worker 详情")}>
        <div className="runtime-detail-head"><div><span className="runtime-eyebrow">{tr("Selected Worker", "当前 Worker")}</span><h2>{selected.profile_key}</h2><p>{selected.account_key}</p></div><span className={`runtime-state-label runtime-state-large ${stateClass(selectedState)}`}><span className="runtime-state-dot" />{selectedState}</span></div>
        <dl className="runtime-facts"><div><dt>{tr("Provider", "Provider")}</dt><dd>{selected.worker?.provider || provider || "—"}</dd></div><div><dt>{tr("Model", "模型")}</dt><dd>{selected.worker?.model || model || "—"}</dd></div><div><dt>{tr("Supervisor", "托管器")}</dt><dd>{selected.worker?.supervisor || selected.supervisor || "screen"}</dd></div><div><dt>{tr("PID", "进程 PID")}</dt><dd>{selected.observed_worker?.pid || "—"}</dd></div></dl>
        {selected.last_error ? <div className="runtime-last-error"><strong>{tr("Last error", "最近错误")}</strong><span>{selected.last_error.message}</span></div> : null}
        {selected.checks?.length ? <div className="runtime-checks"><h3>{tr("Readiness checks", "就绪检查")}</h3><ul>{selected.checks.map((check) => <li className={stateClass(check.status)} key={check.name}><span>{check.name}</span><strong>{check.status}</strong>{check.message ? <small>{check.message}</small> : null}</li>)}</ul></div> : null}
        <div className="runtime-actions"><div><span>{tr("Worker lifecycle", "Worker 生命周期")}</span><small>{tr("Preflight before starting a draft.", "启动草稿前先执行预检。")}</small></div><button className="worker-button-secondary" disabled={busy || !selected} onClick={() => void action("preflight")} type="button"><ShieldCheck size={15} />{tr("Preflight", "运行预检")}</button><button className="worker-button-primary" disabled={busy || !selected} onClick={() => void action("start")} type="button"><Play size={15} />{tr("Start", "启动")}</button><button className="worker-button-secondary" disabled={busy || !selected || selectedState === "stopped" || selectedState === "draft"} onClick={() => void action("restart")} type="button"><RefreshCw size={15} />{tr("Restart", "重启")}</button><button className="worker-button-danger" disabled={busy || !selected || selectedState !== "running"} onClick={() => void action("stop")} type="button"><Square size={15} />{tr("Stop", "停止")}</button><button className="worker-icon-button" aria-label={tr("Refresh worker state", "刷新 Worker 状态")} title={tr("Refresh worker state", "刷新 Worker 状态")} disabled={busy || !selected} onClick={() => void action("status")} type="button"><RefreshCw size={15} /></button></div>
      </section> : <section className="runtime-detail runtime-detail-empty" aria-label={tr("Worker details", "Worker 详情")}>
        <CircleDashed size={24} /><strong>{records.length ? tr("Select a Worker", "选择一个 Worker") : tr("No Workers yet", "还没有 Worker")}</strong>
        <span>{records.length ? tr("Choose a saved Worker from the selector above to inspect its details and actions.", "使用上方筛选框选择 Worker，查看详情并执行操作。") : tr("Connect a Feishu account first, then return here to operate its Worker.", "先连接飞书账号，再回到这里运行 Worker。")}</span>
        {!records.length ? <button className="worker-button-primary" type="button" onClick={() => onNavigate?.("account")}><Bot size={15} />{tr("Connect Feishu", "连接飞书")}</button> : null}
      </section>}
    </div></div>;
  }

  return <div className="worker-settings"><div className="worker-settings-account">
    <div className="connection-progress"><ProvisioningSteps current={step} labels={stepLabels} language={language} onSelect={(index) => { if (!busy && index <= step) setStep(index); }} /></div>
    {error ? <div className="worker-settings-alert" role="alert"><span>{error}</span><button aria-label={tr("Dismiss error", "关闭错误")} type="button" onClick={() => setError("")}><X size={15} /></button></div> : null}
    {statusMessage ? <div className="worker-settings-alert worker-settings-alert-success" role="status">{statusMessage}</div> : null}
    <section className="connection-panel"><div className="connection-panel-head"><div><span className="connection-kicker">{tr("Feishu channel", "飞书渠道")}</span><h1>{tr("Feishu connection", "飞书连接")}</h1></div><span className="connection-security"><ShieldCheck size={15} />{tr("Protected credentials", "受保护凭据")}</span></div>
      {step === 0 ? <div className="connection-fields"><label className="connection-field connection-field-wide"><span className="connection-field-label">{tr("Published agent definition", "已发布智能体定义")}</span><select value={profileKey} onChange={(event) => setProfileKey(event.target.value)}><option value="">{tr("Select a published agent", "选择已发布智能体")}</option>{publishedProfiles.map((profile) => <option value={profile.profile_key} key={`${profile.profile_key}-${profile.profile_version}`}>{profile.display_name} · v{profile.profile_version}</option>)}</select><small>{tr("Only published definitions can receive a new Feishu binding.", "只有已发布定义可以接入新的飞书绑定。")}</small></label><div className="connection-inline-note connection-field-wide"><CheckCircle2 size={16} />{tr("Profile editing stays in Agent definitions.", "Profile 编辑请前往“智能体定义”。")}</div></div> : null}
      {step === 1 ? <div className="connection-fields"><div className="connection-mode connection-field-wide"><button aria-pressed={usingExistingAccount} className={usingExistingAccount ? "is-selected" : ""} type="button" onClick={() => { setUsingExistingAccount(true); setAccountKey(accounts[0]?.account_key || accountKey); }}><Bot size={18} /><span><strong>{tr("Saved account", "已保存账号")}</strong><small>{accounts[0]?.account_key || tr("Use an existing protected account", "使用已有的受保护账号")}</small></span></button><button aria-pressed={!usingExistingAccount} className={!usingExistingAccount ? "is-selected" : ""} type="button" onClick={() => { setUsingExistingAccount(false); setAccountKey(accountKey || `${profileKey}-feishu`); }}><WandSparkles size={18} /><span><strong>{tr("Connect a new bot", "连接新机器人")}</strong><small>{cli?.lark_cli ? tr("lark-cli ready · QR setup available", "lark-cli 已就绪 · 可扫码创建") : tr("One click installs lark-cli and opens QR setup", "一键安装 lark-cli 并打开扫码创建")}</small></span></button></div>{usingExistingAccount ? <label className="connection-field connection-field-wide"><span className="connection-field-label">{tr("Existing account", "已有账号")}</span><select value={accountKey} onChange={(event) => setAccountKey(event.target.value)}><option value="">{tr("Select an account", "选择账号")}</option>{accounts.map((account) => <option value={account.account_key} key={account.id}>{account.account_key} · {account.provider}</option>)}</select></label> : <><label className="connection-field"><span className="connection-field-label">{tr("Account key", "账号 Key")}</span><input value={accountKey} onChange={(event) => setAccountKey(event.target.value)} /></label><label className="connection-field"><span className="connection-field-label">{tr("Provider", "Provider")}</span><select value={provider} onChange={(event) => { setProvider(event.target.value); setModel(providers.find((item) => item.name === event.target.value)?.model || ""); }}><option value="">{tr("Select provider", "选择 Provider")}</option>{providers.map((item) => <option value={item.name} key={item.name}>{item.name} · {item.model}</option>)}</select></label><label className="connection-field connection-field-wide"><span className="connection-field-label">{tr("App ID", "App ID")}</span><input value={appId} onChange={(event) => setAppId(event.target.value)} placeholder="cli_..." /><small>{tr("Automatic setup fills this after approval.", "自动流程会在确认后填充。")}</small></label><label className="connection-field connection-field-wide"><span className="connection-field-label">{tr("App Secret", "App Secret")}</span><input type="password" value={appSecret} onChange={(event) => setAppSecret(event.target.value)} autoComplete="new-password" /><small>{tr("Manual fallback only; QR setup never exposes the secret.", "仅作为手动兜底；扫码流程不会暴露 Secret。")}</small></label></>}</div> : null}
      {step === 2 ? <div className="connection-fields"><div className="connection-field-wide">{onboarding?.verification_qr_code && onboarding.status !== "created" ? <div className="connection-qr"><div><span className="connection-kicker">{tr("Scan in Feishu", "请在飞书扫码")}</span><h3>{tr("Create your bot", "创建你的机器人")}</h3><p>{tr("Scan this QR code in Feishu. The official registration flow will finish in the background and save protected credentials to this tenant.", "用飞书扫描二维码。官方注册流程会在后台完成，并把受保护凭据保存到当前租户。")}</p><small>{onboarding.verification_expires_in ? tr(`Expires in ${onboarding.verification_expires_in}s`, `${onboarding.verification_expires_in} 秒后过期`) : tr("Waiting for approval", "等待确认")}</small></div><img alt={tr("Feishu bot creation QR code", "飞书机器人创建二维码")} src={onboarding.verification_qr_code} /></div> : null}{onboarding?.status === "failed" ? <div className="worker-settings-alert" role="alert">{onboarding.error || tr("Automatic onboarding failed.", "自动创建机器人失败。")}</div> : null}{onboarding?.status === "canceled" ? <div className="connection-inline-note"><CircleDashed size={16} />{tr("QR setup was canceled.", "扫码创建已取消。")}</div> : null}{onboarding?.status === "created" || (!onboarding && selected) ? <div className="connection-saved"><span className="connection-saved-icon"><CheckCircle2 size={24} /></span><div><h3>{tr("Connection draft saved", "连接草稿已保存")}</h3><p>{tr(`${selectedProfile?.display_name || profileKey} is connected to ${accountKey}. Worker has not been started.`, `${selectedProfile?.display_name || profileKey} 已连接到 ${accountKey}。Worker 尚未启动。`)}</p></div><button className="worker-button-primary" type="button" onClick={() => onNavigate?.("lifecycle")}><ArrowRight size={15} />{tr("Go to Worker runtime", "前往 Worker 运行")}</button></div> : null}</div></div> : null}
      <div className="connection-footer"><span className="connection-inline-note">{busy ? tr("Working…", "处理中…") : onboarding?.status === "waiting_for_scan" ? tr("Waiting for Feishu scan", "等待飞书扫码") : tr("Secrets are never shown in the browser.", "密钥不会显示在浏览器中。")}</span><div className="connection-actions">{step > 0 ? <button className="worker-button-secondary" disabled={busy} type="button" onClick={() => setStep((current) => current - 1)}>{tr("Back", "返回")}</button> : null}{step === 0 ? <button className="worker-button-primary" disabled={busy || !profileKey} type="button" onClick={() => setStep(1)}>{tr("Continue", "继续")}<ArrowRight size={15} /></button> : null}{step === 1 && usingExistingAccount ? <button className="worker-button-primary" disabled={busy || !accountKey} type="button" onClick={() => void createConnectionDraft()}><Bot size={15} />{tr("Save connection draft", "保存连接草稿")}</button> : null}{step === 1 && !usingExistingAccount ? <><button className="worker-button-primary" disabled={busy || !accountKey} type="button" onClick={() => void startAutomaticOnboarding()}><WandSparkles size={15} />{tr("Install and create with QR", "自动安装并扫码创建")}</button><button className="worker-button-secondary" disabled={busy || !accountKey || !appSecret} type="button" onClick={() => void createConnectionDraft()}><KeyRound size={15} />{tr("Use manual credentials", "使用手动凭据")}</button></> : null}{step === 2 && onboarding && (onboarding.status === "starting" || onboarding.status === "waiting_for_scan") ? <button className="worker-button-secondary" disabled={busy} type="button" onClick={() => void cancelOnboarding()}><X size={15} />{tr("Cancel QR setup", "取消扫码创建")}</button> : null}</div></div>
    </section>
  </div></div>;
}
