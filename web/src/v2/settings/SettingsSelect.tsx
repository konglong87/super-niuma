import { Check, ChevronDown } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";

export type SettingsSelectOption = {
  value: string;
  label: ReactNode;
  disabled?: boolean;
};

type Props = {
  value: string;
  options: readonly SettingsSelectOption[];
  onChange: (value: string) => void;
  ariaLabel?: string;
  id?: string;
  disabled?: boolean;
  className?: string;
};

export function SettingsSelect({ value, options, onChange, ariaLabel, id, disabled = false, className = "" }: Props) {
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listboxID = useId();
  const [open, setOpen] = useState(false);
  const selectedIndex = options.findIndex((option) => option.value === value);
  const firstEnabledIndex = options.findIndex((option) => !option.disabled);
  const [activeIndex, setActiveIndex] = useState(selectedIndex >= 0 ? selectedIndex : firstEnabledIndex);
  const selected = options[selectedIndex] ?? options[firstEnabledIndex];
  const enabledIndexes = useMemo(() => options.flatMap((option, index) => option.disabled ? [] : [index]), [options]);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: PointerEvent): void => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    return () => document.removeEventListener("pointerdown", closeOnOutsidePointer);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    setActiveIndex(selectedIndex >= 0 && !options[selectedIndex]?.disabled ? selectedIndex : firstEnabledIndex);
  }, [firstEnabledIndex, open, selectedIndex]);

  function select(index: number): void {
    const option = options[index];
    if (!option || option.disabled) return;
    onChange(option.value);
    setOpen(false);
    triggerRef.current?.focus();
  }

  function moveActive(direction: 1 | -1): void {
    if (!enabledIndexes.length) return;
    const currentPosition = Math.max(0, enabledIndexes.indexOf(activeIndex));
    const nextPosition = (currentPosition + direction + enabledIndexes.length) % enabledIndexes.length;
    setActiveIndex(enabledIndexes[nextPosition]);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLButtonElement>): void {
    if (disabled) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) setOpen(true);
      moveActive(event.key === "ArrowDown" ? 1 : -1);
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      if (open) select(activeIndex);
      else setOpen(true);
      return;
    }
    if (event.key === "Escape" && open) {
      event.preventDefault();
      setOpen(false);
    }
  }

  return <div className={`webui2-settings-select ${className}`.trim()} ref={rootRef}>
    <button
      aria-controls={open ? listboxID : undefined}
      aria-expanded={open}
      aria-haspopup="listbox"
      aria-label={ariaLabel}
      className="webui2-settings-select-trigger"
      data-value={value}
      disabled={disabled}
      id={id}
      onClick={() => setOpen((current) => !current)}
      onKeyDown={handleKeyDown}
      ref={triggerRef}
      type="button"
    >
      <span className="webui2-settings-select-value">{selected?.label}</span>
      <ChevronDown aria-hidden="true" className="webui2-settings-select-chevron" size={16} />
    </button>
    {open ? <div aria-label={ariaLabel} className="webui2-settings-select-menu" id={listboxID} role="listbox">
      {options.map((option, index) => <button
        aria-selected={option.value === value}
        className={`webui2-settings-select-option${index === activeIndex ? " is-active" : ""}`}
        data-value={option.value}
        disabled={option.disabled}
        key={option.value}
        onClick={() => select(index)}
        onMouseEnter={() => setActiveIndex(index)}
        role="option"
        type="button"
      >
        <span>{option.label}</span>
        {option.value === value ? <Check aria-hidden="true" size={15} /> : null}
      </button>)}
    </div> : null}
  </div>;
}
