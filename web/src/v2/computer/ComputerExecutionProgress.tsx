import { Activity, ChevronUp, GripVertical, Minus } from "lucide-react";
import { createPortal } from "react-dom";
import type { ReactElement, PointerEvent as ReactPointerEvent } from "react";
import { useState, useRef } from "react";
import { useI18n } from "../../lib/i18n";
import type { ComputerActionReceipt, ComputerSessionState } from "./types";
import { clampComputerWorkspacePosition, type ComputerWorkspacePoint } from "./computerWorkspacePreferences";
import { computerProgressCopy, preferredComputerLanguage, type ComputerProgressStatus } from "./computerUICopy";

const DRAG_THRESHOLD_PX = 6;
const DEFAULT_RIGHT_PX = 22;
const DEFAULT_BOTTOM_PX = 22;
const MAX_VISIBLE_STEPS = 4;

type DragState = {
  pointerId: number;
  startX: number;
  startY: number;
  originX: number;
  originY: number;
  moved: boolean;
};

type ProgressTone = "active" | "ready" | "paused" | "attention";

type ProgressStatus = {
  key: ComputerProgressStatus;
  tone: ProgressTone;
  summary: string;
};

export type ComputerExecutionProgressProps = {
  state: ComputerSessionState;
  receipts: ComputerActionReceipt[];
  loading?: boolean;
  controlIntent?: "pause" | "stop" | null;
  error?: string | null;
  defaultCollapsed?: boolean;
  onCollapsedChange?: (collapsed: boolean) => void;
};

function portalHost(): HTMLElement {
  return document.querySelector<HTMLElement>(".webui2-page") ?? document.body;
}

function progressStatus(state: ComputerSessionState, loading: boolean, controlIntent: "pause" | "stop" | null, error: string | null): ProgressStatus {
  if (error || state === "failed") return { key: "needsAttention", tone: "attention", summary: error ?? "" };
  if (controlIntent === "pause") return { key: "pausing", tone: "active", summary: "" };
  if (controlIntent === "stop") return { key: "stopping", tone: "active", summary: "" };
  if (loading) return { key: "working", tone: "active", summary: "" };
  if (state === "paused") return { key: "paused", tone: "paused", summary: "" };
  if (state === "needs_observation") return { key: "waiting", tone: "active", summary: "" };
  if (state === "pending_approval") return { key: "awaitingApproval", tone: "paused", summary: "" };
  return { key: "ready", tone: "ready", summary: "" };
}

function stepTime(value: string, language: "en" | "zh"): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleTimeString(language === "zh" ? "zh-CN" : "en-US", { hour: "2-digit", minute: "2-digit" });
}

