import type { ControlOption } from "../../components/AgentControls";

export type ComposerRuntimeValue = {
  provider: string;
  model: string;
  permissionMode: string;
  effort: string;
  promptMode: string;
};

// The workspace owns configuration and facts; the composer only edits the next run.
export type ComposerRuntimeControls = {
  value: ComposerRuntimeValue;
  providerOptions: ControlOption[];
  modelOptions: ControlOption[];
  locked: boolean;
  onChange: (value: ComposerRuntimeValue) => void;
  contextPercent: number | null;
  cacheHitPercent: number | null;
};
