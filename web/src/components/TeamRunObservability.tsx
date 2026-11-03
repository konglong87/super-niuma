import {
  Activity,
  ArrowDownLeft,
  ArrowRight,
  ArrowUpRight,
  Boxes,
  CheckCircle2,
  CircleDashed,
  FileText,
  Inbox,
  LoaderCircle,
  MessageSquare,
  RefreshCw,
  ShieldAlert,
  UserRound,
  UsersRound,
  XCircle
} from "lucide-react";
import { useMemo, type JSX } from "react";
import type { AgentTeamRun } from "../lib/types";
import "./teamRunObservability.css";

export type RunSnapshot = {
  team_key: string;
  team_version: number;
  display_name?: string;
  coordinator_member: string;
  mode: string;
  source?: {
    provider: string;
    account_key: string;
    external_chat_id: string;
    external_thread_id?: string;
  };
  members: Array<{
    member_key: string;
    display_name?: string;
    profile_key: string;
    profile_version: number;
    role: string;
    profile_id?: number;
  }>;
};

export type TeamRunEvent = {
  id: number;
  sequence_no: number;
  event_type: string;
  member_key?: string;
  from_member_key?: string;
  to_member_key?: string;
  status?: string;
  summary?: string;
  payload_json?: string;
  artifact_ref?: string;
  created_at: string;
};

export type TeamMailboxMessage = {
  id: number;
  sequence_no: number;
  from_member_key: string;
  to_member_key: string;
  message_kind: string;
  payload_ref?: string;
  evidence_ref?: string;
  status: string;
  created_at: string;
};

export type TeamRunObservation = {
  run: AgentTeamRun;
  snapshot?: RunSnapshot;
  events: TeamRunEvent[];
  mailbox: TeamMailboxMessage[];
  next_cursor: number;
  has_more: boolean;
};

export type TeamRunObservabilityProps = {
  language: "en" | "zh";
  observation: TeamRunObservation;
  loading?: boolean;
  error?: string;
  onRefresh: () => void;
  onLoadMore: () => void;
  onProfile?: (key: string, version: number) => void;
};

type Language = TeamRunObservabilityProps["language"];
type MemberSnapshot = RunSnapshot["members"][number];
type MemberState = "completed" | "running" | "failed" | "cancelled" | "waiting" | "not_observed" | "observed";
type ArtifactKind = "file" | "artifact" | "evidence";

type OwnedArtifact = {
  kind: ArtifactKind;
  ref: string;
  action?: string;
  eventID: number;
  sequenceNo: number;
};

type Copy = {
  kicker: string;
  title: string;
  subtitle: string;
  refresh: string;
  refreshing: string;
  runStatus: string;
  finalResult: string;
  mode: string;
  events: string;
  mailbox: string;
  artifacts: string;
  members: string;
  source: string;
  teamVersion: string;
  runID: string;
  coordinator: string;
  timeline: string;
  timelineHint: string;
  topology: string;
  topologyHint: string;
  artifactsTitle: string;
  artifactsHint: string;
  mailboxTitle: string;
  mailboxHint: string;
  loadMore: string;
  noMore: string;
  noEvents: string;
  noMailbox: string;
  noArtifacts: string;
  snapshotUnavailable: string;
  snapshotUnavailableHint: string;
  profile: string;
  role: string;
  pinned: string;
  profileUnavailable: string;
  noSource: string;
  eventDetails: string;
  payload: string;
  noPayload: string;
  from: string;
  to: string;
  addressed: string;
  created: string;
  action: string;
  reference: string;
  rawStatus: string;
  coordinatorTag: string;
  reviewerTag: string;
  memberTag: string;
  status: Record<string, string>;
  sourceProvider: Record<string, string>;
};

