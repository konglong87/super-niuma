# Structured Tenant Skill Routing Plan

## Background

`/v1/chat/completions` already supports an OpenAI-compatible structured fast path when `response_format.type=json_schema` is present:

- run exactly one query turn;
- disable runtime tools;
- skip automatic title generation;
- forward provider-level JSON Schema options;
- keep tenant persistence, telemetry, usage ledger, and trace available.

Because tools are disabled on this path, tenant skill instructions that would normally be loaded through the `Skill` tool must be inlined before the model request. The current implementation can inline the existing AI Study `teach` skill for `teach_decision_v1` / `teach_context_v1`, but the new AI Study tenant route should use `teach-v2` so it is distinguishable from the current AI Study skill. Other tenants can still configure `teach`, `teach-v2`, or any other skill key. That selection logic should become generic and configurable so the middle layer stays reusable for other tenants and structured applications.

## Goal

Build a generic, configurable tenant skill routing layer for structured OpenAI-compatible requests.

The middle layer owns:

- provider routing;
- tenant skill lookup and inline injection;
- request / trace / telemetry observability;
- tenant runtime smoke validation.

The middle layer must not own product-specific business state machines. For AI Study, rules such as `teach_context_v1`, `CalibrationPolicy`, `allowed_state_changes`, `familiar_domain`, `analogy_anchor`, and decision validation remain in the AI Study backend.

## Non-Goals

- Do not add AI Study-specific state rules to global prompts or shared query logic.
- Do not make `golang-claude-code` decide whether AI Study can draft a syllabus.
- Do not validate AI Study `analogy.used=true`; that belongs to AI Study's decision validator.
- Do not require tenant skill changes to restart the API server.
- Do not hand-edit generated Swagger files unless the public API schema changes and `swag init` is run.

## Routing Model

Add a structured tenant skill selector that resolves zero or more skill keys for a structured request.

Recommended priority:

1. Explicit request header.
2. Explicit request body metadata.
3. Current tenant settings.
4. Server environment config.
5. Built-in compatibility fallback.

The first matching source should record its selector source in telemetry and trace metadata.

### Request Header

Support:

```text
X-Tenant-Skill-Key: teach-v2
```

For multiple skills, prefer comma-separated values:

```text
X-Tenant-Skill-Key: teach-v2,review
```

Header routing is explicit and has the highest priority. It should still load only enabled effective skills for the current tenant/user through `TenantSkillProvider`.

### Request Body Metadata

Extend `OpenAIChatRequest` with optional metadata:

```go
Metadata map[string]any `json:"metadata,omitempty"`
```

Recognized keys:

```json
{
  "tenant_skill_key": "teach-v2",
  "tenant_skill_keys": ["teach-v2"]
}
```

This keeps compatibility with OpenAI-style clients that can pass request metadata but cannot easily add custom headers.

### Tenant Settings

Allow tenant-owned routing config in `tenants.settings_json`:

```json
{
  "structured_skill_routes": [
    {
      "schema_name": "teach_decision_v1",
      "skill_key": "teach-v2"
    },
    {
      "schema_name": "quiz_decision_v1",
      "skill_key": "quiz"
    }
  ]
}
```

Optional future fields:

```json
{
  "schema_name": "teach_decision_v1",
  "schema_name_prefix": "teach_",
  "message_contains": "teach_context_v1",
  "skill_key": "teach-v2"
}
```

P0 should implement exact `schema_name` first. Prefix and prompt text matching can stay P1 unless a real tenant needs it.

### Environment Config

Support a server-wide fallback for deployments that cannot or do not want to store routes in tenant settings:

```bash
GOLANG_CLAUDE_CODE_STRUCTURED_SKILL_ROUTES='[{"schema_name":"teach_decision_v1","skill_key":"teach-v2"}]'
```

This config should be parsed at server startup and kept immutable for the process lifetime.

### Built-In Compatibility Fallback

Keep the current AI Study-compatible behavior as a fallback route only when no explicit or configured route is present:

```json
[
  {"schema_name": "teach_decision_v1", "skill_key": "teach"}
]
```

This fallback does not reserve the `teach` name globally. It only preserves existing behavior for deployments that have not configured a structured route yet. New AI Study runtime data should configure `teach_decision_v1 -> teach-v2` through header, metadata, tenant settings, or environment config. Other tenants remain free to configure `teach` for their own structured routes. If prompt-text matching for `teach_context_v1` is retained for compatibility, isolate it behind the same selector abstraction and label telemetry as `builtin_compat`. Do not spread string matching across the OpenAI handler.

## Proposed Code Changes

### Server Request Shape

File:

```text
internal/server/server.go
```

Add:

```go
type OpenAIChatRequest struct {
    Model               string                 `json:"model"`
    Messages            []OpenAIMessage        `json:"messages"`
    Stream              bool                   `json:"stream,omitempty"`
    MaxTokens           int                    `json:"max_tokens,omitempty"`
    MaxCompletionTokens int                    `json:"max_completion_tokens,omitempty"`
    ResponseFormat      *OpenAIResponseFormat  `json:"response_format,omitempty"`
    Metadata            map[string]any         `json:"metadata,omitempty"`
}
```

### Selector Types

Create a small generic selector, either in `internal/server/server.go` near the OpenAI helpers or in a dedicated file such as:

```text
internal/server/structured_skill_routes.go
```

Suggested types:

```go
type StructuredSkillRoute struct {
    SchemaName string `json:"schema_name"`
    SkillKey   string `json:"skill_key"`
}

type StructuredSkillSelection struct {
    SkillKeys []string
    Source    string
}
```

Suggested selector:

```go
func selectStructuredTenantSkills(r *http.Request, req OpenAIChatRequest, opts Options) StructuredSkillSelection
```

The selector should:

- return no skills when `response_format.type` is not `json_schema`;
- normalize whitespace and deduplicate skill keys case-insensitively;
- reject empty skill keys;
- prefer explicit request routes over configured routes;
- never check whether a skill exists directly; existence remains the `TenantSkillProvider` job.

### Options

Extend server options with parsed config:

```go
type Options struct {
    StructuredSkillRoutes []StructuredSkillRoute
}
```

CLI server startup parses `GOLANG_CLAUDE_CODE_STRUCTURED_SKILL_ROUTES` into this field.

Tenant settings routes are resolved through the current `TenantService.ResolveContext()` result. The selector reads only `tenants.settings_json.structured_skill_routes`; it does not add AI Study-specific state or validation to the middle layer.

### OpenAI Query Request

Current flow:

```go
queryReq := openAIQueryRequest(req, opts.Workspace, systemPrompt, prompt)
```

Change to pass selected skill keys:

```go
selection := selectStructuredTenantSkills(r, req, opts)
queryReq := openAIQueryRequest(req, opts.Workspace, systemPrompt, prompt, selection.SkillKeys)
```

Keep `QueryRequest.InlineTenantSkills` generic. Do not introduce `TeachSkill` or AI Study naming into `QueryRequest`.

### Observability

Record selector evidence in trace / telemetry / manifest.

Recommended query request fields:

```go
InlineTenantSkills       []string
InlineTenantSkillSource  string
```

Recommended telemetry properties:

```text
tenant_runtime.active=true
tenant_runtime.resolved=true
tenant_skill_inline.active=true
tenant_skill_inline.skill_keys=teach-v2
tenant_skill_inline.selector=header|metadata|tenant_settings|env|builtin_compat
```

`tenant_context.active=true` is reserved for tenant addendum content such as memory,
profile, documents, or knowledge chunks entering the prompt. A structured request can
therefore have `tenant_runtime.active=true` and `tenant_skill_inline.active=true` while
`tenant_context.active=false`. `tenant_context.resolved=true` is only used when the
server tenant context path resolved tenant/user context for addendum construction.

When `query.Session.inlineTenantSkills` successfully loads a tenant skill, also expose:

