import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildConversationMessages } from "./WebAgentPage";
import { GeneratedArtifactImage } from "./agent/MessageList";
import { getImageArtifact } from "../lib/api";
import type { IdentityConfig } from "../lib/types";

vi.mock("../lib/api", () => ({ getImageArtifact: vi.fn() }));

const identity: IdentityConfig = {
  apiBase: "/api", apiToken: "test-token", mobileJwt: "", tenantKey: "webui-local",
  userId: "webui-local-user", deviceId: "test-device", role: "owner", model: "gpt-5.5"
};

afterEach(() => {
  vi.mocked(getImageArtifact).mockReset();
  document.body.innerHTML = "";
});

describe("WebUI image regressions", () => {
  it("uses persisted duration after image events reset the segment clock", () => {
    const task = { id: 63, status: "completed", model: "gpt-5.6-sol", metadata_json: "{}", result_json: JSON.stringify({ duration_ms: 34698 }) } as never;
    const at = "2026-09-02T07:39:00.000Z";
    const event = (id: number, event_type: string, payload_json: string) => ({ id, task_id: 63, event_type, payload_json, created_at: at });
    const messages = buildConversationMessages(task, [
      event(1, "message", JSON.stringify({ from_agent: "webui", content: "生成图片" })),
      event(2, "tool_call", JSON.stringify({ tool_name: "GenerateImage", tool_id: "tool-1", input: "{}" })),
      event(3, "image_artifact", JSON.stringify({ asset_id: "asset-1", url: "/tenant/media/assets/asset-1", media_type: "image/png" })),
      event(4, "tool_result", JSON.stringify({ tool_name: "GenerateImage", tool_id: "tool-1", output: "{}", is_error: false })),
      event(5, "text_delta", JSON.stringify({ content: "图片已生成" }))
    ], []);
    expect(messages.find((message) => message.role === "assistant" && message.content.trim())?.durationMs).toBe(34698);
  });

  it("shows an image artifact load failure instead of silently hiding it", async () => {
    vi.mocked(getImageArtifact).mockRejectedValue(new Error("asset unavailable"));
    const host = document.createElement("div");
    document.body.appendChild(host);
    const root = createRoot(host);
    await act(async () => root.render(<GeneratedArtifactImage identity={identity} assetId="asset-1" />));
    await vi.waitFor(() => expect(host.textContent).toContain("图片加载失败"));
    root.unmount();
  });
});
