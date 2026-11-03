import { useEffect, useRef, useState, type ReactNode } from "react";
import type { WorkflowClient } from "./client";
import { useWorkflowText, type WorkflowWord } from "./i18n";
import { TERMINAL_STATUSES, WORKFLOW_FAILURE_CODES, type Failure, type Usage, type WorkflowEventType, type WorkflowStatus } from "./types";
import { message, useWorkflowRun } from "./useWorkflowRun";

const EVENT_LABELS: Record<WorkflowEventType, WorkflowWord> = {
  run_queued: "eventQueued", run_started: "eventStarted", task_created: "eventTaskCreated",
  task_waiting_dependency: "eventWaiting", agent_started: "eventAgentStarted", agent_output_delta: "eventOutput",
  agent_phase: "eventPhase", agent_completed: "eventAgentCompleted", agent_failed: "eventAgentFailed",
  task_retry_scheduled: "eventRetry", run_cancel_requested: "eventCancelRequested", run_cancelled: "eventCancelled",
  task_cancelled: "eventTaskCancelled", run_completed: "eventCompleted", run_failed: "eventFailed",
  run_timed_out: "eventTimedOut", run_interrupted: "eventInterrupted", run_recovered: "eventRecovered",
};

export function WorkflowRunDetail({ client, runID }: { client: WorkflowClient; runID: string }) {
  const text = useWorkflowText();
  const { state, connection, error, reload, accept } = useWorkflowRun(client, runID);
  const [busy, setBusy] = useState(false);
  const [operationError, setOperationError] = useState("");
  const run = state.run;
  const status = state.status ?? run?.status;
  const definitions = run?.plan.definition.nodes ?? [];
  const nodeStatus = (id: string): WorkflowStatus => state.nodes[id]?.status ?? run?.nodes?.find((item) => item.node_id === id)?.status ?? "pending";
  const completed = definitions.filter((node) => nodeStatus(node.id) === "completed").length;
  const operate = async (operation: "cancel" | "recover") => {
    if (!run) return;
    setBusy(true); setOperationError("");
    try { accept(operation === "cancel" ? await client.cancel(runID) : await client.recover(runID, run.revision)); reload(); }
    catch (reason) { setOperationError(message(reason)); reload(); }
    finally { setBusy(false); }
  };
  return <section className="workflow-run" aria-label={run?.name || runID}>
    <header className="workflow-card workflow-run-summary">
      <div className="workflow-run-title"><div><p className="workflow-eyebrow">TEAM / RUN</p><h2>{run?.name || runID}</h2></div>{status ? <span className="workflow-status" data-status={status}>{text(status)}</span> : null}</div>
      {run ? <div className="workflow-progress"><div><strong>{text("progress").replace("{done}", String(completed)).replace("{total}", String(definitions.length))}</strong><span className="workflow-connection" data-connection={connection}>{text(connection)}</span></div><div className="workflow-progress-track" role="progressbar" aria-label={text("steps")} aria-valuenow={completed} aria-valuemin={0} aria-valuemax={definitions.length}><span style={{ width: `${definitions.length ? (completed / definitions.length) * 100 : 0}%` }} /></div></div> : <p role="status">{text("loading")}</p>}
      <div className="workflow-run-toolbar"><div className="workflow-actions"><button onClick={reload} type="button">{text("retry")}</button>{run && status && !TERMINAL_STATUSES.has(status) ? <button disabled={busy || status === "cancel_requested"} onClick={() => { void operate("cancel"); }} type="button">{text("cancel")}</button> : null}{run && status === "interrupted" ? <button className="workflow-primary" disabled={busy} onClick={() => { void operate("recover"); }} type="button">{text("recover")}</button> : null}</div><details className="workflow-technical"><summary>{text("technicalDetails")}</summary><small>{runID} {run ? `· ${text("revision")} ${run.revision} · epoch ${run.epoch}` : ""}</small></details></div>
      {connection === "offline" || connection === "readback" ? <p className="workflow-muted" role="status">{text("readbackHint")}</p> : null}
      {error || operationError ? <p role="alert">{operationError || error}</p> : null}
      {run ? <><div className="workflow-run-facts"><div className="workflow-run-goal"><h3>{text("input")}</h3><p>{run.input}</p></div><div className="workflow-run-tokens"><UsageView usage={run.usage} uncertainTokens={run.uncertain_tokens} /></div></div><FailureView failure={run.failure} /></> : null}
    </header>
    {run ? <div className="workflow-run-grid"><section className="workflow-run-steps" aria-label={text("steps")}><div className="workflow-section-heading"><h2>{text("steps")}</h2><span className="workflow-muted">{completed}/{definitions.length}</span></div><ol className="workflow-step-list">{definitions.map((definition, index) => {
      const node = (run.nodes ?? []).find((item) => item.node_id === definition.id);
      const projection = state.nodes[definition.id];
      const agent = (run.plan.agents ?? []).find((item) => item.ref === definition.agent_ref);
      const currentStatus = nodeStatus(definition.id);
      return <li className="workflow-card workflow-run-step" data-status={currentStatus} key={definition.id}>
        <div className="workflow-step-head"><span className="workflow-step-number">{index + 1}</span><div><h3>{agent?.name ?? definition.agent_ref}</h3><p className="workflow-muted">{definition.instruction}</p></div><span className="workflow-status" data-status={currentStatus}>{text(currentStatus)}</span></div>
        {definition.depends_on.length ? <p className="workflow-dependency-note">{text("dependencies")}: {definition.depends_on.map((id) => { const position = definitions.findIndex((item) => item.id === id); return position >= 0 ? `${text("step")} ${position + 1}` : id; }).join(", ")}</p> : null}
        {node?.output ? <p className="workflow-output-preview">{node.output.slice(0, 160)}{node.output.length > 160 ? "…" : ""}</p> : null}
        <FailureView failure={node?.failure} />{node?.next_retry_at ? <p className="workflow-muted">{text("nextRetry")}: {new Date(node.next_retry_at).toLocaleString()}</p> : null}
        {(node?.attempts ?? []).map((attempt) => <AttemptDisclosure key={`${attempt.epoch}:${attempt.number}`} active={attempt.number === projection?.attempt && (currentStatus === "running" || currentStatus === "failed" || currentStatus === "retry_wait")} label={`${text("attempt")} ${attempt.number} · ${text(projection?.attempts[attempt.number]?.status ?? attempt.status)} · ${[attempt.provider, attempt.model].filter(Boolean).join(" ")}`}><div className="workflow-attempt-body"><UsageView usage={attempt.usage} known={attempt.usage_known} uncertainTokens={attempt.usage_known === false ? attempt.budget_reserved : undefined} /><FailureView failure={projection?.attempts[attempt.number]?.failure ?? attempt.failure} />{projection?.attempts[attempt.number]?.text ? <section aria-label={text("output")}><pre>{projection.attempts[attempt.number].text}</pre></section> : null}</div></AttemptDisclosure>)}
        {projection && !node?.attempts?.some((attempt) => attempt.number === projection.attempt) && projection.attempt > 0 ? <div className="workflow-attempt-body"><h4>{text("attempt")} {projection.attempt}</h4><FailureView failure={projection.attempts[projection.attempt]?.failure} /><pre>{projection.attempts[projection.attempt]?.text}</pre></div> : null}
        {node?.output && !projection?.attempt ? <details className="workflow-attempt"><summary>{text("output")}</summary><pre>{node.output}</pre></details> : null}
        <details className="workflow-technical"><summary>{text("technicalDetails")}</summary><small>{definition.id}</small></details>
      </li>;
    })}</ol></section>
    <aside className="workflow-run-aside"><section aria-label={text("artifacts")}><div className="workflow-section-heading"><h2>{text("artifacts")}</h2><span className="workflow-muted">{(run.artifacts ?? []).length}</span></div>{!(run.artifacts ?? []).length ? <p className="workflow-muted">{text("noArtifacts")}</p> : (run.artifacts ?? []).map((artifact) => <details className="workflow-card workflow-artifact" key={artifact.id}><summary>{text("step")} {definitions.findIndex((item) => item.id === artifact.node_id) + 1} · {text("attempt")} {artifact.attempt_no} · {artifact.kind}</summary><pre>{artifact.content}</pre><details className="workflow-technical"><summary>{text("technicalDetails")}</summary><small>{artifact.id} · SHA256 {artifact.sha256}</small></details></details>)}</section>
      <details className="workflow-card workflow-event-panel"><summary>{text("timeline")} · {state.events.length}</summary>{!state.events.length ? <p>{text("noEvents")}</p> : <ol className="workflow-timeline">{state.events.map((event) => <li key={event.sequence}><div><strong>{text(EVENT_LABELS[event.type])}</strong><time dateTime={event.created_at}>{new Date(event.created_at).toLocaleTimeString()}</time></div><small>{event.node_id} {event.attempt_no ? `· ${text("attempt")} ${event.attempt_no}` : ""} {event.status ? `· ${text(event.status)}` : ""}</small>{event.text ? <pre>{event.text}</pre> : null}<FailureView failure={event.failure} /><code className="workflow-event-code">#{event.sequence} · {event.type}</code></li>)}</ol>}</details>
    </aside></div> : null}
  </section>;
}
function AttemptDisclosure({ active, label, children }: { active: boolean; label: string; children: ReactNode }) {
  const ref = useRef<HTMLDetailsElement>(null);
  // Open the active attempt once. Streaming readbacks must not collapse a panel the user opened.
  useEffect(() => { if (active && ref.current) ref.current.open = true; }, [active]);
  return <details ref={ref} className="workflow-attempt"><summary>{label}</summary>{children}</details>;
}
function UsageView({ usage, known, uncertainTokens }: { usage: Usage; known?: boolean; uncertainTokens?: number }) {
  const text = useWorkflowText();
  return <><p className="workflow-muted workflow-usage-caption">{text(known === false ? "unknownUsage" : "actualUsage")}</p><dl className="workflow-usage" aria-label={text("usage")}>{([ ["input_tokens", "inputTokens"], ["output_tokens", "outputTokens"], ["cache_read_tokens", "cacheRead"], ["cache_write_tokens", "cacheWrite"] ] as const).map(([key, label]) => <div key={key}><dt>{text(label)}</dt><dd>{known === false ? text("unknownValue") : usage?.[key] ?? 0}</dd></div>)}</dl>{uncertainTokens !== undefined && uncertainTokens > 0 ? <p className="workflow-muted" role="status">{text("uncertainUsage").replace("{tokens}", String(uncertainTokens))}</p> : null}</>;
}
function FailureView({ failure }: { failure?: Failure }) {
  const text = useWorkflowText();
  if (!failure) return null;
  const cancelled = failure.code === WORKFLOW_FAILURE_CODES.cancelled;
  return <p className={cancelled ? "workflow-failure workflow-cancelled" : "workflow-failure"} role={cancelled ? "status" : "alert"}><strong>{failure.code}</strong>: {failure.message} · {text(failure.retryable ? "retryable" : "notRetryable")}</p>;
}
