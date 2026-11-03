import { useEffect, useRef, useState } from "react";
import type { AgentProfileRecord, ProviderOption } from "../../lib/types";
import { WORKFLOW_AGENT_ROLE_TYPES, type WorkflowAgentRoleType } from "../../lib/workflowAgentProfiles";
import { type AgentDraft, type AgentOption, canManageWorkflowAgent, emptyAgentDraft, hasNoWorkflowTools, type WorkflowAgentClient } from "./agentClient";
import { useWorkflowText } from "./i18n";
import { WORKFLOW } from "./types";
import { message } from "./useWorkflowRun";

export function AgentManager({ client, onDirtyChange, onRequestNavigation }: { client: WorkflowAgentClient; onDirtyChange: (dirty: boolean) => void; onRequestNavigation: (action: () => void) => void }) {
  const text = useWorkflowText();
  const mounted = useRef(true);
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [draft, setDraft] = useState<AgentDraft>(emptyAgentDraft);
  const [editing, setEditing] = useState<AgentProfileRecord>();
  const [providers, setProviders] = useState<ProviderOption[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [custom, setCustom] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [loading, setLoading] = useState(true);
  const refresh = async () => { const items = await client.list(); if (mounted.current) setAgents(items); };
  useEffect(() => {
    let active = true;
    void client.list().then((value) => { if (active) setAgents(value); }).catch((reason) => { if (active) setError(message(reason)); }).finally(() => { if (active) setLoading(false); });
    void Promise.all([client.providers(), client.models()]).then(([ps, ms]) => { if (active) { setProviders(ps); setModels(ms); } }).catch((reason) => { if (active) setError(message(reason)); });
    return () => { active = false; };
  }, [client]);
  const update = (patch: Partial<AgentDraft>) => { setDraft((value) => ({ ...value, ...patch })); setSaved(false); onDirtyChange(true); };
  const focusEditor = () => { formRef.current?.scrollIntoView?.({ behavior: "smooth", block: "start" }); };
  const operate = async (action: () => Promise<void>) => { setBusy(true); setError(""); try { await action(); if (mounted.current) await refresh(); } catch (reason) { if (mounted.current) setError(message(reason)); } finally { if (mounted.current) setBusy(false); } };
  const save = () => operate(async () => {
    if (!draft.name.trim()) throw new Error(text("nameError"));
    if (custom && (!draft.provider.trim() || !draft.model.trim())) throw new Error(`${text("provider")} / ${text("model")}`);
    await client.save(draft, editing);
    if (!mounted.current) return;
    onDirtyChange(false); setDraft(emptyAgentDraft()); setEditing(undefined); setCustom(false); setSaved(true);
  });
  const choose = (agent?: AgentOption) => onRequestNavigation(() => {
    const record = agent?.record;
    const manageable = record ? canManageWorkflowAgent(record) : false;
    setEditing(manageable ? record : undefined);
    setDraft(agent ? { name: record!.display_name, role: agent.document.identity.role_type, prompt: [agent.document.prompt.persona, agent.document.prompt.system_addendum].filter(Boolean).join("\n\n"), provider: agent.document.execution.provider ?? "", model: agent.document.execution.model ?? "" } : emptyAgentDraft());
    setCustom(Boolean(agent?.document.execution.provider || agent?.document.execution.model));
    setSaved(false); onDirtyChange(false); focusEditor();
  });
  return <div className="workflow-agent-layout">
    <section className="workflow-agent-directory" aria-label={text("agents")}> <div className="workflow-section-heading"><div><h2>{text("agents")}</h2><p className="workflow-muted">{text("agentDirectoryHint")}</p></div><div className="workflow-actions"><button disabled={busy} onClick={() => choose()} type="button">+ {text("newAgent")}</button><button className="workflow-quiet" disabled={busy} onClick={() => { void operate(async () => undefined); }} type="button">{text("retry")}</button></div></div>
      {loading ? <p role="status">{text("loading")}</p> : agents.length === 0 ? <p className="workflow-empty">{text("noAgents")}</p> : null}
      <div className="workflow-agent-list">{agents.map((agent) => {
        const { record, document } = agent;
        const manageable = canManageWorkflowAgent(record);
        return <article className="workflow-agent-row" data-active={editing?.id === record.id} key={`${record.id}:${record.scope}:${record.profile_key}:${record.profile_version}`}>
          <div className="workflow-agent-row-header"><h3>{record.display_name}</h3><span className="workflow-status" data-status={record.status}>{record.status === "published" ? text("published") : record.status === "draft" ? text("draft") : text("archived")}</span></div>
          <p className="workflow-agent-facts">{text(document.identity.role_type)} · v{record.profile_version} · {document.execution.provider ? `${document.execution.provider} / ${document.execution.model ?? ""}` : text("inherited")}</p>
          <p className="workflow-agent-permission">{text(hasNoWorkflowTools(agent) ? "tools" : "toolsUnavailable")}</p>
          {!manageable ? <p className="workflow-muted">{text("personalAgentHint")}</p> : null}
          <div className="workflow-actions"><button disabled={busy} onClick={() => choose(agent)} type="button">{text(manageable ? "edit" : "createPersonal")}</button>
            {manageable && record.status !== "published" ? <button disabled={busy} onClick={() => { void operate(() => client.publish(record)); }} type="button">{text("publish")}</button> : null}
            {manageable ? <button className="workflow-quiet" disabled={busy} onClick={() => { void operate(() => client.archive(record)); }} type="button">{text("archive")}</button> : null}</div>
        </article>;
      })}</div>
    </section>
    <form ref={formRef} className="workflow-agent-form workflow-card" onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <div className="workflow-section-heading"><div><p className="workflow-eyebrow">AGENT</p><h2>{editing ? text("edit") : text("newAgent")}</h2></div></div>
      {saved ? <p className="workflow-notice workflow-success" role="status">{text("agentSaved")}</p> : null}
      <fieldset disabled={busy}>
        <div className="workflow-form-pair"><label>{text("name")}<input required maxLength={128} value={draft.name} onChange={(event) => update({ name: event.target.value })} /></label><label>{text("role")}<select value={draft.role} onChange={(event) => update({ role: event.target.value as WorkflowAgentRoleType })}>{WORKFLOW_AGENT_ROLE_TYPES.map((role) => <option key={role} value={role}>{text(role)}</option>)}</select></label></div>
        <label>{text("prompt")}<textarea maxLength={16384} value={draft.prompt} onChange={(event) => update({ prompt: event.target.value })} rows={5} /></label>
        <label>{text("model")}<select value={custom ? "custom" : "inherit"} onChange={(event) => { const next = event.target.value === "custom"; setCustom(next); setSaved(false); if (!next) update({ provider: "", model: "" }); else onDirtyChange(true); }}><option value="inherit">{text("inherited")}</option><option value="custom">{text("customModel")}</option></select></label>
        {custom ? <div className="workflow-form-pair"><label>{text("provider")}<select required value={draft.provider} onChange={(event) => { const provider = providers.find((item) => item.name === event.target.value); update({ provider: event.target.value, model: provider?.model ?? "" }); }}><option value="">—</option>{providers.map((provider) => <option key={provider.name} value={provider.name}>{provider.name === WORKFLOW.primaryProvider ? text("primaryProvider") : provider.name}</option>)}</select></label><label>{text("model")}<input required list="workflow-agent-models" value={draft.model} onChange={(event) => update({ model: event.target.value })} /><datalist id="workflow-agent-models">{models.map((model) => <option key={model} value={model} />)}</datalist></label></div> : null}
        <p className="workflow-permission-note"><strong>{text("tools")}</strong><span>{text("toolsHint")}</span></p>
        <div className="workflow-form-actions"><button className="workflow-primary" type="submit">{busy ? text("busy") : text("saveAgent")}</button></div>
      </fieldset>{error ? <p role="alert">{error}</p> : null}
    </form>
  </div>;
}
