# Desktop V2 multi-Agent delivery ledger

Date: 2026-10-29. User approved implementation after architecture review and
clarification that Profile/Team orchestration integrates with, rather than
replaces, screen-managed channel workers. Baseline main/origin/main: 10409af.
Initial worktree: clean; no pre-existing user changes. No branches/worktrees.
Reference `../go-e2e` is out of the write scope.

## Ordered delivery queue

| Priority | Slice | Status / dependency | Acceptance |
|---|---|---|---|
| P1a.1 | Pure Team V2 execution contracts and static DAG compiler | Committed/pushed d2f3140 + bc81e9d | Canonical snapshot, bounds, cycle/dependency/authority checks PASS |
| P1a.2 | Profile V2/role registry and persistent SQLite/MySQL run/task journal | Committed/pushed 72c9860 + 6ad4337 | SQLite reopen/CAS/idempotency/task ownership/race PASS; real MySQL deferred |
| P1b | Bounded scheduler, retries/deadlines, cancel and interrupted recovery | Committed/pushed bc81e9d | Focused/race tests, usage reservations, orphan/epoch fencing PASS |
| P1c | Safe text executor and authenticated workflow application/API | Committed/pushed 884dffa + b7d2cfb | Real protocol loopbacks, HTTP/SSE, production composition and full Go PASS |
| P1d | Beginner-friendly V2 Team workspace, editors, real Run details | Committed/pushed a124e9b, 7137bbe, f951509 | Typecheck, 881 UI tests, history navigation, real-backend E2E PASS |
| P1e | Final regression/build/native desktop acceptance | Complete on source f951509; final evidence closure pending commit | Signed release-candidate build, native clicks/19 final PNGs, cancel/recovery, broad regression PASS |
| P2 | Durable approval/checkpoint pause/resume | Explicitly deferred by approved plan | No fake buttons or unsupported success |
| P3 | Desktop/image executors and channel Workflow V2 dispatch | Explicitly deferred by approved plan | Preserve current screen workers/Computer Use isolation |

Implementation boundaries:
- Agent definitions reuse Profile; Workflow definitions reuse Team schema V2.
- Pure scheduler/contracts live in `internal/agentteam/workflow`, a child package
  importing only standard library. Existing parent package adapters depend on SQL;
  SQL may import this pure child but never its parent. This uses the contracts
  alternative from review to prevent an import cycle without moving V1 code.
- Workflow-owned attempts/tasks have one terminal transaction authority.
- New text adapter calls query.New directly with a bare/no-tool/empty-hook policy,
  no raw Recorder or project discovery. No CLI factory initialization side effects.
- First release supports explicit no-tool text Agents, user-supplied research
  material, static sequential/parallel/join workflows. No paid model acceptance.
- Existing model settings, pets, screen supervision and default JSONL chat remain.

## Change ownership

All changes below are current-task work, not pre-existing user edits.
- Main: pure workflow compiler/scheduler, application/API/composition, integration
  tests, delivery ledger and final verification.
- Profile worker: `internal/agentprofile`, frontend Profile parser/helpers/tests.
- Storage worker: workflow GORM models/store/migrations and SQLite migration.
- UI worker: `web/src/v2/workflows`, route/shell/sidebar wiring and tests.
- Executor worker: `internal/agentteam/runtimecompose` safe text adapter/tests.

Generated databases/logs/browser artifacts are temporary and must be removed after
acceptance. Requested native evidence is retained under
`desktop-v2/build/validation/YYYYMMDD/multi-agent-orchestration/`, normally ignored.

## Verification and commits

- P1a.1: `GOTOOLCHAIN=local ~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.darwin-arm64/bin/go test ./internal/agentteam/workflow` PASS.
- No native/browser/model acceptance claimed at this stage.
- Commit/push hashes and later gates are recorded in subsequent entries/final
  report rather than embedding a commit's own hash.

### Profile V2 slice

- Versioned role/kind metadata and extensible registry implemented; V1 canonical
  hashes preserved. Existing frontend Profile parser accepts versioned documents;
  new workflow helpers default to text/no tools/no inherited context authority.
- Main reran Profile Go tests and frontend Profile/helper tests: PASS (32 cases).
  Worker also passed Profile race tests and existing CLI/server Profile regressions.
- No new desktop UI acceptance claimed yet; only definitions/helpers in this slice.

### Integration hardening: legacy parallel event ordering

