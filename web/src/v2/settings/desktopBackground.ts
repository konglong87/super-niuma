import { useCallback, useEffect, useState } from "react";
import { isDesktopV2Host } from "../../lib/config";
import { getDesktopServiceBridge, type DesktopBackgroundImage, type DesktopBackgroundMode } from "../desktopServiceBridge";

export const DESKTOP_BACKGROUND_CHANGED_EVENT = "niuma:desktop-background-changed";

export const EMPTY_DESKTOP_BACKGROUND: DesktopBackgroundImage = {
  mode: "external",
  enabled: false
};

export async function loadDesktopBackground(): Promise<DesktopBackgroundImage> {
  const bridge = getDesktopServiceBridge();
  if (!bridge?.GetBackgroundImage) return EMPTY_DESKTOP_BACKGROUND;
  try {
    return await bridge.GetBackgroundImage();
  } catch {
    return EMPTY_DESKTOP_BACKGROUND;
  }
}

export function useDesktopBackground(enabled = isDesktopV2Host()): {
  available: boolean;
  background: DesktopBackgroundImage;
  busy: boolean;
  error: string;
  save: (dataURL: string, name: string) => Promise<DesktopBackgroundImage | null>;
  setMode: (mode: DesktopBackgroundMode) => Promise<DesktopBackgroundImage | null>;
  clear: () => Promise<boolean>;
} {
  const available = enabled && Boolean(getDesktopServiceBridge()?.GetBackgroundImage);
  const [background, setBackground] = useState<DesktopBackgroundImage>(EMPTY_DESKTOP_BACKGROUND);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const reload = useCallback(async () => {
    if (!available) {
      setBackground(EMPTY_DESKTOP_BACKGROUND);
      return;
    }
    setBackground(await loadDesktopBackground());
  }, [available]);

  useEffect(() => {
    void reload();
    const refresh = (): void => { void reload(); };
    window.addEventListener(DESKTOP_BACKGROUND_CHANGED_EVENT, refresh);
    return () => window.removeEventListener(DESKTOP_BACKGROUND_CHANGED_EVENT, refresh);
  }, [reload]);

  const save = useCallback(async (dataURL: string, name: string): Promise<DesktopBackgroundImage | null> => {
    const bridge = getDesktopServiceBridge();
    if (!bridge?.SaveBackgroundImage) return null;
    setBusy(true);
    setError("");
    try {
      const next = await bridge.SaveBackgroundImage(dataURL, name);
      setBackground(next);
      window.dispatchEvent(new Event(DESKTOP_BACKGROUND_CHANGED_EVENT));
      return next;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "本地背景图片导入失败。");
      return null;
    } finally {
      setBusy(false);
    }
  }, []);

  const setMode = useCallback(async (mode: DesktopBackgroundMode): Promise<DesktopBackgroundImage | null> => {
    const bridge = getDesktopServiceBridge();
    if (!bridge?.SetBackgroundMode) return null;
    setBusy(true);
    setError("");
    try {
      const next = await bridge.SetBackgroundMode(mode);
      setBackground(next);
      window.dispatchEvent(new Event(DESKTOP_BACKGROUND_CHANGED_EVENT));
      return next;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "背景模式切换失败。");
      return null;
    } finally {
      setBusy(false);
    }
  }, []);

  const clear = useCallback(async (): Promise<boolean> => {
    const bridge = getDesktopServiceBridge();
    if (!bridge?.ClearBackgroundImage) return false;
    setBusy(true);
    setError("");
    try {
      await bridge.ClearBackgroundImage();
      setBackground(EMPTY_DESKTOP_BACKGROUND);
      window.dispatchEvent(new Event(DESKTOP_BACKGROUND_CHANGED_EVENT));
      return true;
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "本地背景图片移除失败。");
      return false;
    } finally {
      setBusy(false);
    }
  }, []);

  return { available, background, busy, error, save, setMode, clear };
}
