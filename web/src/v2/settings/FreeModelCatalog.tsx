import { useCallback, useEffect, useRef, useState } from "react";
import { ExternalLink, ShieldAlert, TriangleAlert } from "lucide-react";
import { ApiError, apiRequest, getFreeModels, type FreeModelProvider } from "../../lib/api";
import { useI18n, type Language } from "../../lib/i18n";
import { openExternalURL } from "../../lib/openExternal";
import type { SettingsDoc } from "../../lib/types";
import { SETTINGS_ENDPOINT, settingsValue, type GlobalSettingsDraft, type SettingsConnection } from "./globalSettingsDraft";
import { useFreeModelText } from "./freeModelCopy";

const DOMESTIC_SLUGS = new Set(["zhipu-glm", "siliconflow", "modelscope"]);
const PROVIDER_TYPE = "openai-compatible";
const GOOGLE_SLUG = "google-ai-studio";

type Verification = { ok: boolean; reason?: string; toolSupport?: string; message?: string };
type PendingSave = { slug: string; role: "primary" | "fallback" };

const trimSlash = (url: unknown): string => typeof url === "string" ? url.trim().replace(/\/+$/, "") : "";

/** Chinese UI: domestic providers first; English UI: overseas first. Stable within each group. */
export function sortFreeProviders(providers: FreeModelProvider[], language: Language): FreeModelProvider[] {
  const rank = (p: FreeModelProvider): number => DOMESTIC_SLUGS.has(p.slug ?? "") === (language === "zh") ? 0 : 1;
  return providers.map((p, index) => ({ p, index })).sort((a, b) => rank(a.p) - rank(b.p) || a.index - b.index).map(({ p }) => p);
}

/** Why a provider is flagged unverified: one sentence per cause (keys of freeModelCopy). */
export function unverifiedReasons(p: FreeModelProvider): string[] {
  const reasons: string[] = [];
  if (p.status !== "ok") reasons.push("注册状态待确认（目录状态为 pending）。");
  if (p.openai_compatible !== "supported") reasons.push("OpenAI 兼容接口未核实。");
  if (!p.recommended_model) reasons.push("没有可用的推荐模型。");
  else if (p.recommended_model.tool_calling !== "supported") reasons.push("推荐模型的工具调用支持未核实。");
  return reasons;
}

/** Insert or replace the provider; the same slug never appears twice on the chain. */
export function upsertFallbackProvider(existing: unknown, entry: Record<string, unknown>): unknown[] {
  const list = Array.isArray(existing) ? [...existing] : [];
  const at = list.findIndex((item) => settingsValue(item, ["name"]) === entry.name);
  if (at >= 0) list[at] = { ...(list[at] as Record<string, unknown>), ...entry };
  else list.push(entry);
  return list;
}

const REASON_MESSAGE: Record<string, string> = {
  key_invalid: "Key 无效或没有权限，请检查是否复制完整。",
  quota_or_rate_limit: "额度已用完或触发限流，请稍后再试或换一家。",
  model_not_found: "找不到推荐模型，可能已下线，请在高级设置中改用其他模型。",
  network: "网络不通，请检查网络或代理后重试。",
  chat_failed: "对话测试失败，请检查 Key 与模型后重试。",
  catalog_failed: "无法读取该服务的模型目录，请检查 API 地址。",
};

function requirementText(t: (s: string) => string, value?: string): string {
  return t(value === "required" ? "需要" : value === "none" ? "不需要" : "未知");
}

