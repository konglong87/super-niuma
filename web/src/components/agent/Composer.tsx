import { useLayoutEffect, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type RefObject } from "react";
import { ArrowUp, Bot, Ellipsis, Gauge, GripVertical, ImagePlus, ListX, MessageSquarePlus, Pencil, RotateCcw, Shield, Sparkles, Square, Trash2, X } from "lucide-react";
import { PopoverSelect, type ControlOption } from "../AgentControls";
import type { AgentCopy, ComposerState } from "../WebAgentPage";
import type { AgentSlashCommand, IdentityConfig, PendingInputRecord, ProviderOption } from "../../lib/types";
import { MAX_IMAGE_ATTACHMENTS } from "../../lib/imageAttachments";
import { PromptPicker } from "../../v2/components/PromptPicker";

export type SlashSuggestionState = "idle" | "loading" | "ready" | "error";

export type PendingImage = {
  id: string;
  file: File;
  previewURL: string;
};

export function slashCommandDisplayName(command: AgentSlashCommand | undefined): string {
  const name = (command?.name || "").trim();
  if (!name) {
    return "";
  }
  return name.startsWith("/") ? name : `/${name}`;
}

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

const COMPOSER_TEXTAREA_DEFAULT_HEIGHT = 36;
const COMPOSER_TEXTAREA_MAX_HEIGHT = 176;

export function resizeComposerTextarea(textarea: HTMLTextAreaElement | null): void {
  if (!textarea) {
    return;
  }
  textarea.style.height = "auto";
  const nextHeight = Math.min(COMPOSER_TEXTAREA_MAX_HEIGHT, Math.max(COMPOSER_TEXTAREA_DEFAULT_HEIGHT, textarea.scrollHeight));
  textarea.style.height = `${nextHeight}px`;
  textarea.style.overflowY = textarea.scrollHeight > COMPOSER_TEXTAREA_MAX_HEIGHT ? "auto" : "hidden";
}

function SlashSuggestions({
  commands,
  selected,
  state,
  error,
  copy,
  onSelect,
  onApply
}: {
  commands: AgentSlashCommand[];
  selected: number;
  state: SlashSuggestionState;
  error: string;
  copy: AgentCopy;
  onSelect: (index: number) => void;
  onApply: (command: AgentSlashCommand) => void;
}) {
  if (state === "idle") {
    return null;
  }
  if (state === "loading") {
    return <div className="slash-suggestion-panel muted">{copy.slashLoading}</div>;
  }
  if (state === "error") {
    return <div className="slash-suggestion-panel error">{copy.slashError}: {error}</div>;
  }
  if (commands.length === 0) {
    return <div className="slash-suggestion-panel muted">{copy.slashEmpty}</div>;
  }
  return (
    <div className="slash-suggestion-panel" role="listbox" aria-label={copy.slashCommands}>
      <div className="slash-suggestion-hint">{copy.slashHint}</div>
      {commands.map((command, index) => (
        <button
          key={`${command.source || "command"}:${command.name}`}
          className={index === selected ? "active" : ""}
          onMouseEnter={() => onSelect(index)}
          onMouseDown={(event) => {
            event.preventDefault();
            onApply(command);
          }}
          role="option"
          aria-selected={index === selected}
          type="button"
        >
          <strong>{slashCommandDisplayName(command)}</strong>
          <span>{command.description || copy.slashNoDescription}</span>
          {command.source ? <em>{command.source}</em> : null}
        </button>
      ))}
    </div>
  );
}

// NextStepSuggestions 展示 turn 结束后的下一步候选。可见性由父组件的派生条件
// 决定,这里只负责渲染,空数组即不渲染。
function NextStepSuggestions({
  suggestions,
  copy,
  onApply
}: {
  suggestions: string[];
  copy: AgentCopy;
  onApply: (suggestion: string) => void;
}) {
  if (suggestions.length === 0) {
    return null;
  }
  return (
    // biome-ignore lint/a11y/useSemanticElements: keep the suggestion panel as a layout div; its aria-label supplies the group semantics without fieldset UA styles.
    <div className="next-step-panel" role="group" aria-label={copy.nextStepsHint}>
      <div className="next-step-hint" aria-hidden="true">{copy.nextStepsHint}</div>
      {suggestions.map((suggestion) => (
        <button
          key={suggestion}
          onMouseDown={(event) => {
            event.preventDefault();
            onApply(suggestion);
          }}
          onClick={(event) => {
            // event.detail === 0 表示这是键盘（Enter/Space）触发的 click，而非鼠标点击。
            // 鼠标路径已经在 onMouseDown 里 apply 过了，这里再 apply 会重复触发。
            if (event.detail === 0) {
              onApply(suggestion);
            }
          }}
          type="button"
        >
          <Sparkles size={13} />
          <span>{suggestion}</span>
        </button>
      ))}
    </div>
  );
}

