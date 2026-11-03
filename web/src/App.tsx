import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Activity, Bot, Brain, CheckCircle2, Database, FileSearch, Flag, Gauge, History, Image as ImageIcon, KeyRound, MessageSquareText, MonitorCog, RefreshCcw, Settings, Settings2, SlidersHorizontal, Sparkles, Users, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { ChatLab } from "./components/ChatLab";
import { ContextPanel } from "./components/ContextPanel";
import { GoalWorkbench } from "./components/GoalWorkbench";
import { AgentTeamsPanel } from "./components/AgentTeamsPanel";
import { InspectorPanels, type InspectorSection } from "./components/InspectorPanels";
import { SettingsPanel } from "./components/SettingsPanel";
import { WebAgentPage } from "./components/WebAgentPage";
import { ImageGenerationWorkbench } from "./components/image/ImageGenerationWorkbench";
import { listTenants, listTenantUsers } from "./lib/api";
import { defaultIdentity, isDesktopHost, isDesktopV2Host, loadIdentity, normalizeIdentity, saveIdentity } from "./lib/config";
import { useI18n } from "./lib/i18n";
import { readProductStorage } from "./lib/productStorage";
import { seedValidationScenario } from "./lib/scenario";
import type { IdentityConfig, TenantRecord, TenantUserRecord } from "./lib/types";
import { WebUIV2App } from "./v2/WebUIV2App";
import niumaMark from "./v2/assets/niuma-mark.svg";

type PrimarySection = "chat" | "knowledge" | "skills" | "goals" | "images" | "observability" | "context" | "config" | "teams";
type SecondarySection = "conversation" | InspectorSection | "goals" | "images" | "run-context" | "global-config" | "teams" | "team-operations";

const primarySections: Array<{
  key: PrimarySection;
  labelKey: string;
  detailKey: string;
  icon: React.ReactNode;
}> = [
  { key: "chat", labelKey: "nav.chat", detailKey: "nav.chat.detail", icon: <MessageSquareText size={18} /> },
  { key: "knowledge", labelKey: "nav.knowledge", detailKey: "nav.knowledge.detail", icon: <Brain size={18} /> },
  { key: "skills", labelKey: "nav.skills", detailKey: "nav.skills.detail", icon: <Sparkles size={18} /> },
  { key: "goals", labelKey: "nav.goals", detailKey: "nav.goals.detail", icon: <Flag size={18} /> },
  { key: "images", labelKey: "nav.images", detailKey: "nav.images.detail", icon: <ImageIcon size={18} /> },
  { key: "observability", labelKey: "nav.observability", detailKey: "nav.observability.detail", icon: <Activity size={18} /> },
  { key: "context", labelKey: "nav.context", detailKey: "nav.context.detail", icon: <Settings2 size={18} /> },
  { key: "config", labelKey: "nav.config", detailKey: "nav.config.detail", icon: <SlidersHorizontal size={18} /> }
  ,{ key: "teams", labelKey: "nav.teams", detailKey: "nav.teams.detail", icon: <Users size={18} /> }
];

const secondarySections: Record<
  PrimarySection,
  Array<{ key: SecondarySection; labelKey: string; detailKey: string; icon: React.ReactNode }>
