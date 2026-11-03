import type { ComputerActionReceipt, ComputerCapabilitiesResponse, ComputerObservationResponse, ComputerPermissionTarget, ComputerRuntimeStatus, ComputerSessionSnapshot, StartComputerSessionInput } from "./computer/types";

export type WorkOrderInput = { title: string; objective: string; acceptance_criteria: string[]; workspace?: string };
export type WorkOrder = WorkOrderInput & {
  id: string;
  status: "recorded" | "cancelled";
  execution_status: "not_started";
  verification_status: "not_started";
  delivery_status: "not_started";
  created_at: string;
  updated_at: string;
};
export type SuperWorkOrderBridge = {
  CreateSuperWorkOrder: (key: string, input: WorkOrderInput) => Promise<WorkOrder>;
  ListSuperWorkOrders: () => Promise<WorkOrder[]>;
  GetSuperWorkOrder: (id: string) => Promise<WorkOrder>;
  CancelSuperWorkOrder: (id: string) => Promise<WorkOrder>;
};

// Super control-plane methods are optional: older desktops retain intake-only UI.
// These wire types mirror internal/super/control and verification; claim tokens
// and model text are never rendered by the Super control panel.
export type SuperNode = {
  id: string; parent_id?: string; instruction: string; depends_on?: string[];
  read_paths?: string[]; write_paths?: string[]; max_turns: number; max_output_tokens: number;
};
export type SuperTarget = {
  id: string; provider: string; account?: string; chat?: string; thread?: string;
  recipient?: string; reply_to?: string;
};
export type SuperVerificationPolicy = { version: string; rules: {
  path: string; kind: string; expected?: string; keys?: string[]; max_bytes?: number;
}[] };
export type SuperGrant = {
  profile_id: string; revision: string; workspace: string; provider: string; model: string;
  system_prompt: string; max_tokens: number; timeout_ms: number; expires_at_ms: number;
  nodes: SuperNode[]; verification: SuperVerificationPolicy; targets: SuperTarget[]; auto_approve: boolean;
};
export type SuperClaim = {
  scope: string; run_id: string; job_id: string; owner: string; token: string; epoch: number; until_ms: number;
};
export type SuperUsage = { input_tokens: number; output_tokens: number };
export type SuperJob = {
  spec: SuperNode; status: string; claim: SuperClaim; attempts: number; started_at_ms?: number;
  finished_at_ms?: number; safe_code?: string;
  result: { text: string; usage: SuperUsage; usage_known: boolean; tool_calls: number;
    writes?: { path: string; sha256: string; bytes: number; attempt_id: string }[] };
};
export type SuperReservation = {
  id: string; job_id: string; attempt_id: string; input: number; output: number;
  actual: SuperUsage; settled: boolean;
};
export type SuperVerificationReport = {
  passed: boolean; policy_hash: string; verified_at_ms: number;
  artifacts: { path: string; sha256: string; bytes: number }[];
  checks: { path: string; kind: string; passed: boolean; code: string }[];
};
export type SuperNotice = {
  id: string; target: SuperTarget; status: string; attempts: number; claim: SuperClaim;
  safe_code?: string; retry_at_ms?: number;
  receipt?: { message_id: string; request_id?: string; chat?: string; delivered_at_ms: number };
};
export type ControlRun = {
  id: string; scope: string; owner: string; intake_id?: string; request_key: string; objective: string;
  grant: SuperGrant; grant_hash: string; version: number; status: string; jobs: SuperJob[];
  reservations: SuperReservation[]; verification_attempt_id?: string; verification_claim: SuperClaim;
  verification?: SuperVerificationReport | null; notices: SuperNotice[]; safe_code?: string;
  approved: boolean; created_at_ms: number; updated_at_ms: number;
};
export type SuperProfile = { id: string; name: string; enabled: boolean; workspace: string; model: string };
// Deliberately project only DLQ metadata used by the UI; payloads/tokens are not display fields.
export type SuperDeadLetter = { code: string; attempts: number; event: { id: string; topic: string }; subscriber?: string };
export type SuperPlatformSnapshot = {
  configured: boolean; running: boolean; workers: number; profiles: SuperProfile[]; runs: ControlRun[];
  config_json: string; capabilities: Record<string, unknown>; safe_code?: string;
  webhook_address?: string; channel_code?: string; dead_letters?: SuperDeadLetter[]; has_more_runs?: boolean;
};
export type SuperEvent = {
  sequence: number; id: string; scope: string; topic: string; aggregate_id: string;
  version: number; payload: unknown; occurred_at_ms: number;
};
export type SuperPlatformBridge = {
  GetSuperPlatform: () => Promise<SuperPlatformSnapshot>;
  SaveSuperPlatformConfig: (configJSON: string) => Promise<SuperPlatformSnapshot>;
  StartSuperRun: (intakeID: string, profileID: string, key: string) => Promise<ControlRun>;
  ApproveSuperRun: (id: string, version: number, grantHash: string) => Promise<ControlRun>;
  CancelSuperRun: (id: string, version: number) => Promise<ControlRun>;
  RetrySuperRun: (id: string, version: number) => Promise<ControlRun>;
  AbandonSuperRun?: (id: string, version: number, confirmation: string) => Promise<ControlRun>;
  GetSuperEvents: (after: number) => Promise<SuperEvent[]>;
};

