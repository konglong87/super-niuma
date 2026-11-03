# Super channel bridge: wiring and safety

## Public wiring

- `New(Options) (*Bridge, error)`; `Start(context.Context) error`;
  `Close(context.Context) error`; `WebhookHandler() http.Handler`.
- Implements `control.Sender.Send(ctx, Target, noticeID, text) (Receipt, error)`.
- `Health() []Health` returns sorted `account_id/provider/status/code` only.
  Codes are fixed and raw SDK errors, env references/values and URL credentials
  are never health fields. A webhook reports `configured` / `handler_mount_unverified`,
  not proof of a mounted listener or real external acceptance.
- Account configuration fields are snake_case JSON with environment **references**:
  `secret_env`, `app_id_env`, `app_secret_env`, `bot_id_env`, `webhook_url`.
- Options.Accounts and the router's Bindings function must return concurrency-safe
  immutable snapshots. Options.Handler is the router. Options.AuthorizeTarget is
  mandatory for sends: missing means deny. The control plane must validate the
  exact target and current authorization, not just whether its account exists.
- AdapterFactory, Getenv, Now and HTTPClient are trusted test-injection points,
  never request-body options. Default network transport disables proxies, pins
  validated nonprivate unicast DNS answers and does not follow redirects.
  AllowLoopback is explicitly opt-in for local integration tests; defaults false.

## Webhook v1

POST only: `/super/webhook/{accountID}`, no query/alternate encoded path.
Six required string body fields, including an explicit empty thread for root:

```json
{"event_id":"evt-1","message_id":"msg-1","chat":"chat-1","thread":"","sender":"user-1","text":"/run produce a result"}
```

No body field can set scope, owner, authentication, bot, thread-known, grant or
system-origin flags. Unknown/duplicate fields, trailing JSON and non-string
values fail closed. Parse only after authenticating at most 64 KiB of raw bytes.

Headers: `X-Super-Version: v1`, `X-Super-Account`, `X-Super-Key-Id` (currently equal
to the configured account ID), `X-Super-Timestamp` (canonical Unix seconds),
`X-Super-Nonce` (1..128 bytes, no controls/outer whitespace), `X-Super-Signature`
(64 hex digits). The secret environment value must be at least 32 bytes.
HMAC-SHA256 signs these newline-separated lines, with **no final newline**:

```text
v1
POST
/super/webhook/{accountID}
{accountID}/{keyID}
{timestamp}
{nonce}
{lowercase SHA256(raw request body)}
```

Timestamp window is UTC ±300 seconds; comparison is constant-time. There is one
active secret reference per account. Rotation rejects the previous secret and
never clears durable dedup state. Transport authentication is not owner
authorization: the router still requires the exact configured sender binding.
A direct signed command is treated as explicitly addressed (Mentioned=true),
not as a user-controlled mention flag. Optional configured BotIDEnv enables
self-echo suppression; missing configured bot identity fails closed.

The HTTP response is the router's safe Decision. It contains no objective,
profile or worker output. Domain denial/conflict/unavailability maps to a safe
4xx/503. Persistent replay authority is the control/command receipt store.
A rejected or clarification-only message performs no execution and is not a
synthetic accepted run.

## Feishu and sending

The bridge calls the existing Feishu adapter Start for SDK normalization but
never imports or uses `channel/runtime.Service`, SessionResolver or a legacy
runner. Known own bots and provider sender_type=app are discarded; missing bot
identity or unproven thread is not routed. Raw callbacks cannot approve a grant.
A Super-only wrapper fixes ambiguous thread lookup: successful matching provider
message lookup is required to prove a root. Account/credential configuration
changes invalidate an already started transport until it is recreated.

Feishu sends use a deterministic provider UUID, verified reply target and
explicit `reply_in_thread`; returned chat/thread identities must match. A thread
without an explicit reply-message anchor is rejected, not silently delivered to
root. SDK failure, timeout, mismatch or lost receipt remains unknown rather than
claiming exactly-once. All sends are invoked by the control outbox. A Feishu
transport ACK is **not** a sent command notification or human read receipt.

Webhook sends use only Account.WebhookURL plus exact target authorization. No
caller-provided URL is accepted. HTTPS is required (explicit loopback HTTP test
exception); redirects are not followed. The signed JSON notice includes
message_id/text/chat/thread/recipient and an Idempotency-Key header. The receiver
must return a bounded JSON control.Receipt with nonempty message_id, matching
chat and positive delivered_at_ms. Duplicate/trailing/invalid receipts fail
closed. Remote non-2xx/network/timeout outcomes are unknown; no blind retry is
performed here. Only locally proven pre-send rejection is DefinitelyNotAccepted.

## Acceptance boundary

Tests are offline: handler recorders, fake adapters and fake HTTP RoundTrippers
(including SDK requests). No real account, provider, model, app or external
message is used. Real credentials/scopes, actual thread replies, provider replay,
reconnect and native Desktop evidence remain separate acceptance gates. Nothing
in Health, a unit-test receipt or successful compilation closes those gates.
