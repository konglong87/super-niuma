import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { IdentityConfig } from "../../lib/types";
import type { CreateSessionInput, OperationResult, SendSessionInput, SessionDetail, SessionListFilters, SessionRef, SessionSummary } from "../types";
import { SessionControlError, sessionControlQueryKeys, useSessionControlClient, type RenameSessionInput } from "./sessionControlClient";
import { mergeSessionDetail } from "./sessionDetailMerge";

type CreateSessionMutationInput = Omit<CreateSessionInput, "idempotencyKey"> & { idempotencyKey?: string };
type SendSessionMutationInput = Omit<SendSessionInput, "idempotencyKey"> & { idempotencyKey?: string };
type SessionRefMutationInput = { ref: SessionRef };
type ReadbackOptions = { background?: boolean };

export function useSessionList(identity: IdentityConfig, filters: SessionListFilters, enabled = true) {
  const client = useSessionControlClient();
  return useQuery({
    queryKey: sessionControlQueryKeys.list(identity, filters),
    queryFn: ({ signal }) => client.list(identity, filters, signal),
    refetchInterval: client.subscribe ? 5000 : false,
    retry: false,
    enabled
  });
}

export function useSessionDetail(identity: IdentityConfig, ref: SessionRef, enabled = true) {
  const client = useSessionControlClient();
  return useQuery({
    queryKey: sessionControlQueryKeys.detail(identity, ref),
    queryFn: ({ signal }) => client.get(identity, ref, signal),
    structuralSharing: (oldData, newData) => mergeSessionDetail(oldData as SessionDetail | undefined, newData as SessionDetail),
    enabled
  });
}

function useReadback(identity: IdentityConfig, options: ReadbackOptions = {}) {
  const queryClient = useQueryClient();
  const applyReadback = async (result: OperationResult): Promise<void> => {
    const detailKey = sessionControlQueryKeys.detail(identity, result.session.ref);
    const readback = sessionWithOperationReplay(result.session, result);
    queryClient.setQueryData<SessionDetail>(detailKey, (current) => current?.events ? { ...current, status: readback.status } : readback);
    await queryClient.invalidateQueries({ queryKey: detailKey });
    queryClient.setQueryData<SessionDetail>(detailKey, (current) => current ? sessionWithOperationReplay(current, result) : readback);
    await queryClient.invalidateQueries({ queryKey: ["session-control", "list", identity.tenantKey, identity.userId] });
  };
  if (!options.background) return applyReadback;
  return (result: OperationResult): void => {
    // The mutation response is the acceptance boundary for the composer. A
    // slow readback must not keep an accepted draft visible in the input box.
    void applyReadback(result).catch(() => undefined);
  };
}

function sessionWithOperationReplay(session: SessionDetail, result: OperationResult): SessionDetail {
  const operationMessage = result.session.messages.find((message) => message.operation?.id === result.operation.id);
  const operationExists = session.messages.some((message) => message.operation?.id === result.operation.id);
  const messages = operationExists ? session.messages : operationMessage ? [...session.messages, operationMessage] : session.messages;
  return {
    ...session,
    messages: messages.map((message) => message.operation?.id === result.operation.id ? {
      ...message,
      operation: { ...message.operation, replayed: result.replayed }
    } : message)
  };
}

export function useCreateSession(identity: IdentityConfig) {
  const client = useSessionControlClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateSessionMutationInput) => client.create(identity, { ...input, idempotencyKey: input.idempotencyKey ?? crypto.randomUUID() }),
    onSuccess: (result) => {
      const detailKey = sessionControlQueryKeys.detail(identity, result.session.ref);
      const readback = sessionWithOperationReplay(result.session, result);
      queryClient.setQueryData<SessionDetail>(detailKey, (current) => current?.events
        ? sessionWithOperationReplay({ ...current, status: readback.status, updatedAt: readback.updatedAt, activeRunID: readback.activeRunID }, result)
        : readback);
      void queryClient.invalidateQueries({ queryKey: ["session-control", "list", identity.tenantKey, identity.userId] }).catch(() => undefined);
    }
  });
}

export function useSendSession(identity: IdentityConfig) {
  const client = useSessionControlClient();
  const applyReadback = useReadback(identity, { background: true });
  return useMutation({
    mutationFn: (input: SendSessionMutationInput) => client.send(identity, { ...input, idempotencyKey: input.idempotencyKey ?? crypto.randomUUID() }),
    onSuccess: applyReadback
  });
}

export function useCompactSession(identity: IdentityConfig) {
  const client = useSessionControlClient();
  const applyReadback = useReadback(identity);
  return useMutation({
    mutationFn: (input: { ref: SessionRef; idempotencyKey?: string }) => {
      if (!client.compact) throw new SessionControlError("invalid_state");
      return client.compact(identity, { ...input, idempotencyKey: input.idempotencyKey ?? crypto.randomUUID() });
    },
    onSuccess: applyReadback
  });
}

export function useStopSession(identity: IdentityConfig) {
  const client = useSessionControlClient();
  const applyReadback = useReadback(identity);
  return useMutation({
    mutationFn: (input: SessionRefMutationInput) => client.stop(identity, { ...input, idempotencyKey: crypto.randomUUID() }),
    onSuccess: applyReadback
  });
}

export function useArchiveSession(identity: IdentityConfig) {
  const client = useSessionControlClient();
  const applyReadback = useReadback(identity);
  return useMutation({
    mutationFn: (input: SessionRefMutationInput) => client.archive(identity, { ...input, idempotencyKey: crypto.randomUUID() }),
    onSuccess: applyReadback
  });
}

export function useRenameSession(identity: IdentityConfig) {
  const client = useSessionControlClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (input: RenameSessionInput) => {
      if (!client.rename) throw new SessionControlError("invalid_state");
      await client.rename(identity, { ...input, title: input.title.trim() });
    },
    onSuccess: async (_result, input) => {
      const detailKey = sessionControlQueryKeys.detail(identity, input.ref);
      const lists = {
        queryKey: ["session-control", "list", identity.tenantKey, identity.userId],
        predicate: (query: { queryKey: readonly unknown[] }) => query.queryKey.at(-1) === identity.apiBase
      };
      await Promise.all([queryClient.cancelQueries({ queryKey: detailKey, exact: true }), queryClient.cancelQueries(lists)]);
      const title = input.title.trim();
      queryClient.setQueryData<SessionDetail>(detailKey, (current) => current ? { ...current, title } : current);
      queryClient.setQueriesData<SessionSummary[]>(lists, (current) => current?.map((session) => session.ref === input.ref ? { ...session, title } : session));
      await Promise.all([queryClient.invalidateQueries({ queryKey: detailKey, exact: true }), queryClient.invalidateQueries(lists)]);
    }
  });
}
