import { ArrowLeft, ChevronRight, Monitor, PanelLeft, ShieldCheck, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { SessionRef, SettingsSection } from "../routes";
import { useSessionControlClient } from "../api/sessionControlClient";
import type { WebUIV2Theme } from "../components/SettingsDrawer";
import { AgentSettingsPanel } from "./AgentSettingsPanel";
import { PromptManager } from "./PromptManager";
import { ProfileSettingsPanel } from "./ProfileSettingsPanel";
import { SettingsDocumentPanel } from "./SettingsDocumentPanel";
import { useGlobalSettingsDraft } from "./globalSettingsDraft";
import { EffectiveSettingsPanel } from "./EffectiveSettingsPanel";
import { P2ManagementPanel } from "./P2ManagementPanel";
import { AgentTeamsPanel } from "../../components/AgentTeamsPanel";
import { ProvisioningWizard } from "../../components/provisioning/ProvisioningWizard";
import { CURRENT_SETTINGS_ENVIRONMENT, settingsEnvironmentIdentity, useSettingsEnvironments, type SettingsEnvironment } from "./settingsEnvironments";
import { SETTINGS_NAV_GROUPS, settingsNavItem } from "./settingsRegistry";
import { AppearanceSettingsPanel } from "./AppearanceSettingsPanel";
import { PetSettingsPanel } from "./PetSettingsPanel";
import { ObservabilityPanel } from "./ObservabilityPanel";
import { readVisualSettings, type GlobalVisualSettings } from "./globalVisualSettings";
import { SettingsSelect } from "./SettingsSelect";
import { SessionBackendSettingsPanel } from "./SessionBackendSettingsPanel";
import { getDesktopServiceBridge } from "../desktopServiceBridge";
import { COMPUTER_WORKSPACE_DISPLAY_MODES, COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT, loadComputerWorkspacePreferences, saveComputerWorkspacePreferences, type ComputerWorkspaceDisplayMode } from "../computer/computerWorkspacePreferences";
import { messageExperienceCopy } from "../components/messageExperienceCopy";
import { setThinkingMode, useThinkingMode } from "../components/thinkingPreference";
import brandLogo from "../assets/niuma-mark.svg";
import "./settingsCenter.css";
import { useDesktopBackground } from "./desktopBackground";

const currentEnvironmentSections = new Set<SettingsSection>(["prompts", "memory", "skills", "teams", "feishu", "provisioning"]);

type Props = {
  identity: IdentityConfig;
  section: SettingsSection;
  onSectionChange: (section: SettingsSection) => void;
  onBack: () => void;
  onRequestNavigation: (action: () => void) => void;
  onDirtyChange: (dirty: boolean) => void;
  onBusyChange: (busy: boolean) => void;
  theme: WebUIV2Theme;
  onThemeChange: (theme: WebUIV2Theme) => void;
  inspectorOpen: boolean;
  onInspectorChange: (open: boolean) => void;
  selectedRef: SessionRef | null;
  onOpenSession: (ref: SessionRef) => void;
  onVisualPreview: (settings: GlobalVisualSettings | null) => void;
};

export function SettingsCenter(props: Props): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const environments = useSettingsEnvironments(props.identity);
  const [environmentID, setEnvironmentID] = useState(CURRENT_SETTINGS_ENVIRONMENT);
  const [busy, setBusy] = useState(false);
  const fallback: SettingsEnvironment = { id: CURRENT_SETTINGS_ENVIRONMENT, label: zh ? "Web 对话" : "Web chat", database: "", tenant_key: props.identity.tenantKey, user_id: props.identity.userId, api_path: "", available: true };
  const items = environments.data?.environments || [fallback];
  const environment = items.find((item) => item.id === environmentID);
  const environmentKey = environment?.id;
  const environmentAPIPath = environment?.api_path;
  // Catalog refreshes must not replace the connection object and reset editors.
  const identity = useMemo(() => environmentKey ? settingsEnvironmentIdentity(props.identity, { id: environmentKey, api_path: environmentAPIPath || "" }) : props.identity, [props.identity, environmentKey, environmentAPIPath]);
  const dirtyChanged = useCallback((value: boolean) => { props.onDirtyChange(value); }, [props.onDirtyChange]);
  function switchEnvironment(next: string): void {
    if (busy || next === environmentID) return;
    props.onRequestNavigation(() => {
      props.onDirtyChange(false);
      setEnvironmentID(next);
    });
  }
  const selector = <label className="settings-environment-selector" htmlFor="settings-environment-select"><span>{zh ? "设置环境" : "Settings environment"}</span><SettingsSelect id="settings-environment-select" ariaLabel={zh ? "设置环境" : "Settings environment"} value={environmentID} disabled={busy || environments.isPending} onChange={switchEnvironment} options={items.map((item) => ({ value: item.id, disabled: !item.available, label: <>{item.id === CURRENT_SETTINGS_ENVIRONMENT ? (zh ? "Web 对话" : "Web chat") : item.label}{!item.available ? (zh ? "（连接不可用）" : " (unavailable)") : ""}</> }))} /><small>{environment?.database || (zh ? "当前服务" : "Current server")}</small><small>{environment?.tenant_key} / {environment?.user_id}</small>{environments.error ? <span role="alert">{zh ? "环境目录读取失败" : "Environment catalog unavailable"}</span> : null}</label>;
  if (!environment) return <section className="webui2-settings-center"><aside className="settings-navigation">{selector}<button type="button" onClick={() => switchEnvironment(CURRENT_SETTINGS_ENVIRONMENT)}>{zh ? "返回当前环境" : "Return to current environment"}</button></aside><p role="alert">{zh ? "所选环境不再可用，请重新选择。" : "Selected environment is no longer available."}</p></section>;
  return <SettingsEnvironmentContent key={environment.id} {...props} identity={identity} globalIdentity={props.identity} environment={environment} selector={selector} settingsPath={environments.data?.global_settings_path || ""} sharedSettings={Boolean(environments.data?.global_settings_shared)} onDirtyChange={dirtyChanged} onBusyChange={(value) => { setBusy(value); props.onBusyChange(value); }} />;
}

