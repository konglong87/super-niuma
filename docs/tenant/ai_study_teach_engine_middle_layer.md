# AI Study Teach Engine Middle Layer

This note records the API-server-side contract for serving AI Study and similar structured upper-layer apps through `/v1/chat/completions`.

## Request Path

AI Study calls the OpenAI-compatible endpoint with:

- `X-Tenant-Key: ai-study`
- `X-User-Id: <ai-study user id>`
- `X-Trace-Id: <engine run trace id>`
- `response_format.type=json_schema`
- schema name `teach_decision_v1`

When `response_format.type=json_schema` is present, the server uses the structured fast path:

- exactly one query turn by setting `max_turns=1`;
- no runtime tools, so no `Skill`, `Task`, MCP, shell, or self-review loop can run;
- no automatic session title model call;
- provider-level JSON Schema passthrough for OpenAI-compatible providers;
- normal telemetry, tenant session/message persistence, and usage ledger settlement remain enabled.

For AI Study, the fast path inlines the tenant `teach` skill into the system prompt because tools are disabled. This preserves hot-swappable tenant behavior without paying for an extra tool/model turn. Updating `/tenant/skills` or `/tenant/skill-overrides` takes effect on the next request.

## Teach Skill Contract

The `ai-study` tenant `teach` skill content must explicitly require:

```text
skill_version must be integer 1. Never output "unknown".
decision_schema_version must be teach_decision_v1.
contract_version must be teach_context_v1.
profile_item_upsert.payload must contain item_type, concept, note.
Do not output key, value, or source inside profile_item_upsert.payload.
Only output state_changes allowed by teach_context_v1.allowed_state_changes.
```

This contract belongs in tenant skill data so different upper-layer apps can evolve their own schemas without hardcoding app logic into the server.

## Verification

Use `TEACH_ENGINE_RESPONSE_FORMAT=json_schema` on the AI Study side. Then confirm:

- `/tenant/usage/ledger?search=<trace_id>` returns the request with duration and tokens.
- `/tenant/telemetry?search=<trace_id>` includes `api.request.*`, `query.run.*`, and exactly one `model.request.finished` for the main decision.
- `tenant_session_messages` contains user and assistant messages for the trace.
- AI Study `ValidateLLMDecision` accepts the returned JSON without repair retry.