> = {
  chat: [
    { key: "conversation", labelKey: "tab.conversation", detailKey: "tab.conversation.detail", icon: <MessageSquareText size={16} /> },
    { key: "validation", labelKey: "tab.validation", detailKey: "tab.validation.detail", icon: <CheckCircle2 size={16} /> }
  ],
  knowledge: [
    { key: "memory", labelKey: "tab.memory", detailKey: "tab.memory.detail", icon: <Brain size={16} /> },
    { key: "profile", labelKey: "tab.profile", detailKey: "tab.profile.detail", icon: <Database size={16} /> },
    { key: "documents", labelKey: "tab.documents", detailKey: "tab.documents.detail", icon: <Database size={16} /> },
    { key: "knowledge-search", labelKey: "tab.knowledgeSearch", detailKey: "tab.knowledgeSearch.detail", icon: <FileSearch size={16} /> },
    { key: "scoped-memory", labelKey: "tab.scopedMemory", detailKey: "tab.scopedMemory.detail", icon: <Database size={16} /> },
    { key: "automem", labelKey: "tab.automem", detailKey: "tab.automem.detail", icon: <Brain size={16} /> }
  ],
  skills: [{ key: "skills", labelKey: "tab.skills", detailKey: "tab.skills.detail", icon: <Sparkles size={16} /> }],
  goals: [{ key: "goals", labelKey: "tab.goals", detailKey: "tab.goals.detail", icon: <Flag size={16} /> }],
  images: [{ key: "images", labelKey: "tab.images", detailKey: "tab.images.detail", icon: <ImageIcon size={16} /> }],
  observability: [
    { key: "agents", labelKey: "tab.agents", detailKey: "tab.agents.detail", icon: <Bot size={16} /> },
    { key: "loops", labelKey: "tab.loops", detailKey: "tab.loops.detail", icon: <RefreshCcw size={16} /> },
    { key: "trace", labelKey: "tab.trace", detailKey: "tab.trace.detail", icon: <Activity size={16} /> },
    { key: "local-trace", labelKey: "tab.localTrace", detailKey: "tab.localTrace.detail", icon: <History size={16} /> },
    { key: "quota", labelKey: "tab.quota", detailKey: "tab.quota.detail", icon: <Gauge size={16} /> },
    { key: "telemetry", labelKey: "tab.telemetry", detailKey: "tab.telemetry.detail", icon: <Activity size={16} /> }
  ],
  context: [{ key: "run-context", labelKey: "tab.runtime", detailKey: "tab.runtime.detail", icon: <Settings2 size={16} /> }],
  config: [{ key: "global-config", labelKey: "tab.globalConfig", detailKey: "tab.globalConfig.detail", icon: <SlidersHorizontal size={16} /> }]
  ,teams: [
    { key: "teams", labelKey: "tab.agentTeamWorkspace", detailKey: "tab.agentTeamWorkspace.detail", icon: <Users size={16} /> },
    { key: "team-operations", labelKey: "tab.agentTeamOperations", detailKey: "tab.agentTeamOperations.detail", icon: <History size={16} /> }
  ]
};

const sidebarWidthStorageKey = "niuma-webui.sidebar-width.v1";
const sidebarMinWidth = 232;
const sidebarMaxWidth = 420;
const sidebarDefaultWidth = 284;

function clampSidebarWidth(value: number): number {
  return Math.min(sidebarMaxWidth, Math.max(sidebarMinWidth, Math.round(value)));
}

function loadSidebarWidth(): number {
  try {
	    const raw = readProductStorage(sidebarWidthStorageKey);
    return raw ? clampSidebarWidth(Number(raw)) : sidebarDefaultWidth;
  } catch {
    return sidebarDefaultWidth;
  }
}

function translationsReady(language: "en" | "zh"): string {
  return language === "zh" ? "就绪" : "Ready";
}

function tenantFallback(identity: IdentityConfig): TenantRecord {
  return {
    tenant_key: identity.tenantKey,
    name: identity.tenantKey,
    status: "current"
  };
}

function normalizeTenants(tenants: TenantRecord[], currentTenantKey: string): TenantRecord[] {
  const byKey = new Map<string, TenantRecord>();
  for (const tenant of tenants) {
    const tenantKey = tenant.tenant_key?.trim();
    if (tenantKey) {
      byKey.set(tenantKey, tenant);
    }
  }
  if (currentTenantKey.trim() !== "" && !byKey.has(currentTenantKey)) {
    byKey.set(currentTenantKey, { tenant_key: currentTenantKey, name: currentTenantKey, status: "current" });
  }
  return Array.from(byKey.values());
}

