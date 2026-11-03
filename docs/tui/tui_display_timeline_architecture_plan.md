# TUI DisplayTimeline 架构彻底修复方案

本文档记录 TUI 显示顺序漂移、底部遮挡和 live/transcript 重复回归的根因与彻底修复方案。结论很明确：继续修单个空行、单个 footer 高度或单个 tool 渲染分支，不能彻底解决这类问题；TUI 显示层需要从“可变消息拼接渲染”迁移到“不可变显示时间线渲染”。

## 背景

近期 TUI 已连续修过多类显示问题：

1. 用户消息提交后覆盖历史消息，或历史位置出现空白。
2. 完成态 assistant 尾部被底部 `Usage/input/status/controls` 覆盖。
3. 工具命令实时输出时先在底部出现，完成后又跳到上方或下方。
4. `assistant text A -> tool -> assistant text B` 被渲染成 `assistant text A+B -> tool`。
5. 10 轮连续对话后，live 区残留旧回答或 transcript 出现重复。

这些问题不是 provider 没输出、session JSONL 丢内容或终端随机裁剪。实际证据多次显示：JSONL 里完整 assistant 内容和 tool 事件都存在，问题发生在 TUI 把事件转换为屏幕内容的显示层。

## 当前根因

当前 TUI 显示状态不是单一事实源，而是由多套状态拼接：

| 状态 | 用途 | 问题 |
| --- | --- | --- |
| `m.messages` | 保存 user/assistant/status/recap 等消息 | assistant 流式文本会追加到同一个 message，无法表达中间插入过 tool |
| `m.toolActivity` | 保存当前或刚完成的工具活动 | 工具不是独立时间线片段，而是可被归档到 assistant sidecar |
| `m.liveDisplayBlocks` | 临时控制 live 区块顺序 | 只保存 block 类型和 message index，不能表达不可变事件顺序 |
| `transcriptPrintedCount` | 记录已打印到 terminal scrollback 的 message 数 | 以 message 为粒度，无法精确标记 tool/text/meta 片段 |
| `tea.Println` transcript layer | 固化完成态内容到自然 scrollback | 与 `View()` live layer 共存，容易出现保护区和重复清理不一致 |

核心错误模型是：TUI 试图用“聊天消息 + 工具 sidecar + live 临时块”模拟真实流式事件时间线。

真实事件顺序可能是：

```text
assistant text A
tool start
tool result
assistant text B
```

但当前 `StreamText` 在最后一条消息是 assistant 时会继续追加内容，因此内部会变成：

```text
assistant message = text A + text B
toolActivity = tool start/result
```

渲染时再按 `assistant message -> tools` 或 `liveDisplayBlocks` 拼接，就会把工具块挤到错误位置。即使局部测试覆盖了 “tool first” 或 “assistant first”，只要没有把 `assistant A -> tool -> assistant B` 作为不可变时间线，回归仍会出现。

底部遮挡是同一架构问题的另一面：completed transcript 和 live layer 分别由 `tea.Println` 与 `View()` 管理。只要错误排序把工具块或 assistant 尾部重新放回 live 区附近，就可能再次靠近底部 chrome，被 `Usage/input/status/controls` 挤压或覆盖。`tui_render_budget_architecture_fix_plan.md` 解决的是统一可见预算；本文档解决的是更上层的显示顺序和单一事实源。

## 为什么之前修不彻底

之前的修复大多是局部补丁：

- bottom guard：解决 completed transcript 末尾与 bottom chrome 贴住。
- visual line count：解决 CJK/ANSI/软换行导致的视觉行高漏算。
- live duplicate pruning：解决完成态内容残留在 live view。
- tool display block：尝试保持 tool 和 assistant 的局部顺序。
- render budget：统一宽高预算和 compact footer 策略。

这些修复都有价值，但没有删除根因：显示顺序仍由多个可变结构共同推导。只要 assistant 文本可以跨过 tool 继续追加到旧 message，只要 tool 可以作为 sidecar 归档到 assistant，只要 live 和 transcript 走不同渲染路径，就无法保证“屏幕上一旦出现的片段位置永远不变”。

## 彻底修复目标

必须建立以下硬不变量：

