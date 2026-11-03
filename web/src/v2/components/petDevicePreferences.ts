export const PET_DEVICE_PREFERENCES_KEY = "niuma.pet.device.v1";
export const PET_VIEWPORT_MARGIN = 8;

export type PetNormalizedPosition = { x: number; y: number };
export type PetPoint = { x: number; y: number };
export type PetSize = { width: number; height: number };
export type PetDevicePreferences = { hidden: boolean; position: PetNormalizedPosition | null };

const DEFAULT_PREFERENCES: PetDevicePreferences = { hidden: false, position: null };

function finiteUnit(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 1;
}

function available(viewport: PetSize, pet: PetSize): PetSize {
  return {
    width: Math.max(0, viewport.width - pet.width - PET_VIEWPORT_MARGIN * 2),
    height: Math.max(0, viewport.height - pet.height - PET_VIEWPORT_MARGIN * 2)
  };
}

function roundUnit(value: number): number {
  return Math.round(Math.min(1, Math.max(0, value)) * 1000) / 1000;
}

export function clampPetTopLeft(point: PetPoint, viewport: PetSize, pet: PetSize): PetPoint {
  const room = available(viewport, pet);
  return {
    x: Math.min(PET_VIEWPORT_MARGIN + room.width, Math.max(PET_VIEWPORT_MARGIN, point.x)),
    y: Math.min(PET_VIEWPORT_MARGIN + room.height, Math.max(PET_VIEWPORT_MARGIN, point.y))
  };
}

export function normalizePetPosition(point: PetPoint, viewport: PetSize, pet: PetSize): PetNormalizedPosition {
  const room = available(viewport, pet);
  const clamped = clampPetTopLeft(point, viewport, pet);
  return {
    x: room.width === 0 ? 0 : roundUnit((clamped.x - PET_VIEWPORT_MARGIN) / room.width),
    y: room.height === 0 ? 0 : roundUnit((clamped.y - PET_VIEWPORT_MARGIN) / room.height)
  };
}

export function denormalizePetPosition(position: PetNormalizedPosition, viewport: PetSize, pet: PetSize): PetPoint {
  const room = available(viewport, pet);
  return clampPetTopLeft({
    x: PET_VIEWPORT_MARGIN + room.width * position.x,
    y: PET_VIEWPORT_MARGIN + room.height * position.y
  }, viewport, pet);
}

export function loadPetDevicePreferences(): PetDevicePreferences {
  try {
    const raw = localStorage.getItem(PET_DEVICE_PREFERENCES_KEY);
    if (!raw) return { ...DEFAULT_PREFERENCES };
    const value = JSON.parse(raw) as Record<string, unknown>;
    const hidden = typeof value.hidden === "boolean" ? value.hidden : false;
    const position = value.position && typeof value.position === "object" ? value.position as Record<string, unknown> : null;
    return {
      hidden,
      position: position && finiteUnit(position.x) && finiteUnit(position.y) ? { x: position.x, y: position.y } : null
    };
  } catch {
    return { ...DEFAULT_PREFERENCES };
  }
}

export function savePetDevicePreferences(preferences: PetDevicePreferences): void {
  localStorage.setItem(PET_DEVICE_PREFERENCES_KEY, JSON.stringify(preferences));
}
