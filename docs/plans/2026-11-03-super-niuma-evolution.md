# Super Niuma architecture evolution and delivery ledger

## Desktop entry correction — 2026-11-03

User-visible requirement: Desktop is the single primary application. Add **超级牛马干活** immediately beside/below **新建会话** in the upper-left sidebar; open a native React workspace in the existing window. Business isolation must not create a second user application. Port 61585 was a disposable QA listener, not a permanent product entry.

Architecture: Desktop composition → optional Super Wails bridge → existing workorder.Service → dedicated sqlitestore. No iframe, extra HTTP listener, new Session, Session schema migration, or provider/model invocation. Lazy store initialization and bounded, serialized bridge calls; close on app shutdown. Existing Session callbacks and state remain authoritative. Super pane navigation is shell-only and preserves the selected conversation and its drafts. The standalone binary remains a developer/headless diagnostic surface.

### Active delivery ledger (priority order)

Initial state: clean main at 4971f40; origin/main pulled and current. No pre-existing dirty files. All following changes are task-owned; generated assets, isolated acceptance data and screenshots remain ignored.

| Priority | Slice / intended files | Acceptance gate | Status |
| --- | --- | --- | --- |
| D1 | Dedicated desktop-v2 Super bridge + tests; main.go composition/lifecycle only; shared Super state-location helper if required | create/list/get/cancel, idempotency, sanitized failures, lazy lifecycle, concurrent shutdown, isolated persistence; desktop Go tests | Implemented; focused tests, race stress and vet pass |
| D2 | web/src/v2/super workspace/client/styles/tests; sidebar and WebUIV2App composition only | native pane, no HTTP/window-open, original session navigation/create unchanged, stale async protection, focused tests + typecheck | Implemented; full frontend 896 tests + typecheck pass |
| D3 | Explicit reviewed composition-only baseline exceptions; architecture/usage docs | old Session/runtime baseline still protected; guard self-tests; no blanket baseline rewrite | Reviewed four exact composition files; 6 guard tests pass |
| D4 | Build real Wails app; isolated native click/create/read/cancel/restart/back-to-session acceptance; screenshots | real native screenshots under desktop-v2/build/validation/20261103; no changes to user's running app/data | Passed: real native create/retry/restart/cancel, original session creation and draft-preserving mode switches |
| D5 | Verify diff, commit/push each coherent slice, cleanup and reconcile ledger | task-owned source committed; HEAD == origin/main | First slice 25fa7df pushed; final acceptance commit closes this gate |

Automatic execution, worker pool, model routing, automated acceptance and delivery remain explicitly deferred to the existing P2+ queue after this Desktop correction. Native scripted acceptance is not autonomous model acceptance.

## Current ordered queue

The public successor is an independent source snapshot, not a GitHub fork or a private-source dependency. The original niuma source repository remains unchanged by this task. Final GitHub inspection on 2026-11-03 found it private; this agent did not change its visibility, and will not reopen it. No existing branch or worktree is created. The initial main branch belongs to the new repository.

| Priority | Slice | Implementation | Automated acceptance | UI/native acceptance | Model acceptance | Delivery |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | Independent source baseline and architecture | Implemented | 12 baseline packages passed; 1,383 legacy file hashes passed; source publication review passed | Existing desktop verification remains a migration gate | Not applicable | Committed/pushed 5f6c1a5 |
| P1 | Dedicated intake-only Super entry and durable work orders | Implemented | Fresh race/vet; 18 binary HTTP checks; full Go suite; legacy isolation guard passed | Real built-in-browser intake, refresh, restart, cancel and final screenshot passed; no new native desktop acceptance claimed | Not applicable: no model is connected | Delivered in this feature commit; remote checked at handoff |
| P2 | Controlled execution adapter and worker ownership | Planned, depends on P1 | budget, cancellation, lease loss/fencing, resource exclusion | Actual task and restart acceptance required | Bounded real-model run required | Not started |
| P3 | Independent verification and delivery receipts | Planned, depends on P2 | failed/stale/missing evidence and ambiguous side effects fail closed | Real artifact/native acceptance as applicable | Separate model assessment where useful | Not started |
| P4 | Channel adapters, observe-mode proactive admission, exception dashboard | Planned, depends on P3 | allowlists, duplicate delivery, source isolation, policy revocation | Original-channel delivery and recovery acceptance | Shadow evaluation before autonomous mode | Not started |
| P5 | Public release migration and visibility reconciliation | Old repository observed private; release readiness and migration notices remain pending | independent clean-checkout build/release, link audit | Legacy desktop and Super entry acceptance | No additional autonomy claim | Preserve observed private state; do not reopen |

