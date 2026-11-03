import {
  Archive,
  FilePlus2,
  GitBranch,
  Loader2,
  MessageSquarePlus,
  RefreshCcw,
  Save,
  Send,
  Square,
  Trash2,
  X
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  branchMobileSession,
  cancelMobileMessage,
  createMobileSession,
  DEFAULT_STREAM_TIMEOUT_MS,
  archiveMobileSession,
  listMobileMessages,
  listMobileSessions,
  presignMobileAttachment,
  regenerateMobileMessage,
  streamMobileMessage,
  uploadAttachmentBinary,
  updateMobileSession
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import { tenantMessagesToChat } from "../lib/messages";
import type { ChatMessage, IdentityConfig, MobileAttachment, MobileMessageEvent, TenantSession } from "../lib/types";
import { clearConversationDraft, readConversationDraft, writeConversationDraft } from "../lib/conversationDraft";

type Props = {
  identity: IdentityConfig;
  selectedSessionId: number | null;
  onSelectSession: (id: number | null) => void;
  onStatus: (message: string) => void;
  onDataChanged: () => void;
};

export function ChatLab({ identity, selectedSessionId, onSelectSession, onStatus, onDataChanged }: Props) {
  const { t } = useI18n();
  const [sessions, setSessions] = useState<TenantSession[]>([]);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [prompt, setPrompt] = useState("");
  const [sessionSearch, setSessionSearch] = useState("");
  const [sessionFilter, setSessionFilter] = useState("all");
  const [loadingSessions, setLoadingSessions] = useState(false);
  const [sessionLoadError, setSessionLoadError] = useState("");
  const [streaming, setStreaming] = useState(false);
  const [streamState, setStreamState] = useState<"idle" | "streaming" | "completed" | "failed" | "cancelled" | "timed_out">("idle");
  const [activeMessageId, setActiveMessageId] = useState<number | null>(null);
  const [sessionTitle, setSessionTitle] = useState("");
  const [sessionStatus, setSessionStatus] = useState("active");
  const [attachments, setAttachments] = useState<MobileAttachment[]>([]);
  const [attachmentType, setAttachmentType] = useState("image");
  const [attachmentName, setAttachmentName] = useState("screenshot.png");
  const [attachmentMediaType, setAttachmentMediaType] = useState("image/png");
  const [attachmentURL, setAttachmentURL] = useState("");
  const [attachmentTranscript, setAttachmentTranscript] = useState("");
  const [uploadStatus, setUploadStatus] = useState("");
  const [uploadingAttachment, setUploadingAttachment] = useState(false);
  const draftHydratedKeyRef = useRef("");
  const abortRef = useRef<AbortController | null>(null);

  const selectedSession = useMemo(
    () => sessions.find((session) => session.id === selectedSessionId) || null,
    [sessions, selectedSessionId]
  );
  const draftSessionKey = selectedSessionId ? `session:${selectedSessionId}` : "";
  const draftWorkspace = selectedSession?.cwd || "unknown";
  const draftContextKey = draftSessionKey ? `${draftSessionKey}\u0000${draftWorkspace}` : "";
  const filteredSessions = useMemo(() => {
    const query = sessionSearch.trim().toLowerCase();
    return sessions.filter((session) => {
      const matchesStatus = sessionFilter === "all" || (session.status || "active") === sessionFilter;
      if (!matchesStatus) {
        return false;
      }
      if (!query) {
        return true;
      }
      return [session.title, session.session_key, session.status, session.model, String(session.id)]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(query));
    });
  }, [sessions, sessionFilter, sessionSearch]);

  async function refreshSessions(nextSelectedId = selectedSessionId) {
    setLoadingSessions(true);
    try {
      const items = await listMobileSessions(identity);
      setSessionLoadError("");
      setSessions(items);
      if (!nextSelectedId && items[0]?.id) {
        onSelectSession(items[0].id);
      }
    } catch (err) {
      const message = mobileSessionErrorMessage(err, t("chat.mobileAuthHint"));
      setSessionLoadError(message);
      onStatus(message);
    } finally {
      setLoadingSessions(false);
    }
  }

  async function refreshMessages(sessionId = selectedSessionId) {
    if (!sessionId) {
      setMessages([]);
      return;
    }
    try {
      const items = await listMobileMessages(identity, sessionId);
      setMessages(tenantMessagesToChat(items));
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  useEffect(() => {
    void refreshSessions();
  }, [identity.apiBase, identity.apiToken, identity.mobileJwt, identity.tenantKey, identity.userId]);

  useEffect(() => {
    void refreshMessages();
  }, [selectedSessionId, identity.apiBase, identity.apiToken, identity.mobileJwt, identity.tenantKey, identity.userId]);

  useEffect(() => {
    if (!draftSessionKey) {
      draftHydratedKeyRef.current = "";
      setPrompt("");
      setAttachments([]);
      return;
    }
    const draft = readConversationDraft("chat-lab", draftSessionKey, draftWorkspace);
    draftHydratedKeyRef.current = draftContextKey;
    setPrompt(draft?.text || "");
    setAttachments((draft?.assetIDs || []).map((attachmentID) => ({ type: "image", attachment_id: attachmentID })));
  }, [draftContextKey, draftSessionKey, draftWorkspace]);

  useEffect(() => {
    if (!draftContextKey || draftHydratedKeyRef.current !== draftContextKey) {
      return;
    }
    const timer = window.setTimeout(() => {
      writeConversationDraft({
        surface: "chat-lab",
        sessionKey: draftSessionKey,
        workspace: draftWorkspace,
        text: prompt,
        assetIDs: draftAttachmentIDs(attachments),
        updatedAt: new Date().toISOString()
      });
    }, 350);
    return () => window.clearTimeout(timer);
  }, [attachments, draftContextKey, draftSessionKey, draftWorkspace, prompt]);

  useEffect(() => {
    setSessionTitle(selectedSession?.title || "");
    setSessionStatus(selectedSession?.status || "active");
  }, [selectedSession?.id, selectedSession?.title, selectedSession?.status]);

  async function handleCreateSession() {
    try {
      const id = await createMobileSession(identity, `Web Chat ${new Date().toLocaleString()}`);
      onSelectSession(id);
      await refreshSessions(id);
      onStatus(t("chat.createdSession", { id }));
      onDataChanged();
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleSend() {
    if (!selectedSessionId || prompt.trim() === "" || streaming) {
      return;
    }
    const content = prompt.trim();
    const outgoingAttachments = [...attachments];
    const draftSession = draftSessionKey;
    const draftCwd = draftWorkspace;
    if (draftSession) {
      clearConversationDraft("chat-lab", draftSession, draftCwd);
    }
    setPrompt("");
    setStreaming(true);
    setStreamState("streaming");
    const controller = new AbortController();
    abortRef.current = controller;
    const localAssistantId = `stream-${Date.now()}`;
    setMessages((current) => [
      ...current,
      { id: `user-${Date.now()}`, role: "user", content, status: "completed" },
      { id: localAssistantId, role: "assistant", content: "", status: "streaming" }
    ]);
    try {
      await streamMobileMessage(
        identity,
        selectedSessionId,
        content,
        outgoingAttachments,
        {
          onEvent: (event) => applyStreamEvent(event, localAssistantId),
          onDone: () => {
            setStreamState("completed");
            onStatus(t("chat.streamCompleted"));
          }
        },
        controller.signal
      );
      setAttachments([]);
      await refreshMessages(selectedSessionId);
      await refreshSessions(selectedSessionId);
      notifyDataChangedWithFollowup();
    } catch (err) {
      const nextState = streamFailureState(err, controller.signal);
      setStreamState(nextState);
      markLocalAssistantStatus(localAssistantId, nextState);
      if (draftSession) {
        setPrompt(content);
        setAttachments(outgoingAttachments);
        writeConversationDraft({
          surface: "chat-lab",
          sessionKey: draftSession,
          workspace: draftCwd,
          text: content,
          assetIDs: draftAttachmentIDs(outgoingAttachments),
          updatedAt: new Date().toISOString()
        });
      }
      if (nextState === "timed_out") {
        onStatus(t("chat.streamTimedOut", { seconds: Math.round(DEFAULT_STREAM_TIMEOUT_MS / 1000) }));
      } else if (nextState === "cancelled") {
        onStatus(t("chat.streamCancelled"));
      } else {
        onStatus(errorMessage(err));
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  }

  async function handlePresignAttachment() {
    try {
      const signed = await presignMobileAttachment(identity, {
        type: attachmentType,
        media_type: attachmentMediaType,
        name: attachmentName,
        size_bytes: 1,
        sha256: "webui-placeholder"
      });
      setAttachmentURL(signed.attachment.url || signed.upload_url);
      setAttachments((current) => [...current, { ...signed.attachment, transcript: attachmentTranscript }]);
      onStatus(t("chat.presignedAttachment", { id: signed.attachment_id }));
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleUploadAttachment(file: File | null) {
    if (!file || uploadingAttachment) {
      return;
    }
    setUploadingAttachment(true);
    setUploadStatus(t("chat.uploadHashing"));
    try {
      const sha256 = await sha256Hex(file);
      setUploadStatus(t("chat.uploadPresigning"));
      const signed = await presignMobileAttachment(identity, {
        type: attachmentType,
        media_type: file.type || attachmentMediaType || "application/octet-stream",
        name: file.name,
        size_bytes: file.size,
        sha256
      });
      const nextAttachment = { ...signed.attachment, transcript: attachmentTranscript };
      setAttachmentName(file.name);
      setAttachmentMediaType(file.type || attachmentMediaType);
      setAttachmentURL(signed.attachment.url || signed.upload_url);
      if (isHTTPUploadURL(signed.upload_url)) {
        setUploadStatus(t("chat.uploadUploading"));
        await uploadAttachmentBinary(signed.upload_url, file);
        setUploadStatus(t("chat.uploadComplete", { name: file.name }));
      } else {
        setUploadStatus(t("chat.uploadMetadataOnly", { name: file.name }));
      }
      setAttachments((current) => [...current, nextAttachment]);
      onStatus(t("chat.attachmentUploaded", { name: file.name }));
    } catch (err) {
      const message = errorMessage(err);
      setUploadStatus(message);
      onStatus(message);
    } finally {
      setUploadingAttachment(false);
    }
  }

  function handleAddAttachmentMetadata() {
    if (!attachmentURL.trim()) {
      onStatus(t("chat.attachmentUrlRequired"));
      return;
    }
    setAttachments((current) => [
      ...current,
      {
        type: attachmentType,
        media_type: attachmentMediaType,
        name: attachmentName,
        url: attachmentURL,
        size_bytes: 1,
        transcript: attachmentTranscript
      }
    ]);
    onStatus(t("chat.attachmentAdded"));
  }

  async function handleStop() {
    abortRef.current?.abort();
    setStreamState("cancelled");
    if (selectedSessionId && activeMessageId) {
      try {
        await cancelMobileMessage(identity, selectedSessionId, activeMessageId);
        onStatus(t("chat.cancelledMessage", { id: activeMessageId }));
        await refreshMessages(selectedSessionId);
        onDataChanged();
      } catch (err) {
        onStatus(errorMessage(err));
      }
    }
    setStreaming(false);
  }

  async function handleRegenerate() {
    if (!selectedSessionId || streaming) {
      return;
    }
    const assistant = [...messages].reverse().find((message) => message.role === "assistant" && message.messageId);
    if (!assistant?.messageId) {
      onStatus(t("chat.noAssistant"));
      return;
    }
    setStreaming(true);
    setStreamState("streaming");
    const controller = new AbortController();
    abortRef.current = controller;
    const localAssistantId = `regen-${Date.now()}`;
    setMessages((current) => [...current, { id: localAssistantId, role: "assistant", content: "", status: "streaming" }]);
    try {
      await regenerateMobileMessage(
        identity,
        selectedSessionId,
        assistant.messageId,
        {
          onEvent: (event) => applyStreamEvent(event, localAssistantId),
          onDone: () => {
            setStreamState("completed");
            onStatus(t("chat.regenerateCompleted"));
          }
        },
        controller.signal
      );
      await refreshMessages(selectedSessionId);
      notifyDataChangedWithFollowup();
    } catch (err) {
      const nextState = streamFailureState(err, controller.signal);
      setStreamState(nextState);
      markLocalAssistantStatus(localAssistantId, nextState);
      if (nextState === "timed_out") {
        onStatus(t("chat.regenerateTimedOut", { seconds: Math.round(DEFAULT_STREAM_TIMEOUT_MS / 1000) }));
      } else if (nextState === "cancelled") {
        onStatus(t("chat.regenerateCancelled"));
      } else {
        onStatus(errorMessage(err));
      }
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  }

  async function handleBranch() {
    if (!selectedSessionId) {
      return;
    }
    try {
      const id = await branchMobileSession(identity, selectedSessionId);
      onSelectSession(id);
      await refreshSessions(id);
      onStatus(t("chat.createdBranch", { id }));
      notifyDataChangedWithFollowup();
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleUpdateSession() {
    if (!selectedSessionId) {
      return;
    }
    try {
      await updateMobileSession(identity, selectedSessionId, {
        title: sessionTitle,
        status: sessionStatus,
        model: identity.model,
        cwd: selectedSession?.cwd,
        metadata_json: selectedSession?.metadata_json
      });
      await refreshSessions(selectedSessionId);
      onStatus(t("chat.updatedSession", { id: selectedSessionId }));
      notifyDataChangedWithFollowup();
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  async function handleArchiveSession() {
    if (!selectedSessionId) {
      return;
    }
    try {
      await archiveMobileSession(identity, selectedSessionId);
      onSelectSession(null);
      setMessages([]);
      await refreshSessions(null);
      onStatus(t("chat.archivedSession", { id: selectedSessionId }));
      notifyDataChangedWithFollowup();
    } catch (err) {
      onStatus(errorMessage(err));
    }
  }

  function applyStreamEvent(event: MobileMessageEvent, localAssistantId: string) {
    if (event.type === "message_start" && event.message_id) {
      setActiveMessageId(event.message_id);
    }
    if (event.type === "delta" && event.delta) {
      setMessages((current) =>
        current.map((message) =>
          message.id === localAssistantId ? { ...message, content: message.content + event.delta } : message
        )
      );
    }
    if (event.type === "message_stop" || event.type === "error") {
      setMessages((current) =>
        current.map((message) =>
          message.id === localAssistantId ? { ...message, status: event.status || event.type } : message
        )
      );
      if (event.type === "error") {
        setStreamState("failed");
        onStatus(event.error || t("chat.streamFailed"));
      } else {
        setStreamState("completed");
      }
    }
  }

  function markLocalAssistantStatus(localAssistantId: string, status: string) {
    setMessages((current) =>
      current.map((message) => (message.id === localAssistantId ? { ...message, status } : message))
    );
  }

  function notifyDataChangedWithFollowup() {
    onDataChanged();
    window.setTimeout(onDataChanged, 900);
  }

  return (
    <section className="chat-layout">
      <aside className="panel sessions-panel">
        <div className="panel-header">
          <div>
            <h2>{t("chat.sessions")}</h2>
            <p>{loadingSessions ? t("chat.loading") : t("chat.loaded", { count: sessions.length })}</p>
          </div>
          <button type="button" className="icon-button" onClick={handleCreateSession} title={t("chat.createSession")}>
            <MessageSquarePlus size={16} />
          </button>
        </div>
        <div className="session-filters">
          <input
            aria-label={t("chat.searchSessions")}
            value={sessionSearch}
            onChange={(event) => setSessionSearch(event.target.value)}
            placeholder={t("chat.searchSessions")}
          />
          <select aria-label={t("chat.filterStatus")} value={sessionFilter} onChange={(event) => setSessionFilter(event.target.value)}>
            <option value="all">{t("chat.allStatuses")}</option>
            <option value="active">active</option>
            <option value="paused">paused</option>
            <option value="archived">archived</option>
          </select>
        </div>
        <div className="session-list">
          {sessionLoadError ? (
            <div className="empty-state compact session-error" role="alert">
              <strong>{t("chat.sessionLoadFailed")}</strong>
              <span>{sessionLoadError}</span>
            </div>
          ) : filteredSessions.length === 0 ? (
            <div className="empty-state compact">{t("chat.noSessions")}</div>
          ) : (
            filteredSessions.map((session) => (
              <button
                key={session.id}
                type="button"
                className={session.id === selectedSessionId ? "session-item active" : "session-item"}
                onClick={() => onSelectSession(session.id)}
              >
                <span>{session.title || session.session_key || `Session ${session.id}`}</span>
                <small>{session.status || "active"} · #{session.id}</small>
              </button>
            ))
          )}
        </div>
      </aside>
      <main className="panel chat-panel">
        <div className="panel-header">
          <div>
            <h2>{selectedSession ? selectedSession.title || `Session ${selectedSession.id}` : t("chat.chatLab")}</h2>
            <p>
              {t("chat.subtitle")}
              {streamState !== "idle" ? ` · ${t("chat.stream", { state: streamState })}` : ""}
              {activeMessageId ? ` · ${t("chat.activeMessage", { id: activeMessageId })}` : ""}
            </p>
          </div>
          <div className="button-row">
            <button type="button" className="icon-button" onClick={() => void refreshMessages()} title={t("chat.refreshMessages")}>
              <RefreshCcw size={16} />
            </button>
            <button type="button" className="icon-button" onClick={handleRegenerate} title={t("chat.regenerate")}>
              <Trash2 size={16} />
            </button>
            <button type="button" className="icon-button" onClick={handleBranch} title={t("chat.branch")}>
              <GitBranch size={16} />
            </button>
            <button type="button" className="icon-button" onClick={handleArchiveSession} title={t("chat.archive")}>
              <Archive size={16} />
            </button>
          </div>
        </div>
        <section className="chat-context-strip" aria-label={t("chat.context")}>
          <div>
            <span>{t("app.tenant")}</span>
            <strong>{identity.tenantKey}</strong>
          </div>
          <div>
            <span>{t("app.user")}</span>
            <strong>{identity.userId}</strong>
          </div>
          <div>
            <span>{t("app.model")}</span>
            <strong>{identity.model}</strong>
          </div>
          <div>
            <span>{t("inspector.session")}</span>
            <strong>{selectedSessionId ? `#${selectedSessionId}` : t("inspector.selectSession")}</strong>
          </div>
        </section>
        <div className="session-editor">
          <input
            value={sessionTitle}
            onChange={(event) => setSessionTitle(event.target.value)}
            placeholder={t("chat.sessionTitle")}
            disabled={!selectedSessionId}
          />
          <select value={sessionStatus} onChange={(event) => setSessionStatus(event.target.value)} disabled={!selectedSessionId}>
            <option value="active">active</option>
            <option value="paused">paused</option>
            <option value="archived">archived</option>
          </select>
          <button type="button" className="secondary-button" onClick={handleUpdateSession} disabled={!selectedSessionId}>
            <Save size={15} />
            {t("chat.saveSession")}
          </button>
        </div>
        <div className="message-list">
          {messages.length === 0 ? (
            <div className="empty-state">{t("chat.empty")}</div>
          ) : (
            messages.map((message) => (
              <article key={message.id} className={`message ${message.role}`}>
                <header>
                  <span>{t(`chat.role.${message.role}`)}</span>
                  <small>{message.status || t("chat.saved")}</small>
                </header>
                <p>{message.content || " "}</p>
              </article>
            ))
          )}
        </div>
        <div className="composer">
          <div className="attachment-composer">
            <select value={attachmentType} onChange={(event) => setAttachmentType(event.target.value)}>
              <option value="image">image</option>
              <option value="voice">voice</option>
              <option value="audio">audio</option>
              <option value="file">file</option>
            </select>
            <input value={attachmentName} onChange={(event) => setAttachmentName(event.target.value)} placeholder={t("chat.attachmentName")} />
            <input value={attachmentMediaType} onChange={(event) => setAttachmentMediaType(event.target.value)} placeholder={t("chat.mediaType")} />
            <input value={attachmentURL} onChange={(event) => setAttachmentURL(event.target.value)} placeholder={t("chat.attachmentUrl")} />
            <input value={attachmentTranscript} onChange={(event) => setAttachmentTranscript(event.target.value)} placeholder={t("chat.voiceTranscript")} />
            <label className="file-upload-button">
              <FilePlus2 size={15} />
              <span>{uploadingAttachment ? t("chat.uploading") : t("chat.uploadFile")}</span>
              <input
                type="file"
                onChange={(event) => {
                  const file = event.currentTarget.files?.[0] || null;
                  event.currentTarget.value = "";
                  void handleUploadAttachment(file);
                }}
                disabled={uploadingAttachment}
              />
            </label>
            <button type="button" className="icon-button" onClick={handlePresignAttachment} title={t("chat.presignAttachment")}>
              <FilePlus2 size={16} />
            </button>
            <button type="button" className="secondary-button" onClick={handleAddAttachmentMetadata}>{t("chat.addMetadata")}</button>
          </div>
          {uploadStatus ? <div className="attachment-upload-status">{uploadStatus}</div> : null}
          {attachments.length > 0 ? (
            <div className="attachment-list">
              {attachments.map((attachment, index) => (
                <div key={attachment.attachment_id || attachment.sha256 || `${attachment.type}-${attachment.name || attachment.url || "attachment"}`} className="attachment-item">
                  {isPreviewableImageAttachment(attachment) ? <img className="attachment-preview" src={attachment.url} alt={attachment.name || t("chat.imageAttachment")} loading="lazy" /> : null}
                  <span className="attachment-chip">
                    {attachment.type}:{attachment.name || attachment.url}
                    <button
                      type="button"
                      className="chip-button"
                      onClick={() => setAttachments((current) => current.filter((_, itemIndex) => itemIndex !== index))}
                      title={t("chat.removeAttachment")}
                    >
                      <X size={12} />
                    </button>
                  </span>
                </div>
              ))}
            </div>
          ) : null}
          <textarea
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            placeholder={t("chat.placeholder")}
            rows={3}
            onKeyDown={(event) => {
              if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
                void handleSend();
              }
            }}
          />
          <div className="composer-actions">
            <button type="button" className="primary-button" onClick={streaming ? handleStop : handleSend} disabled={!selectedSessionId}>
              {streaming ? <Square size={16} /> : <Send size={16} />}
              {streaming ? t("chat.stop") : t("chat.send")}
            </button>
            {streaming ? <Loader2 className="spin" size={18} /> : null}
            <div className={`stream-badge ${streamState}`}>{t("chat.streamBadge", { state: streamState })}</div>
          </div>
        </div>
      </main>
    </section>
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function mobileSessionErrorMessage(err: unknown, hint: string): string {
  const message = errorMessage(err);
  const lower = message.toLowerCase();
  if (lower.includes("invalid mobile token") || lower.includes("mobile jwt") || lower.includes("unauthorized")) {
    return `${message}. ${hint}`;
  }
  return message;
}

function streamFailureState(err: unknown, signal: AbortSignal): "failed" | "cancelled" | "timed_out" {
  if (signal.aborted) {
    return "cancelled";
  }
  if (err instanceof DOMException && err.name === "TimeoutError") {
    return "timed_out";
  }
  if (err instanceof Error && (err.name === "TimeoutError" || err.message.toLowerCase().includes("timed out"))) {
    return "timed_out";
  }
  return "failed";
}

export async function sha256Hex(file: Blob): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
}

export function draftAttachmentIDs(attachments: MobileAttachment[]): string[] {
  return attachments
    .map((attachment) => String(attachment.attachment_id || attachment.sha256 || "").trim())
    .filter((attachmentID, index, all) => attachmentID !== "" && all.indexOf(attachmentID) === index);
}

export function isPreviewableImageAttachment(attachment: MobileAttachment): boolean {
  const mediaType = String(attachment.media_type || "").toLowerCase();
  const url = String(attachment.url || "").trim();
  if (attachment.type !== "image" || !mediaType.startsWith("image/") || !url) {
    return false;
  }
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:" || parsed.protocol === "http:";
  } catch {
    return false;
  }
}

function isHTTPUploadURL(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}
