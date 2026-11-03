# AI Study Skill Publish Control Plane Plan

## Background

AI Study currently builds `dist/teach-v2.skill.zip`, then the `golang-claude-code`
middle layer publishes that package into the `ai-study` tenant as `teach-v2`.
The middle layer already supports tenant skill package render/publish APIs,
tenant skill versions, structured skill routing, runtime hot reload, telemetry,
and usage ledger evidence. The remaining problem is operational: publishing a
new AI Study teaching skill still requires manual middle-layer operation.

The preferred goal is to make the middle-layer Web admin the unified skill
publish control plane. AI Study and other upper-layer applications should not
each build their own full package publishing back office. They can optionally
call the middle-layer API from CI or expose a lightweight shortcut, but package
validation, publish, rollback, runtime verification, and audit should live in
the middle layer.

## Goals

- Middle-layer Web admin is the primary UI for publishing tenant skill packages.
- AI Study can provide package artifacts or optionally call the publish API, but
  does not need to own a full package lifecycle UI.
- Every upper-layer app can reuse the same middle-layer publish, preview,
  verify, history, rollback, and audit workflow.
- `teach` and `teach-v2` continue to coexist; publishing `teach-v2` must not
  overwrite the old `teach` skill.
- Structured route remains generic and configurable:
  `teach_decision_v1 -> teach-v2`.
- Publishing is hot-loaded from MySQL tenant skill runtime on the next request;
  no server restart is required.
- Verification proves `tenant_runtime.active=true`, `loaded_keys` includes
  `teach-v2`, version/hash match the newly published package, loaded bytes are
  positive, and telemetry does not persist full skill content.

## Non-Goals

- Do not move AI Study `CalibrationPolicy`, state machine,
  `allowed_state_changes`, familiar-domain gating, analogy validation, or LLM
  decision validation into the middle layer.
- Do not hard-code AI Study workflow rules in global prompts or shared query
  logic.
- Do not introduce OSS/S3 in this step. Phase 1 remains local artifact storage.
- Do not require AI Study to run middle-layer CLI commands.
- Do not require each upper-layer application to implement its own complete
  skill package publishing page.

## Ownership Boundary

AI Study owns:

- building `dist/teach-v2.skill.zip`;
- computing and displaying package sha256;
- optionally calling the middle-layer publish API from CI or a lightweight
  shortcut;
- optionally storing AI Study-side release records for business release
  correlation;
- enforcing business rules through backend state machine and validators;
- correlating AI Study `engine_runs.trace_id` with middle-layer telemetry.

The middle layer owns:

- tenant skill package lifecycle: render, publish, history, rollback;
- the primary Web admin page for tenant skill package publishing;
- package validation, runtime rendering, artifact storage, and MySQL skill
  version writes;
- structured route resolution and tenant skill runtime injection;
- OpenAI-compatible provider calls and structured fast path;
- telemetry, usage ledger, response headers, and safe runtime metadata.

## Existing Middle-Layer Capability

The middle layer already has most of the foundation:

- `POST /tenant/skill-packages/render`
- `POST /tenant/skill-packages/publish`
- local artifact refs under tenant/skill/hash directories
- `tenant_skills.content_md` as the hot request path
- package metadata fields on tenant skill versions
- structured skill route support from header, metadata, tenant settings, env,
  and compatibility fallback
- OpenAI-compatible `/v1/chat/completions` structured fast path with tools
  disabled and tenant skills inlined
- safe response metadata via `tenant_runtime` body and
  `X-Tenant-Skill-*` headers
- MySQL telemetry for `query.prompt_context`

The missing piece is a productized middle-layer publish and verify workflow.

## Proposed P0 Flow

1. AI Study builds or exports `teach-v2.skill.zip`.
2. An operator opens the middle-layer Web admin, selects tenant `ai-study`, and
   uploads the zip package.
3. The middle layer computes sha256, validates the package, renders
   deterministic `runtime.md`, and previews manifest/runtime metadata before
   publish.
4. The operator confirms publish as `skill_key=teach-v2` and verifies the
   structured route `teach_decision_v1 -> teach-v2`.
5. The middle layer writes local artifacts, creates a new tenant skill version,
   and preserves or updates the structured route.
6. The middle layer automatically triggers runtime verification.
7. The verification result is shown in the middle-layer Web admin and can be
   used as the release gate.
8. AI Study can read or display the published version/hash through middle-layer
   API if it needs business-side release visibility.

