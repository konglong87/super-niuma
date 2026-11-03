import { CircleCheck, CircleDashed, CircleX } from "lucide-react";
import { useState, type ComponentType, type JSX, type SVGProps } from "react";
import { useI18n } from "../../lib/i18n";
import type { OperationCard as OperationCardModel, OperationKind, SessionControlErrorCode } from "../types";

type OperationCardProps = {
  operation: OperationCardModel;
};

type OperationStatus = OperationCardModel["status"];
type StatusPresentation = { icon: ComponentType<SVGProps<SVGSVGElement>>; labelKey: string };

const operationKindKeys: Record<OperationKind, string> = {
	archive: "webui2.operation.kind.archive",
	attach: "webui2.operation.kind.attach",
	compact: "webui2.operation.kind.compact",
	create: "webui2.operation.kind.create",
  profile_draft: "webui2.operation.kind.profile_draft",
  send: "webui2.operation.kind.send",
  stop: "webui2.operation.kind.stop"
};

const operationStatuses: Record<OperationStatus, StatusPresentation> = {
  completed: { icon: CircleCheck, labelKey: "webui2.operation.status.completed" },
  failed: { icon: CircleX, labelKey: "webui2.operation.status.failed" },
  pending: { icon: CircleDashed, labelKey: "webui2.operation.status.pending" }
};

const operationErrorKeys: Record<SessionControlErrorCode, string> = {
  forbidden: "webui2.error.forbidden",
  idempotency_conflict: "webui2.error.idempotency_conflict",
  invalid_request: "webui2.error.invalid_request",
  invalid_state: "webui2.error.invalid_state",
  local_read_only: "webui2.error.local_read_only",
  budget_exceeded: "webui2.error.budget_exceeded",
  provider_route_invalid: "webui2.error.provider_route_invalid",
  service_unavailable: "webui2.error.service_unavailable",
  workspace_unavailable: "webui2.error.workspace_unavailable",
  network_unavailable: "webui2.error.network_unavailable",
  stream_disconnected: "webui2.error.stream_disconnected",
  not_found: "webui2.error.not_found"
};

export function OperationCard({ operation }: OperationCardProps): JSX.Element {
  const { t } = useI18n();
  const [detailOpen, setDetailOpen] = useState(false);
  const normalizedKind = safeLookup(operationKindKeys, operation.kind);
  const kindKey = normalizedKind ? operationKindKeys[normalizedKind] : "webui2.operation.kind.unknown";
  const normalizedStatus = safeLookup(operationStatuses, operation.status);
  const statusPresentation = normalizedStatus ? operationStatuses[normalizedStatus] : { icon: CircleDashed, labelKey: "webui2.operation.status.unknown" };
  const errorKey = normalizedStatus === "failed" ? safeLookup(operationErrorKeys, operation.detail) : null;
  const localizedKind = t(kindKey);
  const localizedStatus = t(statusPresentation.labelKey);
  const result = errorKey ? t(operationErrorKeys[errorKey]) : localizedStatus;
  const replay = operation.replayed === true ? t("webui2.operation.replayed") : operation.replayed === false ? t("webui2.operation.notReplayed") : t("webui2.operation.replayUnknown");
  const timestamp = formatTimestamp(operation.createdAt, t("webui2.operation.timestampUnknown"));
  const StatusIcon = statusPresentation.icon;

  return <section aria-label={t("webui2.operation.record")} className="webui2-operation-card" data-operation-status={normalizedStatus ?? "unknown"} role="status">
    <div className="webui2-operation-card-summary">
      <StatusIcon aria-hidden="true" size={16} />
      <div><strong>{localizedKind}</strong><span>{result}</span></div>
      <time dateTime={timestamp.dateTime}>{timestamp.label}</time>
    </div>
    <dl className="webui2-operation-card-meta">
      <div><dt>{t("webui2.operation.result")}</dt><dd>{result}</dd></div>
      <div><dt>{t("webui2.operation.readback")}</dt><dd>{localizedStatus}</dd></div>
      <div><dt>{t("webui2.operation.replay")}</dt><dd>{replay}</dd></div>
    </dl>
    <details className="webui2-folded-record" onToggle={(event) => setDetailOpen(event.currentTarget.open)}>
      <summary>{t("webui2.operation.detail")}</summary>
      {detailOpen ? <dl className="webui2-operation-card-detail">
        <div><dt>{t("webui2.operation.kind")}</dt><dd>{localizedKind}</dd></div>
        <div><dt>{t("webui2.operation.status")}</dt><dd>{localizedStatus}</dd></div>
        <div><dt>{t("webui2.operation.timestamp")}</dt><dd><time dateTime={timestamp.dateTime}>{timestamp.label}</time></dd></div>
        {errorKey ? <div><dt>{t("webui2.operation.error")}</dt><dd>{t(operationErrorKeys[errorKey])}</dd></div> : null}
      </dl> : null}
    </details>
  </section>;
}

function safeLookup<T extends string, Value>(values: Record<T, Value>, candidate: unknown): T | null {
  return typeof candidate === "string" && Object.hasOwn(values, candidate) ? candidate as T : null;
}

function formatTimestamp(value: unknown, fallback: string): { dateTime?: string; label: string } {
  if (typeof value !== "string") return { label: fallback };
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.getTime()) ? { label: fallback } : { dateTime: timestamp.toISOString(), label: timestamp.toLocaleString() };
}
