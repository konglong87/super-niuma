import { WorkflowWorkbench } from "./workflows/WorkflowWorkbench";
import { useWorkflowLeaveGuard } from "./workflows/useWorkflowLeaveGuard";
import type { WorkflowClient } from "./workflows/client";
import type { WorkflowAgentClient } from "./workflows/agentClient";
import { AlertTriangle, LoaderCircle, MessageSquare, PanelLeftOpen, Plus, RefreshCw, Shield } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type JSX } from "react";
import { useSessionConversations } from "./api/useSessionConversations";
import type { StreamState } from "../hooks/useAgentTaskStream";
import { sessionRetryInput } from "./api/sessionRetry";
import { PendingQueueSettingsButton, SessionPendingQueue } from "./components/SessionPendingQueue";
import { useQueryClient } from "@tanstack/react-query";
import { CommandPalette, type Command } from "../components/AgentCommandPalette";
import { sessionRuntimeConfig, useRuntimeCatalog, useRuntimeDefaults, useSessionRuntimeDetails } from "./api/useSessionRuntime";
import { collectConversationNextSteps, conversationRuntimeMetrics } from "./components/conversationViewModel";
import type { ComposerRuntimeValue } from "./components/composerRuntimeControls";
import { useI18n } from "../lib/i18n";
import { isDesktopV2Host } from "../lib/config";
import { validateAgentWorkspace } from "../lib/api";
import type { IdentityConfig, PendingInputSideChatResponse } from "../lib/types";
import { createHTTPSessionControlClient } from "./api/httpSessionControlClient";
import { SessionControlClientProvider, sessionControlErrorCode, useSessionControlClient, type SessionControlClient } from "./api/sessionControlClient";
import { useArchiveSession, useCompactSession, useCreateSession, useRenameSession, useSendSession, useSessionDetail, useSessionList, useStopSession } from "./api/sessionControlQueries";
import { Composer, type ComposerDraft } from "./components/Composer";
import { HOME_EXPERIENCE } from "./components/homeExperience";
import { SimpleHomePage } from "./components/SimpleHomePage";
import { SessionSidebar } from "./components/SessionSidebar";
import { SessionSearchDialog } from "./components/SessionSearchDialog";
import { TaskFirstHomePage } from "./components/TaskFirstHomePage";
import { ConversationWorkspace } from "./components/ConversationWorkspace";
import { Inspector, type InspectorTab } from "./components/Inspector";
import { NewSessionDialog } from "./components/NewSessionDialog";
import { loadWebUIV2Theme, saveWebUIV2Theme, type WebUIV2Theme } from "./components/SettingsDrawer";
import { SettingsCenter } from "./settings/SettingsCenter";
import { GLOBAL_SETTINGS_SAVED_EVENT } from "./settings/globalSettingsDraft";
import { loadInspectorPreference, saveInspectorPreference } from "./settings/preferences";
import { parseWebUIV2Route, settingsReturnSession, webUIV2SettingsPath, webUIV2SessionPath, webUIV2WorkflowPath, type SettingsSection, type SessionRef, type WebUIV2Route } from "./routes";
import type { OperationResult, SessionListFilters, SessionMessage, SessionStatus, SessionSummary } from "./types";
import { DesktopPet } from "./components/DesktopPet";
import { getDesktopServiceBridge } from "./desktopServiceBridge";
import { createComputerClient, getComputerBridge } from "./computer/client";
import { ComputerWorkspace } from "./computer/ComputerWorkspace";
import { useDesktopReadiness } from "./useDesktopReadiness";
import { UnsavedChangesDialog } from "./components/UnsavedChangesDialog";
import { GLOBAL_SETTINGS_QUERY_KEY, useGlobalVisualSettings } from "./settings/useGlobalVisualSettings";
import { visualSettingsStyle, type GlobalVisualSettings } from "./settings/globalVisualSettings";
import "./styles.css";
import "./components/thinkingMessage.css";

type RouteState = {
  route: WebUIV2Route;
  selectedRef: SessionRef | null;
};

type LeaveDialogKind = "dirty" | "busy";

const ACTIVE_RUNTIME_STATUSES = new Set<SessionStatus>(["running", "queued", "waiting_permission", "waiting_input"]);

function routeState(pathname: string): RouteState {
  const route = parseWebUIV2Route(pathname, import.meta.env.VITE_DESKTOP_UI_VERSION === "2");
  return { route, selectedRef: route.kind === "session" ? route.ref : route.kind === "settings" ? settingsReturnSession(window.location.search) : null };
}

