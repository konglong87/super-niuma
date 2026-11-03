# TUI Todo and Usage Status UX Fix Plan

Date: 2026-07-13

## Background

真实 session `79916b88-ec5b-41ce-9cef-3f4a91afb7ee` 暴露了两个 TUI 可读性问题：

1. `Tasks 6/6 all completed ✓ ctrl+y expand` 在任务全部完成后仍长期显示在页面底部。
2. `Usage ... 工具 43/32 次 · 失败 2 · 保护拦截 1 · 最近 Bash` 对非工程用户不友好，用户无法判断失败是什么、是否影响结果、和保护拦截是不是同一类问题。

这两个问题都不是单纯的颜色或排版问题，而是 TUI 把不同语义混在了同一个底部状态区域：

- 当前 live todo 状态；
- 已完成 todo 摘要；
- session 级 usage 汇总；
- 普通工具失败；
- gate、workspace、permission 类保护拦截；
- 最近一次工具名。

目标是保留可观测性和安全信息，同时让底部信息更像产品 UI，而不是调试日志。

## Evidence

### Todo completion evidence

Session 末尾的 TodoWrite 结果显示：

```text
Summary: 6 total, 0 pending, 0 in_progress, 6 completed.
All todos are completed, so the persisted session todo state was cleared...
```

这说明任务确实全部完成，持久化 todo 状态也已清理。但 TUI live model 仍保留 `m.todos`，因此继续渲染完成态摘要。

相关代码路径：

- `internal/tui/app.go` `Model.View()` 把 `bottomChromeParts()` 固定拼在 viewport 后面。
- `bottomChromePartsForBudget()` 会优先追加 `todoProgressView()`。
- `todoProgressView()` 在 `completed == len(m.todos)` 且未展开时返回 `Tasks N/N all completed ✓`。
- 当前隐藏条件只覆盖 disk-loaded completed todos 和 welcome 空闲态，不覆盖当前 live turn 完成后的常驻状态。

### Usage failure evidence

同一 session 中有三条 `tool_result is_error=true`：

| Type | Example | Expected category |
| --- | --- | --- |
| Bash exit failure | environment check returned `exit status 1` | 普通工具失败 |
| Workspace write boundary | write path under `/tmp` outside configured workspace | 保护拦截 |
| Script/data parsing failure | Python `TypeError` while parsing API response | 普通工具失败 |

TUI 当前计数逻辑：

```go
if call.IsError && isToolGateBlocked(call.Output) {
    panel.ToolBlocked++
} else if call.IsError {
    panel.ToolErrors++
}
```

因此 UI 显示 `失败 2 · 保护拦截 1` 在统计上是对的，但文案和上下文不够清楚。

## Problems

### P1: Completed todo summary is treated as permanent bottom chrome

`Tasks 6/6 all completed` 当前不是某条 transcript 消息的一部分，而是 bottom chrome 的全局 live 状态。它会跟随页面底部存在，直到模型状态被清空、welcome 条件命中或新一轮状态覆盖。

这导致用户感知上有三个问题：

- 已完成任务看起来像仍在当前页面占位。
- `ctrl+y expand` 对已完成历史任务价值不高，却长期占用底部区域。
- 任务完成摘要没有归档到具体回合，无法解释“这是谁的任务、什么时候完成的”。

### P2: Usage failure copy is technically correct but product-unfriendly

`失败 2` 对用户没有足够信息：

- 不知道是哪两次失败。
- 不知道是否已经恢复。
- 不知道是否影响最终结果。
- 不知道和 `保护拦截 1` 的关系。

`保护拦截` 的正向语义也没有体现出来。它通常表示系统保护生效、危险动作未执行，不应和普通失败产生同样的负面观感。

### P3: Usage counter mixes debug metrics and user-facing states

`工具 43/32 次`、`失败 2`、`最近 Bash` 是工程视角字段。对普通用户来说，更关心：

- 做了多少操作；
- 有没有未完成的问题；
- 有没有安全保护；
- 是否需要自己处理。

当前文案没有把这些信息分层。

## Goals

- 完成态 todos 不再常驻底部，而是锚定到当次会话位置，跟随 transcript 一起滚动。
- 当前正在进行的 todo 仍然实时可见。
- 已完成 todo 摘要可追溯，并能回答“这是哪一轮完成的任务”。
- 普通工具失败和保护拦截在文案、颜色、计数和详情中明确分离。
- Usage 默认态更像用户语言，展开态再显示工程细节。
- 不隐藏真实失败，不把安全保护伪装成成功。
- 保持 terminal 宽度、bottom chrome budget 和历史 transcript 稳定。
- 所有可见 TUI 改动必须通过真实 PTY 截图验收。

