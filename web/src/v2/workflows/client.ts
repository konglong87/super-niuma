import { apiRequest } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import { streamWorkflowEvents } from "./sse";
import type { Builtin, EventPage, Run, Template, TemplateInput, WorkflowEvent } from "./types";

export interface WorkflowClient {
  list(signal?: AbortSignal): Promise<Template[]>;
  builtins(signal?: AbortSignal): Promise<Builtin[]>;
  createBuiltin(key: string, signal?: AbortSignal): Promise<Template>;
  save(input: TemplateInput, signal?: AbortSignal): Promise<Template>;
  start(key: string, version: number, input: string, idempotencyKey: string, signal?: AbortSignal): Promise<{ run: Run; replayed: boolean }>;
  runs(key: string, version: number, signal?: AbortSignal): Promise<Run[]>;
  get(runID: string, signal?: AbortSignal): Promise<Run>;
  events(runID: string, after: number, signal?: AbortSignal): Promise<EventPage>;
  stream(runID: string, after: number, onEvent: (event: WorkflowEvent) => void, signal: AbortSignal): Promise<void>;
  cancel(runID: string, signal?: AbortSignal): Promise<Run>;
  recover(runID: string, revision: number, signal?: AbortSignal): Promise<Run>;
}
const templatePath = (key: string) => `/tenant/workflows/${encodeURIComponent(key)}`;
const runPath = (id: string) => `/tenant/workflow-runs/${encodeURIComponent(id)}`;

export function createHTTPWorkflowClient(identity: IdentityConfig): WorkflowClient {
  return {
    list: async (signal) => (await apiRequest<{ items: Template[] }>(identity, "/tenant/workflows", { signal })).items ?? [],
    builtins: async (signal) => (await apiRequest<{ items: Builtin[] }>(identity, "/tenant/workflows/templates", { signal })).items ?? [],
    createBuiltin: async (key, signal) => (await apiRequest<{ template: Template }>(identity, `/tenant/workflows/templates/${encodeURIComponent(key)}/create`, { method: "POST", body: {}, signal })).template,
    save: async (input, signal) => (await apiRequest<{ template: Template }>(identity, "/tenant/workflows", { method: "POST", body: { ...(input.key ? { key: input.key } : {}), name: input.name, description: input.description, definition: input.definition, agents: input.agents }, signal })).template,
    start: (key, version, input, idempotencyKey, signal) => apiRequest(identity, `${templatePath(key)}/runs`, { method: "POST", body: { version, input }, headers: { "Idempotency-Key": idempotencyKey }, signal }),
    runs: async (key, version, signal) => (await apiRequest<{ items: Run[] }>(identity, `${templatePath(key)}/runs?version=${version}`, { signal })).items ?? [],
    get: async (id, signal) => (await apiRequest<{ run: Run }>(identity, runPath(id), { signal })).run,
    events: (id, after, signal) => apiRequest(identity, `${runPath(id)}/events?after_sequence=${after}`, { signal }),
    stream: (id, after, onEvent, signal) => streamWorkflowEvents(identity, `${runPath(id)}/events/stream?after_sequence=${after}`, onEvent, signal),
    cancel: async (id, signal) => (await apiRequest<{ run: Run }>(identity, `${runPath(id)}/cancel`, { method: "POST", body: {}, signal })).run,
    recover: async (id, revision, signal) => (await apiRequest<{ run: Run }>(identity, `${runPath(id)}/recover`, { method: "POST", body: { revision }, signal })).run,
  };
}
