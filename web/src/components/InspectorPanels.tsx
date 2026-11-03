import { Activity, Brain, ChevronDown, ChevronRight, Clock3, Database, Download, FileSearch, FileText, Gauge, History, Play, Plus, RadioTower, RefreshCcw, Save, Sparkles, Square, UserRound } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { AgentCockpit } from "./AgentCockpit";
import { QuotaPanel } from "./QuotaPanel";
import { ValidationPanel } from "./ValidationPanel";
import {
  getEffectiveSkill,
  getProfile,
  getStatus,
  getTenantSkill,
  getLocalTrace,
  getRuntimeTraceArtifact,
  getTrace,
  getRuntimeBackgroundLogs,
  createRuntimeLoop,
  listLocalTraceSessions,
  listMemoryReviewCandidates,
  listMobileMessages,
  listMobileSessions,
  listDocuments,
  listEffectiveSkills,
  listKnowledgeDocuments,
  listManagedMemory,
  listMemories,
  listSkillOverrides,
  listTeamMemory,
  listTelemetry,
  listTenantSkills,
  listRuntimeBackground,
  listRuntimeRuns,
  reviewMemoryCandidate,
  rollbackTenantSkill,
  renderTenantSkillPackage,
  saveDocument,
  saveKnowledgeDocument,
  saveManagedMemory,
  saveMemory,
  saveProfile,
  saveSkillOverride,
  saveTeamMemory,
  saveTenantSkill,
  publishTenantSkillPackage,
  verifyTenantSkillPackageRuntime,
  searchKnowledge,
  runRuntimeBackground,
  stopRuntimeBackground,
  updateRuntimeLoop
} from "../lib/api";
import { useI18n } from "../lib/i18n";
import { useTracePanel } from "../hooks/useTracePanel";
import { useLoopPanel } from "../hooks/useLoopPanel";
import { useMemoryPanel } from "../hooks/useMemoryPanel";
import { useSkillPanel } from "../hooks/useSkillPanel";
import type {
  DocumentRecord,
  IdentityConfig,
  KnowledgeChunkRecord,
  KnowledgeDocumentRecord,
  MemoryRecord,
  ProfileRecord,
  RuntimeBackgroundJob,
  ServerStatus,
  SkillRecord,
  TelemetryRecord,
  TraceDetail,
  TraceQuality,
  TraceRecap,
  TraceSessionSummary,
  TenantMessage,
  TenantSession
} from "../lib/types";

type Props = {
  identity: IdentityConfig;
  selectedSessionId: number | null;
  activeSection: InspectorSection;
  refreshTick: number;
  onSelectSession: (id: number | null) => void;
  onStatus: (message: string) => void;
};

export type InspectorSection = "validation" | "memory" | "profile" | "documents" | "knowledge-search" | "scoped-memory" | "automem" | "skills" | "agents" | "loops" | "trace" | "local-trace" | "telemetry" | "quota";

export type RefreshTask<T> = {
  label: string;
  run: () => Promise<T>;
};

export function settledValue<T>(result: PromiseSettledResult<T>, fallback: T): T {
  return result.status === "fulfilled" ? result.value : fallback;
}

export async function runRefreshTask<T>(task: RefreshTask<T>): Promise<{ label: string; result: PromiseSettledResult<T> }> {
  try {
    return { label: task.label, result: { status: "fulfilled", value: await task.run() } };
  } catch (reason) {
    return { label: task.label, result: { status: "rejected", reason } };
  }
}

export function settledErrorSummary(results: Array<{ label: string; result: PromiseSettledResult<unknown> }>): string {
  const errors = results
    .filter((item) => item.result.status === "rejected")
    .map((item) => {
      const reason = item.result.status === "rejected" ? item.result.reason : null;
      const message = reason instanceof Error ? reason.message : String(reason);
      return `${item.label}: ${message}`;
    });
  return errors.slice(0, 3).join("; ");
}

function skillRecordKey(item: SkillRecord): string {
  return [item.skill_key || "skill", item.version || 1, item.id || ""].join(":");
}

function isSelectedSkill(item: SkillRecord, selectedKey: string, selectedVersion: number): boolean {
  return item.skill_key === selectedKey && Number(item.version || 1) === Number(selectedVersion || 1);
}

type TraceEventRecord = {
  id?: string;
  type?: string;
  name?: string;
  time?: string;
  status?: string;
  role?: string;
  content?: string;
  tool_name?: string;
  model?: string;
  duration_ms?: number;
  input?: string;
  output?: string;
  is_error?: boolean;
  skill?: string;
  trace_id?: string;
  properties?: Record<string, unknown>;
};

type TraceSpanRecord = {
  id?: string;
  parent_id?: string;
  sequence?: number;
  depth?: number;
  trace_id?: string;
  start?: string;
  end?: string;
  type?: string;
  name?: string;
  status?: string;
  duration_ms?: number;
  self_duration_ms?: number;
  tool_name?: string;
  model?: string;
  skill?: string;
  error?: string;
};

type MemoryReviewCandidate = {
  item: MemoryRecord;
  key: string;
  metadata: Record<string, unknown>;
  candidateType: string;
  riskStatus: string;
  sourceSessionId: number | null;
  sourceMessageId: number | null;
  traceId: string;
};

export type LocalTraceTimeFilter = "all" | "today" | "24h" | "7d";

export type LocalTraceFilterOptions = {
  search: string;
  timeFilter: LocalTraceTimeFilter;
  projectOnly: boolean;
  includeTestSessions?: boolean;
  currentProject: string;
  now?: Date;
};

export type SkillTelemetrySummary = {
  key: string;
  skill: string;
  source: string;
  version: string;
  fallback: string;
  count: number;
  errors: number;
  durationMs: number;
  tokens: number;
  latestAt: number;
};

export function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.hidden = true;
  try {
    document.body.appendChild(link);
    link.click();
  } finally {
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }
}

