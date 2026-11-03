# TUI Natural Scrollback Design

本文档设计 Go Claude TUI 从固定高度消息 viewport 迁移到更接近原生 Claude Code 的自然终端 scrollback 体验。目标是先明确架构和风险边界，review 通过后再实施。

## 背景

当前 TUI 使用 Bubble Tea 管理整个可见页面：

```text
fixed header
viewport(messages)
tool activity / usage / suggestions / picker / attachments
input box
```

`messages` 会被格式化后放入 `viewport.Model`，`View()` 每次重新绘制固定高度页面。近期修复已经把欢迎 header 从 `viewport` 内移出，避免输入框扩展或 `GotoBottom()` 裁剪 header。但主聊天历史仍然在固定高度 viewport 里，消息变多后必须在 viewport 内部滚动。

用户期望的行为是：

```text
welcome header
history message 1
history message 2
history message 3
...
latest message

input box
```

新消息直接追加到终端底部，终端自然 scrollback 变长；查看旧消息使用 terminal 原生滚动，而不是进入 TUI 消息容器滚动。

## 根因

问题不在某个高度常量，而在渲染模型。

当前模型：

- Bubble Tea `View()` 返回完整页面。
- 聊天历史被放进 `viewport`。
- `viewport.Height` 由终端窗口高度减去 header、输入框、usage 等固定 chrome 高度得到。
- 新消息到来后调用 `refreshViewportAtBottom()`，`viewport.GotoBottom()` 保持内部滚动到底。
- 终端页面本身高度不变，只有 viewport 内部内容在滚动。

因此继续调整 `fixedChromeHeight()`、`normalViewportHeight()` 或 input 高度，只能缓解局部遮挡，不能实现自然 scrollback。

## 目标

P0 目标：

- 欢迎 header 成为 transcript 的第一段输出，随历史一起被终端自然上推。
- 用户消息、assistant 完整回复、错误、状态、recap 等完成态消息追加到终端 scrollback。
- 主聊天历史不再由 `viewport` 承载。
- 底部输入框保持可交互，并支持多行输入完整显示。
- 默认鼠标滚轮和终端选择复制仍走 terminal 原生行为，不被 TUI 抢占。
- 权限审批、slash suggestions、resume picker、rewind picker、附件栏、usage、tool activity 不回归。

非目标：

- 不追求像原生 Claude Code 一样逐 token 永久写入 scrollback。
- 不删除 `messages` 数据结构；它仍是会话内 UI 状态、测试和后续功能的事实源之一。
- 不改变 recorder/session transcript 的持久化格式。
- 不改变 query loop、provider、tool 执行和权限策略。

## 推荐架构

拆分为两层：

```text
Terminal transcript layer
  append-only, unmanaged by Bubble Tea, preserved in terminal scrollback

Bubble Tea live layer
  current input
  running status
  current streaming preview
  permission prompt
  tool activity
  usage panel
  slash suggestions
  resume / rewind picker
  attachment tray
```

Bubble Tea v1.3.10 已提供官方接口：

- `tea.Println(args ...interface{})`
- `tea.Printf(template string, args ...interface{})`

这两个命令会把内容打印到 Program 上方，输出不再由 Program 管理，会保留在 terminal scrollback。项目当前没有启用 `WithAltScreen()`，因此该机制可用。不要手写 ANSI escape 或直接 `fmt.Fprintln(output)`，避免破坏 Bubble Tea renderer 的光标和重绘状态。

## 数据模型调整

保留现有 `messages []message`，新增 transcript 渲染进度：

```go
type Model struct {
    // existing fields...
    messages []message

    transcriptPrintedHeader bool
    transcriptPrintedCount  int
    liveAssistantPreview    string
    liveThinkingPreview     string
}
```

含义：

