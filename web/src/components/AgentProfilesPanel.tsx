import { Archive, Bot, Check, Copy, Eye, FileJson, History, MessageSquareText, Pencil, Plus, RefreshCcw, RotateCcw, Save, ShieldCheck, Sparkles, Users, WandSparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  archiveAgentProfile,
  archiveAgentProfileBinding,
  getAgentProfileBinding,
  getAgentProfileAssignment,
  listAgentProfiles,
  listChannelAccounts,
  publishAgentProfile,
  rollbackAgentProfile,
  saveAgentProfile,
  saveAgentProfileAssignment,
  saveAgentProfileBinding,
  validateAgentProfile
} from "../lib/api";
import { defaultAgentProfileDocument, parseAgentProfileJSON, profileCapabilitySummary, validateAgentProfileDraft } from "../lib/agentProfiles";
import { useI18n } from "../lib/i18n";
import { ProfileConversationsDialog } from "./ProfileConversationsDialog";
import type { AgentProfileAssignment, AgentProfileDocument, AgentProfileRecord, ChannelAccountRecord, IdentityConfig } from "../lib/types";

type Props = { identity: IdentityConfig; onStatus: (message: string) => void; onDataChanged?: () => void; onOpenSession?: (sessionId: number) => void; embedded?: boolean; onDirtyChange?: (dirty: boolean) => void; onBusyChange?: (busy: boolean) => void; isolatedEnvironment?: boolean };
type View = "catalog" | "editor" | "preview" | "versions" | "assignment" | "binding" | "conversations";
type Language = "en" | "zh";
const text = (language: Language, en: string, zh: string) => language === "zh" ? zh : en;
const profileName = (profile: AgentProfileRecord, language: Language) => language === "zh" ? ({ "chat-assistant": "聊天助手", coder: "编码智能体", copywriter: "文案智能体" }[profile.profile_key] || profile.display_name) : profile.display_name;
const profileDescription = (profile: AgentProfileRecord, language: Language) => language === "zh" ? ({ "chat-assistant": "通用对话助手", coder: "受控的编码智能体", copywriter: "面向受众的营销内容助手" }[profile.profile_key] || profile.description) : profile.description;
const statusName = (status: string, language: Language) => language === "zh" ? ({ published: "已发布", draft: "草稿", archived: "已归档", validating: "校验中" }[status] || status) : status;
const sourceKindName = (kind: string | undefined, language: Language) => language === "zh" ? ({ builtin: "内置定义", database: "MySQL 数据库", file: "本地文件", generated: "生成草稿" }[kind || ""] || kind || "未知来源") : ({ builtin: "Builtin definition", database: "MySQL database", file: "Local file", generated: "Generated draft" }[kind || ""] || kind || "Unknown source");
function sourceForProfile(profile: AgentProfileRecord): AgentProfileRecord {
  if (profile.source_kind) return profile;
  if (profile.scope === "builtin") return { ...profile, source_kind: "builtin", source_ref: "web/src/components/AgentProfilesPanel.tsx" };
  return { ...profile, source_kind: "database", source_ref: `agent_profiles/${profile.id}` };
}
function ProfileSource({ language, profile }: { language: Language; profile: AgentProfileRecord | null }) {
  if (!profile) return null;
  const source = sourceForProfile(profile);
  const sourceLabel = sourceKindName(source.source_kind, language);
  const ref = source.source_path || source.source_ref || text(language, "No source reference", "暂无来源引用");
  const pathText = source.source_kind === "database" ? text(language, "No local file; stored in MySQL.", "无本地文件，配置存储在 MySQL。") : source.source_kind === "builtin" ? text(language, "Not an independent Profile file.", "不是独立的 Profile 文件。") : source.source_path || text(language, "No local path", "无本地路径");
  return <div className="profile-source-card"><div><span className="eyebrow">{text(language, "Profile source", "Profile 来源")}</span><strong>{sourceLabel}</strong></div><div><span>{text(language, "Reference", "来源引用")}</span><code>{ref}</code></div><div><span>{text(language, "Local path", "本地文件路径")}</span><code>{pathText}</code></div></div>;
}
const roleName = (role: string | undefined, language: Language) => language === "zh" ? ({ owner: "所有者", admin: "管理员", member: "成员" }[role || "member"] || role || "成员") : role || "member";
const capabilityText = (value: string | number, language: Language) => language === "zh" ? String(value).replace("allowed", "允许").replace("denied", "拒绝").replace("tenant memory", "租户记忆").replace("user memory", "用户记忆").replace("knowledge base", "知识库") : String(value);

