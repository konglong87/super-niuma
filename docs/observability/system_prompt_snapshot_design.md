# System Prompt Snapshot Observability Design

## Background

Current prompt observability intentionally records only `query.prompt_context`.
The manifest proves which context sources, feature sections, tenant runtime
metadata, and workflow rule counts were active, but it does not show the final
system prompt text sent to the model.

That is the right default for production safety, but it is not enough for prompt
engineering verification. For example, TODO-046 added `workflowClosureSection()`.
To verify the fix end to end, operators need to see whether the final runtime
system prompt actually contains:

- `# Workflow Closure`
- `source of truth`
- `derived files`
- `git diff --name-only`

The manifest can show `feature_sections=["workflow_closure"]`, but it cannot
prove that the assembled system blocks were not replaced by an override,
suppressed by `keep-coding-instructions:false`, or lost during prompt assembly.

## Goal

Add an explicit debug-only System Prompt Snapshot capability to Observability.

The first phase captures only the final assembled system prompt blocks. It does
not capture full request messages, user prompts, tool results, attachments,
knowledge chunks, or memory bodies.

This narrow scope solves the immediate verification problem while keeping the
sensitive data blast radius much smaller than full prompt/request capture.

## Non-Goals

- Do not record complete user messages.
- Do not record tool result bodies.
- Do not record tenant memory, knowledge chunk, document, or attachment bodies.
- Do not enable snapshot capture by default.
- Do not store large prompt bodies inside ordinary telemetry event properties.
- Do not expose snapshots to non-owner/non-admin tenant users.
- Do not make this a general prompt replay feature.

## User Story

As an operator debugging prompt behavior, I can enable a local debug switch,
send one request, then open WebUI Observability and inspect the exact full system
prompt sections used for that request, including section order and block cache
metadata, so I can verify whether a prompt fix is actually active at runtime.

## Capture Levels

Use a dedicated system prompt switch instead of overloading generic telemetry:

```bash
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT=off|redacted|full
```

Default is `off`.

`redacted` records system blocks after secret redaction.

`full` records exact final system block text. This mode is intended for local
debug and controlled verification only.

Full mode must require a second acknowledgement switch:

```bash
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT=full
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_ALLOW_FULL=true
```

This prevents accidental full prompt capture from a single environment variable.

## Request-Level Gate

Global enablement should not mean every request is captured. A request should
opt in with a header:

```text
X-Debug-System-Prompt-Snapshot: redacted
X-Debug-System-Prompt-Snapshot: full
```

CLI/TUI can later add an equivalent flag:

```bash
--debug-system-prompt-snapshot=redacted
--debug-system-prompt-snapshot=full
```

Effective capture level is the lower of global and request levels:

| Env | Header | Effective |
|-----|--------|-----------|
| `off` | any | off |
| `redacted` | absent | off |
| `redacted` | `redacted` | redacted |
| `redacted` | `full` | redacted |
| `full` + allow | absent | off |
| `full` + allow | `redacted` | redacted |
| `full` + allow | `full` | full |

For automated local investigations, a separate capture-all switch may be added:

```bash
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_CAPTURE_ALL=true
```

That switch should be rejected unless the global snapshot mode is not `off`.

## Data Model

Do not place prompt text in `tenant_telemetry_events.properties_json`.

Add a dedicated table:

```sql
CREATE TABLE IF NOT EXISTS tenant_system_prompt_snapshots (
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_id BIGINT UNSIGNED NULL,
  trace_id VARCHAR(128) NOT NULL,
  message_id VARCHAR(128) NULL,
  model VARCHAR(128) NULL,
  prompt_mode VARCHAR(32) NOT NULL,
  capture_level VARCHAR(16) NOT NULL,
  prompt_sha256 CHAR(64) NOT NULL,
  system_blocks_json LONGTEXT NOT NULL,
  manifest_json LONGTEXT NULL,
  redaction_report_json TEXT NULL,
  bytes INT NOT NULL DEFAULT 0,
  expires_at TIMESTAMP(6) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  KEY idx_prompt_snapshot_trace (tenant_id, trace_id, created_at),
  KEY idx_prompt_snapshot_session (tenant_id, user_id, session_id, created_at),
  KEY idx_prompt_snapshot_expires (expires_at)
);
```

