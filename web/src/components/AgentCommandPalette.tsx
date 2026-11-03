import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";

export type Command = {
  id: string;
  label: string;
  section: string;
  hint?: string;
  keywords?: string;
  icon?: ReactNode;
  run: () => void;
};

type CommandPaletteProps = {
  open: boolean;
  onClose: () => void;
  commands: Command[];
  placeholder: string;
  emptyLabel: string;
  ariaLabel: string;
};

/** Self-contained ⌘K command palette. No external dependency; keyboard driven. */
export function CommandPalette({ open, onClose, commands, placeholder, emptyLabel, ariaLabel }: CommandPaletteProps) {
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) {
      return commands;
    }
    return commands.filter((command) =>
      `${command.label} ${command.section} ${command.keywords ?? ""} ${command.hint ?? ""}`.toLowerCase().includes(needle)
    );
  }, [commands, query]);

  useEffect(() => {
    if (!open) {
      return;
    }
    setQuery("");
    setActiveIndex(0);
    const raf = requestAnimationFrame(() => inputRef.current?.focus());
    return () => cancelAnimationFrame(raf);
  }, [open]);

  useEffect(() => {
    setActiveIndex(0);
  }, [query]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const active = listRef.current?.querySelector<HTMLElement>(`[data-index="${activeIndex}"]`);
    active?.scrollIntoView({ block: "nearest" });
  }, [activeIndex, open]);

  if (!open) {
    return null;
  }

  function runIndex(index: number) {
    const command = filtered[index];
    if (!command) {
      return;
    }
    onClose();
    command.run();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    switch (event.key) {
      case "Escape":
        event.preventDefault();
        onClose();
        break;
      case "ArrowDown":
        event.preventDefault();
        setActiveIndex((index) => Math.min(filtered.length - 1, index + 1));
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
        setActiveIndex(filtered.length - 1);
        break;
      case "Enter":
        event.preventDefault();
        runIndex(activeIndex);
        break;
    }
  }

  return (
    <div className="agent-cmdk-layer">
      <div className="agent-cmdk-backdrop" aria-hidden="true" onClick={onClose} />
      <div className="agent-cmdk" role="dialog" aria-modal="true" aria-label={ariaLabel} onKeyDown={handleKeyDown}>
        <input
          ref={inputRef}
          className="agent-cmdk-input"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={placeholder}
          aria-label={placeholder}
          role="combobox"
          aria-expanded="true"
          aria-controls="agent-cmdk-list"
          aria-activedescendant={filtered[activeIndex] ? `agent-cmdk-item-${activeIndex}` : undefined}
        />
        <div className="agent-cmdk-list" id="agent-cmdk-list" role="listbox" ref={listRef}>
          {filtered.length === 0 ? <div className="agent-cmdk-empty">{emptyLabel}</div> : null}
          {filtered.map((command, index) => {
            const showSection = index === 0 || filtered[index - 1].section !== command.section;
            return (
              <div key={command.id}>
                {showSection ? <div className="agent-cmdk-section">{command.section}</div> : null}
                <div
                  id={`agent-cmdk-item-${index}`}
                  data-index={index}
                  role="option"
                  tabIndex={0}
                  aria-selected={index === activeIndex}
                  className={`agent-cmdk-item${index === activeIndex ? " active" : ""}`}
                  onPointerEnter={() => setActiveIndex(index)}
                  onClick={() => runIndex(index)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      if (event.key === " ") {
                        event.preventDefault();
                      }
                      runIndex(index);
                    }
                  }}
                >
                  {command.icon ? <span className="agent-cmdk-icon">{command.icon}</span> : null}
                  <span className="agent-cmdk-label">{command.label}</span>
                  {command.hint ? <span className="agent-cmdk-hint">{command.hint}</span> : null}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
