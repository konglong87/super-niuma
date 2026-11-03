import { isWorkflowBindableAgent, type AgentOption } from "./agentClient";
import type { Binding } from "./types";
// ID is the primary identity; key/version are still sent and checked as immutable assertions.
export function resolveAgentBinding(binding: Binding, agents: AgentOption[]): AgentOption | undefined {
  const matches = agents.filter(isWorkflowBindableAgent).filter(({ record }) => record.profile_key === binding.profile_key && record.profile_version === binding.profile_version);
  if (binding.profile_id !== undefined) return matches.find(({ record }) => record.id === binding.profile_id);
  // Legacy bindings follow the server's private-before-shared resolution rule, never an ID=0 catalog row.
  return matches.find(({ record }) => record.scope === "user_private") ?? matches[0];
}
export function workflowAgentChoices(agents: AgentOption[], bindings: Binding[]) {
  const eligible = agents.filter(isWorkflowBindableAgent);
  return eligible.map((agent) => {
    const { record } = agent;
    const existing = bindings.find((binding) => resolveAgentBinding(binding, eligible)?.record.id === record.id);
    return { value: String(record.id), record, binding: { ref: existing?.ref ?? `a-${record.id}-v${record.profile_version}`, profile_id: record.id, profile_key: record.profile_key, profile_version: record.profile_version } satisfies Binding };
  });
}
export function withAgentBindingIDs(bindings: Binding[], agents: AgentOption[]): Binding[] {
  return bindings.map((binding) => {
    const agent = resolveAgentBinding(binding, agents);
    return agent ? { ...binding, profile_id: agent.record.id } : binding;
  });
}
