import { RefreshCw } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type JSX } from "react";
import { apiRequest } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { SessionRef } from "../routes";
import { useSessionDetail } from "../api/sessionControlQueries";
import { useSessionRuntimeDetails } from "../api/useSessionRuntime";
import { SettingsSelect } from "./SettingsSelect";

type EffectiveSettings = {
  workspace: string;
  global_path: string;
  file_resolved: { doc: Record<string, unknown>; sources: string[]; route_sources: Record<string, string>; includes_process_environment: boolean };
  process_snapshot: { available: boolean; kind: string; active_run_config_known: boolean; doc: Record<string, unknown> };
  activation: { requires_restart: boolean; existing_runs: string; new_runs: string };
};

export function EffectiveSettingsPanel({ identity, selectedRef }: { identity: IdentityConfig; selectedRef: SessionRef | null }): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const [scope, setScope] = useState("files");
  const [query, setQuery] = useState("");
  const queryClient = useQueryClient();
  async function refresh(): Promise<void> {
    if (scope === "session") {
      await queryClient.invalidateQueries({ predicate: (entry) => entry.queryKey.includes(selectedRef) && (entry.queryKey[0] === "session-control" || entry.queryKey[0] === "webui2-runtime-details") });
    } else await inspection.refetch();
  }
  const inspection = useQuery({ queryKey: ["webui2-settings-effective", identity.apiBase, identity.tenantKey, identity.userId], queryFn: ({ signal }) => apiRequest<EffectiveSettings>(identity, "/runtime/settings/effective", { signal }), retry: false, staleTime: 0 });
  const data = inspection.data;
  return <>
    <div className="settings-effective-controls"><label htmlFor="settings-effective-scope">{zh ? "查看范围" : "Scope"} <SettingsSelect id="settings-effective-scope" ariaLabel={zh ? "配置范围" : "Configuration scope"} value={scope} onChange={setScope} options={[{ value: "files", label: zh ? "文件解析结果" : "Resolved files" }, { value: "process", label: zh ? "服务启动快照" : "Server startup snapshot" }, ...(selectedRef ? [{ value: "session", label: zh ? "当前会话 / Run" : "Current session / Run" }] : [])]} /></label><input aria-label={zh ? "搜索配置项" : "Search configuration"} placeholder={zh ? "搜索配置项" : "Search configuration"} value={query} onChange={(event) => setQuery(event.target.value)} /><button disabled={inspection.isFetching} onClick={() => void refresh()} title={zh ? "刷新配置" : "Refresh configuration"} type="button"><RefreshCw size={15} />{zh ? "刷新" : "Refresh"}</button></div>
    {scope === "session" && selectedRef ? <SessionConfiguration identity={identity} selectedRef={selectedRef} query={query} /> : <>
      {inspection.isPending ? <p role="status">{zh ? "正在读取配置…" : "Loading configuration…"}</p> : null}
      {inspection.error ? <p role="alert" className="settings-error">{inspection.error.message}</p> : null}
      {data ? <>
        <div className="settings-notice">{scope === "files" ? (zh ? "以下为磁盘配置按加载顺序合并的结果，不包含进程环境变量或会话覆盖。" : "Files merged in loading order, excluding process environment and session overrides.") : data.process_snapshot.kind === "startup" ? (zh ? "以下为服务启动时记录的默认值，不代表正在执行的 Run。保存配置后需重启刷新此快照。" : "Defaults captured at server startup, not active Run configuration. Restart updates this snapshot.") : (zh ? "此服务没有启动快照。以下仅为状态回调结果，不代表活跃 Run。" : "No startup snapshot is available. These are status callback values, not active Run configuration.")}</div>
        <ConfigurationTable doc={scope === "files" ? data.file_resolved.doc : data.process_snapshot.doc} sources={scope === "files" ? data.file_resolved.route_sources : {}} defaultSource={scope === "files" ? (zh ? "来源未逐字段记录" : "Field source not recorded") : data.process_snapshot.kind === "startup" ? (zh ? "服务启动快照" : "Server startup") : (zh ? "状态回调" : "Status callback")} query={query} />
        {scope === "files" ? <section className="settings-section"><h2>{zh ? "配置来源 · 加载顺序" : "Sources in loading order"}</h2><ul className="settings-effective-sources">{data.file_resolved.sources.map((source, index) => {
          // biome-ignore lint/suspicious/noArrayIndexKey: Loading position identifies repeated files in this read-only ordered snapshot.
          return <li key={`${index}:${source}`}><code>{source}</code></li>;
        })}</ul><p className="settings-notice">{zh ? "工作区" : "Workspace"}: {data.workspace}<br />{zh ? "全局文件" : "Global file"}: {data.global_path}</p></section> : null}
      </> : null}
    </>}
  </>;
}

