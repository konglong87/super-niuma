import { Monitor } from "lucide-react";
import { settingsValue, type GlobalSettingsDraft } from "./globalSettingsDraft";
import { useGlobalSettingsText } from "./globalSettingsCopy";
import { COMPUTER_USE_ROUTES } from "./modelSettingsConstants";
import { Section, stringValue } from "./modelSettingsFields";

type ComputerUseRouteOption = { provider: string; model: string; label: string; inheritedModel: boolean };

function routeMatches(route: unknown, provider: string, model: string): boolean {
  if (!route || typeof route !== "object") return false;
  const entry = route as Record<string, unknown>;
  return stringValue(entry.provider) === provider && stringValue(entry.model) === model;
}

export function ComputerUseRouting({ draft, providers }: { draft: GlobalSettingsDraft; providers: unknown[] }) {
  const t = useGlobalSettingsText();
  const primaryModel = stringValue(settingsValue(draft.doc, ["model"]));
  const routeValue = settingsValue(draft.doc, COMPUTER_USE_ROUTES);
  const routes = Array.isArray(routeValue) ? routeValue : [];
  const options: ComputerUseRouteOption[] = [];
  if (primaryModel) options.push({ provider: "primary", model: primaryModel, label: t("主模型"), inheritedModel: false });
  const seen = new Set<string>();
  providers.forEach((provider) => {
    const name = stringValue(settingsValue(provider, ["name"])).trim();
    if (!name || seen.has(name)) return;
    seen.add(name);
    const configuredModel = stringValue(settingsValue(provider, ["model"])).trim();
    const model = configuredModel || primaryModel;
    options.push({ provider: name, model, label: name, inheritedModel: !configuredModel && Boolean(model) });
  });
  const toggle = (option: ComputerUseRouteOption, enabled: boolean) => {
    const next = routes.filter((route) => !routeMatches(route, option.provider, option.model));
    if (enabled) next.push({ provider: option.provider, model: option.model });
    draft.setField(COMPUTER_USE_ROUTES, next);
  };
  const configuredCount = options.filter((option) => routes.some((route) => routeMatches(route, option.provider, option.model))).length;
  return <Section title="Computer Use 桌面操作" description="Computer Use 会读取桌面截图并返回点击、输入、滚动等操作。只有支持图片输入和 ComputerUse 动作调用的模型，才应加入这条视觉路由。普通 Fallback 不会自动接收桌面截图。">
    <div className="global-settings-callout global-settings-callout-computer"><Monitor size={17} /><div><strong>{t("这是独立的桌面视觉路由")}</strong><p>{t("普通模型 Fallback 用于文本和常规工具调用；Computer Use 只会把截图发送给下面明确选择的 Provider/model。")}</p></div></div>
    {options.length === 0 ? <div className="global-settings-route-empty">{t("请先在上方配置主模型或备用模型。")}</div> : <div className="global-settings-computer-routes">{options.map((option) => {
      const checked = routes.some((route) => routeMatches(route, option.provider, option.model));
      const disabled = !option.model;
      return <label className={`global-settings-computer-route${checked ? " checked" : ""}${disabled ? " disabled" : ""}`} key={`${option.provider}:${option.model}`}>
        <input type="checkbox" checked={checked} disabled={disabled} aria-label={`${option.label} ${option.model || t("未配置模型")}`} onChange={(event) => toggle(option, event.target.checked)} />
        <span className="global-settings-computer-route-copy"><strong>{option.label}</strong><small>{option.model || t("未配置模型")}{option.inheritedModel ? ` · ${t("继承主模型")}` : ""}</small></span>
        <span className="global-settings-computer-route-status">{checked ? t("已启用截图") : disabled ? t("需要模型") : t("未启用截图")}</span>
      </label>;
    })}</div>}
    <p className="global-settings-section-footnote">{t("已启用")} {configuredCount}/{options.length} {t("个 Computer Use 视觉路由。保存后，运行中的服务可能需要重启。")}</p>
  </Section>;
}
