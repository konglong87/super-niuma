import { describe, expect, it } from "vitest";
import { imageFilesFromClipboard, selectImageFiles } from "./imageAttachments";

function clipboardItem(kind: string, type: string, file: File | null): DataTransferItem {
  return { kind, type, getAsFile: () => file } as unknown as DataTransferItem;
}

describe("image attachment input", () => {
  it("extracts image files and ignores text clipboard items", () => {
    const image = new File(["png"], "clip.png", { type: "image/png" });
    const items = [clipboardItem("string", "text/plain", null), clipboardItem("file", "image/png", image)];
    expect(imageFilesFromClipboard(items)).toEqual([image]);
  });

  it("accepts only images and caps the attachment count", () => {
    const imageA = new File(["a"], "a.png", { type: "image/png" });
    const imageB = new File(["b"], "b.jpg", { type: "image/jpeg" });
    const text = new File(["text"], "notes.txt", { type: "text/plain" });
    expect(selectImageFiles([imageA, text, imageB], 2)).toEqual([imageA, imageB]);
    expect(selectImageFiles([imageA, imageB], 1)).toEqual([imageA]);
  });
});
