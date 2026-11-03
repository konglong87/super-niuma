# Computer Use stability and real desktop closure — 2026-10-08

> Historical stability ledger. The implementation described here was folded into the
> current runtime. For public usage scope and current acceptance status, see
> [Computer Use 使用说明](../usage/computer-use.md) and the
> [current implementation ledger](2026-09-27-computer-use-product-functions.md).
> Historical failures below are not current release status.

## Current continuation ledger — 2026-10-08 evening

Baseline main/HEAD/origin 942ed52, pulled before work. Only pre-existing generated
`scripts/__pycache__/` is untracked; never include it. No branch/worktree.
Read-only Codex/Claude comparison completed before this continuation.

| Priority | Item / intended files | Acceptance gate | Status |
|---|---|---|---|
| P0-1 | Native lifecycle diagnostics + origin-only capture recovery; Platform.swift, Engine.swift, native fake tests, the two current plan ledgers | Failing regression first; native fake + focused Go/race; commit/push; current clean build | implemented; automated gates passed, commit/push next |
| P0-2 | Isolate passive lifecycle and one-shot input using the existing acceptance Driver | Same-run screenshots; no input during lifecycle; type exactly once, no send/replay during input probe | passive lifecycle reproduced; one-shot probe blocked before input |
| P0-3 | Requested gpt-6-sol/high closure | Fresh attested build; cold launch/type/send once/reply/Stop <=120s | waits for P0-1/P0-2 |
| P1 | Native control/display regression from authoritative product queue | Native clicks and current screenshots | unchanged |
| Deferred | Second physical display and formal signing/distribution | Explicit user deferrals | unchanged |

Current-build experiment `lifecycle-942ed52-01`: target PID 48385/window 31582
observed at 0/5/15 seconds. At 15 seconds frame changed from 1200x792 to 472x312;
at 30 seconds it was missing from on-screen inventory while the target process
was still present. No input was dispatched. Stop returned. Initial/15s images
were visually inspected. This does not establish hidden vs minimized vs destroyed.
The host later exited; no attribution to the user or OS is made.

`input-942ed52-02` stopped before its first input: after-capture check returned
unsupported_display despite unchanged display/window/pixel dimensions. Existing
metadata omits bounds, so record both bounds and distinguish position-only motion
from identity/dimension/topology changes. Retrying only a discarded observation
must preserve focus/permission/session checks and must never replay an input.

## Architecture and safety contract

Reuse the host target registry, Controller/Runner, macOS Backend, native bridge,
SafetyState and existing acceptance wrapper. Keep stable window identity
(window ID + owner PID + bundle ID) separate from capture geometry, render
readiness and input authority. Diagnostics are metadata-only NDJSON, never
screenshots, window titles, input text, keys, headers or raw private errors.

An input outcome is based on dispatch stage, never only on an error code:
pre-dispatch rejection; fully acknowledged input but missing evidence;
partial/uncertain input; broken transport; user Pause/Stop; permission revocation.
Unknown input must not automatically replay even under a new action ID.
Explicit Pause/Stop and permissions always win. No app-specific core logic.

## Change ledger / execution queue

Baseline: main, HEAD/origin/main bb13e6e; only pre-existing generated
scripts/__pycache__/ is dirty. Never include it. No branch/worktree, no credential
inspection. Existing /tmp/tool-observe.log and /tmp/go-mismatch.log retained.

| Priority | Slice / intended files | Gate | Status |
|---|---|---|---|
| P0-1 | Append-only Go/native diagnostics; computerdiag, tool, backend, controller, native Diagnostics/Engine/Platform/Safety, helper build/tests | Focused Go + native regression; commit/push before real run | implemented; focused Go and 257 native assertions pass; committed/pushed d16fad7 |
| P0-2 | Fresh-run evidence isolation; scripts/computer-use-120s-acceptance.py and tests | Reject mixed time/session/build evidence and existing output dir; preserve launch receipt on observation failure | committed/pushed 3dcec8d + 6cf1d91; follow-up evidence in ee92517 |
| P0-3 | Diagnose real launch/observe; bounded unique-window stabilization and capture snapshot consistency | Fresh build identity + repeated cold observe-only, screenshots and Stop | implementation committed/pushed 222de75 + 950818d; real gate blocked by Mac lock |
| P0-4 | Dispatch-stage recovery contract across Swift/Go/controller/session | Native partial/complete/before-input faults, Go contract tests, unknown replay prevention, Pause/Stop/permission races | committed/pushed c6c2d48; automated safety gates pass |
| P0-5 | Actual autonomous model closure | Actual provider jiuan-responses-gpt-5.6sol, gpt-6-sol, high; launch→observe ready→new task→type 1+1=2→send once→reply screenshot→Stop, <=120 s | depends above |
| P1 | Current queue native Resume/Stop + broader regression, non-WorkBuddy fixture | Native clicks/screenshots; focused then broader Go/race | pending |
| Deferred | Second physical display; formal signing/distribution; blocked optional release artifacts | User explicitly deferred; not represented as passed | unchanged |

## Verification / delivery

For each coherent source slice: focused tests, git diff --check, status/stat,
commit konglong <konglong@com>, push origin/main. Desktop build exclusively via
bash scripts/build-desktop-v2.sh. GUI target operations through project
ComputerUse only. Use unique dated evidence directories, bind every receipt and
image to current session/run/build, inspect actual images, preserve failed
screenshots. Readiness is not proved by non-white pixels alone. At most 12 s
passive blank-screen waiting in the complete 120 s loop, no blind input.

### Phase update — deterministic Computer Use prompt fast-path fix — 2026-10-13

The last fresh model run reached four valid native actions but missed send because
model turns consumed the 110s operational boundary. The exact 1428-rune acceptance
prompt was rejected by the 1200-rune fast-path classifier. A separate regression
covers a negated `Bash` safety mention; it is not proven to be a second rejection
cause for this exact prompt's punctuation.
Commit `cc9e806` raises the classifier ceiling to 2000 runes and distinguishes
negated safety mentions from positive blocked intent. Exact prompt auto-fast-path
regression coverage passes with bounded turns/tokens and bare runtime profile;
`go test ./internal/query`, `go test -race ./internal/query`, focused CLI/race
tests, native fake safety assertions, tagged desktop build, and strict deep
codesign verification pass. This does not claim the final model closure; only a
fresh run on the attested build can close that gate.

