import { ExternalLink, FileDiff, FileText, GitBranch, ShieldCheck } from "lucide-react";
import { useMemo, type JSX, type ReactNode } from "react";
import { MarkdownLite } from "../../components/agent/markdown";
import { useI18n } from "../../lib/i18n";
import { formatDuration } from "../../lib/messages";
import type { IdentityConfig, WebAgentConversationDetail } from "../../lib/types";
import { webUIV2SessionPath } from "../routes";
import type { SessionDetail } from "../types";
import { buildConversationInspection, conversationTraceURL, type ConversationRunView } from "./conversationViewModel";
import type { InspectorTab } from "./Inspector";
import { PermissionMessage } from "./ConversationMessage";

const copy = {
  en: { subAgents: "Subtasks", permissions: "Permission history", pending: "Pending", allowed: "Allowed", denied: "Denied", changed: "Changed files", read: "Read files", lineDelta: "Net line change", unknown: "Unknown", diffUnavailable: "Diff was not saved for this change.", before: "Before", after: "After", lines: "lines", diff: "Tool diff", usage: "Usage", context: "Context", total: "Total tokens", input: "Input tokens", output: "Output tokens", cacheRead: "Cache read tokens", cacheCreate: "Cache creation tokens", cache5m: "5-minute cache tokens", cache1h: "1-hour cache tokens", serviceTier: "Service tier", inferenceGeo: "Inference region", speed: "Speed", stopReason: "Stop reason", initialMessages: "Initial messages", toolCalls: "Tool calls", duration: "Duration", provider: "Provider", model: "Model", mode: "Permission mode", stage: "Stage", completed: "Completed", openItems: "Open items", risks: "Risks", nextActions: "Next actions", snapshot: "Context snapshot", captured: "Captured", tokens: "Estimated tokens", turn: "Turn", source: "Source", created: "Created", modified: "Modified", deleted: "Deleted" },
  zh: { subAgents: "子任务", permissions: "权限历史", pending: "待确认", allowed: "已允许", denied: "已拒绝", changed: "修改文件", read: "读取文件", lineDelta: "净行数变化", unknown: "未知", diffUnavailable: "此变更未保存 diff。", before: "修改前", after: "修改后", lines: "行", diff: "工具 diff", usage: "用量", context: "上下文", total: "总 tokens", input: "输入 tokens", output: "输出 tokens", cacheRead: "缓存读取 tokens", cacheCreate: "缓存创建 tokens", cache5m: "5 分钟缓存 tokens", cache1h: "1 小时缓存 tokens", serviceTier: "服务层级", inferenceGeo: "推理区域", speed: "速度", stopReason: "停止原因", initialMessages: "初始消息数", toolCalls: "工具调用", duration: "耗时", provider: "供应商", model: "模型", mode: "权限模式", stage: "阶段", completed: "已完成", openItems: "未完成事项", risks: "风险", nextActions: "下一步", snapshot: "上下文快照", captured: "捕获时间", tokens: "估算 tokens", turn: "轮次", source: "来源", created: "已创建", modified: "已修改", deleted: "已删除" }
} as const;
type InspectorCopy = { [Key in keyof typeof copy.en]: string };

type Inspection = ReturnType<typeof buildConversationInspection>;
type InspectorDetailsProps = { detail: SessionDetail; tab: InspectorTab; runtimeDetails?: WebAgentConversationDetail; identity?: IdentityConfig };

export function InspectorDetails({ detail, tab, runtimeDetails, identity }: InspectorDetailsProps): JSX.Element {
  const inspection = useMemo(() => buildConversationInspection(detail, runtimeDetails), [detail, runtimeDetails]);
  if (tab === "activity") return <ActivityDetails detail={detail} inspection={inspection} identity={identity} />;
  if (tab === "context") return <ContextDetails detail={detail} inspection={inspection} />;
  if (tab === "changes") return <ChangesDetails detail={detail} inspection={inspection} />;
  return <RunsDetails detail={detail} inspection={inspection} identity={identity} />;
}

