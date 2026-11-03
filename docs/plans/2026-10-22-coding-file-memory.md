# Coding file memory — implementation and acceptance ledger

## Scope and architecture

Base: `deec0c9`, clean `main`, pulled before editing. No pre-existing dirty files.
Only coding-mode Markdown project memory; no tenant/database changes, new branch,
worktree, vector service, or additional runtime dependency. Preserve MEMORY.md,
rooted file reads, existing budgets and Read fallback. Common path enforcement
owns write containment; memory owns bounded lexical ranking; query owns context.

## Ordered delivery queue

| Priority | Slice | Dependency | Acceptance | Status |
|---|---|---|---|---|
| P0 | Canonicalize missing writable roots safely | None | Real Write, fresh/existing symlink roots, escape denial, tool regressions/race | Committed/pushed `0c9c877` |
| P1 | Chinese short-query and English-boundary retrieval; weighted index metadata | P0 | 25 multilingual cases; ranking, invalid metadata, scope/budget/security regressions | Committed/pushed `c0154b8` |
| P1 | File write/index/cold-process and Read-fallback integration | Retrieval | Actual file contents and captured request; no tenant dependency | Verified; test-double transport only |
| P1 | Real-model lifecycle E2E | Deterministic gates, isolated configured provider | Save, cold recall, paraphrase Read, update, forget, readonly, isolation; inspect disk/request/response independently | Passed; see dated acceptance report |
| P1 | Actual coding desktop entry acceptance | Current built app and isolated workspace | Native clicks and screenshots, new session recall; do not substitute browser/mocked model evidence | Passed 2026-10-23 on `05910fc`; screenshots and receipt recorded |
| P2 | Targeted corrective iteration | Any demonstrated lifecycle failure | Reproduce first, minimal fix, rerun affected and negative cases | Index-hint ownership defect reproduced and fixed in this review; six real-model cases pass |

## Verification policy

Run offline tests before paid model calls. Keep credentials in owner-only persistent
validation directories outside the checkout, never /tmp or Git. Raw provider data
is local-only; retain requested screenshots under ignored
`desktop-v2/build/validation/20261022/`. Preserve global settings. Clean temporary
build/test data after acceptance. Native scripted acceptance and autonomous model
behavior are distinct gates. Framework leaderboard claims are not our acceptance.

## Change ledger

- Intended: `internal/tools/path.go`, focused path tests; focused memory retrieval
  module/tests; query file-memory integration tests; existing memory acceptance
  runner/tests; architecture documentation; this ledger.
- Completed: missing writable-root canonicalization; actual Write regression and escaping-child denial.
- Tests: reproduced first-write failure before fix; `go test -count=1 ./internal/tools/...` and focused `go test -race` pass (Go 1.26.6, offline dependencies).
- Commits / pushes: path slice is this commit; push immediately after committing. Each verified slice goes to origin/main.
- Explicit deferrals: embeddings/vector database/background consolidation unless
  controlled evidence establishes necessity; unrelated tenant issues; previously
  user-deferred physical second display and formal signing/distribution.

## Retrieval slice verification

The old implementation failed 12/25 lexical cases plus field-ranking coverage.
The new implementation passes the fixed corpus without claiming overall model
accuracy. Query-side integration verifies actual file writes through a fresh
symlinked config root, cold-process request context, Read fallback and readonly
bytes. Full `go test -count=1 ./...`, memory and focused query race tests, and
`go vet ./internal/memory ./internal/tools ./internal/query` pass. No new
library/model/storage dependency; body/index caps and memory budgets are unchanged.
Next: extend and run real-provider lifecycle acceptance, then build and inspect
the actual native coding interface. No native or real-model completion claimed yet.

## Real-provider harness slice

The runner now starts without a precreated memory directory, optionally uses a
symlinked config root, and adds Read fallback, activity readonly, update/cold recall,
forget/cold recall, Chinese short-query and English-word-boundary scenarios.
Fallback requires the actual Read output to contain the answer while the first
request lacks it; forgetting scans unindexed Markdown too. Six offline harness
unit tests pass. Copied provider credentials are deleted in finally. Real-provider
execution remains pending; no claim is based on harness tests alone.

## Real-provider environment correction

First launch correctly rejected inherited home-directory guidance before copying
credentials. Retried under an owner-only `/Users/Shared` validation directory.
The actual model successfully created the missing memory directory, body and
index through the symlinked configuration root. The next cold process received
HTTP 429 (`inference exceeds tpm/rpm limit`); this is not a recall verdict.
Added opt-in inter-scenario pacing to the existing runner, with an offline test.
No automatic replay of mutating scenarios, no weakened assertions and no runtime
behavior change. Copied credentials were removed after the failed run.

## Acceptance closure

- Offline: full Go suite (117 passing packages), targeted vet, focused race checks,
  seven Python gate tests. No pre-existing user changes were overwritten.
- Real provider: all 18 gates passed on deepseek-v4-pro with the signed app's
  embedded service. GLM 5.2 429 and GLM 5.1 503 runs are failures, not passes.
- Native scripted and autonomous-model gates: actual coding write/readback, app
  shutdown/restart, then a fresh session/transcript store sharing the same physical
  Markdown root. Native screenshot + first-request provenance + unchanged hashes.
- Ordinary restart carried the answer in an old task title. This confounded run
  and a database-only isolation attempt are excluded as file-only proof.
- See `docs/deployment/2026-10-22-coding-file-memory-acceptance.md` for limitations,
  artifact digests, model usage, retained screenshots and reproduction details.
