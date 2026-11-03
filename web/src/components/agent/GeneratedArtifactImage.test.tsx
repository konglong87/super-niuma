import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GeneratedArtifactImage } from "./MessageList";
import { getImageArtifact } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";

vi.mock("../../lib/api", () => ({ getImageArtifact: vi.fn() }));

const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", role: "member", model: "gpt-5.5" };

describe("GeneratedArtifactImage", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal("URL", class extends URL { static createObjectURL = vi.fn(() => "blob:generated-preview"); static revokeObjectURL = vi.fn(); });
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: vi.fn(function (this: HTMLDialogElement) { this.setAttribute("open", ""); }) });
    Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: vi.fn(function (this: HTMLDialogElement) { this.removeAttribute("open"); }) });
    vi.mocked(getImageArtifact).mockResolvedValue(new Blob(["png"], { type: "image/png" }));
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.unstubAllGlobals();
    vi.clearAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("opens a large preview and exposes a download action", async () => {
    await act(async () => root.render(<GeneratedArtifactImage identity={identity} assetId="asset-1" />));
    await vi.waitFor(() => expect(host.querySelector("img")).not.toBeNull());
    const image = host.querySelector<HTMLImageElement>("img")!;
    expect(image.closest("button")).not.toBeNull();
    expect(host.querySelector('a[download]')).not.toBeNull();
    await act(async () => image.click());
    expect(document.body.querySelector('dialog[open]')).not.toBeNull();
    expect(document.body.querySelector('a[download]')).not.toBeNull();
    expect(getImageArtifact).toHaveBeenCalledWith(identity, "asset-1");
  });

  it("focuses the modal close control and restores image focus after Escape", async () => {
    await act(async () => root.render(<GeneratedArtifactImage identity={identity} assetId="asset-1" />));
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Open generated asset"]')!;
    await act(async () => trigger.click());
    const close = document.querySelector<HTMLButtonElement>('dialog [aria-label="Close preview"]')!;
    expect(document.activeElement).toBe(close);
    const escapeEvent = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    act(() => close.dispatchEvent(escapeEvent));
    expect(escapeEvent.defaultPrevented).toBe(true);
    expect(document.querySelector("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(getImageArtifact).toHaveBeenCalledTimes(1);
  });

  it("keeps image clicks open while the accessible backdrop closes and returns focus", async () => {
    await act(async () => root.render(<GeneratedArtifactImage identity={identity} assetId="asset-1" />));
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Open generated asset"]')!;
    await act(async () => trigger.click());
    act(() => document.querySelector<HTMLImageElement>("dialog img")!.click());
    expect(document.querySelector("dialog[open]")).not.toBeNull();
    act(() => document.querySelector<HTMLButtonElement>('dialog button[aria-label="Close image preview"]')!.click());
    expect(document.querySelector("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("handles native modal cancellation without affecting the authenticated image lifecycle", async () => {
    await act(async () => root.render(<GeneratedArtifactImage identity={identity} assetId="asset-1" />));
    const trigger = host.querySelector<HTMLButtonElement>('[aria-label="Open generated asset"]')!;
    await act(async () => trigger.click());
    const cancel = new Event("cancel", { cancelable: true });
    act(() => document.querySelector("dialog")!.dispatchEvent(cancel));
    expect(document.querySelector("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(URL.revokeObjectURL).not.toHaveBeenCalled();
    act(() => root.render(<div />));
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:generated-preview");
  });
});