const builtinProfiles: AgentProfileRecord[] = [
  { id: 0, profile_key: "chat-assistant", display_name: "Chat Assistant", description: "General conversation assistant", profile_version: 1, status: "published", scope: "builtin", source_kind: "builtin", source_ref: "web/src/components/AgentProfilesPanel.tsx", config_json: JSON.stringify(defaultAgentProfileDocument("chat")) },
  { id: 0, profile_key: "copywriter", display_name: "Copywriter", description: "Audience-aware marketing content", profile_version: 1, status: "published", scope: "builtin", source_kind: "builtin", source_ref: "web/src/components/AgentProfilesPanel.tsx", config_json: JSON.stringify({ ...defaultAgentProfileDocument("chat"), identity: { display_name: "Copywriter", description: "Audience-aware marketing content" }, prompt: { ...defaultAgentProfileDocument("chat").prompt, output_style: "marketing", persona: "You are a careful copywriter who clarifies audience, channel, goals, and constraints before drafting." }, capabilities: { ...defaultAgentProfileDocument("chat").capabilities, tools: { allow: ["Read", "WebSearch"], deny: [] }, skills: ["copywriting"] } }) },
  { id: 0, profile_key: "coder", display_name: "Coder", description: "Guarded coding agent", profile_version: 1, status: "published", scope: "builtin", source_kind: "builtin", source_ref: "web/src/components/AgentProfilesPanel.tsx", config_json: JSON.stringify(defaultAgentProfileDocument("code")) }
];

function parseDocument(record: AgentProfileRecord | null): AgentProfileDocument {
  if (!record) return defaultAgentProfileDocument("chat");
  try { return parseCompleteDocument(record.config_json); } catch { return defaultAgentProfileDocument("chat"); }
}

function parseCompleteDocument(raw: string): AgentProfileDocument {
  const next = parseAgentProfileJSON(raw);
  if (!next.identity || !next.prompt || !next.capabilities?.tools || !next.context || !next.safety || !next.execution) throw new Error("Profile sections are required");
  return next;
}

function errorMessage(error: unknown): string { return error instanceof Error ? error.message : String(error); }

