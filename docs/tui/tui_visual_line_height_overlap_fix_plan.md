# TUI Visual Line Height Overlap Fix Plan

本文档记录 TUI 底部内容遮挡问题的第三阶段根因、修复方案和验收标准。前两阶段分别解决了完成态 transcript 被底部 chrome 重绘覆盖、以及 away recap 追加后 completed assistant tail 丢失锚点的问题；本阶段处理的是中文长段落、ANSI 样式和终端软换行导致的视觉行高漏算。

## 现象

复现会话：

```text
sessionId: 1ecc1f57-97a5-42ca-ac82-53762ee83ed9
transcript: $HOME/.go-claude/projects/Users-example-GolandProjects-anything-ai/1ecc1f57-97a5-42ca-ac82-53762ee83ed9.jsonl
```

截图中最后一段停在：

```text
下一步的重点不是继续扩骨架，而是把已有骨架填实、把用户从入口到实战的路
```

但 transcript 中完整 assistant 内容实际包含：

```text
下一步的重点不是继续扩骨架，而是把已有骨架填实、把用户从入口到实战的路径打通。
```

同时调试日志显示本轮模型正常结束：

```text
2026-07-09T23:53:58+08:00 model.request.finished stop_reason=end_turn
2026-07-09T23:54:00+08:00 query.run.finished stop_reason=end_turn
2026-07-09T23:54:20+08:00 session.recap.started
2026-07-09T23:54:27+08:00 session.recap.finished
```

这说明问题不是模型提前截断，也不是 JSONL 持久化丢内容，而是 TUI 当前屏布局对真实终端行数估计不足。

## 根因

当前 TUI 有三层保护：

1. `flushTranscriptCmd()` 在完成态 transcript 后追加 bottom guard。
2. `completedAssistantTailView()` 在回答完成后把最近一条 assistant 尾部保留在 live viewport。
3. `refreshViewport()` 在 completed tail 激活时强制 `GotoBottom()`。

这些保护仍然依赖一个过窄的前提：`lineCount(text)` 等于真实屏幕占用高度。

当前实现：

```go
func lineCount(text string) int {
    if text == "" {
        return 0
    }
    return strings.Count(text, "\n") + 1
}
```

这个函数只数逻辑换行符，不计算终端软换行。对中文长句、宽字符、ANSI 样式、Glamour markdown 渲染后的长行，真实终端会按列宽折成多行，但 `lineCount` 仍只算 1 行。

结果是：

```text
rendered assistant tail
  -> 视觉上占 N 行
  -> lineCount 只算 M 行，M < N
  -> viewport.Height / bottom guard / fixedChromeHeight 偏小
  -> Usage + input + controls 提前进入回答尾部区域
  -> 用户看到最后几个字像被遮住或截断
```

截图中的中文长句被截在“路”，正是视觉软换行漏算的典型表现。

## 彻底修复定义

这里的“彻底修复”只针对已验证的 TUI 布局类问题，必须同时满足：

1. 模型完整输出、`stop_reason=end_turn`、JSONL 完整落盘时，TUI 当前屏不能遮挡最后一条 assistant 尾部。
2. 中文长句、英文长词、emoji、ANSI 样式、markdown heading/list/bold、Usage 换行、多行输入框都按真实视觉行高参与布局。
3. `View()` 返回内容的视觉高度不能超过当前 `WindowSizeMsg.Height`，除非 Bubble Tea/terminal 明确处于历史 scrollback 打印阶段。
4. bottom guard 不写入 JSONL，不污染 export markdown，不修改模型原始输出。
5. recap/status/system 等内部消息不能抢占 completed assistant tail。
6. 真实 PTY/tmux 截图和单元测试都覆盖同一类复现文本。

不能宣称修复的范围：

1. provider 返回半句、网络中断、`partial_stream_error`。
2. `stop_reason=max_tokens` 或上游 API 自身截断。
3. terminal emulator 字体宽度与 `runewidth` 不一致的极端情况。
4. 用户手动把窗口缩到小于最小可用高度后仍要求所有区域完整显示。