Physical second-display acceptance and formal signing/distribution remain explicitly deferred, as recorded in the existing Computer Use ledger. They are not silently claimed by this project migration.

## Goal and scope

Create a new Super entry that owns authorized work orders without modifying the existing Session lifecycle, storage schema, prompts or request path. Preserve the traditional entry as a first-class product mode. Deliver incremental, tested, committed slices; do not label an intake prototype an autonomous platform.

## Architecture

Traditional entry -> existing Session flow -> existing runtime/tools/providers.
Super entry -> durable intake -> admission -> WorkOrder -> scheduler -> isolated execution sessions -> verifier -> delivery/outbox.

WorkOrder is the business aggregate; Session is execution context. Neither inherits the other's state machine. Super business state has its own store and default directory (~/.super-niuma), and ordinary entry startup does not start Super services. The first slice is loopback-only and intake-only; no scheduler, model, shell execution, credentials, old session database or external channel is opened.

### Boundaries and files

- Existing cmd/niuma and internal Session/runtime behavior: unchanged except mechanical module import identity migration.
- cmd/super-niuma/main.go: separate executable and signal-aware entry.
- internal/super/app: composition, flags, lifecycle, embedded intake console; no domain rules.
- internal/super/workorder: domain validation, states, application service and Repository port.
- internal/super/sqlitestore: dedicated SQLite schema, transactional uniqueness, persistence and restricted file permissions; never invokes the legacy desktop schema migrations.
- internal/super/httpapi: bounded JSON requests, exact route matching, sanitized failures, loopback-host/origin validation and browser request marker.
- scripts/super-niuma-boundary-test.py: legacy snapshot and independent module guard.
- docs/architecture/super-niuma.md and docs/usage/super-niuma.md: architecture and truthful operational contract.

No distributed broker, public SDK or microservice split is introduced before measured need. Existing workflow leases/epochs, budgets, agent runtime and evidence mechanisms are candidates for the execution phase, not replaced with another competing engine.

## P1 contract (fixed before implementation)

Default listen: 127.0.0.1:7391. Non-loopback bind is refused. Default state: ~/.super-niuma/work-orders.db. Browser API requests use X-Super-Niuma-Request: 1. Mutating requests additionally require application/json and Idempotency-Key for creation. Request Host must match the bound listener authority; an Origin, when present, must match the same HTTP origin; cross-site browser requests are refused. No permissive CORS is installed. This is a local-user trust boundary, not remote multi-user authentication.

- GET /api/super/v1/status -> {name: "super-niuma", mode: "intake_only", execution_enabled: false, llm_routing_enabled: false}.
- GET /api/super/v1/work-orders -> {items: [WorkOrder]} (bounded latest 100).
- POST /api/super/v1/work-orders with {title, objective, acceptance_criteria: [string], workspace?: string}; returns WorkOrder, 201 for new / 200 for exact retry. Same key with different normalized content -> 409. Explicit local intake only, no inferred autonomous admission.
- GET /api/super/v1/work-orders/{id} -> WorkOrder or 404.
- POST /api/super/v1/work-orders/{id}/cancel with {} -> WorkOrder; repeat cancellation is idempotent.
- Error shape: {error: {code, message}}. Do not expose SQL errors, user content, secrets or absolute state paths in errors/logs.
- WorkOrder: id, title, objective, acceptance_criteria, workspace, status (recorded|cancelled), execution_status=not_started, verification_status=not_started, delivery_status=not_started, created_at, updated_at.

Creation and its intake audit event share a transaction. Cancellation and its event share a transaction. A failed transaction creates neither. The service logs stable actions and work-order IDs, not objective text or acceptance text. Body, field, list and concurrency bounds are constants. Workspace is metadata only and must be absolute when supplied; it grants no authority to execute.

### Go integration contract

workorder.NewService(repo Repository, logger *slog.Logger) *Service.
sqlitestore.Open(ctx context.Context, dbPath string) (*Store, error), Store.Close() error.
httpapi.NewHandler(Config{Service: service, AllowedHost: listener.Addr().String(), Logger: logger}) http.Handler.
The application mounts the handler at /api/super/v1/ and embeds its own static assets without changing the old WebUI.