function SessionConfiguration({ identity, selectedRef, query }: { identity: IdentityConfig; selectedRef: SessionRef; query: string }): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const detail = useSessionDetail(identity, selectedRef);
  const runtime = useSessionRuntimeDetails(identity, detail.data);
  const latest = runtime.data?.tasks.find((task) => task.id === detail.data?.activeRunID) ?? runtime.data?.latest_task;
  let metadata: Record<string, unknown> = {};
  try { const parsed: unknown = JSON.parse(latest?.metadata_json || "{}"); if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) metadata = parsed as Record<string, unknown>; } catch { /* Legacy runs may have no readable metadata. */ }
  const session = detail.data;
  const doc = latest ? { task_id: latest.id, model: latest.model, provider: metadata.provider, permission_mode: metadata.permission_mode, prompt_mode: metadata.prompt_mode, effort: metadata.effort, profile_key: metadata.profile_key, profile_version: metadata.profile_version, cwd: metadata.cwd } : {};
  return <>
    {detail.isPending || runtime.isFetching ? <p role="status">{zh ? "正在读取运行记录…" : "Loading run records…"}</p> : null}
    {detail.error || runtime.error ? <p role="alert" className="settings-error">{detail.error?.message || runtime.error?.message}</p> : null}
    {session ? <><div className="settings-notice"><span>{session.title}</span><code>{selectedRef}</code></div><section className="settings-section"><h2>{zh ? "会话配置快照" : "Session configuration snapshot"}</h2><ConfigurationTable doc={{ provider: session.provider, model: session.model, permission_mode: session.permissionMode, prompt_mode: session.promptMode, effort: session.effort, cwd: session.cwd }} sources={{}} defaultSource={zh ? "会话视图（包含活动 Run 覆盖）" : "Session view, including active Run overrides"} query={query} /></section></> : null}
    <section className="settings-section"><h2>{zh ? "最近 Run 配置" : "Latest Run configuration"}</h2>{latest ? <ConfigurationTable doc={doc} sources={{}} defaultSource={zh ? `Task ${latest.id} 持久化快照` : `Task ${latest.id} persisted snapshot`} query={query} /> : <p>{zh ? "暂无可读取的 Run 配置。" : "No Run configuration is available."}</p>}<p className="settings-notice">{zh ? "此处展示已记录的请求路由。实际发生的模型降级需在 Trace 中核对，未记录的值不会从全局配置补填。" : "Recorded requested routes are shown here. Check Trace for actual fallback. Missing values are not filled from global defaults."}</p></section>
  </>;
}

export function flattenConfiguration(doc: Record<string, unknown>, prefix = ""): Array<[string, string]> {
  return Object.entries(doc).flatMap(([key, value]): Array<[string, string]> => {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value === undefined || value === null || value === "") return [];
    if (value && typeof value === "object" && !Array.isArray(value)) return flattenConfiguration(value as Record<string, unknown>, path);
    return [[path, typeof value === "string" ? value : JSON.stringify(value)]];
  });
}

function ConfigurationTable({ doc, sources, defaultSource, query }: { doc: Record<string, unknown>; sources: Record<string, string>; defaultSource: string; query: string }): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const rows = flattenConfiguration(doc).filter(([key, value]) => `${key} ${value}`.toLowerCase().includes(query.toLowerCase()));
  return <div className="settings-table-wrap"><table><thead><tr><th>{zh ? "配置项" : "Field"}</th><th>{zh ? "值" : "Value"}</th><th>{zh ? "来源" : "Source"}</th></tr></thead><tbody>{rows.map(([key, value]) => <tr key={key}><td><code>{key}</code></td><td><section className="settings-config-value" tabIndex={0} aria-label={key}>{value}</section></td><td>{sources[key] || defaultSource}</td></tr>)}</tbody></table>{!rows.length ? <p>{zh ? "没有匹配的配置项。" : "No matching configuration fields."}</p> : null}</div>;
}