export function WebUIV2App({ identity, client: providedClient, workflowClient, workflowAgentClient }: { identity: IdentityConfig; client?: SessionControlClient; workflowClient?: WorkflowClient; workflowAgentClient?: WorkflowAgentClient }): JSX.Element {
  const [httpClient] = useState(createHTTPSessionControlClient);
  return <SessionControlClientProvider client={providedClient ?? httpClient}><WebUIV2RouteShell key={JSON.stringify([identity.apiBase, identity.tenantKey, identity.userId])} identity={identity} workflowClient={workflowClient} workflowAgentClient={workflowAgentClient} /></SessionControlClientProvider>;
}

function WebUIV2RouteShell({ identity, workflowClient, workflowAgentClient }: { identity: IdentityConfig; workflowClient?: WorkflowClient; workflowAgentClient?: WorkflowAgentClient }): JSX.Element {
  const { t } = useI18n();
  const { language } = useI18n();
  const queryClient = useQueryClient();
  const client = useSessionControlClient();
  const drafts = useRef(new Map<SessionRef, ComposerDraft>());
  const [state, setState] = useState<RouteState>(() => routeState(window.location.pathname));
  const [filters, setFilters] = useState<SessionListFilters>({ query: "", statuses: [] });
  const settingsOpen = state.route.kind === "settings";
  const workflowsOpen = state.route.kind === "workflows";
  const workflowLeave = useWorkflowLeaveGuard(workflowsOpen);
  const settingsDirty = useRef(false);
  const settingsBusy = useRef(false);
  const pendingNavigation = useRef<(() => void) | null>(null);
  const [leaveDialog, setLeaveDialog] = useState<LeaveDialogKind | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [sidebarHidden, setSidebarHidden] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [newSessionOpen, setNewSessionOpen] = useState(false);
  const [commandsOpen, setCommandsOpen] = useState(false);
  const [inspectorOpen, setInspectorOpen] = useState(loadInspectorPreference);
  const [inspectorTab, setInspectorTab] = useState<InspectorTab>("activity");
  const [theme, setTheme] = useState<WebUIV2Theme>(() => loadWebUIV2Theme());
  const [visualPreview, setVisualPreview] = useState<GlobalVisualSettings | null>(null);
  const [errorCode, setErrorCode] = useState("");
  const isDesktop = isDesktopV2Host();
  const [, refreshDesktopBridge] = useState(0);
  useEffect(() => {
    if (!isDesktop) return;
    let attempts = 0;
    const probe = (): void => {
      attempts += 1;
      if (getDesktopServiceBridge() || attempts >= 40) {
        refreshDesktopBridge((value) => value + 1);
        if (attempts >= 40) window.clearInterval(timer);
      }
    };
    const timer = window.setInterval(probe, 250);
    probe();
    return () => window.clearInterval(timer);
  }, [isDesktop]);
  const refreshDesktopData = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ["webui2-server-status"] });
    void queryClient.invalidateQueries({ queryKey: ["webui2-providers"] });
    void queryClient.invalidateQueries({ queryKey: ["webui2-models"] });
    void queryClient.invalidateQueries({ queryKey: ["session-control"] });
    void queryClient.invalidateQueries({ queryKey: [GLOBAL_SETTINGS_QUERY_KEY] });
  }, [queryClient]);
  const desktop = useDesktopReadiness({ enabled: isDesktop, apiBase: identity.apiBase, apiToken: identity.apiToken, onReady: refreshDesktopData });
  const desktopReady = desktop.ready;
  const desktopServiceBridge = isDesktop ? getDesktopServiceBridge() : null;
  const computerClient = useMemo(() => {
    const bridge = isDesktop ? getComputerBridge() : null;
    return bridge ? createComputerClient(bridge) : null;
  }, [isDesktop, desktopServiceBridge]);
  const refSearch = completeSessionRef(filters.query);
  const sessionList = useSessionList(identity, refSearch ? { ...filters, query: "" } : filters, desktopReady);
  const create = useCreateSession(identity);
  const stop = useStopSession(identity);
  const archive = useArchiveSession(identity);
  const rename = useRenameSession(identity);
  const sessions = sessionList.data ?? [];
  const allSessions = useSessionList(identity, { query: "", statuses: [] }, desktopReady);
  const selectedSession = sessions.find((session) => session.ref === state.selectedRef) ?? (allSessions.data ?? []).find((session) => session.ref === state.selectedRef);
  const savedVisual = useGlobalVisualSettings(identity, desktopReady);
  const visual = visualPreview ?? savedVisual.visual;
  const stream = useSessionConversations(identity, allSessions.data ?? [], state.selectedRef, desktopReady);
  const runtimeDefaults = useRuntimeDefaults(identity, isDesktop && desktopReady);
  const defaultWorkspace = runtimeDefaults.data?.workspace || runtimeDefaults.data?.cwd || "";
  const needsModelSetup = isDesktop && desktopReady && runtimeDefaults.data?.runtime_defaults?.needs_setup === true;
  const showTaskFirstHome = isDesktop && desktopReady && HOME_EXPERIENCE === "task-first";
  const directCreateBusyRef = useRef(false);

  useEffect(() => {
    const refreshRuntimeCatalog = (): void => {
      void queryClient.invalidateQueries({ queryKey: ["webui2-server-status"] });
      void queryClient.invalidateQueries({ queryKey: ["webui2-providers"] });
      void queryClient.invalidateQueries({ queryKey: ["webui2-models"] });
    };
    window.addEventListener(GLOBAL_SETTINGS_SAVED_EVENT, refreshRuntimeCatalog);
    return () => window.removeEventListener(GLOBAL_SETTINGS_SAVED_EVENT, refreshRuntimeCatalog);
  }, [queryClient]);

  const navigateToSession = useCallback((ref: SessionRef): void => {
    window.history.pushState({}, "", webUIV2SessionPath(ref));
    setState({ route: { kind: "session", ref }, selectedRef: ref });
    setErrorCode("");
    setSidebarOpen(false);
    setSearchOpen(false);
  }, []);

  const requestSettingsNavigation = useCallback((action: () => void): void => {
    if (!settingsOpen) {
      workflowLeave.request(action);
      return;
    }
    if (settingsBusy.current) {
      setLeaveDialog("busy");
      return;
    }
    if (settingsDirty.current) {
      pendingNavigation.current = action;
      setLeaveDialog("dirty");
      return;
    }
    action();
  }, [settingsOpen, workflowLeave.request]);

  const selectSession = useCallback((ref: SessionRef): void => {
    requestSettingsNavigation(() => navigateToSession(ref));
  }, [navigateToSession, requestSettingsNavigation]);

  function openSettings(section: SettingsSection = "general"): void {
    workflowLeave.request(() => {
    window.history.pushState({}, "", webUIV2SettingsPath(section, state.selectedRef));
    setState((current) => ({ ...current, route: { kind: "settings", section } }));
    setSidebarOpen(false);
    });
  }

  function navigateWorkflow(path: string): void {
    if (path === window.location.pathname) return;
    requestSettingsNavigation(() => {
      workflowLeave.setDirty(false);
      window.history.pushState({}, "", path);
      setState((current) => ({ ...current, route: parseWebUIV2Route(window.location.pathname) }));
      setSidebarOpen(false); setSearchOpen(false);
    });
  }

  const closeSettings = useCallback((): void => {
    const ref = state.selectedRef;
    requestSettingsNavigation(() => {
      settingsDirty.current = false;
      window.history.pushState({}, "", ref ? webUIV2SessionPath(ref) : "/webui/v2");
      setState({ route: ref ? { kind: "session", ref } : { kind: "index" }, selectedRef: ref });
    });
  }, [requestSettingsNavigation, state.selectedRef]);

  function cancelPendingNavigation(): void {
    pendingNavigation.current = null;
    setLeaveDialog(null);
  }

  function discardPendingNavigation(): void {
    const action = pendingNavigation.current;
    pendingNavigation.current = null;
    settingsDirty.current = false;
    setLeaveDialog(null);
    action?.();
  }

  function changeTheme(next: WebUIV2Theme): void {
    saveWebUIV2Theme(next);
    setTheme(next);
  }

  function changeInspector(open: boolean): void {
    saveInspectorPreference(open);
    setInspectorOpen(open);
  }

  function openNewSession(): void {
    if (isDesktop && !desktopReady) return;
    setSidebarOpen(false);
    setNewSessionOpen(true);
  }

  async function createSessionInWorkspace(cwd: string): Promise<void> {
    if ((isDesktop && !desktopReady) || directCreateBusyRef.current) return;
    const selectedCWD = cwd.trim();
    if (!selectedCWD) {
      setErrorCode("workspace_unavailable");
      return;
    }
    if (runtimeDefaults.data?.runtime_defaults?.needs_setup) {
      setErrorCode("runtime_not_configured");
      return;
    }
    directCreateBusyRef.current = true;
    setErrorCode("");
    setSidebarOpen(false);
    try {
      const validation = await validateAgentWorkspace(identity, selectedCWD);
      if (!validation.exists || !validation.is_dir) {
        setErrorCode("workspace_unavailable");
        return;
      }
      const result = await create.mutateAsync({
        title: t("webui2.defaultSessionTitle"),
        cwd: validation.cwd,
        promptMode: "code",
        idempotencyKey: crypto.randomUUID()
      });
      handleCreated(result);
    } catch (error) {
      setErrorCode(sessionControlErrorCode(error));
    } finally {
      directCreateBusyRef.current = false;
    }
  }

  function openSessionSearch(): void {
    setSidebarOpen(false);
    setSearchOpen(true);
  }

  function hideSidebar(): void {
    setSidebarOpen(false);
    setSidebarHidden(true);
  }

  function showSidebar(): void {
    setSidebarHidden(false);
  }

  function handleCreated(result: OperationResult): void {
    setNewSessionOpen(false);
    selectSession(result.session.ref);
    window.requestAnimationFrame(() => document.querySelector<HTMLTextAreaElement>(".webui2-composer textarea")?.focus());
  }

  useEffect(() => {
    function restoreRoute() {
      const next = routeState(window.location.pathname);
      if (state.route.kind === "workflows") {
        const targetURL = window.location.pathname + window.location.search;
        window.history.pushState({}, "", webUIV2WorkflowPath(state.route));
        workflowLeave.request(() => {
          workflowLeave.setDirty(false);
          window.history.replaceState({}, "", targetURL);
          setState({ ...next, selectedRef: next.selectedRef ?? (next.route.kind === "workflows" ? state.selectedRef : null) });
          setErrorCode("");
        });
        return;
      }
      if (state.route.kind === "settings" && next.route.kind !== "settings" && (settingsDirty.current || settingsBusy.current)) {
        window.history.pushState({}, "", webUIV2SettingsPath(state.route.section, state.selectedRef));
        requestSettingsNavigation(() => {
          if (next.route.kind !== "settings") settingsDirty.current = false;
          setState(next);
          setErrorCode("");
        });
        return;
      }
      if (next.route.kind !== "settings") settingsDirty.current = false;
      setState(next);
      setErrorCode("");
    }

    window.addEventListener("popstate", restoreRoute);
    return () => window.removeEventListener("popstate", restoreRoute);
  }, [requestSettingsNavigation, state, workflowLeave.request, workflowLeave.setDirty]);

  useEffect(() => {
    if (!refSearch || settingsOpen || workflowsOpen || state.selectedRef === refSearch) return;
    let current = true;
    void client.get(identity, refSearch).then(() => {
      if (current) selectSession(refSearch);
    }).catch((error: unknown) => {
      if (current) setErrorCode(sessionControlErrorCode(error));
    });
    return () => { current = false; };
  }, [client, identity, refSearch, selectSession, settingsOpen, workflowsOpen, state.selectedRef]);

  function recoverToIndex(): void {
    window.history.replaceState({}, "", "/webui/v2");
    setState({ route: { kind: "index" }, selectedRef: null });
    setErrorCode("");
  }

  function handleStop(ref: SessionRef): void {
    stop.mutate({ ref }, { onError: (error) => setErrorCode(sessionControlErrorCode(error)) });
  }

  function handleArchive(ref: SessionRef): void {
    archive.mutate({ ref }, { onError: (error) => setErrorCode(sessionControlErrorCode(error)) });
  }

  async function handleRename(session: SessionSummary, title: string): Promise<boolean> {
    if (session.source !== "tenant" || session.id === undefined) {
      setErrorCode("invalid_state");
      return false;
    }
    try {
      await rename.mutateAsync({ ref: session.ref, id: session.id, title });
      setErrorCode("");
      return true;
    } catch (error) {
      setErrorCode(sessionControlErrorCode(error));
      return false;
    }
  }

  useEffect(() => {
    function handleCommandKey(event: KeyboardEvent) {
      if (settingsOpen) return;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") { event.preventDefault(); setCommandsOpen((open) => !open); }
    }
    window.addEventListener("keydown", handleCommandKey);
    return () => window.removeEventListener("keydown", handleCommandKey);
  }, [settingsOpen]);

  const commands: Command[] = [
    { id: "new", label: t("webui2.newSession"), section: language === "zh" ? "操作" : "Actions", icon: <Plus size={16} />, run: openNewSession },
    { id: "refresh", label: language === "zh" ? "刷新会话" : "Refresh sessions", section: language === "zh" ? "操作" : "Actions", icon: <RefreshCw size={16} />, run: () => { void queryClient.invalidateQueries({ queryKey: ["session-control"] }); } },
    { id: "permissions", label: language === "zh" ? "权限记录" : "Permissions", section: language === "zh" ? "操作" : "Actions", icon: <Shield size={16} />, run: () => { setInspectorOpen(true); setInspectorTab("activity"); } },
    ...(allSessions.data ?? sessions).map((session) => ({ id: session.ref, label: session.title, section: session.cwd || t("webui2.sessions"), keywords: session.ref, icon: <MessageSquare size={16} />, run: () => selectSession(session.ref) }))
  ];

  const searchSessions = allSessions.data ?? sessions;

  return <main aria-label={t("webui2.workspace")} className="webui2-page" data-appearance-enabled={visual.appearance.enabled ? "true" : "false"} data-inspector-open={!workflowsOpen && inspectorOpen} data-session-ref={state.selectedRef ?? undefined} data-sidebar-hidden={sidebarHidden} data-sidebar-open={sidebarOpen} data-theme={theme} style={visualSettingsStyle(visual)}>
    {isDesktop && !desktopReady ? <section className="webui2-desktop-readiness" data-state={desktop.state} aria-busy={desktop.state !== "failed"} role="status">
      <div className="webui2-desktop-status-copy">
        {desktop.state === "failed" ? <AlertTriangle aria-hidden="true" size={18} /> : <LoaderCircle className="webui2-desktop-status-spinner" aria-hidden="true" size={18} />}
        <div>
          <strong>{desktop.state === "failed" ? (language === "zh" ? "暂时无法准备工作区" : "Workspace preparation is unavailable") : desktop.state === "recovering" ? (language === "zh" ? "正在恢复工作区…" : "Restoring your workspace…") : (language === "zh" ? "正在准备工作区…" : "Preparing your workspace…")}</strong>
          <span>{desktop.state === "failed" ? (language === "zh" ? "可以重新准备，恢复后会自动继续。" : "Try preparing again. The app will continue automatically when ready.") : (language === "zh" ? "准备完成后将自动进入，无需操作。" : "The app will continue automatically when ready.")}</span>
        </div>
      </div>
      {desktop.state === "failed" ? <div className="webui2-desktop-status-actions">
        <button type="button" className="webui2-desktop-status-button" disabled={desktop.busy} onClick={desktop.retry}><RefreshCw aria-hidden="true" size={15} />{language === "zh" ? "重新准备" : "Prepare again"}</button>
      </div> : null}
    </section> : null}
    <div className="webui2-chat-shell" hidden={settingsOpen} inert={settingsOpen}>
    <SessionSidebar
      filters={filters}
      identity={identity}
      onContextDragStart={() => undefined}
      onFiltersChange={setFilters}
      onMobileClose={() => setSidebarOpen(false)}
      onOpenSearch={openSessionSearch}
      onHideSidebar={hideSidebar}
      onCreateSession={openNewSession}
      createDisabled={!desktopReady}
      onCreateSessionInWorkspace={(cwd) => { void createSessionInWorkspace(cwd); }}
      onOpenSettings={() => openSettings()}
      onOpenWorkflows={() => navigateWorkflow(webUIV2WorkflowPath())}
      workflowsActive={workflowsOpen}
      onArchive={handleArchive}
      onRenameSession={client.rename ? handleRename : undefined}
      onStop={handleStop}
      onSelect={selectSession}
      selectedRef={state.selectedRef}
      sessions={sessions}
    />
    {sidebarHidden ? <button aria-label={t("webui2.showSidebar")} className="webui2-sidebar-restore" onClick={showSidebar} title={t("webui2.showSidebar")} type="button"><PanelLeftOpen aria-hidden="true" size={18} /></button> : null}
    {sidebarOpen ? <button aria-label={t("webui2.closeSessions")} className="webui2-mobile-sidebar-backdrop" onClick={() => setSidebarOpen(false)} tabIndex={-1} type="button" /> : null}
    <button aria-controls="webui2-session-sidebar" aria-expanded={sidebarOpen} aria-label={t("webui2.openSessions")} className="webui2-mobile-sidebar-open" onClick={() => setSidebarOpen(true)} title={t("webui2.openSessions")} type="button"><PanelLeftOpen aria-hidden="true" size={18} /></button>
    <div className="webui2-content">
      {isDesktop && !workflowsOpen ? <ComputerWorkspace client={computerClient} selectedConversationRef={state.selectedRef} nativeOverlay={false} /> : null}
      {workflowsOpen && desktopReady && state.route.kind === "workflows" ? <WorkflowWorkbench identity={identity} route={state.route} client={workflowClient} agentClient={workflowAgentClient} onNavigate={navigateWorkflow} onDirtyChange={workflowLeave.setDirty} onRequestNavigation={workflowLeave.request} /> : null}
      {!workflowsOpen ? <section className="webui2-workspace">
        {state.route.kind === "invalid" ? <div className="webui2-route-error" role="alert"><p>{t("webui2.invalidRoute")}</p><button onClick={recoverToIndex} type="button">{t("webui2.backToSessions")}</button></div> : null}
        {state.route.kind === "index" && !sessionList.isLoading && !sessionList.isError ? !showTaskFirstHome
          ? <SimpleHomePage onCreateSession={openNewSession} createDisabled={!desktopReady} />
          : <TaskFirstHomePage
            availableSources={allSessions.data ?? sessions}
            defaultWorkspace={defaultWorkspace}
            identity={identity}
            onCreated={handleCreated}
            onSelectWorkspace={desktopServiceBridge?.SelectWorkspace}
            ready={desktopReady}
          /> : null}
        {!workflowsOpen && state.selectedRef ? <SelectedSessionWorkspace key={state.selectedRef} drafts={drafts.current} availableSources={allSessions.data ?? sessions} identity={identity} onSelectSession={selectSession} onCreateSession={openNewSession} onOpenInspector={() => setInspectorOpen(true)} selectedRef={state.selectedRef} streamState={stream.state} ready={desktopReady} /> : null}
        {sessionList.isLoading ? <p className="webui2-workspace-empty">{t("webui2.loading")}</p> : null}
        {sessionList.isError ? <p className="webui2-workspace-empty" role="alert">{t(`webui2.error.${sessionControlErrorCode(sessionList.error)}`)}</p> : null}
        {errorCode ? <p role="alert">{t(`webui2.error.${errorCode}`)}</p> : null}
        {stream.error ? <p role="alert">{t(`webui2.error.${stream.error}`)}</p> : null}
      </section> : null}
      {!workflowsOpen && state.selectedRef && desktopReady ? <SelectedSessionInspector identity={identity} onClose={() => setInspectorOpen(false)} onTabChange={setInspectorTab} open={inspectorOpen} selectedRef={state.selectedRef} tab={inspectorTab} /> : null}
    </div>
    <NewSessionDialog identity={identity} onSelectWorkspace={desktopServiceBridge?.SelectWorkspace} onClose={() => setNewSessionOpen(false)} onCreated={handleCreated} open={newSessionOpen} />
    <SessionSearchDialog open={searchOpen} sessions={searchSessions} onClose={() => setSearchOpen(false)} onCreateSession={openNewSession} onSelect={selectSession} />
    <CommandPalette open={commandsOpen} onClose={() => setCommandsOpen(false)} commands={commands} placeholder={language === "zh" ? "搜索会话或操作" : "Search sessions or actions"} emptyLabel={t("webui2.emptySessions")} ariaLabel={language === "zh" ? "命令面板" : "Command palette"} />
    </div>
    {workflowLeave.dialog}
    {state.route.kind === "settings" && desktopReady ? <SettingsCenter identity={identity} section={state.route.section} onSectionChange={openSettings} onBack={closeSettings} onRequestNavigation={requestSettingsNavigation} onDirtyChange={(dirty) => { settingsDirty.current = dirty; }} onBusyChange={(busy) => { settingsBusy.current = busy; }} theme={theme} onThemeChange={changeTheme} inspectorOpen={inspectorOpen} onInspectorChange={changeInspector} selectedRef={state.selectedRef} onOpenSession={selectSession} onVisualPreview={setVisualPreview} /> : null}
    {leaveDialog ? <UnsavedChangesDialog kind={leaveDialog} language={language} onCancel={cancelPendingNavigation} onDiscard={discardPendingNavigation} /> : null}
    {state.route.kind !== "settings" ? <DesktopPet settings={savedVisual.visual.pet} status={selectedSession?.status} onOpenSettings={() => openSettings("pet")} /> : null}
    {needsModelSetup && state.route.kind !== "settings" ? <section className="webui2-onboarding-backdrop" role="dialog" aria-modal="true" aria-labelledby="webui2-onboarding-title">
      <div className="webui2-onboarding">
        <p className="webui2-onboarding-kicker">{language === "zh" ? "开始使用" : "Get started"}</p>
        <h1 id="webui2-onboarding-title">{language === "zh" ? "先配置你的模型" : "Configure your model"}</h1>
        <p>{language === "zh" ? "Default 工作区已经准备好，配置模型后即可开始工作。" : "Your Default workspace is ready. Configure a model to start working."}</p>
        <div className="webui2-onboarding-steps">
          <span><strong>1</strong>{language === "zh" ? "模型配置" : "Model setup"}</span>
          <span><strong>2</strong>{language === "zh" ? "Default 工作区" : "Default workspace"}</span>
          <span><strong>3</strong>{language === "zh" ? "开始会话" : "Start a session"}</span>
        </div>
        <div className="webui2-onboarding-actions">
          <button type="button" onClick={() => openSettings("models")}>{language === "zh" ? "配置模型" : "Configure model"}</button>
        </div>
      </div>
    </section> : null}
  </main>;
}

