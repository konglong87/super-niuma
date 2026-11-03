# Team Workflow V2 — final macOS native acceptance

Date: 2026-10-29. Scope: the **approved text-only/no-tool MVP** in
`docs/plans/2026-10-29-multi-agent-orchestration.md`. This is not acceptance of
approval/pause-resume, Computer/Image execution, distributed workers, arbitrary
condition/loops, Apple notarization or a real paid model.

## Build identity and isolation

- Built the final `.app` with `COMPUTER_USE_RELEASE_CANDIDATE=1
  bash scripts/build-desktop-v2.sh` and Go 1.26 `GOTOOLCHAIN=local`.
- Source commit: `f95150926a368844566f332b269d81be0b78bb59`.
  Manifest: `desktop-v2/build/bin/niuma.app/Contents/Resources/computer-use-build.json`.
  `source_dirty=false`, `web_source_dirty=false`, `release_candidate=true`;
  `codesign --verify --deep --strict` passed (local ad-hoc, not notarized).
- Launched the **final rebuilt** `niuma-desktop` executable, not a prior PID,
  with isolated `NIUMA_DESKTOP_CONFIG_DIR`, `NIUMA_CONFIG_DIR`, home, workspace
  and SQLite. Settings contained only a dummy key and literal 127.0.0.1 model
  endpoint. Fixture neither forwards traffic nor records raw prompts/Auth.
- Used the Computer Use interface for **real Wails/macOS clicks and screenshots**.
  No AppleScript/OS keyboard automation, browser fixture substituted for native
  acceptance, or real model call. Captures were decoded from the native tool's
  JPEG screenshot bytes and re-encoded as valid PNG with `sips` (verified PNG
  header/dimensions). The PNG files and `evidence-manifest.json` remain in
  `desktop-v2/build/validation/20261029/multi-agent-orchestration/` and are
  ignored by Git as requested local build/validation evidence. The manifest
  records every screenshot's SHA-256, dimensions and final source commit.
- The acceptance session and offline fixture were stopped; isolated temporary
  config, dummy credentials, SQLite, logs and workspace were removed afterward.
  Existing screen/channel and Computer Use processes were not signaled.

## Actual native actions and observed outcomes

| Action | Observed result | Native evidence |
|---|---|---|
| Open Team V2 at minimum 1024×700 | Template and Team cards legible; no horizontal overflow | `01-final-workbench-1024x700.png` |
| Create and publish a personal text Agent | Published/no-tool card visible, including main or explicit model route | `02-final-agent-published.png`, `11-final-created-agent-published.png` |
| Create Team/edit static dependencies and run | Final-build Agent and Team saved from UI; run produced an actual SQLite artifact, no model charge | `12-final-created-workflow.png`, `13-final-created-run-completed.png` |
| Choose Agent's independent model | Existing main configuration can be explicitly pinned to `slow`; another Agent retains inherited `fast`; both routes observed in actual native execution | `05-distinct-model-completed-after-recovery.png` |
| Run parallel builtin | Planner, two distinct Researchers, Reviewer all completed; fixture measured two overlapping requests, Reviewer received two declared dependency artifacts | `06-parallel-review-two-artifacts.png` |
| Exhaust provider retries and retry node | Attempt 1 failed; attempt 2 succeeded; unknown usage has a separate conservative reservation, never shown as free/zero | `08-failed-then-retried-unknown-usage.png` |
| Fail all attempts | Run `failed`, downstream `blocked`, no fabricated output/artifact | `09-terminal-failed-run.png`, `10-blocked-downstream-no-artifact.png` |
| Cancel actual slow SSE request | UI emitted `run_cancelled`, persisted terminal `cancelled`; fixture reported disconnected, 0 in-flight requests, ~1.45s elapsed | `14-final-active-run-before-cancel.png`, `15-final-cancelled-run.png` |
| Quit/restart Wails during an active Run | Original attempt persisted as `interrupted`; after restart UI showed an explicit recovery control. Click recovered the same Run (new epoch/attempt), then persisted `completed`. | `16-running-before-restart.png`, `17-interrupted-after-restart.png`, `18-recovered-completed-final-build.png`, `19-interrupted-and-recovered-attempts.png` |
| Save Team v2, read old v1 history | v2 editor displays “运行记录 · 全部版本”, old Run labels retain v1; clicking the old entry opens its unchanged snapshot and earlier recovery events | `03-v2-shows-v1-run-history.png`, `04-restarted-run-v1-final-build.png` |

The first six native PNGs are 1024×700; later captures are 1230×768 after
macOS resized the same final-build window. We visually inspected representative
minimum-size editor/list, parallel result, failure/cancel, and interrupted/
recovered views. Locale/theme/responsive browser validation is separate and
is **not** represented as native evidence.

## Automated gates on the delivered source

- `go test ./...` with the cached local Go 1.26 toolchain: PASS (root suite).
- `go test -race` on workflow, workflowapp, runtimecompose, storage/mysql,
  server, CLI, tenant and observability: **all eight packages PASS**.
- `cd desktop-v2 && go test ./...`: PASS.
- `npm --prefix web run typecheck`: PASS.
- `cd web && vitest run`: **90 files, 881 tests PASS**.
- `cd web && playwright test e2e/workflows-v2.spec.ts
  --project=chromium-webui-v2-desktop --workers=1`: **5/5 PASS** at 360 en/light,
  768 zh/dark, 1024 en/light and 1440 zh/dark plus interaction case. Real
  rendered browser images were inspected, then deleted as intermediate output.
- `python3 scripts/run-workflow-integrated.py --binary
  desktop-v2/build/bin/niuma.app/Contents/MacOS/niuma` (absolute path supplied):
  **5/5 PASS** against the binary embedded in the final `.app`, isolated real
  SQLite + HTTP/SSE frontend and loopback fake provider; six Runs persisted,
  final statuses completed=4, failed=1, cancelled=1; peak provider concurrency=2.
  It also verifies the old v1 Run is still clickable from newly saved v2.
- Offline fixture tests 5/5, runner loopback/dirty-DB gate tests 3/3, Swagger
  route coverage and `git diff --check`: PASS.

## Explicit remaining risk / rollback

- Real MySQL 000029 migration/live concurrency was not exercised: no nonproduction
  MySQL instance was available. SQLite was exercised both by automated tests and
  the native UI. Migration is explicit, not automatic; if its read-only schema
  probe is false, legacy server remains available without Workflow V2.
- Ad-hoc signed `.app` is not notarized or formally distributed. No real paid
  provider latency/rate/cost acceptance was attempted. An uncooperative external
  executor cannot be forcibly killed; slots remain reserved and stop reports a
  bounded timeout rather than claiming successful cleanup.
- `NIUMA_WORKFLOW_READ_ONLY=1` prevents new execution and writes while preserving
  historical readback. Stop writers and back up before a MySQL rollback; its
  down migration refuses to erase existing V2 Runs.
- Approved P2/P3 deferrals: durable approval/checkpoint pause-resume; desktop/
  image executor; channel V2 dispatch, distributed worker, loops/conditions and
  automatic Agent generation. Existing single Agent, model settings, pets,
  Computer Use and screen-launched channel Profile workers were preserved.
