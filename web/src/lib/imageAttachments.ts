export const MAX_IMAGE_ATTACHMENTS = 8;

export function imageFilesFromClipboard(items: ArrayLike<DataTransferItem>): File[] {
  const files: File[] = [];
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (item?.kind !== "file" || !item.type.toLowerCase().startsWith("image/")) {
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      files.push(file);
    }
  }
  return files;
}

export function selectImageFiles(files: Iterable<File>, limit = MAX_IMAGE_ATTACHMENTS): File[] {
  const selected: File[] = [];
  const seen = new Set<string>();
  for (const file of files) {
    if (!file.type.toLowerCase().startsWith("image/") || selected.length >= limit) {
      continue;
    }
    const key = `${file.name}:${file.size}:${file.lastModified}:${file.type}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    selected.push(file);
  }
  return selected;
}

export async function sha256File(file: File): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0")).join("");
}

export async function fileToDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error || new Error("failed to read image"));
    reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : "");
    reader.readAsDataURL(file);
  });
}

export function dataURLPayload(dataURL: string): string {
  const separator = dataURL.indexOf(",");
  return separator >= 0 ? dataURL.slice(separator + 1) : dataURL;
}
