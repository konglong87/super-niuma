import { ArrowDown, History, PanelRightOpen } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from "react";
import { useI18n } from "../../lib/i18n";
import type { SessionDetail, SessionMessage, SessionRef } from "../types";
import { ConversationMessage } from "./ConversationMessage";
import { EmptyState } from "./EmptyState";
import type { IdentityConfig, WebAgentConversationDetail } from "../../lib/types";
import type { StreamState } from "../../hooks/useAgentTaskStream";
import { advanceDisplayProgress, type DisplayProgress } from "./conversationDisplayProgress";
import { messageExperienceCopy } from "./messageExperienceCopy";
import { buildConversationRuns, elapsedMilliseconds, isTerminalStatus, messageWithRuntime } from "./conversationViewModel";
import { useConversationClock } from "./useConversationClock";
import { THINKING_PREFERENCE_KEY, useThinkingMode } from "./thinkingPreference";
import "./messageExperience.css";

const MESSAGE_PAGE_SIZE = 60;
const FOLLOW_SCROLL_THRESHOLD = 80;
export { THINKING_PREFERENCE_KEY };

type ConversationWorkspaceProps = {
  identity?: IdentityConfig;
  detail: SessionDetail | undefined;
  selectedRef: SessionRef;
  onOpenInspector: () => void;
  composer: ReactNode;
  onCreateSession?: () => void;
  onRetry?: (message: SessionMessage) => void;
  streamState?: StreamState;
  runtimeDetails?: WebAgentConversationDetail;
};

