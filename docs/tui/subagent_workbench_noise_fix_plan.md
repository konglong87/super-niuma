# TUI Sub-agent Workbench Noise Fix Plan

Date: 2026-07-07

## Problem

Starting a lightweight sub-agent request can make the TUI show stale and overly dense workbench state:

- Completed tasks from a previous workspace run appear in the current view.
- The footer switches from a compact status hint to full `runtime`, `safety`, `goal`, and `evidence` rows.
- Sub-agent details are useful, but the surrounding old todo/goal/evidence context makes the current turn look contaminated.

Concrete reproduction observed in `$HOME/GolandProjects/skills`:

- User prompt: `测试-subagent机制，简单测试就行`
- TUI showed the current `Task` and `Sub-agents` panels, but also showed old SQL todos.
- The old todos came from `$HOME/GolandProjects/skills/.claude/todos.json`; all six items were already `completed`.

## Root Cause

1. `loadTodosFromDisk()` loads `.claude/todos.json` into the TUI model at startup/resume.
2. `todoProgressView()` suppresses all-completed disk todos only on the welcome screen or in quiet conversation mode.
3. `shouldUseQuietConversationHint()` currently exits quiet mode whenever `toolActivity`, `agentProgress`, or `recentAgentEvidence` exists.
4. A sub-agent run therefore opens full workbench chrome, which makes stale completed disk todos and full runtime/goal/evidence rows visible again.

The sub-agent mechanism itself is not the root issue. The problem is the TUI display policy for stale disk todos and lightweight live activity.

## Fix Scope

P0:

- Treat disk-loaded, all-completed todos as stale by default.
- Never show stale completed disk todos automatically, including during active tools/sub-agents.
- Keep incomplete disk todos visible, because they can represent resumable work.
- Keep todos written by the current turn visible, because `syncTodosFromToolEvent()` clears `todosLoadedFromDisk`.

P1:

- Let lightweight tool/sub-agent activity keep a compact footer.
- The compact footer should still show `status`, elapsed time, token summary, and relevant controls such as `agents=...`, `ctrl+e sub-agents`, and `ctrl+t tools`.
- Do not show full `runtime`, `safety`, `goal`, and `evidence` rows unless there is real resumed/session state, incomplete todos, permission/error state, or another heavy workbench condition.

Out of scope for this pass:

- Changing the agent/task execution model.
- Changing permission semantics.
- Removing sub-agent visibility.
- Reworking stored todo format.

## Acceptance Criteria

- A model with disk-loaded all-completed todos plus active sub-agent progress shows `Sub-agents`, but does not show `Tasks 6/6` or old todo text.
- The same lightweight sub-agent view uses compact footer rows and does not repeat `runtime`, `safety`, `goal`, or `evidence` context.
- Incomplete disk todos remain visible.
- Current-turn TodoWrite/TodoRead todos remain visible.
- Targeted TUI/CLI tests pass, then full `go test ./... -count=1` and `git diff --check` pass.

## Progress

- 2026-07-07: Plan created from screenshot/root-cause review. Implementation next: update TUI display policy and add focused regressions.
- 2026-07-07: Implemented P0/P1 display policy changes. Disk-loaded all-completed todos are suppressed in every view state, including active sub-agent/tool turns. Lightweight live tool/sub-agent activity now keeps the compact footer unless incomplete todos or session lifecycle state require the full workbench footer. Added focused regression tests for stale completed disk todos and incomplete disk todo visibility.
- 2026-07-07: Focused verification passed:
  - `go test ./internal/tui -run 'LightSubAgent|IncompleteDiskTodos|QuietConversation|ModeHintShowsRuntimeContextStatus|ModeHintWrapsNarrowTerminalRows|ModeHintShowsRecentAgentEvidence|ModeHintSkipsPlaceholder|ModeHintShowsRecentTaskFailureStatus|TodoPanel|LoadsTodos|ReloadsTodos' -count=1`
  - `go test ./internal/tui ./internal/cli -run 'ModeHint|Welcome|Todo|NestedAgent|SubAgent|SessionStatus|ConfigReload|TranscriptToolView|ConversationLayout|ToolExpanded|PermissionPrompt' -count=1`
  - `go test ./internal/tui -count=1`
- 2026-07-07: Full verification passed:
  - `go test ./... -count=1`
  - `git diff --check`