function FreeModelCard({ provider, draft, verification, pending, onVerify }: {
  provider: FreeModelProvider; draft: GlobalSettingsDraft; verification?: Verification;
  pending: boolean; onVerify: (provider: FreeModelProvider, key: string) => Promise<boolean>;
}) {
  const t = useFreeModelText();
  const [key, setKey] = useState("");
  const [openError, setOpenError] = useState(false);
  const model = provider.recommended_model;
  const baseURL = trimSlash(provider.base_url);
  const fallbacks = settingsValue(draft.doc, ["fallback", "providers"]);
  const isPrimary = Boolean(baseURL) && trimSlash(settingsValue(draft.doc, ["baseURL"])) === baseURL && Boolean(settingsValue(draft.doc, ["model"]));
  const inChain = Array.isArray(fallbacks) && fallbacks.some((item) => settingsValue(item, ["name"]) === provider.slug);
  const requirements = provider.requirements ?? {};

  async function openSignup(): Promise<void> {
    setOpenError(false);
    try { await openExternalURL(provider.signup_url ?? ""); } catch { setOpenError(true); }
  }
  const failureText = verification && !verification.ok ? t(REASON_MESSAGE[verification.reason ?? ""] ?? "验证未通过，请重试。") : "";
  return <article className="free-model-card" data-slug={provider.slug} aria-label={provider.name}>
    <header>
      <h4>{provider.name}</h4>
      {provider.unverified && <span className="free-model-badge">{t("未核实")}</span>}
      <span className="free-model-status">{isPrimary ? t("已配置为主模型") : inChain ? t("已在备用链中") : t("未配置")}</span>
    </header>
    {provider.unverified && <ul className="free-model-unverified">{unverifiedReasons(provider).map((r) => <li key={r}>{t(r)}</li>)}</ul>}
    {provider.slug === GOOGLE_SLUG && <p className="free-model-region" role="note"><ShieldAlert size={15} />{t("地区限制：中国大陆不支持，请勿在中国大陆使用。")}</p>}
    <dl>
      <dt>{t("推荐模型")}</dt><dd>{model?.id ?? t("未知")}</dd>
      <dt>{t("免费方式")}</dt><dd>{provider.free?.type === "permanent" ? t("长期免费") : provider.free?.type === "daily" ? t("每日额度") : t("免费方式未核实")}{provider.free?.note ? ` · ${provider.free.note}` : ""}</dd>
      <dt>{t("门槛")}</dt><dd>{t("绑卡")}：{requirementText(t, requirements.credit_card)} · {t("实名")}：{requirementText(t, requirements.real_name)} · {t("地区")}：{requirements.region === "required" ? t("地区限制") : requirementText(t, requirements.region)}{requirements.other ? ` · ${requirements.other}` : ""}</dd>
      {model && <><dt>{t("能力")}</dt><dd>{(model.capabilities ?? []).map((c) => <span className="free-model-tag" key={c}>{c}</span>)}<span className="free-model-tag">{t("工具调用")}：{model.tool_calling === "supported" ? t("支持") : model.tool_calling === "unsupported" ? t("不支持") : t("待核实")}</span></dd></>}
    </dl>
    {(provider.usage_notes ?? []).length > 0 && <ul className="free-model-notes">{(provider.usage_notes ?? []).map((note) => <li key={note}>{note}</li>)}</ul>}
    <div className="free-model-actions">
      <button type="button" onClick={() => void openSignup()}><ExternalLink size={14} />{t("打开注册页")}</button>
    </div>
    {openError && <p className="global-settings-error" role="alert">{t("打开注册页失败，请手动访问供应商官网。")}</p>}
    <div className="free-model-verify">
      <input type="password" autoComplete="off" spellCheck={false} aria-label={`${t("粘贴 API Key")} · ${provider.name}`} placeholder={t("粘贴 API Key")} value={key} onChange={(event) => setKey(event.target.value)} />
      <button type="button" className="primary" disabled={draft.busy || pending || !key.trim() || !model || !draft.doc} onClick={() => void onVerify(provider, key.trim()).then((ok) => { if (ok) setKey(""); })}>{t(draft.busy || pending ? "验证中…" : "验证并使用")}</button>
    </div>
    {verification && <div role="status" className={verification.ok ? "global-settings-success" : "global-settings-error"}>
      <span>{t("最近一次验证")}：{verification.ok ? t("通过") : t("未通过")}</span>
      {verification.message && <p>{t(verification.message)}</p>}
      {failureText && <p>{failureText}</p>}
      {verification.toolSupport === "unsupported" && <p className="free-model-warning"><TriangleAlert size={14} />{t("该模型不支持工具调用，Agent 任务可能无法使用，但仍可保存用于普通对话。")}</p>}
    </div>}
  </article>;
}

