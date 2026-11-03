import type { LucideIcon } from "lucide-react";
import { Code2, FileText, Sparkles } from "lucide-react";
import type { SessionRef } from "../types";

export type HomeExperience = "task-first" | "simple";

// Keep the old home available as a deliberate fallback. Switching this value
// restores the original centered empty state without touching session views.
export const HOME_EXPERIENCE: HomeExperience = "task-first";

export const WELCOME_DRAFT_REF = "tenant:welcome-draft" as SessionRef;

export type HomePresetID = "code" | "files" | "ideas";

export type HomePreset = {
  id: HomePresetID;
  icon: LucideIcon;
  promptMode: "code" | "chat";
  title: { en: string; zh: string };
  description: { en: string; zh: string };
};

export const HOME_PRESETS: readonly HomePreset[] = [
  {
    id: "code",
    icon: Code2,
    promptMode: "code",
    title: { zh: "写代码", en: "Write code" },
    description: { zh: "阅读、修改、调试和测试项目", en: "Read, change, debug, and test projects" }
  },
  {
    id: "files",
    icon: FileText,
    promptMode: "code",
    title: { zh: "文件处理", en: "Handle files" },
    description: { zh: "整理、总结和批量处理工作区文件", en: "Organize, summarize, and process workspace files" }
  },
  {
    id: "ideas",
    icon: Sparkles,
    promptMode: "chat",
    title: { zh: "火花想法", en: "Spark ideas" },
    description: { zh: "头脑风暴、写方案和拆解下一步", en: "Brainstorm, shape plans, and find next steps" }
  }
];
