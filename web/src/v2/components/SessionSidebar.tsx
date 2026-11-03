import { Archive, ChevronDown, ChevronRight, Ellipsis, Filter, Folder, Info, PanelLeftClose, Pencil, Plus, Search, Share2, Square, X } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, type CSSProperties, type DragEvent, type JSX, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { webUIV2SessionPath } from "../routes";
import type { SessionListFilters, SessionRef, SessionSource, SessionStatus, SessionSummary } from "../types";
import { SESSION_REF_MIME_TYPE } from "./sessionContextDrag";
import "./composerExperience.css";
import brandLogo from "../assets/niuma-mark.svg";
import { SessionRow } from "./SessionRow";
import { SidebarAccountFooter } from "./SidebarAccountFooter";

type SessionSidebarProps = {
  sessions: SessionSummary[];
  filters: SessionListFilters;
  selectedRef: SessionRef | null;
  onFiltersChange: (filters: SessionListFilters) => void;
  onSelect: (ref: SessionRef) => void;
  onContextDragStart: (ref: SessionRef) => void;
  onOpenSettings: () => void;
  onOpenWorkflows?: () => void;
  workflowsActive?: boolean;
  onOpenSearch: () => void;
  onHideSidebar: () => void;
  onCreateSession: () => void;
  createDisabled?: boolean;
  onCreateSessionInWorkspace?: (cwd: string) => void;
  onMobileClose?: () => void;
  onStop?: (ref: SessionRef) => void;
  onArchive?: (ref: SessionRef) => void;
  onRenameSession?: (session: SessionSummary, title: string) => Promise<boolean>;
  identity?: IdentityConfig;
};

const statuses: SessionStatus[] = ["idle", "queued", "running", "waiting_permission", "waiting_input", "blocked", "completed", "failed", "stopped", "archived"];
const sidebarWidthStorageKey = "niuma-webui.v2.sidebar-width.v1";
const sidebarMinWidth = 232;
const sidebarMaxWidth = 380;
const sidebarDefaultWidth = 288;
const sidebarKeyboardStep = 8;
const workspaceCollapseStorageKey = "niuma-webui.v2.workspace-collapse.v1";

