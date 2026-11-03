# TUI Markdown Readability Optimization Plan

## 背景

当前 TUI 模式中，AI 回复的 Markdown 已经能渲染标题、列表、表格、inline code 和自定义颜色，但普通正文的阅读质感仍然不够接近原生 Claude Code：

- 普通正文偏灰，和 Tools、Usage 等状态信息层级接近，阅读重点不够突出。
- inline code 使用红色文字和深色背景，视觉权重过高，长段回复中会抢正文注意力。
- 标题颜色偏高饱和，更像状态提示，不够像阅读型 Markdown。
- 表格中如果包含 inline code，单元格会被红色代码样式打碎，表格整体不够干净。
- Tools、Usage、mode hint 等状态区和正文都偏灰，层级区分不够清晰。

本优化目标是提升“普通文字、Markdown 正文、表格和 inline code”的可读性与保真度，而不是改变 TUI 的交互行为。

## 硬约束

以下约束优先级高于视觉优化：

1. 不得为了美化破坏 Markdown 语义。
2. 不得破坏现有 `<span style="...">...</span>` 和 `<font color="...">...</font>` 自定义颜色支持。
3. 不得破坏 fenced code block 的原样显示。
4. 不得破坏 GFM table 的结构识别和列边界。
5. 不得让 Tools、Usage、权限提示、输入框布局重新出现挤压、遮挡、滚动错位问题。
6. 不得把 Bash/Edit/Write 等权限或工具输出误当成 Markdown 内容重写。

## 差异原因

### 1. 终端字体本身不由 TUI 控制

字体家族、字号、字重渲染、抗锯齿主要由 Terminal/iTerm/WezTerm 控制。TUI 程序只能通过 ANSI 样式影响：

- 前景色；
- 背景色；
- bold / italic / underline / strikethrough；
- Markdown block 的布局；
- 表格线、缩进、间距。

因此“字体更美观”的代码侧优化重点应是 Markdown 主题和视觉层级，而不是试图在 TUI 里指定字体。

### 2. 当前主题偏终端代码风，不偏阅读风

当前 Markdown 渲染基于 `glamour/styles.DarkStyleConfig`，默认更像技术终端输出：

- inline code 权重偏重；
- link/code/title 颜色更鲜艳；
- 普通正文和状态信息之间的亮度层级不够稳定。

### 3. inline code 样式过强

当前 inline code 在截图中表现为红色文字 + 深色背景。对于字段名、文件路径、SQL 标识符这类高频内容，过强样式会导致整段回复被红色块切碎。

### 4. 表格和 inline code 样式互相干扰

表格本身需要列边界和横线提供结构感，但单元格内大量 inline code 使用高饱和颜色，会降低表格整体可读性。

## 优化目标

1. 普通正文更清晰：正文应比 Tools/Usage/status 更亮。
2. inline code 更克制：保留可识别性，但不抢正文。
3. 标题更像文档标题：降低高饱和感，保留轻微强调。
4. 表格更干净：边框稳定、单元格文字自然、inline code 不破坏列结构。
5. 状态信息更弱：Tools、Usage、耗时、cwd、mode hint 继续可读，但不抢 AI 回复正文。

## 建议视觉规范

### 普通正文

- 前景色：接近 `252` 或 `253`。
- 不加背景。
- 不默认 bold。

### 标题

- H1/H2/H3 使用柔和蓝青或蓝紫，例如 `39`、`75`、`81`。
- 可 bold，但避免高饱和 cyan 长时间占据屏幕。
- 标题前后间距保持现状，不额外扩大。

### inline code

建议从“红色 + 强背景”改为以下之一：

- 方案 A：淡紫/淡蓝前景 + 极弱暗背景；
- 方案 B：淡紫/淡蓝前景，无背景；
- 方案 C：只保留轻微暗背景，文字使用正文色。

推荐先采用方案 A，但背景必须低对比，不能形成大面积色块。

### code block

- fenced code block 仍保持等宽、原样、可复制。
- 不改变代码块内容。
- 不为了 wrap 或表格美化重写代码块内部文本。

