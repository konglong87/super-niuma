import { ArrowUpRight, Cpu, Plus, Trash2 } from "lucide-react";
import { settingsValue, type GlobalSettingsDraft, type SettingsPath } from "./globalSettingsDraft";
import { useModelSettingsText } from "./modelSettingsCopy";
import { PRIMARY_PROVIDER, PROTOCOL_RESPONSES, PROVIDER_TYPES, PROTOCOLS, FALLBACK_PROVIDERS } from "./modelSettingsConstants";
import { Field, BooleanField, Section, StringListField, stringValue } from "./modelSettingsFields";
import { ProviderConnection } from "./ModelConnectionTest";

function providerMatchesPrimary(doc: Record<string, unknown> | null, provider: Record<string, unknown>): boolean {
  if (!doc) return false;
  for (const [providerKey, primaryKey] of [["type", "provider"], ["protocol", "providerProtocol"], ["baseURL", "baseURL"], ["model", "model"]] as const) {
    if (stringValue(provider[providerKey]) !== stringValue(doc[primaryKey])) return false;
  }
  return JSON.stringify(provider.responses ?? null) === JSON.stringify(doc.responses ?? null);
}

function ProviderForm({ draft, selected, onRemove, onPromote }: { draft: GlobalSettingsDraft; selected: number; onRemove: () => void; onPromote: () => void }) {
  const t = useModelSettingsText();
  const primary = selected === PRIMARY_PROVIDER;
  const root: SettingsPath = primary ? [] : [...FALLBACK_PROVIDERS, selected];
  const protocolPath = [...root, primary ? "providerProtocol" : "protocol"];
  const isResponses = settingsValue(draft.doc, protocolPath) === PROTOCOL_RESPONSES;
  const selectedProvider = !primary ? settingsValue(draft.doc, root) : null;
  const alreadyPrimary = !primary && selectedProvider && typeof selectedProvider === "object" && providerMatchesPrimary(draft.doc, selectedProvider as Record<string, unknown>);
  return <div className="global-settings-provider-body">
    <div className="global-settings-provider-heading"><h2>{primary ? t("全局主模型") : stringValue(settingsValue(draft.doc, [...root, "name"])) || t("未命名供应商")}</h2>{primary ? <span className="global-settings-badge">{t("默认路由")}</span> : <div className="global-settings-provider-actions">{alreadyPrimary ? <span className="global-settings-badge">{t("已设为主模型")}</span> : <button type="button" className="global-settings-promote" title={t("设为主模型")} onClick={onPromote}><ArrowUpRight size={15} />{t("设为主模型")}</button>}<button type="button" className="global-settings-icon danger" title={t("删除供应商")} aria-label={t("删除供应商")} onClick={onRemove}><Trash2 size={16} /></button></div>}</div>
    <div className="global-settings-grid">
      {!primary && <Field draft={draft} path={[...root, "name"]} label="供应商名称" />}
      <Field draft={draft} path={[...root, primary ? "provider" : "type"]} label="供应商类型" options={PROVIDER_TYPES} />
      <Field draft={draft} path={protocolPath} label="API 协议" options={PROTOCOLS} />
      <Field draft={draft} path={[...root, "baseURL"]} label="API 地址" placeholder="https://api.example.com/v1" full />
      <Field draft={draft} path={[...root, "apiKey"]} label="API Key" type="password" full />
      <Field draft={draft} path={[...root, "authToken"]} label="Auth Token" type="password" full />
      <Field draft={draft} path={[...root, "model"]} label="默认模型" placeholder="模型标识" />
      {primary && <Field draft={draft} path={["effort"]} label="思考强度" options={[["", "继承默认"], ["off", "关闭"], ["low", "Low"], ["medium", "Medium"], ["high", "High"], ["max", "Max"]]} />}
    </div>
    {(isResponses || settingsValue(draft.doc, [...root, "responses"]) !== undefined) && <Section title="Responses"><div className="global-settings-grid">
      <Field draft={draft} path={[...root, "responses", "stateMode"]} label="状态模式" options={[["", "继承默认"], ["stateless", "Stateless"]]} />
      <BooleanField draft={draft} path={[...root, "responses", "store"]} label="服务端存储" />
    </div>{!isResponses && <button type="button" onClick={() => draft.setField([...root, "responses"], undefined)}><Trash2 size={14} />{t("移除 Responses 配置")}</button>}</Section>}
    {!primary && <details className="global-settings-advanced"><summary>{t("图片接口配置")}</summary><div className="global-settings-grid"><Field draft={draft} path={[...root, "imageProtocol"]} label="图片 API 协议" placeholder="继承供应商默认" /><StringListField draft={draft} path={[...root, "imageResultHosts"]} label="图片结果域名白名单" /></div></details>}
    <ProviderConnection draft={draft} provider={primary ? undefined : stringValue(settingsValue(draft.doc, [...root, "name"]))} />
  </div>;
}

