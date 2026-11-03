// UI-only mock fixtures. These are not provider/model/E2E acceptance evidence.
import { vi } from "vitest";
import type { ControlRun, SuperClaim, SuperEvent, SuperGrant, SuperPlatformBridge, SuperPlatformSnapshot, WorkOrder } from "../desktopServiceBridge";

export const MOCK_HASH = "a".repeat(64);
export const MOCK_ARTIFACT_HASH = "b".repeat(64);
export const mockClaim: SuperClaim = { scope: "local", run_id: "mock-run", job_id: "mock-node", owner: "mock-worker", token: "do-not-render-claim", epoch: 1, until_ms: 0 };
export function mockGrant(): SuperGrant {
  return { profile_id: "mock-profile", revision: "mock-revision", workspace: "/mock/workspace", provider: "mock", model: "mock-model",
    system_prompt: "do-not-render-system-prompt", max_tokens: 8000, timeout_ms: 120000, expires_at_ms: 4102444800000,
    nodes: [{ id: "mock-node", instruction: "do-not-render-instruction", read_paths: ["input.txt"], write_paths: ["output.txt"], max_turns: 3, max_output_tokens: 1000 }],
    verification: { version: "1", rules: [{ path: "output.txt", kind: "exists" }] }, targets: [{ id: "mock-target", provider: "desktop" }], auto_approve: false };
}
export function mockRun(overrides: Partial<ControlRun> = {}): ControlRun {
  const grant = mockGrant();
  return { id: "mock-run", scope: "local", owner: "local", intake_id: "mock-order", request_key: "do-not-render-request-key", objective: "do-not-render-objective",
    grant, grant_hash: MOCK_HASH, version: 1, status: "awaiting_approval", jobs: [{ spec: grant.nodes[0], status: "queued", claim: mockClaim, attempts: 0,
      result: { text: "do-not-render-model-output", usage: { input_tokens: 0, output_tokens: 0 }, usage_known: false, tool_calls: 0 } }],
    reservations: [], verification_claim: mockClaim, notices: [], approved: false, created_at_ms: 1793664000000, updated_at_ms: 1793664000000, ...overrides };
}
export function mockOrder(): WorkOrder {
  return { id: "mock-order", title: "Mock 工单", objective: "Mock 目标", acceptance_criteria: ["Mock 验收"], workspace: "/mock/workspace", status: "recorded",
    execution_status: "not_started", verification_status: "not_started", delivery_status: "not_started", created_at: "2026-11-03", updated_at: "2026-11-03" };
}
export function mockSnapshot(overrides: Partial<SuperPlatformSnapshot> = {}): SuperPlatformSnapshot {
  const grant = mockGrant();
  return { configured: true, running: true, workers: 2,
    profiles: [{ id: grant.profile_id, name: "Mock Profile", workspace: grant.workspace, enabled: true, model: grant.model }],
    runs: [], config_json: JSON.stringify({ version: 1, workers: 2, profiles: [{ id: grant.profile_id, name: "Mock Profile", enabled: true, scopes: ["local"], grant }] }),
    capabilities: { mock: true }, ...overrides };
}
export function mockBridge(snapshot = mockSnapshot()): SuperPlatformBridge {
  return {
    GetSuperPlatform: vi.fn().mockResolvedValue(snapshot), GetSuperEvents: vi.fn().mockResolvedValue([]),
    SaveSuperPlatformConfig: vi.fn().mockResolvedValue(snapshot), StartSuperRun: vi.fn().mockResolvedValue(mockRun()),
    ApproveSuperRun: vi.fn().mockResolvedValue(mockRun({ version: 2, status: "queued", approved: true })),
    CancelSuperRun: vi.fn().mockResolvedValue(mockRun({ version: 2, status: "cancelled" })),
    RetrySuperRun: vi.fn().mockResolvedValue(mockRun({ version: 2, status: "queued" })),
  };
}
export function mockEvent(sequence: number): SuperEvent {
  return { sequence, id: `mock-event-${sequence}`, scope: "local", topic: "super.run.changed", aggregate_id: "mock-run", version: sequence,
    payload: { raw: "do-not-render-event-payload" }, occurred_at_ms: 1793664000000 };
}
export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
