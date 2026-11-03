import { describe, expect, it } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import { sessionControlQueryKeys } from "./sessionControlClient";

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "tenant-a",
  userId: "user-a",
  deviceId: "device-a",
  model: "test-model"
};

describe("session control query keys", () => {
  it("scopes list and detail keys by identity, normalized filters, and ref", () => {
    expect(sessionControlQueryKeys.list(identity, { query: "  release  ", statuses: ["running", "completed", "running"] })).toEqual([
      "session-control",
      "list",
      "tenant-a",
      "user-a",
      { query: "release", statuses: ["completed", "running"] },
      "/api"
    ]);
    expect(sessionControlQueryKeys.detail(identity, "tenant:release/a")).toEqual([
      "session-control",
      "detail",
      "tenant-a",
      "user-a",
      "tenant:release/a",
      "/api"
    ]);
  });

  it("uses the same list key for queries that differ only by case and surrounding whitespace", () => {
    expect(sessionControlQueryKeys.list(identity, { query: "  ReLeAsE  ", statuses: [] })).toEqual(
      sessionControlQueryKeys.list(identity, { query: "release", statuses: [] })
    );
  });

  it("isolates conversation caches between API servers", () => {
    const otherServer = { ...identity, apiBase: "/other-api" };
    const filters = { query: "", statuses: [] };
    expect(sessionControlQueryKeys.list(identity, filters)).not.toEqual(sessionControlQueryKeys.list(otherServer, filters));
    expect(sessionControlQueryKeys.detail(identity, "tenant:release/a")).not.toEqual(
      sessionControlQueryKeys.detail(otherServer, "tenant:release/a")
    );
  });
});