function PendingInputPanel({
  items,
  queueEnabled,
  copy,
  onMoveUp,
  onEdit,
  onDirection,
  onDelete,
  onRetry,
  onSideChat,
  onToggleQueue
}: {
  items: PendingInputRecord[];
  queueEnabled: boolean;
  copy: AgentCopy;
  onMoveUp: (item: PendingInputRecord) => void;
  onEdit: (item: PendingInputRecord) => void;
  onDirection: (item: PendingInputRecord) => void;
  onDelete: (item: PendingInputRecord) => void;
  onRetry: (item: PendingInputRecord) => void;
  onSideChat: (item: PendingInputRecord) => void;
  onToggleQueue: () => void;
}) {
  const [menuID, setMenuID] = useState<string | null>(null);
  if (items.length === 0 && queueEnabled) {
    return null;
  }
  return (
    <section className="pending-input-panel" aria-label={copy.pendingInputs}>
      <div className="pending-input-panel-header">
        <span>{copy.pendingInputs}</span>
        <button type="button" className="pending-input-queue-toggle" onClick={onToggleQueue} aria-label={queueEnabled ? copy.closeQueue : copy.openQueue} title={queueEnabled ? copy.closeQueue : copy.openQueue}>
          <ListX size={14} />
        </button>
      </div>
      {!queueEnabled ? <div className="pending-input-queue-disabled">{copy.queueDisabled}</div> : null}
      {items.map((item, index) => (
        <div className={`pending-input-row status-${item.status}`} key={item.id}>
          <span className="pending-input-index" aria-hidden="true"><GripVertical size={14} />{index + 1}</span>
          <span className="pending-input-content" title={item.content}>{item.content}</span>
          <button type="button" className="pending-input-action" onClick={() => onMoveUp(item)} aria-label={copy.movePendingInputUp} title={copy.movePendingInputUp} disabled={index === 0 || item.status !== "queued"}><ArrowUp size={14} /></button>
          <button type="button" className="pending-input-direction" onClick={() => onDirection(item)}>{copy.adjustDirection}</button>
          <button type="button" className="pending-input-action" onClick={() => onDelete(item)} aria-label={copy.deletePendingInput} title={copy.deletePendingInput} disabled={item.status === "running"}><Trash2 size={14} /></button>
          <div className="pending-input-menu-wrap">
            <button type="button" className="pending-input-action" onClick={() => setMenuID((current) => current === item.id ? null : item.id)} aria-label={copy.pendingInputMore} title={copy.pendingInputMore}><Ellipsis size={15} /></button>
            {menuID === item.id ? (
              <div className="pending-input-menu" role="menu">
                <button type="button" onClick={() => { setMenuID(null); onEdit(item); }} role="menuitem"><Pencil size={14} />{copy.editPendingInput}</button>
                <button type="button" onClick={() => { setMenuID(null); onSideChat(item); }} role="menuitem"><MessageSquarePlus size={14} />{copy.openPendingInputSideChat}</button>
                {item.status === "failed" ? <button type="button" onClick={() => { setMenuID(null); onRetry(item); }} role="menuitem"><RotateCcw size={14} />{copy.retryPendingInput}</button> : null}
                <button type="button" onClick={() => { setMenuID(null); onToggleQueue(); }} role="menuitem"><ListX size={14} />{queueEnabled ? copy.closeQueue : copy.openQueue}</button>
              </div>
            ) : null}
          </div>
        </div>
      ))}
    </section>
  );
}

