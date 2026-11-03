import { Bot, Check, GitCompareArrows, History, Plus, RefreshCcw, Save, ShieldCheck, Users, Workflow } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { cancelAgentTeamRun, getAgentTeamRun, getAgentTeamRunTimeline, listAgentProfiles, listAgentTeamBindings, listAgentTeamMembers, listAgentTeamRuns, listAgentTeams, listChannelAccounts, publishAgentTeam, saveAgentTeam, saveAgentTeamBindings, saveAgentTeamMembers, validateAgentTeam } from "../lib/api";
import { defaultTeamPolicy } from "../lib/agentProfiles";
import { TeamRunConsole, TeamRunReplayConsole } from "./CollaborationRunConsole";
import { useI18n } from "../lib/i18n";
import type { AgentProfileRecord, AgentTeamBinding, AgentTeamMember, AgentTeamPolicy, AgentTeamRecord, AgentTeamRun, AgentTeamRunTimeline, ChannelAccountRecord, IdentityConfig } from "../lib/types";

type TeamWorkspace = "workspace" | "operations";
type Props = { identity: IdentityConfig; onStatus: (message: string) => void; onDataChanged?: () => void; workspace?: TeamWorkspace; onWorkspaceChange?: (workspace: TeamWorkspace) => void };
type View = "catalog" | "builder" | "bindings" | "runs" | "replay";
type Language = "en" | "zh";
const text = (language: Language, en: string, zh: string) => language === "zh" ? zh : en;
const statusName = (status: string, language: Language) => language === "zh" ? ({ published: "已发布", draft: "草稿", archived: "已归档", validating: "校验中" }[status] || status) : status;
const roleName = (role: string, language: Language) => language === "zh" ? ({ coordinator: "协调者", researcher: "研究员", writer: "写作者", coder: "编码者", reviewer: "审阅者" }[role] || role) : role;
const modeName = (mode: string, language: Language) => language === "zh" ? ({ coordinator: "协调者模式", parallel_review: "并行评审" }[mode] || mode) : mode;

function message(error: unknown): string { return error instanceof Error ? error.message : String(error); }
function parsePolicy(text: string): AgentTeamPolicy { try { return JSON.parse(text) as AgentTeamPolicy; } catch { return defaultTeamPolicy(); } }
function latestTeamVersions(records: AgentTeamRecord[]): AgentTeamRecord[] {
  const latest = new Map<string, AgentTeamRecord>();
  for (const team of records) {
    if (team.status === "archived") continue;
    const current = latest.get(team.team_key);
    if (!current || team.team_version > current.team_version) latest.set(team.team_key, team);
  }
  return Array.from(latest.values());
}