1. 所有用户可见输出片段按真实事件顺序进入同一条 display timeline。
2. segment 一旦进入 timeline，它与其他 segment 的相对顺序不可改变。
3. 后续 assistant token 只能追加到当前 active assistant segment；如果中间出现过 tool/agent/meta，再来的 assistant token 必须创建新 segment。
4. tool start、tool result、tool error 是 timeline segment 的状态更新，不是 assistant message 的附属渲染。
5. live view、completed transcript flush、resume history、acceptance export、截图 fixture 必须读取同一条 timeline。
6. bottom chrome 高度和 content viewport 高度从同一个 render budget 计算。
7. 用户提交后不能产生大段空白；模型完成后 transcript 尾部必须有足够 bottom chrome guard。
8. 不改变 provider 请求、query loop、session JSONL 主格式和模型上下文语义；本次先重构 TUI 显示层。

非目标：

1. 不把所有历史重新塞回 fixed viewport；自然 terminal scrollback 仍是目标体验。
2. 不默认开启 mouse tracking，不破坏终端原生选择复制。
3. 不把 transient UI 状态写入模型上下文。
4. 不在第一阶段重写 session transcript schema；必要时通过 adapter 从 transcript 恢复 timeline。

## 目标架构

新增 TUI 专用显示时间线：

```go
type DisplaySegmentKind string

const (
    SegmentUser          DisplaySegmentKind = "user"
    SegmentAssistantText DisplaySegmentKind = "assistant_text"
    SegmentTool          DisplaySegmentKind = "tool"
    SegmentAgent         DisplaySegmentKind = "agent"
    SegmentMeta          DisplaySegmentKind = "meta"
    SegmentStatus        DisplaySegmentKind = "status"
    SegmentError         DisplaySegmentKind = "error"
    SegmentRecap         DisplaySegmentKind = "recap"
)

type DisplaySegmentStatus string

const (
    SegmentStreaming DisplaySegmentStatus = "streaming"
    SegmentRunning   DisplaySegmentStatus = "running"
    SegmentDone      DisplaySegmentStatus = "done"
    SegmentErrorDone DisplaySegmentStatus = "error"
)

type DisplaySegment struct {
    ID      string
    Seq     int64
    TurnID  string
    Kind    DisplaySegmentKind
    Status  DisplaySegmentStatus

    ToolID   string
    ToolName string
    Content  string
    Detail   string
    Result   string

    CreatedAt time.Time
    UpdatedAt time.Time
}

type DisplayTimeline struct {
    Segments []DisplaySegment
}
```

`DisplayTimeline` 是 TUI 显示唯一事实源。`m.messages` 可以继续用于会话状态、query 上下文、session 持久化和旧逻辑兼容，但 TUI 屏幕渲染不能再从 `messages + toolActivity + liveDisplayBlocks` 拼顺序。

## Reducer 规则

所有 stream event 进入一个 reducer：

```text
User submit       -> append SegmentUser, mark printable
StreamText A      -> append or append-to-current SegmentAssistantText
StreamToolStart   -> close current assistant append window; append SegmentTool(running)
StreamToolResult  -> update SegmentTool(done/error)
StreamText B      -> append new SegmentAssistantText, not append to old A
StreamUsage       -> update usage panel, optionally append SegmentMeta if user-visible
TurnComplete      -> mark active segments done; flush printable timeline segments
Away recap        -> append SegmentRecap only as UI-only segment
Resume            -> rebuild timeline from transcript adapter; mark printed correctly
```

关键规则：

- `StreamText` 只有当最后一个 segment 是 `assistant_text` 且仍是当前 active text segment 时，才追加内容。
- 一旦插入 `tool`、`agent`、`meta`、`permission` 等非文本 segment，当前 assistant text segment 关闭。
- 工具结果只更新对应 `ToolID` segment，不改变 segment 顺序。
- 任何渲染路径不得重新按 message role 排序。

## Renderer 规则

拆成两个纯渲染入口：

```go
func RenderTimelineLive(t DisplayTimeline, budget renderBudget) string
func RenderTimelineTranscript(t DisplayTimeline, fromSeq int64, budget renderBudget) string
```

二者复用同一套 segment renderer：

```go
func RenderSegment(seg DisplaySegment, mode RenderMode, budget renderBudget) string
```

