import type { RefObject } from "react";
import { Bot, Check, ChevronDown, Clock3, Copy, Download, Hash, History, Image, RotateCcw, Sparkles, Terminal, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { MarkdownLite, StreamingAssistantContent } from "./markdown";
import type { AgentCopy } from "./copy";
import type { StreamState } from "../../hooks/useAgentTaskStream";
import { formatDuration, formatTokens } from "../../lib/messages";
import type { ConversationMessage, MessageToolRun, ThinkingMessageStatus, ThinkingMode, ThinkingState } from "../WebAgentPage";
import { getImageArtifact } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import { UserQuestionCard } from "./UserQuestionCard";

export function GeneratedArtifactImage({ identity, assetId }: { identity?: IdentityConfig; assetId: string }) {
  const [src, setSrc] = useState("");
  const [loadState, setLoadState] = useState<"loading" | "ready" | "error">("loading");
  const [previewOpen, setPreviewOpen] = useState(false);
  const imageButtonRef = useRef<HTMLButtonElement>(null);
  const previewRef = useRef<HTMLDialogElement>(null);
  const closePreviewRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!identity || !assetId || typeof getImageArtifact !== "function") return;
    let active = true;
    let objectURL = "";
    setLoadState("loading");
    setSrc("");
    void getImageArtifact(identity, assetId).then((blob) => {
      if (!active) return;
      objectURL = URL.createObjectURL(blob);
      setSrc(objectURL);
      setLoadState("ready");
    }).catch(() => {
      if (active) setLoadState("error");
    });
    return () => {
      active = false;
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [assetId, identity]);
  useEffect(() => {
    const preview = previewRef.current;
    if (!previewOpen || loadState !== "ready" || !preview) return;
    // Native modal focus containment also prevents background keyboard actions.
    preview.showModal();
    closePreviewRef.current?.focus();
    const trigger = imageButtonRef.current;
    return () => {
      preview.close();
      if (trigger?.isConnected) trigger.focus();
    };
  }, [previewOpen, loadState]);
  if (loadState === "loading") {
    return <div className="agent-generated-image-status" role="status">图片加载中...</div>;
  }
  if (loadState === "error") {
    return <div className="agent-generated-image-status error" role="alert">图片加载失败</div>;
  }
  if (!src) return null;
  return (
    <>
      <div className="agent-generated-image-wrap">
        <button className="agent-generated-image-button" type="button" onClick={() => setPreviewOpen(true)} aria-label="Open generated asset" title="Open generated asset" ref={imageButtonRef}>
          <img className="agent-generated-image" src={src} alt="Generated asset" />
        </button>
        <a className="agent-generated-image-download agent-icon-button" href={src} download={`${assetId}.png`} title="Download generated asset" aria-label="Download generated asset"><Download size={15} /></a>
      </div>
      {previewOpen && typeof document !== "undefined" ? createPortal(
        <dialog className="agent-image-lightbox" aria-modal="true" aria-label="Generated asset preview" ref={previewRef} onCancel={(event) => { event.preventDefault(); setPreviewOpen(false); }} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setPreviewOpen(false); } }}>
          <button className="agent-image-lightbox-backdrop" type="button" aria-label="Close image preview" tabIndex={-1} onClick={() => setPreviewOpen(false)} />
          <div className="agent-image-lightbox-content">
            <div className="agent-image-lightbox-actions">
              <a className="agent-icon-button" href={src} download={`${assetId}.png`} title="Download generated asset" aria-label="Download generated asset"><Download size={17} /></a>
              <button className="agent-icon-button" type="button" onClick={() => setPreviewOpen(false)} title="Close preview" aria-label="Close preview" ref={closePreviewRef}><X size={17} /></button>
            </div>
            <img src={src} alt="Generated asset preview" />
          </div>
        </dialog>,
        document.body
      ) : null}
    </>
  );
}