For CI or a lightweight AI Study shortcut, steps 2 to 7 can be driven by API
instead of the Web UI, but the middle layer remains the source of truth for
package lifecycle and verification.

## Middle-Layer Web Admin

The middle-layer Web admin is the primary user experience for package publish.

P0 page capabilities:

- tenant selector, defaulting from current tenant context where possible;
- skill key input/selector, such as `teach-v2`;
- zip upload using browser file input;
- pre-publish preview: sha256, file size, manifest, file tree, runtime bytes,
  and `runtime.md` preview;
- structured route editor or selector, including
  `teach_decision_v1 -> teach-v2`;
- publish button guarded by tenant owner/admin permission;
- automatic runtime verification after publish;
- publish result: version, sha256, package ref, runtime ref, trace id,
  loaded keys, loaded version, loaded bytes, and pass/fail status;
- version history and rollback entry;
- audit trail link for publish and rollback operations.

This page should be generic. AI Study is only one tenant/app using the same
control plane.

## API Contract For Optional Automation

The API remains important for CI/CD and upper-layer shortcuts. It should not be
the only operational path.

AI Study or CI can call:

```http
POST /tenant/skill-packages/publish
Authorization: Bearer <middle-layer-token>
X-Tenant-Key: ai-study
X-User-Id: <admin-user-id>
Content-Type: application/json
```

Recommended request:

```json
{
  "skill_key": "teach-v2",
  "content_base64": "<zip-base64>",
  "update_structured_route": {
    "schema_name": "teach_decision_v1",
    "skill_key": "teach-v2"
  }
}
```

Recommended response fields:

```json
{
  "skill_key": "teach-v2",
  "version": 4,
  "enabled": true,
  "package_sha256": "...",
  "package_ref": "file://...",
  "runtime_ref": "file://...",
  "structured_route": {
    "schema_name": "teach_decision_v1",
    "skill_key": "teach-v2"
  }
}
```

If the current API response does not expose all these fields, the middle layer
should add them as additive response fields. This keeps compatibility with
existing callers.

## AI Study Optional Backend Shortcut

AI Study does not need to implement a full package publishing back office. If
the product team still wants a business-side shortcut, keep it thin:

```http
POST /admin/skill-packages/teach-v2/publish
```

Request:

```json
{
  "expected_sha256": "optional expected package sha256",
  "notes": "release note for this skill package"
}
```

Shortcut behavior:

1. Read the latest package from `dist/teach-v2.skill.zip`, or accept a zip
   uploaded by the Admin UI.
2. Compute sha256 locally.
3. If `expected_sha256` is provided and does not match, reject before calling
   the middle layer.
4. Base64-encode the zip content.
5. Call the middle-layer publish API with AI Study tenant headers.
6. Store only business-side release correlation if needed.
7. Link to the middle-layer publish result or runtime verification trace.

Suggested optional AI Study release record fields:

```text
id
skill_key
package_sha256
middle_layer_version
middle_layer_package_ref
middle_layer_runtime_ref
status: publishing | published | smoke_failed | failed
operator_id
trace_id
error_message
created_at
updated_at
```

This shortcut should call middle-layer APIs. It should not reimplement package
validation, runtime rendering, version history, rollback, or telemetry
verification.

## Middle-Layer Publish Implementation

When publishing from Web admin or API, the middle layer validates the package,
renders deterministic `runtime.md`, writes local artifacts, creates a new tenant
skill version, and preserves the structured route
`teach_decision_v1 -> teach-v2`.

## Runtime Verification Contract

P0 can verify by sending a real OpenAI-compatible structured request and reading
response metadata plus MySQL telemetry. To make AI Study integration simpler,
the middle layer should add a generic verification endpoint:

```http
POST /tenant/skill-packages/verify-runtime
Authorization: Bearer <middle-layer-token>
X-Tenant-Key: ai-study
X-User-Id: <admin-user-id>
Content-Type: application/json
```

Request:

```json
{
  "skill_key": "teach-v2",
  "schema_name": "teach_decision_v1",
  "expected_package_sha256": "...",
  "expected_version": 4
}
```

Response:

```json
{
  "ok": true,
  "trace_id": "skill-publish-smoke-20260628-...",
  "tenant_runtime": {
    "active": true,
    "resolved": true,
    "selector": "tenant_settings",
    "loaded_keys": ["teach-v2"],
    "versions": ["4"],
    "package_sha256": ["..."],
    "bytes": 20792
  }
}
```