Keep requested diagnostic logs and validation evidence locally and out of Git;
clean only task-created unrequested temporaries. Final gate: diff --check,
status, log -1, HEAD == origin/main. Report tests, screenshots, commit/push,
actual loop result and exact residual risk; no false closure.

### Slice 1 verification

Append-only metadata diagnostics implemented; no recovery or identity gate
semantics changed. Focused six Go packages plus computerdiag pass; native
fake-platform suite 257 assertions passes. Diagnostics now identify helper
command/request IDs and native identity/geometry gates. New native log:
`/tmp/swift-mismatch.log`. Real current-build acceptance still pending.

### Slice 2 verification

Acceptance now uses unique default directories and rejects nonempty directories;
action timestamps must belong to the current run. Structured launch identity is
read even when tool output is truncated. Launch receipt is retained if the bound
observe fails. Build entry adds a signed-resource source/byte attestation;
acceptance CLI rejects stale/dirty/tampered builds. Added observe-only gate.
Tests: Python 20 tests, ComputerUse tool tests; shell syntax check. Native test
script executable-bit change from slice 1 is restored to the repository pattern.
Full model closure remains pending; no old mixed batch is counted as a pass.

Build-attestation follow-up: the outer codesign seal changes the main executable
signature bytes, so embedding its full SHA in its own signed resources is
circular. Use the immutable Go build ID in the resource manifest, helper SHA,
and strict outer/nested codesign verification; measure final executable SHA in
the run evidence. Initial gate correctly rejected this inconsistency before
any target input. Follow-up tested; latest build rerun required.

### Fresh desktop diagnosis / slice 3

Current attested 6cf1d91 cold run saved under
`desktop-v2/build/validation/20261008/stability-observe-6cf1d91-01`.
Correct actual route: jiuan-responses-gpt-5.6sol / gpt-6-sol / high, image/png.
Native launch discovered two same-app windows and selected a frontmost blank
full-screen window; the body screenshot is visibly white, not a pass. Later
wait failed before backend dispatch. Persisted server events discarded structured
identity and truncated the nested launch window. Fixes: all supplied model
session IDs are rebound to trusted current session; shared ResultMetadata is
used in both runtime and server event paths, with titles/private fields excluded.
Focused/full computeruse/tool/agentruntime/server tests and Python 20 tests pass.
This slice is separate from pending native readiness and dispatch recovery.

### Slice 4 — native readiness / snapshot consistency

Generic content-body variation sampling excludes titlebar/shadows and normalizes
pixel format/alpha. A ready candidate must keep the same stable identity/frame
for 100 ms; ambiguous startup inventory waits within the existing launch budget
rather than failing immediately. Capture returns the actual geometry/window
snapshot, which is saved for next-input authorization; motion after observation
still rejects input. No identity loosening. Python blank guard ignores chrome
and preserves failed/white images as invalid evidence instead of dropping them.
Native 267 assertions and Python 20 tests pass. Actual latest-build cold/native
and model acceptance pending; older white image remains failed evidence.

### Slice 5 — dispatch-stage recovery

Native/Go receipts now carry dispatch_state independently of screenshot
verification. Fully acknowledged input with a failed after-image stays executed
with verification unknown; only observation is refreshed, never the input.
Partial/unknown input is terminal even if focus_changed or the helper is alive;
Session latches uncertainty so new action IDs and Resume cannot replay it.
Pause/Stop revocation and permission-required explicit recovery remain gates.
Public screenshot_failed/input_uncertain codes are retained; read-only capture
retries are bounded and generation/identity/permission checked.
Verification: focused Go tests, broader query/agentruntime/server packages,
race computeruse/macos, native 266 assertions (after-capture expectations now
correctly distinguish acknowledged input from partial input). Actual native
and production-model closure remain pending; no simulated pass is substituted.

### Scripted acceptance support

The opt-in computeracceptance build now exposes registered target launch through
the same active Controller (forged session/unregistered target are rejected),
so native smoke/cold-repeat checks do not spend model turns or use external
GUI automation. This adapter remains disabled in ordinary builds. Build evidence
also attests the embedded model-service bytes, not only desktop/helper.
Tagged desktop tests and Python 20 tests pass. Next highest-priority gate:
latest tagged .app, real cold target capture and a non-WorkBuddy fixture, then
actual requested-provider/model 120-second closure. Deferred gates unchanged.

Launch selection follow-up: readiness now filters the inventory before selection;
a unique rendered main window may be raised during launch when a blank splash
is frontmost. Exact frontmost identity must then stabilize before binding; no
activation is added to input/evidence checks. Multiple ready ambiguous windows
still fail closed within the existing budget. Native fake regression added.

## Current phase closure / remaining queue — 2026-10-08

Code slices committed/pushed through 950818d; final malformed-dispatch/privacy
and acceptance-gate follow-up tested below. Full `go test ./...` passes. Earlier
full-suite runs exposed a stale CLI Stop test expecting the removed observe_failed
label (updated to the stable public error contract), and one unrelated default
agent-eval timing failure (focused repeated run passes; final all-package run
passes). ComputerUse focused/race suites, tagged desktop tests, Python 22 tests,
and native 267 assertions pass. Latest tagged desktop build and deep/strict
local signature verification pass. No release-signing claim.

Concrete external blocker: native Computer Use returned “The Mac is locked and
automatic unlock could not unlock it” when exiting the old app for cold-start
acceptance. User was asked to unlock manually. Do not bypass the lock, type an
unlock credential, or claim actual target/control/model acceptance.

Remaining in descending priority:
1. **Native scripted acceptance (blocked on unlock):** cold-start the current
   attested app; repeated target observe/launch/bind/content screenshots + Stop;
   also Calculator fixture and actual native Pause/Resume/Stop clicks/screenshots.
