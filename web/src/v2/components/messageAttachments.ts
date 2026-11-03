import { isPreviewableImageAttachment } from "../../components/ChatLab";
import type { IdentityConfig } from "../../lib/types";
import type { PreparedAttachment } from "../types";

const INLINE_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/webp", "image/gif", "image/avif"]);
const TENANT_ASSET_PATH = /^\/(?:api\/)?tenant\/media\/assets\/([^/]+)$/;
const SESSION_CONTROL_IMAGE_ID = /^sc-image-[a-f0-9]{64}$/;

export function messageAttachmentPreview(attachment: PreparedAttachment, identity?: IdentityConfig): { assetID?: string; source?: string } {
  if (attachment.type !== "image") return {};
  const url = attachment.url?.trim();
  if (url) {
    try {
      const base = new URL(identity?.apiBase || "/api", window.location.origin);
      const parsed = new URL(url, base);
      const asset = parsed.origin === base.origin ? parsed.pathname.match(TENANT_ASSET_PATH) : null;
      if (identity && asset) return { assetID: decodeURIComponent(asset[1]) };
    } catch { return {}; }
    if (isPreviewableImageAttachment({ type: "image", media_type: attachment.media_type, url })) return { source: url };
  }
  if (identity && attachment.attachment_id && SESSION_CONTROL_IMAGE_ID.test(attachment.attachment_id)) return { assetID: attachment.attachment_id };
  if (attachment.inline_data && INLINE_IMAGE_TYPES.has(attachment.media_type.toLowerCase()) && /^[A-Za-z0-9+/]+={0,2}$/.test(attachment.inline_data)) {
    return { source: `data:${attachment.media_type.toLowerCase()};base64,${attachment.inline_data}` };
  }
  return {};
}
