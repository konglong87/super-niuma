# Approved-session Unix bridge client

`NewClient(Config{SocketPath: absoluteSocketPath, Token: token})` returns
`(*Client, error)`. `RequestTimeout` defaults to 15 seconds when zero; negative
values are invalid. Constructor validation does not dial or create a socket.
The token and socket path are trusted configuration, never model input.

## Application API

`Client` implements `computeruse.Service`:

- `Capabilities(ctx, owner, sessionID) (computeruse.Capabilities, error)`
- `Observe(ctx, owner, computeruse.ObserveRequest) (computeruse.Observation, error)`
- `Execute(ctx, owner, computeruse.Action) (computeruse.ActionReceipt, error)`
- `Pause`, `Resume`, `Stop(ctx, owner, sessionID) error`

The package's `Service` additionally requires:

- `ObservationImage(ctx, owner, sessionID, observationID) ([]byte, string, error)`

The optional `SessionLookup` interface supplies:

- `Lookup(ctx, owner) (string, error)`

There is **no Start or approval API**, and no approval flag in `Request`.
`Lookup` only returns a session already approved and bound by the trusted host
UI. It must not create, approve, resume, or reassign a session. Client instances
are concurrent-safe and do not cache ownership, lookup results, or images.

The complete `SessionOwner` triple is required and forwarded unchanged. Its
numeric conversation `SessionID` is distinct from the string host `session_id`.
Each owner component must be in `[1, MaxInt64]`; this also rejects negative
signed IDs that an upstream adapter accidentally converted to the domain's
`uint64`. No fixed tenant/user or one-user-to-one-session mapping is assumed.

## Shared HTTP wire contract

- `POST http://localhost/command`, over the configured Unix socket only.
- `CommandPath`, `AuthorizationHeader`, and `BearerPrefix` are exported.
- Standard `Authorization: Bearer <token>`; request and accept type are JSON.
- No browser Origin, proxy, redirects, HTTP compression, connection pooling,
  idempotency keys, automatic retries, or background reconnect loop.
- `Request`: `op`, `owner`, optional `session_id`, `observe_request`, `action`,
  and `observation_id`. The nested request/action session must equal the outer
  session. Extraneous operation-specific inputs fail validation.
- A successful HTTP response is status `200`, wrapping operation data as
  `Response{Data: json.RawMessage, Error: optionalString}`. Remote error text
  is never propagated. Only execute may combine a failure receipt and error.

| Operation constant / wire value | Request extras | Successful `data` |
| --- | --- | --- |
| `OpLookup` / `lookup` | None | `LookupResponse{SessionID}` |
| `OpCapabilities` / `capabilities` | `session_id` | Direct `computeruse.Capabilities` |
| `OpObserve` / `observe` | `session_id`, `observe_request` | Direct `computeruse.Observation` |
| `OpExecute` / `execute` | `session_id`, `action` | Direct `computeruse.ActionReceipt` |
| `OpPause` / `pause` | `session_id` | `SessionResponse{SessionID}` |
| `OpResume` / `resume` | `session_id` | `SessionResponse{SessionID}` |
| `OpStop` / `stop` | `session_id` | `SessionResponse{SessionID}` |
| `OpImage` / `image` | `session_id`, `observation_id` | `ImageResponse` |

`LookupResponse` aliases `SessionResponse`; their JSON is
`{"session_id":"existing-approved-session"}`. `ImageResponse` requires matching
`session_id` and `observation_id`, `media_type: "image/png"`, and `image_data`
containing standard base64 PNG bytes (not a data URL or a nested `data` field).

The client enforces:

- Request body: **32 KiB**, validated before network access.
- Response JSON: **24 MiB**, including chunked/unknown-length responses.
- Compressed PNG: **16 MiB**; decoded image: **32 Mi pixels**.
- Strict JSON: one object, no trailing values, unknown typed fields, duplicate
  keys, case aliases, invalid UTF-8, or nesting deeper than 32 levels.
