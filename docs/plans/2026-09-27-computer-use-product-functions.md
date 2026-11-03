# Computer Use functional parity — implementation ledger

## Current ordered queue — 2026-10-16

### Current public status

The core Computer Use runtime and the current macOS single-display model closure are
complete for Developer Preview use. The latest tagged desktop build passed the
focused/race/Python/native/helper/build/codesign gates. On 2026-10-16 the local
Mac completed live helper-kill fault acceptance and Retina pointer screenshot
acceptance without sending model input. On 2026-10-19 the same current build
completed the full screenshot-backed bridge-disconnect acceptance without sending
model input. The existing fresh
`gpt-6-sol/high` run completed `launch → click → Command+A → type → send → wait →
reply screenshot → Stop` in one build/session/run. The local screenshot and JSON
evidence are intentionally ignored and are not committed to the public repository.

This ledger does not claim formal Developer ID signing/notarization/distribution,
physical second-display acceptance, or Windows/Linux native GUI acceptance. The
P0 permission-revoke, helper-fault, bridge-disconnect, worker-exit, and no-replay
lifecycle matrix is now implemented and covered by deterministic Go/native tests;
no fresh model retry was used for this hardening pass.

The runtime contract is specified in
`docs/plans/2026-10-10-computer-use-runtime-rearchitecture.md`; the stability
history is in `docs/plans/2026-10-08-computer-use-stability.md`.

| Priority | Remaining gate / dependency | Status |
|---|---|---|
| P0 | Immutable duplicate/lost-ACK action facts | Completed/unknown action IDs now return the original committed turn without redispatch or overwriting receipt. A canceled duplicate of an in-flight action returns bound unknown metadata, never zero/not_started. Bounded metadata cache excludes PNG bytes. Focused/race regressions pass. |
| P0 | Consistent native passive-wait acknowledgement across atomic turn/bridge/tool | Committed/pushed 714cf13; 297 native safety assertions plus focused Go/race and 469 platform assertions pass. Current official build passive-wait smoke now returned `executed/complete` with ready same-turn observation and PNG, followed by legal Stop. No validation relaxation. |
| P0 | Production request/context contract and first-action guidance | Closed for the current macOS Computer Use route. Offline production Responses HTTP regression proves requested model/high, exact 2048-token cap, latest-only PNG and preserved tool receipts. Shared prompt fixture prevents test/harness drift. Closed targets use first-call atomic launch in both guidance surfaces. |
| P0 | Runtime lifecycle/control audit | Closed for deterministic/runtime scope. Typed runtime states/faults now distinguish ready, degraded, stopping, stopped, and faulted; bridge status carries redacted fault metadata; worker exit is observed without stale authority; Stop falls back to the authenticated process handle when the bridge is unavailable. Permission revoke pauses with `permission_required`, invalidates observation, requires capability refresh + explicit Resume + fresh Observe, and never replays. Helper transport/response faults quarantine the helper, preserve unknown/complete receipt semantics, and allow manager replacement only through explicit recovery. Focused/race Go, native safety/platform, full Go, tagged desktop build, and strict codesign gates pass. Live model closure was not rerun. |
| P0 | Native activation/launch ambiguity audit | Typed activation now distinguishes read-only failure from possible AX effects; no ambiguous raise is retried and original PID/bundle/window identity is carried across recovery. Native unknown/lost-ACK launch remains unknown and quarantines the helper. AX inspection records only the exact read stage, raw AXError, attribute name, and CF type ID; the WorkBuddy AX-geometry incompatibility now has a strict exact-Window-Server fallback with no replay. Current official build deterministic launch/observation/PNG/Stop smoke passed; no input was sent. |
| P0 | Current-build automated/native gates | Closed for the current macOS build. Focused Go/race, Python acceptance, native safety/platform, helper build, tagged official build and strict codesign passed. |
| P0 | Fresh gpt-6-sol/high closure after all deterministic gates | Closed on 2026-10-15 for the current macOS single-display Developer Preview. One fresh build/session/run completed launch → click → Command+A → type → send once → wait once → real reply screenshot → model Stop; all receipts were executed/complete and no input was replayed. |
| P1 | Stop convergence after caller timeout | Completed in `cb82080`: Stop is retryable, performs bounded graceful shutdown, then SIGKILL fallback and final exit confirmation. Regression and race tests pass. |
| P1 | Live helper/bridge fault acceptance | Helper-kill acceptance completed in `64c05c9`/`4caaa41`; full bridge-disconnect acceptance completed on 2026-10-19 against the current tagged build: real observation PNG, bridge unlink, fail-closed status, process-handle Stop, and helper/runtime cleanup. No input action was sent. |
| P1 | Retina pointer screenshot acceptance | Completed on 2026-10-16: real AppKit pointer overlay screenshot and geometry passed on the built-in Retina display (`2704×1756`, scale `2`); evidence is under ignored `desktop-v2/build/validation/20261016/pointer-acceptance/`. |
| P1 | ScreenCaptureKit migration | Implemented in `fb19bcf`: ScreenCaptureKit is preferred on macOS 14+, with exact geometry/size checks and the existing Window Server path retained as a bounded compatibility fallback. Native safety/platform and tagged helper build pass. |
| P1 | Native diagnostics privacy | Completed in `8b26c23`: native diagnostics are silent by default and only enabled through an owner-only explicit path; no unconditional `/tmp/swift-mismatch.log` remains. |
| P1 | Historical native control/display follow-ups | Second physical display remains hardware-deferred; formal signing/distribution remains user-deferred. Do not promote old screenshots to current evidence. |

`model-ca023a7-final-01` completed click/hotkey/type/send once/wait once and model
Stop on the requested route. Native wait completed with a screenshot, but its
executed/not_started reply failed atomic contract validation and became unknown;
no reply screenshot was delivered to the model. The saved send screenshot was
visually inspected and shows the target preparing execution, **not a reply**.
This run is a failure, not a closure pass. Concurrent community/documentation
work is outside this task's write set and must never be included in its commits.


### Phase update — local macOS reliability hardening — 2026-10-16

The local macOS hardening pass is complete for the current single-display Developer
Preview. Runtime Stop now converges even when the caller deadline expires: the
parent retries Stop, waits through a bounded graceful phase, escalates to SIGKILL
only when necessary, and confirms process exit. A later explicit Stop can retry
after an earlier timeout; no receipt or replay policy is changed.

The acceptance adapter now exposes a read-only `runtime_status` probe and a
deterministic live fault script. On the current tagged desktop build, the exact
bundled `ComputerHelper` was terminated after a real observation. The next
observe returned an explicit error, runtime status became degraded with
`helper_unavailable`, the session Stop and runtime Stop both converged, and no
input action was sent.

macOS capture now prefers ScreenCaptureKit on macOS 14+ while retaining the old
Window Server capture as a bounded fallback. Existing target/window/focus/size
checks remain in Engine before and after capture. The real Retina pointer gate
passed with a `2704×1756` pixel capture, scale factor `2`, and verified AppKit
coordinate conversion. Native diagnostics are now opt-in through an owner-only
path and do not create an unconditional `/tmp` log.

Verification: focused Go/race tests, native 313/469 assertions, helper build,
tagged desktop build, strict codesign, live helper-kill fault acceptance, and
real pointer screenshots. No fresh model retry was performed.

### Phase update — P0 runtime fault lifecycle matrix — 2026-10-15

The highest-priority deterministic runtime hardening pass is complete. The
implementation now has one platform-neutral runtime lifecycle contract:
`starting → ready → degraded → stopping → stopped/faulted`, with redacted
`RuntimeFault` metadata and stable error codes. The standalone worker is watched
by the parent supervisor; unexpected worker exit becomes terminal `worker_exited`
without exposing process details, while bridge disconnect is fail-closed for
window-close protection and explicit Stop can still signal/wait on the process
handle.

The safety matrix is explicit and covered by regression tests:

- Permission revoke before dispatch returns a bound rejected/not-started turn with
  `permission_required`; the old observation is invalidated, no input reaches the
  backend, and recovery requires capability refresh, explicit Resume, fresh Observe,
  then a new action ID.
- Permission revoke or helper loss after dispatch preserves `executed/complete`
  or `unknown/partial` exactly as returned; unknown is terminal with
  `RetryNever`, the session cannot Resume, and a new action ID cannot replay it.
- Helper observe/transport faults are reported as the stable
  `helper_unavailable` code, quarantine the exact helper, and replace the backend
  only after a capability probe; approval survives, observation does not, and
  recovery is explicit.
- Runtime bridge status reports `ready`, `degraded`, or `faulted` plus redacted
  fault metadata; it never grants authority or reconstructs a turn.

Verification for this phase: focused `internal/computeruse`, macOS backend,
bridge, and `desktop-v2` tests; focused `go test -race`; full `go test ./...`;
313 native safety assertions; 469 native platform assertions; fresh tagged
`computeracceptance` desktop build; and deep strict local codesign. No fresh model
request, input, or replay was performed.

### Phase update — standalone bridge disconnect recovery — 2026-10-16

The current bridge-disconnect slice is implemented and pushed in `bbe80f2`.
The Wails acceptance owner now treats the Unix bridge as a control plane rather
than the runtime owner: `runtime_stop` falls back only for an authenticated
transport failure to the parent-held `RuntimeSupervisor` process handle. Bridge
logical errors are returned unchanged, so a normal session/revocation failure
can never be bypassed by killing the runtime. The acceptance-only
`runtime_disconnect_bridge` operation unlinks only the already-authenticated
standalone endpoint and does not add a provider fallback to the legacy
`execute + image` path.

The freshly built `computeracceptance` app passed a live no-observe bridge
disconnect proof: status was `ready`, the endpoint was disconnected, a second
status call failed closed with transport error, and `runtime_stop` returned
`stopped` while the standalone runtime process exited. Evidence is retained in
ignored `desktop-v2/build/validation/20261016/runtime-bridge-disconnect/` without
credentials or raw logs. The full screenshot-backed start/observe variant was
not run because the macOS preflight reported `Online: Yes` and `Display Asleep:
Yes`; no lock-screen bypass, permission relaxation, or input replay was used.

Verification: focused and full Go tests, focused/full race tests, Go vet, Python
acceptance/doc tests, native 314/469 assertions, fresh tagged desktop build,
strict codesign, and the live bridge-disconnect process-handle proof passed.
No fresh model request was made.


### Phase update — screenshot-backed bridge disconnect closure — 2026-10-19

The environment blocker from the 2026-10-16 run is closed after the display was
awake. The current tagged `computeracceptance` desktop build completed the full
deterministic bridge-disconnect acceptance in one fresh build/session/run:

```text
start
→ observe real screenshot
→ disconnect standalone bridge
→ runtime_status fails closed with transport error
→ runtime_stop falls back to the parent process handle
→ helper and standalone runtime both exit
```

The run sent zero input actions and did not start a model request. The real
observation screenshot was visually inspected and is retained under ignored
`desktop-v2/build/validation/20261019/runtime-bridge-disconnect/`; the manifest
records `passed: true`, `stop.state: stopped`, and both process cleanup checks
true. No focus, permission, screenshot, or replay guard was relaxed.

Verification: official tagged desktop build, deep strict codesign, full
screenshot-backed bridge-disconnect acceptance, visual screenshot inspection,
process/socket cleanup, and final `HEAD == origin/main` gate.


### Phase update — release candidate reproducibility and fault recovery UX — 2026-10-19

The next P0 productization slice is implemented and pushed. Desktop release-candidate
builds now require a clean worktree when `COMPUTER_USE_RELEASE_CANDIDATE=1`, embed
source commit, version, Go/Node/Wails versions, target, signing mode, build ID,
and helper/service hashes, and self-verify the sealed app with
`verify-computer-use-build.py`. The verifier never reads credentials, screenshots,
provider requests, or runtime logs.

The desktop bridge now exposes redacted `GetComputerRuntimeStatus` and explicit
`StopComputerRuntime`. The Computer Workspace polls lifecycle status, maps stable
fault codes to user-facing recovery guidance, disables new observe/pause/resume
when a runtime fault blocks safe continuation, preserves Stop/runtime Stop, and
explicitly states that the previous action will not be replayed. Permission revoke
remains recoverable only through the existing permission refresh and explicit Resume
path; bridge/helper/worker ambiguity remains fail-closed.

Verification: focused Go/race desktop and bridge tests, 119 Computer Use frontend
tests, frontend typecheck, build-manifest verifier tests, public documentation tests,
fresh `computeracceptance` release-candidate build, generated Wails bindings,
deep strict codesign, and live bridge-disconnect process-handle acceptance. No
fresh model request or input replay was performed.


### Phase update — deterministic model-deadline root cause — 2026-10-13