`system_blocks_json` stores an array of block metadata:

```json
[
  {
    "index": 0,
    "type": "text",
    "text": "...",
    "sha256": "...",
    "bytes": 12345,
    "cache_control": {
      "type": "ephemeral",
      "ttl": "1h",
      "scope": "global"
    }
  }
]
```

`manifest_json` should store the same `ContextManifest` shape used by
`query.prompt_context`, so the UI can show body and manifest side by side.

## Telemetry Reference

The existing `query.prompt_context` event should remain the primary index. When
a snapshot is captured, add safe references only:

```json
{
  "system_prompt_snapshot.active": true,
  "system_prompt_snapshot.id": 123,
  "system_prompt_snapshot.capture_level": "full",
  "system_prompt_snapshot.prompt_sha256": "...",
  "system_prompt_snapshot.bytes": 54321,
  "system_prompt_snapshot.expires_at": "2026-07-02T00:00:00Z"
}
```

No prompt body should be duplicated into telemetry properties.

## Capture Point

Capture after the final effective system blocks are assembled and before the
model request is sent.

The correct area is inside the query runtime after these operations:

1. `effectiveSystemBlocks()`
2. append tenant inline skill or structured skill catalog if applicable
3. append local skills catalog if applicable
4. context manifest assembly
5. before `StreamMessages`

This ensures the snapshot matches the actual model request.

The capture should include:

- final `systemBlocks`
- joined prompt SHA-256
- cache metadata per block
- model
- prompt mode/profile/query source
- trace id/session id/message id
- `ContextManifest`

The capture should not include `req.Messages`.

## Redaction

`redacted` mode should apply redaction before storage.

Minimum redaction rules:

- Bearer tokens: `Bearer ...` -> `Bearer [REDACTED_SECRET]`
- API keys and tokens near keys like `api_key`, `token`, `secret`, `password`
- JWT-like strings
- private URL query values such as `token=`, `signature=`, `X-Amz-Signature=`
- cookie values
- very long single-line values

Each block should keep:

- original byte count
- redacted byte count
- original SHA-256
- redacted SHA-256
- redaction count

In `full` mode, keep exact text but still calculate hashes and bytes.

## Retention

Snapshots are temporary debug artifacts.

Recommended defaults:

```bash
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_TTL_HOURS=24
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_MAX_BYTES=200000
```

If the assembled system prompt exceeds max bytes:

- store the first `MAX_BYTES` bytes,
- mark `truncated=true`,
- store full prompt SHA-256,
- record original byte count.

Expired snapshots should not be returned by API. Physical cleanup can be a
background maintenance task or an admin command in a later phase.

## API

Add tenant-scoped APIs:

```text
GET /tenant/system-prompt-snapshots?session_id=&trace_id=&limit=20
GET /tenant/system-prompt-snapshots/{id}
DELETE /tenant/system-prompt-snapshots/{id}
```

Access rules:

- Bearer auth required.
- Tenant/user context required.
- Owner/admin role required.
- Scope queries by `tenant_id`.
- For non-admin users, scope by `user_id` unless product explicitly allows
  tenant-wide observability for owner/admin.
- Expired snapshots return 404.

Each read should emit an audit event:

```text
prompt_snapshot.viewed
```

Each delete should emit:

```text
prompt_snapshot.deleted
```

## WebUI

Add a Prompt Snapshot panel under Observability.

Recommended placement:

- `Observability -> Telemetry`: when a `query.prompt_context` event has
  `system_prompt_snapshot.id`, show an `Open system prompt snapshot` action.
- `Observability -> Trace`: for a selected session/trace, show a `Prompt`
  sub-tab if snapshots exist.

Panel contents:

- capture level
- model
- trace id
- session id
- prompt SHA-256
- byte count
- expiry
- feature sections from manifest
- workflow rule count
- system blocks in order
- cache control per block
- search box

For TODO-046 verification, the search box should make it easy to find:

- `# Workflow Closure`
- `source of truth`
- `derived files`
- `git diff --name-only`

Full snapshots should show a visible warning:

```text
Full system prompt snapshot. Local debug data may include sensitive project
instructions. Do not share outside the debugging context.
```

## Verification Scenarios

### Workflow Closure Positive Case

Setup:

