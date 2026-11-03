import { Check, CircleAlert, Cpu, ChevronRight } from "lucide-react";
import { settingsValue, type GlobalSettingsDraft } from "./globalSettingsDraft";
import { PROVIDER_TYPES, FALLBACK_PROVIDERS, COMPUTER_USE_ROUTES, IMAGE_ROOT } from "./modelSettingsConstants";
import { Field, stringValue } from "./modelSettingsFields";
import { ProviderConnection } from "./ModelConnectionTest";
import { useModelSettingsText } from "./modelSettingsCopy";
import type { ModelSection } from "./ModelSettingsTabs";

function PrimaryConfigurationStatus({ draft }: { draft: GlobalSettingsDraft }) {
  const t = useModelSettingsText();
  // A successful fallback test must never label the primary connection as working.
  const connection = draft.connection?.provider === undefined ? draft.connection : null;
  const model = stringValue(settingsValue(draft.doc, ["model"]));
  const provider = stringValue(settingsValue(draft.doc, ["provider"]));
  const title = connection ? t(connection.ok ? "模型目录可访问" : "模型目录连接失败") : t("尚未测试目录连接");
  return <section className="model-status" aria-label={t("配置中的主模型")}>
    <div className="model-status-icon" data-result={connection ? connection.ok ? "success" : "error" : "unknown"}>
      {connection ? connection.ok ? <Check size={19} /> : <CircleAlert size={19} /> : <Cpu size={19} />}
    </div>
    <div className="model-status-copy">
      <span>{t("配置中的主模型")} · {title}</span>
      <strong>{model || t("未填写模型名称")}</strong>
      <small>{provider || t("沿用现有默认值")} · {t("配置状态，不代表运行中任务的实际模型。")}</small>
    </div>
  </section>;
}

function OptionalCapabilities({ draft, onNavigate }: { draft: GlobalSettingsDraft; onNavigate: (value: ModelSection) => void }) {
  const t = useModelSettingsText();
  const providerValue = settingsValue(draft.doc, FALLBACK_PROVIDERS);
  const routeValue = settingsValue(draft.doc, COMPUTER_USE_ROUTES);
  const providers = Array.isArray(providerValue) ? providerValue : [];
  const routes = Array.isArray(routeValue) ? routeValue : [];
  const items: Array<{ id: ModelSection; label: string; state: string; detail: string }> = [
    { id: "providers", label: "备用模型", state: providers.length ? `${providers.length} ${t("个备用连接")}` : t("未添加备用模型"), detail: settingsValue(draft.doc, ["fallback", "enabled"]) === true ? "自动切换已启用" : "自动切换未显式启用" },
    { id: "computer", label: "电脑操作", state: routes.length ? `${routes.length} ${t("个截图路由")}` : t("未选择截图路由"), detail: "需主动选择可接收截图的模型，支持能力仍需核实。" },
    { id: "images", label: "图片生成", state: t(settingsValue(draft.doc, [...IMAGE_ROOT, "enabled"]) === true ? "已启用，未验证能力" : "关闭或沿用默认值"), detail: "单独配置图片模型与生成限制。" },
  ];
  return <section className="model-capabilities" aria-label={t("可选能力")}>
    <h3>{t("可选能力")}</h3><p>{t("按需配置，不会自动授予截图权限或启用图片请求。")}</p>
    <div className="model-capability-list">{items.map((item) => <button type="button" key={item.id} onClick={() => onNavigate(item.id)}>
      <span><strong>{t(item.label)}</strong><small>{t(item.detail)}</small></span><span className="model-capability-state">{item.state}</span><ChevronRight size={16} />
    </button>)}</div>
  </section>;
}

export function ModelQuickSetup({ draft, onNavigate }: { draft: GlobalSettingsDraft; onNavigate: (value: ModelSection) => void }) {
  const t = useModelSettingsText();
  const authToken = settingsValue(draft.doc, ["authToken"]);
  const useToken = typeof authToken === "string" && !settingsValue(draft.doc, ["apiKey"]);
  const options = PROVIDER_TYPES.map(([value, label]) => [value, value === "" ? t("沿用现有默认值") : value === "custom" ? t("自定义服务") : value === "openai-compatible" ? t("OpenAI 兼容服务") : label] as const);
  return <>
    <PrimaryConfigurationStatus draft={draft} />
    <section className="model-quick-form">
      <h3>{t("快速配置主模型")}</h3>
      <p>{t("先配置日常对话和代码任务使用的模型，其他能力可以稍后设置。")}</p>
      <div className="global-settings-grid">
        <Field draft={draft} path={["provider"]} label={t("模型服务")} options={options} />
        <Field draft={draft} path={["model"]} label={t("模型名称")} placeholder={t("填写服务商提供的模型 ID")} />
        <Field draft={draft} path={["baseURL"]} label="API 地址" placeholder="https://api.example.com/v1" full />
        <Field key={useToken ? "token" : "key"} draft={draft} path={[useToken ? "authToken" : "apiKey"]} label={useToken ? "Auth Token" : "API Key"} type="password" full />
      </div>
      {useToken && <p className="model-help">{t("使用已有 Auth Token")}</p>}
      <div className="model-advanced-hint">
        <details className="model-credential-help"><summary>{t("密钥与连接说明")}</summary>
          <p>{t("凭据也可能由环境变量或服务端配置提供；这里留空不代表未配置。")}</p>
          <p>{t("已保存的密钥保持脱敏；留在原值时不会替换。新输入的密钥会随测试或保存请求发送到当前配置服务，请仅使用可信服务。")}</p>
          <p>{t("以下为配置值，不会自动探测或改写 API 协议。")}</p>
        </details>
        <button type="button" onClick={() => onNavigate("providers")}>{t("协议与其他认证方式")}</button>
      </div>
      <ProviderConnection draft={draft} />
    </section>
    <OptionalCapabilities draft={draft} onNavigate={onNavigate} />
  </>;
}
