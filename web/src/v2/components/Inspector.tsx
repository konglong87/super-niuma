import { X } from "lucide-react";
import { useEffect, useId, useRef, useState, type JSX, type KeyboardEvent } from "react";
import { useI18n } from "../../lib/i18n";
import type { SessionDetail } from "../types";
import type { IdentityConfig, WebAgentConversationDetail } from "../../lib/types";
import { InspectorDetails } from "./InspectorDetails";
import "./inspectorExperience.css";

export type InspectorTab = "activity" | "context" | "changes" | "runs";

type InspectorProps = {
  detail: SessionDetail | undefined;
  open: boolean;
  onClose: () => void;
  onTabChange?: (tab: InspectorTab) => void;
  tab?: InspectorTab;
  initialTab?: InspectorTab;
  runtimeDetails?: WebAgentConversationDetail;
  identity?: IdentityConfig;
};

const inspectorTabs: InspectorTab[] = ["activity", "context", "changes", "runs"];
const inspectorFocusableSelector = "button:not([disabled]), summary, a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([disabled])";

export function Inspector({ detail, initialTab = "activity", onClose, onTabChange, open, tab, runtimeDetails, identity }: InspectorProps): JSX.Element | null {
  const { t } = useI18n();
  const [uncontrolledTab, setUncontrolledTab] = useState<InspectorTab>(initialTab);
  const isMobile = useMobileInspector();
  const panelRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const titleID = useId();
  const activeTab = tab ?? uncontrolledTab;

  useEffect(() => {
    if (!open || !isMobile) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButtonRef.current?.focus();
    return () => previousFocusRef.current?.focus();
  }, [isMobile, open]);

  if (!open) return null;

  function selectTab(nextTab: InspectorTab): void {
    setUncontrolledTab(nextTab);
    onTabChange?.(nextTab);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLElement>): void {
    if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
      const target = event.target;
      if (!(target instanceof HTMLElement) || target.getAttribute("role") !== "tab") return;
      const currentIndex = inspectorTabs.findIndex((item) => target.id === `webui2-inspector-tab-${item}`);
      if (currentIndex < 0) return;
      event.preventDefault();
      const direction = event.key === "ArrowRight" ? 1 : -1;
      const nextTab = inspectorTabs[(currentIndex + direction + inspectorTabs.length) % inspectorTabs.length];
      selectTab(nextTab);
      panelRef.current?.querySelector<HTMLElement>(`#webui2-inspector-tab-${nextTab}`)?.focus();
      return;
    }
    if (!isMobile) return;
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = Array.from(panelRef.current?.querySelectorAll<HTMLElement>(inspectorFocusableSelector) ?? []).filter(isInspectorFocusable);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable.at(-1);
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  const content = <>
      <header>
        <h2 id={titleID}>{t("webui2.inspector")}</h2>
        <button aria-label={t("webui2.inspector.close")} onClick={onClose} ref={closeButtonRef} title={t("webui2.inspector.close")} type="button"><X aria-hidden="true" size={18} /></button>
      </header>
      <div aria-label={t("webui2.inspector.sections")} className="webui2-inspector-tabs" role="tablist">
        {inspectorTabs.map((item) => <button aria-controls={`webui2-inspector-${item}`} aria-selected={activeTab === item} disabled={!detail} id={`webui2-inspector-tab-${item}`} key={item} onClick={() => selectTab(item)} role="tab" tabIndex={activeTab === item ? 0 : -1} type="button">{t(`webui2.inspector.${item}`)}</button>)}
      </div>
      <div aria-labelledby={`webui2-inspector-tab-${activeTab}`} className="webui2-inspector-content" id={`webui2-inspector-${activeTab}`} role="tabpanel">
        {detail ? <InspectorDetails detail={detail} tab={activeTab} runtimeDetails={runtimeDetails} identity={identity} /> : <InspectorLoading />}
      </div>
  </>;

  return <>
    {isMobile ? <button aria-label={t("webui2.inspector.close")} className="webui2-inspector-backdrop" onClick={onClose} tabIndex={-1} type="button" /> : null}
    {isMobile ? <aside aria-busy={detail ? undefined : "true"} aria-labelledby={titleID} aria-modal="true" className="webui2-inspector" onKeyDown={handleKeyDown} ref={panelRef} role="dialog">{content}</aside> : <aside aria-busy={detail ? undefined : "true"} aria-labelledby={titleID} className="webui2-inspector" onKeyDown={handleKeyDown} ref={panelRef}>{content}</aside>}
  </>;
}

function InspectorLoading(): JSX.Element {
  const { t } = useI18n();
  return <div className="webui2-inspector-loading" role="status"><div aria-hidden="true" className="webui2-inspector-loading-skeleton"><span /><span /></div><p>{t("webui2.inspector.loading")}</p></div>;
}

function useMobileInspector(): boolean {
  const query = "(max-width: 760px)";
  const [isMobile, setIsMobile] = useState(() => matchesInspectorViewport(query));

  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(query);
    const update = () => setIsMobile(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  return isMobile;
}

function matchesInspectorViewport(query: string): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(query).matches;
}

function isInspectorFocusable(element: HTMLElement): boolean {
  if (element.getAttribute("tabindex") === "-1") return false;
  for (let parent = element.parentElement; parent; parent = parent.parentElement) {
    if (parent instanceof HTMLDetailsElement && !parent.open && !parent.querySelector(":scope > summary")?.contains(element)) return false;
  }
  return true;
}
