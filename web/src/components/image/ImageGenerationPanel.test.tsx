import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ImageGenerationPanel } from "./ImageGenerationPanel";
import { editImage, generateImage, getImageArtifact, listImageHistory, getImageCapabilities } from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  generateImage: vi.fn(),
  editImage: vi.fn(),
  getImageArtifact: vi.fn(),
  listImageHistory: vi.fn(),
  getImageCapabilities: vi.fn()
}));

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "device-a",
  model: "gpt-image-2"
};

const artifact = {
  asset_id: "asset-1",
  generation_id: "generation-1",
  operation: "generate" as const,
  media_type: "image/png",
  width: 1024,
  height: 1024,
  size_bytes: 42,
  sha256: "hash",
  url: "/tenant/media/assets/asset-1",
  session_id: 7
};

describe("ImageGenerationPanel", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    vi.mocked(listImageHistory).mockResolvedValue([]);
    vi.mocked(getImageCapabilities).mockResolvedValue([]);
    vi.mocked(generateImage).mockResolvedValue({ asset: artifact });
    vi.mocked(editImage).mockResolvedValue({ asset: { ...artifact, operation: "edit" } });
    vi.mocked(getImageArtifact).mockResolvedValue(new Blob(["image"], { type: "image/png" }));
    vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn(() => "blob:image-1"), revokeObjectURL: vi.fn() });
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  it("generates an image and renders the authenticated artifact blob", async () => {
    await act(async () => {
      root.render(<ImageGenerationPanel identity={identity} sessionId={7} />);
    });
    const prompt = host.querySelector<HTMLTextAreaElement>("textarea[name=prompt]");
    expect(prompt).not.toBeNull();
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(prompt, "A red bridge at sunrise");
      prompt!.dispatchEvent(new Event("change", { bubbles: true }));
      host.querySelector<HTMLButtonElement>("button[type=submit]")!.click();
    });
    expect(generateImage).toHaveBeenCalledWith(identity, 7, expect.objectContaining({ prompt: "A red bridge at sunrise" }), expect.anything());
    expect(getImageArtifact).toHaveBeenCalledWith(identity, "asset-1", expect.anything());
    expect(host.querySelector('img[alt="Generated asset"]')?.getAttribute("src")).toBe("blob:image-1");
  });

  it("switches to edit mode, selects a source, retries, and downloads the result", async () => {
    await act(async () => {
      root.render(<ImageGenerationPanel identity={identity} sessionId={7} />);
    });
    await act(async () => {
      host.querySelector<HTMLButtonElement>("[data-mode=edit]")!.click();
    });
    expect(host.querySelector("input[type=file]")).not.toBeNull();
    expect(host.textContent).toContain("Select a source image");
    await act(async () => {
      const prompt = host.querySelector<HTMLTextAreaElement>("textarea[name=prompt]")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(prompt, "Make it blue");
      prompt.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const retry = host.querySelector<HTMLButtonElement>("[data-action=retry]");
    expect(retry).toBeNull();
    vi.mocked(editImage).mockRejectedValueOnce(new Error("temporary failure"));
    await act(async () => {
      host.querySelector<HTMLButtonElement>("button[type=submit]")!.click();
    });
    expect(host.textContent).toContain("temporary failure");
    expect(host.querySelector<HTMLButtonElement>("[data-action=retry]")).not.toBeNull();
  });

  it("renders Agnes resolution and aspect ratio controls from capabilities", async () => {
    vi.mocked(getImageCapabilities).mockResolvedValue([{ provider: "agnes", model: "agnes-image-2.5-flash", label: "Agnes Image 2.5 Flash", capability: { operations: ["generate", "edit"], resolutions: ["1K", "2K", "4K"], aspectRatios: ["1:1", "16:9"], outputFormats: ["png"] } }]);
    await act(async () => {
      root.render(<ImageGenerationPanel identity={identity} sessionId={7} />);
    });
    const selects = Array.from(host.querySelectorAll("select"));
    expect(selects.some((select) => select.textContent?.includes("4K"))).toBe(true);
    expect(selects.some((select) => select.textContent?.includes("16:9"))).toBe(true);
    expect(host.textContent).toContain("Agnes Image 2.5 Flash");
  });
});