## Verification and publication gates

1. Record initial dirty files (none in niuma at baseline 6b021afb71de9b6b43fa8de0e35e62638df75581). Imported snapshot and every new file are task-owned; ignored local data is excluded.
2. Red/green focused tests for store/service/API; fresh race tests for persistence and cancellation; go vet.
3. Verify every legacy .go file matches the source snapshot after reversing only the module import rewrite. Verify the old entry has no dependency on internal/super. Go unit tests and module build prove the mechanically renamed baseline remains buildable.
4. Start the built Super binary with a fresh isolated state directory; use actual in-app-browser clicks to submit, inspect, cancel and restart/reload; inspect real screenshots. Tests also prove duplicate requests, content conflicts, cross-origin/host rejection, body limits and unknown routes.
5. Retain only requested/necessary redacted screenshots under ignored validation output; remove throwaway databases/logs/binaries when acceptance finishes. Never commit these data.
6. Audit tracked source before publication. Keep Apache-2.0 license and upstream attribution. No dependency on private niuma or developer machine paths in build inputs.
7. Commit each coherent verified slice as konglong <konglong@com>, push main, check git diff --check, clean status and HEAD == origin/main.
8. The original plan deferred a visibility change until release/migration readiness. Final inspection instead found niuma already private, without a visibility-changing tool call by this agent. Preserve that observed state; do not make it public. Existing Session UI/native and public download migration remain unfinished gates.

## Non-goals of this slice

Automatic execution, automatic approval, worker pool scaling, model-based routing, channel authentication, automatic acceptance and delivery are NOT implemented by an intake console. No fake completed status, synthetic evidence or model cost claim is allowed.

## Baseline slice verification — 2026-11-03

- Original repository was clean and pulled before work; baseline remains 6b021afb71de9b6b43fa8de0e35e62638df75581.
- GitHub public repository konglong87/super-niuma was created. The old repository was not modified by this task; final inspection found it private (initial visibility had not been verified).
- Twelve legacy packages passed (CLI, server, query, runtime, AgentTeam/workflow, channel, Goal, capability loop). The first five were newly tested under the new module.
- 1,383 legacy Go/frontend files match their baseline after reversing only Go module import identity. No new legacy-session code was added.
- Source privacy/attribution review excludes 45 uncleared historical operational screenshots; licenses and immediate/transitive provenance remain.
- Two inherited public-documentation checks failed before repair: a stale assertion rejected the still-valid cmd/niuma entry, and the English README lacked the current Computer Use scope sentence. The guard now validates actual entry files and the scope is explicit; seven public-doc tests pass. No Session/runtime source was changed to repair documentation.
- P0 commit/push follows these checks. P1 remains separate and must pass its own API/race/browser acceptance before being described as delivered.

- Initial root-snapshot diff check exposed inherited whitespace in historical Markdown and five frontend EOFs. Only whitespace-only documentation lines and empty EOF lines were normalized; the legacy guard records EOF normalization explicitly. No frontend statements changed.
- First HTTPS push lacked a credential helper. Publication retries use the already-authenticated gh helper for this command only; no credentials are written to the repository or shell text.

## P1 slice closure — 2026-11-03

- Dedicated cmd/super-niuma, local intake UI, WorkOrder/Repository boundary, private SQLite, exact-path HTTP API and independent state are implemented. No legacy Session logic, prompts, storage schema or entry request path changed.
- Fresh race tests and vet passed for all Super packages. The compiled-binary black-box smoke passes 18 checks, including same-key retry, conflicts, cancellation/retry preservation, restart durability and host/origin/body/path refusal.
- Real in-app-browser clicks created a synthetic work order, verified it after refresh and process restart, cancelled it through the confirmation UI, and verified the cancelled state after another restart on the final binary. Four real screenshots and a sanitized receipt are retained under ignored validation/20261103/super-intake/. Console error/warning collection was empty.
- Integration found and fixed ServeMux path-normalization redirects and a plain-text outer Host refusal on API routes. Exact API paths now reach the API unchanged, and API Host refusals retain the structured JSON error contract.
- The initial local broad suite hit macOS Unix-socket path length limits; a /tmp copy was correctly refused by existing bridge ancestor-trust rules. No safety rule was relaxed. The suite passed from an owned short private directory under the user's home. An initial PTY spacing failure did not reproduce in the original repository or the final full run; the TUI implementation was not changed.
- The P0 remote CI, Linux desktop build and Windows desktop build passed. The P1 push adds the binary HTTP smoke to CI; remote completion is checked separately after push.
- Real-model acceptance, automatic execution, independent task verification, external delivery, proactive sources and old-repository visibility migration remain unimplemented/planned; this slice is intake-only, not autonomous delivery.

