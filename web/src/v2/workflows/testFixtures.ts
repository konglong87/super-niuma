// Test-only fixtures. Production creates no mock client and never fabricates runs.
import { vi } from "vitest";
import { defaultWorkflowAgentProfileDocument } from "../../lib/workflowAgentProfiles";
import type { AgentOption, WorkflowAgentClient } from "./agentClient";
import type { WorkflowClient } from "./client";
import { blankTemplate } from "./editor";
import type { Run, Template, Usage, WorkflowEvent } from "./types";
export const usage: Usage = { input_tokens: 7, output_tokens: 11, cache_read_tokens: 2, cache_write_tokens: 3 };
export function templateFixture(): Template {
  const value = blankTemplate();
  value.name = "Release review";
  value.agents = [{ ref: "writer", profile_key: "writer", profile_version: 1 }];
  value.definition.nodes[0] = { ...value.definition.nodes[0], agent_ref: "writer", instruction: "Write a release summary" };
  return { ...value, id: 1, key: "review", version: 1 };
}
export function runFixture(patch: Partial<Run> = {}): Run {
  const template = templateFixture();
  return { id: "run-1", scope: { tenant_id: 1, user_id: 1 }, team_id: 1, team_key: "review", team_version: 1, name: "Release review", input: "Review the release", plan: { definition: template.definition, agents: [], hash: "hash" }, status: "running", revision: 1, epoch: 1, last_sequence: 0, created_at: "2026-10-29T00:00:00Z", deadline: "2026-10-29T00:10:00Z", nodes: [{ node_id: "step-1", status: "pending", attempts: [] }], artifacts: [], usage, ...patch };
}
export function eventFixture(sequence: number, patch: Partial<WorkflowEvent> = {}): WorkflowEvent { return { sequence, type: "agent_started", created_at: "2026-10-29T00:00:00Z", ...patch }; }
export function workflowClientFixture(): WorkflowClient {
  return { list: vi.fn(async () => [templateFixture()]), builtins: vi.fn(async () => [{ key: "chain", name: "Chain", description: "Plan → execute → review" }, { key: "parallel-join", name: "Parallel join", description: "Research → join" }]), createBuiltin: vi.fn(async () => templateFixture()), save: vi.fn(async () => templateFixture()), start: vi.fn(async () => ({ run: runFixture(), replayed: false })), runs: vi.fn(async () => []), get: vi.fn(async () => runFixture()), events: vi.fn(async () => ({ events: [], has_more: false, last_sequence: 0 })), stream: vi.fn((_id, _after, _onEvent, signal) => new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }))), cancel: vi.fn(async () => runFixture({ status: "cancel_requested", revision: 2 })), recover: vi.fn(async () => runFixture({ status: "queued", revision: 2, epoch: 2 })) };
}
export function agentFixture(): AgentOption {
  const document = defaultWorkflowAgentProfileDocument("reviewer");
  return { record: { id: 1, profile_key: "writer", display_name: "Release reviewer", profile_version: 1, status: "published", config_json: JSON.stringify(document) }, document };
}
export function agentClientFixture(): WorkflowAgentClient {
  return { list: vi.fn(async () => [agentFixture()]), save: vi.fn(async () => ({ ...agentFixture().record, status: "draft" })), publish: vi.fn(async () => undefined), archive: vi.fn(async () => undefined), providers: vi.fn(async () => [{ name: "local", model: "configured-model" }]), models: vi.fn(async () => ["configured-model"]) };
}
