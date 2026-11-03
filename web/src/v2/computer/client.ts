import { getDesktopServiceBridge, type DesktopServiceBridge } from "../desktopServiceBridge";
import type { ComputerActionReceipt, ComputerCapabilitiesResponse, ComputerObservationResponse, ComputerPermissionTarget, ComputerRuntimeStatus, ComputerSessionSnapshot, StartComputerSessionInput } from "./types";

export type ComputerClient = {
  getCapabilities: () => Promise<ComputerCapabilitiesResponse>;
  getRuntimeStatus?: () => Promise<ComputerRuntimeStatus>;
  stopRuntime?: () => Promise<ComputerRuntimeStatus>;
  openPermissionSettings: (target: ComputerPermissionTarget) => Promise<void>;
  start: (input: StartComputerSessionInput) => Promise<ComputerSessionSnapshot>;
  getSession: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  getActiveSession?: () => Promise<ComputerSessionSnapshot>;
  observe: (sessionID: string) => Promise<ComputerObservationResponse>;
  pause: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  resume: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  stop: (sessionID: string) => Promise<ComputerSessionSnapshot>;
  getReceipt: (sessionID: string, actionID: string) => Promise<ComputerActionReceipt>;
};

export type ComputerBridge = Pick<Required<DesktopServiceBridge>, "GetComputerCapabilities" | "StartComputerSession" | "GetComputerSession" | "ObserveComputerSession" | "PauseComputerSession" | "ResumeComputerSession" | "StopComputerSession" | "GetComputerActionReceipt"> & Pick<DesktopServiceBridge, "GetComputerRuntimeStatus" | "StopComputerRuntime" | "OpenComputerPermissionSettings" | "GetActiveComputerSession">;

export function getComputerBridge(): ComputerBridge | null {
  const bridge = getDesktopServiceBridge();
  if (!bridge?.GetComputerCapabilities || !bridge.StartComputerSession || !bridge.GetComputerSession || !bridge.ObserveComputerSession || !bridge.PauseComputerSession || !bridge.ResumeComputerSession || !bridge.StopComputerSession || !bridge.GetComputerActionReceipt) return null;
  return {
    GetComputerCapabilities: bridge.GetComputerCapabilities,
    OpenComputerPermissionSettings: bridge.OpenComputerPermissionSettings,
    StartComputerSession: bridge.StartComputerSession,
    GetComputerSession: bridge.GetComputerSession,
    GetActiveComputerSession: bridge.GetActiveComputerSession,
    ObserveComputerSession: bridge.ObserveComputerSession,
    PauseComputerSession: bridge.PauseComputerSession,
    ResumeComputerSession: bridge.ResumeComputerSession,
    StopComputerSession: bridge.StopComputerSession,
    GetComputerActionReceipt: bridge.GetComputerActionReceipt
  };
}

export function createComputerClient(bridge: ComputerBridge): ComputerClient {
  return {
    getCapabilities: () => bridge.GetComputerCapabilities(),
    getRuntimeStatus: bridge.GetComputerRuntimeStatus ? () => bridge.GetComputerRuntimeStatus!() : undefined,
    stopRuntime: bridge.StopComputerRuntime ? () => bridge.StopComputerRuntime!() : undefined,
    openPermissionSettings: (target) => bridge.OpenComputerPermissionSettings?.(target) ?? Promise.reject(new Error("Computer permission settings are unavailable.")),
    start: (input) => bridge.StartComputerSession(input),
    getSession: (sessionID) => bridge.GetComputerSession(sessionID),
    getActiveSession: bridge.GetActiveComputerSession ? () => bridge.GetActiveComputerSession!() : undefined,
    observe: (sessionID) => bridge.ObserveComputerSession(sessionID),
    pause: (sessionID) => bridge.PauseComputerSession(sessionID),
    resume: (sessionID) => bridge.ResumeComputerSession(sessionID),
    stop: (sessionID) => bridge.StopComputerSession(sessionID),
    getReceipt: (sessionID, actionID) => bridge.GetComputerActionReceipt(sessionID, actionID)
  };
}
