import { BrainCircuit, Check, ChevronRight, Clock3, Copy, FileText, Hash, Paperclip, RotateCcw, Terminal } from "lucide-react";
import { useMemo, useState, type JSX } from "react";
import { MarkdownLite, StreamingAssistantContent } from "../../components/agent/markdown";
import { GeneratedArtifactImage } from "../../components/agent/MessageList";
import { formatDuration, formatTokens } from "../../lib/messages";
import { useI18n } from "../../lib/i18n";
import type { SessionMessage, ThinkingMode } from "../types";
import { OperationCard } from "./OperationCard";
import { resolveAgentTaskPermission } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import { messageExperienceCopy } from "./messageExperienceCopy";
import { elapsedMilliseconds } from "./conversationViewModel";
import { messageAttachmentPreview } from "./messageAttachments";
import type { PreparedAttachment } from "../types";
import { UserQuestionCard } from "../../components/agent/UserQuestionCard";
import { ThinkingMessage } from "./ThinkingMessage";

type ConversationMessageProps = {
  identity?: IdentityConfig;
  message: SessionMessage;
  thinkingMode?: ThinkingMode;
  animate?: boolean;
  live?: boolean;
  onDisplayComplete?: () => void;
  onRetry?: (message: SessionMessage) => void;
  retryDisabled?: boolean;
  responseContent?: string;
  elapsedMs?: number;
  now?: number;
};

const foldedMessageLabels: Record<Extract<SessionMessage["kind"], "thinking" | "tool">, string> = {
  thinking: "webui2.message.thinking",
  tool: "webui2.message.tool"
};

export function ConversationMessage({ identity, message, thinkingMode = "summary", animate = false, live = false, onDisplayComplete, onRetry, retryDisabled = false, responseContent, elapsedMs, now }: ConversationMessageProps): JSX.Element | null {
  const { t, language } = useI18n();
  const copy = messageExperienceCopy[language];
  if (message.kind === "operation" && message.operation) return <OperationCard operation={message.operation} />;
  if (message.kind === "handoff") return <HandoffRecord message={message} />;
  if (message.kind === "question" && message.question) return <UserQuestionCard identity={identity} prompt={message.content} question={message.question} />;
  if (message.kind === "error") return <ErrorMessage message={message} onRetry={onRetry} retryDisabled={retryDisabled} />;
  if (message.permission && identity) return <PermissionMessage identity={identity} message={message} />;
  if (message.kind === "thinking") {
    if (thinkingMode === "hidden") return null;
    const stage = message.thinkingStatus === "streaming" ? copy.running : message.thinkingStatus ? copy[message.thinkingStatus] : "";
    const duration = message.stageDurationMs ?? (message.thinkingStatus === "streaming" ? elapsedMilliseconds(message.createdAt, now) : undefined);
    const meta = <><BrainCircuit size={14} /><span className="webui2-thinking-title">{copy.thinking}</span><span className="webui2-thinking-meta">{message.phase ? <span>{copy.phase} {message.phase}</span> : null}<span className="webui2-thinking-state" data-status={message.thinkingStatus}>{stage}</span>{duration !== undefined ? <small className="webui2-thinking-duration" title={copy.stageDuration}>{formatDuration(duration)}</small> : null}<MessageTimestamp value={message.createdAt} /></span></>;
    return <ThinkingMessage content={message.content} meta={meta} full={thinkingMode === "full"} live={message.thinkingStatus === "streaming"} />;
  }
  if (message.kind === "compact") return <FoldedMessage content={message.content} label={copy.compact} summary={<><FileText size={14} /><span className="webui2-stage-label">{copy.compact}</span><MessageTimestamp value={message.createdAt} /></>} className="webui2-compact-message" />;
  if (message.kind === "tool" && message.tool) return <ToolMessage identity={identity} message={message} now={now} />;
  if (message.kind === "tool") {
    return <FoldedMessage content={message.content} label={t(foldedMessageLabels[message.kind])} />;
  }
  return <article className={`webui2-conversation-message webui2-conversation-message--${message.role}`} data-role={message.role}>
    {message.role === "user" ? <><div className="webui2-user-surface"><MarkdownLite content={message.content} /><MessageAttachments message={message} identity={identity} /></div><footer className="webui2-message-footer webui2-message-footer--user"><MessageTimestamp value={message.createdAt} /></footer></> : <>
      <StreamingAssistantContent content={message.content} animate={animate} live={live} final={!live} onComplete={onDisplayComplete} />
      <MessageAttachments message={message} identity={identity} />
      {message.artifacts?.length ? <div className="agent-message-artifacts">{message.artifacts.map((artifact) => <GeneratedArtifactImage key={artifact.asset_id} identity={identity} assetId={artifact.asset_id} />)}</div> : null}
      <footer className="webui2-message-footer">
        <MessageTimestamp value={message.createdAt} />
        {message.provider || message.model ? <span className="webui2-message-model" title={[message.provider, message.model].filter(Boolean).join(" · ")}>{[message.provider, message.model].filter(Boolean).join(" · ")}</span> : null}
        {(elapsedMs ?? message.durationMs) !== undefined ? <span title={copy.duration} className={live ? "webui2-live-elapsed" : undefined}><Clock3 size={12} />{formatDuration(elapsedMs ?? message.durationMs ?? 0)}</span> : null}
        {message.tokens !== undefined ? <span title={`${copy.tokens}: ${message.tokens.toLocaleString()}`}><Hash size={12} />{formatTokens(message.tokens)} {copy.tokenUnit}</span> : null}
        <MessageActions message={message} responseContent={responseContent} onRetry={onRetry} retryDisabled={retryDisabled} />
      </footer>
    </>}
  </article>;
}