export function SessionSidebar({ sessions, filters, selectedRef, onFiltersChange, onSelect, onContextDragStart, onOpenSettings, onOpenWorkflows, workflowsActive, onOpenSearch, onHideSidebar, onCreateSession, createDisabled = false, onCreateSessionInWorkspace, onMobileClose, onStop, onArchive, onRenameSession, identity }: SessionSidebarProps): JSX.Element {
  const { t, language } = useI18n();
  const [copyStatus, setCopyStatus] = useState("");
  const [sidebarWidth, setSidebarWidth] = useState(loadSidebarWidth);
  const resizeStartRef = useRef<{ pointerX: number; width: number } | null>(null);
  const query = filters.query.trim().toLowerCase();
  const visibleSessions = sessions.filter((session) => (filters.statuses.length === 0 || filters.statuses.includes(session.status)) && (query === "" || session.title.toLowerCase().includes(query) || session.ref.toLowerCase().includes(query) || session.cwd?.toLowerCase().includes(query)));
  const managed = visibleSessions.filter((session) => session.source === "tenant");
  const local = visibleSessions.filter((session) => session.source === "local");

  const updateSidebarWidth = useCallback((value: number): void => {
    const width = clampSidebarWidth(value);
    setSidebarWidth(width);
    try {
      window.localStorage.setItem(sidebarWidthStorageKey, String(width));
    } catch {
      // Width persistence is optional; the in-memory resize remains available.
    }
  }, []);

  useEffect(() => {
    function resize(event: PointerEvent): void {
      const start = resizeStartRef.current;
      if (start) updateSidebarWidth(start.width + event.clientX - start.pointerX);
    }
    function finishResize(): void {
      resizeStartRef.current = null;
    }
    window.addEventListener("pointermove", resize);
    window.addEventListener("pointerup", finishResize);
    window.addEventListener("pointercancel", finishResize);
    return () => {
      window.removeEventListener("pointermove", resize);
      window.removeEventListener("pointerup", finishResize);
      window.removeEventListener("pointercancel", finishResize);
    };
  }, [updateSidebarWidth]);

  function toggleStatus(status: SessionStatus): void {
    const current = new Set(filters.statuses);
    if (current.has(status)) current.delete(status);
    else current.add(status);
    onFiltersChange({ ...filters, statuses: statuses.filter((item) => current.has(item)) });
  }

  async function copyRef(ref: SessionRef): Promise<void> {
    try {
      await navigator.clipboard.writeText(ref);
      setCopyStatus(t("webui2.copied"));
    } catch {
      setCopyStatus(t("webui2.copyFailed"));
    }
  }

  function dragStart(event: DragEvent<HTMLElement>, ref: SessionRef): void {
    event.dataTransfer?.setData(SESSION_REF_MIME_TYPE, ref);
    event.dataTransfer?.setData("text/plain", ref);
    if (event.dataTransfer) event.dataTransfer.effectAllowed = "copy";
    onContextDragStart(ref);
  }

  function startResize(event: ReactPointerEvent<HTMLElement>): void {
    event.preventDefault();
    resizeStartRef.current = { pointerX: event.clientX, width: sidebarWidth };
  }

  function resizeWithKeyboard(event: ReactKeyboardEvent<HTMLElement>): void {
    const nextWidths: Partial<Record<string, number>> = {
      ArrowLeft: sidebarWidth - sidebarKeyboardStep,
      ArrowRight: sidebarWidth + sidebarKeyboardStep,
      Home: sidebarMinWidth,
      End: sidebarMaxWidth
    };
    const nextWidth = nextWidths[event.key];
    if (nextWidth === undefined) return;
    event.preventDefault();
    updateSidebarWidth(nextWidth);
  }

  function toggleStatusDisclosureWithKeyboard(event: ReactKeyboardEvent<HTMLElement>): void {
    if (event.key !== " ") return;
    event.preventDefault();
    const disclosure = event.currentTarget.parentElement;
    if (disclosure instanceof HTMLDetailsElement) disclosure.open = !disclosure.open;
  }

  return (
    <aside aria-label={t("webui2.sessions")} className="webui2-sidebar" id="webui2-session-sidebar" style={{ width: `${sidebarWidth}px` } as CSSProperties}>
      <div className="webui2-sidebar-controls">
        <div className="webui2-sidebar-toolbar">
          <span className="webui2-brand-mark"><img alt="" src={brandLogo} />{t("webui2.brand")}</span>
          <div className="webui2-sidebar-icon-actions">
            <button aria-label={t("webui2.searchSessions")} className="webui2-sidebar-icon-button" onClick={onOpenSearch} title={t("webui2.searchSessions")} type="button"><Search aria-hidden="true" size={18} /></button>
            <button aria-label={t("webui2.hideSidebar")} className="webui2-sidebar-icon-button" onClick={onHideSidebar} title={t("webui2.hideSidebar")} type="button"><PanelLeftClose aria-hidden="true" size={18} /></button>
            {onMobileClose ? <button aria-label={t("webui2.closeSessions")} className="webui2-sidebar-mobile-close" onClick={onMobileClose} title={t("webui2.closeSessions")} type="button"><X aria-hidden="true" size={17} /></button> : null}
          </div>
        </div>
        <button className="webui2-new-session" disabled={createDisabled} onClick={() => onCreateSession()} type="button"><Plus aria-hidden="true" size={17} />{t("webui2.newSession")}</button>
        {onOpenWorkflows ? <button aria-current={workflowsActive ? "page" : undefined} className="webui2-new-session" onClick={onOpenWorkflows} type="button">{language === "zh" ? "Team 编排工作台" : "Team workflows"}</button> : null}
        {/* Temporarily hide status filtering while retaining the existing controls. */}
        <details className="webui2-status-filter-disclosure" hidden>
          <summary aria-label={t("webui2.filterStatus")} onKeyDown={toggleStatusDisclosureWithKeyboard}>
            <Filter aria-hidden="true" size={15} />
            <span>{t("webui2.filterStatus")}</span>
            <span aria-live="polite" className="webui2-status-filter-count">{filters.statuses.length === 0 ? t("webui2.allStatuses") : t("webui2.statusFilterCount", { count: filters.statuses.length })}</span>
          </summary>
          <fieldset className="webui2-status-filters">
            <legend className="webui2-visually-hidden">{t("webui2.filterStatus")}</legend>
            {statuses.map((status) => <label key={status}><input aria-label={t(`webui2.status.${status}`)} checked={filters.statuses.includes(status)} onChange={() => toggleStatus(status)} type="checkbox" /><span>{t(`webui2.status.${status}`)}</span></label>)}
          </fieldset>
        </details>
      </div>
      <div className="webui2-session-list">
        <SessionGroup copyRef={copyRef} hideTitle onContextDragStart={dragStart} onCreateSessionInWorkspace={onCreateSessionInWorkspace} createDisabled={createDisabled} onRenameSession={onRenameSession} onSelect={onSelect} onStop={onStop} onArchive={onArchive} selectedRef={selectedRef} sessions={managed} source="tenant" title={t("webui2.managed")} />
        <SessionGroup copyRef={copyRef} onContextDragStart={dragStart} onCreateSessionInWorkspace={onCreateSessionInWorkspace} createDisabled={createDisabled} onRenameSession={onRenameSession} onSelect={onSelect} selectedRef={selectedRef} sessions={local} source="local" subtitle={t("webui2.localReadOnly")} title={t("webui2.local")} />
        {visibleSessions.length === 0 ? <p className="webui2-list-empty">{t("webui2.emptySessions")}</p> : null}
      </div>
      <div className="webui2-sidebar-bottom">
        {copyStatus ? <p aria-live="polite" className="webui2-copy-status">{copyStatus}</p> : null}
        {identity ? <SidebarAccountFooter identity={identity} onOpenSettings={onOpenSettings} /> : null}
      </div>
      <hr aria-label={t("webui2.resizeSidebar")} aria-orientation="vertical" aria-valuemax={sidebarMaxWidth} aria-valuemin={sidebarMinWidth} aria-valuenow={sidebarWidth} className="webui2-sidebar-resize" onKeyDown={resizeWithKeyboard} onPointerDown={startResize} tabIndex={0} />
    </aside>
  );
}

