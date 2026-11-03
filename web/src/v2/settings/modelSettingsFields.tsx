import { useId, useState, type ReactNode } from "react";
import { Eye, EyeOff } from "lucide-react";
import { SETTINGS_SECRET_SENTINEL, settingsValue, type GlobalSettingsDraft, type SettingsPath } from "./globalSettingsDraft";
import { useGlobalSettingsText } from "./globalSettingsCopy";
import { SettingsSelect } from "./SettingsSelect";
import { BOOL_OPTIONS } from "./modelSettingsConstants";

type FieldProps = { draft: GlobalSettingsDraft; path: SettingsPath; label: string; type?: "text" | "password" | "number"; options?: readonly (readonly [string, string])[]; placeholder?: string; full?: boolean };
export function stringValue(value: unknown): string { return typeof value === "string" || typeof value === "number" ? String(value) : ""; }
export function Field({ draft, path, label, type = "text", options, placeholder, full }: FieldProps) {
  const t = useGlobalSettingsText();
  const id = useId();
  const [visible, setVisible] = useState(false);
  const value = stringValue(settingsValue(draft.doc, path));
  const choices = options && (options.some(([key]) => key === value) ? options : [...options, [value, value] as const]);
  const change = (text: string) => draft.setField(path, type === "number" ? text === "" ? undefined : Number(text) : text);
  return <label htmlFor={id} className={`global-settings-field${full ? " full" : ""}`}>
    <span>{t(label)}</span>
    {choices ? <SettingsSelect id={id} ariaLabel={t(label)} value={value} onChange={(next) => draft.setField(path, next || undefined)} options={choices.map(([key, title]) => ({ value: key, label: t(title) }))} />
      : <span className="global-settings-input-row"><input id={id} aria-label={t(label)} type={type === "password" && visible ? "text" : type} value={value} placeholder={placeholder ? t(placeholder) : undefined} autoComplete={type === "password" ? "new-password" : "off"} spellCheck={false} onChange={(event) => change(event.target.value)} />
        {type === "password" && <button type="button" className="global-settings-icon" title={t(visible ? "隐藏密钥" : "显示密钥")} aria-label={t(visible ? "隐藏密钥" : "显示密钥")} onClick={(event) => { event.preventDefault(); setVisible(!visible); }}>{visible ? <EyeOff size={16} /> : <Eye size={16} />}</button>}
      </span>}
    {type === "password" && value === SETTINGS_SECRET_SENTINEL && <small>{t("已存储，未修改时保留")}</small>}
  </label>;
}

export function BooleanField({ draft, path, label }: Pick<FieldProps, "draft" | "path" | "label">) {
  const t = useGlobalSettingsText();
  const id = useId();
  const value = settingsValue(draft.doc, path);
  return <label className="global-settings-field" htmlFor={id}><span>{t(label)}</span><SettingsSelect id={id} ariaLabel={t(label)} value={typeof value === "boolean" ? String(value) : ""} onChange={(next) => draft.setField(path, next === "" ? undefined : next === "true")} options={BOOL_OPTIONS.map(([key, title]) => ({ value: key, label: t(title) }))} /></label>;
}

export function StringListField({ draft, path, label }: Pick<FieldProps, "draft" | "path" | "label">) {
  const t = useGlobalSettingsText();
  const value = settingsValue(draft.doc, path);
  const lines = Array.isArray(value) ? value.map(stringValue).join("\n") : "";
  return <label className="global-settings-field full"><span>{t(label)}</span><textarea aria-label={t(label)} rows={3} spellCheck={false} value={lines}
    onChange={(event) => draft.setField(path, event.target.value === "" ? [] : event.target.value.split("\n"))}
    onBlur={() => { if (Array.isArray(value)) draft.setField(path, value.map(stringValue).map((item) => item.trim()).filter(Boolean)); }} /></label>;
}

export function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  const t = useGlobalSettingsText();
  return <section className="global-settings-section"><h3>{t(title)}</h3>{description && <p className="global-settings-section-description">{t(description)}</p>}{children}</section>;
}
