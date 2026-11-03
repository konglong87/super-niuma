import { Activity, Bot, CheckCircle2, Clock3, RadioTower, Zap } from "lucide-react";
import type { ProvisioningRecord, ProvisioningWorkerStatus } from "../../lib/types";
import type { Language } from "../../lib/i18n";

export function ProvisioningOverview({ records, workers, language }: { records: ProvisioningRecord[]; workers: ProvisioningWorkerStatus[]; language: Language }) {
  const tr = (en: string, zh: string) => language === "zh" ? zh : en;
  const running = workers.filter((worker) => worker.state === "running").length;
  const healthy = workers.filter((worker) => worker.state === "running" && (worker.pid || 0) > 0).length;
  const passed = records.reduce((sum, record) => sum + (record.checks?.filter((check) => check.status === "passed").length ?? 0), 0);
  const failed = records.filter((record) => record.status === "failed").length;
  const metrics = [{ label: tr("Active workers", "运行中 Worker"), value: running, hint: tr("live screen inventory", "实时 screen 进程"), icon: <RadioTower size={17} />, tone: "blue" }, { label: tr("Healthy readback", "健康读回"), value: healthy, hint: tr("PID + process ready", "PID 与进程就绪"), icon: <CheckCircle2 size={17} />, tone: "green" }, { label: tr("Checks passed", "校验通过"), value: passed, hint: tr("provider and Feishu", "Provider 与飞书"), icon: <Zap size={17} />, tone: "amber" }, { label: tr("Needs attention", "需要处理"), value: failed, hint: tr("retry available", "可重试"), icon: <Activity size={17} />, tone: "red" }];
  return <section className="provisioning-overview"><div className="provisioning-overview-head"><div><span className="eyebrow">{tr("Operations overview", "运维概览")}</span><h2>{tr("Agent fleet, at a glance", "智能体运行概览")}</h2></div><span className="provisioning-live"><span /> {tr("Live readback", "实时读回")}</span></div><div className="provisioning-metrics">{metrics.map((metric) => <div className={`provisioning-metric ${metric.tone}`} key={metric.label}><span className="provisioning-metric-icon">{metric.icon}</span><span>{metric.label}</span><strong>{metric.value}</strong><small>{metric.hint}</small></div>)}</div><div className="provisioning-activity"><div><Bot size={16} /><span>{tr("Managed profiles", "向导管理 Profile")}</span><strong>{records.length}</strong></div><div><RadioTower size={16} /><span>{tr("Worker inventory", "Worker 实时发现")}</span><strong>{workers.length}</strong></div><div><Clock3 size={16} /><span>{tr("Supervisor", "进程托管")}</span><strong>{records[0]?.supervisor || "screen"}</strong></div></div></section>;
}
