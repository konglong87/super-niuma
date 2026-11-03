import { useCallback, useEffect, useState } from "react";
import type { Language } from "../../lib/i18n";
import { getDesktopServiceBridge, type DesktopServiceBridge } from "../desktopServiceBridge";
import { computerUICopy } from "./computerUICopy";
import type { ComputerWorkspaceDisplayMode } from "./computerWorkspacePreferences";
import type { ComputerSessionSnapshot } from "./types";

// Native visibility/expansion and preview capture belong to the host. This hook
// only probes support, publishes session status, and forwards explicit reopen.
export function useNativeComputerOverlay(enabled: boolean, session: ComputerSessionSnapshot | null, language: Language, readiness: string | null, displayMode: ComputerWorkspaceDisplayMode) {
  const bridge = getDesktopServiceBridge();
  const [availableBridge, setAvailableBridge] = useState<DesktopServiceBridge | null>(null);
  const available = enabled && bridge !== null && bridge === availableBridge;
  const active = available && session !== null && session.state !== "stopped";

  useEffect(() => {
    let disposed = false;
    setAvailableBridge(null);
    if (enabled && bridge?.IsComputerOverlayAvailable && bridge.UpdateComputerOverlay && bridge.ShowComputerOverlay) {
      void Promise.resolve().then(() => bridge.IsComputerOverlayAvailable!()).then((supported) => {
        if (!disposed) setAvailableBridge(supported === true ? bridge : null);
      }).catch(() => { /* Unsupported/unreachable hosts retain DOM controls. */ });
    }
    return () => { disposed = true; };
  }, [bridge, enabled]);

  useEffect(() => {
    if (!available || !bridge?.UpdateComputerOverlay) return;
    let disposed = false;
    const copy = computerUICopy[language];
    const activeSession = session?.state !== "stopped" ? session : null;
    const modelManaged = activeSession?.owner_kind === "managed_conversation";
    void Promise.resolve().then(() => bridge.UpdateComputerOverlay!({
      visible: Boolean(activeSession),
      // The host applies display_mode on session/mode changes and preserves
      // explicit expansion/hiding across status polls. No frontend auto-expand.
      expanded: false,
      display_mode: displayMode,
      session_id: activeSession?.session_id ?? "",
      title: copy.nativeTitle,
      detail: readiness || (activeSession ? copy.session[activeSession.state] : copy.idle),
      image_data: "", // The host supplies its independent read-only preview.
      can_stop: Boolean(activeSession?.capabilities.supports_stop),
      can_pause: !modelManaged && Boolean(activeSession?.capabilities.supports_pause)
        && (activeSession?.state === "ready" || activeSession?.state === "needs_observation"),
      can_resume: !modelManaged && Boolean(activeSession?.capabilities.supports_pause) && activeSession?.state === "paused",
      language,
    })).catch(() => {
      if (!disposed) setAvailableBridge(null);
    });
    return () => { disposed = true; };
  }, [available, bridge, displayMode, language, readiness, session]);

  const show = useCallback(() => {
    if (!active || !bridge?.ShowComputerOverlay) return;
    void Promise.resolve().then(() => bridge.ShowComputerOverlay!()).catch(() => setAvailableBridge(null));
  }, [active, bridge]);

  return { available, active, show };
}
