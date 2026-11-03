import { Check, ImagePlus, Link, RotateCcw, Save, ShieldCheck, Trash2, Upload } from "lucide-react";
import { useRef, type ChangeEvent, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import { settingsValue, type GlobalSettingsDraft, type SettingsPath } from "./globalSettingsDraft";
import { APPEARANCE_ROOT, APPEARANCE_FIELD_LABELS, DEFAULT_APPEARANCE } from "./globalVisualSettings";
import type { DesktopBackgroundImage, DesktopBackgroundMode } from "../desktopServiceBridge";

type DesktopBackgroundControls = {
  available: boolean;
  background: DesktopBackgroundImage;
  busy: boolean;
  error: string;
  save: (dataURL: string, name: string) => Promise<DesktopBackgroundImage | null>;
  setMode: (mode: DesktopBackgroundMode) => Promise<DesktopBackgroundImage | null>;
  clear: () => Promise<boolean>;
};

type Props = { draft: GlobalSettingsDraft; desktopBackground: DesktopBackgroundControls };

const paths = {
  enabled: [...APPEARANCE_ROOT, "enabled"] as const,
  backgroundColor: [...APPEARANCE_ROOT, "backgroundColor"] as const,
  backgroundImage: [...APPEARANCE_ROOT, "backgroundImage"] as const,
  overlayOpacity: [...APPEARANCE_ROOT, "overlayOpacity"] as const,
  sidebarSurfaceOpacity: [...APPEARANCE_ROOT, "sidebarSurfaceOpacity"] as const,
  emptyBlur: [...APPEARANCE_ROOT, "emptyBlur"] as const,
  conversationBlur: [...APPEARANCE_ROOT, "conversationBlur"] as const,
  composerBlur: [...APPEARANCE_ROOT, "composerBlur"] as const,
  bubbleOpacity: [...APPEARANCE_ROOT, "bubbleOpacity"] as const
} as const;

function label(language: "en" | "zh", value: readonly [string, string]): string {
  return value[language === "zh" ? 0 : 1];
}

function toggleValue(value: unknown): boolean {
  return value === true;
}

function numberValue(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function Field({ draft, path, name, type = "text", min, max, step, fallback, placeholder, percentage = false }: { draft: GlobalSettingsDraft; path: SettingsPath; name: readonly [string, string]; type?: "text" | "color" | "range" | "number"; min?: number; max?: number; step?: number; fallback?: number; placeholder?: string; percentage?: boolean }): JSX.Element {
  const { language } = useI18n();
  const current = settingsValue(draft.doc, path);
  const text = typeof current === "string" ? current : "";
  const value = type === "range" || type === "number" ? numberValue(current, fallback ?? min ?? 0) : text;
  return <label className={`visual-settings-field${type === "range" ? " visual-settings-range" : ""}`}>
    <span>{label(language, name)}</span>
    <input aria-label={label(language, name)} type={type} value={value} min={min} max={max} step={step} placeholder={placeholder} onChange={(event) => draft.setField(path, type === "range" || type === "number" ? Number(event.target.value) : event.target.value)} />
    {type === "range" ? <output>{typeof value === "number" ? (percentage ? Math.round(value * 100) : value) : 0}{percentage ? "%" : " px"}</output> : null}
  </label>;
}

function Toggle({ draft, path, name }: { draft: GlobalSettingsDraft; path: SettingsPath; name: readonly [string, string] }): JSX.Element {
  const { language } = useI18n();
  const text = label(language, name);
  return <label className="visual-settings-toggle"><span>{text}</span><input aria-label={text} role="switch" type="checkbox" checked={toggleValue(settingsValue(draft.doc, path))} onChange={(event) => draft.setField(path, event.target.checked)} /></label>;
}

export function AppearanceSettingsPanel({ draft, desktopBackground }: Props): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const disabled = draft.busy || !draft.doc || Boolean(draft.syntaxError);
  const fileInput = useRef<HTMLInputElement>(null);
  const reset = (): void => {
    if (window.confirm(zh ? "放弃未保存的外观修改？" : "Discard unsaved appearance changes?")) draft.reset();
  };
  const backgroundMode = desktopBackground.available && desktopBackground.background.mode === "local" ? "local" : "external";
  const onFileChange = async (event: ChangeEvent<HTMLInputElement>): Promise<void> => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    const dataURL = await readFileAsDataURL(file);
    await desktopBackground.save(dataURL, file.name);
  };
  const selectLocalBackground = (): void => {
    if (desktopBackground.background.data_url) {
      void desktopBackground.setMode("local");
      return;
    }
    fileInput.current?.click();
  };
  return <div className="visual-settings-panel">
    <section className="visual-settings-section">
      <input ref={fileInput} type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden onChange={(event) => void onFileChange(event)} />
      <div className="visual-settings-section-heading"><div><h2>{zh ? "页面氛围" : "Page atmosphere"}</h2><p>{zh ? "这些值通过共享全局 settings 文档保存，编辑时会在当前页面预览。" : "These values are saved in the shared global settings document and previewed on this page while editing."}</p></div><Toggle draft={draft} path={paths.enabled} name={APPEARANCE_FIELD_LABELS.enabled} /></div>
      <fieldset className="visual-settings-grid" disabled={disabled}>
        <Field draft={draft} path={paths.backgroundColor} name={APPEARANCE_FIELD_LABELS.backgroundColor} type="color" />
        {desktopBackground.available ? <div className="visual-settings-field visual-settings-background-source">
          <span>{zh ? "背景图片来源" : "Background source"}</span>
          <div className="visual-settings-source-options" role="group" aria-label={zh ? "背景图片来源" : "Background source"}>
            <button type="button" aria-pressed={backgroundMode === "external"} disabled={disabled || desktopBackground.busy} onClick={() => void desktopBackground.setMode("external")}><Link size={15} />{zh ? "外部链接" : "External URL"}</button>
            <button type="button" aria-pressed={backgroundMode === "local"} disabled={disabled || desktopBackground.busy} onClick={selectLocalBackground}><ImagePlus size={15} />{zh ? "本地图片" : "Local image"}</button>
          </div>
        </div> : <Field draft={draft} path={paths.backgroundImage} name={APPEARANCE_FIELD_LABELS.backgroundImage} placeholder="https://..." />}
        {desktopBackground.available && backgroundMode === "external" ? <Field draft={draft} path={paths.backgroundImage} name={APPEARANCE_FIELD_LABELS.backgroundImage} placeholder="https://..." /> : null}
        {desktopBackground.available && backgroundMode === "local" ? <div className="visual-settings-field visual-settings-local-background">
          <span>{zh ? "本地背景图片" : "Local background image"}</span>
          {desktopBackground.background.data_url ? <div className="visual-settings-local-background-card">
            <img src={desktopBackground.background.data_url} alt={desktopBackground.background.name || (zh ? "当前本地背景" : "Current local background")} />
            <div><strong>{desktopBackground.background.name || (zh ? "本地图片" : "Local image")}</strong><small>{zh ? "图片已保存到桌面端本地目录" : "Stored in the desktop app data directory"}</small></div>
          </div> : <div className="visual-settings-local-background-empty">{zh ? "尚未选择本地图片" : "No local image selected"}</div>}
          <div className="visual-settings-local-background-actions">
            <button type="button" disabled={disabled || desktopBackground.busy} onClick={() => fileInput.current?.click()}><Upload size={15} />{desktopBackground.background.data_url ? (zh ? "替换图片" : "Replace image") : (zh ? "选择图片" : "Choose image")}</button>
            {desktopBackground.background.data_url ? <button type="button" disabled={disabled || desktopBackground.busy} onClick={() => void desktopBackground.clear()}><Trash2 size={15} />{zh ? "移除" : "Remove"}</button> : null}
          </div>
        </div> : null}
        <Field draft={draft} path={paths.overlayOpacity} name={APPEARANCE_FIELD_LABELS.overlayOpacity} type="range" min={0} max={1} step={0.05} fallback={DEFAULT_APPEARANCE.overlayOpacity} percentage />
        <Field draft={draft} path={paths.sidebarSurfaceOpacity} name={APPEARANCE_FIELD_LABELS.sidebarSurfaceOpacity} type="range" min={0} max={1} step={0.01} fallback={DEFAULT_APPEARANCE.sidebarSurfaceOpacity} percentage />
        <Field draft={draft} path={paths.bubbleOpacity} name={APPEARANCE_FIELD_LABELS.bubbleOpacity} type="range" min={0.2} max={1} step={0.05} fallback={DEFAULT_APPEARANCE.bubbleOpacity} percentage />
      </fieldset>
    </section>
    <section className="visual-settings-section">
      <div className="visual-settings-section-heading"><div><h2>{zh ? "层次与清晰度" : "Layers and clarity"}</h2><p>{zh ? "只对对应的内容层应用模糊，不会把整页变成不可读的滤镜。" : "Blur is scoped to the corresponding surface so the whole page remains readable."}</p></div></div>
      <fieldset className="visual-settings-grid" disabled={disabled}>
        <Field draft={draft} path={paths.emptyBlur} name={APPEARANCE_FIELD_LABELS.emptyBlur} type="range" min={0} max={24} step={1} fallback={DEFAULT_APPEARANCE.emptyBlur} />
        <Field draft={draft} path={paths.conversationBlur} name={APPEARANCE_FIELD_LABELS.conversationBlur} type="range" min={0} max={24} step={1} fallback={DEFAULT_APPEARANCE.conversationBlur} />
        <Field draft={draft} path={paths.composerBlur} name={APPEARANCE_FIELD_LABELS.composerBlur} type="range" min={0} max={24} step={1} fallback={DEFAULT_APPEARANCE.composerBlur} />
      </fieldset>
    </section>
    {draft.error ? <p className="visual-settings-error" role="alert">{draft.error}</p> : null}
    {desktopBackground.error ? <p className="visual-settings-error" role="alert">{desktopBackground.error}</p> : null}
    {draft.validation && !draft.validation.valid ? <ul className="visual-settings-error" role="alert">{draft.validation.issues.map((issue) => <li key={`${issue.field}-${issue.code}`}>{issue.field}: {issue.message}</li>)}</ul> : null}
    {draft.saved ? <p className="visual-settings-success" role="status"><Check size={15} />{zh ? "已保存并完成读回确认。" : "Saved and confirmed by readback."}</p> : null}
    <footer className="visual-settings-footer"><span>{draft.dirty ? (zh ? "有未保存的修改" : "Unsaved changes") : (zh ? "与已保存配置一致" : "Matches saved configuration")}</span><div><button type="button" disabled={!draft.dirty || draft.busy} onClick={reset}><RotateCcw size={15} />{zh ? "重置" : "Reset"}</button><button type="button" disabled={disabled} onClick={() => void draft.validate()}><ShieldCheck size={15} />{zh ? "校验" : "Validate"}</button><button type="button" className="primary" disabled={!draft.dirty || disabled || draft.conflict} onClick={() => void draft.save()}><Save size={15} />{draft.operation === "save" ? (zh ? "保存中…" : "Saving…") : (zh ? "保存更改" : "Save changes")}</button></div></footer>
  </div>;
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => resolve(typeof reader.result === "string" ? reader.result : ""));
    reader.addEventListener("error", () => reject(reader.error ?? new Error("Unable to read the selected image.")));
    reader.readAsDataURL(file);
  });
}
