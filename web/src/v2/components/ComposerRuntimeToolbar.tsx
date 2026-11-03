import { Bot, ChevronDown, DatabaseZap, Gauge, Shield, Sparkles } from "lucide-react";
import type { ReactNode } from "react";
import { PopoverSelect, type ControlOption } from "../../components/AgentControls";
import type { ComposerRuntimeControls, ComposerRuntimeValue } from "./composerRuntimeControls";

const PERMISSION_OPTIONS: ControlOption[] = [
  { value: "ask", label: "ask" },
  { value: "auto", label: "auto" },
  { value: "bypass", label: "bypass" }
];
const EFFORT_OPTIONS: ControlOption[] = [
  { value: "low", label: "low" },
  { value: "medium", label: "medium" },
  { value: "high", label: "high" }
];

type Props = { controls: ComposerRuntimeControls; disabled: boolean; language: "en" | "zh"; side: "left" | "right" };

export function ComposerRuntimeToolbar({ controls, disabled, language, side }: Props) {
  const locked = disabled || controls.locked;
  const update = (patch: Partial<ComposerRuntimeValue>) => { if (!locked) controls.onChange({ ...controls.value, ...patch }); };
  const defaultOption = { value: "", label: language === "zh" ? "默认" : "Default" };
  const permissionOptions = controls.value.permissionMode ? PERMISSION_OPTIONS : [defaultOption, ...PERMISSION_OPTIONS];
  const effortOptions = controls.value.effort ? EFFORT_OPTIONS : [defaultOption, ...EFFORT_OPTIONS];
  if (side === "left") return <>
    <RuntimeSelect value={controls.value.permissionMode} options={permissionOptions} onChange={(permissionMode) => update({ permissionMode })} label={language === "zh" ? "权限模式" : "Permission mode"} icon={<Shield size={13} aria-hidden="true" />} locked={locked} className="permission" />
    <span className="webui2-composer-metric" title={language === "zh" ? "上下文占用" : "Context usage"}><Gauge size={13} aria-hidden="true" /><span>{language === "zh" ? "上下文" : "Context"} {percent(controls.contextPercent)}</span></span>
    <span className="webui2-composer-metric" title={language === "zh" ? "缓存命中率" : "Cache hit rate"}><DatabaseZap size={13} aria-hidden="true" /><span>Hit {percent(controls.cacheHitPercent)}</span></span>
  </>;
  return <>
    {controls.providerOptions.length > 0 ? <RuntimeSelect value={controls.value.provider} options={controls.providerOptions} onChange={(provider) => update({ provider })} label={language === "zh" ? "供应商" : "Provider"} icon={<Bot size={13} aria-hidden="true" />} locked={locked} className="provider" /> : null}
    <RuntimeSelect value={controls.value.model} options={controls.modelOptions} onChange={(model) => update({ model })} label={language === "zh" ? "模型" : "Model"} icon={controls.providerOptions.length === 0 ? <Bot size={13} aria-hidden="true" /> : undefined} locked={locked} className="model" />
    <RuntimeSelect value={controls.value.effort} options={effortOptions} onChange={(effort) => update({ effort })} label={language === "zh" ? "思考强度" : "Effort"} icon={<Sparkles size={13} aria-hidden="true" />} locked={locked} className="effort" />
  </>;
}

function RuntimeSelect({ value, options, onChange, label, icon, locked, className }: { value: string; options: ControlOption[]; onChange: (value: string) => void; label: string; icon?: ReactNode; locked: boolean; className: string }) {
  const resolved = options.some((option) => option.value === value) ? options : [{ value, label: value || "-" }, ...options];
  if (locked) {
    const selected = resolved.find((option) => option.value === value);
    return <button aria-label={label} className={`webui2-runtime-locked ${className}`} disabled title={selected?.label} type="button">{icon}<span>{selected?.triggerLabel ?? selected?.label}</span><ChevronDown size={12} aria-hidden="true" /></button>;
  }
  return <PopoverSelect ariaLabel={label} className={`webui2-runtime-select ${className}`} leading={icon} onChange={onChange} options={resolved} value={value} />;
}

function percent(value: number | null): string {
  return value === null || !Number.isFinite(value) ? "-" : `${Math.round(Math.max(0, Math.min(100, value)))}%`;
}
