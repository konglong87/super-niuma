// No model output, event payload, exception text, claim token or raw capability
// object is presented. Only explicit display fields pass through these helpers.
const SECRET_FIELD = /(?:secret|password|passwd|credential|authorization|private.?key|api.?key|access.?token|refresh.?token|^(?:token|key|auth|bearer)$)/i;
const ENV_FIELD = /(?:_env|_env_ref|env_ref)$/i;
const ENV_NAME = /^[A-Z_][A-Z0-9_]*$/;
const SECRET_VALUE = /(?:\b(?:sk|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{8,}|\bBearer\s+\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----|\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|https?:\/\/[^\s/@]+:[^\s/@]+@)/i;
const SECRET_ASSIGNMENT = /(?:secret|password|credential|authorization|api[_-]?key|access[_-]?token)\s*[=:]\s*\S+/i;
export const PLATFORM_LIMITS = { pollMS: 3000, maxAttempts: 3, eventPages: 4, events: 200, deadLetters: 100, text: 512, configBytes: 262144 } as const;

export function displayText(value: unknown): string {
  if (typeof value !== "string") return "—";
  if (SECRET_VALUE.test(value) || SECRET_ASSIGNMENT.test(value)) return "[redacted]";
  return value.slice(0, PLATFORM_LIMITS.text);
}
export function displayHash(value: unknown): string {
  return typeof value === "string" && /^[a-f0-9]{64}$/i.test(value) ? value : "—";
}
export function count(value: unknown): number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : 0;
}
export function displayTime(value: number, language: "zh" | "en"): string {
  return Number.isSafeInteger(value) && value > 0 && value <= 8640000000000000
    ? new Date(value).toLocaleString(language === "zh" ? "zh-CN" : "en-US") : "—";
}
// Configuration stays opaque to the UI. This is a display/safety check only,
// not provider/schema validation; the original JSON is sent to the backend.
export function configSafety(value: string): "safe" | "invalid" | "sensitive" {
  if (value.length > PLATFORM_LIMITS.configBytes) return "invalid";
  let parsed: unknown;
  try { parsed = JSON.parse(value); } catch { return "invalid"; }
  function safe(item: unknown, depth: number): boolean {
    if (depth > 32) return false;
    if (typeof item === "string") return !SECRET_VALUE.test(item) && !SECRET_ASSIGNMENT.test(item);
    if (Array.isArray(item)) return item.every((child) => safe(child, depth + 1));
    if (item && typeof item === "object") return Object.entries(item).every(([key, child]) => {
      if (ENV_FIELD.test(key)) return typeof child === "string" && ENV_NAME.test(child);
      if (SECRET_FIELD.test(key) && child !== "" && child !== null) return false;
      return safe(child, depth + 1);
    });
    return true;
  }
  return safe(parsed, 0) ? "safe" : "sensitive";
}
export function safeConfig(value: string): string | null {
  if (!value) return "";
  return configSafety(value) === "safe" ? value : null;
}

// The backend returns net.Listener's host:port, not a product URL. Refuse URL
// userinfo, paths, query strings and fragments rather than displaying credentials.
export function displayWebhookAddress(value: unknown): string | null {
  if (typeof value !== "string" || value.length > PLATFORM_LIMITS.text) return null;
  const match = /^(?:[a-zA-Z0-9.-]+|\[[a-fA-F0-9:]+\]):([0-9]{1,5})$/.exec(value);
  if (!match || Number(match[1]) < 1 || Number(match[1]) > 65535 || displayText(value) !== value) return null;
  return value;
}
