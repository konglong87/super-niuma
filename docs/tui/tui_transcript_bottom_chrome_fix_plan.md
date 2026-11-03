# TUI Transcript Bottom Chrome Fix Plan

本文档记录 TUI 模式下“最后几句话看起来不完整”的根因、修复方案和验收标准。目标是先固化技术方案，再进入实现。

## 背景

复现场景：

```bash
go run cmd/golang-cc/* --cwd $HOME/GolandProjects/anything-ai
```

用户提问：

```text
你怎么看待当前项目？有啥用？有啥优化的地方？
```

TUI 截图里最后只看到：

```text
一句话：骨架
```

但 session transcript 中完整 assistant message 已经落盘，最后一句实际是：

```text
**一句话**：骨架结实、内容丰富，但"门面"（首页+导航）和"中间层"（Agent路径）需要修。先把3-ai-agents填上或合并，再把导航从10个砍到5-6个，项目体验会有质的提升。
```

同时最终 usage 显示输出 token 没有接近 code mode 默认 `max_tokens=32000`，因此这不是模型输出被 token 上限截断，也不是 transcript 持久化丢内容。

## 根因

当前 TUI 已经采用自然 scrollback 方向：

- streaming 中的最后一条消息留在 live viewport。
- 完成态消息通过 `flushTranscriptCmd()` 使用 `tea.Println(...)` 打到 terminal scrollback。
- 打印完成后调用 `markTranscriptPrinted()`，之后 `View()` 只渲染 live layer、Usage、输入框、status/controls 等底部 chrome。

关键问题是：完成态 transcript 打印后，底部 chrome 立即重绘。若 transcript 最后一行刚好落在终端底部附近，随后重绘的 `Usage`、输入框、`status/controls` 会占据这些屏幕行，用户视觉上就会看到最后几行像被截断。

这类问题本质上是 transcript layer 和 live layer 的屏幕空间协调问题，不是模型质量问题。

相关代码位置：

- `internal/tui/app.go` 的 `View()`：拼接 live viewport、Usage、输入框和 status/controls。
- `internal/tui/app.go` 的 `flushTranscriptCmd()`：把完成态 blocks 通过 `tea.Println(...)` 打到 terminal scrollback。
- `internal/tui/app.go` 的 `markTranscriptPrinted()`：标记已经固化到 scrollback 的消息。
- `internal/tui/app.go` 的 `refreshViewport()`：刷新 live viewport 内容和高度。

## 修复目标

P0 目标：

1. 完成态 assistant 回复最后一行在终端中稳定可见，不被 `Usage`、输入框、status/controls 遮挡。
2. 不改变模型原始输出，不污染 session transcript JSONL。
3. 不改变 query loop、provider、tool 执行、权限审批和 recorder 持久化格式。
4. streaming 期间仍只展示 live preview，不逐 token 写入 permanent scrollback。
5. resume history、background update、recap 等完成态 UI 消息也走同一套 transcript flush 保护策略。

非目标：

1. 不解决模型真的提前停止、provider 返回不完整内容或网络中断导致的 partial stream。这类问题应由 stop reason、partial stream recovery 和 provider telemetry 处理。
2. 不把所有历史重新塞回 viewport。当前自然 scrollback 架构方向保持不变。
3. 不用硬编码固定空行数凑效果。

## 推荐方案

引入集中式 transcript flush 策略：

```go
func (m Model) flushTranscriptCmd() tea.Cmd {
    return m.flushTranscriptCmdFor(flushReasonDefault)
}
```

内部统一做三件事：

1. 收集 `pendingTranscriptBlocks()`。
2. 根据当前底部 chrome 高度计算 bottom guard 行数。
3. 使用 `tea.Println(...)` 输出 transcript blocks 和 bottom guard。

示意：

```go
func (m Model) flushTranscriptCmdFor(reason transcriptFlushReason) tea.Cmd {
    blocks := m.pendingTranscriptBlocks()
    if len(blocks) == 0 {
        return nil
    }
    output := formatTranscriptBlocks(blocks)
    output += strings.Repeat("\n", m.transcriptBottomGuardLines(reason))
    return tea.Println(output)
}
```

### Bottom guard 计算

不要写死 `3` 或 `5` 行。应复用现有底部 chrome 的真实渲染高度：

```go
func (m Model) transcriptBottomGuardLines(reason transcriptFlushReason) int {
    chrome := 0
    if usage := m.usageView(); usage != "" {
        chrome += lineCount(usage)
    }
    chrome += lineCount(m.inputBoxView("Ready"))
    chrome += 1 // transcript 与 bottom chrome 的最小视觉间隔
    return clamp(chrome, 1, maxTranscriptBottomGuardLines)
}
```

设计要点：

- guard 只进入 terminal 输出层，不进入 `session.Entry.Content`。
- guard 行数来自实际 UI 高度，未来 Usage 换行、textarea 多行、controls 变多时能自动适配。
- 设置上限，例如 `maxTranscriptBottomGuardLines = 12`，避免异常状态下刷出过多空行。
- 完成态 flush 后再 `markTranscriptPrinted()`，保证未成功生成 cmd 前不提前标记。

