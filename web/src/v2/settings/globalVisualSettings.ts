import type { CSSProperties } from "react";
import type { SettingsDoc } from "../../lib/types";
import type { DesktopBackgroundImage } from "../desktopServiceBridge";
import { DEFAULT_PET_MODEL } from "../components/petAssets";
import { settingsValue, type SettingsPath } from "./globalSettingsDraft";

export const APPEARANCE_ROOT = ["appearance"] as const satisfies SettingsPath;
export const PET_ROOT = ["pet"] as const satisfies SettingsPath;

export type AppearanceSettings = {
  enabled: boolean;
  backgroundColor: string;
  backgroundImage: string;
  overlayOpacity: number;
  sidebarSurfaceOpacity: number;
  emptyBlur: number;
  conversationBlur: number;
  composerBlur: number;
  bubbleOpacity: number;
};

export type PetSettings = {
  enabled: boolean;
  visible: boolean;
  model: string;
  scale: number;
  right: number;
  bottom: number;
  animation: string;
  statusBubble: boolean;
  fontScale: number;
};

export type GlobalVisualSettings = {
  appearance: AppearanceSettings;
  pet: PetSettings;
};

export const DEFAULT_APPEARANCE: AppearanceSettings = {
  enabled: false,
  backgroundColor: "#f7f8fa",
  backgroundImage: "",
  overlayOpacity: 0,
  sidebarSurfaceOpacity: 0.46,
  emptyBlur: 0,
  conversationBlur: 0,
  composerBlur: 0,
  bubbleOpacity: 1
};

export const DEFAULT_PET: PetSettings = {
  enabled: false,
  visible: true,
  model: DEFAULT_PET_MODEL,
  scale: 1,
  right: 24,
  bottom: 20,
  animation: "idle",
  statusBubble: true,
  fontScale: 1
};

const NUMBER_LIMITS = {
  overlayOpacity: [0, 1],
  sidebarSurfaceOpacity: [0, 1],
  emptyBlur: [0, 24],
  conversationBlur: [0, 24],
  composerBlur: [0, 24],
  bubbleOpacity: [0.2, 1],
  scale: [0.5, 2],
  right: [0, 160],
  bottom: [0, 160],
  fontScale: [0.75, 1.5]
} as const;

