# Real-input acceptance fixture

Stdlib-only, isolated support package. It does **not** open a window, drive the
computer, synthesize DOM input, or certify an acceptance run. The parent harness
owns real desktop actions, screenshots, and assertions.

## Integration

```go
fixture, err := computeracceptance.StartFixture()
if err != nil {
    return err
}
defer fixture.Close()

pageURL := fixture.URL() // Open this exact URL using the parent's real UI path.
_ = pageURL              // Do not persist/log its short-lived token fragment.
snapshot := fixture.Snapshot()
// Wait with a deadline for snapshot.Reports > 0 before issuing input.
// Save baseline := snapshot.TotalEvents, perform an actual desktop action,
// then poll fixture.Events() for Sequence > baseline, expected Type/Target,
// IsTrusted == true, and the expected coordinates/text/modifiers.
_ = snapshot
```

Public API:

- `StartFixture() (*Fixture, error)` binds `127.0.0.1:0` and starts its HTTP server.
- `(*Fixture).URL() string` returns the page URL with a per-instance random token
  in the fragment, not in HTTP paths/query strings or embedded assets.
  The page captures it in memory and immediately removes the fragment with
  `history.replaceState`, without reloading, before its first network request.
  Wait for readiness and confirm the address is scrubbed before screenshots.
  Refreshing loses the in-memory token: reopen the original `fixture.URL()` to
  reconnect. The page does not persist the token in browser storage.
- `(*Fixture).Events() []Event` returns retained events in ascending sequence order.
- `(*Fixture).Snapshot() Snapshot` returns a detached copy of events, current page
  state/geometry, receipt timestamp, and retention/delivery counters.
- `(*Fixture).Close() error` closes active connections/listener and waits for the
  server goroutine. Concurrent/repeated calls are safe.

`Event` embeds `DOMEvent` and adds server-assigned `Sequence` and `ReceivedAt`.
Exported `EventType` and `TargetID` constants avoid harness-side string literals.

| Target constant / DOM ID | Acceptance use |
| --- | --- |
| `ClickTarget` / `click-target` | `Click` |
| `DoubleClickTarget` / `double-click-target` | `DoubleClick` (`dblclick`), plus its native click sequence |
| `ContextMenuTarget` / `context-menu-target` | `ContextMenu`; native menu overlay is suppressed after observation |
| `MouseMoveTarget` / `mouse-move-target` | `MouseMove` |
| `ScrollTarget` / `scroll-target` | `Wheel`, `Scroll`, `State.ScrollTop` |
| `TextTarget` / `text-target` | `Input`, `BeforeInput`, IME composition, `KeyDown`/`KeyUp`, modifiers and native selection |
| `DragSourceTarget` / `drag-source` + `DragDropTarget` / `drag-drop-target` | Real press/drag/release path with trusted endpoint events and drop state |

Mouse down/up and focus/blur are also recorded. The fixture includes a drag source and drop target; drag state is only marked complete after trusted native press/drag/release events.
Text input intentionally has a 1024 UTF-16-unit UI limit. Use harmless test text,
for example `中文🙂`; never use credentials. Browser/OS-reserved shortcuts may
not reach DOM listeners. Key selection offsets use UTF-16, not UTF-8 byte counts.
Do not assert text entry from a key event alone; assert `Input.Value` and/or
`Snapshot.State.InputValue` too.

## Geometry and evidence

`Snapshot.Geometry` reports:

- Window screen position, inner/outer size, DPR, page scroll, screen dimensions,
  available screen rectangle, and visual viewport offsets/size/scale when present.
  `AvailableScreenOriginKnown` is false when the browser omits the available
  screen origin; in that case its rectangle x/y are placeholders, not evidence.
- Each target's `getBoundingClientRect()` and center in **client CSS pixels**.
- An optional `Calibration` containing the **actual** client/screen pair and
  timestamp of the latest trusted pointing event. The page clears it when
  window/viewport geometry changes.

The page displays client centers and clearly labeled screen-center estimates:
`target.Center + Calibration.Screen - Calibration.Client`. No window title-bar
height is guessed. Before a trusted pointing event the screen estimate is unknown.
Window screen position and inner/outer size alone do not identify the content
origin. The harness must account for screenshot crop/display origin, screenshot
scale, browser zoom and visual viewport scale; do not blindly multiply every
screen coordinate by DPR. Recalibrate after moving/resizing/zooming the window.

`IsTrusted` is copied directly from the browser. Untrusted events are retained,
not discarded or promoted. This is not cryptographic attestation or proof of
physical human input: browser-generated events such as programmatic focus can
also be trusted. A token holder can forge HTTP reports. Combine event assertions
with the parent's actual desktop action path and native screenshot evidence.
Server tests deliberately submit fixture payloads; these are **not** real-input
acceptance evidence.

## HTTP boundary and resource limits

- Public, token-free assets: `GET /`, `/fixture.js`, `/fixture.css`.
- Protected: `POST /events` (`Report` JSON), `GET /snapshot`, `GET /config`.
  Send the URL fragment in the exported `TokenHeader` (`X-Fixture-Token`).
- Both socket peer and Host are checked. Supplied Origin must exactly match this
  fixture's HTTP origin; no CORS, cookies, external resources or frame embedding.
- Wrong token/origin/peer/host: 403; method: 405; media type/encoding: 415;
  oversized body: 413; malformed, unknown-field or out-of-bounds report: 400.
- Server retains the latest `MaxEvents` (512) events; `Dropped` counts overwritten
  events. `TotalEvents` and monotonically increasing event sequences never reset.
- `MaxBatchEvents` = 8; `MaxRequestBytes` = 128 KiB (also enforced for streaming
  bodies); text fields <= `MaxTextBytes` = 4096 UTF-8 bytes; labels <= 128 bytes.
  Geometry arrays and numeric values are bounded. Requests have server timeouts.
- Browser queue <= 512 events, local visible log <= 18 lines, one in-flight report,
  150ms reporting interval, 3s fetch timeout. Oversized batches are split.
  `Truncated` / `InputTruncated` flag text clipping. `ClientDropped` counts queue
  overflow and failed/uncertain delivery; a failed event batch is not retried.
- Heartbeats contain real current state/geometry but no fabricated input events.
  `UpdatedAt` is server receipt time, not action time. Wait for expected events
  before closing/navigating away. Reopening the page resets browser-side counters;
  prefer a fresh fixture for each acceptance run and a single page per instance.
- All data remains in memory. There are no logs, credentials, or persistent state.

For strict acceptance, fail/investigate any new `Dropped`, `ClientDropped`, or
truncation flag rather than treating incomplete evidence as success.

## Verification / change ledger

Scope: this package only. Initial working tree was clean; the starting main was
fast-forward checked. Concurrent parent-agent desktop/docs changes are untouched.
No commits or pushes are performed for this delegated slice.

Run:

```sh
go test -race -count=1 ./internal/computeracceptance
go vet ./internal/computeracceptance
node --check internal/computeracceptance/assets/fixture.js
```

Tests cover HTTP guards, JSON/schema/value/body bounds, Unicode and trust flag
preservation, FIFO ring eviction, deep snapshot isolation, concurrent access,
embedded assets/config, actual loopback HTTP service, and active-connection/server
cleanup. Local results: race-enabled tests passed (97.4% statement coverage),
20 repeated package runs passed, `go vet` and JS syntax checks passed.
Real browser rendering, IME/hotkeys, coordinate calibration and desktop
screenshots remain the parent agent's acceptance responsibility.
