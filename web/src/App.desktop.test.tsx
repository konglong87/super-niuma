import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./App";
import { I18nProvider } from "./lib/i18n";
import type { IdentityConfig } from "./lib/types";

const observed = vi.hoisted(() => ({ identity: undefined as IdentityConfig | undefined }));
vi.mock("./v2/WebUIV2App", () => ({
  WebUIV2App: ({ identity }: { identity: IdentityConfig }) => { observed.identity = identity; return null; }
}));
vi.mock("./lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("./lib/api")>(),
  listTenants: vi.fn(async () => []),
  listTenantUsers: vi.fn(async () => [])
}));

let root: Root;
const desktopStorageKey = "niuma.desktop.identity.v2";
const webStorageKey = "niuma-webui.identity.v1";
const persisted: IdentityConfig = { apiBase: "/api", apiToken: "old-process", mobileJwt: "web-jwt", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const storage = new Map([[desktopStorageKey, JSON.stringify(persisted)], [webStorageKey, JSON.stringify(persisted)]]);
  vi.spyOn(window, "localStorage", "get").mockReturnValue({
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => { storage.set(key, value); }
  } as Storage);
  const host = document.createElement("div");
  document.body.replaceChildren(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  delete window.__NIUMA_DESKTOP_TOKEN__;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

function setOrigin(origin: string) {
  const location = new URL(`${origin}/webui/v2`);
  vi.stubGlobal("window", new Proxy(window, {
    get(target, key) {
      if (key === "location") return location;
      const value = Reflect.get(target, key, target);
      return ["addEventListener", "removeEventListener", "dispatchEvent"].includes(String(key)) ? value.bind(target) : value;
    }
  }));
}

it.each(["wails://wails", "http://wails.localhost"])("syncs late injection and restarted tokens in desktop-v2 App on %s", async (origin) => {
  vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
  setOrigin(origin);
  await act(async () => root.render(<I18nProvider><App /></I18nProvider>));
  expect(observed.identity).toMatchObject({ apiBase: "", apiToken: "", mobileJwt: "" });
  for (const token of ["late-token", "restart-token"]) {
    await act(async () => {
      window.__NIUMA_DESKTOP_TOKEN__ = token;
      window.dispatchEvent(new Event("niuma-desktop-token"));
    });
    expect(observed.identity).toMatchObject({ apiBase: "", apiToken: token, mobileJwt: "", tenantKey: "tenant" });
  }
});

it("ignores desktop token events on a normal HTTP web host", async () => {
  vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
  setOrigin("http://localhost:5173");
  await act(async () => root.render(<I18nProvider><App /></I18nProvider>));
  const before = observed.identity;
  await act(async () => {
    window.__NIUMA_DESKTOP_TOKEN__ = "desktop-looking-token";
    window.dispatchEvent(new Event("niuma-desktop-token"));
  });
  expect(observed.identity).toBe(before);
  expect(observed.identity).toMatchObject({ apiToken: persisted.apiToken, mobileJwt: persisted.mobileJwt });
});

it.each(["wails://wails", "http://wails.localhost"])("keeps the legacy desktop token without DOM injection on %s", async (origin) => {
  vi.stubEnv("VITE_DESKTOP_UI_VERSION", "");
  window.localStorage.setItem(desktopStorageKey, JSON.stringify({ ...persisted, apiToken: "test-token" }));
  setOrigin(origin);
  await act(async () => root.render(<I18nProvider><App /></I18nProvider>));
  expect(observed.identity).toMatchObject({ apiBase: "", apiToken: "test-token", mobileJwt: "" });
  const before = observed.identity;
  await act(async () => {
    window.__NIUMA_DESKTOP_TOKEN__ = "v2-only-token";
    window.dispatchEvent(new Event("niuma-desktop-token"));
  });
  expect(observed.identity).toBe(before);
});
