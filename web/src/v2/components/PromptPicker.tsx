import { ArrowLeft, Check, FileText, LoaderCircle, Pencil, Pin, Plus, Search, Star, Trash2, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type JSX, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { deletePromptTemplate, listPromptTemplates, savePromptTemplate } from "../../lib/api";
import type { IdentityConfig, PromptTemplate } from "../../lib/types";
import { useI18n } from "../../lib/i18n";
import "./promptPicker.css";

type Props = {
  identity: IdentityConfig;
  disabled?: boolean;
  // The host owns draft readiness; management remains available when insertion is blocked.
  selectionDisabledReason?: string;
  onSelect: (content: string) => void;
};
const EMPTY_PROMPT: PromptTemplate = { id: 0, title: "", content: "", category: "", pinned: false, sort_order: 0 };
const SEARCH_DELAY_MS = 180;
const FOCUSABLE_SELECTOR = 'button:not(:disabled), input:not(:disabled), textarea:not(:disabled)';

export function PromptPicker({ identity, disabled = false, selectionDisabledReason, onSelect }: Props): JSX.Element {
  const { language } = useI18n();
  const label = language === "zh" ? "常用提示词" : "Common prompts";
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  // Keep theme inheritance while escaping the Composer's form/control selectors.
  const portalTarget = triggerRef.current?.closest(".webui2-page, .web-agent-page") ?? document.body;
  return <span className="prompt-picker-root">
    <button className="prompt-picker-trigger" type="button" ref={triggerRef} aria-label={label} title={label} aria-haspopup="dialog" aria-expanded={open} disabled={disabled} onClick={() => setOpen(true)}>
      <Star aria-hidden="true" size={18} />
    </button>
    {open ? createPortal(<PromptPickerDialog
      key={JSON.stringify([identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId])}
      identity={identity} selectionDisabledReason={selectionDisabledReason} onSelect={onSelect} onClose={() => { setOpen(false); triggerRef.current?.focus(); }}
    />, portalTarget) : null}
  </span>;
}

function PromptPickerDialog({ identity, selectionDisabledReason, onSelect, onClose }: Pick<Props, "identity" | "selectionDisabledReason" | "onSelect"> & { onClose: () => void }): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const text = (chinese: string, english: string) => zh ? chinese : english;
  const titleID = useId();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const mutationRef = useRef(false);
  const mountedRef = useRef(true);
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<PromptTemplate[]>([]);
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<PromptTemplate | null>(null);
  const [original, setOriginal] = useState<PromptTemplate | null>(null);
  const [deleting, setDeleting] = useState<PromptTemplate | null>(null);
  const dirty = editing && JSON.stringify(editing) !== JSON.stringify(original);

  useEffect(() => {
    mountedRef.current = true;
    const dialog = dialogRef.current!;
    dialog.showModal();
    return () => { mountedRef.current = false; dialog.close(); };
  }, []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setLoadError("");
    const timer = window.setTimeout(() => {
      void listPromptTemplates(identity, query).then(
        (result) => { if (active) setItems(result); },
        (failure) => { if (active) setLoadError(String(failure instanceof Error ? failure.message : failure)); }
      ).finally(() => { if (active) setLoading(false); });
    }, query ? SEARCH_DELAY_MS : 0);
    return () => { active = false; window.clearTimeout(timer); };
  }, [identity.apiBase, identity.apiToken, identity.mobileJwt, identity.tenantKey, identity.userId, identity.deviceId, query, revision]);

  useEffect(() => {
    if (deleting) cancelRef.current?.focus();
    else if (!editing) searchRef.current?.focus();
  }, [editing, deleting]);

  function leaveEditor(): boolean {
    return !dirty || window.confirm(text("放弃尚未保存的提示词？", "Discard unsaved prompt changes?"));
  }

  function close(): void {
    if (!mutationRef.current && leaveEditor()) dismiss();
  }

  function dismiss(): void {
    // Release the native inert background before restoring launcher focus.
    dialogRef.current?.close();
    onClose();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDialogElement>): void {
    event.stopPropagation();
    if (event.key !== "Tab") return;
    const controls = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? []);
    const first = controls[0];
    const last = controls.at(-1);
    if (!first || !last) { event.preventDefault(); return; }
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function back(): void {
    if (mutationRef.current || !leaveEditor()) return;
    setEditing(null);
    setDeleting(null);
    setError("");
  }

  function edit(item: PromptTemplate): void {
    setOriginal(item);
    setEditing({ ...item });
    setError("");
  }

  async function mutate(operation: () => Promise<unknown>, afterSave: () => void): Promise<void> {
    if (mutationRef.current) return;
    mutationRef.current = true;
    setSaving(true);
    setError("");
    try {
      await operation();
      if (!mountedRef.current) return;
      afterSave();
      setRevision((value) => value + 1);
    } catch (failure) {
      if (mountedRef.current) setError(String(failure instanceof Error ? failure.message : failure));
    } finally {
      mutationRef.current = false;
      if (mountedRef.current) setSaving(false);
    }
  }

  function save(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    event.stopPropagation();
    if (!editing?.title.trim() || !editing.content.trim()) return;
    void mutate(() => savePromptTemplate(identity, editing), () => { setEditing(null); setQuery(""); });
  }

  const title = deleting ? text("删除提示词", "Delete prompt") : editing ? (editing.id ? text("编辑提示词", "Edit prompt") : text("新增提示词", "New prompt")) : text("常用提示词", "Common prompts");
  return <dialog className="prompt-picker" ref={dialogRef} aria-labelledby={titleID} aria-modal="true"
    onCancel={(event) => { event.preventDefault(); deleting || editing ? back() : close(); }}
    onKeyDown={handleKeyDown}
    onClick={(event) => { if (event.target === event.currentTarget) close(); }}>
    <div className="prompt-picker-panel">
      <header className="prompt-picker-header">
        <div className="prompt-picker-heading">
          {editing || deleting ? <button className="prompt-picker-icon" type="button" aria-label={text("返回列表", "Back to list")} title={text("返回列表", "Back to list")} disabled={saving} onClick={back}><ArrowLeft size={18} aria-hidden="true" /></button> : <Star size={18} aria-hidden="true" />}
          <h2 id={titleID}>{title}</h2>
          {!editing && !deleting && !loading && !loadError ? <span className="prompt-picker-count">{items.length}</span> : null}
        </div>
        <button className="prompt-picker-icon" type="button" aria-label={text("关闭", "Close")} title={text("关闭", "Close")} disabled={saving} onClick={close}><X size={18} aria-hidden="true" /></button>
      </header>
      {error ? <p className="prompt-picker-error" role="alert">{error}</p> : null}
      {deleting ? <>
        <div className="prompt-picker-delete">
          <Trash2 size={24} aria-hidden="true" />
          <p>{text("确认删除", "Delete")} <strong>{deleting.title}</strong>{text("？", "?")}</p>
          <p>{text("此操作无法撤销。", "This cannot be undone.")}</p>
        </div>
        <footer className="prompt-picker-footer">
          <button className="prompt-picker-button" type="button" ref={cancelRef} disabled={saving} onClick={back}>{text("取消", "Cancel")}</button>
          <button className="prompt-picker-button is-danger" type="button" disabled={saving} onClick={() => void mutate(() => deletePromptTemplate(identity, deleting.id), () => setDeleting(null))}>
            {saving ? <LoaderCircle className="prompt-picker-spinner" size={15} aria-hidden="true" /> : <Trash2 size={15} aria-hidden="true" />}{text("确认删除", "Delete prompt")}
          </button>
        </footer>
      </> : editing ? <PromptEditor value={editing} busy={saving} onChange={setEditing} onSubmit={save} onCancel={back} zh={zh} /> : <>
        <div className="prompt-picker-toolbar">
          <label className="prompt-picker-search"><Search size={17} aria-hidden="true" /><input type="search" ref={searchRef} aria-label={text("搜索标题、内容或分类", "Search title, content or category")} placeholder={text("搜索提示词…", "Search prompts…")} value={query} onChange={(event) => setQuery(event.target.value)} /></label>
          <button className="prompt-picker-button is-primary" type="button" disabled={saving} onClick={() => edit(EMPTY_PROMPT)}><Plus size={16} aria-hidden="true" />{text("新增", "New")}</button>
        </div>
        <div className="prompt-picker-results" aria-busy={loading}>
          {loading ? <div className="prompt-picker-empty" role="status"><LoaderCircle className="prompt-picker-spinner" size={24} aria-hidden="true" /><p>{text("加载中…", "Loading…")}</p></div>
            : loadError ? <div className="prompt-picker-empty"><p className="prompt-picker-error" role="alert">{loadError}</p><button className="prompt-picker-button" type="button" onClick={() => setRevision((value) => value + 1)}>{text("重试", "Retry")}</button></div>
            : items.length === 0 ? <div className="prompt-picker-empty" role="status"><FileText size={30} strokeWidth={1.4} aria-hidden="true" /><h3>{query ? text("没有匹配的提示词", "No matching prompts") : text("暂无常用提示词", "No common prompts yet")}</h3>{!query ? <button className="prompt-picker-button" type="button" onClick={() => edit(EMPTY_PROMPT)}><Plus size={15} aria-hidden="true" />{text("新增提示词", "Add prompt")}</button> : null}</div>
            : <ul className="prompt-picker-list">{items.map((item) => <li className="prompt-picker-item" key={item.id}>
              <button className="prompt-picker-select" type="button" disabled={saving || Boolean(selectionDisabledReason)} title={selectionDisabledReason} aria-description={selectionDisabledReason} aria-label={`${text("使用", "Use")} ${item.title}`} onClick={() => { onSelect(item.content); dismiss(); }}>
                <span className="prompt-picker-item-title">{item.title}</span>
                <span className="prompt-picker-preview">{item.content}</span>
                <span className="prompt-picker-category">{item.category || text("未分类", "Uncategorized")}</span>
              </button>
              <div className="prompt-picker-item-actions">
                <button className="prompt-picker-icon" type="button" disabled={saving} aria-label={text("置顶", "Pin")} title={text("置顶", "Pin")} aria-pressed={item.pinned} onClick={() => void mutate(() => savePromptTemplate(identity, { ...item, pinned: !item.pinned }), () => {})}><Pin size={15} aria-hidden="true" /></button>
                <button className="prompt-picker-icon" type="button" disabled={saving} aria-label={text("编辑", "Edit")} title={text("编辑", "Edit")} onClick={() => edit(item)}><Pencil size={15} aria-hidden="true" /></button>
                <button className="prompt-picker-icon is-danger" type="button" disabled={saving} aria-label={text("删除", "Delete")} title={text("删除", "Delete")} onClick={() => { setError(""); setDeleting(item); }}><Trash2 size={15} aria-hidden="true" /></button>
              </div>
            </li>)}</ul>}
        </div>
      </>}
    </div>
  </dialog>;
}