function MessageToolRuns({ tools, copy, live = false }: { tools: MessageToolRun[]; copy: AgentCopy; live?: boolean }) {
  if (tools.length === 0) {
    return null;
  }
  const running = tools.find((run) => run.status === "running");
  const hasError = tools.some((run) => run.status === "error");
  return (
    <details className={`agent-msg-tools${live ? " live" : ""}`} open={live && running ? true : undefined}>
      <summary title={copy.commandDetails}>
        <Terminal size={13} />
        {running ? (
          <>
            <span className="agent-msg-tools-spinner" aria-hidden="true" />
            <span className="agent-msg-tools-label">{copy.runningCommand}</span>
            <code className="agent-msg-tools-current">{running.command || running.name}</code>
          </>
        ) : (
          <span className="agent-msg-tools-label">{copy.ranCommands.replace("{n}", String(tools.length))}</span>
        )}
        {hasError ? <span className="agent-status-dot fail" aria-hidden="true" /> : null}
        <ChevronDown size={12} className="agent-msg-tools-caret" aria-hidden="true" />
      </summary>
      <div className="agent-msg-tools-list">
        {tools.map((run) => (
          <div key={run.id} className="agent-msg-tool-item">
            <div className={`agent-msg-tool-row ${run.status}`}>
              <span className={`agent-status-dot ${run.status === "error" ? "fail" : run.status === "running" ? "warn" : "ok"}`} />
              <span className="agent-msg-tool-name">{run.name}</span>
              <code className="agent-msg-tool-command" title={run.command || undefined}>{run.command || "-"}</code>
              <span className="agent-msg-tool-elapsed">{run.status === "running" ? copy.toolStatus.running : run.elapsed}</span>
            </div>
            {run.output ? <pre className="agent-msg-tool-output">{run.output}</pre> : null}
          </div>
        ))}
      </div>
    </details>
  );
}

function ThinkingPlaceholder({ copy, state }: { copy: AgentCopy; state: ThinkingState }) {
  return (
    <article className="agent-message assistant thinking">
      <div className="agent-thinking-bubble">
        <span className="agent-thinking-dot" aria-hidden="true" />
        <strong>{state.label}</strong>
        <small>{state.detail}</small>
        <span className="agent-thinking-pulse" aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
      </div>
      <small className="agent-thinking-meta">
        {state.meta.length > 0 ? state.meta.map((item, index) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: thinking-meta chips are transient status text with no identity beyond their position
          <span key={`${item}-${index}`}>{item}</span>
        )) : copy.agentWorking}
      </small>
    </article>
  );
}

function ThinkingMessage({ copy, message, mode, expanded, onToggle }: { copy: AgentCopy; message: ConversationMessage; mode: ThinkingMode; expanded: boolean; onToggle: (open: boolean) => void }) {
  if (mode === "summary") {
    return <CollapsibleThinkingMessage copy={copy} message={message} expanded={expanded} onToggle={onToggle} />;
  }
  return (
    <article className="agent-message assistant thinking">
      <div className="agent-thinking-full">
        <div className="agent-thinking-full-head">
          <Sparkles size={13} aria-hidden="true" />
          <strong>{copy.thinkingSummary}</strong>
          <span>{thinkingStatusLabel(copy, message.thinkingStatus)}</span>
        </div>
        <div className="agent-thinking-content"><MarkdownLite content={message.content} /></div>
      </div>
      <small className="agent-message-foot">{message.meta}</small>
    </article>
  );
}

