import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { createMockSessionControlClient } from "../api/mockSessionControlClient";
import type { SessionDetail } from "../types";
import { ConversationWorkspace } from "./ConversationWorkspace";

const webUIV2Styles = readFileSync(resolve(process.cwd(), "src/v2/styles.css"), "utf8");
const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };

const detail: SessionDetail = {
  ref: "tenant:alpha", source: "tenant", title: "Release coordination", status: "running", updatedAt: "2026-09-05T00:00:00.000Z", shortID: "alpha", profileLabel: "Release manager",
  messages: [{ id: "assistant-1", role: "assistant", kind: "message", content: "The rollout is ready.", createdAt: "2026-09-05T00:00:00.000Z" }], activity: [], context: [], changes: [], runs: []
};

describe("ConversationWorkspace", () => {
  let host: HTMLDivElement;
  let root: Root;
  let style: HTMLStyleElement;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    host.className = "webui2-page";
    document.body.replaceChildren(host);
    style = document.createElement("style");
    style.textContent = webUIV2Styles;
    document.head.append(style);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    style.remove();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("starts directly at the stream and reserves the bottom composer slot", () => {
    const onOpenInspector = vi.fn();
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={<div data-testid="composer-slot">Composer placeholder</div>} detail={detail} onOpenInspector={onOpenInspector} selectedRef="tenant:alpha" /></I18nProvider>));

    expect(host.querySelector(".webui2-conversation-stream")?.textContent).toContain("The rollout is ready.");
    expect(host.querySelector(".webui2-conversation-composer-slot")?.textContent).toContain("Composer placeholder");
    expect(host.querySelector("[role=toolbar]")).toBeNull();
    expect(host.textContent).not.toContain("Release coordination");
    expect(host.textContent).not.toContain("tenant:alpha");
    expect(host.textContent).not.toContain("Release manager");
    expect(host.querySelector('button[aria-label="Share session"]')).toBeNull();
    expect(host.querySelector('button[aria-label="Inspector"]')).toBeNull();
    expect(onOpenInspector).not.toHaveBeenCalled();
    expect(host.querySelector(".webui2-conversation-workspace--permission-pending")).toBeNull();
    expect(getComputedStyle(host.querySelector<HTMLElement>(".webui2-conversation-workspace")!).gridTemplateRows).toBe("minmax(0, 1fr) auto");
  });

  it("keeps user and assistant messages in explicit opposite-side containers", () => {
    const split: SessionDetail = {
      ...detail,
      status: "completed",
      messages: [
        { id: "user-1", role: "user", kind: "message", content: "User input", createdAt: "2026-09-05T00:00:00.000Z" },
        { id: "assistant-1", role: "assistant", kind: "message", content: "Agent answer", createdAt: "2026-09-05T00:00:01.000Z" }
      ]
    };
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={null} detail={split} onOpenInspector={vi.fn()} selectedRef="tenant:alpha" /></I18nProvider>));

    const user = host.querySelector<HTMLElement>('[data-role="user"]');
    const assistant = host.querySelector<HTMLElement>('[data-role="assistant"]');
    expect(user).not.toBeNull();
    expect(assistant).not.toBeNull();
    expect(user?.querySelector(".webui2-user-surface")?.textContent).toContain("User input");
    expect(assistant?.textContent).toContain("Agent answer");
    expect(getComputedStyle(user!).alignItems).toBe("flex-end");
    expect(getComputedStyle(assistant!).marginRight).toBe("auto");
  });

  it("opens Inspector only from the compact conversation affordance", () => {
    const onOpenInspector = vi.fn();
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={null} detail={detail} onOpenInspector={onOpenInspector} selectedRef="tenant:alpha" /></I18nProvider>));
    const affordance = host.querySelector<HTMLButtonElement>('button[aria-label="Open Inspector"]');

    expect(affordance).not.toBeNull();
    act(() => affordance?.click());
    expect(onOpenInspector).toHaveBeenCalledTimes(1);
  });

  it("surfaces a waiting permission request above the conversation", () => {
    const waiting = { ...detail, status: "waiting_permission" as const };
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={null} detail={waiting} onOpenInspector={vi.fn()} selectedRef="tenant:alpha" /></I18nProvider>));

    const strip = host.querySelector<HTMLElement>(".webui2-permission-strip");
    const workspace = host.querySelector<HTMLElement>(".webui2-conversation-workspace");
    expect(strip?.textContent).toContain("Permission request pending");
    expect(workspace?.classList.contains("webui2-conversation-workspace--permission-pending")).toBe(true);
    expect(getComputedStyle(workspace!).gridTemplateRows).toBe("auto minmax(0, 1fr) auto");
    expect(strip?.nextElementSibling).toBe(host.querySelector(".webui2-conversation-body"));
    expect(workspace?.lastElementChild).toBe(host.querySelector(".webui2-conversation-composer-slot"));
  });

  it("keeps an empty state and composer in bounded workspace rows", () => {
    const emptyDetail = { ...detail, status: "idle" as const, messages: [] };
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={<div>Composer placeholder</div>} detail={emptyDetail} onOpenInspector={vi.fn()} selectedRef="tenant:alpha" /></I18nProvider>));

    const workspace = host.querySelector<HTMLElement>(".webui2-conversation-workspace");
    const emptyState = host.querySelector<HTMLElement>(".webui2-empty-state");
    const composer = host.querySelector<HTMLElement>(".webui2-conversation-composer-slot");
    const workspaceStyle = getComputedStyle(workspace!);
    const emptyStyle = getComputedStyle(emptyState!);

    expect(workspaceStyle.display).toBe("grid");
    expect(workspaceStyle.gridTemplateRows).toBe("minmax(0, 1fr) auto");
    expect(["0", "0px"]).toContain(workspaceStyle.minHeight);
    expect(["0", "0px"]).toContain(emptyStyle.minHeight);
    expect(getComputedStyle(composer!).position).toBe("static");
    expect(workspace?.lastElementChild).toBe(composer);
    expect(host.querySelector('button[aria-label="Open Inspector"]')).not.toBeNull();
    expect(host.querySelector("[role=toolbar]")).toBeNull();
  });

  it("renders the populated fixture with operational records folded by default", async () => {
    const populated = await createMockSessionControlClient().get(identity, "tenant:beta");
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={null} detail={populated} onOpenInspector={vi.fn()} selectedRef="tenant:beta" /></I18nProvider>));

    expect(host.textContent).toContain("Please review the navigation states.");
    expect(host.textContent).toContain("The responsive review is complete.");
    expect(host.textContent).toContain("Thinking");
    expect(host.textContent).toContain("Tool output");
    expect(host.textContent).toContain("Context handoff");
    expect(host.textContent).toContain("Profile draft");
    expect(Array.from(host.querySelectorAll("details")).every((record) => !record.open)).toBe(true);
    expect(host.textContent).not.toContain("Compared desktop and mobile constraints.");
    expect(host.textContent).not.toContain("Viewport checks completed without overflow.");
    expect(host.textContent).not.toContain("Fixture handoff payload");
    expect(host.textContent).not.toContain("Fixture profile detail");
  });

  it("reserves a separate layout row for the Inspector control above rich messages", async () => {
    const populated = await createMockSessionControlClient().get(identity, "tenant:beta");
    act(() => root.render(<I18nProvider><ConversationWorkspace composer={null} detail={populated} onOpenInspector={vi.fn()} selectedRef="tenant:beta" /></I18nProvider>));
    const body = host.querySelector<HTMLElement>(".webui2-conversation-body");
    const inspector = host.querySelector<HTMLElement>(".webui2-conversation-inspector");
    const stream = host.querySelector<HTMLElement>(".webui2-conversation-stream");

    expect(getComputedStyle(body!).display).toBe("grid");
    expect(getComputedStyle(body!).gridTemplateRows).toBe("auto minmax(0, 1fr)");
    expect(getComputedStyle(inspector!).position).toBe("static");
    expect(inspector?.nextElementSibling).toBe(stream);
  });
});
