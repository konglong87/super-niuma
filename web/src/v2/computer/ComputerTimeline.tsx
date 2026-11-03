import type { ReactElement } from "react";
import { useI18n } from "../../lib/i18n";
import { computerUICopy, preferredComputerLanguage } from "./computerUICopy";
import type { ComputerActionReceipt } from "./types";

export function ComputerTimeline({ receipts }: { receipts: ComputerActionReceipt[] }): ReactElement {
  useI18n(); // Subscribe to the app language so a settings change rerenders this surface.
  const language = preferredComputerLanguage();
  const copy = computerUICopy[language];
  return <section className="webui2-computer-timeline" aria-label={copy.timelineAria}><div className="webui2-computer-section-heading"><div><span className="webui2-computer-eyebrow">{copy.auditTrail}</span><h3>{copy.timelineTitle}</h3></div><span className="webui2-computer-count">{receipts.length}</span></div>{receipts.length === 0 ? <p className="webui2-computer-empty">{copy.noReceipts}</p> : <ol>{receipts.slice().reverse().map((receipt) => <li key={receipt.action_id}><span className={`webui2-computer-dot webui2-computer-dot--${receipt.outcome}`} /><div><strong>{receipt.redacted_action_summary || copy.computerAction}</strong><span>{copy.verification[receipt.verification]} · {copy.focus[receipt.focus_after]}</span>{receipt.error_message ? <small role="alert">{receipt.error_message}</small> : null}</div><time>{new Date(receipt.completed_at).toLocaleTimeString(language === "zh" ? "zh-CN" : "en-US")}</time></li>)}</ol>}</section>;
}
