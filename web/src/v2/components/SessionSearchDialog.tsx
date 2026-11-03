import { Clock3, MessageSquarePlus, Search, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { SessionRef, SessionSummary } from "../types";

type SessionSearchDialogProps = {
  open: boolean;
  sessions: SessionSummary[];
  onClose: () => void;
  onCreateSession: () => void;
  onSelect: (ref: SessionRef) => void;
};

export function SessionSearchDialog({ open, sessions, onClose, onCreateSession, onSelect }: SessionSearchDialogProps): JSX.Element | null {
  const { language, t } = useI18n();
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const recentSessions = useMemo(() => [...sessions].sort(compareRecentSessions).slice(0, 10), [sessions]);
  const results = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return recentSessions;
    return sessions.filter((session) => sessionSearchText(session).includes(needle)).sort(compareRecentSessions);
  }, [query, recentSessions, sessions]);

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setActiveIndex(0);
    const frame = requestAnimationFrame(() => inputRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [open]);

  useEffect(() => {
    setActiveIndex((current) => Math.min(current, Math.max(0, results.length - 1)));
  }, [results.length]);

  if (!open) return null;

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveIndex((current) => Math.min(results.length - 1, current + 1));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveIndex((current) => Math.max(0, current - 1));
      return;
    }
    if (event.key === "Enter" && results[activeIndex]) {
      event.preventDefault();
      onSelect(results[activeIndex].ref);
    }
  }

  return (
    <div className="webui2-session-search-layer">
      <button aria-label={t("webui2.closeSearch")} className="webui2-session-search-backdrop" onClick={onClose} tabIndex={-1} type="button" />
      <section aria-label={t("webui2.searchSessions")} aria-modal="true" className="webui2-session-search-dialog" onKeyDown={handleKeyDown} role="dialog">
        <div className="webui2-session-search-input-wrap">
          <Search aria-hidden="true" size={21} />
          <input
            aria-activedescendant={results[activeIndex] ? `webui2-session-search-result-${activeIndex}` : undefined}
            aria-label={t("webui2.searchSessions")}
            aria-controls="webui2-session-search-results"
            aria-expanded="true"
            autoComplete="off"
            onChange={(event) => { setQuery(event.target.value); setActiveIndex(0); }}
            placeholder={t("webui2.searchSessionsPlaceholder")}
            ref={inputRef}
            role="combobox"
            value={query}
          />
          <button aria-label={t("webui2.closeSearch")} className="webui2-session-search-close" onClick={onClose} title={t("webui2.closeSearch")} type="button"><X aria-hidden="true" size={20} /></button>
        </div>
        <div className="webui2-session-search-heading">
          <span>{query.trim() ? t("webui2.searchResults") : t("webui2.recentSessions")}</span>
          <small>{query.trim() ? t("webui2.searchResultCount", { count: results.length }) : t("webui2.recentSessionsHint")}</small>
        </div>
        <div className="webui2-session-search-results" id="webui2-session-search-results" role="listbox">
          {results.length === 0 ? <p className="webui2-session-search-empty">{t("webui2.emptySearchResults")}</p> : null}
          {results.map((session, index) => (
            <button
              aria-selected={index === activeIndex}
              className={`webui2-session-search-result${index === activeIndex ? " is-active" : ""}`}
              id={`webui2-session-search-result-${index}`}
              key={session.ref}
              onClick={() => onSelect(session.ref)}
              onPointerEnter={() => setActiveIndex(index)}
              role="option"
              type="button"
            >
              <span className="webui2-session-search-status" data-status={session.status} />
              <span className="webui2-session-search-result-main">
                <strong>{session.title || t("webui2.untitledSession")}</strong>
                <small>{sessionScopeLabel(session, t)} · {formatSessionTime(session.updatedAt, language)}</small>
              </span>
              <code>{session.shortID}</code>
            </button>
          ))}
        </div>
        <div className="webui2-session-search-footer">
          <button className="webui2-session-search-quick-action" onClick={() => { onClose(); onCreateSession(); }} type="button"><MessageSquarePlus aria-hidden="true" size={17} />{t("webui2.newSession")}</button>
          <span><Clock3 aria-hidden="true" size={14} />{t("webui2.searchKeyboardHint")}</span>
        </div>
      </section>
    </div>
  );
}

function sessionSearchText(session: SessionSummary): string {
  return `${session.title} ${session.ref} ${session.cwd ?? ""} ${session.shortID}`.toLowerCase();
}

function sessionScopeLabel(session: SessionSummary, translate: (key: string) => string): string {
  if (session.cwd) return session.cwd;
  return session.source === "local" ? translate("webui2.local") : translate("webui2.managed");
}

function compareRecentSessions(left: SessionSummary, right: SessionSummary): number {
  return Date.parse(right.updatedAt || "") - Date.parse(left.updatedAt || "");
}

function formatSessionTime(value: string, language: string): string {
  const parsed = Date.parse(value);
  if (!Number.isFinite(parsed)) return "";
  return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", { month: "short", day: "numeric" }).format(parsed);
}
