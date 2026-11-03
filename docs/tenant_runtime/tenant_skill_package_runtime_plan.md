# Tenant Skill Package Runtime Plan

Related implementation plan:
[AI Study Skill Publish Control Plane Plan](ai_study_skill_self_publish_plan.md)
describes the middle-layer Web admin control plane for publishing new
`teach-v2` packages without manual CLI operation, while keeping API automation
available for CI and optional upper-layer shortcuts.

## Background

Tenant skills currently work well for single-file runtime instructions:

- `tenant_skills.content_md` stores the skill text used by the model.
- `/query` and normal chat paths expose tenant skill metadata first, then load full `content_md` through the `Skill` tool.
- structured JSON Schema fast paths disable tools, so selected tenant skills such as AI Study `teach-v2` must be inlined before the model call.

This is enough for P0 AI Study validation, but it is not a good long-term shape for multi-file skill packages that contain `SKILL.md`, supporting markdown files, examples, assets, and scripts.

## Goal

Define a generic tenant skill package mechanism that supports:

- multi-file skill packages;
- tenant-specific skill binding and versioning;
- Web UI display and editing;
- hot reload without server restart;
- reliable rollback and audit;
- structured fast path runtime injection;
- non-AI-Study tenants with their own skill keys and routing.

The middle layer should still own only provider routing, tenant skill loading, runtime injection, trace, telemetry, and package lifecycle. Product rules remain in the caller backend.

For AI Study specifically:

- `teach-v2` can be authored as a full package.
- runtime still receives a compiled `runtime.md` through the tenant skill mechanism.
- AI Study backend remains responsible for `teach_context_v1`, `CalibrationPolicy`, `allowed_state_changes`, and decision validation.

## Non-Goals

- Do not execute arbitrary skill scripts during request handling.
- Do not store binary assets directly in MySQL.
- Do not make a filesystem tenant directory the only production source of truth.
- Do not add AI Study-specific state machine logic to package import or runtime.
- Do not require server restart after publishing a new skill version.
- Do not implement S3/OSS package storage in the first phase.

## Implementation Priority

The first implementation phase should build the complete package lifecycle on local infrastructure, not a partial backend-only prototype.

Phase 1 must include:

- local artifact storage for original `*.skill.zip`, extracted manifest, and rendered `runtime.md`;
- MySQL metadata and tenant binding, continuing to reuse `tenant_skills` as the request runtime source;
- package validation, path safety, hash calculation, manifest generation, and deterministic render;
- import/render/publish APIs and CLI;
- Web UI file tree display, text editing, render preview, publish, rollback, and version history;
- structured route update for schema-to-skill binding;
- hot reload through MySQL version/hash changes without server restart;
- end-to-end tests from package import to `/v1/chat/completions` structured fast path injection.

Current Phase 1 implementation status:

- Implemented local artifact store under `~/.golang-claude-code/tenant-skill-packages` or `GOLANG_CLAUDE_CODE_TENANT_SKILL_PACKAGE_DIR`, organized by `<tenant_key>/<skill_key>/<package_sha256>/`.
- Implemented zip/directory validator with path safety checks, symlink rejection, file count/size limits, allowed extensions, deterministic manifest generation, and deterministic `runtime.md` rendering from `SKILL.md` plus runtime-safe text files. `assets/` and `scripts/` are preserved in the artifact but excluded from request runtime.
- Implemented Option A migration by adding nullable `package_ref`, `package_sha256`, `manifest_json`, and `runtime_ref` columns to `tenant_skills`; `content_md` remains the request hot-path runtime.
- Implemented API and CLI render/publish flows, while list/history/rollback continue to use tenant skill version rows and rollback semantics.
- Implemented Web UI minimum flow for package source path, render preview, publish, version history, and rollback.
- Structured fast path telemetry now records loaded skill key/version/bytes plus package hash/ref/runtime ref, without logging skill content.

Real AI Study data review on 2026-06-28 confirmed the Phase 1 runtime data model
is coherent:

