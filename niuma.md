# niuma.md

This file provides guidance to niuma when working in this repository.

## Project Goal

niuma is a Go-based agent runtime with shared query execution, TUI, WebUI2,
Desktop-v2, tenant APIs, and resumable session transcripts.

## Architecture Rules

- Keep query execution independent from transcript storage.
- Use the session repository and session-control interfaces instead of coupling
  callers directly to JSONL or SQLite implementations.
- JSONL is the default authoritative transcript backend for Desktop-v2.
- SQLite is an alternate authoritative backend for rollback and validation.
  Only one backend is active per process; dual-write behavior is forbidden.
- Keep tenant memory, project memory, and transcript events as separate
  responsibilities.
- Prefer small, composable changes over broad refactors.

## Important Areas

- `internal/query`: model execution and prompt/context assembly
- `internal/session`: transcript format, JSONL storage, resume, and compact
- `internal/sessioncontrol`: session backend abstraction
- `internal/memory`: project rules and memory loading
- `internal/server`: tenant context and API server
- `web/src/v2`: Desktop-v2 and WebUI2 frontend
- `desktop-v2`: native Wails packaging and service lifecycle

## Rules and Memory

- The project instruction file is `niuma.md`.
- Code mode loads project rules and project memory.
- Chat mode intentionally does not load local code-development rules.
- `MEMORY.md` is an index; linked memory files are recalled only when relevant.
- Rules and memory are loaded dynamically and are not snapshotted into the
  transcript.
- Do not put secrets, credentials, or temporary debugging notes in this file.

## Change Workflow

1. Inspect the existing implementation and call path before editing.
2. Keep public interfaces stable and isolate backend-specific behavior.
3. Add focused tests for every behavior change.
4. Run affected package tests before the broad test suite.
5. Run `git diff --check`.
6. Verify storage, resume, compact, and restart behavior when session code
   changes.
7. Verify the native Wails UI with an end-to-end flow when Desktop-v2 changes.

## Verification Gates

- `go test ./internal/memory ./internal/session ./internal/query ./internal/server`
- `go test ./...`
- `git diff --check`
- JSONL mode: real conversation, long conversation, resume, compact, restart
- SQLite mode: real conversation, resume, compact, restart, and rollback
- Desktop-v2: native window screenshot verification for user-facing changes

## Known Boundaries

- The transcript stores conversation events, not a permanent snapshot of rules
  or memory.
- Resuming a session can observe the current versions of project rules and
  memory files.
- Tenant memory remains tenant-scoped and must not be mixed into local project
  memory.