function tenantLabel(tenant: TenantRecord): string {
  const tenantKey = tenant.tenant_key || "";
  const name = tenant.name?.trim();
  if (name && name !== tenantKey) {
    return `${name} (${tenantKey})`;
  }
  return tenantKey;
}

function userFallback(identity: IdentityConfig): TenantUserRecord {
  return {
    user_key: identity.userId,
    display_name: identity.userId,
    role: identity.role || "member",
    status: "current"
  };
}

function normalizeUsers(users: TenantUserRecord[], currentUserId: string): TenantUserRecord[] {
  const byKey = new Map<string, TenantUserRecord>();
  for (const user of users) {
    const userKey = user.user_key?.trim();
    if (userKey) {
      byKey.set(userKey, user);
    }
  }
  if (currentUserId.trim() !== "" && !byKey.has(currentUserId)) {
    byKey.set(currentUserId, { user_key: currentUserId, display_name: currentUserId, status: "current" });
  }
  return Array.from(byKey.values());
}

function userLabel(user: TenantUserRecord): string {
  const userKey = user.user_key || "";
  const displayName = user.display_name?.trim();
  if (displayName && displayName !== userKey) {
    return `${displayName} (${userKey})`;
  }
  return userKey;
}

function initialImageView(): { active: boolean; sessionID?: number; prompt?: string } {
  if (typeof window === "undefined") return { active: false };
  const params = new URLSearchParams(window.location.search);
  if (params.get("view") !== "images") return { active: false };
  const sessionID = Number(params.get("session_id")) || undefined;
  return { active: true, sessionID, prompt: params.get("prompt") || undefined };
}

function identityWithUser(identity: IdentityConfig, user?: TenantUserRecord): IdentityConfig {
  if (!user?.user_key) {
    return identity;
  }
  return { ...identity, userId: user.user_key, role: user.role || "member" };
}