function SelectedSessionWorkspace({ drafts, availableSources, identity, onSelectSession, onCreateSession, onOpenInspector, selectedRef, streamState, ready }: { drafts: Map<SessionRef, ComposerDraft>; availableSources: SessionSummary[]; identity: IdentityConfig; onSelectSession: (ref: SessionRef) => void; onCreateSession: () => void; onOpenInspector: () => void; selectedRef: SessionRef; streamState: StreamState; ready: boolean }): JSX.Element {
  const sessionDetail = useSessionDetail(identity, selectedRef, ready);
  const send = useSendSession(identity);
  const compact = useCompactSession(identity);
  const stop = useStopSession(identity);
  const detail = sessionDetail.data;
  const client = useSessionControlClient();
  const runtimeDetails = useSessionRuntimeDetails(identity, detail, ready && Boolean(client.subscribe));
  const catalog = useRuntimeCatalog(identity, ready && Boolean(client.subscribe));
  const [runtimeDraft, setRuntimeDraft] = useState<ComposerRuntimeValue | null>(null);
  const [queueBusy, setQueueBusy] = useState(false);
  const retryInFlight = useRef(false);
  const retryKeys = useRef(new Map<string, string>());
  const [retryError, setRetryError] = useState("");
  const { t } = useI18n();
  const { language } = useI18n();
  const runtimeBusy = queueBusy || Boolean(detail && ACTIVE_RUNTIME_STATUSES.has(detail.status));
  // Another client may start this session after the local next-run draft was edited.
  // Queued messages must follow that active Run; retain the draft for the next idle turn.
  const runtimeValue = (runtimeBusy ? null : runtimeDraft) ?? (detail ? sessionRuntimeConfig(detail, runtimeDetails.data) : { provider: "", model: "", permissionMode: "", effort: "", promptMode: "" });
  async function retry(message: SessionMessage) {
    if (!detail || retryInFlight.current || send.isPending) return;
    const fingerprint = JSON.stringify([message.id, runtimeValue]);
    const key = retryKeys.current.get(fingerprint) ?? crypto.randomUUID();
    let input: ReturnType<typeof sessionRetryInput>;
    try { input = sessionRetryInput(detail, message, key); }
    catch (error) { setRetryError(sessionControlErrorCode(error)); return; }
    if (!input) return;
    retryKeys.current.set(fingerprint, key);
    retryInFlight.current = true;
    setRetryError("");
    try { await send.mutateAsync({ ...input, ...runtimeValue }); retryKeys.current.delete(fingerprint); setRuntimeDraft(null); }
    catch (error) { setRetryError(sessionControlErrorCode(error)); }
    finally { retryInFlight.current = false; }
  }
  if (sessionDetail.isError) return <p role="alert">{t(`webui2.error.${sessionControlErrorCode(sessionDetail.error)}`)}</p>;
  const taskID = detail?.activeRunID || Number(detail?.runs.at(-1)?.id) || undefined;
  const metrics = detail ? conversationRuntimeMetrics(detail, runtimeDetails.data) : {};
  const locked = !ready || send.isPending || runtimeBusy || !detail || detail.source === "local" || ["blocked", "archived"].includes(detail.status);
  const providerOptions = (catalog.providers.data ?? []).map((provider) => ({ value: provider.name, label: `${provider.name} · ${provider.model}`, triggerLabel: provider.name }));
  if (runtimeValue.provider && !providerOptions.some((option) => option.value === runtimeValue.provider)) providerOptions.unshift({ value: runtimeValue.provider, label: runtimeValue.provider, triggerLabel: runtimeValue.provider });
  if (!runtimeValue.provider) providerOptions.unshift({ value: "", label: language === "zh" ? "默认 Provider" : "Default provider", triggerLabel: "Provider" });
  const modelOptions = [...new Set([runtimeValue.model, catalog.status.data?.model, ...(catalog.providers.data ?? []).map((provider) => provider.model), ...(catalog.models.data ?? [])].filter((model): model is string => Boolean(model)))].map((model) => ({ value: model, label: model }));
  async function openSideChat(result: PendingInputSideChatResponse) {
    const list = await client.list(identity, { query: "", statuses: [] });
    const session = list.find((item) => item.id === result.session_id);
    if (!session) throw new Error("Side chat session not found");
    onSelectSession(session.ref);
  }
  const queueScope = { identity, sessionRef: selectedRef, taskID, revision: detail?.cursor };
  return <>
    {retryError ? <p role="alert">{t(`webui2.error.${retryError}`)}</p> : null}
    <ConversationWorkspace identity={identity} runtimeDetails={runtimeDetails.data} streamState={streamState} onRetry={send.isPending || compact.isPending ? undefined : (message) => void retry(message)} composer={detail ? <Composer key={selectedRef} drafts={drafts} availableSources={availableSources} identity={identity} cwd={detail.cwd} disabled={send.isPending || compact.isPending}
      runtimeControls={{ value: runtimeValue, providerOptions, modelOptions, locked, onChange: (next) => { if (locked) return; const provider = catalog.providers.data?.find((item) => item.name === next.provider); setRuntimeDraft(next.provider !== runtimeValue.provider && provider?.model ? { ...next, model: provider.model } : next); }, contextPercent: metrics.contextPercent ?? null, cacheHitPercent: metrics.cacheHitPercent ?? null }}
      nextStepSuggestions={collectConversationNextSteps(detail.events ?? [])}
      queuePanel={client.subscribe && detail.source === "tenant" ? <SessionPendingQueue {...queueScope} onBusyChange={setQueueBusy} onOpenSession={openSideChat} /> : null}
      queueSettings={client.subscribe && detail.source === "tenant" ? <PendingQueueSettingsButton {...queueScope} /> : null}
      onSend={(input) => send.mutateAsync(input)} onCompact={() => compact.mutateAsync({ ref: selectedRef })} onSent={() => setRuntimeDraft(null)} onStopRequested={() => stop.mutateAsync({ ref: selectedRef }).then(() => undefined)} sessionStatus={detail.status} targetRef={selectedRef} /> : null} detail={detail} onCreateSession={onCreateSession} onOpenInspector={onOpenInspector} selectedRef={selectedRef} />
  </>;
}

function SelectedSessionInspector({ identity, onClose, onTabChange, open, selectedRef, tab }: { identity: IdentityConfig; onClose: () => void; onTabChange: (tab: InspectorTab) => void; open: boolean; selectedRef: SessionRef; tab: InspectorTab }): JSX.Element {
  const sessionDetail = useSessionDetail(identity, selectedRef, true);
  const client = useSessionControlClient();
  const runtimeDetails = useSessionRuntimeDetails(identity, sessionDetail.data, Boolean(client.subscribe));
  return <Inspector identity={identity} runtimeDetails={runtimeDetails.data} detail={sessionDetail.data} onClose={onClose} onTabChange={onTabChange} open={open} tab={tab} />;
}

function completeSessionRef(value: string): SessionRef | null {
  const parsed = parseWebUIV2Route(`/webui/v2/sessions/${encodeURIComponent(value.trim())}`);
  return parsed.kind === "session" ? parsed.ref : null;
}
