import { Check, ChevronDown, Search, X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import type { Language } from "../../lib/i18n";
import type { ProvisioningRecord } from "../../lib/types";

type Props = {
  records: ProvisioningRecord[];
  selected: ProvisioningRecord | null;
  disabled: boolean;
  language: Language;
  onSelect: (record: ProvisioningRecord) => void;
};

function stateClass(state: string): string {
  return state.replace(/[^a-z0-9_-]/gi, "-").toLowerCase();
}

export function WorkerSelector({ records, selected, disabled, language, onSelect }: Props) {
  const zh = language === "zh";
  const tr = (en: string, chinese: string): string => zh ? chinese : en;
  const listboxId = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return records;
    return records.filter((record) => `${record.profile_key} ${record.account_key}`.toLocaleLowerCase().includes(normalized));
  }, [query, records]);

  useEffect(() => {
    if (!open) return;
    searchRef.current?.focus();
    const dismiss = (event: PointerEvent): void => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const handleEscape = (event: KeyboardEvent): void => {
      if (event.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [open]);

  function choose(record: ProvisioningRecord): void {
    onSelect(record);
    setOpen(false);
    setQuery("");
    triggerRef.current?.focus();
  }

  return <div className="worker-selector" ref={rootRef}>
    <button
      aria-controls={listboxId}
      aria-expanded={open}
      aria-haspopup="listbox"
      className="worker-selector-trigger"
      disabled={disabled || records.length === 0}
      onClick={() => { setQuery(""); setOpen((value) => !value); }}
      ref={triggerRef}
      type="button"
    >
      <span className="worker-selector-copy">
        <small>{tr("Selected Worker", "当前 Worker")}</small>
        <strong>{selected ? `${selected.profile_key} · ${selected.account_key}` : tr("No Workers yet", "暂无 Worker")}</strong>
      </span>
      <span className="worker-selector-count">{records.length}</span>
      <ChevronDown aria-hidden="true" className={open ? "is-open" : undefined} size={16} />
    </button>
    {open ? <div className="worker-selector-popover">
      <label className="worker-selector-search">
        <Search aria-hidden="true" size={16} />
        <input
          aria-label={tr("Filter Workers", "筛选 Worker")}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={tr("Search by agent or account", "按智能体或账号筛选")}
          ref={searchRef}
          type="search"
          value={query}
        />
        {query ? <button aria-label={tr("Clear filter", "清除筛选")} onClick={() => setQuery("")} type="button"><X size={14} /></button> : null}
      </label>
      <div aria-label={tr("Available Workers", "可选 Worker")} className="worker-selector-options" id={listboxId} role="listbox">
        {filtered.length ? filtered.map((record) => {
          const state = record.observed_worker?.state || record.status;
          const active = selected?.id === record.id;
          return <button
            aria-selected={active}
            className={`worker-selector-option${active ? " is-selected" : ""}`}
            key={record.id}
            onClick={() => choose(record)}
            role="option"
            type="button"
          >
            <span className={`runtime-state-dot ${stateClass(state)}`} />
            <span className="worker-selector-option-copy"><strong>{record.profile_key}</strong><small>{record.account_key}</small></span>
            <span className={`runtime-state-label ${stateClass(state)}`}>{state}</span>
            {active ? <Check aria-hidden="true" size={16} /> : null}
          </button>;
        }) : <div className="worker-selector-empty">{tr("No matching Workers", "没有匹配的 Worker")}</div>}
      </div>
    </div> : null}
  </div>;
}
