import { describe, expect, it } from "vitest";
import { settingsEnvironmentIdentity, type SettingsEnvironment } from "./settingsEnvironments";
import type { IdentityConfig } from "../../lib/types";

const identity: IdentityConfig = { apiBase: "/api/", apiToken: "token", mobileJwt: "mobile", tenantKey: "web", userId: "operator", model: "model", deviceId: "device" };
const channel: SettingsEnvironment = { id: "channel", label: "Channel", database: "channel_db", tenant_key: "target", user_id: "worker", api_path: "/runtime/settings/environments/channel", available: true };

describe("settings environment identity", () => {
  it("preserves source operator authentication and leaves the original chat identity untouched", () => {
    expect(settingsEnvironmentIdentity(identity, { id: "current", api_path: "" })).toBe(identity);
    expect(settingsEnvironmentIdentity(identity, channel)).toEqual({ ...identity, apiBase: "/api/runtime/settings/environments/channel", mobileJwt: "" });
    expect(identity.apiBase).toBe("/api/");
    expect(identity.mobileJwt).toBe("mobile");
  });

  it.each(["https://other.example", "//other.example", "/runtime/settings/environments/other", "/runtime/settings/environments/channel/.."]) ("rejects an untrusted gateway path %s", (api_path) => {
    expect(() => settingsEnvironmentIdentity(identity, { ...channel, api_path })).toThrow("Invalid settings environment route");
  });
});
