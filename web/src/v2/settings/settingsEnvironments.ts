import { useQuery } from "@tanstack/react-query";
import { apiRequest } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";

export const CURRENT_SETTINGS_ENVIRONMENT = "current";
export const SETTINGS_ENVIRONMENTS_PATH = "/runtime/settings/environments";

export type SettingsEnvironment = {
  id: string;
  label: string;
  database: string;
  tenant_key: string;
  user_id: string;
  api_path: string;
  available: boolean;
};

export type SettingsEnvironments = {
  environments: SettingsEnvironment[];
  global_settings_path: string;
  global_settings_shared: boolean;
};

export function useSettingsEnvironments(identity: IdentityConfig) {
  return useQuery({
    queryKey: ["settings-environments", identity.apiBase, identity.tenantKey, identity.userId],
    queryFn: ({ signal }) => apiRequest<SettingsEnvironments>(identity, SETTINGS_ENVIRONMENTS_PATH, { signal }),
    retry: false,
    staleTime: 0,
  });
}

export function settingsEnvironmentIdentity(identity: IdentityConfig, environment: Pick<SettingsEnvironment, "id" | "api_path">): IdentityConfig {
  if (environment.id === CURRENT_SETTINGS_ENVIRONMENT) return identity;
  // Keep source authentication and headers: the server authorizes the operator
  // before binding the target tenant/user. Never accept an arbitrary API origin.
  const expected = `${SETTINGS_ENVIRONMENTS_PATH}/${encodeURIComponent(environment.id)}`;
  if (!/^[a-z][a-z0-9_-]{0,63}$/.test(environment.id) || environment.api_path !== expected) throw new Error("Invalid settings environment route");
  return { ...identity, apiBase: identity.apiBase.replace(/\/+$/, "") + expected, mobileJwt: "" };
}
