import { describe, expect, it } from "vitest";
import { draftAttachmentIDs, isPreviewableImageAttachment, sha256Hex } from "./ChatLab";
import type { MobileAttachment } from "../lib/types";

describe("ChatLab attachment helpers", () => {
  it("computes stable sha256 hex for uploads", async () => {
    const digest = await sha256Hex(new Blob(["hello"]));

    expect(digest).toBe("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824");
  });

  it("only previews image attachments with a safe browser URL", () => {
    expect(isPreviewableImageAttachment({ type: "image", media_type: "image/png", url: "https://cdn.example.test/image.png" })).toBe(true);
    expect(isPreviewableImageAttachment({ type: "image", media_type: "image/png", url: "javascript:alert(1)" })).toBe(false);
    expect(isPreviewableImageAttachment({ type: "file", media_type: "image/png", url: "https://cdn.example.test/image.png" })).toBe(false);
  });

  it("projects only stable attachment IDs into a local draft", () => {
    const attachments: MobileAttachment[] = [
      { type: "image", attachment_id: "asset-1", name: "one.png" },
      { type: "image", sha256: "hash-2", name: "two.png" },
      { type: "image", name: "without-id.png" }
    ];
    expect(draftAttachmentIDs(attachments)).toEqual(["asset-1", "hash-2"]);
  });
});
