import { Activity, Bot, CheckCircle2, Clock3, ExternalLink, MessageCircle, MessageSquareText, RefreshCcw, TriangleAlert, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { apiRequest, getAgentProfileConversations, listMobileMessages } from "../lib/api";
import { useI18n, type Language } from "../lib/i18n";
import type { AgentProfileConversationCatalog, AgentProfileConversationSummary, AgentProfileRecord, IdentityConfig, TenantMessage } from "../lib/types";

type Props = {
  identity: IdentityConfig;
  profile: AgentProfileRecord;
  onClose: () => void;
  onOpenSession?: (sessionId: number) => void;
  tenantMessages?: boolean;
  showTrace?: boolean;
};

const text = (language: Language, en: string, zh: string) => language === "zh" ? zh : en;
type ConversationLoadError = "not_found" | "failed";

function chatTypeLabel(language: Language, chatType: string): string {
  return chatType === "group" ? text(language, "Group", "群聊") : text(language, "Direct message", "私聊");
}

function dateLabel(language: Language, value?: string): string {
  if (!value) return text(language, "No activity", "暂无活动");
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
}

function messageContent(message: TenantMessage): string {
  return message.content || message.content_json || "";
}

export function ProfileConversationsDialog({ identity, profile, onClose, onOpenSession, tenantMessages = false, showTrace = true }: Props) {
  const { language } = useI18n();
  const [catalog, setCatalog] = useState<AgentProfileConversationCatalog | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [messages, setMessages] = useState<TenantMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMessages, setLoadingMessages] = useState(false);
  const [messageError, setMessageError] = useState(false);
  const [error, setError] = useState<ConversationLoadError | null>(null);
  const [reloadTick, setReloadTick] = useState(0);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const builtinProfile = profile.id === 0 || profile.scope === "builtin";

  const selectedConversation = useMemo(() => catalog?.conversations.find((item) => item.conversation_id === selectedId) || catalog?.conversations[0] || null, [catalog, selectedId]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true); setError(null); setCatalog(null); setMessages([]); setSelectedId(null);
    if (builtinProfile) {
      setLoading(false);
      return () => { cancelled = true; };
    }
    getAgentProfileConversations(identity, profile.profile_key, profile.profile_version, 50)
      .then((next) => { if (!cancelled) { setCatalog(next); setSelectedId(next.conversations[0]?.conversation_id || null); } })
      .catch((err) => { if (!cancelled) setError(errorStatus(err) === 404 ? "not_found" : "failed"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [builtinProfile, identity, profile.profile_key, profile.profile_version, reloadTick]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); };
    window.addEventListener("keydown", handleKeyDown);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    closeButtonRef.current?.focus();
    return () => { window.removeEventListener("keydown", handleKeyDown); document.body.style.overflow = previousOverflow; };
  }, [onClose]);

  useEffect(() => {
    const sessionId = selectedConversation?.session_id;
    if (!sessionId) { setMessages([]); setMessageError(false); setLoadingMessages(false); return; }
    let cancelled = false;
    setLoadingMessages(true); setMessageError(false);
    const request = tenantMessages ? apiRequest<{data: TenantMessage[]}>(identity, `/tenant/messages?session_id=${sessionId}&limit=100`).then((result) => result.data) : listMobileMessages(identity, sessionId);
    request
      .then((next) => { if (!cancelled) setMessages(next); })
      .catch(() => { if (!cancelled) { setMessages([]); setMessageError(true); } })
      .finally(() => { if (!cancelled) setLoadingMessages(false); });
    return () => { cancelled = true; };
  }, [identity, selectedConversation?.session_id, tenantMessages]);

  return <div className="profile-conversation-backdrop">
    <button className="profile-conversation-dismiss" type="button" onClick={onClose} aria-label={text(language, "Close conversations", "关闭对话记录")} />
    <section className="profile-conversation-dialog" role="dialog" aria-modal="true" aria-labelledby="profile-conversations-title" aria-describedby="profile-conversations-description">
      <header className="profile-conversation-header"><div className="profile-conversation-title"><span className="eyebrow">{text(language, "Profile activity", "Profile 活动")}</span><h2 id="profile-conversations-title">{profile.display_name} · {text(language, "Conversations", "对话记录")}</h2><p id="profile-conversations-description">{text(language, "A read-only view across bound Feishu conversations and team runs.", "查看该 Profile 绑定的飞书会话和团队运行记录。")}</p></div><div className="profile-conversation-summary"><div><MessageSquareText size={15} /><span>{text(language, "Conversations", "会话")}</span><strong>{catalog?.conversations.length || 0}</strong></div><div><MessageCircle size={15} /><span>{text(language, "Messages", "消息")}</span><strong>{catalog?.message_count || 0}</strong></div><div><Activity size={15} /><span>{text(language, "Runs", "运行")}</span><strong>{catalog?.run_count || 0}</strong></div><div><Bot size={15} /><span>{text(language, "Teams", "团队")}</span><strong>{catalog?.teams.length || 0}</strong></div></div><button className="icon-button" ref={closeButtonRef} type="button" onClick={onClose} aria-label={text(language, "Close conversations", "关闭对话记录")} title={text(language, "Close", "关闭")}><X size={18} /></button></header>
      {builtinProfile ? <ConversationLoadNotice language={language} kind="builtin" /> : loading ? <div className="profile-conversation-loading"><Activity size={20} className="spin" /><span>{text(language, "Loading conversation activity...", "正在加载对话活动...")}</span></div> : error ? <ConversationLoadNotice language={language} kind={error} onRetry={() => setReloadTick((current) => current + 1)} /> : (
        <div className="profile-conversation-body"><aside className="profile-conversation-list"><div className="profile-conversation-list-head"><span>{text(language, "Recent conversations", "最近会话")}</span><strong>{catalog?.conversations.length || 0}</strong></div>{catalog?.conversations.length ? catalog.conversations.map((conversation) => <ConversationRow key={conversation.conversation_id} language={language} selected={conversation.conversation_id === selectedConversation?.conversation_id} conversation={conversation} onClick={() => setSelectedId(conversation.conversation_id)} />) : <div className="profile-conversation-empty"><MessageSquareText size={24} /><strong>{text(language, "No conversations yet", "暂无对话记录")}</strong><span>{text(language, "Messages will appear here after the bound bot receives traffic.", "绑定机器人收到消息后，会话会显示在这里。")}</span></div>}</aside><div className="profile-conversation-detail">{messageError ? <p role="alert">{text(language, "Unable to load messages in this environment.", "当前环境的消息正文读取失败。")}</p> : null}{selectedConversation ? <ConversationDetail identity={identity} language={language} conversation={selectedConversation} messages={messages} loadingMessages={loadingMessages} onOpenSession={onOpenSession} showTrace={showTrace} /> : <div className="profile-conversation-empty detail"><MessageSquareText size={28} /><strong>{text(language, "Select a conversation", "选择一个会话")}</strong></div>}</div></div>
       )}
    </section>
  </div>;
}