export function App() {
  const { language, setLanguage, t } = useI18n();
  const queryClient = useMemo(() => new QueryClient(), []);
  const [identity, setIdentity] = useState<IdentityConfig>(() => loadIdentity());
  const [tenants, setTenants] = useState<TenantRecord[]>(() => [tenantFallback(identity)]);
  const [tenantLoadError, setTenantLoadError] = useState("");
  const [users, setUsers] = useState<TenantUserRecord[]>(() => [userFallback(identity)]);
  const [userLoadError, setUserLoadError] = useState("");
  const imageView = useMemo(initialImageView, []);
  const [selectedSessionId, setSelectedSessionId] = useState<number | null>(imageView.sessionID || null);
  const [status, setStatus] = useState(() => t("app.ready"));
  const [showWelcome, setShowWelcome] = useState(!imageView.active);
  const [primarySection, setPrimarySection] = useState<PrimarySection>(imageView.active ? "images" : "chat");
  const [secondarySection, setSecondarySection] = useState<SecondarySection>(imageView.active ? "images" : "conversation");
  const [refreshTick, setRefreshTick] = useState(0);
  const [sidebarWidth, setSidebarWidth] = useState(loadSidebarWidth);
  const [sidebarDrag, setSidebarDrag] = useState<{ startX: number; startWidth: number } | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);

  useEffect(() => {
    if (!isDesktopV2Host()) return;
    const applyDesktopToken = (): void => {
      setIdentity((current) => {
        const next = normalizeIdentity(current);
        return current.apiToken === next.apiToken && current.apiBase === next.apiBase && current.mobileJwt === next.mobileJwt ? current : next;
      });
    };
    applyDesktopToken();
    window.addEventListener("niuma-desktop-token", applyDesktopToken);
    return () => window.removeEventListener("niuma-desktop-token", applyDesktopToken);
  }, []);

  const activeSecondarySections = secondarySections[primarySection];

  useEffect(() => {
    setStatus((current) => (current === translationsReady("en") || current === translationsReady("zh") ? t("app.ready") : current));
  }, [t]);

  useEffect(() => {
    setTenants((current) => normalizeTenants(current, identity.tenantKey));
  }, [identity.tenantKey]);

  useEffect(() => {
    setUsers((current) => normalizeUsers(current, identity.userId));
  }, [identity.userId]);

  useEffect(() => {
    let cancelled = false;

    async function loadTenantList() {
      try {
        const tenantList = await listTenants(identity);
        if (cancelled) {
          return;
        }
        setTenantLoadError("");
        setTenants(normalizeTenants(tenantList, identity.tenantKey));
      } catch (err) {
        if (cancelled) {
          return;
        }
        setTenantLoadError(err instanceof Error ? err.message : String(err));
        setTenants((current) => normalizeTenants(current, identity.tenantKey));
      }
    }

    void loadTenantList();
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, identity.deviceId, identity.userId]);

  useEffect(() => {
    let cancelled = false;

    async function loadUserList() {
      try {
        const userList = await listTenantUsers(identity);
        if (cancelled) {
          return;
        }
        setUserLoadError("");
        setUsers(normalizeUsers(userList, identity.userId));
        const currentUser = userList.find((user) => user.user_key === identity.userId);
        if (currentUser?.role && currentUser.role !== identity.role) {
          updateIdentity({ ...identity, role: currentUser.role }, { persist: true });
        }
      } catch (err) {
        if (cancelled) {
          return;
        }
        setUserLoadError(err instanceof Error ? err.message : String(err));
        setUsers((current) => normalizeUsers(current, identity.userId));
      }
    }

    void loadUserList();
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, identity.deviceId, identity.tenantKey, identity.userId]);

  useEffect(() => {
    window.localStorage.setItem(sidebarWidthStorageKey, String(sidebarWidth));
  }, [sidebarWidth]);

  useEffect(() => {
    if (!settingsOpen) {
      return;
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setSettingsOpen(false);
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [settingsOpen]);

  useEffect(() => {
    if (!sidebarDrag) {
      return;
    }
    const drag = sidebarDrag;

    function handlePointerMove(event: PointerEvent) {
      const nextWidth = drag.startWidth + event.clientX - drag.startX;
      setSidebarWidth(clampSidebarWidth(nextWidth));
    }

    function handlePointerUp() {
      setSidebarDrag(null);
    }

    document.body.classList.add("sidebar-resizing");
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp, { once: true });
    return () => {
      document.body.classList.remove("sidebar-resizing");
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
    };
  }, [sidebarDrag]);

  function switchPrimary(next: PrimarySection) {
    setSettingsOpen(false);
    setShowWelcome(false);
    setPrimarySection(next);
    setSecondarySection(secondarySections[next][0].key);
    if (next === "images") {
      const params = new URLSearchParams(window.location.search);
      params.set("view", "images");
      if (selectedSessionId) params.set("session_id", String(selectedSessionId));
      window.history.replaceState({}, "", `${window.location.pathname}?${params.toString()}`);
    }
  }

  function showWelcomePage() {
    setSettingsOpen(false);
    setShowWelcome(true);
  }

  function openWebAgent() {
    const url = new URL("/webui/agent", window.location.origin);
    const currentToken = new URLSearchParams(window.location.search).get("token") || identity.apiToken.trim();
    if (currentToken) {
      url.searchParams.set("token", currentToken);
    }
    window.location.href = `${url.pathname}${url.search}`;
  }

  function handleSaveIdentity() {
    saveIdentity(identity);
    setStatus(t("app.contextSaved"));
  }

  function updateIdentity(nextIdentity: IdentityConfig, options: { persist?: boolean; resetSession?: boolean } = {}) {
    setIdentity(isDesktopHost() ? normalizeIdentity(nextIdentity) : nextIdentity);
    if (options.persist) {
      saveIdentity(nextIdentity);
    }
    if (options.resetSession) {
      setSelectedSessionId(null);
      handleDataChanged();
    }
  }

  function handleTenantChange(nextTenantKey: string) {
    if (nextTenantKey === identity.tenantKey) {
      return;
    }
    const nextIdentity = { ...identity, tenantKey: nextTenantKey };
    updateIdentity(nextIdentity, { persist: true, resetSession: true });
    setStatus(t("app.tenantChanged", { tenant: nextTenantKey }));
  }

  function handleUserChange(nextUserId: string) {
    if (nextUserId === identity.userId) {
      return;
    }
    const selectedUser = users.find((user) => user.user_key === nextUserId);
    const nextIdentity = selectedUser ? identityWithUser(identity, selectedUser) : { ...identity, userId: nextUserId, role: "member" };
    updateIdentity(nextIdentity, { persist: true, resetSession: true });
    setStatus(t("app.userChanged", { user: nextUserId }));
  }

  function handleResetIdentity() {
    updateIdentity(defaultIdentity, { persist: true, resetSession: true });
    setTenantLoadError("");
    setUserLoadError("");
    setTenants([tenantFallback(defaultIdentity)]);
    setUsers([userFallback(defaultIdentity)]);
    setStatus(t("app.identityReset"));
  }

  function handleClearAPIToken() {
    updateIdentity({ ...identity, apiToken: "" }, { persist: true });
    setStatus(t("app.apiTokenCleared"));
  }

  function handleClearMobileJWT() {
    updateIdentity({ ...identity, mobileJwt: "" }, { persist: true });
    setStatus(t("app.mobileJwtCleared"));
  }

  function handleDataChanged() {
    setRefreshTick((current) => current + 1);
  }

  function openTenantSession(sessionId: number) {
    setSelectedSessionId(sessionId);
    setShowWelcome(false);
    setPrimarySection("chat");
    setSecondarySection("conversation");
  }

  async function handleSeedScenario() {
    try {
      const result = await seedValidationScenario(identity);
      setSelectedSessionId(result.sessionId);
      setStatus(t("app.seededScenario", { id: result.sessionId }));
      handleDataChanged();
    } catch (err) {
      setStatus(err instanceof Error ? err.message : String(err));
    }
  }

  const settingsDialog = settingsOpen ? (
    <>
      <button className="settings-backdrop" onClick={() => setSettingsOpen(false)} type="button" aria-label={t("app.closeSettings")} />
      <section className="settings-drawer" role="dialog" aria-modal="true" aria-labelledby="settings-drawer-title">
        <div className="settings-drawer-header">
          <div>
            <strong id="settings-drawer-title">{t("app.settingsDetail")}</strong>
            <span>{identity.tenantKey} · {identity.userId}</span>
          </div>
          <button className="icon-button compact" onClick={() => setSettingsOpen(false)} type="button" title={t("app.closeSettings")}>
            <X size={15} />
          </button>
        </div>
        <div className="sidebar-footer settings-panel">
          <span>{t("app.tenant")}</span>
          <select
            aria-label={t("app.tenant")}
            className="sidebar-tenant-select"
            onChange={(event) => handleTenantChange(event.target.value)}
            title={tenantLoadError || undefined}
            value={identity.tenantKey}
          >
            {tenants.map((tenant) => {
              const tenantKey = tenant.tenant_key || "";
              return (
                <option key={tenantKey} value={tenantKey}>
                  {tenantLabel(tenant)}
                </option>
              );
            })}
          </select>
          <span>{t("app.user")}</span>
          <select
            aria-label={t("app.user")}
            className="sidebar-identity-select"
            onChange={(event) => handleUserChange(event.target.value)}
            title={userLoadError || undefined}
            value={identity.userId}
          >
            {users.map((user) => {
              const userKey = user.user_key || "";
              return (
                <option key={userKey} value={userKey}>
                  {userLabel(user)}
                </option>
              );
            })}
          </select>
          {tenantLoadError || userLoadError ? (
            <div className="sidebar-error-box">
              {tenantLoadError ? (
                <small>
                  <strong>{t("app.tenantLoadFailed")}</strong>
                  {tenantLoadError}
                </small>
              ) : null}
              {userLoadError ? (
                <small>
                  <strong>{t("app.userLoadFailed")}</strong>
                  {userLoadError}
                </small>
              ) : null}
              <button className="sidebar-reset-button" onClick={handleResetIdentity} type="button">
                {t("app.resetIdentity")}
              </button>
            </div>
          ) : null}
        </div>
        <div className="settings-section">
          <span>{t("app.auth")}</span>
          {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .auth-status-grid is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
          <div className="auth-status-grid" role="group" aria-label={t("app.auth")}>
            <div>
              <KeyRound size={14} />
              <span>{t("app.apiToken")}</span>
              <strong className={identity.apiToken.trim() ? "auth-ok" : "auth-missing"}>
                {identity.apiToken.trim() ? t("app.present") : t("app.missing")}
              </strong>
            </div>
            <div>
              <KeyRound size={14} />
              <span>{t("app.mobileJwt")}</span>
              <strong className={identity.mobileJwt.trim() ? "auth-ok" : "auth-missing"}>
                {identity.mobileJwt.trim() ? t("app.present") : t("app.missing")}
              </strong>
            </div>
          </div>
          <div className="settings-action-row">
            <button className="secondary-button compact-action" onClick={handleClearAPIToken} type="button">
              {t("app.clearAPIToken")}
            </button>
            <button className="secondary-button compact-action" onClick={handleClearMobileJWT} type="button">
              {t("app.clearMobileJwt")}
            </button>
          </div>
        </div>
        <div className="settings-section">
          <span>{t("app.language")}</span>
          <div className="language-switch sidebar-language" role="radiogroup" aria-label={t("app.language")}>
            <button type="button" className={language === "en" ? "active" : ""} onClick={() => setLanguage("en")}>
              {t("app.english")}
            </button>
            <button type="button" className={language === "zh" ? "active" : ""} onClick={() => setLanguage("zh")}>
              {t("app.chinese")}
            </button>
          </div>
        </div>
      </section>
    </>
  ) : null;

  const desktopV2 = import.meta.env.VITE_DESKTOP_UI_VERSION === "2";
  if (
    desktopV2 ||
    (typeof window !== "undefined" && (window.location.pathname === "/webui/v2" || window.location.pathname.startsWith("/webui/v2/")))
  ) {
    return (
      <QueryClientProvider client={queryClient}>
        <WebUIV2App identity={identity} />
      </QueryClientProvider>
    );
  }

  if (typeof window !== "undefined" && window.location.pathname.startsWith("/webui/agent")) {
    return (
      <QueryClientProvider client={queryClient}>
        <WebAgentPage identity={identity} onIdentityChange={setIdentity} onStatus={setStatus} />
      </QueryClientProvider>
    );
  }

  return (
    <QueryClientProvider client={queryClient}>
      <div className="dashboard-shell" style={{ gridTemplateColumns: `${sidebarWidth}px minmax(0, 1fr)` }}>
        <aside className="sidebar">
          <button className={showWelcome ? "brand-lockup active" : "brand-lockup"} onClick={showWelcomePage} type="button">
            <img alt="" className="brand-mark" src={niumaMark} />
            <div>
              <strong>{t("webui2.brand")}</strong>
              <span>{t("app.workbench")}</span>
            </div>
          </button>
          <div className="sidebar-main">
            <nav className="primary-nav" aria-label={t("app.primaryNav")}>
              {primarySections.map((item) => {
                const active = !showWelcome && primarySection === item.key;
                const sidebarGroup = active && item.key === "teams";
                return <div className={sidebarGroup ? "primary-nav-group expanded" : "primary-nav-group"} key={item.key}>
                  <button
                    type="button"
                    className={active ? "primary-nav-item active" : "primary-nav-item"}
                    onClick={() => switchPrimary(item.key)}
                    aria-expanded={item.key === "teams" ? sidebarGroup : undefined}
                  >
                    {item.icon}
                    <span>
                      <strong>{t(item.labelKey)}</strong>
                      <small>{t(item.detailKey)}</small>
                    </span>
                  </button>
                  {sidebarGroup ? <fieldset className="sidebar-subnav">
                    <legend className="sr-only">{`${t(item.labelKey)} ${t("app.secondaryNav")}`}</legend>
                    {secondarySections[item.key].map((child) => <button key={child.key} type="button" className={secondarySection === child.key ? "sidebar-subnav-item active" : "sidebar-subnav-item"} onClick={() => setSecondarySection(child.key)}>
                      {child.icon}
                      <span><strong>{t(child.labelKey)}</strong><small>{t(child.detailKey)}</small></span>
                    </button>)}
                  </fieldset> : null}
                </div>;
              })}
              <button className="primary-nav-item web-agent-entry" onClick={openWebAgent} type="button">
                <MonitorCog size={18} />
                <span>
                  <strong>{t("nav.webAgent")}</strong>
                  <small>{t("nav.webAgent.detail")}</small>
                </span>
              </button>
            </nav>
          </div>
          <div className="sidebar-bottom">
            <button
              aria-expanded={settingsOpen}
              className={settingsOpen ? "settings-launcher active" : "settings-launcher"}
              onClick={() => setSettingsOpen((open) => !open)}
              type="button"
            >
              <Settings size={18} />
              <span>
                <strong>{t("app.settings")}</strong>
                <small>{identity.tenantKey} · {language === "zh" ? t("app.chinese") : t("app.english")}</small>
              </span>
            </button>
          </div>
          {/* biome-ignore lint/a11y/useSemanticElements: this is a draggable interactive resizer (drag + arrow-key resize), not a thematic break — <hr> cannot carry pointer/keyboard handlers or aria-value* state */}
          <button
            type="button"
            className="sidebar-resizer"
            role="separator"
            aria-label={t("app.resizeSidebar")}
            aria-orientation="vertical"
            aria-valuemin={sidebarMinWidth}
            aria-valuemax={sidebarMaxWidth}
            aria-valuenow={sidebarWidth}
            onPointerDown={(event) => {
              event.currentTarget.setPointerCapture(event.pointerId);
              setSidebarDrag({ startX: event.clientX, startWidth: sidebarWidth });
            }}
            onKeyDown={(event) => {
              if (event.key === "ArrowLeft") {
                event.preventDefault();
                setSidebarWidth((current) => clampSidebarWidth(current - 16));
              }
              if (event.key === "ArrowRight") {
                event.preventDefault();
                setSidebarWidth((current) => clampSidebarWidth(current + 16));
              }
              if (event.key === "Home") {
                event.preventDefault();
                setSidebarWidth(sidebarMinWidth);
              }
              if (event.key === "End") {
                event.preventDefault();
                setSidebarWidth(sidebarMaxWidth);
              }
            }}
          />
        </aside>

        <main className={!showWelcome && primarySection === "teams" ? "app-shell team-app-shell" : "app-shell"}>
          {showWelcome ? (
            <section className="welcome-page">
              <div className="welcome-shell">
                <div className="welcome-status-card">
                  <span>{t("app.status")}</span>
                  <strong>{status}</strong>
                </div>
                <img alt="" className="welcome-mark" src={niumaMark} />
                <span className="page-kicker">{t("app.pageKicker")}</span>
                <h1>{t("app.title")}</h1>
                <p>{t("app.subtitle")}</p>
                <section className="runtime-strip welcome-runtime" aria-label={t("app.activeRunContext")}>
                  <div>
                    <span>{t("app.api")}</span>
                    <strong title={identity.apiBase}>{identity.apiBase}</strong>
                  </div>
                  <div>
                    <span>{t("app.tenant")}</span>
                    <strong title={identity.tenantKey}>{identity.tenantKey}</strong>
                  </div>
                  <div>
                    <span>{t("app.user")}</span>
                    <strong title={identity.userId}>{identity.userId}</strong>
                  </div>
                  <div>
                    <span>{t("app.model")}</span>
                    <strong title={identity.model}>{identity.model}</strong>
                  </div>
                </section>
              </div>
            </section>
          ) : (
            <>
              {primarySection !== "teams" ? <section className="workspace-toolbar">
                <nav className="secondary-tabs" aria-label={t("app.secondaryNav")}>
                  {activeSecondarySections.map((item) => (
                    <button
                      key={item.key}
                      type="button"
                      className={secondarySection === item.key ? "secondary-tab active" : "secondary-tab"}
                      onClick={() => setSecondarySection(item.key)}
                    >
                      {item.icon}
                      <span>
                        <strong>{t(item.labelKey)}</strong>
                        <small>{t(item.detailKey)}</small>
                      </span>
                    </button>
                  ))}
                </nav>
              </section> : null}

              {secondarySection === "run-context" ? (
                <ContextPanel
                  identity={identity}
                  onChange={setIdentity}
                  onCommitIdentity={(nextIdentity) => updateIdentity(nextIdentity, { persist: true, resetSession: true })}
                  onSave={handleSaveIdentity}
                  onSeedScenario={handleSeedScenario}
                  onDataChanged={handleDataChanged}
                  onStatus={setStatus}
                />
              ) : null}

              {secondarySection === "global-config" ? <SettingsPanel identity={identity} onStatus={setStatus} /> : null}

              {secondarySection === "images" ? <ImageGenerationWorkbench identity={identity} initialSessionId={selectedSessionId} initialPrompt={imageView.prompt} onSessionChange={(sessionID) => {
                setSelectedSessionId(sessionID || null);
                const params = new URLSearchParams(window.location.search);
                params.set("view", "images");
                if (sessionID) params.set("session_id", String(sessionID)); else params.delete("session_id");
                window.history.replaceState({}, "", `${window.location.pathname}?${params.toString()}`);
              }} /> : null}

              {secondarySection === "teams" || secondarySection === "team-operations" ? <AgentTeamsPanel identity={identity} onStatus={setStatus} onDataChanged={handleDataChanged} workspace={secondarySection === "team-operations" ? "operations" : "workspace"} onWorkspaceChange={(workspace) => setSecondarySection(workspace === "operations" ? "team-operations" : "teams")} /> : null}

              {secondarySection === "conversation" ? (
                <ChatLab
                  identity={identity}
                  selectedSessionId={selectedSessionId}
                  onSelectSession={setSelectedSessionId}
                  onStatus={setStatus}
                  onDataChanged={handleDataChanged}
                />
              ) : null}

              {secondarySection === "goals" ? (
                <GoalWorkbench
                  identity={identity}
                  selectedSessionId={selectedSessionId}
                  onStatus={setStatus}
                  onDataChanged={handleDataChanged}
                />
              ) : null}

              {secondarySection !== "conversation" && secondarySection !== "images" && secondarySection !== "run-context" && secondarySection !== "global-config" && secondarySection !== "teams" && secondarySection !== "team-operations" ? (
                secondarySection === "goals" ? null : (
                  <InspectorPanels
                    identity={identity}
                    selectedSessionId={selectedSessionId}
                    activeSection={secondarySection}
                    refreshTick={refreshTick}
                    onSelectSession={setSelectedSessionId}
                    onStatus={setStatus}
                  />
                )
              ) : null}
            </>
          )}
        </main>
        {settingsDialog}
      </div>
    </QueryClientProvider>
  );
}
