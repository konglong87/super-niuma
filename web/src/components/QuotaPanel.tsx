import { Gauge, RefreshCcw, Save } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  getTenantQuotaConfig,
  listTenantQuotaEvents,
  listTenantUsageDaily,
  listTenantUsageLedger,
  updateTenantQuotaConfig
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import type { IdentityConfig, TenantQuotaConfig, TenantQuotaEvent, TenantUsageDaily, TenantUsageLedger } from "../lib/types";

type Props = {
  identity: IdentityConfig;
  refreshTick: number;
  onStatus: (message: string) => void;
};

type QuotaDraft = {
  quotaEnabled: boolean;
  qpsLimit: string;
  dailyTokenLimit: string;
  dailyMessageLimit: string;
  maxConcurrentRequests: string;
  timezone: string;
  reserveOutputTokens: string;
  status: string;
};

const defaultDraft: QuotaDraft = {
  quotaEnabled: false,
  qpsLimit: "",
  dailyTokenLimit: "",
  dailyMessageLimit: "",
  maxConcurrentRequests: "",
  timezone: "UTC",
  reserveOutputTokens: "4096",
  status: "active"
};

export function QuotaPanel({ identity, refreshTick, onStatus }: Props) {
  const { t } = useI18n();
  const [config, setConfig] = useState<TenantQuotaConfig | null>(null);
  const [draft, setDraft] = useState<QuotaDraft>(defaultDraft);
  const [daily, setDaily] = useState<TenantUsageDaily[]>([]);
  const [ledger, setLedger] = useState<TenantUsageLedger[]>([]);
  const [events, setEvents] = useState<TenantQuotaEvent[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const canEdit = isTenantQuotaEditor(identity.role);

  async function refreshQuota() {
    setLoading(true);
    try {
      const [nextConfig, nextDaily, nextLedger, nextEvents] = await Promise.all([
        getTenantQuotaConfig(identity),
        listTenantUsageDaily(identity, 30),
        listTenantUsageLedger(identity, 50),
        listTenantQuotaEvents(identity, 50)
      ]);
      setConfig(nextConfig);
      setDraft(draftFromConfig(nextConfig));
      setDaily(nextDaily);
      setLedger(nextLedger);
      setEvents(nextEvents);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refreshQuota();
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, refreshTick]);

  async function saveQuota() {
    if (!canEdit) {
      onStatus(t("quota.readOnly"));
      return;
    }
    setSaving(true);
    try {
      const nextConfig = await updateTenantQuotaConfig(identity, configFromDraft(draft));
      setConfig(nextConfig);
      setDraft(draftFromConfig(nextConfig));
      onStatus(t("quota.saved"));
      await refreshQuota();
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  const totals = useMemo(() => summarizeDaily(daily), [daily]);
  const rejected = events.filter((event) => event.event_type?.includes("rejected")).length;

  return (
    <section className="panel inspector-panel quota-panel">
      <div className="panel-header">
        <div className="title-row">
          <Gauge size={17} />
          <div>
            <h2>{t("tab.quota")}</h2>
            <p>{config?.quota_enabled ? t("quota.enabled") : t("quota.disabled")}</p>
          </div>
        </div>
        <div className="panel-header-actions">
          <button className="icon-button" onClick={() => void refreshQuota()} disabled={loading} title={t("quota.refresh")} type="button">
            <RefreshCcw size={16} />
          </button>
          {canEdit ? (
            <button className="secondary-button compact-action" onClick={() => void saveQuota()} disabled={saving} type="button">
              <Save size={15} />
              {saving ? t("quota.saving") : t("quota.save")}
            </button>
          ) : null}
        </div>
      </div>

      <div className="quota-summary">
        <QuotaMetric label={t("quota.totalTokens")} value={formatNumber(totals.totalTokens)} />
        <QuotaMetric label={t("quota.requests")} value={formatNumber(totals.requests)} />
        <QuotaMetric label={t("quota.rejected")} value={formatNumber(rejected)} />
        <QuotaMetric label={t("quota.dailyLimit")} value={limitLabel(config?.daily_token_limit, t("quota.unlimited"))} />
      </div>

      <div className="quota-layout">
        <section className="quota-config-form compact-form">
          {!canEdit ? <div className="quota-readonly-note">{t("quota.readOnly")}</div> : null}
          <label className="quota-toggle-row">
            <input
              type="checkbox"
              checked={draft.quotaEnabled}
              disabled={!canEdit}
              onChange={(event) => setDraft((current) => ({ ...current, quotaEnabled: event.target.checked }))}
            />
            <span>{t("quota.enableTenantQuota")}</span>
          </label>
          <label>
            {t("quota.qpsLimit")}
            <input inputMode="numeric" value={draft.qpsLimit} disabled={!canEdit} onChange={(event) => setDraft((current) => ({ ...current, qpsLimit: event.target.value }))} placeholder={t("quota.unlimited")} />
          </label>
          <label>
            {t("quota.dailyTokenLimit")}
            <input inputMode="numeric" value={draft.dailyTokenLimit} disabled={!canEdit} onChange={(event) => setDraft((current) => ({ ...current, dailyTokenLimit: event.target.value }))} placeholder={t("quota.unlimited")} />
          </label>
          <label>
            {t("quota.dailyMessageLimit")}
            <input inputMode="numeric" value={draft.dailyMessageLimit} disabled={!canEdit} onChange={(event) => setDraft((current) => ({ ...current, dailyMessageLimit: event.target.value }))} placeholder={t("quota.unlimited")} />
          </label>
          <label>
            {t("quota.concurrentLimit")}
            <input inputMode="numeric" value={draft.maxConcurrentRequests} disabled={!canEdit} onChange={(event) => setDraft((current) => ({ ...current, maxConcurrentRequests: event.target.value }))} placeholder={t("quota.unlimited")} />
          </label>
          <label>
            {t("quota.reserveOutputTokens")}
            <input inputMode="numeric" value={draft.reserveOutputTokens} disabled={!canEdit} onChange={(event) => setDraft((current) => ({ ...current, reserveOutputTokens: event.target.value }))} />
          </label>
          <label>
            {t("quota.timezone")}
            <input value={draft.timezone} disabled={!canEdit} onChange={(event) => setDraft((current) => ({ ...current, timezone: event.target.value }))} />
          </label>
        </section>

        <section className="quota-table-stack">
          <QuotaTable
            title={t("quota.dailyUsage")}
            empty={t("quota.noUsage")}
            rows={daily.slice(0, 8).map((item) => ({
              key: `${item.usage_date}-${item.source}-${item.model}`,
              cells: [item.usage_date || "-", item.source || "-", item.model || "-", formatNumber(item.total_tokens), formatNumber(item.rejected_count)]
            }))}
            headers={[t("quota.date"), t("quota.source"), t("quota.model"), t("quota.tokens"), t("quota.rejected")]}
          />
          <QuotaTable
            title={t("quota.ledger")}
            empty={t("quota.noLedger")}
            rows={ledger.slice(0, 8).map((item) => ({
              key: item.request_id || String(item.id),
              cells: [shortValue(item.request_id), item.status || "-", item.source || "-", formatNumber(item.total_tokens), relativeTime(item.started_at)]
            }))}
            headers={[t("quota.request"), t("quota.status"), t("quota.source"), t("quota.tokens"), t("quota.started")]}
          />
          <QuotaTable
            title={t("quota.events")}
            empty={t("quota.noEvents")}
            rows={events.slice(0, 8).map((item) => ({
              key: String(item.id || item.request_id || item.created_at),
              cells: [item.event_type || "-", item.limit_type || "-", item.source || "-", item.model || "-", relativeTime(item.created_at)]
            }))}
            headers={[t("quota.event"), t("quota.limitType"), t("quota.source"), t("quota.model"), t("quota.created")]}
          />
        </section>
      </div>
    </section>
  );
}

function draftFromConfig(config: TenantQuotaConfig): QuotaDraft {
  return {
    quotaEnabled: Boolean(config.quota_enabled),
    qpsLimit: numberDraft(config.qps_limit),
    dailyTokenLimit: numberDraft(config.daily_token_limit),
    dailyMessageLimit: numberDraft(config.daily_message_limit),
    maxConcurrentRequests: numberDraft(config.max_concurrent_requests),
    timezone: config.timezone || "UTC",
    reserveOutputTokens: String(config.reserve_output_tokens || 4096),
    status: config.status || "active"
  };
}

function configFromDraft(draft: QuotaDraft): TenantQuotaConfig {
  return {
    quota_enabled: draft.quotaEnabled,
    qps_limit: nullableNumber(draft.qpsLimit),
    daily_token_limit: nullableNumber(draft.dailyTokenLimit),
    daily_message_limit: nullableNumber(draft.dailyMessageLimit),
    max_concurrent_requests: nullableNumber(draft.maxConcurrentRequests),
    timezone: draft.timezone.trim() || "UTC",
    reserve_output_tokens: nullableNumber(draft.reserveOutputTokens) || 4096,
    status: draft.status || "active"
  };
}

function isTenantQuotaEditor(role?: string): boolean {
  const normalized = (role || "").trim().toLowerCase();
  return normalized === "owner" || normalized === "admin";
}

function nullableNumber(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === "") {
    return null;
  }
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : null;
}

function numberDraft(value?: number | null): string {
  return value ? String(value) : "";
}

function summarizeDaily(items: TenantUsageDaily[]) {
  return items.reduce(
    (acc, item) => ({
      totalTokens: acc.totalTokens + Number(item.total_tokens || 0),
      requests: acc.requests + Number(item.request_count || 0)
    }),
    { totalTokens: 0, requests: 0 }
  );
}

function QuotaMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function QuotaTable({ title, headers, rows, empty }: { title: string; headers: string[]; rows: Array<{ key: string; cells: string[] }>; empty: string }) {
  return (
    <section className="quota-table-card">
      <strong>{title}</strong>
      {rows.length === 0 ? (
        <div className="empty-state compact">{empty}</div>
      ) : (
        // biome-ignore lint/a11y/useSemanticElements: keep <div> — .quota-table/.quota-table-row are CSS grid layouts (fixed column templates); converting to <table>/<tr> would break the grid column sizing
        <div className="quota-table" role="table" aria-label={title}>
          {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .quota-table-row is a CSS grid layout; converting to <tr> would break the grid column sizing */}
          {/* biome-ignore lint/a11y/useFocusableInteractive: header row is static (no interaction); a real <table> is required for a natively-focusable <tr>, which the CSS grid layout above rules out */}
          <div className="quota-table-row quota-table-head" role="row">
            {headers.map((header) => <span key={header}>{header}</span>)}
          </div>
          {rows.map((row) => (
            // biome-ignore lint/a11y/useSemanticElements: keep <div> — .quota-table-row is a CSS grid layout; converting to <tr> would break the grid column sizing
            // biome-ignore lint/a11y/useFocusableInteractive: row is static (no interaction); a real <table> is required for a natively-focusable <tr>, which the CSS grid layout above rules out
            <div className="quota-table-row" role="row" key={row.key}>
              {row.cells.map((cell, index) => <span key={`${row.key}-${headers[index]}`} title={cell}>{cell}</span>)}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function formatNumber(value?: number): string {
  return Number(value || 0).toLocaleString();
}

function limitLabel(value: number | null | undefined, unlimited: string): string {
  return value ? formatNumber(value) : unlimited;
}

function shortValue(value?: string): string {
  if (!value) {
    return "-";
  }
  return value.length > 14 ? `${value.slice(0, 6)}...${value.slice(-5)}` : value;
}

function relativeTime(value?: string): string {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}