function SettingsEnvironmentContent(props: Props & { globalIdentity: IdentityConfig; environment: SettingsEnvironment; selector: JSX.Element; settingsPath: string; sharedSettings: boolean; onBusyChange: (busy: boolean) => void }): JSX.Element {
  const { identity, section, onSectionChange, onBack, onDirtyChange, selectedRef, onOpenSession } = props;
  const { language } = useI18n();
  const zh = language === "zh";
  const [navOpen, setNavOpen] = useState(false);
  const [visited, setVisited] = useState(() => new Set<SettingsSection>([section]));
  const [profileDirty, setProfileDirty] = useState(false);
  const [agentDirty, setAgentDirty] = useState(false);
  const [profileBusy, setProfileBusy] = useState(false);
  const [agentBusy, setAgentBusy] = useState(false);
  const [profileRevision, setProfileRevision] = useState(0);
  const [navigationError, setNavigationError] = useState("");
  const desktopBridge = getDesktopServiceBridge();
  const draft = useGlobalSettingsDraft(props.globalIdentity, visited.has("models") || visited.has("json") || visited.has("appearance") || visited.has("pet"));
  const desktopBackground = useDesktopBackground();
  const sessionClient = useSessionControlClient();
  const isolated = props.environment.id !== CURRENT_SETTINGS_ENVIRONMENT;
  const sectionAvailable = !isolated || !currentEnvironmentSections.has(section);
  const busy = draft.busy || profileBusy || agentBusy;
  const dirty = draft.dirty || profileDirty || agentDirty;
  const item = settingsNavItem(section);
  const profilesChanged = useCallback(() => setProfileRevision((revision) => revision + 1), []);

  useEffect(() => { setVisited((previous) => previous.has(section) ? previous : new Set([...previous, section])); setNavOpen(false); }, [section]);
  useEffect(() => { onDirtyChange(dirty); props.onBusyChange(busy); }, [dirty, busy, onDirtyChange, props.onBusyChange]);
  useEffect(() => {
    props.onVisualPreview(draft.doc ? readVisualSettings(draft.doc, desktopBackground.background) : null);
    return () => props.onVisualPreview(null);
  }, [desktopBackground.background, draft.doc, props.onVisualPreview]);
  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent): void => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const openProfileSession = useCallback((sessionID: number): void => {
    void sessionClient.list(identity, { query: "", statuses: [] }).then((sessions) => {
      const session = sessions.find((entry) => entry.id === sessionID && entry.source === "tenant");
      if (session) onOpenSession(session.ref);
      else setNavigationError(zh ? "会话不在当前可访问列表中。" : "Session is not in the accessible list.");
    }).catch(() => setNavigationError(zh ? "会话读取失败" : "Unable to load session"));
  }, [sessionClient, identity, onOpenSession, zh]);

  return <section aria-label={zh ? "设置中心" : "Settings center"} className="webui2-settings-center">
    {navOpen ? <button type="button" className="settings-nav-backdrop" aria-label={zh ? "关闭设置导航" : "Close settings navigation"} onClick={() => setNavOpen(false)} /> : null}
    <aside className="settings-navigation" data-open={navOpen}>
      <div className="settings-brand"><img alt="" src={brandLogo} /><span>{zh ? "牛马 Niuma" : "Niuma"}</span><button className="settings-mobile-nav-close settings-icon-button" aria-label={zh ? "关闭设置导航" : "Close settings navigation"} onClick={() => setNavOpen(false)} type="button"><X size={18} /></button></div>
      <button className="settings-back" onClick={onBack} type="button"><ArrowLeft size={16} />{zh ? "返回会话" : "Back to chat"}</button>
      {props.selector}
      <nav aria-label={zh ? "设置分类" : "Settings categories"}>{SETTINGS_NAV_GROUPS.map((group) => <div className="settings-nav-group" key={group.key}><div className="settings-nav-label">{zh ? group.zh : group.en}</div>{group.items.filter(({ key, desktopOnly }) => !(isolated && key === "feishu") && !(desktopOnly && !desktopBridge)).map(({ key, icon: Icon, zh: chinese, en, advanced }) => <button aria-current={section === key ? "page" : undefined} disabled={isolated && currentEnvironmentSections.has(key)} title={isolated && currentEnvironmentSections.has(key) ? (zh ? "此环境不支持该设置" : "Unavailable in this environment") : undefined} className={advanced ? "settings-nav-advanced" : undefined} key={key} onClick={() => onSectionChange(key)} type="button"><Icon aria-hidden="true" size={17} /><span>{zh ? chinese : en}</span>{((key === "models" || key === "json" || key === "appearance" || key === "pet") && draft.dirty) || (key === "profiles" && profileDirty) || (key === "agent" && agentDirty) ? <span role="img" aria-label={zh ? "未保存" : "Unsaved"} className="settings-dirty-dot" /> : null}</button>)}</div>)}</nav>
      <div className="settings-identity"><span className="settings-avatar">{(props.environment.user_id || "U").slice(0, 1).toUpperCase()}</span><div><strong>{props.environment.tenant_key}</strong><small>{props.environment.user_id}</small></div></div>
    </aside>
    <div className={`settings-main settings-main-${section}`}>
      <header className="settings-topbar"><div className="settings-breadcrumb"><button className="settings-mobile-nav-open settings-icon-button" aria-label={zh ? "打开设置导航" : "Open settings navigation"} title={zh ? "设置导航" : "Settings navigation"} onClick={() => setNavOpen(true)} type="button"><PanelLeft size={18} /></button><span>{zh ? "设置" : "Settings"}</span><ChevronRight size={13} /><strong>{zh ? item.zh : item.en}</strong></div><span className="settings-scope"><Monitor size={14} />{identity.apiBase ? new URL(identity.apiBase, window.location.origin).host : window.location.host}</span></header>
      <div className={`settings-content settings-content-${section}`}>
        <div className="settings-active-environment">{zh ? "设置环境" : "Settings environment"}: <strong>{isolated ? props.environment.label : (zh ? "Web 对话" : "Web chat")}</strong></div>
        {section !== "feishu" && section !== "provisioning" ? <div className="settings-page-heading"><div><h1>{zh ? item.zh : item.en}</h1><p>{item.description[zh ? 0 : 1]}</p></div>{dirty ? <span className="settings-badge settings-badge-warning">{zh ? "有未保存的更改" : "Unsaved changes"}</span> : null}</div> : null}
        {props.settingsPath && (section === "models" || section === "json" || section === "effective") ? section === "models" ?
          <details className="model-config-details">
            <summary>{zh ? "配置保存位置与生效范围" : "Storage location and when changes take effect"}</summary>
            <SettingsStorageNotice path={props.settingsPath} shared={props.sharedSettings} />
          </details> : <SettingsStorageNotice path={props.settingsPath} shared={props.sharedSettings} snapshotWarning={isolated && section === "effective"} /> : null}
        {navigationError ? <p role="alert" className="settings-error">{navigationError}</p> : null}
        {!sectionAvailable ? <p role="alert" className="settings-environment-notice">{zh ? "此环境不支持该设置，请选择 Web 对话环境。" : "This section is unavailable in this environment. Select the Web chat environment."}</p> : null}
        {visited.has("general") ? <div hidden={section !== "general"}><GeneralSettings {...props} identity={{ ...identity, tenantKey: props.environment.tenant_key, userId: props.environment.user_id }} /></div> : null}
        {visited.has("appearance") ? <div hidden={section !== "appearance"}><AppearanceSettingsPanel draft={draft} desktopBackground={desktopBackground} /></div> : null}
        {visited.has("pet") ? <div hidden={section !== "pet"}><PetSettingsPanel draft={draft} /></div> : null}
        {visited.has("agent") ? <div hidden={section !== "agent"}><AgentSettingsPanel identity={identity} onDirtyChange={setAgentDirty} onBusyChange={setAgentBusy} refreshVersion={profileRevision} /></div> : null}
        {visited.has("profiles") ? <div hidden={section !== "profiles"}><ProfileSettingsPanel identity={identity} onDirtyChange={setProfileDirty} onBusyChange={setProfileBusy} isolatedEnvironment={isolated} onOpenSession={isolated ? undefined : openProfileSession} onDataChanged={profilesChanged} /></div> : null}
        {sectionAvailable && section === "prompts" ? <PromptManager identity={identity} /> : null}
        {sectionAvailable && (section === "memory" || section === "skills") ? <P2ManagementPanel identity={identity} section={section} /> : null}
        {sectionAvailable && section === "teams" ? <AgentTeamsPanel identity={identity} onStatus={setNavigationError} onDataChanged={profilesChanged} /> : null}
        {sectionAvailable && section === "feishu" ? <ProvisioningWizard identity={identity} onStatus={setNavigationError} onNavigate={(view) => props.onRequestNavigation(() => onSectionChange(view === "account" ? "feishu" : "provisioning"))} view="account" /> : null}
        {sectionAvailable && section === "provisioning" ? <ProvisioningWizard identity={identity} onStatus={setNavigationError} onNavigate={(view) => props.onRequestNavigation(() => onSectionChange(view === "account" ? "feishu" : "provisioning"))} view="lifecycle" /> : null}
        {sectionAvailable && section === "observability" ? <ObservabilityPanel identity={identity} /> : null}
        {section === "session-backend" ? <SessionBackendSettingsPanel bridge={desktopBridge} onDirtyChange={props.onDirtyChange} onBusyChange={props.onBusyChange} /> : null}
        {section === "models" || section === "json" ? <SettingsDocumentPanel draft={draft} view={section} /> : null}
        {section === "effective" ? <EffectiveSettingsPanel identity={props.globalIdentity} selectedRef={isolated ? null : selectedRef} /> : null}
      </div>
    </div>
  </section>;
}

