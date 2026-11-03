# TUI Workbench Upgrade Progress

本文档记录本轮 TUI workbench 升级的审计、计划、实施进度和验证边界。目标是把 TUI 从可用的聊天终端提升为长期任务下可读、可控、可排查的 agent workbench。

## P0 Audit

### Architecture Map

- Entry and state assembly: `internal/cli/cli.go` builds `tui.Options`, resolves `WelcomeInfo`, and converts query/session callbacks into `tui.StreamEvent`.
- Core TUI model: `internal/tui/app.go` owns Bubble Tea `Model`, `Update`, `View`, textarea, viewport, transcript printing, slash/resume/rewind pickers, permission prompt, tool activity, sub-agent progress, todo panel and usage panel.
- Runtime context: `WelcomeInfo` carries model, provider, context length, cwd, permission mode, sandbox, tool summary, MCP count, resume and goal fields.
- Message model: `message{role, content, meta, tools, agents}` renders user, assistant, tool, agent, thinking, status, loop and recap blocks.
- Tool lifecycle: `StreamToolStart` and `StreamToolResult` update `toolActivityItem` through `startToolActivity` and `finishToolActivity`, then `toolCallInlineView` renders the current or archived tool rows.
- Task/Agent/SubAgent lifecycle: `StreamNestedProgress` with `TaskID` updates `agentProgress`; `agentProgressView` sorts and limits visible sub-agent rows; `ctrl+e` toggles expansion.
- Permission flow: `StreamPermissionRequest` installs `pendingPermission`, `permissionPromptView` renders the confirmation UI, and `handlePermissionKey` returns session/project/global/deny decisions.
- Plan/Todo/Progress: Todo state is extracted from `TodoWrite`/`TodoRead` JSON into `todoProgressView`; goal status/evidence/next action currently flows through `WelcomeInfo` and `runtimeContextPanelLines`.
- Usage: query results update `usagePanel`; `usageView` renders model, turns, tokens, cache, tools, stop reason, speed, tier, geo, session and cwd/context.

### Problems And Evidence

1. Bottom status line is overloaded. `modeHintView` mixes status, runtime context, permission, mouse mode and shortcuts in mostly unlabeled rows, so long tasks are hard to scan.
2. Prompt mode is not visible in TUI context. CLI has `opts.promptMode`, but `WelcomeInfo` does not expose it yet, so TUI cannot truthfully show code/chat mode.
3. Tool cards are still one-line summaries. `toolCallInlineView` shows tool name, input summary, status, result and elapsed, but there is no detail surface for full input/output or failure recovery context.
4. Permission UI shows raw input JSON. `permissionPromptView` truncates `PermissionRequest.Input` directly, which is accurate but not very readable for high-risk commands.
5. Sub-agent progress is aggregated but dense. `renderAgentProgress` renders status and metadata, but evidence/risk/next-action hierarchy is a long one-line summary.
6. Goal and agent evidence are visible but not visually distinct. `runtimeContextPanelLines` prefixes several rows with the same `context` label, making cwd/session, goal, evidence and next action look equivalent.
7. `internal/tui/app.go` is a very large single file. Rendering, event handling, markdown, permissions, tool summaries, todos and sub-agent progress live together, increasing future change risk.

## Plan

### P0: Audit And Tracking

- [x] Read project rules and confirm clean working tree.
- [x] Locate TUI entry, rendering loop, state model and display paths.
- [x] Record architecture map and concrete problems in this document.

### P1: Highest ROI Small Changes

- [x] Upgrade bottom status/context bar into clear runtime, status, goal/evidence and controls rows.
- [x] Improve tool cards with clearer status/detail hierarchy while keeping `ctrl+t` behavior.
- [x] Improve Task/Agent/SubAgent progress rows for goal, evidence, risk and next-action readability.
- [x] Refine todo/plan/progress visual hierarchy without changing model context semantics.
- [x] Keep colors restrained and spacing stable across narrow terminals.

### P2: Completeness

- [x] Make permission confirmation show readable summaries before raw JSON.
- [x] Add better summaries for long output, validation failure and tool error recovery.
- [x] Surface compact/resume/checkpoint state as first-class TUI context.
- [x] Consider extraction of TUI render helpers after behavior is stable and covered.
- [x] Revisit WebUI/trace parity after TUI workbench baseline stabilizes.