function ActivityDetails({ detail, inspection, identity }: Omit<InspectorDetailsProps, "tab"> & { inspection: Inspection }): JSX.Element {
  const { t, language } = useI18n();
  const labels = copy[language];
  const latestRun = inspection.runs.at(-1);
  const hasActivity = detail.activity.length > 0 || inspection.subAgents.length > 0 || inspection.permissions.length > 0 || latestRun?.hasUsage;
  return <div className="webui2-inspector-details">
    {detail.status === "waiting_permission" ? <p className="webui2-inspector-permission-status">{t("webui2.inspector.permission")}</p> : null}
    {detail.status === "waiting_input" ? <p className="webui2-inspector-permission-status">{t("webui2.status.waiting_input")}</p> : null}
    <TraceLink identity={identity} detail={detail} />
    {latestRun?.hasUsage ? <details className="webui2-run-usage"><summary>{labels.usage}<span>{latestRun.usage.totalTokens.toLocaleString()} tokens</span></summary><UsageDetails run={latestRun} /></details> : null}
    {inspection.subAgents.length ? <CollaborationActivitySummary language={language} labels={labels} subAgents={inspection.subAgents} /> : null}
    {inspection.permissions.length ? <section><h3><ShieldCheck size={14} />{labels.permissions}</h3><ul className="webui2-inspector-list">{inspection.permissions.map((permission) => <li key={`${permission.taskID}:${permission.requestID}`}><details>
      <summary><strong>{permission.toolName || labels.permissions}</strong><span data-permission-status={permission.status}>{permission.status === "pending" && !permission.active ? t("webui2.status.stopped") : labels[permission.status]}</span></summary>
      <small>Task {permission.taskID} · {permission.requestID}</small>
      {permission.reason ? <p>{permission.reason}</p> : null}{permission.input ? <pre>{permission.input}</pre> : null}
      {permission.decision ? <p>{permission.decision}</p> : null}<InspectorTime value={permission.resolvedAt || permission.createdAt} />
      {permission.status === "pending" && permission.active && identity ? <PermissionMessage identity={identity} message={{ id: `permission:${permission.taskID}:${permission.requestID}`, role: "assistant", kind: "message", content: "", createdAt: "", permission: { taskID: permission.taskID, requestID: permission.requestID, resolved: false } }} /> : null}
    </details></li>)}</ul></section> : null}
    {detail.activity.length ? <TextList items={detail.activity} /> : null}
    {!hasActivity ? <Empty>{t("webui2.inspector.empty.activity")}</Empty> : null}
  </div>;
}

function CollaborationActivitySummary({ language, labels, subAgents }: { language: string; labels: InspectorCopy; subAgents: Inspection["subAgents"] }): JSX.Element {
  const running = subAgents.filter((sub) => sub.status === "running").length;
  const completed = subAgents.filter((sub) => sub.status === "done").length;
  const attention = subAgents.filter((sub) => sub.status === "error" || sub.status === "cancelled").length;
  const stateLabel = (status: string): string => status === "done" ? labels.completed : status === "error" ? (language === "zh" ? "失败" : "Failed") : status === "cancelled" ? (language === "zh" ? "已停止" : "Stopped") : (language === "zh" ? "运行中" : "Running");
  return <section className="webui2-collaboration-summary" aria-label={labels.subAgents}>
    <div className="webui2-collaboration-summary-head"><div><h3><GitBranch size={14} />{language === "zh" ? "协作概览" : "Collaboration overview"}</h3><p>{language === "zh" ? "当前会话中的 Agent 协作节点。" : "Agent nodes participating in this session."}</p></div><div className="webui2-collaboration-summary-counts"><span><strong>{running}</strong>{language === "zh" ? "运行" : "running"}</span><span><strong>{completed}</strong>{language === "zh" ? "完成" : "done"}</span><span><strong>{attention}</strong>{language === "zh" ? "关注" : "attention"}</span></div></div>
    <div className="webui2-collaboration-node-list">{subAgents.map((sub) => <article className={`webui2-collaboration-node webui2-collaboration-node--${sub.status}`} key={sub.taskID}><span className="webui2-collaboration-node-dot" /><div><strong>{sub.agent || sub.description || `Task ${sub.taskID}`}</strong><span>{stateLabel(sub.status)}{sub.model ? ` · ${sub.model}` : ""}</span><small>{[sub.turn ? `${labels.turn} ${sub.turn}` : "", sub.lastTool, sub.toolCalls ? `${sub.toolCalls} ${labels.toolCalls}` : "", sub.durationMS ? formatDuration(sub.durationMS) : ""].filter(Boolean).join(" · ") || (language === "zh" ? "等待活动" : "Waiting for activity")}</small>{sub.tokens ? <small>{sub.tokens}</small> : null}{sub.detail ? <p>{sub.detail}</p> : null}</div></article>)}</div>
  </section>;
}

