# Workflow frontend implementation ledger

Scope: shared workspace, no branch/worktree, no commit/push, no go-e2e, no real model calls.
Pre-existing work: internal/agentprofile/{model.go,schema.go,types.go}, internal/agentteam/workflow/** (other agents; do not edit).

Architecture: independent WorkflowClient port → authenticated HTTP/SSE adapter → pure snapshot/event reducer → route-specific workbench. Profile operations integrate the shared workflowAgentProfiles helper, not the settings panel. Chat subscriptions stay in the shell; workspace and inspector render only on their own route. Workflow dirty guard is independent of settings.

Priority / acceptance:
1. DTO, transport, sequence-safe reducer and contract tests.
2. Agent creation/list, static-DAG editor, built-in templates, real start/run/readback/cancel/recover UI; injected client rendering tests.
3. Sidebar/routes/shell exclusivity, independent leave guard and route tests.
4. Typecheck/focused tests, browser 360/768 and 1024×700 screenshot acceptance without model calls.
Native build/window acceptance depends on coordinating the shared desktop build; do not claim browser evidence is native evidence.

Files: workflows/**; routes.ts/tests; WebUIV2App.tsx; SessionSidebar.tsx; playwright config workflow-only spec match. Additional workflow-only E2E spec if needed.
Progress: frontend implementation, automated verification and mock-backed browser acceptance recorded below; backend/native integration gates remain explicit. Commit/push deliberately disabled by this task.

## Implementation / acceptance ledger (2026-10-29)

- Implemented: standalone JSON DTOs, authenticated HTTP + header-bearing fetch/SSE, strict LF/CRLF event framing; pure reducer with run ownership, sequence gap buffering/deduplication, node/attempt isolation, snapshot authority and epoch-aware delta preservation. Includes final `run_cancelled` separately from cancellation request.
- Implemented: workflow list and server-created `research-review` / `parallel-review` templates; immutable save-new-version editor; static dependency checkboxes, sequential/parallel/custom DAG presets, add/remove steps, cycle checks and advanced budget/retry fields; real run input, idempotent start retries, history, steps/attempts/errors/usage/artifacts/timeline, cancel/recover with revision, reconnect plus periodic persisted readback.
- Implemented: private no-tool V2 text Agent draft creation/list/edit/publish/archive through existing Profile APIs and shared workflowAgentProfiles helper. Catalog ID=0 and capability-granted profiles cannot be selected as bindings. Shared/catalog profiles are copied to a new personal identity rather than patched.
- Contract update implemented: Binding.profile_id is optional for historical data; selectors use unique database IDs. Saves send profile_id + profile_key + profile_version. Legacy exact key/version resolution prefers private over shared; explicit IDs never fall back to another identity. New scope-collision unit/render tests exercise this.
- Implemented: sidebar entry, deep links, workflow/chat/Inspector exclusion; selected chat subscriptions remain owned by the shell. Existing hidden settings/chat draft behavior remains intact. Independent workflow dirty confirmation, beforeunload protection, focus restoration, and ignored stale async callbacks.
- Automated verification: `npm run typecheck` passed; focused Biome lint (26 files) passed; focused Vitest + existing shell/sidebar regressions passed (10 files, 99 tests). No model calls and no go-e2e changes/runs.
- Browser scripted acceptance: `npx playwright test e2e/workflows-v2.spec.ts --project=chromium-webui-v2-desktop --workers=1` passed (5 scenarios). The suite sets 360×800, 768×900, 1024×700, 1440×960 explicitly, exercises English/light + Chinese/dark, and checks horizontal overflow, exact API request bodies, creation/publish, immutable save/start, independent leave guard, recover/cancel and final timeline. HTTP is intercepted ONLY by test fixtures; production has no mock switch.
- Browser screenshot evidence captured (21 images; redundant mock images removed by main after visual/integration review): `desktop-v2/build/validation/20261029/workflow-browser-mock-*.png`. Lists, editors, step cards, run summaries, artifacts/timeline, and Agent list are included. Representative 360-light, 768-dark and 1024×700 images were opened and visually inspected. This is mock-backed BROWSER evidence, not native evidence.

## Remaining queue / explicit deferrals

1. Main-agent integration acceptance against the assembled backend and latest optional profile_id handling. Frontend transport covers all declared endpoints; this subtask did not start real-model runs. Dependency: coordinated backend integration fixture/server.
2. Actual rebuilt Wails window clicks/screenshots at minimum 1024×700. Dependency: main-agent coordinated desktop build and safe model-free acceptance environment. Browser 1024×700 was tested, but must not be called native acceptance.
3. Autonomous real-model acceptance remains deliberately excluded by user. Physical second display and formal distribution/signing remain outside this subtask.

Commit/push: NOT performed, as explicitly requested. No branch or worktree created. Shared HEAD may move due to other agents; no unrelated dirty files were edited or staged by this subtask. Test logs in /tmp are removed after reporting; requested screenshot evidence remains under the repository build validation directory.

## Final handoff update

The shared native acceptance completed on the final signed Wails build. After
visual inspection, mock and real-backend browser PNGs were removed as temporary
intermediates; only genuine Wails native PNGs remain in the requested
`desktop-v2/build/validation/20261029/multi-agent-orchestration/` directory.
The version-history fix is documented in the native acceptance report; the
editor now reads all Team versions but starts new Runs only on the latest.
