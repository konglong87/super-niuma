const DRAFT_SCHEMA_VERSION = 1;
const DRAFT_STORAGE_PREFIX = "niuma-webui.conversation-draft.v1";

export type ConversationDraft = {
  surface: string;
  sessionKey: string;
  workspace: string;
  text: string;
  assetIDs?: string[];
  updatedAt: string;
  schemaVersion?: number;
};

export function draftStorageKey(surface: string, sessionKey: string, workspace: string): string {
  return [DRAFT_STORAGE_PREFIX, encodePart(surface), encodePart(sessionKey), encodePart(workspace)].join(":");
}

export function readConversationDraft(surface: string, sessionKey: string, workspace: string): ConversationDraft | null {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    const raw = window.localStorage.getItem(draftStorageKey(surface, sessionKey, workspace));
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<ConversationDraft>;
    if (parsed.schemaVersion !== undefined && parsed.schemaVersion !== DRAFT_SCHEMA_VERSION) {
      return null;
    }
    if (parsed.surface !== surface || parsed.sessionKey !== sessionKey || parsed.workspace !== workspace || typeof parsed.text !== "string") {
      return null;
    }
    return {
      surface,
      sessionKey,
      workspace,
      text: parsed.text,
      assetIDs: Array.isArray(parsed.assetIDs) ? parsed.assetIDs.filter((value): value is string => typeof value === "string") : undefined,
      updatedAt: typeof parsed.updatedAt === "string" ? parsed.updatedAt : "",
      schemaVersion: DRAFT_SCHEMA_VERSION
    };
  } catch {
    return null;
  }
}

export function writeConversationDraft(draft: ConversationDraft): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    const normalized: ConversationDraft = {
      ...draft,
      surface: draft.surface.trim(),
      sessionKey: draft.sessionKey.trim(),
      workspace: draft.workspace.trim(),
      text: draft.text,
      assetIDs: draft.assetIDs?.filter((value) => value.trim() !== ""),
      updatedAt: draft.updatedAt || new Date().toISOString(),
      schemaVersion: DRAFT_SCHEMA_VERSION
    };
    if (normalized.surface === "" || normalized.sessionKey === "" || normalized.workspace === "" || (normalized.text.trim() === "" && !normalized.assetIDs?.length)) {
      clearConversationDraft(normalized.surface, normalized.sessionKey, normalized.workspace);
      return;
    }
    window.localStorage.setItem(draftStorageKey(normalized.surface, normalized.sessionKey, normalized.workspace), JSON.stringify(normalized));
  } catch {
    // Local storage is best effort; the composer remains usable in private mode.
  }
}

export function clearConversationDraft(surface: string, sessionKey: string, workspace: string): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.removeItem(draftStorageKey(surface, sessionKey, workspace));
  } catch {
    // Ignore storage failures.
  }
}

function encodePart(value: string): string {
  return encodeURIComponent(value.trim()).replace(/%/g, "~");
}
