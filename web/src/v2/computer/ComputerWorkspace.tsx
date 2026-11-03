import { ChevronDown, GripVertical, Monitor, Minus } from "lucide-react";
import { createPortal } from "react-dom";
import type { ReactElement, PointerEvent as ReactPointerEvent } from "react";
import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { computerReadinessMessage, computerRuntimeFaultMessage, computerUICopy, preferredComputerLanguage, localizeComputerError } from "./computerUICopy";
import { ComputerApprovalDialog } from "./ComputerApprovalDialog";
import { ComputerExecutionProgress } from "./ComputerExecutionProgress";
import { ComputerPermissionGuide } from "./ComputerPermissionGuide";
import type { ComputerClient } from "./client";
import type { SessionRef } from "../routes";
import type { StartComputerSessionInput } from "./types";
import { managedComputerConversationRef } from "./conversationApproval";
import { ComputerPreview } from "./ComputerPreview";
import { ComputerTimeline } from "./ComputerTimeline";
import { ComputerToolbar } from "./ComputerToolbar";
import { COMPUTER_WORKSPACE_DISPLAY_MODES, COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT, clampComputerWorkspacePosition, loadComputerWorkspacePreferences, saveComputerWorkspacePreferences, type ComputerWorkspaceDisplayMode, type ComputerWorkspacePoint } from "./computerWorkspacePreferences";
import { useComputerSession } from "./useComputerSession";
import { useNativeComputerOverlay } from "./useNativeComputerOverlay";
import { createComputerWorkspaceDisplayState, projectComputerWorkspaceDisplay, reduceComputerWorkspaceDisplay } from "./computerWorkspaceDisplay";

const DRAG_THRESHOLD_PX = 6;
const DEFAULT_TOP_PX = 76;
const DEFAULT_RIGHT_PX = 20;
type DragState = {
  pointerId: number;
  startX: number;
  startY: number;
  originX: number;
  originY: number;
  moved: boolean;
};

function portalHost(): HTMLElement {
  return document.querySelector<HTMLElement>(".webui2-page") ?? document.body;
}

