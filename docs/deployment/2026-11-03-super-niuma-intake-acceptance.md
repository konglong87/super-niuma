# Super Niuma intake acceptance — 2026-11-03

## Scope

Dedicated intake-only entry, separate WorkOrder persistence, cancellation and exact HTTP boundaries. This is not an autonomous execution/verification/delivery acceptance. No model credentials or old Session database were opened by the Super application.

## Automated checks

- Full Go regression: 124 packages with tests passed; packages without tests were built. Some package results may use the Go cache. The full run used a short, owned private source directory because macOS Unix-domain socket paths cannot accommodate the original long checkout path used by inherited bridge tests.
- Fresh `go test -race -count=1 ./internal/super/... ./cmd/super-niuma` passed; `go vet` on those packages passed.
- `scripts/super-niuma-boundary-test.py`: 1,383 legacy Go/frontend files unchanged after Go module and empty-EOF normalization; the traditional CLI has no Super dependency. The new CLI imports only Super domain/API/store/app packages from this repository.
- `scripts/super-niuma-smoke.py`: 18 black-box checks against the compiled binary, including restart persistence, same-key replay, conflict rejection, repeated cancellation and cancellation-preserving retries, invalid origin/host/header/media/body/path refusal.
- Seven public-doc tests and four source-snapshot tests passed. The source publication requires UPSTREAM.md and retains all applicable licenses.
- JavaScript syntax check passed. The root CI runs the new binary smoke in addition to existing build/vet/race/web checks.

## Real browser acceptance

The built-in browser drove the actual served page: empty state -> form entry -> saved work order -> reload -> process restart -> same work order -> cancellation dialog -> cancelled historical record -> final-binary restart -> cancelled record still present. All examples were synthetic and no work was executed. Browser warning/error collection was empty.

Real screenshots are retained locally and intentionally ignored by Git:

- `validation/20261103/super-intake/01-empty.png`
- `validation/20261103/super-intake/02-recorded.png`
- `validation/20261103/super-intake/03-cancelled.png`
- `validation/20261103/super-intake/04-workspace.png`

The compiled binary's black-box receipt and screenshot digest manifest are in the same local evidence directory. Temporary processes, databases, test source copies and raw logs are cleaned after verification. The screenshots are not fabricated from HTML or reused historical captures.

## Initial failures and their disposition

The root checkout's full test run encountered macOS socket-length errors plus one PTY spacing failure. A first short copy under /tmp was rejected by existing bridge trust checks. The final full suite passed under a short private home-directory copy without any bridge or TUI source change. The original-repository isolated PTY run also passed. These results do not establish that all existing PTY flakiness is solved.

## Limits

- Super remains loopback-only/local-user trusted; remote authentication and multi-tenant exposure are not supported.
- The Super runtime was exercised on macOS; Linux binary smoke is a CI gate. Windows Super runtime acceptance is not claimed.
- Existing native desktop logic is preserved, but a fresh macOS desktop UI acceptance for the successor is still a migration gate. Cross-platform build success is not native UI acceptance.
- Automatic execution, independent task acceptance, external delivery and proactive intake remain future work.
- Final GitHub inspection found the old niuma repository private. This agent made no visibility-changing call; initial visibility was not verified, so the time or actor of that change is unknown. No formal binary release, signing or visibility migration was performed by this task.