export function InspectorPanels({ identity, selectedSessionId, activeSection, refreshTick, onSelectSession, onStatus }: Props) {
  const { t } = useI18n();
  const {
    traceView,
    setTraceView,
    selectedTraceId,
    setSelectedTraceId,
    localTraceView,
    setLocalTraceView,
    selectedLocalTraceId,
    setSelectedLocalTraceId,
    localTraceSearch,
    setLocalTraceSearch,
    localTraceTimeFilter,
    setLocalTraceTimeFilter,
    localTraceProjectOnly,
    setLocalTraceProjectOnly,
    localTraceIncludeTests,
    setLocalTraceIncludeTests,
    trace,
    setTrace,
    localTraceSessions,
    setLocalTraceSessions,
    localTraceWorkspace,
    setLocalTraceWorkspace,
    selectedLocalTraceSessionId,
    setSelectedLocalTraceSessionId,
    localTrace,
    setLocalTrace,
    localTraceReloadTick,
    setLocalTraceReloadTick,
  } = useTracePanel();
  const [messageCount, setMessageCount] = useState(0);
  const {
    memories,
    setMemories,
    teamMemories,
    setTeamMemories,
    managedMemories,
    setManagedMemories,
    memoryReviewCandidates,
    setMemoryReviewCandidates,
    memoryReviewTypeFilter,
    setMemoryReviewTypeFilter,
    memoryReviewRiskFilter,
    setMemoryReviewRiskFilter,
    memoryReviewSessionFilter,
    setMemoryReviewSessionFilter,
    selectedMemoryReviewKey,
    setSelectedMemoryReviewKey,
    memoryReviewSourceMessages,
    setMemoryReviewSourceMessages,
    memoryReviewSourceLoading,
    setMemoryReviewSourceLoading,
    memoryKey,
    setMemoryKey,
    memoryContent,
    setMemoryContent,
    scopedMemoryScope,
    setScopedMemoryScope,
    scopedMemoryKey,
    setScopedMemoryKey,
    scopedMemoryContent,
    setScopedMemoryContent,
  } = useMemoryPanel();
  const {
    skills,
    setSkills,
    tenantSkills,
    setTenantSkills,
    skillOverrides,
    setSkillOverrides,
    skillKey,
    setSkillKey,
    skillName,
    setSkillName,
    skillVersion,
    setSkillVersion,
    skillContent,
    setSkillContent,
    skillEnabled,
    setSkillEnabled,
    skillOverrideConfig,
    setSkillOverrideConfig,
    skillValidation,
    setSkillValidation,
    skillPackagePath,
    setSkillPackagePath,
    skillPackageFileName,
    setSkillPackageFileName,
    skillPackageFileSize,
    setSkillPackageFileSize,
    skillPackageBase64,
    setSkillPackageBase64,
    skillPackageSchemaName,
    setSkillPackageSchemaName,
    skillPackagePreview,
    setSkillPackagePreview,
    skillPackageVerify,
    setSkillPackageVerify,
  } = useSkillPanel();
  const [documents, setDocuments] = useState<DocumentRecord[]>([]);
  const [knowledgeDocs, setKnowledgeDocs] = useState<KnowledgeDocumentRecord[]>([]);
  const [knowledgeResults, setKnowledgeResults] = useState<KnowledgeChunkRecord[]>([]);
  const [telemetry, setTelemetry] = useState<TelemetryRecord[]>([]);
  const [sessions, setSessions] = useState<TenantSession[]>([]);
  const [profile, setProfile] = useState<ProfileRecord | null>(null);
  const {
    runtimeLoops,
    setRuntimeLoops,
    selectedLoopId,
    setSelectedLoopId,
    selectedLoopLogs,
    setSelectedLoopLogs,
    selectedLoopRuns,
    setSelectedLoopRuns,
    loopLogsLoading,
    setLoopLogsLoading,
    loopPrompt,
    setLoopPrompt,
    loopCwd,
    setLoopCwd,
    loopInterval,
    setLoopInterval,
    loopDraftNew,
    setLoopDraftNew,
  } = useLoopPanel();
  const [profileSummary, setProfileSummary] = useState("");
  const [profileJson, setProfileJson] = useState('{"source":"webui","traits":[]}');
  const [docType, setDocType] = useState("CLAUDE.md");
  const [docContent, setDocContent] = useState("# WebUI test memory\n");
  const [knowledgeTitle, setKnowledgeTitle] = useState("WebUI KB Note");
  const [knowledgeContent, setKnowledgeContent] = useState("Tenant knowledge can be retrieved before chat responses.");
  const [knowledgeQuery, setKnowledgeQuery] = useState("tenant knowledge");
  const selectedSkillRef = useRef({ key: skillKey, version: skillVersion });
  const memoryReviewItems = useMemo(() => memoryReviewCandidates.map(enrichMemoryReviewCandidate), [memoryReviewCandidates]);
  const memoryReviewRisks = useMemo(() => uniqueStrings(memoryReviewItems.map((item) => item.riskStatus)), [memoryReviewItems]);
  const memoryReviewSessionIds = useMemo(() => {
    const ids = memoryReviewItems.map((item) => item.sourceSessionId).filter((id): id is number => id !== null);
    return Array.from(new Set(ids)).sort((a, b) => a - b);
  }, [memoryReviewItems]);
  const filteredMemoryReviewItems = useMemo(() => {
    return memoryReviewItems.filter((item) => {
      const typeMatches = memoryReviewTypeFilter === "all" || item.candidateType === memoryReviewTypeFilter;
      const riskMatches = memoryReviewRiskFilter === "all" || item.riskStatus === memoryReviewRiskFilter;
      const sessionMatches = memoryReviewSessionFilter === "all" || String(item.sourceSessionId || "") === memoryReviewSessionFilter;
      return typeMatches && riskMatches && sessionMatches;
    });
  }, [memoryReviewItems, memoryReviewRiskFilter, memoryReviewSessionFilter, memoryReviewTypeFilter]);
  const selectedMemoryReviewCandidate = useMemo(() => {
    return filteredMemoryReviewItems.find((item) => item.key === selectedMemoryReviewKey) || filteredMemoryReviewItems[0] || null;
  }, [filteredMemoryReviewItems, selectedMemoryReviewKey]);
  const selectedMemoryReviewSourceMessages = selectedMemoryReviewCandidate?.sourceSessionId ? memoryReviewSourceMessages[selectedMemoryReviewCandidate.sourceSessionId] || [] : [];
  const selectedLocalTraceSession = useMemo(
    () => localTraceSessions.find((session) => session.session_id === selectedLocalTraceSessionId) || null,
    [localTraceSessions, selectedLocalTraceSessionId]
  );
  const selectedLoop = useMemo(
    () => loopDraftNew ? null : runtimeLoops.find((item) => item.id === selectedLoopId) || runtimeLoops[0] || null,
    [loopDraftNew, runtimeLoops, selectedLoopId]
  );
  const currentLocalProject = useMemo(() => projectSlug(localTraceWorkspace) || inferCurrentLocalProject(localTraceSessions), [localTraceSessions, localTraceWorkspace]);
  const filteredLocalTraceSessions = useMemo(
    () => filterLocalTraceSessions(localTraceSessions, {
      search: localTraceSearch,
      timeFilter: localTraceTimeFilter,
      projectOnly: localTraceProjectOnly,
      includeTestSessions: localTraceIncludeTests,
      currentProject: currentLocalProject
    }),
    [currentLocalProject, localTraceIncludeTests, localTraceProjectOnly, localTraceSearch, localTraceSessions, localTraceTimeFilter]
  );
  const selectedSkillOverride = useMemo(
    () => skillOverrides.find((item) => item.skill_key === skillKey) || null,
    [skillKey, skillOverrides]
  );
  const tenantSkillMap = useMemo(() => {
    const out = new Map<string, SkillRecord>();
    for (const item of tenantSkills) {
      const current = item.skill_key ? out.get(item.skill_key) : null;
      if (item.skill_key && (!current || Number(item.version || 0) > Number(current.version || 0))) {
        out.set(item.skill_key, item);
      }
    }
    return out;
  }, [tenantSkills]);
  const selectedSkillHistory = useMemo(() => {
    return tenantSkills
      .filter((item) => item.skill_key === skillKey)
      .sort((a, b) => Number(b.version || 0) - Number(a.version || 0));
  }, [skillKey, tenantSkills]);

  useEffect(() => {
    selectedSkillRef.current = { key: skillKey, version: skillVersion };
  }, [skillKey, skillVersion]);

  async function refreshAll() {
    const telemetrySearch = selectedSessionId ? String(selectedSessionId) : "";
    const [
      memoryTask,
      teamMemoryTask,
      managedMemoryTask,
      memoryReviewTask,
      skillTask,
      tenantSkillTask,
      overrideTask,
      documentTask,
      knowledgeDocTask,
      telemetryTask,
      profileTask,
      sessionTask,
      localSessionTask,
      loopTask,
      statusTask
    ] = await Promise.all([
      runRefreshTask({ label: "memories", run: () => listMemories(identity) }),
      runRefreshTask({ label: "team memory", run: () => listTeamMemory(identity) }),
      runRefreshTask({ label: "managed memory", run: () => listManagedMemory(identity) }),
      runRefreshTask({
        label: "memory review",
        run: () => listMemoryReviewCandidates(identity, {
          candidateType: memoryReviewTypeFilter,
          riskStatus: memoryReviewRiskFilter,
          sourceSessionId: memoryReviewSessionFilter === "all" ? null : Number(memoryReviewSessionFilter),
          limit: 50
        })
      }),
      runRefreshTask({ label: "effective skills", run: () => listEffectiveSkills(identity) }),
      runRefreshTask({ label: "tenant skills", run: () => listTenantSkills(identity) }),
      runRefreshTask({ label: "skill overrides", run: () => listSkillOverrides(identity) }),
      runRefreshTask({ label: "documents", run: () => listDocuments(identity) }),
      runRefreshTask({ label: "knowledge documents", run: () => listKnowledgeDocuments(identity) }),
      runRefreshTask({ label: "telemetry", run: () => listTelemetry(identity, telemetrySearch) }),
      runRefreshTask({ label: "profile", run: () => getProfile(identity) }),
      runRefreshTask({ label: "mobile sessions", run: () => listMobileSessions(identity) }),
      runRefreshTask({ label: "local trace sessions", run: () => listLocalTraceSessions(identity) }),
      runRefreshTask({ label: "runtime loops", run: () => listRuntimeBackground(identity, "loop") }),
      runRefreshTask<ServerStatus | null>({ label: "status", run: () => getStatus(identity).catch(() => null) })
    ]);

    const memoryItems = settledValue(memoryTask.result, memories);
    const teamMemoryItems = settledValue(teamMemoryTask.result, teamMemories);
    const managedMemoryItems = settledValue(managedMemoryTask.result, managedMemories);
    const memoryReviewItems = settledValue(memoryReviewTask.result, memoryReviewCandidates);
    const skillItems = settledValue(skillTask.result, skills);
    const tenantSkillItems = settledValue(tenantSkillTask.result, tenantSkills);
    const overrideItems = settledValue(overrideTask.result, skillOverrides);
    const documentItems = settledValue(documentTask.result, documents);
    const knowledgeDocItems = settledValue(knowledgeDocTask.result, knowledgeDocs);
    const telemetryItems = settledValue(telemetryTask.result, telemetry);
    const profileItem = settledValue(profileTask.result, profile);
    const sessionItems = settledValue(sessionTask.result, sessions);
    const localSessionItems = settledValue(localSessionTask.result, localTraceSessions);
    const loopItems = settledValue(loopTask.result, runtimeLoops);
    const statusItem = settledValue(statusTask.result, null);

    setMemories(memoryItems);
    setTeamMemories(teamMemoryItems);
    setManagedMemories(managedMemoryItems);
    setMemoryReviewCandidates(memoryReviewItems);
    setSkills(skillItems);
    setTenantSkills(tenantSkillItems);
    setSkillOverrides(overrideItems);
    const selectedSkill = selectedSkillRef.current;
    const shouldAutoSelectSkill =
      (selectedSkill.key === "webui-test-skill" || selectedSkill.key === "") &&
      !tenantSkillItems.some((item) => isSelectedSkill(item, selectedSkill.key, selectedSkill.version));
    if (shouldAutoSelectSkill && tenantSkillItems[0]) {
      selectSkillForEdit(tenantSkillItems[0], overrideItems);
    }
    setDocuments(documentItems);
    setKnowledgeDocs(knowledgeDocItems);
    setTelemetry(telemetryItems);
    setSessions(sessionItems);
    setLocalTraceSessions(localSessionItems);
    setRuntimeLoops(loopItems);
    const nextLoopId =
      selectedLoopId && loopItems.some((item) => item.id === selectedLoopId)
        ? selectedLoopId
        : loopItems[0]?.id || "";
    if (nextLoopId !== selectedLoopId) {
      setSelectedLoopId(nextLoopId);
    }
    if (loopItems.length > 0 && !selectedLoopId) {
      setLoopDraftNew(false);
    }
    setLocalTraceWorkspace(statusItem?.workspace || "");
    const nextLocalSessionId =
      selectedLocalTraceSessionId && localSessionItems.some((item) => item.session_id === selectedLocalTraceSessionId)
        ? selectedLocalTraceSessionId
        : localSessionItems[0]?.session_id || "";
    if (nextLocalSessionId !== selectedLocalTraceSessionId) {
      setSelectedLocalTraceSessionId(nextLocalSessionId);
    }
    setProfile(profileItem);
    if (profileItem?.summary) {
      setProfileSummary(profileItem.summary);
    }
    if (profileItem?.profile_json) {
      setProfileJson(profileItem.profile_json);
    }
    if (selectedSessionId) {
      try {
        const messages = await listMobileMessages(identity, selectedSessionId);
        setMessageCount(messages.length);
        setTrace(await getTrace(identity, selectedSessionId));
      } catch (err) {
        setMessageCount(0);
        setTrace(null);
        onStatus(err instanceof Error ? err.message : String(err));
      }
    } else {
      setMessageCount(0);
      setTrace(null);
    }

    const errorSummary = settledErrorSummary([
      memoryTask,
      teamMemoryTask,
      managedMemoryTask,
      memoryReviewTask,
      skillTask,
      tenantSkillTask,
      overrideTask,
      documentTask,
      knowledgeDocTask,
      telemetryTask,
      profileTask,
      sessionTask,
      localSessionTask,
      loopTask,
      statusTask
    ]);
    if (errorSummary) {
      onStatus(errorSummary);
    }
  }

  useEffect(() => {
    void refreshAll();
  }, [
    identity.apiBase,
    identity.apiToken,
    identity.mobileJwt,
    identity.tenantKey,
    identity.userId,
    selectedSessionId,
    refreshTick,
    memoryReviewTypeFilter,
    memoryReviewRiskFilter,
    memoryReviewSessionFilter
  ]);

  useEffect(() => {
    if (!selectedMemoryReviewCandidate) {
      setSelectedMemoryReviewKey("");
      return;
    }
    if (selectedMemoryReviewCandidate.key !== selectedMemoryReviewKey) {
      setSelectedMemoryReviewKey(selectedMemoryReviewCandidate.key);
    }
  }, [selectedMemoryReviewCandidate, selectedMemoryReviewKey]);

  useEffect(() => {
    if (activeSection !== "automem" || !selectedMemoryReviewCandidate?.sourceSessionId) {
      return;
    }
    const sourceSessionId = selectedMemoryReviewCandidate.sourceSessionId;
    if (memoryReviewSourceMessages[sourceSessionId]) {
      return;
    }
    let cancelled = false;
    setMemoryReviewSourceLoading(sourceSessionId);
    listMobileMessages(identity, sourceSessionId)
      .then((messages) => {
        if (!cancelled) {
          setMemoryReviewSourceMessages((current) => ({ ...current, [sourceSessionId]: messages }));
        }
      })
      .catch((err) => {
        if (!cancelled) {
          onStatus(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setMemoryReviewSourceLoading((current) => current === sourceSessionId ? null : current);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [
    activeSection,
    identity.apiBase,
    identity.apiToken,
    identity.mobileJwt,
    identity.tenantKey,
    identity.userId,
    selectedMemoryReviewCandidate?.key,
    selectedMemoryReviewCandidate?.sourceSessionId,
    memoryReviewSourceMessages,
    onStatus
  ]);

  useEffect(() => {
    const requestOptions = traceRequestOptions(trace);
    if (requestOptions.length === 0) {
      setSelectedTraceId("");
      return;
    }
    if (!requestOptions.some((option) => option.id === selectedTraceId)) {
      setSelectedTraceId(requestOptions[0].id);
    }
  }, [trace, selectedTraceId]);

  useEffect(() => {
    const requestOptions = traceRequestOptions(localTrace);
    if (requestOptions.length === 0) {
      setSelectedLocalTraceId("");
      return;
    }
    if (!requestOptions.some((option) => option.id === selectedLocalTraceId)) {
      setSelectedLocalTraceId(requestOptions[0].id);
    }
  }, [localTrace, selectedLocalTraceId]);

  useEffect(() => {
    if (!selectedLocalTraceSessionId) {
      return;
    }
    if (filteredLocalTraceSessions.some((session) => session.session_id === selectedLocalTraceSessionId)) {
      return;
    }
    setSelectedLocalTraceSessionId(filteredLocalTraceSessions[0]?.session_id || "");
  }, [filteredLocalTraceSessions, selectedLocalTraceSessionId]);

  useEffect(() => {
    if (!selectedLocalTraceSessionId) {
      setLocalTrace(null);
      return;
    }
    let cancelled = false;
    setLocalTrace(null);
    getLocalTrace(identity, selectedLocalTraceSessionId)
      .then((detail) => {
        if (!cancelled) {
          setLocalTrace(detail);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          onStatus(err instanceof Error ? err.message : String(err));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, selectedLocalTraceSessionId, localTraceReloadTick, onStatus]);

  useEffect(() => {
    if (!selectedLoop?.id) {
      setSelectedLoopLogs(null);
      setSelectedLoopRuns([]);
      return;
    }
    let cancelled = false;
    setLoopLogsLoading(true);
    Promise.all([getRuntimeBackgroundLogs(identity, selectedLoop.id), listRuntimeRuns(identity, selectedLoop.schedule_id || selectedLoop.id)])
      .then(([logs, runs]) => {
        if (!cancelled) {
          setSelectedLoopLogs(logs);
          setSelectedLoopRuns(runs);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          onStatus(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoopLogsLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [identity.apiBase, identity.apiToken, selectedLoop?.id, onStatus]);

  useEffect(() => {
    if (!selectedLoop) {
      return;
    }
    setLoopPrompt(selectedLoop.prompt || "");
    setLoopCwd(selectedLoop.cwd || "");
    setLoopInterval(formatLoopInterval(selectedLoop) === "-" ? "10m" : formatLoopInterval(selectedLoop));
  }, [selectedLoop?.id]);

  async function handleSaveMemory() {
    try {
      await saveMemory(identity, {
        memory_key: memoryKey,
        category: "webui",
        content: memoryContent,
        importance: 5,
        source: "webui"
      });
      setMemoryContent("");
      await refreshAll();
      onStatus(t("inspector.memorySaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveProfile() {
    try {
      await saveProfile(identity, profileJson, profileSummary);
      await refreshAll();
      onStatus(t("inspector.profileSaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveDocument() {
    try {
      await saveDocument(identity, {
        doc_type: docType,
        title: docType,
        content_md: docContent,
        active: true
      });
      await refreshAll();
      onStatus(t("inspector.documentSaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveKnowledgeDocument() {
    try {
      await saveKnowledgeDocument(identity, {
        title: knowledgeTitle,
        content: knowledgeContent,
        source_type: "webui",
        status: "active",
        metadata_json: JSON.stringify({ source: "webui", updated_at: new Date().toISOString() })
      });
      await refreshAll();
      onStatus(t("inspector.knowledgeSaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSearchKnowledge() {
    try {
      setKnowledgeResults(await searchKnowledge(identity, knowledgeQuery, 10));
      onStatus(t("inspector.knowledgeSearched"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveScopedMemory() {
    try {
      const record: MemoryRecord = {
        memory_key: scopedMemoryKey,
        content: scopedMemoryContent,
        importance: 7,
        source: "webui"
      };
      if (scopedMemoryScope === "team") {
        await saveTeamMemory(identity, record);
      } else {
        await saveManagedMemory(identity, record);
      }
      setScopedMemoryContent("");
      await refreshAll();
      onStatus(t("inspector.scopedMemorySaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleReviewMemory(memoryKey: string, action: "approve" | "reject" | "archive") {
    try {
      await reviewMemoryCandidate(identity, { memory_key: memoryKey, action });
      await refreshAll();
      onStatus(reviewStatusMessage(action, t));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveSkill() {
    try {
      await saveTenantSkill(identity, {
        skill_key: skillKey,
        name: skillName,
        version: skillVersion,
        enabled: skillEnabled,
        content_md: skillContent
      });
      await saveSkillOverride(identity, {
        skill_key: skillKey,
        version: skillVersion,
        enabled: skillEnabled,
        config_json: skillOverrideConfig
      });
      await refreshAll();
      onStatus(t("inspector.skillSaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  function handleNewSkill() {
    selectedSkillRef.current = { key: "new-tenant-skill", version: 1 };
    setSkillKey("new-tenant-skill");
    setSkillName("New Tenant Skill");
    setSkillVersion(1);
    setSkillEnabled(true);
    setSkillContent("---\ndescription: New tenant skill\n---\nUse this skill when the tenant workflow needs custom instructions.\n");
    setSkillOverrideConfig('{"source":"webui"}');
  }

  function selectSkillForEdit(skill: SkillRecord, overrides = skillOverrides) {
    selectedSkillRef.current = { key: skill.skill_key || "", version: Number(skill.version || 1) };
    setSkillKey(skill.skill_key || "");
    setSkillName(skill.name || skill.skill_key || "");
    setSkillVersion(Number(skill.version || 1));
    setSkillEnabled(skill.enabled !== false);
    setSkillContent(skill.content_md || "");
    const override = overrides.find((item) => item.skill_key === skill.skill_key);
    setSkillOverrideConfig(override?.config_json || skill.config_json || '{"source":"webui"}');
    setSkillValidation("");
  }

  async function handleValidateSkill(version = skillVersion) {
    try {
      const item = await getEffectiveSkill(identity, skillKey, version);
      const state = item.enabled === false ? t("inspector.disabled") : t("inspector.enabled");
      setSkillValidation(t("inspector.skillValidationResult", {
        skill: item.skill_key || skillKey,
        source: item.source || "tenant",
        version: item.version || version,
        state
      }));
      onStatus(t("inspector.skillValidated"));
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setSkillValidation(message);
      onStatus(message);
    }
  }

  async function handleRollbackSkill(version: number) {
    try {
      const result = await rollbackTenantSkill(identity, skillKey, version);
      const rolledBackSkill = await getTenantSkill(identity, result.skill_key || skillKey, result.version);
      await refreshAll();
      selectSkillForEdit(rolledBackSkill);
      setSkillValidation(t("inspector.skillRollbackResult", { from: result.from_version, version: result.version }));
      onStatus(t("inspector.skillRolledBack"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRenderSkillPackage() {
    try {
      const result = await renderTenantSkillPackage(identity, {
        skill_key: skillKey,
        name: skillName,
        ...skillPackageSourceRequest(skillPackagePath, skillPackageBase64)
      });
      setSkillPackagePreview(result);
      setSkillPackageVerify(null);
      setSkillContent(result.runtime_md || "");
      onStatus(t("inspector.skillPackageRendered"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handlePublishSkillPackage() {
    try {
      const result = await publishTenantSkillPackage(identity, {
        skill_key: skillKey,
        name: skillName,
        ...skillPackageSourceRequest(skillPackagePath, skillPackageBase64),
        enabled: skillEnabled
      });
      setSkillPackagePreview(result);
      await refreshAll();
      if (result.version) {
        const published = await getTenantSkill(identity, result.skill_key || skillKey, result.version);
        selectSkillForEdit(published);
      }
      try {
        const verify = await verifyTenantSkillPackageRuntime(identity, {
          skill_key: result.skill_key || skillKey,
          schema_name: skillPackageSchemaName.trim(),
          expected_package_sha256: result.package_sha256,
          expected_version: result.version
        });
        setSkillPackageVerify(verify);
      } catch (verifyErr) {
        setSkillPackageVerify({
          ok: false,
          error: verifyErr instanceof Error ? verifyErr.message : String(verifyErr)
        });
      }
      onStatus(t("inspector.skillPackagePublished"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSkillPackageFile(file: File | null) {
    if (!file) {
      setSkillPackageFileName("");
      setSkillPackageFileSize(0);
      setSkillPackageBase64("");
      return;
    }
    try {
      const content = await fileToBase64(file);
      setSkillPackageFileName(file.name);
      setSkillPackageFileSize(file.size);
      setSkillPackageBase64(content);
      setSkillPackagePath("");
      setSkillPackagePreview(null);
      setSkillPackageVerify(null);
      onStatus(t("inspector.skillPackageSelected"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveSkillOverrideOnly() {
    try {
      await saveSkillOverride(identity, {
        skill_key: skillKey,
        version: skillVersion,
        enabled: skillEnabled,
        config_json: skillOverrideConfig
      });
      await refreshAll();
      onStatus(t("inspector.skillOverrideSaved"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleToggleSelectedSkill() {
    try {
      const nextEnabled = !skillEnabled;
      await saveTenantSkill(identity, {
        skill_key: skillKey,
        name: skillName,
        version: skillVersion,
        enabled: nextEnabled,
        content_md: skillContent
      });
      await saveSkillOverride(identity, {
        skill_key: skillKey,
        version: skillVersion,
        enabled: nextEnabled,
        config_json: skillOverrideConfig
      });
      setSkillEnabled(nextEnabled);
      await refreshAll();
      onStatus(nextEnabled ? t("inspector.skillEnabled") : t("inspector.skillDisabled"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRefreshLocalTrace() {
    await refreshAll();
    setLocalTraceReloadTick((current) => current + 1);
  }

  async function handleTraceExport(sessionId: string, source: "local" | "tenant") {
    try {
      const blob = await getRuntimeTraceArtifact(identity, sessionId, source);
      downloadBlob(blob, `runtime-trace-${sessionId.replace(/[^a-zA-Z0-9._-]/g, "-")}.json`);
      onStatus(t("trace.exported"));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleStopLoop(id: string) {
    try {
      await stopRuntimeBackground(identity, id);
      await refreshAll();
      onStatus(t("inspector.loopStopped", { id: shortTraceId(id) }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleCreateLoop() {
    try {
      const created = await createRuntimeLoop(identity, { prompt: loopPrompt, cwd: loopCwd, interval: loopInterval });
      await refreshAll();
      setLoopDraftNew(false);
      setSelectedLoopId(created.id);
      onStatus(t("inspector.loopCreated", { id: shortTraceId(created.id) }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSaveLoop() {
    if (!selectedLoop) {
      return handleCreateLoop();
    }
    try {
      const updated = await updateRuntimeLoop(identity, selectedLoop.schedule_id || selectedLoop.id, { prompt: loopPrompt, cwd: loopCwd, interval: loopInterval });
      await refreshAll();
      setSelectedLoopId(updated.id);
      onStatus(t("inspector.loopSaved", { id: shortTraceId(updated.id) }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRunLoopNow(id: string) {
    try {
      await runRuntimeBackground(identity, id);
      await refreshAll();
      onStatus(t("inspector.loopRan", { id: shortTraceId(id) }));
    } catch (err) {
      onStatus(err instanceof Error ? err.message : String(err));
    }
  }

  function handleNewLoopDraft() {
    setSelectedLoopId("");
    setLoopDraftNew(true);
    setLoopPrompt("drink water");
    setLoopCwd(localTraceWorkspace || "");
    setLoopInterval("10m");
    setSelectedLoopLogs(null);
    setSelectedLoopRuns([]);
  }

  return (
    <section className="inspector-grid">
      {activeSection === "validation" ? (
        <ValidationPanel
          snapshot={{
            selectedSessionId,
            messageCount,
            memories,
            skills,
            tenantSkills,
            skillOverrides,
            documents,
            telemetry,
            profile,
            trace
          }}
          onRefresh={refreshAll}
        />
      ) : null}

      {activeSection === "memory" ? <Panel title={t("tab.memory")} subtitle={t("inspector.records", { count: memories.length })} icon={<Brain size={17} />} onRefresh={refreshAll}>
        <div className="compact-form">
          <label>
            {t("inspector.memoryKey")}
            <input value={memoryKey} onChange={(event) => setMemoryKey(event.target.value)} />
          </label>
          <label>
            {t("inspector.memoryContent")}
          <textarea
            value={memoryContent}
            onChange={(event) => setMemoryContent(event.target.value)}
            placeholder={t("inspector.memoryPlaceholder")}
            rows={3}
          />
          </label>
          <button type="button" className="secondary-button" onClick={handleSaveMemory}>{t("inspector.saveMemory")}</button>
        </div>
        <RecordList
          items={memories.map((item) => ({
            title: item.memory_key || `Memory ${item.id}`,
            body: item.content || "",
            meta: item.category || item.source || ""
          }))}
        />
      </Panel> : null}

      {activeSection === "profile" ? <Panel title={t("tab.profile")} subtitle={profile ? t("inspector.version", { version: profile.profile_version || t("inspector.latest") }) : t("inspector.notLoaded")} icon={<UserRound size={17} />}>
        <div className="compact-form">
          <label>
            {t("inspector.profileSummary")}
            <input value={profileSummary} onChange={(event) => setProfileSummary(event.target.value)} placeholder={t("inspector.profileSummary")} />
          </label>
          <label>
            {t("inspector.profileJson")}
            <textarea value={profileJson} onChange={(event) => setProfileJson(event.target.value)} rows={5} />
          </label>
          <button type="button" className="secondary-button" onClick={handleSaveProfile}>{t("inspector.saveProfile")}</button>
        </div>
      </Panel> : null}

      {activeSection === "documents" ? <Panel title={t("tab.documents")} subtitle={t("inspector.records", { count: documents.length })} icon={<FileText size={17} />} onRefresh={refreshAll}>
        <div className="compact-form">
          <label>
            {t("inspector.documentType")}
            <input value={docType} onChange={(event) => setDocType(event.target.value)} placeholder={t("inspector.documentType")} />
          </label>
          <label>
            {t("inspector.documentContent")}
            <textarea value={docContent} onChange={(event) => setDocContent(event.target.value)} rows={4} />
          </label>
          <button type="button" className="secondary-button" onClick={handleSaveDocument}>{t("inspector.saveDocument")}</button>
        </div>
        <RecordList
          items={documents.map((item) => ({
            title: item.title || item.doc_type || `Document ${item.id}`,
            body: item.content_md || item.content_json || "",
            meta: `${item.doc_type || ""} v${item.version || 1}`
          }))}
        />
      </Panel> : null}

      {activeSection === "knowledge-search" ? <Panel title={t("tab.knowledgeSearch")} subtitle={t("inspector.records", { count: knowledgeDocs.length })} icon={<FileSearch size={17} />} onRefresh={refreshAll}>
        <div className="compact-form knowledge-form">
          <label>
            {t("inspector.knowledgeTitle")}
            <input value={knowledgeTitle} onChange={(event) => setKnowledgeTitle(event.target.value)} />
          </label>
          <label>
            {t("inspector.knowledgeContent")}
            <textarea value={knowledgeContent} onChange={(event) => setKnowledgeContent(event.target.value)} rows={4} />
          </label>
          <button type="button" className="secondary-button" onClick={handleSaveKnowledgeDocument}>{t("inspector.saveKnowledge")}</button>
          <label>
            {t("inspector.knowledgeQuery")}
            <input value={knowledgeQuery} onChange={(event) => setKnowledgeQuery(event.target.value)} />
          </label>
          <button type="button" className="secondary-button" onClick={handleSearchKnowledge}>{t("inspector.searchKnowledge")}</button>
        </div>
        <div className="split-record-grid">
          <RecordList
            items={knowledgeDocs.map((item) => ({
              title: item.title || `Knowledge ${item.id}`,
              body: item.content || "",
              meta: [item.source_type, item.status].filter(Boolean).join(" · ")
            }))}
          />
          <RecordList
            items={knowledgeResults.map((item) => ({
              title: item.title || `Chunk ${item.id}`,
              body: item.content || "",
              meta: [item.search_mode, item.score ? `score ${item.score}` : "", item.source_type].filter(Boolean).join(" · ")
            }))}
          />
        </div>
      </Panel> : null}

      {activeSection === "scoped-memory" ? <Panel title={t("tab.scopedMemory")} subtitle={t("inspector.teamManagedCounts", { team: teamMemories.length, managed: managedMemories.length })} icon={<Database size={17} />} onRefresh={refreshAll}>
        <div className="compact-form scoped-memory-form">
          <label>
            {t("inspector.memoryScope")}
            <select value={scopedMemoryScope} onChange={(event) => setScopedMemoryScope(event.target.value as "team" | "managed")}>
              <option value="team">{t("inspector.teamMemory")}</option>
              <option value="managed">{t("inspector.managedMemory")}</option>
            </select>
          </label>
          <label>
            {t("inspector.memoryKey")}
            <input value={scopedMemoryKey} onChange={(event) => setScopedMemoryKey(event.target.value)} />
          </label>
          <label className="scoped-memory-content">
            {t("inspector.memoryContent")}
            <textarea value={scopedMemoryContent} onChange={(event) => setScopedMemoryContent(event.target.value)} rows={3} />
          </label>
          <button type="button" className="secondary-button" onClick={handleSaveScopedMemory}>{t("inspector.saveScopedMemory")}</button>
        </div>
        <div className="split-record-grid">
          <RecordList
            items={teamMemories.map((item) => ({
              title: item.memory_key || `Team ${item.id}`,
              body: item.content || "",
              meta: [t("inspector.teamMemory"), item.source].filter(Boolean).join(" · ")
            }))}
          />
          <RecordList
            items={managedMemories.map((item) => ({
              title: item.memory_key || `Managed ${item.id}`,
              body: item.content || "",
              meta: [t("inspector.managedMemory"), item.source].filter(Boolean).join(" · ")
            }))}
          />
        </div>
      </Panel> : null}

      {activeSection === "automem" ? <Panel title={t("tab.automem")} subtitle={t("inspector.memoryReviewCount", { shown: filteredMemoryReviewItems.length, total: memoryReviewItems.length })} icon={<Brain size={17} />} onRefresh={refreshAll}>
        <div className="memory-review-workbench">
          <div className="memory-review-filters">
            <label>
              {t("inspector.candidateType")}
              <select value={memoryReviewTypeFilter} onChange={(event) => setMemoryReviewTypeFilter(event.target.value)}>
                <option value="all">{t("inspector.allTypes")}</option>
                <option value="explicit_remember">{t("inspector.explicitRemember")}</option>
                <option value="automem_candidate">{t("inspector.autoMemCandidate")}</option>
              </select>
            </label>
            <label>
              {t("inspector.riskStatus")}
              <select value={memoryReviewRiskFilter} onChange={(event) => setMemoryReviewRiskFilter(event.target.value)}>
                <option value="all">{t("inspector.allRisks")}</option>
                {memoryReviewRisks.map((risk) => <option key={risk} value={risk}>{risk}</option>)}
              </select>
            </label>
            <label>
              {t("inspector.sourceSession")}
              <select value={memoryReviewSessionFilter} onChange={(event) => setMemoryReviewSessionFilter(event.target.value)}>
                <option value="all">{t("inspector.allSessions")}</option>
                {memoryReviewSessionIds.map((sessionId) => (
                  <option key={sessionId} value={sessionId}>
                    {memoryReviewSessionLabel(sessionId, sessions)}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="memory-review-layout">
            <div className="review-list">
              {filteredMemoryReviewItems.length === 0 ? <div className="empty-state compact">{t("inspector.noRecords")}</div> : null}
              {filteredMemoryReviewItems.map((candidate) => {
                const item = candidate.item;
            return (
            // biome-ignore lint/a11y/useSemanticElements: keep <div role="button"> — this card contains its own nested <button> action row (details/approve/reject/archive); a native <button> cannot contain other interactive buttons
            <div
              key={candidate.key}
              role="button"
              tabIndex={0}
              className={`record-item review-item ${selectedMemoryReviewCandidate?.key === candidate.key ? "selected" : ""}`}
              onClick={() => setSelectedMemoryReviewKey(candidate.key)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  if (event.key === " ") {
                    event.preventDefault();
                  }
                  setSelectedMemoryReviewKey(candidate.key);
                }
              }}
            >
              <strong>{item.memory_key || `Memory ${item.id}`}</strong>
              <small>{reviewMetaLine(candidate)}</small>
              <p>{item.content || ""}</p>
              <div className="review-actions">
                <button type="button" className="secondary-button" onClick={(event) => { event.stopPropagation(); setSelectedMemoryReviewKey(candidate.key); }}>{t("inspector.details")}</button>
                <button type="button" className="secondary-button" onClick={(event) => { event.stopPropagation(); item.memory_key && void handleReviewMemory(item.memory_key, "approve"); }}>{t("inspector.approve")}</button>
                <button type="button" className="secondary-button danger" onClick={(event) => { event.stopPropagation(); item.memory_key && void handleReviewMemory(item.memory_key, "reject"); }}>{t("inspector.reject")}</button>
                <button type="button" className="secondary-button" onClick={(event) => { event.stopPropagation(); item.memory_key && void handleReviewMemory(item.memory_key, "archive"); }}>{t("inspector.archive")}</button>
              </div>
            </div>
          );})}
            </div>
            <MemoryReviewDetail
              candidate={selectedMemoryReviewCandidate}
              sourceMessages={selectedMemoryReviewSourceMessages}
              sourceLoading={memoryReviewSourceLoading === selectedMemoryReviewCandidate?.sourceSessionId}
            />
          </div>
        </div>
      </Panel> : null}

      {activeSection === "skills" ? <Panel title={t("tab.skills")} subtitle={t("inspector.effectiveTenant", { effective: skills.length, tenant: tenantSkills.length })} icon={<Sparkles size={17} />} onRefresh={refreshAll}>
        <div className="skill-manager-grid">
          <div className="skill-sidebar">
            <div className="skill-toolbar">
              <button type="button" className="secondary-button" onClick={handleNewSkill}>{t("inspector.newSkill")}</button>
              <button type="button" className="secondary-button" onClick={refreshAll}>{t("inspector.refreshSkills")}</button>
            </div>
            <h3>{t("inspector.tenantSkills")}</h3>
            <div className="skill-list">
              {tenantSkills.length === 0 ? <div className="empty-state">{t("inspector.noTenantSkills")}</div> : null}
              {tenantSkills.map((item) => (
                <button
                  key={skillRecordKey(item)}
                  type="button"
                  className={`skill-row ${isSelectedSkill(item, skillKey, skillVersion) ? "active" : ""}`}
                  onClick={() => selectSkillForEdit(item)}
                >
                  <span>{item.name || item.skill_key || `Skill ${item.id}`}</span>
                  <small>{[item.skill_key, `v${item.version || 1}`, item.enabled === false ? t("inspector.disabled") : t("inspector.enabled")].filter(Boolean).join(" · ")}</small>
                </button>
              ))}
            </div>
            <h3>{t("inspector.effectiveSkills")}</h3>
            <div className="skill-list compact">
              {skills.map((item) => (
                <button
                  key={skillRecordKey(item)}
                  type="button"
                  className={`skill-row ${isSelectedSkill(item, skillKey, skillVersion) ? "active" : ""}`}
                  onClick={() => selectSkillForEdit(tenantSkillMap.get(item.skill_key || "") || item)}
                >
                  <span>{item.name || item.skill_key || `Skill ${item.id}`}</span>
                  <small>{[item.skill_key, item.source, `v${item.version || 1}`].filter(Boolean).join(" · ")}</small>
                </button>
              ))}
            </div>
          </div>
          <div className="compact-form skill-editor-form">
            <label>
              {t("inspector.skillKey")}
              <input value={skillKey} onChange={(event) => setSkillKey(event.target.value)} placeholder={t("inspector.skillKey")} />
            </label>
            <label>
              {t("inspector.skillName")}
              <input value={skillName} onChange={(event) => setSkillName(event.target.value)} placeholder={t("inspector.skillName")} />
            </label>
            <label>
              {t("inspector.skillVersion")}
              <input type="number" min={1} value={skillVersion} onChange={(event) => setSkillVersion(Number(event.target.value) || 1)} />
            </label>
            <label className="inline-check">
              <input type="checkbox" checked={skillEnabled} onChange={(event) => setSkillEnabled(event.target.checked)} />
              {t("inspector.enabled")}
            </label>
            <div className="package-editor">
              <label>
                {t("inspector.skillPackageUpload")}
                <input type="file" accept=".zip" onChange={(event) => void handleSkillPackageFile(event.currentTarget.files?.[0] || null)} />
              </label>
              {skillPackageFileName ? (
                <div className="small-count">
                  {t("inspector.skillPackageFile", {
                    name: skillPackageFileName,
                    size: formatBytes(skillPackageFileSize)
                  })}
                </div>
              ) : null}
              <label>
                {t("inspector.skillPackagePath")}
                <input value={skillPackagePath} onChange={(event) => setSkillPackagePath(event.target.value)} placeholder="/path/to/package.skill.zip" />
              </label>
              <label>
                {t("inspector.skillPackageSchema")}
                <input value={skillPackageSchemaName} onChange={(event) => setSkillPackageSchemaName(event.target.value)} placeholder="teach_decision_v1" />
              </label>
              <div className="skill-actions">
                <button type="button" className="secondary-button" onClick={() => void handleRenderSkillPackage()}>{t("inspector.renderPackage")}</button>
                <button type="button" className="secondary-button" onClick={() => void handlePublishSkillPackage()}>{t("inspector.publishPackage")}</button>
              </div>
              {skillPackagePreview ? (
                <div className="package-preview">
                  <div className="small-count">
                    {t("inspector.packagePreview", {
                      sha: shortHash(skillPackagePreview.package_sha256),
                      files: skillPackagePreview.manifest?.files?.length || 0
                    })}
                  </div>
                  <div className="small-count">
                    {[skillPackagePreview.version ? `v${skillPackagePreview.version}` : "", skillPackagePreview.package_ref ? shortHash(skillPackagePreview.package_ref) : "", skillPackagePreview.runtime_ref ? shortHash(skillPackagePreview.runtime_ref) : ""].filter(Boolean).join(" · ")}
                  </div>
                  <div className="package-file-list">
                    {(skillPackagePreview.manifest?.files || []).slice(0, 8).map((file) => (
                      <span key={file.path}>{file.runtime ? "R" : "-"} {file.path}</span>
                    ))}
                  </div>
                </div>
              ) : null}
              {skillPackageVerify ? (
                <div className={skillPackageVerify.ok ? "small-count skill-validation-result" : "small-count skill-validation-result fail"}>
                  {t("inspector.skillPackageVerify", {
                    state: skillPackageVerify.ok ? "ok" : "fail",
                    trace: shortTraceId(skillPackageVerify.trace_id || ""),
                    keys: (skillPackageVerify.tenant_runtime?.loaded_keys || []).join(",") || "-",
                    versions: (skillPackageVerify.tenant_runtime?.versions || []).join(",") || "-",
                    bytes: skillPackageVerify.tenant_runtime?.bytes || 0
                  })}
                </div>
              ) : null}
            </div>
            <label className="skill-content-field">
              {t("inspector.skillContent")}
              <textarea value={skillContent} onChange={(event) => setSkillContent(event.target.value)} rows={8} />
            </label>
            <label className="skill-content-field">
              {t("inspector.overrideConfig")}
              <textarea value={skillOverrideConfig} onChange={(event) => setSkillOverrideConfig(event.target.value)} rows={4} />
            </label>
            <div className="skill-actions">
              <button type="button" className="secondary-button" onClick={handleSaveSkill}>{t("inspector.saveSkill")}</button>
              <button type="button" className="secondary-button" onClick={handleSaveSkillOverrideOnly}>{t("inspector.saveOverride")}</button>
              <button type="button" className="secondary-button" onClick={handleToggleSelectedSkill}>{skillEnabled ? t("inspector.disableSkill") : t("inspector.enableSkill")}</button>
              <button type="button" className="secondary-button" onClick={() => void handleValidateSkill()}>{t("inspector.validateSkill")}</button>
            </div>
            {skillValidation ? <div className="small-count skill-validation-result">{skillValidation}</div> : null}
            <div className="skill-history">
              <div className="section-heading compact">
                <h3>{t("inspector.skillHistory")}</h3>
                <p>{t("inspector.skillHistoryDetail", { count: selectedSkillHistory.length })}</p>
              </div>
              {selectedSkillHistory.length === 0 ? <div className="empty-state compact">{t("inspector.noSkillHistory")}</div> : null}
              {selectedSkillHistory.map((item) => (
                <article className="skill-history-row" key={`${item.skill_key}-${item.version || item.id}`}>
                  <button type="button" className="skill-history-main" onClick={() => selectSkillForEdit(item)}>
                    <strong>{item.name || item.skill_key}</strong>
                    <span>{[`v${item.version || 1}`, item.package_sha256 ? shortHash(item.package_sha256) : "", item.enabled === false ? t("inspector.disabled") : t("inspector.enabled"), relativeTimeLabel(item.updated_at || item.created_at)].filter(Boolean).join(" · ")}</span>
                  </button>
                  <div className="skill-history-actions">
                    <button type="button" className="secondary-button" onClick={() => void handleValidateSkill(Number(item.version || 1))}>{t("inspector.validateSkill")}</button>
                    <button type="button" className="secondary-button" onClick={() => void handleRollbackSkill(Number(item.version || 1))}>{t("inspector.rollbackSkill")}</button>
                  </div>
                </article>
              ))}
            </div>
            <div className="small-count">{t("inspector.overrideState", { count: skillOverrides.length, selected: selectedSkillOverride ? t("inspector.hasOverride") : t("inspector.noOverride") })}</div>
          </div>
        </div>
      </Panel> : null}

      {activeSection === "agents" ? (
        <AgentCockpit identity={identity} selectedSessionId={selectedSessionId} onSelectSession={onSelectSession} onStatus={onStatus} />
      ) : null}

      {activeSection === "loops" ? <Panel
        title={t("tab.loops")}
        subtitle={t("inspector.loopCount", { count: runtimeLoops.length })}
        icon={<RefreshCcw size={17} />}
        onRefresh={refreshAll}
      >
        <div className="loop-workbench">
          <aside className="loop-list" aria-label={t("inspector.loopList")}>
            <div className="loop-list-head">
              <div>
                <strong>{t("inspector.loopList")}</strong>
                <span>{t("inspector.loopCount", { count: runtimeLoops.length })}</span>
              </div>
              <button className="icon-button" onClick={handleNewLoopDraft} type="button" aria-label={t("inspector.newLoop")} title={t("inspector.newLoop")}>
                <Plus size={16} />
              </button>
            </div>
            {runtimeLoops.length === 0 ? <div className="empty-state compact">{t("inspector.noLoops")}</div> : null}
            {runtimeLoops.map((loop) => (
              <button
                key={loop.id}
                type="button"
                className={selectedLoop?.id === loop.id ? "loop-row active" : "loop-row"}
                onClick={() => {
                  setLoopDraftNew(false);
                  setSelectedLoopId(loop.id);
                }}
              >
                <span className="loop-row-top">
                  <span className={loopStatusClass(loop)}>{loop.enabled === false ? t("inspector.disabled") : loop.status || "unknown"}</span>
                  <small>{formatLoopInterval(loop)}</small>
                </span>
                <strong>{loop.prompt || loop.id}</strong>
                <small>{loopMeta(loop)}</small>
              </button>
            ))}
          </aside>
          <section className="loop-detail">
            {selectedLoop || loopDraftNew ? (
              <>
                <div className="loop-detail-head">
                  <div>
                    <span>{loopDraftNew ? t("inspector.newLoop") : selectedLoop?.schedule_id || selectedLoop?.id}</span>
                    <strong>{loopDraftNew ? t("inspector.newLoopTitle") : selectedLoop?.prompt || selectedLoop?.id}</strong>
                    <small title={loopDraftNew ? loopCwd : selectedLoop?.cwd || ""}>{loopDraftNew ? loopCwd || "-" : selectedLoop?.cwd || "-"}</small>
                  </div>
                  <div className="loop-head-actions">
                    {selectedLoop ? (
                      <button
                        className="secondary-button danger"
                        disabled={selectedLoop.enabled === false || selectedLoop.status === "killed"}
                        onClick={() => void handleStopLoop(selectedLoop.schedule_id || selectedLoop.id)}
                        type="button"
                      >
                        <Square size={14} />
                        {t("inspector.stopLoop")}
                      </button>
                    ) : null}
                  </div>
                </div>

                <div className="loop-editor-form">
                  <label>
                    {t("inspector.loopPrompt")}
                    <input value={loopPrompt} onChange={(event) => setLoopPrompt(event.target.value)} placeholder={t("inspector.loopPromptPlaceholder")} />
                  </label>
                  <label>
                    {t("inspector.loopCwd")}
                    <input value={loopCwd} onChange={(event) => setLoopCwd(event.target.value)} placeholder={localTraceWorkspace || "/path/to/workspace"} />
                  </label>
                  <label>
                    {t("inspector.loopInterval")}
                    <input value={loopInterval} onChange={(event) => setLoopInterval(event.target.value)} placeholder="10m" />
                  </label>
                  <div className="loop-editor-actions">
                    <button className="primary-button" onClick={() => void handleSaveLoop()} type="button" disabled={loopPrompt.trim() === ""}>
                      <Save size={15} />
                      {loopDraftNew ? t("inspector.createLoop") : t("inspector.saveLoop")}
                    </button>
                    <button
                      className="secondary-button"
                      onClick={() => selectedLoop ? void handleRunLoopNow(selectedLoop.schedule_id || selectedLoop.id) : undefined}
                      type="button"
                      disabled={!selectedLoop}
                    >
                      <Play size={15} />
                      {t("inspector.runLoopNow")}
                    </button>
                  </div>
                </div>

                {selectedLoop ? (
                  <>
                    <div className="metric-grid loop-metrics">
                      <Metric label={t("inspector.status")} value={selectedLoop.enabled === false ? t("inspector.disabled") : selectedLoop.status || "-"} />
                      <Metric label={t("inspector.interval")} value={formatLoopInterval(selectedLoop)} />
                      <Metric label={t("inspector.runs")} value={selectedLoop.run_count || 0} />
                      <Metric label={t("inspector.lastRun")} value={relativeTimeLabel(selectedLoop.last_run_at)} />
                      <Metric label={t("inspector.nextRun")} value={relativeTimeLabel(selectedLoop.next_run_at)} />
                      <Metric label={t("inspector.pid")} value={selectedLoop.pid || "-"} />
                    </div>
                    {selectedLoop.last_error ? <div className="loop-error">{selectedLoop.last_error}</div> : null}
                  </>
                ) : null}

                <div className="loop-output-grid">
                  <div className="loop-log-panel">
                    <div className="loop-log-header">
                      <Clock3 size={15} />
                      <span>{loopLogsLoading ? t("chat.loading") : t("inspector.loopLatestOutput")}</span>
                    </div>
                    <pre>{selectedLoop ? selectedLoopLogs?.log_tail || selectedLoop.log_tail || t("inspector.loopNoOutput") : t("inspector.loopNoOutput")}</pre>
                  </div>
                  <div className="loop-run-history">
                    <div className="loop-log-header">
                      <History size={15} />
                      <span>{t("inspector.loopRunHistory")}</span>
                    </div>
                    {selectedLoopRuns.length === 0 ? <div className="empty-state compact">{t("inspector.loopNoRunHistory")}</div> : null}
                    {selectedLoopRuns.map((run) => (
                      <article className="loop-run-row" key={run.id}>
                        <div>
                          <strong className={runStatusClass(run.status)}>{run.status || "-"}</strong>
                          <span>{relativeTimeLabel(run.finished_at || run.started_at)}</span>
                        </div>
                        {run.error ? <small className="loop-run-error">{run.error}</small> : <small>{formatBytes(run.log_bytes || 0)}</small>}
                      </article>
                    ))}
                  </div>
                </div>
              </>
            ) : (
              <div className="empty-state">
                <p>{t("inspector.noLoops")}</p>
                <button className="primary-button" onClick={handleNewLoopDraft} type="button">
                  <Plus size={15} />
                  {t("inspector.newLoop")}
                </button>
              </div>
            )}
          </section>
        </div>
      </Panel> : null}

      {activeSection === "trace" ? <Panel
        title={t("tab.trace")}
        subtitle={selectedSessionId ? t("inspector.session", { id: selectedSessionId }) : t("inspector.selectSession")}
        icon={<Activity size={17} />}
        headerExtra={<SessionSelector sessions={sessions} selectedSessionId={selectedSessionId} onSelectSession={onSelectSession} compact />}
        onRefresh={refreshAll}
      >
        {trace?.summary ? (
          <div className="metric-grid trace-summary-grid">
            <Metric label={t("metric.turns")} value={trace.summary.turns} />
            <Metric label={t("metric.messages")} value={trace.summary.messages} />
            <Metric label={t("metric.tools")} value={trace.summary.tool_calls} />
            <Metric label={t("metric.tokens")} value={trace.summary.total_tokens} />
            <Metric label={t("metric.cache")} value={trace.summary.cache_summary || "-"} />
            <Metric label={t("metric.skills")} value={trace.summary.skills?.join(", ") || "-"} />
          </div>
        ) : (
          <div className="empty-state">{t("inspector.traceEmpty")}</div>
        )}
        <TracePerformanceOverview trace={trace} />
        <TraceViewTabs active={traceView} onChange={setTraceView} />
        {traceView === "overview" ? (
          <TraceEvidence trace={trace} />
        ) : (
          <TraceSequence trace={trace} selectedTraceId={selectedTraceId} onSelectTrace={setSelectedTraceId} />
        )}
        <div className="trace-actions">
          <button className="secondary-button" disabled={!selectedSessionId} onClick={() => selectedSessionId && void handleTraceExport(String(selectedSessionId), "tenant")} type="button">
            <Download size={15} /> {t("trace.export")}
          </button>
          <a className="trace-link" href={`${identity.apiBase.replace(/\/api$/, "")}/trace?token=${encodeURIComponent(identity.apiToken)}`} target="_blank" rel="noreferrer">
            {t("inspector.openTrace")}
          </a>
        </div>
      </Panel> : null}

      {activeSection === "local-trace" ? <Panel
        title={t("tab.localTrace")}
        subtitle={selectedLocalTraceSessionId ? t("inspector.localTraceSelected", { id: shortTraceId(selectedLocalTraceSessionId) }) : t("inspector.selectLocalTrace")}
        icon={<History size={17} />}
        onRefresh={handleRefreshLocalTrace}
      >
        <div className="local-trace-workbench">
          <aside className="local-trace-sidebar" aria-label={t("inspector.localTraceSessions")}>
            <div className="local-trace-sidebar-head">
              <span>{t("inspector.localTraceSessions")}</span>
              <strong>{filteredLocalTraceSessions.length}/{localTraceSessions.length}</strong>
            </div>
            <div className="local-trace-filters">
              <label className="local-trace-search-filter">
                <span>{t("inspector.localTraceSearch")}</span>
                <input aria-label={t("inspector.localTraceSearch")} value={localTraceSearch} onChange={(event) => setLocalTraceSearch(event.target.value)} placeholder={t("inspector.localTraceSearchPlaceholder")} />
              </label>
              <div className="local-trace-filter-row">
                <label className="local-trace-time-filter">
                  <span>{t("inspector.localTraceTime")}</span>
                  <select aria-label={t("inspector.localTraceTime")} value={localTraceTimeFilter} onChange={(event) => setLocalTraceTimeFilter(event.target.value as LocalTraceTimeFilter)}>
                    <option value="all">{t("inspector.localTraceTimeAll")}</option>
                    <option value="today">{t("inspector.localTraceTimeToday")}</option>
                    <option value="24h">{t("inspector.localTraceTime24h")}</option>
                    <option value="7d">{t("inspector.localTraceTime7d")}</option>
                  </select>
                </label>
                <label className="inline-check local-trace-project-filter">
                  <input type="checkbox" checked={localTraceProjectOnly} onChange={(event) => setLocalTraceProjectOnly(event.target.checked)} />
                  <span>{t("inspector.localTraceProjectOnly")}</span>
                </label>
                <label className="inline-check local-trace-project-filter">
                  <input type="checkbox" checked={localTraceIncludeTests} onChange={(event) => setLocalTraceIncludeTests(event.target.checked)} />
                  <span>{t("inspector.localTraceIncludeTests")}</span>
                </label>
              </div>
            </div>
            <div className="local-trace-session-list">
              {filteredLocalTraceSessions.length === 0 ? <div className="empty-state compact">{t("inspector.localTraceNoMatches")}</div> : null}
              {filteredLocalTraceSessions.map((session) => (
                <button
                  key={session.session_id}
                  className={session.session_id === selectedLocalTraceSessionId ? "local-trace-session active" : "local-trace-session"}
                  onClick={() => setSelectedLocalTraceSessionId(session.session_id)}
                  type="button"
                >
                  <strong>{localTraceTitle(session)}</strong>
                  <span>{localTraceMeta(session)}</span>
                </button>
              ))}
            </div>
          </aside>
          <section className="local-trace-detail">
            {selectedLocalTraceSession ? (
              <div className="local-trace-context">
                <span>{selectedLocalTraceSession.source || "local"}</span>
                <strong>{localTraceTitle(selectedLocalTraceSession)}</strong>
                <small title={selectedLocalTraceSession.path || ""}>{localTraceMeta(selectedLocalTraceSession)}</small>
              </div>
            ) : null}
            {localTrace?.summary ? (
              <div className="metric-grid trace-summary-grid local-trace-summary">
                <Metric label={t("metric.turns")} value={localTrace.summary.turns} />
                <Metric label={t("metric.messages")} value={localTrace.summary.messages} />
                <Metric label={t("metric.tools")} value={localTrace.summary.tool_calls} />
                <Metric label={t("metric.tokens")} value={localTrace.summary.total_tokens} />
                <Metric label={t("metric.cache")} value={localTrace.summary.cache_summary || "-"} />
                <Metric label={t("metric.skills")} value={localTrace.summary.skills?.join(", ") || "-"} />
              </div>
            ) : (
              <LocalTraceEmptyState trace={localTrace} session={selectedLocalTraceSession} />
            )}
            <TracePerformanceOverview trace={localTrace} />
            <TraceRecapCard trace={localTrace} />
            <TraceViewTabs active={localTraceView} onChange={setLocalTraceView} />
            {localTraceView === "overview" ? (
              <TraceEvidence trace={localTrace} />
            ) : (
              <TraceSequence trace={localTrace} selectedTraceId={selectedLocalTraceId} onSelectTrace={setSelectedLocalTraceId} />
            )}
            <div className="trace-actions">
              <button className="secondary-button" disabled={!selectedLocalTraceSessionId} onClick={() => selectedLocalTraceSessionId && void handleTraceExport(selectedLocalTraceSessionId, "local")} type="button">
                <Download size={15} /> {t("trace.export")}
              </button>
              <a className="trace-link" href={`${identity.apiBase.replace(/\/api$/, "")}/trace?token=${encodeURIComponent(identity.apiToken)}`} target="_blank" rel="noreferrer">
                {t("inspector.openTrace")}
              </a>
            </div>
          </section>
        </div>
      </Panel> : null}

      {activeSection === "telemetry" ? <Panel
        title={t("tab.telemetry")}
        subtitle={t("inspector.records", { count: telemetry.length })}
        icon={<RadioTower size={17} />}
        headerExtra={<SessionSelector sessions={sessions} selectedSessionId={selectedSessionId} onSelectSession={onSelectSession} compact />}
        onRefresh={refreshAll}
      >
        <TelemetryPanel telemetry={telemetry} />
      </Panel> : null}

      {activeSection === "quota" ? <QuotaPanel identity={identity} refreshTick={refreshTick} onStatus={onStatus} /> : null}
    </section>
  );
}

function SessionSelector({
  sessions,
  selectedSessionId,
  onSelectSession,
  compact = false
}: {
  sessions: TenantSession[];
  selectedSessionId: number | null;
  onSelectSession: (id: number | null) => void;
  compact?: boolean;
}) {
  const { t } = useI18n();
  return (
    <div className={compact ? "trace-session-selector compact" : "trace-session-selector"}>
      <label>
        {compact ? <span className="sr-only">{t("inspector.selectTraceSession")}</span> : t("inspector.selectTraceSession")}
        <select
          aria-label={t("inspector.selectTraceSession")}
          value={selectedSessionId ? String(selectedSessionId) : ""}
          onChange={(event) => onSelectSession(event.target.value ? Number(event.target.value) : null)}
        >
          <option value="">{t("inspector.selectSession")}</option>
          {sessions.map((session) => (
            <option key={session.id} value={session.id}>
              {sessionLabel(session)}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

function Panel({
  title,
  subtitle,
  icon,
  children,
  onRefresh,
  headerExtra
}: {
  title: string;
  subtitle: string;
  icon: React.ReactNode;
  children: React.ReactNode;
  onRefresh?: () => void | Promise<void>;
  headerExtra?: React.ReactNode;
}) {
  const { t } = useI18n();
  return (
    <section className="panel inspector-panel">
      <div className="panel-header">
        <div className="title-row">
          {icon}
          <div>
            <h2>{title}</h2>
            <p>{subtitle}</p>
          </div>
        </div>
        <div className="panel-header-actions">
          {headerExtra}
          {onRefresh ? (
            <button type="button" className="icon-button" onClick={() => void onRefresh()} title={t("inspector.refresh", { title })}>
              <RefreshCcw size={16} />
            </button>
          ) : null}
        </div>
      </div>
      {children}
    </section>
  );
}

function RecordList({ items }: { items: Array<{ id?: string; title: string; body: string; meta: string }> }) {
  const { t } = useI18n();
  if (items.length === 0) {
    return <div className="empty-state compact">{t("inspector.noRecords")}</div>;
  }
  return (
    <div className="record-list">
      {items.slice(0, 6).map((item, index) => (
        <article key={item.id || `${item.title}-${item.meta}-${index}`} className="record-item">
          <strong>{item.title}</strong>
          <small>{item.meta}</small>
          <p>{item.body}</p>
        </article>
      ))}
    </div>
  );
}

function MemoryReviewDetail({
  candidate,
  sourceMessages,
  sourceLoading
}: {
  candidate: MemoryReviewCandidate | null;
  sourceMessages: TenantMessage[];
  sourceLoading: boolean;
}) {
  const { t } = useI18n();
  if (!candidate) {
    return <aside className="memory-review-detail empty"><div className="empty-state compact">{t("inspector.noRecords")}</div></aside>;
  }
  const item = candidate.item;
  const sourceMessage = findSourceMessage(sourceMessages, candidate.sourceMessageId);
  return (
    <aside className="memory-review-detail">
      <div className="memory-review-detail-header">
        <span>{t("inspector.details")}</span>
        <strong>{item.memory_key || `Memory ${item.id}`}</strong>
      </div>
      <dl className="memory-review-facts">
        <div><dt>{t("inspector.candidateType")}</dt><dd>{candidate.candidateType}</dd></div>
        <div><dt>{t("inspector.riskStatus")}</dt><dd>{candidate.riskStatus}</dd></div>
        <div><dt>{t("inspector.category")}</dt><dd>{item.category || "-"}</dd></div>
        <div><dt>{t("inspector.source")}</dt><dd>{item.source || "-"}</dd></div>
        <div><dt>{t("inspector.sourceSession")}</dt><dd>{candidate.sourceSessionId ? `#${candidate.sourceSessionId}` : "-"}</dd></div>
        <div><dt>{t("inspector.sourceMessage")}</dt><dd>{candidate.sourceMessageId ? `#${candidate.sourceMessageId}` : "-"}</dd></div>
        <div><dt>{t("trace.traceIds")}</dt><dd title={candidate.traceId}>{candidate.traceId ? shortTraceId(candidate.traceId) : "-"}</dd></div>
      </dl>
      <section className="memory-review-block">
        <h3>{t("inspector.memoryContent")}</h3>
        <p>{item.content || "-"}</p>
      </section>
      <section className="memory-review-block">
        <h3>{t("inspector.sourceMessage")}</h3>
        {sourceLoading ? <p>{t("chat.loading")}</p> : null}
        {!sourceLoading && sourceMessage ? (
          <article className="source-message-card">
            <strong>{sourceMessage.role || t("trace.event")}</strong>
            <small>{sourceMessage.created_at || sourceMessage.status || ""}</small>
            <p>{sourceMessage.content || sourceMessage.content_json || "-"}</p>
          </article>
        ) : null}
        {!sourceLoading && !sourceMessage ? <p>{candidate.sourceSessionId ? t("inspector.sourceMessageUnavailable") : t("inspector.noSourceMessage")}</p> : null}
      </section>
      <section className="memory-review-block">
        <h3>{t("inspector.metadata")}</h3>
        <pre>{prettyJSON(candidate.metadata)}</pre>
      </section>
    </aside>
  );
}

function memoryMetadata(item: MemoryRecord): Record<string, unknown> {
  if (!item.metadata_json || typeof item.metadata_json !== "string") {
    return {};
  }
  try {
    const parsed = JSON.parse(item.metadata_json);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

function enrichMemoryReviewCandidate(item: MemoryRecord): MemoryReviewCandidate {
  const metadata = memoryMetadata(item);
  const candidateType = stringValue(metadata.candidate_type) || (item.category === "automem_pending" ? "automem_candidate" : "explicit_remember");
  return {
    item,
    key: item.memory_key || `memory-${item.id}`,
    metadata,
    candidateType,
    riskStatus: stringValue(metadata.risk_status) || "needs_review",
    sourceSessionId: numberValue(metadata.source_session_id),
    sourceMessageId: numberValue(metadata.source_message_id),
    traceId: stringValue(metadata.trace_id)
  };
}

function reviewMetaLine(candidate: MemoryReviewCandidate): string {
  const sourceSession = candidate.sourceSessionId ? `session ${candidate.sourceSessionId}` : "";
  const sourceMessage = candidate.sourceMessageId ? `message ${candidate.sourceMessageId}` : "";
  const trace = candidate.traceId ? `trace ${shortTraceId(candidate.traceId)}` : "";
  return [candidate.candidateType, candidate.item.category, candidate.riskStatus, candidate.item.source, sourceSession, sourceMessage, trace].filter(Boolean).join(" · ");
}

function reviewStatusMessage(action: "approve" | "reject" | "archive", t: (key: string) => string): string {
  if (action === "approve") {
    return t("inspector.memoryCandidateApproved");
  }
  if (action === "archive") {
    return t("inspector.memoryCandidateArchived");
  }
  return t("inspector.memoryCandidateRejected");
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function numberValue(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
}

function uniqueStrings(values: string[]): string[] {
  return Array.from(new Set(values.filter(Boolean))).sort((a, b) => a.localeCompare(b));
}

function memoryReviewSessionLabel(sessionId: number, sessions: TenantSession[]): string {
  const session = sessions.find((item) => item.id === sessionId);
  return session ? sessionLabel(session) : `#${sessionId}`;
}

function findSourceMessage(messages: TenantMessage[], sourceMessageId: number | null): TenantMessage | null {
  if (messages.length === 0) {
    return null;
  }
  if (!sourceMessageId) {
    return messages[0] || null;
  }
  return messages.find((message) => message.id === sourceMessageId) || null;
}

function prettyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function skillPackageSourceRequest(sourcePath: string, contentBase64: string): { source_path?: string; content_base64?: string } {
  if (contentBase64.trim() !== "") {
    return { content_base64: contentBase64.trim() };
  }
  return { source_path: sourcePath.trim() };
}

async function fileToBase64(file: File): Promise<string> {
  const buffer = await file.arrayBuffer();
  let binary = "";
  const bytes = new Uint8Array(buffer);
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

function sessionLabel(session: TenantSession): string {
  const title = session.title || session.session_key || `Session ${session.id}`;
  const status = session.status || "active";
  const time = session.last_message_at || session.started_at || "";
  return [`#${session.id}`, title, status, time].filter(Boolean).join(" · ");
}

function localTraceTitle(session: TraceSessionSummary): string {
  return session.title || session.message_hint || shortTraceId(session.session_id) || "Local trace";
}

function localTraceMeta(session: TraceSessionSummary): string {
  return [shortTraceId(session.session_id), relativeTimeLabel(session.updated_at || session.started_at), localTraceProjectLabel(session)].filter(Boolean).join(" · ");
}

export function loopMeta(loop: RuntimeBackgroundJob): string {
  return [loop.id, formatLoopInterval(loop), `runs ${loop.run_count || 0}`, relativeTimeLabel(loop.last_run_at)].filter(Boolean).join(" · ");
}

export function formatLoopInterval(loop: RuntimeBackgroundJob): string {
  if (loop.interval_seconds && loop.interval_seconds > 0) {
    const seconds = loop.interval_seconds;
    if (seconds%3600 === 0) {
      return `${seconds / 3600}h`;
    }
    if (seconds%60 === 0) {
      return `${seconds / 60}m`;
    }
    return `${seconds}s`;
  }
  return loop.spec || "-";
}

function loopStatusClass(loop: RuntimeBackgroundJob): string {
  const status = loop.enabled === false ? "disabled" : (loop.status || "").toLowerCase();
  if (status === "running") {
    return "loop-status running";
  }
  if (status === "killed" || status === "failed" || status === "disabled") {
    return "loop-status stopped";
  }
  return "loop-status";
}

function runStatusClass(status?: string): string {
  const normalized = (status || "").toLowerCase();
  if (normalized === "completed") {
    return "loop-run-status completed";
  }
  if (normalized === "failed" || normalized === "error") {
    return "loop-run-status failed";
  }
  return "loop-run-status";
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${units[unitIndex]}`;
}

function shortHash(value?: string): string {
  const text = (value || "").trim();
  return text.length > 12 ? text.slice(0, 12) : text;
}

function LocalTraceEmptyState({ trace, session }: { trace: TraceDetail | null; session: TraceSessionSummary | null }) {
  const { t } = useI18n();
  const events = traceEvents(trace);
  const spans = traceSpans(trace);
  return (
    <div className="empty-state local-trace-empty">
      <strong>{t("inspector.localTraceEmpty")}</strong>
      <span>{t("inspector.localTraceEmptyDetail", { events: events.length, spans: spans.length })}</span>
      {session?.path ? <small title={session.path}>{session.path}</small> : null}
    </div>
  );
}

function TraceRecapCard({ trace }: { trace: TraceDetail | null }) {
  const { t } = useI18n();
  const latest = latestTraceRecap(trace);
  if (!latest?.content) {
    return null;
  }
  const recaps = traceRecaps(trace);
  const meta = traceRecapMeta(latest);
  return (
    <section className="trace-recap-card" aria-label={t("trace.latestRecap")}>
      <div className="trace-recap-heading">
        <span>{t("trace.latestRecap")}</span>
        <small>{t("trace.recapCount", { count: recaps.length })}</small>
      </div>
      <p>{latest.content}</p>
      {meta ? <div className="trace-recap-meta">{meta}</div> : null}
    </section>
  );
}

export function inferCurrentLocalProject(sessions: TraceSessionSummary[]): string {
  const projectCounts = new Map<string, { value: string; count: number; latest: number }>();
  for (const session of sessions) {
    if (isLocalTraceTestSession(session)) {
      continue;
    }
    const project = localTraceProjectKey(session);
    if (!project) {
      continue;
    }
    const current = projectCounts.get(project) || { value: project, count: 0, latest: 0 };
    current.count += 1;
    current.latest = Math.max(current.latest, parseTimeMs(session.updated_at || session.started_at));
    projectCounts.set(project, current);
  }
  return Array.from(projectCounts.values()).sort((a, b) => b.latest - a.latest || b.count - a.count || a.value.localeCompare(b.value))[0]?.value || "";
}

export function filterLocalTraceSessions(sessions: TraceSessionSummary[], options: LocalTraceFilterOptions): TraceSessionSummary[] {
  const query = options.search.trim().toLowerCase();
  const cutoff = localTraceCutoff(options.timeFilter, options.now || new Date());
  return sessions.filter((session) => {
    if (!options.includeTestSessions && isLocalTraceTestSession(session)) {
      return false;
    }
    if (options.projectOnly && options.currentProject && localTraceProjectKey(session) !== options.currentProject) {
      return false;
    }
    if (cutoff && localTraceSessionTime(session) < cutoff.getTime()) {
      return false;
    }
    if (!query) {
      return true;
    }
    const haystack = [session.session_id, session.title, session.message_hint, session.cwd, session.path, session.model, session.status].filter(Boolean).join(" ").toLowerCase();
    return haystack.includes(query);
  });
}

export function isLocalTraceTestSession(session: TraceSessionSummary): boolean {
  const value = [session.cwd, session.path].filter(Boolean).join(" ").toLowerCase();
  if (!value) {
    return false;
  }
  return (
    value.includes("testruntime") ||
    value.includes("testagent") ||
    value.includes("test_runtime") ||
    value.includes("var-folders-") ||
    value.includes("/var/folders/") ||
    value.includes("/tmp/test")
  );
}

function localTraceCutoff(filter: LocalTraceTimeFilter, now: Date): Date | null {
  if (filter === "all") {
    return null;
  }
  if (filter === "today") {
    return new Date(now.getFullYear(), now.getMonth(), now.getDate());
  }
  const hours = filter === "24h" ? 24 : 24 * 7;
  return new Date(now.getTime() - hours * 60 * 60 * 1000);
}

function localTraceSessionTime(session: TraceSessionSummary): number {
  return parseTimeMs(session.updated_at || session.started_at);
}

function localTraceProjectLabel(session: TraceSessionSummary): string {
  const key = localTraceProjectKey(session);
  if (!key) {
    return session.path || "";
  }
  return key.replace(/^-/, "").replace(/-/g, "/");
}

function localTraceProjectKey(session: TraceSessionSummary): string {
  const cwd = (session.cwd || "").trim();
  if (cwd) {
    return cwd;
  }
  const match = (session.path || "").match(/\/projects\/([^/]+)\//);
  return match?.[1] || "";
}

function projectSlug(cwd: string): string {
  const normalized = cwd.trim().replace(/\\/g, "/").replace(/\/+$/, "");
  if (!normalized) {
    return "";
  }
  const withoutRoot = normalized.replace(/^\/+/, "");
  return withoutRoot.replace(/:/g, "").replace(/\s+/g, "-").replace(/\//g, "-");
}

function Metric({ label, value }: { label: string; value: React.ReactNode }) {
  const title = typeof value === "string" || typeof value === "number" ? `${label}: ${value}` : label;
  return (
    <div className="metric" title={title} data-metric-tooltip={title}>
      <span>{label}</span>
      <strong>{value ?? "-"}</strong>
    </div>
  );
}

function TelemetryPanel({ telemetry }: { telemetry: TelemetryRecord[] }) {
  const { t } = useI18n();
  if (telemetry.length === 0) {
    return <div className="empty-state compact">{t("inspector.noRecords")}</div>;
  }
  const okCount = telemetry.filter((item) => statusClass(item.status || "") === "ok").length;
  const warningCount = telemetry.filter((item) => statusClass(item.status || "") === "warn").length;
  const failureCount = telemetry.filter((item) => statusClass(item.status || "") === "fail").length;
  const traceCount = new Set(telemetry.map((item) => item.trace_id).filter(Boolean)).size;
  const totalDurationMs = telemetry.reduce((sum, item) => sum + Number(item.duration_ms || 0), 0);
  const totalTokens = telemetry.reduce((sum, item) => sum + Number(item.input_tokens || 0) + Number(item.output_tokens || 0), 0);
  const skillSummary = summarizeSkillTelemetry(telemetry);

  return (
    <div className="telemetry-panel">
      {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .telemetry-summary is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
      <div className="telemetry-summary" role="group" aria-label={t("tab.telemetry")}>
        <Metric label={t("trace.telemetryEvents")} value={telemetry.length} />
        <Metric label={t("trace.traceIds")} value={traceCount || "-"} />
        <Metric label={t("trace.totalDuration")} value={durationLabel(totalDurationMs) || "0ms"} />
        <Metric label={t("metric.tokens")} value={totalTokens || "-"} />
        <div className="telemetry-health">
          <span className="trace-status-chip ok">ok {okCount}</span>
          <span className="trace-status-chip warn">warn {warningCount}</span>
          <span className="trace-status-chip fail">fail {failureCount}</span>
        </div>
      </div>
      {skillSummary.length > 0 ? (
        /* biome-ignore lint/a11y/useSemanticElements: keep <div> — .telemetry-skill-summary is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */
        <div className="telemetry-skill-summary" role="group" aria-label={t("inspector.skillTelemetry")}>
          <div className="section-heading compact">
            <h3>{t("inspector.skillTelemetry")}</h3>
            <p>{t("inspector.skillTelemetryDetail", { count: skillSummary.length })}</p>
          </div>
          <div className="telemetry-skill-grid">
            {skillSummary.slice(0, 6).map((item) => (
              <article className="telemetry-skill-card" key={item.key}>
                <strong title={item.skill}>{item.skill}</strong>
                <span>{[item.source, item.version ? `v${item.version}` : "", item.fallback === "true" ? "fallback" : ""].filter(Boolean).join(" · ")}</span>
                <small>{t("inspector.skillTelemetryCounts", { count: item.count, errors: item.errors, duration: durationLabel(item.durationMs) || "0ms", tokens: item.tokens || "-" })}</small>
              </article>
            ))}
          </div>
        </div>
      ) : null}
      <div className="telemetry-list">
        {telemetry.slice(0, 12).map((item, index) => (
          <TelemetryRow key={item.id || `${item.name}-${item.trace_id}-${index}`} item={item} />
        ))}
      </div>
    </div>
  );
}

function TelemetryRow({ item }: { item: TelemetryRecord }) {
  const title = item.name || `Telemetry ${item.id || ""}`.trim();
  const status = item.status || "";
  const statusKind = statusClass(status);
  const timeLabel = relativeTimeLabel(item.occurred_at || item.created_at);
  const detail = telemetryDetail(item);
  return (
    <article className={`telemetry-row ${statusKind}`}>
      <div className="telemetry-row-marker" aria-hidden="true" />
      <div className="telemetry-row-main">
        <div className="telemetry-row-header">
          <div className="telemetry-row-title">
            <span className={`telemetry-category ${categoryClass(item.category || item.name || "")}`}>{item.category || "event"}</span>
            <strong title={title}>{title}</strong>
          </div>
          <div className="telemetry-row-badges">
            {status ? <span className={`trace-status-chip ${statusKind}`}>{status}</span> : null}
            {item.duration_ms ? <span className="telemetry-duration">{durationLabel(item.duration_ms)}</span> : null}
          </div>
        </div>
        <div className="telemetry-row-meta">
          {timeLabel ? <span>{timeLabel}</span> : null}
          {item.trace_id ? <span title={item.trace_id}>trace {shortTraceId(item.trace_id)}</span> : null}
          {item.session_id ? <span>session #{item.session_id}</span> : null}
          {item.model ? <span>{item.model}</span> : null}
          {item.tool_name ? <span>{item.tool_name}</span> : null}
          {tokenLine(item) ? <span>{tokenLine(item)}</span> : null}
        </div>
        {detail ? <p title={detail}>{detail}</p> : null}
      </div>
    </article>
  );
}

function TraceViewTabs({ active, onChange }: { active: "overview" | "sequence"; onChange: (tab: "overview" | "sequence") => void }) {
  const { t } = useI18n();
  return (
    <div className="trace-subtabs" role="tablist" aria-label={t("trace.viewTabs")}>
      <button type="button" className={active === "overview" ? "active" : ""} role="tab" aria-selected={active === "overview"} onClick={() => onChange("overview")}>
        {t("trace.overviewTab")}
      </button>
      <button type="button" className={active === "sequence" ? "active" : ""} role="tab" aria-selected={active === "sequence"} onClick={() => onChange("sequence")}>
        {t("trace.sequenceTab")}
      </button>
    </div>
  );
}

function TraceEvidence({ trace }: { trace: TraceDetail | null }) {
  const { t } = useI18n();
  const events = traceEvents(trace);
  const spans = traceSpans(trace);
  const modelSpans = spans.filter((span) => span.type === "model" || span.model);
  const toolSpans = spans.filter((span) => span.type === "tool" || span.tool_name);
  const auditEvents = events.filter((event) => event.type?.includes("audit") || event.name?.includes("permission") || event.name?.includes("audit"));
  const recaps = traceRecaps(trace);
  const runtimeEvents = events
    .filter(
      (event) =>
        event.name?.startsWith("mobile.") ||
        event.name?.startsWith("model.") ||
        event.name?.startsWith("query.") ||
        event.name?.startsWith("api.request") ||
        event.type === "message" ||
        event.type === "telemetry"
    )
    .slice(0, 10);
  const tokenSummary = trace?.summary?.token_summary || `${trace?.summary?.input_tokens || 0} in / ${trace?.summary?.output_tokens || 0} out`;
  const cacheSummary = trace?.summary?.cache_summary || `${trace?.summary?.cache_read_tokens || 0} read / ${trace?.summary?.cache_creation_tokens || 0} create`;
  const traceIds = Array.isArray(trace?.trace_ids) ? trace.trace_ids : [];
  const telemetryEvents = Array.isArray(trace?.telemetry_events) ? trace.telemetry_events : [];

  return (
    <div className="trace-evidence-grid">
      <div className="trace-token-band trace-evidence-strip">
        <Metric label={t("trace.traceIds")} value={traceIds.length ? traceIds.join(", ") : "-"} />
        <Metric label={t("trace.telemetryEvents")} value={telemetryEvents.length} />
      </div>
      <EvidenceBlock title={t("trace.recapHistory")} count={recaps.length}>
        <RecordList
          items={recaps.slice().reverse().map((recap, index) => ({
            id: recap.id || `recap-${index}`,
            title: recap.status || t("trace.recap"),
            body: recap.content || "",
            meta: traceRecapMeta(recap) || "-"
          }))}
        />
      </EvidenceBlock>
      <EvidenceBlock title={t("trace.runtimeTimeline")} count={events.length}>
        <RecordList
          items={runtimeEvents.map((event, index) => ({
            id: event.id || `runtime-${index}`,
            title: event.name || event.type || t("trace.event"),
            body: event.content || compactJSON(event.properties) || event.output || "",
            meta: [event.status, event.role, event.trace_id, durationLabel(event.duration_ms)].filter(Boolean).join(" · ")
          }))}
        />
      </EvidenceBlock>
      <EvidenceBlock title={t("trace.modelRequests")} count={modelSpans.length}>
        <RecordList
          items={modelSpans.slice(0, 6).map((span, index) => ({
            id: span.id || `model-${index}`,
            title: span.model || span.name || t("trace.model"),
            body: span.error || "",
            meta: [span.status, durationLabel(span.duration_ms)].filter(Boolean).join(" · ")
          }))}
        />
      </EvidenceBlock>
      <EvidenceBlock title={t("trace.toolCalls")} count={toolSpans.length}>
        <RecordList
          items={toolSpans.slice(0, 6).map((span, index) => ({
            id: span.id || `tool-${index}`,
            title: span.tool_name || span.name || t("trace.tool"),
            body: span.error || span.skill || "",
            meta: [span.status, durationLabel(span.duration_ms)].filter(Boolean).join(" · ")
          }))}
        />
      </EvidenceBlock>
      <EvidenceBlock title={t("trace.auditPermissions")} count={auditEvents.length}>
        <RecordList
          items={auditEvents.slice(0, 6).map((event, index) => ({
            id: event.id || `audit-${index}`,
            title: event.name || event.type || t("trace.audit"),
            body: compactJSON(event.properties) || event.content || "",
            meta: [event.status, event.tool_name].filter(Boolean).join(" · ")
          }))}
        />
      </EvidenceBlock>
      <div className="trace-token-band trace-evidence-strip">
        <Metric label={t("trace.tokenUsage")} value={tokenSummary} />
        <Metric label={t("trace.promptCache")} value={cacheSummary} />
      </div>
    </div>
  );
}

export function traceQualityMetrics(quality: TraceQuality, t: (key: string) => string): Array<{ label: string; value: string | number }> {
  const booleanValue = (value: boolean | undefined) => typeof value === "boolean" ? t(value ? "trace.yes" : "trace.no") : t("trace.unknown");
  const finalVerification = typeof quality.final_verification_passed === "boolean"
    ? t(quality.final_verification_passed ? "trace.passed" : "trace.failed")
    : t("trace.unknown");
  return [
    { label: t("trace.tests"), value: quality.tests_run ? t(quality.tests_passed ? "trace.passed" : "trace.failed") : t("trace.notRun") },
    { label: t("trace.finalVerification"), value: finalVerification },
    { label: t("trace.testAttempts"), value: quality.test_attempts ?? "-" },
    { label: t("trace.failedTestAttempts"), value: quality.failed_test_attempts ?? "-" },
    { label: t("trace.testFailureRecovered"), value: booleanValue(quality.test_failure_recovered) },
    { label: t("trace.completionVerified"), value: booleanValue(quality.completion_verified) },
    { label: t("trace.toolErrors"), value: quality.tool_errors },
    { label: t("trace.recoveries"), value: quality.recoveries },
    { label: t("trace.gateBlocks"), value: quality.gate_blocks },
    { label: t("trace.todoWrites"), value: quality.todo_writes }
  ];
}

function TracePerformanceOverview({ trace }: { trace: TraceDetail | null }) {
  const { t } = useI18n();
  const summary = trace?.summary;
  const quality = trace?.quality;
  const diagnostics = Array.isArray(trace?.diagnostics) ? trace.diagnostics : [];
  if (!summary && !quality && diagnostics.length === 0) {
    return null;
  }
  return (
    <div className="trace-performance-overview">
      <div className="metric-grid trace-summary-grid">
        <Metric label={t("trace.taskWall")} value={durationLabel(summary?.task_wall_ms)} />
        <Metric label={t("trace.modelWall")} value={durationLabel(summary?.model_wall_ms)} />
        <Metric label={t("trace.toolWork")} value={durationLabel(summary?.tool_work_ms)} />
        <Metric label={t("trace.toolWall")} value={durationLabel(summary?.tool_wall_ms)} />
        <Metric label={t("trace.criticalPath")} value={durationLabel(summary?.critical_path_ms)} />
        <Metric label={t("trace.unattributed")} value={durationLabel(summary?.unattributed_ms)} />
        <Metric label={t("trace.parallelSavings")} value={durationLabel(summary?.parallel_savings_ms)} />
      </div>
      {quality ? (
        <div className="metric-grid trace-summary-grid trace-quality-grid">
          {traceQualityMetrics(quality, t).map((metric) => <Metric key={metric.label} label={metric.label} value={metric.value} />)}
        </div>
      ) : null}
      {diagnostics.length ? (
        <EvidenceBlock title={t("trace.diagnostics")} count={diagnostics.length}>
          <RecordList items={diagnostics.map((item, index) => ({
            id: item.fingerprint || `${item.code}-${index}`,
            title: item.code || t("trace.diagnostic"),
            body: item.message || "",
            meta: [item.severity, item.turn_index ? `turn ${item.turn_index}` : "", item.tool_name, item.count ? `x${item.count}` : "", durationLabel(item.estimated_savings_ms)].filter(Boolean).join(" · ")
          }))} />
        </EvidenceBlock>
      ) : null}
    </div>
  );
}

type TraceRequestOption = {
  id: string;
  label: string;
  count: number;
  startMs: number;
};

type TraceStep = {
  id: string;
  traceId: string;
  kind: string;
  title: string;
  subtitle: string;
  status: string;
  startMs: number;
  endMs: number;
  durationMs: number;
  selfDurationMs: number;
  detail: string;
  skill: string;
  level: number;
  childrenDurationMs: number;
  parentId: string;
  childIds: string[];
};

function TraceSequence({
  trace,
  selectedTraceId,
  onSelectTrace
}: {
  trace: TraceDetail | null;
  selectedTraceId: string;
  onSelectTrace: (traceId: string) => void;
}) {
  const { t } = useI18n();
  const options = traceRequestOptions(trace);
  const effectiveTraceId = selectedTraceId || options[0]?.id || "";
  const steps = traceSequenceSteps(trace, effectiveTraceId);
  const [collapsedSteps, setCollapsedSteps] = useState<Set<string>>(new Set());
  useEffect(() => {
    setCollapsedSteps(new Set());
  }, [effectiveTraceId]);
  const bounds = sequenceBounds(steps);
  const totalDuration = Math.max(0, bounds.endMs - bounds.startMs);
  const selectedOption = options.find((option) => option.id === effectiveTraceId);
  const visibleSteps = useMemo(() => visibleTraceSteps(steps, collapsedSteps), [steps, collapsedSteps]);
  const attribution = traceAttribution(steps, totalDuration);
  const slowPath = traceSlowPath(steps, totalDuration);
  const toggleStep = (stepId: string) => {
    setCollapsedSteps((current) => {
      const next = new Set(current);
      if (next.has(stepId)) {
        next.delete(stepId);
      } else {
        next.add(stepId);
      }
      return next;
    });
  };

  if (options.length === 0) {
    return <div className="empty-state compact">{t("trace.noSequence")}</div>;
  }

  return (
    <section className="trace-sequence-panel">
      <div className="trace-request-picker">
        <label>
          {t("trace.request")}
          <select aria-label={t("trace.request")} value={effectiveTraceId} onChange={(event) => onSelectTrace(event.target.value)}>
            {options.map((option) => (
              <option key={option.id} value={option.id}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
        <div className="trace-selected-request">
          <span>{t("trace.selectedRequest")}</span>
          <strong title={effectiveTraceId}>{shortTraceId(effectiveTraceId)}</strong>
          <small>{selectedOption ? t("trace.requestEventCount", { count: selectedOption.count }) : t("trace.noEvents")}</small>
        </div>
        <div className="trace-sequence-summary">
          <Metric label={t("trace.steps")} value={steps.length} />
          <Metric label={t("trace.totalDuration")} value={durationLabel(totalDuration)} />
        </div>
      </div>
      {steps.length > 0 ? (
        /* biome-ignore lint/a11y/useSemanticElements: keep <div> — .trace-attribution-grid is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */
        <div className="trace-attribution-grid" role="group" aria-label={t("trace.attribution")}>
          <AttributionCard label={t("trace.bottleneck")} value={attribution.bottleneck.label} detail={attribution.bottleneck.detail} title={attribution.bottleneck.title} />
          <AttributionCard label={t("trace.streamCreate")} value={attribution.streamCreate.value} detail={attribution.streamCreate.detail} title={t("trace.streamCreateHelp")} />
          <AttributionCard label={t("trace.firstDelta")} value={attribution.firstDelta.value} detail={attribution.firstDelta.detail} title={t("trace.firstDeltaHelp")} />
          <AttributionCard label={t("trace.streamRead")} value={attribution.streamRead.value} detail={attribution.streamRead.detail} title={t("trace.streamReadHelp")} />
        </div>
      ) : null}
      {slowPath ? <SlowPathPanel slowPath={slowPath} /> : null}
      {/* biome-ignore lint/a11y/useSemanticElements: keep <div> — .trace-waterfall is a CSS grid layout, converting to <fieldset> would add UA default border/padding/min-width and require CSS rework */}
      <div className="trace-waterfall" role="group" aria-label={t("trace.sequenceTab")}>
        {steps.length === 0 ? (
          <div className="empty-state compact">{t("trace.noSequence")}</div>
        ) : (
          visibleSteps.map((step, index) => (
            <article key={step.id} className={`trace-step ${step.kind} level-${step.level} ${traceStepVisualClass(step, slowPath?.bottleneck.id || "")}`} style={{ "--trace-step-level": step.level } as CSSProperties}>
              <div className="trace-step-index">{index + 1}</div>
              <div className="trace-step-main">
                <div className="trace-step-header">
                  <div className="trace-step-title">
                    {step.childIds.length > 0 ? (
                      <button className="trace-collapse-button" type="button" title={collapsedSteps.has(step.id) ? t("trace.expand") : t("trace.collapse")} onClick={() => toggleStep(step.id)}>
                        {collapsedSteps.has(step.id) ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
                      </button>
                    ) : (
                      <span className="trace-collapse-spacer" />
                    )}
                    <span className={`trace-kind-chip ${step.kind}`}>{stepKindLabel(step.kind, t)}</span>
                    <strong title={phaseHelp(step.title, t)}>{step.title}</strong>
                    {slowPath?.bottleneck.id === step.id ? <span className="trace-bottleneck-pill">{t("trace.bottleneck")}</span> : null}
                  </div>
                  <span className="trace-step-duration">{durationLabel(step.durationMs) || t("trace.instant")}</span>
                </div>
                <div className="trace-step-bar">
                  <span style={barStyle(step, bounds)} />
                </div>
                <div className="trace-step-meta">
                  <span>{t("trace.relativeStart")}: +{durationLabel(step.startMs - bounds.startMs) || "0ms"}</span>
                  {step.status ? <span className={`trace-status-chip ${statusClass(step.status)}`}>{step.status}</span> : null}
                  {step.skill ? <span>{t("trace.skill")}: {step.skill}</span> : null}
                </div>
                {step.childrenDurationMs > 0 ? (
                  <div className="trace-step-breakdown">
                    <span>{t("trace.childDuration")}: {durationLabel(step.childrenDurationMs)}</span>
                    <span>{t("trace.selfDuration")}: {durationLabel(step.selfDurationMs || Math.max(0, step.durationMs - step.childrenDurationMs)) || "0ms"}</span>
                  </div>
                ) : null}
                {step.subtitle || step.detail ? <p>{step.subtitle || step.detail}</p> : null}
              </div>
            </article>
          ))
        )}
      </div>
    </section>
  );
}

function AttributionCard({ label, value, detail, title }: { label: string; value: string; detail: string; title: string }) {
  return (
    <div className="trace-attribution-card" title={title}>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}

type SlowPath = {
  bottleneck: TraceStep;
  streamCreate?: TraceStep;
  waitFirstByte?: TraceStep;
  dns?: TraceStep;
  read?: TraceStep;
  totalDuration: number;
};

function SlowPathPanel({ slowPath }: { slowPath: SlowPath }) {
  const { t } = useI18n();
  const rows = [
    slowPath.waitFirstByte ? { key: "wait", label: t("trace.remoteFirstByte"), step: slowPath.waitFirstByte, tone: "hot" } : null,
    slowPath.dns ? { key: "dns", label: t("trace.dnsLookup"), step: slowPath.dns, tone: "warm" } : null,
    slowPath.read ? { key: "read", label: t("trace.streamRead"), step: slowPath.read, tone: "cool" } : null
  ].filter(Boolean) as { key: string; label: string; step: TraceStep; tone: string }[];

  return (
    <section className="trace-slow-path" aria-label={t("trace.slowPath")}>
      <div className="trace-slow-path-lead">
        <Gauge size={18} />
        <div>
          <span>{t("trace.slowPath")}</span>
          <strong>{slowPath.bottleneck.title}</strong>
        </div>
        <b>{durationLabel(slowPath.bottleneck.durationMs)}</b>
      </div>
      <div className="trace-slow-path-rows">
        {rows.map((row) => (
          <div key={row.key} className={`trace-slow-path-row ${row.tone}`} title={phaseHelp(row.step.title, t)}>
            <span>{row.label}</span>
            <strong>{durationLabel(row.step.durationMs)}</strong>
            <small>{durationPercentLabel(row.step.durationMs, slowPath.totalDuration)}</small>
          </div>
        ))}
      </div>
      <p>{slowPathMessage(slowPath, t)}</p>
    </section>
  );
}

function EvidenceBlock({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <section className="evidence-block">
      <div className="evidence-heading">
        <strong>{title}</strong>
        <small>{count}</small>
      </div>
      {children}
    </section>
  );
}

function traceEvents(trace: TraceDetail | null): TraceEventRecord[] {
  return Array.isArray(trace?.events) ? (trace.events as TraceEventRecord[]) : [];
}

function traceSpans(trace: TraceDetail | null): TraceSpanRecord[] {
  return Array.isArray(trace?.spans) ? (trace.spans as TraceSpanRecord[]) : [];
}

export function traceRecaps(trace: TraceDetail | null): TraceRecap[] {
  if (Array.isArray(trace?.recaps)) {
    return trace.recaps.filter((item) => Boolean(item?.content));
  }
  return traceEvents(trace)
    .filter((event) => event.type === "recap_summary" || event.name === "session.recap")
    .map((event): TraceRecap => ({
      id: event.id,
      content: event.content,
      model: event.model,
      source: stringFromProperties(event.properties, "source"),
      status: event.status || stringFromProperties(event.properties, "status"),
      time: event.time,
      duration_ms: event.duration_ms || numberFromProperties(event.properties, "duration_ms"),
      summarizes_entry_id: stringFromProperties(event.properties, "summarizes_entry_id") || stringFromProperties(event.properties, "message_id"),
      properties: event.properties
    }))
    .filter((item) => Boolean(item.content));
}

export function latestTraceRecap(trace: TraceDetail | null): TraceRecap | null {
  if (trace?.latest_recap?.content) {
    return trace.latest_recap;
  }
  const recaps = traceRecaps(trace);
  for (let index = recaps.length - 1; index >= 0; index -= 1) {
    if ((recaps[index].status || "").toLowerCase() !== "invalidated") {
      return recaps[index];
    }
  }
  return null;
}

function traceRecapMeta(recap: TraceRecap): string {
  return [
    recap.source,
    recap.status,
    recap.model,
    relativeTimeLabel(recap.time),
    durationLabel(recap.duration_ms),
    recap.summarizes_entry_id ? `entry ${shortTraceId(recap.summarizes_entry_id)}` : ""
  ].filter(Boolean).join(" · ");
}

function traceRequestOptions(trace: TraceDetail | null): TraceRequestOption[] {
  const groups = new Map<string, TraceRequestOption>();
  const add = (traceId: string, timeValue?: string) => {
    const id = traceId.trim();
    if (!id) {
      return;
    }
    const timeMs = parseTimeMs(timeValue);
    const current = groups.get(id);
    if (current) {
      current.count += 1;
      current.startMs = Math.min(current.startMs || timeMs, timeMs || current.startMs);
      return;
    }
    groups.set(id, {
      id,
      label: id,
      count: 1,
      startMs: timeMs
    });
  };
  for (const event of traceEvents(trace)) {
    add(event.trace_id || "", event.time);
  }
  for (const span of traceSpans(trace)) {
    add(span.trace_id || "", span.start || span.end);
  }
  const options = Array.from(groups.values()).sort((a, b) => a.startMs - b.startMs || a.id.localeCompare(b.id));
  return options.map((option, index) => ({
    ...option,
    label: `#${index + 1} · ${shortTraceId(option.id)} · ${option.count} ${option.count === 1 ? "event" : "events"}`
  }));
}

function traceSequenceSteps(trace: TraceDetail | null, traceId: string): TraceStep[] {
  const events = traceEvents(trace).filter((event) => (event.trace_id || "") === traceId);
  const spans = traceSpans(trace).filter((span) => (span.trace_id || "") === traceId);
  const steps: TraceStep[] = [];

  for (const span of spans) {
    const endMs = parseTimeMs(span.end);
    const durationMs = Math.max(0, Number(span.duration_ms || 0));
    const startMs = parseTimeMs(span.start) || (endMs > 0 ? endMs - durationMs : 0);
    const phase = phaseName(span.name);
    steps.push({
      id: span.id || `span-${steps.length}`,
      traceId,
      kind: phase ? "phase" : stepKind(span.type, span.name, span.tool_name, span.model),
      title: phase ? phaseLabel(phase) : span.tool_name || span.model || span.name || span.type || "span",
      subtitle: span.error || "",
      status: span.status || "",
      startMs,
      endMs: endMs || startMs + durationMs,
      durationMs,
      selfDurationMs: Number(span.self_duration_ms || 0),
      detail: span.error || "",
      skill: span.skill || "",
      level: Number(span.depth || 0),
      childrenDurationMs: 0,
      parentId: span.parent_id || "",
      childIds: []
    });
  }

  for (const event of events) {
    const timeMs = parseTimeMs(event.time);
    const durationMs = Math.max(0, Number(event.duration_ms || 0));
    const startMs = durationMs > 0 ? timeMs - durationMs : timeMs;
    const phase = phaseName(event.name);
    if (spans.some((span) => span.id && event.id && span.id === event.id)) {
      continue;
    }
    steps.push({
      id: event.id || `event-${steps.length}`,
      traceId,
      kind: phase ? "phase" : stepKind(event.type, event.name, event.tool_name, event.model),
      title: phase ? phaseLabel(phase) : event.tool_name || event.model || event.name || event.type || "event",
      subtitle: event.content || event.output || compactJSON(event.properties),
      status: event.status || event.role || "",
      startMs,
      endMs: startMs + durationMs,
      durationMs,
      selfDurationMs: 0,
      detail: event.content || event.output || compactJSON(event.properties),
      skill: event.skill || stringFromProperties(event.properties, "active_skill") || stringFromProperties(event.properties, "skill"),
      level: phase ? 2 : 0,
      childrenDurationMs: 0,
      parentId: "",
      childIds: []
    });
  }

  const spanSequence = new Map(spans.map((span, index) => [span.id || `span-${index}`, Number(span.sequence || index + 1)]));
  const sorted = steps.sort((a, b) => {
    const aSequence = spanSequence.get(a.id) || 0;
    const bSequence = spanSequence.get(b.id) || 0;
    if (aSequence > 0 && bSequence > 0 && aSequence !== bSequence) {
      return aSequence - bSequence;
    }
    return a.startMs - b.startMs || stepRank(a.kind) - stepRank(b.kind) || a.title.localeCompare(b.title);
  });
  return annotateSequenceHierarchy(sorted);
}

function sequenceBounds(steps: TraceStep[]): { startMs: number; endMs: number } {
  if (steps.length === 0) {
    return { startMs: 0, endMs: 0 };
  }
  const startMs = Math.min(...steps.map((step) => step.startMs || step.endMs));
  const endMs = Math.max(...steps.map((step) => step.endMs || step.startMs));
  return { startMs, endMs: Math.max(endMs, startMs + 1) };
}

function barStyle(step: TraceStep, bounds: { startMs: number; endMs: number }): CSSProperties {
  const total = Math.max(1, bounds.endMs - bounds.startMs);
  const left = Math.max(0, ((step.startMs - bounds.startMs) / total) * 100);
  const width = Math.max(step.durationMs > 0 ? 2 : 1, ((Math.max(1, step.endMs - step.startMs) / total) * 100));
  return {
    marginLeft: `${Math.min(98, left)}%`,
    width: `${Math.min(100 - Math.min(98, left), width)}%`
  };
}

function annotateSequenceHierarchy(steps: TraceStep[]): TraceStep[] {
  const parents = new Map<string, string>();
  for (const step of steps) {
    if (step.parentId) {
      parents.set(step.id, step.parentId);
      continue;
    }
    const parent = parentStepFor(step, steps);
    if (parent) {
      parents.set(step.id, parent.id);
    }
  }
  const childIds = new Map<string, string[]>();
  for (const [childId, parentId] of parents.entries()) {
    childIds.set(parentId, [...(childIds.get(parentId) || []), childId]);
  }
  return steps.map((step) => {
    const children = steps.filter((candidate) => candidate.id !== step.id && candidate.startMs >= step.startMs && candidate.endMs <= step.endMs && candidate.durationMs > 0);
    const childrenDurationMs = coveredDurationMs(children);
    return {
      ...step,
      parentId: parents.get(step.id) || "",
      childIds: childIds.get(step.id) || [],
      level: step.level > 0 ? step.level : stepLevel(step, parents),
      childrenDurationMs: Math.min(step.durationMs, childrenDurationMs),
      selfDurationMs: step.selfDurationMs || Math.max(0, step.durationMs - Math.min(step.durationMs, childrenDurationMs))
    };
  });
}

function parentStepFor(step: TraceStep, steps: TraceStep[]): TraceStep | null {
  const candidates = steps.filter((candidate) => candidate.id !== step.id && candidate.startMs <= step.startMs && candidate.endMs >= step.endMs && candidate.durationMs > 0);
  if (candidates.length === 0) {
    return null;
  }
  candidates.sort((a, b) => (a.endMs - a.startMs) - (b.endMs - b.startMs) || stepRank(a.kind) - stepRank(b.kind));
  return candidates[0];
}

function stepLevel(step: TraceStep, parents: Map<string, string>): number {
  let level = 0;
  let current = parents.get(step.id) || "";
  const seen = new Set<string>();
  while (current && !seen.has(current)) {
    seen.add(current);
    level += 1;
    current = parents.get(current) || "";
  }
  return Math.min(3, level);
}

function visibleTraceSteps(steps: TraceStep[], collapsed: Set<string>): TraceStep[] {
  const hidden = new Set<string>();
  for (const step of steps) {
    if (hidden.has(step.id)) {
      continue;
    }
    if (collapsed.has(step.id)) {
      for (const childId of descendantStepIds(step.id, steps)) {
        hidden.add(childId);
      }
    }
  }
  return steps.filter((step) => !hidden.has(step.id));
}

function descendantStepIds(stepId: string, steps: TraceStep[]): string[] {
  const byParent = new Map<string, string[]>();
  for (const step of steps) {
    if (!step.parentId) {
      continue;
    }
    byParent.set(step.parentId, [...(byParent.get(step.parentId) || []), step.id]);
  }
  const out: string[] = [];
  const queue = [...(byParent.get(stepId) || [])];
  while (queue.length > 0) {
    const id = queue.shift() || "";
    if (!id) {
      continue;
    }
    out.push(id);
    queue.push(...(byParent.get(id) || []));
  }
  return out;
}

type AttributionItem = {
  label: string;
  value: string;
  detail: string;
  title: string;
};

function traceAttribution(steps: TraceStep[], totalDuration: number): { bottleneck: AttributionItem; streamCreate: AttributionItem; firstDelta: AttributionItem; streamRead: AttributionItem } {
  const measured = steps.filter((step) => step.durationMs > 0 && !step.status.toLowerCase().includes("started") && (step.kind === "phase" || step.kind === "llm" || step.kind === "query" || step.kind === "tool"));
  const bottleneckStep = measured.sort((a, b) => b.durationMs - a.durationMs)[0];
  return {
    bottleneck: bottleneckStep ? {
      label: attributionStepLabel(bottleneckStep),
      value: durationLabel(bottleneckStep.durationMs),
      detail: durationPercentLabel(bottleneckStep.durationMs, totalDuration),
      title: bottleneckStep.detail || bottleneckStep.subtitle || bottleneckStep.title
    } : emptyAttribution(),
    streamCreate: phaseAttribution(steps, "stream create", totalDuration),
    firstDelta: phaseAttribution(steps, "stream first delta", totalDuration),
    streamRead: phaseAttribution(steps, "stream read", totalDuration)
  };
}

function traceSlowPath(steps: TraceStep[], totalDuration: number): SlowPath | null {
  const measured = steps.filter((step) => step.durationMs > 0 && !step.status.toLowerCase().includes("started") && (step.kind === "phase" || step.kind === "llm" || step.kind === "query" || step.kind === "tool"));
  const actionable = measured.filter((step) => step.childIds.length === 0 && (step.kind === "phase" || step.kind === "tool"));
  const bottleneck = [...(actionable.length > 0 ? actionable : measured)].sort((a, b) => b.durationMs - a.durationMs)[0];
  if (!bottleneck) {
    return null;
  }
  return {
    bottleneck,
    streamCreate: steps.find((step) => step.title === "stream create"),
    waitFirstByte: steps.find((step) => step.title === "http wait first response byte"),
    dns: steps.find((step) => step.title === "http dns"),
    read: steps.find((step) => step.title === "stream read"),
    totalDuration
  };
}

function traceStepVisualClass(step: TraceStep, bottleneckId: string): string {
  const classes: string[] = [];
  if (step.id === bottleneckId) {
    classes.push("hot");
  }
  if (step.title === "stream create") {
    classes.push("stream-root");
  }
  if (step.title.startsWith("http ")) {
    classes.push("http-phase");
  }
  if (step.title === "http wait first response byte") {
    classes.push("remote-wait");
  }
  return classes.join(" ");
}

function slowPathMessage(slowPath: SlowPath, t: (key: string, params?: Record<string, string | number>) => string): string {
  const waitMs = slowPath.waitFirstByte?.durationMs || 0;
  const dnsMs = slowPath.dns?.durationMs || 0;
  const readMs = slowPath.read?.durationMs || 0;
  if (waitMs >= Math.max(dnsMs, readMs) && waitMs > 0) {
    return t("trace.slowPathRemote", { duration: durationLabel(waitMs) });
  }
  if (dnsMs >= Math.max(waitMs, readMs) && dnsMs > 0) {
    return t("trace.slowPathDNS", { duration: durationLabel(dnsMs) });
  }
  return t("trace.slowPathGeneric", { duration: durationLabel(slowPath.bottleneck.durationMs), phase: slowPath.bottleneck.title });
}

function attributionStepLabel(step: TraceStep): string {
  if (step.kind === "phase") {
    return step.title;
  }
  if (step.kind === "llm") {
    return "model request";
  }
  return step.title || step.kind;
}

function phaseAttribution(steps: TraceStep[], title: string, totalDuration: number): AttributionItem {
  const step = steps.find((item) => item.title === title);
  if (!step) {
    return emptyAttribution();
  }
  return {
    label: title,
    value: durationLabel(step.durationMs) || "0ms",
    detail: durationPercentLabel(step.durationMs, totalDuration),
    title
  };
}

function emptyAttribution(): AttributionItem {
  return { label: "-", value: "-", detail: "-", title: "" };
}

function durationPercentLabel(value: number, total: number): string {
  if (total <= 0 || value <= 0) {
    return "0%";
  }
  return `${Math.round((value / total) * 100)}%`;
}

function coveredDurationMs(steps: TraceStep[]): number {
  const intervals = steps
    .filter((step) => step.endMs > step.startMs)
    .map((step) => ({ start: step.startMs, end: step.endMs }))
    .sort((a, b) => a.start - b.start || a.end - b.end);
  let total = 0;
  let currentStart = 0;
  let currentEnd = 0;
  for (const interval of intervals) {
    if (currentEnd <= currentStart) {
      currentStart = interval.start;
      currentEnd = interval.end;
      continue;
    }
    if (interval.start > currentEnd) {
      total += currentEnd - currentStart;
      currentStart = interval.start;
      currentEnd = interval.end;
      continue;
    }
    currentEnd = Math.max(currentEnd, interval.end);
  }
  if (currentEnd > currentStart) {
    total += currentEnd - currentStart;
  }
  return total;
}

function phaseName(name?: string): string {
  const raw = name || "";
  if (raw.startsWith("mobile.phase.")) {
    return raw.replace(/^mobile\.phase\./, "").replace(/\.finished$/, "");
  }
  if (raw.startsWith("model.phase.")) {
    return raw.replace(/^model\.phase\./, "").replace(/\.finished$/, "");
  }
  if (!raw.endsWith(".finished")) {
    return "";
  }
  return "";
}

function phaseLabel(phase: string): string {
  return phase.replaceAll("_", " ").replaceAll(".", " ");
}

function phaseHelp(title: string, t: (key: string, params?: Record<string, string | number>) => string): string {
  switch (title) {
    case "stream create":
      return t("trace.streamCreateHelp");
    case "http get conn":
      return t("trace.httpGetConnHelp");
    case "http dns":
      return t("trace.httpDNSHelp");
    case "http connect":
      return t("trace.httpConnectHelp");
    case "http tls":
      return t("trace.httpTLSHelp");
    case "http request send":
      return t("trace.httpRequestSendHelp");
    case "http write request":
      return t("trace.httpWriteRequestHelp");
    case "http wait first response byte":
      return t("trace.httpWaitFirstByteHelp");
    case "http stream ready":
      return t("trace.httpStreamReadyHelp");
    case "http round trip":
      return t("trace.httpRoundTripHelp");
    case "stream first event":
      return t("trace.firstEventHelp");
    case "stream first delta":
      return t("trace.firstDeltaHelp");
    case "stream read":
      return t("trace.streamReadHelp");
    default:
      return title;
  }
}

function stepKind(type?: string, name?: string, toolName?: string, model?: string): string {
  const raw = `${type || ""}.${name || ""}`.toLowerCase();
  if (raw.includes("mobile.phase") || raw.includes("model.phase")) {
    return "phase";
  }
  if (model || raw.includes("model")) {
    return "llm";
  }
  if (toolName || raw.includes("tool")) {
    return "tool";
  }
  if (raw.includes("skill")) {
    return "skill";
  }
  if (raw.includes("mobile")) {
    return "mobile";
  }
  if (raw.includes("query")) {
    return "query";
  }
  if (raw.includes("api")) {
    return "api";
  }
  if (raw.includes("message")) {
    return "message";
  }
  return type || "event";
}

function stepKindLabel(kind: string, t: (key: string, params?: Record<string, string | number>) => string): string {
  const key = `trace.kind.${kind}`;
  const label = t(key);
  return label === key ? kind : label;
}

function statusClass(status: string): string {
  const normalized = status.toLowerCase();
  if (normalized.includes("ok") || normalized.includes("success") || normalized.includes("finished") || normalized.includes("complete")) {
    return "ok";
  }
  if (normalized.includes("fail") || normalized.includes("error") || normalized.includes("deny")) {
    return "fail";
  }
  if (normalized.includes("cancel") || normalized.includes("timeout") || normalized.includes("warn")) {
    return "warn";
  }
  return "neutral";
}

function categoryClass(value: string): string {
  const normalized = value.toLowerCase();
  if (normalized.includes("model")) {
    return "llm";
  }
  if (normalized.includes("tool")) {
    return "tool";
  }
  if (normalized.includes("audit") || normalized.includes("permission")) {
    return "audit";
  }
  if (normalized.includes("mobile")) {
    return "mobile";
  }
  if (normalized.includes("query")) {
    return "query";
  }
  return "event";
}

function tokenLine(item: TelemetryRecord): string {
  const input = Number(item.input_tokens || 0);
  const output = Number(item.output_tokens || 0);
  const cacheRead = Number(item.cache_read_input_tokens || 0);
  const cacheWrite = Number(item.cache_creation_input_tokens || 0);
  const parts = [];
  if (input || output) {
    parts.push(`${input} in / ${output} out`);
  }
  if (cacheRead || cacheWrite) {
    parts.push(`cache ${cacheRead} read / ${cacheWrite} write`);
  }
  return parts.join(" · ");
}

function telemetryDetail(item: TelemetryRecord): string {
  if (item.error) {
    return item.error;
  }
  if (item.properties_json) {
    return item.properties_json;
  }
  if (item.properties) {
    return compactJSON(item.properties);
  }
  return [item.resource_type, item.resource_id, item.source].filter(Boolean).join(" · ");
}

export function summarizeSkillTelemetry(telemetry: TelemetryRecord[]): SkillTelemetrySummary[] {
  const groups = new Map<string, SkillTelemetrySummary>();
  for (const item of telemetry) {
    const props = telemetryProperties(item);
    const skill = telemetryPropString(props, "active_skill") || telemetryPropString(props, "skill") || stringValue((item as { skill?: unknown }).skill);
    if (!skill) {
      continue;
    }
    const source = telemetryPropString(props, "active_skill_source") || telemetryPropString(props, "skill_source") || telemetryPropString(props, "source") || item.source || "unknown";
    const version = telemetryPropString(props, "active_skill_version") || telemetryPropString(props, "skill_version") || telemetryPropString(props, "version");
    const fallback = telemetryPropString(props, "active_skill_fallback") || telemetryPropString(props, "skill_fallback") || telemetryPropString(props, "fallback");
    const key = [skill, source, version, fallback].join("|");
    const current = groups.get(key) || {
      key,
      skill,
      source,
      version,
      fallback,
      count: 0,
      errors: 0,
      durationMs: 0,
      tokens: 0,
      latestAt: 0
    };
    current.count += 1;
    current.errors += statusClass(item.status || "") === "fail" ? 1 : 0;
    current.durationMs += Number(item.duration_ms || 0);
    current.tokens += Number(item.input_tokens || 0) + Number(item.output_tokens || 0);
    current.latestAt = Math.max(current.latestAt, parseTimeMs(item.occurred_at || item.created_at));
    groups.set(key, current);
  }
  return Array.from(groups.values()).sort((a, b) => b.latestAt - a.latestAt || b.count - a.count || a.skill.localeCompare(b.skill));
}

function telemetryProperties(item: TelemetryRecord): Record<string, unknown> {
  if (item.properties && typeof item.properties === "object") {
    return item.properties as Record<string, unknown>;
  }
  if (!item.properties_json) {
    return {};
  }
  try {
    const parsed = JSON.parse(item.properties_json) as unknown;
    return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function telemetryPropString(props: Record<string, unknown>, key: string): string {
  const value = props[key];
  if (typeof value === "boolean") {
    return value ? "true" : "false";
  }
  if (typeof value === "number" && Number.isFinite(value)) {
    return String(value);
  }
  return stringValue(value);
}

function relativeTimeLabel(value?: string): string {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function stepRank(kind: string): number {
  const order = ["api", "mobile", "phase", "query", "llm", "skill", "tool", "message"];
  const index = order.indexOf(kind);
  return index === -1 ? order.length : index;
}

function parseTimeMs(value?: string): number {
  if (!value) {
    return 0;
  }
  const time = Date.parse(value);
  return Number.isFinite(time) ? time : 0;
}

function shortTraceId(value: string): string {
  if (value.length <= 18) {
    return value;
  }
  return `${value.slice(0, 10)}...${value.slice(-6)}`;
}

function stringFromProperties(props: Record<string, unknown> | undefined, key: string): string {
  const value = props?.[key];
  return typeof value === "string" ? value : "";
}

function numberFromProperties(props: Record<string, unknown> | undefined, key: string): number {
  const value = props?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function durationLabel(value?: number): string {
  return value ? `${value}ms` : "";
}

function compactJSON(value: unknown): string {
  if (!value || typeof value !== "object") {
    return "";
  }
  try {
    return JSON.stringify(value).slice(0, 240);
  } catch {
    return "";
  }
}
