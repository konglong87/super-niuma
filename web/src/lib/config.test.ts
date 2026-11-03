import { afterEach, describe, expect, it, vi } from "vitest";
import { isDesktopHost, isDesktopV2Host, loadIdentity, normalizeIdentity, saveIdentity } from "./config";
import type { IdentityConfig } from "./types";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "api-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "browser-a",
  model: "model-a"
};

describe("webui identity config", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    window.history.replaceState(null, "", "/");
  });

  it("keeps vite dev api proxy outside static webui hosting", () => {
    window.history.replaceState(null, "", "/");

    expect(normalizeIdentity(identity).apiBase).toBe("/api");
  });

  it("migrates stale dev api base for static webui hosting", () => {
    window.history.replaceState(null, "", "/webui/");

    expect(normalizeIdentity(identity).apiBase).toBe("");
    expect(normalizeIdentity({ ...identity, apiBase: "/api/" }).apiBase).toBe("");
    expect(normalizeIdentity({ ...identity, apiBase: `${window.location.origin}/api` }).apiBase).toBe("");
  });

  it("keeps the vite proxy for a web agent path with an explicit dev target", () => {
	    vi.stubEnv("VITE_NIUMA_API_TARGET", "http://127.0.0.1:18087");
    window.history.replaceState(null, "", "/webui/agent");

    expect(normalizeIdentity(identity).apiBase).toBe("/api");
    expect(normalizeIdentity({ ...identity, apiBase: "" }).apiBase).toBe("/api");
  });

  it("preserves explicit remote api bases for static webui hosting", () => {
    window.history.replaceState(null, "", "/webui/");

    expect(normalizeIdentity({ ...identity, apiBase: "http://127.0.0.1:18080" }).apiBase).toBe(
      "http://127.0.0.1:18080"
    );
  });

  function host(url: string, raw?: string, token?: string) {
    const storage = new Map(raw ? [["niuma.desktop.identity.v2", raw]] : []);
    vi.stubGlobal("window", {
      location: new URL(url),
      __NIUMA_DESKTOP_TOKEN__: token,
      localStorage: { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value) }
    });
    return storage;
  }

  it.each(["wails://wails", "http://wails.localhost"])("uses only the injected process token for desktop-v2 on %s", (origin) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    const storage = host(`${origin}/webui/v2`, JSON.stringify({ ...identity, apiToken: "old-process", mobileJwt: "old-jwt" }));
    expect(isDesktopHost()).toBe(true);
    expect(isDesktopV2Host()).toBe(true);
    expect(loadIdentity()).toMatchObject({ apiBase: "", apiToken: "", mobileJwt: "", tenantKey: identity.tenantKey });
    window.__NIUMA_DESKTOP_TOKEN__ = "new-process";
    expect(loadIdentity().apiToken).toBe("new-process");
    saveIdentity({ ...identity, apiToken: "stale" });
    expect(JSON.parse(storage.get("niuma.desktop.identity.v2")!).apiToken).toBe("new-process");
    window.__NIUMA_DESKTOP_TOKEN__ = undefined;
    expect(loadIdentity().apiToken).toBe("");
    window.__NIUMA_DESKTOP_TOKEN__ = "restarted-process";
    expect(loadIdentity().apiToken).toBe("restarted-process");
  });

  it.each([undefined, "{broken"])("normalizes desktop defaults with absent or malformed storage: %s", (raw) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    host("http://wails.localhost/webui/v2", raw, "injected-after-module-import");
    vi.stubEnv("VITE_NIUMA_API_TARGET", "http://127.0.0.1:18087");
    expect(loadIdentity()).toMatchObject({ apiBase: "", apiToken: "injected-after-module-import", mobileJwt: "" });
  });

  it.each(["http://localhost:5173", "http://127.0.0.1:18087", "https://example.com", "http://wails.localhost.example.com", "http://wails.localhost:8080", "https://wails.localhost", "wails://other", "wails://wails.localhost", "wails://wails:8080", "http://wails"])("does not treat %s as desktop, even with a v2 build and injected-looking token", (origin) => {
    vi.stubEnv("VITE_DESKTOP_UI_VERSION", "2");
    const storage = host(`${origin}/`, undefined, "not-a-desktop-token");
    storage.set("niuma-webui.identity.v1", JSON.stringify(identity));
    expect(isDesktopHost()).toBe(false);
    expect(isDesktopV2Host()).toBe(false);
    expect(normalizeIdentity(identity)).toEqual(identity);
    expect(loadIdentity()).toEqual(identity);
    saveIdentity(identity);
    expect(storage.has("niuma.desktop.identity.v2")).toBe(false);
  });

  describe.each(["", "1"])("legacy desktop build flag '%s'", (version) => {
    it.each(["wails://wails", "http://wails.localhost"])("keeps fixed defaults and saved tokens without injection on %s", (origin) => {
      vi.stubEnv("VITE_DESKTOP_UI_VERSION", version);
      const storage = host(`${origin}/`);
      expect(isDesktopHost()).toBe(true);
      expect(isDesktopV2Host()).toBe(false);
      expect(loadIdentity()).toMatchObject({ apiBase: "", apiToken: "test-token", mobileJwt: "" });
      storage.set("niuma.desktop.identity.v2", "{broken");
      expect(loadIdentity().apiToken).toBe("test-token");
      window.__NIUMA_DESKTOP_TOKEN__ = "v2-only-token";
      saveIdentity(identity);
      expect(loadIdentity()).toEqual({ ...identity, apiBase: "" });
      expect(normalizeIdentity({ ...identity, apiToken: "" }).apiToken).toBe("");
    });
  });
});