- The latest fresh run `model-f574e81-final-01` executed native `launch_app`,
  `click`, `command+a`, and `type` successfully; native work took 7.441s.
  The run stopped at the 110s operational boundary before the required send.
- The acceptance prompt is 1428 Unicode characters. The Computer Use fast-path
  classifier deterministically rejected it at the 1200-character limit. The
  phrase `不要使用 Bash` also motivated a negated-term regression, but has not
  been established as a second rejection cause for the exact punctuated prompt.
  The run retained code context and grew from 19412 to 39092 input tokens;
  reduced-context run `model-ca023a7-final-01` started at 3626 input tokens.
- Commit `cc9e806` raises the bounded classifier ceiling to 2000 Unicode
  characters and ignores blocked terms only when they occur in a negated safety
  clause; positive coding/shell intent remains excluded. Exact acceptance-prompt
  regression coverage, normal-code non-regression, focused Go/race tests, and
  native fake safety assertions pass. A new tagged desktop build from this commit
  passes deep strict codesign verification.
- This is a deterministic context-path fix, not proof that all provider latency
  is solved. The subsequent `ca023a7` run exposed the native wait contract defect
  described above. Finish the deterministic queue before another model gate;
  no prior input or screenshot may be replayed or mixed into fresh evidence.

Second physical display and formal signing/distribution remain explicitly
user-deferred. Optional GitHub binary release remains externally blocked.

### Phase update — granular AX read-stage diagnostics — 2026-10-13

The current native launch blocker is still `ax_window_unreadable`, but that
label previously merged application/window inventory, messaging timeout,
window PID, `AXPosition`, `AXSize`, `AXMinimized`, and AXValue conversion
failures. A failing native regression now requires metadata-only diagnostics to
identify the exact stage, raw AXError code, attribute, and CF type ID. The
implementation records no title, text, image, or other window content and does
not alter focus, activation, permission, screenshot, or replay policy. The
next gate is a read-only probe against the current WorkBuddy candidate using a
fresh helper build; no previous unknown launch may be replayed.

### Phase update — strict Window Server fallback for AX geometry — 2026-10-13

A metadata-only probe against the current WorkBuddy process showed
`AXWindows` returning an empty array and the focused AX window returning
`kAXErrorCannotComplete` for `AXPosition`, `AXSize`, and `AXMinimized`, even
though AX trust, role, title, and the exact Window Server candidate were
available. Timeout increases did not change those results, so the blocker was
not provider latency or an AX timeout race.

The native adapter now permits a narrowly enumerated fallback only for those
read-only AX geometry stages, and only after the exact Window Server ID/PID/
bundle is re-read and confirmed visible, frontmost, and focused. Launch fallback
uses `NSRunningApplication.activate` once; any attempted activation is marked
as dispatched, is never retried, and Engine still requires the exact target to
become frontmost before input or capture. Untrusted accessibility, permission
failure, unknown stages, and already-dispatched ambiguity remain fail-closed.
Focused Go/race, 311 native fake assertions, and 469 platform assertions pass.
A fresh official no-model launch smoke is still required; the old unknown
launch is not replayed.

### Phase update — official build preflight blocked by locked macOS session — 2026-10-13

The official tagged build `16b005e` was built and passed deep strict codesign.
A fresh no-model acceptance host reached the helper preflight, but readiness
reported no active displays, `image_supported=false`, and helper-side capture
permission required, so the approved session was not created and **no launch
was dispatched**. Native CUA inspection independently reported that the Mac
session was locked and could not be automatically unlocked. This is an
external desktop/TCC precondition, not a new runtime launch result and not a
reason to replay the previous unknown action. After the user unlocks the Mac,
run exactly one fresh deterministic launch/stop smoke on this build before any
model closure.

### Phase update — unlocked official deterministic launch smoke passed — 2026-10-14

After the Mac was manually unlocked, the official tagged build was rebuilt from
`c39a6a6eb27f35bed75c664a1e3376ee235b40f4`, with `source_dirty=false` and
`web_source_dirty=false`; deep strict codesign passed. One fresh acceptance
session `3cbc3cd0-a4ee-4eef-8628-af52850851c3` executed exactly one new
`launch_app` action `native-c39a6a6-launch-01`. The committed result was
`outcome=executed`, `dispatch_state=complete`, and the same turn carried the
observation plus PNG. The PNG was visually inspected at
`desktop-v2/build/validation/20261014/native-c39a6a6-launch-01/launch-screenshot.png`:
it is a real WorkBuddy window, not a placeholder or stale prior screenshot.
The same session then returned a legal `Stop` with state `stopped`. No click,
type, hotkey, send, wait, or model request was issued. This proves the guarded
native launch fallback and same-turn launch evidence path; it does not claim
the autonomous model closure, which remains intentionally unrun.

### Phase update — current-build passive wait and same-turn media passed — 2026-10-14

A second fresh session `f1752d32-27ed-41bf-90cd-f6e155de39bc` on the same
official build launched WorkBuddy once, then issued exactly one passive wait
with a fresh launch observation. The wait committed
`outcome=executed`, `dispatch_state=complete`, `observation_state=ready`, and
`screenshot_state=ready`; its PNG was visually inspected at
`desktop-v2/build/validation/20261014/native-c39a6a6-wait-01/wait-screenshot.png`.
The session then returned `Stop=stopped`. No input, send, or model request was
issued. This closes the current-build deterministic launch → wait → same-turn
observation/PNG → Stop gate; autonomous model closure remains intentionally
unrun.

### Phase update — single model closure attempt stopped by Wails host shutdown — 2026-10-14

The final closure build was rebuilt and codesign-verified from runtime commit
`9bfe330f3d1a74120b99d9952b721354abf5bcd6`, with
`source_dirty=false` and `web_source_dirty=false`. A single production
`gpt-6-sol/high` request used the exact shared prompt and created session 146;
this is the only model closure attempt in this phase. The model issued exactly
one `ComputerUse` call: `launch_app`.

The host lifecycle, not native launch, then became the blocker. The startup
ledger records `before close` at `11:18:18.667 +08`, immediately followed by
`shutdown begin`; the ComputerUse result at `11:18:18.716 +08` was the bound
non-replayable `outcome=unknown`, `dispatch_state=unknown`, `error_code=inactive`.
The server finished the run as cancelled/context-canceled and the bridge cleanup
also failed because the host had already shut down. No click, command+a, type,
send, wait, or model retry occurred, and no reply screenshot exists for this
run. The run is a failure, not a closure pass.

This exposes a separate P0-E acceptance blocker: the official Wails host can
receive an external/undetermined termination while a provider ComputerUse turn
is active, even though the runtime has an independent supervisor context. Before
any further model request, reproduce this with a no-model host-lifecycle probe,
identify the close initiator, and make the runtime/acceptance harness retain the
host until explicit Stop or test teardown. Do not weaken native guards and do not
replay the unknown launch.


### Phase update — active native window close retained the runtime — 2026-10-14

The official tagged build from `15e1c812c8402ea1d4acf64753f07174f9d1f02e` was
started with the owner-only acceptance socket and an explicitly approved local
Computer Use session. A native CUA click closed the real go-e2e window while
that session was `ready`; this was a window-client close, not a model action and
not a replay. For 20 samples over 5 seconds, the Wails host, local server, and
Computer Helper all remained alive. A post-close acceptance `snapshot` succeeded
against the same session, proving the runtime was still reachable after the
window disappeared. The session then returned an explicit `Stop=stopped`, and
the app was torn down for cleanup.

Evidence is under:
`desktop-v2/build/validation/20261014/window-close-active-session-01/`.
No WorkBuddy input, launch, screenshot capture, model request, or permission
change was performed in this gate. This closes the active native window-close
acceptance for the short-term Wails/client boundary. It does **not** claim that
the runtime survives an external OS process kill or that the full standalone
headless runtime-process extraction is complete; those remain the next P0-E
implementation slice. No model retry was performed.

Verification for this phase:

- CUA native close click against the real go-e2e window passed.
- 20/20 post-close process samples retained host/server/helper availability.
- Same-session post-close acceptance `snapshot` succeeded.
- Explicit acceptance `Stop` returned `state=stopped`.
- Official build manifest and prior focused/race/native gates remain unchanged;
  no source code was modified in this evidence-only phase.


### Phase update — standalone provider runtime survived host termination — 2026-10-14

The official tagged build from `2dbe5f2dcf185c335e6a649adc3317a79b9041d5`
was rebuilt with `source_dirty=false` and `web_source_dirty=false`; the app,
embedded service, and helper passed deep strict local codesign verification. The
Wails host now starts a separate `go-e2e-desktop --computer-runtime-process`
with an owner-only bridge root, an in-memory authenticated handshake, matching
PID/protocol/binary hash checks, and no token persisted to disk. The provider
bridge uses that process; it is not the old in-process Wails manager.

A no-model lifecycle probe started the final build, observed the outer Wails
host, standalone runtime process, local server, and helper together, then sent
`SIGTERM` only to the Wails host. The host and acceptance socket disappeared,
while the standalone runtime became orphaned under launchd and remained alive;
the runtime bridge root also remained present. The worker was then terminated
with an explicit cleanup `SIGINT`. Evidence and the exact build identity are in
`desktop-v2/build/validation/20261014/runtime-process-lifecycle-08/`.

A second final-build native UI gate used the real CUA close-button click while
an explicitly approved local Computer Use session was `ready`. Across 20
post-close samples the host/server/helper remained available, a same-session
acceptance `snapshot` returned `state=ready`, and explicit `Stop` returned
`state=stopped`. Evidence is in
`desktop-v2/build/validation/20261014/native-window-close-final-01/`. No
WorkBuddy launch/input, model request, screenshot capture, or permission change
was performed, and no model retry was made.

This closes the provider-process survival and current-build native client-close
gates. It does **not** claim the complete P0-E target yet: Wails UI/panel
control still has a local compatibility manager path, and a separately defined
runtime-owner Stop/revoke/fault policy must be completed before declaring the
whole runtime lifecycle refactor closed. Unknown or dispatched actions remain
non-replayable.


### Phase update — fixed UI-bridge owner mapping and passed remote atomic native gate — 2026-10-14

The first final-build attempt through the new runtime bridge exposed an adapter
bug before native dispatch: the local UI bridge owner was translated to the
local preview owner, then rejected by the generic conversation-owner check
because its internal `SessionID` is intentionally zero. The bridge returned a
bound `unknown` result, so that action was not replayed and no model retry was
made. A failing regression now pins the separation between the UI bridge owner
and model conversation owners; the adapter permits the UI path only after the
explicit local approval boundary, while model ensure remains denied for that
owner.

Commit `11cc31d` fixes the mapping and adds focused coverage for the local UI
atomic turn and provider/UI authority split. A fresh tagged official build was
created from that commit with `source_dirty=false` and `web_source_dirty=false`;
deep strict local codesign passed. Through the standalone runtime process and
new authenticated UI bridge, one new deterministic local session executed:

- atomic `launch_app` once: `outcome=executed`, `dispatch_state=complete`;
- the same-turn observation and PNG were returned and visually inspected;
- one passive `wait`: `outcome=executed`, `dispatch_state=complete`;
- explicit `Stop`: `state=stopped`.

Real screenshots from the same build/session/run:

- `desktop-v2/build/validation/20261014/native-runtime-bridge-final-02/launch-screenshot.png`
- `desktop-v2/build/validation/20261014/native-runtime-bridge-final-02/wait-screenshot.png`

The screenshots show the real WorkBuddy window and the post-wait desktop state;
there was no model request, click/type/hotkey/send input, or replay of the
previous unknown action. This closes the UI owner mapping defect and the
remote-bridge deterministic launch/wait/Stop gate. The separate runtime process
still intentionally remains alive across Wails host shutdown until its explicit
runtime-owner teardown policy is implemented; that lifecycle policy remains the
next P0-E item.


### Phase update — explicit runtime-owner Stop drained and terminated the worker — 2026-10-14

The official tagged build from `0e1eb659dda3b4cc7f3a1f755022980ecdec803f`
was rebuilt with `source_dirty=false` and `web_source_dirty=false`; the app,
embedded service, and helper passed deep strict local codesign verification.
The bridge now exposes `runtime_stop` only through the supervision owner. It
rejects teardown while a session is active, requires the session Stop/revocation
boundary first, drains the manager, and then signals the standalone worker to
exit. Wails host shutdown does not call this operation.

A real no-model acceptance run against that build returned
`runtime_stop -> state=stopped`; the standalone runtime worker exited while the
outer Wails host and local service remained alive, and the host was then cleaned
up separately. Evidence is under
`desktop-v2/build/validation/20261014/runtime-stop-final-01/`, with the exact
build manifest under the app resources. No model request, WorkBuddy input, or
replay was performed in this gate.

