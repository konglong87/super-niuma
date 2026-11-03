# Super Niuma intake workspace

**Status: intake-only Developer Preview.** Use the **超级牛马干活** action in Desktop, directly below **新建会话**. It opens inside the same app window and does not replace or alter the conversation entry. It records explicit work requests and acceptance criteria, persists them across restarts, and supports cancellation. It does not execute tasks, call a model, verify artifacts or deliver results yet.

## Desktop (primary entry)

Build with `npm --prefix web ci --legacy-peer-deps` then `scripts/build-desktop-v2.sh`, and open the built app. Use **超级牛马干活** to record a task name, objective and one acceptance criterion per line. Inspect recent work orders and cancel an intake from the detail card. **返回会话**, an existing conversation, or **新建会话** returns to the normal conversation experience. Switching modes does not create a session. New users may choose **先登记工单（无需模型）** from model onboarding.

The native bridge uses the dedicated Super database, not the Session database. It opens on first Super use; no extra port/service is required. For isolated testing only, `SUPER_NIUMA_STATE_DIR` selects an absolute private directory. Keep normal Desktop and the optional headless console pointed at the intended state directory.

## Headless / developer console (optional)

Requirements: Go as declared by go.mod, CGO and a C toolchain for the existing SQLite dependency.

```sh
go run ./cmd/super-niuma
```

For headless debugging only, open the exact loopback address printed by the process (default `http://127.0.0.1:7391`). The application refuses non-loopback binding. A separate initial main branch belongs to the new repository; it does not change branches in the previous project.

The traditional entry remains:

```sh
go run ./cmd/niuma --help
```

There is no published Super binary release at this milestone; source startup is the supported path.

## Storage and isolation

Super stores work orders in `~/.super-niuma/work-orders.db`. It does not open the legacy Session store or read provider credentials. The new domain uses its own schema, not the desktop/session migrations. The directory and database are restricted to the local user. Stop the process with Ctrl+C or SIGTERM.

For an isolated evaluation:

```sh
go run ./cmd/super-niuma --listen 127.0.0.1:7391 --state-dir ./validation/super-intake
```

`validation/` is ignored. Never commit work-order databases, WAL files, logs, screenshots containing private requests, or real credentials. Remove throwaway state after evaluation. Do not point this flag at a shared or legacy database directory.

## What the statuses mean

- `recorded`: intake was durably recorded. No execution is scheduled.
- `cancelled`: the request is cancelled; the historical record remains.
- `execution_status`, `verification_status`, `delivery_status`: all remain `not_started` in this milestone.

The console says “已接单 · 未执行” and never claims a request was completed merely because it was saved. Intake success is not an execution, verification or delivery receipt.

## API and local trust boundary

The API prefix is `/api/super/v1`. Browser/API calls carry `X-Super-Niuma-Request: 1`. Creation also needs an `Idempotency-Key` and JSON content type. Retries with the same key and normalized content return the existing record; changing content under the same key is rejected. Retrying a cancelled request does not revive it.

Allowed Host is the exact printed IP and port. An Origin, when supplied, must match that same origin. Cross-site requests are rejected, and no permissive CORS is enabled. These controls address browser cross-origin/rebinding risks; they are not remote user authentication. Another process running with local access may call the service. Do not reverse-proxy or expose it to a network until an authenticated deployment mode is implemented.

The optional workspace is metadata only. Recording a path does not authorize filesystem operations. Do not enter credentials or sensitive user data in requests.

## Next phases and release migration

See the [ordered implementation ledger](../plans/2026-11-03-super-niuma-evolution.md). Controlled execution, worker ownership, independent verification, delivery receipts and proactive channel admission remain future gates. Final GitHub inspection found the former niuma repository private. This task did not change its visibility and does not reopen it. Release readiness and migration notices remain separate unfinished gates.

## Validation status

The intake entry is exercised on macOS in this milestone; CI also runs its binary HTTP smoke on Linux. Windows Super runtime acceptance is not claimed. The POSIX private-directory/file guard is intentionally fail-closed; it must not be weakened just to claim another platform. Existing desktop build jobs remain separate from Super entry acceptance.

Run the isolated black-box acceptance after building:

```sh
go build -o .tmp/super-niuma ./cmd/super-niuma
python3 scripts/super-niuma-smoke.py --binary .tmp/super-niuma
```

The smoke starts only owned loopback processes with synthetic inputs, exercises 18 HTTP checks including restart persistence, and cleans its processes and databases on exit. Browser screenshots are a separate acceptance surface, not replaced by this test.

The previously demonstrated `127.0.0.1:61585` address was a temporary acceptance listener and has been stopped. It is not the product entry.