### Next ordered work

1. P2: define and implement an ExecutionAdapter on the existing runtime with a newly isolated execution Session, explicit authority/budget snapshots, resource ownership and cancellation; do not route old conversation requests through it.
2. P2 safety gate: expired owner/epoch cannot commit results, same-resource writers serialize, manual Stop stays responsive, and ambiguous side effects do not replay.
3. P3: bind independent verifier evidence to the exact work-order/attempt/artifact version; only verified outcomes may enter delivery, and delivery needs its own receipt.
4. P4: external sources and observe-mode admission after the controlled execution/verification path is accepted.
5. P5: finish release/migration readiness and public entry/link migration. niuma is already observed private; do not change or reopen it. Old native desktop acceptance remains an explicit gate. Physical second display and formal signing remain the user's existing deferrals.

## Visibility reconciliation — final audit, 2026-11-03

GitHub reports super-niuma public and niuma private. No tool call in this task changed niuma visibility. Earlier references to it remaining public were an unverified assumption and are corrected here. The actor/time of the private transition are not established. Do not undo the observed state. Source independence is verified, but old native UI and release/link migration gates remain open.

## Desktop implementation verification (first coherent slice)

- Backend: isolated bridge tests, race tests, 20 iterations of concurrent lifecycle tests, and go vet passed. Normal startup does not initialize Super persistence.
- Frontend: full suite 92 files / 895 tests passed; final focused Super suite 10 tests and typecheck passed. Existing sidebar, route shell and workflow suites remain green.
- Full Go suite and full Desktop/Super race suite passed from an owned short private home-directory source copy. Normal checkout path exceeds macOS Unix-domain socket length in an inherited bridge fixture; no security checks were weakened.
- Real Wails macOS app built and ad-hoc signature validated. Initial native creation succeeds; final native restart/cancellation/navigation and screenshot review remain the next gate. The first screenshot review prompted stronger form-label/active-navigation contrast.
- Generated assets, acceptance app state and logs are ignored; only source, tests and documentation are committed. CI now runs the narrow boundary guard's own regression tests.

### Native verification follow-up

Implementation slice 25fa7df was committed and pushed after focused checks. An initial native click after text paste only changed focus; the unchanged legacy New Session dialog reproduced the same first-click behavior. A fresh-state click then succeeded in both modes. No Super-specific event-handler defect was established. Remove the speculative explicit-submit workaround and retain the standard form/onSubmit pattern used by existing Desktop dialogs; keep a whitespace-only input guard and click-based unit coverage. Final rebuilt native pointer submission/cancellation passed; screenshots and a sanitized receipt are retained.

Native observations so far: work-order creation and cancellation survive restarts; creating a traditional session uses the original dialog; switching Super → conversation preserves the selected session and its unsent draft. No message/model invocation was triggered. Only synthetic isolated fixtures were used.

### Desktop correction closure

D1–D4 pass. Final frontend suite: **92 files / 896 tests**, plus typecheck. Full Go: **125 tested packages**; full Desktop/Super race suite passes. Six boundary self-tests and seven public-doc tests pass. Native receipt and four actual screenshots live under `desktop-v2/build/validation/20261103/`; see `docs/deployment/2026-11-03-super-niuma-desktop-acceptance.md`. Test app, eight owned runtime/helper processes, private fixture databases, raw logs and disposable source copy are cleaned. Original niuma is unchanged and clean. This acceptance slice closes D5 through the required commit/push and HEAD/origin verification.

Next highest-priority unimplemented work remains **P2 controlled Worker/execution**, after this user-requested Desktop correction. P3 independent acceptance/delivery depends on P2; P4 channel admission depends on P3; P5 release migration remains separate. No worker/automatic delivery or autonomous-model acceptance is claimed here.
