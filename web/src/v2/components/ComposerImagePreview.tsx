import { useEffect, useState } from "react";

export function ComposerImagePreview({ file }: { file: File }) {
  const [url, setURL] = useState("");
  useEffect(() => {
    if (typeof URL.createObjectURL !== "function") return;
    const nextURL = URL.createObjectURL(file);
    setURL(nextURL);
    return () => URL.revokeObjectURL(nextURL);
  }, [file]);
  return url ? <img alt={file.name} className="webui2-image-preview" src={url} /> : null;
}
