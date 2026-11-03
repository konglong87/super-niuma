import { useQuery } from "@tanstack/react-query";
import { getStatus, getWebAgentConversation, listModels, listProviders } from "../../lib/api";
import type { IdentityConfig, WebAgentConversationDetail } from "../../lib/types";
import type { SessionDetail, SessionRunConfig } from "../types";

const CATALOG_STALE_MS = 60000;
const RUNTIME_STATUS_STALE_MS = 5000;

export function useRuntimeCatalog(identity: IdentityConfig, enabled = true) {
  const scope = [identity.apiBase, identity.tenantKey, identity.userId];
  const providers = useQuery({ queryKey: ["webui2-providers", ...scope], queryFn: () => listProviders(identity), staleTime: CATALOG_STALE_MS, enabled });
  const models = useQuery({ queryKey: ["webui2-models", ...scope], queryFn: () => listModels(identity), staleTime: CATALOG_STALE_MS, enabled });
  const status = useRuntimeDefaults(identity, enabled);
  return { providers, models, status };
}

export function useRuntimeDefaults(identity: IdentityConfig, enabled = true) {
  const scope = [identity.apiBase, identity.tenantKey, identity.userId];
  return useQuery({
    queryKey: ["webui2-server-status", ...scope],
    queryFn: () => getStatus(identity),
    staleTime: RUNTIME_STATUS_STALE_MS,
    refetchOnMount: true,
    enabled
  });
}

export function useSessionRuntimeDetails(identity: IdentityConfig, detail: SessionDetail | undefined, enabled = true) {
  const runID = detail?.activeRunID || detail?.runs.at(-1)?.id;
  // Lifecycle keys share one request across workspace and Inspector observers.
  // Text deltas and the display clock do not invalidate task metadata.
  const key = ["webui2-runtime-details", identity.apiBase, identity.tenantKey, identity.userId, detail?.ref, runID, detail?.status];
  const available = enabled && detail?.source === "tenant" && Boolean(detail.id) && Boolean(detail.activeRunID || detail.runs.length);
  return useQuery({
    queryKey: key,
    queryFn: () => getWebAgentConversation(identity, `session:${detail!.id}`, { limit: 100, eventLimit: 1 }),
    enabled: available,
    staleTime: CATALOG_STALE_MS
  });
}

export function sessionRuntimeConfig(detail: SessionDetail, runtime?: WebAgentConversationDetail): Required<SessionRunConfig> {
  const latest = runtime?.tasks.find((task) => task.id === detail.activeRunID) ?? runtime?.latest_task;
  let metadata: Record<string, unknown> = {};
  try {
    const parsed: unknown = JSON.parse(latest?.metadata_json || "{}");
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) metadata = parsed as Record<string, unknown>;
  } catch { /* Legacy tasks may not contain runtime metadata. */ }
  const string = (value: unknown) => typeof value === "string" ? value : "";
  return {
    provider: detail.provider || string(metadata.provider), model: detail.model || latest?.model || "",
    permissionMode: detail.permissionMode || string(metadata.permission_mode), effort: detail.effort || string(metadata.effort),
    promptMode: detail.promptMode || string(metadata.prompt_mode)
  };
}