function errorStatus(error: unknown): number {
  if (!error || typeof error !== "object" || !("status" in error)) return 0;
  return typeof error.status === "number" ? error.status : 0;
}

function ConversationLoadNotice({ language, kind, onRetry }: { language: Language; kind: "builtin" | ConversationLoadError; onRetry?: () => void }) {
  const builtin = kind === "builtin";
  const notFound = kind === "not_found";
  const title = builtin
    ? text(language, "Builtin profiles do not store conversations", "内置 Profile 暂不保存对话记录")
    : notFound
      ? text(language, "No conversation data found", "尚未找到对话数据")
      : text(language, "Unable to load conversations", "暂时无法加载对话记录");
  const description = builtin
    ? text(language, "This profile is a reusable template. Create and publish a tenant profile, then bind a Feishu bot to view real conversations here.", "这是一个可复用模板。请新建并发布租户 Profile，再绑定飞书机器人，即可在这里查看真实会话。")
    : notFound
      ? text(language, "This profile may not be published in the current tenant, or it has not received any bound channel traffic yet.", "当前租户中可能尚未发布该 Profile，或绑定的渠道还没有产生会话。")
      : text(language, "The service is temporarily unavailable. Check the connection and try again.", "服务暂时不可用，请检查连接后重新加载。");
  return <div className={`profile-conversation-notice ${kind === "failed" ? "error" : ""}`} role={kind === "failed" ? "alert" : "status"}>
    <span className="profile-conversation-notice-icon">{kind === "failed" ? <TriangleAlert size={22} /> : <MessageSquareText size={22} />}</span>
    <span className="eyebrow">{builtin ? text(language, "Template profile", "模板 Profile") : text(language, "Conversation data", "对话数据")}</span>
    <h3>{title}</h3>
    <p>{description}</p>
    {!builtin && onRetry ? <button className="secondary-button" type="button" onClick={onRetry}><RefreshCcw size={15} /> {text(language, "Try again", "重新加载")}</button> : null}
  </div>;
}