- AI Study backend was configured to call `http://127.0.0.1:18088/v1`, which was
  running `/path/to/golang-cc` at `49f30cd`, not
  the newer `d6d98e7` process started on `127.0.0.1:8080`.
- The real REST E2E run persisted `engine_runs.skill_key=teach-v2` and the
  conversation remained in `calibration` when the learner had no familiar domain.
  No `syllabi` rows were created for those calibration-only conversations.
- Middle-layer MySQL telemetry for the same trace ids showed
  `tenant_skill_inline.loaded_keys=["teach-v2"]`, `versions=["2"]`,
  `package_sha256=["5e8c4b60e9193672e73b54c3ecc651b48778e0d89573ae5eb8b6d1bf1e39208b"]`,
  and `bytes=17497`, proving the structured fast path injected the published
  package runtime without logging skill content.
- AI Study `engine_runs.skill_version=1` is still the model-output field from
  the business decision JSON, not the middle-layer loaded runtime version. The
  caller should store the safe `tenant_runtime` response metadata or
  `X-Tenant-Skill-*` headers after it points to a middle-layer build that includes
  `d6d98e7`.

Phase 2 should focus on hardening after the local chain is proven:

- richer package history and package reuse model;
- optional `tenant_skill_packages` table if package management becomes first-class;
- package diff UX, validation reports, and better rollback comparisons;
- short runtime cache keyed by tenant/user/skill/version/hash if MySQL reads become expensive;
- operational scripts for backup and restore of MySQL plus local artifact directory.

Phase 3 is storage optimization:

- add S3/OSS/signed HTTPS package stores behind the same `package_ref` abstraction;
- add multi-instance artifact synchronization or remote fetch;
- keep runtime behavior unchanged so storage migration does not affect model injection.

This order is intentional: first prove the architecture and product workflow with local artifacts, then improve storage. There is currently no free S3/OSS dependency available, and adding one before the package lifecycle works would increase complexity without improving the core validation.

## Recommended Architecture

Use a hybrid model:

```text
Source package
  - SKILL.md
  - docs/*.md
  - examples/*.json
  - assets/*
  - scripts/*

Package store
  - original zip or unpacked artifact
  - manifest.json
  - package sha256

MySQL tenant runtime
  - tenant binding
  - version
  - enabled flag
  - runtime_content_md or runtime_ref
  - manifest_json
  - package_ref
  - package_sha256

Request runtime
  - resolve tenant + user
  - resolve structured route or Skill tool load
  - load compiled runtime markdown
  - record trace and telemetry
```

The important split:

- package storage keeps the full source shape;
- MySQL decides which tenant uses which skill version;
- runtime uses compiled markdown, not the whole package directory.

## Package Artifact Storage

The original `*.skill.zip` should not be stored directly in `tenant_skills.content_md`. Phase 1 should store package artifacts outside MySQL in a local artifact directory and keep only references, hashes, manifest, and compiled runtime markdown in MySQL.

Recommended Phase 1 local artifact directory:

```text
~/.golang-claude-code/tenant-skill-packages/
  <tenant_key>/
    <skill_key>/
      <package_sha256>/
        package.skill.zip
        manifest.json
        runtime.md
```

For service deployments, make the root configurable:

```bash
GOLANG_CLAUDE_CODE_TENANT_SKILL_PACKAGE_DIR=/data/golang-claude-code/tenant-skill-packages
```

MySQL stores references:

```json
{
  "package_ref": "file:///data/golang-claude-code/tenant-skill-packages/ai-study/teach-v2/<sha>/package.skill.zip",
  "runtime_ref": "file:///data/golang-claude-code/tenant-skill-packages/ai-study/teach-v2/<sha>/runtime.md",
  "package_sha256": "<sha>",
  "manifest_json": {
    "schema_version": "tenant_skill_package_v1"
  }
}
```

`tenant_skills.content_md` should still contain the compiled runtime markdown in Phase 1. This keeps request-time runtime simple and resilient even if the package store is temporarily unavailable. `runtime_ref` is useful for audit and Web preview, not required for hot-path model injection in Phase 1.