```text
tenant_skill_inline.loaded=true
tenant_skill_inline.loaded_keys=teach-v2
tenant_skill_inline.versions=2
tenant_skill_inline.package_sha256=<package sha256>
tenant_skill_inline.package_refs=<file artifact ref>
tenant_skill_inline.runtime_refs=<file runtime ref>
tenant_skill_inline.bytes=<content bytes>
tenant_runtime.loaded_keys=teach-v2
tenant_runtime.versions=2
tenant_runtime.package_sha256=<package sha256>
tenant_runtime.package_refs=<file artifact ref>
tenant_runtime.runtime_refs=<file runtime ref>
```

OpenAI-compatible non-streaming responses should also expose the same safe runtime
summary in the non-standard `tenant_runtime` JSON field and `X-Tenant-Skill-*`
headers for both success and provider failure responses. Streaming responses cannot
reliably add headers after the model stream starts, so they should emit an
`object="tenant.runtime"` SSE data event before `[DONE]`; if the provider fails after
runtime metadata is known, emit the runtime event before the SSE error event.

Stable telemetry query paths:

```text
context_manifest.tenant_runtime.active
context_manifest.tenant_runtime.resolved
context_manifest.tenant_runtime.source
context_manifest.tenant_runtime.skill_keys
context_manifest.tenant_runtime.loaded_keys
context_manifest.tenant_runtime.versions
context_manifest.tenant_runtime.package_sha256
context_manifest.tenant_runtime.package_refs
context_manifest.tenant_runtime.runtime_refs
context_manifest.tenant_runtime.bytes
context_manifest.tenant_skill_inline.*
context_manifest.tenant_context.active
tenant_runtime.active
tenant_runtime.resolved
tenant_runtime.loaded_keys
tenant_runtime.versions
tenant_runtime.package_sha256
tenant_skill_inline.loaded_keys
tenant_skill_inline.versions
tenant_skill_inline.package_sha256
```

For provider-specific structured JSON failures containing
`JSON response_format generation abnormal` or `InternalError.Algo.InvalidParameter`,
the middle layer retries the same structured request once and records
`openai.structured_retry`. It does not remove `response_format`, switch models, or
apply caller-specific business rules by default.

If the selected skill is missing or disabled, record:

```text
tenant_skill_inline.loaded=false
tenant_skill_inline.error=not_found_or_disabled
```

Do not log full skill content.

2026-06-28 AI Study E2E review note: real traffic that still targeted the
older `127.0.0.1:18088` middle-layer instance persisted correct business state
and telemetry evidence. The AI Study conversation stayed in `calibration`,
created no syllabus when familiar domain was missing, and MySQL telemetry showed
`tenant_skill_inline.loaded_keys=["teach-v2"]`, `versions=["2"]`, the package
SHA256, artifact refs, runtime refs, and byte count. That validates structured
tenant skill injection. It does not validate the newer `tenant_runtime` response
extension because AI Study was not yet pointed at the `d6d98e7` process.

## AI Study Usage

AI Study should be able to call either:

```text
X-Tenant-Key: ai-study
X-User-Id: <ai-study user>
X-Trace-Id: <engine run trace id>
X-Tenant-Skill-Key: teach-v2
```

or rely on a configured route:

```json
{
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "teach_decision_v1",
      "strict": true,
      "schema": {}
    }
  }
}
```

AI Study tenant `teach-v2` skill content should describe the teaching contract and output format, but AI Study backend remains responsible for:

- building `teach_context_v1`;
- computing calibration state;
- choosing `allowed_state_changes`;
- validating decisions;
- advancing product state.

## Tenant Skill Data Update Safety

Before updating or migrating an AI Study tenant skill in MySQL, back up both the existing `teach` skill and any existing `teach-v2` skill:

```bash
mysql -uroot golang_cc_ai_study \
  -e "SELECT ts.* FROM tenant_skills ts JOIN tenants t ON t.id = ts.tenant_id WHERE t.tenant_key='ai-study' AND ts.skill_key IN ('teach','teach-v2') ORDER BY ts.skill_key, ts.version DESC\\G" \
  > /tmp/ai-study-teach-skill-before-$(date +%Y%m%d%H%M%S).txt
```

