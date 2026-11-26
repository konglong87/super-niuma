import { afterEach, describe, expect, it, vi } from "vitest";
import { openExternalURL } from "./openExternal";

describe("openExternalURL", () => {
  afterEach(() => {
    delete window.go;
    vi.restoreAllMocks();
  });

  it("uses the desktop binding when present", async () => {
    const OpenExternalURL = vi.fn().mockResolvedValue(undefined);
    window.go = { main: { app: { RestartLocalService: vi.fn(), OpenExternalURL } } };
    const spy = vi.spyOn(window, "open");
    await openExternalURL("https://example.com/signup");
    expect(OpenExternalURL).toHaveBeenCalledWith("https://example.com/signup");
    expect(spy).not.toHaveBeenCalled();
  });

  it("propagates desktop binding errors", async () => {
    const OpenExternalURL = vi.fn().mockRejectedValue(new Error("external url host is not allowed"));
    window.go = { main: { app: { RestartLocalService: vi.fn(), OpenExternalURL } } };
    await expect(openExternalURL("https://evil.test/")).rejects.toThrow("not allowed");
  });

  it("falls back to window.open in plain web", async () => {
    const spy = vi.spyOn(window, "open").mockReturnValue(null);
    await openExternalURL("https://example.com/signup");
    expect(spy).toHaveBeenCalledWith("https://example.com/signup", "_blank", "noopener,noreferrer");
  });

  it("propagates window.open errors", async () => {
    vi.spyOn(window, "open").mockImplementation(() => { throw new Error("blocked"); });
    await expect(openExternalURL("https://example.com/")).rejects.toThrow("blocked");
  });
});
