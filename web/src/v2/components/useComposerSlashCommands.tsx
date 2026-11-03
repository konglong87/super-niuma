import { useEffect, useId, useState, type KeyboardEvent } from "react";
import { slashCommandDisplayName } from "../../components/agent/Composer";
import { listAgentSlashCommands } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import type { AgentSlashCommand, IdentityConfig } from "../../lib/types";

const SLASH_LIMIT = 8;

export function useComposerSlashCommands(identity: IdentityConfig, cwd: string, text: string, disabled: boolean, onApply: (text: string) => void) {
  const { language } = useI18n();
  const listID = useId();
  const [commands, setCommands] = useState<AgentSlashCommand[]>([]);
  const [selected, setSelected] = useState(0);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "ready" | "error">("idle");
  const prefix = /^[ \t]*\/([^\s]*)$/.exec(text)?.[1].toLowerCase() ?? null;
  const active = !disabled && prefix !== null && dismissed !== text;
  const selectedID = active && commands.length > 0 ? `${listID}-${selected}` : undefined;

  useEffect(() => { setDismissed((current) => current === text ? current : null); }, [text]);
  useEffect(() => {
    if (selectedID) document.getElementById(selectedID)?.scrollIntoView?.({ block: "nearest" });
  }, [selectedID]);

  useEffect(() => {
    if (!active || prefix === null) {
      setCommands([]);
      setState("idle");
      return;
    }
    let cancelled = false;
    setCommands([]);
    setState("loading");
    setSelected(0);
    listAgentSlashCommands(identity, cwd, prefix, SLASH_LIMIT).then((result) => {
      if (cancelled) return;
      setCommands(result);
      setState("ready");
    }).catch(() => {
      if (!cancelled) setState("error");
    });
    return () => { cancelled = true; };
  }, [active, prefix, identity, cwd]);

  function apply(command: AgentSlashCommand | undefined): void {
    if (disabled) return;
    const name = slashCommandDisplayName(command);
    if (name) onApply(`${name} `);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>): boolean {
    if (!active) return false;
    if (event.key === "Escape") {
      event.preventDefault();
      setDismissed(text);
      return true;
    }
    if (commands.length === 0 || state !== "ready") return false;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      setSelected((current) => (current + (event.key === "ArrowDown" ? 1 : -1) + commands.length) % commands.length);
      return true;
    }
    const selectedName = slashCommandDisplayName(commands[selected]).slice(1).toLowerCase();
    const plainEnter = event.key === "Enter" && !event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey;
    if (event.key === "Tab" || (plainEnter && (prefix === "" || prefix !== selectedName))) {
      event.preventDefault();
      apply(commands[selected]);
      return true;
    }
    return false;
  }

  const label = language === "zh" ? "斜杠命令" : "Slash commands";
  const status = state === "loading" ? (language === "zh" ? "正在加载命令…" : "Loading commands...")
    : state === "error" ? (language === "zh" ? "命令暂时不可用" : "Commands unavailable")
      : language === "zh" ? "没有匹配的命令" : "No matching commands";
  const panel = active && state !== "idle" ? commands.length > 0 ? <div aria-label={label} className="webui2-slash-commands" id={listID} role="listbox">
    {commands.map((command, index) => <button aria-selected={index === selected} className={index === selected ? "is-selected" : ""} id={`${listID}-${index}`} key={`${command.source}:${command.name}`} onClick={() => apply(command)} onMouseDown={(event) => event.preventDefault()} onMouseEnter={() => setSelected(index)} role="option" type="button"><strong>{slashCommandDisplayName(command)}</strong><span>{command.description}</span></button>)}
  </div> : <p aria-live="polite" className="webui2-slash-status">{status}</p> : null;
  return { panel, handleKeyDown, dismiss: () => setDismissed(text), listID: active && commands.length > 0 ? listID : undefined, selectedID };
}
