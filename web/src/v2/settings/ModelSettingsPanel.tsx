import { useId, useState } from "react";
import { settingsValue, type GlobalSettingsDraft } from "./globalSettingsDraft";
import { FALLBACK_PROVIDERS, PRIMARY_PROVIDER } from "./modelSettingsConstants";
import { useModelSettingsText } from "./modelSettingsCopy";
import { ModelSettingsTabs, MODEL_SECTIONS, type ModelSection } from "./ModelSettingsTabs";
import { ModelQuickSetup } from "./ModelQuickSetup";
import { ModelProviderSettings } from "./ModelProviderSettings";
import { ComputerUseRouting } from "./ModelComputerSettings";
import { ModelImageSettings } from "./ModelImageSettings";

export function ModelSettingsPanel({ draft }: { draft: GlobalSettingsDraft }) {
  const t = useModelSettingsText();
  const id = useId();
  const [section, setSection] = useState<ModelSection>("quick");
  const [selectedProvider, setSelectedProvider] = useState(PRIMARY_PROVIDER);
  const value = settingsValue(draft.doc, FALLBACK_PROVIDERS);
  const providers = Array.isArray(value) ? value : [];
  function navigate(value: ModelSection): void {
    if (value === "providers") setSelectedProvider(PRIMARY_PROVIDER);
    setSection(value);
    document.getElementById(`${id}-${value}-tab`)?.focus();
  }
  return <fieldset className="global-settings-form model-settings-form" disabled={draft.busy || !draft.doc || Boolean(draft.syntaxError)}>
    <ModelSettingsTabs section={section} onChange={setSection} id={id} />
    {MODEL_SECTIONS.map(({ id: value }) => <div key={value} role="tabpanel" id={`${id}-${value}-panel`} aria-labelledby={`${id}-${value}-tab`} hidden={section !== value}>
      {value === "quick" && section === value && <ModelQuickSetup draft={draft} onNavigate={navigate} />}
      {value === "providers" && <>
        <h3>{t("高级模型设置")}</h3><p className="model-help">{t("管理主模型连接与备用模型。改动仅保存在草稿中，点击保存后才写入配置。")}</p>
        <ModelProviderSettings draft={draft} selected={selectedProvider} onSelect={setSelectedProvider} />
      </>}
      {value === "computer" && <ComputerUseRouting draft={draft} providers={providers} />}
      {value === "images" && <ModelImageSettings draft={draft} />}
    </div>)}
  </fieldset>;
}
