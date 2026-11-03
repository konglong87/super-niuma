/** Platform-neutral Computer Use DTOs. Keep these aligned with internal/computeruse JSON tags. */
export type ComputerReadiness = "unknown" | "unavailable" | "permission_required" | "ready" | "failed";
export type ComputerPermissionTarget = "accessibility" | "screen_capture";
export type ComputerPermissionState = "unknown" | "required" | "approved" | "denied";
export type ComputerFocusState = "unknown" | "focused" | "changed" | "unavailable";
export type ComputerSessionState = "pending_approval" | "ready" | "paused" | "needs_observation" | "stopped" | "failed";
export type ComputerOutcome = "not_started" | "executed" | "rejected" | "failed" | "unknown";
export type ComputerVerification = "not_checked" | "passed" | "failed" | "unknown";

export type ComputerCoordinateSpace = {
  display_id?: string;
  origin: "top_left";
  unit: "pixels";
  width: number;
  height: number;
  scale_factor: number;
  bounds?: ComputerWindowFrame;
};

export type ComputerWindowFrame = { x: number; y: number; width: number; height: number };
export type ComputerWindowRef = { id?: string; title?: string; owner_pid?: number; bundle_id?: string; frame?: ComputerWindowFrame; is_visible?: boolean; is_frontmost?: boolean };
export type ComputerPoint = { x: number; y: number };
export type ComputerMediaRef = {
  id?: string;
  url?: string;
  media_type?: string;
  sha256?: string;
  width?: number;
  height?: number;
  size_bytes?: number;
};

export type ComputerCapabilities = {
  protocol_version: string;
  platform: string;
  backend: string;
  capture_readiness: ComputerReadiness;
  input_readiness: ComputerReadiness;
  focus_state: ComputerFocusState;
  permission_state: ComputerPermissionState;
  coordinate_space: ComputerCoordinateSpace;
  displays?: ComputerCoordinateSpace[];
  target_window?: ComputerWindowRef;
  actions?: string[];
  image_supported: boolean;
  supports_pause: boolean;
  supports_stop: boolean;
};

export type ComputerObservation = {
  id: string;
  session_id: string;
  display_id?: string;
  window_id?: string;
  width: number;
  height: number;
  scale_factor: number;
  screenshot: ComputerMediaRef;
  active_window?: ComputerWindowRef;
  cursor: ComputerPoint;
  capabilities: ComputerCapabilities;
  observed_at: string;
  expires_at?: string;
  image_data?: string;
  media_type?: string;
};

export type ComputerActionReceipt = {
  action_id: string;
  session_id: string;
  platform: string;
  backend: string;
  before_observation_id?: string;
  after_observation_id?: string;
  outcome: ComputerOutcome;
  verification: ComputerVerification;
  focus_before: ComputerFocusState;
  focus_after: ComputerFocusState;
  redacted_action_summary: string;
  error_code?: string;
  error_message?: string;
  duration?: number;
  completed_at: string;
  before?: ComputerMediaRef;
  after?: ComputerMediaRef;
  actual_point?: ComputerPoint;
  active_window_after?: ComputerWindowRef;
};

export type ComputerObservationResponse = {
  observation: ComputerObservation;
  image_data?: string;
  media_type?: string;
};

export type ComputerCapabilitiesResponse = {
  capabilities: ComputerCapabilities;
  available: boolean;
  error_code?: string;
  error_message?: string;
};

export type ComputerSessionOwner = { tenant_id: number; user_id: number; session_id: number };

export type ComputerRuntimeState = "starting" | "ready" | "degraded" | "stopping" | "stopped" | "faulted";
export type ComputerRuntimeFault = {
  state: ComputerRuntimeState;
  error_code: string;
  recoverable: boolean;
  session_id?: string;
  action_id?: string;
};
export type ComputerRuntimeStatus = {
  state: ComputerRuntimeState;
  fault?: ComputerRuntimeFault;
  active_session: boolean;
  session_id?: string;
  available: boolean;
  error_code?: string;
};

export type ComputerSessionSnapshot = {
  session_id: string;
  state: ComputerSessionState;
  owner: ComputerSessionOwner;
  owner_kind: "local_preview" | "managed_conversation";
  capabilities: ComputerCapabilities;
  observation?: ComputerObservation;
  last_receipt?: ComputerActionReceipt;
};

export type StartComputerSessionInput = {
  approved: boolean;
  conversation_ref?: string;
};