interface ComposerProps {
  identity: IdentityConfig;
  copy: AgentCopy;
  composerRef: RefObject<HTMLTextAreaElement | null>;
  composerText: string;
  onComposerTextChange: (value: string) => void;
  onComposerKeyDown: (event: KeyboardEvent<HTMLTextAreaElement>) => void;
  onComposerPaste: (event: ClipboardEvent<HTMLTextAreaElement>) => void;
  onImageDrop: (event: DragEvent<HTMLDivElement>) => void;
  pendingImages: PendingImage[];
  onAddImages: (files: File[]) => void;
  onRemoveImage: (id: string) => void;
  hasSelectedTask: boolean;
  slashCommands: AgentSlashCommand[];
  slashSelected: number;
  slashState: SlashSuggestionState;
  slashError: string;
  onSlashSelect: (index: number) => void;
  onSlashApply: (command: AgentSlashCommand) => void;
  nextStepSuggestions: string[];
  onApplyNextStep: (suggestion: string) => void;
  pendingInputs: PendingInputRecord[];
  pendingInputQueueEnabled: boolean;
  onMovePendingInputUp: (item: PendingInputRecord) => void;
  onEditPendingInput: (item: PendingInputRecord) => void;
  onDirectionPendingInput: (item: PendingInputRecord) => void;
  onDeletePendingInput: (item: PendingInputRecord) => void;
  onRetryPendingInput: (item: PendingInputRecord) => void;
  onSideChatPendingInput: (item: PendingInputRecord) => void;
  onTogglePendingInputQueue: () => void;
  permissionMode: string;
  onPermissionModeChange: (value: string) => void;
  composerState: ComposerState;
  contextPercent: number;
  cacheHitPercent: number | null;
  providerOptions: ProviderOption[];
  provider: string;
  model: string;
  servedModels: string[];
  onChooseProvider: (value: string) => void;
  onChooseModel: (value: string) => void;
  effort: string;
  onEffortChange: (value: string) => void;
  composerActionIsCancel: boolean;
  sendDisabled: boolean;
  onCancel: () => void;
  onSend: () => void;
}