### Why Not Store Zip in MySQL

- A zip may contain assets, examples, scripts, and binary files that are not request runtime data.
- Large blobs make database backups, migrations, replication, and row inspection heavier.
- Runtime does not need the zip; it needs deterministic markdown.
- Web diff and review are easier against manifest plus extracted text files than a DB blob.

### Why Local Artifact Directory First

Local artifact storage is the recommended Phase 1 default because it:

- avoids adding S3/OSS credentials and SDK dependencies before the package lifecycle is proven;
- works well for local development and single-node deployments;
- is easy to back up together with MySQL during early rollout;
- keeps the same `package_ref` abstraction that can later point to object storage.

### Production Object Storage

Phase 3 can move package artifacts to object storage and keep MySQL references unchanged:

```text
s3://golang-claude-code-skill-packages/tenants/ai-study/teach-v2/<sha>/package.skill.zip
oss://golang-claude-code-skill-packages/tenants/ai-study/teach-v2/<sha>/package.skill.zip
https://artifact.example.com/tenants/ai-study/teach-v2/<sha>/package.skill.zip
```

The runtime should treat `package_ref` as an opaque URI. Phase 1 should support only `file://`; Phase 3 can add `s3://`, `oss://`, or signed `https://` fetchers behind the same interface.

## Why Not Only Tenant Directories

A per-tenant directory is useful for local development, but it should not be the only production runtime store.

Problems with directory-only runtime:

- multi-instance servers need directory synchronization;
- container filesystems may be read-only or ephemeral;
- Web edits must write files safely and handle concurrent changes;
- hot reload needs many watchers and still needs distributed invalidation;
- audit, rollback, user overrides, and version locks are harder than MySQL rows;
- backups must combine DB and filesystem snapshots consistently.

Recommended use of directories:

- local authoring;
- local dev preview;
- optional artifact cache;
- import source for a package publish operation.

Production runtime should be driven by MySQL bindings plus package artifacts.

## Data Model

P0 can keep the current `tenant_skills` table and store compiled runtime markdown in `content_md`.

Phase 1 should add package metadata and a local artifact store. Two implementation shapes are possible.

### Option A: Extend tenant_skills

Add nullable columns:

```sql
ALTER TABLE tenant_skills
  ADD COLUMN package_ref VARCHAR(1024) NULL,
  ADD COLUMN package_sha256 CHAR(64) NULL,
  ADD COLUMN manifest_json JSON NULL,
  ADD COLUMN runtime_ref VARCHAR(1024) NULL;
```

Use existing fields:

- `skill_key`: stable tenant-visible key, for example `teach-v2`;
- `version`: runtime version;
- `content_md`: compiled runtime markdown, unless `runtime_ref` is used;
- `package_ref`: immutable URI of the original package zip or package artifact;
- `package_sha256`: hash of the original package artifact;
- `manifest_json`: normalized package manifest and file hashes;
- `runtime_ref`: URI of rendered runtime markdown for preview/audit;
- `config_json`: backward-compatible metadata and source labels.

Pros:

- smaller migration;
- existing API and runtime can keep working;
- rollback model stays simple.

Cons:

- source package lifecycle is mixed into runtime skill rows;
- large metadata may make the table heavier.

### Option B: Add tenant_skill_packages

Add a separate package table:

```sql
CREATE TABLE tenant_skill_packages (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  tenant_id BIGINT UNSIGNED NOT NULL,
  package_key VARCHAR(128) NOT NULL,
  package_version VARCHAR(64) NOT NULL,
  package_ref VARCHAR(1024) NOT NULL,
  package_sha256 CHAR(64) NOT NULL,
  manifest_json JSON NOT NULL,
  runtime_content_md LONGTEXT NULL,
  runtime_ref VARCHAR(1024) NULL,
  created_by_user_id BIGINT UNSIGNED NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uk_tenant_skill_packages_version (tenant_id, package_key, package_version)
);
```

Then `tenant_skills` points to the published package:

```sql
ALTER TABLE tenant_skills
  ADD COLUMN package_id BIGINT UNSIGNED NULL;
```

