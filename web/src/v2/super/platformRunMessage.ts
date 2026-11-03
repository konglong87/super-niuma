import type { ControlRun } from "../desktopServiceBridge";
import { CONTROL_STATUS } from "./platformClient";
import type { PlatformCopy } from "./platformCopy";

type RunMessage = { tone: "info" | "warning"; text: string };
// Safe codes describe lifecycle transitions as well as faults. Pair known
// lifecycle codes with the durable status, and never render an unknown code.
export function platformRunMessage(run: Pick<ControlRun, "status" | "safe_code">, copy: PlatformCopy): RunMessage | null {
  const code = run.safe_code;
  if (run.status === CONTROL_STATUS.cancelled) {
    switch (code) {
      case undefined: case "": case "cancelled": case "cancel_requested":
        return { tone: "info", text: copy.runCancelled };
      case "authority_revoked":
        return { tone: "info", text: copy.runAuthorityRevoked };
      case "operator_abandoned_no_retry":
        return { tone: "info", text: copy.runAbandoned };
    }
  }
  if (run.status === CONTROL_STATUS.cancelling && code === "cancel_requested") {
    return { tone: "info", text: copy.runCancelling };
  }
  if (run.status === CONTROL_STATUS.cancelling && code === "authority_revoked") {
    return { tone: "info", text: copy.runRevocationPending };
  }
  if ((run.status === CONTROL_STATUS.queued || run.status === CONTROL_STATUS.running) && code === "verification_revision_required") {
    return { tone: "info", text: copy.runRevisionRequired };
  }
  return code ? { tone: "warning", text: copy.dataWarning } : null;
}
