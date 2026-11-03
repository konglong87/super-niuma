import { GripVertical, MessageSquareText, Pencil } from "lucide-react";
import { useEffect, useId, useRef, useState, type DragEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../lib/i18n";
import type { SessionRef, SessionSummary } from "../types";
import { SessionHoverPreview } from "./SessionHoverPreview";
import { SessionStatusIcon } from "./SessionStatusIcon";
import { SessionTitleEditor } from "./SessionTitleEditor";
import { getSessionChannelLabel, normalizeSessionProvider } from "./sessionSource";
import "./sessionSidebarExperience.css";

const PREVIEW_OPEN_DELAY = 450;
const PREVIEW_CLOSE_DELAY = 180;
const CONTEXT_MENU_WIDTH = 180;
const CONTEXT_MENU_HEIGHT = 48;
const CONTEXT_MENU_MARGIN = 8;

type Props = {
  session: SessionSummary;
  selected: boolean;
  onSelect: (ref: SessionRef) => void;
  onContextDragStart: (event: DragEvent<HTMLElement>, ref: SessionRef) => void;
  onRename?: (title: string) => Promise<boolean>;
  actions: (showDetails: () => void, startRename?: () => void) => ReactNode;
};

export function SessionRow({ session, selected, onSelect, onContextDragStart, onRename, actions }: Props) {
  const { t } = useI18n();
  const anchor = useRef<HTMLDivElement>(null);
  const contextMenuRef = useRef<HTMLDivElement>(null);
  const selectRef = useRef<HTMLButtonElement>(null);
  const dragged = useRef(false);
  const lastPointerType = useRef<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [contextMenu, setContextMenu] = useState<{ left: number; top: number } | null>(null);
  const [editing, setEditing] = useState(false);
  const previewID = useId();
  const canRename = session.source === "tenant" && (session.id ?? 0) > 0 && Boolean(onRename);
  const channelProvider = normalizeSessionProvider(session.channel?.provider);
  const channelLabel = getSessionChannelLabel(session.channel?.provider, t);
  function clearTimer() { clearTimeout(timer.current); }
  function closePreview() { clearTimer(); setPreviewOpen(false); }
  function showPreview() { clearTimer(); if (!editing && !contextMenu) setPreviewOpen(true); }
  function scheduleClose() {
    clearTimer();
    timer.current = setTimeout(() => setPreviewOpen(false), PREVIEW_CLOSE_DELAY);
  }
  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    if (!previewOpen) return;
    function dismiss() { clearTimeout(timer.current); setPreviewOpen(false); }
    function escape(event: KeyboardEvent) { if (event.key === "Escape") dismiss(); }
    function scroll(event: Event) {
      if (!document.getElementById(previewID)?.contains(event.target as Node)) dismiss();
    }
    function outside(event: PointerEvent) {
      const target = event.target as Node;
      if (!anchor.current?.contains(target) && !document.getElementById(previewID)?.contains(target)) dismiss();
    }
    document.addEventListener("keydown", escape);
    document.addEventListener("pointerdown", outside);
    window.addEventListener("resize", dismiss);
    window.addEventListener("scroll", scroll, true);
    return () => {
      document.removeEventListener("keydown", escape);
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("resize", dismiss);
      window.removeEventListener("scroll", scroll, true);
    };
  }, [previewOpen, previewID]);
  useEffect(() => {
    if (!contextMenu) return;
    contextMenuRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
    function dismiss(event: PointerEvent): void {
      if (!contextMenuRef.current?.contains(event.target as Node)) setContextMenu(null);
    }
    function escape(event: KeyboardEvent): void {
      if (event.key !== "Escape") return;
      event.preventDefault();
      selectRef.current?.focus();
      setContextMenu(null);
    }
    function reposition(): void {
      setContextMenu(null);
    }
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", escape);
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", escape);
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [contextMenu]);
  function startRename(): void {
    if (!canRename) return;
    closePreview();
    setContextMenu(null);
    setEditing(true);
  }
  function openContextMenu(event: ReactMouseEvent<HTMLDivElement>): void {
    if (!canRename || editing) return;
    event.preventDefault();
    closePreview();
    const bounds = event.currentTarget.getBoundingClientRect();
    const x = event.clientX || bounds.left;
    const y = event.clientY || bounds.bottom;
    const left = Math.max(CONTEXT_MENU_MARGIN, Math.min(x, window.innerWidth - CONTEXT_MENU_WIDTH - CONTEXT_MENU_MARGIN));
    const top = Math.max(CONTEXT_MENU_MARGIN, Math.min(y, window.innerHeight - CONTEXT_MENU_HEIGHT - CONTEXT_MENU_MARGIN));
    setContextMenu({ left, top });
  }
  function dragStart(event: DragEvent<HTMLElement>) {
    closePreview();
    dragged.current = true;
    onContextDragStart(event, session.ref);
  }
  return <div ref={anchor} className="webui2-session-row" data-selected={selected} onContextMenu={openContextMenu}
    onPointerEnter={(event) => {
      if (event.pointerType === "touch" || editing || contextMenu) return;
      clearTimer();
      timer.current = setTimeout(() => setPreviewOpen(true), PREVIEW_OPEN_DELAY);
    }} onPointerLeave={scheduleClose}>
    <button aria-label={t("webui2.dragRef", { ref: session.ref })} className="webui2-drag-handle"
      title={t("webui2.dragRef", { ref: session.ref })} disabled={editing} draggable={!editing} onDragStart={dragStart} type="button">
      <GripVertical aria-hidden="true" size={14} />
    </button>
    {editing && onRename ? <SessionTitleEditor title={session.title} onSave={onRename} onClose={() => setEditing(false)} /> : <button aria-pressed={selected} aria-describedby={previewOpen ? previewID : undefined}
      ref={selectRef}
      className="webui2-session-select" draggable onDragStart={dragStart}
      onPointerDown={(event) => {
        lastPointerType.current = event.pointerType;
        dragged.current = false;
        if (event.pointerType === "touch") closePreview();
      }}
      onFocus={() => {
        if (lastPointerType.current !== "touch") showPreview();
      }} onBlur={scheduleClose}
      onClick={(event) => {
        closePreview();
        if (!dragged.current || event.detail === 0) onSelect(session.ref);
        dragged.current = false;
        lastPointerType.current = null;
      }} type="button">
      <span className="webui2-session-title-wrap">
        <span className="webui2-session-title">{session.title}</span>
        {channelLabel ? <span className="webui2-session-channel" title={channelLabel} data-provider={channelProvider} aria-label={channelLabel}>
          <MessageSquareText aria-hidden="true" size={12} />
          <span>{channelLabel}</span>
        </span> : null}
      </span>
      <SessionStatusIcon status={session.status} />
    </button>}
    {!editing ? <div className="webui2-session-action-slot" onPointerEnter={closePreview} onFocusCapture={closePreview}>
      {actions(showPreview, canRename ? startRename : undefined)}
    </div> : null}
    {previewOpen ? <SessionHoverPreview id={previewID} session={session} anchor={anchor} onEnter={clearTimer} onLeave={scheduleClose} /> : null}
    {contextMenu && canRename ? createPortal(<div className="webui2-session-context-menu" ref={contextMenuRef} role="menu" aria-label={t("webui2.sessionActions", { ref: session.ref })} style={{ ...contextMenu, width: CONTEXT_MENU_WIDTH }}>
      <button onClick={startRename} role="menuitem" type="button"><Pencil aria-hidden="true" size={14} />{t("webui2.renameSession")}</button>
    </div>, anchor.current?.closest(".webui2-page") ?? document.body) : null}
  </div>;
}