The verify endpoint must remain generic. It checks that a selected tenant skill
runtime is loaded and observable; it does not validate AI Study learning state
or decision semantics.

## Middle-Layer P0 Work Items

1. DONE: `POST /tenant/skill-packages/publish` supports `content_base64`
   upload and does not require server-local `source_path`.
2. DONE: Additive publish response fields for version, hash, artifact refs, and
   structured route.
3. DONE: Add `POST /tenant/skill-packages/verify-runtime`.
4. PARTIAL: Make WebUI the primary publish surface. The Skills panel now
   supports zip upload via `content_base64`, manifest/runtime preview, publish,
   automatic verify, version history, rollback, and audit-backed API calls.
   A richer tenant selector and structured route editor remain follow-up UI
   polish.
5. Keep API publishing available for CI/CD and optional upper-layer shortcuts.
6. Keep `tenant_context` and `tenant_runtime` telemetry semantics separate:
   `tenant_runtime.active=true` means runtime route/skill was resolved;
   `tenant_context.active=true` only means tenant memory/profile/document
   addendum entered the prompt.
7. Update Swagger annotations and regenerate generated docs after API changes.
8. Add unit, server, WebUI, and MySQL E2E coverage.

## Security And Permission Rules

- Publishing requires tenant owner/admin role.
- Every publish and rollback writes audit log with operator, tenant, skill key,
  version, hash, and trace id.
- API tokens, JWTs, full prompt, full messages, private URLs, and skill content
  must not be persisted to telemetry.
- The publish API should validate package size, file count, path traversal,
  absolute paths, symlinks, and unsupported extensions.
- If a publish succeeds but verification fails, keep the new immutable version
  but mark the middle-layer publish result as `smoke_failed` and allow explicit
  rollback.

## Risk Points

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Wrong package uploaded | Model behavior changes unexpectedly | middle-layer sha256 precheck, preview, immutable version history |
| Old `teach` overwritten | Existing compatibility breaks | publish only `skill_key=teach-v2`; assert `teach` latest remains unchanged |
| Route drift | Structured calls load wrong skill | verify `teach_decision_v1 -> teach-v2` after publish |
| Telemetry leaks skill content | Sensitive prompt/runtime exposure | record only key/version/hash/bytes/refs; add leakage tests |
| Business rules move into middle layer | Product logic becomes scattered | keep validators and state transitions in AI Study backend |
| Provider smoke fails | Publish status unclear | verify runtime metadata before provider response when possible; surface trace and error |
| Each app builds its own publish page | Duplicate code and inconsistent controls | use middle-layer Web admin as the primary control plane |

## Acceptance Checklist

AI Study side:

```bash
sha256sum dist/teach-v2.skill.zip
go test ./... -count=1
pnpm test
```

Middle-layer side:

```bash
go test ./internal/tenantpkg -count=1
go test ./internal/server -count=1
go test ./internal/storage/mysql -count=1
go test ./... -count=1
git diff --check
swag init -g cmd/golang-cc/main.go --parseInternal --parseDependency
```

Middle-layer Web/admin E2E:

- Middle-layer Web admin publishes a new `teach-v2.skill.zip` into tenant
  `ai-study`.
- Middle-layer `tenant_skills` latest enabled `teach-v2` version has the
  uploaded package sha256.
- Old `teach` latest version is not overwritten.
- Structured route remains `teach_decision_v1 -> teach-v2`.
- Verification response reports `tenant_runtime.active=true` and
  `tenant_runtime.resolved=true`.
- Runtime metadata reports `loaded_keys=["teach-v2"]`, expected version,
  expected package hash, and `bytes > 0`.
- `tenant_telemetry_events` contains `query.prompt_context` evidence for the
  same trace id.
- Telemetry leakage check confirms no full skill body was persisted.
- Optional AI Study shortcut or CI path, if implemented, calls the same
  middle-layer API and reports the middle-layer publish/verify result.

## Rollout Plan

1. Middle layer: implement Web admin publish flow as the primary path.
2. Middle layer: implement additive publish response fields and verify API.
3. Middle layer: add WebUI/API/server/MySQL E2E coverage.
4. Run local MySQL E2E with real `ai-study` tenant.
5. Enable middle-layer admin access for AI Study maintainers.
6. AI Study: optionally add a thin CI/admin shortcut that calls the middle-layer
   API and links to the middle-layer verification trace.
7. Keep manual CLI publish as emergency fallback only.