- Remaining outside the bounded file-memory plan: provider availability; strict
  cross-source forgetting/task-status retention; embeddings only if justified.
- Commits/pushes: `0c9c877`, `c0154b8`, `97d7b85`, `d870233` are on origin/main.
  This acceptance report is the final documentation slice.

### Explicit follow-ups (not silently counted as implemented)

1. P1, external: default GLM service availability. Dependency: healthy provider
   capacity/channel. Acceptance: rerun all 18 cases on that provider without 429/503.
2. P1, separately scoped: strict forgetting across task status and session history.
   Dependency: an explicit retention/context-source policy. Acceptance: normal new
   desktop sessions cannot obtain a forgotten fact from a task title or transcript.
   The current gate proves file lifecycle only; no cross-source deletion is claimed.
3. P2, deferred: semantic embeddings/background consolidation. Dependency: measured
   lexical/fallback failures justifying added cost. No service/index added speculatively.

Cleanup verified: task fixture directories/databases/transcripts, copied API settings
and disposable CLI removed. Retained requested screenshots and compact evidence total
approximately 385 KB. Global settings digest unchanged; native test app stopped.

## Review and gateway revalidation - 2026-10-22

Started from clean `f600a50`, pulled main. Scope: review the already-landed range
`deec0c9..f600a50`, replace only the user's exact old gateway URL in local settings,
and revalidate real recall. Four endpoint fields changed; model/credentials and
the unrelated default Sensenova route were preserved. No credential enters Git.

Review found a pre-existing metadata boundary defect affecting the claimed recall
contract: ---suffix/----/---: were accepted as closing fences, while an indented
--- in a valid block scalar prematurely ended the preview. Three negative and one
positive regression fail before the fix. Minimal architecture: one shared closing
fence predicate inside project-memory parsing; reuse the YAML parser and budgets.

Ordered gate: focused/full Go, race and vet -> commit/push parser slice -> rebuild
signed app -> rerun 20 real GLM 5.2 cases on the new gateway -> finalize evidence.
The pre-fix new-gateway GLM 5.2 run passed 18/18; GLM 5.1 still returned HTTP 503.
Post-fix real-provider validation remains pending until run against the new binary.

The Python acceptance validator had the same prefix-fence issue; its two new
negative variants also failed before correction. Its fence predicate now mirrors
the Go parser. Two real-provider cases were added for block-scalar recall and
invalid-fence exclusion, bringing the post-review gate to 20 cases.

Post-review delimiter fix: full Go suite, focused race/vet and all seven Python tests passed. Parser/harness slice is ready for commit and immediate push.

## Gateway and post-fix verification closure

- Replaced the exact old gateway in four local fallback-provider endpoints, preserving
  their credentials/models; the separate default GLM-5.2 provider remains on the
  Sensenova host. Global settings are user data and remain outside Git. SHA-256
  matches the recorded post-migration receipt; no credential values are recorded.
- Authenticated `/models` on the new gateway returned 200 and listed glm-5.1 and
  glm-5.2. GLM-5.1 inference itself still returned 503 (no available channel).
- Isolated GLM-5.2 routing through the same migrated gateway completed 20/20 real
  scenarios against the post-fix embedded app binary. Cold recall and valid YAML
  block-scalar recall passed; malformed closing fence was excluded. Default route
  and user global settings were not switched for this test.
- Post-fix full suite: 117 Go package reports ok; focused race and vet; seven Python
  harness tests. Signed release-candidate build digest recorded with the acceptance.
- Native post-fix screenshot acceptance is BLOCKED by the host Mac being locked.
  Earlier native acceptance belongs to the prior binary and is not represented as
  post-fix evidence. Unlock and rerun on the actual app for that final surface.

## Re-review: index-hint ownership

Base `4a5e64f`; clean main, pulled before review; no pre-existing dirty files.
The new regressions failed before the fix: a later same-line link's hook
selected the first file, a later alias was ignored, and commented hooks/entries
could select an otherwise irrelevant body. Repeated weak hints could also add up
and incorrectly preload a file. Fix index parsing independently from
rooted reads/ranking, keep duplicate aliases separate and use their strongest
score without amplification. No provider/global settings or tenant changes.

- Intended/completed implementation: `project_memory_index.go`, recall caller.
- Tests: regressions reproduced pre-fix. Post-fix full Go suite, focused race/vet,
  Python harness and six isolated GLM-5.2 cases pass. Real-model probes cover
  same-line link ownership, normalized duplicate aliases, comments and weak-hint
  non-amplification; no unrelated file body entered the request context.
- New isolated CLI SHA-256: `a24f25f1958cc10b1865e33cdd6a773f2a8a4b0b7868ab66c9eff43c416f1ffd`.
  Global model settings stayed byte-identical; copied test credentials and fixture
  directory were removed.
- The current source was rebuilt as a signed, clean release-candidate app for
  `05910fc`; embedded service SHA-256 is
  `34e89e0db58203f68c063a6b7df3542effd0b53e013e72b1c83d82022d7f192e` and signature
  verification passed. Native UI write plus fresh-process cold recall then passed
  on that exact binary: see `native-acceptance-summary.json` and the two 2026-10-23
  screenshots under `desktop-v2/build/validation/20261023/coding-memory-review/`.
  The locked-host blocker is closed; no remaining native acceptance gate for this
  coding-file-memory slice.
- Explicit deferrals remain unchanged. The earlier 20-case suite predates the
  index-hint patch; this patch has its own six real-model cases plus the native
  write/cold-recall acceptance above. All generated validation outputs remain
  ignored/local.
