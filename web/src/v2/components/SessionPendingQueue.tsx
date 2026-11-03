import { AlertCircle, ArrowUp, Check, ChevronDown, ChevronUp, ListOrdered, MessageSquarePlus, Pause, Pencil, Play, RefreshCw, Trash2, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { PendingInputRecord, PendingInputSideChatResponse } from "../../lib/types";
import { pendingQueueQueryKey, useSessionPendingQueue, type PendingQueueAction, type PendingQueueScope } from "../api/useSessionPendingQueue";
import "./SessionPendingQueue.css";

const copy = {
  en: { title: "Pending inputs", manage: "Manage pending inputs", queued: "queued", failed: "Failed", empty: "No pending inputs", enabled: "Queue enabled", edit: "Edit pending input", content: "Message", direction: "Direction", save: "Save changes", cancel: "Cancel editing", move: "Move up", remove: "Delete pending input", retry: "Retry pending input", sideChat: "Open side chat", refresh: "Refresh queue", error: "The queue could not be updated. Refresh and try again.", loading: "Loading queue", attachments: "attachment(s)" },
  zh: { title: "待发送消息", manage: "管理待发送消息", queued: "条消息排队中", failed: "失败", empty: "暂无待发送消息", enabled: "启用队列", edit: "编辑待发送消息", content: "消息", direction: "执行方向", save: "保存修改", cancel: "取消编辑", move: "上移", remove: "删除待发送消息", retry: "重试待发送消息", sideChat: "打开旁路会话", refresh: "刷新队列", error: "队列更新失败，请刷新后重试。", loading: "正在加载队列", attachments: "个附件" }
};

export type SessionPendingQueueProps = PendingQueueScope & {
  onOpenSession?: (result: PendingInputSideChatResponse) => Promise<void> | void;
  onBusyChange?: (busy: boolean) => void;
};

export function PendingQueueSettingsButton(props: PendingQueueScope): JSX.Element | null {
  if (!props.sessionRef.startsWith("tenant:") || !Number.isSafeInteger(props.taskID) || Number(props.taskID) <= 0) return null;
  return <QueueSettingsButton key={JSON.stringify(pendingQueueQueryKey(props))} {...props} />;
}

function QueueSettingsButton(props: PendingQueueScope): JSX.Element {
  const { language } = useI18n();
  const queue = useSessionPendingQueue(props, true, false);
  const enabled = queue.settings.data?.enabled;
  const failed = queue.settings.isError || queue.mutation.isError;
  const label = failed ? copy[language].refresh : enabled === undefined ? copy[language].loading : enabled ? (language === "zh" ? "暂停队列" : "Pause queue") : (language === "zh" ? "恢复队列" : "Resume queue");
  return <button aria-label={label} disabled={queue.mutation.isPending || (!failed && enabled === undefined)} onClick={() => {
    if (failed) { queue.mutation.reset(); void queue.refresh(); }
    else void queue.mutation.mutateAsync({ kind: "enable", enabled: !enabled }).catch(() => undefined);
  }} role="menuitem" type="button">{failed ? <RefreshCw size={16} aria-hidden="true" /> : enabled ? <Pause size={16} aria-hidden="true" /> : <Play size={16} aria-hidden="true" />}<span>{label}</span></button>;
}

export function SessionPendingQueue(props: SessionPendingQueueProps): JSX.Element | null {
  if (!props.sessionRef.startsWith("tenant:") || !Number.isSafeInteger(props.taskID) || Number(props.taskID) <= 0) return null;
  // Remount transient editing/error state when either the identity or session changes.
  return <PendingQueue key={JSON.stringify(pendingQueueQueryKey(props))} {...props} />;
}

function PendingQueue(props: SessionPendingQueueProps): JSX.Element | null {
  const { language } = useI18n();
  const labels = copy[language];
  const [expanded, setExpanded] = useState(false);
  const [editing, setEditing] = useState<{ id: string; content: string; direction: string } | null>(null);
  const [actionFailed, setActionFailed] = useState(false);
  const actionLock = useRef(false);
  const panelID = useId();
  const queue = useSessionPendingQueue(props, expanded);
  const records = (queue.items.data ?? []).filter((item) => item.status === "queued" || item.status === "failed");
  const queued = records.filter((item) => item.status === "queued");
  const failureCount = records.filter((item) => item.status === "failed").length;
  const failed = actionFailed;
  const queueUnavailable = queue.items.isError;
  const busy = queue.mutation.isPending;
  const summary = `${queued.length} ${labels.queued}`;
  const queueBusy = queue.items.isPending || queueUnavailable || (queue.items.isSuccess && (queue.items.data ?? []).some((item) => item.status === "queued" || item.status === "running"));

  useEffect(() => { props.onBusyChange?.(queueBusy); }, [props.onBusyChange, queueBusy]);
  useEffect(() => {
    if (queued.length === 0 && failureCount === 0 && !failed) {
      setExpanded(false);
      setEditing(null);
    }
  }, [queued.length, failureCount, failed]);

  async function perform(action: PendingQueueAction): Promise<void> {
    if (actionLock.current) return;
    actionLock.current = true;
    setActionFailed(false);
    try {
      const result = await queue.mutation.mutateAsync(action);
      if (action.kind === "edit") setEditing(null);
      if (action.kind === "side-chat" && result && "source_pending_input_id" in result) await props.onOpenSession?.(result);
    } catch {
      setActionFailed(true);
    } finally {
      actionLock.current = false;
    }
  }

  function startEditing(item: PendingInputRecord): void {
    setEditing({ id: item.id, content: item.content, direction: item.direction ?? "" });
  }

  if (queued.length === 0 && failureCount === 0 && !failed && !queueUnavailable) return null;

  return <section className="webui2-pending-queue" aria-label={labels.title}>
    <div className="webui2-pending-queue-summary">
      {queued.length > 0 && <button className="webui2-pending-queue-toggle" type="button" title={labels.manage} aria-label={labels.manage} aria-expanded={expanded} aria-controls={panelID} onClick={() => setExpanded(!expanded)}>
        <ListOrdered size={16} aria-hidden="true" />
        <span aria-live="polite">{summary}</span>
        {expanded ? <ChevronUp size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
      </button>}
      {(failed || failureCount > 0 || queueUnavailable) && <button aria-controls={panelID} aria-expanded={expanded} aria-label={queueUnavailable ? labels.refresh : language === "zh" ? "管理失败消息" : "Manage failed inputs"} className="webui2-pending-failure-toggle" onClick={() => setExpanded(!expanded)} title={queueUnavailable ? labels.refresh : labels.failed} type="button"><AlertCircle size={15} aria-hidden="true" /><span>{queueUnavailable ? labels.error : `${labels.failed}${failureCount > 0 ? ` ${failureCount}` : ""}`}</span></button>}
    </div>
    {expanded && <div id={panelID} className="webui2-pending-queue-panel">
      <div className="webui2-pending-queue-toolbar">
        <span>{labels.title}</span>
        <label><input type="checkbox" role="switch" aria-checked={queue.settings.data?.enabled ?? false} checked={queue.settings.data?.enabled ?? false} disabled={busy || !queue.settings.data || queue.settings.isError} onChange={(event) => void perform({ kind: "enable", enabled: event.target.checked })} />{labels.enabled}</label>
        <button type="button" title={labels.refresh} aria-label={labels.refresh} disabled={busy || queue.items.isFetching} onClick={() => { setActionFailed(false); void queue.refresh(); }}><RefreshCw size={15} aria-hidden="true" /></button>
      </div>
      {(failed || queueUnavailable) && <p className="webui2-pending-error" role="alert">{labels.error}</p>}
      {queue.items.isPending ? <p role="status">{labels.loading}</p> : records.length === 0 && !queue.items.isError ? <p>{labels.empty}</p> : null}
      <ol className="webui2-pending-queue-list">
        {records.map((item) => <li key={item.id} className="webui2-pending-queue-item" data-status={item.status}>
          {editing?.id === item.id ? <form className="webui2-pending-editor" onSubmit={(event) => { event.preventDefault(); void perform({ kind: "edit", ...editing, content: editing.content.trim(), direction: editing.direction.trim() }); }}>
            <label>{labels.content}<textarea aria-label={labels.content} value={editing.content} onChange={(event) => setEditing({ ...editing, content: event.target.value })} disabled={busy} rows={3} /></label>
            <label>{labels.direction}<textarea aria-label={labels.direction} value={editing.direction} onChange={(event) => setEditing({ ...editing, direction: event.target.value })} disabled={busy} rows={2} /></label>
            <div className="webui2-pending-item-actions">
              <button type="submit" aria-label={labels.save} title={labels.save} disabled={busy || (!editing.content.trim() && !item.attachments?.length)}><Check size={15} aria-hidden="true" /></button>
              <button type="button" aria-label={labels.cancel} title={labels.cancel} disabled={busy} onClick={() => setEditing(null)}><X size={15} aria-hidden="true" /></button>
            </div>
          </form> : <>
            <div className="webui2-pending-item-body"><p>{item.content}</p>{item.direction && <p className="webui2-pending-direction">{labels.direction}: {item.direction}</p>}{!!item.attachments?.length && <small>{item.attachments.length} {labels.attachments}</small>}{item.status === "failed" && <small className="webui2-pending-error">{labels.failed}{item.error_code ? `: ${item.error_code}` : ""}</small>}</div>
            <div className="webui2-pending-item-actions">
              <button type="button" aria-label={labels.move} title={labels.move} disabled={busy || item.status !== "queued" || queued[0]?.id === item.id} onClick={() => void perform({ kind: "move", id: item.id })}><ArrowUp size={15} aria-hidden="true" /></button>
              <button type="button" aria-label={labels.edit} title={labels.edit} disabled={busy} onClick={() => startEditing(item)}><Pencil size={15} aria-hidden="true" /></button>
              {item.status === "failed" && <button type="button" aria-label={labels.retry} title={labels.retry} disabled={busy} onClick={() => void perform({ kind: "retry", id: item.id })}><RefreshCw size={15} aria-hidden="true" /></button>}
              {props.onOpenSession && <button type="button" aria-label={labels.sideChat} title={labels.sideChat} disabled={busy} onClick={() => void perform({ kind: "side-chat", id: item.id })}><MessageSquarePlus size={15} aria-hidden="true" /></button>}
              <button type="button" aria-label={labels.remove} title={labels.remove} disabled={busy} onClick={() => void perform({ kind: "delete", id: item.id })}><Trash2 size={15} aria-hidden="true" /></button>
            </div>
          </>}
        </li>)}
      </ol>
    </div>}
  </section>;
}