## Non-goals

- 不改变 query runtime 的工具执行语义。
- 不改变 gate、permission、sandbox 的安全边界。
- 不修改 session JSONL 存储格式作为本轮必要条件。
- 不重做整个 TUI layout 架构。
- 不默认开启 mouse tracking。
- 不用 prompt-only 方案解决 UI 语义问题。

## Design Principles

1. 当前状态优先：底部只放“现在正在发生或需要用户处理”的信息。
2. 完成信息归档：已完成摘要必须进入 transcript，跟随当次会话滚动，不占用 bottom chrome。
3. 失败分级：普通失败、安全保护、用户拒绝、已恢复问题必须分开表达。
4. 默认少、展开多：默认只显示结论，展开后提供原因和最近记录。
5. 用户语言优先：避免 `err`、`blocked`、`status=done`、`result=updated`、裸 JSON 和内部字段。
6. 眼见为实：单元测试只能证明逻辑，最终必须看真实终端截图。

## Proposed UX

### Todo bottom chrome behavior

Current in-progress state:

```text
任务 3/6 · 正在：启动后端服务 · 待处理 3
```

Collapsed state when there are more items:

```text
任务 3/6 · 正在：启动后端服务 · 还有 3 项 · Ctrl+Y 查看
```

All completed state should leave bottom chrome and become a transcript-anchored item:

```text
✓ 本轮任务完成：6 项全部完成
```

The completed item should appear at the same conversation position as the TodoWrite completion result. When the user scrolls the conversation, this completed task summary scrolls away with that turn. It must not remain pinned above Usage or the input box.

Bottom chrome after completion:

```text
<hidden unless a new task is in progress>
```

Expanded completed item, if the user opens details:

```text
✓ 创建 Python venv 并安装后端依赖
✓ 安装前端 node_modules
✓ 创建 .env 配置文件
✓ 启动后端 uvicorn
✓ 启动前端 Vite dev server
✓ 验证前后端连通 + 种子数据
```

### Usage default copy

Replace:

```text
工具 43/32 次 · 失败 2 · 保护拦截 1 · 最近 Bash
```

With:

```text
执行了 43 次操作 · 2 次未完成 · 1 次安全保护 · 最近使用 Bash
```

Compact/narrow variant:

```text
操作 43 次 · 未完成 2 · 安全保护 1
```

When there are no issues:

```text
执行了 43 次操作 · 最近使用 Bash
```

When all failures are later recovered:

```text
执行了 43 次操作 · 2 个小问题已处理 · 最近使用 Bash
```

The recovered variant requires tracking whether a later successful action superseded the failure. If this cannot be inferred reliably in P0, do not show `已处理`; keep the neutral `未完成`.

### Usage details copy

Expanded details should explain categories:

```text
操作概览
  执行：43 次
  未完成：2 次
  安全保护：1 次
  最近工具：Bash

最近未完成
  Bash：环境检查没有找到预期文件或端口
  Bash：接口返回格式和预期不一致

安全保护
  Bash：已阻止写入工作区外目录，命令未执行
```

The first line remains compact; details can live behind an existing tool/activity expansion shortcut or a new usage detail state if needed.

## Architecture

### 1. Todo display lifecycle

Introduce explicit display lifecycle for todos inside the TUI model:

```go
type todoDisplayState struct {
    Items             []todoItem
    Source            todoSource
    CompletedAt       time.Time
    CompletionArchived bool
    Dismissed          bool
}
```

This can be implemented minimally without a new struct first, but the state machine should be explicit:

| State | Bottom chrome | Transcript/archive |
| --- | --- | --- |
| No todos | hidden | none |
| Pending/in progress | visible | normal tool rows |
| All completed | hidden | transcript-anchored summary once |
| Disk-loaded all completed | hidden | none by default |
| User expands completed item | hidden | expanded transcript detail |

P0 can avoid adding persistence and only manage this within `Model`.

### 2. Completed todo transcript anchor

When `syncTodosFromToolEvent()` or `setTodos()` observes a transition from not-all-completed to all-completed:

1. Record completion timestamp.
2. Append or update a lightweight transcript display item at the current turn position.
3. Mark that todo set as archived so resize/re-render does not duplicate it.
4. Stop rendering completed todo state in bottom chrome immediately.

Implementation must prefer a display-only transcript segment over mutating model-visible conversation text:

- The item is visible in the TUI transcript.
- The item is not sent back to the model as assistant text.
- The item is not written as a fake assistant message unless the session format already has a display-only event type.