## Implementation Log

### 2026-07-06: Status Bar P1.1

Planned change: add a real `PromptMode` field to `WelcomeInfo`, populate it from existing CLI options, and restructure `modeHintView` rows into labeled `status`, `runtime`, `goal`, `evidence` and `controls` sections. This is intentionally display-only except for the small state field; it does not alter query/runtime behavior, permissions, mouse tracking, transcript printing or input handling.

Verification target:

- `go test ./internal/tui ./internal/cli -run 'ModeHint|Welcome|ConfigReload' -count=1`
- `git diff --check`

Result:

- Added `WelcomeInfo.PromptMode` and populated it from CLI `opts.promptMode`.
- Bottom hint now uses labeled rows: `status`, `runtime`, `safety`, `goal`, `evidence`, `controls`.
- Runtime row now shows `ui=tui`, model, provider, mode, context length, tools, MCP count, cwd and session when available.
- Focused verification passed: `go test ./internal/tui ./internal/cli -run 'ModeHint|Welcome|ConfigReload' -count=1`.
- TUI package verification passed: `go test ./internal/tui -count=1`.
- Full repository verification passed: `go test ./... -count=1`.
- Whitespace verification passed: `git diff --check`.

### 2026-07-06: Workbench P1/P2 Completion

Planned change: complete the remaining display-layer improvements without a large TUI rewrite. The scope stays inside existing Bubble Tea state and render helpers, preserving terminal copy defaults, scrolling, input handling and existing shortcuts.

Result:

- Tool cards now retain raw input/output internally and show compact `input`, `output` and `failure` detail rows when `ctrl+t` expands the tool panel.
- Permission confirmation now shows a readable `Summary` before the truncated `Raw input`.
- Todo progress titles now show active and pending counts while keeping collapsed current-task behavior.
- Sub-agent capability summaries now render as labeled evidence/detail rows and keep risks/next-action visible instead of clipping after the first few fields.
- Session lifecycle state is now visible in the bottom context: `/resume` sets `state=resumed ...`, `/compact` surfaces the compact summary, and `/checkpoint`/`/rewind` surface the selected session operation.
- Extraction was considered and deferred: `internal/tui/app.go` is still large, but this round intentionally avoided a risky mechanical split before more visual behavior is stabilized by tests.
- WebUI/trace parity remains a separate follow-up boundary. The TUI now exposes the workbench baseline needed before reopening `docs/web_agent/tui_display_parity_gap_analysis.md`.

Verification target:

- `go test ./internal/tui ./internal/cli -run 'ToolExpanded|PermissionPromptDecision|SessionStatus|ModeHint|ConfigReload|Todo|NestedAgent|SessionControlOptions' -count=1`
- `scripts/tui-tool-progress-acceptance.sh`
- `go test ./internal/tui ./internal/cli -count=1`
- `go test ./... -count=1`
- `git diff --check`

Verification result:

- Focused TUI/CLI workbench tests passed.
- `scripts/tui-tool-progress-acceptance.sh` passed and generated report `reports/tui-tool-progress/20260706T123848Z/tui-acceptance-report.json`.
- `go test ./internal/tui ./internal/cli -count=1` passed.
- `go test ./... -count=1` passed.
- `git diff --check` passed.

### 2026-07-06: Real TUI Acceptance And Transcript Polish

Plan: run the actual TUI through success, failure and mixed multi-tool flows, then make only evidence-backed polish changes. The fixed manual report directory is `reports/tui-workbench-real-20260706`.

Manual TUI evidence:

- Success flow prompt created 3 todos, read `README.md` and `internal/demo/calc.go`, ran `go test ./...`, and showed `Tasks 3/3 all completed`, readable tool rows, usage, recap and no duplicate legacy `Tools` panel.
- Failure flow prompt created 2 todos, attempted `Read missing-readme.md` and `ls missing-file-for-tui-acceptance`, and showed explicit failures: `Read missing-readme.md ✗ → not found`, `Bash ls missing-file-for-tui-acceptance ✗ → exit 1`, plus `tools=4/32 err=2`.
- Mixed flow prompt created 5 todos, covered `Read`, `Glob`, `Grep`, successful `Bash go test ./...`, and failing `Bash ls missing-multi-tool`. The TUI showed `Glob **/*.go ✓ → 2 files`, `Grep func in . ✓ → 4 matches`, `Bash go test ./... ✓ → exit 0`, `Bash ls missing-multi-tool ✗ → exit 1`, `Tasks 5/5 expanded`, usage with `err=1`, and a final Chinese summary.

