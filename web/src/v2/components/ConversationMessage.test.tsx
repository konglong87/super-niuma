import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { SessionMessage } from "../types";
import { ConversationMessage } from "./ConversationMessage";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };

const createdAt = "2026-09-05T00:00:00.000Z";

function message(overrides: Partial<SessionMessage>): SessionMessage {
  return { id: "message-1", role: "assistant", kind: "message", content: "Assistant **prose**", createdAt, ...overrides };
}

describe("ConversationMessage", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key), clear: () => storage.clear() } });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.useRealTimers();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render(item: SessionMessage, language: "en" | "zh" = "en"): void {
    storage.set("niuma-webui.language.v1", language);
    act(() => root.render(<I18nProvider><ConversationMessage identity={identity} message={item} /></I18nProvider>));
  }

  it("renders assistant prose in document flow without a bubble surface", () => {
    render(message({ role: "assistant" }));

    expect(host.querySelector(".webui2-conversation-message--assistant .agent-markdown")).not.toBeNull();
    expect(host.querySelector(".webui2-conversation-message--assistant .webui2-user-surface")).toBeNull();
    expect(host.querySelector(".webui2-conversation-message--assistant .webui2-message-bubble")).toBeNull();
  });

  it("renders user prose in the single neutral user surface", () => {
    render(message({ role: "user", content: "Please check the rollout." }));

    expect(host.querySelectorAll(".webui2-user-surface")).toHaveLength(1);
    expect(host.querySelector(".webui2-user-surface .agent-markdown")?.textContent).toContain("Please check the rollout.");
  });

  it("shows a Computer Use observation thumbnail below the collapsed tool card", async () => {
    vi.stubGlobal("URL", class extends URL { static createObjectURL = vi.fn(() => "blob:computer-observation"); static revokeObjectURL = vi.fn(); });
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: vi.fn(function (this: HTMLDialogElement) { this.setAttribute("open", ""); }) });
    Object.defineProperty(HTMLDialogElement.prototype, "close", { configurable: true, value: vi.fn(function (this: HTMLDialogElement) { this.removeAttribute("open"); }) });
    // Use a string body instead of passing a Blob to Node's Response
    // constructor: Node 22's undici implementation does not consistently
    // expose Blob#stream(), while the browser contract only requires the
    // response to support response.blob().
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("png", { status: 200, headers: { "Content-Type": "image/png" } })));
    render(message({ kind: "tool", tool: { id: "tool-1", name: "ComputerUse", input: "{}", command: "observe", output: "done", status: "completed", computerObservation: { observationID: "obs-1", assetID: "asset-screen", mediaType: "image/png", name: "screen.png", sizeBytes: 42, sha256: "hash" } } }));
    await vi.waitFor(() => expect(host.querySelector('button[aria-label="Open generated asset"]')).not.toBeNull());
    expect(host.querySelector(".webui2-tool-message")?.getAttribute("open")).toBeNull();
    expect(host.textContent).toContain("Computer Use observation");
  });

  it("keeps thinking, tool, and handoff content folded until their own summary is opened", () => {
    render(message({ kind: "thinking", content: "private reasoning" }));
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.textContent).not.toContain("private reasoning");
    act(() => openDetails(host.querySelector("details")));
    expect(host.querySelector("details")?.open).toBe(true);
    expect(host.textContent).toContain("private reasoning");

    act(() => root.unmount());
    root = createRoot(host);
    render(message({ kind: "tool", content: "tool output" }));
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.textContent).not.toContain("tool output");

    act(() => root.unmount());
    root = createRoot(host);
    render(message({ kind: "handoff", content: "raw transcript", handoff: { packageID: "handoff-1", hashPrefix: "abc123", stale: true, sourceRefs: ["tenant:alpha", "local:workspace"] } }));
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.textContent).toContain("2");
    expect(host.textContent).toContain("abc123");
    expect(host.textContent).not.toContain("raw transcript");
  });

  it("renders operation status semantically and folds its detail", () => {
    render(message({ kind: "operation", content: "Ignored prose", operation: { id: "operation-1", kind: "send", status: "failed", title: "Queued input", detail: "network_unavailable", createdAt } }));

    const operation = host.querySelector(".webui2-operation-card");
    expect(operation?.getAttribute("data-operation-status")).toBe("failed");
    expect(operation?.getAttribute("role")).toBe("status");
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.textContent).not.toContain("Ignored prose");
    expect(host.textContent).not.toContain("network_unavailable");
  });

  it("keeps a fixture profile draft operation compact and folded", () => {
    render(message({ kind: "operation", operation: { id: "operation-profile", kind: "profile_draft", status: "completed", title: "fixture title", detail: "fixture detail", createdAt } }));

    expect(host.querySelector(".webui2-operation-card")?.textContent).toContain("Profile draft");
    expect(host.querySelector("details")?.open).toBe(false);
    expect(host.innerHTML).not.toContain("fixture title");
    expect(host.innerHTML).not.toContain("fixture detail");
  });

  it("renders a localized replay result instead of an unknown replay state", () => {
    const operation = { id: "operation-replay", kind: "create", status: "completed", title: "raw", detail: "", createdAt, replayed: true } as NonNullable<SessionMessage["operation"]>;
    render(message({ kind: "operation", operation }));

    const replay = Array.from(host.querySelectorAll(".webui2-operation-card-meta div")).find((row) => row.querySelector("dt")?.textContent === "Replay");
    expect(replay?.querySelector("dd")?.textContent).toBe("Replayed result");
    expect(host.textContent).not.toContain("Not recorded");
  });

  it("never renders arbitrary backend operation detail after expansion", () => {
    render(message({ kind: "operation", operation: { id: "operation-2", kind: "send", status: "failed", title: "Backend title: SECRET_TITLE=private-title", detail: "Backend diagnostic: SECRET_DETAIL=private-detail", createdAt } }));

    act(() => openDetails(host.querySelector("details")));

    expect(host.innerHTML).not.toContain("Backend title");
    expect(host.innerHTML).not.toContain("SECRET_TITLE");
    expect(host.innerHTML).not.toContain("private-title");
    expect(host.innerHTML).not.toContain("Backend diagnostic");
    expect(host.innerHTML).not.toContain("SECRET_DETAIL");
    expect(host.innerHTML).not.toContain("private-detail");
    const safeDetail = host.querySelector(".webui2-operation-card-detail");
    expect(safeDetail?.textContent).toContain("Send input");
    expect(safeDetail?.textContent).toContain("Failed");
    expect(safeDetail?.querySelector("time")?.getAttribute("datetime")).toBe(createdAt);
  });

  it.each([
    ["en", "Archive session", "Completed"],
    ["zh", "归档会话", "已完成"]
  ] as const)("maps a known operation kind and status in %s", (language, expectedKind, expectedStatus) => {
    render(message({ kind: "operation", operation: { id: "operation-3", kind: "archive", status: "completed", title: "raw title", detail: "raw detail", createdAt } }), language);

    expect(host.textContent).toContain(expectedKind);
    expect(host.textContent).toContain(expectedStatus);
    expect(host.innerHTML).not.toContain("raw title");
    expect(host.innerHTML).not.toContain("raw detail");
  });

  it("maps unknown kind and status to safe generic labels", () => {
    const unknownOperation = { id: "operation-4", kind: "SECRET_KIND=private-kind", status: "SECRET_STATUS=private-status", title: "SECRET_TITLE=private-title", detail: "SECRET_DETAIL=private-detail", createdAt } as unknown as NonNullable<SessionMessage["operation"]>;

    render(message({ kind: "operation", operation: unknownOperation }));

    expect(host.textContent).toContain("Operation");
    expect(host.textContent).toContain("Status unavailable");
    expect(host.innerHTML).not.toContain("SECRET_");
    expect(host.innerHTML).not.toContain("private-");
  });

  it("submits a selected question choice through its explicit answer form", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 14, request_id: "question-1", status: "answered", answer: "Canary" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    render(message({ kind: "question" as never, taskID: 14, content: "Which release channel?", question: { taskID: 14, requestID: "question-1", choices: ["Stable", "Canary"], status: "pending" } } as unknown as SessionMessage));

    const submit = host.querySelector<HTMLButtonElement>('button[type="submit"]');
    expect(host.querySelector("fieldset")).not.toBeNull();
    expect(submit?.disabled).toBe(true);
    act(() => host.querySelector<HTMLInputElement>('input[value="Canary"]')?.click());
    expect(submit?.disabled).toBe(false);
    await act(async () => submit?.click());

    expect(fetchMock).toHaveBeenCalledWith("/api/tenant/agent-tasks/14/questions/question-1", expect.objectContaining({ method: "PATCH", body: '{"answer":"Canary"}' }));
    expect(host.textContent).toContain("Canary");
    expect(host.querySelector("fieldset")).toBeNull();
  });

  it("closes a stale question after conflict and preserves the attempted answer", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "question is no longer pending" }), { status: 409 }));
    vi.stubGlobal("fetch", fetchMock);
    render(message({ kind: "question" as never, taskID: 14, content: "Continue?", question: { taskID: 14, requestID: "question-1", choices: [], status: "pending" } } as unknown as SessionMessage));
    const textarea = host.querySelector<HTMLTextAreaElement>("textarea");
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
      setter?.call(textarea, "Use the canary");
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => host.querySelector<HTMLButtonElement>('button[type="submit"]')?.click());
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("no longer waiting");
    expect(textarea?.value).toBe("Use the canary");
    expect(textarea?.disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')?.disabled).toBe(true);
    await act(async () => host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("keeps a network failure retryable", async () => {
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new TypeError("offline"))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 14, request_id: "question-1", status: "answered", answer: "Use the canary" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    render(message({ kind: "question" as never, taskID: 14, content: "Continue?", question: { taskID: 14, requestID: "question-1", choices: [], status: "pending" } } as unknown as SessionMessage));
    const textarea = host.querySelector<HTMLTextAreaElement>("textarea");
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
      setter?.call(textarea, "Use the canary");
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => host.querySelector<HTMLButtonElement>('button[type="submit"]')?.click());
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Try again");
    expect(textarea?.disabled).toBe(false);
    expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')?.disabled).toBe(false);
    await act(async () => host.querySelector<HTMLButtonElement>('button[type="submit"]')?.click());
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain("Use the canary");
  });

  it("closes a pending question at its expiry time", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-07T00:00:00Z"));
    render(message({ kind: "question" as never, taskID: 14, content: "Continue?", question: { taskID: 14, requestID: "question-1", choices: ["Yes"], expiresAt: "2026-09-07T00:00:01Z", status: "pending" } } as unknown as SessionMessage));
    expect(host.querySelector("form")).not.toBeNull();

    act(() => vi.advanceTimersByTime(1_000));

    expect(host.querySelector("form")).toBeNull();
    expect(host.querySelector(".agent-user-question")?.getAttribute("data-question-status")).toBe("expired");
  });

  it("does not prematurely close a question with an invalid expiry date", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-07T00:00:00Z"));
    render(message({ kind: "question" as never, taskID: 14, content: "Continue?", question: { taskID: 14, requestID: "question-1", choices: ["Yes"], expiresAt: "not-a-date", status: "pending" } } as unknown as SessionMessage));

    act(() => vi.advanceTimersByTime(60_000));

    expect(host.querySelector("form")).not.toBeNull();
  });

  it("shows resolved and historical questions without answer actions", () => {
    render(message({ kind: "question" as never, content: "Which database?", question: { taskID: 14, requestID: "question-1", choices: ["Postgres"], status: "answered", answer: "Postgres" } } as unknown as SessionMessage));
    expect(host.textContent).toContain("Postgres");
    expect(host.querySelector("form")).toBeNull();

    act(() => root.unmount());
    root = createRoot(host);
    render(message({ kind: "question" as never, content: "Old question", question: { taskID: 14, choices: ["A", "B"], status: "unavailable", legacy: true } } as unknown as SessionMessage));
    expect(host.textContent).toContain("A");
    expect(host.textContent).toContain("B");
    expect(host.querySelector("form")).toBeNull();
  });

  it("explains that a generated image remains available when the later reply fails", () => {
    render(message({ kind: "error", content: "upstream image download failed", error: { code: "provider_error", artifactAvailable: true } } as unknown as SessionMessage));
    expect(host.textContent).toContain("generated image is still available");
    expect(host.textContent).toContain("upstream image download failed");
  });

  it("explains persisted preparation failures that have only an error code", () => {
    render(message({ kind: "error", content: "failed", error: { code: "not_found" } }));
    expect(host.textContent).toContain("The session could not be found.");
    expect(host.querySelectorAll(".agent-markdown")).toHaveLength(0);
  });
});

function openDetails(details: HTMLDetailsElement | null): void {
  if (!details) return;
  details.open = true;
  details.dispatchEvent(new Event("toggle", { bubbles: true }));
}
