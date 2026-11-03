import { ClipboardList, RefreshCw, Settings2 } from "lucide-react";
import { useState, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { WorkOrder } from "../desktopServiceBridge";
import { WORK_ORDER_STATUS } from "./client";
import { SuperConsent } from "./SuperConsent";
import { PlatformConfigEditor } from "./PlatformConfigEditor";
import { PlatformGrant, profileGrant } from "./PlatformGrant";
import { PlatformRunCard } from "./PlatformRunCard";
import { PlatformReliability } from "./PlatformReliability";
import { PLATFORM_EVENT_TOPICS, type PlatformBridge } from "./platformClient";
import { PLATFORM_COPY } from "./platformCopy";
import { count, displayText, displayTime } from "./platformSafety";
import { useSuperPlatform } from "./useSuperPlatform";
import "./platformPanel.css";

type Props = { bridge: PlatformBridge | null; orders: WorkOrder[] };
export function PlatformPanel({ bridge, orders }: Props): JSX.Element {
  const { language: activeLanguage } = useI18n();
  const language = activeLanguage === "zh" ? "zh" : "en";
  const copy = PLATFORM_COPY[language];
  const model = useSuperPlatform(bridge);
  const [intakeID, setIntakeID] = useState("");
  const [profileID, setProfileID] = useState("");
  const [consent, setConsent] = useState<{ bridge: PlatformBridge | null; key: string }>({ bridge: null, key: "" });
  const setConfirmedKey = (key: string) => setConsent({ bridge, key });
  const [configOpen, setConfigOpen] = useState(false);
  const snapshot = model.snapshot;
  const needsSetup = snapshot && !snapshot.configured && (!snapshot.safe_code || snapshot.safe_code === "configuration_required");
  const profile = snapshot?.profiles.find((item) => item.id === profileID && item.enabled);
  const selectedOrder = orders.find((item) => item.id === intakeID && item.status === WORK_ORDER_STATUS.recorded);
  const grant = snapshot && profile ? profileGrant(snapshot, profile.id) : null;
  const confirmationKey = JSON.stringify([intakeID, profileID, snapshot?.config_json]);
  const confirmed = consent.bridge === bridge && consent.key === confirmationKey;
  const disabled = model.busy || model.pollError;
  const canStart = typeof bridge?.StartSuperRun === "function" && snapshot?.configured && snapshot.running
    && selectedOrder && profile && grant && grant.expires_at_ms > Date.now() && confirmed && !disabled;
  const apiAvailable = typeof bridge?.GetSuperPlatform === "function";
  return <section className="super-platform" aria-labelledby="super-platform-heading" aria-busy={model.busy}>
    <header className="super-platform-header"><div><h2 id="super-platform-heading">{copy.title}</h2><p>{copy.intro}</p></div>
      {apiAvailable ? <button type="button" onClick={model.refresh} disabled={model.busy}><RefreshCw aria-hidden="true" size={15} />{copy.refresh}</button> : null}
    </header>
    {!apiAvailable ? <p className="super-platform-muted">{copy.unavailableBridge}</p> : <>
      {model.loading ? <p role="status">{copy.loading}</p> : null}
      {model.pollError ? <p role="alert" className="super-error">{copy.pollError}</p> : null}
      {model.error ? <p role="alert" className="super-error">{copy[model.error]}</p> : null}
      {snapshot ? <>
        <div className="super-platform-status" data-configured={snapshot.configured}>
          <div className="super-platform-state-main"><Settings2 aria-hidden="true" size={19} /><div><strong>{snapshot.configured ? copy.configured : copy.unconfigured}</strong><small>{snapshot.configured ? copy.configuredHint : copy.setupHint}</small></div></div>
          <span className="super-badge">{snapshot.running ? copy.running : copy.stopped}</span>
          <strong className="super-platform-worker-count">{copy.workers}: {count(snapshot.workers)}</strong>
        </div>
        {needsSetup ? <div className="super-platform-onboarding" role="status"><div><strong>{copy.setupTitle}</strong><p>{copy.setupBody}</p></div><button type="button" onClick={() => setConfigOpen(true)}>{copy.setupAction}</button></div> : snapshot.safe_code ? <p role="alert" className="super-error">{snapshot.safe_code === "configuration_invalid" ? copy.configRejected : copy.dataWarning}</p> : null}
        <PlatformReliability snapshot={snapshot} copy={copy} />
        <PlatformConfigEditor configJSON={snapshot.config_json} copy={copy} busy={model.busy}
          canSave={typeof bridge?.SaveSuperPlatformConfig === "function"} onSave={model.save} expanded={configOpen} onExpandedChange={setConfigOpen} />
        <section className="super-card super-platform-submit" aria-label={copy.startTitle}>
          <div className="super-platform-section-heading"><h3>{copy.startTitle}</h3><small>{copy.manualOnly}</small></div>
          <div className="super-platform-selectors">
            <label htmlFor="super-platform-order">{copy.order}<select id="super-platform-order" disabled={disabled} value={intakeID} onChange={(event) => { setIntakeID(event.target.value); setConfirmedKey(""); }}>
              <option value="">{copy.choose}</option>{orders.filter((order) => order.status === WORK_ORDER_STATUS.recorded).map((order) => <option value={order.id} key={order.id}>{displayText(order.title)}</option>)}
            </select></label>
            <label htmlFor="super-platform-profile">{copy.profile}<select id="super-platform-profile" disabled={disabled} value={profileID} onChange={(event) => { setProfileID(event.target.value); setConfirmedKey(""); }}>
              <option value="">{copy.choose}</option>{snapshot.profiles.map((item) => <option key={item.id} value={item.id} disabled={!item.enabled}>{displayText(item.name)}{!item.enabled ? ` (${copy.disabled})` : ""}</option>)}
            </select></label>
          </div>
          {profile ? <dl className="super-platform-metrics"><div><dt>{copy.workspace}</dt><dd>{displayText(profile.workspace)}</dd></div><div><dt>{copy.model}</dt><dd>{displayText(profile.model)}</dd></div></dl> : null}
          <p className="super-platform-muted super-platform-helper">{copy.preview}</p>
          {grant ? <><dl className="super-platform-metrics super-platform-budget-preview"><div><dt>{copy.budget}</dt><dd>{count(grant.max_tokens).toLocaleString()}</dd></div><div><dt>{copy.timeout}</dt><dd>{count(grant.timeout_ms).toLocaleString()}</dd></div><div><dt>{copy.autoApprove}</dt><dd>{grant.auto_approve ? copy.enabled : copy.disabled}</dd></div></dl><details className="super-platform-scope-details"><summary>{copy.grant}</summary><PlatformGrant grant={grant} copy={copy} language={language} /></details></> : profile ? <p role="status">{copy.grantUnavailable}</p> : null}
          <div className="super-platform-submit-footer"><SuperConsent checked={confirmed} disabled={disabled || !grant || !selectedOrder} label={copy.confirmStart} onChange={(checked) => setConfirmedKey(checked ? confirmationKey : "")} />
          <button type="button" className="super-platform-start" disabled={!canStart} onClick={() => { if (canStart) void model.start(intakeID, profileID); }}>{copy.start}</button></div>
          {typeof bridge?.StartSuperRun !== "function" ? <p>{copy.startMissing}</p> : null}
        </section>
        <section aria-label={copy.runs} className="super-platform-runs"><h3>{copy.runs} <small>{snapshot.runs.length}</small></h3>
          {snapshot.has_more_runs === true ? <p role="status" className="super-platform-history">{copy.moreRuns}</p> : null}
          {snapshot.runs.length === 0 ? <div className="super-empty-state"><ClipboardList aria-hidden="true" size={22} /><p>{copy.noRuns}</p></div> : snapshot.runs.map((run) => <PlatformRunCard key={`${run.id}:${run.version}:${run.grant_hash}`} run={run} copy={copy} language={language}
            bridge={bridge!} disabled={disabled} onApprove={model.approve} onCancel={model.cancel} onRetry={model.retry} onAbandon={model.abandon} />)}
        </section>
      </> : null}
      <section className="super-card super-platform-events" aria-label={copy.timeline}><h3>{copy.timeline}</h3>
        <p className="super-platform-muted">{copy.eventHint}</p>
        {typeof bridge?.GetSuperEvents !== "function" ? <p>{copy.eventsMissing}</p> : <>
          {model.eventError ? <p role="alert" className="super-error">{copy.eventError}</p> : null}
          {model.events.length === 0 ? <p>{copy.noEvents}</p> : <ol>{model.events.map((event) => <li key={event.sequence}>
            <strong>#{count(event.sequence)} · {PLATFORM_EVENT_TOPICS.has(event.topic) ? event.topic : copy.unknownStatus}</strong>
            <span>{displayText(event.aggregate_id)} · v{count(event.version)}</span><time>{displayTime(event.occurred_at_ms, language)}</time>
          </li>)}</ol>}
        </>}
      </section>
      <p className="super-platform-muted">{copy.scopeNote}</p>
    </>}
  </section>;
}