export type DesktopServiceStatus = {
  state: "stopped" | "starting" | "ready" | "failed";
  pid?: number;
  port: number;
  error?: string;
};

export type DesktopBackgroundMode = "external" | "local";

export type ComputerOverlaySnapshot = {
  visible: boolean;
  expanded: boolean;
  display_mode?: "auto" | "compact" | "expanded";
  session_id: string;
  title: string;
  detail: string;
  image_data: string;
  can_stop: boolean;
  can_pause: boolean;
  can_resume: boolean;
  language: "zh" | "en";
};

export type DesktopBackgroundImage = {
  mode: DesktopBackgroundMode;
  enabled: boolean;
  name?: string;
  mime_type?: string;
  data_url?: string;
};

export type DesktopServiceBridge = Partial<SuperWorkOrderBridge> & Partial<SuperPlatformBridge> & {
  RestartLocalService: () => Promise<void>;
  GetLocalServiceStatus?: () => Promise<DesktopServiceStatus>;
  SelectWorkspace?: () => Promise<string>;
  GetSessionBackend?: () => Promise<"jsonl" | "sqlite">;
  SetSessionBackend?: (backend: "jsonl" | "sqlite") => Promise<void>;
  GetBackgroundImage?: () => Promise<DesktopBackgroundImage>;
  SaveBackgroundImage?: (dataURL: string, name: string) => Promise<DesktopBackgroundImage>;
  SetBackgroundMode?: (mode: DesktopBackgroundMode) => Promise<DesktopBackgroundImage>;
  ClearBackgroundImage?: () => Promise<void>;
  GetComputerCapabilities?: () => Promise<ComputerCapabilitiesResponse>;
  GetComputerRuntimeStatus?: () => Promise<ComputerRuntimeStatus>;
  StopComputerRuntime?: () => Promise<ComputerRuntimeStatus>;
  OpenComputerPermissionSettings?: (target: ComputerPermissionTarget) => Promise<void>;
  StartComputerSession?: (input: StartComputerSessionInput) => Promise<ComputerSessionSnapshot>;
  GetComputerSession?: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  GetActiveComputerSession?: () => Promise<ComputerSessionSnapshot>;
  ObserveComputerSession?: (sessionID: string) => Promise<ComputerObservationResponse>;
  PauseComputerSession?: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  ResumeComputerSession?: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  StopComputerSession?: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  GetComputerActionReceipt?: (sessionID: string, actionID: string) => Promise<ComputerActionReceipt>;
  IsComputerOverlayAvailable?: () => Promise<boolean> | boolean;
  ShowComputerOverlay?: () => Promise<void> | void;
  UpdateComputerOverlay?: (snapshot: ComputerOverlaySnapshot) => Promise<void> | void;
};

declare global {
  interface Window {
    go?: {
      main?: {
        app?: DesktopServiceBridge;
        App?: DesktopServiceBridge;
      };
    };
  }
}

export function getDesktopServiceBridge(): DesktopServiceBridge | null {
  if (typeof window === "undefined") {
    return null;
  }
  const app = window.go?.main?.app ?? window.go?.main?.App;
  return app?.RestartLocalService ? app : null;
}

// Computer Use methods are optional until the desktop backend is available.
// The Computer Workspace checks the complete capability set before rendering.
