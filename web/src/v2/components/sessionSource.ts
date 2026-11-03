import type { SessionSource } from "../types";

type Translate = (key: string, params?: Record<string, string | number>) => string;

export function normalizeSessionProvider(provider?: string): string | undefined {
  const normalized = provider?.trim().toLowerCase();
  return normalized || undefined;
}

export function getSessionChannelLabel(provider: string | undefined, t: Translate): string | undefined {
  const normalized = normalizeSessionProvider(provider);
  if (!normalized) return undefined;
  return normalized === "feishu" ? t("webui2.channel.feishu") : provider?.trim();
}

export function getSessionSourceLabel(source: SessionSource, provider: string | undefined, t: Translate): string {
  const channelLabel = getSessionChannelLabel(provider, t);
  if (channelLabel) return t("webui2.sessionSource.channel", { name: t("webui2.channel.source", { name: channelLabel }) });
  return source === "local" ? t("webui2.sessionSource.local") : t("webui2.sessionSource.desktop");
}