export function AgentTeamsPanel({ identity, onStatus, onDataChanged, workspace = "workspace", onWorkspaceChange }: Props) {
  const { language } = useI18n();
  const [teams, setTeams] = useState<AgentTeamRecord[]>([]);
  const [profiles, setProfiles] = useState<AgentProfileRecord[]>([]);
  const [accounts, setAccounts] = useState<ChannelAccountRecord[]>([]);
  const [selectedKey, setSelectedKey] = useState("");
  const [selected, setSelected] = useState<AgentTeamRecord | null>(null);
  const [view, setView] = useState<View>("catalog");
  const [policyText, setPolicyText] = useState(JSON.stringify(defaultTeamPolicy(), null, 2));
  const [members, setMembers] = useState<AgentTeamMember[]>([]);
  const [bindings, setBindings] = useState<AgentTeamBinding[]>([]);
  const [runs, setRuns] = useState<AgentTeamRun[]>([]);
  const [selectedRun, setSelectedRun] = useState<AgentTeamRun | null>(null);
  const [selectedRunTimeline, setSelectedRunTimeline] = useState<AgentTeamRunTimeline | null>(null);
  const [dirty, setDirty] = useState(false);
  const [_loading, setLoading] = useState(false);
  const [validation, setValidation] = useState<{ valid: boolean; issues?: Array<{ code: string; message: string }> } | null>(null);
  const teamOptions = useMemo(() => latestTeamVersions(teams).sort((a, b) => a.display_name.localeCompare(b.display_name)), [teams]);

  async function refresh(nextKey = selectedKey) {
    setLoading(true);
    try {
      const [teamItems, profileItems, accountItems] = await Promise.all([listAgentTeams(identity), listAgentProfiles(identity), listChannelAccounts(identity).catch(() => [])]);
      const legacyTeams = teamItems.filter((team) => (team.schema_version ?? 1) <= 1);
      const options = latestTeamVersions(legacyTeams);
      setTeams(legacyTeams); setProfiles(profileItems.filter((profile) => profile.status === "published")); setAccounts(accountItems);
      const next = options.find((team) => team.team_key === nextKey) ?? options[0] ?? null;
      if (next) await hydrate(next); else { setSelected(null); setSelectedKey(""); }
    } catch (error) { onStatus(message(error)); }
    finally { setLoading(false); }
  }
  async function hydrate(team: AgentTeamRecord) {
    setSelected(team); setSelectedKey(team.team_key); setPolicyText(pretty(team.policy_json, defaultTeamPolicy())); setDirty(false); setValidation(null);
    try { const [nextMembers, nextBindings, nextRuns] = await Promise.all([listAgentTeamMembers(identity, team.team_key, team.team_version), listAgentTeamBindings(identity, team.team_key, team.team_version), listAgentTeamRuns(identity, team.team_key, team.team_version)]); setMembers(nextMembers); setBindings(nextBindings); setRuns(nextRuns); } catch (error) { onStatus(message(error)); }
  }
  useEffect(() => { void refresh(); }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);
  useEffect(() => {
    setView((current) => {
      if (workspace === "operations" && current !== "runs" && current !== "replay") return "runs";
      if (workspace === "workspace" && (current === "runs" || current === "replay")) return "catalog";
      return current;
    });
  }, [workspace]);
  const memberProfiles = useMemo(() => new Map(profiles.map((profile) => [profile.id, profile])), [profiles]);

  function selectView(next: View) { setView(next); onWorkspaceChange?.(next === "runs" || next === "replay" ? "operations" : "workspace"); }
  function selectTeam(key: string) { const team = teamOptions.find((item) => item.team_key === key); if (team) void hydrate(team); }

  function updateMember(index: number, patch: Partial<AgentTeamMember>) { setMembers((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item)); setDirty(true); }
  function updateBinding(index: number, patch: Partial<AgentTeamBinding>) { setBindings((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item)); setDirty(true); }
  function addMember() { setMembers((current) => [...current, { member_key: `member-${current.length + 1}`, profile_id: profiles[0]?.id || 0, role: current.length === 0 ? "coordinator" : "reviewer", status: "active" }]); setDirty(true); selectView("builder"); }
  function addBinding() { setBindings((current) => [...current, { provider: "feishu", account_id: accounts[0]?.id || 0, external_chat_id: "", trigger_policy: current.length === 0 ? "mention" : "internal_only", status: "active" }]); setDirty(true); selectView("bindings"); }

  async function createTeam() { const draft: AgentTeamRecord = { id: 0, team_key: `team-${Date.now().toString(36)}`, team_version: 1, display_name: language === "zh" ? "新团队" : "New Team", description: "", status: "draft", schema_version: 1, policy_json: JSON.stringify(defaultTeamPolicy()) }; setTeams((current) => [draft, ...current]); await hydrate(draft); selectView("builder"); }
  async function saveTeam() { if (!selected) return; try { const saved = await saveAgentTeam(identity, { team_key: selected.team_key, scope: "tenant_shared", display_name: selected.display_name, description: selected.description, team_version: selected.team_version, status: "draft", schema_version: selected.schema_version || 1, policy: parsePolicy(policyText) }, selected.id ? selected.team_key : undefined); setTeams((current) => [...current.filter((item) => !(item.team_key === saved.team_key && item.team_version === saved.team_version)), saved]); await hydrate(saved); onDataChanged?.(); onStatus(language === "zh" ? "Team 草稿已保存" : "Team draft saved"); } catch (error) { onStatus(message(error)); } }
  async function saveGraph() { if (!selected?.id) return; try { await saveAgentTeamMembers(identity, selected.team_key, selected.team_version, members); await saveAgentTeamBindings(identity, selected.team_key, selected.team_version, bindings); await hydrate(selected); onDataChanged?.(); onStatus(language === "zh" ? "成员和绑定已保存" : "Members and bindings saved"); } catch (error) { onStatus(message(error)); } }
  async function validate() { if (!selected) return; try { const result = await validateAgentTeam(identity, selected.team_key, { policy: parsePolicy(policyText), members, bindings }); setValidation(result); selectView("replay"); } catch (error) { onStatus(message(error)); } }
  async function publish() { if (!selected?.id) return; try { await publishAgentTeam(identity, selected.team_key, selected.team_version); await refresh(selected.team_key); onDataChanged?.(); onStatus(language === "zh" ? "Team 已发布" : "Team published"); } catch (error) { onStatus(message(error)); } }
  async function inspectRun(run: AgentTeamRun) {
    if (!selected) return;
    try {
      const [fullRun, timeline] = await Promise.all([getAgentTeamRun(identity, selected.team_key, selected.team_version, run.id), getAgentTeamRunTimeline(identity, selected.team_key, selected.team_version, run.id).catch(() => null)]);
      setSelectedRun(fullRun);
      setSelectedRunTimeline(timeline);
      selectView("replay");
    } catch (error) { onStatus(message(error)); }
  }
  async function cancelRun(run: AgentTeamRun) { if (!selected) return; try { await cancelAgentTeamRun(identity, selected.team_key, selected.team_version, run.id); await hydrate(selected); } catch (error) { onStatus(message(error)); } }

  const viewNames: Record<View, string> = { catalog: text(language, "catalog", "目录"), builder: text(language, "builder", "编排器"), bindings: text(language, "bindings", "群绑定"), runs: text(language, "runs", "运行记录"), replay: text(language, "replay", "回放") };
  return <section className="panel agent-team-panel" aria-label={text(language, "Agent Teams", "智能体团队")}>
    <div className="panel-header agent-workbench-header">
      <div className="title-row"><Workflow size={18} /><div><h2>{text(language, "Agent Teams", "智能体团队")}</h2><p>{text(language, "Pin profile versions, bind multiple Feishu bots to one group, and retain collaboration evidence.", "固定 Profile 版本，把多个飞书机器人绑定到同一群组，并保留协作证据。")}</p></div></div>
      <div className="panel-header-actions">
        <label className="team-header-picker"><span>{text(language, "Team", "团队")}</span><select aria-label={text(language, "Select team", "选择团队")} value={selectedKey} onChange={(event) => selectTeam(event.target.value)}>{teamOptions.length === 0 ? <option value="">{text(language, "No teams", "暂无团队")}</option> : teamOptions.map((team) => <option key={team.team_key} value={team.team_key}>{team.display_name} · {team.team_key} · v{team.team_version} · {statusName(team.status, language)}</option>)}</select></label>
        <button className="secondary-button compact-action" type="button" onClick={() => void createTeam()}><Plus size={14} /> {text(language, "New", "新建")}</button>
        <span className="status-pill"><ShieldCheck size={14} /> {text(language, "coordinator-only final", "仅协调者输出")}</span>
        <button className="icon-button" type="button" onClick={() => void refresh()} title={text(language, "Refresh", "刷新")}><RefreshCcw size={16} /></button>
      </div>
    </div>
    <div className="agent-workbench-body">
      <nav className="agent-workbench-tabs" aria-label={text(language, "Team navigation", "团队导航")} role="tablist">
        <span className="agent-workbench-tabs-label">{text(language, "Workspace", "工作区")}</span>
        {(["catalog", "builder", "bindings", "runs", "replay"] as View[]).map((item) => <button key={item} type="button" className={view === item ? "active" : ""} onClick={() => selectView(item)} role="tab" aria-selected={view === item}>{item === "catalog" ? <Users size={15} /> : item === "builder" ? <Workflow size={15} /> : item === "bindings" ? <Bot size={15} /> : item === "runs" ? <History size={15} /> : <GitCompareArrows size={15} />}<span>{viewNames[item]}</span></button>)}
      </nav>
      <div className="agent-workbench-main">
      {view === "catalog" ? <TeamCatalogView language={language} selected={selected} members={members} profiles={profiles} bindings={bindings} onBuild={() => selectView("builder")} onCreate={() => void createTeam()} /> : null}
      {view === "builder" ? <TeamBuilderView language={language} selected={selected} policyText={policyText} members={members} profiles={profiles} memberProfiles={memberProfiles} dirty={dirty} onPolicy={(value) => { setPolicyText(value); setDirty(true); }} onTeamName={(value) => { if (selected) setSelected({ ...selected, display_name: value }); setDirty(true); }} onMember={updateMember} onRemoveMember={(index) => { setMembers((current) => current.filter((_, itemIndex) => itemIndex !== index)); setDirty(true); }} onAdd={addMember} onSave={saveTeam} onSaveGraph={saveGraph} onValidate={() => void validate()} onPublish={() => void publish()} /> : null}
      {view === "bindings" ? <TeamBindingsView language={language} bindings={bindings} accounts={accounts} onBinding={updateBinding} onRemove={(index) => { setBindings((current) => current.filter((_, itemIndex) => itemIndex !== index)); setDirty(true); }} onAdd={addBinding} onSave={saveGraph} /> : null}
      {view === "runs" ? <TeamRunConsole language={language} team={selected} members={members} profiles={profiles} runs={runs} onInspect={(run) => void inspectRun(run)} onCancel={(run) => void cancelRun(run)} /> : null}
      {view === "replay" ? <TeamRunReplayConsole language={language} team={selected} members={members} profiles={profiles} run={selectedRun} timeline={selectedRunTimeline} validation={validation} onBack={() => selectView("runs")} onRefresh={() => { if (selectedRun) void inspectRun(selectedRun); }} /> : null}
      </div>
    </div>
  </section>;
}