### 统一触发点

所有已经完成、应固化到 scrollback 的路径都调用同一个 flush 函数：

- 用户提交 prompt 后打印 user message。
- `responseMsg` 完成后打印 assistant/error message。
- `StreamSessionResume` 打印恢复历史。
- background update 打印后台任务状态。
- `awayRecapMsg` 打印 recap。

streaming 中的 `StreamText` / `StreamThinking` 不调用 bottom guard，因为此时内容仍属于 live preview。

## 为什么这个方案可以彻底修复

在当前已确认的故障类型里，根因是底部 chrome 重绘覆盖 terminal scrollback 末尾可视区域。bottom guard 的作用是让完成态 transcript 末尾与下一次底部 chrome 重绘之间始终保留足够屏幕空间。

因此它能彻底修复这类问题：

- 模型完整输出已经到达。
- transcript JSONL 已完整落盘。
- TUI 屏幕只显示末尾的一部分。
- Usage/input/status 区域紧贴或覆盖最后几行。

它不能声称修复所有“回答不完整”：

- provider 真的返回半句。
- stop reason 是 `max_tokens`、`partial_stream_error` 或网络中断。
- 用户终端本身 scrollback 设置太小。
- 外部 terminal emulator 有特殊重绘 bug。

这部分边界必须在验收报告里区分。对本次截图对应的问题，方案是对因修复。

## 扩展性

这个方案扩展性较好，原因是把“底部保护区”变成 transcript flush 策略，而不是散落在各个事件分支里：

1. 后续新增面板，比如 goal progress、sub-agent dashboard、permission summary，只要它出现在 bottom chrome，高度计算可以统一纳入。
2. 后续若要支持 compact mode、quiet mode、mobile terminal mode，可以按 `transcriptFlushReason` 或 layout mode 调整 guard。
3. 后续若把 `transcriptPrintedCount` 升级为 message id，flush 策略不需要大改。
4. 后续若实现逐段 transcript streaming，也可以复用同一个 guard，只改变 flush cadence。

需要避免的反模式：

- 在 `responseMsg` 分支里手写空行。
- 在 `View()` 里把完整历史重新放回 viewport。
- 为某个 terminal 尺寸写特殊 case。
- 把 padding 写入 transcript JSONL 或 export markdown。

## 测试计划

### 单元测试

新增或更新 `internal/tui/app_test.go`：

1. `TestModelFlushTranscriptAddsBottomGuard`
   - 构造完成态 assistant message。
   - 调用 flush test helper。
   - 断言输出包含完整最后一句。
   - 断言输出末尾包含至少 `usage + inputBox + spacer` 行 guard。

2. `TestModelTranscriptBottomGuardDoesNotPolluteMessageContent`
   - 构造 `message{role:"assistant"}`。
   - flush 后检查 `model.messages[i].content` 未追加空行。
   - 如测试 recorder，则检查 session entry content 未变化。

3. `TestModelStreamingPartialDoesNotAddBottomGuard`
   - streamingActive 为 true。
   - `StreamText` partial 仍停留在 live viewport。
   - pending transcript blocks 为空或不含当前 partial。

4. `TestModelBottomGuardTracksWrappedUsage`
   - 构造较窄宽度，让 Usage 换行。
   - 断言 guard 行数随 `usageView()` 行数增加。

### 回归测试

执行：

```bash
go test ./internal/tui -count=1
go test ./... -count=1
git diff --check
```

### 真实 TUI 验收

使用原复现命令：

```bash
go run cmd/golang-cc/* --cwd $HOME/GolandProjects/anything-ai
```

发送同一问题：

```text
你怎么看待当前项目？有啥用？有啥优化的地方？
```

验收点：

1. 最后一段完整可见，不停在“骨架”。
2. `Usage`、输入框、`status/controls` 不覆盖回答末尾。
3. 终端原生滚动能看到完整历史。
4. session JSONL 中 assistant content 没有额外 padding。

## 实施顺序

1. 提取 `transcriptBottomGuardLines(...)`，只读现有 UI 高度，不改变布局。
2. 改造 `flushTranscriptCmd()`，统一追加 guard。
3. 跑 TUI 单元测试，补齐缺失断言。
4. 跑全量 Go 测试和 `git diff --check`。
5. 用真实 TUI 复现原问题并截图或记录可见结果。

## 风险与回滚

风险：

- guard 太少：仍可能遮挡。
- guard 太多：回答后出现明显空白，体验松散。
- guard 计算调用了带副作用的 view helper：可能导致测试不稳定。

控制：

- guard 使用真实渲染高度并设置上限。
- 单测覆盖窄屏、Usage 换行、多行 textarea。
- 不修改 transcript 持久化格式，回滚时只需撤销 TUI flush 层改动。

回滚策略：

- 如果出现终端空白过多，先调小 `maxTranscriptBottomGuardLines` 或 spacer。
- 如果出现内容仍遮挡，优先检查 `inputBoxView("Ready")` 与真实完成态 status 是否一致。
- 如果出现 transcript 污染，立即回滚 flush 层 padding 拼接位置，确保 padding 只在 `tea.Println` 输出字符串中存在。
