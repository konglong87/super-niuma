import { Flag, Loader2, Play, RefreshCcw, RotateCcw, Square } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  createGoal,
  getGoalPlan,
  listGoalEvidence,
  listGoalEvents,
  listGoals,
  resumeGoal,
  runGoalOnce,
  stopGoal
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import type { GoalEventRecord, GoalEvidence, GoalPlan, GoalRecord, IdentityConfig } from "../lib/types";

type Props = {
  identity: IdentityConfig;
  selectedSessionId: number | null;
  onStatus: (message: string) => void;
  onDataChanged: () => void;
};

const terminalStatuses = new Set(["complete", "failed", "stopped"]);

export function GoalWorkbench({ identity, selectedSessionId, onStatus, onDataChanged }: Props) {
  const { t } = useI18n();
  const [goals, setGoals] = useState<GoalRecord[]>([]);
  const [events, setEvents] = useState<GoalEventRecord[]>([]);
  const [plan, setPlan] = useState<GoalPlan | null>(null);
  const [evidence, setEvidence] = useState<GoalEvidence[]>([]);
  const [selectedGoalId, setSelectedGoalId] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(false);
  const [running, setRunning] = useState(false);
  const [objective, setObjective] = useState("Keep validating WebUI readiness until every evidence panel is green.");
  const [cwd, setCwd] = useState("/workspace");
  const [turnBudget, setTurnBudget] = useState(5);
  const [tokenBudget, setTokenBudget] = useState(20000);
  const [evaluator, setEvaluator] = useState("deterministic");

  const selectedGoal = useMemo(() => goals.find((goal) => goal.id === selectedGoalId) || null, [goals, selectedGoalId]);
  const filteredGoals = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return goals.filter((goal) => {
      const status = goal.status || "active";
      if (statusFilter !== "all" && status !== statusFilter) {
        return false;
      }
      if (!needle) {
        return true;
      }
      return [goal.id, goal.objective, goal.model, goal.cwd, goal.session_id, goal.last_reason, goal.last_next_action]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle));
    });
  }, [goals, query, statusFilter]);
  const activeCount = goals.filter((goal) => goal.status === "active").length;
  const blockedCount = goals.filter((goal) => goal.status === "blocked").length;
  const finishedCount = goals.filter((goal) => terminalStatuses.has(goal.status || "")).length;
  const tokenUsed = (selectedGoal?.input_tokens || 0) + (selectedGoal?.output_tokens || 0);
  const currentStep = useMemo(() => {
    const steps = plan?.steps || [];
    return steps.find((step) => step.id === plan?.current_step_id) || steps.find((step) => step.status === "active") || steps[0] || null;
  }, [plan]);
  const criteriaStats = useMemo(() => {
    const criteria = plan?.acceptance_criteria || [];
    const required = criteria.filter((criterion) => criterion.required);
    return {
      passed: criteria.filter((criterion) => criterion.status === "passed").length,
      total: criteria.length,
      requiredPassed: required.filter((criterion) => criterion.status === "passed").length,
      requiredTotal: required.length
    };
  }, [plan]);
  const openRiskCount = useMemo(() => (plan?.risks || []).filter((risk) => risk.status === "open" || risk.status === "escalated").length, [plan]);

  async function refresh(nextGoalId = selectedGoalId) {
    setLoading(true);
    try {
      const items = await listGoals(identity, { limit: 100 });
      setGoals(items);
      const effectiveGoalId = nextGoalId && items.some((goal) => goal.id === nextGoalId) ? nextGoalId : items[0]?.id || "";
      setSelectedGoalId(effectiveGoalId);
      if (effectiveGoalId) {
        await loadGoalContext(effectiveGoalId);
      } else {
        setEvents([]);
        setPlan(null);
        setEvidence([]);
      }
    } catch (err) {
      onStatus(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refresh();
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  useEffect(() => {
    if (!selectedGoalId) {
      setEvents([]);
      setPlan(null);
      setEvidence([]);
      return;
    }
    let cancelled = false;
    fetchGoalContext(identity, selectedGoalId, onStatus)
      .then((context) => {
        if (!cancelled) {
          setEvents(context.events);
          setPlan(context.plan);
          setEvidence(context.evidence);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          onStatus(errorMessage(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, selectedGoalId, onStatus]);

  async function loadGoalContext(goalId: string) {
    const context = await fetchGoalContext(identity, goalId, onStatus);
    setEvents(context.events);
    setPlan(context.plan);
    setEvidence(context.evidence);
  }

  async function handleCreateGoal() {
    if (objective.trim() === "") {
      onStatus(t("goals.objectiveRequired"));
      return;
    }
    try {
      const goal = await createGoal(identity, {
        objective: objective.trim(),
        cwd: cwd.trim() || undefined,
        session_id: selectedSessionId ? String(selectedSessionId) : undefined,
        model: identity.model,
        turn_budget: turnBudget,
        token_budget: tokenBudget
      });
      await refresh(goal.id);
      onDataChanged();
      onStatus(t("goals.created", { id: goal.id }));
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleStopGoal() {
    if (!selectedGoalId) {
      return;
    }
    try {
      const goal = await stopGoal(identity, selectedGoalId);
      await refresh(goal.id);
      onDataChanged();
      onStatus(t("goals.stopped", { id: goal.id }));
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleResumeGoal(force = false) {
    if (!selectedGoalId) {
      return;
    }
    try {
      const goal = await resumeGoal(identity, selectedGoalId, force);
      await refresh(goal.id);
      onDataChanged();
      onStatus(t("goals.resumed", { id: goal.id }));
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleRunGoal() {
    if (!selectedGoalId || running) {
      return;
    }
    setRunning(true);
    try {
      const result = await runGoalOnce(identity, selectedGoalId, evaluator);
      const goalID = result.goal?.id || selectedGoalId;
      await refresh(goalID);
      onDataChanged();
      onStatus(t("goals.ran", { id: goalID }));
    } catch (err) {
      onStatus(errorMessage(err));
    } finally {
      setRunning(false);
    }
  }

  return (
    <section className="panel inspector-panel goal-workbench">
      <div className="panel-header">
        <div className="title-row">
          <Flag size={17} />
          <div>
            <h2>{t("tab.goals")}</h2>
            <p>{t("goals.subtitle", { active: activeCount, total: goals.length })}</p>
          </div>
        </div>
        <div className="panel-header-actions">
          <button className="icon-button" onClick={() => void refresh()} type="button" title={t("goals.refresh")}>
            <RefreshCcw size={16} />
          </button>
        </div>
      </div>

      {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .agent-summary is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
      <div className="agent-summary goal-summary" role="group" aria-label={t("goals.summary")}>
        <Metric label={t("goals.total")} value={goals.length} />
        <Metric label={t("goals.active")} value={activeCount} />
        <Metric label={t("goals.blocked")} value={blockedCount} />
        <Metric label={t("goals.finished")} value={finishedCount} />
      </div>

      <div className="goal-workspace">
        <aside className="goal-list-panel">
          <div className="goal-filters">
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("goals.search")} />
            <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
              <option value="all">{t("chat.allStatuses")}</option>
              <option value="active">active</option>
              <option value="blocked">blocked</option>
              <option value="stopped">stopped</option>
              <option value="complete">complete</option>
              <option value="failed">failed</option>
            </select>
          </div>
          <div className="goal-list">
            {filteredGoals.length === 0 ? <div className="empty-state compact">{loading ? t("goals.loading") : t("goals.noGoals")}</div> : null}
            {filteredGoals.map((goal) => (
              <button
                key={goal.id}
                className={goal.id === selectedGoalId ? "goal-row active" : "goal-row"}
                onClick={() => setSelectedGoalId(goal.id)}
                type="button"
              >
                <span className={`trace-status-chip ${statusClass(goal.status || "")}`}>{goal.status || "active"}</span>
                <strong>{goal.objective || goal.id}</strong>
                <small>{goal.id} · {goal.turns_used || 0}/{goal.turn_budget || "-"}</small>
              </button>
            ))}
          </div>
        </aside>

        <div className="goal-detail">
          <section className="goal-create-form compact-form">
            <label>
              {t("goals.objective")}
              <textarea value={objective} onChange={(event) => setObjective(event.target.value)} rows={3} />
            </label>
            <label>
              {t("goals.cwd")}
              <input value={cwd} onChange={(event) => setCwd(event.target.value)} />
            </label>
            <label>
              {t("goals.turnBudget")}
              <input type="number" min={1} value={turnBudget} onChange={(event) => setTurnBudget(Number(event.target.value) || 1)} />
            </label>
            <label>
              {t("goals.tokenBudget")}
              <input type="number" min={1} value={tokenBudget} onChange={(event) => setTokenBudget(Number(event.target.value) || 1)} />
            </label>
            <button className="secondary-button" onClick={handleCreateGoal} type="button">
              <Flag size={15} />
              {t("goals.create")}
            </button>
          </section>

          <section className="goal-selected-panel">
            <div className="goal-selected-header">
              <div>
                <span>{t("goals.selected")}</span>
                <strong>{selectedGoal ? selectedGoal.objective || selectedGoal.id : t("goals.none")}</strong>
              </div>
              <div className="agent-actions">
                <select aria-label={t("goals.evaluator")} value={evaluator} onChange={(event) => setEvaluator(event.target.value)}>
                  <option value="deterministic">deterministic</option>
                  <option value="model">model</option>
                </select>
                <button className="secondary-button" disabled={!selectedGoalId || running} onClick={() => void handleRunGoal()} type="button">
                  {running ? <Loader2 className="spin" size={15} /> : <Play size={15} />}
                  {t("goals.runOnce")}
                </button>
                <button className="secondary-button" disabled={!selectedGoalId} onClick={() => void handleResumeGoal(true)} type="button">
                  <RotateCcw size={15} />
                  {t("goals.resume")}
                </button>
                <button className="secondary-button danger" disabled={!selectedGoalId} onClick={handleStopGoal} type="button">
                  <Square size={15} />
                  {t("goals.stop")}
                </button>
              </div>
            </div>
            <div className="goal-meta-grid">
              <Metric label={t("goals.status")} value={selectedGoal?.status || "-"} />
              <Metric label={t("goals.turns")} value={`${selectedGoal?.turns_used || 0}/${selectedGoal?.turn_budget || "-"}`} />
              <Metric label={t("goals.tokens")} value={`${tokenUsed}/${selectedGoal?.token_budget || "-"}`} />
              <Metric label={t("goals.criteria")} value={`${criteriaStats.passed}/${criteriaStats.total}`} />
              <Metric label={t("goals.evidence")} value={evidence.length} />
              <Metric label={t("goals.openRisks")} value={openRiskCount} />
              <Metric label={t("goals.events")} value={events.length} />
            </div>
            <dl className="goal-facts">
              <div>
                <dt>{t("goals.currentStep")}</dt>
                <dd>{currentStep ? `${currentStep.title || currentStep.id} · ${currentStep.status || "pending"}` : "-"}</dd>
              </div>
              <div>
                <dt>{t("goals.requiredCriteria")}</dt>
                <dd>{`${criteriaStats.requiredPassed}/${criteriaStats.requiredTotal || criteriaStats.total}`}</dd>
              </div>
              <div>
                <dt>{t("goals.id")}</dt>
                <dd>{selectedGoal?.id || "-"}</dd>
              </div>
              <div>
                <dt>{t("goals.session")}</dt>
                <dd>{selectedGoal?.session_id || "-"}</dd>
              </div>
              <div>
                <dt>{t("goals.model")}</dt>
                <dd>{selectedGoal?.model || identity.model}</dd>
              </div>
              <div>
                <dt>{t("goals.nextAction")}</dt>
                <dd>{selectedGoal?.last_next_action || "-"}</dd>
              </div>
              <div>
                <dt>{t("goals.reason")}</dt>
                <dd>{selectedGoal?.last_reason || selectedGoal?.error || "-"}</dd>
              </div>
              <div>
                <dt>{t("goals.checkpoint")}</dt>
                <dd>{selectedGoal?.last_checkpoint || "-"}</dd>
              </div>
            </dl>
          </section>

          <section className="goal-plan-panel" aria-label={t("goals.planEvidence")}>
            <div className="goal-plan-column">
              <h3>{t("goals.plan")}</h3>
              {plan?.summary ? <p>{plan.summary}</p> : null}
              {currentStep ? (
                <article className="goal-insight-row">
                  <span className={`trace-status-chip ${statusClass(currentStep.status || "")}`}>{currentStep.status || "pending"}</span>
                  <div>
                    <strong>{currentStep.title || currentStep.id}</strong>
                    <small>{currentStep.id || t("goals.currentStep")}</small>
                    {currentStep.rationale ? <p>{currentStep.rationale}</p> : null}
                  </div>
                </article>
              ) : (
                <div className="empty-state compact">{t("goals.noPlan")}</div>
              )}
              {(plan?.acceptance_criteria || []).slice(0, 4).map((criterion) => (
                <article key={criterion.id || criterion.description} className="goal-insight-row">
                  <span className={`trace-status-chip ${statusClass(criterion.status || "")}`}>{criterion.status || "pending"}</span>
                  <div>
                    <strong>{criterion.description || criterion.id}</strong>
                    <small>{criterion.required ? t("goals.required") : t("goals.optional")}</small>
                  </div>
                </article>
              ))}
            </div>
            <div className="goal-plan-column">
              <h3>{t("goals.recentEvidence")}</h3>
              {evidence.length === 0 ? <div className="empty-state compact">{t("goals.noEvidence")}</div> : null}
              {evidence.slice(0, 5).map((item) => (
                <article key={item.id} className="goal-insight-row">
                  <span className={`trace-status-chip ${item.passed ? "ok" : "fail"}`}>{item.type || "manual"}</span>
                  <div>
                    <strong>{item.summary || item.id}</strong>
                    <small>{formatEvidenceMeta(item)}</small>
                    {evidenceCapabilitySummary(item) ? <p>{evidenceCapabilitySummary(item)}</p> : null}
                  </div>
                </article>
              ))}
            </div>
          </section>

          <section className="goal-event-stream" aria-label={t("goals.events")}>
            {events.length === 0 ? <div className="empty-state compact">{t("goals.noEvents")}</div> : null}
            {events.map((event) => (
              <article key={event.id} className="agent-event-row">
                <Flag size={15} />
                <div>
                  <strong>{event.type || "event"}</strong>
                  <small>{formatTime(event.created_at)}{event.status ? ` · ${event.status}` : ""}</small>
                  <p>{event.message || event.reason || event.next_action || event.checkpoint || event.id}</p>
                </div>
              </article>
            ))}
          </section>
        </div>
      </div>
    </section>
  );
}

async function fetchGoalContext(
  identity: IdentityConfig,
  goalId: string,
  onStatus: (message: string) => void
): Promise<{ events: GoalEventRecord[]; plan: GoalPlan | null; evidence: GoalEvidence[] }> {
  const [eventsResult, planResult, evidenceResult] = await Promise.allSettled([
    listGoalEvents(identity, goalId, 100),
    getGoalPlan(identity, goalId),
    listGoalEvidence(identity, goalId, 20)
  ]);
  if (eventsResult.status === "rejected") {
    onStatus(errorMessage(eventsResult.reason));
  }
  if (planResult.status === "rejected" && !isNotFoundError(planResult.reason)) {
    onStatus(errorMessage(planResult.reason));
  }
  if (evidenceResult.status === "rejected" && !isNotFoundError(evidenceResult.reason)) {
    onStatus(errorMessage(evidenceResult.reason));
  }
  return {
    events: eventsResult.status === "fulfilled" ? eventsResult.value : [],
    plan: planResult.status === "fulfilled" ? planResult.value : null,
    evidence: evidenceResult.status === "fulfilled" ? evidenceResult.value : []
  };
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function formatTime(value?: string): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function statusClass(status: string): string {
  const raw = status.toLowerCase();
  if (raw === "complete" || raw === "done" || raw === "passed" || raw === "available" || raw === "mitigated") {
    return "ok";
  }
  if (raw === "active" || raw === "blocked" || raw === "pending" || raw === "open" || raw === "unknown") {
    return "warn";
  }
  if (raw === "failed" || raw === "stopped" || raw === "missing" || raw === "escalated") {
    return "fail";
  }
  return "neutral";
}

function formatEvidenceMeta(item: GoalEvidence): string {
  const parts = [item.id, item.passed ? "passed" : "failed", formatTime(item.created_at)].filter(Boolean);
  return parts.join(" · ");
}

function evidenceCapabilitySummary(item: GoalEvidence): string {
  const payload = payloadObject(item.payload as unknown);
  const loop = objectValue(payload.capability_loop);
  if (!loop) {
    return "";
  }
  const status = stringValue(payload.agent_status);
  const source = evidenceSourceLabel(stringValue(payload.evidence_source));
  const partial = payload.partial_evidence === true ? "partial" : "";
  const evidence = firstCapabilityLoopText(loop.evidence);
  const unknown = firstCapabilityLoopText(loop.unknowns);
  const risk = firstCapabilityLoopText(loop.risks);
  const verification = firstCapabilityLoopText(loop.verification);
  const next = firstCapabilityLoopText(loop.next_action);
  const parts = [
    source ? `source: ${source}` : "",
    status ? `status: ${status}` : "",
    partial,
    evidence ? `evidence: ${truncateOneLine(evidence, 88)}` : "",
    unknown ? `unknown: ${truncateOneLine(unknown, 72)}` : "",
    risk ? `risk: ${truncateOneLine(risk, 72)}` : "",
    verification ? `verification: ${truncateOneLine(verification, 72)}` : "",
    next ? `next: ${truncateOneLine(next, 88)}` : ""
  ].filter(Boolean);
  return parts.join(" | ");
}

function evidenceSourceLabel(source: string): string {
  if (source === "terminal_agent_task_store") {
    return "task_store";
  }
  if (source === "agent_get") {
    return "agent_get";
  }
  return truncateOneLine(source, 32);
}

function payloadObject(value: unknown): Record<string, unknown> {
  if (Array.isArray(value) && value.every((item) => typeof item === "number")) {
    try {
      return parseJSONObject(new TextDecoder().decode(new Uint8Array(value)));
    } catch {
      return {};
    }
  }
  if (typeof value === "string") {
    return parseJSONObject(value);
  }
  return objectValue(value) || {};
}

function parseJSONObject(value: string): Record<string, unknown> {
  try {
    return objectValue(JSON.parse(value)) || {};
  } catch {
    return {};
  }
}

function objectValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function firstText(value: unknown): string {
  if (typeof value === "string") {
    return value.trim();
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const text = firstText(item);
      if (text) {
        return text;
      }
    }
    return "";
  }
  const object = objectValue(value);
  if (!object) {
    return "";
  }
  return firstText(object.summary) || firstText(object.description) || firstText(object.text) || firstText(object.value);
}

function firstCapabilityLoopText(value: unknown): string {
  if (Array.isArray(value)) {
    for (const item of value) {
      const text = firstCapabilityLoopText(item);
      if (text) {
        return text;
      }
    }
    return "";
  }
  const text = firstText(value);
  return isCapabilityLoopPlaceholder(text) ? "" : text;
}

function isCapabilityLoopPlaceholder(value: string): boolean {
  const normalized = value.trim().replace(/[.。]+$/u, "").toLowerCase();
  return normalized === "" ||
    normalized === "none" ||
    normalized === "none observed" ||
    normalized === "not observed" ||
    normalized === "n/a" ||
    normalized === "na" ||
    normalized === "not applicable" ||
    normalized === "no explicit assumptions reported" ||
    normalized === "no explicit unknowns reported" ||
    normalized === "no explicit verification reported";
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function truncateOneLine(value: string, limit: number): string {
  const line = value.replace(/\s+/g, " ").trim();
  if (line.length <= limit) {
    return line;
  }
  return `${line.slice(0, Math.max(0, limit - 3))}...`;
}

function isNotFoundError(err: unknown): boolean {
  const object = objectValue(err);
  return object?.status === 404;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