这样 live 和 completed transcript 的差异只体现在 mode：

| mode | 用途 | 特点 |
| --- | --- | --- |
| live | 当前 Bubble Tea `View()` | 只显示未固化或正在运行的 segment，受 content viewport 高度约束 |
| transcript | `tea.Println` 固化到 natural scrollback | 输出从 `printedSeq` 后的完成态 segment，并追加 bottom chrome guard |
| export | acceptance/test fixture | 不依赖终端，生成可比对文本和 PNG |

禁止在 renderer 外手写 tool/assistant 拼接顺序。

## Layout 规则

显示架构必须和 render budget 绑定：

```text
terminal height
- usage height
- input height
- status height
- controls height
= content viewport height
```

live view 只允许占用 content viewport。completed transcript flush 必须追加 `bottomChromeHeight` guard。所有宽度换行、视觉行高、table/CJK/ANSI 统计都使用同一个 `renderBudget`。

## 迁移方案

### Phase 0：冻结当前问题为回归测试

新增失败优先测试：

1. `assistant A -> tool -> assistant B` 顺序不变。
2. 多个 tool start/result 后 assistant 总结不能把 tool 挤到总结下方。
3. 真实 session replay：`c2c15ec3-e2d4-400d-b012-1b9cade19911`。
4. 真实 session replay：`761fc502-b61d-4531-9217-829b009cc508`。
5. 10 轮连续对话 + 工具 + 长尾哨兵 + PNG fixture。

### Phase 1：引入 timeline 数据结构和 reducer

- 新增 `internal/tui/display_timeline.go`。
- 在 stream event path 中同步写入 timeline。
- 保留 `m.messages` 原有写入，降低第一阶段 blast radius。
- 增加 timeline reducer 单元测试，不碰 renderer。

### Phase 2：live view 切换到 timeline renderer

- `liveTranscriptView()` 不再读取 `liveDisplayBlocks`。
- tool/agent/meta 显示均来自 timeline segments。
- 保留旧函数但只作为 compatibility fallback，测试全部指向 timeline。

### Phase 3：completed transcript flush 切换到 timeline renderer

- `pendingTranscriptBlocks()` 改为从 `printedSeq` 后的 timeline segment 生成。
- `transcriptPrintedCount` 逐步替换为 `transcriptPrintedSeq`。
- `markTranscriptPrinted()` 标记 seq，不再按 message count 推导。
- completed transcript 和 live view 不再重复显示同一 segment。

### Phase 4：resume/session adapter

- 从现有 JSONL transcript 构建 display timeline。
- 对历史 message/tool_call/tool_result 尽量恢复事件顺序。
- 对旧 transcript 无法恢复精确 token segment 的情况，按 entry 顺序生成稳定 segment。
- resume 后必须 flush 到 terminal scrollback，不能只留在 live layer。

### Phase 5：删除旧显示路径

- 删除或收缩 `liveDisplayBlocks`。
- 删除 tool sidecar 作为显示顺序来源的逻辑。
- `archiveToolActivityToAssistant()` 只保留持久化/上下文兼容用途，不参与 TUI ordering。
- 所有显示测试不再依赖 message sidecar 顺序。

## 验收矩阵

必须新增或扩展以下验收：

| 场景 | 断言 |
| --- | --- |
| assistant A -> tool -> assistant B | 屏幕和 transcript 都保持 A、tool、B 顺序 |
| 多 tool 连续执行 | 每个 tool start/result 位置不变 |
| 工具完成后 usage 到来 | tool 不跳位，assistant 不穿越 |
| completed flush | live 区不重复旧内容 |
| bottom chrome | 最后一条正文下方 guard >= chrome height |
| user submit | 不产生大段空白 |
| 10 轮连续对话 | 每轮唯一答案出现一次，final live 不残留 |
| CJK/Markdown table | 视觉行高准确，不遮挡 |
| resize/narrow | safe width 下不裁右侧字符 |
| resume | 历史显示完整，顺序稳定 |
| real session replay | 已知问题 session 不复发 |

建议命令：

```bash
go test ./internal/tui -count=1
scripts/tui-display-order-acceptance.sh
scripts/tui-render-budget-acceptance.sh
scripts/tui-ten-turn-acceptance.sh
scripts/tui-session-replay-acceptance.sh
go test ./... -count=1
git diff --check
```

