# Isolated Super execution adapter

This package reuses `query.New` / `RunWithCallbacks` for a **fresh session per
attempt**, not an existing interactive Session. The resolver supplied to `New`
is the only source of provider configuration. It must return a trusted snapshot,
never load configuration from the worker workspace.

## Control-plane integration

- `RouteFingerprint(ctx, provider, model)` binds public route identity, protocol,
  endpoint, model and effective Responses settings. It excludes credentials and
  unselected routes. Persist it in the grant and compare it on every authority
  check; changing an endpoint requires new authorization, rotating a key does not.
- `Request.CheckAuthority` must check the live grant, claim token/epoch, expiry,
  cancellation and resource ownership. It runs before each model request and each
  tool, including just before a write is published. The caller owns deadlines,
  cancellation monitoring and cross-attempt writer exclusion.
- `Budget.Reserve` precedes **each** actual model HTTP request. The reservation is
  at least the larger of internal/actual wire JSON byte counts plus conservative
  framing and the requested output-token maximum. A unique attempt/request ID
  identifies settlement. Incomplete/missing/invalid usage and transport errors
  retain the reservation. A complete, strictly validated usage report is settled
  even if the output is rejected (including truncation, empty text and unsupported
  output); execution success and trustworthy accounting are independent. Settlement is bounded cleanup and may run
  after cancellation; it never starts another model request.
- `Result.Writes` contains only receipts from successful trusted `super_write`
  operations. Each carries the exact relative path, byte length, SHA-256 and
  `AttemptID` (the control-plane claim token). A later write supersedes that path's
  intermediate receipt. Writing the same bytes again is a real fresh action;
  reading an old file or claiming a write in model text creates no receipt.
  Receipts can exist on a failed attempt: only a **successful current claim** may
  enter independent verification. The verifier must reread and hash the file.

## Capability and transport boundaries

- Nil/empty path slices register no corresponding tool. Blank entries, `.` and
  wildcard grants are invalid. Prefix `a` authorizes `a` and `a/...`, not `ab`.
- `super_read` reads UTF-8 files or bounded immediate directory entries;
  `super_write` publishes bounded UTF-8 files with atomic rename. Both use the
  bound `os.Root` and component-wise `openat(O_NOFOLLOW)` on macOS/Linux. Symlinks,
  hardlinked files, special files, hidden paths, credential/state paths and known
  verification-fixture/policy directories fail closed. Other OSes fail preflight.
- Only the two explicit guarded tools exist. No shell, hooks, MCP, Agent/Task,
  worktree creation, recorder, inherited telemetry sink or automatic compaction
  is enabled. A globally enabled prompt dump makes preflight fail rather than
  writing the prompt. No process-global environment/logger settings are mutated.
- The legacy `anthropic.NewClient` has automatic retries/fallbacks and is **not**
  used. The private buffered HTTP adapter supports Anthropic Messages, OpenAI
  Chat Completions, and stateless Responses **text/function-call** exchanges. It
  makes one non-replayable POST over a fresh HTTP/1 connection, with no redirect,
  proxy discovery, retry, fallback, server tools or remote content fetching.
  HTTPS is required except explicitly configured literal loopback HTTP.
- Responses reasoning/encrypted continuation/server-tool items, streaming-only
  providers and unsupported payloads fail closed; there is no compatibility retry.
  Provider errors are replaced by safe sentinel codes. Oversized, malformed or
  truncated results fail instead of being truncated into apparent success.

This is a narrow file capability boundary, **not an OS sandbox**. It cannot revoke
an operation already in progress, undo prior writes, defeat a malicious same-user
host process moving directories/mounts, or prove application-level acceptance.
The independent control-plane verifier and resource/lease authority remain
required.

## Verification performed in this implementation slice

Fake-streamer tests execute the real query tool loop, not a substitute loop.
Local HTTP fixture tests exercise all three request mappings and prove that
429/500/307/401 responses do not retry, redirect or fall back. These are synthetic
transport tests, **not real-model/provider acceptance**. Tests also cover budget
refusal/unknown usage, cancellation/revocation, output limits, filesystem escapes,
receipts, route drift and discovery/logging suppression.

Run `GOSUMDB=sum.golang.org go test -race ./internal/super/execution` and
`GOSUMDB=sum.golang.org go vet ./internal/super/execution`.

## Safe diagnostics / controller handoff (2026-11-03)

`Result.Diagnostic *Diagnostic` is optional and backward-compatible; it contains
only the following bounded numbers and fixed enums:

- `Code ErrorCode` (`code`): stable safe root cause. Persist `FailureCode(err)`
  instead of `err.Error()`; nil gives `""`, unknown errors give
  `execution_unknown`. Diagnosed errors preserve `errors.Is` for safe sentinels.
- `HTTPStatus int`, `ResponseBytes int64`, `RequestIndex int`: HTTP status (or 0),
  bytes actually read (at most response limit + 1), and one-based request index
  (0 before a model admission).
- `MediaType MediaType`: `unknown`, `missing`, `json`, `event_stream`, `text`,
  `html`, `other`, `invalid`; no raw Content-Type or parameters.
- `BodyState BodyState`: `unobserved`, `partial`, `complete`, `size_limit_hit`.
  Complete means the bounded HTTP body finished reading, not output success.
- `UsageState UsageState`: `not_observed`, `missing`, `partial`, `known`, `invalid`.
  Known means validated counters, not successful execution. `Result.UsageKnown`
  describes aggregate attempt accounting; the diagnostic describes the last or
  failed request. Non-2xx bodies are never read/trusted for refund.
- `FailureStage FailureStage`: `none`, `unknown`, `request`, `authority`, `budget`,
  `network`, `http`, `media_type`, `body`, `json`, `usage`, `output`, `callback`.
- `CompletionKind CompletionKind`: `unknown`, `complete`, `tool_call`, `truncated`,
  `refused`. `ContentKind ContentKind`: `unknown`, `empty`, `text`, `tool_call`,
  `reasoning`, `mixed`, `unsupported`. These describe shape only.

The private typed rejected outcome carries **only validated numeric usage**, no
partial stream/text/tool messages. The meter settles it on the error path; query
cannot recover it via `PartialStreamError`. Unknown/malformed usage cannot refund
reservations or authorize tools. No reasoning continuation, unknown server tool,
retry, fallback, or provider compatibility path has been enabled. JSON media type
is required for accepted bodies. Both diagnostics serialization and logs enforce
the enum/numeric whitelist; there are no URLs, headers, body/reasoning values,
credentials, or raw errors.

Regression evidence is synthetic/offline: real query + HTTP RoundTripper fixtures
cover HTTP 200 known usage 7/9 with length/null/reasoning-only output; rejected
writes, all three protocols, HTTP failures, timeout/partial body, size/media/JSON
failures, absent/partial/invalid/duplicate usage, budget overrun, cancellation,
revocation, settlement failure, privacy, and legacy result encoding. Existing
loopback HTTP tests still exercise one-POST/no-retry behavior and successful tools.
This does **not** prove the earlier real-provider failure had the same cause and
is not a second model acceptance run.

Slice ledger: implementation and offline tests stay inside `execution/**`;
pre-existing reservation-ID / hashed-attempt logging changes are preserved.
Controller persistence/UI and any later paid acceptance remain owned by the main
agent. No commit/push in this delegated slice, per explicit instruction.