For SQL file changes in the AI Study repo, copy the bootstrap file before editing:

```bash
cp $HOME/Documents/ai-study/backend/config/golang-claude-code-ai-study-bootstrap.sql \
  $HOME/Documents/ai-study/backend/config/golang-claude-code-ai-study-bootstrap.sql.bak.$(date +%Y%m%d%H%M%S)
```

Do not commit local `.bak.*` files unless the user explicitly asks for backup artifacts to be versioned.

## Tests

### Unit Tests

Add server tests for selector behavior:

```bash
go test ./internal/server -run 'TestOpenAI.*Structured.*TenantSkill|TestStructuredTenantSkillRoute' -count=1
```

Coverage:

- non-structured request selects no inline skill;
- `X-Tenant-Skill-Key` wins over all other sources;
- metadata selects skill when header is absent;
- tenant settings select skill when header and metadata are absent;
- environment route selects by exact schema name;
- configured routes can map `teach_decision_v1` to `teach-v2`;
- built-in compatibility still maps `teach_decision_v1` to existing `teach` when no explicit or configured route is present;
- duplicate and empty skill keys are normalized away;
- selected skill keys are passed to `QueryRequest.InlineTenantSkills`;
- selector source is propagated for observability.

Keep existing query test:

```bash
go test ./internal/query -run TestSessionStructuredFastPathInlinesTenantSkillAndDisablesTools -count=1
```

### Real MySQL Runtime Smoke

Use an isolated or known local tenant database:

```bash
GOLANG_CLAUDE_CODE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_ai_study?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
go run ./cmd/golang-cc \
  --cwd /path/to/golang-cc \
  server --host 127.0.0.1 --port 8080 --auth-token test-token
```

Verify tenant runtime state:

```bash
curl -sS http://127.0.0.1:8080/tenant/effective-skills?enabled=true\&limit=50 \
  -H 'Authorization: Bearer test-token' \
  -H 'X-Tenant-Key: ai-study' \
  -H 'X-User-Id: 00000000-0000-0000-0000-000000000001'
```

Then call the structured path with explicit skill routing:

```bash
curl -sS http://127.0.0.1:8080/v1/chat/completions \
  -H 'Authorization: Bearer test-token' \
  -H 'Content-Type: application/json' \
  -H 'X-Tenant-Key: ai-study' \
  -H 'X-User-Id: 00000000-0000-0000-0000-000000000001' \
  -H 'X-Trace-Id: ai-study-structured-skill-smoke' \
  -H 'X-Tenant-Skill-Key: teach-v2' \
  -d @/tmp/ai-study-teach-decision-request.json
```

Acceptance evidence:

- `ai-study` tenant exists and is active.
- effective skill `teach-v2` exists and is enabled.
- the latest `teach-v2` skill version is visible.
- request telemetry or trace records selected skill key and selector source.
- query context manifest shows tenant context active.
- tenant session messages and usage ledger contain the smoke trace.
- no global prompt or AI Study business state machine was modified.

### Full Regression

After code changes:

```bash
go test ./... -count=1
git diff --check
```

If only this document changes:

```bash
git diff --check
```

## Rollout Plan

1. Add selector types and tests.
2. Add header and metadata selection.
3. Add environment route config.
4. Preserve built-in compatibility fallback behind the selector.
5. Add observability fields without logging skill content.
6. Run targeted tests and full Go regression.
7. Back up AI Study tenant `teach` and `teach-v2` skills.
8. Update AI Study tenant skill data if needed.
9. Run real MySQL tenant runtime smoke.
10. Commit and push scoped changes only.

## Open Questions

- Should tenant settings route support be P0, or is header + metadata + env enough for the first implementation?
- Should multiple inline tenant skills be allowed for structured requests, or should P0 enforce one skill to keep prompts deterministic?
- Should WebUI expose structured skill routes under tenant settings, or should this remain deployment/API config for now?
