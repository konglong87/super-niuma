import type { LucideIcon } from "lucide-react";
import { Activity, Bot, Brain, Cpu, Database, FileJson2, Layers, ListChecks, Monitor, Settings2, Sparkles, Star, Users, WandSparkles } from "lucide-react";
import type { SettingsSection } from "../routes";

export type SettingsNavItem = {
  key: SettingsSection;
  icon: LucideIcon;
  zh: string;
  en: string;
  description: readonly [string, string];
  advanced?: boolean;
  desktopOnly?: boolean;
};

export type SettingsNavGroup = {
  key: string;
  zh: string;
  en: string;
  items: readonly SettingsNavItem[];
};

const item = (key: SettingsSection, icon: LucideIcon, zh: string, en: string, description: readonly [string, string], advanced = false, desktopOnly = false): SettingsNavItem => ({ key, icon, zh, en, description, advanced, desktopOnly });

export const SETTINGS_NAV_GROUPS: readonly SettingsNavGroup[] = [
  {
    key: "workspace",
    zh: "工作区",
    en: "Workspace",
    items: [
      item("general", Settings2, "通用设置", "General", ["管理语言、主题和会话界面偏好。", "Manage language, theme and conversation preferences."]),
      item("appearance", WandSparkles, "外观 · 皮肤", "Appearance · Skin", ["调整背景、遮罩和内容层的视觉效果。", "Tune backgrounds, overlays and content surfaces."]),
      item("pet", Bot, "桌面宠物", "Pet", ["配置陪伴角色的显示、位置和动画。", "Configure the companion's visibility, placement and animation."])
    ]
  },
  {
    key: "operations",
    zh: "运行配置",
    en: "Operations",
    items: [
      item("models", Cpu, "大模型设置", "Models", ["先配置主模型，再按需设置备用模型、电脑操作和图片生成。", "Set up your primary model first, then optional fallbacks, computer use and images."]),
      item("observability", Activity, "可观测性", "Observability", ["读取服务健康、遥测和实际用量记录。", "Read service health, telemetry and recorded usage."]),
      item("profiles", Layers, "智能体定义", "Agent definitions", ["定义智能体的身份、能力、权限和发布版本。", "Define an agent's identity, capabilities, permissions and published versions."]),
      item("feishu", WandSparkles, "飞书连接", "Feishu connections", ["连接飞书账号并准备机器人绑定，不负责 Worker 启停。", "Connect Feishu accounts and prepare bot bindings; Worker controls live elsewhere."]),
      item("teams", Users, "Teams 管理", "Teams", ["编排多个 Profile 的协作团队。", "Compose collaborative teams from profiles."])
    ]
  },
  {
    key: "library",
    zh: "内容库",
    en: "Library",
    items: [
      item("prompts", Star, "常用提示词", "Common prompts", ["维护可复用的提示词模板。", "Maintain reusable prompt templates."]),
      item("memory", Brain, "Memory 管理", "Memory", ["查看和维护桌面端持久记忆。", "View and maintain desktop memory."]),
      item("skills", Sparkles, "Skills 管理", "Skills", ["查看和维护可注入的技能内容。", "View and maintain injectable skills."])
    ]
  },
  {
    key: "advanced",
    zh: "高级",
    en: "Advanced",
    items: [
      item("agent", Bot, "入口分配", "Entry assignments", ["选择 Web、移动端和渠道入口使用哪个已发布智能体定义。", "Choose which published agent definition each entry point uses."], true),
      item("provisioning", Monitor, "Worker 运行", "Worker runtime", ["预检、启动、重启、停止并查看渠道 Worker 状态。", "Preflight, start, restart, stop and inspect channel Worker status."], true),
      item("session-backend", Database, "会话存储", "Session storage", ["选择桌面端聊天事件的权威存储，并在切换后重启本地服务。", "Choose the desktop chat event store; the local service restarts after switching."], true, true),
      item("json", FileJson2, "全局 Settings JSON", "Settings JSON", ["编辑全局配置文档，与模型表单保持同步。", "Edit the global settings document, synchronized with the model form."], true),
      item("effective", ListChecks, "生效配置", "Effective configuration", ["核对文件解析结果、服务启动快照及会话运行配置。", "Compare resolved files, server startup defaults and session configuration."], true)
    ]
  }
];

export const SETTINGS_NAV_ITEMS = SETTINGS_NAV_GROUPS.flatMap((group) => group.items);

export function settingsNavItem(section: SettingsSection): SettingsNavItem {
  return SETTINGS_NAV_ITEMS.find((item) => item.key === section) ?? SETTINGS_NAV_ITEMS[0];
}
