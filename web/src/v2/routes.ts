export type SessionRef = `tenant:${string}` | `local:${string}`;

export const SETTINGS_SECTIONS = ["general", "appearance", "pet", "models", "observability", "profiles", "feishu", "teams", "prompts", "memory", "skills", "agent", "provisioning", "session-backend", "json", "effective"] as const;
export type SettingsSection = typeof SETTINGS_SECTIONS[number];
export type WorkflowRoute = { kind: "workflows"; page: "list" | "new" | "agents" } | { kind: "workflows"; page: "edit"; key: string } | { kind: "workflows"; page: "run"; runID: string };
export type WebUIV2Route = WorkflowRoute | { kind: "index" } | { kind: "session"; ref: SessionRef } | { kind: "settings"; section: SettingsSection } | { kind: "invalid" };

const v2IndexPath = "/webui/v2";
const v2SessionsPath = `${v2IndexPath}/sessions/`;
const v2WorkflowsPath = `${v2IndexPath}/workflows`;
const v2SettingsPath = `${v2IndexPath}/settings`;
const sessionRefPattern = /^(tenant|local):.+$/;

export function parseWebUIV2Route(pathname: string, desktopV2 = false): WebUIV2Route {
  // Wails loads the desktop bundle at root; web and invalid deep links stay strict.
  if ((desktopV2 && pathname === "/") || pathname === v2IndexPath || pathname === `${v2IndexPath}/`) {
    return { kind: "index" };
  }

  if (pathname === v2WorkflowsPath || pathname === `${v2WorkflowsPath}/`) return { kind: "workflows", page: "list" };
  if (pathname.startsWith(`${v2WorkflowsPath}/`)) {
    const suffix = pathname.slice(v2WorkflowsPath.length + 1);
    if (suffix === "new" || suffix === "agents") return { kind: "workflows", page: suffix };
    const match = /^(edit|runs)\/([^/]+)$/.exec(suffix);
    if (!match) return { kind: "invalid" };
    try {
      const value = decodeURIComponent(match[2]);
      if (!value.trim()) return { kind: "invalid" };
      return match[1] === "edit" ? { kind: "workflows", page: "edit", key: value } : { kind: "workflows", page: "run", runID: value };
    } catch { return { kind: "invalid" }; }
  }

  if (pathname === v2SettingsPath || pathname === `${v2SettingsPath}/`) return { kind: "settings", section: "general" };
  if (pathname.startsWith(`${v2SettingsPath}/`)) {
    const section = pathname.slice(v2SettingsPath.length + 1);
    return SETTINGS_SECTIONS.includes(section as SettingsSection) ? { kind: "settings", section: section as SettingsSection } : { kind: "invalid" };
  }

  if (!pathname.startsWith(v2SessionsPath)) {
    return { kind: "invalid" };
  }

  const encodedRef = pathname.slice(v2SessionsPath.length);
  if (encodedRef === "" || encodedRef.includes("/")) {
    return { kind: "invalid" };
  }

  try {
    const ref = decodeURIComponent(encodedRef);
    return sessionRefPattern.test(ref) ? { kind: "session", ref: ref as SessionRef } : { kind: "invalid" };
  } catch {
    return { kind: "invalid" };
  }
}

export function webUIV2SessionPath(ref: SessionRef): string {
  return `${v2SessionsPath}${encodeURIComponent(ref)}`;
}

export function webUIV2SettingsPath(section: SettingsSection, ref: SessionRef | null = null): string {
  const query = ref ? `?${new URLSearchParams({ session: ref })}` : "";
  return `${v2SettingsPath}/${section}${query}`;
}

export function settingsReturnSession(search: string): SessionRef | null {
  const ref = new URLSearchParams(search).get("session");
  return ref && sessionRefPattern.test(ref) ? ref as SessionRef : null;
}

export function webUIV2WorkflowPath(route: WorkflowRoute = { kind: "workflows", page: "list" }): string {
  if (route.page === "edit") return `${v2WorkflowsPath}/edit/${encodeURIComponent(route.key)}`;
  if (route.page === "run") return `${v2WorkflowsPath}/runs/${encodeURIComponent(route.runID)}`;
  return route.page === "list" ? v2WorkflowsPath : `${v2WorkflowsPath}/${route.page}`;
}