## 方案

### 1. 引入视觉行高计算

新增集中 helper，而不是继续散落调用 `lineCount`：

```go
func visualLineCount(text string, width int) int
```

语义：

- 先按 `\n` 拆分逻辑行。
- 对每一行移除 ANSI escape sequence 后计算 display width。
- 使用 `runewidth.StringWidth` 或 `lipgloss.Width` 得到终端列宽。
- 每个逻辑行贡献 `max(1, ceil(displayWidth / width))` 行。
- 空行贡献 1 行。
- `width <= 0` 时退化为当前 `lineCount`，避免未知宽度时崩溃。

注意点：

- helper 必须处理 ANSI 样式，不能把颜色控制符算进宽度。
- helper 只用于布局估算，不改变原始渲染文本。
- 对已经被 `glamour.WithWordWrap(width)` 换行的内容，helper 应保持幂等：已有换行照数，仍能兜底超过 width 的残余长行。

### 2. 替换所有布局关键路径

第一批必须替换：

```text
viewportHeightForContent(content)
fixedChromeHeight()
transcriptBottomGuardLines()
resumePickerStartY()
rewindPickerStartY()
```

对应规则：

- live viewport 内容用 `m.contentWidth()` 计算视觉高度。
- Usage、todo、inputBox、status/controls 等 bottom chrome 用 `m.contentWidth()` 或 `m.width` 中更贴近实际渲染宽度的值计算。
- picker start Y 不能再用普通 `lineCount(strings.Join(...))`，否则 picker 点击区域会和视觉位置错位。

第二批审计：

```text
测试 helper、断言、文档中所有用 lineCount 表示屏幕行数的位置。
```

保留 `lineCount` 只用于确实需要逻辑行数的场景，例如字符串块数量断言；视觉布局一律不能直接用它。

### 3. 让 viewport 高度以视觉行数为准

当前逻辑：

```go
contentHeight := lineCount(content)
return max(3, min(normalHeight, contentHeight))
```

调整为：

```go
contentHeight := m.visualLineCount(content)
return max(3, min(normalHeight, contentHeight))
```

目标：

- 中文长段落即使没有显式 `\n`，也会让 viewport 高度扩展到真实占用高度。
- 内容超过可用高度时，viewport 继续裁剪到底部，底部 chrome 不覆盖 tail。
- 短内容仍贴近 prompt，不引入大块空白。

### 4. bottom guard 按真实 bottom chrome 高度计算

当前 guard 使用：

```go
lineCount(m.usageView())
lineCount(m.inputBoxView("Ready"))
```

调整为视觉行高：

```go
visualLineCount(m.usageView(), m.contentWidth())
visualLineCount(m.inputBoxView("Ready"), m.width)
```

同时把以下区域纳入审计：

- `todoProgressView()`
- `slashSuggestionView()`
- `resumePickerView()`
- `rewindPickerView()`
- `attachmentTrayView()`
- `quietConversationModeHintView(status)`

如果某个区域在 `View()` 中出现在 live viewport 和 input box 之间，它就必须被 bottom guard 或 fixed chrome 计算覆盖。

### 5. 统一“屏幕高度不溢出”测试 helper

新增测试 helper：

```go
func visualLineCountForTest(text string, width int) int
func assertViewFitsTerminal(t *testing.T, model Model)
```

断言逻辑：

```go
view := stripANSI(model.View())
if got := visualLineCountForTest(view, model.width); got > model.height {
    t.Fatalf("view visual height = %d, terminal height = %d\n%s", got, model.height, view)
}
```

这个测试是防复发关键。只断言 `strings.Contains(view, "路径打通")` 不够，因为文本存在但仍可能在真实终端下被 bottom chrome 挤掉。

### 6. 加入本次真实中文复现用例

新增回归测试：

```go
TestModelCompletedAssistantTailFitsCJKSoftWrappedLines
```

测试数据必须包含真实尾句：

```text
下一步的重点不是继续扩骨架，而是把已有骨架填实、把用户从入口到实战的路径打通。
```