function ContextDetails({ detail, inspection }: { detail: SessionDetail; inspection: Inspection }): JSX.Element {
  const { t, language } = useI18n();
  const labels = copy[language];
  if (!detail.context.length && !inspection.handoffs.length) return <Empty>{t("webui2.inspector.empty.context")}</Empty>;
  return <div className="webui2-inspector-details">
    {detail.context.length ? <ul className="webui2-inspector-list">{detail.context.map((item) => <li key={item.sourceRef}><strong>{item.title}</strong><span>{item.sourceRef}</span><span data-status={item.status}>{t(`webui2.status.${item.status}`)}</span></li>)}</ul> : null}
    {inspection.handoffs.map((handoff) => <section className="webui2-context-snapshot" key={handoff.id}>
      <h3>{labels.snapshot} · Task {handoff.taskID}</h3><a href={webUIV2SessionPath(handoff.sourceRef)}>{handoff.sourceRef}<ExternalLink size={12} /></a>
      {handoff.objective ? <p>{handoff.objective}</p> : null}{handoff.summary ? <MarkdownLite content={handoff.summary} /> : null}
      <dl className="webui2-inspector-facts"><Fact label="Cursor" value={handoff.cursor || labels.unknown} /><Fact label="SHA256" value={handoff.hash || labels.unknown} /><Fact label={labels.captured} value={<InspectorTime value={handoff.capturedAt} />} />{handoff.estimatedTokens !== undefined ? <Fact label={labels.tokens} value={handoff.estimatedTokens} /> : null}</dl>
      {([ [labels.completed, handoff.completed], [labels.openItems, handoff.openItems], [labels.risks, handoff.risks], [labels.nextActions, handoff.nextActions] ] as const).map(([label, items]) => items.length ? <div key={label}><h4>{label}</h4><TextList items={items} /></div> : null)}
    </section>)}
  </div>;
}

function ChangesDetails({ detail, inspection }: { detail: SessionDetail; inspection: Inspection }): JSX.Element {
  const { t, language } = useI18n();
  const labels = copy[language];
  const { activity, fileEvents } = inspection;
  if (!activity.editedFiles.length && !activity.readFilePaths.length && !detail.changes.length) return <Empty>{t("webui2.inspector.empty.changes")}</Empty>;
  return <div className="webui2-inspector-details">
    {fileEvents.length ? <dl className="webui2-inspector-facts"><Fact label={labels.changed} value={activity.changedFiles} /><Fact label={labels.read} value={activity.filesRead} /><Fact label={labels.lineDelta} value={activity.editedFiles.some((file) => file.lineDelta === null) ? labels.unknown : lineDelta(activity.lineDelta)} /></dl> : null}
    <ul className="webui2-inspector-list webui2-inspector-changes">{activity.editedFiles.map((file) => {
      const edits = fileEvents.filter((event) => event.path === file.path && event.access === "edited");
      const changeLabel = labels[file.change as "created" | "modified" | "deleted"] ?? file.change;
      return <li key={file.path}><details><summary><FileDiff size={14} /><strong>{file.path}</strong><span>{file.lineDelta === null ? labels.unknown : lineDelta(file.lineDelta)}</span></summary>
        <small>{changeLabel} · {file.toolName}{file.object !== "file" ? ` · ${file.object}` : ""}</small>
        {edits.map((edit) => <div className="webui2-file-change-event" key={edit.id}><span>Task {edit.taskID} · <InspectorTime value={edit.createdAt} /></span>{edit.beforeLines !== undefined && edit.afterLines !== undefined ? <p>{labels.before} {edit.beforeLines} / {labels.after} {edit.afterLines} {labels.lines}</p> : null}{edit.diff ? <pre className="webui2-file-diff">{edit.diff}</pre> : <p className="webui2-inspector-muted">{labels.diffUnavailable}</p>}</div>)}
      </details></li>;
    })}{detail.changes.filter((path) => !activity.editedFiles.some((file) => file.path === path)).map((path) => <li key={path}><details><summary><FileDiff size={14} /><strong>{path}</strong></summary><p>{labels.diffUnavailable}</p></details></li>)}</ul>
    {activity.readFilePaths.length ? <section><h3><FileText size={14} />{labels.read}</h3><ul className="webui2-inspector-list">{activity.readFilePaths.map((path) => <li key={path}>{path}</li>)}</ul></section> : null}
  </div>;
}

