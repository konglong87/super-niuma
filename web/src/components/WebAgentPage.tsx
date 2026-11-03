import {
  Bot,
  CheckCircle2,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  FileDiff,
  FileText,
  Folder,
  Home,
  History,
  LayoutPanelLeft,
  MessageSquarePlus,
  Moon,
  PanelRight,
  Play,
  RefreshCcw,
  Search,
  Shield,
  Square,
  Sparkles,
  Sun,
  Terminal,
  X
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ClipboardEvent, type CSSProperties, type DragEvent, type KeyboardEvent, } from "react";
import { CommandPalette, type Command } from "./AgentCommandPalette";
import { defaultIdentity, saveIdentity } from "../lib/config";
import * as agentAPI from "../lib/api";
import {
  cancelAgentTask,
  createAgentTask,
  createTenantSession,
  getAgentTask,
  getGlobalSettings,
  getWebAgentConversation,
  getStatus,
  listAgentTaskEvents,
  listAgentSlashCommands,
  listAgentTasks,
  listModels,
  listProviders,
  listTenantSessions,
  listWebAgentConversations,
  presignMobileAttachment,
  resolveAgentTaskPermission,
  sendAgentTaskMessage,
  uploadAttachmentBinary,
  validateAgentWorkspace
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import { readProductStorage } from "../lib/productStorage";
import { clearConversationDraft, readConversationDraft, writeConversationDraft } from "../lib/conversationDraft";
import { TASK_STATUS, } from "../lib/constants";
import { useConversationScroll } from "../hooks/useConversationScroll";
import { useAgentTaskStream } from "../hooks/useAgentTaskStream";
import type { StreamState } from "../hooks/useAgentTaskStream";
import { useTaskPolling } from "../hooks/useTaskPolling";
import { AGENT_EVENT_PAGE_LIMIT, mergeEvents, terminalStatuses, upsertTask } from "../lib/agentEvents";
import { formatDuration, } from "../lib/messages";
import { appendStreamText } from "../lib/streamText";
import { dataURLPayload, fileToDataURL, imageFilesFromClipboard, MAX_IMAGE_ATTACHMENTS, selectImageFiles, sha256File } from "../lib/imageAttachments";
import { NewSessionModal } from "./agent/NewSessionModal";
import { Composer, slashCommandDisplayName, type PendingImage, type SlashSuggestionState } from "./agent/Composer";
import { MessageList } from "./agent/MessageList";
import { imageWorkbenchHref } from "./image/ImageGenerationWorkbench";
import { agentCopy } from "./agent/copy";
import type { AgentCopy } from "./agent/copy";
export { StreamingAssistantContent } from "./agent/markdown";
export type { AgentCopy } from "./agent/copy";
import type { AgentSlashCommand, AgentTaskAttachment, AgentTaskEventRecord, AgentTaskRecord, AgentUserQuestion, AgentWorkspaceValidation, IdentityConfig, ImageArtifact, PendingInputRecord, ProviderOption, TenantSession, WebAgentConversation, WebAgentConversationDetail } from "../lib/types";
import { movePendingInputUp as reorderPendingInputUp, removePendingInput, replacePendingInput, sortPendingInputs } from "../lib/pendingInputs";
import { claimUserQuestionToolID, parseUserQuestionInput, questionChoices, type UserQuestionToolCandidate } from "../lib/userQuestion";

type Props = {
  identity: IdentityConfig;
  onIdentityChange?: (identity: IdentityConfig) => void;
  onStatus: (message: string) => void;
};

function pendingApiFunction(name: string): any {
  try {
    return (agentAPI as unknown as Record<string, unknown>)[name];
  } catch {
    return undefined;
  }
}

type RightTab = "progress" | "runs" | "files" | "permissions" | "trace" | "usage";
type ParsedPayload = Record<string, unknown>;
export type ComposerState = "idle" | "ready" | "sending" | "running" | "cancelling";
export type PromptMode = "chat" | "code";
type PendingMessageStatus = "sending" | "sent" | "failed";
type ConversationLoadToken = { generation: number; taskID: number; identityKey: string };
export type WorkspaceValidationState = "idle" | "checking" | "ready" | "error";
type ToolActivityStatus = "running" | "done" | "error";
type ToolActivity = {
  id: string;
  name: string;
  status: ToolActivityStatus;
  startedAt: string;
  finishedAt: string;
  elapsed: string;
  preview: string;
  evidenceSource: string;
  evidenceProvenance: string;
  evidenceArtifacts: string;
  turn: number;
};
type SubAgentStatus = "running" | "done" | "error" | "cancelled";
type SubAgentProgress = {
  taskID: string;
  agent: string;
  model: string;
  description: string;
  status: SubAgentStatus;
  detail: string;
  turn: number;
  toolCalls: number;
  lastTool: string;
  tokens: string;
  durationMS: number;
};
type PermissionRequestView = {
  requestID: string;
  taskID: number;
  toolName: string;
  reason: string;
  input: string;
  status: "pending" | "allowed" | "denied";
  decision: string;
  createdAt: string;
  resolvedAt: string;
};
type AgentUsageView = {
  contextPercent: number;
  contextLength: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  cacheCreationTokens: number;
  cacheReadTokens: number;
  cacheCreationEphemeral1hTokens: number;
  cacheCreationEphemeral5mTokens: number;
  serviceTier: string;
  inferenceGeo: string;
  speed: string;
  initialMessages: number;
  toolCalls: number;
  stopReason: string;
  durationMS: number;
};
type RuntimeSummary = {
  provider: string;
  model: string;
  sandbox: string;
  tools: string;
  mcp: string;
  goal: string;
};
type WebAgentSessionView = {
  id: string;
  sessionID: number;
  sessionKey: string;
  title: string;
  cwd: string;
  workspaceName: string;
  latestTask: AgentTaskRecord;
  tasks: AgentTaskRecord[];
  status: string;
  updatedAt: string;
};
type WebAgentConversationSource = {
  tasks: AgentTaskRecord[];
  sessions: TenantSession[];
  views: WebAgentSessionView[];
};
const DEFAULT_CONTEXT_LENGTH = 200_000;
// 长会话窗口化渲染:默认只渲染最近 N 条消息,顶部按钮按步长加载更早。
const MESSAGE_WINDOW_SIZE = 80;
const MESSAGE_WINDOW_STEP = 80;
const DEFAULT_THINKING_MODE: ThinkingMode = "summary";
export type ThinkingMode = "full" | "summary" | "hidden";
export type ThinkingMessageStatus = "streaming" | "completed" | "failed" | "cancelled" | "timed_out";
const workspaceExpansionStorageKey = "niuma-webui.agent.workspace-expansion.v1";
const themeStorageKey = "niuma-webui.agent.theme.v1";
const webAgentDraftSurface = "web-agent";

type ThemeMode = "light" | "dark";

function loadInitialTheme(): ThemeMode {
  if (typeof window === "undefined") {
    return "light";
  }
  try {
	    const stored = readProductStorage(themeStorageKey);
    if (stored === "dark" || stored === "light") {
      return stored;
    }
  } catch {
    // fall through to system preference
  }
  return typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
const paneWidthStorageKey = "niuma-webui.agent.pane-widths.v1";
const LEFT_PANE_MIN = 232;
const LEFT_PANE_MAX = 460;
const LEFT_PANE_DEFAULT = 296;
const RIGHT_PANE_MIN = 288;
const RIGHT_PANE_MAX = 520;
const RIGHT_PANE_DEFAULT = 352;

function clampWidth(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.round(value)));
}

function loadPaneWidths(): { left: number; right: number } {
  const fallback = { left: LEFT_PANE_DEFAULT, right: RIGHT_PANE_DEFAULT };
  try {
	    const raw = readProductStorage(paneWidthStorageKey);
    if (!raw) {
      return fallback;
    }
    const parsed = JSON.parse(raw) as { left?: number; right?: number };
    return {
      left: clampWidth(Number(parsed.left) || LEFT_PANE_DEFAULT, LEFT_PANE_MIN, LEFT_PANE_MAX),
      right: clampWidth(Number(parsed.right) || RIGHT_PANE_DEFAULT, RIGHT_PANE_MIN, RIGHT_PANE_MAX)
    };
  } catch {
    return fallback;
  }
}