function pretty(text: string, fallback: AgentTeamPolicy): string { try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return JSON.stringify(fallback, null, 2); } }
function TeamCatalogView({ language, selected, members, profiles, bindings, onBuild, onCreate }: { language: Language; selected: AgentTeamRecord | null; members: AgentTeamMember[]; profiles: AgentProfileRecord[]; bindings: AgentTeamBinding[]; onBuild: () => void; onCreate: () => void }) {
  const policy = safePolicy(selected?.policy_json);
  const profileByID = new Map(profiles.map((profile) => [profile.id, profile]));
  if (!selected) {
    return <div className="agent-detail-view team-empty-view">
      <div className="team-empty-header">
        <div><span className="eyebrow">{text(language, "Team workspace", "团队工作区")}</span><h3>{text(language, "Create your first collaboration team", "创建第一支协作团队")}</h3><p>{text(language, "Compose a coordinator-led workflow from published Profiles, then connect it to Feishu when it is ready.", "从已发布的 Profile 组建一支由协调者驱动的工作流，准备好后再连接飞书。")}</p></div>
        <button className="primary-button" type="button" onClick={onCreate}><Plus size={15} /> {text(language, "New Team", "新建 Team")}</button>
      </div>
      <div className="team-quick-start-grid">
        <div className="team-quick-start-card"><span className="team-quick-start-number">01</span><div><strong>{text(language, "Pin Profiles", "固定 Profile")}</strong><p>{text(language, "Keep each member's role and version explicit.", "明确每个成员的职责和版本。")}</p></div></div>
        <div className="team-quick-start-card"><span className="team-quick-start-number">02</span><div><strong>{text(language, "Choose a coordinator", "选择协调者")}</strong><p>{text(language, "One writer owns the final answer; reviewers stay read-only.", "由单一 Writer 负责最终输出，其他 Agent 默认只读。")}</p></div></div>
        <div className="team-quick-start-card"><span className="team-quick-start-number">03</span><div><strong>{text(language, "Connect delivery", "连接交付")}</strong><p>{text(language, "Bind Feishu groups without mixing internal mailbox traffic.", "绑定飞书群，同时隔离内部 mailbox 消息。")}</p></div></div>
      </div>
      <div className="team-empty-lower-grid">
        <section className="team-empty-panel"><div className="team-card-heading"><div><strong>{text(language, "Recommended workflow", "推荐协作链路")}</strong><span>{text(language, "Coordinator + parallel review", "协调者 + 并行评审")}</span></div><Workflow size={16} /></div><div className="team-empty-flow"><div><b>01</b><strong>{text(language, "Coordinator", "协调者")}</strong><span>{text(language, "Break down the request and assign work.", "拆解问题并分派任务。")}</span></div><div><b>02</b><strong>{text(language, "Parallel review", "并行评审")}</strong><span>{text(language, "Research, copy and code profiles work in parallel.", "研究、文案和代码 Profile 并行处理。")}</span></div><div><b>03</b><strong>{text(language, "Single writer", "单一 Writer")}</strong><span>{text(language, "Only the accepted result is delivered outside.", "只有被接受的结果可以对外发送。")}</span></div></div></section>
        <section className="team-empty-panel"><div className="team-card-heading"><div><strong>{text(language, "Default guardrails", "默认安全边界")}</strong><span>{text(language, "Ready for production review", "面向生产评审")}</span></div><ShieldCheck size={16} /></div><div className="team-empty-guardrails"><div><Check size={14} /><span>{text(language, "Pinned profile versions", "固定 Profile 版本")}</span></div><div><Check size={14} /><span>{text(language, "Read-only reviewers by default", "评审者默认只读")}</span></div><div><Check size={14} /><span>{text(language, "Internal mailbox stays private", "内部 mailbox 默认隔离")}</span></div><div><Check size={14} /><span>{text(language, "Auditable TeamRun history", "保留可审计 TeamRun 历史")}</span></div></div></section>
      </div>
    </div>;
  }
  return <div className="agent-detail-view team-overview-view">
    <div className="team-overview-header">
      <div><span className="eyebrow">{selected.scope || text(language, "team", "团队")} · v{selected.team_version}</span><h3>{selected.display_name}</h3><p>{selected.description || text(language, "A bounded collaboration graph built from published Profiles.", "基于已发布 Profile 构建的受控协作关系图。")}</p></div>
      <div className="team-overview-actions"><span className={`team-status-chip team-status-${selected.status === "published" ? "published" : selected.status === "draft" ? "draft" : "neutral"}`}>{statusName(selected.status, language)}</span><button className="primary-button" type="button" onClick={onBuild}><Workflow size={15} /> {text(language, "Open builder", "打开编排器")}</button></div>
    </div>
    <div className="team-fact-grid">
      <div><span>{text(language, "Team key", "Team Key")}</span><strong title={selected.team_key}>{selected.team_key}</strong></div>
      <div><span>{text(language, "Members", "成员")}</span><strong>{members.length}</strong></div>
      <div><span>{text(language, "Group bindings", "群绑定")}</span><strong>{bindings.length}</strong></div>
      <div><span>{text(language, "Mode", "模式")}</span><strong>{modeName(policy.orchestration.mode, language)}</strong></div>
      <div><span>{text(language, "Parallel / rounds", "并行 / 轮次")}</span><strong>{policy.orchestration.max_parallel_members || "—"} / {policy.orchestration.max_rounds || "—"}</strong></div>
    </div>
    <div className="team-overview-columns">
      <section className="team-overview-card"><div className="team-card-heading"><div><strong>{text(language, "Member topology", "成员拓扑")}</strong><span>{text(language, "Pinned profile versions", "固定 Profile 版本")}</span></div><span>{members.length}</span></div>{members.length ? <div className="team-member-compact-list">{members.map((member, index) => { const profile = profileByID.get(member.profile_id); return <div className="team-member-compact-row" key={member.id ?? member.member_key}><span className="team-member-index">{String(index + 1).padStart(2, "0")}</span><div><strong>{member.member_key}</strong><span>{roleName(member.role, language)} · {profile?.display_name || text(language, "Unresolved Profile", "未解析 Profile")} · v{profile?.profile_version || "—"}</span></div><em>{member.status || text(language, "active", "启用")}</em></div>; })}</div> : <div className="team-inline-empty">{text(language, "No members configured yet.", "还没有配置成员。")}</div>}</section>
      <section className="team-overview-card"><div className="team-card-heading"><div><strong>{text(language, "Collaboration contract", "协作契约")}</strong><span>{text(language, "Safe defaults for this Team", "这支 Team 的安全默认值")}</span></div><ShieldCheck size={16} /></div><div className="team-rule-list"><div><span>{text(language, "Final output", "最终输出")}</span><strong>{text(language, "Coordinator only", "仅协调者")}</strong></div><div><span>{text(language, "Delivery", "交付")}</span><strong>{bindings.length ? text(language, "Feishu bound", "已绑定飞书") : text(language, "Not connected", "未连接")}</strong></div><div><span>{text(language, "Execution timeout", "执行超时")}</span><strong>{policy.orchestration.run_timeout_seconds || "—"}s</strong></div><div><span>{text(language, "Token budget", "Token 预算")}</span><strong>{policy.orchestration.max_total_tokens || "—"}</strong></div></div></section>
    </div>
    <div className="team-overview-footer"><Check size={14} /> {text(language, "Published versions are immutable; internal mailbox traffic stays inside the Team until the coordinator accepts a result.", "已发布版本不可变；内部 mailbox 消息留在团队内，只有协调者接受结果后才会对外输出。")}</div>
  </div>;
}

