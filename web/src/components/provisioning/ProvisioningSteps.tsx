import { Check, Circle } from "lucide-react";
import type { Language } from "../../lib/i18n";

export function ProvisioningSteps({ current, labels, language, onSelect }: { current: number; labels: string[]; language: Language; onSelect: (index: number) => void }) {
  return <ol className="provisioning-steps" aria-label={language === "zh" ? "连接进度" : "Connection progress"}>
    {labels.map((label, index) => {
      const isCurrent = index === current;
      const isComplete = index < current;
      return <li className={isCurrent ? "active" : isComplete ? "complete" : ""} key={label}>
        <button className="provisioning-step-button" disabled={index > current} aria-current={isCurrent ? "step" : undefined} onClick={() => onSelect(index)} type="button">
          <span className="provisioning-step-visual" aria-hidden="true">
            <span className="provisioning-step-marker">{isComplete ? <Check size={14} /> : <Circle size={11} />}</span>
          </span>
          <span className="provisioning-step-label">{label}</span>
        </button>
      </li>;
    })}
  </ol>;
}
