import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, apiRequest, promoteGlobalSettingsProvider } from "../../lib/api";
import type { GlobalSettingsResponse, IdentityConfig, SettingsDoc } from "../../lib/types";

export const SETTINGS_ENDPOINT = "/runtime/settings";
export const SETTINGS_SECRET_SENTINEL = "••••••";
export const DESKTOP_CONFIG_VERIFIED_KEY = "niuma.desktop.config-verified.v1";
export const GLOBAL_SETTINGS_SAVED_EVENT = "niuma:global-settings-saved";
const SETTINGS_CONFLICT_MESSAGE = "服务端配置已变更。当前草稿已保留，请重新载入最新配置后再修改。";
export type SettingsPath = readonly (string | number)[];
export type SettingsIssue = { field: string; code: string; message: string };
export type SettingsValidation = { valid: boolean; issues: SettingsIssue[] };
export type SettingsConnection = { ok: boolean; kind: "catalog_connection"; status_code?: number; message: string };
type SettingsSnapshot = GlobalSettingsResponse & { revision?: string };
type DraftState = {
  snapshot?: SettingsSnapshot;
  raw: string;
  doc: SettingsDoc | null;
  syntaxError: string;
  error: string;
  conflict: boolean;
  loading: boolean;
  operation: "" | "validate" | "save" | "test" | "promote";
  validation: SettingsValidation | null;
  connection: (SettingsConnection & { provider?: string }) | null;
  saved: boolean;
  promotedProviderIndex: number | null;
};

export function parseSettingsDraft(raw: string): { doc: SettingsDoc | null; syntaxError: string } {
  try {
    const doc: unknown = JSON.parse(raw);
    if (!doc || typeof doc !== "object" || Array.isArray(doc)) return { doc: null, syntaxError: "配置根节点必须是 JSON 对象。" };
    return { doc: doc as SettingsDoc, syntaxError: "" };
  } catch (error) {
    // Browser JSON errors can include credential text. Only expose the position.
    const message = error instanceof Error ? error.message : "";
    const position = /position (\d+)/.exec(message);
    const line = /line (\d+) column (\d+)/.exec(message);
    const location = line ? `第 ${line[1]} 行，第 ${line[2]} 列` : position ? `字符 ${position[1]}` : "请检查引号、逗号和括号";
    return { doc: null, syntaxError: `JSON 语法错误：${location}。` };
  }
}

export function settingsValue(doc: unknown, path: SettingsPath): unknown {
  return path.reduce<unknown>((node, key) => node && typeof node === "object" ? (node as Record<string | number, unknown>)[key] : undefined, doc);
}

export function updateSettingsValue(doc: SettingsDoc, path: SettingsPath, value: unknown): SettingsDoc {
  function update(node: unknown, depth: number): unknown {
    if (depth === path.length) return value;
    const key = path[depth];
    if (typeof key === "number") {
      const next = Array.isArray(node) ? [...node] : [];
      next[key] = update(next[key], depth + 1);
      return next;
    }
    const next = node && typeof node === "object" && !Array.isArray(node) ? { ...node as SettingsDoc } : {};
    const child = update(Object.hasOwn(next, key) ? next[key] : undefined, depth + 1);
    if (child === undefined) delete next[key];
    else Object.defineProperty(next, key, { value: child, enumerable: true, configurable: true, writable: true });
    return next;
  }
  return update(doc, 0) as SettingsDoc;
}

const initialState = (): DraftState => ({ raw: "", doc: null, syntaxError: "", error: "", conflict: false, loading: false, operation: "", validation: null, connection: null, saved: false, promotedProviderIndex: null });
const desktopBuild = import.meta.env.VITE_DESKTOP_UI_VERSION === "2";
const serialize = (doc: SettingsDoc) => JSON.stringify(doc, null, 2);
const snapshotState = (snapshot: SettingsSnapshot): DraftState => ({ ...initialState(), snapshot, raw: serialize(snapshot.doc), doc: snapshot.doc });

function requestError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401 || error.status === 403) return "无权访问全局设置，请检查连接身份和管理权限。";
    if (error.status === 409) return SETTINGS_CONFLICT_MESSAGE;
    return `配置请求失败（HTTP ${error.status}），请重试。`;
  }
  return "无法连接配置服务，请检查网络后重试。";
}