- Resume audit: main/origin/main at 72c9860, pull fast-forward-only reported up to
  date. All dirty files belong to the current approved implementation; no user
  edits were overwritten. Parallel workers retain disjoint write scopes.
- V1 parallel event sequence assignment and publication now share the same lock,
  preventing a later sequence from being persisted/published before an earlier
  event. This preserves existing screen/channel observability rather than
  replacing it with Workflow V2.
- Focused race verification: `go test -race ./internal/agentteam -run
  'TestOrchestratorPersistsMemberAndMailboxEvents|TestParallelEventPublicationFollowsAssignedSequence'
  -count=20` PASS using cached Go 1.26 with GOTOOLCHAIN=local.
- Remaining priority: finish scheduler/storage/adapter safety checks; commit these
  layers; verify real-backend UI E2E; final regression/build/native acceptance.
  P2 approval/pause and P3 desktop/image execution remain deferred, not simulated.

### Bounded scheduler slice

- Implemented static sequential/parallel/join scheduling, independent step/run
  deadlines, limited retries, cancellation, durable checkpoints and explicit
  interrupted-run recovery. Immutable epoch/lease fencing rejects stale workers.
- Cancellation/stop drains bounded partial results before freezing terminal
  state. Actual executor lifetime owns concurrency slots; uncooperative calls do
  not release slots early or permit same-process recovery. Close reports timeout
  or failed checkpoint instead of claiming successful cleanup.
- Actual provider usage and unknown admission reservations are separate fields;
  missing usage is never invented, and remains conservatively budgeted. Completed
  dependency artifacts are isolated and immutable; root dependencies serialize
  as [] for UI interoperability.
- Focused workflow tests/race/vet PASS (worker independently tested 18 safety
  groups). Full root Go suite now PASS after API coverage regeneration; desktop-v2
  Go suite PASS. Broad race regression is running before subsequent commits.
- Remaining: storage/safe text/application/UI commit gates; real-backend E2E;
  final rebuilt native acceptance. No native/model success claimed here.

### Shared persistent journal slice

- Reused agent_teams/agent_team_runs; added workflow checkpoint, immutable Task
  binding and node/attempt/artifact projections, with atomic event sequence/CAS.
  SQLite migrates locally; MySQL requires explicit 000029 migration. Read-only
  schema readiness prevents accidental writes against old/half-migrated stores.
- Queue capacity and scoped idempotency enforced transactionally; terminal Runs,
  completed Nodes and artifacts cannot be rewritten. Existing generic Task
  mutation/finish/cancel/events/session stop/reaper paths refuse owned Tasks.
- Publish timestamp binding works with SQLite and MySQL. Guarded MySQL rollback
  refuses to erase live V2 history. Do not downgrade while writers run.
- Main reran full storage plus workflow/runtimecompose/workflowapp/observability
  race tests: PASS. Full root Go suite and desktop Go tests: PASS. Worker also
  passed 37 focused storage test groups under race. Real MySQL is unavailable;
  its migration/live acceptance remains an explicit deployment risk.

### Isolated text executor slice

- New adapter reuses production query/client protocols and fallback, but supplies
  no tools, hooks, workspace discovery, memory, delegation, raw recording, global
  telemetry or Computer Use authority. Prompt dump enabled => fail closed.
- Public route fingerprints pin model/endpoint/fallback configuration; credential
  rotation is allowed. Capability/model conflicts fail before queued execution.
- Secrets are registered for sanitization; scoped logging suppression prevents
  SDK retry diagnostics from exposing credentials while normal request logging
  stays unchanged. Thinking/signatures never become artifacts or UI events.
- Three protocol loopbacks, fallback, missing/partial/invalid usage, raw logging,
  secret echoes, tool refusal and cancellation tests passed. Main race regression
  PASS; worker race+coverage PASS (runtimecompose 92.9%, observability 79.9%) and
  vet PASS. No paid provider or external service invoked.

### Application/API and compatibility slice

- Authenticated Team facade composes existing published Profile/Team versions,
  atomic template creation, fixed run snapshots, idempotent start, event history/
  SSE, cancellation and revision-checked recovery. Two beginner seeds included.
- CLI desktop-local/server composition uses existing repository and settings;
  read-only kill switch preserves history. Legacy V1 channel loaders and Team
  editors cannot execute/mutate V2; nontext Profiles cannot enter text sessions.
