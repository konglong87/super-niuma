import { useState } from "react";
import type { RuntimeBackgroundJob, RuntimeBackgroundLogs, RuntimeRunRecord } from "../lib/types";

// Loop（后台循环任务）面板状态域（原样迁自 InspectorPanels）。
export function useLoopPanel() {
  const [runtimeLoops, setRuntimeLoops] = useState<RuntimeBackgroundJob[]>([]);
  const [selectedLoopId, setSelectedLoopId] = useState("");
  const [selectedLoopLogs, setSelectedLoopLogs] = useState<RuntimeBackgroundLogs | null>(null);
  const [selectedLoopRuns, setSelectedLoopRuns] = useState<RuntimeRunRecord[]>([]);
  const [loopLogsLoading, setLoopLogsLoading] = useState(false);
  const [loopPrompt, setLoopPrompt] = useState("drink water");
  const [loopCwd, setLoopCwd] = useState("");
  const [loopInterval, setLoopInterval] = useState("10m");
  const [loopDraftNew, setLoopDraftNew] = useState(false);

  return {
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
  };

}
