import { CheckCircle2, CircleAlert, CircleX, RefreshCcw } from "lucide-react";
import {
  buildCapabilityChecks,
  buildValidationChecks,
  validationScore,
  type ValidationSnapshot,
  type ValidationStatus
} from "../lib/validation";
import { useI18n } from "../lib/i18n";

type Props = {
  snapshot: ValidationSnapshot;
  onRefresh: () => void | Promise<void>;
};

export function ValidationPanel({ snapshot, onRefresh }: Props) {
  const { t } = useI18n();
  const checks = buildValidationChecks(snapshot);
  const capabilities = buildCapabilityChecks(snapshot);
  const score = validationScore(checks);

  return (
    <section className="panel validation-panel">
      <div className="panel-header">
        <div>
          <h2>{t("validation.title")}</h2>
          <p>
            {t("validation.summary", { passed: score.passed, total: score.total, warning: score.warning, failing: score.failing })}
          </p>
        </div>
        <button type="button" className="icon-button" onClick={() => void onRefresh()} title={t("validation.refresh")}>
          <RefreshCcw size={16} />
        </button>
      </div>
      <div className="validation-grid">
        {checks.map((check) => (
          <article key={check.key} className={`validation-item ${check.status}`}>
            <StatusIcon status={check.status} />
            <div>
              <strong>{t(`validation.${check.key}`) === `validation.${check.key}` ? check.label : t(`validation.${check.key}`)}</strong>
              <small>{localizedDetail(check.key, check.detail, t)}</small>
            </div>
          </article>
        ))}
      </div>
      <div className="capability-section">
        <h3>{t("validation.capability")}</h3>
        <div className="validation-grid compact-grid">
          {capabilities.map((check) => (
            <article key={check.key} className={`validation-item ${check.status}`}>
              <StatusIcon status={check.status} />
              <div>
                <strong>{t(`validation.${check.key}`) === `validation.${check.key}` ? check.label : t(`validation.${check.key}`)}</strong>
                <small>{localizedDetail(check.key, check.detail, t)}</small>
              </div>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}

function localizedDetail(key: string, detail: string, t: (key: string, params?: Record<string, string | number>) => string): string {
  if (key === "session") {
    const id = detail.match(/#(\d+)/)?.[1];
    return id ? t("validation.detail.selectedSession", { id }) : t("validation.detail.selectSession");
  }
  if (key === "messages") {
    return t("validation.detail.persistedMessages", { count: numberAt(detail, 0) });
  }
  if (key === "memory") {
    return t("validation.detail.records", { count: numberAt(detail, 0) });
  }
  if (key === "profile") {
    if (detail.includes("not saved")) {
      return t("validation.detail.notSaved");
    }
    return t("validation.detail.version", { version: detail.replace(/^version\s+/, "") });
  }
  if (key === "documents") {
    return t("validation.detail.documentRecords", { count: numberAt(detail, 0) });
  }
  if (key === "skills") {
    const values = detail.match(/\d+/g) || ["0", "0", "0"];
    return t("validation.detail.skills", { effective: values[0] || 0, tenant: values[1] || 0, overrides: values[2] || 0 });
  }
  if (key === "telemetry") {
    return t("validation.detail.events", { count: numberAt(detail, 0) });
  }
  if (key === "trace") {
    if (detail.includes("no trace")) {
      return t("validation.detail.noTrace");
    }
    const values = detail.match(/\d+/g) || ["0", "0"];
    return t("validation.detail.trace", { messages: values[0] || 0, tools: values[1] || 0 });
  }
  if (key === "tokens") {
    return detail.includes("not reported") ? t("validation.detail.notReported") : detail;
  }
  if (key === "prompt-cache") {
    return detail.includes("not reported") || detail.includes("no cache") ? t("validation.detail.notReported") : detail;
  }
  if (key === "mobile-stream") {
    return detail.includes("found") ? t("validation.detail.mobileEvent") : t("validation.detail.sendMobile");
  }
  if (key === "auto-compact") {
    return detail.includes("found") ? t("validation.detail.compactFound") : t("validation.detail.compactMissing");
  }
  if (key === "skills-progressive") {
    return detail.includes("no active") ? t("validation.detail.noSkillEvidence") : detail;
  }
  if (key === "long-memory") {
    return detail.includes("no memory") ? t("validation.detail.noMemory") : t("validation.detail.persistedMemories", { count: numberAt(detail, 0) });
  }
  if (key === "short-memory") {
    return detail.includes("no CLAUDE") ? t("validation.detail.noDocuments") : t("validation.detail.documentCount", { count: numberAt(detail, 0) });
  }
  return detail.includes("no profile") ? t("validation.detail.noProfile") : detail;
}

function numberAt(value: string, index: number): number {
  return Number(value.match(/\d+/g)?.[index] || 0);
}

function StatusIcon({ status }: { status: ValidationStatus }) {
  if (status === "pass") {
    return <CheckCircle2 size={18} />;
  }
  if (status === "warn") {
    return <CircleAlert size={18} />;
  }
  return <CircleX size={18} />;
}
