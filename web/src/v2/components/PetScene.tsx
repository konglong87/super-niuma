import { useEffect, useRef, useState, type CSSProperties, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { SessionStatus } from "../types";
import type { PetSettings } from "../settings/globalVisualSettings";
import { petAsset } from "./petAssets";

export type PetSceneProps = {
  settings: PetSettings;
  status?: SessionStatus;
  className?: string;
};

export function PetScene({ settings, status = "idle", className = "" }: PetSceneProps): JSX.Element | null {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const animationRef = useRef(settings.animation);
  animationRef.current = settings.animation;
  const [state, setState] = useState<"loading" | "ready" | "fallback">("loading");
  const { language } = useI18n();
  const shown = settings.enabled && settings.visible;
  const asset = petAsset(settings.model);
  const assetLabel = asset.label[language === "zh" ? "zh" : "en"];

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !shown) return;
    let cancelled = false;
    let dispose: (() => void) | undefined;
    setState("loading");
    void import("./petRenderer").then(({ createPetRenderer }) => {
      if (cancelled) return;
      try {
        dispose = createPetRenderer(canvas, asset, () => animationRef.current, () => setState("ready"), () => setState("fallback"));
      } catch {
        setState("fallback");
      }
    }).catch(() => { if (!cancelled) setState("fallback"); });
    return () => { cancelled = true; dispose?.(); };
  }, [asset, shown]);

  if (!shown) return null;
  const style = {
    "--pet-scale": String(settings.scale),
    "--pet-font-scale": String(settings.fontScale)
  } as CSSProperties;
  const zh = language === "zh";
  const statusLabel = status === "running" || status === "queued" ? (zh ? "正在工作" : "Working") : status === "waiting_permission" || status === "waiting_input" ? (zh ? "需要你" : "Needs you") : status === "failed" ? (zh ? "需要检查" : "Needs review") : (zh ? "准备就绪" : "Ready");
  return <div className={`webui2-pet-scene ${className}`} data-asset={asset.url} data-render-state={state} data-status={status} style={style}>
    <div className="pet-model">
      <canvas aria-label={assetLabel} ref={canvasRef} width={112} height={132} />
      {state !== "ready" ? <img className="pet-poster" src={asset.poster} alt={assetLabel} /> : null}
    </div>
    {settings.statusBubble ? <span className="webui2-pet-status">{statusLabel}</span> : null}
  </div>;
}