function PromptEditor({ value, busy, zh, onChange, onSubmit, onCancel }: {
  value: PromptTemplate; busy: boolean; zh: boolean;
  onChange: (value: PromptTemplate) => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void; onCancel: () => void;
}): JSX.Element {
  const titleRef = useRef<HTMLInputElement>(null);
  useEffect(() => { titleRef.current?.focus(); }, []);
  const text = (chinese: string, english: string) => zh ? chinese : english;
  return <form className="prompt-picker-editor" onSubmit={onSubmit}>
    <div className="prompt-picker-scroll">
    <fieldset className="prompt-picker-fields" disabled={busy}>
      <label className="prompt-picker-field">{text("标题", "Title")}<input ref={titleRef} required maxLength={200} placeholder={text("例如：功能开发需求", "e.g. Feature implementation")} value={value.title} onChange={(event) => onChange({ ...value, title: event.target.value })} /></label>
      <label className="prompt-picker-field">{text("提示词内容", "Prompt content")}<textarea required rows={7} placeholder={text("根据 {{需求}}，完成 {{功能}}，要求：\n1. …\n2. …", "Implement {{feature}} based on {{requirements}}:\n1. …\n2. …")} value={value.content} onChange={(event) => onChange({ ...value, content: event.target.value })} /></label>
      <div className="prompt-picker-field-row">
        <label className="prompt-picker-field">{text("分类", "Category")}<input maxLength={100} placeholder={text("未分类", "Uncategorized")} value={value.category} onChange={(event) => onChange({ ...value, category: event.target.value })} /></label>
        <label className="prompt-picker-field">{text("排序", "Sort order")}<input type="number" step={1} value={value.sort_order} onChange={(event) => onChange({ ...value, sort_order: Number(event.target.value) })} /></label>
      </div>
      <label className="prompt-picker-pin"><input type="checkbox" checked={value.pinned} onChange={(event) => onChange({ ...value, pinned: event.target.checked })} /><Pin size={15} aria-hidden="true" />{text("置顶", "Pin")}</label>
    </fieldset>
    </div>
    <footer className="prompt-picker-footer">
      <button className="prompt-picker-button" type="button" disabled={busy} onClick={onCancel}>{text("取消", "Cancel")}</button>
      <button className="prompt-picker-button is-primary" type="submit" disabled={busy || !value.title.trim() || !value.content.trim()}>
        {busy ? <LoaderCircle className="prompt-picker-spinner" size={15} aria-hidden="true" /> : <Check size={16} aria-hidden="true" />}
        {busy ? text("保存中…", "Saving…") : text("保存", "Save")}
      </button>
    </footer>
  </form>;
}
