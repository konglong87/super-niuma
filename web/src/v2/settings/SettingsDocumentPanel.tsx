import { Braces, Check, RotateCcw, Save, ShieldCheck } from "lucide-react";
import type { GlobalSettingsDraft } from "./globalSettingsDraft";
import { useGlobalSettingsText } from "./globalSettingsCopy";
import { ModelSettingsPanel } from "./ModelSettingsPanel";
import "./globalSettings.css";
import "./modelSettings.css";

type Props = { draft: GlobalSettingsDraft; view: "models" | "json" };

export function SettingsDocumentPanel({ draft, view }: Props) {
  const t = useGlobalSettingsText();
  const reset = () => { if (window.confirm(t("放弃未保存的修改，恢复到上次读取的配置？"))) draft.reset(); };
  const reload = () => { if (!draft.dirty || window.confirm(t("重新载入会放弃当前草稿，确定继续？"))) void draft.reload(); };
  const validationStatus = draft.operation === "validate"
    ? t("校验中…")
    : draft.validation?.valid
      ? t("配置校验通过")
      : draft.validation
        ? t("配置校验未通过")
        : "";
  return <div className={`global-settings-panel${view === "models" ? " model-settings-panel" : ""}`}>
    {draft.loading && <p role="status">{t("正在读取全局设置…")}</p>}
    {draft.error && <div className="global-settings-error" role="alert"><span>{t(draft.error)}</span><button type="button" disabled={draft.busy} onClick={reload}><RotateCcw size={15} />{t(draft.conflict ? "重新载入" : "重试读取")}</button></div>}
    {draft.loaded && <>
      {draft.syntaxError && <p className="global-settings-error" role="alert">{t(draft.syntaxError)}{view === "models" ? ` ${t("请在全局 Settings JSON 中修正后继续。")}` : ""}</p>}
      {view === "models" ? <ModelSettingsPanel draft={draft} /> : <div className="global-settings-json"><div className="global-settings-json-toolbar"><span><Braces size={16} />settings.json</span><button type="button" className="global-settings-icon" title={t("格式化 JSON")} aria-label={t("格式化 JSON")} disabled={draft.busy || !draft.doc} onClick={() => draft.doc && draft.setRaw(JSON.stringify(draft.doc, null, 2))}><Braces size={16} /></button></div><textarea aria-label={t("全局 Settings JSON")} spellCheck={false} autoCapitalize="off" autoComplete="off" value={draft.raw} disabled={draft.busy} onChange={(event) => draft.setRaw(event.target.value)} /><div className="global-settings-json-meta"><span>{draft.raw.split("\n").length} {t("行")}</span><span>JSON · UTF-8</span></div></div>}
      {draft.validation && <div className={draft.validation.valid ? "global-settings-success" : "global-settings-error"} role="status" aria-live="polite">{draft.validation.valid ? <><Check size={16} />{t("配置校验通过")}</> : <ul>{draft.validation.issues.map((issue) => <li key={`${issue.field}-${issue.code}-${issue.message}`}><code>{issue.field || "settings"}</code>: {issue.message}</li>)}</ul>}</div>}
      {draft.saved && <p className="global-settings-success" role="status"><Check size={16} />{t("已保存并读取确认。运行中的服务需重启以加载新配置。")}</p>}
      <footer className="global-settings-footer"><span className="global-settings-footer-status" role="status" aria-live="polite">{validationStatus || t(draft.dirty ? "有未保存的修改" : "与上次读取的配置一致")}</span><div><button type="button" title={t("重置草稿")} disabled={!draft.dirty || draft.busy} onClick={reset}><RotateCcw size={15} />{t("重置")}</button><button type="button" aria-busy={draft.operation === "validate"} disabled={draft.busy || !draft.doc} onClick={() => void draft.validate()}><ShieldCheck size={15} />{t(draft.operation === "validate" ? "校验中…" : "校验")}</button><button type="button" className="primary" disabled={!draft.dirty || draft.busy || !draft.doc || draft.conflict} onClick={() => void draft.save()}><Save size={15} />{t(draft.operation === "save" ? "保存中…" : "保存更改")}</button></div></footer>
    </>}
  </div>;
}
