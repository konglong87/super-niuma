import { Crosshair, Settings2, X } from "lucide-react";
import { createPortal } from "react-dom";
import {
  useEffect, useRef, useState, type CSSProperties, type JSX,
  type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent
} from "react";
import { useI18n } from "../../lib/i18n";
import type { PetSettings } from "../settings/globalVisualSettings";
import type { SessionStatus } from "../types";
import { PetScene } from "./PetScene";
import {
  PET_VIEWPORT_MARGIN,
  clampPetTopLeft,
  denormalizePetPosition,
  loadPetDevicePreferences,
  normalizePetPosition,
  savePetDevicePreferences,
  type PetDevicePreferences,
  type PetPoint,
  type PetSize
} from "./petDevicePreferences";

const DRAG_THRESHOLD_PX = 6;
const MENU_GAP_PX = 10;
const MENU_VIEWPORT_MARGIN_PX = 12;
const MEASURE_TOLERANCE_PX = 0.5;

type DragState = {
  pointerId: number;
  startX: number;
  startY: number;
  originX: number;
  originY: number;
  moved: boolean;
};

type PetMenuPosition = {
  left: number;
  top: number;
};

export type DesktopPetProps = {
  settings: PetSettings;
  status?: SessionStatus;
  onOpenSettings: () => void;
};