function RunsDetails({ detail, inspection, identity }: Omit<InspectorDetailsProps, "tab"> & { inspection: Inspection }): JSX.Element {
  const { t, language } = useI18n();
  const labels = copy[language];
  return <div className="webui2-inspector-details"><TraceLink identity={identity} detail={detail} />{inspection.runs.length ? <ul className="webui2-inspector-list">{inspection.runs.map((run) => <li key={run.id}>
    <strong>{run.id}</strong><span>{t(`webui2.status.${run.status}`)}</span>
    <time dateTime={run.startedAt}>{t("webui2.inspector.started", { time: timeLabel(run.startedAt, language) })}</time>{run.endedAt ? <time dateTime={run.endedAt}>{t("webui2.inspector.ended", { time: timeLabel(run.endedAt, language) })}</time> : null}
    <dl className="webui2-inspector-facts"><Fact label={labels.provider} value={run.provider || labels.unknown} /><Fact label={labels.model} value={run.model || labels.unknown} />{run.permissionMode ? <Fact label={labels.mode} value={run.permissionMode} /> : null}{run.durationMs !== undefined ? <Fact label={labels.duration} value={formatDuration(run.durationMs)} /> : null}{run.traceID ? <Fact label="Trace" value={run.traceID} /> : null}</dl>
    {run.hasUsage ? <details className="webui2-run-usage"><summary>{labels.usage}<span>{run.usage.totalTokens.toLocaleString()} tokens</span></summary><UsageDetails run={run} /></details> : null}
  </li>)}</ul> : <Empty>{t("webui2.inspector.empty.runs")}</Empty>}</div>;
}

function UsageDetails({ run }: { run: ConversationRunView }): JSX.Element {
  const { language } = useI18n();
  const labels = copy[language];
  const usage = run.usage;
  const measured = (key: string, value: number): ReactNode => run.usageFields.includes(key) ? value : labels.unknown;
  const rows: Array<[string, ReactNode]> = [
    [labels.context, run.hasContext ? `${usage.contextPercent}%${usage.contextLength ? ` / ${usage.contextLength.toLocaleString()}` : ""}` : labels.unknown],
    [labels.total, usage.totalTokens], [labels.input, measured("input_tokens", usage.inputTokens)], [labels.output, measured("output_tokens", usage.outputTokens)],
    [labels.cacheRead, measured("cache_read_input_tokens", usage.cacheReadTokens)], [labels.cacheCreate, measured("cache_creation_input_tokens", usage.cacheCreationTokens)], [labels.cache5m, measured("cache_creation_ephemeral_5m_input_tokens", usage.cacheCreationEphemeral5mTokens)], [labels.cache1h, measured("cache_creation_ephemeral_1h_input_tokens", usage.cacheCreationEphemeral1hTokens)],
    [labels.serviceTier, usage.serviceTier || labels.unknown], [labels.inferenceGeo, usage.inferenceGeo || labels.unknown], [labels.speed, usage.speed || labels.unknown], [labels.initialMessages, measured("initial_messages", usage.initialMessages)], [labels.stopReason, usage.stopReason || labels.unknown], [labels.toolCalls, usage.toolCalls]
  ];
  return <dl className="webui2-inspector-facts">{rows.map(([label, value]) => <Fact key={label} label={label} value={value} />)}</dl>;
}

function TraceLink({ identity, detail }: { identity?: IdentityConfig; detail: SessionDetail }): JSX.Element | null {
  const url = identity ? conversationTraceURL(identity, detail) : undefined;
  return url ? <a className="webui2-inspector-trace-link" href={url} target="_blank" rel="noreferrer">Trace<ExternalLink size={13} /></a> : null;
}
function Fact({ label, value }: { label: string; value: ReactNode }): JSX.Element { return <div><dt>{label}</dt><dd>{typeof value === "number" ? value.toLocaleString() : value}</dd></div>; }
function InspectorTime({ value }: { value?: string }): JSX.Element | null { const { language } = useI18n(); return value ? <time dateTime={value}>{timeLabel(value, language)}</time> : null; }
function Empty({ children }: { children: string }): JSX.Element { return <p className="webui2-inspector-empty">{children}</p>; }
function timeLabel(value: string, language: string): string { return Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString(language) : value; }
function lineDelta(value: number): string { return value > 0 ? `+${value}` : String(value); }
function TextList({ items }: { items: readonly string[] }): JSX.Element {
  const occurrences = new Map<string, number>();
  return <ul className="webui2-inspector-list">{items.map((item) => {
    const occurrence = (occurrences.get(item) ?? 0) + 1;
    occurrences.set(item, occurrence);
    return <li key={`${item}:${occurrence}`}>{item}</li>;
  })}</ul>;
}
