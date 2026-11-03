import { WORKFLOW, type Definition, type TemplateInput, type WorkflowNode } from "./types";
export const blankNode = (id: string): WorkflowNode => ({ id, agent_ref: "", instruction: "", depends_on: [], timeout_seconds: WORKFLOW.nodeSeconds, retry: { max_attempts: WORKFLOW.attempts, backoff_ms: WORKFLOW.backoffMS } });
export const blankTemplate = (): TemplateInput => ({ name: "", description: "", definition: { schema_version: WORKFLOW.schemaVersion, nodes: [blankNode("step-1")], limits: { max_parallel: WORKFLOW.defaultParallel, run_timeout_seconds: WORKFLOW.runSeconds, max_total_tokens: WORKFLOW.tokens } }, agents: [] });
export type DependencyPreset = "sequential" | "parallel" | "custom";
export function withPreset(definition: Definition, preset: DependencyPreset): Definition {
  if (preset === "custom") return definition;
  return { ...definition, nodes: definition.nodes.map((node, index, nodes) => ({ ...node, depends_on: preset === "parallel" || index === 0 ? [] : [nodes[index - 1].id] })) };
}
export function removeNode(definition: Definition, id: string): Definition {
  return { ...definition, nodes: definition.nodes.filter((node) => node.id !== id).map((node) => ({ ...node, depends_on: node.depends_on.filter((dependency) => dependency !== id) })) };
}
export function nextNodeID(definition: Definition): string {
  let number = 1;
  while (definition.nodes.some((node) => node.id === `step-${number}`)) number++;
  return `step-${number}`;
}
export function validateTemplate(input: TemplateInput): string[] {
  const errors: string[] = [];
  const { nodes, limits } = input.definition;
  if (!input.name.trim()) errors.push("name");
  if (!nodes.length || nodes.length > WORKFLOW.maxNodes) errors.push("nodes");
  const ids = new Set(nodes.map((node) => node.id));
  const bindings = new Set(input.agents.map((agent) => agent.ref));
  if (ids.size !== nodes.length) errors.push("duplicate");
  for (const node of nodes) {
    if (!node.instruction.trim() || !bindings.has(node.agent_ref)) errors.push("step");
    if (node.depends_on.some((id) => !ids.has(id) || id === node.id)) errors.push("dependency");
    if (!bounded(node.timeout_seconds, 1, WORKFLOW.maxNodeSeconds) || !bounded(node.retry.max_attempts, 1, WORKFLOW.maxAttempts) || !bounded(node.retry.backoff_ms, 1, WORKFLOW.maxBackoffMS)) errors.push("limits");
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const cyclic = (id: string): boolean => {
    if (visiting.has(id)) return true;
    if (visited.has(id)) return false;
    visiting.add(id);
    if ((nodes.find((node) => node.id === id)?.depends_on ?? []).some(cyclic)) return true;
    visiting.delete(id); visited.add(id); return false;
  };
  if (nodes.some((node) => cyclic(node.id))) errors.push("cycle");
  if (!bounded(limits.max_parallel, 1, WORKFLOW.maxParallel) || !bounded(limits.run_timeout_seconds, 1, WORKFLOW.maxRunSeconds) || !bounded(limits.max_total_tokens, 1, WORKFLOW.maxTokens)) errors.push("limits");
  return [...new Set(errors)];
}
function bounded(value: number, min: number, max: number): boolean { return Number.isInteger(value) && value >= min && value <= max; }