function ErrorMessage({ message, onRetry, retryDisabled }: Pick<ConversationMessageProps, "message" | "onRetry" | "retryDisabled">): JSX.Element {
  const { t } = useI18n();
  const code = message.error?.code || (message.content === "budget_exceeded" ? message.content : "");
  const translationKey = `webui2.error.${code}`;
  const translated = code && t(translationKey) !== translationKey ? t(translationKey) : "";
  const showActual = !translated || ![code, "failed", "timeout"].includes(message.content);
  return <article className="webui2-conversation-message webui2-message-error" role="alert">
    <strong>{t("webui2.status.failed")}</strong>
    {message.error?.artifactAvailable ? <p className="webui2-image-reply-failure">{t("webui2.error.image_reply_failed")}</p> : null}
    {translated ? <p>{translated}</p> : null}
    {showActual ? <MarkdownLite content={message.content} /> : null}
    <footer className="webui2-message-footer"><MessageTimestamp value={message.createdAt} /><MessageActions message={message} onRetry={onRetry} retryDisabled={retryDisabled} /></footer>
  </article>;
}

function MessageActions({ message, responseContent, onRetry, retryDisabled }: Pick<ConversationMessageProps, "message" | "responseContent" | "onRetry" | "retryDisabled">): JSX.Element {
  const { language } = useI18n();
  const copy = messageExperienceCopy[language];
  const [state, setState] = useState<"idle" | "copied" | "error">("idle");
  const content = responseContent ?? message.content;
  async function copyResponse() {
    try { await navigator.clipboard.writeText(content); setState("copied"); }
    catch { setState("error"); }
  }
  return <span className="webui2-message-actions">
    {content.trim() ? <button type="button" onClick={() => void copyResponse()} title={state === "copied" ? copy.copied : copy.copy} aria-label={state === "copied" ? copy.copied : copy.copy}>{state === "copied" ? <Check size={14} /> : <Copy size={14} />}</button> : null}
    {onRetry ? <button type="button" onClick={() => onRetry(message)} disabled={retryDisabled} title={copy.retry} aria-label={copy.retry}><RotateCcw size={14} /></button> : null}
    {state === "error" ? <span role="alert">{copy.copyFailed}</span> : null}
  </span>;
}

function MessageAttachments({ message, identity }: { message: SessionMessage; identity?: IdentityConfig }): JSX.Element | null {
  if (!message.attachments?.length) return null;
  return <div className="webui2-message-attachments">{message.attachments.map((attachment, index) => <MessageAttachment key={attachment.attachment_id || `${attachment.name}:${index}`} attachment={attachment} identity={identity} />)}</div>;
}

function MessageAttachment({ attachment, identity }: { attachment: PreparedAttachment; identity?: IdentityConfig }): JSX.Element {
  const { language } = useI18n();
  const preview = useMemo(() => messageAttachmentPreview(attachment, identity), [attachment, identity]);
  const name = attachment.name || messageExperienceCopy[language].attachment;
  return <figure className="webui2-message-attachment" title={attachment.media_type}>
    {preview.assetID ? <GeneratedArtifactImage identity={identity} assetId={preview.assetID} /> : preview.source ? <img src={preview.source} alt={name} loading="lazy" /> : null}
    <figcaption><Paperclip size={13} />{name}</figcaption>
  </figure>;
}

