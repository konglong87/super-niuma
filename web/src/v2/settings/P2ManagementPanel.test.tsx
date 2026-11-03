import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { getLocalSkill, getTenantSkill, listLocalSkills, listMemories, listTenantSkills, rollbackTenantSkill, saveMemory, saveSkill, saveTenantSkill } from "../../lib/api";
import { I18nProvider } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { P2ManagementPanel } from "./P2ManagementPanel";

vi.mock("../../lib/api", () => ({
  getLocalSkill: vi.fn(), getTenantSkill: vi.fn(), listLocalSkills: vi.fn(), listMemories: vi.fn(), listTenantSkills: vi.fn(),
  rollbackTenantSkill: vi.fn(), saveMemory: vi.fn(), saveSkill: vi.fn(), saveTenantSkill: vi.fn()
}));
const identity: IdentityConfig = { apiBase: "/api", apiToken: "token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.mocked(listMemories).mockResolvedValue([]);
  vi.mocked(listLocalSkills).mockResolvedValue([]);
  vi.mocked(listTenantSkills).mockResolvedValue([]);
  vi.mocked(getLocalSkill).mockResolvedValue({ name: "detail", path: "/detail/SKILL.md", content: "# Detail" });
  vi.mocked(getTenantSkill).mockResolvedValue({ skill_key: "review", name: "Review", version: 2, enabled: true, content_md: "# Review" });
  vi.mocked(rollbackTenantSkill).mockResolvedValue({ id: 9, skill_key: "review", from_version: 1, version: 3 });
  vi.mocked(saveMemory).mockResolvedValue(undefined);
  vi.mocked(saveSkill).mockResolvedValue(undefined);
  vi.mocked(saveTenantSkill).mockResolvedValue(undefined);
  host = document.createElement("div");
  document.body.replaceChildren(host);
  root = createRoot(host);
});

it("shows local and tenant skills in separate groups", async () => {
  vi.mocked(listLocalSkills).mockResolvedValue([{
    name: "anysearch",
    path: "/Users/test/.claude/skills/anysearch/SKILL.md",
    description: "Search the web",
    source: "user",
    plugin: "browser-pack"
  }]);
  vi.mocked(listTenantSkills).mockResolvedValue([{ skill_key: "review", name: "Review", version: 2, enabled: true }]);

  await render("skills");

  expect(listLocalSkills).toHaveBeenCalledWith(identity);
  expect(listTenantSkills).toHaveBeenCalledWith(identity);
  expect(host.textContent).toContain("Local skills");
  expect(host.textContent).toContain("anysearch");
  expect(host.textContent).toContain("/Users/test/.claude/skills/anysearch/SKILL.md");
  expect(host.textContent).toContain("Tenant skills");
  expect(host.textContent).toContain("review");
});

it("loads local skill details without offering filesystem deletion", async () => {
  vi.mocked(listLocalSkills).mockResolvedValue([{
    name: "anysearch",
    path: "/Users/test/.claude/skills/anysearch/SKILL.md",
    description: "Search the web",
    source: "user"
  }]);
  vi.mocked(getLocalSkill).mockResolvedValue({
    name: "anysearch",
    path: "/Users/test/.claude/skills/anysearch/SKILL.md",
    source: "user",
    content: "---\nname: anysearch\n---\n\n# Search"
  });

  await render("skills");
  const viewButton = Array.from(editor().querySelectorAll("button")).find((item) => item.textContent?.includes("View details"))!;
  await act(async () => viewButton.click());
  expect(getLocalSkill).toHaveBeenCalledWith(identity, "anysearch");
  expect(host.textContent).toContain("# Search");
  expect(host.textContent).toContain("use the CLI or plugin manager to install or uninstall");
  expect(host.textContent).not.toContain("Delete");
});

it("shows tenant versions and supports detail, disable, and rollback actions", async () => {
  vi.mocked(listTenantSkills).mockResolvedValue([
    { skill_key: "review", name: "Review", version: 2, enabled: true, content_md: "# Current" },
    { skill_key: "review", name: "Review", version: 1, enabled: true, content_md: "# Old" }
  ]);
  vi.mocked(getTenantSkill).mockResolvedValue({ skill_key: "review", name: "Review", version: 2, enabled: true, content_md: "# Current" });

  await render("skills");
  expect(host.textContent).toContain("Current v2");
  expect(host.textContent).toContain("History v1");

  const viewButton = Array.from(editor().querySelectorAll("button")).find((item) => item.textContent?.includes("View details"))!;
  await act(async () => viewButton.click());
  expect(getTenantSkill).toHaveBeenCalledWith(identity, "review", 2);
  expect(host.textContent).toContain("# Current");

  const disableButton = Array.from(editor().querySelectorAll("button")).find((item) => item.textContent?.includes("Disable"))!;
  await act(async () => disableButton.click());
  expect(saveTenantSkill).toHaveBeenCalledWith(identity, expect.objectContaining({ skill_key: "review", version: 2, enabled: false }));

  vi.stubGlobal("confirm", vi.fn(() => true));
  const rollbackButton = Array.from(editor().querySelectorAll("button")).find((item) => item.textContent?.includes("Roll back to v1"))!;
  await act(async () => rollbackButton.click());
  expect(rollbackTenantSkill).toHaveBeenCalledWith(identity, "review", 1);
});
afterEach(() => { act(() => root.unmount()); vi.resetAllMocks(); vi.unstubAllGlobals(); });

