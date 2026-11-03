import type { ReactElement } from "react";
import { useI18n } from "../../lib/i18n";
import { computerUICopy, preferredComputerLanguage } from "./computerUICopy";
import type { ComputerCapabilities, ComputerObservation } from "./types";

const CONTROLLER_WINDOW_BUNDLE_ID = "com.wails.niuma";
const CONTROLLER_WINDOW_TITLE = "niuma";

function isControllerWindowObservation(observation: ComputerObservation | null): boolean {
  const activeWindow = observation?.active_window;
  return activeWindow?.bundle_id === CONTROLLER_WINDOW_BUNDLE_ID || activeWindow?.title === CONTROLLER_WINDOW_TITLE;
}

export function ComputerPreview({ observation, capabilities }: { observation: ComputerObservation | null; capabilities: ComputerCapabilities | null }): ReactElement {
  useI18n(); // Subscribe to the app language so a settings change rerenders this surface.
  const language = preferredComputerLanguage();
  const copy = computerUICopy[language];
  const screenshot = observation?.screenshot;
  const controllerPreviewHidden = isControllerWindowObservation(observation);
  const source = controllerPreviewHidden ? undefined : observation?.image_data ? `data:${observation.media_type || "image/png"};base64,${observation.image_data}` : screenshot?.url;
  return <section className="webui2-computer-preview" aria-label={copy.previewAria}>
    <div className="webui2-computer-section-heading"><div><span className="webui2-computer-eyebrow">{copy.liveDesktop}</span><h3>{copy.previewTitle}</h3></div><span className={`webui2-computer-status webui2-computer-status--${capabilities?.capture_readiness ?? "unknown"}`}>{capabilities?.capture_readiness ? copy.readiness[capabilities.capture_readiness] : copy.notReady}</span></div>
    {controllerPreviewHidden ? <div className="webui2-computer-preview-empty"><strong>{copy.controllerPreviewHidden}</strong><span>{copy.controllerPreviewHiddenDescription}</span></div> : source ? <img className="webui2-computer-screenshot" src={source} alt={`${copy.observationAlt} ${observation?.id ?? ""}`} /> : <div className="webui2-computer-preview-empty"><strong>{observation ? copy.screenshotReference : copy.noObservation}</strong><span>{observation ? screenshot?.id || copy.noDisplayURL : copy.startToCapture}</span></div>}
    {observation ? <div className="webui2-computer-preview-meta"><span>{observation.width} × {observation.height}</span><span>{observation.active_window?.title || capabilities?.target_window?.title || copy.desktop}</span><span>{new Date(observation.observed_at).toLocaleTimeString(language === "zh" ? "zh-CN" : "en-US")}</span></div> : null}
  </section>;
}