export function ComputerExecutionProgress({
  state,
  receipts,
  loading = false,
  controlIntent = null,
  error = null,
  defaultCollapsed = false,
  onCollapsedChange,
}: ComputerExecutionProgressProps): ReactElement {
  useI18n(); // Subscribe to the app language so a settings change rerenders this surface.
  const language = preferredComputerLanguage();
  const copy = computerProgressCopy[language];
  const [collapsed, setCollapsed] = useState(defaultCollapsed);
  const [position, setPosition] = useState<ComputerWorkspacePoint | null>(null);
  const [dragging, setDragging] = useState(false);
  const panelRef = useRef<HTMLElement>(null);
  const dragRef = useRef<DragState | null>(null);
  const status = progressStatus(state, loading, controlIntent, error);
  const statusLabel = copy.statusLabel[status.key];
  const statusSummary = status.summary || copy.statusSummary[status.key];
  const recentReceipts = receipts.slice(-MAX_VISIBLE_STEPS).reverse();
  const completedLabel = copy.completedSteps(receipts.length);

  const toggleCollapsed = (): void => {
    const next = !collapsed;
    setCollapsed(next);
    onCollapsedChange?.(next);
  };

  const onDragStart = (event: ReactPointerEvent<HTMLButtonElement>): void => {
    if (event.button !== 0 || !panelRef.current) return;
    const rect = panelRef.current.getBoundingClientRect();
    dragRef.current = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: rect.left, originY: rect.top, moved: false };
    try { event.currentTarget.setPointerCapture?.(event.pointerId); } catch { /* Pointer capture is unavailable in some test environments. */ }
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
    try { event.currentTarget.releasePointerCapture?.(event.pointerId); } catch { /* Pointer capture is unavailable in some test environments. */ }
    if (!current.moved) return;
    setDragging(false);
    const rect = panelRef.current?.getBoundingClientRect();
    if (!rect) return;
    setPosition(clampComputerWorkspacePosition(
      { x: current.originX + event.clientX - current.startX, y: current.originY + event.clientY - current.startY },
      { x: window.innerWidth, y: window.innerHeight },
      { x: rect.width, y: rect.height },
    ));
  };

  const onDragCancel = (event: ReactPointerEvent<HTMLButtonElement>): void => {
    if (dragRef.current?.pointerId !== event.pointerId) return;
    dragRef.current = null;
    setDragging(false);
  };

  const panelStyle = position ? { left: position.x, top: position.y } : { right: DEFAULT_RIGHT_PX, bottom: DEFAULT_BOTTOM_PX };
  const launcher = <button
    aria-expanded={false}
    aria-label={copy.openProgress}
    className="webui2-computer-progress-launcher"
    onClick={toggleCollapsed}
    type="button"
  >
    <Activity aria-hidden="true" size={16} />
    <span className={`webui2-computer-progress-dot webui2-computer-progress-dot--${status.tone}`} />
    <span className="webui2-computer-progress-launcher-copy"><strong>{copy.title}</strong><span>{completedLabel}</span></span>
    <ChevronUp aria-hidden="true" size={15} />
  </button>;

  const panel = <aside
    ref={panelRef}
    aria-label={copy.title}
    className="webui2-computer-progress"
    data-dragging={dragging ? "true" : "false"}
    data-status={status.tone}
    style={panelStyle}
  >
    <header className="webui2-computer-progress-header">
      <button
        aria-label={copy.dragProgress}
        className="webui2-computer-progress-drag-handle"
        data-drag-handle="execution-progress"
        onPointerCancel={onDragCancel}
        onPointerDown={onDragStart}
        onPointerMove={onDragMove}
        onPointerUp={onDragEnd}
        type="button"
      >
        <GripVertical aria-hidden="true" size={16} />
        <span className="webui2-computer-progress-title"><span className="webui2-computer-eyebrow">{copy.liveExecution}</span><strong>{copy.title}</strong></span>
      </button>
      <div className="webui2-computer-progress-header-actions">
        <span className={`webui2-computer-progress-status webui2-computer-progress-status--${status.tone}`}>{statusLabel}</span>
        <button aria-label={copy.closeProgress} className="webui2-computer-icon-button" onClick={toggleCollapsed} title={copy.collapse} type="button"><Minus aria-hidden="true" size={16} /></button>
      </div>
    </header>
    <div className="webui2-computer-progress-summary" aria-live="polite">
      <div className="webui2-computer-progress-summary-row"><strong>{completedLabel}</strong><span>{statusLabel}</span></div>
      <p>{statusSummary}</p>
    </div>
    <section aria-label={copy.executionSteps} className="webui2-computer-progress-steps">
      <div className="webui2-computer-section-heading"><div><span className="webui2-computer-eyebrow">{copy.stepSummary}</span><h3>{copy.recentActivity}</h3></div><span className="webui2-computer-count">{receipts.length}</span></div>
      {recentReceipts.length === 0 ? <p className="webui2-computer-empty">{copy.noSteps}</p> : <ol>
        {recentReceipts.map((receipt) => <li key={receipt.action_id}>
          <span className={`webui2-computer-progress-step-dot webui2-computer-progress-step-dot--${receipt.outcome}`} />
          <div><strong>{receipt.redacted_action_summary || copy.fallbackAction}</strong><span>{copy.outcome[receipt.outcome]} · {copy.verification[receipt.verification]}</span></div>
          <time dateTime={receipt.completed_at}>{stepTime(receipt.completed_at, language)}</time>
        </li>)}
      </ol>}
    </section>
  </aside>;

  return createPortal(collapsed ? launcher : panel, portalHost());
}
