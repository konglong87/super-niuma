# Controlled diagnostic recheck - preregistration

This is a separate, explicitly authorized follow-up to the failed original trial. The original receipt and one-shot marker are preserved. It is not an automatic retry and cannot be rerun after this report is claimed.

- Authorization basis: the user requested full implementation and real end-to-end acceptance; the parent agent permits exactly ONE additional bounded diagnostic trial after the known-usage bug and review blockers are closed.
- Existing primary route only: custom / glm-5.2; pinned fingerprint remains unchanged. No credential, URL, or response body is recorded.
- Maximum total budget: 40,000 tokens; timeout: 180 seconds; at most five model requests across one DAG; no job retry or verifier-driven revision.
- Explicit changed input: each node allows at most 1,024 output tokens (the original analyses allowed 256 and writer 512). This does not assert truncation caused the original failure. The new safe diagnostics distinguish known accounting from execution acceptance.
- Acceptance contract remains exact: two parallel real analyses, dependent tool-authored artifact, trusted current-attempt write receipt, exact independent SHA-256 check, durable bus/outbox local Desktop receipt, and restart readback. No output or expected result is prewritten into the fixture.
- This still does NOT substitute for live Feishu transport or native UI plus model on the same run.
- Prerequisite: final offline race tests and security-review findings closed. Default test runs skip; both opt-in variables are required.

A zero-network configuration probe during concurrent integration edits did not compile because the administrative repository implementation was mid-edit. It made no model request and did not claim the paid marker. Repeat only after the frozen build succeeds.

<!-- super-niuma-real-model-paid-attempt-claimed -->

Paid trial claimed. Outcome pending; do not rerun automatically.

# Super Niuma — real-model control-plane acceptance

## Outcome and boundary

- Outcome: **failed**. Failure code: analysis_model_or_usage_failed.
- Exactly one authorized work order; no automatic second paid attempt or revision.
- This evidence covers real model responses, SQLite intake/control state, two read-only analysis workers, a dependent writer, trusted write receipts, independent semantic file verification, durable event/outbox delivery to DesktopSender, and restart readback **only where the flags below confirm success**.
- DesktopSender is the local receipt implementation, not native UI interaction or human acknowledgement. This is **not** the final unified Feishu + native screenshot + model end-to-end acceptance.
- Model-request counts are observed successful budget admissions; confirmed response counts require known usage and successful settlement. An unconfirmed admission is not claimed to prove a provider response. Parallel windows are admission-to-settlement observations, not packet-level traces.
- Public attempt/request/claim references are SHA-256 correlation hashes, not authority tokens. No credentials, URLs, raw model text, raw error bodies, database or logs are retained. The private HOME fixture is removed after closing SQLite.
- Opt-in: SUPER_NIUMA_REAL_MODEL_ACCEPTANCE=1. Default skip means **not accepted**, not a pass. The append-only receipt marker is a one-shot guard; do not remove it to retry without new authorization.
- Native app execution/screenshots and external-channel acceptance remain the main agent's responsibility. No commit or push performed by this task.

## Sanitized receipt

```json
{
  "admitted_model_requests": 2,
  "budget_accounted": 13822,
  "budget_limit": 40000,
  "configuration": {
    "configured": true,
    "model": "glm-5.2",
    "provider": "custom",
    "route_fingerprint": "eae79ff5b9fd15b22e4fac29f46e20583d8940f099106876d9ff9027b5369fe6"
  },
  "confirmed_model_responses": 0,
  "database_closed": true,
  "deadline_seconds": 180,
  "desktop_send_count": 0,
  "durable_notice_acknowledged": false,
  "executor_attempts": 2,
  "failure_code": "analysis_model_or_usage_failed",
  "finished_at": "2026-11-03T09:10:58.383307Z",
  "fixture_removed": true,
  "freshness_verified": false,
  "intake_id": "c1696bb0-f9be-44fa-b810-e4ead5ece97e",
  "jobs": [
    {
      "attempt_ref": "sha256:bb08c4aca288cff6f2c78cb51048f51a72fefe42aec251076b9ddff45feb8d56",
      "attempts": 1,
      "diagnostic": {
        "body_state": "unobserved",
        "code": "execution_http_rate_limited",
        "completion_kind": "unknown",
        "content_kind": "unknown",
        "failure_stage": "http",
        "http_status": 429,
        "media_type": "json",
        "request_index": 1,
        "usage_state": "not_observed"
      },
      "id": "analyze-sum",
      "max_output_tokens": 1024,
      "safe_code": "execution_http_rate_limited",
      "status": "failed",
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false
    },
    {
      "attempt_ref": "sha256:b6193c7322b7a193ddd4bfa86fb2080e09109fb92abe4fb4b7a25ea3d9235e51",
      "attempts": 1,
      "diagnostic": {
        "body_state": "unobserved",
        "code": "execution_cancelled",
        "completion_kind": "unknown",
        "content_kind": "unknown",
        "failure_stage": "network",
        "media_type": "unknown",
        "request_index": 1,
        "usage_state": "not_observed"
      },
      "id": "analyze-product",
      "max_output_tokens": 1024,
      "safe_code": "root_stopped",
      "status": "failed",
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false
    },
    {
      "attempt_ref": "",
      "attempts": 0,
      "id": "write-artifact",
      "max_output_tokens": 1024,
      "status": "queued",
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false
    }
  ],
  "model_attempt_started": true,
  "outcome": "failed",
  "parallel_analysis_observed": false,
  "parallel_model_reservation_windows_observed": false,
  "requests": [
    {
      "attempt_ref": "sha256:bb08c4aca288cff6f2c78cb51048f51a72fefe42aec251076b9ddff45feb8d56",
      "job_id": "analyze-sum",
      "request_ref": "sha256:f743830299d4eeafaaf4c5062861394941ab4478c519eac3aecd24bdd2b2b231",
      "reserved_input": 5883,
      "reserved_output": 1024,
      "settled": true,
      "settled_at_ns": 1793697058361756000,
      "started_at_ns": 1793697057967936000,
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false
    },
    {
      "attempt_ref": "sha256:b6193c7322b7a193ddd4bfa86fb2080e09109fb92abe4fb4b7a25ea3d9235e51",
      "job_id": "analyze-product",
      "request_ref": "sha256:0efa72166f8b6f21c471bdf8784335ec0b41a107d796c3f180b03d635143e55b",
      "reserved_input": 5891,
      "reserved_output": 1024,
      "settled": true,
      "settled_at_ns": 1793697058363555000,
      "started_at_ns": 1793697057975272000,
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false
    }
  ],
  "restart_persistence_verified": false,
  "run_id": "f564c76b-a5ca-493d-a27e-a7d62954f126",
  "semantic_json_verified": false,
  "started_at": "2026-11-03T09:10:57.886121Z",
  "status": "needs_attention",
  "trial": "diagnostic-recheck-v1",
  "usage": {
    "input_tokens": 0,
    "output_tokens": 0
  },
  "verification_attempt_ref": ""
}
```
