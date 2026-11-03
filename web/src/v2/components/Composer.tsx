import { ArrowUp, Sparkles, Square, X } from "lucide-react";
import { type ChangeEvent, type ClipboardEvent, type DragEvent, type JSX, type KeyboardEvent, type ReactNode, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { resizeComposerTextarea } from "../../components/agent/Composer";
import { ApiError, presignMobileAttachment, uploadAttachmentBinary } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { dataURLPayload, fileToDataURL, imageFilesFromClipboard, selectImageFiles, sha256File } from "../../lib/imageAttachments";
import type { IdentityConfig } from "../../lib/types";
import { sessionControlErrorCode } from "../api/sessionControlClient";
import { parseWebUIV2Route } from "../routes";
import type { ContextChip, OperationResult, PreparedAttachment, SendSessionInput, SessionDetail, SessionRef, SessionStatus, SessionSummary } from "../types";
import { ComposerImagePreview } from "./ComposerImagePreview";
import { ComposerMoreMenu } from "./ComposerMoreMenu";
import { ComposerRuntimeToolbar } from "./ComposerRuntimeToolbar";
import { clearComposerDraft, composerDraftMemoryKey, readComposerDraft, writeComposerDraft } from "./composerDraftStorage";
import type { ComposerRuntimeControls, ComposerRuntimeValue } from "./composerRuntimeControls";
import { PromptPicker } from "./PromptPicker";
import { SkillPicker } from "./SkillPicker";
import { SESSION_REF_MIME_TYPE, supportsComposerDrop } from "./sessionContextDrag";
import { useComposerSlashCommands } from "./useComposerSlashCommands";
import "./composerExperience.css";

const EMPTY_FILES: File[] = [];
const PRESIGN_UNAVAILABLE_STATUSES = new Set([404, 501, 503]);

type ComposerProps = {
  drafts?: Map<SessionRef, ComposerDraft>;
  identity: IdentityConfig;
  targetRef: SessionRef;
  sessionStatus: SessionStatus;
  availableSources: SessionSummary[];
  attachmentPreparer?: AttachmentPreparer;
  cwd?: string;
  queuePanel?: ReactNode;
  queueSettings?: ReactNode;
  runtimeControls?: ComposerRuntimeControls;
  nextStepSuggestions?: string[];
  disabled?: boolean;
  onSend: (input: SendSessionInput) => Promise<OperationResult>;
  onCompact?: () => Promise<OperationResult>;
  onStopRequested: () => Promise<void> | void;
  onSent: (session: SessionDetail) => void;
  promptPicker?: ReactNode;
};
export type ComposerDraft = { text: string; files: File[]; chips: ContextChip[]; skillName?: string; scope?: string };
export type { ComposerRuntimeControls, ComposerRuntimeValue } from "./composerRuntimeControls";

type PrepareSendInputOptions = {
  identity: IdentityConfig;
  ref: SessionRef;
  text: string;
  files: File[];
  sourceRefs: SessionRef[];
  idempotencyKey: string;
  signal: AbortSignal;
  attachmentPreparer?: AttachmentPreparer;
  runtimeValue?: ComposerRuntimeValue;
};

export type AttachmentPreparer = (identity: IdentityConfig, file: File, signal: AbortSignal) => Promise<PreparedAttachment>;

type SubmissionCache = { fingerprint: string; idempotencyKey: string; input?: SendSessionInput };

export async function prepareSendInput({ identity, ref, text, files, sourceRefs, idempotencyKey, signal, attachmentPreparer = prepareAttachment, runtimeValue }: PrepareSendInputOptions): Promise<SendSessionInput> {
  const attachments: PreparedAttachment[] = [];
  for (const file of selectImageFiles(files)) {
    attachments.push(await attachmentPreparer(identity, file, signal));
  }
  return {
    ...runtimeValue,
    ref,
    text: text.trim(),
    attachments,
    sourceRefs: uniqueRefs(sourceRefs),
    idempotencyKey
  };
}

export function Composer(props: ComposerProps): JSX.Element {
  const scope = composerDraftMemoryKey({ identity: props.identity, targetRef: props.targetRef, cwd: props.cwd ?? "" });
  // Identity/workspace changes must never reuse a mounted private draft or upload.
  return <ScopedComposer key={scope} {...props} />;
}

function ScopedComposer({ drafts, identity, targetRef, sessionStatus, availableSources, attachmentPreparer, cwd = "", queuePanel, queueSettings, runtimeControls, nextStepSuggestions = [], disabled = false, onSend, onCompact, onStopRequested, onSent, promptPicker }: ComposerProps): JSX.Element {
  const { t, language } = useI18n();
  const fileInput = useRef<HTMLInputElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const composingRef = useRef(false);
  const contextWaitID = useId();
  const [isDragOver, setIsDragOver] = useState(false);
  const sendAbortRef = useRef<AbortController | null>(null);
  const submissionRef = useRef<SubmissionCache | null>(null);
  const mountedRef = useRef(true);
  const [initialDraft] = useState(() => {
    const scope = composerDraftMemoryKey({ identity, targetRef, cwd });
    const memoryDraft = drafts?.get(targetRef);
    if (memoryDraft?.scope === scope) return memoryDraft;
    const stored = readComposerDraft({ identity, targetRef, cwd });
    return { text: stored?.text ?? "", files: EMPTY_FILES, chips: stored?.sourceRefs.map((ref) => sourceChip(ref, availableSources)) ?? [], ...(stored?.skillName ? { skillName: stored.skillName } : {}) };
  });
  const [text, setText] = useState(initialDraft.text);
  const [files, setFiles] = useState<File[]>(initialDraft.files);
  const [chips, setChips] = useState<ContextChip[]>(initialDraft.chips);
  const [skillName, setSkillName] = useState(initialDraft.skillName ?? "");
  useEffect(() => {
    const scope = { identity, targetRef, cwd };
    drafts?.set(targetRef, { text, files, chips, skillName: skillName || undefined, scope: composerDraftMemoryKey(scope) });
    writeComposerDraft(scope, { text, sourceRefs: chips.map((chip) => chip.sourceRef), skillName });
  }, [drafts, identity, targetRef, cwd, text, files, chips, skillName]);
  const [isSending, setIsSending] = useState(false);
  const [isPreparingAttachments, setIsPreparingAttachments] = useState(false);
  const [isStopping, setIsStopping] = useState(false);
  const [errorCode, setErrorCode] = useState("");
  const [uploadStatus, setUploadStatus] = useState("");
  const readOnly = targetRef.startsWith("local:");
  const isRunning = sessionStatus === "running" || sessionStatus === "queued" || sessionStatus === "waiting_input" || sessionStatus === "waiting_permission";
  const inputDisabled = readOnly || disabled || sessionStatus === "waiting_input" || isSending || isStopping;
  const showStop = !readOnly && isRunning;
  const canStop = showStop && !disabled;
  const contextBlocked = chips.length > 0 && (isRunning || sessionStatus === "blocked");
  const contextWaitReason = language === "zh" ? "当前会话忙碌中，包含会话上下文的草稿需等待空闲后发送。草稿已保留。" : "This session is busy. Send this context draft once it is idle. Your draft is preserved.";
  const hasContent = text.trim() !== "" || files.length > 0;
  const sendLabel = t("webui2.sendMessage");
  const slash = useComposerSlashCommands(identity, cwd, text, inputDisabled, changeText);

  useLayoutEffect(() => {
    if (textareaRef.current?.value === text) resizeComposerTextarea(textareaRef.current);
  }, [text]);
  useEffect(() => { if (inputDisabled) setIsDragOver(false); }, [inputDisabled]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      sendAbortRef.current?.abort();
    };
  }, []);

  function invalidateSubmission(): void {
    submissionRef.current = null;
  }

  function changeText(value: string): void {
    if (inputDisabled) return;
    invalidateSubmission();
    setText(value);
    textareaRef.current?.focus();
  }

  function changeSkill(value: string): void {
    if (inputDisabled) return;
    invalidateSubmission();
    setSkillName(value.trim());
  }

  function addSourceRef(value: string): void {
    if (inputDisabled) return;
    const route = parseWebUIV2Route(`/webui/v2/sessions/${encodeURIComponent(value)}`);
    if (route.kind !== "session" || route.ref === targetRef || chips.some((item) => item.sourceRef === route.ref)) return;
    const chip = sourceChip(route.ref, availableSources);
    invalidateSubmission();
    setChips((current) => current.some((item) => item.sourceRef === chip.sourceRef) ? current : [...current, chip]);
  }

  function handleDrop(event: DragEvent<HTMLElement>): void {
    setIsDragOver(false);
    if (inputDisabled) return;
    const sourceRef = event.dataTransfer.getData(SESSION_REF_MIME_TYPE);
    const imageFiles = selectImageFiles(Array.from(event.dataTransfer.files ?? EMPTY_FILES));
    if (!sourceRef && imageFiles.length === 0) return;
    event.preventDefault();
    addSourceRef(sourceRef);
    addFiles(imageFiles);
  }

  function handleDragOver(event: DragEvent<HTMLElement>): void {
    if (inputDisabled || !supportsComposerDrop(event.dataTransfer)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setIsDragOver(true);
  }

  function handlePaste(event: ClipboardEvent<HTMLTextAreaElement>): void {
    if (inputDisabled) return;
    const images = imageFilesFromClipboard(event.clipboardData.items);
    if (images.length === 0) return;
    event.preventDefault();
    addFiles(images);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>): void {
    if (inputDisabled || composingRef.current || event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (slash.handleKeyDown(event)) return;
    if (event.key !== "Enter" || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return;
    event.preventDefault();
    void send();
  }

  function addFiles(nextFiles: Iterable<File>): void {
    if (inputDisabled) return;
    setFiles((current) => {
      const selected = selectImageFiles([...current, ...nextFiles]);
      if (selected.length === current.length) return current;
      invalidateSubmission();
      setUploadStatus(t("webui2.attachmentsReady", { count: selected.length - current.length }));
      return selected;
    });
  }

  function handleFileChange(event: ChangeEvent<HTMLInputElement>): void {
    addFiles(Array.from(event.target.files ?? EMPTY_FILES));
    event.target.value = "";
  }

  async function send(): Promise<void> {
    if (inputDisabled || contextBlocked || !hasContent || sendAbortRef.current) return;
    if (onCompact && text.trim().toLowerCase() === "/compact" && files.length === 0 && chips.length === 0) {
      setErrorCode("");
      setIsSending(true);
      try {
        const result = await onCompact();
        drafts?.delete(targetRef);
        clearComposerDraft({ identity, targetRef, cwd });
        submissionRef.current = null;
        setText("");
        setSkillName("");
        onSent(result.session);
      } catch (error) {
        if (mountedRef.current) setErrorCode(sessionControlErrorCode(error));
      } finally {
        if (mountedRef.current) setIsSending(false);
      }
      return;
    }
    setErrorCode("");
    setUploadStatus(files.length > 0 ? t("webui2.attachmentsPreparing", { count: files.length }) : "");
    setIsSending(true);
    const runtimeValue = runtimeControls ? { ...runtimeControls.value } : undefined;
    const submissionText = applySelectedSkill(text, skillName);
    const fingerprint = draftFingerprint(targetRef, submissionText, files, chips.map((chip) => chip.sourceRef), skillName, runtimeValue);
    const cached = submissionRef.current?.fingerprint === fingerprint ? submissionRef.current : { fingerprint, idempotencyKey: crypto.randomUUID() };
    submissionRef.current = cached;
    const controller = new AbortController();
    sendAbortRef.current = controller;
    try {
      setIsPreparingAttachments(cached.input === undefined && files.length > 0);
      const input = cached.input ?? await prepareSendInput({
        identity,
        ref: targetRef,
        text: submissionText,
        files,
        sourceRefs: chips.map((chip) => chip.sourceRef),
        idempotencyKey: cached.idempotencyKey,
        signal: controller.signal,
        attachmentPreparer,
        runtimeValue
      });
      setIsPreparingAttachments(false);
      cached.input = input;
      const result = await onSend(input);
      drafts?.delete(targetRef);
      clearComposerDraft({ identity, targetRef, cwd });
      submissionRef.current = null;
      setText("");
      setFiles(EMPTY_FILES);
      setChips([]);
      setSkillName("");
      setUploadStatus("");
      onSent(result.session);
    } catch (error) {
      if (mountedRef.current) {
        setErrorCode(isAbortError(error) ? "" : sessionControlErrorCode(error));
        setUploadStatus("");
      }
    } finally {
      if (sendAbortRef.current === controller) sendAbortRef.current = null;
      if (mountedRef.current) {
        setIsPreparingAttachments(false);
        setIsSending(false);
      }
    }
  }

  async function stop(): Promise<void> {
    if (!canStop || isStopping || isSending) return;
    setIsStopping(true);
    setErrorCode("");
    try {
      await onStopRequested();
    } catch (error) {
      setErrorCode(sessionControlErrorCode(error));
    } finally {
      setIsStopping(false);
    }
  }

  return <section aria-label={t("webui2.composer")} className={`webui2-composer${isDragOver ? " is-drag-over" : ""}`} onDragLeave={(event) => { if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) setIsDragOver(false); }} onDragOver={handleDragOver} onDrop={handleDrop}>
    {queuePanel}
    <div aria-live="polite" className="webui2-composer-status">{uploadStatus ? <span>{uploadStatus}</span> : null}</div>
    {errorCode ? <p className="webui2-composer-error" role="alert">{t(`webui2.error.${errorCode}`)}</p> : null}
    {contextBlocked ? <p aria-live="polite" className="webui2-context-wait" id={contextWaitID}>{contextWaitReason}</p> : null}
    {!hasContent && !isRunning && !inputDisabled && nextStepSuggestions.length > 0 ? <section aria-label={language === "zh" ? "下一步建议" : "Next steps"} className="webui2-next-step-suggestions">{nextStepSuggestions.map((suggestion) => <button key={suggestion} onClick={() => changeText(suggestion)} type="button"><Sparkles size={13} aria-hidden="true" /><span>{suggestion}</span></button>)}</section> : null}
    <div className="webui2-composer-shell webui2-composer-dropzone">
      {slash.panel}
      {chips.length > 0 ? <ul aria-label={t("webui2.contextSources")} className="webui2-context-chips">{chips.map((chip) => <li className="webui2-context-chip" data-status={chip.status} key={chip.sourceRef}><span>{chip.title}</span><button aria-label={t("webui2.removeContext", { title: chip.title })} disabled={inputDisabled} onClick={() => { invalidateSubmission(); setChips((current) => current.filter((item) => item.sourceRef !== chip.sourceRef)); }} title={t("webui2.removeContext", { title: chip.title })} type="button"><X aria-hidden="true" size={14} /></button></li>)}</ul> : null}
      {files.length > 0 ? <ul aria-label={t("webui2.attachments")} className="webui2-file-chips">{files.map((file) => <li className="webui2-file-chip" key={fileKey(file)}><ComposerImagePreview file={file} /><span>{file.name}</span><button aria-label={t("webui2.removeAttachment", { name: file.name })} disabled={inputDisabled} onClick={() => { invalidateSubmission(); setFiles((current) => current.filter((item) => fileKey(item) !== fileKey(file))); }} title={t("webui2.removeAttachment", { name: file.name })} type="button"><X aria-hidden="true" size={14} /></button></li>)}</ul> : null}
      <textarea aria-activedescendant={slash.selectedID} aria-controls={slash.listID} aria-describedby={contextBlocked ? contextWaitID : undefined} aria-label={t("webui2.message")} aria-readonly={readOnly ? "true" : undefined} disabled={inputDisabled} onBlur={slash.dismiss} onChange={(event) => changeText(event.target.value)} onCompositionEnd={() => { composingRef.current = false; }} onCompositionStart={() => { composingRef.current = true; }} onKeyDown={handleKeyDown} onPaste={handlePaste} placeholder={t("webui2.messagePlaceholder")} ref={textareaRef} rows={2} value={text} />
    <div className="webui2-composer-controls">
      <input accept="image/*" aria-hidden="true" className="webui2-composer-file-input" disabled={inputDisabled} multiple onChange={handleFileChange} ref={fileInput} tabIndex={-1} type="file" />
      <div className="webui2-composer-controls-left">
      {promptPicker ?? <PromptPicker identity={identity} disabled={inputDisabled} onSelect={(content) => changeText(text.trim() ? `${text.trim()}\n\n${content}` : content)} />}
      <SkillPicker cwd={cwd} disabled={inputDisabled} identity={identity} onChange={changeSkill} selectedName={skillName} />
      <ComposerMoreMenu attachLabel={t("webui2.attachFiles")} disabled={inputDisabled} language={language} onAttach={() => fileInput.current?.click()} queueSettings={queueSettings} />
      {/* The context selector is intentionally disabled. Native session drag and chips remain available. */}
      {runtimeControls ? <ComposerRuntimeToolbar controls={runtimeControls} disabled={inputDisabled} language={language} side="left" /> : null}
      </div>
      <span className="webui2-composer-controls-spacer" />
      <div className="webui2-composer-controls-right">
      {runtimeControls ? <ComposerRuntimeToolbar controls={runtimeControls} disabled={inputDisabled} language={language} side="right" /> : null}
      {isPreparingAttachments ? <button aria-label={t("webui2.cancelUpload")} className="webui2-composer-cancel" onClick={() => sendAbortRef.current?.abort()} title={t("webui2.cancelUpload")} type="button"><X aria-hidden="true" size={16} /><span>{t("webui2.cancelUpload")}</span></button> : null}
      <button aria-describedby={!showStop && contextBlocked ? contextWaitID : undefined} aria-label={showStop ? t("webui2.stop") : sendLabel} className={showStop ? "webui2-composer-stop" : "webui2-composer-send"} disabled={showStop ? !canStop || isStopping || isSending : inputDisabled || contextBlocked || !hasContent} onClick={() => void (showStop ? stop() : send())} title={showStop ? t("webui2.stop") : contextBlocked ? contextWaitReason : sendLabel} type="button">{showStop ? <Square aria-hidden="true" size={14} /> : <ArrowUp aria-hidden="true" size={20} />}</button>
      </div>
    </div>
    </div>
  </section>;
}

async function prepareAttachment(identity: IdentityConfig, file: File, signal: AbortSignal): Promise<PreparedAttachment> {
  throwIfAborted(signal);
  const mediaType = file.type || "image/png";
  const sha256 = await sha256File(file);
  throwIfAborted(signal);
  const metadata = { type: "image", media_type: mediaType, name: file.name, size_bytes: file.size, sha256 };
  const inline = async (): Promise<PreparedAttachment> => {
    const inlineData = dataURLPayload(await fileToDataURL(file));
    throwIfAborted(signal);
    return { ...metadata, inline_data: inlineData };
  };
  if (!identity.mobileJwt.trim()) return inline();
  let signed: Awaited<ReturnType<typeof presignMobileAttachment>>;
  try {
    signed = await presignMobileAttachment(identity, metadata, signal);
  } catch (error) {
    // Only unavailable presign routes permit inline transport; auth/validation
    // failures and upload failures retain their original error and draft.
    if (error instanceof ApiError && PRESIGN_UNAVAILABLE_STATUSES.has(error.status)) return inline();
    throw error;
  }
  throwIfAborted(signal);
  if (!/^https?:\/\//i.test(signed.upload_url) || !signed.attachment?.url) return inline();
  await uploadAttachmentBinary(signed.upload_url, file, signal, signed.headers);
  throwIfAborted(signal);
  return {
    ...(signed.attachment.attachment_id ? { attachment_id: signed.attachment.attachment_id } : {}),
    type: signed.attachment.type || "image",
    media_type: mediaType,
    name: file.name,
    size_bytes: file.size,
    sha256,
    ...(signed.attachment.url ? { url: signed.attachment.url } : {}),
    ...(signed.attachment.transcript ? { transcript: signed.attachment.transcript } : {})
  };
}

function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) throw new DOMException("aborted", "AbortError");
}

