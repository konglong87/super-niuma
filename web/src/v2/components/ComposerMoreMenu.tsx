import { ImagePlus, Plus } from "lucide-react";
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";

export function ComposerMoreMenu({ disabled, attachLabel, language, onAttach, queueSettings }: { disabled: boolean; attachLabel: string; language: "en" | "zh"; onAttach: () => void; queueSettings?: ReactNode }) {
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menuID = useId();
  const label = language === "zh" ? "添加附件与选项" : "Add attachments and options";

  useEffect(() => {
    if (!open) return;
    container.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();
    const closeOutside = (event: PointerEvent) => { if (!container.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, [open]);

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false);
      trigger.current?.focus();
    } else if (event.key === "Tab") setOpen(false);
    else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const buttons = [...(container.current?.querySelectorAll<HTMLButtonElement>('[role="menu"] button:not(:disabled)') ?? [])];
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      buttons[(index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length]?.focus();
    }
  }

  return <div className="webui2-composer-more" ref={container}>
    <button aria-controls={open && !disabled ? menuID : undefined} aria-expanded={open && !disabled} aria-haspopup="menu" aria-label={label} className="webui2-composer-icon-button" disabled={disabled} onClick={() => setOpen(!open)} ref={trigger} title={label} type="button"><Plus size={20} aria-hidden="true" /></button>
    {open && !disabled ? <div aria-label={label} className="webui2-composer-menu" id={menuID} onKeyDown={handleKeyDown} role="menu">
      <button aria-label={attachLabel} onClick={() => { setOpen(false); onAttach(); }} role="menuitem" type="button"><ImagePlus size={16} aria-hidden="true" /><span>{attachLabel}</span></button>
      {queueSettings}
    </div> : null}
  </div>;
}
