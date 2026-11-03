import { ChevronUp, Settings } from "lucide-react";
import { useEffect, useId, useRef, useState, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import { isDesktopV2Host } from "../../lib/config";
import type { IdentityConfig } from "../../lib/types";
import niumaMark from "../assets/niuma-mark.svg";

type Props = {
  identity: IdentityConfig;
  onOpenSettings: () => void;
};

export function SidebarAccountFooter({ identity, onOpenSettings }: Props): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const menuID = useId();
  const desktop = isDesktopV2Host();
  const accountLabel = desktop ? t("webui2.brand") : (identity.tenantKey || t("webui2.brand"));
  const accountDetail = desktop ? t("webui2.localWorkspace") : (identity.userId || "—");
  const avatar = (identity.userId || "U").slice(0, 1).toUpperCase();

  useEffect(() => {
    if (!open) return;
    function closeFromOutside(event: PointerEvent): void {
      if (!containerRef.current?.contains(event.target as Node)) setOpen(false);
    }
    function closeFromEscape(event: globalThis.KeyboardEvent): void {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
    }
    document.addEventListener("pointerdown", closeFromOutside);
    document.addEventListener("keydown", closeFromEscape);
    return () => {
      document.removeEventListener("pointerdown", closeFromOutside);
      document.removeEventListener("keydown", closeFromEscape);
    };
  }, [open]);

  function openSettings(): void {
    setOpen(false);
    onOpenSettings();
  }

  return <div className="webui2-account-footer" ref={containerRef}>
    {open ? <div aria-label={t("webui2.accountMenu")} className="webui2-account-menu" id={menuID} role="menu">
      <button aria-label={t("webui2.systemSettings")} onClick={openSettings} role="menuitem" type="button"><Settings aria-hidden="true" size={17} />{t("webui2.systemSettings")}</button>
    </div> : null}
    <button aria-controls={menuID} aria-expanded={open} aria-haspopup="menu" aria-label={t("webui2.accountMenu")} className="webui2-account-trigger" onClick={() => setOpen((current) => !current)} type="button">
      <span className="webui2-sidebar-avatar" aria-hidden="true">{desktop ? <img src={niumaMark} alt="" width={24} height={24} /> : avatar}</span>
      <span className="webui2-account-copy"><strong>{accountLabel}</strong><small>{accountDetail}</small></span>
      <ChevronUp aria-hidden="true" className="webui2-account-chevron" size={16} />
    </button>
  </div>;
}