function isAbortError(error: unknown): boolean {
  return typeof error === "object" && error !== null && "name" in error && error.name === "AbortError";
}

function draftFingerprint(targetRef: SessionRef, text: string, files: File[], sourceRefs: SessionRef[], skillName: string, runtimeValue?: ComposerRuntimeValue): string {
  return JSON.stringify({ targetRef, text: text.trim(), files: files.map(fileKey), sourceRefs: uniqueRefs(sourceRefs).sort(), skillName, runtimeValue });
}

export function applySelectedSkill(text: string, skillName: string): string {
  const trimmedText = text.trim();
  const trimmedSkill = skillName.trim();
  if (!trimmedSkill || !trimmedText) return trimmedText;
  const command = `/${trimmedSkill}`;
  if (trimmedText === command || trimmedText.startsWith(`${command} `) || trimmedText.startsWith(`${command}\n`)) return trimmedText;
  return `${command}\n\n${trimmedText}`;
}

function uniqueRefs(refs: SessionRef[]): SessionRef[] {
  return Array.from(new Set(refs));
}

function sourceChip(ref: SessionRef, sources: SessionSummary[]): ContextChip {
  const source = sources.find((session) => session.ref === ref);
  return { sourceRef: ref, title: source?.title ?? ref, status: source?.status === "archived" ? "stale" : "ready" };
}

function fileKey(file: File): string {
  return `${file.name}:${file.size}:${file.lastModified}:${file.type}`;
}