新增 `scripts/tui-session-replay-acceptance.sh`，最少覆盖：

```text
c2c15ec3-e2d4-400d-b012-1b9cade19911
761fc502-b61d-4531-9217-829b009cc508
8eb2c866-3b52-416d-86dd-4cc6f0e1da5f
a0afeec7-c7a1-4402-8c2a-4df0ada5579b
```

每个 replay 输出：

- cleaned text fixture
- JSON report
- PNG screenshot fixture
- ordering assertions
- bottom guard assertions
- duplicate/live residue assertions

## 风险和控制

| 风险 | 控制 |
| --- | --- |
| 一次性替换显示层风险大 | 分 phase 引入 timeline，先双写后切读 |
| transcript 历史无法还原 token 级顺序 | adapter 以 JSONL entry 顺序为准，保证稳定不跳位 |
| 与 query/messages 上下文耦合 | 第一阶段不改 provider request 和 session schema |
| live 和 transcript 又分叉 | renderer 共享 `RenderSegment`，测试禁止独立拼接 |
| footer 再次覆盖正文 | 强制使用 render budget 和 bottom chrome guard |
| 新架构引入重复显示 | 用 `printedSeq` 而不是 message count 标记固化边界 |

## 完成定义

只有满足以下条件，才能把这类问题视为彻底修复：

1. TUI 显示顺序由 `DisplayTimeline` 单一事实源驱动。
2. `assistant A -> tool -> assistant B` 回归测试和 session replay 通过。
3. live view 和 completed transcript 使用同一套 segment renderer。
4. 底部 chrome 保护由统一 render budget 计算。
5. 旧的 `liveDisplayBlocks` 和 tool sidecar 不再决定屏幕顺序。
6. 10 轮连续对话 + 截图 fixture + replay matrix 全部通过。
7. `go test ./internal/tui -count=1`、`go test ./... -count=1`、`git diff --check` 全部通过。

在这些条件完成前，任何只改空行、guard、单个 tool render 分支的补丁，都只能算局部止血，不能标记为彻底修复。

## 实施状态（2026-07-11）

已完成 P0 DisplayTimeline 主链路：

- 新增 `internal/tui/display_timeline.go`，定义不可变 display segment 和 timeline。
- TUI stream path 双写 timeline：assistant text、thinking、tool start/result、nested agent progress、assistant meta、status/error/recap/loop 均有 segment 表达。
- `StreamText` 只追加到当前 active assistant segment；一旦插入 tool/agent/meta，后续 assistant text 会创建新 segment。
- `liveTranscriptView()` 和 `pendingTranscriptBlocks()` 优先从 timeline 渲染；旧 `messages/liveDisplayBlocks` 仅作为 compatibility fallback。
- tool result 更新原 tool segment，不改变 segment 顺序。
- `markTranscriptPrinted()` 同步 `printedSeq`，完成态 transcript flush 后 live 区不再重复显示同一 segment。
- 新增 `scripts/tui-session-replay-acceptance.sh`，读取真实 JSONL session 生成 text/json/png replay 工件。

本次实现保留了部分旧结构作为兼容层：

- `m.messages` 继续服务 query/session 状态和旧测试数据。
- `m.toolActivity` 继续服务工具状态、usage 和权限/工具面板。
- `m.liveDisplayBlocks` 保留为无 timeline 场景的 fallback；正常 stream/replay 路径已由 timeline 决定屏幕顺序。

验收已通过：

```bash
go test ./internal/tui -count=1
scripts/tui-display-order-acceptance.sh
scripts/tui-render-budget-acceptance.sh
scripts/tui-ten-turn-acceptance.sh
scripts/tui-session-replay-acceptance.sh
go test ./... -count=1
```

Replay 覆盖以下真实问题 session，并生成 cleaned text、JSON report 和 PNG fixture：

```text
c2c15ec3-e2d4-400d-b012-1b9cade19911
761fc502-b61d-4531-9217-829b009cc508
8eb2c866-3b52-416d-86dd-4cc6f0e1da5f
a0afeec7-c7a1-4402-8c2a-4df0ada5579b
```
