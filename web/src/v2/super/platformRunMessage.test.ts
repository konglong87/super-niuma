import { describe, expect, it } from "vitest";
import { PLATFORM_COPY } from "./platformCopy";
import { platformRunMessage } from "./platformRunMessage";

describe("Super lifecycle messages", () => {
  it.each(["zh", "en"] as const)("maps known lifecycle codes to neutral messages in %s", (language) => {
    const copy = PLATFORM_COPY[language];
    expect(platformRunMessage({ status: "cancelled", safe_code: "cancelled" }, copy)).toEqual({ tone: "info", text: copy.runCancelled });
    expect(platformRunMessage({ status: "cancelled", safe_code: "cancel_requested" }, copy)).toEqual({ tone: "info", text: copy.runCancelled });
    expect(platformRunMessage({ status: "cancelling", safe_code: "cancel_requested" }, copy)).toEqual({ tone: "info", text: copy.runCancelling });
    expect(platformRunMessage({ status: "cancelled", safe_code: "authority_revoked" }, copy)).toEqual({ tone: "info", text: copy.runAuthorityRevoked });
    expect(platformRunMessage({ status: "cancelling", safe_code: "authority_revoked" }, copy)).toEqual({ tone: "info", text: copy.runRevocationPending });
    expect(platformRunMessage({ status: "cancelled", safe_code: "operator_abandoned_no_retry" }, copy)).toEqual({ tone: "info", text: copy.runAbandoned });
    expect(platformRunMessage({ status: "queued", safe_code: "verification_revision_required" }, copy)).toEqual({ tone: "info", text: copy.runRevisionRequired });
  });
  it("does not hide inconsistent states or render unrecognized raw codes", () => {
    const copy = PLATFORM_COPY.en;
    expect(platformRunMessage({ status: "needs_attention", safe_code: "cancelled" }, copy)).toEqual({ tone: "warning", text: copy.dataWarning });
    expect(platformRunMessage({ status: "failed", safe_code: "private-token-do-not-render" }, copy)).toEqual({ tone: "warning", text: copy.dataWarning });
    expect(platformRunMessage({ status: "running" }, copy)).toBeNull();
  });
});