Pros:

- clean separation between source package and runtime skill binding;
- better for Web UI file tree and package history;
- easier to support multiple skill bindings from one package.

Cons:

- larger migration and service/API work;
- more joins and import flow complexity.

Recommendation:

- P0: current `tenant_skills.content_md`.
- Phase 1: Option A if speed matters.
- Phase 2: Option B if package management becomes a first-class product surface.

## Package Manifest

Every imported package should produce a normalized manifest:

```json
{
  "schema_version": "tenant_skill_package_v1",
  "package_key": "teach-v2",
  "entrypoint": "SKILL.md",
  "runtime_entrypoint": "runtime.md",
  "package_sha256": "...",
  "files": [
    {
      "path": "SKILL.md",
      "kind": "markdown",
      "sha256": "...",
      "runtime_included": true
    },
    {
      "path": "docs/calibration.md",
      "kind": "markdown",
      "sha256": "...",
      "runtime_included": true
    },
    {
      "path": "assets/diagram.png",
      "kind": "asset",
      "sha256": "...",
      "runtime_included": false
    },
    {
      "path": "scripts/render.mjs",
      "kind": "script",
      "sha256": "...",
      "runtime_included": false
    }
  ],
  "frontmatter": {
    "description": "...",
    "when_to_use": "...",
    "allowed_tools": [],
    "model": "inherit",
    "context": ""
  }
}
```

Rules:

- `SKILL.md` is always the runtime entrypoint.
- If `package.yaml` or `package.yml` declares `runtime_includes` or `runtime_excludes`, the renderer uses those rules to decide supporting runtime files.
- If no runtime include/exclude rules are declared, the renderer keeps the legacy compatibility behavior and includes runtime-safe text files by extension, except package metadata files.
- Only markdown/text/YAML/JSON files selected by the renderer become runtime content.
- Runtime rule patterns are relative package paths. Exact paths, directory prefixes ending in `/`, standard `*` globs, and `**` cross-directory globs are supported.
- `runtime_excludes` wins over `runtime_includes`.
- `package.yaml` and `package.yml` are package metadata and are never included in runtime content.
- Assets are stored as package files and referenced by manifest, not inlined into MySQL.
- Scripts are package tooling, not request-time execution.
- Manifest paths must be normalized relative paths and must not contain `..`.
- Package and file hashes are required for reproducibility and audit.

Example package runtime manifest:

```yaml
runtime_includes:
  - runtime.md
  - docs/*.md
  - examples/decision.json
runtime_excludes:
  - docs/release-notes.md
```

## Rendering Runtime Markdown

Introduce an import/render step:

```text
skill package -> validate -> render -> runtime.md -> publish to tenant skill version
```

Rendering responsibilities:

- read `SKILL.md`;
- parse frontmatter;
- include selected supporting markdown/examples;
- omit assets and scripts unless explicitly converted to text;
- produce deterministic `runtime.md`;
- record included files and hashes in manifest.

The runtime markdown should be the only content injected into structured fast path prompts.

For AI Study `teach-v2`, the package can contain many files, but the published runtime should be a concise teaching decision contract:

```text
runtime.md
  - role and runtime contract
  - Calibration Slots
  - Knowledge Transfer Teaching
  - output schema guidance
  - short examples if needed
```

Large examples, images, local validation scripts, and authoring notes should stay in the package store and CI, not in request prompts.

## 2026-06-28 Runtime Rule Validation

After adding `package.yaml` runtime rule support, the middle-layer package lifecycle was validated with unit tests, MySQL golden tests, and a real local API publish flow.

Regression and golden commands:

```bash
go test ./internal/tenantpkg -count=1
go test ./internal/storage/mysql -run 'TestIsRetryableMySQLTransactionError|TestRepositoryRollbackSkillVersion' -count=1
go test ./internal/server -run 'TestTenantSkillPackage|Test.*SkillPackage|TestMySQLE2ETenantSkillPackageVerifyRuntime|TestMySQLE2ETenantSkillRollbackConcurrent' -count=1
go test ./... -count=1
scripts/tenant-mysql-e2e.sh
git diff --check
```

