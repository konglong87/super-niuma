import { Check, ChevronDown, LoaderCircle, Search, Sparkles } from "lucide-react";
import { type JSX, type KeyboardEvent, useEffect, useId, useRef, useState } from "react";
import { listAgentSlashCommands } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import type { AgentSlashCommand, IdentityConfig } from "../../lib/types";
import "./skillPicker.css";

const SKILL_LIMIT = 50;
const SEARCH_THRESHOLD = 8;

type SkillPickerProps = {
  identity: IdentityConfig;
  cwd: string;
  disabled?: boolean;
  selectedName: string;
  onChange: (name: string) => void;
};

export function SkillPicker({ identity, cwd, disabled = false, selectedName, onChange }: SkillPickerProps): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const label = zh ? "选择技能" : "Select Skill";
  const defaultLabel = zh ? "不指定 Skill" : "No Skill";
  const defaultDescription = zh ? "使用会话默认能力" : "Use the session default";
  const menuID = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const autoSelectedKeyRef = useRef("");
  const manuallySelectedQueryRef = useRef<string | null>(null);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<AgentSlashCommand[]>([]);
  const [activeIndex, setActiveIndex] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);

  const skillItems = items.filter((item) => item.source !== "builtin" && item.name.trim() !== "");
  const filteredItems = skillItems.filter((item) => {
    const needle = query.trim().toLowerCase();
    if (!needle) return true;
    return `${item.name} ${item.description ?? ""}`.toLowerCase().includes(needle);
  });
  const options = [{ name: "", description: defaultDescription }, ...filteredItems];
  const firstFilteredName = filteredItems[0]?.name ?? "";
  const selectedItem = skillItems.find((item) => item.name === selectedName);
  const selectedLabel = selectedItem?.name || selectedName || label;
  const listLabel = zh ? "可用 Skills" : "Available Skills";

  useEffect(() => {
    if (!open) return;
    let active = true;
    setLoading(true);
    setError(false);
    void listAgentSlashCommands(identity, cwd, "", SKILL_LIMIT).then((commands) => {
      if (active) setItems(commands);
    }).catch(() => {
      if (active) setError(true);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [cwd, identity, open]);

  useEffect(() => {
    const normalizedQuery = query.trim();
    if (!normalizedQuery) {
      autoSelectedKeyRef.current = "";
      manuallySelectedQueryRef.current = null;
      return;
    }
    if (!open || !firstFilteredName || manuallySelectedQueryRef.current === normalizedQuery) return;
    const selectionKey = `${normalizedQuery}\u0000${firstFilteredName}`;
    if (autoSelectedKeyRef.current === selectionKey) return;
    autoSelectedKeyRef.current = selectionKey;
    onChange(firstFilteredName);
  }, [firstFilteredName, onChange, open, query]);

  const selectedIndex = Math.max(0, options.findIndex((option) => option.name === selectedName));
  useEffect(() => {
    if (!open) return;
    setActiveIndex(selectedIndex);
    if (items.length > SEARCH_THRESHOLD) searchRef.current?.focus();
    else menuRef.current?.focus();
  }, [items.length, open, selectedIndex]);

  useEffect(() => {
    if (!open) return;
    const closeOutside = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, [open]);

  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);

  function commit(index: number): void {
    const option = options[index];
    if (!option) return;
    manuallySelectedQueryRef.current = query.trim() || null;
    autoSelectedKeyRef.current = "";
    onChange(option.name);
    setOpen(false);
    triggerRef.current?.focus();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
      return;
    }
    if (event.target instanceof HTMLInputElement) return;
    if (event.key === "Tab") {
      setOpen(false);
      return;
    }
    if (options.length === 0) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      setActiveIndex((index) => (index + (event.key === "ArrowDown" ? 1 : -1) + options.length) % options.length);
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      commit(activeIndex);
    }
  }

  return <div className="webui2-skill-picker" ref={rootRef}>
    <button
      aria-controls={open ? menuID : undefined}
      aria-expanded={open}
      aria-haspopup="listbox"
      aria-label={label}
      className={`webui2-skill-picker-trigger${selectedName ? " has-selection" : ""}`}
      disabled={disabled}
      onClick={() => setOpen((value) => !value)}
      ref={triggerRef}
      title={selectedItem?.description ? `${selectedItem.name}: ${selectedItem.description}` : selectedLabel}
      type="button"
    >
      <span>{selectedLabel}</span>
      <ChevronDown aria-hidden="true" size={14} />
    </button>
    {open ? <div
      aria-label={listLabel}
      aria-activedescendant={`${menuID}-option-${activeIndex}`}
      className="webui2-skill-picker-menu"
      id={menuID}
      onKeyDown={handleKeyDown}
      ref={menuRef}
      role="listbox"
      tabIndex={-1}
    >
      <header className="webui2-skill-picker-header">
        <strong>{listLabel}</strong>
        {loading ? <LoaderCircle aria-label={zh ? "加载中" : "Loading"} className="webui2-skill-picker-spinner" size={15} /> : <span>{skillItems.length}</span>}
      </header>
      {skillItems.length > SEARCH_THRESHOLD ? <label className="webui2-skill-picker-search">
        <Search aria-hidden="true" size={15} />
        <input aria-label={zh ? "搜索 Skill" : "Search Skill"} onChange={(event) => setQuery(event.target.value)} placeholder={zh ? "搜索 Skill" : "Search skills"} ref={searchRef} value={query} />
      </label> : null}
      {error ? <p className="webui2-skill-picker-state" role="alert">{zh ? "Skills 暂时不可用" : "Skills are unavailable"}</p>
        : loading ? <p className="webui2-skill-picker-state" role="status">{zh ? "正在加载…" : "Loading…"}</p>
          : options.length === 1 ? <p className="webui2-skill-picker-state" role="status">{query ? (zh ? "没有匹配的 Skill" : "No matching skills") : (zh ? "暂无可用 Skill" : "No skills available")}</p>
            : <div className="webui2-skill-picker-options">
              {options.map((option, index) => {
                const selected = option.name === selectedName;
                const active = index === activeIndex;
                const item = option.name ? filteredItems.find((candidate) => candidate.name === option.name) : undefined;
                return <button
                  aria-selected={selected}
                  className={`webui2-skill-picker-option${active ? " is-active" : ""}${selected ? " is-selected" : ""}`}
                  id={`${menuID}-option-${index}`}
                  key={option.name || "default"}
                  onClick={() => commit(index)}
                  onMouseEnter={() => setActiveIndex(index)}
                  role="option"
                  type="button"
                >
                  <Sparkles aria-hidden="true" size={17} />
                  <span className="webui2-skill-picker-option-copy">
                    <strong>{option.name || defaultLabel}</strong>
                    <small>{item?.description || option.description}</small>
                    {item?.source ? <em>{item.source}</em> : null}
                  </span>
                  {selected ? <Check aria-hidden="true" className="webui2-skill-picker-check" size={17} /> : null}
                </button>;
              })}
            </div>}
    </div> : null}
  </div>;
}
