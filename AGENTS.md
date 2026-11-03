# Repository Delivery Rules

These rules apply to every task in this repository.

## Change Tracking

1. Before editing, run `git status --short` and record the existing dirty files.
2. Classify every dirty file as:
   - pre-existing user work,
   - work created in the current task,
   - generated or temporary output.
3. Never overwrite, revert, or silently include pre-existing user work.
4. Maintain a small change ledger during the task: intended files, completed files, tests, commit, and push status.
5. After each coherent implementation slice, run `git status --short` and `git diff --stat`. Do not wait until the end to discover leftover files.

## Commit And Push Gate

1. A task is not complete while task-owned source changes are uncommitted.
2. Commit each coherent slice as soon as its focused verification passes. Use:
   `konglong <konglong@com>`
3. Push every completed slice to `origin/main` unless the user explicitly says not to push.
4. Before the final response, verify all of:
   - `git diff --check`
   - `git status --short`
   - `git log -1 --oneline --decorate`
   - `git rev-parse HEAD`
   - `git rev-parse origin/main`
5. Do not say "complete", "fixed", or "verified" if task-owned changes remain uncommitted or if `HEAD` differs from `origin/main`. Report the exact blocker instead.
6. If a push fails, keep the commit, retry when reasonable, and explicitly report the commit hash and push failure.

## Resume And Handoff

1. At the start of every resumed session, inspect `git status --short`, recent commits, and the current diff before doing new work.
2. If unfinished task-owned changes are found, finish, verify, commit, and push them before starting unrelated work.
3. Do not abandon a completed feature because the conversation moved to another topic. The delivery gate still applies.

## Verification

1. Match verification to the user-visible surface:
   - backend changes: focused Go tests, then broader tests when the blast radius requires it;
   - frontend changes: focused UI tests and typecheck;
   - desktop changes: build the Wails `.app` and test the real desktop window;
   - desktop UI acceptance: use actual clicks and native screenshots, not only DOM or browser tests.
2. Save requested desktop evidence under the repository, normally:
   `desktop-v2/build/validation/YYYYMMDD/`
3. Do not store secrets, databases, logs, or real credentials in Git. Clean up unrequested temporary artifacts after verification.
4. Every final report must list the tests run, screenshot paths when applicable, commit hash, push result, and remaining risks.

## Scope And Safety

1. Keep commits small and related. Do not mix unrelated refactors into a feature fix.
2. Prefer existing project patterns and abstractions. Do not add compatibility code only to preserve an unreleased structure unless requested.
3. Never use destructive Git commands such as `git reset --hard` or `git checkout --` to clean up work.
4. Do not create a branch or worktree without user approval.

## Phase Closure And Priority Order

1. After every phase, review all unfinished requirements before starting the next phase.
2. Update the task's implementation ledger with remaining items in descending priority, dependencies, acceptance gates, and explicit deferrals. Distinguish implementation, automated tests, native scripted acceptance, and autonomous model acceptance.
3. Finish focused verification, commit and push the coherent slice, then execute the highest-priority unblocked item. If an item is blocked, record the concrete blocker before proceeding to the next unblocked item; do not silently drop it.
4. For the current Computer Use work, the authoritative remaining queue is at the top of `docs/plans/2026-09-27-computer-use-product-functions.md`. Physical second-display acceptance and formal signing/distribution remain explicitly deferred by the user.
