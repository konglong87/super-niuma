import { useEffect, useState, type JSX } from "react";
import type { PlatformCopy } from "./platformCopy";
import { configSafety, PLATFORM_LIMITS, safeConfig } from "./platformSafety";
import { SuperConsent } from "./SuperConsent";

type Props = { configJSON: string; busy: boolean; canSave: boolean; copy: PlatformCopy; activeRuns: number; onSave: (json: string) => Promise<boolean>; expanded?: boolean; onExpandedChange?: (expanded: boolean) => void };
export function PlatformConfigEditor({ configJSON, busy, canSave, copy, activeRuns, onSave, expanded, onExpandedChange }: Props): JSX.Element {
  const [draft, setDraft] = useState("");
  const [dirty, setDirty] = useState(false);
  const [saved, setSaved] = useState(false);
  // The backend revokes in-flight runs whose grant no longer matches; consent covers this exact draft.
  const [revokeConsent, setRevokeConsent] = useState<string | null>(null);
  const safe = safeConfig(configJSON);
  useEffect(() => { if (!dirty) setDraft(safe ?? ""); }, [safe, dirty]);
  const validation = draft ? configSafety(draft) : "invalid";
  const ready = !busy && canSave && dirty && validation === "safe";
  const confirmed = activeRuns === 0 || revokeConsent === draft;
  async function save() {
    if (!ready || !confirmed) return;
    if (await onSave(draft)) { setDirty(false); setSaved(true); setRevokeConsent(null); }
  }
  return <details className="super-platform-config super-card" open={expanded} onToggle={(event) => onExpandedChange?.(event.currentTarget.open)}>
    <summary><span>{copy.config}</span><small>{copy.configSummary}</small></summary>
    <div className="super-form">
      <p id="super-platform-config-hint" className="super-platform-muted">{copy.configHint}</p>
      {safe === null ? <p role="alert" className="super-error">{copy.configBlocked}</p> : null}
      <label htmlFor="super-platform-config">{copy.configLabel}</label>
      <textarea id="super-platform-config" rows={8} spellCheck={false} autoComplete="off"
        aria-describedby="super-platform-config-hint" maxLength={PLATFORM_LIMITS.configBytes}
        disabled={busy || !canSave} value={draft} onChange={(event) => { setDraft(event.target.value); setDirty(true); setSaved(false); }} />
      {dirty && validation !== "safe" ? <p role="alert" className="super-error">{validation === "invalid" ? copy.configInvalid : copy.configSensitive}</p> : null}
      {!canSave ? <p>{copy.configMissing}</p> : null}
      {dirty && activeRuns > 0 ? <>
        <p role="alert" className="super-error">{copy.configActiveRuns.replace("{count}", String(activeRuns))}</p>
        <SuperConsent checked={revokeConsent === draft} disabled={!ready} label={copy.configActiveRunsConfirm} onChange={(checked) => setRevokeConsent(checked ? draft : null)} />
      </> : null}
      <div className="super-actions"><button type="button" disabled={!ready || !confirmed} onClick={() => void save()}>{busy ? copy.saving : copy.save}</button></div>
      {saved ? <p role="status">{copy.saved}</p> : null}
    </div>
  </details>;
}
