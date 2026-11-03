import "./ComputerPermissionGuide.css";
import { useState, type ReactElement } from "react";
import { useI18n } from "../../lib/i18n";
import { computerUICopy, preferredComputerLanguage } from "./computerUICopy";
import type { ComputerClient } from "./client";
import { computerPermissionGuideActions } from "./readiness";
import type { ComputerPermissionTarget, ComputerCapabilities } from "./types";

type Props = {
  capabilities: ComputerCapabilities | null;
  available: boolean;
  client: Pick<ComputerClient, "openPermissionSettings">;
  onRecheck: () => void | Promise<void>;
};

const TITLE_ID = "webui2-computer-permission-guide-title";

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}

export function ComputerPermissionGuide({ capabilities, available, client, onRecheck }: Props): ReactElement | null {
  useI18n(); // Subscribe to the app language so a settings change rerenders this surface.
  const language = preferredComputerLanguage();
  const copy = computerUICopy[language];
  const actions = computerPermissionGuideActions(available, capabilities);
  const [openingTarget, setOpeningTarget] = useState<ComputerPermissionTarget | null>(null);
  const [rechecking, setRechecking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (actions.length === 0) return null;

  const openSettings = async (target: ComputerPermissionTarget) => {
    setError(null);
    setOpeningTarget(target);
    try {
      await client.openPermissionSettings(target);
    } catch (cause) {
      setError(errorMessage(cause, copy.settingsError));
    } finally {
      setOpeningTarget(null);
    }
  };

  const recheck = async () => {
    setError(null);
    setRechecking(true);
    try {
      await onRecheck();
    } catch (cause) {
      setError(errorMessage(cause, copy.settingsError));
    } finally {
      setRechecking(false);
    }
  };

  return <section className="webui2-computer-permission-guide" aria-labelledby={TITLE_ID}>
    <span className="webui2-computer-eyebrow">{copy.systemPermissions}</span>
    <h3 id={TITLE_ID}>{copy.permissionsTitle}</h3>
    <p>{copy.permissionsIntro}</p>
    <div className="webui2-computer-permission-actions">
      {actions.map((action) => <div className="webui2-computer-permission-action" key={action.target}>
        <div>
          <strong>{action.target === "accessibility" ? copy.accessibility : copy.screenRecording}</strong>
          <span>{action.target === "accessibility" ? copy.accessibilityDescription : copy.screenRecordingDescription}</span>
        </div>
        <button
          className="webui2-computer-button webui2-computer-button--quiet"
          disabled={openingTarget !== null || rechecking}
          onClick={() => void openSettings(action.target)}
          type="button"
        >{openingTarget === action.target ? copy.openingSettings : action.target === "accessibility" ? copy.openAccessibility : copy.openScreenRecording}</button>
      </div>)}
    </div>
    {error ? <p className="webui2-computer-error" role="alert">{error}</p> : null}
    <button className="webui2-computer-button webui2-computer-button--quiet" disabled={openingTarget !== null || rechecking} onClick={() => void recheck()} type="button">
      {rechecking ? copy.checkingPermissions : copy.recheckPermissions}
    </button>
  </section>;
}
