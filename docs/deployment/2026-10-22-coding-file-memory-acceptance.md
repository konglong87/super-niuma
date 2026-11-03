# Coding file memory acceptance - 2026-10-22

## Conclusion and scope

The agreed bounded file-memory changes are implemented. Deterministic regressions,
18 real-provider cases and actual native desktop write/cold-recall evidence passed.
This is not a claim that every provider or every form of memory is now perfect.

Coding file memory does not require a tenant memory database. Discovery still uses
`<config>/projects/<cwd-slug>/memory/MEMORY.md` and indexed topic Markdown files;
an arbitrary flat `projects/<slug>/memory.md` is not automatically discovered.
Desktop session/task storage is separate from the source of this file memory.

## Changes

- `0c9c877`: canonicalize both declared writable root and destination; fix first
  writes beneath a symlinked configuration ancestor when the root does not exist.
  Preserve lexical containment and escaping-child-symlink rejection.
- `c0154b8`: exact word boundaries, short Go/CI/UI words, identifier components and
  Han bigrams; name/description/index weights 4/2/1, minimum score 2. Index hints
  cannot bypass missing or invalid frontmatter.
- `97d7b85`: expand actual file-tool/cold-process and real-provider lifecycle gates.
- `d870233`: opt-in pacing after observed provider rate limits; never automatically
  replay a mutating scenario whose earlier tool effects may have persisted.
- Preserve the file format, four-topic cap, rooted reads, existing byte/line/context
  budgets and Read fallback. No vector database, embeddings, background consolidation,
  extra runtime model call or new runtime dependency.

All 25 fixed lexical cases pass; the old matcher failed 12 of these cases. This is
an intentionally targeted regression corpus, not general-world accuracy or a
leaderboard comparison with another memory product.

## Automated verification

Go 1.26.6, with `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly`:

- `go test -count=1 ./...`: passed; 117 packages reported ok, plus packages without tests.
- `go vet ./internal/memory ./internal/tools ./internal/query`: passed.
- `go test -race -count=1 ./internal/memory ./internal/tools ./internal/query -run 'TestProjectMemory|TestMemoryRecall|TestWriteCreatesMissingRoot|TestCodingFileMemory'`: passed.
- `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p memory_release_acceptance_test.py -v`: seven passed.
- File integration runs real Write/Read and a separate process, but its model
  transport is a test double. It does not substitute for the real-provider gate.

## Real-provider gate

The complete passing run used configured `deepseek-v4-pro`, the actual signed
app's embedded CLI, new synthetic workspaces and a symlinked configuration root.
The answer was only in the body, not index/frontmatter or the cold-recall question.
Checks inspect disk, logical pre-provider requests, actual tool output and response.
They do not merely trust a model's assertion that it saved something.

All 18 cases passed:

| Cases | What was verified |
|---|---|
| write, cold-recall | Actual first creation and a new process with no transcript/tool calls |
| other-project, chat-mode, bare-mode | No extra file-memory body or expected answer in those contexts |
| ignore, unindexed, missing-file | No automatic body recall; existing index contract remains unchanged |
| readonly-write-tools, activity-no-write | Available write tools did not imply writes; bytes remained unchanged |
| read-fallback | First request lacked the answer; actual Read supplied it; response was correct |
| update, updated-cold-recall | Real Edit changed the fact; a new process received only the new value |
| forget, forgotten-cold-recall | All Markdown, including unindexed files, cleared of old/new values; no later recall |
| chinese-short-recall, english-word-boundary | Seeded lexical fixtures: short Chinese match and no art/artifact substring match |
| budget | Four-topic cap, document/content budgets and explicit read-pointer overhead |

Read fallback used two requests and one Read. Ordinary cold recall and the Chinese
short-query case used one request each and zero tool calls. An extra Bash attempt
in the update case was rejected with `unknown tool: Bash`; it did not execute.
The 2048-byte content-budget case used 2562 bytes including 514 bytes of retained
read pointers; do not misrepresent this as a strict 2048-byte total limit.

Successful-run provider counters: 210,978 input tokens, 4,994 output tokens and
145,408 cache-read tokens. Cache counters are reported as supplied, not added again
as billable input. Process time totaled 177.7 seconds plus pacing. No currency cost
or provider-wide reliability SLO is inferred from a single run.

### Failed environment attempts, not counted as passes

- The first directory inherited home guidance; the harness rejected it before
  copying credentials.
- `sensenova-glm-5.2` completed actual writes and some cold/negative cases, but two
  runs received HTTP 429, `inference exceeds tpm/rpm limit`. A 65-second interval
  did not eliminate this. Its complete suite remains unverified.