function clampSidebarWidth(value: number): number {
  return Math.min(sidebarMaxWidth, Math.max(sidebarMinWidth, Math.round(value)));
}

function loadSidebarWidth(): number {
  try {
    const value = Number(window.localStorage.getItem(sidebarWidthStorageKey));
    return Number.isFinite(value) && value > 0 ? clampSidebarWidth(value) : sidebarDefaultWidth;
  } catch {
    return sidebarDefaultWidth;
  }
}

type SessionGroupProps = {
  createDisabled?: boolean;
  sessions: SessionSummary[];
  title: string;
  subtitle?: string;
  hideTitle?: boolean;
  source: SessionSource;
  onStop?: (ref: SessionRef) => void;
  onArchive?: (ref: SessionRef) => void;
  onCreateSessionInWorkspace?: (cwd: string) => void;
  onRenameSession?: (session: SessionSummary, title: string) => Promise<boolean>;
  selectedRef: SessionRef | null;
  onSelect: (ref: SessionRef) => void;
  onContextDragStart: (event: DragEvent<HTMLElement>, ref: SessionRef) => void;
  copyRef: (ref: SessionRef) => Promise<void>;
};

function SessionGroup({ sessions, title, subtitle, hideTitle, source, selectedRef, onSelect, onContextDragStart, onCreateSessionInWorkspace, createDisabled, onRenameSession, copyRef, onStop, onArchive }: SessionGroupProps): JSX.Element | null {
  const { t } = useI18n();
  const previousSelectedRef = useRef<SessionRef | null | undefined>(undefined);
  const [collapsedWorkspaces, setCollapsedWorkspaces] = useState(loadCollapsedWorkspaces);
  const workspaces = new Map<string, SessionSummary[]>();
  for (const session of sessions) {
    const cwd = session.cwd || "";
    workspaces.set(cwd, [...(workspaces.get(cwd) ?? []), session]);
  }

  useEffect(() => {
    const selectionChanged = previousSelectedRef.current !== selectedRef;
    previousSelectedRef.current = selectedRef;
    if (!selectionChanged) return;
    const selected = sessions.find((session) => session.ref === selectedRef);
    if (!selected) return;
    const key = workspaceKey(source, selected.cwd);
    setCollapsedWorkspaces((current) => {
      if (!current.has(key)) return current;
      const next = new Set(current);
      next.delete(key);
      persistCollapsedWorkspaces(next);
      return next;
    });
  }, [selectedRef, sessions, source]);

  function toggleWorkspace(cwd: string): void {
    const key = workspaceKey(source, cwd);
    setCollapsedWorkspaces((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      persistCollapsedWorkspaces(next);
      return next;
    });
  }

  if (sessions.length === 0) return null;
  return <section aria-label={title} className="webui2-session-group">
    {hideTitle ? null : <header><strong>{title}</strong>{subtitle ? <span>{subtitle}</span> : null}</header>}
    {[...workspaces].map(([cwd, items]) => {
      const key = workspaceKey(source, cwd);
      const collapsed = collapsedWorkspaces.has(key);
      const workspaceName = cwd.replace(/\/$/, "").split("/").pop() || t("webui2.unknownWorkspace");
      const workspaceContentID = `webui2-workspace-${key}`;
      const hasSelectedSession = items.some((session) => session.ref === selectedRef);
      return <div className={`webui2-workspace-group${collapsed ? " is-collapsed" : ""}${hasSelectedSession ? " is-active" : ""}`} data-collapsed={collapsed ? "true" : "false"} key={cwd}>
        <div className="webui2-workspace-heading">
          <button aria-controls={workspaceContentID} aria-expanded={!collapsed} aria-label={t(collapsed ? "webui2.expandWorkspace" : "webui2.collapseWorkspace", { name: workspaceName })} className="webui2-workspace-toggle" onClick={() => toggleWorkspace(cwd)} title={cwd || workspaceName} type="button">
            {collapsed ? <ChevronRight aria-hidden="true" size={15} /> : <ChevronDown aria-hidden="true" size={15} />}
            <Folder aria-hidden="true" size={16} />
            <span className="webui2-workspace-copy">
              <span className="webui2-workspace-name">{workspaceName}</span>
              {cwd ? <span className="webui2-workspace-path" title={cwd}>{cwd}</span> : null}
            </span>
          </button>
          {source === "tenant" && onCreateSessionInWorkspace ? <button aria-label={t("webui2.newSessionInWorkspace", { name: workspaceName })} className="webui2-workspace-new" disabled={createDisabled} onClick={(event) => { event.stopPropagation(); onCreateSessionInWorkspace(cwd); }} title={t("webui2.newSessionInWorkspace", { name: workspaceName })} type="button"><Plus aria-hidden="true" size={15} /></button> : null}
          <span className="webui2-workspace-count">{items.length}</span>
        </div>
        {!collapsed ? <div className="webui2-workspace-sessions" id={workspaceContentID}>
          {items.map((session) => <SessionRow key={session.ref} session={session} selected={selectedRef === session.ref}
            onSelect={onSelect} onContextDragStart={onContextDragStart}
            onRename={onRenameSession ? (title) => onRenameSession(session, title) : undefined}
            actions={(onPreview, startRename) => <SessionOverflowMenu copyRef={copyRef} session={session} onStop={onStop} onArchive={onArchive} onPreview={onPreview} onRename={startRename} />} />)}
        </div> : null}
      </div>;
    })}
  </section>;
}

