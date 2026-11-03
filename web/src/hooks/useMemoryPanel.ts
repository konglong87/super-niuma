import { useState } from "react";
import type { MemoryRecord, TenantMessage } from "../lib/types";

// Memory（记忆编辑/审核/团队与托管记忆）面板状态域（原样迁自 InspectorPanels）。
export function useMemoryPanel() {
  const [memories, setMemories] = useState<MemoryRecord[]>([]);
  const [teamMemories, setTeamMemories] = useState<MemoryRecord[]>([]);
  const [managedMemories, setManagedMemories] = useState<MemoryRecord[]>([]);
  const [memoryReviewCandidates, setMemoryReviewCandidates] = useState<MemoryRecord[]>([]);
  const [memoryReviewTypeFilter, setMemoryReviewTypeFilter] = useState("all");
  const [memoryReviewRiskFilter, setMemoryReviewRiskFilter] = useState("all");
  const [memoryReviewSessionFilter, setMemoryReviewSessionFilter] = useState("all");
  const [selectedMemoryReviewKey, setSelectedMemoryReviewKey] = useState("");
  const [memoryReviewSourceMessages, setMemoryReviewSourceMessages] = useState<Record<number, TenantMessage[]>>({});
  const [memoryReviewSourceLoading, setMemoryReviewSourceLoading] = useState<number | null>(null);
  const [memoryKey, setMemoryKey] = useState("web.preference");
  const [memoryContent, setMemoryContent] = useState("");
  const [scopedMemoryScope, setScopedMemoryScope] = useState<"team" | "managed">("team");
  const [scopedMemoryKey, setScopedMemoryKey] = useState("webui.scoped.rule");
  const [scopedMemoryContent, setScopedMemoryContent] = useState("");

  return {
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
  };

}