export function FreeModelCatalog({ draft }: { draft: GlobalSettingsDraft }) {
  const t = useFreeModelText();
  const { language } = useI18n();
  const [providers, setProviders] = useState<FreeModelProvider[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [results, setResults] = useState<Record<string, Verification>>({});
  const [pendingSave, setPendingSave] = useState<PendingSave | null>(null);
  const identity = draft.identity;
  const identityKey = JSON.stringify([identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, identity.deviceId]);
  const generation = useRef(0);

  // biome-ignore lint/correctness/useExhaustiveDependencies: identity content is tracked through identityKey.
  const load = useCallback(() => {
    const current = ++generation.current;
    setLoadError(false);
    getFreeModels(identity).then((response) => { if (current === generation.current) setProviders(response.providers ?? []); })
      .catch(() => { if (current === generation.current) setLoadError(true); });
  }, [identityKey]);
  useEffect(() => { load(); return () => { generation.current++; }; }, [load]);

  // setField only queues state; saving one render later lets save() read the new document.
  useEffect(() => {
    if (!pendingSave || draft.busy) return;
    const { slug, role } = pendingSave;
    setPendingSave(null);
    void draft.save().then((ok) => setResults((prev) => ({ ...prev, [slug]: { ...prev[slug], ok: true, message: ok ? (role === "primary" ? "验证通过，已设为主模型并保存。" : "验证通过，已加入备用链并保存。") : "验证通过，但保存失败，请在页面底部重试保存。" } })));
  }, [pendingSave, draft]);

  async function verify(provider: FreeModelProvider, apiKey: string): Promise<boolean> {
    const slug = provider.slug ?? "";
    const model = provider.recommended_model?.id;
    if (!draft.doc || !model || !slug) return false;
    const hasPrimary = Boolean(settingsValue(draft.doc, ["model"]));
    const sameAsPrimary = trimSlash(settingsValue(draft.doc, ["baseURL"])) === trimSlash(provider.base_url);
    const role: PendingSave["role"] = hasPrimary && !sameAsPrimary ? "fallback" : "primary";
    const entry = { name: slug, type: PROVIDER_TYPE, baseURL: provider.base_url, model, apiKey };
    const providersOnChain = upsertFallbackProvider(settingsValue(draft.doc, ["fallback", "providers"]), entry);
    const doc: SettingsDoc = role === "primary"
      ? { ...draft.doc, provider: PROVIDER_TYPE, baseURL: provider.base_url, model, apiKey }
      : { ...draft.doc, fallback: { ...(settingsValue(draft.doc, ["fallback"]) as Record<string, unknown> | undefined), providers: providersOnChain } };
    let connection: SettingsConnection;
    try {
      connection = await apiRequest<SettingsConnection>(identity, `${SETTINGS_ENDPOINT}/test-provider`, { method: "POST", body: { doc, ...(role === "fallback" ? { provider: slug } : {}) } });
    } catch (error) {
      // Never log the error: a failing request may echo the pasted key.
      setResults((prev) => ({ ...prev, [slug]: { ok: false, reason: error instanceof ApiError ? "chat_failed" : "network" } }));
      return false;
    }
    setResults((prev) => ({ ...prev, [slug]: { ok: connection.ok, reason: connection.reason, toolSupport: connection.tool_support } }));
    if (!connection.ok) return false;
    if (role === "primary") {
      draft.setField(["provider"], PROVIDER_TYPE);
      draft.setField(["baseURL"], provider.base_url);
      draft.setField(["model"], model);
      draft.setField(["apiKey"], apiKey);
    } else {
      draft.setField(["fallback", "providers"], providersOnChain);
      draft.setField(["fallback", "enabled"], true);
    }
    setPendingSave({ slug, role });
    return true;
  }

  const sorted = providers ? sortFreeProviders(providers, language) : [];
  return <section className="free-model-catalog" aria-label={t("免费模型")}>
    <h3>{t("免费模型")}</h3>
    <p>{t("几分钟内用免费模型跑通第一条回复：注册、粘贴 Key、验证并使用。目录为内置快照，以供应商官方页面为准。")}</p>
    {providers === null && !loadError && <p role="status">{t("正在读取免费模型目录…")}</p>}
    {loadError && <div className="global-settings-error" role="alert"><span>{t("无法读取免费模型目录，请稍后重试。")}</span><button type="button" onClick={load}>{t("重试")}</button></div>}
    <div className="free-model-list">{sorted.map((provider) => <FreeModelCard key={provider.slug} provider={provider} draft={draft} verification={results[provider.slug ?? ""]} pending={pendingSave?.slug === provider.slug} onVerify={verify} />)}</div>
  </section>;
}