function CollapsibleThinkingMessage({ copy, message, expanded, onToggle }: { copy: AgentCopy; message: ConversationMessage; expanded: boolean; onToggle: (open: boolean) => void }) {
  const anchor = [
    message.turn ? copy.thinkingTurn.replace("{n}", String(message.turn)) : "",
    message.phase ? copy.thinkingPhase.replace("{n}", String(message.phase)) : ""
  ].filter(Boolean).join(" · ");
  const metrics = [
    message.durationMs ? formatDuration(message.durationMs) : "",
    message.lineCount ? copy.thinkingLines.replace("{n}", String(message.lineCount)) : "",
    copy.thinkingTokensUnavailable
  ].filter(Boolean).join(" · ");
  const status = thinkingStatusLabel(copy, message.thinkingStatus);
  return (
    <article className="agent-message assistant thinking">
      <details className="agent-thinking-details" open={expanded} onToggle={(event) => onToggle(event.currentTarget.open)}>
        {/* biome-ignore lint/a11y/noStaticElementInteractions: summary is a native interactive element, and the handler keeps controlled React state deterministic. */}
        <summary
          onClick={(event) => {
            // Keep the controlled open state as the single source of truth;
            // prevent the native details toggle from racing React state.
            event.preventDefault();
            event.stopPropagation();
            onToggle(!expanded);
          }}
        >
          <Sparkles size={13} aria-hidden="true" />
          <strong>{copy.thinkingSummary}</strong>
          <span>{[status, anchor, message.meta, metrics].filter(Boolean).join(" · ")}</span>
          <span className="agent-thinking-action expand">{copy.expandThinking}</span>
          <span className="agent-thinking-action collapse">{copy.collapseThinking}</span>
          <ChevronDown size={12} className="agent-thinking-caret" aria-hidden="true" />
        </summary>
        <div className="agent-thinking-content"><MarkdownLite content={message.content} /></div>
      </details>
    </article>
  );
}

function thinkingStatusLabel(copy: AgentCopy, status: ThinkingMessageStatus | undefined) {
  if (status === "failed") {
    return copy.thinkingFailed;
  }
  if (status === "cancelled") {
    return copy.thinkingCancelled;
  }
  if (status === "timed_out") {
    return copy.thinkingTimedOut;
  }
  if (status === "completed") {
    return copy.thinkingCompleted;
  }
  return copy.thinkingStates.thinking;
}

function EnvironmentState({
  copy,
  tenantStorageMissing,
  loadError,
  onRestoreLocalTestIdentity
}: {
  copy: AgentCopy;
  tenantStorageMissing: boolean;
  loadError: string;
  onRestoreLocalTestIdentity?: () => void;
}) {
  return (
    <div className={tenantStorageMissing ? "agent-environment-state" : "agent-error-state"}>
      <div>
        <strong>{tenantStorageMissing ? copy.storageMissingTitle : copy.apiErrorTitle}</strong>
        <span>{tenantStorageMissing ? copy.storageMissingBody : loadError}</span>
      </div>
      {tenantStorageMissing ? (
        <code>NIUMA_MYSQL_DSN=... go run ./cmd/niuma server --host 127.0.0.1 --port 18082 --auth-token test-token</code>
      ) : null}
      {onRestoreLocalTestIdentity ? (
        <button type="button" onClick={onRestoreLocalTestIdentity}>{copy.restoreLocalTestIdentity}</button>
      ) : null}
    </div>
  );
}

interface MessageListProps {
  identity?: IdentityConfig;
  copy: AgentCopy;
  conversationScrollRef: RefObject<HTMLDivElement | null>;
  onConversationScroll: () => void;
  loadError: string;
  tenantStorageMissing: boolean;
  onRestoreLocalTestIdentity?: () => void;
  loading: boolean;
  hasTasks: boolean;
  onNewSession: () => void;
  hasSelectedTask: boolean;
  messagesEmpty: boolean;
  showThinkingPlaceholder: boolean;
  onApplySuggestion: (suggestion: string) => void;
  hiddenMessageCount: number;
  onShowEarlierMessages: () => void;
  visibleMessages: ConversationMessage[];
  thinkingMode: ThinkingMode;
  thinkingSessionKey: string;
  expandedThinkingKeys: Readonly<Record<string, boolean>>;
  onThinkingToggle: (message: ConversationMessage, open: boolean) => void;
  streamState: StreamState;
  selectedTaskId: number | null;
  typewriterTaskIDs: Set<number>;
  isFinalResponse: (message: ConversationMessage) => boolean;
  latestAssistantMessageId: string | undefined;
  fallbackReplyDurationMs?: number;
  setLatestMessageNode: (node: HTMLElement | null) => void;
  onTypewriterDone: (taskID: number) => void;
  copiedMessageId: string | null;
  onCopyMessage: (message: ConversationMessage) => void;
  onRetryMessage: (messageId: string) => void;
  retryDisabled: (messageId: string) => boolean;
  liveToolRuns: MessageToolRun[];
  thinkingState: ThinkingState;
}

