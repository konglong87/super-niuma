import { clearConversationDraft, readConversationDraft, writeConversationDraft } from "../../lib/conversationDraft";
import type { IdentityConfig } from "../../lib/types";
import { parseWebUIV2Route } from "../routes";
import type { SessionRef } from "../types";

const DRAFT_SURFACE = "webui-v2";
const DRAFT_PAYLOAD_VERSION = 1;
const DEFAULT_WORKSPACE = "<default-workspace>";

export type ComposerDraftScope = { identity: IdentityConfig; targetRef: SessionRef; cwd: string };
type StoredComposerDraft = { text: string; sourceRefs: SessionRef[]; skillName?: string };

export function composerDraftScopeKey({ identity, targetRef }: ComposerDraftScope): string {
  return JSON.stringify([identity.apiBase, identity.tenantKey, identity.userId, targetRef]);
}

export function composerDraftMemoryKey(scope: ComposerDraftScope): string {
  return JSON.stringify([composerDraftScopeKey(scope), scope.cwd.trim() || DEFAULT_WORKSPACE]);
}

export function readComposerDraft(scope: ComposerDraftScope): StoredComposerDraft | null {
  const draft = readConversationDraft(DRAFT_SURFACE, composerDraftScopeKey(scope), scope.cwd.trim() || DEFAULT_WORKSPACE);
  if (!draft) return null;
  try {
    const payload = JSON.parse(draft.text) as { version?: unknown; text?: unknown; sourceRefs?: unknown; skillName?: unknown };
    if (payload.version !== DRAFT_PAYLOAD_VERSION || typeof payload.text !== "string" || !Array.isArray(payload.sourceRefs)) return null;
    const sourceRefs = payload.sourceRefs.flatMap((ref) => {
      if (typeof ref !== "string") return [];
      const route = parseWebUIV2Route(`/webui/v2/sessions/${encodeURIComponent(ref)}`);
      return route.kind === "session" && route.ref !== scope.targetRef ? [route.ref] : [];
    });
    return { text: payload.text, sourceRefs: [...new Set(sourceRefs)], ...(typeof payload.skillName === "string" && payload.skillName.trim() ? { skillName: payload.skillName.trim() } : {}) };
  } catch {
    return null;
  }
}

export function writeComposerDraft(scope: ComposerDraftScope, draft: StoredComposerDraft): void {
  if (!draft.text.trim() && draft.sourceRefs.length === 0 && !draft.skillName?.trim()) {
    clearComposerDraft(scope);
    return;
  }
  // A versioned text envelope reuses the shared storage contract. Browser File objects
  // and temporary upload URLs never become persisted attachment claims.
  writeConversationDraft({
    surface: DRAFT_SURFACE,
    sessionKey: composerDraftScopeKey(scope),
    workspace: scope.cwd.trim() || DEFAULT_WORKSPACE,
    text: JSON.stringify({ version: DRAFT_PAYLOAD_VERSION, text: draft.text, sourceRefs: draft.sourceRefs, ...(draft.skillName?.trim() ? { skillName: draft.skillName.trim() } : {}) }),
    updatedAt: new Date().toISOString()
  });
}

export function clearComposerDraft(scope: ComposerDraftScope): void {
  clearConversationDraft(DRAFT_SURFACE, composerDraftScopeKey(scope), scope.cwd.trim() || DEFAULT_WORKSPACE);
}
