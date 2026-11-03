import { useI18n } from "../../lib/i18n";
import { globalSettingsText } from "./globalSettingsCopy";

const english: Record<string, string> = {
  "先添加备用连接，再切换到“电脑操作”选择可接收截图的模型。地址和密钥只需填写一次。": "Add fallback connections here, then open Computer use to choose models allowed to receive screenshots. Enter each URL and credential only once.",
  "密钥与连接说明": "Credentials and connection notes",
  "快速配置": "Quick setup",
  "高级配置": "Advanced setup",
  "模型设置分区": "Model settings sections",
  "配置中的主模型": "Primary model in configuration",
  "未填写模型名称": "Model name not entered",
  "沿用现有默认值": "Use existing defaults",
  "模型目录可访问": "Model catalog reachable",
  "模型目录连接失败": "Model catalog connection failed",
  "尚未测试目录连接": "Catalog connection not tested",
  "配置状态，不代表运行中任务的实际模型。": "Configuration status, not the model of an active task.",
  "目录测试成功不代表模型支持对话、工具或看图。": "A catalog test does not verify chat, tool or vision support.",
  "测试只检查模型目录连接，不验证对话、工具或看图能力。": "This test checks only the model catalog, not chat, tool or vision capabilities.",
  "快速配置主模型": "Quickly configure the primary model",
  "先配置日常对话和代码任务使用的模型，其他能力可以稍后设置。": "Start with your model for everyday chat and coding. Set up other capabilities later.",
  "模型服务": "Model service",
  "模型名称": "Model name",
  "填写服务商提供的模型 ID": "Enter the model ID from your service",
  "OpenAI 兼容服务": "OpenAI-compatible service",
  "自定义服务": "Custom service",
  "以下为配置值，不会自动探测或改写 API 协议。": "These are configuration values. The API protocol is not auto-detected or rewritten.",
  "协议与其他认证方式": "Protocol and other authentication options",
  "使用已有 Auth Token": "Using an existing Auth Token",
  "凭据也可能由环境变量或服务端配置提供；这里留空不代表未配置。": "Credentials may come from environment variables or server configuration; an empty field does not mean they are missing.",
  "已保存的密钥保持脱敏；留在原值时不会替换。新输入的密钥会随测试或保存请求发送到当前配置服务，请仅使用可信服务。": "Stored credentials remain masked and are retained when unchanged. Newly entered credentials are sent to the current configuration service when testing or saving; use only a trusted service.",
  "可选能力": "Optional capabilities",
  "按需配置，不会自动授予截图权限或启用图片请求。": "Configure as needed. No screenshot permission or image requests are enabled automatically.",
  "备用模型": "Fallback models",
  "电脑操作": "Computer use",
  "图片生成": "Image generation",
  "未添加备用模型": "No fallback models added",
  "个备用连接": "fallback connections",
  "自动切换已启用": "Automatic fallback is enabled",
  "自动切换未显式启用": "Automatic fallback is not explicitly enabled",
  "个截图路由": "screenshot routes",
  "未选择截图路由": "No screenshot routes selected",
  "需主动选择可接收截图的模型，支持能力仍需核实。": "Explicitly choose which models may receive screenshots. Capability still needs verification.",
  "已启用，未验证能力": "Enabled; capability not verified",
  "关闭或沿用默认值": "Off or using defaults",
  "单独配置图片模型与生成限制。": "Configure the image model and generation limits separately.",
  "高级模型设置": "Advanced model settings",
  "管理主模型连接与备用模型。改动仅保存在草稿中，点击保存后才写入配置。": "Manage primary and fallback connections. Changes remain drafts until you save.",
  "先配置一个主模型，再按需添加备用连接。": "Configure a primary model first, then add fallback connections if needed.",
};

export function useModelSettingsText() {
  const { language } = useI18n();
  return (text: string): string => language === "en" ? english[text] ?? globalSettingsText(language, text) : text;
}