const copy: Record<Language, Copy> = {
  en: {
    kicker: "DesktopV2 / TeamRun",
    title: "TeamRun observability",
    subtitle: "Pinned topology, member-level state, directional collaboration, and owned evidence in one dense view.",
    refresh: "Refresh observation",
    refreshing: "Refreshing observation",
    runStatus: "Run status",
    finalResult: "Coordinator result",
    mode: "Mode",
    events: "Events",
    mailbox: "Mailbox",
    artifacts: "Artifacts",
    members: "Members",
    source: "Source",
    teamVersion: "Team version",
    runID: "Run ID",
    coordinator: "Coordinator",
    timeline: "Collaboration timeline",
    timelineHint: "Chronological event stream. Expand an event for its raw, safely escaped payload.",
    topology: "Team topology",
    topologyHint: "Live state comes only from the latest event for each member.",
    artifactsTitle: "Owned artifacts",
    artifactsHint: "References are rendered as plain text; no file links are inferred.",
    mailboxTitle: "Owned mailbox",
    mailboxHint: "Messages captured for this TeamRun.",
    loadMore: "Load earlier events",
    noMore: "No earlier events",
    noEvents: "No member events recorded.",
    noMailbox: "No mailbox messages recorded.",
    noArtifacts: "No owned artifacts recorded.",
    snapshotUnavailable: "Pinned snapshot unavailable",
    snapshotUnavailableHint: "This historical run does not include a reconstructable Team snapshot, so topology and source details are unavailable.",
    profile: "Profile",
    role: "Role",
    pinned: "Pinned",
    profileUnavailable: "Profile details unavailable",
    noSource: "No snapshot source",
    eventDetails: "Event details",
    payload: "Payload",
    noPayload: "No payload recorded.",
    from: "From",
    to: "To",
    addressed: "Addressed",
    created: "Created",
    action: "Action",
    reference: "Reference",
    rawStatus: "Raw status",
    coordinatorTag: "Coordinator",
    reviewerTag: "Reviewer",
    memberTag: "Member",
    status: {
      completed: "Completed",
      success: "Succeeded",
      succeeded: "Succeeded",
      done: "Done",
      delivered: "Delivered",
      running: "Running",
      started: "Started",
      processing: "Processing",
      working: "Working",
      failed: "Failed",
      error: "Error",
      rejected: "Rejected",
      timed_out: "Timed out",
      timeout: "Timed out",
      cancelled: "Cancelled",
      canceled: "Cancelled",
      stopped: "Stopped",
      queued: "Queued",
      waiting: "Waiting",
      pending: "Pending",
      observed: "Observed",
      not_observed: "Not observed"
    },
    sourceProvider: { feishu: "Feishu", lark: "Feishu", desktop: "Desktop", desktop_v2: "Desktop V2" }
  },
  zh: {
    kicker: "DesktopV2 / TeamRun",
    title: "TeamRun 可观测性",
    subtitle: "在一个高密度视图中查看固定拓扑、成员状态、定向协作与本次运行产物。",
    refresh: "刷新观测",
    refreshing: "正在刷新观测",
    runStatus: "运行状态",
    finalResult: "协调者结果",
    mode: "模式",
    events: "事件",
    mailbox: "邮箱",
    artifacts: "产物",
    members: "成员",
    source: "来源",
    teamVersion: "团队版本",
    runID: "运行 ID",
    coordinator: "协调者",
    timeline: "协作时间线",
    timelineHint: "按时间排列的事件流。展开事件可查看经过安全转义的原始载荷。",
    topology: "团队拓扑",
    topologyHint: "成员实时状态只来自该成员最新的一条事件。",
    artifactsTitle: "本次运行产物",
    artifactsHint: "引用只以纯文本显示，不推断文件链接。",
    mailboxTitle: "本次运行邮箱",
    mailboxHint: "本次 TeamRun 捕获到的消息。",
    loadMore: "加载更早事件",
    noMore: "没有更早事件",
    noEvents: "暂无成员级事件。",
    noMailbox: "暂无邮箱消息。",
    noArtifacts: "暂无运行产物。",
    snapshotUnavailable: "固定快照不可用",
    snapshotUnavailableHint: "该历史运行未携带可重建的 Team 快照，因此拓扑和来源详情不可用。",
    profile: "Profile",
    role: "职责",
    pinned: "已固定",
    profileUnavailable: "Profile 详情不可用",
    noSource: "没有快照来源",
    eventDetails: "事件详情",
    payload: "载荷",
    noPayload: "未记录载荷。",
    from: "发送方",
    to: "接收方",
    addressed: "地址",
    created: "创建时间",
    action: "动作",
    reference: "引用",
    rawStatus: "原始状态",
    coordinatorTag: "协调者",
    reviewerTag: "审阅者",
    memberTag: "成员",
    status: {
      completed: "已完成",
      success: "已成功",
      succeeded: "已成功",
      done: "已完成",
      delivered: "已投递",
      running: "运行中",
      started: "已启动",
      processing: "处理中",
      working: "工作中",
      failed: "失败",
      error: "错误",
      rejected: "已拒绝",
      timed_out: "超时",
      timeout: "超时",
      cancelled: "已取消",
      canceled: "已取消",
      stopped: "已停止",
      queued: "排队中",
      waiting: "等待中",
      pending: "待处理",
      observed: "已观测",
      not_observed: "未观测"
    },
    sourceProvider: { feishu: "飞书", lark: "飞书", desktop: "桌面端", desktop_v2: "Desktop V2" }
  }
};