If the existing display segment model cannot support this cleanly, the implementation should first add a small display-only segment type. A bottom pinned completion message is not an acceptable final behavior for this requirement.

### 3. Usage issue model

Current fields:

```go
ToolErrors  int
ToolBlocked int
LastTool    string
```

Keep these for backward compatibility, but add a small user-facing issue summary layer:

```go
type toolIssueKind string

const (
    toolIssueIncomplete toolIssueKind = "incomplete"
    toolIssueProtected  toolIssueKind = "protected"
    toolIssueDenied     toolIssueKind = "denied"
)

type toolIssueSummary struct {
    Kind     toolIssueKind
    ToolName string
    Title    string
    Detail   string
    Raw      string
}
```

P0 can store only recent issues in memory, capped to 3-5 items, so the status line remains light.

Classification priority:

1. Structured gate metadata, when available.
2. Existing `classifyToolGateBlock()` fallback.
3. Permission denied / user denied.
4. Ordinary `call.IsError`.

### 4. Friendly wording helpers

Add dedicated helpers instead of formatting raw counters inline:

```go
func friendlyToolUsageSummary(panel usagePanel) string
func compactToolSummary(panel usagePanel) string
func friendlyToolIssueTitle(call ToolCall) toolIssueSummary
```

The current helper already exists, but it should move from count-only wording to user-facing state wording.

Suggested wording map:

| Internal condition | Primary text | Detail text |
| --- | --- | --- |
| ordinary `IsError` | `未完成` | `这次操作没有按预期完成，系统已把结果交给模型继续处理。` |
| workspace boundary | `安全保护` | `已阻止写入工作区外目录，命令未执行。` |
| permission denied | `权限保护` | `当前权限不允许这次操作。` |
| preflight gate | `需要先检查` | `系统要求先完成安全检查，再继续执行该操作。` |
| user denied | `用户已取消` | `这次操作被用户拒绝，没有继续执行。` |

### 5. Color and severity

Default usage line should not turn red just because historical failures exist.

Recommended severity:

| Condition | Color intent |
| --- | --- |
| Ordinary past failure, turn recovered or completed | muted warning |
| Current blocking failure | warning/error |
| Safety protection | neutral/info or soft warning |
| User action required | warning |
| Permission denied | warning |

This prevents one historical `exit status 1` from making the whole bottom area feel broken.

## Implementation Plan

### Phase 0: Lock evidence and tests

1. Add focused tests around `todoProgressView()` for live completed todos.
2. Add focused tests around `friendlyToolUsageSummary()` and `compactToolSummary()`.
3. Add fixture-like cases for:
   - two ordinary tool errors plus one workspace boundary;
   - no failures;
   - only protection;
   - narrow terminal compact mode.

Expected commands:

```bash
go test ./internal/tui -run 'Todo|Usage|ToolSummary|GateBlock' -count=1
```

### Phase 1: Anchor completed todos in the transcript

Implement the todo display lifecycle policy:

- In-progress and pending todos remain visible.
- Disk-loaded all-completed todos remain hidden in all states.
- Live all-completed todos are converted into a transcript-anchored completion item.
- Bottom chrome hides completed todo state immediately after archiving.
- `ctrl+y expand` is not shown for completed todos in bottom chrome.
- Completed todo details are opened from the transcript item or existing tool detail flow, not from a permanently pinned footer.

Candidate code paths:

- `todoProgressView()`
- `setTodos()`
- `syncTodosFromToolEvent()`
- `bottomChromePartsForBudget()`
- display segment / transcript rendering helpers

Required completed transcript copy:

```text
✓ 本轮任务完成：6 项全部完成
```

Avoid:

```text
Tasks 6/6 all completed ✓ ctrl+y expand
```

### Phase 2: Friendly usage wording

Update usage line copy:

- `工具 43/32 次` -> `执行了 43 次操作`
- `失败 2` -> `2 次未完成`
- `保护拦截 1` -> `1 次安全保护`
- `最近 Bash` -> `最近使用 Bash`

Compact variant:

- `操作 43 次`
- `未完成 2`
- `安全保护 1`

Keep technical numbers available in tests and optional detail views, but remove machine-like primary wording.

Candidate code paths:

- `friendlyToolUsageSummary()`
- `compactToolSummary()`
- `usageViewForBudget()`
- `quietUsageViewForWidth()`

### Phase 3: Recent issue details

Add recent issue summaries to expanded tool/activity view:

- keep only last 3-5 issue summaries;
- show friendly title and one-line reason;
- separate `最近未完成` and `安全保护`;
- do not show raw JSON or stack traces in the primary detail line;
- keep raw output accessible only in existing expanded tool transcript rows.