```bash
GOLANG_CLAUDE_CODE_FEATURE_ENHANCED_SYSTEM_PROMPT=true
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT=full
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_ALLOW_FULL=true
```

Send a request with:

```text
X-Debug-System-Prompt-Snapshot: full
```

Expected:

- `query.prompt_context` has `system_prompt_snapshot.id`.
- snapshot contains `# Workflow Closure`.
- snapshot contains `source of truth`.
- snapshot contains `derived files`.
- snapshot contains `git diff --name-only`.
- manifest has `code_context.workflow_rules > 0` when workflow docs exist.
- manifest has `workflow_closure` in feature sections.

### Workflow Closure Disabled By Feature Flag

Setup:

```bash
GOLANG_CLAUDE_CODE_FEATURE_ENHANCED_SYSTEM_PROMPT=false
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT=full
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_ALLOW_FULL=true
```

Expected:

- snapshot exists if request-level debug is enabled.
- snapshot does not contain `# Workflow Closure`.
- manifest feature sections do not include `workflow_closure`.

### keep-coding-instructions False

Run a request path or test case that disables `keep-coding-instructions`.

Expected:

- snapshot does not contain `# Workflow Closure`.
- default coding sections are also absent.

### Override System Prompt

Run a request with `OverrideSystemPrompt`.

Expected:

- snapshot contains only the override system block.
- manifest has `override_system=true`.
- snapshot does not contain default prompt sections.

## Test Plan

Backend tests:

- config parsing for off/redacted/full.
- full mode requires allow-full acknowledgement.
- request header cannot enable capture when env is off.
- effective level chooses the lower privilege mode.
- snapshot captures final system blocks after catalog/addendum assembly.
- snapshot does not include user messages or tool result bodies.
- redactor covers bearer tokens, JWT, API key, password, URL signatures.
- max byte truncation records original hash and truncation flag.
- telemetry event contains only snapshot reference metadata.

Storage tests:

- insert/list/get snapshot scoped by tenant/user/session/trace.
- expired snapshot is not returned.
- cross-tenant read is rejected.
- read/delete audit events are written.

Server tests:

- owner/admin can read snapshot.
- member or different tenant cannot read snapshot.
- missing auth returns 401.
- expired snapshot returns 404.

WebUI tests:

- Telemetry event with snapshot reference shows open action.
- Prompt tab renders block list, hashes, cache control, and warning.
- Search finds `Workflow Closure`.
- Full snapshot warning is visible.

Live smoke:

- start MySQL-backed server with full system snapshot enabled.
- send one debug request.
- open WebUI Observability.
- verify full system prompt section includes `# Workflow Closure`.

## Implementation Phases

### Phase 1: Backend Snapshot Core

- config/env parsing.
- request-level debug header.
- system prompt snapshot model.
- MySQL migration and repository methods.
- capture final system blocks in query runtime.
- telemetry reference fields.
- unit and MySQL e2e coverage.

### Phase 2: API

- list/get/delete tenant prompt snapshot APIs.
- owner/admin authorization.
- audit logs for read/delete.
- API tests.

### Phase 3: WebUI

- Telemetry link from `query.prompt_context`.
- Trace Prompt tab.
- block viewer and search.
- full-mode warning.
- WebUI tests.

### Phase 4: Operational Hardening

- TTL cleanup command or background job.
- optional redaction tuning.
- optional CLI/TUI flag.
- documentation and runbook.

## Rollout Guidance

Keep default off in all environments.

For local verification of TODO-046:

```bash
GOLANG_CLAUDE_CODE_FEATURE_ENHANCED_SYSTEM_PROMPT=true \
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT=full \
GOLANG_CLAUDE_CODE_SYSTEM_PROMPT_SNAPSHOT_ALLOW_FULL=true \
GOLANG_CLAUDE_CODE_MOBILE_DEV_AUTH=true \
GOLANG_CLAUDE_CODE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_prompt_debug?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
go run ./cmd/golang-cc server --host 127.0.0.1 --port 18080 --auth-token test-token
```

Then send a debug request with:

```text
Authorization: Bearer test-token
X-Tenant-Key: yutang
X-User-Id: prompt-debugger
X-Debug-System-Prompt-Snapshot: full
```

Open WebUI Observability and verify the snapshot contains the full
`# Workflow Closure` section.
