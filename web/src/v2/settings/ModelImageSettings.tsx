import { settingsValue, type GlobalSettingsDraft } from "./globalSettingsDraft";
import { useGlobalSettingsText } from "./globalSettingsCopy";
import { FALLBACK_PROVIDERS, IMAGE_ROOT } from "./modelSettingsConstants";
import { Field, BooleanField, Section, StringListField, stringValue } from "./modelSettingsFields";

export function ModelImageSettings({ draft }: { draft: GlobalSettingsDraft }) {
  const t = useGlobalSettingsText();
  const value = settingsValue(draft.doc, FALLBACK_PROVIDERS);
  const providers = Array.isArray(value) ? value : [];
  const namedProviders = providers.map((provider) => stringValue(settingsValue(provider, ["name"]))).filter(Boolean);
  const imageProviders: Array<readonly [string, string]> = [["", "继承默认"], ...namedProviders.map((name): [string, string] => [name, name])];
  return (
    <Section title="图片生成"><div className="global-settings-grid">
      <BooleanField draft={draft} path={[...IMAGE_ROOT, "enabled"]} label="启用图片生成" />
      <BooleanField draft={draft} path={[...IMAGE_ROOT, "previewInContext"]} label="模型上下文包含图片预览" />
      <Field draft={draft} path={[...IMAGE_ROOT, "defaultProvider"]} label="默认图片供应商" options={imageProviders} />
      <Field draft={draft} path={[...IMAGE_ROOT, "defaultModel"]} label="默认图片模型" />
      <Field draft={draft} path={[...IMAGE_ROOT, "size"]} label="默认尺寸" placeholder="auto" />
      <Field draft={draft} path={[...IMAGE_ROOT, "quality"]} label="图片质量" options={[["", "继承默认"], ["auto", "Auto"], ["low", "Low"], ["medium", "Medium"], ["high", "High"]]} />
    </div><details className="global-settings-advanced"><summary>{t("兼容配置与生成限制")}</summary><div className="global-settings-grid">
      <Field draft={draft} path={[...IMAGE_ROOT, "provider"]} label="兼容图片供应商" options={imageProviders} />
      <Field draft={draft} path={[...IMAGE_ROOT, "model"]} label="兼容图片模型" />
      <Field draft={draft} path={[...IMAGE_ROOT, "outputFormat"]} label="输出格式" options={[["", "继承默认"], ["png", "PNG"], ["jpeg", "JPEG"], ["webp", "WebP"]]} />
      <Field draft={draft} path={[...IMAGE_ROOT, "background"]} label="图片背景" options={[["", "继承默认"], ["auto", "Auto"], ["transparent", "透明"], ["opaque", "不透明"]]} />
      {([["maxImages", "最大图片数量"], ["timeoutSeconds", "请求超时（秒）"], ["maxConcurrent", "最大并发"], ["retentionDays", "保留天数"], ["maxPromptChars", "提示词字符上限"], ["maxInputBytes", "输入大小上限（字节）"]] as const).map(([key, label]) => <Field key={key} draft={draft} path={[...IMAGE_ROOT, key]} label={label} type="number" />)}
      <BooleanField draft={draft} path={[...IMAGE_ROOT, "asyncChannelEnabled"]} label="渠道异步图片生成" />
      <StringListField draft={draft} path={[...IMAGE_ROOT, "asyncChannelAccountKeys"]} label="异步生成渠道账号范围" />
    </div></details><details className="global-settings-advanced"><summary>{t("图片任务 Worker")}</summary><div className="global-settings-grid">
      {([["pollIntervalMs", "轮询间隔（毫秒）"], ["maxConcurrent", "Worker 最大并发"], ["maxAttempts", "最大尝试次数"], ["heartbeatSeconds", "心跳间隔（秒）"], ["leaseSeconds", "租约时长（秒）"], ["finalizeTimeoutSeconds", "完成阶段超时（秒）"], ["maxQueuedPerTenant", "每租户排队上限"], ["maxQueuedPerUser", "每用户排队上限"]] as const).map(([key, label]) => <Field key={key} draft={draft} path={[...IMAGE_ROOT, "worker", key]} label={label} type="number" />)}
    </div></details></Section>
  );
}
