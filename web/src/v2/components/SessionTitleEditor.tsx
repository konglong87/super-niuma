import { Check, LoaderCircle, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useI18n } from "../../lib/i18n";

type Props = {
  title: string;
  onSave: (title: string) => Promise<boolean>;
  onClose: () => void;
};

export function SessionTitleEditor({ title, onSave, onClose }: Props) {
  const { t } = useI18n();
  const inputRef = useRef<HTMLInputElement>(null);
  const savingRef = useRef(false);
  const [draft, setDraft] = useState(title);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const errorID = useId();

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  async function save(): Promise<void> {
    if (savingRef.current) return;
    const nextTitle = draft.trim();
    if (!nextTitle) {
      setError(t("webui2.error.title_required"));
      inputRef.current?.focus();
      return;
    }
    if (nextTitle === title) {
      onClose();
      return;
    }
    savingRef.current = true;
    setSaving(true);
    setError("");
    const saved = await onSave(nextTitle).catch(() => false);
    savingRef.current = false;
    setSaving(false);
    if (saved) onClose();
    else {
      setError(t("webui2.renameSessionFailed"));
      inputRef.current?.focus();
    }
  }

  return <div className="webui2-session-editing" aria-busy={saving}
    onBlur={(event) => {
      if (!savingRef.current && !event.currentTarget.contains(event.relatedTarget as Node | null)) onClose();
    }}
    onKeyDown={(event) => {
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        if (!savingRef.current) onClose();
      }
    }}>
    <input aria-label={t("webui2.renameSession")} aria-invalid={Boolean(error)} aria-describedby={error ? errorID : undefined}
      className="webui2-session-title-input" readOnly={saving} ref={inputRef} value={draft}
      onChange={(event) => { setDraft(event.target.value); setError(""); }}
      onKeyDown={(event) => {
        if (event.key !== "Enter" || event.nativeEvent.isComposing || event.keyCode === 229) return;
        event.preventDefault();
        event.stopPropagation();
        void save();
      }} />
    <button aria-label={t("settings.save")} title={t("settings.save")} type="button" disabled={saving}
      // WebKit can blur the input without focusing a clicked button.
      onMouseDown={(event) => event.preventDefault()}
      onClick={() => void save()}>{saving ? <LoaderCircle aria-hidden="true" size={14} /> : <Check aria-hidden="true" size={14} />}</button>
    <button aria-label={t("webui2.cancel")} title={t("webui2.cancel")} type="button" disabled={saving}
      onMouseDown={(event) => event.preventDefault()} onClick={onClose}><X aria-hidden="true" size={14} /></button>
    {error ? <span className="webui2-session-rename-error" id={errorID} role="alert">{error}</span> : null}
  </div>;
}
