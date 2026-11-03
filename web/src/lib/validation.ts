import type {
  DocumentRecord,
  MemoryRecord,
  ProfileRecord,
  SkillOverrideRecord,
  SkillRecord,
  TelemetryRecord,
  TraceDetail
} from "./types";

export type ValidationStatus = "pass" | "warn" | "fail";

export type ValidationCheck = {
  key: string;
  label: string;
  status: ValidationStatus;
  detail: string;
};

export type ValidationSnapshot = {
  selectedSessionId: number | null;
  messageCount: number;
  memories: MemoryRecord[];
  skills: SkillRecord[];
  tenantSkills: SkillRecord[];
  skillOverrides: SkillOverrideRecord[];
  documents: DocumentRecord[];
  telemetry: TelemetryRecord[];
  profile: ProfileRecord | null;
  trace: TraceDetail | null;
};

export function buildValidationChecks(snapshot: ValidationSnapshot): ValidationCheck[] {
  const assistantMessages = snapshot.messageCount > 1;
  const traceSummary = snapshot.trace?.summary;
  const telemetryNames = snapshot.telemetry.map((item) => item.name || "");
  const hasMobileTelemetry = telemetryNames.some((name) => name.startsWith("mobile.chat."));
  const hasModelTelemetry = telemetryNames.some((name) => name.startsWith("model.") || name.startsWith("query."));
  const hasPromptCache = Boolean(traceSummary?.cache_summary || traceSummary?.cache_read_tokens || traceSummary?.cache_creation_tokens);
  const hasTokens = Boolean(traceSummary?.total_tokens || traceSummary?.input_tokens || traceSummary?.output_tokens);
  const hasSkills = snapshot.skills.length > 0 || (traceSummary?.skills?.length || 0) > 0;

  return [
    {
      key: "session",
      label: "Mobile session",
      status: snapshot.selectedSessionId ? "pass" : "fail",
      detail: snapshot.selectedSessionId ? `selected #${snapshot.selectedSessionId}` : "create or select a session"
    },
    {
      key: "messages",
      label: "Conversation cycle",
      status: assistantMessages ? "pass" : snapshot.messageCount > 0 ? "warn" : "fail",
      detail: `${snapshot.messageCount} persisted messages`
    },
    {
      key: "memory",
      label: "Long-term memory",
      status: snapshot.memories.length > 0 ? "pass" : "warn",
      detail: `${snapshot.memories.length} records`
    },
    {
      key: "profile",
      label: "User profile",
      status: snapshot.profile ? "pass" : "warn",
      detail: snapshot.profile ? `version ${snapshot.profile.profile_version || "latest"}` : "not saved yet"
    },
    {
      key: "documents",
      label: "Short-term documents",
      status: snapshot.documents.length > 0 ? "pass" : "warn",
      detail: `${snapshot.documents.length} active/history records`
    },
    {
      key: "skills",
      label: "Skills loading",
      status: hasSkills ? "pass" : "warn",
      detail: `${snapshot.skills.length} effective, ${snapshot.tenantSkills.length} tenant, ${snapshot.skillOverrides.length} overrides`
    },
    {
      key: "telemetry",
      label: "Telemetry lifecycle",
      status: hasMobileTelemetry && hasModelTelemetry ? "pass" : snapshot.telemetry.length > 0 ? "warn" : "fail",
      detail: `${snapshot.telemetry.length} recent events`
    },
    {
      key: "trace",
      label: "Trace summary",
      status: traceSummary ? "pass" : snapshot.selectedSessionId ? "warn" : "fail",
      detail: traceSummary ? `${traceSummary.messages || 0} messages, ${traceSummary.tool_calls || 0} tools` : "no trace summary"
    },
    {
      key: "tokens",
      label: "Token usage",
      status: hasTokens ? "pass" : traceSummary ? "warn" : "fail",
      detail: hasTokens ? traceSummary?.token_summary || `${traceSummary?.total_tokens || 0} tokens` : "not reported"
    },
    {
      key: "prompt-cache",
      label: "Prompt cache",
      status: hasPromptCache ? "pass" : traceSummary ? "warn" : "fail",
      detail: traceSummary?.cache_summary || "not reported"
    }
  ];
}

