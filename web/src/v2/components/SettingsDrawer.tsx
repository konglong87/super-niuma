import { Archive, Copy, Share2, Square, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type ChangeEvent, type JSX, type KeyboardEvent } from "react";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { SessionRef, SessionSummary } from "../types";

type SettingsDrawerProps = {
  open: boolean;
  identity: IdentityConfig;
  selectedSession: SessionSummary | undefined;
  onClose: () => void;
  onInspectorChange: (open: boolean) => void;
  onStop: (ref: SessionRef) => void;
  onArchive: (ref: SessionRef) => void;
  inspectorOpen?: boolean;
  theme?: WebUIV2Theme;
  onThemeChange?: (theme: WebUIV2Theme) => void;
};

const themeStorageKey = "niuma-webui.v2.theme.v1";
export type WebUIV2Theme = "light" | "dark";

export function SettingsDrawer({ open, identity, selectedSession, onClose, onInspectorChange, onStop, onArchive, inspectorOpen, theme: controlledTheme, onThemeChange }: SettingsDrawerProps): JSX.Element | null {
  const { language, setLanguage, t } = useI18n();
  const [storedTheme, setStoredTheme] = useState<WebUIV2Theme>(() => loadTheme());
  const [localInspectorOpen, setLocalInspectorOpen] = useState(false);
  const [copyStatus, setCopyStatus] = useState("");
  const drawerRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const titleID = useId();

  useEffect(() => {
    if (!open) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButtonRef.current?.focus();
    return () => previousFocusRef.current?.focus();
  }, [open]);

  if (!open) return null;
  const mutable = selectedSession?.source === "tenant";
  const stoppable = mutable && ["queued", "running", "waiting_permission", "waiting_input"].includes(selectedSession?.status ?? "");
  const theme = controlledTheme ?? storedTheme;
  const inspector = inspectorOpen ?? localInspectorOpen;

  async function copyRef(): Promise<void> {
    if (!selectedSession) return;
    try {
      await navigator.clipboard.writeText(selectedSession.ref);
      setCopyStatus(t("webui2.copied"));
    } catch {
      setCopyStatus(t("webui2.copyFailed"));
    }
  }

  function changeTheme(event: ChangeEvent<HTMLSelectElement>): void {
    const nextTheme: WebUIV2Theme = event.target.value === "dark" ? "dark" : "light";
    setStoredTheme(nextTheme);
    saveWebUIV2Theme(nextTheme);
    onThemeChange?.(nextTheme);
  }

  function changeInspector(event: ChangeEvent<HTMLInputElement>): void {
    setLocalInspectorOpen(event.target.checked);
    onInspectorChange(event.target.checked);
  }

  async function shareSession(): Promise<void> {
    if (!selectedSession) return;
    try {
      if (navigator.share) await navigator.share({ title: selectedSession.title, url: webUIV2SessionURL(selectedSession.ref) });
      else await copyRef();
    } catch {
      setCopyStatus(t("webui2.copyFailed"));
    }
  }

  function handleDialogKeyDown(event: KeyboardEvent<HTMLElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = Array.from(drawerRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])') ?? []);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return <>
    <button aria-label={t("webui2.closeSettings")} className="webui2-drawer-backdrop" onClick={onClose} tabIndex={-1} type="button" />
    <aside aria-labelledby={titleID} aria-modal="true" className="webui2-settings-drawer" data-theme={theme} onKeyDown={handleDialogKeyDown} ref={drawerRef} role="dialog">
      <header><h2 id={titleID}>{t("webui2.settingsTitle")}</h2><button aria-label={t("webui2.closeSettings")} onClick={onClose} ref={closeButtonRef} title={t("webui2.closeSettings")} type="button"><X aria-hidden="true" size={18} /></button></header>
      <section><h3>{t("webui2.currentSession")}</h3>{selectedSession ? <>
        <div className="webui2-ref-copy"><code>{selectedSession.ref}</code><div><button aria-label={t("webui2.copySessionRef")} onClick={() => void copyRef()} title={t("webui2.copy")} type="button"><Copy aria-hidden="true" size={15} /></button><button aria-label={t("webui2.share")} onClick={() => void shareSession()} title={t("webui2.share")} type="button"><Share2 aria-hidden="true" size={15} /></button></div></div>
        <p>{t("webui2.profile")}: {selectedSession.profileLabel ?? t("webui2.noProfile")}</p>
      </> : <p>{t("webui2.noSessionSelected")}</p>}{copyStatus ? <p aria-live="polite">{copyStatus}</p> : null}</section>
      <section><h3>{t("webui2.workspaceSection")}</h3><ul><li>{t("webui2.workspaceAgents")}</li><li>{t("webui2.workspaceModel")}: {identity.model}</li><li>{t("webui2.workspaceSkills")}</li><li>{t("webui2.workspaceObservability")}</li><li>{t("webui2.workspaceEvaluation")}</li></ul></section>
      <section><h3>{t("webui2.interface")}</h3><label>{t("webui2.language")}<select aria-label={t("webui2.language")} onChange={(event) => setLanguage(event.target.value === "zh" ? "zh" : "en")} value={language}><option value="en">{t("webui2.languageEnglish")}</option><option value="zh">{t("webui2.languageChinese")}</option></select></label><label>{t("webui2.theme")}<select aria-label={t("webui2.theme")} onChange={changeTheme} value={theme}><option value="light">{t("webui2.themeLight")}</option><option value="dark">{t("webui2.themeDark")}</option></select></label><label className="webui2-toggle"><input aria-label={t("webui2.inspector")} checked={inspector} onChange={changeInspector} type="checkbox" />{t("webui2.inspector")}</label></section>
      <section><h3>{t("webui2.auth")}</h3><p>{identity.apiToken ? t("webui2.authTokenPresent") : t("webui2.authTokenMissing")}</p><p>{identity.mobileJwt ? t("webui2.authMobilePresent") : t("webui2.authMobileMissing")}</p></section>
      {selectedSession ? <section className="webui2-danger"><h3>{t("webui2.dangerZone")}</h3>{!mutable ? <p>{t("webui2.immutableSession")}</p> : <div>{stoppable ? <button aria-label={t("webui2.stop")} onClick={() => { if (window.confirm(t("webui2.confirm.stop"))) onStop(selectedSession.ref); }} type="button"><Square aria-hidden="true" size={15} />{t("webui2.stop")}</button> : null}<button aria-label={t("webui2.archive")} onClick={() => { if (window.confirm(t("webui2.confirm.archive"))) onArchive(selectedSession.ref); }} type="button"><Archive aria-hidden="true" size={15} />{t("webui2.archive")}</button></div>}</section> : null}
    </aside>
  </>;
}

export function loadWebUIV2Theme(): WebUIV2Theme {
  try {
    return window.localStorage.getItem(themeStorageKey) === "dark" ? "dark" : "light";
  } catch {
    return "light";
  }
}

export function saveWebUIV2Theme(theme: WebUIV2Theme): void {
  try { window.localStorage.setItem(themeStorageKey, theme); } catch { /* The current view can still use the selected theme. */ }
}

function loadTheme(): WebUIV2Theme {
  return loadWebUIV2Theme();
}

function webUIV2SessionURL(ref: SessionRef): string {
  return `${window.location.origin}/webui/v2/sessions/${encodeURIComponent(ref)}`;
}