Finding:

- Completed assistant messages are printed into natural terminal scrollback with `tea.Println`. That historical output cannot be redrawn by `ctrl+t`, so printing `... +N tool uses ctrl+t expand` into completed transcript blocks was misleading once the message left the live viewport.

Fix:

- Live tool rows remain compact and interactive: collapsed live views still show the latest 3 tools and `ctrl+t expand`, expanded live views still show compact input/output/failure detail rows.
- Completed transcript rendering now uses a self-contained compact tool list: all retained tool rows are printed without non-interactive `ctrl+t` controls. This preserves native terminal copy/scroll behavior and avoids a larger rendering rewrite.

Verification target:

- `go test ./internal/tui -run 'TestModelTranscriptToolViewIsSelfContainedWithoutExpandHint|TestModelToolExpandedViewShowsInputOutputAndFailureDetails|TestModelArchivesCompletedToolActivityWithAssistantMessage' -count=1`
- `go test ./internal/tui ./internal/cli -run 'ToolExpanded|PermissionPromptDecision|SessionStatus|ModeHint|ConfigReload|Todo|NestedAgent|SessionControlOptions|TranscriptToolView' -count=1`
- `scripts/tui-tool-progress-acceptance.sh`
- `go test ./... -count=1`
- `git diff --check`

Verification result so far:

- Focused transcript/tool archive test passed.
- Focused TUI/CLI workbench tests passed.
- `scripts/tui-tool-progress-acceptance.sh` passed and generated `reports/tui-tool-progress/20260706T134149Z/tui-acceptance-report.json`.
- Full repository verification passed: `go test ./... -count=1`.
- Whitespace verification passed: `git diff --check`.

### 2026-07-06: Final TUI Polish Batch

Plan: close the remaining TUI-only polish gaps without changing agent runtime behavior, permissions, terminal mouse defaults, input handling or natural scrollback. Web Agent/TUI P1 parity is tracked as done in `docs/todo.md` TODO-049; remaining Web Agent P2 product work stays outside this TUI terminal batch.

Result:

- Narrow terminal status/context rows now wrap by semantic segments instead of hard-truncating the whole row. Long `runtime`, `goal`, `evidence` and `controls` content stays readable at 80 columns while preserving compact density.
- Markdown rendering now strips ANSI-styled whitespace-only lines after Glamour rendering, removing the visible colored blank-span artifacts seen during streamed Chinese markdown/list output.
- Added `internal/tui/view_format.go` for display-format helpers, reducing further growth in `internal/tui/app.go` for status wrapping and ANSI cleanup logic.

Verification target:

- `go test ./internal/tui -run 'ModeHintWrapsNarrow|ModeHintShowsRuntimeContextStatus|RenderMarkdownCleansANSIOnlyBlankLines|RenderMarkdownUsesReadableClaudeLikeColors|TranscriptToolView' -count=1`
- `go test ./internal/tui ./internal/cli -run 'ModeHint|ToolExpanded|PermissionPromptDecision|SessionStatus|ConfigReload|Todo|NestedAgent|SessionControlOptions|TranscriptToolView|RenderMarkdown' -count=1`
- `scripts/tui-tool-progress-acceptance.sh`
- `go test ./... -count=1`
- `git diff --check`

Verification result so far:

- Focused narrow-status/markdown tests passed.
- Focused TUI/CLI workbench tests passed.
- `scripts/tui-tool-progress-acceptance.sh` passed and generated `reports/tui-tool-progress/20260706T141051Z/tui-acceptance-report.json`.
- Full repository verification passed: `go test ./... -count=1`.
- Whitespace verification passed: `git diff --check`.

### 2026-07-06: Welcome Screen Noise Fix

Finding: a fresh TUI start could render stale workbench state before the user sent any prompt. The visible symptoms were old `goal/step/criteria/evidence/next` fields in both the welcome header and bottom status area, plus `Tasks N/N all completed` loaded from persisted `.claude/todos.json`. That is not a good new-session first screen: it conflates active workbench execution state with idle startup context.

Fix:

- Welcome idle mode now uses a minimal bottom hint: status, model/provider/cwd, permission/sandbox and essential input shortcuts.
- Goal/evidence/session workbench detail stays available after a conversation starts, during tool/sub-agent activity, or when fresh runtime evidence exists.
- The welcome header no longer prints goal/evidence fields. Long-running task context is still shown in the workbench status area once the user is actually in a session turn.
- Persisted todos are still loaded from `.claude/todos.json`, but all-completed todo lists are hidden on the idle welcome screen. In-progress or pending persisted todos remain visible, so interrupted work can still be resumed intentionally.
- Follow-up screenshot review found that welcome idle mode still duplicated runtime context: the header already showed model/provider/cwd/tools/mcp/sandbox/permissions, while the bottom hint repeated `runtime ui=tui ...` and `safety permissions=...`. The bottom welcome hint now keeps only `status` and interaction controls; runtime/safety rows remain available in the active workbench status area after the conversation starts.
- Follow-up after sending a trivial first prompt found a second path: once a user/assistant message exists, `shouldShowWelcome()` becomes false and the normal workbench status rows were shown even when the turn was just idle chat. Quiet conversation idle mode now keeps the bottom hint to `status` and controls, hides disk-loaded all-completed todos, and compresses `Usage` to usage-only fields. Full runtime/safety/goal/evidence context appears when tools/agents/evidence are visible, when there are incomplete todos, or when session lifecycle state is active.
- Follow-up during streaming found the remaining path: `busy=true` was treated as a full workbench signal even before any tool, agent, permission prompt or incomplete todo existed. Quiet conversation mode now also applies during ordinary model generation; it keeps the running status, elapsed time and session token summary, while still suppressing repeated runtime/safety/goal/evidence rows and disk-loaded completed todos. Full workbench chrome still appears as soon as real tool/agent/todo/session lifecycle state exists.

### 2026-07-06: Markdown Table Rendering Fix

Finding: GFM tables were already intercepted by `renderMarkdownTables`, but the custom renderer intentionally converted them into fenced code blocks with ASCII `+---+` borders. Column width calculation also capped each column and `markdownTableRow` called `truncateDisplayWidth`, so long cells were visually cut off instead of wrapping.

Fix:

- Table blocks now render through placeholders restored after Glamour, avoiding fenced-code styling around the table.
- Borders use solid box-drawing rules (`┌─┬─┐`, `│`, `├─┼─┤`, `└─┴─┘`) instead of dashed ASCII separators.
- Long table cells wrap into multiple physical table rows using display-width accounting, including Chinese wide characters, so content is preserved instead of truncated.
- Fenced code blocks that contain markdown-like tables are still left literal.
- Follow-up alignment fix: Glamour could add paragraph indentation to the single-line table placeholder, and direct placeholder replacement applied that indentation only to the first physical table line. Table restoration is now line-aware: a placeholder-only rendered line is replaced by the table block without inheriting Glamour's prefix, keeping the top border, header, body and bottom border aligned.

### 2026-07-07: Permission And Tool Detail Density

Finding: permission prompts and expanded tool rows had the right data, but the visual hierarchy was still log-like. Permission prompts showed summary/raw input without an explicit risk or persisted rule cue. Expanded tool rows truncated detail fields, so long input/output summaries could hide useful failure context.

Fix:

- Permission prompts now show labeled `Summary`, `Risk`, `Request`, `Reason`, `Source`, `Rule` and `Raw input` rows with wrapped values, plus a clearer `Decision` section and shortcut legend.
- Tool rows now expose `status=...`, `result=...` and elapsed time on the headline, while expanded `failure`, `input` and `output` detail lines wrap by terminal display width instead of hard truncating.
- Existing shortcuts and permission decision behavior are unchanged.
- Self-review follow-up: permission prompt detail rows, decision choices and scope legend now wrap to the current TUI content width instead of a fixed 76-column layout, so narrow terminals do not overflow.

Verification target:

- `go test ./internal/tui -run 'WelcomeSuppressesWorkbenchGoal|ModeHintShowsGoalStatusAfterConversationStarts|ModeHintShowsRuntimeContextStatus|TodoWriteUpdatesTaskProgressPanel|LoadsTodosFromWelcomeCWD|ReloadsTodosFromResumeWelcomeCWD|ConversationLayoutHasInputRules' -count=1`
- `go test ./internal/tui ./internal/cli -run 'ModeHint|Welcome|Todo|SessionStatus|ConfigReload|TranscriptToolView|ConversationLayout' -count=1`
- `scripts/tui-tool-progress-acceptance.sh`
- `go test ./... -count=1`
- `git diff --check`