export function buildCapabilityChecks(snapshot: ValidationSnapshot): ValidationCheck[] {
  const traceEvents = traceEventRecords(snapshot.trace);
  const telemetryProps = snapshot.telemetry.map((item) => parseProperties(item.properties_json));
  const telemetryNames = snapshot.telemetry.map((item) => item.name || "");
  const traceSummary = snapshot.trace?.summary;

  const compactEvidence =
    traceEvents.some((event) => stringValue(event.type) === "compact_summary" || stringValue(event.name).includes("compact")) ||
    telemetryNames.some((name) => name.includes("autocompact") || name.includes("compact"));
  const skillEvidence =
    (traceSummary?.skills?.length || 0) > 0 ||
    traceEvents.some((event) => stringValue(event.skill) || stringValue(recordValue(event.properties, "active_skill"))) ||
    telemetryProps.some((props) => stringValue(props.active_skill) || stringValue(props.skill));
  const promptCacheEvidence = Boolean(traceSummary?.cache_summary || traceSummary?.cache_read_tokens || traceSummary?.cache_creation_tokens);
  const memoryEvidence = snapshot.memories.length > 0;
  const profileEvidence = Boolean(snapshot.profile);
  const shortTermEvidence = snapshot.documents.length > 0;
  const mobileStreamEvidence = telemetryNames.some((name) => name === "mobile.chat.stream.finished") || traceEvents.some((event) => stringValue(event.name) === "mobile.chat.stream.finished");

  return [
    {
      key: "mobile-stream",
      label: "Mobile stream",
      status: mobileStreamEvidence ? "pass" : snapshot.selectedSessionId ? "warn" : "fail",
      detail: mobileStreamEvidence ? "mobile.chat stream event found" : "send a message through Mobile Chat"
    },
    {
      key: "auto-compact",
      label: "Auto compression",
      status: compactEvidence ? "pass" : "warn",
      detail: compactEvidence ? "compact summary or telemetry found" : "not triggered in this session"
    },
    {
      key: "skills-progressive",
      label: "Skills progressive loading",
      status: skillEvidence ? "pass" : snapshot.skills.length > 0 ? "warn" : "fail",
      detail: skillEvidence ? skillEvidenceDetail(snapshot, telemetryProps) : "no active skill evidence"
    },
    {
      key: "long-memory",
      label: "Long-term memory",
      status: memoryEvidence ? "pass" : "warn",
      detail: memoryEvidence ? `${snapshot.memories.length} persisted memories` : "no memory records"
    },
    {
      key: "short-memory",
      label: "Short-term memory",
      status: shortTermEvidence ? "pass" : "warn",
      detail: shortTermEvidence ? `${snapshot.documents.length} document records` : "no CLAUDE.md/user document records"
    },
    {
      key: "profile",
      label: "User profile",
      status: profileEvidence ? "pass" : "warn",
      detail: profileEvidence ? snapshot.profile?.summary || `version ${snapshot.profile?.profile_version || "latest"}` : "no profile record"
    },
    {
      key: "prompt-cache",
      label: "Prompt cache",
      status: promptCacheEvidence ? "pass" : snapshot.trace?.summary ? "warn" : "fail",
      detail: traceSummary?.cache_summary || "no cache evidence"
    }
  ];
}

export function validationScore(checks: ValidationCheck[]): { passed: number; total: number; failing: number; warning: number } {
  return {
    passed: checks.filter((item) => item.status === "pass").length,
    failing: checks.filter((item) => item.status === "fail").length,
    warning: checks.filter((item) => item.status === "warn").length,
    total: checks.length
  };
}

function traceEventRecords(trace: TraceDetail | null): Array<Record<string, unknown>> {
  if (!Array.isArray(trace?.events)) {
    return [];
  }
  return trace.events.filter((event): event is Record<string, unknown> => Boolean(event && typeof event === "object"));
}

function parseProperties(value?: string): Record<string, unknown> {
  if (!value) {
    return {};
  }
  try {
    const parsed = JSON.parse(value) as unknown;
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

function recordValue(value: unknown, key: string): unknown {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>)[key] : undefined;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function skillEvidenceDetail(snapshot: ValidationSnapshot, telemetryProps: Array<Record<string, unknown>>): string {
  const names = [
    ...(snapshot.trace?.summary?.skills || []),
    ...telemetryProps.map((props) => stringValue(props.active_skill) || stringValue(props.skill)).filter(Boolean)
  ];
  return names.length > 0 ? Array.from(new Set(names)).join(", ") : `${snapshot.skills.length} effective skills`;
}