- HTTP contract/production composition tests use real SQLite and loopback model
  protocols, not fake workflow endpoints. Full root Go suite PASS; focused API
  and ownership rejection tests PASS; broader API race running.
- Added Swagger workflow contracts and regenerated docs/types using repository
  commands. Regeneration also synchronizes stale pre-brand package names already
  present in generated API schemas; existing handler sources are unchanged.
- Remaining P1: UI commit, real-backend browser acceptance, final clean-source
  desktop build and native clicks/screenshots; then evidence/guide closure.

### Beginner desktop V2 workspace slice

- Added Team sidebar route, Agent manager, two one-click seeds, immutable Team
  version editor with sequential/parallel/custom dependency cards, and persisted
  run detail with attempts, timeline, artifacts, usage, cancel/recover. Chat and
  Inspector are not duplicated; existing subscriptions/model settings remain.
- Agent selectors bind unique Profile IDs/versions, not ambiguous names. Unsafe
  tool-granted/nontext/catalog-only profiles cannot be silently selected. Save
  and run validation expose clear errors; dirty editor navigation is guarded.
- Unknown provider usage is labeled unknown; conservative reservations are shown
  separately from actual tokens/cost. No unsupported pause/approval buttons.
- Main typecheck and focused Vitest shell/routes/workflows: PASS (100 tests).
  Worker focused Biome: PASS (28 files). Mock browser responsive/language/theme
  suite: 5/5 PASS at 360/768/1024x700/1440.
- Real-backend integration: 5/5 PASS, genuine isolated CLI server + SQLite +
  frontend HTTP/SSE + loopback protocol fixture, no mocked workflow APIs.
  Sequential/custom/parallel join/retry/terminal failure/active cancellation
  covered. Provider: 17 local requests, peak concurrency 2; DB: 6 Runs, 12 Nodes,
  13 Attempts, 10 artifacts (4 completed, 1 failed, 1 cancelled).
- Offline fixture and integration runner deny non-loopback targets/dirty business
  DBs, isolate provider config and remove temp state. Browser images are not
  native acceptance; redundant intermediates will be removed after native QA.
- Next highest-priority item: final clean-source Wails build, actual native
  clicks and screenshots. Real paid-model and physical Computer Use remain out
  of this release's acceptance scope.

### Final committed-source regression and build checkpoint

- Source for build: a124e9b07b8d0dd9600687ac63197630d18356f2, clean worktree.
- Full root Go suite: PASS (120 tested packages, optional live services disabled).
  Full desktop-v2 Go suite: PASS. Scheduler/storage/adapter/app/observability and
  full server/CLI/tenant race suites: PASS. Swagger route coverage: PASS.
- Full frontend Vitest: 90 files / 877 tests PASS; typecheck PASS.
- Python offline provider: 5 tests PASS; integration gate: 3 tests PASS.
- Repeated integration E2E against the service binary embedded in final .app:
  5/5 PASS (15.5s), 17 local requests, peak parallel=2; 6 persisted Runs with
  completed=4, failed=1, cancelled=1. No real keys/model charges/public provider.
- Release-candidate Wails build and `codesign --verify --deep --strict` PASS.
  App: `desktop-v2/build/bin/niuma.app`; manifest source_dirty=false,
  web_source_dirty=false, release_candidate=true. Build uses local ad-hoc signing,
  not Apple notarization/distribution. Existing Swift CGWindowListCreateImage
  deprecation and Wails time.Time binding warnings do not fail the build.
- Native desktop gate is NOT passed: computer-use twice reported “The Mac is
  locked and automatic unlock could not unlock it”. User was asked to unlock.
  Final build was started only with isolated dummy model settings. No native
  workflow clicks or screenshots have been produced. Browser screenshots remain
  explicitly browser-only and cannot satisfy this gate.
- Remaining P1 priority: after unlock, run actual native create/publish Agent,
  custom Team save/start, parallel/join, retry/failure, active cancellation and
  restart/recover. Save final-app screenshots in
  `desktop-v2/build/validation/20261029/multi-agent-orchestration/`, inspect them,
  clean temporary isolated state and commit/push evidence closure.
- Source implementation is committed/pushed; the overall user goal remains
  incomplete until the native gate is genuinely performed. No autonomous paid
  model acceptance, physical second-display or formal distribution claimed.

### Locked-machine handoff cleanup

