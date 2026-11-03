# Deterministic Super router

Public constructor stays `New(*control.Engine, func() []Binding,
func(context.Context, control.Principal, string) (control.Grant, error),
*slog.Logger) *Service`. `Handle(ctx, Envelope)` returns only a safe action/code,
run ID, and `used_llm=false`. There is no model/classifier invocation or execution
kernel in this package. Ordinary language requests clarification.

Bindings use snake_case JSON and exact provider/account/chat/thread/sender
matches: no wildcard mapping, duplicate match, or shared scope with distinct
owners. The caller supplies immutable, concurrency-safe configuration snapshots.
The account ID is globally unique across providers, matching the shared
`super_ingress` account key. The bridge rejects duplicate account registrations.

Only an authenticated adapter constructs Envelope; JSON decoding it is rejected.
`/run` resolves the server-owned profile, then **overwrites** SourceBindingID and
SourceBindingHash using the exact selected binding and `control.Digest(binding)`
before Engine.Start. Account enablement and live binding/profile/grant revocation
remain the control Authority's responsibility at every execution boundary.
`/status id` and `/cancel id` use a non-admin principal and Engine ownership checks.
No approve/link/profile/retry command is exposed externally.

## Command receipts and migration

`engine.Store` must also implement `CommandReceipts`. The SQLite implementation
is confined to `sqlitestore/router_*.go`. The coordinator calls
`MigrateRouter(ctx, tx)` **after the control schema in its existing migration
transaction**; router handlers never lazily create tables or alter schema versions.

BeginCommand atomically reserves event/message/nonce aliases in the **same**
`super_ingress` index used by CreateRun, and records the original run revision and
status. FinishCommand persists a safe response. The command mutation itself still
belongs to Engine; there is no parallel cancel state machine or transaction held
across calls into Engine. A crash after reservation is recovered by replay using
that original revision. A lost cancel receipt can be settled by observing the
cancelled/cancelling aggregate; changed unrelated revisions return conflict,
never fresh-authority cancellation. Concurrent receipt completion is CAS-like
and cannot overwrite a settled response. Status replay returns the original
snapshot, not a newly fabricated response.

Transport retries with new event IDs/nonces but the same message can bind missing
aliases. Content changes or collisions with another command/run fail closed.
No-op rejection/clarification has no execution effect or LLM work; no in-memory
cache is claimed as durable authority.

All tests use fake execution ports that panic if invoked. Real SQLite tests cover
shared dedup identities, restart/pending receipts, content/ownership conflicts,
immutable decisions and coordinator-owned migration rollback. No model or
external channel is exercised. Command receipt persistence is not itself a
user-visible platform message; transports must not bypass the control outbox to
send a reply directly.