export function ConversationWorkspace({ identity, detail, selectedRef, onOpenInspector, composer, onCreateSession = () => undefined, onRetry, streamState = "idle", runtimeDetails }: ConversationWorkspaceProps): JSX.Element {
  const { t, language } = useI18n();
  const copy = messageExperienceCopy[language];
  const stream = useRef<HTMLDivElement>(null);
  const follow = useRef(true);
  const [showJump, setShowJump] = useState(false);
  const thinkingMode = useThinkingMode();
  const [visibleCount, setVisibleCount] = useState(MESSAGE_PAGE_SIZE);
  const permissionPending = detail?.status === "waiting_permission";
  const questionPending = detail?.status === "waiting_input";
  const running = detail?.status === "running" || detail?.status === "queued" || permissionPending || questionPending;
  const now = useConversationClock(running);
  const runs = useMemo(() => detail ? buildConversationRuns(detail, runtimeDetails) : [], [detail, runtimeDetails]);
  const runsByID = useMemo(() => new Map(runs.map((run) => [run.id, run])), [runs]);
  const projectedMessages = useMemo(() => detail?.messages.map((message) => messageWithRuntime(message, runsByID.get(String(message.taskID)))) ?? [], [detail?.messages, runsByID]);
  const hasStream = Boolean(detail && (detail.messages.length > 0 || running));
  const [progress, setProgress] = useState<DisplayProgress>(() => ({ selectedRef, seen: new Map(), animating: new Set() }));
  const display = advanceDisplayProgress(progress, selectedRef, detail?.messages);
  if (display !== progress) setProgress(display);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Selection changes reset viewport ownership.
  useEffect(() => { follow.current = true; setShowJump(false); setVisibleCount(MESSAGE_PAGE_SIZE); }, [selectedRef]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: New message projections trigger auto-follow.
  useEffect(() => { if (follow.current && stream.current) stream.current.scrollTop = stream.current.scrollHeight; }, [detail?.messages]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: Observe each visible message after projection/pagination changes; the scrolling viewport itself has fixed height.
  useEffect(() => {
    const node = stream.current;
    if (!node || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => { if (follow.current) node.scrollTop = node.scrollHeight; });
    for (const child of node.children) observer.observe(child);
    return () => observer.disconnect();
  }, [selectedRef, hasStream, detail?.messages, visibleCount, thinkingMode]);
  const messages = projectedMessages.filter((message) => thinkingMode !== "hidden" || message.kind !== "thinking");
  const hiddenCount = Math.max(0, messages.length - visibleCount);
  const latest = detail?.messages.at(-1);
  const stage = permissionPending ? copy.permission : questionPending ? copy.question : detail?.status === "queued" ? copy.queued : latest?.kind === "thinking" ? copy.thinkingStage : latest?.tool?.status === "running" ? copy.toolStage : latest?.role === "assistant" ? copy.responseStage : copy.waiting;
  const connection = streamState === "error" ? copy.reconnecting : streamState === "connecting" ? copy.connecting : streamState === "closed" && running ? copy.closed : "";
  const responseByTask = new Map<number | string, string[]>();
  for (const message of detail?.messages ?? []) {
    if (message.role !== "assistant" || message.kind !== "message" || message.permission || !message.content) continue;
    const key = message.taskID ?? message.id;
    responseByTask.set(key, [...(responseByTask.get(key) ?? []), message.content]);
  }
  function completeDisplay(id: string) {
    setProgress((current) => {
      if (!current.animating.has(id)) return current;
      const animating = new Set(current.animating);
      animating.delete(id);
      return { ...current, animating };
    });
  }
  function showEarlier() {
    const node = stream.current;
    const height = node?.scrollHeight ?? 0;
    const top = node?.scrollTop ?? 0;
    follow.current = false;
    setVisibleCount((count) => count + MESSAGE_PAGE_SIZE);
    requestAnimationFrame(() => { if (node) node.scrollTop = top + node.scrollHeight - height; });
  }
  const workspaceClass = `webui2-conversation-workspace${permissionPending ? " webui2-conversation-workspace--permission-pending" : ""}`;
  return <section aria-busy={detail ? undefined : "true"} className={workspaceClass}>
    {permissionPending ? <div className="webui2-permission-strip" role="alert">{t("webui2.inspector.permission")}</div> : null}
    <div className="webui2-conversation-body">
      <button aria-label={t("webui2.openInspector")} className="webui2-conversation-inspector" onClick={onOpenInspector} title={t("webui2.openInspector")} type="button"><PanelRightOpen aria-hidden="true" size={16} /></button>
      {detail ? (detail.messages.length === 0 && !running ? <EmptyState onCreateSession={onCreateSession} /> : <div className="webui2-conversation-stream" key={selectedRef} ref={stream} onScroll={(event) => { const node = event.currentTarget; follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < FOLLOW_SCROLL_THRESHOLD; setShowJump(!follow.current); }}>
        {hiddenCount ? <button type="button" className="webui2-show-earlier" onClick={showEarlier}><History size={14} />{copy.earlier} ({hiddenCount})</button> : null}
        {messages.slice(-visibleCount).map((message) => {
          const run = runsByID.get(String(message.taskID));
          const activeRun = running && !isTerminalStatus(message.status) && (run ? !isTerminalStatus(run.status) : message.taskID === latest?.taskID);
          const elapsedMs = activeRun ? elapsedMilliseconds(run?.startedAt || message.createdAt, now) : message.durationMs;
          return <ConversationMessage identity={identity} key={message.id} message={message} thinkingMode={thinkingMode} animate={display.animating.has(message.id)} live={activeRun && message.id === latest?.id} now={now} elapsedMs={elapsedMs} onDisplayComplete={() => completeDisplay(message.id)} onRetry={onRetry} retryDisabled={running || detail.source === "local"} responseContent={responseByTask.get(message.taskID ?? message.id)?.join("\n\n")} />;
        })}
        {running || connection ? <div className="webui2-conversation-status" role="status" aria-live="polite">{running ? <span><i aria-hidden="true" />{stage}</span> : null}{connection ? <span>{connection}</span> : null}</div> : null}
      </div>) : <p role="status">{t("webui2.loading")}</p>}
    </div>
    <div className="webui2-conversation-composer-slot">{showJump ? <button type="button" className="webui2-jump-latest" title={copy.jump} aria-label={copy.jump} onClick={() => { follow.current = true; setShowJump(false); if (stream.current) stream.current.scrollTop = stream.current.scrollHeight; }}><ArrowDown size={16} /></button> : null}{composer}</div>
  </section>;
}