This closes the explicit runtime-owner teardown policy. Permission revoke and
unrecoverable helper-fault handling still require their own deterministic
termination/recovery matrix before P0-E can be fully closed.


### Phase update — current deterministic and automated gates green — 2026-10-14

Against the pushed runtime/control commits and the final tagged desktop build:

- `go test ./...` passed.
- `go test -race ./...` passed; only the repository's existing macOS linker
  `LC_DYSYMTAB` warnings were emitted.
- `python3 -m unittest scripts/computer-use-120s-acceptance_test.py
  scripts/computer_acceptance_safety_test.py` passed all 60 tests.
- `bash native/macos/tests/run.sh` passed 311 native safety assertions.
- `bash native/macos/tests/platform.sh` passed 469 platform assertions.
- `bash scripts/build-desktop-v2.sh -tags computeracceptance` passed, and
  `codesign --verify --deep --strict` passed for the app and nested helper.

No model request was made during these gates. The remaining open acceptance is
only the separately authorized fresh model closure; the prior unknown action is
not replayed.

## Historical ordered queue — 2026-09-30

This queue is authoritative; older phase notes below are historical and must not
be read as evidence that the native overlay or the whole product fully passed.

| Order | Remaining item | Acceptance / dependency |
|---|---|---|
| P0 | Preserve capture/backend error codes and diagnose paused/unknown outcomes | Spotlight activation into full-screen WorkBuddy and a later Observe on the capture-enabled build failed. Current receipts lose the originating helper code; do not claim permissions, focus changes, or PNG size as proven causes. No input replay or weakened safety guards. |
| P0 | Complete native control acceptance across apps and model ownership | Expand, collapse, hide/reopen and Pause verified below. Resume/Stop native-click acceptance and autonomous model-managed preview/control run remain open; manager Stop alone is not a UI pass. |
| P1 | Geometry persistence and broader display-mode/hidden-window acceptance | Host-owned discovery and presentation tests pass; full native restart-position, mode transition, and hidden-main-window acceptance remain separate. |
| P2 — blocked | Optional GitHub Release binary artifacts | Previous tag run blocked by account payment/spending limit; no billing changes made. |
| Deferred | Second physical display | Requires hardware; not passed. |
| Deferred LAST | Formal signing and distribution | Developer ID/notarization/clean installation and upgrade; local ad-hoc builds do not substitute. |

### Phase update — generic target/binding foundation — 2026-10-01

- Replaced the model-visible application allowlist boundary with a generic
  `TargetID` and host-owned `ApplicationTarget` registry. The model supplies
  only a registered target ID; the host resolves the provider launch key and
  window policy before any native launch request.
- Added reusable `TargetBinding` identity validation (`window_id`, owner PID,
  bundle ID, frame, visibility, generation, and bind time). The macOS backend
  now stores and revalidates this binding on every window-scoped observation;
  stale identity fails closed and never falls back to display-level input.
- Generalized the native launch protocol to carry a trusted target descriptor
  (`target_id`, display name, provider bundle key) without WorkBuddy-specific
  logic in the Computer Use core, bridge, or native engine. WorkBuddy is now a
  desktop fixture registration only.
- Added the platform-neutral `RunBudget`/FSM runner with bounded total,
  launch/bind/observe, input, wait, model-turn, and zero-unknown-replay
  budgets. It is independently tested and ready to be wired into the host
  controller lifecycle.
- Generic GUI fast-path guidance/classification no longer requires WorkBuddy;
  existing WorkBuddy acceptance artifacts remain fixture evidence only.

Acceptance boundary: this slice proves generic contracts and fake/native safety
coverage, not a new real WorkBuddy send/reply acceptance. The next slice must
wire the runner into the controller lifecycle, expose registered targets to the
model/tool surface, and add at least one non-WorkBuddy fixture (Calculator or a
native fixture app) before rerunning the 120-second WorkBuddy fixture loop.

### Phase update — trusted WorkBuddy launch and window binding — 2026-10-01

- Native helper slice delivered in `9ade442`: `launch_app` is an allowlisted
  Computer Use session operation for `WorkBuddy` only
  (`com.workbuddy.workbuddy`). It uses AppKit/Window Server discovery, waits
  up to 8 seconds for a visible target window, returns a separate launch receipt,
  and binds window ID, owner PID, bundle ID, and frame for subsequent capture/input.
- Go integration is now wired through the domain controller, desktop agent
  service, authenticated bridge, CLI query service, and `ComputerUse` tool.
  After launch, the backend automatically pins the next observation to the
  discovered WorkBuddy window; window-scoped actions inherit that trusted window
  ID and never fall back to display-level input.
- Controlled error codes and tests cover unsupported application, launch timeout,
  target-window mismatch, stale binding, and launch receipt validation. Focused
  Go tests and native fake-platform tests pass.
- First fresh production-model run on the 3cd7cc3 build stopped safely at the
  launch boundary: the model called `launch_app`, received a rejected receipt,
  and then called Stop. It completed in 107.959 seconds, but produced no
  WorkBuddy window binding or reply evidence and is not an acceptance pass.
  The follow-up fix keeps launch independent from input TCC and preserves a
  controlled launch failure receipt instead of collapsing it to an empty result.
  It also prevents successful launch receipts from being mislabeled as
  `action_failed` when their optional error code is empty, and rebinds launch
  requests to the query-owned session ID rather than trusting model text. The
  bridge now preserves controlled host error codes such as `self_target` instead
  of collapsing rejected receipts to `action_failed`, so the model receives a
  safe corrective instruction instead of losing the target diagnosis.
- A real cold-start run on the `1711455` build then produced a WorkBuddy
  launch receipt and bound window (`bundle_id=com.workbuddy.workbuddy`,
  `window_id=18925`, `owner_pid=23430`, frame `(76,35,1200,792)`) from the
  ComputerUse path in 42.770 seconds. The target screenshot/observation was
  real and WorkBuddy was not manually opened. The model stopped immediately
  because the success receipt was incorrectly labeled `error_code=action_failed`;
  commit `3879b67` fixes that labeling, but a fresh cold-start full send/reply
  run after that fix is still required.
- A later fresh run on the `ae5a9c3` build still spent its first model turn
  attempting a host-window click and timed out at 120.093 seconds; no WorkBuddy
  action or reply evidence was produced. The bridge error-code preservation fix
  and the new fast-path host-window guard are therefore required before the next
  final run; acceptance remains open.
- This closes the implementation dependency for reliable launch/bind, but does
  **not** close the real acceptance gate: WorkBuddy must still be cold-started
  from the latest desktop build and prove real screenshots, receipts, input,
  one reply, Stop, and total elapsed time <= 120 seconds. No real WorkBuddy
  evidence was generated in this slice.

Priority after this phase:

1. Build and cold-start the latest ordinary macOS Wails app, then run the real
   authenticated `gpt-6-sol` WorkBuddy send/reply path with a fresh validation
   directory and inspect every screenshot/receipt.
2. If the real run exposes capture/error-code or focus regressions, preserve the
   originating controlled code and fix only the failing boundary; do not replay
   unknown input.
3. Keep second physical display and formal signing/distribution deferred.

### Compact chat screenshots — user-prioritized follow-up (2026-10-01)

- Initial tree clean at `c71ecaf`; pulled origin/main before editing. No pre-existing
  user work. Intended files: scoped message CSS, browser layout regression tests,
  and this ledger. No backend, image-reference protocol, or live-preview changes.
- Implementation: Computer Use message thumbnails use one divisor (initially 4)
  against the existing 640 px limit, with proportional sizing and a matching
  height bound for tall images. Titles remain readable; original-resolution
  lightbox and ordinary images retain existing behavior. Divisors 6/8 remain
  optional visual tuning, not additional requirements.
- Automated acceptance passed: `ConversationMessage` / `GeneratedArtifactImage`
  focused Vitest suites (23 tests), `npm run typecheck`, focused Biome lint, and
  the full message-experience Playwright file across desktop/narrow/mobile
  (9 tests). Browser geometry checks cover landscape/portrait/square proportions,
  layout height, original-source enlargement, Escape/focus restoration, ordinary
  image isolation, and optional divisor 6/8 behavior.
- Native scripted acceptance passed: ordinary Wails `.app` build and ad-hoc
  signature verification; quit confirmed, cold-started the new build, selected
  an existing persisted screenshot, clicked its thumbnail, inspected the large
  original preview, and pressed Escape back to the focused thumbnail. No model
  run, external message send, or computer-control command was triggered.
- Native evidence (ignored, not committed):
  `desktop-v2/build/validation/20261001/compact-observations/before-thumbnail.png`,
  `desktop-v2/build/validation/20261001/compact-observations/after-thumbnail.png`,
  `desktop-v2/build/validation/20261001/compact-observations/original-preview.png`.
  The same landscape image now occupies approximately one quarter of its former
  width/height, with later message cards visible; the preview remains large.
- Delivery: focused acceptance passed; commit/push receipts and final HEAD vs
  origin/main equality are recorded in Git and the final report. No unrelated
  source changes are included. No implementation/acceptance items remain in this
  bounded slice; user feedback may request divisor 6/8 later.
- Autonomous model acceptance: not required for this CSS-only slice; no model
  run or computer action authorization is changed. Existing P0/P1 queue and
  physical-second-display/formal-distribution deferrals above remain unchanged.

### Screenshot visibility policy — user-requested follow-up

- Initial tree clean at `4a1de20`; pulled origin/main before edits.
- Scope: remove native panel screenshot exclusion in ordinary builds; delete
  the acceptance-only capture switch. No changes to TUI/CLI/browser UI scope.
- Intended files: native panel renderer, capture-policy regression test, deleted
  acceptance-only flag file, and this ledger. Desktop/region screenshots should
  include the visible panel; single-window capture still has its own target.
- Automated checks completed before the user deferred desktop acceptance:
  `go test ./desktop-v2`, `go test -tags computeracceptance ./desktop-v2`,
  ordinary Wails build and ad-hoc signature validation passed.
- User requested commit/push now and native verification tomorrow (2026-10-01).
  **Native screenshot acceptance remains PENDING, not passed.** Resume with a
  cold start of the ordinary build (no capture env/tag), actual preview start,
  and desktop/region screenshot inspection. Old-process quit attempts did not
  confirm exit, so verify process identity rather than assuming a cold start.
- Build log: ignored `desktop-v2/build/validation/20260930/capture-visible/build-normal.log`.
  No native screenshot evidence was produced for this policy change today.
- Known risk: display-wide Computer Use observations may now include the panel or
  its preview recursively. This is an intentional user-visible screenshot policy
  requested by the user; it does not change input authorization or safety guards
  and should be evaluated separately if model observation quality regresses.
- Remaining priority after this slice: P0 capture-error diagnostics and native
  control/model acceptance; P1 geometry; blocked release assets; explicit deferrals.

### Native interaction and preview repair — bounded closure

- Initial tree clean at `ca9ac8d`; pulled origin/main before editing. No pre-existing
  user work. Source slice: host overlay/state/shared types, native renderer,
  read-only preview/tests, frontend native-support/reopen/copy tests and this ledger.
- Root causes confirmed in code: expand/collapse commands were ignored; hiding
  was overwritten by frontend snapshots; metadata-only model status did not carry
  preview bytes. These were project UI defects, not macOS system dialogs.
- Host now owns per-session hidden/expanded state and polls the existing manager
  independently of the WebView. Session/mode changes reset overrides; status
  refresh does not. Commands reject stale sessions; managed sessions remain
  Stop-only. Frontend probes actual native availability and retains a reopen
  launcher; unsupported platforms keep DOM controls. Control errors are surfaced.
- Preview reads existing current-observation bytes, or the latest successfully
  executed receipt's after-image between actions. It does not Observe, refresh
  freshness, grant input authority, or install receipt metadata as an observation.
  Stop/replacement/permission and evidence-source changes reject in-flight reads.
  This is **latest operation imagery, not continuous live video**.
- Native copy now uses “电脑操作”, “已就绪，等待下一步操作”, “操作已暂停”, and
  “最近操作画面 · <window>” rather than `native_host · ready`.
- Historical behavior (superseded by the screenshot visibility follow-up above):
  ordinary builds requested `NSWindowSharingNone`; the tagged acceptance build
  could explicitly enable screenshot visibility.
  Targeted app-window screenshots also differ from composed desktop captures.
  No blanket claim of exclusion from every screenshot method is made.

Verification for this slice:
- Full web tests: **77 files / 775 passed**; TypeScript typecheck passed.
- Desktop, computeruse, macOS backend Go tests passed; desktop race tests passed;
  tagged desktop build and ad-hoc signature validation passed.