function GeneralSettings({ identity, theme, onThemeChange, inspectorOpen, onInspectorChange }: Props): JSX.Element {
  const { language, setLanguage, t } = useI18n();
  const zh = language === "zh";
  const thinkingMode = useThinkingMode();
  const copy = messageExperienceCopy[language];
  const [computerDisplayMode, setComputerDisplayMode] = useState<ComputerWorkspaceDisplayMode>(() => loadComputerWorkspacePreferences().displayMode);
  const updateComputerDisplayMode = (value: string): void => {
    if (!Object.values(COMPUTER_WORKSPACE_DISPLAY_MODES).includes(value as ComputerWorkspaceDisplayMode)) return;
    const displayMode = value as ComputerWorkspaceDisplayMode;
    const current = loadComputerWorkspacePreferences();
    saveComputerWorkspacePreferences({ ...current, displayMode });
    setComputerDisplayMode(displayMode);
    window.dispatchEvent(new Event(COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT));
  };
  return <div className="settings-general">
    <section className="settings-section"><h2>{zh ? "语言" : "Language"}</h2><div className="settings-form-grid"><label htmlFor="settings-language-select">{t("webui2.language")}<SettingsSelect id="settings-language-select" ariaLabel={t("webui2.language")} value={language} onChange={(value) => setLanguage(value === "zh" ? "zh" : "en")} options={[{ value: "zh", label: "简体中文" }, { value: "en", label: "English" }]} /></label></div></section>
    <section className="settings-section"><h2>{zh ? "外观" : "Appearance"}</h2><fieldset aria-label={t("webui2.theme")} className="settings-theme-options">{(["light", "dark"] as const).map((value) => <button aria-pressed={theme === value} key={value} onClick={() => onThemeChange(value)} type="button"><span aria-hidden="true" className="settings-theme-sample" data-theme={value}><span /></span>{t(`webui2.theme${value === "light" ? "Light" : "Dark"}`)}</button>)}</fieldset></section>
    <section className="settings-section"><h2>{zh ? "对话界面" : "Conversation"}</h2><div className="settings-preference-stack">
      <div className="settings-preference-row"><div><strong>{zh ? "Inspector 默认展开" : "Open Inspector by default"}</strong><p>{zh ? "查看会话活动、上下文与运行详情。" : "Show activity, context and run details."}</p></div><input aria-label={t("webui2.inspector")} aria-checked={inspectorOpen} checked={inspectorOpen} onChange={(event) => onInspectorChange(event.target.checked)} type="checkbox" role="switch" /></div>
      <div className="settings-preference-row"><div><strong>{copy.thinking}</strong><p>{zh ? "选择会话中思考过程的展示方式。" : "Choose how thinking details appear in conversations."}</p></div><SettingsSelect ariaLabel={copy.thinking} className="settings-preference-select" id="settings-thinking-mode-select" onChange={(value) => setThinkingMode(value as typeof thinkingMode)} options={[{ value: "full", label: copy.full }, { value: "summary", label: copy.summary }, { value: "hidden", label: copy.hidden }]} value={thinkingMode} /></div>
    </div></section>
    <section className="settings-section"><h2>{zh ? "Computer Use 显示" : "Computer Use display"}</h2><div className="settings-preference-stack">
      <div className="settings-preference-row"><div><strong>{zh ? "显示方式" : "Display mode"}</strong><p>{zh ? "空闲时隐藏，需要操作时自动显示；也可以固定为紧凑浮层或展开面板。" : "Hide when idle and show when needed, or keep a compact launcher or expanded panel visible."}</p></div><SettingsSelect ariaLabel={zh ? "Computer Use 显示方式" : "Computer Use display mode"} className="settings-preference-select" id="settings-computer-display-mode" value={computerDisplayMode} onChange={updateComputerDisplayMode} options={[{ value: COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO, label: zh ? "自动显示（推荐）" : "Automatic (recommended)" }, { value: COMPUTER_WORKSPACE_DISPLAY_MODES.COMPACT, label: zh ? "始终显示紧凑浮层" : "Always show compact launcher" }, { value: COMPUTER_WORKSPACE_DISPLAY_MODES.EXPANDED, label: zh ? "始终展开详细面板" : "Always show expanded panel" }]} /></div>
    </div></section>
    <section className="settings-section"><h2>{zh ? "连接身份" : "Connection identity"}</h2><dl className="settings-identity-fields"><div><dt>{zh ? "租户" : "Tenant"}</dt><dd>{identity.tenantKey || "—"}</dd></div><div><dt>{zh ? "用户" : "User"}</dt><dd>{identity.userId || "—"}</dd></div></dl><div className="settings-notice"><ShieldCheck size={16} /><span>{identity.apiToken ? t("webui2.authTokenPresent") : t("webui2.authTokenMissing")}</span><span>{identity.mobileJwt ? t("webui2.authMobilePresent") : t("webui2.authMobileMissing")}</span></div></section>
  </div>;
}

function SettingsStorageNotice({ path, shared, snapshotWarning }: { path: string; shared?: boolean; snapshotWarning?: boolean }) {
  const { language } = useI18n();
  const zh = language === "zh";
  return <div className="settings-environment-notice">
    <strong>{shared ? (zh ? "全局配置由 Web 服务与渠道 Worker 共用" : "Global settings shared by Web and channel workers") : (zh ? "全局配置文件" : "Global settings file")}</strong>
    <code>{path}</code>
    {shared && <span>{zh ? "保存影响共用此文件的服务；运行中的任务不变，部分配置需重启对应服务生效。" : "Saving affects services sharing this file. Active runs are unchanged; some settings require a service restart."}</span>}
    {snapshotWarning && <span>{zh ? "启动快照来自当前管理服务，不代表 screen worker 的进程内配置。" : "Startup snapshot belongs to this management service, not the screen workers."}</span>}
  </div>;
}
