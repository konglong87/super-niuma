import { useState } from "react";
import type { TraceDetail, TraceSessionSummary } from "../lib/types";
import type { LocalTraceTimeFilter } from "../components/InspectorPanels";

// Trace 面板（远端 trace + 本地 trace 会话）状态域（原样迁自 InspectorPanels）。
export function useTracePanel() {
  const [traceView, setTraceView] = useState<"overview" | "sequence">("overview");
  const [selectedTraceId, setSelectedTraceId] = useState("");
  const [localTraceView, setLocalTraceView] = useState<"overview" | "sequence">("overview");
  const [selectedLocalTraceId, setSelectedLocalTraceId] = useState("");
  const [localTraceSearch, setLocalTraceSearch] = useState("");
  const [localTraceTimeFilter, setLocalTraceTimeFilter] = useState<LocalTraceTimeFilter>("all");
  const [localTraceProjectOnly, setLocalTraceProjectOnly] = useState(true);
  const [localTraceIncludeTests, setLocalTraceIncludeTests] = useState(false);
  const [trace, setTrace] = useState<TraceDetail | null>(null);
  const [localTraceSessions, setLocalTraceSessions] = useState<TraceSessionSummary[]>([]);
  const [localTraceWorkspace, setLocalTraceWorkspace] = useState("");
  const [selectedLocalTraceSessionId, setSelectedLocalTraceSessionId] = useState("");
  const [localTrace, setLocalTrace] = useState<TraceDetail | null>(null);
  const [localTraceReloadTick, setLocalTraceReloadTick] = useState(0);

  return {
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
  };

}
