# Desktop SQLite Runtime Acceptance

## Topology Impact

- Topology impact: updated
- Blast radius: B2_MODE
- Topology reason: Wails desktop entrypoints now launch a local server backed by SQLite while reusing the existing tenant/session/task repository contract. WebUI 2 remains the existing RT-OUTPUT consumer.

The desktop path uses `RT-ENTRY -> RT-WIRING -> RT-PERSIST -> RT-SESSION-CONTROL`. MySQL behavior remains unchanged for server deployments. SQLite migration is idempotent and creates the tables needed by tenant sessions, messages, tasks, events, pending input, audit, and telemetry.

## Acceptance Evidence

- SQLite repository test covers migration reopen, tenant/user, session, message, task, and event persistence.
- Wails v2 production build succeeds on macOS.
- Real desktop server creates a session and persists it in the user data SQLite database.
- Real provider run using the existing global settings completes with a model response visible in WebUI 2.
- macOS screenshot acceptance: `desktop-v2/build/acceptance-real-e2e-selected.png` (generated artifact, not source-controlled).