function TeamBuilderView({ language, selected, policyText, members, profiles, memberProfiles, dirty, onPolicy, onTeamName, onMember, onRemoveMember, onAdd, onSave, onSaveGraph, onValidate, onPublish }: { language: Language; selected: AgentTeamRecord | null; policyText: string; members: AgentTeamMember[]; profiles: AgentProfileRecord[]; memberProfiles: Map<number, AgentProfileRecord>; dirty: boolean; onPolicy: (value: string) => void; onTeamName: (value: string) => void; onMember: (index: number, patch: Partial<AgentTeamMember>) => void; onRemoveMember: (index: number) => void; onAdd: () => void; onSave: () => void; onSaveGraph: () => void; onValidate: () => void; onPublish: () => void }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Team policy", "团队策略")} {dirty ? text(language, "· unsaved", "· 未保存") : ""}</span><h3>{text(language, "Builder", "编排器")}</h3></div><div className="button-row"><button className="secondary-button" type="button" onClick={onValidate}><ShieldCheck size={15} /> {text(language, "Validate", "校验")}</button><button className="primary-button" type="button" onClick={onSave}><Save size={15} /> {text(language, "Save draft", "保存草稿")}</button>{selected?.status === "draft" ? <button className="secondary-button" type="button" onClick={onPublish}>{text(language, "Publish", "发布")}</button> : null}</div></div><div className="profile-form-grid"><label>{text(language, "Team key", "Team Key")}<input readOnly value={selected?.team_key || ""} /></label><label>{text(language, "Display name", "显示名称")}<input value={selected?.display_name || ""} onChange={(event) => onTeamName(event.target.value)} /></label><label>{text(language, "Orchestration mode", "编排模式")}<select value={safePolicy(policyText).orchestration.mode} onChange={(event) => onPolicy(JSON.stringify({ ...safePolicy(policyText), orchestration: { ...safePolicy(policyText).orchestration, mode: event.target.value } }, null, 2))}><option value="coordinator">{text(language, "coordinator", "协调者")}</option><option value="parallel_review">{text(language, "parallel_review", "并行评审")}</option></select></label></div><div className="team-member-editor"><div className="section-heading"><h4>{text(language, "Members · pinned versions", "成员 · 固定版本")}</h4><button className="secondary-button compact-action" type="button" onClick={onAdd}><Plus size={14} /> {text(language, "Add member", "添加成员")}</button></div>{members.map((member, index) => <div className="team-member-row" key={member.id ?? member.member_key}><input value={member.member_key} onChange={(event) => onMember(index, { member_key: event.target.value })} placeholder={text(language, "member key", "成员 Key")} /><select value={member.profile_id} onChange={(event) => onMember(index, { profile_id: Number(event.target.value) })}>{profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.display_name} · v{profile.profile_version}</option>)}</select><select value={member.role} onChange={(event) => onMember(index, { role: event.target.value })}>{["coordinator", "researcher", "writer", "coder", "reviewer"].map((role) => <option key={role} value={role}>{roleName(role, language)}</option>)}</select><span className="team-member-pin">{memberProfiles.get(member.profile_id)?.profile_key || text(language, "unresolved", "未解析")}</span><button className="icon-button compact" type="button" onClick={() => onRemoveMember(index)} title={text(language, "Remove", "删除")}>×</button></div>)}</div><div className="raw-json-editor"><div className="section-heading"><h4>Team policy JSON</h4><span className="valid-text"><Check size={14} /> {text(language, "server validated", "服务端已校验")}</span></div><textarea rows={13} value={policyText} onChange={(event) => onPolicy(event.target.value)} spellCheck={false} /></div><button className="primary-button" disabled={!selected?.id} type="button" onClick={onSaveGraph}><Users size={15} /> {text(language, "Save member graph", "保存成员关系图")}</button></div>; }

function TeamBindingsView({ language, bindings, accounts, onBinding, onRemove, onAdd, onSave }: { language: Language; bindings: AgentTeamBinding[]; accounts: ChannelAccountRecord[]; onBinding: (index: number, patch: Partial<AgentTeamBinding>) => void; onRemove: (index: number) => void; onAdd: () => void; onSave: () => void }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Feishu group routing", "飞书群路由")}</span><h3>{text(language, "Bot & group bindings", "机器人与群绑定")}</h3></div><div className="button-row"><button className="secondary-button" type="button" onClick={onAdd}><Plus size={14} /> {text(language, "Add binding", "添加绑定")}</button><button className="primary-button" type="button" onClick={onSave}><Save size={15} /> {text(language, "Save bindings", "保存绑定")}</button></div></div><div className="binding-security-note"><Bot size={17} /><span>{text(language, "One inbound event maps to one TeamRun. internal_only bots never answer ordinary group messages.", "一条入站事件只对应一个 TeamRun；internal_only 机器人不会回复普通群消息。")}</span></div><div className="team-binding-list">{bindings.map((binding, index) => <div className="team-binding-row" key={`${binding.account_id}-${binding.external_chat_id}`}><select value={binding.account_id} onChange={(event) => onBinding(index, { account_id: Number(event.target.value) })}><option value={0}>{text(language, "Select bot", "选择机器人")}</option>{accounts.map((account) => <option key={account.id} value={account.id}>{account.account_key} · {account.provider}</option>)}</select><input value={binding.external_chat_id} onChange={(event) => onBinding(index, { external_chat_id: event.target.value })} placeholder="external_chat_id" /><input value={binding.external_thread_id || ""} onChange={(event) => onBinding(index, { external_thread_id: event.target.value })} placeholder={text(language, "thread (optional)", "thread（可选）")} /><select value={binding.trigger_policy} onChange={(event) => onBinding(index, { trigger_policy: event.target.value })}><option value="mention">mention</option><option value="command">command</option><option value="internal_only">internal_only</option></select><button className="icon-button compact" type="button" onClick={() => onRemove(index)} title={text(language, "Remove", "删除")}>×</button></div>)}</div></div>; }

function safePolicy(text: string | undefined): AgentTeamPolicy { try { return text ? JSON.parse(text) as AgentTeamPolicy : defaultTeamPolicy(); } catch { return defaultTeamPolicy(); } }