场景：

1. 构造 `Width` 接近截图终端宽度、`Height` 接近截图高度。
2. 构造包含 markdown heading、bold、长中文段落的 assistant。
3. 模拟 `responseMsg` 完成，写入 usage。
4. 模拟 `awayRecapMsg` 追加 recap。
5. 断言 viewport 和 View 都包含 `路径打通。`。
6. 断言 `assertViewFitsTerminal` 通过。
7. 断言 recap 不替代 completed assistant tail。

再补两个边界：

- `TestModelBottomGuardCountsWrappedCJKUsage`
- `TestModelPickerStartYUsesVisualLineHeight`

### 7. 真实 PTY 验收

单元测试不能完全替代终端真实渲染。实现后必须跑真实 PTY 验收：

```bash
go run cmd/golang-cc/main.go --cwd $HOME/GolandProjects/anything-ai
```

验收流程：

1. 使用同一 terminal/tmux 尺寸复现。
2. 发送同类问题或用测试 hook 固定返回同一段 assistant 文本。
3. 等待 away recap 触发。
4. 截取当前屏文本和截图。
5. 检查当前屏包含：

```text
路径打通。
status  Ready
controls
```

6. 检查 Usage/input/controls 没有覆盖回答尾句。
7. 检查 JSONL assistant content 没有额外空行或 padding。

建议保存证据：

```text
/tmp/gocc-tui-visual-line-height-before.txt
/tmp/gocc-tui-visual-line-height-after.txt
/tmp/gocc-tui-visual-line-height-after.png
```

## 实施顺序

1. 新增 `visualLineCount(text, width)` 和 ANSI 清理复用。
2. 替换 `viewportHeightForContent`、`fixedChromeHeight`、`transcriptBottomGuardLines`、picker start Y。
3. 新增视觉高度测试 helper。
4. 加入本次 `1ecc1f57-97a5-42ca-ac82-53762ee83ed9` 的中文尾句回归测试。
5. 跑窄屏、CJK、recap、completed tail 相关测试。
6. 跑全量测试和 diff check。
7. 启动真实 TUI 做 PTY 验收，保存 before/after evidence。

## 验证命令

最小相关测试：

```bash
go test ./internal/tui -run 'TestModel(CompletedAssistantTail|BottomGuard|Viewport|Picker|CJK|Visual)' -count=1
```

模块测试：

```bash
go test ./internal/tui -count=1
```

提交前完整验证：

```bash
go test ./... -count=1
git diff --check
```

真实链路验证：

```bash
go run cmd/golang-cc/main.go --cwd $HOME/GolandProjects/anything-ai
```

## 风险与控制

风险：

- 视觉行高估算过大，导致回答和输入框之间空白变多。
- 视觉行高估算过小，仍然遮挡。
- ANSI 清理不完整，彩色 markdown 行宽被算错。
- picker 点击位置和视觉行数不一致。
- 修复普通段落时破坏 markdown 表格、代码块或权限弹窗。

控制：

- helper 集中实现，所有布局路径复用同一算法。
- 用真实中文长句、英文长词、emoji、ANSI markdown、Usage 换行覆盖测试。
- `assertViewFitsTerminal` 作为金标，避免只检查字符串存在。
- 保留原始文本，不把任何 padding 写入 transcript。
- 真实 PTY 验收必须和单元测试一起作为完成条件。

## 完成标准

此问题只有在以下全部满足后才能标记完成：

1. `TestModelCompletedAssistantTailFitsCJKSoftWrappedLines` 通过。
2. `assertViewFitsTerminal` 覆盖 completed assistant tail + away recap 场景。
3. `go test ./internal/tui -count=1` 通过。
4. `go test ./... -count=1` 通过。
5. `git diff --check` 通过。
6. 真实 TUI/tmux 验收截图显示 `路径打通。` 完整可见。
7. session JSONL 中 assistant content 未被 padding 污染。

如果只完成单元测试、没有真实 PTY 验收，不能宣称彻底修复，只能标记为“代码层修复待真机验证”。
