import type { JSX } from "react";
import type { SuperGrant, SuperPlatformSnapshot } from "../desktopServiceBridge";
import type { PlatformCopy } from "./platformCopy";
import { count, displayText, displayTime, safeConfig } from "./platformSafety";

type Props = { grant: SuperGrant; copy: PlatformCopy; language: "zh" | "en" };
export function PlatformGrant({ grant, copy, language }: Props): JSX.Element {
  return <div className="super-platform-grant">
    <dl className="super-platform-metrics">
      <div><dt>{copy.workspace}</dt><dd>{displayText(grant.workspace)}</dd></div>
      <div><dt>{copy.model}</dt><dd>{displayText(grant.model)}</dd></div>
      <div><dt>{copy.revision}</dt><dd>{displayText(grant.revision)}</dd></div>
      <div><dt>{copy.budget}</dt><dd>{count(grant.max_tokens).toLocaleString()}</dd></div>
      <div><dt>{copy.timeout}</dt><dd>{count(grant.timeout_ms).toLocaleString()}</dd></div>
      <div><dt>{copy.expires}</dt><dd>{displayTime(grant.expires_at_ms, language)}</dd></div>
      <div><dt>{copy.autoApprove}</dt><dd>{grant.auto_approve ? copy.enabled : copy.disabled}</dd></div>
    </dl>
    <ul className="super-platform-grant-nodes">{(grant.nodes ?? []).map((node) => <li key={node.id}>
      <strong>{displayText(node.id)}</strong>
      <dl><dt>{copy.reads}</dt><dd>{(node.read_paths ?? []).map(displayText).join(", ") || copy.noPaths}</dd>
        <dt>{copy.writes}</dt><dd>{(node.write_paths ?? []).map(displayText).join(", ") || copy.noPaths}</dd>
        <dt>{copy.turns} / {copy.output}</dt><dd>{count(node.max_turns)} / {count(node.max_output_tokens)}</dd></dl>
    </li>)}</ul>
    <h4>{copy.targets}</h4>
    {(grant.targets ?? []).length === 0 ? <p>{copy.noTargets}</p> : <ul>{grant.targets.map((target) => <li key={target.id}>
      {displayText(target.id)} · {displayText(target.provider)}
      <dl>{(["account", "chat", "thread", "recipient", "reply_to"] as const).map((field) => target[field]
        ? <div key={field}><dt>{field}</dt><dd>{displayText(target[field])}</dd></div> : null)}</dl>
    </li>)}</ul>}
  </div>;
}

// The profile grant lives in the server's configuration, not in ProfileSummary.
// Read only this known contract to preview budgets. Never invent defaults or
// providers; absent/mismatched grants cannot authorize an execution submission.
export function profileGrant(snapshot: SuperPlatformSnapshot, profileID: string): SuperGrant | null {
  if (safeConfig(snapshot.config_json) === null) return null;
  try {
    const config = JSON.parse(snapshot.config_json);
    if (!Array.isArray(config?.profiles)) return null;
    const matches = config.profiles.filter((profile: { id?: string } | null) => profile?.id === profileID);
    const profile = snapshot.profiles.find((item) => item.id === profileID && item.enabled);
    const grant = matches.length === 1 ? matches[0]?.grant : null;
    if (!profile || !grant || grant.workspace !== profile.workspace || grant.model !== profile.model
      || !Number.isSafeInteger(grant.max_tokens) || grant.max_tokens <= 0
      || !Number.isSafeInteger(grant.timeout_ms) || grant.timeout_ms <= 0
      || typeof grant.auto_approve !== "boolean" || !Array.isArray(grant.nodes) || !Array.isArray(grant.targets)) return null;
    return grant as SuperGrant;
  } catch { return null; }
}
