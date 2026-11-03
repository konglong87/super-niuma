export const SESSION_REF_MIME_TYPE = "application/x-niuma-session-ref";

export function supportsComposerDrop(transfer: DataTransfer): boolean {
  if (Array.from(transfer.types ?? []).includes(SESSION_REF_MIME_TYPE)) return true;
  const items = Array.from(transfer.items ?? []);
  if (items.length > 0) return items.some((item) => item.kind === "file" && item.type.toLowerCase().startsWith("image/"));
  return Array.from(transfer.files ?? []).some((file) => file.type.toLowerCase().startsWith("image/"));
}
