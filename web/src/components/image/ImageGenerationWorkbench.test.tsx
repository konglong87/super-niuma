import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ImageGenerationWorkbench, imageWorkbenchHref } from "./ImageGenerationWorkbench";
import type { IdentityConfig, TenantSession } from "../../lib/types";
import { listTenantSessions } from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";

vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, listTenantSessions: vi.fn() };
});

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "webui-local",
  userId: "webui-local-user",
  deviceId: "test-device",
  role: "owner",
  model: "gpt-5.5"
};

const sessions: TenantSession[] = [
  { id: 7, session_key: "session-7", title: "Image session", status: "active", model: "gpt-5.5", cwd: "/repo" },
  { id: 8, session_key: "session-8", title: "Second session", status: "active", model: "gpt-5.5", cwd: "/repo" }
];

describe("ImageGenerationWorkbench", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.mocked(listTenantSessions).mockResolvedValue(sessions);
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
  });

  it("builds a first-class image URL without embedding workspace detail state", () => {
    expect(imageWorkbenchHref(identity, 7)).toBe("/webui/?view=images&session_id=7&token=test-token");
    expect(imageWorkbenchHref(identity)).toBe("/webui/?view=images&token=test-token");
  });

  it("renders session selection on an independent image page", async () => {
    await act(async () => {
      root.render(<I18nProvider><ImageGenerationWorkbench identity={identity} initialSessionId={8} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(host.querySelector("[aria-label='Image generation page']")).not.toBeNull());
    const select = host.querySelector<HTMLSelectElement>("[aria-label='Image session']");
    expect(select?.value).toBe("8");
    expect(host.querySelector(".image-generation-panel")).not.toBeNull();
  });

  it("keeps a changed session selection local to the page", async () => {
    function Harness() {
      const [sessionId, setSessionId] = useState<number | undefined>(7);
      return <I18nProvider><ImageGenerationWorkbench identity={identity} initialSessionId={sessionId} onSessionChange={setSessionId} /></I18nProvider>;
    }
    await act(async () => {
      root.render(<Harness />);
    });
    await vi.waitFor(() => expect(host.querySelector("[aria-label='Image session']")).not.toBeNull());
    await act(async () => {
      const select = host.querySelector<HTMLSelectElement>("[aria-label='Image session']")!;
      select.value = "8";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(host.querySelector<HTMLSelectElement>("[aria-label='Image session']")?.value).toBe("8");
  });
});
