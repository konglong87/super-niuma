# Super Niuma Desktop entry acceptance — 2026-11-03

## Delivered scope

Desktop is the primary application. **超级牛马干活** appears directly below **新建会话** in the existing upper-left sidebar. The Super workspace renders in the same Wails window using native Go bindings, without an iframe, external browser or additional HTTP listener. The former `127.0.0.1:61585` address was a disposable intake-console test listener; it is not a user-facing entry.

Business isolation remains intact: Super lazily opens its dedicated SQLite work-order store; Session schemas, prompts, routing and execution code remain unchanged. The selected conversation remains mounted while Super is visible. The ordinary conversation controls are not redirected through Super.

This is **intake-only**, not autonomous execution. Create/list/detail/cancel, idempotency and restart persistence are implemented. Execution, verification and delivery are explicitly `not_started`.

## Automated verification

| Gate | Result |
| --- | --- |
| `npm --prefix web run typecheck` | PASS |
| `npm --prefix web test` | PASS: 92 files, 896 tests |
| Focused `src/v2/super` tests | PASS: 11 tests including native-bridge capability checks, click submission, idempotent retry, whitespace guard, stale responses, navigation and preserved pane state |
| `GOSUMDB=sum.golang.org go test ./...` | PASS: 125 tested packages |
| Full `go test -race ./desktop-v2 ./internal/super/... -count=1` | PASS |
| Bridge concurrency/shutdown/independent-instance race subset, `-count=20` | PASS |
| `go vet ./desktop-v2 ./internal/super/...` | PASS |
| `python3 scripts/super-niuma-boundary-test.py` | PASS: immutable baseline plus four exact reviewed composition exceptions |
| `python3 scripts/super-niuma-boundary-guard-test.py` | PASS: six guard self-tests; also added to CI |
| `python3 scripts/public_computer_use_docs_test.py` | PASS: seven tests |
| `scripts/build-desktop-v2.sh` | PASS: real macOS Wails app built; local ad-hoc signature validation passes |

The inherited Unix-socket bridge fixture exceeds macOS path length at this checkout location. Full Go and Desktop race suites therefore ran in an owned short private source copy beneath the home directory. The copy contained the current task's Go source and tests. No sandbox or trust-path check was relaxed; the copy was removed afterward.

## Real native acceptance

Tested the newly built app, not a browser substitute or the former repository's app. All input was synthetic. HOME, Desktop state and Super state were isolated; ordinary model configuration used a non-networking loopback test route. No message was sent to a model.

1. Fresh startup: choose the no-model intake action from onboarding; Super is usable without model setup.
2. Confirm adjacent upper-left **新建会话** and **超级牛马干活**, rendered inside the same window.
3. Enter title, objective and acceptance criteria through native input actions; activate **登记工单** and inspect its detail and `not_started` stages.
4. Repeat the submission: count stays unchanged; structured backend logs show `created=false`; no extra creation event.
5. Quit/relaunch: recorded order remains. Cancel it, quit/relaunch again: cancellation remains.
6. Use the unchanged **新建会话** dialog to create one traditional session without sending a message.
7. Type an unsent conversation draft, switch to Super and return: same selected conversation and exact draft remain. Super form data also survives pane switches.
8. On the final rebuilt form, native pointer submission and cancellation both succeed. Three distinct synthetic orders produce three creation events and two cancellation events (five audit events total). Final states: one recorded, two cancelled. DB file mode: `0600`.

Native focus observation: after text paste, the first pointer action sometimes only changed focus; the unchanged traditional dialog reproduced this. Re-observation and a fresh click succeeded. No Super-specific handler defect was established. The final form follows existing Desktop `form/onSubmit` semantics rather than retaining speculative explicit-submit workarounds.

## Retained evidence (ignored local output)

- `desktop-v2/build/validation/20261103/03-desktop-home.png`
- `desktop-v2/build/validation/20261103/05-session-draft-preserved.png`
- `desktop-v2/build/validation/20261103/06-super-final-desktop.png`
- `desktop-v2/build/validation/20261103/07-super-cancelled.png`
- `desktop-v2/build/validation/20261103/native-acceptance.json` — sanitized results and screenshot SHA-256 hashes.

Screenshots are actual native-window captures, not generated mockups. Test databases, raw logs, stale screenshots and the disposable source copy are removed. The test app is closed; eight acceptance-owned background runtime/helper processes are stopped. The former `niuma` working tree remains clean at `6b021af`.

## Delivery and limitations

The first coherent implementation was committed and pushed as `25fa7df`; the final acceptance commit closes this correction. All work stays on `main`, without branches or worktrees. The legacy snapshot manifest was not regenerated: only Desktop composition/lifecycle, shell navigation, sidebar action and optional bridge types have exact reviewed exceptions.

This does not replace the user's installed old app or publish a signed release. The new local build is `desktop-v2/build/bin/niuma.app`. Windows/Linux native UX was not tested in this slice. Automatic workers, execution approval, evidence-based verification and delivery remain the ordered P2+ roadmap. Physical second-display acceptance and formal signing/distribution remain previously deferred work.