- Real native input: project driver clicked Expand/Collapse/Hide; screenshots show
  correct state after polling. CUA clicked the main-window reopen launcher and
  the native panel reappeared. Native Pause click resulted in `paused`; its own
  in-flight test action was interrupted/unknown, not counted as executed success.
- Real preview: `03-preview-visible.png` shows current observation; final build
  `final-after-action-preview.png` shows after-action image while snapshot remains
  `needs_observation` with an executed receipt—without an extra Observe for preview.
- Stop-click attempt was blocked at its prerequisite Observe, before input; it
  is **not a native Stop pass**. Session was then stopped through the manager.
  Resume native-click attempt did not establish a successful state transition.
- Evidence: ignored `desktop-v2/build/validation/20260930/overlay-controls/`,
  including `04-collapsed.png`, `05-hidden-after-polls.png`, `06-reopened.png`,
  `07-paused.png`, `final-after-action-preview.png`, session JSON and build/test logs.
- Cleanup: test sessions stopped, temporary socket/session files and redundant
  screenshots removed. Normal desktop build restored without the capture switch.
- Delivery: this source slice is committed/pushed together; final response gives
  the commit hash. No new tag and no claim of complete product parity.

### Phase 2A closure — automatic display orchestration — 2026-09-30

- Added explicit `auto`, `compact`, and `expanded` display modes with a default of
  `auto` for new preferences; legacy `collapsed` records are normalized without
  breaking existing users.
- Auto mode keeps the idle surface as a small launcher, expands when a session is
  preparing/running/paused/failed or has a readiness error, and collapses after
  Stop. Compact and expanded modes remain user-controlled from Settings.
- Added Settings > General > Computer Use display mode with a cross-component
  browser event so the running workspace updates without restart.
- Auto mode avoids rendering the separate execution-progress overlay while the
  detailed Computer Use surface is visible; this prevents two overlapping panels.
- Verification: ComputerWorkspace tests 29/29; full web suite 76 files / 758
  tests; TypeScript typecheck passed; current desktop `.app` built and local
  ad-hoc signature verified. Native UI smoke used actual clicks: idle launcher,
  manual expansion, local preview approval, live session panel, and Stop.
- Evidence: ignored `desktop-v2/build/validation/20260930/` build log and native
  screenshots. The running app still uses the main WebView DOM, so it cannot yet
  remain above WorkBuddy when go-e2e is not frontmost.

Priority after this phase:

1. Implement Phase 2B native macOS NSPanel adapter and shared snapshot bridge.
2. Re-run real WorkBuddy focus/target/screenshot acceptance with the panel.
3. Then return to optional blocked release assets and explicit deferrals.

### Phase 2B implementation slice — native macOS overlay — 2026-09-30

- Added a project-owned AppKit `NSPanel` adapter instead of modifying the Wails
  module cache. The panel is borderless, non-activating, floating, joins Spaces,
  preserves WorkBuddy focus, supports compact/expanded rendering, drag, Stop,
  Pause/Resume, dismiss-without-stop, screenshot preview, and bounded command
  polling. `NSWindowSharingNone` is used so the overlay is not fed back into the
  Computer Use observation capture; this also means ordinary screen-capture
  evidence intentionally cannot show the panel itself.
- Added a platform-neutral Go controller and Wails `UpdateComputerOverlay`
  snapshot bridge. The existing `computerManager` remains the only authority;
  native commands are checked against the rendered session ID before dispatch.
  Non-macOS builds use a no-op adapter.
- The main WebView now hides its active Computer Use surface when the native
  overlay bridge is available; approval remains in the main window and Stop /
  Pause / Resume are handled by the native panel. Auto mode no longer creates a
  second progress overlay. Idle/stopped auto mode intentionally retains a small
  launcher so users can start a local preview without opening Settings.
- Verification passed: full web suite 77 files / 763 tests, TypeScript
  typecheck, focused Go packages and race suite, tagged normal Wails build, and
  local ad-hoc codesign verification. The macOS app cold-started successfully
  with the native adapter.
- Acceptance boundary still open: because the panel deliberately uses
  `NSWindowSharingNone`, the normal Computer Use screenshot/observation cannot
  be used as visual evidence of the panel. A separate native WorkBuddy focus
  acceptance must verify the panel is physically visible above WorkBuddy without
  contaminating the target observation, and verify native Stop/drag behavior.

Priority after this phase:

1. Run the native WorkBuddy focus/overlay acceptance with independent frontmost
   and panel-window evidence; fix any panel visibility or command-routing issue.
2. Add a user-facing setting/shortcut for “show native overlay” only if the
   acceptance reveals a discoverability gap; current display mode controls remain
   the source of truth.
3. Return to the blocked optional release assets and explicit deferrals.

### Delivery ledger — 2026-09-30

- Initial worktree clean at `42b9b4f`; `git pull --ff-only origin main` up to date.
- Intended tracked files: `CHANGELOG.md` and this queue; no application code changes.
- Implementation: preceding functional slices landed; automated/native scripted
  acceptance and autonomous WorkBuddy model acceptance are separate historical
  evidence, not newly rerun model tests for this documentation-only release.
- Verification passed: packaging regression (1), six focused Go packages,
  Computer Use web tests (83), native safety assertions (244), platform event
  assertions (469), normal Wails build and local ad-hoc signature verification.
- Native UI smoke: launched the normal app, confirmed the ready Computer Use
  panel and collapsed it with an actual click; main workspace rendered. This is
  a handoff smoke test, not a new autonomous-model or full input-matrix run.
- Cleanup slice: removed 683,356,160 allocated bytes of obsolete test binaries,
  duplicate build output and Vite cache; retained current app and evidence.
- Delivered: annotated `v0.2.0` pushed, resolving to `a068279c2ecffb1022795fdf08c96ef3aef477e5`.
  Rebuilt that exact revision without acceptance hooks; embedded sidecar identity
  is `v0.2.0`, same revision, `dirty=false`. Normal app is open; actual native
  click expanded the Computer Use panel and screenshot shows ready status.
- Cleanup complete: Go build/test cache removed another 4,542,111,744 allocated
  bytes; total approximately 4.87 GiB including the earlier cleanup slice.
  Current sealed app, dependencies, user data, screenshots and receipts retained.
- Local handoff artifacts: `desktop-v2/build/validation/20260930/release-v0.2.0/`
  contains `desktop-ready.png`, `build-identity.json`, and `cleanup.json`.
- Tag-triggered Release workflow failed before any step ran: GitHub annotation
  reports recent payment failures or a spending-limit prerequisite. No GitHub
  Release/installer assets were published. This is distinct from source-tag
  delivery; record the blocked optional binary delivery first, then the explicit
  physical-display/signing deferrals. No unblocked in-scope item remains.

## Phase update — P0-1 real host acceptance — 2026-09-28

The rebuilt tagged Wails app (`computeracceptance`) was exercised through the
actual host acceptance socket with a real Chrome window and the isolated trusted
event fixture. The run used the new host-owned batch path, not the old helper
local CGEvent path.

Real evidence under
`desktop-v2/build/validation/20260928/p0-1-real-acceptance/evidence-fix/`:

- trusted left click, double-click, right-click and move;
- trusted drag with source/drop endpoints;
- trusted scroll;
- focus input, Unicode text `go-e2e 中文🙂`, ⌘A selection, replacement,
  backspace and arrow-left;
- all receipts were `executed`; fixture event logs reported `isTrusted=true`,
  `client dropped/uncertain=0`, and final screenshot shows `验收AB` with the
  caret at the expected position;
- key-event inspection proves each ⌘ chord ends with `MetaLeft` keyup and
  `meta=false`; no incorrect `ControlLeft` release remains. This caught and
  fixed the host C batch modifier-index bug in `b6e1e83`.

This closes the real functional input matrix for click/type/key/hotkey on the
current single-screen build. It does **not** yet close the entire P0-1 safety
gate: real desktop helper-SIGKILL during a non-drag batch still needs a
purpose-built deterministic fault-injection run. The Go backend test already
covers deterministic `broker-batch-sigkill` with a host fake driver; that is
strong protocol evidence but not a substitute for the production host/helper
fault-injection evidence.

Priority reorder after this phase:

1. **P0-1 remaining safety gate:** production helper SIGKILL during a real
   non-drag batch, then confirm unknown receipt, no replay, no stuck modifier or
   mouse button, fresh session recovery, trusted fixture state and screenshots.
2. **P0-2:** fresh authenticated `gpt-6-sol` autonomous WorkBuddy loop using
   this rebuilt helper; require launch, visible navigation, New Task click,
   fresh after-observation and Stop with no parent GUI input during the run.
3. **P0-3:** real host-bound Screen Recording/Accessibility revoke and regrant.
4. **P1-1:** exact startup-attempt token for permanent Ensure ACK loss.
5. **P1-2:** authoritative model-created session discovery in desktop UI.
6. **P1-3:** virtual/mocked multi-display topology and mixed-DPI coverage.
7. **P1-4:** final functional regression/evidence audit.
8. Deferred: physical second display and formal signing/distribution.

## Phase update — P0-3 permission lifecycle audit — 2026-09-29

The restart-bound CLI revoke attempt was re-run against the current tagged host:

1. stopped the desktop host/helper;
2. ran `tccutil reset ScreenCapture com.wails.go-e2e` and
   `tccutil reset Accessibility com.wails.go-e2e` successfully;
3. launched a fresh host/helper with a new acceptance socket;
4. queried fresh capabilities.

The fresh host still reported `capture_readiness=ready`,
`input_readiness=ready`, and `permission_state=approved`. This is **negative
lifecycle evidence, not a pass**. It confirms that a successful `tccutil reset`
exit code is insufficient on this development machine and that no code or
report may claim permissions were revoked. The current host remains stopped
again after the audit.

Next gate requires an action-time user-authorized System Settings toggle for
both Screen Recording and Accessibility, followed by: fresh host launch,
failed capabilities/observe/execute evidence, regrant, host/helper restart if
required, fresh capabilities and observe, and proof that no old action replayed.

Priority reorder after this phase:

1. **P0-3 continuation:** user-authorized real TCC toggle/regrant lifecycle;
   do not substitute `tccutil` output or a disabled UI.
2. **P1-1:** exact startup-attempt token for permanent Ensure ACK loss.
3. **P1-2:** authoritative model-created session discovery in desktop UI.
4. **P1-3:** virtual/mocked multi-display topology and mixed-DPI coverage.
5. **P1-4:** final functional regression/evidence audit.
6. Deferred: physical second display and formal signing/distribution.

## Phase closure — P0-2 autonomous WorkBuddy model loop — 2026-09-29

P0-2 is now **closed and passed** on the rebuilt tagged Wails app using the
normal authenticated session-control API and `jiuan-responses-gpt-5.6sol` with
model `gpt-6-sol`. No parent GUI action was used during the successful model
run.

Evidence:

- Successful run: `run_id=87`, session ref
  `tenant:computer-live-5142b8da514744a784404c22f96756e9`.
- Sanitized summary:
  `desktop-v2/build/validation/20260929/model-autonomy-gpt6-retry/autonomy-summary.json`.
- Full private transcript copied to the ignored validation directory as
  `transcript.jsonl`; raw model evidence remains outside Git.
- The ComputerUse trace contains fresh screenshots/observations with active
  window `WorkBuddy`, bundle `com.workbuddy.workbuddy`, PID `23941`, including
  observation IDs 23, 27 and 31, followed by an explicit Stop acknowledgement.
- The model reported and the trace supports: Spotlight launch, WorkBuddy
  frontmost confirmation, 助理 navigation, 新建任务 navigation to an empty
  composer, no assistant run, no task content, no submit/send/login/settings or
  points changes.
- The first attempt in the same phase was correctly recorded as **failed**:
  it clicked Claude instead of WorkBuddy and stopped without continuing. The
  retry prompt tightened launch disambiguation and then passed; this is kept as
  negative evidence, not hidden.

Priority reorder after this phase:

1. **P0-3:** real host-bound Screen Recording/Accessibility revoke and regrant,
   including failed observe/execute, recovery, fresh capabilities/observation,
   no replay, helper/host restart boundaries.
2. **P1-1:** exact startup-attempt token for permanent Ensure ACK loss.
3. **P1-2:** authoritative model-created session discovery in desktop UI.
4. **P1-3:** virtual/mocked multi-display topology and mixed-DPI coverage.
5. **P1-4:** final functional regression/evidence audit.
6. Deferred: physical second display and formal signing/distribution.

## Phase closure — P0-1 non-drag host-owned input — 2026-09-28

P0-1 is now **closed for the single-screen functional scope**. In addition to
the real trusted-input matrix recorded above, a deterministic production
host/helper fault-injection run was completed with the tagged Wails app:

- The opt-in validation hold was propagated through the host's deliberate
  helper environment allowlist; normal environments remain unchanged.
- The helper was verified by exact host PID/path and exact helper PID/parent
  path. The execute future was still pending after the batch request had been
  sent and the helper environment contained the hold hook.
- The helper was SIGKILLed while a real Unicode batch was in flight. The host
  had already committed the balanced key sequence. The receipt was
  `outcome=unknown`, `verification=unknown`; the next observe was rejected and
  the session paused, with no action replay.
- The real fixture recorded the trusted `Z` keydown/beforeinput/input/keyup and
  final state `验收ABZ`, proving the host-owned batch completed physically even
  though the helper died before its ACK reached the caller. Evidence:
  `desktop-v2/build/validation/20260928/p0-1-real-acceptance/evidence-crash-type/crash-type-result.json`
  and `crash-type-fixture-after.json`.

P0-1 delivery commits:

- `9a9dabc` host-owned bounded batches;
- `b6e1e83` correct modifier-release mapping, caught by the real fixture;
- `6ec8d17` deterministic validation hold hook;
- `3450d7d` explicit helper environment propagation for that local validation.

The test-only hold is an acceptance instrument, not a production default, and
formal signing/distribution remains deferred.

Priority reorder after P0-1 closure:

1. **P0-2:** fresh authenticated `gpt-6-sol` autonomous WorkBuddy loop using
   the rebuilt helper; require automatic startup, WorkBuddy launch, visible
   navigation, New Task click, fresh after-observation and Stop, with no parent
   GUI input during the model run. Previous Tasks 84/85 remain non-passing.
2. **P0-3:** real host-bound Screen Recording/Accessibility revoke and regrant.
3. **P1-1:** exact startup-attempt token for permanent Ensure ACK loss.
4. **P1-2:** authoritative model-created session discovery in desktop UI.
5. **P1-3:** virtual/mocked multi-display topology and mixed-DPI coverage.
6. **P1-4:** final functional regression/evidence audit.
7. Deferred: physical second display and formal signing/distribution.

## Phase update — P0-1 bounded non-drag input ownership — 2026-09-28

Implementation and deterministic fault-injection tests are complete for the
host-owned bounded input path; this is **not yet a claim of full real-desktop
P0-1 acceptance**. The host now precomputes one balanced batch for each click
pair, each Unicode scalar, and each key/hotkey. The inherited helper channel
carries only an unguessable token and sequence; it carries no coordinates,
text, key code, or arbitrary event list. The host allocates and posts the full
balanced CGEvent sequence synchronously, so helper SIGKILL cannot leave a
partially posted click or key chord. Drag keeps its separate lease protocol.

Evidence completed:

- Go batch-plan tests cover observed negative/global geometry, left/right click,
  double-click pair count, Unicode scalar/supplementary scalar, key, hotkey,
  duplicate modifiers, bounds and invalid UTF-8.
- Go broker/backend tests cover normal click/double-click/right-click/type/key/
  hotkey batches and deterministic helper SIGKILL after the first batch. The
  normal path records the exact expected number of balanced host commits; the
  crash path returns `unknown` and never replays the already committed batch.
- `go test -race ./internal/computerbackend/macos` passed.
- Native helper build passed. `bash native/macos/tests/platform.sh` passed
  469 assertions, including batch framing, sequence, no event-data leakage,
  inactive rejection, lost-ACK uncertainty and no reuse. `bash
  native/macos/tests/run.sh` passed 240 fake-platform safety assertions.
- No real desktop input was posted by these tests. They prove host ownership,
  bounded protocol behavior and fault injection, not real fixture event logs or
  screenshots after the new path is bundled into the desktop app.

Priority reorder after this phase:

1. **P0-1 acceptance follow-up:** build the current desktop app and perform a
   real isolated fixture run for click, double-click, right-click, Unicode,
   key and hotkey, collecting trusted event logs, before/after screenshots,
   receipts, and a helper-crash run. Do not claim P0-1 complete until this
   evidence exists.
2. **P0-2:** fresh authenticated `gpt-6-sol` WorkBuddy autonomous loop after
   the new helper is bundled; require actual observe/click/new-task/after-image/
   Stop trace and no parent GUI intervention.
3. **P0-3:** real host-bound permission revoke/regrant lifecycle.
4. **P1-1:** exact startup-attempt token for permanent Ensure ACK loss.
5. **P1-2:** authoritative model-created session discovery in desktop UI.
6. **P1-3:** virtual/mocked multi-display topology and mixed-DPI coverage.
7. **P1-4:** final functional regression/evidence audit.
8. Deferred: physical second display and formal signing/distribution.

### Resume/change ledger

- Baseline: `d1ff501`, clean working tree, `git pull --ff-only origin main`
  confirmed up to date. No pre-existing dirty files.
- This documentation slice owns only `AGENTS.md` and this ledger. It persists
  the user's phase-priority rule; it does not implement the pending input broker.
- Non-drag batch delegation returned a design only, with no source changes.
  Protocol remains subject to review: do not expand the existing public hotkey
  limits or batch an entire text action merely to fit a proposed wire schema.
- Baseline tests passed: `go test ./internal/computerbackend/macos
  ./internal/computerbridge ./internal/computeruse`.
- No new native UI acceptance or screenshots in this documentation slice.
  Delivery is recorded by the Git commit containing this section and its push.


## Scope and delivery order

Functional work first; Developer ID, notarization, release distribution and
signed clean-install/upgrade acceptance are explicitly deferred until last.
No claim of complete Codex parity is made from a single successful task.

## Architecture

- Keep model tools → trusted conversation service → controller → macOS backend
  → native helper. No unauthenticated input endpoint or implicit model approval.
- Targets carry display/window identity, current geometry and observation expiry.
  All input coordinates refer to the exact observed image, not a UI preview.
- Multi-step input owns a bounded press/release lifetime. Cancellation cleanup
  must finish before Stop acknowledges; helper death requires an independent
  release guard (a defer alone cannot handle SIGKILL).
- Automatic session startup must consume user-granted scope; model output is
  never authority to approve itself. First OS consent remains user-controlled.
- Prefer the existing macOS/Go interfaces and Apple native APIs. Keep target
  discovery, coordinate conversion, input lifetime and session policy separate.

## Existing work and correction gate

Initial worktree was clean at 8825bc3. All subsequent source edits in this run
are task-owned. No pre-existing user source edits have been observed.

| Slice | State | Verification / delivery |
|---|---|---|
| Drag contract and initial helper path | pushed b141f86; NOT native accepted | Go suites and 161 fake Swift assertions; safety review found cleanup and capability gaps requiring correction before live input |
| Display inventory and selection foundation | pushed b369cb5; NOT multi-monitor accepted | Go focused tests and 163 fake Swift assertions; physical two-monitor matrix outstanding |
| Input safety correction + regression matrix | in progress | preallocate releases, explicit button binding, strict protocol fields, synchronous Stop cleanup, independent crash release |
| Window inventory/identity and target binding | pushed f6aec45; acceptance targeting plumbing pushed in this slice | native Window Server inventory, owner PID/bundle/title/frame, window activation, window capture geometry, active-window guard, Wails acceptance observe target plumbing, tool/DTO binding; real multi-window Wails acceptance outstanding |
| Window capture/focus and stale geometry | pending | same-app multiple windows, overlap, close/move/minimize and stale IDs |
| Multi-display UX and topology invalidation | foundation pushed b369cb5 | inventory and selected-display capture are implemented/tested; negative origins, mixed DPI, unplug, topology invalidation and real two-monitor acceptance remain |
| Authorized automatic session coordinator | pending | no UI manual start within scope; cross-conversation isolation, revoke and Stop precedence |
| Permission lifecycle recovery | pending | active-session OS revoke, helper/host restart requirement, recovery without replay |
| Native E2E completion | pending | Wails-host-owned actions, trusted fixture events and real before/after screenshots |
| Signing and distribution | deferred LAST | no tag/release or signing changes in functional work |

## Acceptance rule

A disabled Start button only proves UI gating, not that a dispatched request is
rejected. A standalone helper probe is not equivalent to a Wails-host TCC path.
Prior permission report overstates that full gate: rerun actual host observe and
execute rejection plus recovery. Similarly, executed receipts with
verification=not_checked are not proof of the intended effect; require fixture
state/events and screenshot inspection. Preserve requested evidence under
ignored desktop-v2/build/validation/YYYYMMDD; do not commit screenshots, logs,
credentials or user databases. Commit/push each tested coherent source slice.

## Current slice evidence boundary

The native tests now exercise a fake window target and selected secondary display
(165 assertions). This proves protocol/state transitions and coordinate binding,
not real Window Server enumeration, real target activation, or a physical
multi-monitor setup. The next gate is a Wails-host build and a real fixture with
WorkBuddy plus a second app, including screenshots and active-window metadata.

## Real Wails-host window-target acceptance — 2026-09-27

On the current ad-hoc build with the real Wails host acceptance socket:

- Capabilities enumerated 14 on-screen Window Server targets and one display.
- A visible `go-e2e` target was observed by `window_id`, producing a window-only
  screenshot (`2674x1588`) and an `executed` native move receipt whose active
  window remained the requested ID.
- WorkBuddy was then launched cold, its visible main window (`bundle_id`
  `com.workbuddy.workbuddy`, window ID `8792`) was selected from the host's
  capability inventory, and a window-only screenshot (`2704x1688`) was taken.
- A real window-targeted move and a harmless click in the empty WorkBuddy
  composer both returned `outcome=executed`; every receipt retained the same
  WorkBuddy active-window ID and fresh after-image.
- Evidence is retained under ignored
  `desktop-v2/build/validation/20260927/window-target/`, including
  `capabilities-after-workbuddy.json`, `workbuddy-window-result.json`,
  `workbuddy-window-click-result.json`, and their before/after PNGs.

This is a real single-display window-target pass, not proof of multi-monitor,
window-close/move invalidation, multiple same-app window disambiguation, or
model-driven automatic target selection. Those remain open acceptance gates.

## Real drag acceptance — 2026-09-27

- The isolated fixture now includes `drag-source` and `drag-drop-target`; the
  server validates both target IDs and the page only marks `dragCompleted` after
  trusted native `mousedown`/intermediate `mousemove`/`mouseup` events.
- Through the Wails host's real Computer Use acceptance socket, the current
  build ran all 12 fixture cases. The new drag case passed with an executed
  receipt, 29 trusted event records, source endpoint `(266,400)`, drop endpoint
  `(539,400)`, and `dragCompleted=true`.
- Evidence is retained under ignored
  `desktop-v2/build/validation/20260927/drag-fixture/`, especially
  `fixture-drag-before.png`, `fixture-drag-after.png`,
  `fixture-drag-receipt.json`, `fixture-results.json`, and `fixture-run.log`.
- This closes basic real drag execution for the single-display fixture. Pause,
  Stop, focus perturbation, helper crash, window-target drag, and multi-display
  drag remain separate gates.

## Display topology stale-observation guard — 2026-09-27

The native snapshot now records the sorted display-ID topology. Every input
revalidates that topology in addition to the selected display geometry, focus,
and target window. A display add/remove therefore rejects a stale action before
any input post. The fake native matrix now reports 167 assertions; physical
monitor hot-plug and mixed-DPI acceptance remain outstanding.

## Lazy managed-session orchestration — 2026-09-27

The ComputerUse tool may now omit `session_id` for its first `observe` call.
The trusted desktop service implements an optional coordinator that lazily
creates/approves a session for the current managed conversation, using the
already-established ComputerUse permission gate and the Wails host lifetime.
Local preview remains explicit and cannot enter this path because local owners
are rejected. Cross-conversation ownership checks remain enforced.

This removes the model-run dependency on a user clicking “授权本次会话” after
OS permissions are already granted. It does not automate first-time macOS TCC
consent, does not let model output mint permission, and does not yet provide a
runtime task-finalizer that automatically stops every session if the model
fails to call Stop.

## Drag interruption release guard — 2026-09-27

The native fake matrix now covers focus loss after drag mouse-down: the result
is `unknown`/`focus_changed` and the emergency mouse-up is posted before the
helper reports the failure. The matrix is now 170 assertions. Real pause/stop,
permission revoke, and helper-crash drag interruption remain to be run.

## Model-route gate for auto orchestration — 2026-09-27

A real latest-source Wails task was run without starting the Computer Use panel
manually. The configured `gpt-5.6-sol` route failed before any ComputerUse call
with provider `404 model is not found`. A retry with the UI-listed `gpt-5.5`
route failed with the same 404. Therefore the new lazy coordinator is covered
by focused unit tests but has not yet received a live model call in this
environment; no automatic-session pass is claimed until an available
vision-capable route is configured.