function workspaceKey(source: SessionSource, cwd?: string): string {
  return `${source}:${encodeURIComponent(cwd || "__default__")}`;
}

function loadCollapsedWorkspaces(): Set<string> {
  try {
    const value = JSON.parse(window.localStorage.getItem(workspaceCollapseStorageKey) || "[]");
    return Array.isArray(value) ? new Set(value.filter((item): item is string => typeof item === "string")) : new Set();
  } catch {
    return new Set();
  }
}

function persistCollapsedWorkspaces(value: Set<string>): void {
  try {
    window.localStorage.setItem(workspaceCollapseStorageKey, JSON.stringify([...value]));
  } catch {
    // Disclosure state is optional; the current render remains interactive.
  }
}

type SessionOverflowMenuProps = {
  session: SessionSummary;
  copyRef: (ref: SessionRef) => Promise<void>;
  onStop?: (ref: SessionRef) => void;
  onArchive?: (ref: SessionRef) => void;
  onPreview: () => void;
  onRename?: () => void;
};

function SessionOverflowMenu({ session, copyRef, onStop, onArchive, onPreview, onRename }: SessionOverflowMenuProps): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuID = useId();

  useEffect(() => {
    if (!open) return;
    function closeFromOutside(event: PointerEvent): void {
      if (!menuRef.current?.contains(event.target as Node)) setOpen(false);
    }
    function closeFromEscape(event: globalThis.KeyboardEvent): void {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    }
    document.addEventListener("pointerdown", closeFromOutside);
    document.addEventListener("keydown", closeFromEscape);
    return () => {
      document.removeEventListener("pointerdown", closeFromOutside);
      document.removeEventListener("keydown", closeFromEscape);
    };
  }, [open]);

  async function copyAndClose(): Promise<void> {
    setOpen(false);
    await copyRef(session.ref);
  }

  async function shareAndClose(): Promise<void> {
    setOpen(false);
    if (!navigator.share) { await copyRef(session.ref); return; }
    try { await navigator.share({ title: session.title, url: `${window.location.origin}${webUIV2SessionPath(session.ref)}` }); } catch { /* Cancelling the share sheet leaves the session unchanged. */ }
  }

  function mutateSession(kind: "stop" | "archive"): void {
    setOpen(false);
    if (window.confirm(t(`webui2.confirm.${kind}`))) (kind === "stop" ? onStop : onArchive)?.(session.ref);
  }

  const stoppable = session.source === "tenant" && ["queued", "running", "waiting_permission", "waiting_input"].includes(session.status);

  return <div className="webui2-session-actions" ref={menuRef}>
    <button aria-controls={menuID} aria-expanded={open} aria-haspopup="menu" aria-label={t("webui2.sessionActions", { ref: session.ref })} onClick={() => setOpen((current) => !current)} ref={triggerRef} title={t("webui2.sessionActions", { ref: session.ref })} type="button"><Ellipsis aria-hidden="true" size={16} /></button>
    {open ? <div aria-label={t("webui2.sessionActions", { ref: session.ref })} className="webui2-session-action-menu" id={menuID} role="menu">
      {onRename ? <button onClick={() => { setOpen(false); onRename(); }} role="menuitem" type="button"><Pencil aria-hidden="true" size={14} />{t("webui2.renameSession")}</button> : null}
      <button onClick={() => { setOpen(false); triggerRef.current?.focus(); onPreview(); }} role="menuitem" type="button"><Info size={14} />{t("webui2.sessionDetails")}</button>
      <button aria-label={t("webui2.copyRef", { ref: session.ref })} onClick={() => void copyAndClose()} role="menuitem" title={t("webui2.copy")} type="button">{t("webui2.copy")}</button>
      <a aria-label={t("webui2.openRef", { ref: session.ref })} href={webUIV2SessionPath(session.ref)} onClick={() => setOpen(false)} role="menuitem" title={t("webui2.openSession")}>{t("webui2.openSession")}</a>
      <button onClick={() => void shareAndClose()} role="menuitem" type="button"><Share2 size={14} />{t("webui2.share")}</button>
      {stoppable && onStop ? <button onClick={() => mutateSession("stop")} role="menuitem" type="button"><Square size={14} />{t("webui2.stop")}</button> : null}
      {session.source === "tenant" && onArchive ? <button onClick={() => mutateSession("archive")} role="menuitem" type="button"><Archive size={14} />{t("webui2.archive")}</button> : null}
    </div> : null}
  </div>;
}
