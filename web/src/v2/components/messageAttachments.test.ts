import { describe, expect, it } from "vitest";
import type { IdentityConfig } from "../../lib/types";
import type { PreparedAttachment } from "../types";
import { messageAttachmentPreview } from "./messageAttachments";

const identity: IdentityConfig = { apiBase: "/api", apiToken: "test-token", mobileJwt: "", tenantKey: "tenant", userId: "user", deviceId: "device", model: "model" };
const attachment: PreparedAttachment = { type: "image", media_type: "image/png", name: "clipboard.png", size_bytes: 4, sha256: "hash" };

describe("message attachment preview", () => {
  it("uses authenticated assets for durable local URLs and safe direct URLs for uploaded images", () => {
    expect(messageAttachmentPreview({ ...attachment, attachment_id: "image-one", url: "/tenant/media/assets/image-one" }, identity)).toEqual({ assetID: "image-one" });
    expect(messageAttachmentPreview({ ...attachment, url: "/api/tenant/media/assets/image-two" }, identity)).toEqual({ assetID: "image-two" });
    expect(messageAttachmentPreview({ ...attachment, url: "https://cdn.example.test/image.png" }, identity)).toEqual({ source: "https://cdn.example.test/image.png" });
    expect(messageAttachmentPreview({ ...attachment, url: "https://other.test/tenant/media/assets/image-one" }, identity)).toEqual({ source: "https://other.test/tenant/media/assets/image-one" });
    const assetID = `sc-image-${"a".repeat(64)}`;
    expect(messageAttachmentPreview({ ...attachment, attachment_id: assetID }, identity)).toEqual({ assetID });
  });

  it("only previews valid inline raster images and never invents a URL from metadata", () => {
    expect(messageAttachmentPreview({ ...attachment, inline_data: "cG5n" })).toEqual({ source: "data:image/png;base64,cG5n" });
    expect(messageAttachmentPreview({ ...attachment, attachment_id: "mobile-metadata-only" })).toEqual({});
    expect(messageAttachmentPreview({ ...attachment, media_type: "image/svg+xml", inline_data: "PHN2Zz4=" })).toEqual({});
    expect(messageAttachmentPreview({ ...attachment, inline_data: "not base64" })).toEqual({});
    expect(messageAttachmentPreview({ ...attachment, url: "javascript:alert(1)" })).toEqual({});
  });
});