export function ModelProviderSettings({ draft, selected, onSelect }: { draft: GlobalSettingsDraft; selected: number; onSelect: (value: number) => void }) {
  const t = useModelSettingsText();
  const providersValue = settingsValue(draft.doc, FALLBACK_PROVIDERS);
  const providers = Array.isArray(providersValue) ? providersValue : [];
  const selection = selected >= providers.length ? PRIMARY_PROVIDER : selected;
  const addProvider = () => {
    const names = new Set(providers.map((provider) => stringValue(settingsValue(provider, ["name"]))));
    let suffix = providers.length + 1;
    while (names.has(`provider-${suffix}`)) suffix++;
    draft.setField(FALLBACK_PROVIDERS, [...providers, { name: `provider-${suffix}`, type: "custom", protocol: "openai-chat-completions" }]);
    onSelect(providers.length);
  };
  const removeProvider = () => {
    if (!window.confirm(t("删除这个供应商？引用它的图片或路由配置也需要调整。"))) return;
    draft.setField(FALLBACK_PROVIDERS, providers.filter((_, index) => index !== selection));
    onSelect(PRIMARY_PROVIDER);
  };
  return <div className="model-provider-settings">
    <div className="global-settings-provider-layout">
      <nav className="global-settings-provider-list" aria-label={t("供应商列表")}>
        <button type="button" className={selection === PRIMARY_PROVIDER ? "selected" : ""} onClick={() => onSelect(PRIMARY_PROVIDER)}><Cpu size={18} /><span><strong>{t("全局主模型")}</strong><small>{stringValue(settingsValue(draft.doc, ["model"])) || t("继承默认模型")}</small></span></button>
        {providers.map((provider, index) =>
          // biome-ignore lint/suspicious/noArrayIndexKey: Draft rows may have duplicate names; positional keys never contain credentials.
          <button type="button" key={index} className={selection === index ? "selected" : ""} onClick={() => onSelect(index)}><Cpu size={18} /><span><strong>{stringValue(settingsValue(provider, ["name"])) || `${t("供应商")} ${index + 1}`}</strong><small>{stringValue(settingsValue(provider, ["model"])) || stringValue(settingsValue(provider, ["type"]))}</small></span></button>)}
        <button type="button" onClick={addProvider}><Plus size={16} />{t("添加供应商")}</button>
      </nav>
      <ProviderForm key={selection} draft={draft} selected={selection} onRemove={removeProvider} onPromote={() => void draft.promoteProvider(selection)} />
    </div>
    <Section title="普通模型 Fallback 链" description="用于文本对话、代码任务和常规工具调用。这里的备用 Provider 不代表一定支持桌面截图。">
      <div className="global-settings-callout"><Cpu size={17} /><div><strong>{t("普通请求和 Computer Use 分开配置")}</strong><p>{t("先添加备用连接，再切换到“电脑操作”选择可接收截图的模型。地址和密钥只需填写一次。")}</p></div></div>
      <div className="global-settings-grid"><BooleanField draft={draft} path={["fallback", "enabled"]} label="启用普通 Fallback" /></div>
    </Section>
  </div>;
}
