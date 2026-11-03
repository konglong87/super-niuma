import type { JSX } from "react";

type Props = { checked: boolean; disabled: boolean; label: string; onChange: (checked: boolean) => void };
// A real text span gets the flexible column; never let an anonymous text node
// compete with a checkbox that inherits the legacy app's full-width inputs.
export function SuperConsent({ checked, disabled, label, onChange }: Props): JSX.Element {
  return <label className="super-consent">
    <input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />
    <span>{label}</span>
  </label>;
}
