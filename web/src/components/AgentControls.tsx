import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";

export type ControlOption = {
  value: string;
  label: string;
  /** Optional shorter label shown on the trigger button when the full label is long. Falls back to `label`. */
  triggerLabel?: string;
  icon?: ReactNode;
};

type SegmentedControlProps = {
  value: string;
  options: ControlOption[];
  onChange: (value: string) => void;
  ariaLabel: string;
  className?: string;
};

/** Accessible segmented control (radiogroup) for a small, frequently toggled choice. */
export function SegmentedControl({ value, options, onChange, ariaLabel, className }: SegmentedControlProps) {
  return (
    <div className={className ? `agent-segmented ${className}` : "agent-segmented"} role="radiogroup" aria-label={ariaLabel}>
      {options.map((option) => {
        const active = option.value === value;
        return (
          // biome-ignore lint/a11y/useSemanticElements: keep <button role="radio"> — this segmented control renders an icon + label inside each option; a native <input type="radio"> is a void element and cannot contain that content
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={active}
            className={active ? "active" : ""}
            onClick={() => onChange(option.value)}
          >
            {option.icon}
            <span>{option.label}</span>
          </button>
        );
      })}
    </div>
  );
}

type PopoverSelectProps = {
  value: string;
  options: ControlOption[];
  onChange: (value: string) => void;
  ariaLabel: string;
  leading?: ReactNode;
  className?: string;
  /** Which side the menu opens toward. Composer controls live at the bottom, so default is "top". */
  placement?: "top" | "bottom";
};

/** Accessible popover menu (button + listbox) replacing a native <select> for a compact pick-one choice. */
export function PopoverSelect({ value, options, onChange, ariaLabel, leading, className, placement = "top" }: PopoverSelectProps) {
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const listId = useId();
  const current = options.find((option) => option.value === value) ?? options[0];
  const currentIndex = Math.max(0, options.findIndex((option) => option.value === value));

  useEffect(() => {
    if (!open) {
      return;
    }
    setActiveIndex(currentIndex);
    listRef.current?.focus();
  }, [open, currentIndex]);

  useEffect(() => {
    if (!open) {
      return;
    }
    function handlePointerDown(event: PointerEvent) {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [open]);

  function commit(index: number) {
    const option = options[index];
    if (option) {
      onChange(option.value);
    }
    setOpen(false);
    triggerRef.current?.focus();
  }

  function handleTriggerKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp" || event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setOpen(true);
    }
  }

  function handleListKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    switch (event.key) {
      case "Escape":
        event.preventDefault();
        setOpen(false);
        triggerRef.current?.focus();
        break;
      case "ArrowDown":
        event.preventDefault();
        setActiveIndex((index) => Math.min(options.length - 1, index + 1));
        break;
      case "ArrowUp":
        event.preventDefault();
        setActiveIndex((index) => Math.max(0, index - 1));
        break;
      case "Home":
        event.preventDefault();
        setActiveIndex(0);
        break;
      case "End":
        event.preventDefault();
        setActiveIndex(options.length - 1);
        break;
      case "Enter":
      case " ":
        event.preventDefault();
        commit(activeIndex);
        break;
      case "Tab":
        setOpen(false);
        break;
    }
  }

  return (
    <div className={className ? `agent-popover-select ${className}` : "agent-popover-select"} ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="agent-popover-trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        onClick={() => setOpen((value) => !value)}
        onKeyDown={handleTriggerKeyDown}
      >
        {leading}
        <span className="agent-popover-value" title={current?.label}>{current?.triggerLabel ?? current?.label}</span>
        <ChevronDown size={12} className="agent-popover-caret" aria-hidden="true" />
      </button>
      {open ? (
        <div
          ref={listRef}
          id={listId}
          className={`agent-popover-menu placement-${placement}`}
          role="listbox"
          aria-label={ariaLabel}
          aria-activedescendant={`${listId}-option-${activeIndex}`}
          tabIndex={-1}
          onKeyDown={handleListKeyDown}
        >
          {options.map((option, index) => {
            const selected = option.value === value;
            const active = index === activeIndex;
            return (
              // biome-ignore lint/a11y/useFocusableInteractive: intentional aria-activedescendant pattern — the listbox (not each option) holds the single tab stop and roving virtual focus via handleListKeyDown; making every option individually focusable would break that pattern
              // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard activation is already handled by the listbox's onKeyDown (Enter/Space call commit(activeIndex)); see handleListKeyDown above
              <div
                key={option.value}
                id={`${listId}-option-${index}`}
                role="option"
                aria-selected={selected}
                className={`agent-popover-option${active ? " active" : ""}${selected ? " selected" : ""}`}
                onPointerEnter={() => setActiveIndex(index)}
                onClick={() => commit(index)}
              >
                {option.icon}
                <span>{option.label}</span>
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