- Configured `glm-5.1` returned HTTP 503, no available model channel.
- Some GLM no-tools negatives emitted raw pseudo-tool markup. No actual tools ran
  and no expected answer leaked; this is not evidence of acceptable answer quality.
- Failure receipts remain in the compact evidence; no assertions were weakened.

## Native desktop acceptance

Built with `COMPUTER_USE_RELEASE_CANDIDATE=1 bash scripts/build-desktop-v2.sh` from
clean commit `97d7b85a92a10f9f227879f6331733ef53f488d5`. Deep ad-hoc signature
verification passed. Later commits changed acceptance scripts/docs only, not
runtime, desktop or Web source. The real-provider suite and native UI used the
same embedded service SHA-256:

`0a86b7e78f7b5dfb8614049940c425c077f4e221437ebe72678d40dee1914252`

1. Native coding entry, actual clicks/keyboard input, model-written topic and index,
   then Read readback. YAML and filesystem contents checked independently. The UI
   model first tried LS on a missing directory, used mkdir, then Write; that alone
   does not prove mkdir-free first-write behavior, which the CLI/tools gates cover.
2. Quit the app and embedded server, restart, create a separate session, and observe
   the correct answer in the actual native window.
3. That ordinary restart also injected the previous task title, which contained the
   answer. Although file recall was present, this run is explicitly NOT counted as
   file-only recall proof.
4. A new database alone was also an invalid clean fixture: retaining the old
   transcript configuration reused old records. The accepted fixture uses BOTH an
   empty session database and empty transcript configuration. Its memory entry is
   a symlink to the SAME physical Markdown directory; the answer was not rewritten
   or seeded by the tester.
5. Accepted first request: `prompt_mode=code`, zero initial history, two documents
   (index plus topic), 207 bytes. The answer occurs only in file context, not in
   question, task status or previous conversation. One request, no tool/history
   reads, complete correct answer visible in the native window. Both Markdown
   SHA-256 values remained unchanged.

Actual native screenshots, not browser substitutes:

- `desktop-v2/build/validation/20261022/coding-memory/01-isolated-coding.png`
- `desktop-v2/build/validation/20261022/coding-memory/02-native-saved.png`
- `desktop-v2/build/validation/20261022/coding-memory/03-restart-status-confound.png` (not file-only proof)
- `desktop-v2/build/validation/20261022/coding-memory/04-clean-cold-recall.png` (accepted proof)

The same ignored directory retains compact results, build manifest, synthetic
request context, native AX reply, screenshot hashes and test logs. Raw provider
transcripts, temporary workspaces/databases, copied credentials and disposable CLI
builds are removed after evidence projection. The user's global settings SHA-256
is unchanged; default model and real project memory were not edited.

## Remaining limitations

- Lexical ranking is not semantic retrieval; paraphrases can use Read fallback but
  are not guaranteed to be automatically recalled.
- Ignore-memory disables additional topic preloading, not the existing index.
- File forgetting does NOT delete session history, logs or task notifications.
  Ordinary desktop task titles can carry an old fact again. Strict cross-source
  forgetting needs a separate context-retention policy; it is not fixed here.
- Existing cwd slug encoding is not a collision-free tenant identity; os.Root is
  not a complete OS sandbox.
- Provider 429/503 availability and the separate task-context-source issue were
  not disguised as fixed. Formal Apple distribution signing and a physical second
  display remain deferred as previously directed by the user.

## Review follow-up and new-gateway GLM rerun

On 2026-10-22, the exact four local fallback endpoints previously using the old
AILab host were changed to `https://ai-gateway.theunifiedcare.com/v1`. Provider
models and credentials were preserved; local settings remain untracked user data.
The separately configured default GLM-5.2 route still uses the Sensenova host.
An authenticated `/models` call over Go HTTPS returned 200 and listed GLM-5.1 and
GLM-5.2. Listing availability is not inference availability: GLM-5.1 still returned
503, `No available channel for model glm-5.1 under group default (distributor)`.

Isolated GLM-5.2 requests on the new gateway then passed all 20 real-model cases
against post-review binary SHA-256
`058e7602375dc6b6e8b8dda4fff7612514525c2c815784740fae7496ab51fe07` (source
commit `c90a4ad11feccd1a86445400e02c8dc70f73600e`). No default-model route or
workspace memory was changed. This extends the earlier cases with valid YAML block
scalar recall containing an indented `---`, and rejection of a malformed
`---not-a-delimiter` closing fence. Both behaved correctly in real requests.

