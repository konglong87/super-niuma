const INSPECTOR_PREFERENCE_KEY = "niuma-webui.v2.inspector-default";

export function loadInspectorPreference(): boolean {
  try { return window.localStorage.getItem(INSPECTOR_PREFERENCE_KEY) === "true"; } catch { return false; }
}

export function saveInspectorPreference(open: boolean): void {
  try { window.localStorage.setItem(INSPECTOR_PREFERENCE_KEY, String(open)); } catch { /* Optional browser preference. */ }
}
