import { useRef, type KeyboardEvent } from "react";
import { Cpu, Monitor, Settings2, Sparkles } from "lucide-react";
import { useModelSettingsText } from "./modelSettingsCopy";

export const MODEL_SECTIONS = [
  { id: "quick", label: "快速配置", icon: Cpu },
  { id: "providers", label: "高级配置", icon: Settings2 },
  { id: "computer", label: "电脑操作", icon: Monitor },
  { id: "images", label: "图片生成", icon: Sparkles },
] as const;
export type ModelSection = typeof MODEL_SECTIONS[number]["id"];

export function ModelSettingsTabs({ section, onChange, id }: { section: ModelSection; onChange: (value: ModelSection) => void; id: string }) {
  const t = useModelSettingsText();
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  function navigate(event: KeyboardEvent<HTMLButtonElement>, index: number): void {
    const last = MODEL_SECTIONS.length - 1;
    const next = event.key === "Home" ? 0 : event.key === "End" ? last : event.key === "ArrowRight" ? (index + 1) % MODEL_SECTIONS.length : event.key === "ArrowLeft" ? (index + last) % MODEL_SECTIONS.length : null;
    if (next === null) return;
    event.preventDefault();
    onChange(MODEL_SECTIONS[next].id);
    refs.current[next]?.focus();
  }
  return <div className="model-settings-tabs" role="tablist" aria-label={t("模型设置分区")}>
    {MODEL_SECTIONS.map(({ id: value, label, icon: Icon }, index) => <button
      type="button" role="tab" key={value} id={`${id}-${value}-tab`}
      aria-selected={section === value} aria-controls={`${id}-${value}-panel`}
      tabIndex={section === value ? 0 : -1} data-section={value}
      ref={(node) => { refs.current[index] = node; }}
      onClick={() => onChange(value)} onKeyDown={(event) => navigate(event, index)}
    ><Icon size={16} aria-hidden="true" />{t(label)}</button>)}
  </div>;
}
