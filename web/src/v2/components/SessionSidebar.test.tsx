import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import type { SessionListFilters, SessionRef, SessionSummary } from "../types";
import type { IdentityConfig } from "../../lib/types";
import { SessionSidebar } from "./SessionSidebar";
import { SESSION_REF_MIME_TYPE } from "./sessionContextDrag";

const sessions: SessionSummary[] = [
  { ref: "tenant:alpha", source: "tenant", title: "Release coordination", status: "running", updatedAt: "2026-09-05T00:00:00.000Z", shortID: "alpha" },
  { ref: "tenant:beta", source: "tenant", title: "Design review", status: "completed", updatedAt: "2026-09-04T00:00:00.000Z", shortID: "beta" },
  { ref: "local:workspace", source: "local", title: "Local workspace", status: "idle", updatedAt: "2026-09-03T00:00:00.000Z", shortID: "workspace" }
];
const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "webui-local", userId: "webui-local-user", deviceId: "device", model: "model" };

describe("SessionSidebar", () => {
  let host: HTMLDivElement;
  let root: Root;
  let filters: SessionListFilters;
  let selected: SessionRef | null;
  let storage: Map<string, string>;
  let select = vi.fn<(ref: SessionRef) => void>();
  let drag = vi.fn<(ref: SessionRef) => void>();
  let openSettings = vi.fn<() => void>();
  let createSession = vi.fn<() => void>();
  let createSessionInWorkspace = vi.fn<(cwd: string) => void>();
  let rename = vi.fn<(session: SessionSummary, title: string) => Promise<boolean>>();

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    storage = new Map();
    Object.defineProperty(window, "localStorage", { configurable: true, value: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key), clear: () => storage.clear() } });
    filters = { query: "", statuses: [] };
    selected = "tenant:alpha";
    select = vi.fn<(ref: SessionRef) => void>();
    drag = vi.fn<(ref: SessionRef) => void>();
    openSettings = vi.fn<() => void>();
    createSession = vi.fn<() => void>();
    createSessionInWorkspace = vi.fn<(cwd: string) => void>();
    rename = vi.fn().mockResolvedValue(true);
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  });

  afterEach(() => {
    act(() => root.unmount());
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function render(items = sessions) {
    act(() => root.render(<I18nProvider><SessionSidebar identity={identity} sessions={items} filters={filters} selectedRef={selected} onRenameSession={rename} onCreateSession={createSession} onCreateSessionInWorkspace={createSessionInWorkspace} onFiltersChange={(next) => { filters = next; }} onSelect={select} onContextDragStart={drag} onOpenSettings={openSettings} onOpenSearch={vi.fn()} onHideSidebar={vi.fn()} /></I18nProvider>));
  }

  function openRename(entry: "overflow" | "context") {
    render(sessions.map((session, index) => ({ ...session, id: index + 1 })));
    const row = host.querySelector<HTMLElement>(".webui2-session-row")!;
    if (entry === "overflow") act(() => row.querySelector<HTMLButtonElement>(".webui2-session-actions > button")!.click());
    else act(() => row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 100, clientY: 120 })));
    const command = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((button) => button.textContent === "Rename session")!;
    expect(command).toBeDefined();
    act(() => command.click());
    return host.querySelector<HTMLInputElement>(".webui2-session-title-input")!;
  }

  function changeTitle(input: HTMLInputElement, title: string) {
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, title);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it.each(["overflow", "context"] as const)("renames inline from %s without selecting or dragging", async (entry) => {
    const input = openRename(entry);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("Release coordination");
    expect(input.selectionEnd).toBe(input.value.length);
    expect(document.querySelector('[role="menu"]')).toBeNull();
    changeTitle(input, "  Release plan  ");
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(rename).toHaveBeenCalledWith(expect.objectContaining({ ref: "tenant:alpha", id: 1 }), "Release plan");
    expect(host.querySelector(".webui2-session-title-input")).toBeNull();
    expect(select).not.toHaveBeenCalled();
    expect(drag).not.toHaveBeenCalled();
  });

  it("cancels with Escape or outside focus and keeps an unchanged title without a write", async () => {
    let input = openRename("overflow");
    changeTitle(input, "Uncommitted");
    act(() => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector(".webui2-session-title")?.textContent).toBe("Release coordination");
    input = openRename("context");
    changeTitle(input, "Uncommitted");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-new-session")!.focus());
    expect(host.querySelector(".webui2-session-title-input")).toBeNull();
    input = openRename("overflow");
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(rename).not.toHaveBeenCalled();
  });

  it("rejects whitespace, waits for IME composition and saves with its button", async () => {
    const input = openRename("overflow");
    changeTitle(input, "   ");
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(host.querySelector('[role="alert"]')?.textContent).toBe("Enter a session title.");
    expect(rename).not.toHaveBeenCalled();
    changeTitle(input, "Release");
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true })));
    expect(rename).not.toHaveBeenCalled();
    const save = host.querySelector<HTMLButtonElement>('.webui2-session-editing [aria-label="Save"]')!;
    act(() => save.focus());
    expect(host.querySelector(".webui2-session-title-input")).not.toBeNull();
    await act(async () => save.click());
    expect(rename).toHaveBeenCalledOnce();
  });

  it("prevents duplicate saves and preserves the old title on failure", async () => {
    const input = openRename("context");
    let finish!: (value: boolean) => void;
    rename.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    changeTitle(input, "Changed");
    await act(async () => {
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    expect(rename).toHaveBeenCalledOnce();
    expect(input.readOnly).toBe(true);
    await act(async () => finish(false));
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("could not be saved");
    act(() => host.querySelector<HTMLButtonElement>('.webui2-session-editing [aria-label="Cancel"]')!.click());
    expect(host.querySelector(".webui2-session-title")?.textContent).toBe("Release coordination");
  });

  it("keeps focus until a save click completes when buttons do not receive mouse focus", async () => {
    const input = openRename("context");
    changeTitle(input, "Mac title");
    const save = host.querySelector<HTMLButtonElement>('.webui2-session-editing [aria-label="Save"]')!;
    act(() => {
      const down = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
      save.dispatchEvent(down);
      if (!down.defaultPrevented) input.blur();
    });
    expect(host.contains(save)).toBe(true);
    expect(document.activeElement).toBe(input);
    await act(async () => save.click());
    expect(rename).toHaveBeenCalledWith(expect.objectContaining({ ref: "tenant:alpha" }), "Mac title");
  });

  it("hides rename for read-only sessions and unknown numeric IDs, and dismisses the context menu", () => {
    render();
    const row = host.querySelector<HTMLElement>(".webui2-session-row")!;
    act(() => row.querySelector<HTMLButtonElement>(".webui2-session-actions > button")!.click());
    expect(row.textContent).not.toContain("Rename session");
    act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    render(sessions.map((session, index) => ({ ...session, id: index + 1 })));
    const local = host.querySelectorAll<HTMLElement>(".webui2-session-row")[2];
    act(() => local.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true })));
    expect(document.querySelector(".webui2-session-context-menu")).toBeNull();
    act(() => row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 9999, clientY: 9999 })));
    const menu = document.querySelector<HTMLElement>(".webui2-session-context-menu")!;
    expect(parseFloat(menu.style.left)).toBeLessThan(window.innerWidth);
    expect(parseFloat(menu.style.top)).toBeLessThan(window.innerHeight);
    act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector(".webui2-session-context-menu")).toBeNull();
    expect(document.activeElement).toBe(row.querySelector(".webui2-session-select"));
  });

  function renderControlled() {
    function Harness() {
      const [currentFilters, setCurrentFilters] = useState<SessionListFilters>({ query: "", statuses: [] });
      return <SessionSidebar identity={identity} sessions={sessions} filters={currentFilters} selectedRef={selected} onCreateSession={createSession} onCreateSessionInWorkspace={createSessionInWorkspace} onFiltersChange={setCurrentFilters} onSelect={select} onContextDragStart={drag} onOpenSettings={openSettings} onOpenSearch={vi.fn()} onHideSidebar={vi.fn()} />;
    }
    act(() => root.render(<I18nProvider><Harness /></I18nProvider>));
  }

  it("groups managed and read-only local sessions with their metadata", () => {
    render();
    expect(host.querySelector('section[aria-label="Managed"]')).not.toBeNull();
    expect(host.textContent).not.toContain("Managed");
    expect(host.textContent).toContain("Local - read only");
    expect(host.textContent).toContain("Release coordination");
    expect(host.textContent).toContain("running");
    expect(host.querySelector('.webui2-session-status-icon[data-status="running"][aria-label="running"]')).not.toBeNull();
    expect(host.querySelector(".webui2-session-select")?.textContent).toBe("Release coordination");
    expect(host.querySelector(".webui2-session-row code")).toBeNull();
    expect(host.querySelector(".webui2-session-row time")).toBeNull();
    expect(host.textContent).toContain("Local sessions are read only");
  });

  it("shows full metadata on keyboard focus and dismisses on Escape", () => {
    render();
    const button = host.querySelector<HTMLButtonElement>(".webui2-session-select");
    act(() => button?.focus());
    const preview = document.querySelector('[role="tooltip"]');
    expect(preview?.textContent).toContain("Release coordination");
    expect(preview?.textContent).toContain("Source: desktop session");
    expect(preview?.querySelector("code")?.textContent).toBe(sessions[0].ref);
    expect(preview?.querySelector("time")?.dateTime).toBe(sessions[0].updatedAt);
    expect(button?.getAttribute("aria-describedby")).toBe(preview?.id);
    act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    expect(document.activeElement).toBe(button);
  });

  it("opens metadata explicitly from the menu without selecting the session", () => {
    render();
    const row = host.querySelector(".webui2-session-row");
    act(() => row?.querySelector<HTMLButtonElement>(".webui2-session-actions > button")?.click());
    const details = Array.from(row?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []).find((button) => button.textContent === "Session details");
    act(() => details?.click());
    expect(document.querySelector('[role="tooltip"] time')).not.toBeNull();
    expect(select).not.toHaveBeenCalled();
    expect(host.querySelector('[role="menu"]')).toBeNull();
    act(() => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
  });

  it("sets whole-row selection and animates only active statuses", () => {
    render();
    expect(host.querySelector(".webui2-session-row")?.getAttribute("data-selected")).toBe("true");
    expect(host.querySelector('[data-status="running"]')?.getAttribute("data-motion")).toBe("spin");
    expect(host.querySelector('[data-status="completed"]')?.getAttribute("data-motion")).toBeNull();
    expect(host.querySelector(".webui2-session-title-wrap")?.nextElementSibling?.classList.contains("webui2-session-status-icon")).toBe(true);
  });

  it("labels Feishu sessions without changing their selection behavior", () => {
    render([{ ...sessions[0], channel: { provider: "feishu", accountKey: "feishu-primary" } }]);
    const label = host.querySelector(".webui2-session-channel");
    expect(label?.textContent).toBe("Feishu");
    expect(label?.getAttribute("data-provider")).toBe("feishu");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-session-select")?.focus());
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain("Source: Feishu channel");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-session-select")?.click());
    expect(select).toHaveBeenCalledWith("tenant:alpha");
  });

  it("moves settings into the account menu without adding a fixed-height launcher", () => {
    render();
    expect(host.querySelector(".webui2-settings-launcher")).toBeNull();
    const account = host.querySelector<HTMLButtonElement>(".webui2-account-trigger");
    expect(account?.textContent).toContain("webui-local");
    act(() => account?.click());
    expect(host.querySelector('[role="menu"]')).not.toBeNull();
    const settings = host.querySelector<HTMLButtonElement>('[role="menuitem"]');
    expect(settings?.textContent).toBe("System settings");
    act(() => settings?.click());
    expect(openSettings).toHaveBeenCalledTimes(1);
    expect(host.querySelector('[role="menu"]')).toBeNull();
  });

  it("adds a workspace-scoped create action without toggling the workspace", () => {
    const items = sessions.map((session) => ({ ...session, cwd: "/work/project" }));
    render(items);
    const toggle = host.querySelector<HTMLButtonElement>('.webui2-workspace-toggle[title="/work/project"]');
    const create = host.querySelector<HTMLButtonElement>('[aria-label="New session in project"]');
    expect(create).not.toBeNull();
    expect(host.querySelector('.webui2-workspace-group[data-collapsed="false"] .webui2-workspace-new')).not.toBeNull();
    act(() => create?.click());
    expect(createSessionInWorkspace).toHaveBeenCalledWith("/work/project");
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector('[aria-label="New session in local"]')).toBeNull();
  });

  it("groups by full workspace path and includes that path in search", () => {
    const items = sessions.map((session, index) => ({ ...session, cwd: index === 0 ? "/work/service" : "/other/service" }));
    render(items);
    expect(host.querySelectorAll('.webui2-workspace-toggle[title="/work/service"]')).toHaveLength(1);
    expect(host.querySelector('.webui2-workspace-path[title="/work/service"]')?.textContent).toBe("/work/service");
    filters = { query: "/work/", statuses: [] };
    render(items);
    expect(host.querySelectorAll(".webui2-session-row")).toHaveLength(1);
    expect(host.textContent).toContain("Release coordination");
  });

  it("expands and collapses workspaces and persists the disclosure state", () => {
    const items = sessions.map((session) => ({ ...session, cwd: "/work/service" }));
    render(items);
    const toggle = host.querySelector<HTMLButtonElement>('.webui2-workspace-toggle[title="/work/service"]');
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelectorAll(".webui2-session-row")).toHaveLength(3);

    act(() => toggle?.click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelectorAll(".webui2-session-row")).toHaveLength(1);
    expect(window.localStorage.getItem("niuma-webui.v2.workspace-collapse.v1")).toContain("tenant:%2Fwork%2Fservice");

    act(() => toggle?.click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelectorAll(".webui2-session-row")).toHaveLength(3);
  });

  it("automatically reopens the selected workspace after it was collapsed", () => {
    const items = sessions.map((session, index) => ({ ...session, cwd: index === 0 ? "/work/service" : "/other/service" }));
    render(items);
    const firstToggle = host.querySelector<HTMLButtonElement>('.webui2-workspace-toggle[title="/work/service"]');
    act(() => firstToggle?.click());
    expect(firstToggle?.getAttribute("aria-expanded")).toBe("false");

    selected = "tenant:beta";
    render(items);
    expect(host.querySelector<HTMLButtonElement>('.webui2-workspace-toggle[title="/work/service"]')?.getAttribute("aria-expanded")).toBe("false");
    selected = "tenant:alpha";
    render(items);
    expect(host.querySelector<HTMLButtonElement>('.webui2-workspace-toggle[title="/work/service"]')?.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelectorAll(".webui2-session-row")).toHaveLength(3);
  });

  it("temporarily hides the retained status filter controls", () => {
    renderControlled();
    const disclosure = host.querySelector<HTMLDetailsElement>(".webui2-status-filter-disclosure");
    const summary = disclosure?.querySelector<HTMLElement>("summary");

    expect(disclosure?.open).toBe(false);
    expect(disclosure?.hidden).toBe(true);
    expect(summary?.getAttribute("aria-label")).toBe("Filter status");
    expect(summary?.textContent).toContain("All statuses");

  });

  it("keeps search as a dedicated toolbar action", () => {
    renderControlled();
    const toggle = host.querySelector<HTMLButtonElement>('button[aria-label="Search sessions"]');
    expect(host.querySelectorAll('button[aria-label="Search sessions"]')).toHaveLength(1);
    expect(host.querySelector(".webui2-sidebar-toolbar")?.contains(toggle)).toBe(true);
    expect(host.querySelector(".webui2-new-session")).not.toBeNull();
    expect(toggle?.textContent).toBe("");
    expect(toggle?.title).toBe("Search sessions");
    expect(host.querySelector('.webui2-sidebar-bottom button[aria-label="Search sessions"]')).toBeNull();
  });

  it("exposes the hidden-sidebar action without changing session filters", () => {
    renderControlled();
    expect(host.querySelector('button[aria-label="Hide sidebar"]')).not.toBeNull();
    expect(host.querySelector('button[aria-label="Search sessions"]')).not.toBeNull();
  });

  it("resizes between 232 and 380 pixels with keyboard controls and persists width", () => {
    render();
    const sidebar = host.querySelector<HTMLElement>(".webui2-sidebar");
    const handle = host.querySelector<HTMLElement>(".webui2-sidebar-resize");

    expect(handle?.getAttribute("aria-valuemin")).toBe("232");
    expect(handle?.getAttribute("aria-valuemax")).toBe("380");
    act(() => handle?.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true })));
    expect(sidebar?.style.width).toBe("380px");
    expect(window.localStorage.getItem("niuma-webui.v2.sidebar-width.v1")).toBe("380");
    act(() => handle?.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true })));
    expect(sidebar?.style.width).toBe("232px");

    const pointerDown = new Event("pointerdown", { bubbles: true });
    const pointerMove = new Event("pointermove", { bubbles: true });
    Object.defineProperty(pointerDown, "clientX", { value: 200 });
    Object.defineProperty(pointerMove, "clientX", { value: 260 });
    act(() => handle?.dispatchEvent(pointerDown));
    act(() => window.dispatchEvent(pointerMove));
    expect(sidebar?.style.width).toBe("292px");
  });

  it("opens session commands from an accessible overflow menu and closes it naturally", async () => {
    render();
    const trigger = host.querySelector<HTMLButtonElement>('button[aria-label="Actions for tenant:alpha"]');
    expect(trigger?.getAttribute("aria-expanded")).toBe("false");
    act(() => trigger?.click());
    expect(trigger?.getAttribute("aria-expanded")).toBe("true");

    const copy = host.querySelector<HTMLButtonElement>('button[role="menuitem"][aria-label="Copy tenant:alpha"]');
    await act(async () => copy?.click());
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("tenant:alpha");
    expect(host.querySelector('[role="menu"]')).toBeNull();

    act(() => trigger?.click());
    const link = host.querySelector<HTMLAnchorElement>('a[role="menuitem"][aria-label="Open tenant:alpha"]');
    expect(link?.getAttribute("href")).toBe("/webui/v2/sessions/tenant%3Aalpha");
    act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(host.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("preserves selection and drag behavior while the overflow menu is closed", () => {
    render();
    const row = host.querySelector<HTMLElement>(".webui2-session-row");
    const selectButton = row?.querySelector<HTMLButtonElement>(".webui2-session-select");
    act(() => selectButton?.click());
    expect(select).toHaveBeenCalledWith("tenant:alpha");

    const handle = host.querySelector<HTMLElement>('[aria-label="Drag tenant:alpha as context"]');
    act(() => handle?.dispatchEvent(new Event("dragstart", { bubbles: true })));
    expect(drag).toHaveBeenCalledWith("tenant:alpha");
  });

  it("keeps touch selection working without opening the hover preview", () => {
    render();
    const button = host.querySelector<HTMLButtonElement>(".webui2-session-select")!;
    const pointerDown = new Event("pointerdown", { bubbles: true });
    Object.defineProperty(pointerDown, "pointerType", { value: "touch" });

    act(() => {
      button.dispatchEvent(pointerDown);
      button.focus();
    });

    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    act(() => button.click());
    expect(select).toHaveBeenCalledWith("tenant:alpha");
  });

  it("drags from the session title without selecting it and keeps normal clicks working", () => {
    render();
    const title = host.querySelector<HTMLElement>(".webui2-session-title");
    const button = host.querySelector<HTMLButtonElement>(".webui2-session-select");
    const setData = vi.fn();
    const transfer = { setData, effectAllowed: "none" };
    const event = new Event("dragstart", { bubbles: true });
    Object.defineProperty(event, "dataTransfer", { value: transfer });
    act(() => title?.dispatchEvent(event));
    expect(button?.draggable).toBe(true);
    expect(setData).toHaveBeenCalledWith(SESSION_REF_MIME_TYPE, "tenant:alpha");
    expect(transfer.effectAllowed).toBe("copy");
    act(() => button?.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 1 })));
    expect(select).not.toHaveBeenCalled();
    act(() => button?.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    act(() => button?.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 1 })));
    expect(select).toHaveBeenCalledWith("tenant:alpha");
  });
});
