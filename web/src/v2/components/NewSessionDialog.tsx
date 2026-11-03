import { ChevronDown, ChevronUp, Code, FolderOpen, MessageSquare, X } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type JSX, type KeyboardEvent } from "react";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { sessionControlErrorCode } from "../api/sessionControlClient";
import { useCreateSession } from "../api/sessionControlQueries";
import { validateAgentWorkspace } from "../../lib/api";
import { useRuntimeDefaults } from "../api/useSessionRuntime";
import type { OperationResult } from "../types";

type NewSessionDialogProps = {
  identity: IdentityConfig;
  open: boolean;
  defaultCWD?: string;
  onSelectWorkspace?: () => Promise<string | null>;
  onClose: () => void;
  onCreated: (result: OperationResult) => void;
};

const focusableSelector = 'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]):not([disabled])';
type CreateSubmission = { fingerprint: string; idempotencyKey: string };

export function NewSessionDialog({ identity, open, defaultCWD, onSelectWorkspace, onClose, onCreated }: NewSessionDialogProps): JSX.Element | null {
  const { t, language } = useI18n();
  const create = useCreateSession(identity);
  const [title, setTitle] = useState("");
  const [initialText, setInitialText] = useState("");
  const [customInstructionOpen, setCustomInstructionOpen] = useState(false);
  const wasOpenRef = useRef(false);
  const [cwd, setCWD] = useState<string | null>(null);
  const [promptMode, setPromptMode] = useState("code");
  const [validating, setValidating] = useState(false);
  const [selectingWorkspace, setSelectingWorkspace] = useState(false);
  const [workspaceError, setWorkspaceError] = useState("");
  const status = useRuntimeDefaults(identity, open);
  const runtimeDefaults = status.data?.runtime_defaults;
  const workspace = cwd ?? defaultCWD ?? status.data?.workspace ?? "";
  const workspaceName = workspace.split(/[\\/]/).filter(Boolean).at(-1) || workspace || t("webui2.unknownWorkspace");
  const busy = create.isPending || validating || selectingWorkspace;
  const [errorCode, setErrorCode] = useState("");
  const dialogRef = useRef<HTMLElement>(null);
  const titleRef = useRef<HTMLInputElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const submissionRef = useRef<CreateSubmission | null>(null);
  const titleID = useId();

  useEffect(() => {
    if (!open) {
      wasOpenRef.current = false;
      return;
    }
    if (wasOpenRef.current) return;
    wasOpenRef.current = true;
    setCWD(null);
    setWorkspaceError("");
    setCustomInstructionOpen(false);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    titleRef.current?.focus();
    return () => previousFocusRef.current?.focus();
  }, [open]);

  if (!open) return null;

  function close(): void {
    if (busy) return;
    setErrorCode("");
    onClose();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(focusableSelector) ?? []);
    const first = focusable[0];
    const last = focusable.at(-1);
    if (!first || !last) return;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  async function selectWorkspace(): Promise<void> {
    if (!onSelectWorkspace || busy) return;
    setSelectingWorkspace(true);
    setWorkspaceError("");
    try {
      const selected = await onSelectWorkspace();
      if (selected?.trim()) setCWD(selected.trim());
    } catch {
      setWorkspaceError(t("webui2.workspaceSelectFailed"));
    } finally {
      setSelectingWorkspace(false);
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (busy) return;
    const trimmedTitle = title.trim() || t("webui2.defaultSessionTitle");
    setErrorCode("");
    setWorkspaceError("");
    try {
      if (runtimeDefaults?.needs_setup) {
        setErrorCode("runtime_not_configured");
        return;
      }
      let validatedCWD = workspace.trim();
      if (!validatedCWD) {
        setWorkspaceError(t("webui2.workspaceNotSelected"));
        return;
      }
      setValidating(true);
      try {
        const result = await validateAgentWorkspace(identity, validatedCWD);
        if (!result.exists || !result.is_dir) throw new Error("invalid_workspace");
        validatedCWD = result.cwd;
      } catch {
        setWorkspaceError(t("webui2.error.workspace_unavailable"));
        return;
      } finally {
        setValidating(false);
      }
      const trimmedInitialText = initialText.trim();
      const fingerprint = JSON.stringify({ title: trimmedTitle, initialText: trimmedInitialText, cwd: validatedCWD, promptMode });
      const submission = submissionRef.current?.fingerprint === fingerprint ? submissionRef.current : { fingerprint, idempotencyKey: crypto.randomUUID() };
      submissionRef.current = submission;
      const result = await create.mutateAsync({ title: trimmedTitle, ...(trimmedInitialText ? { initialText: trimmedInitialText } : {}), cwd: validatedCWD, promptMode, idempotencyKey: submission.idempotencyKey });
      submissionRef.current = null;
      setTitle("");
      setInitialText("");
      setCWD(null);
      onCreated(result);
    } catch (error) {
      setErrorCode(sessionControlErrorCode(error));
    }
  }

  return <>
    <button aria-label={t("webui2.closeNewSession")} className="webui2-new-session-backdrop" disabled={busy} onClick={close} tabIndex={-1} type="button" />
    <aside aria-labelledby={titleID} aria-modal="true" className="webui2-new-session-dialog" onKeyDown={handleKeyDown} ref={dialogRef} role="dialog">
      <header><h2 id={titleID}>{t("webui2.newSession")}</h2><button aria-label={t("webui2.closeNewSession")} disabled={busy} onClick={close} type="button"><X aria-hidden="true" size={18} /></button></header>
      <form onSubmit={(event) => void submit(event)}>
        <fieldset aria-label={language === "zh" ? "对话模式" : "Conversation mode"} className="webui2-session-mode">
          <button aria-pressed={promptMode === "chat"} disabled={busy} onClick={() => setPromptMode("chat")} type="button"><MessageSquare size={15} />Chat</button>
          <button aria-pressed={promptMode === "code"} disabled={busy} onClick={() => setPromptMode("code")} type="button"><Code size={15} />Code</button>
        </fieldset>
        <label>{t("webui2.sessionTitle")}<input aria-label={t("webui2.sessionTitle")} autoComplete="off" disabled={busy} onChange={(event) => { setErrorCode(""); setTitle(event.target.value); }} placeholder={t("webui2.defaultSessionTitle")} ref={titleRef} value={title} /></label>
        <div className="webui2-workspace-picker">
          <div className="webui2-workspace-picker-heading">
            <span>{t("webui2.workspaceSection")}</span>
            <button aria-label={workspace ? t("webui2.changeWorkspace") : t("webui2.chooseWorkspace")} className="webui2-workspace-picker-action" disabled={busy || !onSelectWorkspace} onClick={() => void selectWorkspace()} type="button">
              <FolderOpen aria-hidden="true" size={15} />
              {workspace ? t("webui2.changeWorkspace") : t("webui2.chooseWorkspace")}
            </button>
          </div>
          <div className="webui2-workspace-picker-copy" title={workspace || undefined}>
            <strong className="webui2-workspace-picker-name">{workspace ? workspaceName : t("webui2.workspaceNotSelected")}</strong>
            {workspace ? <span className="webui2-workspace-picker-location">{workspace}</span> : null}
          </div>
        </div>
        {workspaceError ? <p role="alert">{workspaceError}</p> : null}
        {status.isError ? <p role="alert">{t("webui2.error.network_unavailable")}</p> : null}
        <div className="webui2-custom-instruction">
          <button
            aria-controls={`${titleID}-custom-instruction`}
            aria-expanded={customInstructionOpen}
            className="webui2-custom-instruction-toggle"
            disabled={busy}
            onClick={() => setCustomInstructionOpen((openState) => !openState)}
            type="button"
          >
            {customInstructionOpen ? <ChevronUp aria-hidden="true" size={15} /> : <ChevronDown aria-hidden="true" size={15} />}
            <span>{t("webui2.customInstruction")}</span>
          </button>
          {customInstructionOpen ? <label id={`${titleID}-custom-instruction`}>{t("webui2.customInstruction")}<textarea aria-label={t("webui2.customInstruction")} disabled={busy} onChange={(event) => setInitialText(event.target.value)} rows={3} value={initialText} /></label> : null}
        </div>
        {errorCode ? <p role="alert">{t(`webui2.error.${errorCode}`)}</p> : null}
        <footer><button disabled={busy} onClick={close} type="button">{t("webui2.cancel")}</button><button disabled={busy} type="submit">{busy ? t("webui2.creatingSession") : t("webui2.createSession")}</button></footer>
      </form>
    </aside>
  </>;
}
