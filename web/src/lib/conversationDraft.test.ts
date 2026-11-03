import { beforeEach, describe, expect, it } from "vitest";
import { clearConversationDraft, draftStorageKey, readConversationDraft, writeConversationDraft, type ConversationDraft } from "./conversationDraft";

describe("conversation drafts", () => {
  const storage = new Map<string, string>();

  beforeEach(() => {
    storage.clear();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage.get(key) || null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key)
      }
    });
  });

  it("round-trips text and attachment references per surface/session/workspace", () => {
    const draft: ConversationDraft = {
      surface: "web-agent",
      sessionKey: "session:42",
      workspace: "/repo",
      text: "finish the migration",
      assetIDs: ["asset-1"],
      updatedAt: "2026-08-31T00:00:00Z"
    };
    writeConversationDraft(draft);
    expect(readConversationDraft("web-agent", "session:42", "/repo")).toMatchObject({ ...draft, schemaVersion: 1 });
    expect(readConversationDraft("web-agent", "session:43", "/repo")).toBeNull();
    expect(draftStorageKey("web-agent", "session:42", "/repo")).not.toContain("finish the migration");
  });

  it("clears only the requested draft", () => {
    writeConversationDraft({ surface: "web-agent", sessionKey: "session:42", workspace: "/repo", text: "a", updatedAt: "now" });
    writeConversationDraft({ surface: "web-agent", sessionKey: "session:43", workspace: "/repo", text: "b", updatedAt: "now" });
    clearConversationDraft("web-agent", "session:42", "/repo");
    expect(readConversationDraft("web-agent", "session:42", "/repo")).toBeNull();
    expect(readConversationDraft("web-agent", "session:43", "/repo")?.text).toBe("b");
  });
});