export function useGlobalSettingsDraft(identity: IdentityConfig, enabled = true) {
  const [state, setState] = useState<DraftState>(initialState);
  const stateRef = useRef(state);
  stateRef.current = state;
  const identityRef = useRef(identity);
  identityRef.current = identity;
  const generation = useRef(0);
  const active = useRef<AbortController | null>(null);
  const operationLock = useRef(false);
  const identityKey = JSON.stringify([identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId, identity.deviceId]);
  const loadedIdentity = useRef(identityKey);

  const reload = useCallback(async () => {
    active.current?.abort();
    operationLock.current = false;
    const controller = new AbortController();
    active.current = controller;
    const current = ++generation.current;
    setState((previous) => ({ ...previous, loading: true, error: "", operation: "" }));
    try {
      const snapshot = await apiRequest<SettingsSnapshot>(identityRef.current, SETTINGS_ENDPOINT, { signal: controller.signal });
      if (current === generation.current) setState(snapshotState(snapshot));
    } catch (error) {
      if (current === generation.current && !controller.signal.aborted) setState((previous) => ({ ...previous, loading: false, error: requestError(error) }));
    }
  }, []);

  useEffect(() => {
    const changedIdentity = loadedIdentity.current !== identityKey;
    loadedIdentity.current = identityKey;
    if (changedIdentity) {
      ++generation.current;
      active.current?.abort();
      operationLock.current = false;
      setState(initialState());
    }
    // Leaving the document tabs must keep even an invalid draft in memory.
    if (enabled && (changedIdentity || (!stateRef.current.snapshot && !stateRef.current.loading))) void reload();
  }, [identityKey, enabled, reload]);

  useEffect(() => () => { ++generation.current; active.current?.abort(); }, []);

  const setRaw = useCallback((raw: string) => setState((previous) => {
    if (previous.loading || previous.operation || !previous.snapshot) return previous;
    if (desktopBuild) localStorage.removeItem(DESKTOP_CONFIG_VERIFIED_KEY);
    return { ...previous, raw, ...parseSettingsDraft(raw), validation: null, connection: null, error: previous.conflict ? SETTINGS_CONFLICT_MESSAGE : "", saved: false, promotedProviderIndex: null };
  }), []);

  const setField = useCallback((path: SettingsPath, value: unknown) => setState((previous) => {
    if (!previous.doc || previous.syntaxError || previous.loading || previous.operation || !previous.snapshot) return previous;
    if (desktopBuild) localStorage.removeItem(DESKTOP_CONFIG_VERIFIED_KEY);
    const doc = updateSettingsValue(previous.doc, path, value);
    const invalidatesPromotion = path.length > 0 && ["provider", "providerProtocol", "baseURL", "apiKey", "authToken", "model", "responses", "fallback"].includes(String(path[0]));
    return { ...previous, doc, raw: serialize(doc), validation: null, connection: null, error: previous.conflict ? SETTINGS_CONFLICT_MESSAGE : "", saved: false, promotedProviderIndex: invalidatesPromotion ? null : previous.promotedProviderIndex };
  }), []);

  const reset = useCallback(() => setState((previous) => previous.snapshot && !previous.operation && !previous.loading
    ? { ...snapshotState(previous.snapshot), conflict: previous.conflict, error: previous.conflict ? previous.error : "" }
    : previous), []);

  const run = useCallback(async (operation: "validate" | "save"): Promise<boolean> => {
    const draft = stateRef.current;
    if (!draft.doc || draft.syntaxError || draft.loading || draft.operation || operationLock.current || !draft.snapshot || (operation === "save" && draft.conflict)) return false;
    operationLock.current = true;
    const controller = new AbortController();
    active.current = controller;
    const current = ++generation.current;
    let committed = false;
    setState((previous) => ({ ...previous, operation, error: previous.conflict ? SETTINGS_CONFLICT_MESSAGE : "", validation: null, saved: false }));
    try {
      const promotionHeaders: Record<string, string> = draft.promotedProviderIndex !== null
        ? { "X-Settings-Promoted-Provider-Index": String(draft.promotedProviderIndex) }
        : {};
      const validation = await apiRequest<SettingsValidation>(identityRef.current, `${SETTINGS_ENDPOINT}/validate`, {
        method: "POST",
        body: draft.doc,
        signal: controller.signal,
        headers: promotionHeaders,
      });
      if (current !== generation.current) return false;
      setState((previous) => ({ ...previous, validation }));
      if (!validation.valid || operation === "validate") return validation.valid;
      const saved = await apiRequest<{ revision?: string }>(identityRef.current, SETTINGS_ENDPOINT, {
        method: "PUT", body: draft.doc, signal: controller.signal,
        headers: {
          ...(draft.snapshot.revision ? { "If-Match": draft.snapshot.revision } : {}),
          ...promotionHeaders,
        },
      });
      committed = true;
      // Only a successful server readback replaces the draft and masked values.
      const snapshot = await apiRequest<SettingsSnapshot>(identityRef.current, SETTINGS_ENDPOINT, { signal: controller.signal });
      if (current !== generation.current) return false;
      if (saved.revision && saved.revision !== snapshot.revision) {
        setState((previous) => ({ ...previous, conflict: true, error: "保存后配置再次发生变化，当前草稿已保留，请重新载入确认。" }));
        return false;
      }
      setState({ ...snapshotState(snapshot), saved: true });
      window.dispatchEvent(new Event(GLOBAL_SETTINGS_SAVED_EVENT));
      return true;
    } catch (error) {
      if (current === generation.current && !controller.signal.aborted) setState((previous) => ({ ...previous, error: committed ? "保存请求已提交，但读取确认失败。当前草稿已保留，请重新载入确认。" : requestError(error), conflict: error instanceof ApiError && error.status === 409 || previous.conflict }));
      return false;
    } finally {
      if (current === generation.current) {
        operationLock.current = false;
        setState((previous) => ({ ...previous, operation: "" }));
      }
    }
  }, []);

  const testProvider = useCallback(async (provider?: string): Promise<boolean> => {
    const draft = stateRef.current;
    // Only undefined selects the primary route. Empty named rows cannot fall back.
    if (provider !== undefined && !provider.trim()) return false;
    if (!draft.doc || draft.syntaxError || draft.loading || draft.operation || operationLock.current || !draft.snapshot) return false;
    operationLock.current = true;
    const controller = new AbortController();
    active.current = controller;
    const current = ++generation.current;
    setState((previous) => ({ ...previous, operation: "test", error: previous.conflict ? SETTINGS_CONFLICT_MESSAGE : "", connection: null }));
    try {
      const connection = await apiRequest<SettingsConnection>(identityRef.current, `${SETTINGS_ENDPOINT}/test-provider`, { method: "POST", body: { doc: draft.doc, ...(provider ? { provider } : {}) }, signal: controller.signal });
      if (current !== generation.current) return false;
      setState((previous) => ({ ...previous, connection: { ...connection, provider } }));
      if (connection.ok && desktopBuild) localStorage.setItem(DESKTOP_CONFIG_VERIFIED_KEY, "true");
      return connection.ok;
    } catch (error) {
      if (current === generation.current && !controller.signal.aborted) setState((previous) => ({ ...previous, error: requestError(error) }));
      return false;
    } finally {
      if (current === generation.current) {
        operationLock.current = false;
        setState((previous) => ({ ...previous, operation: "" }));
      }
    }
  }, []);

  const promoteProvider = useCallback(async (providerIndex: number): Promise<boolean> => {
    const draft = stateRef.current;
    if (!draft.doc || draft.syntaxError || draft.loading || draft.operation || operationLock.current || !draft.snapshot || !Number.isInteger(providerIndex) || providerIndex < 0) return false;
    operationLock.current = true;
    const controller = new AbortController();
    active.current = controller;
    const current = ++generation.current;
    setState((previous) => ({ ...previous, operation: "promote", error: "", validation: null, connection: null, saved: false }));
    try {
      const promoted = await promoteGlobalSettingsProvider(identityRef.current, draft.doc, providerIndex, controller.signal);
      if (current !== generation.current) return false;
      setState((previous) => ({ ...previous, doc: promoted.doc, raw: serialize(promoted.doc), error: "", validation: null, connection: null, saved: false, promotedProviderIndex: providerIndex }));
      return true;
    } catch (error) {
      if (current === generation.current && !controller.signal.aborted) setState((previous) => ({ ...previous, error: requestError(error) }));
      return false;
    } finally {
      if (current === generation.current) {
        operationLock.current = false;
        setState((previous) => ({ ...previous, operation: "" }));
      }
    }
  }, []);

  return {
    ...state,
    loaded: Boolean(state.snapshot),
    path: state.snapshot?.path ?? "",
    revision: state.snapshot?.revision,
    requiresRestart: state.saved,
    masked: state.snapshot?.masked ?? [],
    dirty: Boolean(state.snapshot) && state.raw !== serialize(state.snapshot!.doc),
    busy: state.loading || Boolean(state.operation),
    setRaw, setField, reset, reload, testProvider, promoteProvider,
    validate: useCallback(() => run("validate"), [run]),
    save: useCallback(() => run("save"), [run]),
  };
}

export type GlobalSettingsDraft = ReturnType<typeof useGlobalSettingsDraft>;