Review also found two real frontmatter parsing defects; regression tests reproduced
both before their fix. Exact, unindented closing fences are now required; horizontal
trailing whitespace remains valid. The fix and expanded real-provider runner are
committed as `c90a4ad` and pushed to main. Full Go, focused race/vet, and Python
acceptance tests passed after the change.

At the time of the 2026-10-22 review, a second native GUI pass on the post-fix binary
was blocked because the Mac was locked; no old screenshot was presented as new-build
evidence. That blocker was later closed by the isolated native acceptance recorded
below on 2026-10-23. Existing GLM 5.1 availability and the default GLM 5.2 route
migration remain distinct follow-ups.


## Re-review: indexed-link hint ownership

A second source review found that Markdown link matching assigned the remainder
of a line to its first link, ignored later aliases for an already-seen path, and
searched raw HTML comment text even though comments are removed from injected
memory context. Repeated weak aliases could also have amplified an otherwise
insufficient index-only score. These are fixed by binding each label/hook to its
own link, stripping comments before extraction, normalizing paths before grouping,
and using the strongest alias score rather than summing aliases. Rooted reads,
frontmatter eligibility, score weights, candidate/body budgets, and path containment
remain in place.

The new regressions failed before the fix. After the fix, the full Go suite, focused
race tests, `go vet`, and all seven Python acceptance harness tests passed. Six
isolated GLM-5.2 requests through `https://ai-gateway.theunifiedcare.com/v1` also
passed: same-line links, later normalized alias, commented hook, commented entry,
duplicate weak hint, and two disjoint weak aliases. The request-context assertions
confirmed that the unrelated decoy body was not preloaded. These are targeted
model observations, not a claim of universal semantic recall.

The isolated CLI binary digest was
`a24f25f1958cc10b1865e33cdd6a773f2a8a4b0b7868ab66c9eff43c416f1ffd`. The global
settings remained byte-identical, and the copied credential/private fixture was
removed. The current task patch is distinct from the earlier 20-case post-frontmatter
run; neither evidence set substitutes for a native screenshot on this patch.

The reviewed source is committed and pushed as `05910fcde2bcc80ea1533171ba16cec33fb9bf94`.
A clean post-change release-candidate app was built from that commit; the app
manifest records the same source commit, clean source flags, service SHA-256
`34e89e0db58203f68c063a6b7df3542effd0b53e013e72b1c83d82022d7f192e`, and verified
ad-hoc signature. This proves packaging/build integrity, not UI behavior.

At the time this review note was first written, native acceptance was blocked by
the locked Mac. The blocker was subsequently closed; see the 2026-10-23 native
post-review acceptance below. This patch now has native screenshots, automated
tests, and targeted real-provider evidence, with the stated single-run limitations.

## Native post-review acceptance - 2026-10-23

The actual Wails desktop app built from source commit `05910fcde2bcc80ea1533171ba16cec33fb9bf94` (embedded service SHA-256 `34e89e0db58203f68c063a6b7df3542effd0b53e013e72b1c83d82022d7f192e`) was operated through native UI. Test routing used GLM-5.2 on the migrated gateway in owner-only isolated settings; default model settings were not changed.

The first app process used a fresh workspace/config and successfully invoked Write twice, saving a topic file and `MEMORY.md` index. Disk inspection confirmed that the synthetic value exists only in the topic body, not in its title, frontmatter or index; YAML parsed the literal block scalar including the indented `---`. Screenshot shows the actual native Write tool calls and `saved` reply.

After closing the first app, a second process used a fresh desktop SQLite/session root and fresh config/transcript root; only the physical project-memory directory was shared through a symlink. The cold query did not contain the expected value, no old conversation or task-title records were available, and the first captured model request was turn 1/code mode with exactly the index and one topic-memory document. The answer was visible in the native window and matched the body-only value. The one request had zero model tool-result calls, and file hashes before/after were identical.

Evidence (screenshots are real native window captures):

- Private native screenshot evidence was retained locally and is not published in this source snapshot.
- Private native screenshot evidence was retained locally and is not published in this source snapshot.
- Sanitized machine-readable receipt: `desktop-v2/build/validation/20261023/coding-memory-review/native-acceptance-summary.json`.

The private fixture directory and isolated copied credential were removed; the user global settings file remained byte-identical. This acceptance demonstrates this specific native write-and-recall path, not general semantic recall for arbitrary paraphrases. Earlier concerns about task-history deletion and the unchanged default Sensenova route remain separate limitations.
