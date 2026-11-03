import { ChevronDown, ChevronRight, ChevronUp } from "lucide-react";
import { useCallback, useId, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { MarkdownLite } from "../../components/agent/markdown";
import { useI18n } from "../../lib/i18n";

const PREVIEW_LINES = 8;

export function ThinkingMessage({ content, meta, full, live }: { content: string; meta: ReactNode; full: boolean; live: boolean }) {
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const body = <ThinkingBody content={content} full={full} expanded={expanded} onExpand={setExpanded} live={live} />;
  if (full) return <article className="webui2-conversation-message webui2-thinking-full webui2-thinking-surface"><header>{meta}</header>{body}</article>;
  return <details className="webui2-folded-record webui2-thinking-message webui2-thinking-surface" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary><ChevronRight className="webui2-disclosure-chevron" size={13} />{meta}</summary>
    {open ? body : null}
  </details>;
}

function ThinkingBody({ content, full, expanded, onExpand, live }: { content: string; full: boolean; expanded: boolean; onExpand: (value: boolean) => void; live: boolean }) {
  const { language } = useI18n();
  const viewport = useRef<HTMLDivElement>(null);
  const id = useId();
  const [long, setLong] = useState(false);
  const measure = useCallback(() => {
    const element = viewport.current;
    if (!element) return;
    const height = Number.parseFloat(getComputedStyle(element).lineHeight) * PREVIEW_LINES;
    if (Number.isFinite(height)) setLong(element.scrollHeight > height + 1);
  }, []);
  // Content grows on SSE updates; observe width changes without resetting the
  // user's disclosure choice when a phase finishes or new fragments arrive.
  useLayoutEffect(() => { if (content) measure(); }, [content, measure]);
  useLayoutEffect(() => {
    const element = viewport.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [measure]);
  const clamped = !full && !expanded && long;
  return <div className="webui2-thinking-body">
    <div id={id} ref={viewport} className="webui2-thinking-content" data-clamped={clamped} style={{ "--thinking-preview-lines": PREVIEW_LINES } as CSSProperties} onFocusCapture={() => onExpand(true)}>
      <MarkdownLite content={content} live={live} />
    </div>
    {!full && long ? <button type="button" className="webui2-thinking-expand" aria-expanded={expanded} aria-controls={id} onClick={() => onExpand(!expanded)}>{expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}{language === "zh" ? (expanded ? "收起" : "展开全部") : (expanded ? "Show less" : "Show all")}</button> : null}
  </div>;
}
