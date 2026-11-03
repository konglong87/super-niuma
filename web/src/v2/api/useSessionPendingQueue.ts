import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { cancelPendingInput, createPendingInputSideChat, getPendingInputQueueSettings, listPendingInputs, movePendingInputUp, retryPendingInput, setPendingInputQueueEnabled, updatePendingInput } from "../../lib/api";
import { sortPendingInputs } from "../../lib/pendingInputs";
import type { IdentityConfig } from "../../lib/types";
import type { SessionRef } from "../types";

const QUEUE_REFRESH_MS = 5000;
const EVENT_REFRESH_DELAY_MS = 300;

export type PendingQueueScope = { identity: IdentityConfig; sessionRef: SessionRef; taskID?: number; revision?: string };
export type PendingQueueAction =
  | { kind: "edit"; id: string; content: string; direction: string }
  | { kind: "move" | "delete" | "retry" | "side-chat"; id: string }
  | { kind: "enable"; enabled: boolean };

export function pendingQueueQueryKey({ identity, sessionRef, taskID }: PendingQueueScope) {
  return ["session-pending-queue", identity.apiBase, identity.tenantKey, identity.userId, sessionRef, taskID] as const;
}

export function useSessionPendingQueue(scope: PendingQueueScope, expanded: boolean, observeItems = true) {
  const { identity, taskID, revision, sessionRef } = scope;
  const queryClient = useQueryClient();
  const queryKey = pendingQueueQueryKey(scope);
  const enabled = sessionRef.startsWith("tenant:") && Number.isSafeInteger(taskID) && Number(taskID) > 0;
  const items = useQuery({
    queryKey: [...queryKey, "items"],
    queryFn: ({ signal }) => listPendingInputs(identity, taskID!, signal),
    select: sortPendingInputs,
    enabled: enabled && observeItems,
    // Only mounted session workspaces observe the queue; background tabs do not poll.
    refetchInterval: observeItems ? QUEUE_REFRESH_MS : false,
    refetchIntervalInBackground: false
  });
  const settings = useQuery({
    queryKey: [...queryKey, "settings"],
    queryFn: ({ signal }) => getPendingInputQueueSettings(identity, taskID!, signal),
    enabled: enabled && expanded,
    refetchInterval: expanded ? QUEUE_REFRESH_MS : false,
    refetchIntervalInBackground: false
  });

  useEffect(() => {
    if (!enabled || !observeItems || revision === undefined) return;
    // Coalesce streaming event bursts while the interval covers a continuously busy run.
    const timeout = window.setTimeout(() => {
      void queryClient.invalidateQueries({ queryKey: pendingQueueQueryKey({ identity, sessionRef, taskID }) });
    }, EVENT_REFRESH_DELAY_MS);
    return () => window.clearTimeout(timeout);
  }, [queryClient, identity, sessionRef, taskID, enabled, observeItems, revision]);

  const mutation = useMutation({
    mutationFn: async (action: PendingQueueAction) => {
      if (!enabled) throw new Error("Queue is unavailable");
      await queryClient.cancelQueries({ queryKey });
      switch (action.kind) {
        case "edit": return updatePendingInput(identity, taskID!, action.id, { content: action.content, direction: action.direction });
        case "move": return movePendingInputUp(identity, taskID!, action.id);
        case "delete": return cancelPendingInput(identity, taskID!, action.id);
        case "retry": return retryPendingInput(identity, taskID!, action.id);
        case "enable": return setPendingInputQueueEnabled(identity, taskID!, action.enabled);
        case "side-chat": return createPendingInputSideChat(identity, taskID!, action.id);
      }
    },
    // Always read back, including conflicts caused by the coordinator claiming an item.
    onSettled: () => queryClient.invalidateQueries({ queryKey })
  });

  return {
    items, settings, mutation, enabled,
    refresh: () => queryClient.invalidateQueries({ queryKey })
  };
}
