import { Clock3, Folder, MessageSquareText } from "lucide-react";
import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../lib/i18n";
import type { SessionSummary } from "../types";
import { SessionStatusIcon } from "./SessionStatusIcon";
import { getSessionSourceLabel } from "./sessionSource";

const PREVIEW_WIDTH = 340;
const VIEWPORT_MARGIN = 12;
const ANCHOR_GAP = 8;

type Props = {
  id: string;
  session: SessionSummary;
  anchor: RefObject<HTMLDivElement | null>;
  onEnter: () => void;
  onLeave: () => void;
};

export function SessionHoverPreview({ id, session, anchor, onEnter, onLeave }: Props) {
  const { t, language } = useI18n();
  const preview = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState({ left: 0, top: 0 });
  const width = Math.min(PREVIEW_WIDTH, window.innerWidth - VIEWPORT_MARGIN * 2);
  useLayoutEffect(() => {
    const row = anchor.current?.getBoundingClientRect();
    const panel = preview.current?.getBoundingClientRect();
    if (!row || !panel) return;
    const right = row.right + ANCHOR_GAP;
    const left = right + width <= window.innerWidth - VIEWPORT_MARGIN ? right : row.left - width - ANCHOR_GAP;
    setPosition({
      left: Math.max(VIEWPORT_MARGIN, Math.min(left, window.innerWidth - width - VIEWPORT_MARGIN)),
      top: Math.max(VIEWPORT_MARGIN, Math.min(row.top, window.innerHeight - panel.height - VIEWPORT_MARGIN))
    });
  }, [anchor, width]);
  const date = new Date(session.updatedAt);
  const time = Number.isNaN(date.getTime()) ? session.updatedAt : date.toLocaleString(language === "zh" ? "zh-CN" : "en-US");
  const workspace = session.cwd?.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || t("webui2.unknownWorkspace");
  const sourceLabel = getSessionSourceLabel(session.source, session.channel?.provider, t);
  // Keep theme tokens, but escape the sidebar's scrolling/clipping container.
  const portalRoot = anchor.current?.closest(".webui2-page") ?? document.body;
  return createPortal(<div ref={preview} id={id} role="tooltip" className="webui2-session-preview"
    style={{ ...position, width }} onPointerEnter={onEnter} onPointerLeave={onLeave}>
    <strong className="webui2-session-preview-title">{session.title}</strong>
    <div className="webui2-session-preview-workspace" title={session.cwd}><Folder aria-hidden="true" size={16} /><span>{workspace}</span></div>
    <div className="webui2-session-preview-details">
      <span><MessageSquareText aria-hidden="true" size={14} />{sourceLabel}</span>
      <span><SessionStatusIcon status={session.status} />{t(`webui2.status.${session.status}`)}</span>
      <span><Clock3 aria-hidden="true" size={14} />{t("webui2.updatedAt")} <time dateTime={session.updatedAt}>{time}</time></span>
      <code>{session.ref}</code>
    </div>
  </div>, portalRoot);
}
