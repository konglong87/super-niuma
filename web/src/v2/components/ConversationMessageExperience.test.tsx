import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import * as api from "../../lib/api";
import type { IdentityConfig } from "../../lib/types";
import type { SessionDetail, SessionMessage } from "../types";
import { ConversationWorkspace, THINKING_PREFERENCE_KEY } from "./ConversationWorkspace";

const message = (id: string, content: string, extra: Partial<SessionMessage> = {}): SessionMessage => ({ id, role: "assistant", kind: "message", content, createdAt: "", ...extra });
const session = (messages: SessionMessage[], extra: Partial<SessionDetail> = {}): SessionDetail => ({ ref: "tenant:a", source: "tenant", shortID: "a", title: "Session", status: "completed", updatedAt: "", messages, activity: [], context: [], changes: [], runs: [], ...extra });

describe("conversation message experience", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    storage = new Map();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value) } });
  });
  afterEach(() => {
    act(() => root.unmount());
    vi.useRealTimers();
    vi.restoreAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });
  const render = (detail: SessionDetail, onRetry = vi.fn()) => act(() => root.render(<I18nProvider><ConversationWorkspace detail={detail} selectedRef={detail.ref} composer={null} onOpenInspector={vi.fn()} onRetry={onRetry} streamState="live" /></I18nProvider>));

  it("copies all text sections of a response and regenerates the selected task", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const retry = vi.fn();
    const final = message("answer-2", "Second part", { taskID: 9, model: "model", provider: "provider", durationMs: 1600, tokens: 321 });
    render(session([message("answer-1", "First part", { taskID: 9 }), message("thinking", "Private reasoning", { kind: "thinking", taskID: 9 }), final]), retry);
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Copy response"]')?.click());
    expect(writeText).toHaveBeenCalledWith("First part\n\nSecond part");
    expect(host.textContent).toContain("provider · model");
    expect(host.textContent).toContain("321 tokens");
    const retryButtons = host.querySelectorAll<HTMLButtonElement>('button[aria-label="Regenerate"]');
    act(() => retryButtons[1]?.click());
    expect(retry).toHaveBeenCalledWith(final);
    render(session([final], { status: "waiting_permission" }), retry);
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Regenerate"]')?.disabled).toBe(true);
    render(session([final], { ref: "local:a", source: "local" }), retry);
    expect(host.querySelector<HTMLButtonElement>('button[aria-label="Regenerate"]')?.disabled).toBe(true);
  });

  it("does not replay initial history, refreshed content, or session switches", () => {
    vi.useFakeTimers();
    render(session([message("old", "Existing history", { liveRevision: 1 })]));
    expect(host.textContent).toContain("Existing history");
    render(session([message("old", "Existing history with live extension", { liveRevision: 2, status: "running" })], { status: "running" }));
    expect(host.textContent).toContain("Existing history");
    expect(host.textContent).not.toContain("Existing history with live extension");
    act(() => vi.advanceTimersByTime(1000));
    render(session([message("old", "Existing history", { liveRevision: 1 }), message("fresh", "Fresh SSE answer", { liveRevision: 2, status: "running" })], { status: "running" }));
    expect(host.querySelectorAll(".webui2-conversation-message")[1]?.textContent).not.toContain("Fresh SSE answer");
    act(() => vi.advanceTimersByTime(1000));
    expect(host.textContent).toContain("Fresh SSE answer");
    render(session([message("elsewhere", "Other session", { liveRevision: 3 })], { ref: "tenant:b" }));
    expect(host.textContent).toContain("Other session");
    render(session([message("fresh", "Fresh SSE answer with background update", { liveRevision: 4 })]));
    expect(host.textContent).toContain("Fresh SSE answer with background update");
    render(session([message("fresh", "Snapshot replacement", { liveRevision: 4 })]));
    expect(host.textContent).toContain("Snapshot replacement");
  });

  it("shows earlier history and supports full, summary and hidden thinking", () => {
    const entries = Array.from({ length: 65 }, (_, index) => message(`answer-${index}`, `Response ${index}`));
    entries.push(message("thinking", "Reasoning detail", { kind: "thinking", thinkingStatus: "completed" }));
    render(session(entries));
    expect(host.querySelectorAll(".webui2-conversation-message")).toHaveLength(59);
    expect(host.textContent).not.toContain("Reasoning detail");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-show-earlier")?.click());
    expect(host.querySelectorAll(".webui2-conversation-message")).toHaveLength(65);
    expect(host.querySelector('select[aria-label="Thinking"]')).toBeNull();
    expect(host.textContent).not.toContain("Reasoning detail");
    act(() => {
      window.localStorage.setItem(THINKING_PREFERENCE_KEY, "full");
      window.dispatchEvent(new StorageEvent("storage", { key: THINKING_PREFERENCE_KEY, newValue: "full" }));
    });
    expect(host.textContent).toContain("Reasoning detail");
    act(() => {
      window.localStorage.setItem(THINKING_PREFERENCE_KEY, "hidden");
      window.dispatchEvent(new StorageEvent("storage", { key: THINKING_PREFERENCE_KEY, newValue: "hidden" }));
    });
    expect(host.textContent).not.toContain("Reasoning detail");
    expect(host.querySelector(".webui2-thinking-full")).toBeNull();
    expect(storage.get(THINKING_PREFERENCE_KEY)).toBe("hidden");
    act(() => root.render(<I18nProvider><ConversationWorkspace key="remounted" detail={session(entries)} selectedRef="tenant:a" composer={null} onOpenInspector={vi.fn()} /></I18nProvider>));
    expect(host.querySelector('select[aria-label="Thinking"]')).toBeNull();
  });

  it("renders timestamps and shares one live clock, then fixes terminal elapsed time", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-06T00:00:10Z"));
    const interval = vi.spyOn(globalThis, "setInterval");
    const entries = [message("user", "Question", { role: "user", taskID: 1, createdAt: "2026-09-06T00:00:00Z" }), message("first", "Response first", { taskID: 1, createdAt: "2026-09-06T00:00:04Z" }), message("last", "Response last", { taskID: 1, createdAt: "2026-09-06T00:00:06Z" })];
    render(session(entries, { status: "running", runs: [{ id: "1", status: "running", startedAt: "2026-09-06T00:00:00Z" }] }));
    expect(host.querySelectorAll(".webui2-message-footer time")).toHaveLength(3);
    expect(interval.mock.calls.filter((call) => call[1] === 1000)).toHaveLength(1);
    expect(Array.from(host.querySelectorAll('[title="Duration"]')).map((element) => element.textContent)).toEqual(["10s", "10s"]);
    act(() => vi.advanceTimersByTime(3000));
    expect(Array.from(host.querySelectorAll('[title="Duration"]')).map((element) => element.textContent)).toEqual(["13s", "13s"]);
    render(session(entries, { status: "completed", runs: [{ id: "1", status: "completed", startedAt: "2026-09-06T00:00:00Z", endedAt: "2026-09-06T00:00:13Z" }] }));
    act(() => vi.advanceTimersByTime(20000));
    expect(Array.from(host.querySelectorAll('[title="Duration"]')).map((element) => element.textContent)).toEqual(["13s", "13s"]);
  });

  it("discloses actual compact summaries and shows thinking-stage timing", () => {
    render(session([message("thinking", "Stage detail", { kind: "thinking", thinkingStatus: "completed", stageDurationMs: 4000, phase: 2, createdAt: "2026-09-06T00:00:01Z" }), message("compact", "Preserved summary", { kind: "compact" })]));
    expect(host.textContent).toContain("Stage 2");
    expect(host.textContent).toContain("4s");
    expect(host.textContent).toContain("Context summary");
    expect(host.textContent).not.toContain("Preserved summary");
    const compact = host.querySelector<HTMLDetailsElement>(".webui2-compact-message")!;
    act(() => { compact.open = true; compact.dispatchEvent(new Event("toggle")); });
    expect(host.textContent).toContain("Preserved summary");
  });

  it("renders available user images and retains the name of metadata-only history", () => {
    const attachment = { type: "image", media_type: "image/png", name: "clipboard.png", size_bytes: 4, sha256: "hash" };
    render(session([message("image-user", "Describe this", { role: "user", attachments: [{ ...attachment, inline_data: "cG5n" }, { ...attachment, name: "uploaded.png", url: "https://cdn.example.test/image.png" }, { ...attachment, name: "old-inline.png" }] })]));
    expect(Array.from(host.querySelectorAll<HTMLImageElement>(".webui2-message-attachment > img")).map((image) => image.getAttribute("src"))).toEqual(["data:image/png;base64,cG5n", "https://cdn.example.test/image.png"]);
    expect(host.textContent).toContain("old-inline.png");
  });

  it("restores durable user images through the authenticated asset renderer", async () => {
    const identity: IdentityConfig = { apiBase: "/api", apiToken: "test-token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
    const fetchAsset = vi.spyOn(api, "getImageArtifact").mockResolvedValue(new Blob(["png"], { type: "image/png" }));
    const entries = [message("durable-user", "Review image", { role: "user", attachments: [{ attachment_id: "asset-image", type: "image", media_type: "image/png", name: "clipboard.png", size_bytes: 4, sha256: "hash", url: "/tenant/media/assets/asset-image" }] })];
    const view = () => <I18nProvider><ConversationWorkspace identity={identity} detail={session(entries)} selectedRef="tenant:a" composer={null} onOpenInspector={vi.fn()} /></I18nProvider>;
    await act(async () => root.render(view()));
    expect(fetchAsset).toHaveBeenCalledWith(identity, "asset-image");
    expect(host.querySelector<HTMLImageElement>(".webui2-user-surface img")?.src).toMatch(/^blob:/);
    expect(host.querySelector(".webui2-user-surface a[download]")).not.toBeNull();
    expect(host.textContent).toContain("clipboard.png");
    await act(async () => root.render(view()));
    expect(fetchAsset).toHaveBeenCalledTimes(1);
  });
});
