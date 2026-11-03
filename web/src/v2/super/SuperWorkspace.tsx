import { ClipboardList, RefreshCw } from "lucide-react";
import { useState, type FormEvent, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import { WORK_ORDER_STATUS, WORK_ORDER_LIMITS, type SuperClient } from "./client";
import { SUPER_COPY } from "./copy";
import { useSuperWorkOrders } from "./useSuperWorkOrders";
import { PlatformPanel } from "./PlatformPanel";
import { getSuperPlatformBridge } from "./platformClient";
import "./superWorkspace.css";

type Props = { client: SuperClient | null; onBack: () => void };
export function SuperWorkspace({ client, onBack }: Props): JSX.Element {
  const { language } = useI18n();
  const copy = SUPER_COPY[language === "zh" ? "zh" : "en"];
  const model = useSuperWorkOrders(client);
  const platformBridge = getSuperPlatformBridge();
  const [title, setTitle] = useState("");
  const [objective, setObjective] = useState("");
  const [criteria, setCriteria] = useState("");
  const [workspace, setWorkspace] = useState("");
  const selected = model.selected;
  function submit(event: FormEvent): void {
    event.preventDefault();
    if (!title.trim() || !objective.trim() || !criteria.trim()) return;
    void model.create({ title: title.trim(), objective: objective.trim(), acceptance_criteria: criteria.split("\n").map((line) => line.trim()).filter(Boolean), workspace: workspace.trim() });
  }
  return <section className="super-workspace" aria-label={copy.title} aria-busy={model.busy}>
    <header className="super-header">
      <div><p className="super-eyebrow">SUPER NIUMA</p><h1>{copy.title}</h1><p>{copy.subtitle}</p></div>
      <div className="super-actions"><button type="button" onClick={onBack}>{copy.back}</button><button type="button" disabled={!client || model.busy} onClick={() => void model.refresh()}><RefreshCw aria-hidden="true" size={15} />{copy.refresh}</button></div>
    </header>
    <div className="super-scope"><ClipboardList aria-hidden="true" size={19} /><div><strong>{copy.intake}</strong><p>{copy.note}</p></div></div>
    {!client ? <p role="alert">{copy.unavailableBridge}</p> : null}
    {model.error ? <p className="super-error" role="alert">{copy[model.error]}</p> : null}
    <p className="super-notice" role="status">{model.notice ? copy[model.notice] : model.busy ? copy.loading : ""}</p>
    <div className="super-columns">
      <form className="super-card super-form super-intake-form" onSubmit={submit}>
        <h2>{copy.form}</h2>
        <div className="super-intake-fields">
        <div className="super-field"><label htmlFor="super-title">{copy.name}</label><input id="super-title" required maxLength={WORK_ORDER_LIMITS.title} value={title} onChange={(event) => setTitle(event.target.value)} /></div>
        <div className="super-field"><label htmlFor="super-workspace">{copy.workspace}</label><input id="super-workspace" maxLength={WORK_ORDER_LIMITS.workspace} value={workspace} onChange={(event) => setWorkspace(event.target.value)} aria-describedby="super-workspace-hint" />
        <small id="super-workspace-hint">{copy.workspaceHint}</small></div>
        <div className="super-field super-field-wide"><label htmlFor="super-objective">{copy.objective}</label><textarea id="super-objective" required maxLength={WORK_ORDER_LIMITS.objective} rows={3} value={objective} onChange={(event) => setObjective(event.target.value)} /></div>
        <div className="super-field super-field-wide"><label htmlFor="super-criteria">{copy.criteria}</label><textarea id="super-criteria" required maxLength={WORK_ORDER_LIMITS.criteria} rows={3} value={criteria} onChange={(event) => setCriteria(event.target.value)} /></div>

        </div>
        <button className="super-primary" disabled={!client || model.busy || !title.trim() || !objective.trim() || !criteria.trim()} type="submit">{model.busy ? copy.saving : copy.create}</button>
      </form>
      <div className="super-records">
        <section className="super-card"><h2>{copy.list} <small>{model.orders.length}</small></h2><small>{copy.limit}</small>
          {model.orders.length === 0 && !model.busy ? <p className="super-empty">{copy.empty}</p> : null}
          <ul className="super-order-list">{model.orders.map((order) => <li key={order.id}><button aria-pressed={selected?.id === order.id} type="button" onClick={() => model.setSelectedID(order.id)}><span>{order.title}</span><small>{copy[order.status]}</small></button></li>)}</ul>
        </section>
        <section className="super-card super-detail" aria-label={copy.criteriaTitle}>
          {selected ? <><h2>{selected.title}</h2><span className="super-badge">{copy[selected.status]}</span><p className="super-objective">{selected.objective}</p>
            <h3>{copy.criteriaTitle}</h3><ul>{selected.acceptance_criteria.map((criterion, index) => <li key={index}>{criterion}</li>)}</ul>
            <dl><dt>{copy.workspace}</dt><dd>{selected.workspace || copy.noWorkspace}</dd><dt>{copy.id}</dt><dd>{selected.id}</dd></dl>
            <div className="super-stages">{(["execution", "verification", "delivery"] as const).map((stage) => <div key={stage}><span>{copy[stage]}</span><strong>{copy.notStarted}</strong></div>)}</div>
            {platformBridge ? <small>{copy.progressInConsole}</small> : null}
            <button className="super-cancel" disabled={model.busy || selected.status === WORK_ORDER_STATUS.cancelled} type="button" onClick={() => void model.cancel(selected.id)}>{copy.cancel}</button>
          </> : <p className="super-empty">{copy.select}</p>}
        </section>
      </div>
    </div>
    <PlatformPanel key={platformBridge ? "platform" : "intake"} bridge={platformBridge} orders={model.orders} />
  </section>;
}
