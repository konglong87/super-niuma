import { useState, type JSX } from "react";
import type { ControlRun } from "../desktopServiceBridge";
import { CONTROL_STATUS, DELIVERY_STATUS, type PlatformBridge } from "./platformClient";
import { statusLabel, type PlatformCopy } from "./platformCopy";
import { platformRunMessage } from "./platformRunMessage";
import { SuperConsent } from "./SuperConsent";
import { PlatformGrant } from "./PlatformGrant";
import { PLATFORM_LIMITS, count, displayHash, displayText, displayTime } from "./platformSafety";

const CANCELLABLE = new Set<string>([CONTROL_STATUS.queued, CONTROL_STATUS.running, CONTROL_STATUS.verifying, CONTROL_STATUS.awaitingApproval, CONTROL_STATUS.deliveryPending]);
type Props = {
  run: ControlRun; copy: PlatformCopy; language: "zh" | "en"; bridge: PlatformBridge; disabled: boolean;
  onApprove: (run: ControlRun) => Promise<boolean>; onCancel: (run: ControlRun) => Promise<boolean>; onRetry: (run: ControlRun) => Promise<boolean>; onAbandon?: (run: ControlRun) => Promise<boolean>;
};
export function PlatformRunCard({ run, copy, language, bridge, disabled, onApprove, onCancel, onRetry, onAbandon }: Props): JSX.Element {
  const [approvalConsent, setApprovalConsent] = useState<PlatformBridge | null>(null);
  const confirmed = approvalConsent === bridge;
  const [retryConsent, setRetryConsent] = useState<PlatformBridge | null>(null);
  const retryConfirmed = retryConsent === bridge;
  const [abandonConsent, setAbandonConsent] = useState<PlatformBridge | null>(null);
  const abandonConfirmed = abandonConsent === bridge;
  const jobs = run.jobs ?? [];
  const notices = run.notices ?? [];
  const reservations = run.reservations ?? [];
  const unknown = run.status === CONTROL_STATUS.needsAttention || jobs.some((job) => job.status === CONTROL_STATUS.unknown) || notices.some((notice) => notice.status === DELIVERY_STATUS.unknown);
  const expired = run.grant.expires_at_ms <= Date.now();
  const canRetry = run.status === CONTROL_STATUS.failed && !unknown && !expired && jobs.every((job) => job.status !== CONTROL_STATUS.running && job.attempts < PLATFORM_LIMITS.maxAttempts);
  const consumed = reservations.filter((item) => item.settled).reduce((total, item) => total + count(item.actual?.input_tokens) + count(item.actual?.output_tokens), 0);
  const reserved = reservations.filter((item) => !item.settled).reduce((total, item) => total + count(item.input) + count(item.output), 0);
  const report = run.verification;
  const message = platformRunMessage(run, copy);
  const label = (value: string) => statusLabel(value, language);
  return <article className="super-card super-platform-run" aria-label={displayText(run.id)}>
    <header className="super-platform-run-header"><h3>{displayText(run.id)} <small>v{count(run.version)}</small></h3>
      <span className={`super-badge${unknown ? " super-platform-warning" : ""}`}>{label(run.status)}</span></header>
    <dl className="super-platform-run-source" aria-label={copy.runSource}>
      <div><dt>{copy.runScope}</dt><dd>{displayText(run.scope)}</dd></div>
      <div><dt>{copy.runOwner}</dt><dd>{displayText(run.owner)}</dd></div>
    </dl>
    <p>{run.approved ? copy.approved : copy.notApproved} · {displayTime(run.updated_at_ms, language)}</p>
    {message ? <p className={`super-platform-run-message ${message.tone === "info" ? "super-platform-muted" : "super-error"}`} role={message.tone === "info" ? "status" : "alert"}>{message.text}</p> : null}
    {unknown ? <p className="super-error" role="alert">{copy.unknownRun}</p> : null}
    <details open={run.status === CONTROL_STATUS.awaitingApproval}>
      <summary>{copy.grant}</summary>
      <p className="super-platform-hash">{copy.grantHash}: <code>{displayHash(run.grant_hash)}</code></p>
      <PlatformGrant grant={run.grant} copy={copy} language={language} />
    </details>
    <dl className="super-platform-metrics">
      <div><dt>{copy.budget}</dt><dd>{count(run.grant.max_tokens).toLocaleString()}</dd></div>
      <div><dt>{copy.used}</dt><dd>{consumed.toLocaleString()}</dd></div>
      <div><dt>{copy.reserved}</dt><dd>{reserved.toLocaleString()}</dd></div>
    </dl>
    {reserved > 0 ? <p className="super-platform-muted">{copy.unknownUsage}</p> : null}
    <h4>{copy.nodes}</h4>
    <ul className="super-platform-jobs">{jobs.map((job) => <li key={job.spec.id}>
      <strong>{displayText(job.spec.id)}</strong> <span className="super-badge">{label(job.status)}</span>
      <small>{copy.attempts}: {count(job.attempts)} · {copy.parent}: {displayText(job.spec.parent_id)} · {copy.depends}: {(job.spec.depends_on ?? []).map(displayText).join(", ") || copy.noPaths}</small>
    </li>)}</ul>
    <section aria-label={copy.verification}>
      <h4>{copy.verification}</h4>
      {!report ? <p>{copy.notVerified}</p> : <>
        <p className={report.passed ? "" : "super-error"}>{report.passed ? copy.passed : copy.failed} · {displayTime(report.verified_at_ms, language)}</p>
        <p className="super-platform-hash">{copy.policyHash}: <code>{displayHash(report.policy_hash)}</code></p>
        {(report.artifacts ?? []).length === 0 ? <p>{copy.noArtifacts}</p> : <ul>{report.artifacts.map((artifact) => <li key={`${artifact.path}:${artifact.sha256}`}>
          <strong>{displayText(artifact.path)}</strong> · {count(artifact.bytes)} {copy.artifactBytes}
          <p className="super-platform-hash">{copy.artifactHash}: <code>{displayHash(artifact.sha256)}</code></p>
        </li>)}</ul>}
        <h5>{copy.checks}</h5><ul>{(report.checks ?? []).map((check) => <li key={`${check.path}:${check.kind}`}>{displayText(check.path)} · {displayText(check.kind)} · {check.passed ? copy.passed : copy.failed}</li>)}</ul>
      </>}
    </section>
    <section aria-label={copy.delivery}>
      <h4>{copy.delivery}</h4>
      {notices.length === 0 ? <p>{copy.noNotices}</p> : <ul className="super-platform-notices">{notices.map((notice) => <li key={notice.id}>
        <strong>{displayText(notice.target.id)}</strong> · {displayText(notice.target.provider)} · {label(notice.status)}
        {notice.status === DELIVERY_STATUS.unknown ? <p role="alert" className="super-error">{copy.deliveryUnknown}</p> : null}
        {notice.receipt ? <dl><dt>{copy.receipt}</dt><dd>{displayText(notice.receipt.message_id)}</dd><dt>{copy.deliveredAt}</dt><dd>{displayTime(notice.receipt.delivered_at_ms, language)}</dd></dl> : null}
      </li>)}</ul>}
    </section>
    {expired ? <p className="super-platform-muted">{copy.stale}</p> : null}
    {run.status === CONTROL_STATUS.awaitingApproval && typeof bridge.ApproveSuperRun === "function" ? <div className="super-platform-confirm">
      <SuperConsent checked={confirmed} disabled={disabled || expired} label={copy.approveConfirm} onChange={(checked) => setApprovalConsent(checked ? bridge : null)} />
      <button type="button" disabled={disabled || expired || !confirmed || displayHash(run.grant_hash) === "—"} onClick={() => void onApprove(run)}>{copy.approve}</button>
    </div> : null}
    {canRetry && typeof bridge.RetrySuperRun === "function" ? <div className="super-platform-confirm">
      <SuperConsent checked={retryConfirmed} disabled={disabled} label={copy.retryConfirm} onChange={(checked) => setRetryConsent(checked ? bridge : null)} />
      <button type="button" disabled={disabled || !retryConfirmed} onClick={() => void onRetry(run)}>{copy.retry}</button>
    </div> : null}
    {run.status === CONTROL_STATUS.needsAttention && typeof bridge.AbandonSuperRun === "function" && onAbandon ? <details className="super-platform-manual">
      <summary>{copy.manualIntervention}</summary>
      <p className="super-platform-muted">{copy.abandonRisk}</p>
      <div className="super-platform-confirm"><SuperConsent checked={abandonConfirmed} disabled={disabled} label={copy.abandonConfirm} onChange={(checked) => setAbandonConsent(checked ? bridge : null)} />
        <button type="button" className="super-platform-abandon" disabled={disabled || !abandonConfirmed} onClick={() => { if (abandonConfirmed) void onAbandon(run); }}>{copy.abandon}</button>
      </div>
    </details> : null}
    {CANCELLABLE.has(run.status) && typeof bridge.CancelSuperRun === "function" ? <button type="button" disabled={disabled} onClick={() => void onCancel(run)}>{copy.cancel}</button> : null}
  </article>;
}