function booleanValue(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function stringValue(value: unknown, fallback: string): string {
  return typeof value === "string" ? value : fallback;
}

function numberValue(value: unknown, fallback: number, limits: readonly [number, number]): number {
  if (typeof value !== "number" || !Number.isFinite(value)) return fallback;
  return Math.min(limits[1], Math.max(limits[0], value));
}

function colorValue(value: unknown, fallback: string): string {
  if (typeof value !== "string") return fallback;
  const trimmed = value.trim();
  return /^#[0-9a-f]{3,8}$/i.test(trimmed) ? trimmed : fallback;
}

function backgroundImageValue(value: unknown): string {
  if (typeof value !== "string") return "";
  const trimmed = value.trim();
  if (!trimmed) return "";
  return /^(?:https?:\/\/|\/|\.\/)/i.test(trimmed) ? trimmed : "";
}

export function readVisualSettings(doc: SettingsDoc | null | undefined, desktopBackground?: DesktopBackgroundImage | null): GlobalVisualSettings {
  const appearance = (settingsValue(doc, APPEARANCE_ROOT) || {}) as Record<string, unknown>;
  const pet = (settingsValue(doc, PET_ROOT) || {}) as Record<string, unknown>;
  const localBackground = desktopBackground?.mode === "local" && desktopBackground.data_url ? desktopBackground.data_url : "";
  return {
    appearance: {
      enabled: booleanValue(appearance.enabled, DEFAULT_APPEARANCE.enabled),
      backgroundColor: colorValue(appearance.backgroundColor, DEFAULT_APPEARANCE.backgroundColor),
      backgroundImage: localBackground || backgroundImageValue(appearance.backgroundImage),
      overlayOpacity: numberValue(appearance.overlayOpacity, DEFAULT_APPEARANCE.overlayOpacity, NUMBER_LIMITS.overlayOpacity),
      sidebarSurfaceOpacity: numberValue(appearance.sidebarSurfaceOpacity, DEFAULT_APPEARANCE.sidebarSurfaceOpacity, NUMBER_LIMITS.sidebarSurfaceOpacity),
      emptyBlur: numberValue(appearance.emptyBlur, DEFAULT_APPEARANCE.emptyBlur, NUMBER_LIMITS.emptyBlur),
      conversationBlur: numberValue(appearance.conversationBlur, DEFAULT_APPEARANCE.conversationBlur, NUMBER_LIMITS.conversationBlur),
      composerBlur: numberValue(appearance.composerBlur, DEFAULT_APPEARANCE.composerBlur, NUMBER_LIMITS.composerBlur),
      bubbleOpacity: numberValue(appearance.bubbleOpacity, DEFAULT_APPEARANCE.bubbleOpacity, NUMBER_LIMITS.bubbleOpacity)
    },
    pet: {
      enabled: booleanValue(pet.enabled, DEFAULT_PET.enabled),
      visible: booleanValue(pet.visible, DEFAULT_PET.visible),
      model: stringValue(pet.model, DEFAULT_PET.model),
      scale: numberValue(pet.scale, DEFAULT_PET.scale, NUMBER_LIMITS.scale),
      right: numberValue(pet.right, DEFAULT_PET.right, NUMBER_LIMITS.right),
      bottom: numberValue(pet.bottom, DEFAULT_PET.bottom, NUMBER_LIMITS.bottom),
      animation: stringValue(pet.animation, DEFAULT_PET.animation),
      statusBubble: booleanValue(pet.statusBubble, DEFAULT_PET.statusBubble),
      fontScale: numberValue(pet.fontScale, DEFAULT_PET.fontScale, NUMBER_LIMITS.fontScale)
    }
  };
}

export function visualSettingsStyle(settings: GlobalVisualSettings): CSSProperties {
  const { appearance } = settings;
  return {
    "--webui2-visual-background-color": appearance.backgroundColor,
    "--webui2-visual-background-image": appearance.backgroundImage ? `url("${appearance.backgroundImage.replaceAll('"', "")}")` : "none",
    "--webui2-visual-overlay-opacity": String(appearance.overlayOpacity),
    "--webui2-sidebar-surface-opacity": `${Math.round(appearance.sidebarSurfaceOpacity * 100)}%`,
    "--webui2-visual-empty-blur": `${appearance.emptyBlur}px`,
    "--webui2-visual-conversation-blur": `${appearance.conversationBlur}px`,
    "--webui2-visual-composer-blur": `${appearance.composerBlur}px`,
    "--webui2-visual-bubble-opacity": String(appearance.bubbleOpacity)
  } as CSSProperties;
}

export function visualSettingsForPreview(doc: SettingsDoc | null | undefined): GlobalVisualSettings | null {
  return doc ? readVisualSettings(doc) : null;
}

export const APPEARANCE_FIELD_LABELS = {
  enabled: ["启用外观", "Enable appearance"],
  backgroundColor: ["背景颜色", "Background color"],
  backgroundImage: ["背景图片 URL", "Background image URL"],
  overlayOpacity: ["遮罩透明度", "Overlay opacity"],
  sidebarSurfaceOpacity: ["侧栏表面色透明度", "Sidebar surface opacity"],
  emptyBlur: ["空状态模糊", "Empty-state blur"],
  conversationBlur: ["对话模糊", "Conversation blur"],
  composerBlur: ["编辑区模糊", "Composer blur"],
  bubbleOpacity: ["气泡不透明度", "Bubble opacity"]
} as const;

export const PET_FIELD_LABELS = {
  enabled: ["启用桌面宠物", "Enable pet"],
  visible: ["显示宠物", "Show pet"],
  model: ["模型资源", "Model asset"],
  scale: ["缩放", "Scale"],
  right: ["默认右侧距离", "Default right offset"],
  bottom: ["默认底部距离", "Default bottom offset"],
  animation: ["动画", "Animation"],
  statusBubble: ["显示状态气泡", "Show status bubble"],
  fontScale: ["文字缩放", "Font scale"]
} as const;
