import { CheckCircle2, Code2, Play, X } from "lucide-react";
import { SegmentedControl } from "../AgentControls";
import type { AgentCopy, PromptMode, WorkspaceValidationState } from "../WebAgentPage";
import type { AgentWorkspaceValidation } from "../../lib/types";

interface NewSessionModalProps {
  copy: AgentCopy;
  workspaceInput: string;
  onWorkspaceInputChange: (value: string) => void;
  workspaceValidationState: WorkspaceValidationState;
  validatedWorkspace: AgentWorkspaceValidation | null;
  workspaceError: string;
  sessionTitle: string;
  onSessionTitleChange: (value: string) => void;
  sessionPrompt: string;
  onSessionPromptChange: (value: string) => void;
  promptMode: PromptMode;
  onPromptModeChange: (next: string) => void;
  onClose: () => void;
  onCreate: () => void;
}

// 新建会话弹窗（JSX 原样迁自 WebAgentPage）。
export function NewSessionModal({
  copy,
  workspaceInput,
  onWorkspaceInputChange,
  workspaceValidationState,
  validatedWorkspace,
  workspaceError,
  sessionTitle,
  onSessionTitleChange,
  sessionPrompt,
  onSessionPromptChange,
  promptMode,
  onPromptModeChange,
  onClose,
  onCreate,
}: NewSessionModalProps) {
  return (
    <div className="agent-modal-layer">
      <button className="agent-modal-backdrop" onClick={onClose} type="button" aria-label="Close new session" />
      <section className="agent-modal" role="dialog" aria-modal="true" aria-labelledby="new-session-title">
        <header>
          <div>
            <span>{copy.newSession}</span>
            <h2 id="new-session-title">{copy.startInWorkspace}</h2>
          </div>
          <button className="agent-icon-button" onClick={onClose} type="button"><X size={16} /></button>
        </header>
        <label>
          {copy.workspaceCwd}
          <div className="modal-input-action workspace-auto-check">
            <input value={workspaceInput} onChange={(event) => onWorkspaceInputChange(event.target.value)} placeholder="/absolute/path/to/project" />
          </div>
        </label>
        {workspaceValidationState === "checking" ? <p className="agent-muted-line">{copy.workspaceChecking}</p> : null}
        {workspaceValidationState === "idle" ? <p className="agent-muted-line">{copy.workspaceEnterAbsolutePath}</p> : null}
        {validatedWorkspace && workspaceValidationState === "ready" ? <p className="agent-success-line"><CheckCircle2 size={14} /> {copy.workspaceReadyState}: {validatedWorkspace.cwd}</p> : null}
        {workspaceError ? <p className="agent-inline-error">{workspaceError}</p> : null}
        <label>
          {copy.title}
          <input value={sessionTitle} onChange={(event) => onSessionTitleChange(event.target.value)} placeholder={copy.optionalTitle} />
        </label>
        <label>
          {copy.firstPrompt}
          <textarea value={sessionPrompt} onChange={(event) => onSessionPromptChange(event.target.value)} rows={4} placeholder={copy.firstPromptPlaceholder} />
        </label>
        <div className="modal-runtime-grid">
          <div className="modal-segment-control">
            <span><Code2 size={14} /> {copy.promptMode}</span>
            <SegmentedControl
              value={promptMode}
              options={[
                { value: "chat", label: copy.promptModes.chat },
                { value: "code", label: copy.promptModes.code }
              ]}
              onChange={onPromptModeChange}
              ariaLabel={copy.newSessionPromptMode}
            />
          </div>
        </div>
        <button className="agent-primary-action" onClick={onCreate} type="button">
          <Play size={15} />
          {copy.createSession}
        </button>
      </section>
    </div>
  );
}
