import { Bot, MessageSquareText, RefreshCcw, Send, Square, TimerReset } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  cancelAgentTask,
  createAgentTask,
  listAgentTaskEvents,
  listAgentTasks,
  sendAgentTaskMessage,
  updateAgentTask
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import type { AgentTaskEventRecord, AgentTaskRecord, IdentityConfig } from "../lib/types";

type Props = {
  identity: IdentityConfig;
  selectedSessionId: number | null;
  onSelectSession: (id: number | null) => void;
  onStatus: (message: string) => void;
};

const terminalStatuses = new Set(["completed", "failed", "cancelled", "timeout"]);

export function AgentCockpit({ identity, selectedSessionId, onSelectSession, onStatus }: Props) {
  const { t } = useI18n();
  const [tasks, setTasks] = useState<AgentTaskRecord[]>([]);
  const [events, setEvents] = useState<AgentTaskEventRecord[]>([]);
  const [selectedTaskId, setSelectedTaskId] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [agentName, setAgentName] = useState("reviewer");
  const [description, setDescription] = useState("WebUI background agent");
  const [prompt, setPrompt] = useState("Review the current session and report progress.");
  const [message, setMessage] = useState("");
  const [resultJson, setResultJson] = useState('{"source":"webui"}');
  const [taskFilter, setTaskFilter] = useState("all");
  const [taskSearch, setTaskSearch] = useState("");
  const [eventFilter, setEventFilter] = useState("all");
  const [autoRefresh, setAutoRefresh] = useState(false);

  const selectedTask = useMemo(() => tasks.find((task) => task.id === selectedTaskId) || null, [selectedTaskId, tasks]);
  const filteredTasks = useMemo(() => {
    const needle = taskSearch.trim().toLowerCase();
    return tasks.filter((task) => {
      const status = task.status || "unknown";
      if (taskFilter !== "all" && status !== taskFilter) {
        return false;
      }
      if (!needle) {
        return true;
      }
      return [task.id, task.agent_name, task.description, task.model, task.trace_id, task.parent_session_id]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle));
    });
  }, [tasks, taskFilter, taskSearch]);
  const filteredEvents = useMemo(() => {
    if (eventFilter === "all") {
      return events;
    }
    return events.filter((event) => (event.event_type || "event") === eventFilter);
  }, [events, eventFilter]);
  const eventTypes = useMemo(() => Array.from(new Set(events.map((event) => event.event_type || "event"))), [events]);
  const runningCount = tasks.filter((task) => task.status === "running").length;
  const completedCount = tasks.filter((task) => terminalStatuses.has(task.status || "")).length;
  const selectedParentSession = Number(selectedTask?.parent_session_id || 0) || null;

  async function refreshAgents(nextSelectedTaskId = selectedTaskId) {
    setLoading(true);
    try {
      const taskItems = await listAgentTasks(identity, 50);
      setTasks(taskItems);
      const effectiveTaskId = nextSelectedTaskId && taskItems.some((task) => task.id === nextSelectedTaskId) ? nextSelectedTaskId : taskItems[0]?.id || null;
      setSelectedTaskId(effectiveTaskId);
      if (effectiveTaskId) {
        setEvents(await listAgentTaskEvents(identity, effectiveTaskId, 100));
      } else {
        setEvents([]);
      }
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refreshAgents();
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  useEffect(() => {
    if (!selectedTaskId) {
      setEvents([]);
      return;
    }
    let cancelled = false;
    listAgentTaskEvents(identity, selectedTaskId, 100)
      .then((items) => {
        if (!cancelled) {
          setEvents(items);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          onStatus(err instanceof Error ? err.message : String(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, selectedTaskId, onStatus]);

  useEffect(() => {
    if (!autoRefresh) {
      return;
    }
    const timer = window.setInterval(() => {
      void refreshAgents(selectedTaskId);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, selectedTaskId, identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  async function handleCreateTask() {
    try {
      const id = await createAgentTask(identity, {
        parent_session_id: selectedSessionId || undefined,
        agent_name: agentName,
        description,
        prompt,
        status: "running",
        model: identity.model,
        metadata_json: {
          source: "webui",
          background: true,
          created_at: new Date().toISOString()
        }
      });
      await refreshAgents(id);
      onStatus(t("agents.created", { id }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSendMessage() {
    if (!selectedTaskId) {
      return;
    }
    try {
      const id = await sendAgentTaskMessage(identity, selectedTaskId, {
        from_agent: "webui",
        content: message
      });
      setMessage("");
      await refreshAgents(selectedTaskId);
      onStatus(t("agents.messageSent", { id }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleUpdateStatus(status: "completed" | "failed") {
    if (!selectedTaskId) {
      return;
    }
    try {
      await updateAgentTask(identity, selectedTaskId, {
        status,
        result_json: parseJSONField(resultJson)
      });
      await refreshAgents(selectedTaskId);
      onStatus(t("agents.updated", { id: selectedTaskId }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleCancelTask() {
    if (!selectedTaskId) {
      return;
    }
    try {
      await cancelAgentTask(identity, selectedTaskId);
      await refreshAgents(selectedTaskId);
      onStatus(t("agents.cancelled", { id: selectedTaskId }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section className="panel inspector-panel agent-cockpit">
      <div className="panel-header">
        <div className="title-row">
          <Bot size={17} />
          <div>
            <h2>{t("tab.agents")}</h2>
            <p>{t("agents.subtitle", { running: runningCount, total: tasks.length })}</p>
          </div>
        </div>
        <div className="panel-header-actions">
          <label className="toggle-row compact-toggle">
            <input type="checkbox" checked={autoRefresh} onChange={(event) => setAutoRefresh(event.target.checked)} />
            {t("agents.autoRefresh")}
          </label>
          <button className="icon-button" onClick={() => void refreshAgents()} type="button" title={t("agents.refresh")}>
            <RefreshCcw size={16} />
          </button>
        </div>
      </div>

      {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .agent-summary is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
      <div className="agent-summary" role="group" aria-label={t("agents.summary")}>
        <Metric label={t("agents.total")} value={tasks.length} />
        <Metric label={t("agents.running")} value={runningCount} />
        <Metric label={t("agents.finished")} value={completedCount} />
        <Metric label={t("agents.events")} value={events.length} />
      </div>

      <div className="agent-workspace">
        <aside className="agent-list" aria-label={t("agents.tasks")}>
          <div className="agent-filters">
            <input value={taskSearch} onChange={(event) => setTaskSearch(event.target.value)} placeholder={t("agents.search")} />
            <select value={taskFilter} onChange={(event) => setTaskFilter(event.target.value)}>
              <option value="all">{t("chat.allStatuses")}</option>
              <option value="running">running</option>
              <option value="completed">completed</option>
              <option value="failed">failed</option>
              <option value="cancelled">cancelled</option>
            </select>
          </div>
          {filteredTasks.length === 0 ? <div className="empty-state compact">{loading ? t("agents.loading") : t("inspector.noRecords")}</div> : null}
          {filteredTasks.map((task) => (
            <button
              key={task.id}
              className={selectedTaskId === task.id ? "agent-task-card active" : "agent-task-card"}
              onClick={() => setSelectedTaskId(task.id)}
              type="button"
            >
              <span className={`trace-status-chip ${statusClass(task.status || "")}`}>{task.status || "unknown"}</span>
              <strong>{task.agent_name || `Agent ${task.id}`}</strong>
              <small>{task.description || task.model || `task ${task.id}`}</small>
              <span className="agent-task-meta">#{task.id}{task.parent_session_id ? ` · session ${task.parent_session_id}` : ""}</span>
            </button>
          ))}
        </aside>

        <div className="agent-detail">
          <div className="agent-detail-header">
            <div>
              <span>{t("agents.selected")}</span>
              <strong>{selectedTask ? selectedTask.agent_name || `Agent ${selectedTask.id}` : t("agents.none")}</strong>
            </div>
            <div className="agent-actions">
              {selectedParentSession ? (
                <button className="secondary-button" onClick={() => onSelectSession(selectedParentSession)} type="button">
                  <TimerReset size={15} />
                  {t("agents.openSession")}
                </button>
              ) : null}
              <button className="secondary-button danger" disabled={!selectedTaskId} onClick={handleCancelTask} type="button">
                <Square size={15} />
                {t("agents.cancel")}
              </button>
            </div>
          </div>

          <div className="agent-inspector-grid">
            <div>
              <span>{t("agents.model")}</span>
              <strong>{selectedTask?.model || "-"}</strong>
            </div>
            <div>
              <span>{t("agents.trace")}</span>
              <strong>{selectedTask?.trace_id || "-"}</strong>
            </div>
            <div>
              <span>{t("agents.parentSession")}</span>
              <strong>{selectedTask?.parent_session_id || "-"}</strong>
            </div>
            <div>
              <span>{t("agents.subagent")}</span>
              <strong>{selectedTask?.subagent_session_key || "-"}</strong>
            </div>
          </div>

          <div className="agent-json-panels">
            <div>
              <span>{t("agents.metadata")}</span>
              <pre>{formatPayload(selectedTask?.metadata_json)}</pre>
            </div>
            <div>
              <span>{t("agents.result")}</span>
              <pre>{formatPayload(selectedTask?.result_json)}</pre>
            </div>
          </div>

          <div className="agent-create-form compact-form">
            <label>
              {t("agents.agentName")}
              <input value={agentName} onChange={(event) => setAgentName(event.target.value)} />
            </label>
            <label>
              {t("agents.description")}
              <input value={description} onChange={(event) => setDescription(event.target.value)} />
            </label>
            <label>
              {t("agents.prompt")}
              <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={3} />
            </label>
            <button className="secondary-button" onClick={handleCreateTask} type="button">
              <Bot size={15} />
              {t("agents.create")}
            </button>
          </div>

          <div className="agent-control-grid">
            <div className="agent-message-box compact-form">
              <label>
                {t("agents.message")}
                <textarea value={message} onChange={(event) => setMessage(event.target.value)} rows={3} />
              </label>
              <button className="secondary-button" disabled={!selectedTaskId || message.trim() === ""} onClick={handleSendMessage} type="button">
                <Send size={15} />
                {t("agents.sendMessage")}
              </button>
            </div>
            <div className="agent-result-box compact-form">
              <label>
                {t("agents.resultJson")}
                <textarea value={resultJson} onChange={(event) => setResultJson(event.target.value)} rows={3} />
              </label>
              <div className="agent-status-buttons">
                <button className="secondary-button" disabled={!selectedTaskId} onClick={() => void handleUpdateStatus("completed")} type="button">
                  {t("agents.markCompleted")}
                </button>
                <button className="secondary-button danger" disabled={!selectedTaskId} onClick={() => void handleUpdateStatus("failed")} type="button">
                  {t("agents.markFailed")}
                </button>
              </div>
            </div>
          </div>

          {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .agent-event-stream is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
          <div className="agent-event-stream" role="group" aria-label={t("agents.events")}>
            <div className="agent-event-toolbar">
              <span>{t("agents.eventFilter")}</span>
              <select value={eventFilter} onChange={(event) => setEventFilter(event.target.value)}>
                <option value="all">{t("chat.allStatuses")}</option>
                {eventTypes.map((type) => (
                  <option key={type} value={type}>{type}</option>
                ))}
              </select>
            </div>
            {filteredEvents.length === 0 ? <div className="empty-state compact">{t("agents.noEvents")}</div> : null}
            {filteredEvents.map((event) => (
              <article key={event.id} className="agent-event-row">
                <MessageSquareText size={15} />
                <div>
                  <strong>{event.event_type || "event"}</strong>
                  <small>{formatTime(event.created_at)}{event.trace_id ? ` · ${event.trace_id}` : ""}</small>
                  <p>{formatPayload(event.payload_json)}</p>
                </div>
              </article>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function parseJSONField(value: string): Record<string, unknown> | string {
  const trimmed = value.trim();
  if (!trimmed) {
    return {};
  }
  try {
    return JSON.parse(trimmed) as Record<string, unknown>;
  } catch {
    return trimmed;
  }
}

function formatPayload(value?: string): string {
  if (!value) {
    return "";
  }
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
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
  if (raw === "completed") {
    return "ok";
  }
  if (raw === "failed" || raw === "cancelled") {
    return "fail";
  }
  if (raw === "running") {
    return "warn";
  }
  return "neutral";
}