export function TeamRunObservability({ language, observation, loading = false, error, onRefresh, onLoadMore, onProfile }: TeamRunObservabilityProps): JSX.Element {
  const labels = copy[language];
  const snapshot = observation.snapshot;
  const run = observation.run;
  const coordinatorKey = snapshot?.coordinator_member || run.coordinator_member_key;
  const members = snapshot?.members || [];
  const sortedEvents = useMemo(() => [...observation.events].sort(compareEvents), [observation.events]);
  const latestEvents = useMemo(() => latestEventsByMember(observation.events), [observation.events]);
  const ownedArtifacts = useMemo(() => collectOwnedArtifacts(observation.events), [observation.events]);
  const memberLabels = useMemo(() => createMemberLabels(snapshot), [snapshot]);
  const source = snapshot?.source;

  return (
    <main className={`teamrun-observability${loading ? " is-loading" : ""}`} aria-busy={loading}>
      <header className="teamrun-observability__header">
        <div className="teamrun-observability__heading">
          <span className="teamrun-observability__kicker"><Activity size={13} aria-hidden="true" />{labels.kicker}</span>
          <h2>{snapshot?.display_name || snapshot?.team_key || labels.title}<span className="teamrun-observability__title-version">{snapshot ? `@v${snapshot.team_version}` : ""}</span></h2>
          <p>{labels.subtitle}</p>
          <div className="teamrun-observability__run-meta">
            <code>{run.id}</code>
            <span>{labels.mode}: <strong>{snapshot?.mode || "—"}</strong></span>
            {coordinatorKey ? <span>{labels.coordinator}: <strong>{memberLabels.get(coordinatorKey) || coordinatorKey}</strong></span> : null}
          </div>
        </div>
        <div className="teamrun-observability__header-actions">
          <div className="teamrun-observability__badges">
            <StatusBadge language={language} status={run.status} />
            {source ? <SourceBadge labels={labels} source={source} /> : null}
          </div>
          <button className="teamrun-observability__icon-button" type="button" onClick={onRefresh} aria-label={loading ? labels.refreshing : labels.refresh} title={loading ? labels.refreshing : labels.refresh} disabled={loading}>
            {loading ? <LoaderCircle className="teamrun-observability__spin" size={16} aria-hidden="true" /> : <RefreshCw size={16} aria-hidden="true" />}
          </button>
        </div>
      </header>

      {error ? <div className="teamrun-observability__error" role="alert"><ShieldAlert size={16} aria-hidden="true" /><span>{error}</span></div> : null}

      <section className="teamrun-observability__summary" aria-label={labels.title}>
        <SummaryMetric label={labels.runStatus} value={statusText(run.status, language)} tone={statusTone(run.status)} />
        <SummaryMetric label={labels.teamVersion} value={snapshot ? `v${snapshot.team_version}` : "—"} tone="neutral" />
        <SummaryMetric label={labels.events} value={String(observation.events.length)} tone="accent" />
        <SummaryMetric label={labels.mailbox} value={String(observation.mailbox.length)} tone="neutral" />
        <SummaryMetric label={labels.artifacts} value={String(ownedArtifacts.length)} tone="neutral" />
        <SummaryMetric label={labels.members} value={snapshot ? String(members.length) : "—"} tone="neutral" />
      </section>

      {!snapshot ? <section className="teamrun-observability__unavailable" aria-label={labels.snapshotUnavailable}><CircleDashed size={17} aria-hidden="true" /><div><strong>{labels.snapshotUnavailable}</strong><span>{labels.snapshotUnavailableHint}</span></div></section> : null}

      {run.result_json ? <section className="teamrun-observability__result-panel" aria-label={labels.finalResult}><SectionHeading icon={<CheckCircle2 size={15} aria-hidden="true" />} title={labels.finalResult} hint={run.coordinator_member_key || labels.coordinator} /><div className="teamrun-observability__result-copy">{run.result_json}</div><details className="collaboration-raw-details"><summary>{labels.eventDetails}</summary><pre>{run.result_json}</pre></details></section> : null}

      <div className="teamrun-observability__workspace">
        <section className="teamrun-observability__timeline-panel" aria-labelledby="teamrun-timeline-title">
          <SectionHeading id="teamrun-timeline-title" icon={<MessageSquare size={15} aria-hidden="true" />} title={labels.timeline} hint={labels.timelineHint} />
          {sortedEvents.length ? <ol className="teamrun-observability__timeline" aria-label={labels.timeline}>{sortedEvents.map((event) => <TimelineEvent key={`${event.id}-${event.sequence_no}`} event={event} labels={labels} language={language} coordinatorKey={coordinatorKey} snapshot={snapshot} memberLabels={memberLabels} />)}</ol> : <EmptyBlock icon={<MessageSquare size={18} aria-hidden="true" />} text={labels.noEvents} />}
          <div className="teamrun-observability__load-more">
            {observation.has_more ? <button type="button" onClick={onLoadMore} disabled={loading} aria-label={labels.loadMore}>{loading ? <LoaderCircle className="teamrun-observability__spin" size={14} aria-hidden="true" /> : <ArrowDownLeft size={14} aria-hidden="true" />}{labels.loadMore}<span>#{observation.next_cursor}</span></button> : <span>{labels.noMore}</span>}
          </div>
        </section>

        <aside className="teamrun-observability__side-column" aria-label={labels.topology}>
          <section className="teamrun-observability__side-panel">
            <SectionHeading icon={<UsersRound size={15} aria-hidden="true" />} title={labels.topology} hint={labels.topologyHint} />
            {snapshot ? <div className="teamrun-observability__member-list">{members.map((member) => <MemberCard key={member.member_key} member={member} latest={latestEvents.get(member.member_key)} labels={labels} language={language} coordinator={member.member_key === coordinatorKey} onProfile={onProfile} />)}</div> : <EmptyBlock icon={<UsersRound size={18} aria-hidden="true" />} text={labels.snapshotUnavailable} />}
          </section>

          <section className="teamrun-observability__side-panel">
            <SectionHeading icon={<Boxes size={15} aria-hidden="true" />} title={labels.artifactsTitle} hint={labels.artifactsHint} />
            {ownedArtifacts.length ? <div className="teamrun-observability__artifact-list">{ownedArtifacts.map((artifact) => <ArtifactRow key={`${artifact.eventID}-${artifact.kind}-${artifact.ref}-${artifact.action || ""}`} artifact={artifact} labels={labels} language={language} />)}</div> : <EmptyBlock icon={<FileText size={18} aria-hidden="true" />} text={labels.noArtifacts} />}
          </section>

          <section className="teamrun-observability__side-panel">
            <SectionHeading icon={<Inbox size={15} aria-hidden="true" />} title={labels.mailboxTitle} hint={labels.mailboxHint} />
            {observation.mailbox.length ? <div className="teamrun-observability__mailbox-list">{[...observation.mailbox].sort(compareMailbox).map((message) => <MailboxRow key={`${message.id}-${message.sequence_no}`} message={message} labels={labels} language={language} memberLabels={memberLabels} />)}</div> : <EmptyBlock icon={<Inbox size={18} aria-hidden="true" />} text={labels.noMailbox} />}
          </section>

          {source ? <section className="teamrun-observability__source-panel"><SourceBadge labels={labels} source={source} /><dl><Fact label={labels.source} value={providerLabel(source.provider, labels)} /><Fact label="Account" value={source.account_key} /><Fact label="Chat" value={source.external_chat_id} />{source.external_thread_id ? <Fact label="Thread" value={source.external_thread_id} /> : null}</dl></section> : null}
        </aside>
      </div>
    </main>
  );
}

function TimelineEvent({ event, labels, language, coordinatorKey, snapshot, memberLabels }: { event: TeamRunEvent; labels: Copy; language: Language; coordinatorKey?: string; snapshot?: RunSnapshot; memberLabels: Map<string, string> }): JSX.Element {
  const routeKeys = [event.from_member_key, event.to_member_key, event.member_key].filter((value): value is string => Boolean(value));
  const hasCoordinator = Boolean(coordinatorKey && routeKeys.includes(coordinatorKey));
  const hasReviewer = routeKeys.some((key) => (snapshot?.members.find((member) => member.member_key === key)?.role || "").toLowerCase().includes("review"));
  const eventStatus = event.status || inferStatusFromEventType(event.event_type);
  const route = event.from_member_key && event.to_member_key ? (
    <span className="teamrun-observability__event-route"><span>{memberLabels.get(event.from_member_key) || event.from_member_key}</span><ArrowRight size={13} aria-hidden="true" /><span>{memberLabels.get(event.to_member_key) || event.to_member_key}</span></span>
  ) : event.member_key ? <span className="teamrun-observability__event-member">{memberLabels.get(event.member_key) || event.member_key}</span> : null;
  const eventPayload = event.payload_json || "";

  return (
    <li className={`teamrun-observability__event${hasCoordinator ? " is-coordinator" : ""}${hasReviewer ? " is-reviewer" : ""}`}>
      <div className="teamrun-observability__event-marker" aria-hidden="true"><EventIcon status={eventStatus} /></div>
      <div className="teamrun-observability__event-body">
        <div className="teamrun-observability__event-topline">
          <div className="teamrun-observability__event-title-wrap">
            <strong>{humanize(event.event_type)}</strong>
            {hasCoordinator ? <span className="teamrun-observability__role-tag is-coordinator">{labels.coordinatorTag}</span> : null}
            {hasReviewer ? <span className="teamrun-observability__role-tag is-reviewer">{labels.reviewerTag}</span> : null}
          </div>
          <time dateTime={event.created_at} title={event.created_at}>{formatTimestamp(event.created_at, language)}</time>
        </div>
        <div className="teamrun-observability__event-flow">{route || <span className="teamrun-observability__event-member">{labels.memberTag}</span>}<StatusBadge language={language} status={eventStatus} /></div>
        {event.summary ? <p className="teamrun-observability__event-summary">{event.summary}</p> : null}
        <details className="teamrun-observability__event-details">
          <summary><span>{labels.eventDetails}</span><span className="teamrun-observability__event-sequence">#{event.sequence_no}</span></summary>
          <dl className="teamrun-observability__event-facts">
            <Fact label="ID" value={String(event.id)} />
            <Fact label={labels.from} value={event.from_member_key || "—"} />
            <Fact label={labels.to} value={event.to_member_key || "—"} />
            <Fact label={labels.rawStatus} value={event.status || "—"} />
            {event.artifact_ref ? <Fact label={labels.reference} value={event.artifact_ref} /> : null}
          </dl>
          <div className="teamrun-observability__payload-label">{labels.payload}</div>
          {eventPayload ? <pre>{eventPayload}</pre> : <span className="teamrun-observability__no-payload">{labels.noPayload}</span>}
        </details>
      </div>
    </li>
  );
}

function MemberCard({ member, latest, labels, language, coordinator, onProfile }: { member: MemberSnapshot; latest?: TeamRunEvent; labels: Copy; language: Language; coordinator: boolean; onProfile?: (key: string, version: number) => void }): JSX.Element {
  const memberState = memberStateFromEvent(latest);
  const profileText = member.profile_key ? `${member.profile_key}@v${member.profile_version}` : labels.profileUnavailable;
  const card = <div className={`teamrun-observability__member teamrun-observability__member--${memberState}`}>
    <span className="teamrun-observability__member-icon" aria-hidden="true"><MemberIcon role={member.role} /></span>
    <div className="teamrun-observability__member-main">
      <div className="teamrun-observability__member-name"><strong>{member.display_name || member.member_key}</strong>{coordinator ? <span className="teamrun-observability__role-tag is-coordinator">{labels.coordinatorTag}</span> : null}</div>
      <span>{labels.role}: {member.role}</span>
      <span>{labels.profile}: {profileText}</span>
    </div>
    <div className="teamrun-observability__member-state"><StatusBadge language={language} status={memberState} /><small>{latest ? `#${latest.sequence_no}` : labels.profileUnavailable}</small></div>
  </div>;
  return onProfile && member.profile_key && member.profile_version > 0 ? <button type="button" className="teamrun-observability__member-button" onClick={() => onProfile(member.profile_key, member.profile_version)} aria-label={`${labels.profile} ${profileText}`}>{card}</button> : card;
}

function ArtifactRow({ artifact, labels, language }: { artifact: OwnedArtifact; labels: Copy; language: Language }): JSX.Element {
  return <div className="teamrun-observability__artifact">
    <span className={`teamrun-observability__artifact-kind kind-${artifact.kind}`}>{artifact.kind}</span>
    <div className="teamrun-observability__artifact-main"><code title={artifact.ref}>{artifact.ref}</code><span>{artifact.action || labels.reference} · #{artifact.sequenceNo}</span></div>
    <StatusBadge language={language} status="observed" />
  </div>;
}

function MailboxRow({ message, labels, language, memberLabels }: { message: TeamMailboxMessage; labels: Copy; language: Language; memberLabels: Map<string, string> }): JSX.Element {
  const reference = message.payload_ref || message.evidence_ref;
  return <article className="teamrun-observability__mailbox-item">
    <div className="teamrun-observability__mailbox-route"><span>{memberLabels.get(message.from_member_key) || message.from_member_key}</span><ArrowRight size={12} aria-hidden="true" /><span>{memberLabels.get(message.to_member_key) || message.to_member_key}</span><StatusBadge language={language} status={message.status} /></div>
    <div className="teamrun-observability__mailbox-kind"><strong>{humanize(message.message_kind)}</strong><span>#{message.sequence_no}</span></div>
    {reference ? <code title={reference}>{reference}</code> : <span className="teamrun-observability__no-payload">{labels.noPayload}</span>}
    <time dateTime={message.created_at}>{formatTimestamp(message.created_at, language)}</time>
  </article>;
}

function SourceBadge({ labels, source }: { labels: Copy; source: NonNullable<RunSnapshot["source"]> }): JSX.Element {
  const provider = source.provider.toLowerCase();
  const className = provider.includes("feishu") || provider === "lark" ? "feishu" : provider.includes("desktop") ? "desktop" : "other";
  return <span className={`teamrun-observability__source-badge is-${className}`}><span aria-hidden="true" />{providerLabel(source.provider, labels)}</span>;
}

function SummaryMetric({ label, value, tone }: { label: string; value: string; tone: string }): JSX.Element {
  return <div className={`teamrun-observability__metric is-${tone}`}><small>{label}</small><strong title={value}>{value}</strong></div>;
}

function StatusBadge({ language, status }: { language: Language; status: string }): JSX.Element {
  return <span className={`teamrun-observability__status is-${statusTone(status)}`}><span aria-hidden="true" />{statusText(status, language)}</span>;
}

function EventIcon({ status }: { status: string }): JSX.Element {
  const tone = statusTone(status);
  if (tone === "success") return <CheckCircle2 size={15} />;
  if (tone === "danger") return <XCircle size={15} />;
  if (tone === "warning") return <Activity size={15} />;
  return <CircleDashed size={15} />;
}

function MemberIcon({ role }: { role: string }): JSX.Element {
  const normalized = role.toLowerCase();
  if (normalized.includes("review")) return <ShieldAlert size={15} />;
  if (normalized.includes("coordin")) return <ArrowUpRight size={15} />;
  return <UserRound size={15} />;
}

function SectionHeading({ id, icon, title, hint }: { id?: string; icon: JSX.Element; title: string; hint: string }): JSX.Element {
  return <div className="teamrun-observability__section-heading" id={id}><span className="teamrun-observability__section-icon">{icon}</span><div><strong>{title}</strong><span>{hint}</span></div></div>;
}

function EmptyBlock({ icon, text }: { icon: JSX.Element; text: string }): JSX.Element {
  return <div className="teamrun-observability__empty"><span>{icon}</span><span>{text}</span></div>;
}

function Fact({ label, value }: { label: string; value: string }): JSX.Element {
  return <div><dt>{label}</dt><dd title={value}>{value}</dd></div>;
}

function compareEvents(left: TeamRunEvent, right: TeamRunEvent): number {
  return left.sequence_no - right.sequence_no || left.id - right.id;
}

function compareMailbox(left: TeamMailboxMessage, right: TeamMailboxMessage): number {
  return right.sequence_no - left.sequence_no || right.id - left.id;
}

function latestEventsByMember(events: TeamRunEvent[]): Map<string, TeamRunEvent> {
  const latest = new Map<string, TeamRunEvent>();
  for (const event of events) {
    if (!event.member_key) continue;
    const current = latest.get(event.member_key);
    if (!current || compareEvents(current, event) < 0) latest.set(event.member_key, event);
  }
  return latest;
}

function memberStateFromEvent(event?: TeamRunEvent): MemberState {
  if (!event) return "not_observed";
  const value = (event.status || inferStatusFromEventType(event.event_type)).toLowerCase();
  if (["completed", "success", "succeeded", "done", "delivered", "finished"].includes(value)) return "completed";
  if (["failed", "error", "rejected", "timed_out", "timeout"].includes(value)) return "failed";
  if (["cancelled", "canceled", "stopped"].includes(value)) return "cancelled";
  if (["running", "started", "processing", "working", "delegated", "dispatched"].includes(value)) return "running";
  if (["queued", "waiting", "pending"].includes(value)) return "waiting";
  return "observed";
}

function inferStatusFromEventType(eventType: string): string {
  const value = eventType.toLowerCase();
  if (value.includes("fail") || value.includes("error") || value.includes("reject")) return "failed";
  if (value.includes("cancel") || value.includes("stop")) return "cancelled";
  if (value.includes("complete") || value.includes("finish") || value.includes("success") || value.includes("done")) return "completed";
  if (value.includes("start") || value.includes("run") || value.includes("process") || value.includes("work") || value.includes("dispatch") || value.includes("delegate")) return "running";
  if (value.includes("queue") || value.includes("wait") || value.includes("pending")) return "waiting";
  return "observed";
}

function collectOwnedArtifacts(events: TeamRunEvent[]): OwnedArtifact[] {
  const artifacts: OwnedArtifact[] = [];
  const seen = new Set<string>();
  for (const event of [...events].sort(compareEvents)) {
    const payload = parsePayload(event.payload_json);
    const payloadArtifacts = payload?.artifacts;
    if (Array.isArray(payloadArtifacts)) {
      for (const candidate of payloadArtifacts) {
        if (!isRecord(candidate) || !isArtifactKind(candidate.kind) || typeof candidate.ref !== "string" || !candidate.ref) continue;
        const item = ownedArtifact(candidate.kind, candidate.ref, typeof candidate.action === "string" ? candidate.action : undefined, event);
        addArtifact(item, artifacts, seen);
      }
    }
    const legacyEvidence = payload?.evidence;
    if (Array.isArray(legacyEvidence)) {
      for (const reference of legacyEvidence) {
        if (typeof reference !== "string" || !reference) continue;
        addArtifact(ownedArtifact("evidence", reference, "legacy evidence", event), artifacts, seen);
      }
    }
    if (event.artifact_ref) addArtifact(ownedArtifact("artifact", event.artifact_ref, "event reference", event), artifacts, seen);
  }
  return artifacts;
}

function ownedArtifact(kind: ArtifactKind, ref: string, action: string | undefined, event: TeamRunEvent): OwnedArtifact {
  return { kind, ref, action, eventID: event.id, sequenceNo: event.sequence_no };
}

function addArtifact(item: OwnedArtifact, artifacts: OwnedArtifact[], seen: Set<string>): void {
  const key = `${item.kind}\u0000${item.ref}\u0000${item.action || ""}`;
  if (seen.has(key)) return;
  seen.add(key);
  artifacts.push(item);
}

function parsePayload(value?: string): Record<string, unknown> | undefined {
  if (!value) return undefined;
  try {
    const parsed: unknown = JSON.parse(value);
    return isRecord(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isArtifactKind(value: unknown): value is ArtifactKind {
  return value === "file" || value === "artifact" || value === "evidence";
}

function createMemberLabels(snapshot?: RunSnapshot): Map<string, string> {
  return new Map((snapshot?.members || []).map((member) => [member.member_key, member.member_key]));
}

function providerLabel(provider: string, labels: Copy): string {
  const normalized = provider.toLowerCase();
  return labels.sourceProvider[normalized] || provider;
}

function statusText(status: string, language: Language): string {
  const normalized = status.toLowerCase();
  return copy[language].status[normalized] || humanize(status);
}

function statusTone(status: string): string {
  const normalized = status.toLowerCase();
  if (["completed", "success", "succeeded", "done", "delivered", "finished", "observed"].includes(normalized)) return "success";
  if (["failed", "error", "rejected", "timed_out", "timeout", "cancelled", "canceled", "stopped"].includes(normalized)) return "danger";
  if (["running", "started", "processing", "working", "delegated", "dispatched", "queued", "waiting", "pending"].includes(normalized)) return "warning";
  return "neutral";
}

function humanize(value: string): string {
  return value.replace(/[_-]+/g, " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

function formatTimestamp(value: string, language: Language): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
}