- `messages`：仍保存当前 TUI 会话内可展示消息。
- `transcriptPrintedHeader`：欢迎 header 是否已经打印到 terminal scrollback。
- `transcriptPrintedCount`：`messages[0:n]` 已经固化打印到 terminal scrollback。
- `liveAssistantPreview`：streaming 中的 assistant 临时预览，完成前不永久打印。
- `liveThinkingPreview`：streaming 中的 thinking 临时预览，完成前不永久打印。

如果后续希望更稳，可以把 `transcriptPrintedCount` 替换成 message id，避免 resume/truncate 时索引语义复杂。P0 可以先用 count，但在 `applySessionResume()`、rewind 等会替换 `messages` 的路径必须显式 reset。

## 渲染职责

### Transcript layer

负责输出完成态内容：

- welcome header
- user message
- assistant final message
- error message
- status message
- recap message
- nested agent summary message

输出格式复用当前已有消息格式化逻辑，避免视觉风格漂移：

- `headerView(...)`
- `messageView(...)` 或拆出的 message rendering helper
- assistant markdown 美化逻辑继续复用

### Live layer

`View()` 不再渲染完整历史，只渲染当前交互态：

```text
current running assistant/thinking preview, if any
pending permission prompt, if any
running tool activity
usage panel
slash suggestions
resume picker / rewind picker
attachment tray
input box
```

这样底部 UI 仍由 Bubble Tea 管理，历史则交给 terminal scrollback。

## 事件流设计

### 启动

`NewModel()` 初始化时不直接打印，因为 Bubble Tea program 尚未运行。推荐在 `Init()` 返回命令：

```go
func (m Model) Init() tea.Cmd {
    return tea.Batch(
        textarea.Blink,
        tickEverySecond(),
        m.checkClipboardImage(),
        m.pollBackground(),
        m.printInitialTranscriptCmd(),
    )
}
```

`printInitialTranscriptCmd()` 打印 welcome header 和 `InitialMessages` 中已经存在的历史提示。

注意：`tea.Println` 是 `Cmd`，必须通过 `Update()`/`Init()` 返回，由 Bubble Tea renderer 执行。

### 用户提交 prompt

当前逻辑会：

1. 读取 textarea。
2. append `message{role:"user"}`。
3. 清空 textarea。
4. 进入 busy。
5. 调用 `refreshViewportAtBottom()`。

目标逻辑：

1. append user message 到 `messages`。
2. 返回 `flushTranscriptCmd()`，把新增 user message 打印到 terminal。
3. 清空 textarea，更新 live layer。
4. 进入 busy。
5. 不再调用主聊天 `refreshViewportAtBottom()`；只刷新 live preview / panels。

### 非 streaming assistant

收到 `responseMsg` 后：

1. append assistant/error message。
2. 归档 tool activity 到 assistant message。
3. 返回 `flushTranscriptCmd()` 打印新增完成态消息。
4. 清空 live previews。

### Streaming assistant

P0 不逐 token 永久打印。建议：

1. `StreamText` 继续累积到最后一条 assistant message。
2. `View()` 显示最后一条未完成 assistant 作为 live preview。
3. `StreamThinking` 继续累积到 thinking message，并显示 live preview。
4. `StreamToolStart/StreamToolResult` 继续更新 tool activity panel。
5. `StreamUsage` 或 `streamClosedMsg` 表示 turn 完成时，归档 tool activity 和 meta，然后 flush 新增完成态 messages 到 terminal scrollback。

关键点：streaming 期间不要每个 token 调用 `tea.Println`，否则会把同一条 assistant 回复拆成大量不可管理的 scrollback 行，也会增加闪烁和格式破碎风险。

### 权限审批

权限 prompt 是交互态，不应默认固化到 transcript。它应该留在 live layer：

```text
permission prompt
input/selection controls
```

用户确认后，如果已有 status/audit message 追加到 `messages`，再作为完成态 flush。

### Session resume / rewind

`applySessionResume()` 当前会替换 `messages`。迁移后必须同步 reset：

```go
m.messages = nil
m.transcriptPrintedCount = 0
m.transcriptPrintedHeader = false
```