Candidate code paths:

- `applyQueryResult()`
- tool transcript rendering around `renderToolResult...`
- existing `errorResultSummary()`
- existing `classifyToolGateBlock()`

This phase can be deferred if P1/P2 already resolve the primary screenshot issue.

### Phase 4: Transcript archive for completed todos

This phase is no longer optional if Phase 1 cannot provide the transcript anchor directly. The user requirement is that completed todos follow the conversation position while scrolling.

Add a stable, low-noise archive marker for completed todo sets:

```text
✓ 本轮任务完成：6 项全部完成
```

Design constraints:

- Append once per completed todo set.
- Do not duplicate on resize/re-render.
- Do not persist fake assistant text into the actual model transcript unless there is already a display-only segment type.
- If display-only segment support is not clean, add that display-only segment first.

Candidate code paths:

- display segment model;
- tool result summary rendering;
- `TodoWrite` result formatter.

## Testing Plan

### Unit tests

Add or update tests for:

- live all-completed todos are hidden when the turn is ready;
- live all-completed todos create exactly one transcript-anchored completion item;
- completed todo summary follows transcript scrolling and is not rendered in bottom chrome;
- in-progress todos still show active task text;
- disk-loaded all-completed todos stay hidden;
- incomplete disk todos stay visible;
- friendly usage copy for ordinary failures;
- friendly usage copy for protection-only cases;
- compact usage copy under narrow width;
- workspace write boundary is counted as protection, not ordinary failure.

Suggested commands:

```bash
go test ./internal/tui -run 'TodoProgress|UsageSummary|ToolIssue|GateBlock' -count=1
go test ./internal/tui -count=1
```

### Full regression

Before shipping implementation:

```bash
go test ./... -count=1
git diff --check
```

### Visual acceptance

TUI visible behavior must be verified with real PTY screenshots.

Required command:

```bash
TUI_VISUAL_SOP_FULL=1 scripts/tui-visual-regression-sop.sh
```

Manual screenshot checks:

1. Completed todos do not occupy bottom chrome.
2. Completed todo summary appears at the original turn position and scrolls with the transcript.
3. In-progress todos still update live in bottom chrome.
4. Usage line uses user-friendly copy.
5. Protection does not look like an ordinary failure.
6. Bottom chrome does not cover assistant tail text.
7. Narrow terminal layout does not overflow or wrap into unreadable fragments.

## Acceptance Criteria

- In session-like flow with all todos completed, TUI no longer keeps `Tasks 6/6 all completed ✓ ctrl+y expand` permanently at the bottom.
- During active work, the current task remains visible and readable.
- Once work is complete, bottom chrome hides completed todo state.
- Completed todo summary is anchored in the transcript at the turn where it happened and scrolls with that conversation content.
- Usage primary text no longer says `失败 2` for ordinary historical tool errors.
- Usage primary text clearly distinguishes `未完成` from `安全保护`.
- Workspace write boundary is categorized as safety protection.
- Ordinary command/script failures remain observable and are not hidden.
- No raw JSON, `status=done`, `result=updated`, `err=`, or `blocked=` appears in primary TUI copy for these states.
- Tests and real Terminal screenshots prove the behavior.

## Risks and Mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Hiding completed todos too aggressively | User may lose track of what finished | Add transcript-anchored completion summary |
| Rewording failures as `未完成` hides severity | Real failures may look harmless | Use detail view and stronger color only for current blocking errors |
| Protection copy sounds like success | User may miss that original command did not run | Detail text must say `命令未执行` |
| New issue model duplicates transcript data | More state to maintain | Cap recent summaries and derive from existing query result |
| Visual layout regresses in narrow terminals | Bottom chrome may wrap badly | Add narrow width tests and real PTY screenshots |

## Review Checklist

- The plan keeps runtime safety behavior unchanged.
- The plan changes display semantics, not tool execution semantics.
- Completed todos are treated as completed history anchored to the relevant conversation turn, not current bottom status.
- Failure and protection counters remain auditable.
- Primary copy is understandable to non-engineering users.
- Detailed technical evidence remains accessible when expanded.
- Implementation can be shipped in phases without blocking on a full transcript model redesign.

## Recommended First Implementation Slice

Highest ROI first slice:

1. Add transcript-anchored completed todo summary and remove completed todo footer rendering.
2. Update `friendlyToolUsageSummary()` and `compactToolSummary()` wording.
3. Add focused tests for todo visibility and usage copy.
4. Run full tests and visual SOP.

This slice should solve the visible screenshot complaint without changing query runtime, session storage, permissions, or gate enforcement.