Verification result so far:

- Focused welcome-noise regression tests passed.
- Focused TUI/CLI tests passed.
- `scripts/tui-tool-progress-acceptance.sh` passed and generated `reports/tui-tool-progress/20260706T145140Z/tui-acceptance-report.json`.
- Full repository verification passed: `go test ./... -count=1`.
- Whitespace verification passed: `git diff --check`.

### 2026-07-07: Compact Sub-agent Panel

Finding: after the stale todo/workbench chrome fix, a lightweight sub-agent run no longer showed old tasks or repeated footer context, but the collapsed `Sub-agents` panel still exposed implementation-heavy detail such as `desc=...`, session ids, output/transcript paths, capability evidence and streamed text snippets. That made a simple Task request look like a diagnostic trace instead of a readable progress summary.

Fix:

- The collapsed `Sub-agents` panel now renders one compact status line per agent: id, agent/model, status, stop hint for running agents, turn/message/tool/token/elapsed counters.
- Streaming text deltas, long descriptions, session/output/transcript paths and capability evidence are hidden in collapsed mode.
- Expanded mode keeps the diagnostic fields, so `ctrl+e` still exposes the deeper detail when needed.
- Real TUI validation was run through a PTY against `/tmp/go-claude-tui-subagent-verify` with a live `Task` request that read the current directory and `docs/sample.txt`. The observed completed panel showed the compact line only, without old SQL todos, repeated runtime/safety/goal/evidence footer rows, or transcript/session/capability noise.

Verification target:

- `go test ./internal/tui -run 'NestedAgent|AgentProgress|LightSubAgent|CompactSubAgent|TogglesAgent|PrioritizesFailed|LimitsCompleted' -count=1`
- `go test ./internal/tui ./internal/cli -run 'ModeHint|Welcome|Todo|NestedAgent|SubAgent|SessionStatus|ConfigReload|TranscriptToolView|ConversationLayout|ToolExpanded|PermissionPrompt' -count=1`
- Real TUI PTY smoke with a live sub-agent request.
- `go test ./... -count=1`
- `git diff --check`

Verification result:

- Focused compact sub-agent tests passed.
- Focused TUI/CLI regression tests passed.
- Real TUI PTY smoke passed observationally.
- Full repository verification passed: `go test ./... -count=1`.
- Whitespace verification passed: `git diff --check`.

### 2026-07-07: OSC 11 Input Pollution Fix

Finding: real Terminal/PTY validation exposed a separate startup/input defect: `]11;rgb:...` could appear in the TUI textarea before the user prompt. The root cause is Bubble Tea v1.3.10 package initialization calling `lipgloss.HasDarkBackground()`, which goes through termenv background detection and sends an OSC 11 terminal background-color query. Some terminal/expect combinations echo the OSC response into Bubble Tea input.

Fix:

- Added a local `replace` for `github.com/muesli/termenv v0.16.0` under `third_party/termenv`.
- Patched only the Unix `backgroundColor()` path to use `COLORFGBG` when available and otherwise default to black without sending OSC 11.
- This keeps Bubble Tea, Lip Gloss and Glamour on the same APIs while preventing the query at the source used during dependency initialization.

Verification target:

- Real TUI PTY startup should show a clean `Ask Go Claude` input area, with no `]11;rgb` text.
- Real TUI PTY sub-agent smoke should still complete and show compact `Sub-agents` output.
- `go test ./internal/tui -run 'CompactSubAgent|NestedAgent|ModeHint|RenderMarkdown|PermissionPrompt' -count=1`
- `go test ./... -count=1`
- `git diff --check`

Verification result:

- Focused TUI tests passed.
- Real TUI PTY startup was clean: no `]11;rgb` pollution.
- Real TUI PTY sub-agent smoke completed. The final collapsed panel showed `#1 general-purpose (glm-5.1) completed - turn=2, messages=3, tools=2, tool=Read, tokens=..., elapsed=...` without session/out/transcript/capability noise.
- Full repository verification passed: `go test ./... -count=1`.
- Whitespace verification passed: `git diff --check`.