// 会话消息区：消息流 + 空态 + 思考占位（JSX 原样迁自 WebAgentPage）。
export function MessageList({
  identity,
  copy,
  conversationScrollRef,
  onConversationScroll,
  loadError,
  tenantStorageMissing,
  onRestoreLocalTestIdentity,
  loading,
  hasTasks,
  onNewSession,
  hasSelectedTask,
  messagesEmpty,
  showThinkingPlaceholder,
  onApplySuggestion,
  hiddenMessageCount,
  onShowEarlierMessages,
  visibleMessages,
  thinkingMode,
  thinkingSessionKey = "none",
  expandedThinkingKeys = {},
  onThinkingToggle = () => undefined,
  streamState,
  selectedTaskId,
  typewriterTaskIDs,
  isFinalResponse,
  latestAssistantMessageId,
  fallbackReplyDurationMs = 0,
  setLatestMessageNode,
  onTypewriterDone,
  copiedMessageId,
  onCopyMessage,
  onRetryMessage,
  retryDisabled,
  liveToolRuns,
  thinkingState,
}: MessageListProps) {
  return (
    <div className="agent-conversation-scroll" data-testid="agent-conversation-scroll" ref={conversationScrollRef} onScroll={onConversationScroll}>
      {loadError ? (
        <EnvironmentState
          copy={copy}
          tenantStorageMissing={tenantStorageMissing}
          loadError={loadError}
          onRestoreLocalTestIdentity={onRestoreLocalTestIdentity}
        />
      ) : null}
      {!loadError && !loading && !hasTasks ? (
        <div className="agent-empty-state">
          <Bot size={28} />
          <strong>{copy.noSessionsTitle}</strong>
          <span>{copy.noSessionsBody}</span>
          <button onClick={onNewSession} type="button">{copy.newSession}</button>
        </div>
      ) : null}
      {hasSelectedTask ? (
        <>
          {messagesEmpty && !showThinkingPlaceholder ? (
            <div className="agent-blank-session">
              <Bot size={24} />
              <strong>{copy.blankSessionTitle}</strong>
              <span>{copy.blankSessionBody}</span>
              <div className="agent-blank-suggestions">
                {copy.blankSuggestions.map((suggestion) => (
                  <button key={suggestion} className="agent-blank-suggestion" type="button" onClick={() => onApplySuggestion(suggestion)}>
                    <Sparkles size={13} />
                    <span>{suggestion}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : null}
          {hiddenMessageCount > 0 ? (
            <button className="agent-show-earlier" type="button" onClick={onShowEarlierMessages}>
              <History size={13} />
              {copy.showEarlierMessages.replace("{n}", String(hiddenMessageCount))}
            </button>
          ) : null}
          {visibleMessages.map((message, index) => {
            if (message.kind === "thinking") {
              const thinkingKey = `${thinkingSessionKey}:${message.turn || 0}:${message.phase || message.id}`;
              return <ThinkingMessage key={message.id} copy={copy} message={message} mode={thinkingMode} expanded={expandedThinkingKeys[thinkingKey] === true} onToggle={(open) => onThinkingToggle(message, open)} />;
            }
            if (message.kind === "question" && message.question) {
              return <UserQuestionCard identity={identity} key={message.id} prompt={message.content} question={message.question} />;
            }
            const isLatest = index === visibleMessages.length - 1 && !showThinkingPlaceholder;
            const isStreamingAssistant = message.role === "assistant" && streamState === "live" && isLatest;
            const shouldAnimateAssistant = message.role === "assistant" && isLatest && message.taskID === selectedTaskId && typewriterTaskIDs.has(message.taskID);
            const isFinalAssistant = shouldAnimateAssistant && isFinalResponse(message);
            const measuredDurationMs = message.durationMs && message.durationMs >= 1000 ? message.durationMs : 0;
            const replyDurationMs = measuredDurationMs || (message.role === "assistant" && message.content.trim() ? fallbackReplyDurationMs : 0);
            return (
              <article
                key={message.id}
                className={`agent-message ${message.role} ${message.status ? `status-${message.status}` : ""} ${message.role === "assistant" ? "typewriter" : ""} ${isStreamingAssistant ? "live" : ""}`}
                ref={message.id === latestAssistantMessageId ? setLatestMessageNode : undefined}
              >
                <div className="agent-message-bubble">
                  <span>{message.role === "user" ? copy.user : message.role === "system" ? copy.system : copy.assistant}</span>
                  {message.role === "assistant" && message.tools ? (
                    <MessageToolRuns tools={message.tools} copy={copy} live={isStreamingAssistant} />
                  ) : null}
                  {message.role === "assistant" ? (
                    <StreamingAssistantContent
                      content={message.content}
                      animate={shouldAnimateAssistant}
                      live={isStreamingAssistant}
                      final={isFinalAssistant}
                      onComplete={() => {
                        const taskID = message.taskID;
                        if (taskID) {
                          onTypewriterDone(taskID);
                        }
                      }}
                    />
                  ) : (
                    <MarkdownLite content={message.content} />
                  )}
                  {message.attachments?.length ? (
                    <div className="agent-message-attachments">
                      {message.attachments.map((attachment) => (
                        <span key={attachment.attachment_id || `${attachment.name}:${attachment.size_bytes}`}>
                          <Image size={13} />
                          {attachment.name || copy.imageAttachment}
                        </span>
                      ))}
                    </div>
                  ) : null}
                  {message.artifacts?.length ? <div className="agent-message-artifacts">{message.artifacts.map((artifact) => <GeneratedArtifactImage key={artifact.asset_id} identity={identity} assetId={artifact.asset_id} />)}</div> : null}
                </div>
                <small className="agent-message-foot">
                  {message.meta}
                  {message.role === "assistant" && replyDurationMs ? (
                    <span className="agent-message-duration" title={copy.replyDuration}>
                      {"· "}
                      <Clock3 size={11} />
                      {formatDuration(replyDurationMs)}
                    </span>
                  ) : null}
                  {message.role === "assistant" && (message.provider || message.model) ? (
                    <span className="agent-message-model" title={[message.provider, message.model].filter(Boolean).join(" · ")}>
                      {"· "}
                      <Bot size={11} />
                      <span className="agent-message-model-text">{[message.provider, message.model].filter(Boolean).join(" · ")}</span>
                    </span>
                  ) : null}
                  {message.role === "assistant" && message.tokens ? (
                    <span className="agent-message-tokens" title={`${copy.tokensUsed}: ${message.tokens.toLocaleString()}`}>
                      {"· "}
                      <Hash size={11} />
                      {formatTokens(message.tokens)} tokens
                    </span>
                  ) : null}
                  {message.role === "assistant" && message.content.trim() ? (
                    <span className="agent-message-actions">
                      <button
                        type="button"
                        className="agent-message-action"
                        onClick={() => onCopyMessage(message)}
                        title={copiedMessageId === message.id ? copy.copied : copy.copyMessage}
                        aria-label={copiedMessageId === message.id ? copy.copied : copy.copyMessage}
                      >
                        {copiedMessageId === message.id ? <Check size={12} /> : <Copy size={12} />}
                      </button>
                      <button
                        type="button"
                        className="agent-message-action"
                        onClick={() => onRetryMessage(message.id)}
                        disabled={retryDisabled(message.id)}
                        title={copy.retryMessage}
                        aria-label={copy.retryMessage}
                      >
                        <RotateCcw size={12} />
                      </button>
                    </span>
                  ) : null}
                </small>
              </article>
            );
          })}
          {showThinkingPlaceholder ? (
            <>
              {liveToolRuns.length > 0 ? (
                <div className="agent-live-tools">
                  <MessageToolRuns tools={liveToolRuns} copy={copy} live />
                </div>
              ) : null}
              <ThinkingPlaceholder copy={copy} state={thinkingState} />
            </>
          ) : null}
        </>
      ) : loading ? <div className="agent-empty-mini">{copy.loadingSessions}</div> : null}
    </div>
  );
}
