import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { TaskFirstHomePage } from "./TaskFirstHomePage";

const create = vi.fn();
const send = vi.fn();

vi.mock("../api/sessionControlQueries", () => ({
  useCreateSession: () => ({ isPending: false, mutateAsync: create }),
  useSendSession: () => ({ isPending: false, mutateAsync: send })
}));

vi.mock("./Composer", () => ({
  Composer: ({ cwd }: { cwd?: string }) => <div data-cwd={cwd} data-testid="composer" />
}));

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "token",
  mobileJwt: "",
  tenantKey: "tenant",
  userId: "user",
  deviceId: "device",
  model: "model"
};

describe("TaskFirstHomePage", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.clearAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("passes the selected workspace to the composer and workspace affordance", async () => {
    const selectedWorkspace = "/Users/test-user/niuma-workspace/niuma";
    const selectWorkspace = vi.fn<() => Promise<string | null>>().mockResolvedValue(selectedWorkspace);

    act(() => root.render(
      <I18nProvider>
        <TaskFirstHomePage
          availableSources={[]}
          defaultWorkspace="/Users/test-user/GolandProjects/niuma"
          identity={identity}
          onCreated={vi.fn()}
          onSelectWorkspace={selectWorkspace}
          ready
        />
      </I18nProvider>
    ));

    expect(host.querySelector("[data-testid=composer]")?.getAttribute("data-cwd")).toBe("/Users/test-user/GolandProjects/niuma");

    await act(async () => {
      host.querySelector<HTMLButtonElement>(".webui2-task-first-context-item")?.click();
    });

    expect(selectWorkspace).toHaveBeenCalledTimes(1);
    expect(host.querySelector("[data-testid=composer]")?.getAttribute("data-cwd")).toBe(selectedWorkspace);
    expect(host.querySelector<HTMLButtonElement>(".webui2-task-first-context-item")?.title).toBe(selectedWorkspace);
  });
});
