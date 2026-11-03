import { useState } from "react";
import { AgentProfilesPanel } from "../../components/AgentProfilesPanel";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import "./profile-settings.css";

export type ProfileSettingsPanelProps = {
  identity: IdentityConfig;
  onDirtyChange?: (dirty: boolean) => void;
  onBusyChange?: (busy: boolean) => void;
  isolatedEnvironment?: boolean;
  onOpenSession?: (sessionId: number) => void;
  onDataChanged?: () => void;
};

export function ProfileSettingsPanel({ identity, onDirtyChange, onBusyChange, isolatedEnvironment, onOpenSession, onDataChanged }: ProfileSettingsPanelProps) {
  const [status, setStatus] = useState("");
  const { language } = useI18n();
  const zh = language === "zh";
  return <div className="webui2-profile-settings">
    <div className="settings-concept-notice" role="note">
      <strong>{zh ? "智能体定义" : "Agent definition"}</strong>
      <span>{zh ? "这里决定智能体是谁、能做什么以及如何运行。保存后发布，入口分配再决定哪些入口使用它。" : "Define who the agent is, what it can do and how it runs. Publish it here; entry assignments decide where it is used."}</span>
    </div>
    {status ? <p className="settings-profile-status" role="status">{status}</p> : null}
    <AgentProfilesPanel identity={identity} embedded onStatus={setStatus} onDirtyChange={onDirtyChange} onBusyChange={onBusyChange} isolatedEnvironment={isolatedEnvironment} onOpenSession={onOpenSession} onDataChanged={onDataChanged} />
  </div>;
}
