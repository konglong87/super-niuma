# TUI Gate Preflight Noise Reduction Plan

## Background

The current pre-tool gate is safe but reactive:

1. The model emits a guarded command, for example `git commit`.
2. `preToolClosureGate` blocks the tool before execution.
3. The blocked result is returned as an error-like tool result.
4. The model sees the gate reminder and runs the required preflight checks.
5. TUI users see a visible blocked or failed-looking tool row before the workflow continues.

This makes a normal safety mechanism feel like a task failure. The goal is to keep the runtime gate as a hard protection while reducing visible "failed" noise and guiding the agent through the gate's expected prerequisite flow earlier.

## Goals

- Gate interception must not be presented as an ordinary tool failure in the TUI.
- Safe read-only preflight checks should run before guarded Git mutations when the gate can deterministically identify the missing prerequisite.
- Mutating actions such as `git commit`, `git push`, `git tag`, deploy, publish, or release must never be auto-executed by the preflight layer.
- The model must still inspect preflight output and decide whether the original operation is still appropriate.
- The transcript and closure ledger must remain auditable, including rule id, original attempted command, preflight command, and result.
- UI copy should be understandable to non-programmers: avoid `err=`, `blocked=`, `failed`, raw JSON, and raw `<system-reminder>` text in primary display.

## Non-goals

- Do not remove or soften the existing runtime gates.
- Do not auto-approve destructive shared-state operations.
- Do not rely on prompt wording as the only enforcement mechanism.
- Do not redesign the whole TUI layout in this change.
- Do not change permission hooks, sandbox behavior, or terminal mouse/selection behavior.

## Current Root Cause

The runtime currently uses one channel for several different meanings:

- Real tool failure, such as command exit failure.
- Permission or sandbox denial.
- Gate-protected operation that has not run.
- Missing preflight evidence.

For pre-tool gates, `query.go` converts a gate block into a `ToolTrace` with `IsError=true`. The TUI then tries to recover intent by parsing output text such as `Pre-Commit Scope Gate`. This is fragile because UI semantics depend on English reminder text rather than structured runtime state.

## Proposed Design

### Phase 1: Structured gate semantics

Extend pre-tool gate results with structured metadata:

- `RuleID`
- `Reminder`
- `UserTitle`
- `UserSummary`
- `PreflightCommand`
- `AutoPreflight`
- `OriginalCommand`

The TUI should prefer structured metadata when available and fall back to text classification only for older transcripts.

### Phase 2: Safe auto-preflight

When a pre-tool gate blocks a guarded command and the rule declares a safe read-only preflight, the runtime should execute that preflight as a separate Bash tool call instead of returning an error-like blocked result immediately.

Initial supported rules:

| Rule | Guarded action | Auto preflight |
| --- | --- | --- |
| `pre_commit_scope` | `git commit` | `git status --short --branch && git diff --name-status && git diff --cached --name-status` |
| `shared_state_git_push` | `git push` | `git status --short --branch && git rev-parse HEAD && (git rev-parse @{u} || git branch -vv)` |
| `shared_state_git_tag` | local/remote tag mutation | `git status --short --branch && git rev-parse HEAD && (git tag --list || git show-ref --tags)` |

Rules that require user authorization, such as destructive force push or external deploy/publish, must not auto-run anything beyond optional read-only diagnostics. They should remain a friendly non-failure gate state that asks for explicit confirmation.

### Phase 3: TUI copy and counters

Primary TUI display should use friendly states:

- `正在确认提交范围`
- `已完成提交前检查`
- `需要先确认分支和远端状态`
- `已保护：该命令尚未执行`
- `等待授权：该操作会影响外部系统`

Usage counters should avoid machine-style abbreviations:

- Prefer: `工具 3 次 · 保护拦截 1 · 最近 Bash`
- Avoid: `tools=3 err=1 blocked=1 last=Bash`

Gate blocks should not increase ordinary failure counters. Only actual failed execution should count as tool errors.

## Runtime Flow

For a model-requested guarded command:

```text
model tool_use: Bash {"command":"git commit -m ..."}
        |
        v
preToolClosureGate detects pre_commit_scope missing evidence
        |
        v
gate has safe AutoPreflight command?
        |
        +-- yes --> execute read-only preflight as Bash tool
        |           record completion_gate event with action=auto_preflight
        |           return preflight tool result to model as non-error if command succeeds
        |           model inspects output and may retry commit
        |
        +-- no  --> return friendly blocked gate result
                    record completion_gate event with action=blocked
                    TUI shows protected/waiting state, not failed
```

The original mutating command is not executed in the auto-preflight step. The next model turn must make a fresh decision after seeing the preflight output.

## Implementation Plan

1. Add preflight metadata to `preToolGateRule` and `completionGateResult`.
2. Add a small helper that builds a synthetic Bash `ContentBlock` for preflight execution while preserving the original tool id in closure event metadata.
3. In `query.go`, when a blockable gate has `AutoPreflight=true`, run the safe preflight command via the existing `runTool` path.
4. Record a `completion_gate` event with `rule_id`, `action=auto_preflight`, `original_command`, `preflight_command`, and result status.
5. Keep the existing hard block path for gates without safe preflight.
6. Update TUI summaries and usage panel copy so gate states are friendly and separate from failures.
7. Add focused query tests for commit preflight and chained verification protection.
8. Add focused TUI tests for friendly gate/preflight copy and usage summary wording.
9. Run full Go tests and TUI visual regression SOP before shipping.

## Acceptance Criteria

- A direct `git commit` attempt with missing scope verification does not execute `git commit`.
- The runtime first runs the exact read-only preflight command for `pre_commit_scope`.
- The model receives the preflight output as a normal tool result, not a failed gate result, when the preflight succeeds.
- Chained verification plus commit remains blocked and does not execute either command as a combined mutation.
- TUI usage text no longer shows `err=` or `blocked=` in primary usage summaries.
- Gate-related TUI rows do not show `failed`, `失败`, or raw `<system-reminder>` in primary display.
- Existing closure gate tests continue to prove that guarded mutating operations cannot bypass preflight evidence.
- `go test ./... -count=1`, `git diff --check`, and the TUI visual regression SOP pass.

## Risks and Mitigations

- Risk: auto-preflight could look like the original guarded action was approved.
  - Mitigation: only run read-only commands, record `action=auto_preflight`, and require a later model turn for the mutation.
- Risk: preflight command shape becomes another hidden policy surface.
  - Mitigation: keep commands explicit in rule metadata and covered by tests.
- Risk: TUI hides real errors as friendly gate text.
  - Mitigation: only classify by structured gate metadata or known gate reminders; ordinary failed commands still render as failures.
- Risk: prompt-only fixes regress across models.
  - Mitigation: runtime remains the enforcement point; prompt/UI wording is secondary.

## Review Checklist

- The plan preserves runtime hard gates.
- The plan separates protected workflow states from real failures.
- The plan only auto-executes read-only commands.
- The implementation path reuses existing `runTool` hooks, permissions, telemetry, and transcript processing.
- The visible TUI acceptance includes real PTY screenshot verification, not only unit tests.
