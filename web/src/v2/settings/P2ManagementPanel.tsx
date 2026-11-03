import { Brain, Eye, EyeOff, Power, RefreshCw, RotateCcw, Save, Sparkles } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getLocalSkill,
  getTenantSkill,
  listLocalSkills,
  listMemories,
  listTenantSkills,
  rollbackTenantSkill,
  saveMemory,
  saveSkill,
  saveTenantSkill
} from "../../lib/api";
import type { IdentityConfig, LocalSkillRecord, MemoryRecord, SkillRecord } from "../../lib/types";
import type { SaveMemoryRequest } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

type Props = { identity: IdentityConfig; section: "memory" | "skills" };
const managementSections = ["memory", "skills"] as const;

export function P2ManagementPanel(props: Props) {
  const { identity } = props;
  return <ManagementEditors key={JSON.stringify([identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId])} {...props} />;
}

function ManagementEditors({ identity, section }: Props) {
  const [visited, setVisited] = useState(() => new Set([section]));
  useEffect(() => { setVisited((previous) => previous.has(section) ? previous : new Set([...previous, section])); }, [section]);
  // Keep drafts and pending completions owned by their original section.
  return managementSections.map((value) => visited.has(value) || value === section
    ? <div key={value} hidden={value !== section}><ManagementEditor identity={identity} section={value} /></div>
    : null);
}

function tenantSkillKey(record: SkillRecord): string {
  return `${String(record.skill_key)}:${String(record.version ?? 0)}`;
}

function localSkillKey(record: LocalSkillRecord): string {
  return record.path || record.name;
}