export function WebAgentPage({ identity, onIdentityChange, onStatus }: Props) {
  const { language, setLanguage } = useI18n();
  const copy = agentCopy[language];
  const [tasks, setTasks] = useState<AgentTaskRecord[]>([]);
  const [sessions, setSessions] = useState<TenantSession[]>([]);
  const [events, setEvents] = useState<AgentTaskEventRecord[]>([]);
  const [conversationTimelineEvents, setConversationTimelineEvents] = useState<AgentTaskEventRecord[]>([]);
  const [conversationDetail, setConversationDetail] = useState<WebAgentConversationDetail | null>(null);
  const [selectedTaskId, setSelectedTaskId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [leftOpen, setLeftOpen] = useState(() => (typeof window === "undefined" ? true : window.innerWidth >= 760));
  const [workspaceExpansion, setWorkspaceExpansion] = useState<Record<string, boolean>>(() => readStoredRecord(workspaceExpansionStorageKey));
  const [rightOpen, setRightOpen] = useState(() => (typeof window === "undefined" ? true : window.innerWidth >= 1120));
  const [paneWidths, setPaneWidths] = useState(() => (typeof window === "undefined" ? { left: LEFT_PANE_DEFAULT, right: RIGHT_PANE_DEFAULT } : loadPaneWidths()));
  const [paneDrag, setPaneDrag] = useState<{ pane: "left" | "right"; startX: number; startWidth: number } | null>(null);
  const [rightTab, setRightTab] = useState<RightTab>("progress");
  const [newSessionOpen, setNewSessionOpen] = useState(false);
  const [workspaceInput, setWorkspaceInput] = useState("");
  const [validatedWorkspace, setValidatedWorkspace] = useState<AgentWorkspaceValidation | null>(null);
  const [workspaceValidationState, setWorkspaceValidationState] = useState<WorkspaceValidationState>("idle");
  const [workspaceError, setWorkspaceError] = useState("");
  const [workspaceSearch, setWorkspaceSearch] = useState("");
  const [sessionTitle, setSessionTitle] = useState("");
  const [sessionPrompt, setSessionPrompt] = useState("");
  const [composerText, setComposerText] = useState("");
  const draftHydratedKeyRef = useRef("");
  const [slashSuggestions, setSlashSuggestions] = useState<AgentSlashCommand[]>([]);
  const [slashSelected, setSlashSelected] = useState(0);
  const [slashState, setSlashState] = useState<SlashSuggestionState>("idle");
  const [slashError, setSlashError] = useState("");
  const [composerState, setComposerState] = useState<ComposerState>("idle");
  const [streamState, setStreamState] = useState<StreamState>("idle");
  const [typewriterTaskIDs, setTypewriterTaskIDs] = useState<Set<number>>(() => new Set());
  const [permissionMode, setPermissionMode] = useState("ask");
  const [effort, setEffort] = useState("medium");
  const [model, setModel] = useState(identity.model);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>([]);
  const [provider, setProvider] = useState(identity.provider ?? "");
  const [serverModel, setServerModel] = useState("");
  const [promptMode, setPromptMode] = useState<PromptMode>("code");
  const [newSessionPromptMode, setNewSessionPromptMode] = useState<PromptMode>("code");
  const [pendingMessages, setPendingMessages] = useState<PendingUserMessage[]>([]);
  const [pendingInputs, setPendingInputs] = useState<PendingInputRecord[]>([]);
  const [pendingInputQueueEnabled, setPendingInputQueueEnabled] = useState(true);
  const [pendingImages, setPendingImages] = useState<PendingImage[]>([]);
  const pendingImagesRef = useRef<PendingImage[]>([]);
  const [activityTick, setActivityTick] = useState(0);
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  const workspaceValidationSeqRef = useRef(0);
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [messageWindow, setMessageWindow] = useState(MESSAGE_WINDOW_SIZE);
  const [theme, setTheme] = useState<ThemeMode>(loadInitialTheme);
  // Hide until the preference is loaded so a configured opt-out cannot flash
  // reasoning content during the initial settings request.
  const [thinkingMode, setThinkingMode] = useState<ThinkingMode>("hidden");
  const [expandedThinkingKeys, setExpandedThinkingKeys] = useState<Record<string, boolean>>({});
  const conversationSelectionRef = useRef<{ generation: number; taskID: number | null; identityKey: string }>({ generation: 0, taskID: null, identityKey: "" });
  const refreshOwnershipRef = useRef<{ generation: number; identityKey: string }>({ generation: 0, identityKey: "" });

  const sessionViews = useMemo(() => buildWebAgentSessionViews(tasks, sessions), [sessions, tasks]);
  // 相对时间标签只随数据变化重算;否则输入框每敲一个字都会重渲染,
  // 左栏时间会跟着实时跳动(bug)。
  const sessionTimeLabels = useMemo(() => new Map(sessionViews.map((view) => [view.id, relativeTime(view.updatedAt)])), [sessionViews]);
  const selectedSessionView = useMemo(() => sessionViews.find((session) => session.latestTask.id === selectedTaskId || session.tasks.some((task) => task.id === selectedTaskId)) || null, [selectedTaskId, sessionViews]);
  const selectedTask = useMemo(() => tasks.find((task) => task.id === selectedTaskId) || selectedSessionView?.latestTask || null, [selectedSessionView, selectedTaskId, tasks]);
  const thinkingSessionKey = selectedSessionView?.id || (selectedTaskId ? `task:${selectedTaskId}` : "none");
  const activity = useMemo(() => summarizeActivity(selectedTask, events), [activityTick, events, selectedTask]);
  const runtimeSummary = useMemo(() => summarizeRuntime(selectedTask, events, { ...identity, model }, permissionMode, providerOptions), [events, identity, model, permissionMode, providerOptions, selectedTask]);
  const selectedTaskPromptMode = useMemo(() => normalizePromptMode(parsePayload(selectedTask?.metadata_json).prompt_mode, "code"), [selectedTask]);
  const conversationEvents = useMemo(() => conversationTimelineEvents.filter(isConversationEvent), [conversationTimelineEvents]);
  const conversationMessages = useMemo(
    () => buildConversationMessages(selectedTask, conversationEvents, providerOptions, selectedSessionView?.tasks || []),
    [conversationEvents, providerOptions, selectedSessionView?.tasks, selectedTask]
  );
  const visibleConversationMessages = useMemo(
    () => (thinkingMode === "hidden" ? conversationMessages.filter((message) => message.kind !== "thinking") : conversationMessages),
    [conversationMessages, thinkingMode]
  );
  const displayedMessages = useMemo(
    () => mergePendingUserMessages(visibleConversationMessages, pendingMessages.filter((message) => message.taskID === selectedTaskId)),
    [pendingMessages, selectedTaskId, visibleConversationMessages]
  );
  const hiddenMessageCount = Math.max(0, displayedMessages.length - messageWindow);
  const visibleMessages = hiddenMessageCount > 0 ? displayedMessages.slice(hiddenMessageCount) : displayedMessages;
  const latestAssistantMessageId = [...visibleMessages].reverse().find((message) => message.role === "assistant")?.id;
  const workspaces = useMemo(() => collectWorkspaces(sessionViews, validatedWorkspace), [sessionViews, validatedWorkspace]);
  const taskIdsSignature = useMemo(() => tasks.map((task) => `${task.id}:${task.status || ""}:${parsePayload(task.metadata_json).continuation_of_task_id || ""}`).join("|"), [tasks]);
  const selectedWorkspace = validatedWorkspace?.cwd || activity.cwd || workspaces[0]?.cwd || "";
  const draftSessionKey = selectedSessionView?.id || (selectedTaskId ? `task:${selectedTaskId}` : "");
  const draftWorkspace = selectedWorkspace || "unknown";
  const draftContextKey = draftSessionKey ? `${draftSessionKey}\u0000${draftWorkspace}` : "";
  const filteredSessionViews = useMemo(() => {
    const needle = workspaceSearch.trim().toLowerCase();
    if (!needle) {
      return sessionViews;
    }
    return sessionViews.filter((session) => sessionMatchesSearch(session, needle));
  }, [sessionViews, workspaceSearch]);
  const filteredWorkspaces = useMemo(() => {
    const needle = workspaceSearch.trim().toLowerCase();
    if (!needle) {
      return workspaces;
    }
    const matchedSessionCwds = new Set(filteredSessionViews.map((session) => session.cwd));
    return workspaces.filter((workspace) => workspace.cwd.toLowerCase().includes(needle) || workspace.name.toLowerCase().includes(needle) || matchedSessionCwds.has(workspace.cwd));
  }, [filteredSessionViews, workspaceSearch, workspaces]);
  const groupedSessions = useMemo(() => groupSessionsByWorkspace(filteredSessionViews, selectedWorkspace), [filteredSessionViews, selectedWorkspace]);
  const centerOnly = !leftOpen && !rightOpen;
  const running = selectedTask?.status === TASK_STATUS.running;
  const canSendToSelectedTask = Boolean(selectedTask && !running);
  const canQueuePendingInput = Boolean(selectedTaskId && running && pendingInputQueueEnabled);
  const sendDisabled = !selectedTaskId || (!canSendToSelectedTask && !canQueuePendingInput) || (composerText.trim() === "" && pendingImages.length === 0) || composerState === "sending" || composerState === "cancelling";
  const composerActionIsCancel = running || composerState === "cancelling";
  const tenantStorageMissing = loadError.includes("tenant storage is not configured");
  const canRestoreLocalTestIdentity = isLocalWebAgentTestIdentityError(identity, loadError);

  useEffect(() => {
    pendingImagesRef.current = pendingImages;
  }, [pendingImages]);

  useEffect(() => () => {
    for (const image of pendingImagesRef.current) {
      URL.revokeObjectURL(image.previewURL);
    }
  }, []);

  useEffect(() => {
    if (!draftSessionKey) {
      draftHydratedKeyRef.current = "";
      return;
    }
    const draft = readConversationDraft(webAgentDraftSurface, draftSessionKey, draftWorkspace);
    draftHydratedKeyRef.current = draftContextKey;
    setComposerText(draft?.text || "");
  }, [draftContextKey, draftSessionKey, draftWorkspace]);

  useEffect(() => {
    if (!draftContextKey || draftHydratedKeyRef.current !== draftContextKey) {
      return;
    }
    const timer = window.setTimeout(() => {
      writeConversationDraft({
        surface: webAgentDraftSurface,
        sessionKey: draftSessionKey,
        workspace: draftWorkspace,
        text: composerText,
        updatedAt: new Date().toISOString()
      });
    }, 350);
    return () => window.clearTimeout(timer);
  }, [composerText, draftContextKey, draftSessionKey, draftWorkspace]);

  useEffect(() => {
    if (!selectedTaskId) {
      setPendingInputs([]);
      setPendingInputQueueEnabled(true);
      return;
    }
    let cancelled = false;
    const listPending = pendingApiFunction("listPendingInputs");
    const getSettings = pendingApiFunction("getPendingInputQueueSettings");
    const itemsPromise = typeof listPending === "function" ? listPending(identity, selectedTaskId) : Promise.resolve([] as PendingInputRecord[]);
    const settingsPromise = typeof getSettings === "function" ? getSettings(identity, selectedTaskId) : Promise.resolve({ enabled: true });
    void Promise.all([itemsPromise, settingsPromise]).then(([items, settings]) => {
      if (cancelled) return;
      setPendingInputs(sortPendingInputs(items));
      setPendingInputQueueEnabled(settings.enabled);
    }).catch((err) => {
      if (!cancelled) onStatus(err instanceof Error ? err.message : String(err));
    });
    return () => { cancelled = true; };
  }, [identity, onStatus, selectedTaskId]);

  function restoreLocalTestIdentity() {
    const nextIdentity = {
      ...identity,
      tenantKey: defaultIdentity.tenantKey,
      userId: defaultIdentity.userId
    };
    saveIdentity(nextIdentity);
    onIdentityChange?.(nextIdentity);
  }
  function handleThinkingToggle(message: ConversationMessage, open: boolean) {
    const key = `${thinkingSessionKey}:${message.turn || 0}:${message.phase || message.id}`;
    setExpandedThinkingKeys((current) => ({ ...current, [key]: open }));
  }
  const liveToolRuns = useMemo(() => (running ? collectLiveToolRuns(conversationEvents) : []), [conversationEvents, running]);
  const slashPrefix = slashCommandPrefix(composerText);
  const slashSuggestionsActive = slashState === "ready" && slashSuggestions.length > 0;
  const nextStepSuggestions = useMemo(() => collectNextStepSuggestions(conversationTimelineEvents), [conversationTimelineEvents]);
  const visibleNextStepSuggestions = computeVisibleNextStepSuggestions(nextStepSuggestions, composerText, slashSuggestionsActive, running);
  const thinkingState = useMemo(() => summarizeThinkingState(copy, activity, composerState, streamState), [activity, composerState, copy, streamState]);
  const showThinkingPlaceholder = Boolean(thinkingMode !== "hidden" && selectedTask && shouldShowThinkingPlaceholder(displayedMessages, running, composerState, streamState));
  const {
    conversationScrollRef,
    setLatestMessageNode,
    showJumpToLatest,
    scrollToLatest,
    handleConversationScroll,
    armFollowLatest,
  } = useConversationScroll({
    selectedTaskId,
    messagesLength: displayedMessages.length,
    latestMessageContentLength: displayedMessages.at(-1)?.content.length,
    eventsLength: events.length,
    running,
    showThinkingPlaceholder,
    streamState,
  });
  const isWorkspaceExpanded = (cwd: string) => workspaceExpansion[cwd] ?? cwd === selectedWorkspace;

  function toggleWorkspaceExpanded(cwd: string) {
    setWorkspaceExpansion((current) => ({ ...current, [cwd]: !(current[cwd] ?? cwd === selectedWorkspace) }));
  }

  function openNewSession() {
    setNewSessionPromptMode("code");
    setNewSessionOpen(true);
  }

  function nudgePaneWidth(pane: "left" | "right", delta: number) {
    if (pane === "left") {
      setPaneWidths((current) => ({ ...current, left: clampWidth(current.left + delta, LEFT_PANE_MIN, LEFT_PANE_MAX) }));
    } else {
      setPaneWidths((current) => ({ ...current, right: clampWidth(current.right + delta, RIGHT_PANE_MIN, RIGHT_PANE_MAX) }));
    }
  }

  function renderPaneResizer(pane: "left" | "right") {
    const width = pane === "left" ? paneWidths.left : paneWidths.right;
    const min = pane === "left" ? LEFT_PANE_MIN : RIGHT_PANE_MIN;
    const max = pane === "left" ? LEFT_PANE_MAX : RIGHT_PANE_MAX;
    return (
      // biome-ignore lint/a11y/useSemanticElements: this is a draggable interactive resizer (drag + arrow-key resize), not a thematic break — <hr> cannot carry pointer/keyboard handlers or aria-value* state
      <button
        type="button"
        className={`agent-pane-resizer ${pane}${paneDrag?.pane === pane ? " dragging" : ""}`}
        role="separator"
        aria-orientation="vertical"
        aria-label={pane === "left" ? copy.resizeLeftRail : copy.resizeRightPanel}
        aria-valuemin={min}
        aria-valuemax={max}
        aria-valuenow={width}
        onPointerDown={(event) => {
          event.preventDefault();
          setPaneDrag({ pane, startX: event.clientX, startWidth: width });
        }}
        onKeyDown={(event) => {
          if (event.key === "ArrowLeft") {
            event.preventDefault();
            nudgePaneWidth(pane, pane === "left" ? -16 : 16);
          } else if (event.key === "ArrowRight") {
            event.preventDefault();
            nudgePaneWidth(pane, pane === "left" ? 16 : -16);
          }
        }}
      />
    );
  }

  useEffect(() => {
    writeStoredRecord(workspaceExpansionStorageKey, workspaceExpansion);
  }, [workspaceExpansion]);

  useEffect(() => {
    try {
      window.localStorage.setItem(paneWidthStorageKey, JSON.stringify(paneWidths));
    } catch {
      // ignore persistence failures (private mode, quota)
    }
  }, [paneWidths]);

  useEffect(() => {
    function handleKeyDown(event: globalThis.KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && (event.key === "k" || event.key === "K")) {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  useEffect(() => {
    let cancelled = false;
    getGlobalSettings(identity)
      .then((response) => {
        if (!cancelled) {
          setThinkingMode(resolveWebAgentThinkingMode(response.doc));
        }
      })
      .catch(() => {
        if (!cancelled) {
          setThinkingMode(DEFAULT_THINKING_MODE);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [identity]);

  useEffect(() => {
    let cancelled = false;
    listModels(identity)
      .then((models) => {
        if (!cancelled && models.length > 0) {
          setModelOptions(models);
        }
      })
      .catch(() => {
        // /v1/models unavailable — fall back to the server-configured model only
      });
    listProviders(identity)
      .then((providers) => {
        if (!cancelled) {
          setProviderOptions(providers);
        }
      })
      .catch(() => {
        // /v1/providers unavailable — fall back to the model picker
      });
    return () => {
      cancelled = true;
    };
  }, [identity]);

  // Models the server actually offers: configured default (from /health) + advertised list.
  // Never includes the stale frontend default, so an unserved model auto-corrects below.
  const servedModels = useMemo(() => {
    const merged: string[] = [];
    for (const candidate of [serverModel, ...modelOptions]) {
      const value = (candidate || "").trim();
      if (value && !merged.includes(value)) {
        merged.push(value);
      }
    }
    return merged;
  }, [serverModel, modelOptions]);

  // Correct the stale frontend default (e.g. gpt-5.5) to a served model exactly once,
  // so it never fights later session-driven or user-driven model changes.
  const modelCorrectedRef = useRef(false);
  useEffect(() => {
    if (servedModels.length === 0 || modelCorrectedRef.current) {
      return;
    }
    modelCorrectedRef.current = true;
    setModel((current) => (servedModels.includes(current) ? current : serverModel || servedModels[0]));
  }, [servedModels, serverModel]);

  // When switching to a session, reflect that session's model in the picker (and the
  // runtime top bar). Keyed on the session id + model string so a background refresh of
  // the same task does not clobber an unsent picker change.
  useEffect(() => {
    if (selectedTask?.model) {
      setModel(selectedTask.model);
    }
  }, [selectedTaskId, selectedTask?.model]);

  // Reflect the selected task's provider (persisted in its metadata) in the picker,
  // so continuing a session keeps showing which provider it runs on.
  const selectedTaskProvider = useMemo(() => stringFrom(parsePayload(selectedTask?.metadata_json).provider), [selectedTask]);
  useEffect(() => {
    if (selectedTaskProvider) {
      setProvider(selectedTaskProvider);
    }
  }, [selectedTaskId, selectedTaskProvider]);

  useEffect(() => {
    setMessageWindow(MESSAGE_WINDOW_SIZE);
  }, [selectedTaskId]);

  function chooseModel(next: string) {
    setModel(next);
    saveIdentity({ ...identity, model: next });
  }

  // Selecting a provider pins routing to that provider by name and drives the model
  // from its config, so two providers serving the same model stay distinguishable.
  function chooseProvider(next: string) {
    setProvider(next);
    const matched = providerOptions.find((option) => option.name === next);
    const nextModel = matched?.model || model;
    if (matched?.model) {
      setModel(matched.model);
    }
    saveIdentity({ ...identity, provider: next, model: nextModel });
  }

  // Routing is by provider name, so a persisted task's model must be the selected
  // provider's own model — otherwise model and provider can drift apart (e.g. a
  // continuation inheriting the parent model while the picker points elsewhere).
  function modelForProvider(fallbackModel: string) {
    return providerOptions.find((option) => option.name === provider)?.model || fallbackModel;
  }

  function applySuggestion(text: string) {
    setComposerText(text);
    requestAnimationFrame(() => composerRef.current?.focus());
  }

  function closeRailsOnMobile() {
    if (typeof window !== "undefined" && window.innerWidth <= 760) {
      setLeftOpen(false);
      setRightOpen(false);
    }
  }

  function toggleTheme() {
    setTheme((current) => {
      const next: ThemeMode = current === "dark" ? "light" : "dark";
      try {
        window.localStorage.setItem(themeStorageKey, next);
      } catch {
        // ignore persistence failures
      }
      return next;
    });
  }

  function showEarlierMessages() {
    const scroller = conversationScrollRef.current;
    const prevHeight = scroller?.scrollHeight ?? 0;
    const prevTop = scroller?.scrollTop ?? 0;
    setMessageWindow((window) => window + MESSAGE_WINDOW_STEP);
    // 加载更早消息后保持视口停在原位置,避免内容整体跳动。
    requestAnimationFrame(() => {
      const node = conversationScrollRef.current;
      if (node) {
        node.scrollTop = node.scrollHeight - prevHeight + prevTop;
      }
    });
  }

  useEffect(() => {
    if (!paneDrag) {
      return;
    }
    const drag = paneDrag;

    function handlePointerMove(event: PointerEvent) {
      const delta = event.clientX - drag.startX;
      if (drag.pane === "left") {
        setPaneWidths((current) => ({ ...current, left: clampWidth(drag.startWidth + delta, LEFT_PANE_MIN, LEFT_PANE_MAX) }));
      } else {
        setPaneWidths((current) => ({ ...current, right: clampWidth(drag.startWidth - delta, RIGHT_PANE_MIN, RIGHT_PANE_MAX) }));
      }
    }

    function handlePointerUp() {
      setPaneDrag(null);
    }

    document.body.classList.add("sidebar-resizing");
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", handlePointerUp, { once: true });
    return () => {
      document.body.classList.remove("sidebar-resizing");
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", handlePointerUp);
    };
  }, [paneDrag]);

  async function refresh(nextSelectedTaskId = selectedTaskId) {
    const identityKey = conversationIdentityKey(identity);
    const request = { generation: refreshOwnershipRef.current.generation + 1, identityKey };
    refreshOwnershipRef.current = request;
    const ownsRefresh = () => refreshOwnershipRef.current.generation === request.generation && refreshOwnershipRef.current.identityKey === request.identityKey;
    setLoading(true);
    const status = await getStatus(identity).catch((err) => {
      if (ownsRefresh()) {
        onStatus(err instanceof Error ? err.message : String(err));
      }
      return null;
    });
    if (!ownsRefresh()) {
      return;
    }
    const serverCwd = status?.workspace || "";
    if (status?.model) {
      setServerModel(status.model);
    }
    if (!validatedWorkspace && serverCwd) {
      setWorkspaceInput(serverCwd);
      setValidatedWorkspace({
        cwd: serverCwd,
        workspace_name: basename(serverCwd),
        exists: true,
        is_dir: true,
        is_git_repo: false
      });
      setWorkspaceValidationState("ready");
    }
    try {
      const { tasks: taskItems, sessions: sessionItems, views } = await loadWebAgentConversationSource(identity);
      if (!ownsRefresh()) {
        return;
      }
      setLoadError("");
      setTasks(taskItems);
      setSessions(sessionItems);
      const preferredSession = nextSelectedTaskId ? views.find((session) => session.latestTask.id === nextSelectedTaskId || session.tasks.some((task) => task.id === nextSelectedTaskId)) : null;
      const effectiveTaskId = preferredSession?.latestTask.id || (nextSelectedTaskId && taskItems.some((task) => task.id === nextSelectedTaskId) ? nextSelectedTaskId : views[0]?.latestTask.id || taskItems[0]?.id || null);
      setSelectedTaskId(effectiveTaskId);
      if (effectiveTaskId) {
        const identityKey = conversationIdentityKey(identity);
        const previous = conversationSelectionRef.current;
        const generation = previous.taskID === effectiveTaskId && previous.identityKey === identityKey ? previous.generation : previous.generation + 1;
        const token = { generation, taskID: effectiveTaskId, identityKey };
        conversationSelectionRef.current = token;
        if (previous.taskID !== effectiveTaskId || previous.identityKey !== identityKey) {
          setEvents([]);
          setConversationTimelineEvents([]);
          setConversationDetail(null);
        }
        await loadSelectedConversationDetail(taskItems, views, effectiveTaskId, token);
        if (!ownsRefresh()) {
          return;
        }
      } else {
        conversationSelectionRef.current = { generation: conversationSelectionRef.current.generation + 1, taskID: null, identityKey: conversationIdentityKey(identity) };
        setEvents([]);
        setConversationTimelineEvents([]);
        setConversationDetail(null);
      }
    } catch (err) {
      if (!ownsRefresh()) {
        return;
      }
      const message = err instanceof Error ? err.message : String(err);
      setLoadError(message);
      setTasks([]);
      setSessions([]);
      setEvents([]);
      setConversationTimelineEvents([]);
      setConversationDetail(null);
      setSelectedTaskId(null);
      onStatus(message);
    } finally {
      if (ownsRefresh()) {
        setLoading(false);
      }
    }
  }

  useEffect(() => {
    void refresh();
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  const streamHealth = useAgentTaskStream({
    identity,
    selectedTaskId,
    selectedTaskStatus: selectedTask?.status,
    selectedTaskResultJSON: selectedTask?.result_json,
    loadError,
    events,
    streamErrorCopy: copy.streamError,
    onStatus,
    setStreamState,
    setEvents,
    setConversationTimelineEvents,
    setTasks,
    markTypewriterTask: (taskID) => setTypewriterTaskIDs((current) => addSetValue(current, taskID)),
    refresh,
  });
  useTaskPolling({
    identity,
    selectedTaskId,
    running,
    streamStaleCopy: copy.streamStale,
    onStatus,
    setEvents,
    setConversationTimelineEvents,
    setTasks,
    refresh,
    health: streamHealth,
  });

  useEffect(() => {
    const identityKey = conversationIdentityKey(identity);
    const previous = conversationSelectionRef.current;
    const generation = previous.taskID === selectedTaskId && previous.identityKey === identityKey ? previous.generation : previous.generation + 1;
    conversationSelectionRef.current = { generation, taskID: selectedTaskId, identityKey };
    if (previous.taskID !== selectedTaskId || previous.identityKey !== identityKey) {
      setEvents([]);
      setConversationTimelineEvents([]);
      setConversationDetail(null);
    }
    if (!selectedTaskId) {
      setEvents([]);
      setConversationTimelineEvents([]);
      setConversationDetail(null);
      setStreamState("idle");
      return;
    }
    let cancelled = false;
    loadSelectedConversationDetail(tasks, sessionViews, selectedTaskId, { generation, taskID: selectedTaskId, identityKey })
      .catch((err) => {
        if (!cancelled) {
          onStatus(err instanceof Error ? err.message : String(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, selectedTaskId, taskIdsSignature, onStatus]);

  async function loadSelectedConversationDetail(taskItems: AgentTaskRecord[], views: WebAgentSessionView[], taskID: number, token?: ConversationLoadToken) {
    const loadToken = token || { generation: conversationSelectionRef.current.generation, taskID, identityKey: conversationIdentityKey(identity) };
    const isCurrent = () => {
      const current = conversationSelectionRef.current;
      return current.generation === loadToken.generation && current.taskID === loadToken.taskID && current.identityKey === loadToken.identityKey;
    };
    const view = views.find((session) => session.tasks.some((task) => task.id === taskID)) || null;
    try {
      if (view?.id) {
        const detail = await getWebAgentConversation(identity, view.id, { limit: 100, eventLimit: AGENT_EVENT_PAGE_LIMIT });
        const detailTasks = mergeConversationTasks(detail);
        if (detailTasks.some((task) => task.id === taskID)) {
          if (!isCurrent()) {
            return;
          }
          const nextTasks = mergeTasks(taskItems, detailTasks);
          const selectedEvents = (detail.events || []).filter((event) => event.task_id === taskID);
          setConversationDetail(detail);
          setTasks(nextTasks);
          setEvents((current) => mergeEvents(current, selectedEvents));
          setConversationTimelineEvents((current) => mergeEvents(current, detail.events || []));
          return;
        }
      }
    } catch (err) {
      if (!isWebAgentConversationFallbackError(err)) {
        throw err;
      }
    }
    const selectedEvents = await listAgentTaskEvents(identity, taskID, AGENT_EVENT_PAGE_LIMIT);
    const timelineEvents = await loadConversationTimelineEvents(taskItems, taskID);
    if (!isCurrent()) {
      return;
    }
    setConversationDetail(null);
    setEvents((current) => mergeEvents(current, selectedEvents));
    setConversationTimelineEvents((current) => mergeEvents(current, timelineEvents));
  }

  useEffect(() => {
    if (!running && composerState !== "sending" && streamState !== "connecting" && streamState !== "live") {
      return;
    }
    const timer = window.setInterval(() => setActivityTick((tick) => tick + 1), 1000);
    return () => window.clearInterval(timer);
  }, [composerState, running, streamState]);

  useEffect(() => {
    if (!selectedTaskId) {
      setComposerState("idle");
    } else if (running) {
      setComposerState((current) => (current === "sending" ? current : "running"));
    } else {
      setComposerState((current) => (current === "sending" ? current : "ready"));
    }
  }, [running, selectedTaskId]);

  useEffect(() => {
    if (slashPrefix === null || composerState === "sending" || composerState === "cancelling") {
      setSlashSuggestions([]);
      setSlashSelected(0);
      setSlashState("idle");
      setSlashError("");
      return;
    }
    let cancelled = false;
    setSlashState("loading");
    setSlashError("");
    listAgentSlashCommands(identity, selectedWorkspace, slashPrefix, 8)
      .then((items) => {
        if (cancelled) {
          return;
        }
        setSlashSuggestions(items);
        setSlashSelected(0);
        setSlashState("ready");
      })
      .catch((err) => {
        if (cancelled) {
          return;
        }
        setSlashSuggestions([]);
        setSlashSelected(0);
        setSlashState("error");
        setSlashError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [composerState, identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, selectedWorkspace, slashPrefix]);

  useEffect(() => {
    if (!selectedTask || newSessionOpen) {
      return;
    }
    const metadata = parsePayload(selectedTask.metadata_json);
    setPromptMode(normalizePromptMode(metadata.prompt_mode, "code"));
    const nextPermissionMode = stringFrom(metadata.permission_mode);
    if (nextPermissionMode) {
      setPermissionMode(nextPermissionMode);
    }
    const nextEffort = stringFrom(metadata.effort);
    if (nextEffort) {
      setEffort(nextEffort);
    }
  }, [newSessionOpen, selectedTask?.id, selectedTask?.metadata_json]);

  useEffect(() => {
    if (pendingMessages.length === 0 || conversationMessages.length === 0) {
      return;
    }
    setPendingMessages((current) => current.filter((pending) => !conversationMessages.some((message) => message.role === "user" && message.content === pending.content)));
  }, [conversationMessages, pendingMessages.length]);

  async function loadConversationTimelineEvents(taskItems: AgentTaskRecord[], taskID: number) {
    const chain = getConversationChain(taskItems, taskID);
    if (chain.length === 0) {
      return [];
    }
    const groups = await Promise.all(chain.map((task) => listAgentTaskEvents(identity, task.id, AGENT_EVENT_PAGE_LIMIT)));
    return mergeEvents([], groups.flat());
  }

  useEffect(() => {
    const cwd = workspaceInput.trim();
    if (!cwd) {
      workspaceValidationSeqRef.current += 1;
      setValidatedWorkspace(null);
      setWorkspaceError("");
      setWorkspaceValidationState("idle");
      return;
    }
    if (validatedWorkspace?.cwd === cwd) {
      return;
    }
    const timer = window.setTimeout(() => {
      void validateWorkspaceInput(cwd, { source: "auto" }).catch(() => undefined);
    }, 400);
    return () => window.clearTimeout(timer);
  }, [workspaceInput, validatedWorkspace?.cwd]);

  async function validateWorkspaceInput(cwd = workspaceInput, options: { source?: "auto" | "manual" | "create" } = {}) {
    const clean = cwd.trim();
    const seq = workspaceValidationSeqRef.current + 1;
    workspaceValidationSeqRef.current = seq;
    setWorkspaceError("");
    if (!clean) {
      setValidatedWorkspace(null);
      setWorkspaceValidationState("idle");
      throw new Error(copy.chooseWorkspaceFirst);
    }
    setWorkspaceValidationState("checking");
    try {
      const workspace = await validateAgentWorkspace(identity, clean);
      if (workspaceValidationSeqRef.current !== seq) {
        return workspace;
      }
      setValidatedWorkspace(workspace);
      setWorkspaceInput(workspace.cwd);
      setWorkspaceValidationState("ready");
      if (options.source !== "auto") {
        onStatus(`${copy.workspaceReady}: ${workspace.cwd}`);
      }
      return workspace;
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (workspaceValidationSeqRef.current !== seq) {
        throw err;
      }
      setValidatedWorkspace(null);
      setWorkspaceError(message);
      setWorkspaceValidationState("error");
      onStatus(message);
      throw err;
    }
  }

  function chooseWorkspace(cwd: string) {
    setWorkspaceInput(cwd);
    setWorkspaceExpansion((current) => ({ ...current, [cwd]: true }));
    void validateWorkspaceInput(cwd, { source: "manual" }).catch(() => undefined);
  }

  async function handleCreateSession() {
    const cwd = validatedWorkspace?.cwd || workspaceInput.trim();
    if (!cwd) {
      setWorkspaceError(copy.chooseWorkspaceFirst);
      setWorkspaceValidationState("error");
      return;
    }
    let workspace = validatedWorkspace;
    if (!workspace || workspace.cwd !== cwd) {
      try {
        workspace = await validateWorkspaceInput(cwd, { source: "create" });
      } catch {
        return;
      }
    }
    if (!workspace) {
      setWorkspaceError(copy.chooseWorkspaceFirst);
      setWorkspaceValidationState("error");
      return;
    }
    try {
      const traceID = makeWebAgentTraceID();
      const now = new Date().toISOString();
      const title = sessionTitle.trim() || `Web Agent - ${workspace.workspace_name}`;
      const sessionKey = `web-agent-${crypto.randomUUID()}`;
      const sessionModel = modelForProvider(model);
      const sessionID = await createTenantSession(identity, {
        session_key: sessionKey,
        title,
        status: "active",
        model: sessionModel,
        cwd: workspace.cwd,
        metadata_json: JSON.stringify({
          source: "webui-agent",
          web_agent_session: true,
          trace_id: traceID,
          cwd: workspace.cwd,
          workspace_name: workspace.workspace_name,
          created_at: now
        })
      });
      const id = await createAgentTask(identity, {
        parent_session_id: sessionID,
        agent_name: "web-agent",
        description: title,
        prompt: sessionPrompt.trim(),
        status: sessionPrompt.trim() ? TASK_STATUS.running : "ready",
        model: sessionModel,
        trace_id: traceID,
        metadata_json: {
          source: "webui-agent",
          trace_id: traceID,
          run_trace_id: traceID,
          web_agent_session_id: sessionID,
          web_agent_session_key: sessionKey,
          run_index: 1,
          cwd: workspace.cwd,
          workspace_name: workspace.workspace_name,
          permission_mode: permissionMode,
          effort,
          prompt_mode: newSessionPromptMode,
          provider,
          created_at: now
        }
      });
      setNewSessionOpen(false);
      setSessionTitle("");
      setSessionPrompt("");
      await refresh(id);
      onStatus(`${copy.createdSession} ${id}`);
      if (sessionPrompt.trim()) {
        setComposerText(sessionPrompt.trim());
        await handleSendForTask(id, sessionPrompt.trim(), traceID);
      }
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSend() {
    const content = composerText.trim();
    if (content === "/image" || content.startsWith("/image ")) {
      const prompt = content.slice("/image".length).trim();
      window.location.href = imageWorkbenchHref(identity, selectedSessionView?.sessionID, prompt);
      return;
    }
    if (!selectedTaskId || (composerText.trim() === "" && pendingImages.length === 0) || composerState === "sending" || composerState === "cancelling") {
      return;
    }
    if (running) {
      if (!pendingInputQueueEnabled) {
        onStatus(copy.queueDisabled);
        return;
      }
      await handleQueuePendingInput(selectedTaskId, composerText.trim());
      return;
    }
    if (!canSendToSelectedTask) {
      return;
    }
    if (selectedTask && terminalStatuses.has(selectedTask.status || "")) {
      await handleContinuationSend(selectedTask, content, pendingImages);
      return;
    }
    await handleSendForTask(selectedTaskId, content, "", pendingImages);
  }

  async function handleQueuePendingInput(taskID: number, content: string) {
    if (!content && pendingImages.length === 0) return;
    setComposerState("sending");
    try {
      const attachments = await prepareAgentTaskAttachments(pendingImages);
      const item = await pendingApiFunction("addPendingInput")?.(identity, taskID, {
        client_input_id: `webui-${crypto.randomUUID()}`,
        content,
        attachments
      });
      setPendingInputs((current) => replacePendingInput(current, item));
      setComposerText("");
      setPendingImages((current) => {
        for (const image of current) URL.revokeObjectURL(image.previewURL);
        return [];
      });
      clearConversationDraft(webAgentDraftSurface, draftSessionKey, draftWorkspace);
      onStatus(`${copy.pendingInputs} #${item.sequence}`);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    } finally {
      setComposerState("ready");
    }
  }

  async function handleMovePendingInputUp(item: PendingInputRecord) {
    if (!selectedTaskId || item.status !== "queued") return;
    setPendingInputs((current) => reorderPendingInputUp(current, item.id));
    try {
      const updated = await pendingApiFunction("movePendingInputUp")?.(identity, selectedTaskId, item.id);
      setPendingInputs((current) => replacePendingInput(current, updated));
    } catch (err) {
      const listPending = pendingApiFunction("listPendingInputs");
      if (typeof listPending === "function") {
        try { setPendingInputs(sortPendingInputs(await listPending(identity, selectedTaskId))); } catch { /* keep optimistic order */ }
      }
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleEditPendingInput(item: PendingInputRecord) {
    if (!selectedTaskId || item.status === "running") return;
    const content = window.prompt(copy.editPendingInput, item.content);
    if (content === null || content.trim() === "") return;
    try {
      const updated = await pendingApiFunction("updatePendingInput")?.(identity, selectedTaskId, item.id, { content });
      setPendingInputs((current) => replacePendingInput(current, updated));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleDirectionPendingInput(item: PendingInputRecord) {
    if (!selectedTaskId || item.status === "running") return;
    const direction = window.prompt(copy.adjustDirection, item.direction || "");
    if (direction === null) return;
    try {
      const updated = await pendingApiFunction("updatePendingInput")?.(identity, selectedTaskId, item.id, { direction });
      setPendingInputs((current) => replacePendingInput(current, updated));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleDeletePendingInput(item: PendingInputRecord) {
    if (!selectedTaskId || item.status === "running") return;
    try {
      await pendingApiFunction("cancelPendingInput")?.(identity, selectedTaskId, item.id);
      setPendingInputs((current) => removePendingInput(current, item.id));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRetryPendingInput(item: PendingInputRecord) {
    if (!selectedTaskId || item.status !== "failed") return;
    try {
      const updated = await pendingApiFunction("retryPendingInput")?.(identity, selectedTaskId, item.id);
      setPendingInputs((current) => replacePendingInput(current, updated));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSideChatPendingInput(item: PendingInputRecord) {
    if (!selectedTaskId) return;
    try {
      const result = await pendingApiFunction("createPendingInputSideChat")?.(identity, selectedTaskId, item.id);
      setRightOpen(true);
      setRightTab("runs");
      await refresh(result.task_id);
      setSelectedTaskId(result.task_id);
      onStatus(`${copy.openPendingInputSideChat}: ${result.session_id}`);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleTogglePendingInputQueue() {
    if (!selectedTaskId) return;
    try {
      const result = await pendingApiFunction("setPendingInputQueueEnabled")?.(identity, selectedTaskId, !pendingInputQueueEnabled);
      setPendingInputQueueEnabled(result.enabled);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  function handleAddImages(files: File[]) {
    const available = Math.max(0, MAX_IMAGE_ATTACHMENTS - pendingImages.length);
    const selected = selectImageFiles(files, available);
    if (selected.length === 0) {
      if (files.length > 0) {
        onStatus(copy.imageAttachmentInvalid);
      }
      return;
    }
    if (selected.length < files.length) {
      onStatus(copy.imageAttachmentLimit.replace("{n}", String(MAX_IMAGE_ATTACHMENTS)));
    }
    setPendingImages((current) => [
      ...current,
      ...selected.map((file) => ({
        id: `image-${Date.now()}-${crypto.randomUUID()}`,
        file,
        previewURL: URL.createObjectURL(file)
      }))
    ]);
  }

  function handleRemoveImage(id: string) {
    setPendingImages((current) => {
      const removed = current.find((image) => image.id === id);
      if (removed) {
        URL.revokeObjectURL(removed.previewURL);
      }
      return current.filter((image) => image.id !== id);
    });
  }

  function handleComposerPaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    const images = imageFilesFromClipboard(event.clipboardData.items);
    if (images.length === 0) {
      return;
    }
    event.preventDefault();
    handleAddImages(images);
  }

  function handleImageDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    handleAddImages(Array.from(event.dataTransfer.files));
  }

  async function prepareAgentTaskAttachments(images: PendingImage[]): Promise<AgentTaskAttachment[]> {
    const prepared: AgentTaskAttachment[] = [];
    for (const image of images) {
      const mediaType = image.file.type || "image/png";
      const dataURL = await fileToDataURL(image.file);
      const sha256 = await sha256File(image.file);
      if (identity.mobileJwt.trim() !== "") {
        try {
          const signed = await presignMobileAttachment(identity, {
            type: "image",
            media_type: mediaType,
            name: image.file.name,
            size_bytes: image.file.size,
            sha256
          });
          if (/^https?:\/\//i.test(signed.upload_url) && signed.attachment?.url) {
            await uploadAttachmentBinary(signed.upload_url, image.file, undefined, signed.headers);
            prepared.push({ ...signed.attachment, type: "image", media_type: mediaType, size_bytes: image.file.size, sha256 });
            continue;
          }
        } catch {
          // Local dev auth may not expose mobile presign or an object store.
        }
      }
      // The inline fallback keeps images usable when the browser only has the
      // WebUI API token or the local server has no object store configured.
      prepared.push({
        type: "image",
        media_type: mediaType,
        name: image.file.name,
        size_bytes: image.file.size,
        sha256,
        inline_data: dataURLPayload(dataURL)
      });
    }
    return prepared;
  }

  async function handleCopyMessage(message: ConversationMessage) {
    const text = message.content.trim();
    if (!text) {
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      setCopiedMessageId(message.id);
      window.setTimeout(() => setCopiedMessageId((current) => (current === message.id ? null : current)), 1500);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  function precedingUserPrompt(messageId: string): string {
    const index = displayedMessages.findIndex((message) => message.id === messageId);
    if (index < 0) {
      return "";
    }
    for (let cursor = index - 1; cursor >= 0; cursor -= 1) {
      if (displayedMessages[cursor].role === "user") {
        return displayedMessages[cursor].content.trim();
      }
    }
    return "";
  }

  async function handleRetryMessage(messageId: string) {
    if (!selectedTask || !selectedTaskId || running || composerState === "sending" || composerState === "cancelling") {
      return;
    }
    const prompt = precedingUserPrompt(messageId);
    if (!prompt) {
      return;
    }
    if (terminalStatuses.has(selectedTask.status || "")) {
      await handleContinuationSend(selectedTask, prompt);
    } else {
      await handleSendForTask(selectedTaskId, prompt);
    }
  }

  async function handleContinuationSend(task: AgentTaskRecord, content: string, images: PendingImage[] = pendingImages) {
    const metadata = parsePayload(task.metadata_json);
    const cwd = stringFrom(metadata.cwd) || activity.cwd || selectedWorkspace;
    const traceID = task.trace_id || stringFrom(metadata.run_trace_id) || stringFrom(metadata.trace_id) || makeWebAgentTraceID();
    const parentSessionID = numberFrom(task.parent_session_id) || selectedSessionView?.sessionID || numberFrom(metadata.web_agent_session_id);
    const pendingInputBaseTaskID = numberFrom(metadata.pending_input_base_task_id) || task.id;
    setComposerState("sending");
    armFollowLatest();
    try {
      const id = await createAgentTask(identity, {
        parent_session_id: parentSessionID || undefined,
        agent_name: task.agent_name || "web-agent",
        description: task.description || `Web Agent - ${basename(cwd)}`,
        prompt: content,
        status: TASK_STATUS.running,
        model: modelForProvider(task.model && (servedModels.length === 0 || servedModels.includes(task.model)) ? task.model : model),
        trace_id: traceID,
        metadata_json: {
          ...metadata,
          source: "webui-agent",
          trace_id: traceID,
          run_trace_id: traceID,
          web_agent_session_id: parentSessionID || undefined,
          web_agent_session_key: stringFrom(metadata.web_agent_session_key),
          run_index: getConversationChain(tasks, task.id).length + 1,
          cwd,
          workspace_name: stringFrom(metadata.workspace_name) || basename(cwd),
          permission_mode: permissionMode,
          effort,
          prompt_mode: promptMode,
          provider,
          continuation_of_task_id: task.id,
          pending_input_base_task_id: pendingInputBaseTaskID,
          continued_at: new Date().toISOString()
        }
      });
      setSelectedTaskId(id);
      await handleSendForTask(id, content, traceID, images);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
      setComposerState("ready");
    }
  }

  async function handleSendForTask(taskID: number, content: string, runTraceID = "", images: PendingImage[] = pendingImages) {
    const draftSession = draftSessionKey;
    const draftCwd = draftWorkspace;
    if (draftSession) {
      clearConversationDraft(webAgentDraftSurface, draftSession, draftCwd);
    }
    setComposerState("sending");
    setTypewriterTaskIDs((current) => addSetValue(current, taskID));
    armFollowLatest();
    const pendingID = `pending-${taskID}-${Date.now()}`;
    setPendingMessages((current) => [
      ...current,
      {
        id: pendingID,
        taskID,
        content,
        status: "sending",
        createdAt: new Date().toISOString()
      }
    ]);
    setComposerText("");
    requestAnimationFrame(() => scrollToLatest("auto"));
    let nextComposerState: ComposerState = "ready";
    try {
      const currentTask = tasks.find((task) => task.id === taskID);
      const metadata = parsePayload(currentTask?.metadata_json);
      const traceID = runTraceID || currentTask?.trace_id || stringFrom(metadata.run_trace_id) || stringFrom(metadata.trace_id) || makeWebAgentTraceID();
      const attachments = await prepareAgentTaskAttachments(images);
      await sendAgentTaskMessage(identity, taskID, {
        from_agent: "webui",
        content,
        trace_id: traceID,
        attachments
      });
      setPendingImages((current) => {
        const consumed = new Set(images.map((image) => image.id));
        for (const image of current) {
          if (consumed.has(image.id)) {
            URL.revokeObjectURL(image.previewURL);
          }
        }
        return current.filter((image) => !consumed.has(image.id));
      });
      setPendingMessages((current) => current.map((message) => (message.id === pendingID ? { ...message, status: "sent" } : message)));
      const task = await getAgentTask(identity, taskID);
      setTasks((current) => upsertTask(current, task));
      nextComposerState = task.status === "running" ? "running" : "ready";
      setStreamState(task.status === "running" ? "live" : "closed");
      requestAnimationFrame(() => scrollToLatest("auto"));
      onStatus(`${copy.sentMessage} ${taskID}`);
    } catch (err) {
      setTypewriterTaskIDs((current) => removeSetValue(current, taskID));
      setPendingMessages((current) => current.map((message) => (message.id === pendingID ? { ...message, status: "failed" } : message)));
      if (draftSession) {
        writeConversationDraft({
          surface: webAgentDraftSurface,
          sessionKey: draftSession,
          workspace: draftCwd,
          text: content,
          updatedAt: new Date().toISOString()
        });
      }
      onStatus(err instanceof Error ? err.message : String(err));
      nextComposerState = "ready";
    } finally {
      setComposerState((current) => (current === "sending" ? nextComposerState : current));
    }
  }

  async function handlePermissionResolve(request: PermissionRequestView, allowed: boolean) {
    try {
      await resolveAgentTaskPermission(identity, request.taskID, request.requestID, {
        allowed,
        reason: allowed ? "approved from Web Agent" : "denied from Web Agent"
      });
      const [task, latestEvents] = await Promise.all([
        getAgentTask(identity, request.taskID),
        listAgentTaskEvents(identity, request.taskID, AGENT_EVENT_PAGE_LIMIT)
      ]);
      setTasks((current) => upsertTask(current, task));
      setEvents(latestEvents);
      setConversationTimelineEvents((current) => mergeEvents(current.filter((event) => event.task_id !== request.taskID), latestEvents));
      onStatus(`${copy.permissionDecisionSaved} ${request.requestID}`);
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleCancel() {
    if (!selectedTaskId) {
      return;
    }
    const taskID = selectedTaskId;
    setComposerState("cancelling");
    try {
      await cancelAgentTask(identity, taskID);
      setTasks((current) => current.map((task) => (task.id === taskID ? { ...task, status: TASK_STATUS.cancelled, finished_at: new Date().toISOString() } : task)));
      setStreamState("closed");
      setComposerState("ready");
      await refresh(taskID);
      onStatus(`${copy.cancelledSession} ${taskID}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      onStatus(message);
      if (message.toLowerCase().includes("not running")) {
        await refresh(taskID);
        setComposerState("ready");
        return;
      }
      setComposerState(running ? "running" : "ready");
    }
  }

  function handleComposerKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.nativeEvent.isComposing) {
      return;
    }
    if (slashSuggestionsActive && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
      event.preventDefault();
      const delta = event.key === "ArrowDown" ? 1 : -1;
      setSlashSelected((current) => (current + delta + slashSuggestions.length) % slashSuggestions.length);
      return;
    }
    if (slashSuggestionsActive && event.key === "Tab") {
      event.preventDefault();
      applySlashSuggestion(slashSuggestions[slashSelected]);
      return;
    }
    if (slashSuggestionsActive && slashPrefix !== null && event.key === "Enter" && !event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey) {
      const selected = slashSuggestions[slashSelected];
      const selectedName = selected?.name || "";
      if (slashPrefix === "" || !stringsEqualSlashName(slashPrefix, selectedName)) {
        event.preventDefault();
        applySlashSuggestion(selected);
        return;
      }
    }
    if (event.key !== "Enter" || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) {
      return;
    }
    event.preventDefault();
    void handleSend();
  }

  function applySlashSuggestion(command: AgentSlashCommand | undefined) {
    const name = slashCommandDisplayName(command);
    if (!name) {
      return;
    }
    setComposerText(`${name} `);
    setSlashSuggestions([]);
    setSlashSelected(0);
    setSlashState("idle");
    setSlashError("");
  }

  const paletteCommands: Command[] = [
    { id: "new-session", section: copy.cmdkActions, label: copy.newSession, icon: <MessageSquarePlus size={15} />, keywords: "new create session 新建", run: openNewSession },
    { id: "image-generation", section: copy.cmdkActions, label: copy.imageGeneration, icon: <Sparkles size={15} />, keywords: "image generate redraw 图片 生成 编辑", run: () => { window.location.href = imageWorkbenchHref(identity, selectedSessionView?.sessionID); } },
    { id: "refresh", section: copy.cmdkActions, label: copy.refresh, icon: <RefreshCcw size={15} />, keywords: "reload 刷新", run: () => void refresh() },
    { id: "toggle-theme", section: copy.cmdkActions, label: copy.toggleTheme, icon: theme === "dark" ? <Sun size={15} /> : <Moon size={15} />, keywords: "theme dark light 主题 深色 浅色", run: toggleTheme },
    { id: "toggle-left", section: copy.cmdkActions, label: copy.toggleLeftRail, icon: <LayoutPanelLeft size={15} />, run: () => setLeftOpen((open) => !open) },
    { id: "toggle-right", section: copy.cmdkActions, label: copy.toggleRightPanel, icon: <PanelRight size={15} />, run: () => setRightOpen((open) => !open) },
    { id: "permissions", section: copy.cmdkActions, label: copy.permissions, icon: <Shield size={15} />, run: () => { setRightOpen(true); setRightTab("permissions"); } },
    { id: "back-webui", section: copy.cmdkActions, label: copy.backToWebui, icon: <Home size={15} />, run: () => { window.location.href = webuiHomeHref(identity); } },
    ...filteredWorkspaces.slice(0, 20).map((workspace) => ({
      id: `workspace-${workspace.cwd}`,
      section: copy.cmdkWorkspaces,
      label: workspace.name,
      hint: workspace.cwd,
      icon: <Folder size={15} />,
      keywords: workspace.cwd,
      run: () => chooseWorkspace(workspace.cwd)
    })),
    ...sessionViews.slice(0, 30).map((session) => ({
      id: `session-${session.latestTask.id}`,
      section: copy.cmdkSessions,
      label: session.title || session.latestTask.description || session.latestTask.agent_name || `Session ${session.latestTask.id}`,
      hint: session.status || "",
      icon: <Bot size={15} />,
      run: () => setSelectedTaskId(session.latestTask.id)
    }))
  ];

  return (
    <main
      className={`web-agent-page ${leftOpen ? "left-open" : "left-collapsed"} ${rightOpen ? "right-open" : "right-collapsed"} ${centerOnly ? "center-only" : ""}`}
      data-agent-theme={theme}
      style={{ "--agent-left-width": `${paneWidths.left}px`, "--agent-right-width": `${paneWidths.right}px` } as CSSProperties}
    >
      {leftOpen ? renderPaneResizer("left") : null}
      {rightOpen ? renderPaneResizer("right") : null}
      <aside className="web-agent-left" aria-label="Web Agent workspaces and sessions">
        <div className="web-agent-left-top">
          <button className="agent-icon-button" onClick={() => setLeftOpen((open) => !open)} title={leftOpen ? copy.hideNavigation : copy.showNavigation} type="button">
            {leftOpen ? <ChevronLeft size={17} /> : <ChevronRight size={17} />}
          </button>
          {leftOpen ? (
            <>
              <strong>Web Agent</strong>
              <div className="agent-language-switch" role="radiogroup" aria-label={copy.language}>
                <button className={language === "en" ? "active" : ""} onClick={() => setLanguage("en")} type="button">EN</button>
                <button className={language === "zh" ? "active" : ""} onClick={() => setLanguage("zh")} type="button">中文</button>
              </div>
            </>
          ) : null}
        </div>

        <nav className="agent-quick-actions" aria-label="Agent actions">
          <button onClick={openNewSession} title={copy.newSession} type="button">
            <MessageSquarePlus size={17} />
            {leftOpen ? <span>{copy.newSession}</span> : null}
          </button>
          <button title={copy.search} type="button">
            <Search size={17} />
            {leftOpen ? <span>{copy.search}</span> : null}
          </button>
          <button onClick={() => void refresh()} title={copy.refresh} type="button">
            <RefreshCcw size={17} />
            {leftOpen ? <span>{copy.refresh}</span> : null}
          </button>
          <button title={copy.permissions} type="button" onClick={() => { setRightOpen(true); setRightTab("permissions"); }}>
            <Shield size={17} />
            {leftOpen ? <span>{copy.permissions}</span> : null}
          </button>
          <button title={copy.backToWebui} type="button" onClick={() => { window.location.href = webuiHomeHref(identity); }}>
            <Home size={17} />
            {leftOpen ? <span>{copy.backToWebui}</span> : null}
          </button>
        </nav>

        {leftOpen ? (
          <section className="workspace-list" aria-label={copy.sessions}>
            <div className="workspace-section-head sessions-head">
              <span className="agent-section-title">
                <span>{copy.sessions}</span>
              </span>
              <span className="agent-section-count">{sessionViews.length}</span>
            </div>
            <div className="session-list-tools">
              <input className="workspace-filter" value={workspaceSearch} onChange={(event) => setWorkspaceSearch(event.target.value)} placeholder={copy.filterSessions} />
              <button className="agent-section-action" onClick={openNewSession} type="button">{copy.changeWorkspace}</button>
            </div>
            {workspaceError ? <p className="agent-inline-error">{workspaceError}</p> : null}
            {filteredWorkspaces.length === 0 ? <div className="agent-empty-mini">{copy.noRealWorkspaces}</div> : null}
            {filteredWorkspaces.map((workspace) => {
              const workspaceSessions = groupedSessions.get(workspace.cwd) || [];
              const expanded = isWorkspaceExpanded(workspace.cwd);
              return (
              <div key={workspace.cwd} className="workspace-group">
                <div className={workspace.cwd === selectedWorkspace ? "workspace-row active" : "workspace-row"}>
                  <button className="workspace-expander" aria-expanded={expanded} aria-label={expanded ? copy.collapseWorkspaceSessions : copy.expandWorkspaceSessions} onClick={(event) => { event.stopPropagation(); toggleWorkspaceExpanded(workspace.cwd); }} type="button">
                    <ChevronRight size={13} />
                  </button>
                  <button className="workspace-row-main" onClick={() => chooseWorkspace(workspace.cwd)} type="button">
                    <Folder size={15} />
                    <span>
                      <strong>{workspace.name}</strong>
                      <small>{workspaceSessions.length} {copy.sessionsLower}</small>
                    </span>
                  </button>
                </div>
                {expanded ? (
                  <div className="workspace-sessions">
                  {workspaceSessions.map((session) => (
                    <button key={session.id} className={session.tasks.some((task) => task.id === selectedTaskId) ? "session-row active" : "session-row"} onClick={() => { setSelectedTaskId(session.latestTask.id); closeRailsOnMobile(); }} type="button">
                      <span className={`agent-status-dot ${statusClass(session.status || "")}`} />
                      <span>
                        <strong>{session.title || session.latestTask.description || session.latestTask.agent_name || `Session ${session.latestTask.id}`}</strong>
                        <small>{session.status || "unknown"} · {sessionTimeLabels.get(session.id) || relativeTime(session.updatedAt)}</small>
                      </span>
                    </button>
                  ))}
                </div>
                ) : null}
              </div>
              );
            })}
          </section>
        ) : null}
      </aside>

      <section className="web-agent-center" aria-label="Web Agent conversation">
        <header className="agent-center-header">
          <div>
            <span>{activity.cwd || selectedWorkspace || copy.workspaceNotSelected}</span>
            <h1>{selectedTask?.description || selectedTask?.agent_name || copy.webAgentSession}</h1>
          </div>
          <div className="agent-header-actions">
            <button className="agent-icon-button" onClick={toggleTheme} title={copy.toggleTheme} type="button">
              {theme === "dark" ? <Sun size={17} /> : <Moon size={17} />}
            </button>
            <button className="agent-icon-button" onClick={() => setLeftOpen((open) => !open)} title={copy.toggleLeftRail} type="button">
              <LayoutPanelLeft size={17} />
            </button>
            <button className="agent-icon-button" onClick={() => setRightOpen((open) => !open)} title={copy.toggleRightPanel} type="button">
              <PanelRight size={17} />
            </button>
            <label className="agent-thinking-mode-control">
              <span>{copy.thinkingMode}</span>
              <select value={thinkingMode} onChange={(event) => setThinkingMode(normalizeThinkingMode(event.target.value))} aria-label={copy.thinkingMode}>
                <option value="full">{copy.thinkingModes.full}</option>
                <option value="summary">{copy.thinkingModes.summary}</option>
                <option value="hidden">{copy.thinkingModes.hidden}</option>
              </select>
            </label>
          </div>
        </header>
        {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .agent-runtime-topbar is a CSS flex layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
        <div className="agent-runtime-topbar" role="group" aria-label={copy.runtimeTopbar}>
          {[
            { label: copy.provider, value: runtimeSummary.provider, always: true },
            { label: copy.model, value: runtimeSummary.model, always: true },
            { label: copy.sandbox, value: runtimeSummary.sandbox, always: true },
            { label: copy.tools, value: runtimeSummary.tools, always: false },
            { label: copy.mcp, value: runtimeSummary.mcp, always: false },
            { label: copy.goal, value: runtimeSummary.goal, always: false }
          ]
            .filter((chip) => chip.always || (chip.value && chip.value !== "-"))
            .map((chip) => (
              <span key={chip.label} title={chip.value}>
                {chip.label}: {chip.value}
              </span>
            ))}
        </div>

        <MessageList
          identity={identity}
          copy={copy}
          conversationScrollRef={conversationScrollRef}
          onConversationScroll={handleConversationScroll}
          loadError={loadError}
          tenantStorageMissing={tenantStorageMissing}
          onRestoreLocalTestIdentity={canRestoreLocalTestIdentity ? restoreLocalTestIdentity : undefined}
          loading={loading}
          hasTasks={tasks.length > 0}
          onNewSession={openNewSession}
          hasSelectedTask={Boolean(selectedTask)}
          messagesEmpty={displayedMessages.length === 0}
          showThinkingPlaceholder={showThinkingPlaceholder}
          onApplySuggestion={applySuggestion}
          hiddenMessageCount={hiddenMessageCount}
          onShowEarlierMessages={showEarlierMessages}
          visibleMessages={visibleMessages}
          thinkingMode={thinkingMode}
          thinkingSessionKey={thinkingSessionKey}
          expandedThinkingKeys={expandedThinkingKeys}
          onThinkingToggle={handleThinkingToggle}
          streamState={streamState}
          selectedTaskId={selectedTaskId}
          typewriterTaskIDs={typewriterTaskIDs}
          isFinalResponse={(message) => isFinalAssistantResponse(message, selectedTask)}
          latestAssistantMessageId={latestAssistantMessageId}
          fallbackReplyDurationMs={numberFrom(conversationDetail?.usage?.total_duration_ms) || summarizeUsage(selectedTask, events).durationMS}
          setLatestMessageNode={setLatestMessageNode}
          onTypewriterDone={(taskID) => setTypewriterTaskIDs((current) => removeSetValue(current, taskID))}
          copiedMessageId={copiedMessageId}
          onCopyMessage={(message) => void handleCopyMessage(message)}
          onRetryMessage={(messageId) => void handleRetryMessage(messageId)}
          retryDisabled={(messageId) => running || composerState === "sending" || composerState === "cancelling" || !precedingUserPrompt(messageId)}
          liveToolRuns={liveToolRuns}
          thinkingState={thinkingState}
        />

        {showJumpToLatest ? (
          <button
            className="agent-jump-latest"
            onClick={() => scrollToLatest()}
            type="button"
            aria-label={copy.jumpToLatest}
            title={copy.jumpToLatest}
          >
            <ChevronDown size={18} aria-hidden="true" />
          </button>
        ) : null}

        <Composer
          identity={identity}
          copy={copy}
          composerRef={composerRef}
          composerText={composerText}
          onComposerTextChange={setComposerText}
          onComposerKeyDown={handleComposerKeyDown}
          onComposerPaste={handleComposerPaste}
          onImageDrop={handleImageDrop}
          pendingImages={pendingImages}
          onAddImages={handleAddImages}
          onRemoveImage={handleRemoveImage}
          hasSelectedTask={Boolean(selectedTask)}
          slashCommands={slashSuggestions}
          slashSelected={slashSelected}
          slashState={slashState}
          slashError={slashError}
          onSlashSelect={(index) => setSlashSelected(index)}
          onSlashApply={applySlashSuggestion}
          nextStepSuggestions={visibleNextStepSuggestions}
          onApplyNextStep={applySuggestion}
          pendingInputs={pendingInputs}
          pendingInputQueueEnabled={pendingInputQueueEnabled}
          onMovePendingInputUp={(item) => void handleMovePendingInputUp(item)}
          onEditPendingInput={(item) => void handleEditPendingInput(item)}
          onDirectionPendingInput={(item) => void handleDirectionPendingInput(item)}
          onDeletePendingInput={(item) => void handleDeletePendingInput(item)}
          onRetryPendingInput={(item) => void handleRetryPendingInput(item)}
          onSideChatPendingInput={(item) => void handleSideChatPendingInput(item)}
          onTogglePendingInputQueue={() => void handleTogglePendingInputQueue()}
          permissionMode={permissionMode}
          onPermissionModeChange={setPermissionMode}
          composerState={composerState}
          contextPercent={activity.contextPercent}
          cacheHitPercent={activity.cacheHitPercent}
          providerOptions={providerOptions}
          provider={provider}
          model={model}
          servedModels={servedModels}
          onChooseProvider={chooseProvider}
          onChooseModel={chooseModel}
          effort={effort}
          onEffortChange={setEffort}
          composerActionIsCancel={composerActionIsCancel}
          sendDisabled={sendDisabled}
          onCancel={() => void handleCancel()}
          onSend={() => void handleSend()}
        />
      </section>

      <aside className="web-agent-right" aria-label="Web Agent details">
        <div className="right-panel-header">
          {rightOpen ? <strong>{copy.workspaceDetail}</strong> : null}
          <button className="agent-icon-button" onClick={() => setRightOpen((open) => !open)} title={rightOpen ? copy.hideDetails : copy.showDetails} type="button">
            {rightOpen ? <ChevronRight size={17} /> : <ChevronLeft size={17} />}
          </button>
        </div>
        {rightOpen ? (
          <>
            <div className="right-tabs" role="tablist">
              {(["progress", "runs", "files", "permissions", "trace", "usage"] as RightTab[]).map((tab) => (
                <button key={tab} className={rightTab === tab ? "active" : ""} onClick={() => setRightTab(tab)} type="button">
                  {copy.tabs[tab]}
                </button>
              ))}
            </div>
            <div className="right-tab-scroll">
              {rightTab === "progress" ? <ProgressTab copy={copy} selectedTask={selectedTask} events={events} promptMode={selectedTaskPromptMode} /> : null}
              {rightTab === "runs" ? <RunsTab copy={copy} selectedTaskId={selectedTaskId} session={selectedSessionView} onSelectTask={setSelectedTaskId} /> : null}
              {rightTab === "files" ? <FilesTab copy={copy} activity={activity} /> : null}
              {rightTab === "permissions" ? <PermissionsTab copy={copy} permissionMode={permissionMode} activity={activity} requests={buildPermissionRequests(events)} onResolve={(request, allowed) => void handlePermissionResolve(request, allowed)} /> : null}
              {rightTab === "trace" ? <TraceTab copy={copy} detail={conversationDetail} events={conversationTimelineEvents} /> : null}
              {rightTab === "usage" ? <UsageTab copy={copy} detail={conversationDetail} activity={activity} task={selectedTask} events={events} /> : null}
            </div>
          </>
        ) : null}
      </aside>

      {leftOpen || rightOpen ? (
        <button
          className="agent-rail-backdrop"
          type="button"
          aria-label={copy.closePanels}
          onClick={() => {
            setLeftOpen(false);
            setRightOpen(false);
          }}
        />
      ) : null}

      {newSessionOpen ? (
        <NewSessionModal
          copy={copy}
          workspaceInput={workspaceInput}
          onWorkspaceInputChange={setWorkspaceInput}
          workspaceValidationState={workspaceValidationState}
          validatedWorkspace={validatedWorkspace}
          workspaceError={workspaceError}
          sessionTitle={sessionTitle}
          onSessionTitleChange={setSessionTitle}
          sessionPrompt={sessionPrompt}
          onSessionPromptChange={setSessionPrompt}
          promptMode={newSessionPromptMode}
          onPromptModeChange={(next) => setNewSessionPromptMode(normalizePromptMode(next, "code"))}
          onClose={() => setNewSessionOpen(false)}
          onCreate={() => void handleCreateSession()}
        />
      ) : null}
      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        commands={paletteCommands}
        placeholder={copy.cmdkPlaceholder}
        emptyLabel={copy.cmdkEmpty}
        ariaLabel={copy.cmdkTitle}
      />
    </main>
  );
}


function isLocalWebAgentTestIdentityError(identity: IdentityConfig, loadError: string): boolean {
  return import.meta.env.DEV
    && typeof window !== "undefined"
    && window.location.pathname.startsWith("/webui/agent")
    && identity.apiBase.replace(/\/+$/, "") === "/api"
    && identity.apiToken === "test-token"
    && (identity.tenantKey !== defaultIdentity.tenantKey || identity.userId !== defaultIdentity.userId)
    && /get tenant .+: mysql storage: not found/i.test(loadError);
}

type TimelineEntry = {
  key: string;
  icon: "prompt" | "start" | "tool" | "reply" | "permission" | "done" | "fail" | "cancel" | "event";
  title: string;
  detail: string;
  time?: string;
  status: "ok" | "warn" | "fail" | "info";
  count: number;
  startedAt?: string;
  payload?: string;
};

function timelinePayloadPreview(payloadJSON: string | undefined): string {
  const raw = (payloadJSON || "").trim();
  if (!raw) {
    return "";
  }
  try {
    return JSON.stringify(JSON.parse(raw), null, 2).slice(0, 1600);
  } catch {
    return raw.slice(0, 1600);
  }
}

// buildActivityTimeline 把原始事件流聚合成人类可读的执行时间线:
// 发送指令 → 开始执行 → 工具步骤(按 tool_id 合并 call/result) → 生成回复(折叠
// 连续 text_delta) → 完成/失败/取消。usage/message_stop 属于结算噪音,不展示。
function buildActivityTimeline(events: AgentTaskEventRecord[], copy: AgentCopy): TimelineEntry[] {
  const entries: TimelineEntry[] = [];
  const toolByID = new Map<string, TimelineEntry>();
  let replyRun: TimelineEntry | null = null;
  for (const event of events) {
    const type = event.event_type || "event";
    if (type === "text_delta") {
      if (replyRun) {
        replyRun.count += 1;
        replyRun.detail = `×${replyRun.count}`;
        replyRun.time = event.created_at || replyRun.time;
      } else {
        replyRun = { key: `reply-${event.id}`, icon: "reply", title: copy.timelineReply, detail: "×1", time: event.created_at, status: "info", count: 1 };
        entries.push(replyRun);
      }
      continue;
    }
    replyRun = null;
    if (type === "usage" || type === "message_stop") {
      continue;
    }
    const payload = parsePayload(event.payload_json);
    if (type === "message") {
      entries.push({ key: `e${event.id}`, icon: "prompt", title: copy.timelinePrompt, detail: truncateOneLine(stringFrom(payload.content), 72), time: event.created_at, status: "info", count: 1 });
      continue;
    }
    if (type === "started") {
      entries.push({ key: `e${event.id}`, icon: "start", title: copy.timelineStarted, detail: "", time: event.created_at, status: "info", count: 1 });
      continue;
    }
    if (type === "tool_call" || type === "tool_result") {
      const name = stringFrom(payload.tool_name ?? payload.name) || "Tool";
      const id = stringFrom(payload.tool_id ?? payload.id) || `${name}:${event.id}`;
      let entry = toolByID.get(id);
      if (!entry) {
        entry = { key: `tool-${id}`, icon: "tool", title: name, detail: copy.toolStatus.running, time: event.created_at, status: "warn", count: 1, startedAt: event.created_at, payload: timelinePayloadPreview(event.payload_json) };
        toolByID.set(id, entry);
        entries.push(entry);
      }
      if (event.event_type === "tool_result") {
        const isError = truthy(payload.is_error);
        const elapsed = elapsedBetween(entry.startedAt || "", event.created_at || "");
        entry.status = isError ? "fail" : "ok";
        entry.detail = `${isError ? copy.toolStatus.error : copy.toolStatus.done}${elapsed && elapsed !== "-" ? ` · ${elapsed}` : ""}`;
        entry.time = event.created_at || entry.time;
        entry.payload = timelinePayloadPreview(event.payload_json) || entry.payload;
      }
      continue;
    }
    if (type === "permission_request") {
      entries.push({ key: `e${event.id}`, icon: "permission", title: copy.timelinePermission, detail: truncateOneLine(stringFrom(payload.tool_name ?? payload.description), 60), time: event.created_at, status: "warn", count: 1 });
      continue;
    }
    if (type === "completed") {
      entries.push({ key: `e${event.id}`, icon: "done", title: copy.timelineCompleted, detail: "", time: event.created_at, status: "ok", count: 1 });
      continue;
    }
    if (type === "failed") {
      entries.push({ key: `e${event.id}`, icon: "fail", title: copy.timelineFailed, detail: truncateOneLine(stringFrom(payload.error), 80), time: event.created_at, status: "fail", count: 1, payload: timelinePayloadPreview(event.payload_json) });
      continue;
    }
    if (type === "cancelled") {
      entries.push({ key: `e${event.id}`, icon: "cancel", title: copy.timelineCancelled, detail: "", time: event.created_at, status: "warn", count: 1 });
      continue;
    }
    const last = entries[entries.length - 1];
    if (last && last.icon === "event" && last.title === type) {
      last.count += 1;
      last.detail = `×${last.count}`;
      last.time = event.created_at || last.time;
    } else {
      entries.push({ key: `e${event.id}`, icon: "event", title: type, detail: "", time: event.created_at, status: "info", count: 1 });
    }
  }
  return entries;
}

function timelineIcon(icon: TimelineEntry["icon"]) {
  switch (icon) {
    case "prompt":
      return <MessageSquarePlus size={13} />;
    case "start":
      return <Play size={13} />;
    case "tool":
      return <Terminal size={13} />;
    case "reply":
      return <Bot size={13} />;
    case "permission":
      return <Shield size={13} />;
    case "done":
      return <CheckCircle2 size={13} />;
    case "fail":
      return <X size={13} />;
    case "cancel":
      return <Square size={13} />;
    default:
      return <History size={13} />;
  }
}

function ProgressTab({ copy, selectedTask, events, promptMode }: { copy: AgentCopy; selectedTask: AgentTaskRecord | null; events: AgentTaskEventRecord[]; promptMode: PromptMode }) {
  if (!selectedTask) {
    return <div className="agent-empty-mini">{copy.selectSessionInspect}</div>;
  }
  const tools = buildToolActivities(events);
  const subAgents = buildSubAgentProgress(events);
  const recentAgentEvidence = recentAgentEvidenceFromToolActivities(tools);
  return (
    <div className="detail-stack">
      <DetailRow label={copy.status} value={selectedTask.status || "unknown"} />
      <DetailRow label={copy.model} value={selectedTask.model || "-"} />
      <DetailRow label={copy.promptMode} value={copy.promptModes[promptMode]} />
      <DetailRow label={copy.trace} value={selectedTask.trace_id || "-"} />
      <DetailRow label={copy.started} value={formatTime(selectedTask.started_at)} />
      {recentAgentEvidence ? (
        <section className="recent-agent-evidence" aria-label={copy.recentAgentEvidence}>
          <strong>{copy.recentAgentEvidence}</strong>
          <p>{recentAgentEvidence}</p>
        </section>
      ) : null}
      {tools.length > 0 ? (
        <section className="tool-activity-stack" aria-label={copy.toolActivity}>
          <strong>{copy.toolActivity}</strong>
          {tools.map((tool) => (
            <div key={tool.id} className={`tool-activity-card ${tool.status}`}>
              <span className={`agent-status-dot ${tool.status === "error" ? "fail" : tool.status === "running" ? "warn" : "ok"}`} />
              <div>
                <strong>{tool.name}</strong>
                <small>{copy.toolStatus[tool.status]} · {tool.elapsed}{tool.turn ? ` · turn ${tool.turn}` : ""}</small>
                {tool.preview ? <p>{tool.preview}</p> : null}
              </div>
            </div>
          ))}
        </section>
      ) : null}
      {subAgents.length > 0 ? (
        <section className="tool-activity-stack" aria-label={copy.subAgents}>
          <strong>{copy.subAgents}</strong>
          {subAgents.map((sub) => (
            <div key={sub.taskID} className={`tool-activity-card ${sub.status}`}>
              <span className={`agent-status-dot ${sub.status === "done" ? "ok" : sub.status === "running" ? "warn" : "fail"}`} />
              <div>
                <strong>{sub.agent || sub.description || `sub-agent ${sub.taskID}`}</strong>
                <small>
                  {copy.subAgentStatus[sub.status]}
                  {sub.model ? ` · ${sub.model}` : ""}
                  {sub.turn ? ` · turn ${sub.turn}` : ""}
                  {sub.lastTool ? ` · ${sub.lastTool}` : ""}
                  {sub.toolCalls ? ` · ${sub.toolCalls} tools` : ""}
                  {sub.durationMS ? ` · ${Math.round(sub.durationMS / 1000)}s` : ""}
                </small>
                {sub.tokens ? <p>{sub.tokens}</p> : null}
                {sub.detail ? <p>{sub.detail}</p> : null}
              </div>
            </div>
          ))}
        </section>
      ) : null}
      {events.length > 0 ? (
        <section className="agent-timeline" aria-label={copy.activityTimeline}>
          <strong className="agent-timeline-heading">{copy.activityTimeline}</strong>
          {buildActivityTimeline(events, copy).map((entry) => {
            const row = (
              <>
                <span className="agent-timeline-icon">{timelineIcon(entry.icon)}</span>
                <span className="agent-timeline-body">
                  <span className="agent-timeline-title">
                    <strong>{entry.title}</strong>
                    {entry.detail ? <em>{entry.detail}</em> : null}
                  </span>
                  <small>{formatTime(entry.time)}</small>
                </span>
              </>
            );
            if (!entry.payload) {
              return (
                <div key={entry.key} className={`agent-timeline-row ${entry.status}`}>
                  {row}
                </div>
              );
            }
            return (
              <details key={entry.key} className="agent-timeline-item">
                <summary className={`agent-timeline-row expandable ${entry.status}`} title={copy.timelineExpand}>
                  {row}
                </summary>
                <pre className="agent-timeline-payload">{entry.payload}</pre>
              </details>
            );
          })}
        </section>
      ) : null}
    </div>
  );
}

function RunsTab({ copy, selectedTaskId, session, onSelectTask }: { copy: AgentCopy; selectedTaskId: number | null; session: WebAgentSessionView | null; onSelectTask: (taskID: number) => void }) {
  if (!session) {
    return <div className="agent-empty-mini">{copy.selectSessionInspect}</div>;
  }
  return (
    <div className="detail-stack">
      <DetailRow label={copy.conversationId} value={session.id} />
      <DetailRow label={copy.runs} value={session.tasks.length} />
      <div className="agent-run-list">
        {session.tasks.map((task, index) => (
          <button key={task.id} className={task.id === selectedTaskId ? "agent-run-row active" : "agent-run-row"} onClick={() => onSelectTask(task.id)} type="button">
            <span className={`agent-status-dot ${statusClass(task.status || "")}`} />
            <span>
              <strong>{copy.run} #{index + 1} · task {task.id}</strong>
              <small>{task.status || "unknown"} · {formatTime(task.started_at)}</small>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

// formatLineDelta renders a measured delta as +N / -N / 0. A null delta means the
// file body was externalized, so no count exists and the caller shows a marker
// instead of a number.
function formatLineDelta(delta: number): string {
  return delta > 0 ? `+${delta}` : String(delta);
}

function EditedFileRow({ copy, file }: { copy: AgentCopy; file: EditedFile }) {
  const changeLabel = copy.fileChangeKinds[file.change] ?? file.change;
  const objectSuffix = file.object === "file" ? "" : ` · ${copy.fileObjects[file.object] ?? file.object}`;
  return (
    <div className="file-row">
      <FileDiff size={14} />
      <span>
        <strong title={file.path}>{file.path}</strong>
        <small>
          {changeLabel}
          {objectSuffix}
          {file.toolName ? ` · ${file.toolName}` : ""}
          {" · "}
          {file.lineDelta === null ? copy.lineDeltaUnavailable : formatLineDelta(file.lineDelta)}
        </small>
      </span>
    </div>
  );
}

function FilesTab({ copy, activity }: { copy: AgentCopy; activity: ReturnType<typeof summarizeActivity> }) {
  const hasFiles = activity.editedFiles.length > 0 || activity.readFilePaths.length > 0;
  return (
    <div className="detail-stack">
      <DetailRow label={copy.currentFile} value={activity.currentFile || "-"} />
      <DetailRow label={copy.changedFiles} value={activity.changedFiles} />
      <DetailRow label={copy.readFiles} value={activity.filesRead} />
      <DetailRow label={copy.lineDelta} value={formatLineDelta(activity.lineDelta)} />
      {hasFiles ? null : <div className="agent-empty-mini">{copy.noFileEvents}</div>}
      {activity.editedFiles.length > 0 ? (
        <section className="file-group">
          <h4>
            {copy.editedFilesGroup} <span className="file-group-count">{activity.editedFiles.length}</span>
          </h4>
          <div className="file-list">
            {activity.editedFiles.map((file) => (
              <EditedFileRow key={file.path} copy={copy} file={file} />
            ))}
          </div>
        </section>
      ) : null}
      {activity.readFilePaths.length > 0 ? (
        <section className="file-group">
          <h4>
            {copy.readFilesGroup} <span className="file-group-count">{activity.readFilePaths.length}</span>
          </h4>
          <div className="file-list">
            {activity.readFilePaths.map((path) => (
              <div key={path} className="file-row">
                <FileText size={14} />
                <span>
                  <strong title={path}>{path}</strong>
                </span>
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function TraceTab({ copy, detail, events }: { copy: AgentCopy; detail: WebAgentConversationDetail | null; events: AgentTaskEventRecord[] }) {
  const traceEvents = detail?.events?.length ? detail.events : events;
  return (
    <div className="detail-stack">
      <DetailRow label={copy.eventCount} value={traceEvents.length} />
      <div className="event-list">
        {traceEvents.length === 0 ? <div className="agent-empty-mini">{copy.noEvents}</div> : null}
        {traceEvents.slice(-80).map((event) => (
          <div key={`${event.task_id}-${event.id}`} className="event-list-row">
            <History size={14} />
            <span>
              <strong>{event.event_type || "event"} · task {event.task_id}</strong>
              <small>{formatTime(event.created_at)} · {event.trace_id || copy.traceMissing}</small>
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function UsageTab({ copy, detail, activity, task, events }: { copy: AgentCopy; detail: WebAgentConversationDetail | null; activity: ReturnType<typeof summarizeActivity>; task: AgentTaskRecord | null; events: AgentTaskEventRecord[] }) {
  const usage = detail?.usage || {};
  const eventUsage = summarizeUsage(task, events);
  const contextPercent = numberFrom(usage.context_percent) || eventUsage.contextPercent || activity.contextPercent;
  return (
    <div className="detail-stack">
      <DetailRow label={copy.context} value={`${contextPercent}%`} />
      <DetailRow label={copy.contextLength} value={eventUsage.contextLength || "-"} />
      <DetailRow label={copy.totalTokens} value={numberFrom(usage.total_tokens) || eventUsage.totalTokens} />
      <DetailRow label={copy.inputTokens} value={numberFrom(usage.input_tokens) || eventUsage.inputTokens} />
      <DetailRow label={copy.outputTokens} value={numberFrom(usage.output_tokens) || eventUsage.outputTokens} />
      <DetailRow label={copy.cacheCreateTokens} value={eventUsage.cacheCreationTokens} />
      <DetailRow label={copy.cacheReadTokens} value={eventUsage.cacheReadTokens} />
      <DetailRow label={copy.cacheEphemeralTokens} value={`${eventUsage.cacheCreationEphemeral5mTokens}/${eventUsage.cacheCreationEphemeral1hTokens}`} />
      <DetailRow label={copy.serviceTier} value={eventUsage.serviceTier || "-"} />
      <DetailRow label={copy.inferenceGeo} value={eventUsage.inferenceGeo || "-"} />
      <DetailRow label={copy.speed} value={eventUsage.speed || "-"} />
      <DetailRow label={copy.initialMessages} value={eventUsage.initialMessages} />
      <DetailRow label={copy.stopReason} value={eventUsage.stopReason || "-"} />
      <DetailRow label={copy.toolCalls} value={numberFrom(usage.tool_calls) || eventUsage.toolCalls} />
      <DetailRow label={copy.duration} value={formatDuration(numberFrom(usage.total_duration_ms) || eventUsage.durationMS)} />
      <DetailRow label={copy.completedRuns} value={numberFrom(usage.completed_runs)} />
      <DetailRow label={copy.failedRuns} value={numberFrom(usage.failed_runs)} />
      <DetailRow label={copy.cancelledRuns} value={numberFrom(usage.cancelled_runs)} />
      <DetailRow label={copy.timeoutRuns} value={numberFrom(usage.timeout_runs)} />
      <DetailRow label={copy.runningRuns} value={numberFrom(usage.running_runs)} />
    </div>
  );
}

function PermissionsTab({
  copy,
  permissionMode,
  activity,
  requests,
  onResolve
}: {
  copy: AgentCopy;
  permissionMode: string;
  activity: ReturnType<typeof summarizeActivity>;
  requests: PermissionRequestView[];
  onResolve: (request: PermissionRequestView, allowed: boolean) => void;
}) {
  return (
    <div className="detail-stack">
      <DetailRow label={copy.mode} value={permissionMode} />
      <DetailRow label={copy.pending} value={activity.pendingPermissions} />
      <DetailRow label={copy.sandbox} value={copy.localWorkspace} />
      <div className="permission-request-list">
        {requests.length === 0 ? <div className="agent-empty-mini">{copy.noPermissionRequests}</div> : null}
        {requests.map((request) => (
          <article key={request.requestID} className={`permission-request-card ${request.status}`}>
            <div>
              <strong>{request.toolName || copy.permissionRequest}</strong>
              <small>{request.status} · {request.requestID}</small>
            </div>
            {request.reason ? <p>{request.reason}</p> : null}
            {request.input ? <code>{request.input}</code> : null}
            {request.status === "pending" ? (
              <div className="permission-actions">
                <button type="button" onClick={() => onResolve(request, true)}>{copy.approve}</button>
                <button type="button" className="danger" onClick={() => onResolve(request, false)}>{copy.deny}</button>
              </div>
            ) : (
              <small>{request.decision || request.status} · {formatTime(request.resolvedAt)}</small>
            )}
          </article>
        ))}
      </div>
      <p className="permission-note">{copy.permissionNote}</p>
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="detail-row">
      <span>{label}</span>
      <strong title={String(value)}>{value}</strong>
    </div>
  );
}




















// One row in the Files tab. Mirrors the file_change event payload emitted by
// internal/server/agent_task_file_events.go; every field here is something the
// server actually measured.
export type EditedFile = {
  path: string;
  change: string;
  object: string;
  toolName: string;
  // null when the file body was externalized to the snapshot store, so no line
  // count was available. Distinct from 0, which means "measured, no net change".
  lineDelta: number | null;
  contentAvailable: boolean;
};

type FileActivity = {
  lineDelta: number;
  editedByPath: Map<string, EditedFile>;
  readPaths: Set<string>;
};

const FILE_ROW_LIMIT = 24;

function newFileActivity(): FileActivity {
  return { lineDelta: 0, editedByPath: new Map(), readPaths: new Set() };
}

// recordFileChange folds one file_change payload into the Edited / Read split
// the Files tab renders, and returns the path it touched ("" if the payload
// carried none).
//
// A path is listed once. Repeated edits collapse onto the first row, keeping the
// original classification — a file this run created still reads as created — but
// their measured deltas keep summing. A path that was read and later written is
// reported only as edited.
function recordFileChange(activity: FileActivity, payload: ParsedPayload): string {
  const path = stringFrom(payload.path);
  if (!path) {
    return "";
  }
  if (stringFrom(payload.access) === "read") {
    if (!activity.editedByPath.has(path)) {
      activity.readPaths.add(path);
    }
    return path;
  }
  activity.readPaths.delete(path);

  // Line counts only exist for regular files whose content was captured inline;
  // anything else stays null rather than being reported as a measured zero.
  const object = stringFrom(payload.object) || "file";
  const contentAvailable = payload.content_available === true;
  const delta = contentAvailable && object === "file" ? numberFrom(payload.line_delta) || 0 : null;
  if (delta !== null) {
    activity.lineDelta += delta;
  }

  const existing = activity.editedByPath.get(path);
  if (existing) {
    if (delta !== null) {
      existing.lineDelta = (existing.lineDelta ?? 0) + delta;
      existing.contentAvailable = true;
    }
    return path;
  }
  activity.editedByPath.set(path, {
    path,
    change: stringFrom(payload.change) || "modified",
    object,
    toolName: stringFrom(payload.tool_name),
    lineDelta: delta,
    contentAvailable
  });
  return path;
}

export function summarizeActivity(task: AgentTaskRecord | null, events: AgentTaskEventRecord[], fileRowLimit = FILE_ROW_LIMIT) {
  const metadata = parsePayload(task?.metadata_json);
  const result = parsePayload(task?.result_json);
  const fileActivity = newFileActivity();
  const firstEventAt = events.reduce((oldest, event) => {
    const timestamp = timestampOf(event.created_at);
    if (!timestamp) {
      return oldest;
    }
    return oldest ? Math.min(oldest, timestamp) : timestamp;
  }, 0);
  let commandsRun = 0;
  let pendingPermissions = 0;
  let currentFile = "";
  let contextPercent = numberFrom(metadata.context_percent ?? result.context_percent) || 0;
  let contextLength = numberFrom(metadata.context_length ?? result.context_length) || 0;
  let totalTokens = numberFrom(metadata.total_tokens ?? result.total_tokens) || 0;
  let cacheReadTokens = 0;
  let cacheCreationTokens = 0;
  let inputTokens = 0;
  let explicitCacheHitPercent: number | null = null;
  const mergeUsage = (payload: ParsedPayload) => {
    const usage = tokenUsage(payload);
    if (usage.cacheReadTokens) {
      cacheReadTokens = usage.cacheReadTokens;
    }
    if (usage.cacheCreationTokens) {
      cacheCreationTokens = usage.cacheCreationTokens;
    }
    if (usage.inputTokens) {
      inputTokens = usage.inputTokens;
    }
    if (usage.cacheHitPercent !== null) {
      explicitCacheHitPercent = usage.cacheHitPercent;
    }
  };
  mergeUsage(metadata);
  mergeUsage(result);

  for (const event of events) {
    const payload = parsePayload(event.payload_json);
    mergeUsage(payload);
    // file_change is the only source of file activity. It is also excluded from
    // the command tally below: a Bash command that touched three files is one
    // command, not four.
    if (event.event_type === "file_change") {
      const touched = recordFileChange(fileActivity, payload);
      if (touched) {
        currentFile = touched;
      }
      continue;
    }
    const kind = `${event.event_type || ""} ${stringFrom(payload.tool_name)} ${stringFrom(payload.action)}`.toLowerCase();
    if (kind.includes("bash") || kind.includes("command") || kind.includes("terminal")) {
      commandsRun += 1;
    }
    if (event.event_type === "permission_request") {
      pendingPermissions += 1;
    } else if (event.event_type === "permission_resolved") {
      pendingPermissions = Math.max(0, pendingPermissions - 1);
    }
    const eventContext = numberFrom(payload.context_percent);
    if (eventContext) {
      contextPercent = eventContext;
    }
    const eventContextLength = numberFrom(payload.context_length);
    const eventTotalTokens = numberFrom(payload.total_tokens) || totalTokenCount(payload);
    if (eventContextLength) {
      contextLength = eventContextLength;
    }
    if (eventTotalTokens) {
      totalTokens = eventTotalTokens;
    }
  }
  if (!totalTokens) {
    totalTokens = totalTokenCount(result) || totalTokenCount(metadata);
  }
  if (!contextPercent && totalTokens > 0) {
    const denominator = contextLength || DEFAULT_CONTEXT_LENGTH;
    contextPercent = Math.ceil((totalTokens * 100) / denominator);
  }
  const cacheHitPercent = explicitCacheHitPercent ?? calculateCacheHitPercent(cacheReadTokens, cacheCreationTokens, inputTokens);

  const startedAt = timestampOf(task?.started_at) || firstEventAt || timestampOf(stringFrom(metadata.created_at));
  const finishedAt = timestampOf(task?.finished_at) || timestampOf(stringFrom(result.finished_at));
  const elapsedMs = startedAt ? Math.max(0, (finishedAt || Date.now()) - startedAt) : 0;

  const editedFiles = Array.from(fileActivity.editedByPath.values()).slice(0, fileRowLimit);
  const readFilePaths = Array.from(fileActivity.readPaths).slice(0, fileRowLimit);
  const lineDelta = fileActivity.lineDelta;

  return {
    cwd: stringFrom(metadata.cwd),
    elapsed: elapsedMs ? formatDuration(elapsedMs) : "-",
    filesRead: readFilePaths.length,
    commandsRun,
    currentFile,
    changedFiles: editedFiles.length,
    lineDelta,
    contextPercent: Math.min(100, Math.max(0, Math.round(contextPercent))),
    cacheHitPercent,
    pendingPermissions,
    eventCount: events.length,
    editedFiles,
    readFilePaths,
    hasOperationalActivity:
      editedFiles.length > 0 || readFilePaths.length > 0 || commandsRun > 0 || lineDelta !== 0 || pendingPermissions > 0
  };
}

export function summarizeUsage(task: AgentTaskRecord | null, events: AgentTaskEventRecord[]): AgentUsageView {
  const metadata = parsePayload(task?.metadata_json);
  const result = parsePayload(task?.result_json);
  const usage: AgentUsageView = {
    contextPercent: numberFrom(metadata.context_percent ?? result.context_percent),
    contextLength: numberFrom(metadata.context_length ?? result.context_length),
    totalTokens: totalTokenCount(result) || totalTokenCount(metadata),
    inputTokens: numberFrom(metadata.input_tokens ?? result.input_tokens),
    outputTokens: numberFrom(metadata.output_tokens ?? result.output_tokens),
    cacheCreationTokens: numberFrom(metadata.cache_creation_input_tokens ?? result.cache_creation_input_tokens),
    cacheReadTokens: numberFrom(metadata.cache_read_input_tokens ?? result.cache_read_input_tokens),
    cacheCreationEphemeral1hTokens: numberFrom(metadata.cache_creation_ephemeral_1h_input_tokens ?? result.cache_creation_ephemeral_1h_input_tokens),
    cacheCreationEphemeral5mTokens: numberFrom(metadata.cache_creation_ephemeral_5m_input_tokens ?? result.cache_creation_ephemeral_5m_input_tokens),
    serviceTier: stringFrom(metadata.service_tier ?? result.service_tier),
    inferenceGeo: stringFrom(metadata.inference_geo ?? result.inference_geo),
    speed: stringFrom(metadata.speed ?? result.speed),
    initialMessages: numberFrom(metadata.initial_messages ?? result.initial_messages),
    toolCalls: numberFrom(metadata.tool_calls ?? result.tool_calls),
    stopReason: stringFrom(metadata.stop_reason ?? result.stop_reason),
    durationMS: numberFrom(metadata.duration_ms ?? result.duration_ms)
  };
  for (const event of events) {
    const payload = parsePayload(event.payload_json);
    const eventUsage = tokenUsage(payload);
    usage.inputTokens = numberFrom(payload.input_tokens) || usage.inputTokens;
    usage.outputTokens = numberFrom(payload.output_tokens) || usage.outputTokens;
    usage.cacheCreationTokens = eventUsage.cacheCreationTokens || usage.cacheCreationTokens;
    usage.cacheReadTokens = eventUsage.cacheReadTokens || usage.cacheReadTokens;
    usage.cacheCreationEphemeral1hTokens = numberFrom(payload.cache_creation_ephemeral_1h_input_tokens) || usage.cacheCreationEphemeral1hTokens;
    usage.cacheCreationEphemeral5mTokens = numberFrom(payload.cache_creation_ephemeral_5m_input_tokens) || usage.cacheCreationEphemeral5mTokens;
    usage.serviceTier = stringFrom(payload.service_tier) || usage.serviceTier;
    usage.inferenceGeo = stringFrom(payload.inference_geo) || usage.inferenceGeo;
    usage.speed = stringFrom(payload.speed) || usage.speed;
    usage.contextLength = numberFrom(payload.context_length) || usage.contextLength;
    usage.contextPercent = numberFrom(payload.context_percent) || usage.contextPercent;
    usage.initialMessages = numberFrom(payload.initial_messages) || usage.initialMessages;
    usage.stopReason = stringFrom(payload.stop_reason) || usage.stopReason;
    usage.durationMS = numberFrom(payload.duration_ms) || usage.durationMS;
    const eventTotal = numberFrom(payload.total_tokens) || totalTokenCount(payload);
    if (eventTotal) {
      usage.totalTokens = eventTotal;
    }
    if (event.event_type === "tool_call") {
      usage.toolCalls += 1;
    }
  }
  if (!usage.totalTokens) {
    usage.totalTokens = usage.inputTokens + usage.outputTokens + usage.cacheCreationTokens + usage.cacheReadTokens;
  }
  if (!usage.contextPercent && usage.totalTokens > 0) {
    const denominator = usage.contextLength || DEFAULT_CONTEXT_LENGTH;
    usage.contextPercent = Math.ceil((usage.totalTokens * 100) / denominator);
  }
  usage.contextPercent = Math.min(100, Math.max(0, Math.round(usage.contextPercent)));
  return usage;
}

export function buildPermissionRequests(events: AgentTaskEventRecord[]): PermissionRequestView[] {
  const byID = new Map<string, PermissionRequestView>();
  const ordered: PermissionRequestView[] = [];
  for (const event of events) {
    if (event.event_type !== "permission_request" && event.event_type !== "permission_resolved") {
      continue;
    }
    const payload = parsePayload(event.payload_json);
    const requestID = stringFrom(payload.request_id);
    if (!requestID) {
      continue;
    }
    if (event.event_type === "permission_request") {
      const item: PermissionRequestView = {
        requestID,
        taskID: event.task_id,
        toolName: stringFrom(payload.tool_name ?? payload.tool),
        reason: stringFrom(payload.reason ?? payload.request),
        input: stringFrom(payload.input),
        status: "pending",
        decision: "",
        createdAt: event.created_at || "",
        resolvedAt: ""
      };
      byID.set(requestID, item);
      ordered.push(item);
      continue;
    }
    const existing = byID.get(requestID);
    const allowed = truthy(payload.allowed) || stringFrom(payload.decision).toLowerCase() === "allow";
    const next: PermissionRequestView = existing || {
      requestID,
      taskID: event.task_id,
      toolName: stringFrom(payload.tool_name ?? payload.tool),
      reason: stringFrom(payload.reason),
      input: "",
      status: allowed ? "allowed" : "denied",
      decision: "",
      createdAt: "",
      resolvedAt: ""
    };
    next.status = allowed ? "allowed" : "denied";
    next.decision = stringFrom(payload.decision) || next.status;
    next.resolvedAt = event.created_at || "";
    byID.set(requestID, next);
    if (!ordered.includes(next)) {
      ordered.push(next);
    }
  }
  return ordered;
}

function summarizeRuntime(task: AgentTaskRecord | null, events: AgentTaskEventRecord[], identity: IdentityConfig, permissionMode: string, providerOptions: ProviderOption[] = []): RuntimeSummary {
  const metadata = parsePayload(task?.metadata_json);
  const startedEvent = events.find((event) => event.event_type === "started");
  const started = parsePayload(startedEvent?.payload_json);
  const toolNames = new Set<string>();
  for (const event of events) {
    const payload = parsePayload(event.payload_json);
    const name = stringFrom(payload.tool_name ?? payload.name);
    if ((event.event_type === "tool_call" || event.event_type === "tool_result") && name) {
      toolNames.add(name);
    }
  }
  const summaryModel = stringFrom(identity.model) || task?.model || stringFrom(metadata.model ?? started.model) || "";
  const resolvedProvider =
    stringFrom(metadata.provider ?? started.provider) ||
    providerOptions.find((option) => option.model === summaryModel)?.name ||
    providerFromModel(summaryModel);
  return {
    provider: resolvedProvider || "-",
    model: summaryModel || "-",
    sandbox: stringFrom(metadata.sandbox ?? started.sandbox ?? metadata.permission_mode ?? started.permission_mode) || permissionMode || "-",
    tools: toolNames.size > 0 ? Array.from(toolNames).slice(0, 4).join(", ") : stringFrom(metadata.tools ?? started.tools) || "-",
    mcp: stringFrom(metadata.mcp ?? metadata.mcp_servers ?? started.mcp ?? started.mcp_servers) || "-",
    goal: stringFrom(metadata.goal ?? metadata.goal_id ?? started.goal ?? started.goal_id) || "-"
  };
}

function buildToolActivities(events: AgentTaskEventRecord[]): ToolActivity[] {
  const byID = new Map<string, ToolActivity>();
  const ordered: ToolActivity[] = [];
  const pushOrGet = (id: string, name: string, event: AgentTaskEventRecord, payload: ParsedPayload) => {
    const existing = byID.get(id);
    if (existing) {
      if (!existing.name && name) {
        existing.name = name;
      }
      return existing;
    }
    const activity: ToolActivity = {
      id,
      name: name || "Tool",
      status: "running",
      startedAt: event.created_at || "",
      finishedAt: "",
      elapsed: "-",
      preview: "",
      evidenceSource: "",
      evidenceProvenance: "",
      evidenceArtifacts: "",
      turn: numberFrom(payload.turn)
    };
    byID.set(id, activity);
    ordered.push(activity);
    return activity;
  };
  for (const event of events) {
    if (event.event_type !== "tool_call" && event.event_type !== "tool_result") {
      continue;
    }
    const payload = parsePayload(event.payload_json);
    const name = stringFrom(payload.tool_name ?? payload.name);
    const id = stringFrom(payload.tool_id ?? payload.id) || `${name || "tool"}:${event.id}`;
    const activity = pushOrGet(id, name, event, payload);
    if (event.event_type === "tool_result") {
      activity.status = truthy(payload.is_error) ? "error" : "done";
      activity.finishedAt = event.created_at || activity.finishedAt;
      activity.preview = capabilityLoopSummaryFromToolPayload(payload) || stringFrom(payload.preview ?? payload.output ?? payload.content ?? payload.error);
      activity.evidenceSource = agentEvidenceSourceFromToolPayload(activity.name, payload);
      activity.evidenceProvenance = agentEvidenceProvenanceFromToolPayload(activity.name, payload);
      activity.evidenceArtifacts = agentEvidenceArtifactsFromToolPayload(activity.name, payload);
      activity.elapsed = elapsedBetween(activity.startedAt, activity.finishedAt);
      if (!activity.turn) {
        activity.turn = numberFrom(payload.turn);
      }
    } else {
      activity.elapsed = elapsedBetween(activity.startedAt, "");
    }
  }
  return ordered;
}

// buildSubAgentProgress 把后端落的 nested_agent_progress 事件(payload 内嵌 sub-agent
// 原始事件)聚合成 sub-agent 卡片,字段语义与 TUI 的 agentProgress 对齐。
export function buildSubAgentProgress(events: AgentTaskEventRecord[]): SubAgentProgress[] {
  const byID = new Map<string, SubAgentProgress>();
  const ordered: SubAgentProgress[] = [];
  for (const event of events) {
    if (event.event_type !== "nested_agent_progress") {
      continue;
    }
    const outer = parsePayload(event.payload_json);
    const subType = stringFrom(outer.sub_event_type);
    // sub_task_id 由后端以数字(uint64)落库,必须用 numberFrom 提取,否则聚合失败、每个事件各成一卡。
    const subTaskID = numberFrom(outer.sub_task_id);
    const id = subTaskID > 0 ? String(subTaskID) : `sub:${event.id}`;
    const payload = objectFrom(outer.sub_payload);
    let card = byID.get(id);
    if (!card) {
      card = {
        taskID: id,
        agent: "",
        model: "",
        description: "",
        status: "running",
        detail: "",
        turn: 0,
        toolCalls: 0,
        lastTool: "",
        tokens: "",
        durationMS: 0
      };
      byID.set(id, card);
      ordered.push(card);
    }
    switch (subType) {
      case "started":
        card.agent = stringFrom(payload.agent_name) || card.agent;
        card.model = stringFrom(payload.model) || card.model;
        card.description = stringFrom(payload.description) || card.description;
        card.status = "running";
        break;
      case "turn_start":
        card.status = "running";
        card.turn = numberFrom(payload.turn);
        break;
      case "tool_call":
        card.status = "running";
        card.lastTool = stringFrom(payload.tool_name) || stringFrom(payload.tool_id) || card.lastTool;
        break;
      case "usage":
        card.tokens = `in ${numberFrom(payload.input_tokens)} / out ${numberFrom(payload.output_tokens)}`;
        break;
      case "completed":
        card.status = "done";
        card.turn = numberFrom(payload.turns) || card.turn;
        card.toolCalls = numberFrom(payload.tool_calls) || card.toolCalls;
        card.durationMS = numberFrom(payload.duration_ms);
        break;
      case "failed":
        card.status = "error";
        card.durationMS = numberFrom(payload.duration_ms);
        card.detail = stringFrom(payload.error) || card.detail;
        break;
      case "cancelled":
        card.status = "cancelled";
        card.durationMS = numberFrom(payload.duration_ms);
        break;
      case "timeout":
        card.status = "error";
        card.durationMS = numberFrom(payload.duration_ms);
        card.detail = stringFrom(payload.error) || card.detail;
        break;
    }
  }
  return ordered;
}

function recentAgentEvidenceFromToolActivities(tools: ToolActivity[]): string {
  let fallback = "";
  for (let index = tools.length - 1; index >= 0; index -= 1) {
    const tool = tools[index];
    if (tool.status === "running" || !tool.preview.includes("capability_loop:")) {
      continue;
    }
    const prefixes = [tool.evidenceProvenance, tool.evidenceSource].filter(Boolean);
    let summary = prefixes.length > 0 ? `${prefixes.join(" | ")} | ${tool.preview}` : tool.preview;
    if (tool.evidenceArtifacts) {
      summary = `${summary} | ${tool.evidenceArtifacts}`;
    }
    if (isAgentEvidenceTool(tool.name)) {
      return summary;
    }
    if (!fallback) {
      fallback = summary;
    }
  }
  return fallback;
}

function isAgentEvidenceTool(name: string): boolean {
  return ["task", "agent", "agentget"].includes(name.trim().toLowerCase());
}

function capabilityLoopSummaryFromToolPayload(payload: ParsedPayload): string {
  const direct = capabilityLoopSummaryFromValue(payload.capability_loop);
  if (direct) {
    return direct;
  }
  const nestedResult = capabilityLoopSummaryFromValue(objectFrom(payload.result).capability_loop);
  if (nestedResult) {
    return nestedResult;
  }
  const nestedTaskResult = capabilityLoopSummaryFromValue(objectFrom(objectFrom(payload.task).result).capability_loop);
  if (nestedTaskResult) {
    return nestedTaskResult;
  }
  for (const value of [payload.output, payload.content, payload.preview, payload.error]) {
    const text = stringFrom(value);
    if (!text.includes("capability_loop")) {
      continue;
    }
    const summary = capabilityLoopSummaryFromText(text);
    if (summary) {
      return summary;
    }
  }
  return "";
}

function agentEvidenceSourceFromToolPayload(toolName: string, payload: ParsedPayload): string {
  if (!isAgentEvidenceTool(toolName)) {
    return "";
  }
  const candidates = [payload, parseCapabilityLoopRootFromToolPayload(payload)];
  for (const candidate of candidates) {
    const source = agentEvidenceSourceFromRoot(toolName, candidate);
    if (source) {
      return source;
    }
  }
  return "";
}

function agentEvidenceArtifactsFromToolPayload(toolName: string, payload: ParsedPayload): string {
  if (!isAgentEvidenceTool(toolName)) {
    return "";
  }
  const candidates = [payload, parseCapabilityLoopRootFromToolPayload(payload)];
  for (const candidate of candidates) {
    const artifacts = agentEvidenceArtifactsFromRoot(candidate);
    if (artifacts) {
      return artifacts;
    }
  }
  return "";
}

function agentEvidenceProvenanceFromToolPayload(toolName: string, payload: ParsedPayload): string {
  if (!isAgentEvidenceTool(toolName)) {
    return "";
  }
  const candidates = [payload, parseCapabilityLoopRootFromToolPayload(payload)];
  for (const candidate of candidates) {
    const source = firstPresentText(
      candidate.evidence_source,
      objectFrom(candidate.result).evidence_source,
      objectFrom(objectFrom(candidate.task).result).evidence_source
    );
    if (source) {
      return `source: ${evidenceSourceLabel(source)}`;
    }
  }
  return "";
}

function parseCapabilityLoopRootFromToolPayload(payload: ParsedPayload): ParsedPayload {
  for (const value of [payload.output, payload.content, payload.preview, payload.error]) {
    const text = stringFrom(value);
    if (!text.includes("capability_loop")) {
      continue;
    }
    const wrapped = extractCapabilityLoopWrapperJSON(text);
    const root = parsePayload(wrapped || text);
    if (Object.keys(root).length > 0) {
      return root;
    }
  }
  return {};
}

function agentEvidenceSourceFromRoot(toolName: string, root: ParsedPayload): string {
  if (Object.keys(root).length === 0) {
    return "";
  }
  const task = objectFrom(root.task);
  const result = objectFrom(root.result);
  const taskID = firstPresentText(root.task_id, task.id, result.task_id);
  const agentName = firstPresentText(task.agent_name, root.agent_name, result.agent_name);
  const status = firstPresentText(task.status, root.status, result.status);
  const description = firstPresentText(task.description, root.description, result.description);
  if (!taskID && !agentName && !status && !description) {
    return "";
  }
  const parts = [toolName || "Agent"];
  if (taskID) {
    parts.push(`#${taskID}`);
  }
  if (agentName && agentName.toLowerCase() !== String(toolName || "").trim().toLowerCase()) {
    parts.push(agentName);
  }
  if (status) {
    parts.push(status);
  }
  const label = parts.join(" ");
  return truncateOneLine(description ? `${label}: ${description}` : label, 96);
}

function agentEvidenceArtifactsFromRoot(root: ParsedPayload): string {
  if (Object.keys(root).length === 0) {
    return "";
  }
  const result = objectFrom(root.result);
  const taskResult = objectFrom(objectFrom(root.task).result);
  const fields: Array<[string, string, number]> = [
    ["session_id", firstPresentText(root.session_id, result.session_id, taskResult.session_id), 48],
    ["transcript_path", firstPresentText(root.transcript_path, result.transcript_path, taskResult.transcript_path), 64],
    ["output_file", firstPresentText(root.output_file, result.output_file, taskResult.output_file), 64],
    ["worktree_path", firstPresentText(root.worktree_path, result.worktree_path, taskResult.worktree_path), 64],
    ["worktree_branch", firstPresentText(root.worktree_branch, result.worktree_branch, taskResult.worktree_branch), 48]
  ];
  const parts = fields
    .filter(([, value]) => value)
    .map(([label, value, limit]) => `${label}: ${truncateOneLine(value, limit)}`);
  return parts.length > 0 ? `artifacts: ${parts.join(" | ")}` : "";
}

function evidenceSourceLabel(source: string): string {
  if (source === "terminal_agent_task_store") {
    return "task_store";
  }
  return truncateOneLine(source, 32);
}

function capabilityLoopSummaryFromText(text: string): string {
  const wrapped = extractCapabilityLoopWrapperJSON(text);
  const root = parsePayload(wrapped || text);
  return capabilityLoopSummaryFromValue(root.capability_loop) ||
    capabilityLoopSummaryFromValue(objectFrom(root.result).capability_loop) ||
    capabilityLoopSummaryFromValue(objectFrom(objectFrom(root.task).result).capability_loop);
}

function extractCapabilityLoopWrapperJSON(text: string): string {
  const startTag = "<capability_loop>";
  const endTag = "</capability_loop>";
  const start = text.indexOf(startTag);
  if (start < 0) {
    return "";
  }
  const contentStart = start + startTag.length;
  const end = text.indexOf(endTag, contentStart);
  if (end < 0) {
    return "";
  }
  return text.slice(contentStart, end).trim();
}

function capabilityLoopSummaryFromValue(value: unknown): string {
  const loop = objectFrom(value);
  if (Object.keys(loop).length === 0) {
    return "";
  }
  const parts = [
    ["evidence", firstCapabilityLoopText(loop.evidence)],
    ["assumptions", firstCapabilityLoopText(loop.assumptions)],
    ["unknowns", firstCapabilityLoopText(loop.unknowns)],
    ["verification", firstCapabilityLoopText(loop.verification)],
    ["risks", firstCapabilityLoopText(loop.risks)],
    ["next_action", firstCapabilityLoopText(loop.next_action)],
    ["follow_up_id", firstCapabilityLoopText(loop.follow_up_id)],
    ["resolved_follow_up", firstCapabilityLoopText(loop.resolved_follow_up)],
    ["supersedes_evidence_id", capabilityLoopSupersedesText(loop)]
  ]
    .filter(([, text]) => text)
    .map(([label, text]) => `${label}: ${truncateOneLine(text, capabilityLoopSummaryLimit(label))}`);
  return parts.length > 0 ? `capability_loop: ${parts.join(" | ")}` : "";
}

function capabilityLoopSummaryLimit(label: string): number {
  if (label === "next_action" || label === "evidence" || label === "resolved_follow_up") {
    return 72;
  }
  if (label === "supersedes_evidence_id") {
    return 96;
  }
  return 64;
}

function capabilityLoopSupersedesText(loop: ParsedPayload): string {
  const ids = uniqueCapabilityLoopTexts([
    firstCapabilityLoopText(loop.supersedes_evidence_id),
    ...capabilityLoopTextList(loop.supersedes_evidence_ids)
  ].filter(Boolean));
  return ids.join(",");
}

function uniqueCapabilityLoopTexts(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  values.forEach((value) => {
    if (!value || seen.has(value)) {
      return;
    }
    seen.add(value);
    out.push(value);
  });
  return out;
}

function capabilityLoopTextList(value: unknown): string[] {
  if (!Array.isArray(value)) {
    const text = firstCapabilityLoopText(value);
    return text ? [text] : [];
  }
  return value
    .map((item) => firstCapabilityLoopText(item))
    .filter(Boolean);
}

function firstPresentText(...values: unknown[]): string {
  for (const value of values) {
    if (typeof value === "number" && Number.isFinite(value)) {
      return String(value);
    }
    const text = stringFrom(value);
    if (text) {
      return text;
    }
  }
  return "";
}

function firstCapabilityLoopText(value: unknown): string {
  if (Array.isArray(value)) {
    for (const item of value) {
      const text = stringFrom(item);
      if (text && !isCapabilityLoopPlaceholder(text)) {
        return text;
      }
    }
    return "";
  }
  const text = stringFrom(value);
  return isCapabilityLoopPlaceholder(text) ? "" : text;
}

function isCapabilityLoopPlaceholder(value: string): boolean {
  const normalized = value.trim().replace(/[.。]+$/u, "").toLowerCase();
  return normalized === "" ||
    normalized === "none" ||
    normalized === "none observed" ||
    normalized === "not observed" ||
    normalized === "n/a" ||
    normalized === "na" ||
    normalized === "not applicable" ||
    normalized === "no explicit assumptions reported" ||
    normalized === "no explicit unknowns reported" ||
    normalized === "no explicit verification reported" ||
    normalized === "parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing";
}

function objectFrom(value: unknown): ParsedPayload {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as ParsedPayload) : {};
}

function truncateOneLine(value: string, limit: number): string {
  const text = value.replace(/\s+/g, " ").trim();
  if (text.length <= limit) {
    return text;
  }
  return `${text.slice(0, Math.max(0, limit - 3))}...`;
}

function tokenUsage(payload: ParsedPayload) {
  const usage = parsePayload(payload.usage);
  const source = Object.keys(usage).length > 0 ? { ...payload, ...usage } : payload;
  return {
    cacheReadTokens: numberFrom(source.cache_read_input_tokens ?? source.prompt_cache_hit_tokens ?? source.cached_input_tokens),
    cacheCreationTokens: numberFrom(source.cache_creation_input_tokens ?? source.prompt_cache_miss_tokens ?? source.cache_write_input_tokens),
    inputTokens: numberFrom(source.input_tokens),
    cacheHitPercent: nullablePercent(source.cache_hit_percent ?? source.prompt_cache_hit_rate ?? source.cache_hit_rate)
  };
}

function nullablePercent(value: unknown): number | null {
  if (value === undefined || value === null || value === "") {
    return null;
  }
  const parsed = numberFrom(value);
  if (!Number.isFinite(parsed)) {
    return null;
  }
  const percent = parsed > 0 && parsed <= 1 ? parsed * 100 : parsed;
  return Math.min(100, Math.max(0, Math.round(percent)));
}

function calculateCacheHitPercent(cacheReadTokens: number, cacheCreationTokens: number, inputTokens: number) {
  if (cacheReadTokens <= 0) {
    return null;
  }
  const denominator = inputTokens > 0 ? inputTokens : cacheReadTokens + cacheCreationTokens;
  if (denominator <= 0) {
    return null;
  }
  return Math.min(100, Math.max(0, Math.round((cacheReadTokens * 100) / denominator)));
}

function totalTokenCount(payload: ParsedPayload) {
  return (
    numberFrom(payload.total_tokens) ||
    numberFrom(payload.input_tokens) +
      numberFrom(payload.output_tokens) +
      numberFrom(payload.cache_creation_input_tokens) +
      numberFrom(payload.cache_read_input_tokens)
  );
}

function providerFromModel(model: string) {
  const raw = model.toLowerCase();
  if (raw.includes("claude")) {
    return "anthropic";
  }
  if (raw.includes("gpt") || raw.includes("o3") || raw.includes("o4")) {
    return "openai";
  }
  if (raw.includes("glm")) {
    return "zhipu";
  }
  if (raw.includes("deepseek")) {
    return "deepseek";
  }
  if (raw.includes("kimi") || raw.includes("moonshot")) {
    return "moonshot";
  }
  if (raw.includes("qwen")) {
    return "qwen";
  }
  if (raw.includes("gemini")) {
    return "google";
  }
  return "-";
}

function mergeTasks(current: AgentTaskRecord[], incoming: AgentTaskRecord[]) {
  const byID = new Map<number, AgentTaskRecord>();
  for (const task of current) {
    byID.set(task.id, task);
  }
  for (const task of incoming) {
    byID.set(task.id, task);
  }
  return Array.from(byID.values()).sort((a, b) => timestampOf(b.started_at) - timestampOf(a.started_at) || b.id - a.id);
}

function isConversationEvent(event: AgentTaskEventRecord) {
  return (
    event.event_type === "message" ||
    event.event_type === "text_delta" ||
    event.event_type === "thinking_delta" ||
    event.event_type === "compact_summary" ||
    // 不渲染成消息,仅用于把回复耗时计到真正的结束时刻、累计本轮 token。
    event.event_type === "message_stop" ||
    event.event_type === "usage" ||
    event.event_type === "completed" ||
    // 配对成消息级命令记录(展开可看详情),不单独渲染。
    event.event_type === "tool_call" ||
    event.event_type === "tool_result"
    || event.event_type === "image_artifact" ||
    event.event_type === "user_question_request" ||
    event.event_type === "user_question_resolved"
  );
}

function conversationIdentityKey(identity: IdentityConfig): string {
  return [identity.apiBase, identity.tenantKey, identity.userId, identity.deviceId, identity.apiToken, identity.mobileJwt].join("\u0000");
}

export type MessageToolRun = {
  id: string;
  name: string;
  command: string;
  status: "running" | "done" | "error";
  elapsed: string;
  output: string;
  startedAt: string;
};

export type ConversationMessage = {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  meta: string;
  status?: PendingMessageStatus;
  durationMs?: number;
  provider?: string;
  model?: string;
  tools?: MessageToolRun[];
  tokens?: number;
  taskID?: number;
  attachments?: AgentTaskAttachment[];
  artifacts?: ImageArtifact[];
  kind?: "thinking" | "question";
  question?: AgentUserQuestion;
  thinkingStatus?: ThinkingMessageStatus;
  turn?: number;
  phase?: number;
  lineCount?: number;
};

// 从 tool_call/tool_result 的 input(JSON 字符串)提取人类可读的命令行:
// Bash 取 command,文件类工具取路径,其余压缩成一行原始参数。
function toolCommandFromInput(payload: ParsedPayload): string {
  const raw = stringFrom(payload.input);
  if (!raw) {
    return "";
  }
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (parsed && typeof parsed === "object") {
      const obj = parsed as Record<string, unknown>;
      const primary = stringFrom(obj.command ?? obj.file_path ?? obj.path ?? obj.pattern ?? obj.url ?? obj.query ?? obj.prompt);
      if (primary) {
        return truncateOneLine(primary, 160);
      }
    }
  } catch {
    // 后端把超过 600 字符的 input JSON 按 rune 截断并追加 "...",产生非法 JSON。
    // 尝试从截断文本里提取首个主字段值,避免把乱码 JSON 前缀当命令展示。
    if (raw.startsWith("{")) {
      const match = raw.match(/"(?:command|file_path|path|pattern|url|query|prompt)"\s*:\s*"((?:[^"\\]|\\.)*)/);
      if (match) {
        try {
          return truncateOneLine(JSON.parse(`"${match[1]}"`) as string, 160);
        } catch {
          return truncateOneLine(match[1], 160);
        }
      }
      return "";
    }
    // input 不是 JSON,按原文展示
  }
  return truncateOneLine(raw, 160);
}

// collectLiveToolRuns 取「最后一条用户消息之后」的工具运行,用于回复尚未
// 开始流式输出时(thinking 占位阶段)的实时命令展示。
function collectLiveToolRuns(events: AgentTaskEventRecord[]): MessageToolRun[] {
  let runs: MessageToolRun[] = [];
  let byID = new Map<string, MessageToolRun>();
  for (const event of events) {
    if (event.event_type === "message") {
      const payload = parsePayload(event.payload_json);
      const fromAgent = stringFrom(payload.from_agent).toLowerCase();
      if (fromAgent === "webui" || fromAgent === "user") {
        runs = [];
        byID = new Map();
      }
      continue;
    }
    if (event.event_type === "tool_call" || event.event_type === "tool_result") {
      applyToolEvent(runs, byID, event, parsePayload(event.payload_json));
    }
  }
  return runs;
}

// collectNextStepSuggestions 取「最后一条用户消息之后」的下一步候选:新一轮
// 开始就作废上一轮的建议。next_steps 是纯 UI 信号,刻意不进 isConversationEvent。
export function collectNextStepSuggestions(events: AgentTaskEventRecord[]): string[] {
  let suggestions: string[] = [];
  let latestUserMessageTaskID = 0;
  for (const event of events) {
    if (event.event_type === "message") {
      const payload = parsePayload(event.payload_json);
      const fromAgent = stringFrom(payload.from_agent).toLowerCase();
      if (fromAgent === "webui" || fromAgent === "user") {
        suggestions = [];
        // A continuation chain can deliver an older task's next_steps after a
        // newer user message. Keep the current generation anchored to that
        // message so delayed events cannot replace current suggestions.
        latestUserMessageTaskID = numberFrom(event.task_id);
      }
      continue;
    }
    if (event.event_type === "next_steps") {
      if (latestUserMessageTaskID > 0 && numberFrom(event.task_id) !== latestUserMessageTaskID) {
        continue;
      }
      const payload = parsePayload(event.payload_json);
      const raw = Array.isArray(payload.suggestions) ? payload.suggestions : [];
      suggestions = raw.map((item) => String(item ?? "").trim()).filter((item) => item.length > 0);
    }
  }
  return suggestions;
}

// computeVisibleNextStepSuggestions 是面板可见性的纯派生函数:输入框非空、slash 面板
// 活跃或正在生成时隐藏,但候选本身不清空——输入框一清空就立刻恢复展示。
export function computeVisibleNextStepSuggestions(
  nextStepSuggestions: string[],
  composerText: string,
  slashSuggestionsActive: boolean,
  running: boolean
): string[] {
  if (composerText !== "" || slashSuggestionsActive || running) {
    return [];
  }
  return nextStepSuggestions;
}

// collectToolRuns 把 tool_call/tool_result 事件配对成消息级命令记录。
function applyToolEvent(runs: MessageToolRun[], byID: Map<string, MessageToolRun>, event: AgentTaskEventRecord, payload: ParsedPayload) {
  const name = stringFrom(payload.tool_name ?? payload.name) || "Tool";
  const id = stringFrom(payload.tool_id ?? payload.id) || `${name}:${event.id}`;
  let run = byID.get(id);
  if (!run) {
    run = { id, name, command: toolCommandFromInput(payload), status: "running", elapsed: "", output: "", startedAt: event.created_at || "" };
    byID.set(id, run);
    runs.push(run);
  }
  if (event.event_type === "tool_result") {
    run.status = truthy(payload.is_error) ? "error" : "done";
    run.command = run.command || toolCommandFromInput(payload);
    // 优先取完整 output(后端截断到 2000 字符),preview 只有 160 字符且是兜底;保留多行供展开查看。
    run.output = stringFrom(payload.output ?? payload.preview);
    const elapsed = elapsedBetween(run.startedAt, event.created_at || "");
    run.elapsed = elapsed === "-" ? "" : elapsed;
  }
}

type PendingUserMessage = {
  id: string;
  taskID: number;
  content: string;
  status: PendingMessageStatus;
  createdAt: string;
};

export type ThinkingState = {
  label: string;
  detail: string;
  meta: string[];
};

type ConversationTaskRuntime = {
  provider: string;
  model: string;
};

function resolveConversationTaskRuntime(task: AgentTaskRecord | null, providerOptions: ProviderOption[]): ConversationTaskRuntime {
  if (!task) {
    return { provider: "", model: "" };
  }
  const taskMetadata = parsePayload(task.metadata_json);
  const metaProvider = stringFrom(taskMetadata.provider);
  // Provider routing is by name, so when a provider is named its own config model is
  // what actually ran — trust it over a possibly-stale task.model to keep the pair aligned.
  const namedProvider = metaProvider ? providerOptions.find((option) => option.name === metaProvider) : undefined;
  const fallbackModel = stringFrom(task.model) || stringFrom(taskMetadata.model) || "";
  const model = namedProvider?.model || fallbackModel;
  const provider = metaProvider || providerOptions.find((option) => option.model === model)?.name || (model ? providerFromModel(model) : "");
  return { provider, model };
}

export function buildConversationMessages(
  task: AgentTaskRecord | null,
  events: AgentTaskEventRecord[],
  providerOptions: ProviderOption[] = [],
  conversationTasks: AgentTaskRecord[] = []
): ConversationMessage[] {
  if (!task) {
    return [];
  }
  const taskResult = parsePayload(task.result_json);
  const persistedDurationMs = numberFrom(taskResult.duration_ms);
  const eventTaskDurationMs = observedTaskDurationMs(events);
  const taskByID = new Map<number, AgentTaskRecord>();
  for (const conversationTask of [...conversationTasks, task]) {
    if (conversationTask) {
      taskByID.set(conversationTask.id, conversationTask);
    }
  }
  const fallbackRuntime = resolveConversationTaskRuntime(task, providerOptions);
  const runtimeForEvent = (event: AgentTaskEventRecord): ConversationTaskRuntime =>
    resolveConversationTaskRuntime(taskByID.get(event.task_id) || task, providerOptions);
  const messages: ConversationMessage[] = [];
  let assistantBuffer = "";
  let assistantID = "";
  let assistantMeta = "";
  let promptAt = "";
  let assistantStartAt = "";
  let assistantLastAt = "";
  let assistantTaskID = 0;
  let assistantProvider = "";
  let assistantModel = "";
  let pendingTools: MessageToolRun[] = [];
  let pendingToolsByID = new Map<string, MessageToolRun>();
  const questions = new Map<string, ConversationMessage>();
  const legacyQuestions = new Map<string, { question: string; choices: string[] }>();
  const questionTools: UserQuestionToolCandidate[] = [];
  const claimedQuestionToolIDs = new Set<string>();
  let pendingTokens = 0;
  let currentTurn = 0;
  let thinkingBuffer = "";
  let thinkingID = "";
  let thinkingMeta = "";
  let thinkingStartedAt = "";
  let thinkingLastAt = "";
  let thinkingPhase = 0;
  const flushAssistant = (id: string, preserveTools = false) => {
    if (assistantBuffer.trim() === "" && pendingTools.length === 0) {
      return;
    }
    // 回复耗时:优先从用户发送时刻算到最后一个事件;没有用户消息时退回
    // 首个 delta 到最后事件的生成时长。
    const startMs = timestampOf(promptAt) || timestampOf(assistantStartAt);
    const endMs = timestampOf(assistantLastAt);
    const eventDurationMs = startMs && endMs && endMs > startMs ? endMs - startMs : 0;
    const durationMs = (eventDurationMs >= 1000 ? eventDurationMs : 0) || persistedDurationMs || taskElapsedMs(task) || eventTaskDurationMs;
    if (assistantBuffer.trim() !== "") {
      messages.push({
        id: assistantID || id,
        role: "assistant",
        content: assistantBuffer.trimEnd(),
        meta: assistantMeta,
        durationMs: durationMs || undefined,
        provider: assistantProvider || fallbackRuntime.provider || undefined,
        model: assistantModel || fallbackRuntime.model || undefined,
        tools: preserveTools ? undefined : pendingTools.length > 0 ? pendingTools : undefined,
        tokens: pendingTokens || undefined,
        taskID: assistantTaskID || task.id
      });
    }
    assistantBuffer = "";
    assistantID = "";
    assistantMeta = "";
    promptAt = "";
    assistantStartAt = "";
    assistantLastAt = "";
    assistantTaskID = 0;
    assistantProvider = "";
    assistantModel = "";
    if (!preserveTools) {
      pendingTools = [];
      pendingToolsByID = new Map();
    }
    pendingTokens = 0;
  };
  const flushThinking = () => {
    if (thinkingBuffer.trim() === "") {
      return;
    }
    const startMs = timestampOf(thinkingStartedAt);
    const endMs = timestampOf(thinkingLastAt);
    const durationMs = startMs && endMs && endMs > startMs ? endMs - startMs : 0;
    messages.push({
      id: thinkingID,
      role: "assistant",
      content: thinkingBuffer,
      meta: thinkingMeta,
      kind: "thinking",
      thinkingStatus: thinkingStatusForTask(task.status),
      durationMs: durationMs || undefined,
      turn: currentTurn || 1,
      phase: thinkingPhase,
      lineCount: thinkingBuffer.split(/\r?\n/).filter((line) => line.trim() !== "").length || 1
    });
    thinkingBuffer = "";
    thinkingID = "";
    thinkingMeta = "";
    thinkingStartedAt = "";
    thinkingLastAt = "";
  };
  for (const event of events) {
    const payload = parsePayload(event.payload_json);
    if (event.event_type !== "thinking_delta") {
      flushThinking();
    }
    if (event.event_type === "message") {
      const content = eventSummary(event, payload);
      if (!content) {
        continue;
      }
      flushAssistant(`assistant-before-${event.id}`);
      const fromAgent = stringFrom(payload.from_agent).toLowerCase();
      const role = fromAgent === "webui" || fromAgent === "user" ? "user" : "assistant";
      if (role === "user") {
        currentTurn += 1;
        promptAt = event.created_at || promptAt;
      }
      messages.push({
        id: `event-${event.id}`,
        role,
        content,
        meta: formatMinuteTime(event.created_at),
        attachments: agentTaskAttachmentsFromPayload(payload.attachments),
        taskID: event.task_id || task.id
      });
      continue;
    }
    if (event.event_type === "image_artifact") {
      const artifact = imageArtifactFromPayload(payload);
      if (artifact) {
        // image_artifact can arrive between tool_call and tool_result. Keep the
        // in-flight tool in the buffer so its later result closes the same card
        // instead of leaving a historical "running" spinner behind.
        flushAssistant(`assistant-before-image-${event.id}`, true);
        messages.push({ id: `image-${event.id}`, role: "assistant", content: "", meta: formatMinuteTime(event.created_at), artifacts: [artifact], taskID: event.task_id || task.id });
      }
      continue;
    }
    if (event.event_type === "user_question_request") {
      const requestID = stringFrom(payload.request_id);
      const prompt = stringFrom(payload.question);
      if (requestID && prompt) {
        const taskID = event.task_id || task.id;
        const toolID = claimUserQuestionToolID(questionTools, taskID, prompt, stringFrom(payload.tool_id));
        if (toolID) claimedQuestionToolIDs.add(questionToolKey(taskID, toolID));
        flushAssistant(`assistant-before-question-${event.id}`);
        const message: ConversationMessage = {
          id: `question-${event.task_id || task.id}-${requestID}`,
          role: "assistant",
          kind: "question",
          content: prompt,
          meta: formatMinuteTime(event.created_at),
          taskID: event.task_id || task.id,
          question: {
            taskID: event.task_id || task.id,
            requestID,
            choices: questionChoices(payload.choices),
            expiresAt: stringFrom(payload.expires_at) || undefined,
            status: "pending"
          }
        };
        messages.push(message);
        questions.set(`${event.task_id || task.id}:${requestID}`, message);
      }
      continue;
    }
    if (event.event_type === "user_question_resolved") {
      const requestID = stringFrom(payload.request_id);
      const message = questions.get(`${event.task_id || task.id}:${requestID}`);
      const status = questionResolutionStatus(payload.status);
      if (message?.question && status) {
        message.question.status = status;
        message.question.answer = stringFrom(payload.answer) || undefined;
      }
      continue;
    }
    if (event.event_type === "thinking_delta") {
      // Preserve whitespace between streamed deltas; trimming each chunk can
      // merge words and lose intentional line breaks in the archived phase.
      const content = contentFrom(payload.content ?? payload.text ?? payload.thinking);
      if (!content.trim()) {
        continue;
      }
      flushAssistant(`assistant-before-thinking-${event.id}`);
      if (!thinkingID) {
        thinkingID = `thinking-${event.task_id || task.id}-${event.id}`;
        thinkingPhase += 1;
        thinkingStartedAt = event.created_at || "";
      }
      thinkingBuffer = appendStreamText(thinkingBuffer, content);
      thinkingMeta = formatMinuteTime(event.created_at);
      thinkingLastAt = event.created_at || thinkingLastAt;
      continue;
    }
    if (event.event_type === "compact_summary") {
      const summary = stringFrom(payload.summary ?? payload.content);
      if (!summary) {
        continue;
      }
      flushAssistant(`assistant-before-compact-${event.id}`);
      messages.push({
        id: `compact-${event.id}`,
        role: "system",
        content: `${copyCompactPrefix()} ${summary}`,
        meta: formatMinuteTime(event.created_at)
      });
      continue;
    }
    if (event.event_type === "text_delta") {
      const content = eventSummary(event, payload);
      if (!content) {
        continue;
      }
      if (!assistantID) {
        assistantID = `assistant-${event.task_id || task.id}-${event.id}`;
        assistantTaskID = event.task_id || task.id;
        assistantStartAt = event.created_at || "";
        const runtime = runtimeForEvent(event);
        assistantProvider = runtime.provider;
        assistantModel = runtime.model;
      }
      assistantBuffer += content;
      assistantMeta = formatMinuteTime(event.created_at);
      assistantLastAt = event.created_at || assistantLastAt;
      continue;
    }
    if (event.event_type === "usage" || event.event_type === "message_stop") {
      // usage 事件通常是累计到当前的总量,取本轮见到的最大值,避免多事件累加翻倍。
      const total = numberFrom(payload.total_tokens) || totalTokenCount(payload);
      if (total > pendingTokens) {
        pendingTokens = total;
      }
    }
    if ((event.event_type === "message_stop" || event.event_type === "completed") && assistantBuffer.trim() !== "") {
      assistantLastAt = event.created_at || assistantLastAt;
      continue;
    }
    if (event.event_type === "tool_call" || event.event_type === "tool_result") {
      const toolName = stringFrom(payload.tool_name ?? payload.name);
      const toolID = stringFrom(payload.tool_id ?? payload.id) || `${toolName}:${event.id}`;
      if (event.event_type === "tool_call" && toolName === "AskUserQuestion") {
        const legacy = parseUserQuestionInput(payload.input);
        if (legacy) {
          legacyQuestions.set(toolID, legacy);
          questionTools.push({ active: true, prompt: legacy.question, taskID: event.task_id || task.id, toolID });
        }
      }
      const questionTool = [...questionTools].reverse().find((candidate) => candidate.taskID === (event.task_id || task.id) && candidate.toolID === toolID);
      if (event.event_type === "tool_result" && questionTool) questionTool.active = false;
      if (event.event_type === "tool_result" && toolName === "AskUserQuestion" && truthy(payload.is_error) && !claimedQuestionToolIDs.has(questionToolKey(event.task_id || task.id, toolID))) {
        const legacy = legacyQuestions.get(toolID) ?? parseUserQuestionInput(payload.input);
        if (legacy) {
          pendingTools = pendingTools.filter((tool) => tool.id !== toolID);
          pendingToolsByID.delete(toolID);
          flushAssistant(`assistant-before-legacy-question-${event.id}`);
          messages.push({
            id: `legacy-question-${event.task_id || task.id}-${event.id}`,
            role: "assistant",
            kind: "question",
            content: legacy.question,
            meta: formatMinuteTime(event.created_at),
            taskID: event.task_id || task.id,
            question: { taskID: event.task_id || task.id, choices: legacy.choices, status: "unavailable", legacy: true }
          });
          continue;
        }
      }
      applyToolEvent(pendingTools, pendingToolsByID, event, payload);
      assistantLastAt = event.created_at || assistantLastAt;
    }
  }
  flushThinking();
  flushAssistant(`assistant-${task.id}-${events.length}`);
  if (terminalStatuses.has(task.status || "")) {
    for (const message of messages) if (message.question?.status === "pending") message.question.status = "unavailable";
  }
  const lastAssistant = [...messages].reverse().find((message) => message.role === "assistant" && message.kind !== "thinking");
  return reconcileCompletedAssistantMessage(messages, task, lastAssistant?.provider || fallbackRuntime.provider, lastAssistant?.model || fallbackRuntime.model);
}

function questionResolutionStatus(value: unknown): "answered" | "cancelled" | "expired" | undefined {
  return value === "answered" || value === "cancelled" || value === "expired" ? value : undefined;
}

function questionToolKey(taskID: number, toolID: string): string {
  return `${taskID}:${toolID}`;
}

function agentTaskAttachmentsFromPayload(value: unknown): AgentTaskAttachment[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const attachments = value.flatMap((item) => {
    if (!item || typeof item !== "object") {
      return [];
    }
    const raw = item as Record<string, unknown>;
    if (raw.type !== "image" || typeof raw.media_type !== "string" || typeof raw.size_bytes !== "number") {
      return [];
    }
    return [{
      attachment_id: typeof raw.attachment_id === "string" ? raw.attachment_id : undefined,
      type: "image" as const,
      media_type: raw.media_type,
      name: typeof raw.name === "string" ? raw.name : undefined,
      url: typeof raw.url === "string" ? raw.url : undefined,
      size_bytes: raw.size_bytes,
      sha256: typeof raw.sha256 === "string" ? raw.sha256 : undefined
    }];
  });
  return attachments.length > 0 ? attachments : undefined;
}

function imageArtifactFromPayload(payload: ParsedPayload): ImageArtifact | undefined {
  const candidate = payload.asset && typeof payload.asset === "object" ? payload.asset : payload;
  if (!candidate || typeof candidate !== "object") return undefined;
  const raw = candidate as Record<string, unknown>;
  if (typeof raw.asset_id !== "string" || typeof raw.url !== "string" || typeof raw.media_type !== "string") return undefined;
  return {
    asset_id: raw.asset_id,
    generation_id: typeof raw.generation_id === "string" ? raw.generation_id : "",
    operation: raw.operation === "edit" ? "edit" : "generate",
    media_type: raw.media_type,
    width: numberFrom(raw.width) || undefined,
    height: numberFrom(raw.height) || undefined,
    size_bytes: numberFrom(raw.size_bytes) || undefined,
    sha256: typeof raw.sha256 === "string" ? raw.sha256 : undefined,
    url: raw.url,
    session_id: numberFrom(raw.session_id) || 0
  };
}

export function normalizeThinkingMode(value: string): ThinkingMode {
  const normalized = value.trim().toLowerCase();
  if (normalized === "summary" || normalized === "hidden" || normalized === "full") {
    return normalized;
  }
  return "full";
}

export function resolveWebAgentThinkingMode(doc: Record<string, unknown>): ThinkingMode {
  const settings = doc.webAgentUI;
  if (!settings || typeof settings !== "object" || Array.isArray(settings)) {
    return DEFAULT_THINKING_MODE;
  }
  const webAgentSettings = settings as Record<string, unknown>;
  if (typeof webAgentSettings.thinkingMode === "string") {
    const mode = webAgentSettings.thinkingMode.trim().toLowerCase();
    if (mode === "full" || mode === "summary" || mode === "hidden") {
      return mode;
    }
  }
  if (webAgentSettings.showThinking === false) {
    return "hidden";
  }
  if (webAgentSettings.showThinking === true) {
    return "full";
  }
  return DEFAULT_THINKING_MODE;
}

function thinkingStatusForTask(status: string | undefined): ThinkingMessageStatus {
  const normalized = (status || "").trim().toLowerCase();
  if (normalized === TASK_STATUS.failed) {
    return "failed";
  }
  if (normalized === TASK_STATUS.cancelled) {
    return "cancelled";
  }
  if (normalized === TASK_STATUS.timeout || normalized === "timed_out") {
    return "timed_out";
  }
  if (terminalStatuses.has(normalized)) {
    return "completed";
  }
  return "streaming";
}

function copyCompactPrefix() {
  return "Compact summary:";
}

function reconcileCompletedAssistantMessage(
  messages: ConversationMessage[],
  task: AgentTaskRecord,
  provider = "",
  model = ""
): ConversationMessage[] {
  if ((task.status || "").toLowerCase() !== TASK_STATUS.completed) {
    return messages;
  }
  const result = parsePayload(task.result_json);
  const response = stringFrom(result.response);
  if (!response) {
    return messages;
  }
  const lastAssistantIndex = findLastIndex(messages, (message) => message.role === "assistant");
  if (lastAssistantIndex < 0) {
    const fallback: ConversationMessage = {
      id: `assistant-result-${task.id}`,
      role: "assistant",
      content: response,
      meta: formatMinuteTime(task.finished_at || task.started_at),
      provider: provider || undefined,
      model: model || undefined,
      taskID: task.id
    };
    return [...messages, fallback];
  }
  const current = messages[lastAssistantIndex];
  if (current.content === response) {
    return messages;
  }
  // result.response 是完整原文(换行齐全);拼接的 text_delta 可能因传输
  // 丢了换行而无法严格 startsWith,这里用"忽略空白"的宽松前缀判定,只要
  // 是同一段回复就采信完整 response 覆盖,让完成态自愈换行结构。
  const collapse = (value: string) => value.replace(/\s+/g, "");
  if (!collapse(response).startsWith(collapse(current.content))) {
    return messages;
  }
  return messages.map((message, index) => (index === lastAssistantIndex ? { ...message, content: response } : message));
}

function isFinalAssistantResponse(message: ConversationMessage, task: AgentTaskRecord | null) {
  if (!task || message.taskID !== task.id || !terminalStatuses.has(task.status || "")) {
    return false;
  }
  if ((task.status || "").toLowerCase() !== TASK_STATUS.completed) {
    return true;
  }
  const response = stringFrom(parsePayload(task.result_json).response);
  return Boolean(response) && response === message.content;
}

function addSetValue(current: Set<number>, value: number) {
  if (current.has(value)) {
    return current;
  }
  const next = new Set(current);
  next.add(value);
  return next;
}

function removeSetValue(current: Set<number>, value: number) {
  if (!current.has(value)) {
    return current;
  }
  const next = new Set(current);
  next.delete(value);
  return next;
}

function findLastIndex<T>(items: T[], predicate: (item: T) => boolean) {
  for (let index = items.length - 1; index >= 0; index--) {
    if (predicate(items[index])) {
      return index;
    }
  }
  return -1;
}

function mergePendingUserMessages(messages: ConversationMessage[], pendingMessages: PendingUserMessage[]) {
  const next = [...messages];
  for (const pending of pendingMessages) {
    if (messages.some((message) => message.role === "user" && message.content === pending.content)) {
      continue;
    }
    next.push({
      id: pending.id,
      role: "user",
      content: pending.content,
      meta: pending.status === "failed" ? "failed" : pending.status === "sent" ? "sent" : "sending",
      status: pending.status
    });
  }
  return next;
}

function shouldShowThinkingPlaceholder(messages: ConversationMessage[], running: boolean, composerState: ComposerState, streamState: StreamState) {
  if (!(running || composerState === "sending" || streamState === "connecting" || streamState === "live")) {
    return false;
  }
  const lastMessage = messages.at(-1);
  return !lastMessage || lastMessage.role === "user";
}

function summarizeThinkingState(copy: AgentCopy, activity: ReturnType<typeof summarizeActivity>, composerState: ComposerState, streamState: StreamState): ThinkingState {
  const meta = [
    activity.elapsed && activity.elapsed !== "-" ? `${copy.elapsed} ${activity.elapsed}` : "",
    activity.eventCount > 0 ? `${activity.eventCount} ${copy.eventCount}` : "",
    streamState !== "idle" ? `${copy.eventStream} ${copy.streamStates[streamState]}` : ""
  ].filter(Boolean);
  if (composerState === "sending") {
    return { label: copy.thinkingStates.sending, detail: copy.thinkingStates.sendingDetail, meta };
  }
  if (activity.commandsRun > 0) {
    return { label: copy.thinkingStates.runningCommand, detail: activity.currentFile || copy.thinkingStates.liveDetail, meta };
  }
  if (activity.filesRead > 0 || activity.currentFile) {
    return { label: copy.thinkingStates.readingContext, detail: activity.currentFile || copy.thinkingStates.liveDetail, meta };
  }
  if (streamState === "connecting") {
    return { label: copy.thinkingStates.connecting, detail: copy.streamStates.connecting, meta };
  }
  return { label: copy.thinkingStates.thinking, detail: activity.elapsed && activity.elapsed !== "-" ? activity.elapsed : copy.thinkingStates.liveDetail, meta };
}

function getConversationChain(tasks: AgentTaskRecord[], taskID: number) {
  const byID = new Map(tasks.map((task) => [task.id, task]));
  const chain: AgentTaskRecord[] = [];
  const seen = new Set<number>();
  let current = byID.get(taskID) || null;
  while (current && !seen.has(current.id)) {
    chain.unshift(current);
    seen.add(current.id);
    const parentID = numberFrom(parsePayload(current.metadata_json).continuation_of_task_id);
    current = parentID ? byID.get(parentID) || null : null;
  }
  return chain;
}

function buildWebAgentSessionViews(tasks: AgentTaskRecord[], sessions: TenantSession[]): WebAgentSessionView[] {
  const sessionByID = new Map(sessions.map((session) => [session.id, session]));
  const byConversation = new Map<string, AgentTaskRecord[]>();
  const rootCache = new Map<number, number>();
  const parentByID = new Map<number, number>();
  for (const task of tasks) {
    const parentID = numberFrom(parsePayload(task.metadata_json).continuation_of_task_id);
    if (parentID) {
      parentByID.set(task.id, parentID);
    }
  }
  const rootTaskID = (task: AgentTaskRecord): number => {
    const cached = rootCache.get(task.id);
    if (cached) {
      return cached;
    }
    const seen = new Set<number>();
    let current = task.id;
    while (parentByID.has(current) && !seen.has(current)) {
      seen.add(current);
      current = parentByID.get(current) || current;
    }
    for (const id of seen) {
      rootCache.set(id, current);
    }
    rootCache.set(task.id, current);
    return current;
  };
  for (const task of tasks) {
    const metadata = parsePayload(task.metadata_json);
    const parentSessionID = numberFrom(task.parent_session_id) || numberFrom(metadata.web_agent_session_id);
    const key = parentSessionID ? `session:${parentSessionID}` : `legacy:${rootTaskID(task)}`;
    const items = byConversation.get(key) || [];
    items.push(task);
    byConversation.set(key, items);
  }
  const views: WebAgentSessionView[] = [];
  for (const [key, items] of byConversation) {
    const ordered = [...items].sort((a, b) => timestampOf(a.started_at) - timestampOf(b.started_at) || a.id - b.id);
    const latestTask = [...ordered].sort((a, b) => timestampOf(b.started_at) - timestampOf(a.started_at) || b.id - a.id)[0];
    if (!latestTask) {
      continue;
    }
    const latestMetadata = parsePayload(latestTask.metadata_json);
    const parentSessionID = numberFrom(latestTask.parent_session_id) || numberFrom(latestMetadata.web_agent_session_id);
    const tenantSession = parentSessionID ? sessionByID.get(parentSessionID) || null : null;
    const sessionMetadata = parsePayload(tenantSession?.metadata_json);
    const cwd = stringFrom(tenantSession?.cwd) || stringFrom(latestMetadata.cwd) || stringFrom(sessionMetadata.cwd) || "unknown";
    const workspaceName = stringFrom(sessionMetadata.workspace_name) || stringFrom(latestMetadata.workspace_name) || basename(cwd);
    views.push({
      id: parentSessionID ? `session:${parentSessionID}` : key,
      sessionID: parentSessionID,
      sessionKey: stringFrom(tenantSession?.session_key) || stringFrom(latestMetadata.web_agent_session_key),
      title: stringFrom(tenantSession?.title) || latestTask.description || latestTask.agent_name || `Session ${latestTask.id}`,
      cwd,
      workspaceName,
      latestTask,
      tasks: ordered,
      status: latestTask.status || stringFrom(tenantSession?.status) || "unknown",
      updatedAt: meaningfulTime(latestTask.finished_at) || meaningfulTime(latestTask.started_at) || meaningfulTime(tenantSession?.last_message_at) || meaningfulTime(tenantSession?.started_at)
    });
  }
  return views.sort((a, b) => timestampOf(b.updatedAt) - timestampOf(a.updatedAt) || b.latestTask.id - a.latestTask.id);
}

async function loadWebAgentConversationSource(identity: IdentityConfig): Promise<WebAgentConversationSource> {
  try {
    const conversations = await listWebAgentConversations(identity, 100);
    if (conversations.length > 0) {
      const views = conversations.map(webAgentConversationToView);
      return {
        tasks: conversations.flatMap((conversation) => mergeConversationTasks(conversation)),
        sessions: conversations.map((conversation) => conversation.session).filter((session): session is TenantSession => Boolean(session)),
        views
      };
    }
  } catch (err) {
    if (!isWebAgentConversationFallbackError(err)) {
      throw err;
    }
  }
  const [taskItems, sessionItems] = await Promise.all([listAgentTasks(identity, 100), listTenantSessions(identity, 100)]);
  return {
    tasks: taskItems,
    sessions: sessionItems,
    views: buildWebAgentSessionViews(taskItems, sessionItems)
  };
}

function isWebAgentConversationFallbackError(err: unknown) {
  const message = err instanceof Error ? err.message : String(err);
  return message.includes("404") || message.includes("<!doctype") || message.includes("not found") || message.includes("Cannot GET");
}

function webAgentConversationToView(conversation: WebAgentConversation): WebAgentSessionView {
  const tasks = mergeConversationTasks(conversation).sort((a, b) => timestampOf(a.started_at) - timestampOf(b.started_at) || a.id - b.id);
  const latestTask = conversation.latest_task || tasks.at(-1) || ({ id: 0, status: conversation.status || "unknown" } as AgentTaskRecord);
  const sessionID = numberFrom(conversation.session_id) || numberFrom(conversation.session?.id);
  const cwd = stringFrom(conversation.cwd) || stringFrom(conversation.session?.cwd) || stringFrom(parsePayload(latestTask?.metadata_json).cwd) || "unknown";
  const workspaceName = stringFrom(conversation.workspace_name) || stringFrom(parsePayload(latestTask?.metadata_json).workspace_name) || basename(cwd);
  return {
    id: conversation.id || (sessionID ? `session:${sessionID}` : `legacy:${latestTask?.id || 0}`),
    sessionID,
    sessionKey: stringFrom(conversation.session_key) || stringFrom(conversation.session?.session_key),
    title: stringFrom(conversation.title) || latestTask?.description || latestTask?.agent_name || `Session ${latestTask?.id || ""}`,
    cwd,
    workspaceName,
    latestTask,
    tasks,
    status: conversation.status || latestTask?.status || "unknown",
    updatedAt: meaningfulTime(conversation.updated_at) || meaningfulTime(latestTask?.finished_at) || meaningfulTime(latestTask?.started_at)
  };
}

function mergeConversationTasks(conversation: WebAgentConversation): AgentTaskRecord[] {
  const byID = new Map<number, AgentTaskRecord>();
  for (const task of conversation.tasks || []) {
    byID.set(task.id, task);
  }
  if (conversation.latest_task) {
    byID.set(conversation.latest_task.id, conversation.latest_task);
  }
  return [...byID.values()];
}

function collectWorkspaces(sessions: WebAgentSessionView[], validated: AgentWorkspaceValidation | null) {
  const byCwd = new Map<string, { cwd: string; name: string; count: number }>();
  if (validated?.cwd) {
    byCwd.set(validated.cwd, { cwd: validated.cwd, name: validated.workspace_name || basename(validated.cwd), count: 0 });
  }
  for (const session of sessions) {
    const cwd = session.cwd;
    if (!cwd) {
      continue;
    }
    const current = byCwd.get(cwd) || { cwd, name: session.workspaceName || basename(cwd), count: 0 };
    current.count += 1;
    byCwd.set(cwd, current);
  }
  return Array.from(byCwd.values()).sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}

function groupSessionsByWorkspace(sessions: WebAgentSessionView[], fallbackCwd: string) {
  const grouped = new Map<string, WebAgentSessionView[]>();
  for (const session of sessions) {
    const cwd = session.cwd || fallbackCwd || "unknown";
    const items = grouped.get(cwd) || [];
    items.push(session);
    grouped.set(cwd, items);
  }
  return grouped;
}

function sessionMatchesSearch(session: WebAgentSessionView, needle: string) {
  const latest = session.latestTask;
  const values = [
    session.title,
    session.cwd,
    session.workspaceName,
    session.status,
    session.sessionKey,
    latest.description,
    latest.agent_name,
    latest.status
  ];
  return values.some((value) => stringFrom(value).toLowerCase().includes(needle));
}


function readStoredRecord(key: string) {
  if (typeof window === "undefined") {
    return {};
  }
  try {
	    const raw = readProductStorage(key);
    const parsed = raw ? JSON.parse(raw) as unknown : {};
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, boolean> : {};
  } catch {
    return {};
  }
}

function writeStoredRecord(key: string, value: Record<string, boolean>) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // Ignore storage failures; the UI state still works for the current page.
  }
}

function parsePayload(value: unknown): ParsedPayload {
  if (!value || typeof value !== "string") {
    return {};
  }
  try {
    const parsed = JSON.parse(value) as unknown;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as ParsedPayload) : {};
  } catch {
    return {};
  }
}

function eventSummary(_event: AgentTaskEventRecord, payload: ParsedPayload) {
  const content = contentFrom(payload.content ?? payload.text ?? payload.message ?? payload.summary ?? payload.tool_name);
  if (content) {
    return content;
  }
  return "";
}

function stringFrom(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function contentFrom(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberFrom(value: unknown): number {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : 0;
  }
  return 0;
}

function truthy(value: unknown): boolean {
  if (typeof value === "boolean") {
    return value;
  }
  if (typeof value === "string") {
    return value === "true" || value === "1";
  }
  if (typeof value === "number") {
    return value !== 0;
  }
  return false;
}

function normalizePromptMode(value: unknown, fallback: PromptMode): PromptMode {
  const mode = stringFrom(value).toLowerCase();
  return mode === "chat" || mode === "code" ? mode : fallback;
}

function slashCommandPrefix(value: string): string | null {
  const trimmedLeft = value.replace(/^[ \t]+/, "");
  if (!trimmedLeft.startsWith("/")) {
    return null;
  }
  const withoutSlash = trimmedLeft.slice(1);
  if (/[\s]/.test(withoutSlash)) {
    return null;
  }
  return withoutSlash.trim().toLowerCase();
}

function stringsEqualSlashName(prefix: string, name: string): boolean {
  return prefix.toLowerCase() === name.trim().replace(/^\//, "").toLowerCase();
}

function makeWebAgentTraceID() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `web-agent-${crypto.randomUUID()}`;
  }
  return `web-agent-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function basename(path: string) {
  const trimmed = path.replace(/\/+$/, "");
  return trimmed.split("/").filter(Boolean).pop() || trimmed || "workspace";
}

function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function formatMinuteTime(value?: string) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString(undefined, {
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit"
  });
}

function timestampOf(value?: string) {
  if (!value) {
    return 0;
  }
  if (value.startsWith("0001-01-01")) {
    return 0;
  }
  const timestamp = new Date(value).getTime();
  return Number.isFinite(timestamp) ? timestamp : 0;
}

function taskElapsedMs(task: AgentTaskRecord | null) {
  if (!task) {
    return 0;
  }
  const startMs = timestampOf(task.started_at);
  const endMs = timestampOf(task.finished_at);
  return startMs && endMs && endMs > startMs ? endMs - startMs : 0;
}

function observedTaskDurationMs(events: AgentTaskEventRecord[]) {
  let startedAt = 0;
  let finishedAt = 0;
  for (const event of events) {
    const timestamp = timestampOf(event.created_at);
    if (!timestamp) {
      continue;
    }
    if (event.event_type === "started" && !startedAt) {
      startedAt = timestamp;
    }
    if (event.event_type === "completed" || event.event_type === "failed" || event.event_type === "cancelled" || event.event_type === "timeout") {
      finishedAt = Math.max(finishedAt, timestamp);
    }
  }
  return startedAt && finishedAt && finishedAt > startedAt ? finishedAt - startedAt : 0;
}

function meaningfulTime(value?: string) {
  return timestampOf(value) > 0 ? value || "" : "";
}

function relativeTime(value?: string) {
  if (!value) {
    return "no time";
  }
  const date = new Date(value).getTime();
  if (Number.isNaN(date)) {
    return value;
  }
  const seconds = Math.max(1, Math.round((Date.now() - date) / 1000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m`;
  }
  return `${Math.round(minutes / 60)}h`;
}



function elapsedBetween(start?: string, end?: string) {
  const startMs = timestampOf(start);
  if (!startMs) {
    return "-";
  }
  const endMs = timestampOf(end) || Date.now();
  return formatDuration(Math.max(0, endMs - startMs));
}

function statusClass(status: string): string {
  const raw = status.toLowerCase();
  if (raw === TASK_STATUS.completed) {
    return "ok";
  }
  if (raw === TASK_STATUS.failed || raw === TASK_STATUS.cancelled || raw === TASK_STATUS.timeout) {
    return "fail";
  }
  if (raw === TASK_STATUS.running) {
    return "warn";
  }
  return "neutral";
}

function webuiHomeHref(identity: IdentityConfig): string {
  if (typeof window === "undefined") {
    return "/webui/";
  }
  const url = new URL("/webui/", window.location.origin);
  const currentToken = new URLSearchParams(window.location.search).get("token") || identity.apiToken.trim();
  if (currentToken) {
    url.searchParams.set("token", currentToken);
  }
  return `${url.pathname}${url.search}`;
}