策略选择：

- P0：resume 后打印恢复状态，例如 `Resumed session ...`。
- 已实现：同时按配置打印最近 N 条用户/助手历史消息，`tui.resumeHistoryLimit` 默认 `6`，设置为 `0` 可关闭；checkpoint、rewind、tool_call、tool_result 等内部 transcript entry 不作为聊天历史直接刷屏。

rewind 同理，不尝试擦除 terminal scrollback 中已经打印过的旧历史。rewind 后追加一条 status 说明当前逻辑状态已回退。

## viewport 调整

主聊天历史不再使用 `viewport`。但建议暂时保留 `viewport.Model`，用于：

- resume picker
- rewind picker
- 未来全屏历史查看模式
- 其他长列表临时 UI

主聊天模式下：

- `isViewportScrollKey()` 不再把 PageUp/PageDown/Home/End 路由给主消息 viewport。
- 鼠标滚动默认继续不启用 tracking，交给 terminal 原生 scrollback。
- `ctrl+o` 的 mouse scroll 模式需要收窄语义，建议只在 picker 或显式历史查看模式里启用。

## 分阶段实施计划

### Phase 1: 引入 transcript flush，但保持 viewport 兜底

目标：

- 新增 transcript 输出状态和 `flushTranscriptCmd()`。
- welcome header 和新增消息可以通过 `tea.Println` 打印。
- 暂时保留 `viewport` 历史显示，便于单测和手测对比。

验收：

- `flushTranscriptCmd()` 对 header、user、assistant、error/status 的输出顺序正确。
- streaming 期间不重复 flush 未完成 token。
- 不改变现有 UI 行为。

### Phase 2: 主 View 移除历史 viewport

目标：

- `View()` 不再渲染主聊天 `viewport.View()`。
- `View()` 只渲染 live layer 和 input box。
- welcome header 不再 fixed render，而是由 transcript layer 打印。

验收：

- 新消息追加到 terminal scrollback。
- 多行输入不会挤压 header 或历史。
- 长历史不需要 TUI 内部滚动。

### Phase 3: Streaming live preview

目标：

- streaming assistant/thinking 在 live layer 临时预览。
- turn 完成后固化打印到 terminal scrollback。
- tool activity 在 running 时显示，完成后归档到 assistant message。

验收：

- stream 文本不重复、不丢失、不拆碎。
- 工具调用完成后 assistant transcript 包含工具摘要。
- usage panel 正常累计。

### Phase 4: 滚动和鼠标行为收口

目标：

- 主聊天模式释放 PageUp/PageDown/Home/End 给 terminal，或至少不再操作主消息 viewport。
- 默认 mouse tracking 仍关闭。
- picker 模式仍可用键盘和鼠标滚轮。

验收：

- terminal 原生滚动可查看历史。
- terminal 原生拖选复制不回归。
- `/resume`、`/rewind` 选择交互不回归。

### Phase 5: 清理旧 viewport 依赖

目标：

- 删除或降级 `refreshViewportAtBottom()` 在主聊天路径中的调用。
- 把 message render helper 从 viewport render 中抽出来复用。
- 更新测试命名和断言，避免再要求聊天历史存在于 `viewport`。

验收：

- `go test ./internal/tui -count=1`
- `go test ./... -count=1`
- `git diff --check`
- 手动验证：短回复、长回复、多行输入、streaming、tool call、permission、resume、rewind、mouse copy。

## 测试计划

新增或调整单测：

- `TestModelPrintsWelcomeHeaderToTranscript`
- `TestModelFlushesUserMessageToTranscript`
- `TestModelFlushesAssistantAfterNonStreamingResponse`
- `TestModelDoesNotFlushPartialStreamingTokens`
- `TestModelFlushesStreamingAssistantWhenTurnCompletes`
- `TestModelLiveInputDoesNotContainHistoricalViewport`
- `TestModelPermissionPromptStaysInLiveLayer`
- `TestModelSessionResumeResetsTranscriptState`
- `TestModelMainChatDoesNotCaptureMouseWheelByDefault`