## Active-session permission lifecycle UI — 2026-09-27

Readiness refreshes are no longer suppressed merely because a session is active.
Focus/visibility/recheck refreshes can now surface a TCC revoke/restore while
preserving the session's safety controls. The permission guide is also rendered
when an active session reports `permission_state=required`, so the user gets a
recovery path instead of a stale “ready” surface. Native action gates remain
fail-closed; this slice does not silently replay an action after recovery.

Focused frontend verification: typecheck passed; 52 Computer Use hook/workspace
tests passed.

## Latest functional slices — 2026-09-28

### Lazy cold-start bridge wiring

The desktop bridge now exposes a host-owned `ensure` operation in addition to
lookup. The runtime no longer requires an existing approved session before it
registers Computer Use: the first model `observe` may omit `session_id`, the
trusted host coordinator creates/binds the session, and the runtime tracks the
created ID for bounded cleanup. Existing non-coordinating test adapters retain
the explicit lookup compatibility path.

Focused Go tests cover the wire command, owner authorization, lazy setup without
an existing-session lookup, and cleanup binding. The production host bridge is
still subject to provider/image-route gating; this change does not bypass that
operator assertion.

### Dynamic targets and stale-window diagnostics

Active sessions refresh dynamic display/window inventory on capabilities/start
refresh instead of retaining the window list from session creation. Native
rejections preserve allowlisted safety codes so stale display/window, focus, and
permission failures can be distinguished without exposing helper text. Stale
window IDs remain fail-closed and are never remapped by title alone.

### Helper crash recovery

When an active helper transport dies, the host retries capabilities once with a
fresh bundled helper. The session is paused and its previous observation is
invalidated before the replacement controller is exposed; recovery requires
explicit Resume plus a fresh Observe, and no action is replayed.

Real latest-source Wails acceptance evidence:

```text
desktop-v2/build/validation/20260928/real-latest/crash-before.png
desktop-v2/build/validation/20260928/real-latest/helper-before-kill.txt
desktop-v2/build/validation/20260928/real-latest/crash-capabilities-after.json
desktop-v2/build/validation/20260928/real-latest/crash-snapshot-after.json
desktop-v2/build/validation/20260928/real-latest/crash-recovered-observe.png
```

The helper PID changed after SIGKILL; the session reported `paused`, then
Resume + a new observation succeeded. This is helper recovery evidence, not a
claim of SIGKILL-time system mouse-up.

### Permission lifecycle boundary

A live `tccutil reset Accessibility/ScreenCapture` request was issued against
the current host bundle. macOS kept the running process's readiness approved;
this is recorded as a deferred live-revocation result rather than a pass. A
restart-bound TCC revoke/restore remains a separate acceptance gate and no
permission is claimed revoked when the native probe still reported approved.

### Effective image-route gate correction — 2026-09-28

The runtime image gate now checks the explicitly asserted effective primary
route instead of requiring every unrelated configured fallback to advertise
image input. This keeps the security boundary (no ComputerUse without an exact
operator assertion) while avoiding a text-only fallback disabling a selected
vision route before the query starts.

The local settings used for the latest provider probe now contain explicit
routes for `jiuan-responses-gpt-5.6sol` with `gpt-5.6-sol`/`gpt-6-sol` and the
primary aliases. A direct Responses request with a 1x1 PNG returned HTTP 200
from the configured Jiuan route. This proves provider image acceptance, not a
completed model-driven desktop run.

Latest model-driven reruns remain uncounted as ComputerUse passes: one deny-mode
run correctly failed closed because the tool was not in the allow list; the
allow-mode rerun was stopped before producing a ComputerUse tool trace. The
native host/bridge acceptance and helper recovery evidence remain independent
passes.

## Scope update and current safety slice — 2026-09-28

The user explicitly deferred physical second-monitor acceptance as well as
formal signing/distribution. Neither blocks this phase; neither is claimed
verified. Single-display window selection, drag cancellation/release, permission
recovery, and automatic session lifecycle remain required.

Worktree at `31840a0` was clean; no pre-existing user edits. Current slice:
- Native target resolution must be read-only during Execute and evidence capture.
  Only an explicit Observe may activate a requested target. Otherwise capture
  can steal focus back after an action/user focus change and conceal the change.
- Bind identity to Window Server ID, process and bundle, not a mutable title.
  Geometry/visibility must remain checked independently before input.
- Regression matrix: identity/geometry replacement, title-only navigation,
  external focus between checks/capture; count activation calls in fakes.
- Verification: native tests, Go controller/backend suites, rebuild Wails app,
  real window-target before/after screenshot and focus-rejection evidence.
- Delivery: pending tests, commit and push; no release tag or distribution work.

Native target-read-only slice results:
- Red/green regression: the new title-navigation test failed before the change;
  203 fake-platform native assertions pass after it. Execute calls activation
  zero times, including focus loss during geometry resolution and after input.
- Go backend/domain/desktop focused suites pass; tagged Wails `.app` built.
- Real host readiness reports approved/ready. Target Observe was attempted while
  the desktop switched Spaces/focus; capture failed and session paused. No input
  was dispatched. This is NOT a real window-target success; that acceptance is
  still pending a stable test window. The test session was explicitly stopped.

### Remaining functional gates (explicit user exclusions applied)

| Requirement | Current evidence / next required gate |
|---|---|
| Unified target/geometry binding | Native snapshot identity and Go metadata validation in progress; reject mismatched target/capture geometry, no cached bounds blending |
| Exact same-app window selection | Public AX exact-window activation and identical-title A/B fixture now pass; see exact-window slice below. Ambiguous matches intentionally fail closed |
| Single-screen drag | Basic trusted-event fixture passed earlier; real Pause/Stop/focus-loss while holding a button and independent SIGKILL release remain open |
| Automatic orchestration | Real run 77 (`live-workbuddy-dock-activation`) recorded WorkBuddy bundle identity plus three clicks and Stop. Final screenshot was inspected; a fresh independently captured intermediate Assistant screenshot is still needed for the strongest navigation evidence |
| Permission lifecycle | Readiness regression now pauses the domain session and restore does not auto-resume. Running-process TCC revocation evidence remains deferred/insufficient, not a pass |
| Target state changes | Native fakes cover stale identity/geometry and focus perturbation; current-source stable real target, close/minimize/move and same-app windows remain acceptance work |
| Physical second monitor | Deferred by user; not needed to finish this phase, not verified |
| Formal signing/distribution | Deferred by user; no release/tag/notarization work in this phase |

### Capture-bound metadata and detached snapshots

The helper now emits the geometry of this capture. The Go backend rejects
missing/malformed geometry, dimensions/scale/display mismatches, mismatched
requested/returned window IDs, missing target frames, and any target Frame that
differs from the capture Bounds. Whole-display capture clears the previous
TargetWindow instead of inheriting it. All nested frame pointers in capabilities,
observations and receipts are detached copies.

Verification: 207 fake-platform native assertions; seven focused Go packages
passed including CLI; five backend/domain/bridge/tool/desktop packages passed
with race detection. The expiry harness also passed 23 offline tests after
adapting its wait budget to the existing 120-second native TTL (actual expiry
still comes from the returned metadata; runtime TTL was not changed).

Latest tagged Wails host capture succeeded after a clean new process launch:
`target-safety/metadata-target-before.png` is a real window-only `2674x1588` PNG;
`metadata-result.json` binds window `10151` to global Bounds `(7,35,1337,794)`
and scale `2`. Evidence is local under
`desktop-v2/build/validation/20260928/target-safety/` (not committed). Screenshot
was visually inspected. The subsequent input scenario found no visible test
window and stopped without dispatch; it is not counted as an input pass. This
supersedes the earlier failed capture attempt, not the still-open input matrix.

Delivery: target validation committed/pushed as `4ea2b42`, expiry harness as
`24a0b69`; capture-bound metadata is the coherent source slice accompanying this
entry. Physical second-monitor and formal release gates remain explicitly out
of this phase; all other open functional gates above remain in scope.

### Exact-window activation slice

Baseline `bb7bdbf` clean; pulled before editing. Architecture: public macOS AX
window inventory + unique geometry/title match within the target PID, explicit
AXRaise, then Window Server ID verification. Ambiguous/missing matches fail
closed; title alone is never identity, and no private AX-to-CG ID API is used.
Activation stays exclusively in Observe; Execute remains read-only. Bound AX
messaging and activation settling; Pause/Stop must interrupt the settle loop.
Write scope: native selector/adapter, Engine activation gate, build/test source
lists; independent agent owns only the two-window AppKit fixture. Verification:
fake selectors and lifecycle cases, tagged Wails build, two identical-title
native windows with click counters and target screenshots. No physical second
monitor or formal release work. Commit/push pending focused/native verification.

Exact-window slice verified on 2026-09-28 (single physical display):
- Public AX adapter matches the selected PID plus geometry/title, rejects
  ambiguity/unreadable candidates, raises the specific AX window, and confirms
  the Window Server ID. Each AX object has a 100ms messaging timeout and checks
  cancellation between reads. Observe settling is bounded and interruptible;
  input/evidence validation never reactivates a window.
- 220 native fake assertions and 436 non-posting platform event assertions pass.
- Tagged Wails build ran the new native AppKit fixture with two identical-title
  windows: A (`10345`) -> B (`10346`) -> A. Counts were `(1,0)`, `(1,1)`, `(2,1)`.
  Each positive click was dispatched by the actual Wails Controller/helper.
- Switching observation to B rejects A's superseded observation. Independently,
  a tester-injected AXRaise to B after observing A caused the old A click to be
  rejected by the native backend; B remained frontmost and counts stayed `(2,1)`.
  The external AXRaise is fault injection, not evidence of model autonomy.
- Real A/B screenshot pixels and counters were inspected. Evidence retained in
  ignored `desktop-v2/build/validation/20260928/window-selection/`: `results.json`,
  `03-A-verified.png`, `02-B-verified.png`, `external-focus-result.json`, and
  `external-focus-B.png`. Stop confirmed in both scenarios.
- Reusable fixture builder: `scripts/build-computer-window-fixture.sh`; runner:
  `scripts/computer-acceptance-windows.py --fixture <absolute state.json>
  --socket <tagged host socket> --output <evidence directory>`.
  Open the fixture `.app` with `--args --output <absolute state.json>` before
  running. The runner scopes every target to the fixture PID and bundle ID.

This closes the previously missing exact-window activation + same-title native
selection gate, not arbitrary-app AX compatibility or all Computer Use gates.
Drag interruption/crash release, move/minimize/close target changes, and full
permission lifecycle remain work. No physical second-screen or release claim.

Delivery/readback: feature `102aeef` pushed to `origin/main`. Rebuilt that exact
source and cold-launched the tagged host; repeated A/B/A acceptance passed with
counts `(3,1) -> (3,2) -> (4,2)`, stale-target rejection and confirmed Stop.
Evidence: `window-selection/head-rerun/results.json` and corresponding verified
PNGs; final A image visually inspected. Six related Go packages pass. The
purpose-built fixture process was stopped and both generated fixture app copies
were removed; requested screenshots/JSON remain. Normal untagged desktop build
is restored after testing; no release artifacts/tags were published.

### Held-button interruption slice (baseline 87600b4, clean, pulled)

Review found Stop acknowledged before the helper's executor drained; the Go
controller then canceled the active RPC, potentially terminating the helper
before drag cleanup. Also emergency release used AppKit bottom-left cursor
coordinates as CG top-left coordinates and allocated its event during failure.
Design: preallocate/reuse the release event in CG coordinates before down;
keep native reader/control responsive but enqueue control acknowledgements after
executor cleanup; retain explicit paused state for Resume+fresh Observe, not
input replay. Close requests bounded Stop before abort. Verify normal drag and
Pause/Stop/focus interruption in isolated AppKit event fixture, counters and
screenshots. SIGKILL needs an independent release authority and stays an explicit
open gate; successful cooperative cleanup is not crash safety.

Held-button slice acceptance on 2026-09-28:
- Native fake safety matrix: 231 assertions; non-posting platform event matrix:
  455 assertions. Focused Go tests and race tests passed for controller, macOS
  backend/native transport, bridge, tool, and Wails host.
- Actual tagged Wails Controller/helper passed eight native fixture cases:
  normal left/right drag, Pause left/right, Stop left/right, and independent
  focus perturbation left/right. Each recorded exactly one down/up pair, up at
  the last posted CG point, fixture pressed=false, and global mouse mask=0.
  Normal cases completed the drop; interrupted receipts remained unknown.
  Pause resumed only with a fresh observation; all cases confirmed Stop.
