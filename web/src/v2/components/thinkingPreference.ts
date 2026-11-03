import { useSyncExternalStore } from "react";
import { readProductStorage } from "../../lib/productStorage";
import type { ThinkingMode } from "../types";

export const THINKING_PREFERENCE_KEY = "niuma-webui.v2.thinking-mode";
const DEFAULT_THINKING_MODE: ThinkingMode = "summary";
const listeners = new Set<() => void>();

let snapshot = readStoredThinkingMode();

function isThinkingMode(value: string | null): value is ThinkingMode {
  return value === "full" || value === "summary" || value === "hidden";
}

function readStoredThinkingMode(): ThinkingMode {
  try {
    const value = readProductStorage(THINKING_PREFERENCE_KEY);
    return isThinkingMode(value) ? value : DEFAULT_THINKING_MODE;
  } catch {
    return DEFAULT_THINKING_MODE;
  }
}

function getSnapshot(): ThinkingMode {
  const stored = readStoredThinkingMode();
  if (stored !== snapshot) snapshot = stored;
  return snapshot;
}

function notify(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  const onStorage = (event: StorageEvent): void => {
    if (event.key !== THINKING_PREFERENCE_KEY) return;
    const next = readStoredThinkingMode();
    if (next === snapshot) return;
    snapshot = next;
    notify();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", onStorage);
  };
}

export function useThinkingMode(): ThinkingMode {
  return useSyncExternalStore(subscribe, getSnapshot, () => DEFAULT_THINKING_MODE);
}

export function setThinkingMode(mode: ThinkingMode): void {
  if (mode === snapshot) return;
  snapshot = mode;
  try {
    window.localStorage.setItem(THINKING_PREFERENCE_KEY, mode);
  } catch {
    // Keep the current in-memory preference when browser storage is unavailable.
  }
  notify();
}
