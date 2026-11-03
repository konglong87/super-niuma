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
