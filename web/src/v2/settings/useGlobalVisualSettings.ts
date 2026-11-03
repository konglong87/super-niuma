import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { getGlobalSettings } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import { isDesktopV2Host } from "../../lib/config";
import { DESKTOP_BACKGROUND_CHANGED_EVENT, loadDesktopBackground } from "./desktopBackground";
import { GLOBAL_SETTINGS_SAVED_EVENT } from "./globalSettingsDraft";
import { readVisualSettings, type GlobalVisualSettings } from "./globalVisualSettings";
import type { DesktopBackgroundImage } from "../desktopServiceBridge";

export const GLOBAL_SETTINGS_QUERY_KEY = "webui2-global-settings";
const SETTINGS_STALE_MS = 60_000;

export function useGlobalVisualSettings(identity: IdentityConfig, enabled = true): { visual: GlobalVisualSettings; loading: boolean; error: boolean } {
  const queryClient = useQueryClient();
  const [desktopBackground, setDesktopBackground] = useState<DesktopBackgroundImage | null>(null);
  const scope = [GLOBAL_SETTINGS_QUERY_KEY, identity.apiBase, identity.tenantKey, identity.userId] as const;
  const query = useQuery({
    queryKey: scope,
    queryFn: () => getGlobalSettings(identity),
    enabled,
    staleTime: SETTINGS_STALE_MS
  });

  useEffect(() => {
    const refresh = (): void => { void queryClient.invalidateQueries({ queryKey: scope }); };
    window.addEventListener(GLOBAL_SETTINGS_SAVED_EVENT, refresh);
    return () => window.removeEventListener(GLOBAL_SETTINGS_SAVED_EVENT, refresh);
  }, [queryClient, identity.apiBase, identity.tenantKey, identity.userId]);

  useEffect(() => {
    if (!enabled || !isDesktopV2Host()) {
      setDesktopBackground(null);
      return;
    }
    let active = true;
    const refresh = (): void => {
      void loadDesktopBackground().then((background) => {
        if (active) setDesktopBackground(background);
      });
    };
    refresh();
    window.addEventListener(DESKTOP_BACKGROUND_CHANGED_EVENT, refresh);
    return () => {
      active = false;
      window.removeEventListener(DESKTOP_BACKGROUND_CHANGED_EVENT, refresh);
    };
  }, [enabled]);

  return { visual: readVisualSettings(query.data?.doc, desktopBackground), loading: query.isPending, error: query.isError };
}
