import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { IdentityConfig } from "../../lib/types";
import type { SessionDetail, SessionRef, SessionSummary } from "../types";
import { sessionControlQueryKeys, useSessionControlClient } from "./sessionControlClient";
import { applyConversationEvents } from "./sessionEventReducer";
import type { StreamState } from "../../hooks/useAgentTaskStream";

const RETRY_DELAY_MS = 2000;
const MAX_SUBSCRIPTIONS = 32;
const ACTIVE = new Set(["running", "queued", "waiting_permission", "waiting_input"]);

function updateReadback(queryClient: ReturnType<typeof useQueryClient>, identity: IdentityConfig, detail: SessionDetail): void {
  queryClient.setQueryData<SessionDetail>(sessionControlQueryKeys.detail(identity, detail.ref), detail);
  queryClient.setQueriesData<SessionSummary[]>({
    queryKey: ["session-control", "list", identity.tenantKey, identity.userId],
    predicate: (query) => query.queryKey.at(-1) === identity.apiBase
  }, (current) => current?.map((item) => item.ref === detail.ref ? { ...item, status: detail.status, updatedAt: detail.updatedAt, activeRunID: detail.activeRunID } : item));
}

// Connection ownership is at the application shell, independent of the selected
// conversation component. Every callback updates only its session's cache.
export function useSessionConversations(identity: IdentityConfig, sessions: SessionSummary[], selected: SessionRef | null, enabled = true): { error: string; state: StreamState } {
  const client = useSessionControlClient();
  const queryClient = useQueryClient();
  const [error, setError] = useState("");
  const [state, setState] = useState<StreamState>("idle");
  const identityRef = useRef(identity);
  identityRef.current = identity;
  const refs = [...new Set([...(selected?.startsWith("tenant:") ? [selected] : []), ...sessions.filter((s) => s.source === "tenant" && ACTIVE.has(s.status)).map((s) => s.ref)])].slice(0, MAX_SUBSCRIPTIONS).sort();
  const subscriptionKey = JSON.stringify(refs);
  const identityKey = JSON.stringify([identity.apiBase, identity.tenantKey, identity.userId, identity.apiToken, identity.mobileJwt]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Reconnect on credential/identity changes; read the current identity through its ref.
  useEffect(() => {
    if (!enabled || !client.subscribe || subscriptionKey === "[]") { setState("idle"); setError(""); return; }
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const currentIdentity = identityRef.current;
    const sessionRefs = JSON.parse(subscriptionKey) as SessionRef[];
    const readbackSessions = async (): Promise<boolean> => {
      const readback = await Promise.allSettled(sessionRefs.map((ref) => client.get(currentIdentity, ref, controller.signal)));
      const recovered = readback.some((result): result is PromiseFulfilledResult<SessionDetail> => result.status === "fulfilled");
      for (const result of readback) if (result.status === "fulfilled") updateReadback(queryClient, currentIdentity, result.value);
      return recovered;
    };
    const connect = async () => {
      setState("connecting");
      const subscriptions = sessionRefs.map((ref) => ({ ref, cursor: queryClient.getQueryData<SessionDetail>(sessionControlQueryKeys.detail(currentIdentity, ref))?.cursor ?? "0" }));
      try {
        await client.subscribe!(currentIdentity, subscriptions, (summary, events) => {
          if (controller.signal.aborted || !sessionRefs.includes(summary.ref)) return;
          setError("");
          setState("live");
          queryClient.setQueryData<SessionDetail>(sessionControlQueryKeys.detail(currentIdentity, summary.ref), (current) => applyConversationEvents({ ...current, ...summary, activeRunID: summary.activeRunID, messages: current?.messages ?? summary.messages, events: current?.events }, events, { live: current?.historyLoaded === true }));
          queryClient.setQueriesData<SessionSummary[]>({ queryKey: ["session-control", "list", currentIdentity.tenantKey, currentIdentity.userId], predicate: (query) => query.queryKey.at(-1) === currentIdentity.apiBase }, (current) => current?.map((item) => item.ref === summary.ref ? { ...item, status: summary.status, updatedAt: summary.updatedAt } : item));
        }, controller.signal);
        if (!controller.signal.aborted) {
          await readbackSessions();
          setError("");
          setState("closed");
        }
      } catch {
        if (!controller.signal.aborted) {
          // SSE is only the live transport. The persisted conversation is the
          // source of truth, so recover missed terminal events before exposing
          // a connection error to the user.
          const recovered = await readbackSessions();
          if (recovered) {
            setError("");
            setState("closed");
          } else {
            setError("stream_disconnected");
            setState("error");
          }
        }
      }
      if (!controller.signal.aborted) timer = setTimeout(() => void connect(), RETRY_DELAY_MS);
    };
    void connect();
    return () => { controller.abort(); clearTimeout(timer); };
  }, [client, queryClient, subscriptionKey, identityKey, enabled]);
  return { error, state };
}