- Real execution exposed two extra failures before the passing rerun: Go Abort
  prevented Pause recovery, and focus-error Abort prevented Stop confirmation.
  Locally requested, native-acknowledged interruption now keeps the helper for
  explicit Resume; focus failure quarantines it to Stop/Close only. Neither
  exception permits replay or changes an unknown receipt into success.
- Final evidence: ignored `desktop-v2/build/validation/20260928/drag-interruption/final/`
  (`results.json`, normal-left/right, pause, stop, focus before/after PNGs).
  Normal-right and Pause native screenshots were visually inspected: completed
  drop and released-interrupted states respectively. This is scripted native
  acceptance, not model-autonomous drag acceptance. No SIGKILL safety claim.

### Window lifecycle real acceptance slice (baseline 2c1895a)

Previous drag-release slice committed/pushed. Current task owns only the fixture,
a new lifecycle runner, and this ledger. Reuse the existing Window Server
identity/geometry guards and Wails acceptance driver; add an opt-in fixture-only
mutation channel for move/resize/minimize/restore/close, never cross-app input.
Verify stale screenshot clicks reject with unchanged native counters; missing
windows cannot be observed; recover with a fresh target image and real click.
Keep screenshot and event evidence, then commit/push after actual acceptance.

Lifecycle acceptance passed on 2026-09-28 with the actual tagged Wails host:
move, resize, minimize/restore, and close each rejected the old-image click with
unchanged native click counters. Minimized/closed targets could not be observed;
observe did not silently restore them. Recovery used explicit Resume after the
failed capture and fresh observations, then native A counts 1/2/3 and B count 1.
All four Stop acknowledgements passed. Swift6 warnings-as-errors fixture build
and Python syntax checks passed. Evidence: ignored
`desktop-v2/build/validation/20260928/window-lifecycle/final/results.json` plus
before/recovery PNGs. Visually inspected restored A (Clicks: 3) and surviving B
(Clicks: 1). Initial test runner omitted Resume after failed capture; corrected
the runner rather than weakening the existing paused-session safety boundary.
This proves single-display window lifecycle guards, not general AX compatibility.

### Independent helper-crash release (baseline e7f432b)

Architecture review rejected a helper-owned defer or best-effort armed message:
SIGKILL can destroy it, and cross-process armed/post messages cannot be atomic.
Use a narrow host-owned mouse broker for drag down/drag/up, with a preallocated
release event and one active action lease. The helper sends bounded requests on
inherited private pipes; no public socket, arbitrary-script endpoint, or local
down fallback. The live host serializes posting and consumes the release once
on normal up, control revocation, action completion, or helper death. Host TCC
is checked independently before drag; helper ACK loss remains OutcomeUnknown.
Scope is helper death with a live, still-authorized host, not simultaneous host
death or permission revocation during a press. Native control gates and target
checks stay in place; no automatic replay. Go broker/transport worker and local
Swift/client/acceptance work have disjoint write scopes. Verify deterministic
allocation/ACK/revoke/death races, existing eight real drag cases, then left/right
SIGKILL fixture cases with real native events, mask=0, screenshot and fresh-session
recovery. Do not mark accepted until that real Wails path passes.

Independent release acceptance on 2026-09-28:
- Real rebuilt/cold-launched tagged Wails host passed all ten cases: the previous
  normal/Pause/Stop/focus left/right matrix plus left/right helper SIGKILL.
  Crash was injected only after fixture down and nonzero system mask, after
  checking exact helper executable and host parent PID twice. Host stayed alive.
- Crash left/right each recorded exactly down, one dragged, one up. Up matched
  the last actual CG point `(182.18182373046875,466.5)`; global masks `1/2 -> 0`,
  view pressed=false, and no duplicate input. Receipts stayed unknown; old action
  rejected. New sessions with fresh screenshot executed a move and confirmed Stop.
  Dead-helper Stop correctly returned an explicit transport error, not a fake ACK.
- Evidence: ignored `desktop-v2/build/validation/20260928/crash-release/run-1/`
  `results.json`, `crash-left/right-held.png`, `crash-left/right-after.png`, and
  recovery evidence. Right-button Holding and Released/interrupted screenshots
  visually inspected. This is scripted native acceptance, not a model trace.
- Native fake matrix: 234 assertions; platform/client/real-pipe (non-posting)
  matrix: 465 assertions. Go worker passed broker/transport normal/race tests,
  CGO-disabled builds, vet, fake-child SIGKILL, lost ACKs, duplicate releases,
  permission denial, and concurrent terminal paths. Whole-related-package race
  rerun is the final delivery gate.
- A broad race run during desktop compilation exposed an old test assumption:
  its 100ms deadline could expire during 900KB JSON encoding, before dispatch,
  when the production contract correctly keeps the unused helper alive. The
  blocked-write test now waits for dispatch before cancellation, still requires
  MayHaveRun and reader/reaping completion; 20 race repetitions pass.
- Scope: independent drag release with live, authorized host. Simultaneous host
  death, permissions revoked during a held input, and non-drag key/modifier
  crash release are NOT covered by this pass. No second-display/release claim.

Final related-package `go test -race ... -count=1` rerun passed all seven packages
(controller, macOS backend, native transport, bridge, ComputerUse tool, Wails,
CLI). Existing Darwin LC_DYSYMTAB linker warnings remain non-fatal.

### Delivery checkpoint — 2026-09-28

Pushed to origin/main:
- `2c1895a`: cooperative held-button cleanup, Resume after Pause, control ACK barrier.
- `e7f432b`: native move/resize/minimize/restore/close stale-target acceptance.
- `5d53053`: host-owned drag broker and actual helper SIGKILL release acceptance.

Cleanup: stopped the acceptance session, verified global button mask zero,
stopped the fixture by verified PID/path, removed generated fixture bundles and
standalone test executables. Kept requested PNG/JSON evidence. Restored and
launched the normal untagged desktop `.app`; private acceptance sockets and
session files are removed. No formal release/signing work was performed.

Remaining functional acceptance priorities (NOT claimed complete):
1. A latest-source production model trace that autonomously starts ComputerUse,
   selects/opens the intended app/window, performs a bounded task, inspects fresh
   screenshots and stops, without a manually pre-started ComputerUse session.
   Scripted fixture passes do not replace this gate.
2. Host-bound permission lifecycle: demonstrably revoked capture/input, actual
   Observe/Execute failure, regrant/restart as needed, explicit safe recovery
   without replay. The earlier live reset that still reported ready is not a pass.
3. Audit non-drag key/modifier held-input lifetime during helper failure. The new
   independent owner covers drag only; do not generalize its guarantee to every
   input or simultaneous host death.

Physical second-display real-hardware acceptance and formal signed distribution
remain explicitly deferred by the user and are not blockers for this phase.

### Production model loop investigation (baseline 4343f52, clean/pulled)

Created a separate managed desktop session through the UI, selected the existing
jiuan gpt-5.6-sol route and retained allow mode. No ComputerUse session was
manually pre-started. The real model invoked observe with no session_id, received
observe_failed, then invoked Stop successfully. This establishes model/tool
connectivity and lazy start, not screenshot/input acceptance. Diagnose the
host-vs-bridge boundary before fixing; temporary safe error diagnostics are
limited to host Observe and the trusted runtime bridge wrapper. No screenshots,
credentials or provider responses will be committed. WorkBuddy was not operated.

The observation-only diagnostic rerun succeeded and the model called Stop;
the initial observe failure was not reproduced, so no speculative capture fix
or temporary logging is retained. A subsequent full model run (task 80) used
only ComputerUse: observe, Spotlight hotkey, type application name, Enter, wait,
Stop. WorkBuddy cold-launched at 18:16:05 while the preceding image still showed
the launch surface. The passive wait was rejected after focus changed.

Root cause confirmed with a failing native fake test: Engine applied input
preflight to an empty operation plan (wait), incorrectly requiring old focus
even though it posts no event. Scope of fix: skip input preflight only for empty
plans; retain observation identity/expiry/consumption, generation/Stop, geometry,
and stable capture checks. A companion test must still reject a focus change
*during* the screenshot; input focus guards are untouched. Intended source
changes now only Engine.swift + native tests + this ledger. Fresh desktop/model
reproduction and native tests remain required before marking this slice passed.

Passive wait slice verified on 2026-09-28:
- Regression failed before the one-line input-preflight condition and passes
  after it. Native matrix 240 assertions; platform/client matrix 465 assertions;
  all seven related Go packages passed. Desktop rebuilt/cold-launched.
- Through the actual tagged Wails Controller, captured a display, externally
  changed application focus to the isolated native fixture, then executed wait
  against the preceding observation. Outcome executed; after-image identified
  the fixture PID/window; native fixture counters and drag events stayed zero;
  Stop confirmed. Before focus belonged to the parent test controller's software
  cursor surface, not the fixture. No claim that this was model-driven input.
- Evidence: ignored `desktop-v2/build/validation/20260928/passive-wait/`
  `result.json`, `before-controlled-switch.png`, and
  `after-controlled-switch-after.png`; the final native image was inspected.

Model gate remains open: task 81 again failed its first observe and stopped.
Task 82's read-only model observe and Stop succeeded; its final response needed
provider stream retries and eventually completed (it was not cancelled).
Successful screenshots were near 5MiB, but repeated full-display probes did not
reproduce a budget error, including a maximized window. Do not claim a size or
permission root cause. Temporary host/CLI/native size diagnostics are removed.
Parent CUA's Software Cursor appeared as a Window Server active surface during
manual inspection; avoid parent UI intervention during the next autonomous run,
prefer the desktop's normal trusted session-control API to start it, and inspect
only task/transcript state until it finishes. No app-specific focus bypass.

### Vision-safe fallback selection (baseline aed0641, clean/pushed)

Production normal-build task 83 was submitted via the existing authenticated
session-control API, with no parent GUI calls during its run. First observe and
Spotlight hotkey succeeded. Primary gpt-5.6-sol then returned HTTP 200 SSE that
failed JSON decoding before any event; after its retry was exhausted the client
attempted glm-5.1 without an image-input assertion. The task eventually stopped
itself and reported incomplete, with no WorkBuddy click. No restart or duplicate
submission was issued. The same query still owned ComputerUse authority while
client-internal fallback changed routes: the existing primary-only registry gate
is insufficient to constrain that live fallback chain.

Design: keep primary registration gate; before constructing the client for an
enabled ComputerUse query, copy/filter fallbacks to exactly asserted provider +
effective model. Ordinary queries retain all existing fallbacks. Canonical
fallback naming is shared with the client; unnamed original positions must be
frozen in retained copies to avoid ordinal alias changes. Do not persist settings
or weaken image/owner/input gates. Independent worker owns only the new CLI tests;
main owns routing helper/wiring. Verify real local HTTP primary failure after an
observe screenshot, zero calls to unasserted fallback, approved fallback image
receipt, model overrides, immutable config, and ordinary-query compatibility.

Vision fallback verification:
- 17 pure route-table cases and five production newQuerySession/local-HTTP
  scenarios pass. Primary first invokes Observe; its next request demonstrably
  contains the returned PNG. HTTP overloaded errors and SSE errors exercise
  actual failover (ordinary non-retryable 400 would be a false-positive test).
- With the client-construction filter deliberately removed, the undeclared-route
  regression fails: one forbidden fallback request receives the image and the
  query incorrectly succeeds. Restoring the filter passes, including -race.
  Declared fallback positive controls still receive the actual image and finish;
  cleanup Stop occurs exactly once on both success and failure.
- Full config, anthropic transport, and CLI packages pass. The shared canonical
  naming helper preserves general client behavior; ordinary queries are unfiltered.
- A minimal gpt-6-sol probe through the actual production Responses client, with
  fallbacks disabled and a generated 1x1 image, succeeded in 2.81s and returned OK.
  An earlier bare urllib probe returned 403; that is not evidence that the model
  is unavailable, given the successful production-client counterexample. This
  proves connectivity/image acceptance only, not desktop task completion.

### Latest model/readback checkpoint

Normal source `8a7e7c2` cold-launched. Fresh managed session 60 / task 84 used
`gpt-6-sol` at low effort, launched through the normal authenticated API. No
manual ComputerUse start or parent GUI calls occurred during the run. Native
observe/hotkey/type/click succeeded and WorkBuddy cold-launched at 21:45:06 local.
Independent native screenshot `model-autonomy/gpt6-workbuddy-opened.png` (existing
window 10676) was inspected. This proves launch only: the run did NOT reach the
requested New Task click, and the empty landing page is not proof of that click.

The query failed on two HTTP 200 SSE responses with zero decoded events and
`unexpected end of JSON input` / `responses_sse_json_decode`. With the route guard
in place, no unasserted fallback was used. Raw frames are still needed to identify
the decoding cause; no empty-frame/truncation hypothesis is claimed proven.

