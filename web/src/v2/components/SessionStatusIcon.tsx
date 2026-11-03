import { Archive, Check, Circle, CircleAlert, Clock3, LoaderCircle, MessageCircleQuestion, ShieldQuestion, Square, type LucideIcon } from "lucide-react";
import { useI18n } from "../../lib/i18n";
import type { SessionStatus } from "../types";

const STATUS_VISUALS: Record<SessionStatus, { icon: LucideIcon; tone: string; motion?: string }> = {
  idle: { icon: Circle, tone: "neutral" },
  queued: { icon: Clock3, tone: "active", motion: "pulse" },
  running: { icon: LoaderCircle, tone: "active", motion: "spin" },
  completed: { icon: Check, tone: "success" },
  stopped: { icon: Square, tone: "neutral" },
  archived: { icon: Archive, tone: "neutral" },
  waiting_permission: { icon: ShieldQuestion, tone: "warning" },
  waiting_input: { icon: MessageCircleQuestion, tone: "warning" },
  blocked: { icon: CircleAlert, tone: "danger" },
  failed: { icon: CircleAlert, tone: "danger" }
};

export function SessionStatusIcon({ status }: { status: SessionStatus }) {
  const { t } = useI18n();
  const { icon: Icon, tone, motion } = STATUS_VISUALS[status];
  const label = t(`webui2.status.${status}`);
  return <span className="webui2-session-status-icon" data-status={status} data-tone={tone} data-motion={motion} role="img" aria-label={label} title={label}>
    <Icon aria-hidden="true" size={14} strokeWidth={1.8} />
  </span>;
}
