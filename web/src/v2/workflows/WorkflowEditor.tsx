import { useEffect, useRef, useState } from "react";
import { type AgentOption, isWorkflowBindableAgent, type WorkflowAgentClient } from "./agentClient";
import { resolveAgentBinding, withAgentBindingIDs, workflowAgentChoices } from "./bindings";
import type { WorkflowClient } from "./client";
import { blankNode, blankTemplate, type DependencyPreset, nextNodeID, removeNode, validateTemplate, withPreset } from "./editor";
import { useWorkflowText } from "./i18n";
import { type Binding, type Run, type Template, type TemplateInput, WORKFLOW, type WorkflowNode } from "./types";
import { message } from "./useWorkflowRun";
export function WorkflowEditor({ template, client, agentClient, onSaved, onRun, onDirtyChange, onAgents }: {
  template?: Template; client: WorkflowClient; agentClient: WorkflowAgentClient; onSaved: (template: Template) => void;
  onRun: (id: string) => void; onDirtyChange: (dirty: boolean) => void; onAgents: () => void;
}) {
  const text = useWorkflowText();
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const [draft, setDraft] = useState<TemplateInput>(() => template ? { key: template.key, name: template.name, description: template.description, definition: template.definition, agents: template.agents } : blankTemplate());
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [preset, setPreset] = useState<DependencyPreset>("custom");
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [input, setInput] = useState("");
  const [runs, setRuns] = useState<Run[]>([]);
  const [runError, setRunError] = useState("");
  const idempotency = useState(() => ({ current: "", fingerprint: "" }))[0];
  useEffect(() => {
    let active = true;
    void agentClient.list().then((value) => { if (active) setAgents(value.filter(isWorkflowBindableAgent)); }).catch((reason) => { if (active) setError(message(reason)); });
    if (template) void client.runs(template.key, 0).then((value) => { if (active) setRuns(value); }).catch((reason) => { if (active) setRunError(message(reason)); });
    return () => { active = false; };
  }, [client, agentClient, template]);
  const update = (next: TemplateInput) => { setDraft(next); setDirty(true); onDirtyChange(true); };
  const updateNode = (id: string, patch: Partial<WorkflowNode>) => update({ ...draft, definition: { ...draft.definition, nodes: draft.definition.nodes.map((node) => node.id === id ? { ...node, ...patch } : node) } });
  const options = workflowAgentChoices(agents, draft.agents);
  const selectedAgentID = (ref: string) => { const binding = draft.agents.find((agent) => agent.ref === ref); return binding ? String(resolveAgentBinding(binding, agents)?.record.id ?? `unavailable:${ref}`) : ""; };
  const save = async () => {
    const used = new Set(draft.definition.nodes.map((node) => node.agent_ref));
    const cleaned = { ...draft, agents: withAgentBindingIDs(draft.agents.filter((agent) => used.has(agent.ref)), agents) };
    if (validateTemplate(cleaned).length) { setError(text("invalid")); return; }
    setBusy(true); setError("");
    try { const result = await client.save(cleaned); if (!mounted.current) return; onDirtyChange(false); setDirty(false); onSaved(result); }
    catch (reason) { if (mounted.current) setError(message(reason)); } finally { if (mounted.current) setBusy(false); }
  };
  const start = async () => {
    if (!template || dirty) return;
    if (!input.trim() || new TextEncoder().encode(input).length > WORKFLOW.inputBytes) { setRunError(text("inputError")); return; }
    const fingerprint = JSON.stringify([template.key, template.version, input]);
    // A failed HTTP response may have created a run: retries of identical input MUST reuse its key.
    if (idempotency.fingerprint !== fingerprint) { idempotency.current = crypto.randomUUID(); idempotency.fingerprint = fingerprint; }
    setBusy(true); setRunError("");
    try { const result = await client.start(template.key, template.version, input, idempotency.current); if (mounted.current) onRun(result.run.id); }
    catch (reason) { if (mounted.current) setRunError(message(reason)); } finally { if (mounted.current) setBusy(false); }
  };
  return <div className="workflow-editor">
    <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <fieldset disabled={busy}>
        <div className="workflow-card workflow-heading-fields"><div className="workflow-section-heading"><h2>{text("workflowSetup")}</h2><div className="workflow-actions">{template ? <span className="workflow-library-meta">v{template.version}</span> : null}<button className="workflow-primary" type="submit">{busy ? text("busy") : text(template ? "saveVersion" : "save")}</button></div></div><p className="workflow-muted workflow-save-hint">{text("saveHint")}</p><label>{text("name")}<input required maxLength={128} value={draft.name} onChange={(event) => update({ ...draft, name: event.target.value })} /></label><label>{text("description")}<textarea rows={2} value={draft.description} onChange={(event) => update({ ...draft, description: event.target.value })} /></label>{template ? <details className="workflow-technical"><summary>{text("technicalDetails")}</summary><small>{template.key} · v{template.version}</small></details> : null}</div>
        <div className="workflow-section-heading"><h2>{text("steps")}</h2><label><span className="workflow-sr">{text("steps")}</span><select aria-label={text("steps")} value={preset} onChange={(event) => { const next = event.target.value as DependencyPreset; setPreset(next); update({ ...draft, definition: withPreset(draft.definition, next) }); }}><option value="sequential">{text("sequential")}</option><option value="parallel">{text("parallel")}</option><option value="custom">{text("custom")}</option></select></label></div>
        <p className="workflow-muted workflow-helper">{text("dagHint")}</p>
        {!agents.length ? <p>{text("noAgents")} <button onClick={onAgents} type="button">{text("newAgent")}</button></p> : null}
        <ol className="workflow-step-list">{draft.definition.nodes.map((node, index) => <li className="workflow-card" key={node.id}>
          <div className="workflow-section-heading"><h3><span className="workflow-step-number">{index + 1}</span>{text("step")}</h3><button disabled={draft.definition.nodes.length <= 1} aria-label={`${text("remove")} ${index + 1}`} onClick={() => { setPreset("custom"); update({ ...draft, definition: removeNode(draft.definition, node.id) }); }} type="button">{text("remove")}</button></div>
          <label>{text("agent")}<select required value={selectedAgentID(node.agent_ref)} onChange={(event) => {
            const binding = options.find((option) => option.value === event.target.value)?.binding;
            if (!binding) { updateNode(node.id, { agent_ref: "" }); return; }
            const bindings: Binding[] = [...draft.agents.filter((agent) => agent.ref !== binding.ref), binding];
            update({ ...draft, agents: bindings, definition: { ...draft.definition, nodes: draft.definition.nodes.map((item) => item.id === node.id ? { ...item, agent_ref: binding.ref } : item) } });
          }}><option value="">{text("selectAgent")}</option>{node.agent_ref && selectedAgentID(node.agent_ref).startsWith("unavailable:") ? <option value={selectedAgentID(node.agent_ref)}>{node.agent_ref}</option> : null}{options.map((option) => <option key={option.value} value={option.value}>{option.record.display_name} · v{option.record.profile_version} · {text(option.record.scope === "user_private" ? "privateAgent" : option.record.scope === "tenant_shared" ? "sharedAgent" : "databaseAgent")} #{option.record.id}</option>)}</select></label>
          <label>{text("instruction")}<textarea required rows={3} value={node.instruction} onChange={(event) => updateNode(node.id, { instruction: event.target.value })} /></label>
          <details className="workflow-dependency-disclosure"><summary>{text("dependencies")} · {node.depends_on.length ? node.depends_on.map((id) => `${text("step")} ${draft.definition.nodes.findIndex((item) => item.id === id) + 1}`).join(", ") : text("noDependencies")}</summary><fieldset className="workflow-dependencies"><legend className="workflow-sr">{text("dependencies")}</legend>{draft.definition.nodes.filter((item) => item.id !== node.id).map((dependency, dependencyIndex) => <label key={dependency.id}><input type="checkbox" checked={node.depends_on.includes(dependency.id)} onChange={(event) => { setPreset("custom"); updateNode(node.id, { depends_on: event.target.checked ? [...node.depends_on, dependency.id] : node.depends_on.filter((id) => id !== dependency.id) }); }} /><span className="workflow-muted">{draft.definition.nodes.findIndex((item) => item.id === dependency.id) + 1}. {dependency.instruction.slice(0, 40) || `${text("step")} ${dependencyIndex + 1}`}</span></label>)}{!node.depends_on.length ? <small>{text("noDependencies")}</small> : null}</fieldset></details>
          <details><summary>{text("advanced")}</summary><div className="workflow-number-grid"><NumberField label={text("timeout")} value={node.timeout_seconds} max={WORKFLOW.maxNodeSeconds} onChange={(timeout_seconds) => updateNode(node.id, { timeout_seconds })} /><NumberField label={text("attempts")} value={node.retry.max_attempts} max={WORKFLOW.maxAttempts} onChange={(max_attempts) => updateNode(node.id, { retry: { ...node.retry, max_attempts } })} /><NumberField label={text("backoff")} value={node.retry.backoff_ms} min={1} max={WORKFLOW.maxBackoffMS} onChange={(backoff_ms) => updateNode(node.id, { retry: { ...node.retry, backoff_ms } })} /></div></details>
        </li>)}</ol>
        <button disabled={draft.definition.nodes.length >= WORKFLOW.maxNodes} onClick={() => { const nodes = [...draft.definition.nodes, blankNode(nextNodeID(draft.definition))]; update({ ...draft, definition: withPreset({ ...draft.definition, nodes }, preset) }); }} type="button">+ {text("add")}</button>
        <details className="workflow-card"><summary>{text("advanced")}</summary><div className="workflow-number-grid">{([
          ["max_parallel", text("maxParallel"), WORKFLOW.maxParallel], ["run_timeout_seconds", text("runTimeout"), WORKFLOW.maxRunSeconds], ["max_total_tokens", text("tokens"), WORKFLOW.maxTokens],
        ] as const).map(([key, label, max]) => <NumberField key={key} label={label} value={draft.definition.limits[key]} max={max} onChange={(value) => update({ ...draft, definition: { ...draft.definition, limits: { ...draft.definition.limits, [key]: value } } })} />)}</div></details>
      </fieldset>{error ? <p role="alert">{error}</p> : null}
    </form>
    {template ? <aside className="workflow-card workflow-launch"><h2>{text("start")}</h2><p className="workflow-muted">{text("startHint")}</p><label>{text("input")}<textarea value={input} onChange={(event) => setInput(event.target.value)} rows={4} disabled={busy} /></label><button className="workflow-primary" disabled={busy || dirty || !input.trim()} onClick={() => { void start(); }} type="button">{busy ? text("busy") : text("start")}</button>{runError ? <p role="alert">{runError}</p> : null}{dirty ? <p className="workflow-muted" role="status">{text("unsavedRunHint")}</p> : null}<h3>{text("runs")} · {text("allVersions")}</h3>{!runs.length ? <p>{text("noRuns")}</p> : <ul className="workflow-plain-list">{runs.map((run) => <li key={run.id}><button type="button" onClick={() => onRun(run.id)}>{run.name || run.id} · v{run.team_version} · {text(run.status)} · {new Date(run.created_at).toLocaleString()}</button></li>)}</ul>}</aside> : null}
  </div>;
}
function NumberField({ label, value, min = 1, max, onChange }: { label: string; value: number; min?: number; max: number; onChange: (value: number) => void }) {
  return <label>{label}<input type="number" required step={1} min={min} max={max} value={Number.isFinite(value) ? value : ""} onChange={(event) => onChange(event.target.valueAsNumber)} /></label>;
}