export function AgentProfilesPanel({ identity, onStatus, onDataChanged, onOpenSession, embedded = false, onDirtyChange, onBusyChange, isolatedEnvironment = false }: Props) {
  const { language } = useI18n();
  const [profiles, setProfiles] = useState<AgentProfileRecord[]>(embedded ? [] : builtinProfiles);
  const [accounts, setAccounts] = useState<ChannelAccountRecord[]>([]);
  const [selectedKey, setSelectedKey] = useState("copywriter");
  const [view, setView] = useState<View>("catalog");
  const [document, setDocument] = useState<AgentProfileDocument>(() => parseDocument(builtinProfiles[1]));
  const [jsonText, setJsonText] = useState(() => JSON.stringify(parseDocument(builtinProfiles[1]), null, 2));
  const [jsonError, setJsonError] = useState("");
  const [displayName, setDisplayName] = useState("Copywriter");
  const [description, setDescription] = useState("Audience-aware marketing content");
  const [scope, setScope] = useState("tenant_shared");
  const [surface, setSurface] = useState("web_chat");
  const [assignment, setAssignment] = useState<AgentProfileAssignment | null>(null);
  const [bindingAccount, setBindingAccount] = useState("");
  const [binding, setBinding] = useState<{ account_id: number; provider: string; binding_key: string } | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [draftDirty, setDraftDirty] = useState(false);
  const [report, setReport] = useState<{ valid: boolean; issues?: Array<{ code: string; field?: string; message: string }> } | null>(null);
  const bindingRequest = useRef(0);
  const refreshRequest = useRef(0);
  const dirtyCallback = useRef(onDirtyChange);
  dirtyCallback.current = onDirtyChange;

  const selected = useMemo(() => profiles.reduce<AgentProfileRecord | null>((latest, profile) => {
    if (profile.profile_key !== selectedKey || (!embedded && profile.status === "archived")) return latest;
    return !latest || profile.profile_version > latest.profile_version ? profile : latest;
  }, null), [profiles, selectedKey, embedded]);
  const versions = useMemo(() => profiles.filter((profile) => profile.profile_key === selectedKey).sort((a, b) => b.profile_version - a.profile_version), [profiles, selectedKey]);
  const profileOptions = useMemo(() => {
    const latest = new Map<string, AgentProfileRecord>();
    for (const profile of profiles) {
      if (!embedded && profile.status === "archived") continue;
      const current = latest.get(profile.profile_key);
      if (!current || profile.profile_version > current.profile_version) latest.set(profile.profile_key, profile);
    }
    return Array.from(latest.values()).sort((a, b) => profileName(a, language).localeCompare(profileName(b, language)));
  }, [language, profiles, embedded]);
  const capabilitySummary = useMemo(() => profileCapabilitySummary(document), [document]);
  const editable = Boolean(selected && selected.scope !== "builtin");
  const bindingDirty = bindingAccount !== (binding ? String(binding.account_id) : "");
  const dirty = draftDirty || bindingDirty;

  useEffect(() => { dirtyCallback.current?.(dirty || saving); }, [dirty, saving]);
  useEffect(() => { onBusyChange?.(saving); }, [saving, onBusyChange]);
  useEffect(() => () => { dirtyCallback.current?.(false); bindingRequest.current++; refreshRequest.current++; }, []);

  function canDiscard() {
    return !dirty || window.confirm(text(language, "Discard unsaved changes?", "放弃尚未保存的修改？"));
  }

  async function refresh(nextKey = selectedKey) {
    const request = ++refreshRequest.current;
    setLoading(true);
    try {
      const [remoteProfiles, remoteAccounts] = await Promise.all([listAgentProfiles(identity), listChannelAccounts(identity).catch(() => [])]);
      const map = new Map<string, AgentProfileRecord>();
      if (request !== refreshRequest.current) return;
      for (const item of [...(embedded ? [] : builtinProfiles), ...remoteProfiles]) { const sourced = sourceForProfile(item); map.set(`${sourced.profile_key}@${sourced.profile_version}@${sourced.scope}`, sourced); }
      const merged = Array.from(map.values()).sort((a, b) => a.profile_key.localeCompare(b.profile_key) || b.profile_version - a.profile_version);
      setProfiles(merged);
      setAccounts(remoteAccounts);
      const next = merged.find((item) => item.profile_key === nextKey && (embedded || item.status !== "archived")) ?? merged[0] ?? null;
      if (next) hydrate(next);
      else { setSelectedKey(""); setDraftDirty(false); setBinding(null); setBindingAccount(""); }
    } catch (error) { onStatus(errorMessage(error)); }
    finally { if (request === refreshRequest.current) setLoading(false); }
  }

  function hydrate(record: AgentProfileRecord) {
    const request = ++bindingRequest.current;
    setSelectedKey(record.profile_key);
    const next = parseDocument(record);
    setDocument(next);
    try { parseCompleteDocument(record.config_json); setJsonText(JSON.stringify(next, null, 2)); setJsonError(""); }
    catch { setJsonText(record.config_json); setJsonError(text(language, "Profile configuration is incomplete or invalid. Repair the original JSON before saving.", "Profile 配置不完整或格式错误，请修复原始 JSON 后再保存。")); }
    setDisplayName(record.display_name); setDescription(record.description || next.identity.description || ""); setScope(record.scope || "tenant_shared");
    setDraftDirty(false); setReport(null); setView("catalog");
    setBinding(null); setBindingAccount("");
    if (record.id > 0) {
      void getAgentProfileBinding(identity, record.profile_key, record.profile_version).then((item) => { if (request !== bindingRequest.current) return; setBinding(item); setBindingAccount(String(item.account_id)); }).catch(() => { if (request !== bindingRequest.current) return; setBinding(null); setBindingAccount(""); });
    }
  }

  useEffect(() => { void refresh(); }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);
  useEffect(() => {
    if (view !== "assignment") return;
    void getAgentProfileAssignment(identity, surface).then(setAssignment).catch(() => setAssignment(null));
  }, [view, surface, identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  function selectProfile(key: string) { if (key === selectedKey || !canDiscard()) return; const record = profileOptions.find((item) => item.profile_key === key); if (record) hydrate(record); }
  function handleNewProfile() {
    if (!canDiscard()) return;
    const draft: AgentProfileRecord = { id: 0, profile_key: `custom-${Date.now().toString(36)}`, display_name: text(language, "New Agent", "新智能体"), description: "", profile_version: 1, status: "draft", scope: "tenant_shared", source_kind: "generated", source_ref: "webui-draft", config_json: JSON.stringify(defaultAgentProfileDocument("chat")) };
    setProfiles((current) => [draft, ...current]);
    hydrate(draft);
    setDraftDirty(true);
    setView("editor");
  }
  function handleCopyProfile() {
    if (!selected || !canDiscard()) return;
    const nextDocument = parseDocument(selected);
    const name = `${selected.display_name} ${text(language, "copy", "副本")}`;
    nextDocument.identity = { ...nextDocument.identity, display_name: name };
    const draft: AgentProfileRecord = { ...selected, id: 0, profile_key: `${selected.profile_key}-copy-${Date.now().toString(36)}`, display_name: name, profile_version: 1, status: "draft", scope: "user_private", source_kind: "generated", source_path: undefined, source_ref: "webui-draft", requested_hash: undefined, effective_hash: undefined, config_json: JSON.stringify(nextDocument) };
    setProfiles((current) => [draft, ...current]); hydrate(draft); setDraftDirty(true); setView("editor");
  }
  function markDirty() { setDraftDirty(true); setReport(null); }
  function setField(path: "mode" | "persona" | "output_style" | "language", value: string) { if (jsonError) return; const next = { ...document, prompt: { ...document.prompt, [path]: value } }; setDocument(next); setJsonText(JSON.stringify(next, null, 2)); markDirty(); }
  function parseEditor(value: string) { setJsonText(value); markDirty(); try { const next = parseAgentProfileJSON(value); if (!next.identity || !next.prompt || !next.capabilities?.tools || !next.context || !next.safety || !next.execution) throw new Error(text(language, "Profile sections are required", "Profile 必须包含 identity、prompt、capabilities、execution、context、safety")); setDocument(next); setDisplayName(next.identity.display_name || ""); setDescription(next.identity.description || ""); setJsonError(""); } catch (error) { setJsonError(errorMessage(error)); } }
  function updateIdentity(field: "display_name" | "description", value: string) {
    if (jsonError) return;
    if (field === "display_name") setDisplayName(value); else setDescription(value);
    const next = { ...document, identity: { ...document.identity, [field]: value } };
    setDocument(next); setJsonText(JSON.stringify(next, null, 2)); setJsonError(""); markDirty();
  }

  async function handleValidate() {
    if (jsonError) { onStatus(jsonError); return; }
    const local = validateAgentProfileDraft(document);
    if (!local.valid) { setReport(local); setView("preview"); return; }
    try { const remote = await validateAgentProfile(identity, { profile_key: selectedKey, display_name: displayName, config: document }, editable ? selectedKey : undefined); setReport(remote); setView("preview"); }
    catch (error) { onStatus(errorMessage(error)); }
  }

  async function handleSave() {
    if (saving) return;
    if (bindingDirty && !window.confirm(text(language, "Discard the pending bot binding and save this profile draft?", "放弃尚未保存的机器人绑定并保存 Profile 草稿？"))) return;
    if (!editable && selected?.scope === "builtin") { onStatus(language === "zh" ? "内置 Profile 只读" : "Builtin profiles are read-only"); return; }
    if (jsonError) { onStatus(jsonError); return; }
    const local = validateAgentProfileDraft(document); if (!local.valid) { setReport(local); setView("preview"); return; }
    setSaving(true);
    try { const saved = await saveAgentProfile(identity, { profile_key: selectedKey, scope, display_name: displayName.trim(), description: description.trim(), profile_version: selected?.id ? undefined : 1, status: "draft", config: document }, selected?.id ? selectedKey : undefined); setProfiles((current) => [...current.filter((item) => !(item.profile_key === saved.profile_key && item.profile_version === saved.profile_version)), saved]); hydrate(saved); onDataChanged?.(); onStatus(language === "zh" ? "Profile 草稿已保存" : "Profile draft saved"); }
    catch (error) { onStatus(errorMessage(error)); }
    finally { setSaving(false); }
  }

  async function handlePublish() { if (draftDirty) { onStatus(text(language, "Save the draft before publishing", "请先保存草稿再发布")); return; } if (!selected || selected.id === 0 || selected.status !== "draft") return; setSaving(true); try { const validation = await validateAgentProfile(identity, { profile_key: selected.profile_key, display_name: displayName, config: document }, selected.profile_key); setReport(validation); if (!validation.valid) return; await publishAgentProfile(identity, selected.profile_key, selected.profile_version); await refresh(selected.profile_key); onDataChanged?.(); onStatus(language === "zh" ? "Profile 已发布" : "Profile published"); } catch (error) { onStatus(errorMessage(error)); } finally { setSaving(false); } }
  async function handleArchive() {
    if (!selected || selected.id === 0 || saving || !canDiscard()) return;
    const confirmed = window.confirm(text(language, "Deactivate this profile? It will stop being available for new assignments, while its version history is kept.", "停用这个智能体配置？停用后不会再用于新的分配，但历史版本会保留。"));
    if (!confirmed) return;
    setSaving(true);
    try {
      await archiveAgentProfile(identity, selected.profile_key, selected.profile_version);
      await refresh();
      onDataChanged?.();
      onStatus(language === "zh" ? "智能体配置已停用，历史版本已保留" : "Profile deactivated; version history was kept");
    } catch (error) {
      onStatus(errorMessage(error));
    } finally {
      setSaving(false);
    }
  }
  async function handleRollback(version: number) { if (saving || versions.find((item) => item.profile_version === version)?.status === "archived" || !canDiscard()) return; setSaving(true); try { const next = await rollbackAgentProfile(identity, selectedKey, version); setProfiles((current) => [...current, next]); hydrate(next); setView("editor"); onDataChanged?.(); onStatus(language === "zh" ? "已创建回滚草稿" : "Rollback draft created"); } catch (error) { onStatus(errorMessage(error)); } finally { setSaving(false); } }
  async function handleAssignment() { if (!selected || selected.id === 0) return; try { const next = await saveAgentProfileAssignment(identity, { surface, profile_id: selected.id }); setAssignment(next); onStatus(language === "zh" ? "Surface 已分配" : "Surface assignment saved"); } catch (error) { onStatus(errorMessage(error)); } }
  async function handleBinding() {
    if (!selected || selected.id === 0 || !bindingAccount || saving) return;
    const account = accounts.find((item) => String(item.id) === bindingAccount);
    if (!account) return;
    setSaving(true);
    try {
      const next = await saveAgentProfileBinding(identity, selected.profile_key, selected.profile_version, { account_id: account.id, provider: account.provider, binding_key: account.account_key });
      setBinding(next); onDataChanged?.(); onStatus(language === "zh" ? "Bot 已绑定" : "Bot binding saved");
    } catch (error) { onStatus(errorMessage(error)); }
    finally { setSaving(false); }
  }

  async function handleArchiveBinding() {
    if (!selected || saving) return;
    setSaving(true);
    try {
      await archiveAgentProfileBinding(identity, selected.profile_key, selected.profile_version);
      setBinding(null); setBindingAccount(""); onDataChanged?.();
    } catch (error) { onStatus(errorMessage(error)); }
    finally { setSaving(false); }
  }

  const viewNames: Record<View, string> = { catalog: text(language, "catalog", "目录"), editor: text(language, "editor", "编辑器"), preview: text(language, "preview", "预览"), versions: text(language, "versions", "版本"), assignment: text(language, "assignment", "分配"), binding: text(language, "binding", "绑定"), conversations: text(language, "conversations", "对话") };
  return <section className={`panel agent-profile-panel${embedded ? " settings-profile-workbench" : ""}`} aria-label={text(language, "Agent Profiles", "智能体 Profile")} aria-busy={loading || saving}>
    {embedded ? <div className="settings-profile-toolbar">
      <div className="settings-profile-heading"><h2>{text(language, "Profile catalog", "智能体配置")} <span>{profileOptions.length}</span></h2><p>{text(language, "Create a configuration, save it, then publish it when you are ready to use it.", "先创建并保存智能体配置，需要正式使用时再发布。")}</p></div>
      <div className="button-row">
        <button type="button" className="secondary-button" disabled={!selected || saving || loading} onClick={handleCopyProfile}><Copy size={15} />{text(language, "Copy", "复制配置")}</button>
        <button type="button" className="primary-button" disabled={saving || loading} onClick={handleNewProfile}><Plus size={15} />{text(language, "New profile", "新建智能体配置")}</button>
        <button type="button" className="icon-button" disabled={saving || loading} aria-label={text(language, "Refresh profiles", "刷新 Profile")} title={text(language, "Refresh profiles", "刷新 Profile")} onClick={() => { if (canDiscard()) void refresh(); }}><RefreshCcw size={16} /></button>
      </div>
    </div> : null}
    <div hidden={embedded}>
    <div className="panel-header agent-workbench-header"><div className="title-row"><Sparkles size={18} /><div><h2>{text(language, "Agent Profiles", "智能体 Profile")}</h2><p>{text(language, "Create publishable execution identities without changing the legacy code/chat path.", "创建多个可发布的执行身份，不改变现有 code/chat 主流程。")}</p></div></div><div className="panel-header-actions"><label className="profile-header-picker"><span>{text(language, "Profile", "Profile")}</span><select aria-label={text(language, "Select profile", "选择 Profile")} value={selectedKey} onChange={(event) => selectProfile(event.target.value)}>{profileOptions.map((profile) => <option key={profile.profile_key} value={profile.profile_key}>{profileName(profile, language)} · {profile.profile_key} · v{profile.profile_version} · {statusName(profile.status, language)}</option>)}</select></label><button className="secondary-button compact-action" type="button" onClick={handleNewProfile}><Plus size={14} /> {text(language, "New", "新建")}</button><span className="status-pill"><ShieldCheck size={14} /> {roleName(identity.role, language)}</span><button className="icon-button" onClick={() => { if (canDiscard()) void refresh(); }} title={text(language, "Refresh", "刷新")} type="button"><RefreshCcw size={16} /></button></div></div>
    </div>
    {embedded ? <div className="settings-profile-catalog">{profileOptions.map((profile) => <button type="button" className={profile.profile_key === selectedKey ? "selected" : ""} aria-pressed={profile.profile_key === selectedKey} key={profile.profile_key} onClick={() => selectProfile(profile.profile_key)} disabled={saving || loading}><Bot size={21} /><strong>{profileName(profile, language)}</strong><p>{profile.description || text(language, "Ready to customize", "可以继续自定义")}</p><footer><span>{statusName(profile.status, language)}</span><span>v{profile.profile_version}</span></footer></button>)}{!loading && profileOptions.length === 0 ? <div className="settings-profile-empty"><Bot size={24} /><strong>{text(language, "No profiles yet", "还没有智能体配置")}</strong><p>{text(language, "Create one to give your agent a name, role and working style.", "创建一个配置，为智能体设置名称、角色和工作方式。")}</p><button type="button" className="primary-button" onClick={handleNewProfile}><Plus size={15} />{text(language, "Create profile", "创建智能体配置")}</button></div> : null}</div> : null}
    {loading ? <p role="status">{text(language, "Loading profiles...", "正在加载 Profile...")}</p> : null}
    {jsonError && view !== "editor" ? <p role="alert">{jsonError}</p> : null}
    <div className={embedded ? "settings-profile-workarea" : undefined}>
      <div className="agent-workbench-tabs" role="tablist" aria-label={text(language, "Profile views", "Profile 视图")}>{(["catalog", "editor", "preview", "versions", ...(!embedded ? ["assignment"] : []), "binding", "conversations"] as View[]).map((item) => <button key={item} type="button" disabled={saving || loading || !selected} className={view === item ? "active" : ""} onClick={() => setView(item)} role="tab" aria-selected={view === item}>{item === "catalog" ? <Users size={15} /> : item === "editor" ? <Pencil size={15} /> : item === "preview" ? <Eye size={15} /> : item === "versions" ? <History size={15} /> : item === "assignment" ? <WandSparkles size={15} /> : item === "conversations" ? <MessageSquareText size={15} /> : <Bot size={15} />}<span>{viewNames[item]}</span></button>)}</div>
      <div className="agent-workbench-body">
        <div className="agent-workbench-main">
          {view === "catalog" && selected ? <CatalogView language={language} selected={selected} summary={capabilitySummary} onEdit={() => setView("editor")} /> : null}
          {view === "editor" ? <EditorView language={language} profile={selected} document={document} jsonText={jsonText} jsonError={jsonError} displayName={displayName} description={description} scope={scope} editable={editable && !saving} saving={saving} dirty={draftDirty} onDisplayName={(value) => updateIdentity("display_name", value)} onDescription={(value) => updateIdentity("description", value)} onScope={(value) => { setScope(value); markDirty(); }} onField={setField} onJson={parseEditor} onValidate={() => void handleValidate()} onSave={() => void handleSave()} /> : null}
          {view === "preview" ? <PreviewView busy={saving} dirty={draftDirty} language={language} document={document} report={report} summary={capabilitySummary} onValidate={() => void handleValidate()} onPublish={() => void handlePublish()} onArchive={() => void handleArchive()} selected={selected} /> : null}
          {view === "versions" ? <VersionsView language={language} busy={saving} versions={versions} onRollback={(version) => void handleRollback(version)} /> : null}
          {view === "assignment" ? <AssignmentView language={language} surface={surface} assignment={assignment} profiles={profiles.filter((item) => item.status === "published")} selected={selected} onSurface={setSurface} onSelect={selectProfile} onSave={() => void handleAssignment()} /> : null}
          {view === "binding" ? <BindingView language={language} accounts={accounts} accountId={bindingAccount} binding={binding} editable={editable && Boolean(selected?.id) && !saving} onAccount={setBindingAccount} onSave={() => void handleBinding()} onArchive={() => void handleArchiveBinding()} /> : null}
        </div>
      </div>
    </div>
    {view === "conversations" && selected ? <ProfileConversationsDialog identity={identity} profile={selected} onClose={() => setView("catalog")} onOpenSession={onOpenSession} tenantMessages={embedded} showTrace={!isolatedEnvironment} /> : null}
  </section>;
}

function CatalogView({ language, selected, summary, onEdit }: { language: Language; selected: AgentProfileRecord | null; summary: ReturnType<typeof profileCapabilitySummary>; onEdit: () => void }) { return <div className="agent-detail-view"><div className="detail-hero"><div><span className="eyebrow">{selected?.scope === "builtin" && language === "zh" ? "内置" : selected?.scope || text(language, "catalog", "目录")} · v{selected?.profile_version || 1}</span><h3>{selected ? profileName(selected, language) : text(language, "Agent Profile", "智能体 Profile")}</h3><p>{selected ? profileDescription(selected, language) : text(language, "Select a profile to inspect its execution policy.", "选择一个 Profile 查看执行策略。")}</p></div><button className="primary-button" type="button" onClick={onEdit}><Pencil size={15} /> {text(language, "Edit profile", "编辑 Profile")}</button></div><ProfileSource language={language} profile={selected} /><div className="profile-summary-grid"><Metric label={text(language, "Status", "状态")} value={selected ? statusName(selected.status, language) : "-"} /><Metric label={text(language, "Tools", "工具")} value={capabilityText(summary.tools, language)} /><Metric label={text(language, "Skills", "技能")} value={capabilityText(summary.skills, language)} /><Metric label={text(language, "Context", "上下文")} value={capabilityText(summary.context, language)} /></div><div className="profile-policy-card"><h4>{text(language, "Runtime contract", "运行时契约")}</h4><p>{text(language, "Published versions are immutable. The effective runtime is still constrained by server and tenant policy.", "已发布版本不可变，最终运行时仍受服务端和租户策略约束。")}</p><pre>{selected?.effective_hash || selected?.requested_hash || text(language, "builtin catalog profile", "内置目录 Profile")}</pre></div></div>; }

function EditorView({ language, profile, document, jsonText, jsonError, displayName, description, scope, editable, saving, dirty, onDisplayName, onDescription, onScope, onField, onJson, onValidate, onSave }: { language: Language; profile: AgentProfileRecord | null; document: AgentProfileDocument; jsonText: string; jsonError: string; displayName: string; description: string; scope: string; editable: boolean; saving: boolean; dirty: boolean; onDisplayName: (value: string) => void; onDescription: (value: string) => void; onScope: (value: string) => void; onField: (path: "mode" | "persona" | "output_style" | "language", value: string) => void; onJson: (value: string) => void; onValidate: () => void; onSave: () => void }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Basics + policy", "基础信息与策略")}</span><h3>{text(language, "Profile editor", "Profile 编辑器")} {dirty ? <span className="dirty-dot">●</span> : null}</h3></div><div className="button-row"><button className="secondary-button" type="button" onClick={onValidate}><ShieldCheck size={15} /> {text(language, "Validate", "校验")}</button><button className="primary-button" disabled={!editable || saving} type="button" onClick={onSave}><Save size={15} /> {saving ? text(language, "Saving...", "保存中...") : text(language, "Save draft", "保存草稿")}</button></div></div><ProfileSource language={language} profile={profile} /><div className="profile-form-grid"><label>{text(language, "Profile key", "Profile Key")}<input readOnly value={profile?.profile_key || ""} /></label><label>{text(language, "Display name", "显示名称")}<input disabled={!editable} value={displayName} onChange={(event) => onDisplayName(event.target.value)} /></label><label>{text(language, "Scope", "范围")}<select disabled={!editable} value={scope} onChange={(event) => onScope(event.target.value)}><option value="tenant_shared">{text(language, "Tenant shared", "租户共享")}</option><option value="user_private">{text(language, "Private", "个人私有")}</option></select></label><label className="wide">{text(language, "Description", "描述")}<textarea disabled={!editable} rows={2} value={description} onChange={(event) => onDescription(event.target.value)} /></label><label>{text(language, "Prompt mode", "Prompt 模式")}<select disabled={!editable} value={document.prompt.mode} onChange={(event) => onField("mode", event.target.value)}><option value="chat">chat</option><option value="code">code</option></select></label><label>{text(language, "Language", "语言")}<input disabled={!editable} value={document.prompt.language || ""} onChange={(event) => onField("language", event.target.value)} /></label><label>{text(language, "Output style", "输出风格")}<input disabled={!editable} value={document.prompt.output_style || ""} onChange={(event) => onField("output_style", event.target.value)} /></label><label className="wide">Persona<textarea disabled={!editable} rows={3} value={document.prompt.persona || ""} onChange={(event) => onField("persona", event.target.value)} /></label></div><div className="raw-json-editor"><div className="section-heading"><div><h4><FileJson size={15} /> {text(language, "Raw JSON", "原始 JSON")}</h4><small>{text(language, "Admin debug surface; server validator remains authoritative.", "管理员调试区域，最终以服务端校验为准。")}</small></div>{jsonError ? <span className="error-text">{jsonError}</span> : <span className="valid-text"><Check size={14} /> {text(language, "valid JSON", "JSON 有效")}</span>}</div><textarea aria-label={text(language, "Profile JSON", "Profile JSON")} disabled={!editable} rows={15} value={jsonText} onChange={(event) => onJson(event.target.value)} spellCheck={false} /></div></div>; }

function PreviewView({ busy, dirty, language, document, report, summary, onValidate, onPublish, onArchive, selected }: { busy: boolean; dirty: boolean; language: Language; document: AgentProfileDocument; report: { valid: boolean; issues?: Array<{ code: string; field?: string; message: string }> } | null; summary: ReturnType<typeof profileCapabilitySummary>; onValidate: () => void; onPublish: () => void; onArchive: () => void; selected: AgentProfileRecord | null }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Profile preview", "配置预览")}</span><h3>{text(language, "Requested configuration", "请求配置")}</h3></div><div className="button-row"><button className="secondary-button" type="button" onClick={onValidate}><ShieldCheck size={15} /> {text(language, "Revalidate", "重新校验")}</button>{selected?.status === "draft" ? <button className="primary-button" disabled={busy || dirty || !selected.id} type="button" onClick={onPublish}>{text(language, "Publish", "发布")}</button> : null}{selected?.status === "published" && selected.id > 0 ? <button className="secondary-button danger" type="button" onClick={onArchive}><Archive size={15} /> {text(language, "Archive", "归档")}</button> : null}</div></div><ProfileSource language={language} profile={selected} />{dirty ? <p role="status">{text(language, "Save the draft before publishing", "请先保存草稿再发布")}</p> : null}<div className="preview-columns"><div><span className="eyebrow">{text(language, "Requested", "请求配置")}</span><pre>{JSON.stringify(document, null, 2)}</pre></div><div><span className="eyebrow">{text(language, "Requested policy", "请求策略")}</span><div className="effective-list"><Metric label={text(language, "Prompt mode", "Prompt 模式")} value={document.prompt.mode} /><Metric label={text(language, "Tools", "工具")} value={summary.tools} /><Metric label={text(language, "Context", "上下文")} value={summary.context} /><Metric label={text(language, "Safety", "安全")} value={`${document.safety.permission_mode || "ask"} · ${document.safety.sandbox || "required"}`} /></div></div></div>{report ? <div className={report.valid ? "validation-callout valid" : "validation-callout invalid"}><strong>{report.valid ? text(language, "Validation passed", "校验通过") : text(language, "Validation blocked", "校验未通过")}</strong>{report.issues?.map((issue) => <p key={`${issue.code}-${issue.field}`}>{issue.code}: {issue.message}</p>)}</div> : <div className="notice"><ShieldCheck size={15} /> {text(language, "Validate before publishing. Blocked overrides are shown here.", "发布前请先校验，受策略阻止的字段会显示在这里。")}</div>}</div>; }

function VersionsView({ language, busy, versions, onRollback }: { language: Language; busy: boolean; versions: AgentProfileRecord[]; onRollback: (version: number) => void }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Immutable history", "不可变历史")}</span><h3>{text(language, "Versions", "版本")}</h3></div></div><div className="version-list">{versions.map((version) => <div className="version-row" key={version.profile_version}><div><strong>v{version.profile_version} · {statusName(version.status, language)}</strong><small>{version.effective_hash || version.requested_hash || text(language, "no hash", "暂无 Hash")}</small></div><div className="button-row">{version.status !== "archived" ? <span className="trace-status-chip ok">{version.scope}</span> : null}<button className="secondary-button compact-action" type="button" disabled={busy || version.status === "archived"} title={version.status === "archived" ? text(language, "Archived versions cannot be rolled back; copy this profile to create a new profile.", "归档版本无法回滚，可复制为新 Profile。") : undefined} onClick={() => onRollback(version.profile_version)}><RotateCcw size={14} /> {text(language, "Rollback", "回滚")}</button></div></div>)}</div></div>; }

function AssignmentView({ language, surface, assignment, profiles, selected, onSurface, onSelect, onSave }: { language: Language; surface: string; assignment: AgentProfileAssignment | null; profiles: AgentProfileRecord[]; selected: AgentProfileRecord | null; onSurface: (value: string) => void; onSelect: (key: string) => void; onSave: () => void }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Surface routing", "入口路由")}</span><h3>{text(language, "Assignments", "分配")}</h3></div></div><div className="profile-form-grid"><label>{text(language, "Surface", "入口")}<select value={surface} onChange={(event) => onSurface(event.target.value)}><option value="web_chat">web_chat</option><option value="mobile_chat">mobile_chat</option><option value="tenant_agent">tenant_agent</option><option value="channel_dm">channel_dm</option><option value="channel_team">channel_team</option></select></label><label>{text(language, "Published profile", "已发布 Profile")}<select value={selected?.profile_key || ""} onChange={(event) => onSelect(event.target.value)}>{profiles.map((profile) => <option key={`${profile.profile_key}-${profile.profile_version}`} value={profile.profile_key}>{profileName(profile, language)} · v{profile.profile_version}</option>)}</select></label></div><div className="assignment-callout"><span>{text(language, "Current assignment", "当前分配")}</span><strong>{assignment?.profile_id ? `Profile #${assignment.profile_id}` : text(language, "No assignment", "尚未分配")}</strong><small>{text(language, "Assignment is scoped to the current tenant and user. CLI/TUI code remains isolated.", "分配仅作用于当前租户和用户，CLI/TUI code 流程保持隔离。")}</small></div><button className="primary-button" type="button" disabled={!selected || selected.id === 0} onClick={onSave}><Save size={15} /> {text(language, "Save assignment", "保存分配")}</button></div>; }

function BindingView({ language, accounts, accountId, binding, editable, onAccount, onSave, onArchive }: { language: Language; accounts: ChannelAccountRecord[]; accountId: string; binding: { account_id: number; provider: string; binding_key: string } | null; editable: boolean; onAccount: (value: string) => void; onSave: () => void; onArchive: () => void }) { return <div className="agent-detail-view"><div className="section-heading"><div><span className="eyebrow">{text(language, "Feishu channel", "飞书渠道")}</span><h3>{text(language, "Bot binding", "机器人绑定")}</h3></div></div><div className="binding-security-note"><Bot size={17} /><span>{text(language, "Credentials stay in channel_accounts. This screen only receives safe bot metadata.", "凭据保存在 channel_accounts，本页面只读取安全的机器人元数据。")}</span></div><div className="profile-form-grid"><label>{text(language, "Channel account", "渠道账号")}<select disabled={!editable} value={accountId} onChange={(event) => onAccount(event.target.value)}><option value="">{text(language, "Select a bot account", "选择机器人账号")}</option>{accounts.map((account) => <option key={account.id} value={account.id}>{account.account_key} · {account.provider}</option>)}</select></label><label>{text(language, "Current binding", "当前绑定")}<input readOnly value={binding ? `${binding.binding_key} · ${binding.provider}` : text(language, "Not bound", "未绑定")} /></label></div><div className="button-row"><button className="primary-button" disabled={!editable || !accountId} type="button" onClick={onSave}><Bot size={15} /> {text(language, "Bind bot", "绑定机器人")}</button><button className="secondary-button danger" disabled={!editable || !binding} type="button" onClick={onArchive}><Archive size={15} /> {text(language, "Archive binding", "归档绑定")}</button></div></div>; }

function Metric({ label, value }: { label: string; value: string | number }) { return <div className="profile-metric"><span>{label}</span><strong>{value}</strong></div>; }
