# Failed Audit Tool Result Closure Fix Plan

## Problem

The July 10 Anything-AI audit exposed a two-layer failure:

1. Two broad Bash audit commands failed before execution because the static shell guard treated `2>/dev/null` as an out-of-workspace write to `/dev/null`.
2. The final answer still presented full-scope conclusions such as "data is complete" and "the only directory with one file" even though the failed broad commands were never replaced by an equivalent successful broad audit.

This is not only a shell compatibility bug. It is a closure bug: failed evidence can be visible in tool results but not enforced as a constraint on final synthesis.

## Root Cause

### Shell Guard

`internal/tools/bash/bash.go` calls `sandbox.CheckShellCommand` before execution. `internal/sandbox/command.go` checks all write redirects with `tools.EnsureWritablePath`. That is correct for files, but `/dev/null` is a null device, not project state. Treating it as a workspace write blocks common read-only audit patterns:

```bash
find . -name "*.md" 2>/dev/null | wc -l
```

### Completion Gate

`internal/query/closure_gate.go` currently checks whether any read/search evidence exists, whether broad claims have broad evidence, and whether test/commit/push claims have matching evidence. It does not check this invariant:

> If a broad read/search/audit command failed, a final whole-scope conclusion must either replace it with a later equivalent broad successful audit, or explicitly disclose the failure and bound the conclusion.

The existing `final_claim` gate is too coarse for this case because later scoped `find` commands count as read evidence, even when they do not replace the failed broad summary command.

## Fix Strategy

### 1. Allow Safe Null-Device Redirects

Add a shell-guard exception for static redirects to `/dev/null`.

Rules:

- Allow exact cleaned path `/dev/null` for shell redirection targets.
- Do not allow `/dev/zero`, `/dev/random`, `/tmp/...`, symlink escapes, or dynamic redirect targets.
- Keep all existing write-path checks for normal files and mutating commands.
- Cover nested shell commands because `checkShellCommand` recursively parses `sh -c` / `bash -c`.

Tests:

- `CheckShellCommand` allows `2>/dev/null`, `>/dev/null`, and nested `sh -c '... 2>/dev/null'`.
- Existing outside-write and dynamic-redirection tests remain unchanged.

### 2. Add Failed Audit Evidence Gate

Add a completion gate before `final_claim`:

`failed_audit_evidence`

Trigger:

- Current task is an investigation/audit, or the final answer claims audit/search/comparative whole-scope synthesis.
- At least one failed broad read/search/audit tool exists in this turn.
- No later successful broad read/search/audit tool replaced that failed broad audit.
- The final answer does not explicitly disclose a failed/partial/unverified audit boundary.

Broad audit command examples:

- `find . ...`
- `for dir in ...; do find "$dir" ...; done`
- broad `rg` / `git grep` / `wc -l` pipelines

Non-broad scoped commands such as `find 3-ai-agents ...` can support a scoped claim, but cannot replace a failed all-directory audit.

Reminder behavior:

- Remove the blocked final text from `result.Response`, same as other completion gates.
- Insert a system reminder requiring either a replacement broad read-only audit command or a bounded final answer that clearly states which evidence failed and which conclusions are partial.
- Record `rule_id=failed_audit_evidence` and missing evidence in the transcript.

Tests:

- A synthetic query reproduces the bug: failed broad audit, later scoped successful audit, then a confident final answer. The gate must block it and force a bounded second answer.
- A direct `completionGate` unit test returns `RuleID=failed_audit_evidence`.
- A later successful broad audit after the failed broad audit allows final synthesis.
- An explicitly bounded final answer that discloses the failed audit is allowed.

### 3. Fix Recovery Guidance

The current failed verification reminder says not to redirect to `/dev/null`. After this fix that statement is stale. Replace it with narrower guidance:

- If sandbox rejects a redirect/path, use workspace-relative output files or rerun without the rejected redirect.
- Null-device output discard is allowed when the shell guard recognizes the static `/dev/null` target.

Tests that assert the old phrase must be updated.

## Verification Plan

### Focused Unit Tests

```bash
go test ./internal/sandbox -run 'TestCheckShellCommandAllowsNullDeviceRedirects|TestCheckShellCommandRejectsWritesOutsideWorkspace|TestCheckShellCommandRejectsDynamicMutatingPath' -count=1
go test ./internal/query -run 'TestFailedAuditEvidenceGate|TestCompletionGateReturnsRuleIDsAndMissingEvidence|TestVerificationFailureRecoveryReminder' -count=1
```

### Full Project Checks

```bash
go test ./... -count=1
git diff --check
```

### Real Operation Validation

Use the actual CLI, not only unit tests:

```bash
go run ./cmd/golang-cc --cwd $HOME/GolandProjects/anything-ai --permission-mode bypassPermissions -p 'Run exactly this read-only command and report whether it succeeded: for dir in 0-start-here 1-understand-ai 2-choose-tools 3-ai-agents 4-advanced-topics 5-skills roles prompts resources; do total=$(find "$dir" -name "*.md" -not -path "*/node_modules/*" 2>/dev/null | wc -l); echo "$dir: total=$total"; done'
```

Expected:

- The command is not blocked by `/dev/null`.
- The response includes directory totals from the real workspace.

For the completion gate, use a deterministic stub/fake-model unit test as the hard proof because it must control the model sequence: failed broad audit -> scoped evidence -> unsupported final claim -> bounded final answer.

## Self Review

- Covers direct root cause: yes, `/dev/null` redirect no longer fails static shell preflight.
- Covers deeper closure failure: yes, failed broad audit evidence blocks confident whole-scope final answers unless replaced or disclosed.
- Avoids overblocking: yes, scoped failures do not block unrelated final answers; bounded disclosure is allowed; later equivalent broad evidence supersedes the failed broad command.
- Keeps security boundary: yes, only `/dev/null` is special-cased; all other out-of-workspace writes remain denied.
- Verifiable: yes, has unit tests, full repo tests, `git diff --check`, and actual CLI validation against `$HOME/GolandProjects/anything-ai`.