2. **Autonomous model acceptance (depends on #1):** actual requested provider,
   gpt-6-sol/high, image/png; launch→ready→type 1+1=2→send exactly once→reply
   screenshot→Stop within 120 seconds. New output directory each run. No pass yet.
3. **Regression-driven implementation:** act only on fresh current-build evidence
   if the native/model gates expose another failure; preserve failing images,
   known/unknown dispatch semantics and receipt identities.
4. **Deferred unchanged:** second physical display and formal signing/distribution;
   optional release artifacts remain externally blocked.

Existing real white screenshot in stability-observe-6cf1d91-01 remains failed
historical evidence, not a passing screenshot. All requested /tmp diagnostic
logs retained. Pre-existing scripts/__pycache__/ left untouched and uncommitted.


### Resumed cold-start investigation — 2026-10-08

- Resumed target thread `01a1194a-9317-75b3-b88b-3823b44a8577`; its last user
  message was “已解锁，继续”. Pulled main (d789c13); only pre-existing generated
  `scripts/__pycache__/` is dirty and remains excluded.
- Current attested tagged app built and signed. Desktop is unlocked and host TCC
  reports capture/input approved. Cold native launch returned the real rendered
  WorkBuddy window in 4.59 s; first bound observe failed. Fresh diagnostics prove
  launch used session `host`, observe used the actual controller session, and
  Swift rejected `stale_observation`. This is not a capture/permission guess.
- Minimal architecture correction: BackendLauncher receives the authorized
  session explicitly from Controller; never infer it from a prior observation
  or fall back to `host`. Native cross-session checks remain unchanged.
- Intended/current-task files: computeruse/backend.go, controller.go,
  targets_test.go; macos/backend.go, backend_test.go; this ledger. New cold-start
  regression failed before the fix. Missing session is rejected before IPC;
  forged Controller session never reaches the launcher. Focused six-package
  tests, ComputerUse/macOS race tests, full `go test ./...`, tagged desktop
  tests and Python 22 tests all pass. Commit/push precede rebuild and fresh
  native/model acceptance.
- Evidence (local ignored):
  `desktop-v2/build/validation/20261008/continuation-d789c13/` (build identity,
  initial state, build log) and `continuation-native-01/` (launch, failure
  diagnostics, paused snapshot, Stop). No failed observation counted as pass.
- Remaining order unchanged: repeated cold target captures + non-WorkBuddy
  fixture + native controls; then requested-model 120 s loop. Old lock blocker
  is cleared. Second physical display/formal signing remain deferred.


### Resumed desktop control discovery slice

- e45454d is committed/pushed. Current rebuilt native host cold-starts WorkBuddy
  and captures rendered content successfully. `continuation-native-e45454d-02/`
  holds three same-session/window/PID/bundle captures (0.138/0.128/0.251 s), a
  3.287 s launch and Stop. Calculator launch (1.075 s) and two 396x700 captures
  are in `continuation-calculator-e45454d-01/`. Actual PNGs inspected. The -01
  WorkBuddy capture succeeded, but the local validation client used the wrong
  metadata path and aborted; retained as harness failure, not product pass.
- Current architecture already intentionally disabled the independent NSPanel
  in c77d5d7 (“keep computer use overlay inside desktop window”). Do not undo
  this product choice to satisfy historical NSPanel queue text. Current desktop
  control acceptance targets actual clicks/screenshots inside the Wails window;
  disabled NSPanel acceptance remains unclaimed.
- Fresh native screenshot showed stale “stopped” while host's exact-session
  snapshot was ready. Root cause: useComputerSession stopped discovery forever
  as soon as any session ID existed, even after terminal Stop/failure.
- Intended files: useComputerSession.ts and its tests, this ledger. Restart
  read-only discovery only when idle/terminal; never resurrect same terminal ID,
  resume Pause/Stop, Observe as a fallback, or retain old images/receipts/approval
  when binding a replacement. Generation checks reject races with local starts.
- Regression tests fail before fix; focused UI tests and typecheck pass. Full
  web suite passes: 79 files / 793 tests. Commit/push, fresh build and native
  UI click acceptance next. Model
  120 s acceptance follows native controls and a repeated cold observation.


### Current desktop controls / exact cold-start evidence guard

- e5b389b committed/pushed; full web 79 files / 793 tests and typecheck passed.
  Current attested desktop build used for actual CUA clicks on Pause, Resume and
  Stop. Exact host session snapshots confirm paused → needs_observation → stopped.
  Evidence: `continuation-ui-e5b389b/02-paused.png`, `03-resumed.png`,
  `04-stopped.png` and corresponding same-session JSON. This is the current Wails
  in-window UI, not disabled standalone NSPanel acceptance. A stale CUA index
  briefly opened the app menu; cancelled, freshly reindexed and never counted
  as a Pause pass. Successful controls followed with actual screenshots.
- Cold-start auditing found fixture executable is `Electron`, not its display
  name `WorkBuddy`. Earlier local pgrep-by-display-name preflights cannot prove
  cold state. Withdraw those cold labels (rendered captures/binding remain real);
  do not combine them with a later run to claim closure.
- Intended files: acceptance Python wrapper/tests and this ledger. Preflight now
  reads public Info.plist metadata, verifies the registered bundle, matches the
  exact primary executable path in `ps pid,comm` only, and rejects a warm target
  before model-session creation. No environment/auth/config reads. Python 24
  tests pass. Wrapper prompt also requires replacing any pre-existing draft
  before the one fresh type action; old draft is never proof of new input.
- One rapid relaunch after fixture Quit returned launch_timeout and no image;
  preserved in `continuation-cold-e5b389b-02/`. Exact process exit must be confirmed
  before rerun; no timeout extension or blind input. Fresh independent batches
  and real image inspection still required.
- Next highest-priority gates: commit/push + rebuild; two cold observe-only
  native batches with exact process preflight; requested gpt-6-sol/high real
  launch/type/send once/reply/Stop <=120 s. Deferred gates unchanged.


### Exact cold-start gates passed / model-wrapper discovery follow-up

- Current 6e1365a attested app: two exact cold native batches passed, starting
  with no primary executable and launching distinct PID/window pairs
  (48855/27610 and 49436/27668). Each saved three same-session, same-target PNGs
  and Stop. Launch 4.385/3.306 s. Actual ready images inspected; all six assets
  retained under `continuation-cold-6e1365a-{01,02}/`. Separate project-ComputerUse
  cleanup sessions Quit the fixture and confirm exact process termination.
- 120 s model attempt stopped before any model call: wrapper process discovery
  incorrectly required the complete host command line to end in its executable;
  the native acceptance socket arguments made the valid host invisible.
- Intended files: wrapper, regression tests, ledger. Match the executable token,
  not the full command suffix; preserve existing workspace/server-parent gates.
  Synthetic-token regression fails before fix; Python 25 tests pass after fix.
  No auth token is emitted or persisted; no credential/config files accessed.
- Next: commit/push, rebuild the current attested app, then actual requested-model
  120 s loop (fresh directory). Native/cold successes above remain run-specific,
  never merged into the model action chain. Hardware/signing deferrals unchanged.


### RFC3339Nano / complete sanitized receipt follow-up

- Actual requested-provider/model/high run on attested 8c0aae9 returned completed
  in 93.887 s, with observe → input-focus click → select-all hotkey → type once
  → send click once → wait → Stop. Wrapper rejected evidence because this
  Mac's older Python cannot parse legal Go RFC3339Nano fractions of 4/5 digits
  (`.39121Z`, `.8689Z`). It was a parsing false rejection, not old timestamps.
- Preserve original failure/timing files. A read-only post-run image audit
  downloaded the original six asset IDs into `continuation-model-8c0aae9-01/
  post-run-image-audit/`. The wait screenshot visibly shows this run's newly
  created task (19 vs initial 18), `1+1=2` and real response `2 ✅`. This audit
  does not rewrite the failed batch as passed or replace a fresh acceptance run.
- Intended files: Python wrapper/tests, computeruse/result_metadata.go and its
  tests, this ledger. Normalize valid Nano fractions to Python microseconds
  for comparison only; retain original timestamps and all isolation gates.
  Preserve exact cold preflight instead of overwriting it with first-observe
  metadata. Reuse ResultMetadata before text truncation to retain a sanitized
  full action receipt (IDs, dispatch, verification, duration, completion and
  window identity); exclude private errors, summaries, fingerprint/media URIs
  and titles. Python retains these structured receipts and launch evidence.
- Atomic timing comes from actual LaunchReceipt duration/completion; binding
  remains included in launch and is not invented as an independently measured
  operation. Separate first-capture readiness timing and stage notes are saved.
- Timestamp regression fails before fix; Python 28 tests (including mixed native
  session/stale completion rejection), focused Go, full `go test ./...`, tagged
  desktop, ComputerUse/macOS race and native 267 assertions pass. Fixture Quit
  and exact process exit confirmed through project ComputerUse. Commit/push,
  rebuild and fresh requested-model 120 s loop remain the next gates.


### Legal maximum wait exposes shared-deadline defect

- 6dcd58c current-build model run -02 completed in 74.401 s, but failed acceptance:
  legal `wait(duration_ms=10000)` consumed the entire 10 s Controller/RPC deadline,
  leaving no capture/receipt time. RPC timed out, helper became unavailable and
  subsequent Stop had control_failed. Preserve failed batch/screenshots; no replay.
- Receipt metadata follow-up: empty success error codes must remain empty, not
  normalize to action_failed. Regression and focused Go gate pass before commit.
- Next architecture slice: retain 10 s operation-duration validation but separate
  a fixed 2 s evidence grace in Controller + macOS execute RPC timeout. Reuse a
  platform-neutral timeout policy, apply to wait/drag only, honor shorter caller
  deadlines/cancellation/Stop. This is <=12 s bounded per operation, not any change
  to the business run budget or the 120 s acceptance deadline. Tests must inspect
  both Controller and native envelope deadlines without sleeping ten seconds.
- New run -01 started its wrapper before the asynchronous local service child
  existed; no model call occurred. -02 started only after read-only child readiness
  was confirmed. Startup failures remain separate and are not mixed into evidence.


### Bounded operation/evidence deadline separation

- Implemented a shared platform-neutral ActionExecutionTimeout policy: legal
  wait/drag duration up to 10 s plus fixed 2 s evidence grace, only when longer
  than the baseline. Invalid durations and other kinds never expand deadlines.
  Earlier caller deadlines, cancellation, user Pause/Stop and total run budgets
  remain authoritative. macOS execution envelopes and broker leases use this
  same policy; ordinary observation/control RPC deadlines are unchanged.
- Intended/completed files: computeruse/model.go, controller.go,
  computeruse_test.go; macos/backend.go, backend_test.go; this ledger. Both the
  Controller deadline test and native-envelope fixture failed before fix at
  9.999 s / closed response; pass with <=12 s now. Earlier caller deadline and
  kind/invalid-duration boundary regressions pass without long sleeps.
- Focused six packages, full `go test ./...`, ComputerUse/macOS race, tagged
  desktop tests pass. No native input was executed by these fake tests.
- Next: commit/push; fresh tagged app; real 10000 ms wait + after-image + Stop
  on Calculator (no model), then isolated cold requested-model 120 s closure.
  Unknown input recovery/replay rules unchanged. Deferred gates unchanged.


### Model prompt hardening after fresh no-send run

- Fresh 9e4914e model run -02 had exact cold preflight, atomic launch and
  successful click/type receipts, but the type after-image visibly remained an
  empty composer; the model correctly stopped without replaying type, so the
  batch failed the send gate. This is not counted as a pass. The prior fresh
  -01 had a target-window mismatch before type; both failed evidence remain.
- The earlier 8c0aae9 audit image had a visible `1+1=2` and reply, but was from
  an older source/wrapper batch and is not retroactively promoted.
- Harden the generic prompt (no WorkBuddy selector/coordinate): after locating
  the input, click it, use exactly one `command+a` hotkey to select/clear any
  existing draft, then exactly one type. Never replay type after a receipt;
  verify the type after-image before send. This matches the observed successful
  model action pattern while preserving unknown-input safety.
- Next: commit/push this prompt-only acceptance slice, rebuild current attested
  app, clean the fixture via project ComputerUse, rerun fresh exact cold model
  closure. If type after-image is still empty, preserve and diagnose rather than
  weakening replay/focus guards.

### Final resumed-run state — 2026-10-08

- `9e4914e` wait-policy build: the latest live Calculator 10 s wait batches
  returned `focus_changed`/`unsupported_display` when the Wails host became the
  active window during the long passive wait. Stop was acknowledged. This is
  correctly a failed evidence action, not permission to bypass focus or activate
  a different window automatically.
- `5248e1a` prompt-hardening build: exact cold model run `continuation-model-
  5248e1a-02` used the requested provider/model/high and launched WorkBuddy, but
  after 30 s of model deliberation the bound window disappeared from the native
  on-screen inventory (`window_unavailable`, no input posted). The model then
  stopped; no type/send/reply pass. The immediately preceding `-01` failed the
  exact cold preflight because the prior fixture process was still present; it
  made no model request. Both are preserved as independent failures.
- The successful older 8c0aae9 run's visible reply remains historical audit
  evidence only; it cannot be promoted because it predates the RFC3339/receipt
  fixes and current prompt/build. Current source is clean and pushed at
  `5248e1a`; native/cold/control automated and real evidence above pass, but the
  requested actual model-managed launch → type → send once → reply → Stop loop
  is **not passed**. The concrete remaining blocker is nondeterministic external
  target-window disappearance during model deliberation, while safety correctly
  rejects stale binding and does not replay input.
- No further code change is justified from these two external-state failures:
  activating/rebinding a disappeared window or replaying type would violate the
  established safety contract. Resume only with a fresh unlocked desktop run
  where WorkBuddy remains visible/frontmost; otherwise leave this gate blocked.

### Evening continuation — confirmed focus race and same-window recovery foundation

- Fresh unlocked run `lifecycle-1e1c343-01` reproduced the failure with no input:
  PID 5050/window 32058 remained running and present in the full Window Server
  inventory, but at 15 seconds `application_active=false`,
  `workspace_frontmost_pid=5050`, and `window_order_focus_pid=39522`
  (`ChatGPT.app`). The target was not destroyed or rebound; the two frontmost
  signals disagreed while the target left the normal on-screen inventory.
- Fresh continuous input run `input-1e1c343-02` passed click → command+a → one type
  (`1+1=2`) with all receipts `executed/complete`; screenshots visibly show the
  text in the WorkBuddy input and later observation. No send or replay occurred.
- Model run `model-e1372a6-02` completed explicit launch and target observe,
  but made no input action before the 110-second operational deadline. The
  screenshot still showed the persisted old draft. The prompt now makes the
  post-observe sequence a hard next-call chain: click → command+a → type.
- Source slice in progress: separate full Window Server inventory from on-screen
  inventory. Same-target authorization and launch activation can now find the
  original window by exact ID/PID/bundle during Space/focus recovery, while
  capture/input still require the target to be raised and on-screen. Fake native
  regression covers an off-screen bound target and no input dispatch.
- Acceptance prompt now treats pre-dispatch focus/window rejection as a recovery
  path: fresh observe with the same identity, never action replay; unknown or
  dispatched actions remain terminal.
- Model run `model-fa7b57c-03` reached cold launch, ready observe and one
  executed click after the focus-race recovery. It stopped before type because
  the screenshot contained a persisted old draft `1+1=2`; the prior wording did
  not force the post-click command+a → type sequence strongly enough. No send,
  type replay, or false pass was accepted. Prompt wording now explicitly treats
  any pre-existing text, including the expected text, as stale and requires the
  fixed click → command+a → type sequence before send/stop.
- Model run `model-d91d5f5-01` did not launch the target: its first observe
  returned the current Edge window (`com.microsoft.edgemac`) and no WorkBuddy
  launch receipt. The run was rejected at launch with no target input. The
  prompt now makes `launch_app(target_id=workbuddy)` the mandatory first action,
  removing ambiguity from implicit observe launch.
- Model run `model-2c483f3-01` used the explicit launch correctly, but the
  persisted WorkBuddy main window was found at `x=-1338` outside the active
  display/Space and launch timed out after the weak activation path. No input
  or screenshot was accepted. Same-target activation now includes
  `activateAllWindows` before AXRaise; it does not relax identity checks.

### Final model closure audit — 2026-10-09

- Native recovery evidence passed on current `773cc14` build:
  launch recovered the same WorkBuddy target window to `x=76,y=35,1200x792`,
  observe returned a real 2400x1584 image, and ComputerUse hotkey quit completed;
  exact primary executable exited. This validates `activateAllWindows` + AXRaise
  same-target recovery, not a new target binding.
- Real one-shot input remains passed in `input-1e1c343-02`: click, command+a,
  one type, visible `1+1=2`, no send/replay.
- Requested model closure remains **not passed** after fresh current-build runs:
  - `model-fa7b57c-03`: launch/observe/click executed after focus recovery, but no
    type before the 110s operational deadline.
  - `model-d91d5f5-01`: first action was an invalid current Edge observe; fixed by
    making explicit launch mandatory.
  - `model-2c483f3-01`: explicit launch timed out while the persisted target was
    outside the active Space; fixed and verified by native recovery regression.
  - `model-e1372a6-02`: explicit launch + observe succeeded, but no input before
    deadline.
  - `model-e1372a6-01`: provider/session emitted a failed event with zero actions.
  - `model-773cc14-01`: local desktop server connection was refused during the
    session GET, with zero actions.
- No run is accepted as passed unless the current run has exactly one fresh type,
  one send click after type, a reply wait screenshot and Stop. Existing screenshots
  are retained as failed evidence and are never mixed into a pass.
- Current conclusion: native safety/input/lifecycle path is verified; remaining
  blocker is autonomous model/tool orchestration and intermittent local/provider
  session availability within the 120-second budget, not an unresolved native
  window-identity failure.

### Evening slice 1 — lifecycle evidence / position-only capture retry

- Intended/completed source files: native Platform/Engine/fake tests and the two
  current plan ledgers. Existing generated Python cache left untouched.
- Native diagnostics add all-window presence/on-screen state, current target
  application running/hidden/active/bundle-match flags, and separate NSWorkspace
  versus Window Server focus PID samples. These are read-only metadata, not a
  new capture/input authority or hidden-window fallback. No titles/text/keys.
- Capture now records both bounds. Position-only motion of the same window with
  identical display, dimensions and bounds size discards the image and enters
  the existing three-attempt read-only retry. Other layout/identity changes and
  Pause/Stop still fail closed. After-input retries do not re-dispatch input.
- Both diagnostic and position-motion regressions fail against the baseline.
  The origin regression was independently compiled against HEAD Engine in an
  isolated temporary test directory; that directory was removed.
- Verification: 285 native fake safety assertions; full go test ./...; focused
  ComputerUse/macOS tests and race; tagged desktop tests; Python model wrapper
  28 and safety 23 tests; diff check pass. No frontend change, so no new frontend
  acceptance claim. Current-build native and model acceptance still pending.
- Commit/push this coherent slice, rebuild from the clean commit, then repeat
  lifecycle/one-type experiments before spending another production-model run.

### Fresh tagged lifecycle rerun — 2026-10-09

- Rebuilt the current clean `c4bee52` commit with `-tags computeracceptance`.
  The build manifest records `source_dirty=false`, the current source commit,
  and deep/strict ad-hoc signature verification passed. Evidence:
  `desktop-v2/build/validation/20261009/rebuild-c4bee52-tagged/`.
- Started the tagged host with its owner-only acceptance socket and built a
  fresh two-window native fixture. The fixture itself launched and published
  valid window state, but the host capabilities reported
  `capture_readiness=unavailable`, `input_readiness=unavailable`, and a zero
  coordinate space. The scripted lifecycle driver stopped before dispatching
  any fixture input with `computer backend is not ready`.
- The authoritative native desktop inventory independently reported that the
  Mac was locked and automatic unlock could not unlock it. The failed run is
  retained at `desktop-v2/build/validation/20261009/lifecycle-c4bee52-01/`;
  it is not a product pass and contains no input acceptance.
- No source regression is inferred from this run. The next gate remains a
  manual Mac unlock followed by a fresh tagged lifecycle/one-shot input rerun;
  do not bypass the lock or enter credentials through automation. The
  autonomous model closure remains blocked behind this native gate.

### Unlocked lifecycle evidence exposes pre-dispatch geometry recovery gap — 2026-10-09

- After the user manually unlocked the Mac, the current tagged host built from
  `bed4763` reached ready native capabilities (`2704x1756`, scale 2, capture and
  input approved). A fresh two-window fixture launched and the first screenshot
  was visually inspected at:
  `desktop-v2/build/validation/20261009/lifecycle-bed4763-01/move-before.png`.
- The fixture's independent move mutation preserved the exact window ID, owner
  PID, bundle ID, dimensions, and topology, but changed its position. The stale
  click was rejected before dispatch as `unsupported_display`; the fixture click
  counter stayed unchanged. This is the correct no-replay safety result.
- The controller nevertheless treated that pre-dispatch rejection as terminal
  because `unsupported_display` was not in the transient observation-recovery
  set. The lifecycle driver then could not perform its required fresh observe.
- Added a focused contract regression: pre-dispatch `unsupported_display` and
  `target_window_mismatch` reopen observation recovery without replaying input;
  unknown/partial outcomes remain terminal. Focused ComputerUse/macOS tests and
  race tests pass. Intended files: `internal/computeruse/controller.go`,
  `internal/computeruse/safety_test.go`, and this ledger.
- Next gate: commit/push this safety slice, rebuild the current tagged app, and
  rerun the same lifecycle fixture. Only after lifecycle recovery passes will
  the one-shot input and requested-model closure runs resume.

### Native lifecycle recovery passes on current build — 2026-10-09

- The tagged `8c28ac4` build was rebuilt and verified. The lifecycle driver was
  corrected so every pre-dispatch stale-input rejection explicitly performs
  `capabilities` → `resume` → fresh observe before the positive recovery click;
  the rejected action is never replayed. This applies to move/resize as well as
  minimize/close.
- Fresh native acceptance `lifecycle-8c28ac4-04` passed all four cases:
  `move`, `resize`, `minimize`, and `close`. Each stale click was rejected with
  `dispatch_state=not_started` and no fixture counter change; recovery used a
  fresh observation and a new click. `stop_confirmed=true`.
- Real screenshots were inspected. For example,
  `move-recovered-verified.png` shows the fixture counter changing from `0` to
  `1`, and `minimize-recovered-verified.png` shows the recovered window and
  counter `3`. Evidence is retained under:
  `desktop-v2/build/validation/20261009/lifecycle-8c28ac4-04/`.
- Native lifecycle recovery is now passed for the current build. Next highest
  gate is the fixed one-shot input probe, then the requested autonomous model
  closure. The earlier `lifecycle-bed4763-01` and `lifecycle-8c28ac4-01`
  failures remain retained as historical diagnostics, not passes.

### Fixed one-shot WorkBuddy input probe passes — 2026-10-09

- Fresh current-build probe `input-eb07b4d-04` passed the exact no-model,
  no-send sequence: cold launch through the trusted target registry, observe,
  click the composer once, fresh observe, `command+a` once, fresh observe, type
  `1+1=2` once, then three passive observations and Stop.
- Every input receipt was `outcome=executed` and `dispatch_state=complete`.
  The final screenshot was visually inspected and clearly shows `1+1=2` in the
  WorkBuddy composer:
  `desktop-v2/build/validation/20261009/input-eb07b4d-04/06-type-once-after.png`.
  The summary records `typed_once=true` and `sent=false`.
- An earlier fresh attempt `input-eb07b4d-02` was correctly rejected before
  dispatch as `input_unavailable`; no text was sent. The successful retry used
  the explicit left-button action field and is the accepted probe. No replay of
  the rejected action is counted.
- Native lifecycle recovery and the fixed one-shot input gate now pass on the
  current acceptance build. Next gate is the requested gpt-6-sol/high
  autonomous launch → type → send once → reply screenshot → Stop run within the
  120-second budget.

### Requested autonomous model retry — 2026-10-09

- Fresh requested run `model-064685f-05` used the current attested tagged build,
  provider `jiuan-responses-gpt-5.6sol`, model `gpt-6-sol`, and effort `high`.
  The local session was created with the requested effective configuration and
  reached `running`, but no ComputerUse action trace or screenshot was
  persisted.
- At the 110-second operational deadline, the wrapper's conversation-trace
  request failed because the desktop local server connection was refused. The
  run is retained at:
  `desktop-v2/build/validation/20261009/model-064685f-05/`.
- This is an external local-session availability failure, not a native input
  or window-identity pass. It does not count as model closure. No type, send,
  reply, or model-managed screenshot was accepted. Native lifecycle recovery
  and the fixed one-shot input probe remain passed; the next model attempt must
  start from a fresh host/session and new evidence directory.

### Desktop/session health observability slice — 2026-10-09

- Added redacted host/server health monitoring to the 120-second acceptance
  wrapper. During every run it records `desktop-health.ndjson` and a final
  `desktop-health-final.json` containing only desktop PID/server PID liveness,
  process state, server port, health reachability, and exception class.
  Commands, auth tokens, headers, URLs, response bodies, and credentials are
  never persisted.
- This directly distinguishes host exit, server-child exit, and a refused
  local health connection during model closure without changing action replay,
  deadline, or safety behavior.
- Added two wrapper regressions for healthy and `ConnectionRefusedError`
  snapshots with token-redaction assertions. Full acceptance wrapper tests:
  31 passed.
- Next gate: rebuild the current tagged app, run one fresh requested-model
  closure attempt, and inspect the health timeline before changing desktop
  lifecycle code or model orchestration.

### Health-monitored autonomous model retry — 2026-10-09

- Fresh run `model-eb1c7b8-01` used the current health-instrumented tagged
  build and the requested provider/model/high configuration. The desktop host
  and local server stayed alive for the entire run; `/health` remained
  reachable, and the final snapshot confirms both PIDs were alive:
  `desktop-v2/build/validation/20261009/model-eb1c7b8-01/desktop-health-final.json`.
- The failure is therefore not a desktop host exit, server-child exit, port
  refusal, or local connection failure. The model session remained `running`
  but persisted zero ComputerUse actions and no image route before the
  110-second operational deadline. The wrapper stopped the session safely and
  retained the exact evidence under:
  `desktop-v2/build/validation/20261009/model-eb1c7b8-01/`.
- Root-cause boundary is now narrowed to provider/session orchestration latency
  or no-action model completion, not native ComputerUse or local desktop
  availability. Do not modify native safety or replay inputs. Model closure is
  still open; the next slice should inspect provider run events/latency and
  distinguish “provider has not emitted a turn” from “model emitted no tool
  call” before another production-model retry.

### Provider event heartbeat slice — 2026-10-09

- Added redacted `conversation-heartbeat.ndjson` sampling during model runs.
  It records only event count, event types, tool-call/result counts, last event
  timestamp, and elapsed time; event payloads and sensitive content are not
  persisted.
- This separates provider states such as “session running but no event emitted”
  from “events exist but no ComputerUse tool call” without extending the
  120-second contract or replaying any action. Wrapper tests now total 32
  passed.
- Next gate: rebuild and run one fresh model attempt, then use the heartbeat
  timeline to choose the final provider/session fix or confirm an external
  provider no-action blocker.

### Second heartbeat model retry narrows host exit timing — 2026-10-09

- Fresh run `model-9772943-01` produced a different and more useful trace.
  The provider conversation emitted one `tool_call` and one `tool_result` by
  approximately 31 seconds, then no further events. The desktop health monitor
  shows the host/server alive and healthy until the final snapshot, where both
  processes were gone and the local connection was refused.
- This proves the failure occurs after the first model ComputerUse turn, not at
  initial session creation or provider startup. The exact tool action is not
  persisted by the redacted heartbeat yet; no input/send/reply pass is claimed.
- Added `before close`, `shutdown begin`, and `shutdown complete` startup-log
  markers to distinguish graceful Wails shutdown from an abrupt host exit on
  the next fresh run. This is read-only lifecycle evidence and does not alter
  action authority or replay semantics.
- Next gate: rebuild with the lifecycle markers, run one fresh model attempt,
  inspect the shutdown markers plus the first tool action, then fix the actual
  host/helper exit path if the exit is reproducible.

### Fresh tagged model attempts after heartbeat summaries — 2026-10-09

- Commit `218b27a` added redacted `tool_summaries` to heartbeat evidence and an
  explicit wrapper assertion; `python3 -m unittest scripts/computer-use-120s-acceptance_test.py -v`
  passed all 32 tests. The rebuilt tagged app attests `source_commit=218b27a`,
  `source_dirty=false`, and passes `codesign --verify --deep --strict`.
- Fresh requested-provider runs were isolated under
  `desktop-v2/build/validation/20261009/model-218b27a-01/` through `-07/`.
  None is a model-closure pass: no run produced the required type/send/reply
  sequence and no reply screenshot is accepted.
- Runs `-03` and `-06` reached `launch_app` successfully, but the native
  diagnostic recorded `observe_result error_code=focus_changed`. The external
  AX/global-state keepalive was observed to move focus away from WorkBuddy; those
  runs are retained as harness-interference diagnostics, not product passes.
- Run `-04` reached the operational deadline while the second observe was being
  dispatched; the conversation result was `context canceled`. Run `-05` stopped
  after launch/observe without a type action. Run `-01` and `-02` show graceful
  host shutdown after the first provider turn. Run `-07` again ended with host
  connection refusal before a verifiable action trail; native launch inventory
  showed periods with no frontmost WorkBuddy window.
- During the final native inspection, the computer-use surface reported that the
  Mac was locked and automatic unlock could not unlock it. This is an external
  desktop-state blocker, not evidence to weaken focus/window safety or replay an
  action. The user must unlock the Mac manually before the final fresh run.
- Remaining P0 is unchanged: after manual unlock, cold-start the attested
  `218b27a` app, verify a fresh WorkBuddy `launch_app → observe → click →
  command+a → type 1+1=2 → send once → wait reply screenshot → stop` run within
  the existing 120-second/110-second contract. Do not count any of `-01`…`-07`
  as a pass and do not reuse their evidence or replay their actions.

### Unlock-cleared model closure attempts — 2026-10-10

- Manual unlock cleared the earlier desktop-lock blocker. The current tagged
  build was rebuilt from `9f15d35`, then the wrapper was fixed in commit
  `56b8352` to ignore zero-value native receipt placeholders from failed tool
  results; wrapper tests increased to 33/33 and the fix was pushed.
- Fresh run `model-9f15d35-01` is the closest real model result so far. The
  requested provider/model/high session successfully executed, with verified
  native receipts and same target window identity for:
  `launch_app → observe → click → command+a → type(1+1=2) → click(send) → wait`.
  WorkBuddy visibly rendered the real `1+1=2` reply. The final `wait` result,
  however, returned `action_failed` with `observation_image_failed` and a
  zero-value receipt placeholder; the wrapper's previous parser incorrectly
  classified that placeholder as a native receipt and cancelled at the
  operational boundary before a model Stop. This is retained as near-pass, not
  as a completed acceptance.
- Runs `model-56b8352-01` through `-03` did not produce a complete closure:
  provider timing/no-action and focus timing prevented the required type/send/
  reply/Stop sequence. No failed run is promoted to a pass.
- The remaining P0 gate is now narrowly bounded to making the model obtain one
  valid post-send reply observation and issue the final ComputerUse Stop within
  the existing 120-second/110-second contract. Do not replay the already
  executed input or send action; use a fresh cold run and fresh evidence.

### Phase update — passive wait completion is not input posting — 2026-10-13

Fresh `model-ca023a7-final-01` on the requested provider/gpt-6-sol/high route
issued observe (implicit atomic launch), click, command+a, type, one send click,
one wait, and model Stop. Native request-24 returned executed/not_started with
no error after ~7.2s; the bridge substituted input_uncertain/unknown because the
atomic contract requires executed/complete. This is a deterministic protocol
mismatch, not evidence of launch failure or uncertain keyboard/mouse dispatch.
The saved send image shows preparing execution, not a reply; no reply pass.

The native fix tracks passive action completion separately from input posting:
only successful completion of the cancellable timer acknowledges complete;
Pause/Stop/deadline before completion stays rejected/not_started. After-image
failure retains completed wait while rejecting inconsistent image evidence.
Tests first failed on executed/not_started and now cover success, after-capture
focus loss, screenshot failure, consumed observation, and Stop interruption.
Contract validators and focus/window/permission guards are unchanged.

Change ledger: task source is Engine.swift and native fake tests plus these two
ledgers; concurrently-created community/docs files are external work, excluded.
Further model attempts are deferred until production request/guidance and runtime
lifecycle audits plus current-build deterministic gates are closed.

### Phase update — production request and atomic launch guidance — 2026-10-13

The tool description and trusted system guidance still told the model to prefer
first observe and use launch_app only as a fallback. That contradicted the
acceptance prompt's explicit first-launch requirement. Failing guidance tests
now require first-call atomic launch for a closed target, same-turn observation
and screenshot, and lazy trusted session binding; already-running targets may
still use observe. Safety, permission, no-replay and target registry rules remain.

The 1428-rune model prompt is now a shared non-secret acceptance asset, read by
both the Python harness and Go classifier/production-wire tests. Its SHA-256 is
unchanged (1a680db5a4a6dbf4947ae3d282339ad544cba0f2dacfbba71a030d5182a447a8);
no prompt compression or acceptance-condition relaxation was used. A localhost
Responses HTTP test exercises automatic fast-path selection, gpt-6-sol/high,
exact max_output_tokens=2048, only ComputerUse advertised, no workspace context,
latest-only PNG over three observations, and preservation of all tool-result
protocol records. It does not invoke an external model or native input and does
not prove that the remaining real-run token growth is old images.

### Phase update — helper lifetime and explicit authorization boundaries — 2026-10-13

Failing desktop regressions exposed two surviving UI lifetime entry paths:
capabilities-first created a helper with the window context, and approved
conversation UI start passed that same context as helper lifetime. Backend
creation now always uses the manager's supervisor-owned runtime context;
caller cancellation still denies approval and does not confer authority.
RuntimeSupervisor Stop performs bounded independent Stop/drain before cancelling
helper lifetime; app shutdown now drains before disconnecting the bridge.

A separate failing query regression showed auto fast-path bare context skipped
a supplied UserPromptSubmit authorization hook. The runtime policy now retains
explicit hooks while still disabling workspace/skills discovery. PreToolUse deny
was already honored and is pinned by another non-regression test. Permission,
focus, window and screenshot safety guards are not relaxed.

These are deterministic short-term P0-E boundary fixes, not proof of survival
across an OS process exit or full headless-runtime extraction. Active native
panel/window-close acceptance and that process architecture remain open. No new
model attempt was made after the known wait failure.

### Phase update — no-replay activation and launch ambiguity — 2026-10-13

Audit of the earlier bounded activation retry found it treated every false Bool
as a safe pre-dispatch failure, although NSRunningApplication.activate or AXRaise
could already have run. A failing native regression proved ambiguous activation
was attempted three times. Activation now returns existing structured metadata
with actionMayHaveRun; only read-only pre-dispatch failures may retry. Launch
polling also never repeats a dispatched raise, and ambiguous raises fail closed.
The original NativeWindow identity is passed through activation and rechecked
between attempts; a second failing regression covered a recycled ID/owner change.

Further failing Swift/Go regressions showed native input_uncertain and launch
lost ACK were downgraded to rejected/launch_failed. Native launch now retains
unknown dispatch; the Go backend preserves unknown receipt/completion time and
quarantines the helper, so a second launch cannot dispatch. Known pre-dispatch
rejection and the existing controlled launch-timeout fixture remain unchanged.
These are safety hardening, not attribution of the previous run's wait failure
to an AX issue. Focused Go/race, 303 native safety assertions and 469 platform
assertions pass; no new model retry was performed.

### Phase update — immutable duplicate receipts and atomic native observer — 2026-10-13

A failing duplicate-turn regression found that a second call with the same
consumed action ID returned rejected/not_started and RecordTurnResult overwrote
the original executed/unknown receipt. The session now stores immutable consumed
turn metadata (bounded by the action budget, excluding PNG bytes); duplicate
reads return the original fact and cannot change current authority. A canceled
in-flight duplicate returns a bound unknown receipt, without canceling/replaying
the original. Tests cover executed, unknown, older-turn, and concurrent cases.

The tagged acceptance observer now exposes execute_turn through the same shared
Controller, transferring only the committed result's same-turn PNG and expiry.
It does not call legacy execute/image or add posthoc observe. A failing tagged
regression pins receipt/observation/PNG consistency and duplicate rejection.
This enables a no-model passive-wait native smoke before any further model gate.
Focused Go, tagged desktop/race, native safety and Python acceptance tests pass.

### Phase update — application-open dispatch precedes window binding — 2026-10-13

A launch callback timeout or AX/window binding failure can occur after AppKit
already opened or activated the application. Platform launch now marks that
side-effect boundary and propagates a structured LaunchOperationError with its
original public error. Engine returns unknown/unknown and ends authority for
these late failures; errors before dispatch still reject. The Go launch adapter
preserves the origin code (for example launch_timeout) while retaining unknown
outcome and preventing a second dispatch. Native fake regression distinguishes
pre-dispatch controlled timeout from post-dispatch timeout, and existing
unavailable-AX coverage now correctly checks unknown rather than not_started.
This is contract hardening, not a newly repeated model acceptance.

### Phase update — window close is a UI client operation — 2026-10-13

The Wails default still quit the entire signed host when its main window was
closed, despite independent helper contexts. A failing options regression now
requires HideWindowOnClose. Window close therefore hides the client; explicit
app Quit still invokes the existing bounded runtime Stop/drain and bridge close.
No focus/window/permission/screenshot guard is changed. The remaining native
gate is an active-turn window-close/passive-wait/screenshot/Stop run on the
fresh official tagged build; this is not another model input/send attempt.
