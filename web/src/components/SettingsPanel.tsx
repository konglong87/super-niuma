import { AlertCircle, Braces, Check, Image as ImageIcon, KeyRound, ListTree, Lock, Plus, RefreshCcw, Save, ShieldAlert, SlidersHorizontal, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { getGlobalSettings, listModels, saveGlobalSettings } from "../lib/api";
import { useI18n } from "../lib/i18n";
import type { IdentityConfig, SettingsDoc } from "../lib/types";

type Props = {
  identity: IdentityConfig;
  onStatus?: (message: string) => void;
};

type ViewMode = "form" | "json";
type EnvRow = { id: number; key: string; value: string; masked: boolean };

const SECRET_SENTINEL = "••••••";

// Must match the JSON editor CSS below so the highlight band lines up exactly.
const EDITOR_LINE_HEIGHT = 20;
const EDITOR_PAD_TOP = 16;

const FALLBACK_MODELS = [
  "claude-sonnet-4-6",
  "claude-opus-4-8",
  "claude-opus-4-7",
  "claude-sonnet-4-5",
  "claude-haiku-4-5"
];

const PERMISSION_MODES = ["default", "acceptEdits", "plan", "bypassPermissions"];

export function SettingsPanel({ identity, onStatus }: Props) {
  const { t } = useI18n();
  const rowId = useRef(1_000_000);
  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const gutterRef = useRef<HTMLDivElement | null>(null);
  const bandRef = useRef<HTMLDivElement | null>(null);

  const [doc, setDoc] = useState<SettingsDoc>({});
  const [jsonText, setJsonText] = useState("{}");
  const [view, setView] = useState<ViewMode>("form");
  const [_maskedPaths, setMaskedPaths] = useState<string[]>([]);
  const [envRows, setEnvRows] = useState<EnvRow[]>([]);
  const [permText, setPermText] = useState({ allow: "", deny: "", ask: "" });

  const [path, setPath] = useState("");
  const [exists, setExists] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const [jsonError, setJsonError] = useState("");
  const [jsonErrorLine, setJsonErrorLine] = useState<number | null>(null);
  const [modelOptions, setModelOptions] = useState<string[]>(FALLBACK_MODELS);

  function hydrateForm(nextDoc: SettingsDoc) {
    const env = asObject(nextDoc.env);
    setEnvRows(
      Object.entries(env).map(([key, value], index) => ({
        id: index,
        key,
        value: asString(value),
        masked: asString(value) === SECRET_SENTINEL
      }))
    );
    const perms = asObject(nextDoc.permissions);
    setPermText({
      allow: asStringArray(perms.allow).join("\n"),
      deny: asStringArray(perms.deny).join("\n"),
      ask: asStringArray(perms.ask).join("\n")
    });
  }

  async function reload() {
    setLoading(true);
    setError("");
    try {
      const resp = await getGlobalSettings(identity);
      const nextDoc = resp.doc ?? {};
      setPath(resp.path);
      setExists(resp.exists);
      setMaskedPaths(resp.masked ?? []);
      setDoc(nextDoc);
      setJsonText(pretty(nextDoc));
      hydrateForm(nextDoc);
      setDirty(false);
      setJsonError("");
      setJsonErrorLine(null);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  // Parse jsonText into a settings object, reporting the 1-based line of any
  // syntax error so the editor can highlight it.
  function parseDoc(text: string): { doc?: SettingsDoc; error?: string; line?: number | null } {
    try {
      const parsed = JSON.parse(text);
      if (!isPlainObject(parsed)) {
        return { error: t("settings.jsonNotObject") };
      }
      return { doc: parsed };
    } catch (err) {
      const message = errorMessage(err);
      return { error: message, line: parseErrorLine(message, text) };
    }
  }

  function positionBand() {
    const textarea = taRef.current;
    if (gutterRef.current && textarea) {
      gutterRef.current.scrollTop = textarea.scrollTop;
    }
    const band = bandRef.current;
    if (textarea && band && jsonErrorLine) {
      band.style.top = `${EDITOR_PAD_TOP + (jsonErrorLine - 1) * EDITOR_LINE_HEIGHT - textarea.scrollTop}px`;
    }
  }

  useEffect(() => {
    positionBand();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jsonErrorLine, jsonText, view]);

  useEffect(() => {
    void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  useEffect(() => {
    let cancelled = false;
    void listModels(identity)
      .then((models) => {
        if (cancelled || models.length === 0) {
          return;
        }
        setModelOptions(Array.from(new Set([...models, ...FALLBACK_MODELS])));
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity.apiBase, identity.apiToken]);

  function patchDoc(next: SettingsDoc) {
    setDoc(next);
    setDirty(true);
  }

  function setScalar(key: string, value: string) {
    patchDoc(setField(doc, key, value.trim() === "" ? undefined : value));
  }

  function setNumber(key: string, value: string) {
    const trimmed = value.trim();
    if (trimmed === "") {
      patchDoc(setField(doc, key, undefined));
      return;
    }
    const parsed = Number(trimmed);
    patchDoc(setField(doc, key, Number.isFinite(parsed) ? parsed : trimmed));
  }

  function setImageField(key: string, value: unknown) {
    const image = { ...asObject(doc.imageGeneration) };
    if (value === undefined || value === null || value === "") {
      delete image[key];
    } else {
      image[key] = value;
    }
    patchDoc(setField(doc, "imageGeneration", Object.keys(image).length === 0 ? undefined : image));
  }

  function updateEnvRow(id: number, patch: Partial<EnvRow>) {
    const rows = envRows.map((row) => (row.id === id ? { ...row, ...patch } : row));
    setEnvRows(rows);
    patchDoc(applyEnvRows(doc, rows));
  }

  function addEnvRow() {
    setEnvRows([...envRows, { id: rowId.current++, key: "", value: "", masked: false }]);
    setDirty(true);
  }

  function removeEnvRow(id: number) {
    const rows = envRows.filter((row) => row.id !== id);
    setEnvRows(rows);
    patchDoc(applyEnvRows(doc, rows));
  }

  function setPermissionMode(value: string) {
    patchDoc(setPermission(doc, "defaultMode", value.trim() === "" ? undefined : value));
  }

  function setPermissionList(field: "allow" | "deny" | "ask", value: string) {
    setPermText({ ...permText, [field]: value });
    patchDoc(setPermission(doc, field, linesToArray(value)));
  }

  function onJsonChange(value: string) {
    setJsonText(value);
    setDirty(true);
    const result = parseDoc(value);
    if (result.doc) {
      setDoc(result.doc);
      setJsonError("");
      setJsonErrorLine(null);
    } else {
      setJsonError(result.error ?? "");
      setJsonErrorLine(result.line ?? null);
    }
  }

  function switchView(next: ViewMode) {
    if (next === view) {
      return;
    }
    if (next === "json") {
      setJsonText(pretty(doc));
      setJsonError("");
      setJsonErrorLine(null);
      setView("json");
      return;
    }
    const result = parseDoc(jsonText);
    if (!result.doc) {
      setJsonError(result.error ?? "");
      setJsonErrorLine(result.line ?? null);
      return;
    }
    setDoc(result.doc);
    hydrateForm(result.doc);
    setJsonError("");
    setJsonErrorLine(null);
    setView("form");
  }

  function formatJson() {
    const result = parseDoc(jsonText);
    if (result.doc) {
      setJsonText(pretty(result.doc));
      setJsonError("");
      setJsonErrorLine(null);
    } else {
      setJsonError(result.error ?? "");
      setJsonErrorLine(result.line ?? null);
    }
  }

  async function handleSave() {
    let payload: SettingsDoc = doc;
    if (view === "json") {
      const result = parseDoc(jsonText);
      if (!result.doc) {
        setJsonError(result.error ?? "");
        setJsonErrorLine(result.line ?? null);
        return;
      }
      payload = result.doc;
    }
    setSaving(true);
    setError("");
    try {
      const resp = await saveGlobalSettings(identity, payload);
      onStatus?.(t("settings.savedTo", { path: resp.path }));
      await reload();
    } catch (err) {
      const message = errorMessage(err);
      setError(message);
      onStatus?.(message);
    } finally {
      setSaving(false);
    }
  }

  const model = asString(doc.model);
  const provider = asString(doc.provider);
  const language = asString(doc.language);
  const outputStyle = asString(doc.outputStyle);
  const contextLength = typeof doc.contextLength === "number" ? String(doc.contextLength) : "";
  const permMode = asString(asObject(doc.permissions).defaultMode);
  const imageSettings = asObject(doc.imageGeneration);
  const imageEnabled = imageSettings.enabled === true;
  const imageProvider = asString(imageSettings.provider);
  const imageModel = asString(imageSettings.model);
  const imageQuality = asString(imageSettings.quality) || "auto";
  const imageSize = asString(imageSettings.size) || "auto";
  const imageFormat = asString(imageSettings.outputFormat) || "png";
  const imageBackground = asString(imageSettings.background) || "auto";

  const modelUnknown = model !== "" && !modelOptions.includes(model);
  const permModeUnknown = permMode !== "" && !PERMISSION_MODES.includes(permMode);
  const hints = computeSchemaHints(doc, envRows, modelOptions, t);

  return (
    <section className="panel settings-panel">
      <div className="panel-header">
        <div>
          <h2>{t("settings.title")}</h2>
          <p>{t("settings.subtitle")}</p>
        </div>
        <div className="button-row">
          <button type="button" className="icon-button" onClick={() => void reload()} title={t("settings.reload")} disabled={loading || saving}>
            <RefreshCcw size={16} className={loading ? "spin" : undefined} />
          </button>
          <button type="button" className="settings-save-button" onClick={() => void handleSave()} disabled={saving || loading || (!dirty && exists)}>
            {saving ? <RefreshCcw size={15} className="spin" /> : <Save size={15} />}
            {saving ? t("settings.saving") : t("settings.save")}
          </button>
        </div>
      </div>

      <div className="settings-meta">
        <span className={exists ? "settings-path-chip is-live" : "settings-path-chip is-new"}>
          <span className="dot" />
          <code>{path || "~/.niuma/settings.json"}</code>
        </span>
        {!exists ? <span className="settings-flag">{t("settings.willCreate")}</span> : null}
        {dirty ? <span className="settings-flag is-dirty">{t("settings.unsaved")}</span> : null}
        <div className="settings-viewtabs" role="tablist" aria-label={t("settings.viewLabel")}>
          <button type="button" role="tab" aria-selected={view === "form"} className={view === "form" ? "active" : ""} onClick={() => switchView("form")}>
            <SlidersHorizontal size={14} />
            {t("settings.formView")}
          </button>
          <button type="button" role="tab" aria-selected={view === "json"} className={view === "json" ? "active" : ""} onClick={() => switchView("json")}>
            <Braces size={14} />
            {t("settings.jsonView")}
          </button>
        </div>
      </div>

      {error ? (
        <div className="settings-banner is-error">
          <AlertCircle size={15} />
          <span>{error}</span>
        </div>
      ) : null}

      {hints.length > 0 ? (
        <div className="settings-banner is-warn">
          <ShieldAlert size={15} />
          <div className="settings-hint-list">
            <strong>{t("settings.hints")}</strong>
            <ul>
              {hints.map((hint) => (
                <li key={hint}>{hint}</li>
              ))}
            </ul>
          </div>
        </div>
      ) : null}

      {view === "form" ? (
        <div className="settings-form">
          <section className="settings-card">
            <div className="settings-card-head">
              <span className="settings-card-icon"><SlidersHorizontal size={16} /></span>
              <div>
                <h3>{t("settings.model.title")}</h3>
                <p>{t("settings.model.subtitle")}</p>
              </div>
            </div>
            <div className="form-grid two">
              <label>
                {t("settings.model.model")}
                <input list="settings-model-options" className={modelUnknown ? "settings-warn-field" : undefined} value={model} onChange={(event) => setScalar("model", event.target.value)} placeholder="claude-sonnet-4-6" />
                <datalist id="settings-model-options">
                  {modelOptions.map((option) => (
                    <option key={option} value={option} />
                  ))}
                </datalist>
              </label>
              <label>
                {t("settings.model.provider")}
                <input value={provider} onChange={(event) => setScalar("provider", event.target.value)} placeholder="anthropic" />
              </label>
              <label>
                {t("settings.model.language")}
                <select value={language} onChange={(event) => setScalar("language", event.target.value)}>
                  <option value="">{t("settings.model.auto")}</option>
                  <option value="en">English</option>
                  <option value="zh">中文</option>
                </select>
              </label>
              <label>
                {t("settings.model.contextLength")}
                <input inputMode="numeric" value={contextLength} onChange={(event) => setNumber("contextLength", event.target.value)} placeholder="200000" />
              </label>
              <label className="settings-span-2">
                {t("settings.model.outputStyle")}
                <input value={outputStyle} onChange={(event) => setScalar("outputStyle", event.target.value)} placeholder="concise" />
              </label>
            </div>
          </section>

          <section className="settings-card">
            <div className="settings-card-head">
              <span className="settings-card-icon"><ImageIcon size={16} /></span>
              <div>
                <h3>{t("settings.image.title")}</h3>
                <p>{t("settings.image.subtitle")}</p>
              </div>
            </div>
            <label className="toggle-row">
              <input type="checkbox" checked={imageEnabled} onChange={(event) => setImageField("enabled", event.target.checked)} />
              <span>{t("settings.image.enabled")}</span>
            </label>
            <div className="form-grid two">
              <label>
                {t("settings.image.provider")}
                <input value={imageProvider} onChange={(event) => setImageField("provider", event.target.value)} placeholder="jiuan" />
              </label>
              <label>
                {t("settings.image.model")}
                <input value={imageModel} onChange={(event) => setImageField("model", event.target.value)} placeholder="gpt-image-2" />
              </label>
              <label>
                {t("settings.image.quality")}
                <select value={imageQuality} onChange={(event) => setImageField("quality", event.target.value)}>
                  <option value="auto">auto</option><option value="low">low</option><option value="medium">medium</option><option value="high">high</option>
                </select>
              </label>
              <label>
                {t("settings.image.size")}
                <select value={imageSize} onChange={(event) => setImageField("size", event.target.value)}>
                  <option value="auto">auto</option><option value="1024x1024">1024x1024</option><option value="1536x1024">1536x1024</option><option value="1024x1536">1024x1536</option>
                </select>
              </label>
              <label>
                {t("settings.image.format")}
                <select value={imageFormat} onChange={(event) => setImageField("outputFormat", event.target.value)}>
                  <option value="png">png</option><option value="jpeg">jpeg</option><option value="webp">webp</option>
                </select>
              </label>
              <label>
                {t("settings.image.background")}
                <select value={imageBackground} onChange={(event) => setImageField("background", event.target.value)}>
                  <option value="auto">auto</option><option value="transparent">transparent</option><option value="opaque">opaque</option>
                </select>
              </label>
            </div>
          </section>

          <section className="settings-card">
            <div className="settings-card-head">
              <span className="settings-card-icon"><KeyRound size={16} /></span>
              <div>
                <h3>{t("settings.env.title")}</h3>
                <p>{t("settings.env.subtitle")}</p>
              </div>
              <button className="secondary-button compact-action" onClick={addEnvRow} type="button">
                <Plus size={15} />
                {t("settings.env.add")}
              </button>
            </div>
            {envRows.length === 0 ? (
              <div className="empty-state compact">{t("settings.env.empty")}</div>
            ) : (
              <div className="settings-env-list">
                {envRows.map((row) => (
                  <div key={row.id} className="settings-env-row">
                    <input
                      className="settings-env-key"
                      value={row.key}
                      onChange={(event) => updateEnvRow(row.id, { key: event.target.value })}
                      placeholder="ANTHROPIC_API_KEY"
                      spellCheck={false}
                    />
                    {row.masked ? (
                      <button className="settings-env-masked" type="button" onClick={() => updateEnvRow(row.id, { masked: false, value: "" })} title={t("settings.env.replace")}>
                        <Lock size={13} />
                        <span>{t("settings.env.encrypted")}</span>
                        <em>{t("settings.env.replace")}</em>
                      </button>
                    ) : (
                      <input
                        className="settings-env-value"
                        value={row.value}
                        onChange={(event) => updateEnvRow(row.id, { value: event.target.value })}
                        placeholder={t("settings.env.valuePlaceholder")}
                        spellCheck={false}
                      />
                    )}
                    <button className="icon-button compact danger" type="button" onClick={() => removeEnvRow(row.id)} title={t("settings.env.remove")}>
                      <Trash2 size={14} />
                    </button>
                  </div>
                ))}
              </div>
            )}
            <div className="settings-hint">
              <ShieldAlert size={14} />
              <span>{t("settings.env.secretHint")}</span>
            </div>
          </section>

          <section className="settings-card">
            <div className="settings-card-head">
              <span className="settings-card-icon"><ListTree size={16} /></span>
              <div>
                <h3>{t("settings.perm.title")}</h3>
                <p>{t("settings.perm.subtitle")}</p>
              </div>
            </div>
            <label className="settings-span-2">
              {t("settings.perm.defaultMode")}
              <input list="settings-perm-modes" className={permModeUnknown ? "settings-warn-field" : undefined} value={permMode} onChange={(event) => setPermissionMode(event.target.value)} placeholder="default" />
              <datalist id="settings-perm-modes">
                {PERMISSION_MODES.map((mode) => (
                  <option key={mode} value={mode} />
                ))}
              </datalist>
            </label>
            <div className="settings-rules-grid">
              <label>
                <span className="settings-rules-label allow">{t("settings.perm.allow")}</span>
                <textarea value={permText.allow} onChange={(event) => setPermissionList("allow", event.target.value)} rows={5} placeholder={"Bash(git*)\nRead(**)"} spellCheck={false} />
              </label>
              <label>
                <span className="settings-rules-label ask">{t("settings.perm.ask")}</span>
                <textarea value={permText.ask} onChange={(event) => setPermissionList("ask", event.target.value)} rows={5} placeholder={"Bash(rm*)"} spellCheck={false} />
              </label>
              <label>
                <span className="settings-rules-label deny">{t("settings.perm.deny")}</span>
                <textarea value={permText.deny} onChange={(event) => setPermissionList("deny", event.target.value)} rows={5} placeholder={"Read(./secrets/**)"} spellCheck={false} />
              </label>
            </div>
            <div className="settings-hint">
              <Check size={14} />
              <span>{t("settings.perm.hint")}</span>
            </div>
          </section>
        </div>
      ) : (
        <div className="settings-json">
          <div className="settings-json-toolbar">
            <span>{t("settings.jsonHint")}</span>
            <button className="secondary-button compact-action" onClick={formatJson} type="button">
              <Braces size={14} />
              {t("settings.format")}
            </button>
          </div>
          <div className={jsonError ? "settings-code has-error" : "settings-code"}>
            <div className="settings-code-gutter" ref={gutterRef} aria-hidden="true">
              {jsonText
                .split("\n")
                .map((_, index) => index + 1)
                .map((lineNumber) => (
                  <div key={lineNumber} className={jsonErrorLine === lineNumber ? "is-error" : undefined}>
                    {lineNumber}
                  </div>
                ))}
            </div>
            <div className="settings-code-main">
              {jsonErrorLine ? <div className="settings-code-band" ref={bandRef} /> : null}
              <textarea
                ref={taRef}
                className="settings-json-editor"
                value={jsonText}
                onChange={(event) => onJsonChange(event.target.value)}
                onScroll={positionBand}
                spellCheck={false}
                wrap="off"
              />
            </div>
          </div>
          {jsonError ? (
            <div className="settings-banner is-error">
              <AlertCircle size={15} />
              <span>{jsonErrorLine ? t("settings.jsonErrorAtLine", { line: jsonErrorLine, message: jsonError }) : jsonError}</span>
            </div>
          ) : (
            <div className="settings-banner is-ok">
              <Check size={15} />
              <span>{t("settings.jsonValid")}</span>
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asObject(value: unknown): Record<string, unknown> {
  return isPlainObject(value) ? value : {};
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}

function pretty(doc: SettingsDoc): string {
  return JSON.stringify(doc, null, 2);
}

function linesToArray(value: string): string[] {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

function setField(doc: SettingsDoc, key: string, value: unknown): SettingsDoc {
  const next = { ...doc };
  if (value === undefined || value === null || value === "") {
    delete next[key];
  } else {
    next[key] = value;
  }
  return next;
}

function applyEnvRows(doc: SettingsDoc, rows: EnvRow[]): SettingsDoc {
  const env: Record<string, string> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (key === "") {
      continue;
    }
    env[key] = row.value;
  }
  const next = { ...doc };
  if (Object.keys(env).length === 0) {
    delete next.env;
  } else {
    next.env = env;
  }
  return next;
}

function setPermission(doc: SettingsDoc, field: string, value: unknown): SettingsDoc {
  const perms = { ...asObject(doc.permissions) };
  const isEmpty = value === undefined || value === "" || (Array.isArray(value) && value.length === 0);
  if (isEmpty) {
    delete perms[field];
  } else {
    perms[field] = value;
  }
  const next = { ...doc };
  if (Object.keys(perms).length === 0) {
    delete next.permissions;
  } else {
    next.permissions = perms;
  }
  return next;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// parseErrorLine extracts the 1-based line number from a JSON.parse error.
// Engines differ: V8 reports "(line L column C)", others only "position N".
function parseErrorLine(message: string, text: string): number | null {
  const lineMatch = message.match(/line (\d+)/i);
  if (lineMatch) {
    return Number(lineMatch[1]);
  }
  const posMatch = message.match(/position (\d+)/i);
  if (posMatch) {
    const position = Number(posMatch[1]);
    return text.slice(0, Math.max(0, position)).split("\n").length;
  }
  return null;
}

type Translate = (key: string, params?: Record<string, string | number>) => string;

// computeSchemaHints returns non-blocking warnings about values that are valid
// JSON but likely mistakes (unknown model / mode, bad number, dropped env keys).
function computeSchemaHints(doc: SettingsDoc, envRows: EnvRow[], modelOptions: string[], t: Translate): string[] {
  const hints: string[] = [];

  const model = asString(doc.model);
  if (model !== "" && !modelOptions.includes(model)) {
    hints.push(t("settings.hint.model", { model }));
  }

  const mode = asString(asObject(doc.permissions).defaultMode);
  if (mode !== "" && !PERMISSION_MODES.includes(mode)) {
    hints.push(t("settings.hint.mode", { mode, modes: PERMISSION_MODES.join(" / ") }));
  }

  if (doc.contextLength !== undefined) {
    const value = doc.contextLength;
    if (typeof value !== "number" || !Number.isInteger(value) || value <= 0) {
      hints.push(t("settings.hint.contextLength"));
    }
  }

  const language = asString(doc.language);
  if (language !== "" && !["en", "zh"].includes(language)) {
    hints.push(t("settings.hint.language", { lang: language }));
  }

  const seen = new Set<string>();
  let hasEmptyKey = false;
  for (const row of envRows) {
    const key = row.key.trim();
    if (key === "") {
      if (row.value.trim() !== "") {
        hasEmptyKey = true;
      }
      continue;
    }
    if (seen.has(key)) {
      hints.push(t("settings.hint.envDupKey", { key }));
    }
    seen.add(key);
  }
  if (hasEmptyKey) {
    hints.push(t("settings.hint.envEmptyKey"));
  }

  return hints;
}