- Stopped only this attempt's two isolated Wails instances/local service children
  and the offline fixture; removed its newly created cache directory (dummy
  settings/DB/logs) and task temporary test logs. Pre-existing screen/channel/
  Computer runtime processes were not signaled or changed.
- Removed 21 redundant mock browser PNGs and current Playwright last-run output.
  Retained 8 clearly named `workflow-browser-integrated-*.png` images as supporting
  browser evidence while native acceptance remains pending; none are labeled native.
- Source is delivered in small commits to origin/main. Native acceptance still
  requires a manually unlocked Mac; no workaround or claimed screenshot success.

### Native acceptance resumed; primary-route picker correction

- User resumed with Mac unlocked. Native Wails clicks created/published a personal
  Agent, saved a custom two-step dependency workflow, completed sequential and
  four-Agent parallel/join runs, displayed retry/failed attempts and unknown usage,
  and cancelled an actually active SSE call (provider disconnected in 1.36s).
  These pre-correction screenshots are NOT the final evidence set.
- Native QA found the independent-model Provider picker empty for main-model-only
  users: existing /v1/providers deliberately catalogs named providers only. Fixed
  solely in the Workflow client by adding the reserved primary route when absent,
  without guessing a default model from stale chat identity. Localized its label;
  existing model settings and shared discovery endpoints remain untouched.
- Added 3 provider-catalog regressions (empty/named/already-primary). Enhanced real
  E2E to assert an empty named catalog, choose primary + slow, persist the model,
  and confirm actual attempts use the selected model against a fast loopback
  response. Focused Vitest: 103 PASS; typecheck PASS; Biome check 6 files PASS;
  real-backend E2E: 5/5 PASS (15.7s, 17 requests, peak parallel 2).
- One exploratory cancel run hit the existing client stream protection before
  cancellation and was correctly failed; it is not counted as cancel acceptance.
  The subsequent native active-cancel run/readback passed, including run_cancelled.
- Next: commit/push correction, rebuild clean source, replace every native PNG
  with final-build captures, verify primary-model editing plus restart/recovery,
  minimum desktop layout and final evidence cleanup/closure.

### Native-discovered historical Run navigation correction

- Saving Team v2 retained v1 checkpoint rows, but the editor called runs with
  version=v2 and thus hid all older Runs from normal UI. Fixed the editor's
  history read to request every version (existing backend version=0), display
  each Run's immutable version and open the exact Run detail; only current
  version is offered for new execution. No database changes or new endpoint.
- New focused component regression verifies old v1 result remains clickable
  on latest v2 Team. Real SQLite/HTTP/SSE integration now saves v2, confirms
  old v1 history is visible and clickable, and checks persisted team_version.
- Typecheck, focused Vitest and real-backend 5/5 E2E PASS (15.6s). This UI fix
  requires another clean-source Wails rebuild and new final native captures.

### Final native closure (supersedes earlier historical blocked checkpoints)

- User unlocked the Mac, and native validation completed against clean-source
  Wails release candidate `f95150926a368844566f332b269d81be0b78bb59`.
  See `docs/plans/2026-10-29-multi-agent-native-acceptance.md` for actions,
  screenshot names, matrix, remaining risks and build identity.
- 19 **actual Wails native** screenshots remain under
  `desktop-v2/build/validation/20261029/multi-agent-orchestration/` with a
  content-addressed local manifest. Each is a valid PNG; first six measure
  1024×700, rest 1230×768. No browser fixture is labeled native. CUA JPEG bytes
  were decoded/re-encoded to PNG via sips; screenshot content was visually read.
- Final source tests: root Go and desktop Go PASS; eight critical backend packages
  PASS under race; frontend 90 files/881 tests, typecheck and browser 5/5 PASS;
  final embedded CLI binary real-backend E2E 5/5 PASS, 5+3 fixture/gate tests.
- Native real UI additionally created/published a fresh Agent, created a Team,
  ran a new Run, cancelled another active run (provider disconnected), and on
  final build stopped/restarted Wails before explicitly recovering an interrupted
  Run. Cross-version history remains visible and clickable.
- Temporary browser screenshots, synthetic DB/settings/logs and fixture process
  were removed after final native screenshots. No real secrets or go-e2e edits.
- Nothing remains in approved P1 scope after this evidence/documentation slice
  is committed/pushed. P2/P3, live MySQL and notarization retain explicit risks/
  deferrals, not hidden TODOs in the delivered MVP.
