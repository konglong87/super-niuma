import type { ReactElement } from "react";
import { useI18n } from "../../lib/i18n";
import { computerReadinessMessage, computerUICopy, preferredComputerLanguage } from "./computerUICopy";
import type { ComputerCapabilities } from "./types";

type Props = {
  capabilities: ComputerCapabilities | null;
  conversationRef?: string | null;
  available: boolean;
  busy: boolean;
  onApprove: () => void;
  onCancel: () => void;
};

export function ComputerApprovalDialog({ capabilities, conversationRef = null, available, busy, onApprove, onCancel }: Props): ReactElement {
  useI18n(); // Subscribe to the app language so a settings change rerenders this surface.
  const language = preferredComputerLanguage();
  const copy = computerUICopy[language];
  const readiness = computerReadinessMessage(available, capabilities, language);
  return <div className="webui2-computer-modal-backdrop" role="presentation">
    <section className="webui2-computer-modal" role="dialog" aria-modal="true" aria-labelledby="webui2-computer-approval-title">
      <span className="webui2-computer-eyebrow">{copy.sessionApproval}</span>
      <h2 id="webui2-computer-approval-title">{conversationRef ? copy.approvalTitle : copy.localPreviewTitle}</h2>
      <p>{conversationRef ? copy.conversationApprovalDescription : copy.localPreviewDescription}</p>
      <p>{copy.approvalDescription}</p>
      {conversationRef ? <p>{copy.integrationNotice}</p> : null}
      <dl>
        <div><dt>{copy.conversationRef}</dt><dd style={{ overflowWrap: "anywhere" }}>{conversationRef ? <code>{conversationRef}</code> : copy.localPreview}</dd></div>
        <div><dt>{copy.platform}</dt><dd>{capabilities?.platform || copy.unknown} · {capabilities?.backend || copy.unknownBackend}</dd></div>
        <div><dt>{copy.capture}</dt><dd>{capabilities?.capture_readiness ? copy.readiness[capabilities.capture_readiness] : copy.unknown}</dd></div>
        <div><dt>{copy.input}</dt><dd>{capabilities?.input_readiness ? copy.readiness[capabilities.input_readiness] : copy.unknown}</dd></div>
        <div><dt>{copy.target}</dt><dd>{capabilities?.target_window?.title || copy.currentDesktop}</dd></div>
      </dl>
      {readiness ? <p role="status">{readiness}</p> : null}
      <div className="webui2-computer-modal-actions">
        <button className="webui2-computer-button webui2-computer-button--quiet" disabled={busy} onClick={onCancel} type="button">{copy.cancel}</button>
        <button className="webui2-computer-button" disabled={busy || Boolean(readiness)} onClick={onApprove} type="button">{busy ? copy.starting : copy.approveSession}</button>
      </div>
    </section>
  </div>;
}
