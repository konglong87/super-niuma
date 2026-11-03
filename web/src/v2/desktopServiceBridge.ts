import type { ComputerActionReceipt, ComputerCapabilitiesResponse, ComputerObservationResponse, ComputerPermissionTarget, ComputerRuntimeStatus, ComputerSessionSnapshot, StartComputerSessionInput } from "./computer/types";

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

export type DesktopServiceBridge = {
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
