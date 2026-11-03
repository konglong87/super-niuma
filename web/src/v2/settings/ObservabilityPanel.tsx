import { Activity, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState, type JSX } from "react";
import { getStatus, listTelemetry, listTenantUsageDaily, listTenantUsageLedger } from "../../lib/api";
import type { IdentityConfig, ServerStatus, TelemetryRecord, TenantUsageDaily, TenantUsageLedger } from "../../lib/types";
import { useI18n } from "../../lib/i18n";

type Props = { identity: IdentityConfig };

type Snapshot = {
  status: ServerStatus | null;
  telemetry: TelemetryRecord[];
  daily: TenantUsageDaily[];
  ledger: TenantUsageLedger[];
};

const EMPTY: Snapshot = { status: null, telemetry: [], daily: [], ledger: [] };

export function ObservabilityPanel({ identity }: Props): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const [snapshot, setSnapshot] = useState<Snapshot>(EMPTY);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const refresh = useCallback(async (): Promise<void> => {
    setLoading(true);
    setError("");
    try {
      const [status, telemetry, daily, ledger] = await Promise.all([getStatus(identity), listTelemetry(identity), listTenantUsageDaily(identity), listTenantUsageLedger(identity)]);
      setSnapshot({ status, telemetry, daily, ledger });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : (zh ? "读取可观测性数据失败" : "Unable to read observability data"));
    } finally {
      setLoading(false);
    }
  }, [identity, zh]);

  useEffect(() => { void refresh(); }, [refresh]);

  const statusText = snapshot.status?.ok === false ? (zh ? "异常" : "Unhealthy") : snapshot.status ? (zh ? "正常" : "Healthy") : (zh ? "未知" : "Unknown");
  return <div className="observability-panel">
    <header className="observability-toolbar"><div><h2><Activity size={18} />{zh ? "服务观测" : "Service observability"}</h2><p>{zh ? "数据来自当前服务的健康、telemetry 和用量 API。" : "Data is read from the current service health, telemetry and usage APIs."}</p></div><button type="button" onClick={() => void refresh()} disabled={loading}><RefreshCw size={15} />{loading ? (zh ? "读取中…" : "Reading…") : (zh ? "刷新" : "Refresh")}</button></header>
    {error ? <p className="visual-settings-error" role="alert">{error}</p> : null}
    <dl className="observability-facts"><div><dt>{zh ? "服务状态" : "Service status"}</dt><dd data-status={snapshot.status?.ok === false ? "error" : "ok"}>{statusText}</dd></div><div><dt>{zh ? "Telemetry 记录" : "Telemetry records"}</dt><dd>{snapshot.telemetry.length}</dd></div><div><dt>{zh ? "日用量记录" : "Daily usage records"}</dt><dd>{snapshot.daily.length}</dd></div><div><dt>{zh ? "用量账本记录" : "Usage ledger records"}</dt><dd>{snapshot.ledger.length}</dd></div></dl>
    <section className="observability-section"><h3>{zh ? "最近 Telemetry" : "Recent telemetry"}</h3>{snapshot.telemetry.length ? <ul className="observability-list">{snapshot.telemetry.slice(0, 12).map((record, index) => <li key={`${record.id ?? "event"}-${index}`}><strong>{record.name || (zh ? "未命名事件" : "Unnamed event")}</strong><span>{record.status || record.category || ""}</span><time>{record.occurred_at || record.created_at || ""}</time></li>)}</ul> : <p className="observability-empty">{loading ? (zh ? "正在读取…" : "Reading…") : (zh ? "暂无 Telemetry 记录。" : "No telemetry records.")}</p>}</section>
    <section className="observability-section"><h3>{zh ? "最近日用量" : "Recent daily usage"}</h3>{snapshot.daily.length ? <div className="settings-table-wrap"><table><thead><tr><th>{zh ? "日期" : "Date"}</th><th>{zh ? "模型" : "Model"}</th><th>{zh ? "请求" : "Requests"}</th><th>{zh ? "Tokens" : "Tokens"}</th></tr></thead><tbody>{snapshot.daily.slice(0, 12).map((record, index) => <tr key={`${record.id ?? "day"}-${index}`}><td>{record.usage_date || "—"}</td><td>{record.model || "—"}</td><td>{record.request_count ?? "—"}</td><td>{record.total_tokens ?? "—"}</td></tr>)}</tbody></table></div> : <p className="observability-empty">{loading ? (zh ? "正在读取…" : "Reading…") : (zh ? "暂无日用量记录。" : "No daily usage records.")}</p>}</section>
  </div>;
}