export function ComputerWorkspace({ client, selectedConversationRef = null, nativeOverlay = false }: { client: ComputerClient | null; selectedConversationRef?: SessionRef | null; nativeOverlay?: boolean }): ReactElement | null {
  useI18n(); // Subscribe to the app language so a settings change rerenders this surface.
  const language = preferredComputerLanguage();
  const copy = computerUICopy[language];
  const computer = useComputerSession(client);
  // Capture the payload when opening the dialog, not when approving it.
  const [approval, setApproval] = useState<StartComputerSessionInput | null>(null);
  const [preferences, setPreferences] = useState(loadComputerWorkspacePreferences);
  const [displayMode, setDisplayMode] = useState<ComputerWorkspaceDisplayMode>(preferences.displayMode);
  const [position, setPosition] = useState<ComputerWorkspacePoint | null>(preferences.position);
  const [displayState, dispatchDisplay] = useReducer(reduceComputerWorkspaceDisplay, preferences.displayMode, createComputerWorkspaceDisplayState);
  const [dragging, setDragging] = useState(false);
  const panelRef = useRef<HTMLElement>(null);
  const dragRef = useRef<DragState | null>(null);

  useEffect(() => {
    setApproval(null);
    void computer.loadCapabilities().catch(() => undefined); // Hook renders the error.
  }, [computer.loadCapabilities]);

  useEffect(() => {
    const refreshPreferences = (): void => {
      const next = loadComputerWorkspacePreferences();
      setPreferences(next);
      setDisplayMode(next.displayMode);
      setPosition(next.position);
    };
    window.addEventListener(COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT, refreshPreferences);
    return () => window.removeEventListener(COMPUTER_WORKSPACE_PREFERENCES_CHANGED_EVENT, refreshPreferences);
  }, []);

  const state = computer.session?.state ?? "idle";
  const readiness = computerReadinessMessage(computer.available, computer.capabilities, language);
  const runtimeFault = computer.runtimeStatus?.fault ?? null;
  const runtimeFaultMessage = runtimeFault ? computerRuntimeFaultMessage(runtimeFault.error_code, language) : null;
  const hasDisplayError = Boolean(computer.error || readiness || runtimeFault);
  const overlay = useNativeComputerOverlay(nativeOverlay && client !== null, computer.session, language, readiness, displayMode);
  useEffect(() => {
    dispatchDisplay({
      type: "sync",
      mode: displayMode,
      sessionID: computer.session?.session_id ?? null,
      sessionState: state,
      hasError: hasDisplayError,
    });
  }, [computer.session?.session_id, displayMode, hasDisplayError, state]);

  useEffect(() => {
    const openWorkspace = (): void => {
      if (overlay.active) overlay.show();
      else dispatchDisplay({ type: "open" });
    };
    window.addEventListener("niuma:computer-workspace-open", openWorkspace);
    return () => window.removeEventListener("niuma:computer-workspace-open", openWorkspace);
  }, [overlay.active, overlay.show]);

  const status = readiness || runtimeFault ? "attention" : state;
  const backend = computer.capabilities?.backend || copy.backendDetecting;
  const active = computer.session && computer.session.state !== "stopped";
  const modelManaged = computer.session?.owner_kind === "managed_conversation";
  const approvalLabel = modelManaged ? copy.modelManagedSession : computer.approvedConversationRef
    ? `${copy.boundConversation}: ${computer.approvedConversationRef}` : copy.localPreview;
  const projection = projectComputerWorkspaceDisplay(displayState);
  useEffect(() => {
    const onResize = (): void => {
      const rect = panelRef.current?.getBoundingClientRect();
      if (!rect || !position) return;
      const next = clampComputerWorkspacePosition(position, { x: window.innerWidth, y: window.innerHeight }, { x: rect.width, y: rect.height });
      if (next.x !== position.x || next.y !== position.y) {
        setPosition(next);
        setPreferences((current) => ({ ...current, position: next }));
        saveComputerWorkspacePreferences({ ...preferences, position: next });
      }
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [position, preferences]);

  const persist = useCallback((next: Partial<typeof preferences>): void => {
    setPreferences((current) => {
      const merged = { ...current, ...next };
      saveComputerWorkspacePreferences(merged);
      return merged;
    });
  }, []);

  if (!client) return null;

  const openApproval = (): void => {
    dispatchDisplay({ type: "attempt" });
    const ref = managedComputerConversationRef(selectedConversationRef);
    setApproval(ref ? { approved: true, conversation_ref: ref } : { approved: true });
  };
  const start = async () => {
    if (!approval) return;
    const approvedInput = approval;
    setApproval(null);
    try {
      const session = await computer.start(approvedInput);
      if (!overlay.available && session?.owner_kind !== "managed_conversation"
        && (session?.state === "ready" || session?.state === "needs_observation")) await computer.observe();
    } catch { /* surfaced in the panel */ }
  };
  const action = (operation: () => Promise<unknown>) => { void operation().catch(() => undefined); };

  const toggleCollapsed = (): void => {
    dispatchDisplay({ type: "toggle" });
  };

  const onDragStart = (event: ReactPointerEvent<HTMLButtonElement>): void => {
    if (event.button !== 0 || !panelRef.current) return;
    const rect = panelRef.current.getBoundingClientRect();
    dragRef.current = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: rect.left, originY: rect.top, moved: false };
    try { event.currentTarget.setPointerCapture(event.pointerId); } catch { /* Pointer capture is unavailable in some test environments. */ }
  };

  const onDragMove = (event: ReactPointerEvent<HTMLButtonElement>): void => {
    const current = dragRef.current;
    if (!current || current.pointerId !== event.pointerId || !panelRef.current) return;
    const dx = event.clientX - current.startX;
    const dy = event.clientY - current.startY;
    if (!current.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD_PX) return;
    if (!current.moved) {
      current.moved = true;
      setDragging(true);
    }
    const rect = panelRef.current.getBoundingClientRect();
    setPosition(clampComputerWorkspacePosition(
      { x: current.originX + dx, y: current.originY + dy },
      { x: window.innerWidth, y: window.innerHeight },
      { x: rect.width, y: rect.height },
    ));
  };

  const onDragEnd = (event: ReactPointerEvent<HTMLButtonElement>): void => {
    const current = dragRef.current;
    if (!current || current.pointerId !== event.pointerId) return;
    dragRef.current = null;
    try { event.currentTarget.releasePointerCapture(event.pointerId); } catch { /* Pointer capture is unavailable in some test environments. */ }
    if (!current.moved) return;
    setDragging(false);
    const rect = panelRef.current?.getBoundingClientRect();
    if (!rect) return;
    const next = clampComputerWorkspacePosition(
      { x: current.originX + event.clientX - current.startX, y: current.originY + event.clientY - current.startY },
      { x: window.innerWidth, y: window.innerHeight },
      { x: rect.width, y: rect.height },
    );
    setPosition(next);
    persist({ position: next });
  };

  const onDragCancel = (event: ReactPointerEvent<HTMLButtonElement>): void => {
    if (dragRef.current?.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(false);
  };

  const nativeOverlaySession = overlay.active;
  const panelStyle = position ? { left: position.x, top: position.y } : { right: DEFAULT_RIGHT_PX, top: DEFAULT_TOP_PX };
  const panel = nativeOverlaySession
    ? <button
      aria-label={copy.reopenNativePanel}
      className="webui2-computer-launcher"
      onClick={overlay.show}
      title={copy.reopenNativePanel}
      type="button"
    >
      <Monitor aria-hidden="true" size={17} strokeWidth={2.2} />
      <span className="webui2-computer-launcher-label">{copy.nativeTitle}</span>
    </button>
    : !projection.visible
    ? null
    : !projection.expanded
    ? <button
      aria-expanded={false}
      aria-label={copy.launcher}
      className="webui2-computer-launcher"
      onClick={toggleCollapsed}
      type="button"
    >
      <Monitor aria-hidden="true" size={17} strokeWidth={2.2} />
      <span className={`webui2-computer-launcher-dot webui2-computer-launcher-dot--${status}`} />
      <span className="webui2-computer-launcher-label">{copy.controlSurface}</span>
      {active ? <span style={{ maxWidth: 220, overflowWrap: "anywhere", fontSize: 11 }}>{approvalLabel}</span> : null}
      <span className="webui2-computer-launcher-backend">{backend}</span>
      <ChevronDown aria-hidden="true" size={15} />
    </button>
    : <aside
      ref={panelRef}
      aria-label={copy.workspace}
      className="webui2-computer-workspace"
      data-dragging={dragging ? "true" : "false"}
      style={panelStyle}
    >
      <header className="webui2-computer-header">
        <button
          aria-label={copy.dragWorkspace}
          className="webui2-computer-drag-handle"
          data-drag-handle="true"
          onPointerCancel={onDragCancel}
          onPointerDown={onDragStart}
          onPointerMove={onDragMove}
          onPointerUp={onDragEnd}
          type="button"
        >
          <GripVertical aria-hidden="true" size={16} />
          <span className="webui2-computer-header-title"><span className="webui2-computer-eyebrow">{copy.controlSurface}</span><span className="webui2-computer-header-title-text">{copy.workspace}</span></span>
        </button>
        <div className="webui2-computer-header-actions">
          <span className="webui2-computer-backend">{backend}</span>
          <button aria-label={copy.collapseWorkspace} className="webui2-computer-icon-button" onClick={toggleCollapsed} title={copy.collapse} type="button"><Minus aria-hidden="true" size={16} /></button>
        </div>
      </header>
      <ComputerToolbar
        state={computer.session?.state ?? null}
        capabilities={computer.capabilities}
        available={computer.available}
        busy={computer.loading}
        controlIntent={computer.controlIntent}
        runtimeFault={runtimeFault}
        readOnly={modelManaged}
        onStart={openApproval}
        onObserve={() => action(computer.observe)}
        onPause={() => action(computer.pause)}
        onResume={() => action(computer.resume)}
        onStop={() => action(computer.stop)}
      />
      {active ? <p role="status" style={{ margin: 0, overflowWrap: "anywhere" }}>{approvalLabel}</p> : null}
      {runtimeFault ? <section className="webui2-computer-error" data-runtime-fault={runtimeFault.error_code} role="alert">
        <strong>{copy.runtimeFaultTitle}</strong>
        <p>{runtimeFaultMessage}</p>
        <p>{copy.runtimeFaultNoReplay}</p>
        <p>{copy.runtimeRestartHint}</p>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button className="webui2-computer-button webui2-computer-button--quiet" onClick={() => action(computer.loadRuntimeStatus)} type="button">{copy.refreshRuntimeStatus}</button>
          {computer.stopRuntime ? <button className="webui2-computer-button webui2-computer-button--danger" disabled={computer.loading} onClick={() => action(computer.stopRuntime)} type="button">{copy.stopRuntime}</button> : null}
        </div>
      </section> : null}
      {projection.showFeedback && (!computer.session || computer.session.state === "stopped" || computer.capabilities?.permission_state === "required") ? <ComputerPermissionGuide
        available={computer.available}
        capabilities={computer.capabilities}
        client={client}
        onRecheck={async () => { await computer.loadCapabilities(); }}
      /> : null}
      {projection.showError && !computer.error && readiness ? <p className="webui2-computer-error" role="status">{readiness}</p> : null}
      {projection.showError && computer.error ? <p className="webui2-computer-error" role="alert">{localizeComputerError(computer.error, language)}</p> : null}
      <ComputerPreview observation={computer.observation} capabilities={computer.capabilities} />
      <ComputerTimeline receipts={computer.receipts} />
      {approval ? <ComputerApprovalDialog
        conversationRef={approval.conversation_ref}
        available={computer.available}
        capabilities={computer.capabilities}
        busy={computer.loading}
        onApprove={() => void start()}
        onCancel={() => setApproval(null)}
      /> : null}
    </aside>;

  const activeSession = computer.session;
  const domWorkspaceVisible = !nativeOverlaySession && projection.visible && projection.expanded;
  const showExecutionProgress = !nativeOverlaySession && !domWorkspaceVisible && displayMode !== COMPUTER_WORKSPACE_DISPLAY_MODES.AUTO
    && activeSession !== null && activeSession.state !== "stopped";
  const executionProgress = showExecutionProgress && activeSession ? <ComputerExecutionProgress
    controlIntent={computer.controlIntent}
    error={computer.error ? localizeComputerError(computer.error, language) : null}
    loading={computer.loading}
    receipts={computer.receipts}
    state={activeSession.state}
  /> : null;

  return createPortal(<>{panel}{executionProgress}</>, portalHost());
}