function ManagementEditor({ identity, section }: Props) {
  const { language } = useI18n();
  const zh = language === "zh";
  const [memories, setMemories] = useState<MemoryRecord[]>([]);
  const [tenantSkills, setTenantSkills] = useState<SkillRecord[]>([]);
  const [localSkills, setLocalSkills] = useState<LocalSkillRecord[]>([]);
  const [localDetails, setLocalDetails] = useState<Record<string, LocalSkillRecord>>({});
  const [tenantDetails, setTenantDetails] = useState<Record<string, SkillRecord>>({});
  const [openDetailKey, setOpenDetailKey] = useState("");
  const [detailBusyKey, setDetailBusyKey] = useState("");
  const [detailError, setDetailError] = useState("");
  const [localError, setLocalError] = useState("");
  const [tenantError, setTenantError] = useState("");
  const [key, setKey] = useState(section === "memory" ? "desktop.preference" : "desktop-skill");
  const [name, setName] = useState("Desktop skill");
  const [content, setContent] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const active = useRef(true);
  const { apiBase, apiToken, mobileJwt, tenantKey, userId, deviceId, model, provider, role } = identity;
  const requestIdentity = useMemo(() => ({ apiBase, apiToken, mobileJwt, tenantKey, userId, deviceId, model, provider, role }), [
    apiBase, apiToken, mobileJwt, tenantKey, userId, deviceId, model, provider, role
  ]);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);

  const refresh = useCallback(async () => {
    setBusy(true);
    setStatus("");
    try {
      if (section === "memory") {
        const records = await listMemories(requestIdentity);
        if (active.current) setMemories(records);
      } else {
        setLocalError("");
        setTenantError("");
        setDetailError("");
        setOpenDetailKey("");
        setLocalDetails({});
        setTenantDetails({});
        await Promise.all([
          listLocalSkills(requestIdentity).then(
            (records) => { if (active.current) setLocalSkills(records); },
            (error) => { if (active.current) setLocalError(error instanceof Error ? error.message : String(error)); }
          ),
          listTenantSkills(requestIdentity).then(
            (records) => { if (active.current) setTenantSkills(records); },
            (error) => { if (active.current) setTenantError(error instanceof Error ? error.message : String(error)); }
          )
        ]);
      }
    } catch (error) {
      if (active.current) setStatus(error instanceof Error ? error.message : String(error));
    } finally {
      if (active.current) setBusy(false);
    }
  }, [requestIdentity, section]);
  useEffect(() => { void refresh(); }, [refresh]);

  async function create() {
    if (busy || !key.trim() || !content.trim()) return;
    setBusy(true);
    try {
      if (section === "memory") {
        const request: SaveMemoryRequest = { memory_key: key.trim(), content: content.trim(), category: "preference", source: "desktop" };
        await saveMemory(identity, request);
      } else {
        await saveSkill(identity, { skill_key: key.trim(), name: name.trim() || key.trim(), content_md: content.trim(), enabled: true });
      }
      if (!active.current) return;
      setContent("");
      setStatus(zh ? "已保存并重新读取。" : "Saved and reloaded.");
      await refresh();
    } catch (error) {
      if (active.current) setStatus(error instanceof Error ? error.message : String(error));
    } finally {
      if (active.current) setBusy(false);
    }
  }

  async function toggleLocalDetails(item: LocalSkillRecord) {
    const itemKey = localSkillKey(item);
    if (openDetailKey === itemKey) {
      setOpenDetailKey("");
      return;
    }
    setOpenDetailKey(itemKey);
    setDetailError("");
    if (localDetails[itemKey]) return;
    setDetailBusyKey(itemKey);
    try {
      const detail = await getLocalSkill(identity, item.name);
      if (active.current) setLocalDetails((previous) => ({ ...previous, [itemKey]: detail }));
    } catch (error) {
      if (active.current) setDetailError(error instanceof Error ? error.message : String(error));
    } finally {
      if (active.current) setDetailBusyKey("");
    }
  }

  async function toggleTenantDetails(item: SkillRecord) {
    const itemKey = tenantSkillKey(item);
    if (openDetailKey === itemKey) {
      setOpenDetailKey("");
      return;
    }
    setOpenDetailKey(itemKey);
    setDetailError("");
    if (tenantDetails[itemKey]) return;
    setDetailBusyKey(itemKey);
    try {
      const detail = await getTenantSkill(identity, String(item.skill_key), Number(item.version ?? 0));
      if (active.current) setTenantDetails((previous) => ({ ...previous, [itemKey]: detail }));
    } catch (error) {
      if (active.current) setDetailError(error instanceof Error ? error.message : String(error));
    } finally {
      if (active.current) setDetailBusyKey("");
    }
  }

  async function setTenantEnabled(item: SkillRecord) {
    if (busy) return;
    setBusy(true);
    try {
      await saveTenantSkill(identity, { ...item, enabled: !item.enabled });
      setStatus(zh ? `${item.name || item.skill_key} 已${item.enabled ? "停用" : "启用"}。` : `${item.name || item.skill_key} ${item.enabled ? "disabled" : "enabled"}.`);
      await refresh();
    } catch (error) {
      if (active.current) setStatus(error instanceof Error ? error.message : String(error));
    } finally {
      if (active.current) setBusy(false);
    }
  }

  async function rollback(item: SkillRecord) {
    const version = Number(item.version ?? 0);
    const skillKey = String(item.skill_key ?? "");
    if (busy || !skillKey || !version) return;
    const confirmed = window.confirm(zh ? `确认把 ${skillKey} 回滚到 v${version}？这会创建一个新的当前版本。` : `Roll back ${skillKey} to v${version}? This creates a new current version.`);
    if (!confirmed) return;
    setBusy(true);
    try {
      const result = await rollbackTenantSkill(identity, skillKey, version);
      setStatus(zh ? `已回滚到 v${version}，当前版本为 v${result.version}。` : `Rolled back to v${version}; current version is v${result.version}.`);
      await refresh();
    } catch (error) {
      if (active.current) setStatus(error instanceof Error ? error.message : String(error));
    } finally {
      if (active.current) setBusy(false);
    }
  }

  const latestTenantVersions = new Map<string, number>();
  for (const item of tenantSkills) {
    const skillKey = String(item.skill_key ?? "");
    const version = Number(item.version ?? 0);
    latestTenantVersions.set(skillKey, Math.max(latestTenantVersions.get(skillKey) ?? 0, version));
  }

  return <section className="p2-management-panel">
    <header className="p2-management-toolbar">
      <div>
        <h2>{section === "memory" ? <><Brain size={18} />{zh ? "持久记忆" : "Persistent memory"}</> : <><Sparkles size={18} />{zh ? "Skills 管理" : "Skills management"}</>}</h2>
        {section === "skills" ? <p>{zh ? "本机 Skills 仅查看；租户 Skills 可查看、启用/停用和回滚。" : "Local skills are view-only; tenant skills can be viewed, enabled, disabled, and rolled back."}</p> : null}
      </div>
      <button className="settings-icon-button" type="button" onClick={() => void refresh()} disabled={busy} aria-label={zh ? "刷新" : "Refresh"} title={zh ? "刷新" : "Refresh"}><RefreshCw size={15} /></button>
    </header>
    {status ? <p role="status" className="p2-management-status">{status}</p> : null}
    {section === "skills" ? <section className="p2-management-group" aria-label={zh ? "本机已安装 Skills" : "Locally installed skills"}>
        <header className="p2-management-group-heading">
          <div><h3>{zh ? "本机已发现 Skills" : "Local skills"}</h3><p className="p2-management-help">{zh ? "来自当前电脑、项目或插件。设置页不会删除外部文件；安装和卸载请使用 CLI 或插件管理。" : "Discovered on this computer, project, or plugins. Settings never deletes external files; use the CLI or plugin manager to install or uninstall."}</p></div>
          <span>{localSkills.length}</span>
        </header>
        {localError ? <p role="alert" className="settings-error">{localError}</p> : null}
        <div className="p2-management-list">
          {localSkills.map((item) => {
            const itemKey = localSkillKey(item);
            const detail = localDetails[itemKey];
            const detailBusy = detailBusyKey === itemKey;
            return <article className="p2-skill-card" key={itemKey}>
              <div className="p2-skill-card-heading"><div><strong>{item.name || item.local_name || item.path}</strong>{item.description ? <p>{item.description}</p> : null}</div><button className="settings-inline-button" type="button" onClick={() => void toggleLocalDetails(item)} disabled={detailBusy} aria-expanded={openDetailKey === itemKey}>{openDetailKey === itemKey ? <EyeOff size={14} /> : <Eye size={14} />}{openDetailKey === itemKey ? (zh ? "收起" : "Hide") : (zh ? "查看详情" : "View details")}</button></div>
              <p className="p2-skill-meta">{[item.source, item.plugin ? `plugin: ${item.plugin}` : "", item.version ? `v${item.version}` : ""].filter(Boolean).join(" · ") || (zh ? "本机发现" : "Local discovery")}</p>
              {item.legacy ? <p className="p2-skill-meta">{zh ? "兼容命令" : "Legacy command"}</p> : null}
              <code className="p2-skill-path">{item.path}</code>
              {openDetailKey === itemKey ? <div className="p2-skill-detail">{detailBusy ? <p>{zh ? "正在读取详情…" : "Loading details…"}</p> : detailError ? <p role="alert" className="settings-error">{detailError}</p> : detail ? <><dl className="p2-skill-detail-grid"><div><dt>{zh ? "来源" : "Source"}</dt><dd>{String(detail.source || (zh ? "本机" : "Local"))}</dd></div><div><dt>{zh ? "可被用户调用" : "User invocable"}</dt><dd>{detail.user_invocable === false ? (zh ? "否" : "No") : (zh ? "是" : "Yes")}</dd></div></dl><pre className="p2-skill-content">{detail.content || (zh ? "没有可显示的 SKILL.md 内容。" : "No SKILL.md content available.")}</pre></> : null}</div> : null}
            </article>;
          })}
          {!busy && !localError && localSkills.length === 0 ? <p>{zh ? "没有发现本机 Skills。" : "No local skills discovered."}</p> : null}
        </div>
    </section> : null}
    <section className="p2-management-group" aria-label={section === "skills" ? (zh ? "租户 Skills" : "Tenant skills") : undefined}>
      {section === "skills" ? <header className="p2-management-group-heading">
        <div><h3>{zh ? "租户 Skills" : "Tenant skills"}</h3><p className="p2-management-help">{zh ? "启用/停用只影响当前租户；回滚会保留历史记录并创建新的当前版本。" : "Enable/disable applies to this tenant; rollback keeps history and creates a new current version."}</p></div>
        <span>{tenantSkills.length}</span>
      </header> : null}
      {tenantError ? <p role="alert" className="settings-error">{tenantError}</p> : null}
      <div className="p2-management-create">
        <label>{section === "memory" ? (zh ? "记忆 Key" : "Memory key") : (zh ? "Skill Key" : "Skill key")}<input disabled={busy} value={key} onChange={(event) => setKey(event.target.value)} /></label>
        {section === "skills" ? <label>{zh ? "名称" : "Name"}<input disabled={busy} value={name} onChange={(event) => setName(event.target.value)} /></label> : null}
        <label className="full">{section === "memory" ? (zh ? "内容" : "Content") : "Markdown"}<textarea disabled={busy} rows={4} value={content} onChange={(event) => setContent(event.target.value)} /></label>
        <button className="primary" type="button" onClick={() => void create()} disabled={busy || !key.trim() || !content.trim()}><Save size={15} />{section === "skills" ? (zh ? "保存租户 Skill" : "Save tenant skill") : (zh ? "保存" : "Save")}</button>
      </div>
      <div className="p2-management-list">
        {section === "memory" ? memories.map((item) => <article key={String(item.memory_key)}><strong>{item.memory_key}</strong><p>{item.content}</p></article>) : tenantSkills.map((item) => {
          const itemKey = tenantSkillKey(item);
          const version = Number(item.version ?? 0);
          const isLatest = version === latestTenantVersions.get(String(item.skill_key ?? ""));
          const detail = tenantDetails[itemKey];
          const detailBusy = detailBusyKey === itemKey;
          return <article className="p2-skill-card" key={itemKey}>
            <div className="p2-skill-card-heading"><div><strong>{item.name || item.skill_key}</strong><p>{item.skill_key}</p></div><span className={`p2-skill-state ${item.enabled ? "is-enabled" : "is-disabled"}`}><Power size={13} />{item.enabled ? (zh ? "已启用" : "Enabled") : (zh ? "已停用" : "Disabled")}</span></div>
            <p className="p2-skill-meta">{isLatest ? (zh ? `当前版本 v${version}` : `Current v${version}`) : (zh ? `历史版本 v${version}` : `History v${version}`)}</p>
            <div className="p2-skill-actions"><button className="settings-inline-button" type="button" onClick={() => void toggleTenantDetails(item)} disabled={detailBusy} aria-expanded={openDetailKey === itemKey}>{openDetailKey === itemKey ? <EyeOff size={14} /> : <Eye size={14} />}{openDetailKey === itemKey ? (zh ? "收起" : "Hide") : (zh ? "查看详情" : "View details")}</button>{isLatest ? <button className="settings-inline-button" type="button" onClick={() => void setTenantEnabled(item)} disabled={busy}><Power size={14} />{item.enabled ? (zh ? "停用" : "Disable") : (zh ? "启用" : "Enable")}</button> : <button className="settings-inline-button" type="button" onClick={() => void rollback(item)} disabled={busy}><RotateCcw size={14} />{zh ? `回滚到 v${version}` : `Roll back to v${version}`}</button>}</div>
            {openDetailKey === itemKey ? <div className="p2-skill-detail">{detailBusy ? <p>{zh ? "正在读取详情…" : "Loading details…"}</p> : detailError ? <p role="alert" className="settings-error">{detailError}</p> : detail ? <><dl className="p2-skill-detail-grid"><div><dt>{zh ? "版本" : "Version"}</dt><dd>v{detail.version}</dd></div><div><dt>{zh ? "状态" : "Status"}</dt><dd>{detail.enabled ? (zh ? "已启用" : "Enabled") : (zh ? "已停用" : "Disabled")}</dd></div><div><dt>{zh ? "更新时间" : "Updated"}</dt><dd>{detail.updated_at || "-"}</dd></div></dl><pre className="p2-skill-content">{detail.content_md || (zh ? "没有可显示的 Markdown 内容。" : "No Markdown content available.")}</pre></> : null}</div> : null}
          </article>;
        })}
        {!busy && !tenantError && (section === "memory" ? memories : tenantSkills).length === 0 ? <p>{zh ? "暂无记录。" : "No records yet."}</p> : null}
      </div>
    </section>
  </section>;
}
