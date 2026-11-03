import { vi } from "vitest";
import type { ComputerClient } from "./client";
import type { ComputerCapabilities, ComputerObservationResponse, ComputerSessionSnapshot, ComputerSessionState } from "./types";

export const capabilities: ComputerCapabilities = {
  protocol_version: "computer-use.v1", platform: "macos", backend: "native_host",
  capture_readiness: "ready", input_readiness: "ready", focus_state: "focused", permission_state: "approved",
  coordinate_space: { origin: "top_left", unit: "pixels", width: 100, height: 100, scale_factor: 1 },
  image_supported: true, supports_pause: true, supports_stop: true
};
export const snapshot = (state: ComputerSessionState = "ready", sessionID = "s1"): ComputerSessionSnapshot => ({ session_id: sessionID, state, owner: { tenant_id: 1, user_id: 1, session_id: 0 }, owner_kind: "local_preview", capabilities });
export const observationResponse: ComputerObservationResponse = {
  observation: { id: "o1", session_id: "s1", width: 100, height: 100, scale_factor: 1, screenshot: { id: "image1", media_type: "image/png" }, cursor: { x: 1, y: 1 }, capabilities, observed_at: "2026-09-24T00:00:00Z" },
  image_data: "iVBORw0KGgo=", media_type: "image/png"
};
export function createTestClient(): ComputerClient {
  return {
    getCapabilities: vi.fn<ComputerClient["getCapabilities"]>().mockResolvedValue({ available: true, capabilities }),
    getRuntimeStatus: vi.fn<NonNullable<ComputerClient["getRuntimeStatus"]>>().mockResolvedValue({ state: "ready", active_session: false, available: true }),
    stopRuntime: vi.fn<NonNullable<ComputerClient["stopRuntime"]>>().mockResolvedValue({ state: "stopped", active_session: false, available: false }),
    openPermissionSettings: vi.fn<ComputerClient["openPermissionSettings"]>().mockResolvedValue(undefined),
    start: vi.fn<ComputerClient["start"]>().mockResolvedValue(snapshot()),
    getSession: vi.fn<ComputerClient["getSession"]>().mockResolvedValue(snapshot()),
    observe: vi.fn<ComputerClient["observe"]>().mockResolvedValue(observationResponse),
    pause: vi.fn<ComputerClient["pause"]>().mockResolvedValue(snapshot("paused")),
    resume: vi.fn<ComputerClient["resume"]>().mockResolvedValue(snapshot()),
    stop: vi.fn<ComputerClient["stop"]>().mockResolvedValue(snapshot("stopped")),
    getReceipt: vi.fn<ComputerClient["getReceipt"]>()
  };
}
export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
