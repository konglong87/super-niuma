import type { IdentityConfig } from "./types";
import { readProductStorage } from "./productStorage";

const storageKey = "niuma-webui.identity.v1";
const desktopStorageKey = "niuma.desktop.identity.v2";

declare global {
  interface Window {
    __NIUMA_DESKTOP_TOKEN__?: string;
  }
}

function isStaticWebUIHost(): boolean {
  return typeof window !== "undefined" && window.location.pathname.startsWith("/webui");
}

export function isDesktopHost(): boolean {
  if (typeof window === "undefined") return false;
  const { protocol, host } = window.location;
  return (protocol === "wails:" && host === "wails") || (protocol === "http:" && host === "wails.localhost");
}

export function isDesktopV2Host(): boolean {
  // Only desktop-v2/main.go injects per-process credentials on DOM readiness.
  return isDesktopHost() && import.meta.env.VITE_DESKTOP_UI_VERSION === "2";
}

function hasExplicitDevProxy(): boolean {
	  return import.meta.env.DEV && Boolean(import.meta.env.VITE_NIUMA_API_TARGET?.trim());
}

function defaultAPIBase(): string {
  return !isDesktopHost() && import.meta.env.DEV && (!isStaticWebUIHost() || hasExplicitDevProxy()) ? "/api" : "";
}

function isStaleStaticAPIBase(apiBase: string): boolean {
  const value = apiBase.trim().replace(/\/+$/, "");
  if (value === "/api") {
    return true;
  }
  if (!isStaticWebUIHost() || typeof window === "undefined") {
    return false;
  }
  try {
    const url = new URL(value, window.location.origin);
    return url.origin === window.location.origin && url.pathname.replace(/\/+$/, "") === "/api";
  } catch {
    return false;
  }
}

export const defaultIdentity: IdentityConfig = {
  apiBase: defaultAPIBase(),
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "webui-local",
  userId: "webui-local-user",
  deviceId: "web-browser",
  model: ""
};

export function loadIdentity(): IdentityConfig {
  try {
	    const key = isDesktopHost() ? desktopStorageKey : storageKey;
	    const raw = isDesktopHost() ? window.localStorage.getItem(key) : readProductStorage(key);
    if (!raw) {
      return normalizeIdentity(defaultIdentity);
    }
    const identity = normalizeIdentity({ ...defaultIdentity, ...JSON.parse(raw) });
    window.localStorage.setItem(key, JSON.stringify(identity));
    return identity;
  } catch {
    return normalizeIdentity(defaultIdentity);
  }
}

export function saveIdentity(config: IdentityConfig): void {
  window.localStorage.setItem(isDesktopHost() ? desktopStorageKey : storageKey, JSON.stringify(isDesktopHost() ? normalizeIdentity(config) : config));
}

export function normalizeIdentity(config: IdentityConfig): IdentityConfig {
  let normalized = isDesktopHost()
    // Legacy desktop keeps its fixed/saved token; v2 must await this process's token.
    ? { ...config, apiBase: "", apiToken: isDesktopV2Host() ? window.__NIUMA_DESKTOP_TOKEN__ || "" : config.apiToken, mobileJwt: "" }
    : config;
  if (!isDesktopHost() && isStaticWebUIHost() && hasExplicitDevProxy() && config.apiBase.trim() === "") {
    normalized = { ...normalized, apiBase: defaultAPIBase() };
  } else if (!isDesktopHost() && isStaticWebUIHost() && isStaleStaticAPIBase(config.apiBase)) {
    normalized = { ...normalized, apiBase: defaultAPIBase() };
  }
  const staleDemoIdentity =
    (normalized.tenantKey === "yutang" && normalized.userId === "web-test-user") ||
    normalized.tenantKey === "webui-alt" ||
    normalized.userId === "webui-alt-user";
  if (staleDemoIdentity) {
    return {
      ...normalized,
      tenantKey: defaultIdentity.tenantKey,
      userId: defaultIdentity.userId
    };
  }
  // The old value was a UI placeholder, not a supported default route.
  if (normalized.model === "gpt-5.5") {
    normalized = { ...normalized, model: "" };
  }
  return normalized;
}