export function DesktopPet({ settings, status = "idle", onOpenSettings }: DesktopPetProps): JSX.Element | null {
  const { language } = useI18n();
  const zh = language === "zh";
  const [prefs, setPrefs] = useState<PetDevicePreferences>(loadPetDevicePreferences);
  const [menuOpen, setMenuOpen] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [dragOffset, setDragOffset] = useState<PetPoint | null>(null);
  const [menuPosition, setMenuPosition] = useState<PetMenuPosition | null>(null);
  const [viewport, setViewport] = useState<PetSize>(() => ({ width: window.innerWidth, height: window.innerHeight }));
  const [size, setSize] = useState<PetSize | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const drag = useRef<DragState | null>(null);
  const shown = settings.enabled && settings.visible && !prefs.hidden;

  useEffect(() => {
    if (!shown) return;
    const onResize = (): void => setViewport({ width: window.innerWidth, height: window.innerHeight });
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [shown]);

  // Re-measure when visual settings change the scaled box; CSS transforms are
  // invisible to ResizeObserver, so the dependency list below is intentional.
  // biome-ignore lint/correctness/useExhaustiveDependencies: measure-only effect keyed by visual inputs
  useEffect(() => {
    if (!shown) return;
    const rect = ref.current?.getBoundingClientRect();
    if (!rect) return;
    setSize((previous) => previous
      && Math.abs(previous.width - rect.width) < MEASURE_TOLERANCE_PX
      && Math.abs(previous.height - rect.height) < MEASURE_TOLERANCE_PX
      ? previous
      : { width: rect.width, height: rect.height });
  }, [shown, settings.scale, settings.statusBubble, settings.fontScale, viewport]);

  useEffect(() => {
    if (!menuOpen) return;
    const onOutsidePointerDown = (event: PointerEvent): void => {
      const target = event.target as Node;
      if (ref.current?.contains(target) || menuRef.current?.contains(target)) return;
      setMenuOpen(false);
    };
    const onEscape = (event: KeyboardEvent): void => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("pointerdown", onOutsidePointerDown, true);
    document.addEventListener("keydown", onEscape);
    return () => {
      document.removeEventListener("pointerdown", onOutsidePointerDown, true);
      document.removeEventListener("keydown", onEscape);
    };
  }, [menuOpen]);

  useEffect(() => {
    if (!menuOpen) {
      setMenuPosition(null);
      return;
    }
    let frame = 0;
    const positionMenu = (): void => {
      const modelRect = ref.current?.querySelector<HTMLElement>(".pet-model")?.getBoundingClientRect()
        ?? ref.current?.getBoundingClientRect();
      const statusRect = ref.current?.querySelector<HTMLElement>(".webui2-pet-status")?.getBoundingClientRect();
      const menuRect = menuRef.current?.getBoundingClientRect();
      if (!modelRect || !menuRect) return;
      const anchorBottom = statusRect?.bottom ?? modelRect.bottom;
      const menuWidth = menuRect.width;
      const menuHeight = menuRect.height;
      const belowTop = anchorBottom + MENU_GAP_PX;
      const aboveTop = modelRect.top - MENU_GAP_PX - menuHeight;
      const spaceBelow = viewport.height - anchorBottom;
      const spaceAbove = modelRect.top;
      const preferredTop = spaceBelow >= menuHeight + MENU_GAP_PX || spaceBelow >= spaceAbove ? belowTop : aboveTop;
      const top = Math.min(
        Math.max(MENU_VIEWPORT_MARGIN_PX, preferredTop),
        Math.max(MENU_VIEWPORT_MARGIN_PX, viewport.height - menuHeight - MENU_VIEWPORT_MARGIN_PX),
      );
      const centeredLeft = modelRect.left + (modelRect.width - menuWidth) / 2;
      const left = Math.min(
        Math.max(MENU_VIEWPORT_MARGIN_PX, centeredLeft),
        Math.max(MENU_VIEWPORT_MARGIN_PX, viewport.width - menuWidth - MENU_VIEWPORT_MARGIN_PX),
      );
      setMenuPosition({ left, top });
    };
    frame = window.requestAnimationFrame(positionMenu);
    return () => window.cancelAnimationFrame(frame);
  }, [menuOpen, viewport]);

  if (!shown) return null;

  const basePosition: PetPoint | null = size
    ? clampPetTopLeft(
      prefs.position
        ? denormalizePetPosition(prefs.position, viewport, size)
        : { x: viewport.width - size.width - settings.right, y: viewport.height - size.height - settings.bottom },
      viewport,
      size
    )
    : null;
  const position = dragOffset ?? basePosition;

  const style: CSSProperties = { "--pet-scale": String(settings.scale) } as CSSProperties;
  if (position) {
    style.left = position.x;
    style.top = position.y;
  } else {
    style.right = settings.right;
    style.bottom = settings.bottom;
  }

  const persistPosition = (point: PetPoint): void => {
    if (!size) return;
    const next: PetDevicePreferences = { hidden: false, position: normalizePetPosition(point, viewport, size) };
    savePetDevicePreferences(next);
    setPrefs(next);
  };

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>): void => {
    if (event.button !== 0 || !ref.current) return;
    const rect = ref.current.getBoundingClientRect();
    drag.current = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: rect.left, originY: rect.top, moved: false };
    try {
      ref.current.setPointerCapture(event.pointerId);
    } catch {
      // Pointer capture is unavailable in some test environments.
    }
  };

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>): void => {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId || !size) return;
    const dx = event.clientX - current.startX;
    const dy = event.clientY - current.startY;
    if (!current.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD_PX) return;
    if (!current.moved) {
      current.moved = true;
      setDragging(true);
      setMenuOpen(false);
    }
    setDragOffset(clampPetTopLeft({ x: current.originX + dx, y: current.originY + dy }, viewport, size));
  };

  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>): void => {
    const current = drag.current;
    if (!current || current.pointerId !== event.pointerId) return;
    drag.current = null;
    try {
      ref.current?.releasePointerCapture(event.pointerId);
    } catch {
      // Pointer capture is unavailable in some test environments.
    }
    if (current.moved) {
      setDragging(false);
      setDragOffset(null);
      if (size) {
        persistPosition(clampPetTopLeft(
          { x: current.originX + event.clientX - current.startX, y: current.originY + event.clientY - current.startY },
          viewport,
          size
        ));
      }
      return;
    }
    setMenuOpen((open) => !open);
  };

  const onPointerCancel = (event: ReactPointerEvent<HTMLDivElement>): void => {
    if (drag.current?.pointerId !== event.pointerId) return;
    drag.current = null;
    setDragging(false);
    setDragOffset(null);
  };

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>): void => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setMenuOpen((open) => !open);
    }
  };

  const closePet = (): void => {
    const next: PetDevicePreferences = { ...prefs, hidden: true };
    savePetDevicePreferences(next);
    setPrefs(next);
    setMenuOpen(false);
  };

  const resetPosition = (): void => {
    const next: PetDevicePreferences = { hidden: false, position: null };
    savePetDevicePreferences(next);
    setPrefs(next);
    setMenuOpen(false);
  };

  const openPetSettings = (): void => {
    setMenuOpen(false);
    onOpenSettings();
  };

  const menu = menuOpen ? createPortal(
    <div
      ref={menuRef}
      aria-label={zh ? "宠物选项" : "Pet options"}
      className="webui2-pet-menu"
      onPointerDown={(event) => event.stopPropagation()}
      onPointerUp={(event) => event.stopPropagation()}
      role="menu"
      style={{
        left: menuPosition?.left ?? MENU_VIEWPORT_MARGIN_PX,
        top: menuPosition?.top ?? MENU_VIEWPORT_MARGIN_PX,
        visibility: menuPosition ? "visible" : "hidden"
      }}
    >
      <button className="webui2-pet-menu-action webui2-pet-menu-action--settings" type="button" role="menuitem" onClick={openPetSettings}><Settings2 aria-hidden="true" size={16} strokeWidth={2} />{zh ? "宠物设置" : "Pet settings"}</button>
      <button className="webui2-pet-menu-action webui2-pet-menu-action--reset" type="button" role="menuitem" onClick={resetPosition}><Crosshair aria-hidden="true" size={16} strokeWidth={2} />{zh ? "恢复默认位置" : "Reset position"}</button>
      <div className="webui2-pet-menu-divider" role="separator" />
      <button className="webui2-pet-menu-action webui2-pet-menu-action--close" type="button" role="menuitem" onClick={closePet}><X aria-hidden="true" size={16} strokeWidth={2} />{zh ? "关闭宠物" : "Close pet"}</button>
    </div>,
    document.body,
  ) : null;

  // biome-ignore lint/a11y/useSemanticElements: the pet region hosts a nested menu with buttons, which a <button> cannot contain
  return <>
    <div
      ref={ref}
      aria-expanded={menuOpen}
      aria-haspopup="menu"
      aria-label={zh ? "桌面宠物，点击打开菜单，拖动移动位置" : "Desktop pet, click for options or drag to move"}
      className="webui2-desktop-pet"
      data-dragging={dragging ? "true" : "false"}
      onKeyDown={onKeyDown}
      onPointerCancel={onPointerCancel}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      role="button"
      style={style}
      tabIndex={0}
    >
      <PetScene settings={settings} status={status} />
    </div>
    {menu}
  </>;
}