Real local publish flow:

1. Created an isolated MySQL database `golang_cc_publish_e2e`.
2. Ran tenant migrations.
3. Started a real API Server with:

```bash
GOLANG_CLAUDE_CODE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_publish_e2e?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
GOLANG_CLAUDE_CODE_TENANT_SKILL_PACKAGE_DIR=/tmp/gcc-package-store-e2e \
go run ./cmd/golang-cc server --host 127.0.0.1 --port 18089 --auth-token publish-e2e-token
```

4. Used HTTP API calls against the real server:

```text
PATCH /tenant/user
POST  /tenant/skill-packages/import
POST  /tenant/skill-packages/publish
POST  /tenant/skill-packages/verify-runtime
```

The test package included these files:

```text
SKILL.md
package.yaml
runtime.md
README.md
docs/contract.md
docs/release-notes.md
examples/decision.json
examples/large.json
```

with this package runtime rule:

```yaml
runtime_includes:
  - runtime.md
  - docs/*.md
  - examples/decision.json
runtime_excludes:
  - docs/release-notes.md
```

Observed import and publish result:

```text
runtime_files:
  - SKILL.md
  - docs/contract.md
  - examples/decision.json
  - runtime.md
runtime_includes:
  - runtime.md
  - docs/*.md
  - examples/decision.json
runtime_excludes:
  - docs/release-notes.md
```

The published artifact runtime at `runtime_ref` contained `SKILL.md`, `docs/contract.md`, `examples/decision.json`, and `runtime.md`. It did not contain `README.md`, `docs/release-notes.md`, `examples/large.json`, or `package.yaml` metadata.

MySQL evidence:

- `tenant_skills` contained `teach-v2-publish-e2e` version `1`, package sha `badf4a0fdde00d8efe4337581a990b64dc2df064feaef6a8866c38dce4a46837`, and a non-empty `runtime_ref`.
- `tenant_audit_logs` contained `tenant.skill_package.publish`.
- `tenant_telemetry_events` contained started/finished API events for `/tenant/skill-packages/import`, `/tenant/skill-packages/publish`, and `/tenant/skill-packages/verify-runtime`.
- `/tenant/skill-packages/verify-runtime` loaded the expected tenant runtime metadata: `loaded_keys=["teach-v2-publish-e2e"]`, `versions=["1"]`, and the expected package sha.

Validation boundary:

- The `verify-runtime` request reached the model provider and failed with provider HTTP 403 because the local token did not have access to `claude-sonnet-4-6`.
- This provider authorization failure did not prevent verifying runtime resolution, package hash, version, runtime ref, and telemetry persistence for the publish flow.

## API and CLI

Phase 1 APIs:

```text
POST /tenant/skill-packages/import
GET  /tenant/skill-packages
GET  /tenant/skill-packages/{id}
GET  /tenant/skill-packages/{id}/files
POST /tenant/skill-packages/{id}/publish
```

Phase 1 CLI:

```bash
golang-cc tenant skill-package import ./teach-v2.skill.zip \
  --tenant ai-study \
  --skill-key teach-v2 \
  --version 3

golang-cc tenant skill-package publish teach-v2 \
  --tenant ai-study \
  --schema-name teach_decision_v1
```

Publish should:

1. validate package structure and manifest;
2. render runtime markdown;
3. create a new tenant skill version;
4. optionally update tenant `settings_json.structured_skill_routes`;
5. emit audit and telemetry events.

## Web UI Behavior

Web UI is part of Phase 1. It should not edit a live row in place.

Recommended flow:

1. List tenant skills and current package metadata.
2. Show package file tree from manifest.
3. Allow editing text files only.
4. Save edits as a draft package version.
5. Run validation/render preview.
6. Publish creates a new immutable tenant skill version.
7. Rollback selects an existing version and publishes/copies it as the new active version.

This keeps Web UI changes compatible with hot reload and audit.

## Hot Reload

Hot reload should be version/hash based, not directory watcher based.

Runtime lookup:

```text
tenant_key + user_id + skill_key -> effective skill latest version -> runtime content
```

Cache policy:

- P0: no process cache; each request reads MySQL.
- Phase 2: short in-process cache keyed by `tenant_id:user_id:skill_key:version:sha`.
- Publish invalidation: version or sha change is enough for next request to miss cache.
- Do not require server restart.

Structured fast path telemetry must record:

- selector source;
- requested skill keys;
- loaded skill keys;
- versions;
- bytes;
- missing/error state.

It must not record full runtime content.

## Security

Package import must enforce:

- max zip size;
- max file count;
- max runtime markdown size;
- allowed file extensions;
- path traversal rejection;
- no symlinks unless explicitly supported;
- artifact path derived from `tenant_key`, `skill_key`, and `package_sha256`, never from raw zip entry names;
- atomic artifact writes, for example write to a temp directory and rename after hash validation;
- script files are stored but never executed during request handling;
- manifest and runtime content hashes;
- tenant/user authorization through existing tenant service roles.

Request runtime must enforce:

- current tenant/user effective skill lookup only;
- enabled skill only;
- structured fast path remains tool-disabled;
- no package scripts or assets are loaded into runtime unless converted during publish.

## Observability

Add events:

```text
tenant.skill_package.import.started
tenant.skill_package.import.finished
tenant.skill_package.render.finished
tenant.skill_package.publish.finished
tenant.skill.inline.loaded
```

Properties should include:

- tenant key/id;
- skill key;
- version;
- package sha;
- runtime bytes;
- included file count;
- status/error.

Do not log package content, runtime markdown, secrets, or uploaded binary data.

## AI Study Recommended Rollout

### Current Working Shape

Use the current MySQL runtime directly:

- keep `teach` as an existing skill;
- create/update `teach-v2` in `tenant_skills`;
- set `ai-study.settings_json.structured_skill_routes` to `teach_decision_v1 -> teach-v2`;
- put the compiled runtime markdown in `tenant_skills.content_md`;
- keep the full package source in AI Study repo or another source-controlled location.

This is the simplest reliable shape for immediate verification.

### Phase 1: Local Package Runtime and Web Workflow

Build the complete local package lifecycle so AI Study can hand off:

```text
teach-v2.skill.zip
```

Phase 1 must include:

- local artifact storage for `teach-v2.skill.zip`, manifest, and `runtime.md`;
- package import and validation;
- deterministic render into runtime markdown;
- publish into a new `tenant_skills` version;
- optional route update to `teach_decision_v1 -> teach-v2`;
- Web UI file tree display;
- Web UI text editing for markdown/json/yaml/txt files;
- render preview and validation feedback;
- publish and rollback from Web UI;
- end-to-end verification that `/v1/chat/completions` structured fast path injects the published `teach-v2`.

This phase intentionally uses local artifact storage because no free OSS/S3 dependency is available and the main risk is package lifecycle correctness, not remote storage.

### Phase 2: Hardening

After Phase 1 works end to end:

- improve package diff and review UX;
- add package history search and richer validation reports;
- consider a separate `tenant_skill_packages` table if packages become first-class;
- add short runtime cache if MySQL reads become expensive;
- document backup/restore of MySQL plus local artifact directory.

### Phase 3: OSS/S3 Storage Optimization

Only after local package lifecycle and Web workflow are stable:

- add `s3://`, `oss://`, or signed `https://` package refs;
- support multi-instance artifact fetching;
- keep the MySQL/runtime contract unchanged so storage migration does not change prompt injection behavior.

## Decision

Current `content_md` storage is the right starting point because it is simple, hot-loadable, testable, and already works with structured fast path.

It is not the best final design for multi-file packages.

The recommended final design is:

```text
package store for full source package
+ MySQL for tenant binding/version/effective runtime metadata
+ compiled runtime markdown for request-time model injection
```

This keeps runtime deterministic and production-safe while preserving a good authoring and Web UI experience.

First implementation should deliver the full local chain, including Web UI, and postpone OSS/S3 until Phase 3.