async function render(section: "memory" | "skills", currentIdentity = identity) {
  await act(async () => root.render(<I18nProvider><P2ManagementPanel identity={currentIdentity} section={section} /></I18nProvider>));
}
function editor() { return host.querySelector<HTMLDivElement>("div:not([hidden]) > .p2-management-panel")!; }
function fill(selector: string, value: string) {
  const input = editor().querySelector<HTMLInputElement | HTMLTextAreaElement>(selector)!;
  const prototype = input.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  act(() => {
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
function pending<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

it("keeps unsaved Memory and Skills drafts independent in both directions", async () => {
  await render("memory");
  expect(listTenantSkills).not.toHaveBeenCalled();
  fill("input", "memory-only");
  fill("textarea", "Memory draft");
  await render("skills");
  expect(editor().querySelector("input")!.value).toBe("desktop-skill");
  expect(editor().querySelector("textarea")!.value).toBe("");
  fill("input", "skill-only");
  fill("textarea", "# Skill draft");
  await render("memory");
  expect(editor().querySelector("input")!.value).toBe("memory-only");
  expect(editor().querySelector("textarea")!.value).toBe("Memory draft");
  await render("skills");
  expect(editor().querySelector("textarea")!.value).toBe("# Skill draft");
  await act(async () => editor().querySelector<HTMLButtonElement>("button.primary")!.click());
  expect(saveMemory).not.toHaveBeenCalled();
  expect(saveSkill).toHaveBeenCalledWith(identity, expect.objectContaining({ skill_key: "skill-only", content_md: "# Skill draft" }));
  await render("memory");
  expect(editor().querySelector("textarea")!.value).toBe("Memory draft");
});

it.each(["memory", "skills"] as const)("keeps pending %s save/readback completion in its original editor", async (section) => {
  const other = section === "memory" ? "skills" : "memory";
  const write = pending<void>();
  const readback = pending<never[]>();
  const save = section === "memory" ? vi.mocked(saveMemory) : vi.mocked(saveSkill);
  const list = section === "memory" ? vi.mocked(listMemories) : vi.mocked(listTenantSkills);
  save.mockReturnValueOnce(write.promise);
  await render(section);
  fill("textarea", "Submitted draft");
  await act(async () => editor().querySelector<HTMLButtonElement>("button.primary")!.click());
  await render(other);
  fill("textarea", "Other unsaved draft");
  list.mockReturnValueOnce(readback.promise);
  await act(async () => write.resolve());
  expect(editor().querySelector("textarea")!.value).toBe("Other unsaved draft");
  expect(editor().querySelector<HTMLButtonElement>("button.primary")!.disabled).toBe(false);
  await act(async () => readback.resolve([]));
  expect(editor().querySelector("textarea")!.value).toBe("Other unsaved draft");
  expect(section === "memory" ? saveSkill : saveMemory).not.toHaveBeenCalled();
  await render(section);
  expect(editor().querySelector("textarea")!.value).toBe("");
});

it("does not move a late refresh error into the other section", async () => {
  const read = pending<never[]>();
  vi.mocked(listMemories).mockReturnValueOnce(read.promise);
  await render("memory");
  await render("skills");
  fill("textarea", "Skill stays editable");
  await act(async () => read.reject(new Error("Memory unavailable")));
  expect(editor().textContent).not.toContain("Memory unavailable");
  expect(editor().querySelector("textarea")!.value).toBe("Skill stays editable");
  await render("memory");
  expect(editor().textContent).toContain("Memory unavailable");
});

it("does not apply an old identity's pending save or readback to a new identity", async () => {
  const write = pending<void>();
  vi.mocked(saveMemory).mockReturnValueOnce(write.promise);
  await render("memory");
  fill("textarea", "Old identity");
  await act(async () => editor().querySelector<HTMLButtonElement>("button.primary")!.click());
  const next = { ...identity, userId: "other-user" };
  await render("memory", next);
  fill("textarea", "New identity");
  vi.mocked(listMemories).mockClear();
  await act(async () => write.resolve());
  expect(listMemories).not.toHaveBeenCalled();
  expect(editor().querySelector("textarea")!.value).toBe("New identity");
});
