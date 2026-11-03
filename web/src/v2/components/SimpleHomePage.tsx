import type { JSX } from "react";
import { Plus } from "lucide-react";
import { useI18n } from "../../lib/i18n";
import niumaBrand from "../assets/niuma-animation.svg";

type SimpleHomePageProps = {
  onCreateSession: () => void;
  createDisabled?: boolean;
};

// Snapshot of the pre-task-first desktop home. Keep this component intact so
// the product can switch back to the simple home without a code archaeology
// exercise.
export function SimpleHomePage({ onCreateSession, createDisabled = false }: SimpleHomePageProps): JSX.Element {
  const { t } = useI18n();
  return <section className="webui2-empty-state">
    <img alt={t("webui2.emptyState.imageAlt")} height="128" src={niumaBrand} width="320" />
    <div><h1>{t("webui2.emptyState.title")}</h1><p>{t("webui2.emptyState.description")}</p></div>
    <button disabled={createDisabled} onClick={onCreateSession} type="button"><Plus aria-hidden="true" size={16} />{t("webui2.newSession")}</button>
  </section>;
}