function ConversationRow({ language, conversation, selected, onClick }: { language: Language; conversation: AgentProfileConversationSummary; selected: boolean; onClick: () => void }) {
  return <button className={selected ? "profile-conversation-row active" : "profile-conversation-row"} type="button" onClick={onClick}><span className="profile-conversation-row-icon">{conversation.chat_type === "group" ? <Bot size={16} /> : <MessageCircle size={16} />}</span><span className="profile-conversation-row-copy"><strong>{conversation.title || conversation.external_chat_id}</strong><small>{chatTypeLabel(language, conversation.chat_type)} · {conversation.account_key}</small><em>{conversation.last_message_preview || text(language, "No message preview", "暂无消息预览")}</em></span><span className="profile-conversation-row-meta"><strong>{conversation.message_count}</strong><small>{dateLabel(language, conversation.last_message_at || conversation.last_inbound_at)}</small></span></button>;
}

function ConversationDetail({ identity, language, conversation, messages, loadingMessages, onOpenSession, showTrace }: { identity: IdentityConfig; language: Language; conversation: AgentProfileConversationSummary; messages: TenantMessage[]; loadingMessages: boolean; onOpenSession?: (sessionId: number) => void; showTrace: boolean }) {
  const traceParams = new URLSearchParams({
    token: identity.apiToken,
    source: "tenant",
    session_id: String(conversation.session_id || ""),
    tenant_key: identity.tenantKey,
    user_id: identity.userId
  });
  if (identity.deviceId) traceParams.set("device_id", identity.deviceId);
  const traceUrl = showTrace && conversation.session_id ? `${identity.apiBase.replace(/\/api$/, "")}/trace?${traceParams.toString()}` : "";
  return <><div className="profile-conversation-detail-head"><div><span className="eyebrow">{chatTypeLabel(language, conversation.chat_type)} · {conversation.account_key}</span><h3>{conversation.title || conversation.external_chat_id}</h3><p>{conversation.external_chat_id}{conversation.external_thread_id ? ` · ${conversation.external_thread_id}` : ""}</p></div><div className="profile-conversation-detail-actions">{conversation.session_id && onOpenSession ? <button className="secondary-button" type="button" onClick={() => onOpenSession(conversation.session_id || 0)}><MessageSquareText size={15} /> {text(language, "Open in Chat Lab", "在聊天实验室打开")}</button> : null}{traceUrl ? <a className="secondary-button" href={traceUrl} target="_blank" rel="noreferrer"><ExternalLink size={15} /> Trace</a> : null}</div></div><div className="profile-conversation-facts"><span><Clock3 size={14} /> {dateLabel(language, conversation.last_message_at || conversation.last_inbound_at)}</span><span><CheckCircle2 size={14} /> {conversation.latest_run_status || text(language, "No run", "暂无运行")}</span><span><Activity size={14} /> {conversation.run_count} {text(language, "runs", "次运行")}</span></div><div className="profile-message-stream">{loadingMessages ? <div className="profile-conversation-loading"><Activity size={18} className="spin" /><span>{text(language, "Loading messages...", "正在加载消息...")}</span></div> : messages.length ? messages.slice(-40).map((message) => <MessageBubble key={message.id} message={message} language={language} />) : <div className="profile-conversation-empty detail"><MessageSquareText size={26} /><strong>{text(language, "No message body available", "暂无消息正文")}</strong><span>{text(language, "Use Trace for the runtime timeline.", "可以打开 Trace 查看运行时间线。")}</span></div>}</div></>;
}

function MessageBubble({ message, language }: { message: TenantMessage; language: Language }) {
  const content = messageContent(message);
  return <article className={`profile-message-bubble ${message.role === "assistant" ? "assistant" : message.role === "user" ? "user" : "system"}`}><div className="profile-message-meta"><strong>{message.role === "assistant" ? text(language, "Agent", "智能体") : message.role === "user" ? text(language, "User", "用户") : message.role}</strong><span>#{message.turn_index}</span></div><p>{content || text(language, "Structured message", "结构化消息")}</p></article>;
}
