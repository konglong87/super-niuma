import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as api from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import type { ConversationEvent, SessionDetail } from "../types";
import { Inspector, type InspectorTab } from "./Inspector";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test-token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "selected-model" };
const event = (id: number, type: string, payload: object): ConversationEvent => ({ id, task_id: 1, event_type: type, payload_json: JSON.stringify(payload), created_at: "2026-09-06T00:00:00Z" });
const detail: SessionDetail = { ref: "tenant:test", id: 7, source: "tenant", shortID: "test", title: "Test", status: "completed", updatedAt: "", messages: [], activity: [], changes: [], context: [], runs: [], events: [
  event(1, "started", { provider: "actual-provider", model: "actual-model" }),
  event(2, "session_handoff", { package_sha256: "verifiedhash", package: { source: { ref: "tenant:source", cursor: "event:3" }, stage_summary: "Recovered context", open_items: ["Pending verification"], budget: { estimated_tokens: 42 } } }),
  event(3, "file_change", { path: "app.go", access: "edited", tool_name: "Edit", change: "modified", content_available: true, before_lines: 3, after_lines: 5, line_delta: 2 }),
  event(4, "usage", { input_tokens: 100, output_tokens: 10, cache_read_input_tokens: 50, service_tier: "priority" }),
  event(5, "permission_request", { request_id: "request", tool_name: "Bash", reason: "Workspace mutation", input: "command" }),
  event(6, "completed", {})
] };

describe("Inspector runtime details", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: () => null, setItem: vi.fn() } });
    Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }) });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });
  afterEach(() => { act(() => root.unmount()); vi.restoreAllMocks(); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT; });
  function render(tab: InspectorTab, selectedDetail = detail) { act(() => root.render(<I18nProvider><Inspector detail={selectedDetail} identity={identity} open onClose={vi.fn()} tab={tab} /></I18nProvider>)); }

  it("shows durable context with no empty placeholder, measured file stats, and actual per-run usage", () => {
    render("context");
    expect(host.textContent).toContain("Recovered context");
    expect(host.textContent).toContain("Pending verification");
    expect(host.querySelector(".webui2-inspector-empty")).toBeNull();
    render("changes");
    expect(host.textContent).toContain("+2");
    expect(host.textContent).toContain("Before 3 / After 5 lines");
    expect(host.textContent).toContain("Diff was not saved");
    render("runs");
    expect(host.textContent).toContain("actual-provider");
    expect(host.textContent).toContain("actual-model");
    expect(host.textContent).not.toContain("selected-model");
    expect(host.textContent).toContain("160 tokens");
    expect(host.textContent).toContain("priority");
    const trace = host.querySelector<HTMLAnchorElement>(".webui2-inspector-trace-link")!;
    expect(new URL(trace.href).searchParams.get("session_id")).toBe("7");
    expect(new URL(trace.href).searchParams.get("tenant_key")).toBe("tenant");
  });

  it("exposes permission history and reuses the authenticated permission API", async () => {
    const resolve = vi.spyOn(api, "resolveAgentTaskPermission").mockResolvedValue({ id: 1, request_id: "request", allowed: false });
    render("activity", { ...detail, status: "waiting_permission", events: detail.events?.filter((item) => item.event_type !== "completed") });
    expect(host.textContent).toContain("Permission history");
    expect(host.textContent).toContain("Workspace mutation");
    expect(host.querySelector(".webui2-run-usage > summary")?.textContent).toContain("Usage");
    const deny = Array.from(host.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "Deny")!;
    await act(async () => deny.click());
    expect(resolve).toHaveBeenCalledWith(identity, 1, "request", { allowed: false });
    expect(host.querySelector(".webui2-permission-actions")).toBeNull();
  });

  it("keeps ended permission requests in history without offering obsolete decisions", () => {
    render("activity");
    expect(host.textContent).toContain("Permission history");
    expect(host.querySelector(".webui2-permission-actions")).toBeNull();
  });
});
