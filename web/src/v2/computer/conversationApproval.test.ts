import { describe, expect, it } from "vitest";
import type { SessionRef } from "../routes";
import { managedComputerConversationRef } from "./conversationApproval";

describe("managedComputerConversationRef", () => {
  it.each<SessionRef>(["tenant:alpha", "tenant:42", "tenant:channel:alpha:beta"])("preserves the exact valid ref %s", (ref) => {
    expect(managedComputerConversationRef(ref)).toBe(ref);
  });
  it.each([undefined, null, "local:workspace", "tenant:", "tenant:bad ref", "tenant:bad/ref", "tenant:bad\\ref", "tenant:bad:key", "tenant:alpha\n", "tenant:a\u0085b", "tenant:a\u0000b"] as const)("does not grant conversation access for %s", (ref) => {
    expect(managedComputerConversationRef(ref)).toBeNull();
  });
});