- Bound observation/session IDs, control acknowledgements, receipt action and
  session IDs, terminal outcomes, verification values, and image IDs.
- PNG MIME, strict base64, decoded byte count, pixel count, and full PNG decode.

Observe also validates requested display/window binding, positive dimensions
and scale, observation timestamps, and domain capability validation. The host
remains authoritative for approval, ownership, freshness, screenshot coordinate
bounds, action budgets, deduplication, lifecycle, and execution serialization.

## Failure semantics

Errors are exported generic sentinels. Transport errors retain only safe
`context.Canceled` / `context.DeadlineExceeded` identity where applicable. Host
strings, response bodies, socket paths, tokens, and raw images are not logged or
embedded in errors. Receipt error messages are removed and codes/summaries are
replaced with locally generated values.

Before an execute dispatch attempt, local rejection returns an empty receipt
and an error. After an attempt, transport failure, malformed/truncated response,
invalid binding/outcome, or a contradictory success-plus-error returns an error
**and a bound `OutcomeUnknown` / `VerificationUnknown` receipt**. A valid bound
failure receipt is preserved with a generic error. Never retry an ambiguous
input automatically. An explicit subsequent Stop is independent of an in-flight
or failed Execute and cannot replay that action.

The host must serve only on a private owner-only Unix socket, validate the bearer
token, reject browser Origin/cross-host requests, and reauthorize the complete
owner/session binding on every operation. Client validation is not authority.

## Verification

Headless tests use actual Unix listeners, request/response fixtures, and PNG
fixtures. Test socket directories are created within this package and removed
by test cleanup; no screenshots, tokens, logs, or generated images are persisted.

```sh
go test ./internal/computerbridge -count=1
go test -race ./internal/computerbridge -count=1
go vet ./internal/computerbridge
```

## Desktop production composition

The Wails host starts `StartListener` under its private app-data directory. It
passes the socket configuration to its own local server through an inherited
pipe (`NewLaunchFile` / `ConsumeLaunchConfig`). Only the descriptor number is in
the environment. The secret is never persisted; the server clears the marker and
closes the descriptor before tools or subprocesses run. Restarting the local
server gets a new launch pipe; quitting the host closes the endpoint.

The native approval input takes a **conversation ref**, not tenant/user IDs. The
host resolves it against its authenticated local server. That endpoint uses the
server's configured desktop identity, resolves a bound tenant context, and calls
existing SessionControl ownership checks. Browser headers cannot choose the
actor. A live Controller cannot change owners: Stop is required before granting
another conversation. A local preview has conversation ID zero and remains
invisible to the model bridge.

For each normal query, after final provider/agent-model selection, runtime
configuration must explicitly declare image input for every effective route:

```json
{
  "computerUse": {
    "imageInputRoutes": [
      {"provider": "primary", "model": "YOUR_VERIFIED_IMAGE_INPUT_MODEL"}
    ]
  }
}
```

This is an operator assertion, **not image-capability discovery and not desktop
approval**. Do not enable a route merely because its protocol accepts image
fields. `provider` matches the actual runtime route name: selected/named provider
name, otherwise `primary` or `fallback-N`. Each effective fallback's model must
also be declared. Model IDs match exactly; there are no wildcards. An explicit
empty list revokes all image routes. Changing endpoints/models requires renewed
capability validation by the operator.

Without a declaration, full trusted identity, or an already approved host
session, ordinary chat remains usable but ComputerUse is absent. Model requests
cannot supply this configuration or approve their own access. With all gates
satisfied, the normal Query registers the ComputerUse tool and receives trusted
guidance with its approved host-session ID. Subagents do not inherit the grant.

The native desktop UI approval/preview, fake-provider runtime integration, and
real model planning are separate acceptance layers. Passing either of the first
two does not establish an autonomous real-provider end-to-end run.
