import type { JSX } from "react";
import type { SuperPlatformSnapshot } from "../desktopServiceBridge";
import { PLATFORM_EVENT_TOPICS } from "./platformClient";
import type { PlatformCopy } from "./platformCopy";
import { count, displayWebhookAddress, PLATFORM_LIMITS } from "./platformSafety";

// Closed vocabularies: unknown backend strings never become user-visible text.
const CHANNEL_MESSAGES = {
  channels_started: "channelsStarted",
  transports_started_not_live_acceptance: "channelsStarted",
  channel_configuration_invalid: "channelsInvalid",
  channel_credentials_or_startup_failed: "channelsFailed",
  webhook_bind_failed: "webhookFailed",
} as const;
const DEAD_LETTER_CODES = new Set(["consumer_failed", "lease_expired", "unavailable", "invalid_payload"]);

type Props = { snapshot: SuperPlatformSnapshot; copy: PlatformCopy };
export function PlatformReliability({ snapshot, copy }: Props): JSX.Element | null {
  const letters = Array.isArray(snapshot.dead_letters) ? snapshot.dead_letters.slice(0, PLATFORM_LIMITS.deadLetters) : [];
  const code = snapshot.channel_code;
  const channelLabel = code && Object.hasOwn(CHANNEL_MESSAGES, code)
    ? copy[CHANNEL_MESSAGES[code as keyof typeof CHANNEL_MESSAGES]] : copy.channelsUnknown;
  const address = displayWebhookAddress(snapshot.webhook_address);
  if (!code && !snapshot.webhook_address && snapshot.dead_letters === undefined) return null;
  return <section className="super-card super-platform-reliability" aria-label={copy.reliability}>
    <h3>{copy.reliability}</h3>
    {code ? <p><strong>{copy.channelStatus}: </strong>{channelLabel}</p> : null}
    {snapshot.webhook_address ? <div>
      <h4>{copy.webhook}</h4>
      {address ? <code className="super-platform-endpoint">{address}</code> : <p>{copy.webhookHidden}</p>}
      <p className="super-platform-muted">{copy.webhookHint}</p>
    </div> : null}
    {snapshot.dead_letters !== undefined ? <section aria-label={copy.deadLetters}>
      <h4>{copy.deadLetters} <small>{letters.length}</small></h4>
      {letters.length === 0 ? <p className="super-platform-muted">{copy.noDeadLetters}</p> : <>
        <p role="alert" className="super-error">{copy.deadLetterHint}</p>
        <ul className="super-platform-deadletters">{letters.map((letter) => <li key={`${letter.subscriber ?? ""}:${letter.event.id}`}>
          <dl><div><dt>{copy.safeCode}</dt><dd>{DEAD_LETTER_CODES.has(letter.code) ? letter.code : copy.unknownCode}</dd></div>
            <div><dt>{copy.topic}</dt><dd>{PLATFORM_EVENT_TOPICS.has(letter.event.topic) ? letter.event.topic : copy.unknownStatus}</dd></div>
            <div><dt>{copy.attempts}</dt><dd>{count(letter.attempts)}</dd></div></dl>
        </li>)}</ul>
      </>}
    </section> : null}
  </section>;
}