// 消息输入区：文本框 + slash 建议 + 运行时元信息控件（JSX 原样迁自 WebAgentPage）。
export function Composer({
  identity,
  copy,
  composerRef,
  composerText,
  onComposerTextChange,
  onComposerKeyDown,
  onComposerPaste,
  onImageDrop,
  pendingImages,
  onAddImages,
  onRemoveImage,
  hasSelectedTask,
  slashCommands,
  slashSelected,
  slashState,
  slashError,
  onSlashSelect,
  onSlashApply,
  nextStepSuggestions,
  onApplyNextStep,
  pendingInputs,
  pendingInputQueueEnabled,
  onMovePendingInputUp,
  onEditPendingInput,
  onDirectionPendingInput,
  onDeletePendingInput,
  onRetryPendingInput,
  onSideChatPendingInput,
  onTogglePendingInputQueue,
  permissionMode,
  onPermissionModeChange,
  composerState,
  contextPercent,
  cacheHitPercent,
  providerOptions,
  provider,
  model,
  servedModels,
  onChooseProvider,
  onChooseModel,
  effort,
  onEffortChange,
  composerActionIsCancel,
  sendDisabled,
  onCancel,
  onSend,
}: ComposerProps) {
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  useLayoutEffect(() => {
    resizeComposerTextarea(composerRef.current);
  }, [composerRef, composerText]);

  return (
    <footer className="agent-composer">
      {/* biome-ignore lint/a11y/noStaticElementInteractions: the composer shell is the native drop target for image files. */}
      <div
        className={`agent-composer-shell${pendingImages.length > 0 ? " has-images" : ""}`}
        onDragOver={(event) => event.preventDefault()}
        onDrop={onImageDrop}
      >
        <PendingInputPanel
          items={pendingInputs}
          queueEnabled={pendingInputQueueEnabled}
          copy={copy}
          onMoveUp={onMovePendingInputUp}
          onEdit={onEditPendingInput}
          onDirection={onDirectionPendingInput}
          onDelete={onDeletePendingInput}
          onRetry={onRetryPendingInput}
          onSideChat={onSideChatPendingInput}
          onToggleQueue={onTogglePendingInputQueue}
        />
        <NextStepSuggestions suggestions={nextStepSuggestions} copy={copy} onApply={onApplyNextStep} />
        {pendingImages.length > 0 ? (
          <div className="agent-image-attachments">
            {pendingImages.map((image) => (
              <div className="agent-image-attachment" key={image.id}>
                <img src={image.previewURL} alt={image.file.name || copy.imageAttachment} />
                <span title={image.file.name}>{image.file.name || copy.imageAttachment}</span>
                <button type="button" aria-label={copy.removeImage} title={copy.removeImage} onClick={() => onRemoveImage(image.id)}>
                  <X size={13} />
                </button>
              </div>
            ))}
          </div>
        ) : null}
        <div className="agent-composer-input">
          <textarea
            ref={composerRef}
            aria-label="Message composer"
            value={composerText}
            onChange={(event) => {
              onComposerTextChange(event.target.value);
              resizeComposerTextarea(event.currentTarget);
            }}
            onKeyDown={onComposerKeyDown}
            onPaste={onComposerPaste}
            placeholder={hasSelectedTask ? copy.messagePlaceholder : copy.selectSessionPlaceholder}
          />
          <SlashSuggestions
            commands={slashCommands}
            selected={slashSelected}
            state={slashState}
            error={slashError}
            copy={copy}
            onSelect={onSlashSelect}
            onApply={onSlashApply}
          />
        </div>
        <div className="agent-composer-bottom">
          {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .agent-composer-runtime is a CSS flex layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
          <div className="agent-composer-runtime" role="group" aria-label={copy.composerRuntime}>
            <input
              ref={imageInputRef}
              className="agent-image-input"
              type="file"
              accept="image/*"
              multiple
              onChange={(event) => {
                onAddImages(Array.from(event.currentTarget.files || []).slice(0, MAX_IMAGE_ATTACHMENTS));
                event.currentTarget.value = "";
              }}
            />
            <button
              className="composer-meta-control composer-image-button"
              type="button"
              aria-label={copy.imageAttachment}
              title={copy.imageAttachment}
              onClick={() => imageInputRef.current?.click()}
              disabled={!hasSelectedTask || pendingImages.length >= MAX_IMAGE_ATTACHMENTS}
            >
              <ImagePlus size={14} />
            </button>
            <PromptPicker
              identity={identity}
              disabled={composerState === "sending" || composerState === "cancelling"}
              selectionDisabledReason={hasSelectedTask ? undefined : copy.selectSessionPlaceholder}
              onSelect={(content) => {
                onComposerTextChange(composerText.trim() ? `${composerText}\n\n${content}` : content);
              }}
            />
            <PopoverSelect
              className="composer-meta-control permission"
              value={permissionMode}
              options={PERMISSION_OPTIONS}
              onChange={onPermissionModeChange}
              ariaLabel="Permission mode"
              leading={<Shield size={13} />}
            />
            <span className={`composer-meta-chip composer-state state ${composerState}`}>
              <span className="composer-state-dot" aria-hidden="true" />
              {copy.composerStates[composerState]}
            </span>
            <span className="composer-meta-chip context">
              <Gauge size={13} />
              {copy.context} {contextPercent}%
            </span>
            <span className="composer-meta-chip cache-hit" title={copy.cacheHitTitle}>
              <Gauge size={13} />
              Hit {cacheHitPercent === null ? "-" : `${cacheHitPercent}%`}
            </span>
            {providerOptions.length > 0 ? (
              <PopoverSelect
                className="composer-meta-control provider"
                value={provider}
                options={(providerOptions.some((option) => option.name === provider) || provider === ""
                  ? providerOptions
                  : [{ name: provider, model }, ...providerOptions]
                ).map((option) => ({ value: option.name, label: option.model ? `${option.name} · ${option.model}` : option.name, triggerLabel: option.name }))}
                onChange={onChooseProvider}
                ariaLabel={copy.provider}
                leading={<Bot size={13} />}
              />
            ) : (
              <PopoverSelect
                className="composer-meta-control model"
                value={model}
                options={(servedModels.includes(model) ? servedModels : [model, ...servedModels]).map((option) => ({ value: option, label: option }))}
                onChange={onChooseModel}
                ariaLabel={copy.model}
                leading={<Bot size={13} />}
              />
            )}
            <PopoverSelect
              className="composer-meta-control effort"
              value={effort}
              options={EFFORT_OPTIONS}
              onChange={onEffortChange}
              ariaLabel="Effort"
              leading={<span className="composer-effort-dot" aria-hidden="true" />}
            />
          </div>
          {composerActionIsCancel ? (
            <button className="agent-send-button agent-cancel-button danger" aria-label={composerState === "cancelling" ? copy.cancelling : copy.cancel} title={composerState === "cancelling" ? copy.cancelling : copy.cancel} disabled={composerState === "cancelling"} onClick={onCancel} type="button">
              <Square size={17} />
            </button>
          ) : (
            <button className="agent-send-button" aria-label={composerState === "sending" ? copy.sending : copy.send} title={composerState === "sending" ? copy.sending : copy.send} disabled={sendDisabled} onClick={onSend} type="button">
              <ArrowUp size={19} />
            </button>
          )}
        </div>
      </div>
    </footer>
  );
}
