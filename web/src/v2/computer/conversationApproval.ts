import type { SessionRef } from "../routes";

const MANAGED_REF_PREFIX = "tenant:";
const CHANNEL_KEY_PREFIX = "channel:";
const INVALID_KEY_CHARACTERS = /[\s\p{White_Space}\p{Cc}/\\]/u;

/** Syntax gate only; the native host/server must resolve and authorize identity. */
export function managedComputerConversationRef(ref: SessionRef | null | undefined): SessionRef | null {
  if (!ref?.startsWith(MANAGED_REF_PREFIX)) return null;
  const key = ref.slice(MANAGED_REF_PREFIX.length);
  if (!key || INVALID_KEY_CHARACTERS.test(key)) return null;
  if (key.includes(":") && !key.startsWith(CHANNEL_KEY_PREFIX)) return null;
  return ref;
}
