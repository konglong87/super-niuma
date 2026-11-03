import { Download, Image as ImageIcon, LoaderCircle, RefreshCcw, Upload, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import { editImage, generateImage, getImageArtifact, listImageHistory, getImageCapabilities, type ImageEditRequest } from "../../lib/api";
import type { IdentityConfig, ImageArtifact, ImageGenerationRecord, ImageCapabilityEntry } from "../../lib/types";
import type { AgentCopy } from "../agent/copy";

type Mode = "generate" | "edit";

type Props = {
  identity: IdentityConfig;
  sessionId: number;
  copy?: AgentCopy;
  onAssetCreated?: (asset: ImageArtifact) => void;
  initialPrompt?: string;
};

type Preview = { asset: ImageArtifact | ImageGenerationRecord; src: string };

const fallbackCopy: Pick<AgentCopy, "imageGeneration" | "imageGenerate" | "imageEdit" | "imagePrompt" | "imagePromptPlaceholder" | "imageSource" | "imageSourceSelect" | "imageUpload" | "imageGenerateAction" | "imageEditAction" | "imageGenerating" | "imageLoadingHistory" | "imageNoHistory" | "imageRetry" | "imageDownload" | "imageError" | "imageQuality" | "imageSize" | "imageFormat" | "imageBackground"> = {
  imageGeneration: "Image generation",
  imageGenerate: "Generate",
  imageEdit: "Edit / redraw",
  imagePrompt: "Prompt",
  imagePromptPlaceholder: "Describe the image you want to create...",
  imageSource: "Source image",
  imageQuality: "Quality",
  imageSize: "Size",
  imageFormat: "Format",
  imageBackground: "Background",
  imageSourceSelect: "Select a source image",
  imageUpload: "Upload image",
  imageGenerateAction: "Generate image",
  imageEditAction: "Edit image",
  imageGenerating: "Generating...",
  imageLoadingHistory: "Loading image history...",
  imageNoHistory: "No generated images yet.",
  imageRetry: "Retry image generation",
  imageDownload: "Download image",
  imageError: "Image generation failed"
};

export function ImageGenerationPanel({ identity, sessionId, copy, onAssetCreated, initialPrompt = "" }: Props) {
  const labels = copy || fallbackCopy;
  const [mode, setMode] = useState<Mode>("generate");
  const [prompt, setPrompt] = useState(initialPrompt);
  const [sourceAsset, setSourceAsset] = useState<ImageGenerationRecord | null>(null);
  const [sourceFile, setSourceFile] = useState<File | undefined>();
  const [quality, setQuality] = useState("auto");
  const [size, setSize] = useState("1024x1024");
  const [outputFormat, setOutputFormat] = useState("png");
  const [background, setBackground] = useState("auto");
  const [capabilities, setCapabilities] = useState<ImageCapabilityEntry[]>([]);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [resolution, setResolution] = useState("");
  const [aspectRatio, setAspectRatio] = useState("");
  const [watermark, setWatermark] = useState<boolean | undefined>(undefined);
  const [history, setHistory] = useState<ImageGenerationRecord[]>([]);
  const [previews, setPreviews] = useState<Preview[]>([]);
  const [loadingHistory, setLoadingHistory] = useState(true);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lastRequest, setLastRequest] = useState<Mode | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const objectURLsRef = useRef<string[]>([]);

  const selectedCapability = useMemo(() => capabilities.find((item) => item.provider === provider && item.model === model) || capabilities[0], [capabilities, model, provider]);

  useEffect(() => {
    if (typeof getImageCapabilities !== "function") return;
    let cancelled = false;
    getImageCapabilities(identity)
      .then((items) => {
        if (cancelled) return;
        setCapabilities(items);
        const first = items[0];
        if (first) {
          setProvider((value) => value || first.provider);
          setModel((value) => value || first.model);
          setResolution((value) => value || first.capability.resolutions?.[0] || "");
          setAspectRatio((value) => value || first.capability.aspectRatios?.[0] || "");
        }
      })
      .catch(() => {
        // Capability discovery is optional for hosts that only expose the legacy API.
      });
    return () => { cancelled = true; };
  }, [identity]);

  useEffect(() => {
    const capability = selectedCapability?.capability;
    if (!capability) return;
    if (capability.resolutions?.length && !capability.resolutions.includes(resolution)) setResolution(capability.resolutions[0]);
    if (capability.aspectRatios?.length && !capability.aspectRatios.includes(aspectRatio)) setAspectRatio(capability.aspectRatios[0]);
    if (capability.outputFormats?.length && !capability.outputFormats.includes(outputFormat)) setOutputFormat(capability.outputFormats[0]);
    if (capability.qualityOptions?.length && !capability.qualityOptions.includes(quality)) setQuality(capability.qualityOptions[0]);
    if (!capability.outputFormats?.length && outputFormat !== "png") setOutputFormat("png");
    if (!capability.qualityOptions?.length && quality !== "auto") setQuality("auto");
    if (!capability.sizes?.length && size !== "auto") setSize("auto");
    if (!capability.supportsWatermark) setWatermark(undefined);
  }, [aspectRatio, outputFormat, quality, resolution, selectedCapability, size]);

  const loadArtifact = useCallback(async (asset: ImageArtifact | ImageGenerationRecord, signal?: AbortSignal) => {
    const blob = await getImageArtifact(identity, asset.asset_id, signal);
    const src = URL.createObjectURL(blob);
    objectURLsRef.current.push(src);
    return { asset, src };
  }, [identity]);

  const refreshHistory = useCallback(async () => {
    setLoadingHistory(true);
    try {
      // Keep the panel renderable in host applications that provide a partial API mock.
      if (typeof listImageHistory !== "function") return;
      const records = await listImageHistory(identity, sessionId);
      setHistory(records);
      const loaded = await Promise.all(records.map((record) => loadArtifact(record)));
      setPreviews(loaded);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setLoadingHistory(false);
    }
  }, [identity, loadArtifact, sessionId]);

  useEffect(() => {
    void refreshHistory();
    return () => {
      abortRef.current?.abort();
      for (const url of objectURLsRef.current) URL.revokeObjectURL(url);
      objectURLsRef.current = [];
    };
  }, [refreshHistory]);

  const submit = async (submitMode = mode) => {
    const trimmed = prompt.trim();
    if (!trimmed || loading) return;
    setLoading(true);
    setError("");
    setLastRequest(submitMode);
    const controller = new AbortController();
    abortRef.current = controller;
    const request: ImageEditRequest = {
      prompt: trimmed,
      source_asset_id: submitMode === "edit" ? sourceAsset?.asset_id : undefined,
      image: sourceFile,
      quality,
      size,
      output_format: outputFormat,
      background,
      provider: provider || undefined,
      model: model || undefined,
      resolution: resolution || undefined,
      aspect_ratio: aspectRatio || undefined,
      watermark,
      idempotency_key: crypto.randomUUID()
    };
    try {
      const response = submitMode === "edit"
        ? await editImage(identity, sessionId, request, controller.signal)
        : await generateImage(identity, sessionId, request, controller.signal);
      const preview = await loadArtifact(response.asset, controller.signal);
      setPreviews((current) => [preview, ...current]);
      setHistory((current) => [response.asset as ImageGenerationRecord, ...current]);
      onAssetCreated?.(response.asset);
    } catch (cause) {
      if (!(cause instanceof DOMException && cause.name === "AbortError")) {
        setError(cause instanceof Error ? cause.message : String(cause));
      }
    } finally {
      setLoading(false);
      abortRef.current = null;
    }
  };

  const handleFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (file) {
      setSourceFile(file);
      setSourceAsset(null);
    }
  };

  return (
    <section className="image-generation-panel" aria-label={labels.imageGeneration}>
      <div className="image-generation-header">
        <div><ImageIcon size={16} aria-hidden="true" /><strong>{labels.imageGeneration}</strong></div>
        <button type="button" className="agent-icon-button" onClick={() => void refreshHistory()} title={labels.imageLoadingHistory} aria-label={labels.imageLoadingHistory}><RefreshCcw size={14} /></button>
      </div>
      <div className="image-generation-modes" role="tablist" aria-label={labels.imageGeneration}>
        <button type="button" role="tab" data-mode="generate" aria-selected={mode === "generate"} className={mode === "generate" ? "active" : ""} onClick={() => setMode("generate")}>{labels.imageGenerate}</button>
        <button type="button" role="tab" data-mode="edit" aria-selected={mode === "edit"} className={mode === "edit" ? "active" : ""} onClick={() => setMode("edit")}>{labels.imageEdit}</button>
      </div>
      <form onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        <label className="image-generation-field">{labels.imagePrompt}<textarea name="prompt" value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder={labels.imagePromptPlaceholder} rows={3} required /></label>
        {capabilities.length > 0 ? <div className="image-generation-options">
          <label>Provider<select value={provider} onChange={(event) => { setProvider(event.target.value); const next = capabilities.find((item) => item.provider === event.target.value); if (next) setModel(next.model); }}><option value="">Default</option>{Array.from(new Set(capabilities.map((item) => item.provider))).map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
          <label>Model<select value={model} onChange={(event) => setModel(event.target.value)}>{capabilities.filter((item) => !provider || item.provider === provider).map((item) => <option key={`${item.provider}:${item.model}`} value={item.model}>{item.label || item.model}</option>)}</select></label>
          {selectedCapability?.capability.resolutions?.length ? <label>Resolution<select value={resolution} onChange={(event) => setResolution(event.target.value)}>{selectedCapability.capability.resolutions.map((item) => <option key={item} value={item}>{item}</option>)}</select></label> : null}
          {selectedCapability?.capability.aspectRatios?.length ? <label>Ratio<select value={aspectRatio} onChange={(event) => setAspectRatio(event.target.value)}>{selectedCapability.capability.aspectRatios.map((item) => <option key={item} value={item}>{item}</option>)}</select></label> : null}
          {selectedCapability?.capability.supportsWatermark ? <label>Watermark<select value={watermark === undefined ? "" : String(watermark)} onChange={(event) => setWatermark(event.target.value === "" ? undefined : event.target.value === "true")}><option value="">Default</option><option value="true">On</option><option value="false">Off</option></select></label> : null}
        </div> : null}
        {mode === "edit" ? (
          <div className="image-generation-source">
            <span>{labels.imageSource}</span>
            <label className="image-generation-upload"><Upload size={14} />{sourceFile?.name || labels.imageSourceSelect}<input type="file" accept="image/*" onChange={handleFile} /></label>
            {history.length > 0 ? <select aria-label={labels.imageSourceSelect} value={sourceAsset?.asset_id || ""} onChange={(event) => setSourceAsset(history.find((item) => item.asset_id === event.target.value) || null)}><option value="">{labels.imageSourceSelect}</option>{history.map((record) => <option key={record.asset_id} value={record.asset_id}>{record.asset_id}</option>)}</select> : null}
          </div>
        ) : null}
        <div className="image-generation-options">
          {capabilities.length === 0 || selectedCapability?.capability.qualityOptions?.length ? <label>{labels.imageQuality}<select value={quality} onChange={(event) => setQuality(event.target.value)}>{(selectedCapability?.capability.qualityOptions || ["auto", "low", "medium", "high"]).map((item) => <option key={item} value={item}>{item}</option>)}</select></label> : null}
          {capabilities.length === 0 || selectedCapability?.capability.sizes?.length ? <label>{labels.imageSize}<select value={size} onChange={(event) => setSize(event.target.value)}>{(selectedCapability?.capability.sizes || ["1024x1024", "1536x1024", "1024x1536"]).map((item) => <option key={item} value={item}>{item}</option>)}</select></label> : null}
          {capabilities.length === 0 || selectedCapability?.capability.outputFormats?.length ? <label>{labels.imageFormat}<select value={outputFormat} onChange={(event) => setOutputFormat(event.target.value)}>{(selectedCapability?.capability.outputFormats || ["png", "jpeg", "webp"]).map((item) => <option key={item} value={item}>{item.toUpperCase()}</option>)}</select></label> : null}
          {capabilities.length === 0 ? <label>{labels.imageBackground}<select value={background} onChange={(event) => setBackground(event.target.value)}><option value="auto">Auto</option><option value="transparent">Transparent</option><option value="opaque">Opaque</option></select></label> : null}
        </div>
        <div className="image-generation-actions">
          {loading ? <button type="button" onClick={() => abortRef.current?.abort()}><X size={14} />Cancel</button> : null}
          <button type="submit" disabled={loading || !prompt.trim()}>{loading ? <LoaderCircle size={14} className="spin" /> : <ImageIcon size={14} />}{loading ? labels.imageGenerating : mode === "edit" ? labels.imageEditAction : labels.imageGenerateAction}</button>
        </div>
      </form>
      {error ? <div className="image-generation-error" role="alert"><span>{labels.imageError}: {error}</span><button type="button" data-action="retry" onClick={() => void submit(lastRequest || mode)} title={labels.imageRetry} aria-label={labels.imageRetry}><RefreshCcw size={14} /></button></div> : null}
      <div className="image-generation-history">
        <strong>{labels.imageGeneration}</strong>
        {loadingHistory ? <span>{labels.imageLoadingHistory}</span> : previews.length === 0 ? <span>{labels.imageNoHistory}</span> : previews.map((preview) => {
          const alt = "name" in preview.asset && typeof preview.asset.name === "string" ? preview.asset.name : "Generated asset";
          return <figure key={preview.asset.asset_id}><img src={preview.src} alt={alt} /><a href={preview.src} download={`${preview.asset.asset_id}.png`} title={labels.imageDownload} aria-label={labels.imageDownload}><Download size={14} /></a><button type="button" onClick={() => { setMode("edit"); setSourceAsset(preview.asset as ImageGenerationRecord); setSourceFile(undefined); }} title={labels.imageEdit} aria-label={labels.imageEdit}>Edit</button></figure>;
        })}
      </div>
    </section>
  );
}