### 表格

- 边框使用浅灰，例如 `248`、`249`、`250`。
- 表头可轻微 bold。
- 表格内普通文本用正文色。
- 表格内 inline code 不使用强红底。
- 宽表格仍允许截断或 wrap，但不能破坏列边界。

### 状态信息

- Tools、Usage、cwd、session、elapsed 等状态信息使用弱灰，例如 `240`、`241`、`242`。
- 状态信息不应和正文使用同等亮度。

## 实施方案

### 阶段 1：定制 Markdown 阅读主题

修改 `internal/tui/app.go` 中的 `newAssistantMarkdownStyle()`：

- 基于现有 `styles.DarkStyleConfig`；
- 调整普通正文、标题、列表、quote、link、inline code、code block 的颜色；
- 保留现有 `glamour.WithWordWrap(width)` 行为；
- 保留现有 rich inline style 预处理和恢复流程。

关键要求：

- `extractRichMarkdownInlineStyles()` 和 `restoreRichMarkdownInlineStyles()` 不能被绕过；
- `<span>/<font>` 指定的颜色必须最终覆盖默认主题；
- theme 只能改变默认 Markdown 样式，不能吞掉用户自定义样式。

### 阶段 2：弱化 inline code

调整 `assistantMarkdownStyle.Code` / inline code 相关配置：

- 去掉或弱化红色背景；
- 使用柔和前景色；
- 保障 file path、SQL field、表名仍然容易识别。

验收点：

- `patient_oximeter_record` 不再显示为强红底；
- 长句中多个 inline code 不会把正文切成碎块；
- 自定义 `<span style="color:red">` 仍能显示红色。

### 阶段 3：表格样式对齐阅读主题

当前表格已通过专用 renderer 输出稳定横线和列边界。后续优化应重点处理：

- 表格线颜色；
- 表头强调；
- 单元格内 inline code 的弱化；
- 中文宽字符列宽稳定性。

不得回退为原始 `|---|` 文本。

### 阶段 4：状态区层级微调

评估 `statusStyle`、`assistantStyle`、`titleStyle`：

- 正文亮度高于状态信息；
- Tools/Usage 不抢正文；
- permission on/bypass 等风险提示仍保留足够醒目。

## 测试计划

### 单元/回归测试

新增或扩展 `internal/tui/app_test.go`：

- 普通 Markdown 段落渲染不为空且不被误判为 raw markdown。
- inline code 不再使用强红底默认样式。
- `<span style="color:red">red</span>` 自定义颜色仍然生效。
- `<font color="blue">blue</font>` 自定义颜色仍然生效。
- fenced code block 中的 Markdown 表格不被重写。
- GFM table 仍能渲染稳定横线和列边界。

### 金标测试

构造一段代表性 AI 回复：

- 普通中文段落；
- 列表；
- inline code；
- GFM table；
- fenced code block；
- `<span>/<font>` 自定义颜色。

断言：

- 表格有横线和列边界；
- fenced code block 内容不被修改；
- 自定义颜色 token 仍被替换为 ANSI 样式；
- 默认 inline code 样式不再是强红底。

### 手工真机检查

在真实 TUI 中检查：

- 普通正文比 Tools/Usage 更清晰；
- inline code 不刺眼；
- 表格可读；
- 终端复制不受影响；
- 输入框、Usage、权限弹窗布局不变。

## 风险与回滚

### 风险

- `glamour` style config 字段较多，改错可能导致某些 Markdown block 样式异常。
- 过度弱化 inline code 会降低代码/路径识别度。
- 表格代码块化渲染和 glamour 主题叠加时，颜色可能被二次处理。

### 回滚策略

- 所有改动应集中在 `newAssistantMarkdownStyle()` 和少量表格样式辅助函数。
- 如果出现 Markdown 语义损坏，优先回滚主题字段，不回滚权限和 scrollback 相关代码。
- 自定义颜色 span/font 一旦测试失败，必须立即停止视觉优化并修复兼容性。
