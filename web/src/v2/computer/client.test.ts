import { afterEach, describe, expect, it, vi } from "vitest";
import { createComputerClient, getComputerBridge, type ComputerBridge } from "./client";
import { capabilities, observationResponse, snapshot } from "./testFixtures";

const bridge = (): ComputerBridge => ({
  GetComputerCapabilities: vi.fn<ComputerBridge["GetComputerCapabilities"]>().mockResolvedValue({ available: true, capabilities }),
  OpenComputerPermissionSettings: vi.fn<NonNullable<ComputerBridge["OpenComputerPermissionSettings"]>>().mockResolvedValue(undefined),
  StartComputerSession: vi.fn<ComputerBridge["StartComputerSession"]>().mockResolvedValue(snapshot()),
  GetComputerSession: vi.fn<ComputerBridge["GetComputerSession"]>().mockResolvedValue(snapshot()),
  ObserveComputerSession: vi.fn<ComputerBridge["ObserveComputerSession"]>().mockResolvedValue(observationResponse),
  PauseComputerSession: vi.fn<ComputerBridge["PauseComputerSession"]>().mockResolvedValue(snapshot("paused")),
  ResumeComputerSession: vi.fn<ComputerBridge["ResumeComputerSession"]>().mockResolvedValue(snapshot()),
  StopComputerSession: vi.fn<ComputerBridge["StopComputerSession"]>().mockResolvedValue(snapshot("stopped")),
  GetComputerActionReceipt: vi.fn<ComputerBridge["GetComputerActionReceipt"]>()
});

describe("computer bridge DTO contract", () => {
  afterEach(() => { delete window.go; });
  it("preserves exact Go envelopes and control snapshots", async () => {
    const host = bridge();
    const client = createComputerClient(host);
    await expect(client.getCapabilities()).resolves.toEqual({ available: true, capabilities });
    await expect(client.openPermissionSettings("accessibility")).resolves.toBeUndefined();
    expect(host.OpenComputerPermissionSettings).toHaveBeenCalledWith("accessibility");
    await expect(client.start({ approved: true })).resolves.toEqual(snapshot());
    expect(host.StartComputerSession).toHaveBeenCalledWith({ approved: true });
    await expect(client.getSession("s1")).resolves.toEqual(snapshot());
    expect(host.GetComputerSession).toHaveBeenCalledExactlyOnceWith("s1");
    expect(host.ObserveComputerSession).not.toHaveBeenCalled();
    await expect(client.observe("s1")).resolves.toEqual(observationResponse);
    await expect(client.pause("s1")).resolves.toEqual(snapshot("paused"));
    await expect(client.resume("s1")).resolves.toEqual(snapshot());
    await expect(client.stop("s1")).resolves.toEqual(snapshot("stopped"));
    expect(host.StopComputerSession).toHaveBeenCalledWith("s1");
  });
  it("forwards the exact approved conversation ref without client identity fields", async () => {
    const host = bridge();
    await createComputerClient(host).start({ approved: true, conversation_ref: "tenant:channel:alpha" });
    expect(host.StartComputerSession).toHaveBeenCalledExactlyOnceWith({ approved: true, conversation_ref: "tenant:channel:alpha" });
  });
  it("does not discard available=false or backend error details", async () => {
    const host = bridge();
    const response = { available: false, capabilities, error_code: "capability_unavailable", error_message: "helper missing" };
    host.GetComputerCapabilities = vi.fn().mockResolvedValue(response);
    await expect(createComputerClient(host).getCapabilities()).resolves.toEqual(response);
  });
  it("rejects missing and partial browser bridges", () => {
    expect(getComputerBridge()).toBeNull();
    window.go = { main: { app: { RestartLocalService: vi.fn(), GetComputerCapabilities: bridge().GetComputerCapabilities } } };
    expect(getComputerBridge()).toBeNull();
    window.go.main!.app = { RestartLocalService: vi.fn(), ...bridge() };
    expect(getComputerBridge()).not.toBeNull();
    delete window.go.main!.app.GetComputerSession;
    expect(getComputerBridge()).toBeNull();
  });
  it("returns a clear error when permission navigation is not exposed by the host", async () => {
    const host = bridge();
    delete host.OpenComputerPermissionSettings;
    await expect(createComputerClient(host).openPermissionSettings("screen_capture")).rejects.toThrow("Computer permission settings are unavailable.");
  });
});