Additional scope findings: the UI hook lacks read-only discovery of auto-created
session IDs; its idle panel is not authoritative Stop evidence. Audit whether
same-query omitted-session Observe can recreate a user-stopped grant. These
session orchestration items, real permission revoke/regrant, non-drag crash
release, and complete model navigation remain required. Investigation and
next-step boundaries are recorded in `memory/2026-09-28-computer-use-runtime-debug.md`.

Task-owned temporary fixture binaries, diagnostic source, test sockets and probe
source were removed. Normal desktop build is running; requested evidence remains
under ignored validation paths. No formal signing or release was attempted.

### SSE framing and same-query Stop slices (baseline f5ae71a, clean/pulled)

Main investigates Responses SSE framing using the pinned SDK v3.32.0, official
streaming documentation, and a gated synthetic live probe. Capture only field
counts/data lengths/classification, never frame contents, prompts, keys or images.
Do not filter frames until the real failure shape is known; corrupt nonempty JSON
must remain an error. Separate worker owns tracked per-query Ensure and CLI tests
to prevent omitted-session Observe from recreating a user-stopped grant. No new
branch/worktree. Each tested coherent slice will be reviewed and pushed separately.

SSE slice root cause and verification:
- With synthetic generated text plus a one-pixel image (no user data), the live
  gpt-6-sol endpoint sent HTTP 200 SSE whose first block was three raw bytes,
  one comment field, zero data bytes. Pinned SDK v3.32.0 dispatches every blank
  separator even when no data field exists, then JSON-decodes the empty payload.
  The probe failed with `unexpected end of JSON input` and zero semantic events.
- Local regression reproducing exactly `:\n\n` failed before the fix. A private
  Responses-only HTTP body adapter now drops *only blocks without data fields*;
  any explicit empty/whitespace/malformed data remains byte-for-byte and errors.
  It does not replace or globally register an SDK decoder, retry calls, rewrite
  JSON, normalize input, or alter non-SSE/error responses. Memory/aggregate frame
  size bounded to the SDK ceiling. Underlying close/cancel/partial errors persist.
- The adapter wraps outside the raw activity timeout guard: comment traffic
  keeps the network alive even if no semantic event is emitted. Tests cover
  partial reads, CRLF, multiline data, EOF, malformed/empty data, scoped responses,
  bounded comments/data, cancellation and idle activity.
- A second large synthetic live run with the fix saw a comment-only block
  followed by nine valid data blocks, and completed successfully in 13.45s.
  Sanitized evidence in ignored `sse-framing/probe-before.json` and
  `sse-framing/probe-after-large.json` contains counts/lengths only. Raw contents
  were never persisted. Temporary opt-in probe source is removed after use.
- Full race suites passed: anthropic client, CLI, bridge, controller, tool. An
  official documentation fetch was attempted but unavailable; the concrete
  claim rests on pinned SDK code and before/after live + local reproduction,
  not an unverified documentation assertion.

Same-query Stop precedence slice:
- Before-change regressions reproduced implicit re-Ensure after Stop, concurrent
  duplicate startup (24 calls), and retry after uncertain startup. Query-local
  service now consumes exactly one startup attempt, returns the same bound ID,
  coalesces concurrent callers without holding state locks over IPC, keeps errors
  sticky, and checks owner/context even on cache hits. Host Stop remains authority;
  subsequent observe addresses the stopped grant and is denied, not recreated.
- Cleanup closes the query to new startup. A late ACK containing the exact ID is
  cleaned up once; callbacks cannot rebind to another session. Independent queries
  still construct independent trackers and can start under their own lifecycle.
- New query tests passed under -race for 50 repetitions, existing ComputerUse and
  cleanup tests passed, and full five-package race run passed with the SSE slice.
- Explicit open boundary: if the host created a grant but its ACK is permanently
  lost, no exact session ID is available for Stop. No Lookup-based guessing is
  used. A future query/start-attempt binding plus cancellation protocol must close
  that gap; this commit does not claim lost-ACK authority revocation is solved.

### Phase closure — P1-1 exact startup-attempt recovery — 2026-09-29

The lost-Ensure-ACK boundary is now implemented for the production desktop
bridge. A query creates one opaque `cu-attempt-*` token and sends it with the
host Ensure request. The host records the `(tenant, user, conversation,
startup-attempt)` binding only after it has an approved live session. If the
Ensure response is lost, the query asks the host to resolve that exact token;
it never falls back to Lookup and cannot bind another conversation's session.
The bridge validates and echoes the token on both Ensure and exact resolve
responses, and legacy bridges retain their previous Ensure-only compatibility
path.

Verification:

- Runtime regression simulates a host that committed one grant and permanently
  drops the Ensure acknowledgement. Exact resolution recovers the same ID with
  one startup and zero Lookup calls.
- A real Unix-socket bridge integration sends the attempt token over the wire,
  resolves it, and confirms the existing owner/controller remains the only
  target. Focused `desktop-v2`, `internal/computerbridge`, and `internal/cli`
  tests pass (the broad CLI suite still has one pre-existing/flaky agent-eval
  failure unrelated to this slice).

Priority after this phase:

1. **P0-3:** real host-bound Screen Recording/Accessibility revoke and regrant;
   user has now confirmed action-time authorization, so perform the native
   settings transition and failure/recovery evidence next.
2. **P1-2:** authoritative model-created session discovery/status in the
   desktop UI, with exact-session controls and real UI evidence.
3. **P1-3:** software multi-display negative-origin, mixed-DPI, display-routing,
   and topology-change coverage; physical second-display acceptance remains
   deferred.
4. **P1-4:** final functional regression and evidence audit.
5. Deferred LAST: formal signing/distribution and second physical display.

### Phase closure — P1-2 authoritative model-session discovery — 2026-09-29

The desktop UI now exposes a read-only `GetActiveComputerSession` bridge backed
by the same host manager used by the model runtime. It does not initialize a
helper, capture a screenshot, refresh observation freshness, or call Lookup.
Snapshots carry the immutable owner and an explicit `owner_kind`; the frontend
polls the authoritative status only while it has no bound local session.
Model-created sessions become visible in the floating Computer Use workspace,
continue through the existing exact-session status polling, and expose only
Stop in the UI. Observe/Pause/Resume remain disabled for a model-managed grant
so the desktop surface cannot inject an unscoped action into the model loop.

Verification:

- Go bridge contract and read-only snapshot tests pass.
- Web Computer Use tests pass: 83 tests, including discovery of a managed
  session and exact-ID Stop; typecheck and production Vite build pass.
- The frontend retains compatibility with older hosts that do not expose the
  optional discovery method; no fallback to Lookup was added.

Priority after this phase:

1. **P0-3:** finish the real Screen Recording/Accessibility revoke and regrant
   run now that the user has confirmed it. macOS has currently opened an
   authentication sheet for the requested toggle; a Touch ID/password action
   is required at the OS boundary.
2. **P1-3:** software multi-display negative-origin, mixed-DPI, display-routing,
   and topology-change coverage; physical second-display acceptance remains
   deferred.
3. **P1-4:** final functional regression and evidence audit.
4. Deferred LAST: formal signing/distribution and second physical display.

### Phase closure — P1-3 software multi-display coverage — 2026-09-29

The native safety fixture now explicitly covers a mixed-DPI secondary display in
addition to the existing negative-origin and topology-change cases. The test
routes an observation and click to the selected display, verifies the reported
scale factor (`1.5`), and checks pixel-to-global-point conversion at the
negative global origin. Existing window-target identity, display routing,
and topology invalidation tests remain in the same deterministic fake-platform
suite. This is software/mocked coverage only; no physical second monitor is
claimed.

Verification:

- `bash native/macos/tests/run.sh` passes 244 native safety assertions.
- `bash native/macos/tests/platform.sh` passes 469 platform event assertions.
- The suite posts no real input and captures no real desktop during this gate.

Priority after this phase:

1. **P0-3:** complete real host-bound permission revoke/regrant and collect
   failure/recovery screenshots. System Settings is currently waiting for a
   user Touch ID/password authentication at the OS security boundary.
2. **P1-4:** final functional regression/evidence audit after P0-3, including
   fresh desktop build, real single-screen input/model evidence reconciliation,
   and cleanup of unrequested validation artifacts.
3. Deferred LAST: second physical display and formal signing/distribution.

### P0-3 implementation slice — host-bound TCC authority — 2026-09-29

The real permission probe exposed a lifecycle defect: the nested helper could
report its own TCC grant as ready even after the signed desktop host entry was
toggled off. Production `macos.Backend` now accepts a host-owned TCC checker,
uses it to overlay capabilities, skips the helper's permission-request prompt,
and blocks Observe/Execute before helper dispatch when host capture/input access
is revoked. The helper remains responsible for geometry/readiness metadata, not
permission authority. The signed desktop factory wires the macOS host checker;
legacy test/fake configurations retain their old helper probe behavior.

Focused and race tests pass, including host-gate capability override, nested
prompt suppression, and pre-dispatch Observe/Execute rejection. This closes the
implementation defect but **does not close the real P0-3 acceptance gate yet**:
the manual System Settings revoke/regrant run must be repeated against this
fresh build, with screenshots and a recovery-after-regrant observation/input
proof. The current desktop build requested another macOS authentication when
attempting the revoke transition.

Priority remains:

1. Finish real P0-3 revoke/failure/regrant/recovery evidence against the fresh
   host-gated build.
2. Run P1-4 final regression/evidence audit and clean temporary artifacts.
3. Deferred LAST: physical second display and formal signing/distribution.

### Phase closure — P0-3 real host permission revoke/regrant — 2026-09-29

The real macOS lifecycle gate is now closed for the current single-screen
functional scope. Using the freshly built `computeracceptance` desktop app and
its actual host acceptance socket:

- Screen Recording and Accessibility were manually revoked in System Settings,
  with screenshots saved under
  `desktop-v2/build/validation/20260929/p0-3-permission-lifecycle/`.
- A fresh host/helper capability probe reported
  `capture_readiness=permission_required`, `input_readiness=permission_required`,
  and `permission_state=required`.
- Start was rejected with `computer backend is not ready`; Observe and Execute
  were not dispatched and were rejected (`computer session not found`).
- Both permissions were restored, the app/helper were restarted fresh, and a
  new capability probe reported `ready/ready/approved`.
- A fresh observation produced a real PNG, a real click returned
  `outcome=executed` with an after-observation screenshot, and Stop returned a
  confirmed stopped session.

This evidence also verified the host-owned TCC fix from `ed74542`: helper-side
permission state can no longer make a revoked desktop host appear ready.
Evidence manifest: `desktop-v2/build/validation/20260929/p0-3-permission-lifecycle/p0-3-summary.json`.

Priority after this phase:

1. **P1-4 final regression/evidence audit:** rerun the affected Go/race,
   native, web, and desktop build suites; inspect the real evidence manifest;
   clean temporary sockets/processes and reconcile the release checklist.
2. Deferred LAST: second physical display and formal signing/distribution.

### Phase closure — P1-4 final functional regression/evidence audit — 2026-09-29

The functional Computer Use phase is now reconciled against current evidence.
The fresh P0-3 manifest proves host-bound revoke/failure/regrant/recovery;
prior real evidence proves single-screen click/double-click/right-click/move,
drag, scroll, Unicode/key/hotkey input, helper crash uncertainty, and the
authenticated gpt-6-sol WorkBuddy navigation loop. The desktop UI now exposes
model-managed sessions read-only with exact Stop, startup ACK recovery uses an
exact attempt token, and software multi-display coverage includes negative
origin, mixed DPI, routing, and topology invalidation.

Final regression results on 2026-09-29:

- `go test ./internal/computerbackend/macos ./internal/computeruse
  ./internal/computerbridge ./desktop-v2 ./internal/tools/computeruse ./internal/cli`
  passed.
- The corresponding `go test -race` suite passed. macOS linker emitted only
  the existing malformed `LC_DYSYMTAB` warnings.
- `bash native/macos/tests/run.sh`: 244 native safety assertions passed.
- `bash native/macos/tests/platform.sh`: 469 platform event assertions passed.
- Web Computer Use tests: 83 passed; web typecheck and production build passed.
- Fresh `bash scripts/build-desktop-v2.sh -tags computeracceptance` completed;
  the app and helper passed local ad-hoc `codesign --verify` validation. This
  is not formal Developer ID signing, notarization, or distribution evidence.
- Worktree is clean and `HEAD` equals `origin/main`.

Functional-phase conclusion: all requested in-scope features are implemented
and supported by matching evidence, subject to the explicitly deferred physical
second-display and formal signing/distribution gates. No claim is made that
those deferred gates passed.