其中 `tea.Println` 是 command，需要通过命令返回值或测试 helper 抽象验证。推荐把待打印内容先构造成纯函数：

```go
func (m Model) pendingTranscriptBlocks() []string
func printTranscriptBlocks(blocks []string) tea.Cmd
```

单测主要覆盖 `pendingTranscriptBlocks()`，少量测试覆盖 cmd 被正确返回。

手动测试矩阵：

| 场景 | 期望 |
| --- | --- |
| 首次启动 | welcome header 出现在终端 scrollback，输入框在底部。 |
| 发送一条短消息 | user 和 assistant 依次追加到 scrollback。 |
| 长回复 | 终端页面自然变长，可用系统滚动查看旧内容。 |
| Ctrl+J 多行输入 | 输入框向下扩展，不顶掉历史或 header。 |
| streaming | live preview 更新，完成后固化为一条 assistant transcript。 |
| tool call | running tool 在 live panel，完成后 assistant 消息带工具摘要。 |
| permission ask | prompt 在 live layer，确认后不污染历史。 |
| `/resume` | picker 正常，恢复后 transcript 状态不重复打印。 |
| `/rewind` | picker 正常，回退后追加状态说明，不尝试清除旧 scrollback。 |
| 鼠标拖选 | 默认可以 terminal 原生复制。 |

## 风险和控制

| 风险 | 影响 | 控制 |
| --- | --- | --- |
| streaming 重复打印 | scrollback 里出现重复 token 或碎片 | P0 只在 turn 完成后固化打印。 |
| Bubble Tea 重绘闪烁 | 输入框附近视觉抖动 | 使用 `tea.Println`，避免手写 ANSI；分阶段手测。 |
| resume 长历史刷屏 | 终端瞬间输出大量旧消息 | P0 只打印恢复状态，不自动刷完整历史。 |
| 已打印历史无法撤销 | rewind 后 scrollback 仍能看到旧内容 | 追加 rewind status，明确当前会话状态已回退。 |
| 鼠标滚动被抢占 | 无法用 terminal 原生 scrollback | 默认不启用 mouse tracking；主聊天模式不处理 mouse wheel。 |
| 测试大量变更 | 难以判断是否行为回归 | 先加 transcript 纯函数测试，再替换 viewport 断言。 |

## 与现有文档的关系

`docs/architecture/runtime_modes.md` 当前写明 TUI 输出是 Bubble Tea viewport。实施完成后需要同步修改为：

- TUI transcript 使用 terminal natural scrollback。
- Bubble Tea live layer 只负责输入、状态、权限和临时交互面板。

`docs/session_recap/session_recap_plan.md` 当前提到 recap 属于 viewport 内容。实施完成后需要更新为 recap 作为完成态 transcript block 打印，live layer 只展示正在生成或最新状态。

`docs/todo.md` 和 `docs/compatibility_matrix.md` 可新增一条 TUI parity 记录，标注自然 scrollback 行为与原生 Claude Code 对齐进度。

## Open Questions

1. 恢复长会话时是否要自动打印最近 N 条历史，还是只显示恢复状态？
2. streaming 时是否需要更激进地接近原生 Claude Code，做可更新的 live block，而不是完成后一次性固化？
3. `ctrl+o` mouse scroll 模式是否保留给主聊天，还是只保留给 picker/历史查看模式？
4. 是否需要新增一个显式 `/history` 或 `/transcript` 全屏查看模式，补偿终端 scrollback 不可程序化搜索的问题？

## 推荐结论

建议按 Phase 1 到 Phase 5 实施。不要继续在主聊天 `viewport` 上叠加高度修补；自然 scrollback 需要把聊天历史从 Bubble Tea 固定页面中移出。`tea.Println` 是当前依赖版本里最合适的低风险入口，可以在不引入新依赖、不重写 renderer 的前提下实现目标体验。
