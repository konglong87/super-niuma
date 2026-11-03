import { AlertTriangle, CheckCircle2, CircleDashed } from "lucide-react";
import type { ProvisioningRecord } from "../../lib/types";
import type { Language } from "../../lib/i18n";

export function ProvisioningStatusPanel({ record, language }: { record: ProvisioningRecord | null; language: Language }) {
  const tr = (en: string, zh: string) => language === "zh" ? zh : en;
  if (!record) return <div className="worker-status-summary is-empty"><CircleDashed size={17} /><span>{tr("No status available", "暂无状态")}</span></div>;
  const state = record.observed_worker?.state || record.status;
  const icon = state === "running" || state === "preflight" ? <CheckCircle2 size={16} /> : state === "failed" || state === "degraded" ? <AlertTriangle size={16} /> : <CircleDashed size={16} />;
  return <div className={`worker-status-summary ${state}`}><span className="worker-status-icon">{icon}</span><span>{tr("Current state", "当前状态")}</span><strong>{state}</strong></div>;
}