function ToolMessage({ identity, message, now }: { identity?: IdentityConfig; message: SessionMessage; now?: number }): JSX.Element {
  const { language } = useI18n();
  const copy = messageExperienceCopy[language];
  const tool = message.tool!;
  const duration = tool.status === "running" ? elapsedMilliseconds(message.createdAt, now) : tool.durationMs;
  return <div className="webui2-tool-message-container">
    <details className="webui2-folded-record webui2-tool-message">
      <summary><ChevronRight className="webui2-disclosure-chevron" size={13} /><Terminal size={14} /><strong>{tool.name}</strong><code title={tool.command}>{tool.command}</code><span className={`webui2-tool-status webui2-tool-status--${tool.status}`}>{copy[tool.status]}</span>{duration !== undefined ? <small>{formatDuration(duration)}</small> : null}<MessageTimestamp value={message.createdAt} /></summary>
      {tool.input ? <div><small>{copy.input}</small><pre>{tool.input}</pre></div> : null}
      {tool.output ? <div><small>{copy.output}</small><pre>{tool.output}</pre></div> : null}
    </details>
    {tool.computerObservation ? <div className="webui2-tool-observation"><small>{copy.computerObservation}</small><GeneratedArtifactImage identity={identity} assetId={tool.computerObservation.assetID} /></div> : null}
  </div>;
}

export function PermissionMessage({ identity, message }: { identity: IdentityConfig; message: SessionMessage }): JSX.Element {
  const [pending, setPending] = useState(false);
  const [resolved, setResolved] = useState(false);
  const [error, setError] = useState("");
  const { t } = useI18n();
  const permission = message.permission!;
  async function resolve(allowed: boolean) {
    setPending(true); setError("");
    try { await resolveAgentTaskPermission(identity, permission.taskID, permission.requestID, { allowed }); setResolved(true); }
    catch { setError(t("webui2.error.network_unavailable")); }
    finally { setPending(false); }
  }
  return <article className="webui2-conversation-message"><MarkdownLite content={message.content} />{!permission.resolved && !resolved ? <div className="webui2-permission-actions"><button disabled={pending} type="button" onClick={() => void resolve(true)}>{t("webui2.permission.allow")}</button><button disabled={pending} type="button" onClick={() => void resolve(false)}>{t("webui2.permission.deny")}</button></div> : null}{error ? <p role="alert">{error}</p> : null}<footer className="webui2-message-footer"><MessageTimestamp value={message.createdAt} /></footer></article>;
}

function FoldedMessage({ content, label, summary, className = "" }: { content: string; label: string; summary?: import("react").ReactNode; className?: string }): JSX.Element {
  const [open, setOpen] = useState(false);
  return <details className={`webui2-folded-record webui2-folded-message ${className}`} onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary><ChevronRight className="webui2-disclosure-chevron" size={13} />{summary ?? label}</summary>
      {open ? <MarkdownLite content={content} /> : null}
    </details>;
}

function MessageTimestamp({ value }: { value: string }): JSX.Element | null {
  const { language } = useI18n();
  if (!value || !Number.isFinite(Date.parse(value))) return null;
  const time = new Date(value);
  return <time className="webui2-message-time" dateTime={value} title={time.toLocaleString(language)}>{time.toLocaleTimeString(language, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })}</time>;
}

function HandoffRecord({ message }: { message: SessionMessage }): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const handoff = message.handoff;
  const sourceCount = handoff?.sourceRefs.length ?? 0;
  return <details className="webui2-folded-record webui2-handoff-record" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>
      <span>{t("webui2.message.handoff")}</span>
      <span>{t("webui2.handoff.sources")}: {sourceCount}</span>
      <span>{t("webui2.handoff.hash")}: {handoff?.hashPrefix ?? t("webui2.handoff.unavailable")}</span>
      <span>{handoff?.stale ? t("webui2.handoff.stale") : t("webui2.handoff.current")}</span>
      <MessageTimestamp value={message.createdAt} />
    </summary>
    {open && message.content ? <MarkdownLite content={message.content} /> : null}
  </details>;
}
