# TUI 工具调用进度展示增强方案（v3 — 渲染策略重构）

## 问题背景

用户希望看到 go-claude 实时在干什么——读哪个文件、搜什么关键词、改哪行代码。当前 go-claude 的 LLM 通过"口述计划"来满足这个需求（"让我先找到 inline code 颜色相关的代码"、"现在看下当前值"），但这浪费 token、干扰阅读。

正确方向：**提示词层禁止口述，TUI 层增强进度展示**。让用户在 UI 上看到过程，而不是让 LLM 用文字描述过程。

## 当前 TUI 工具调用展示机制

所有代码在 `internal/tui/app.go`（单文件 Bubble Tea 模型，~4028 行）。

### 事件总线架构

go-claude 的数据流是事件总线模式：

```
query.Session（大模型交互）
  → streamEvent（带类型的事件）
  → cli.go 回调（转换层）
  → tui.StreamEvent（TUI 事件）
  → Bubble Tea Update()（事件处理器）
  → View()（渲染）
```

事件类型包括：`StreamText`、`StreamThinking`、`StreamToolStart`、`StreamToolResult`、`StreamUsage`、`StreamNestedProgress` 等。TUI 在 `Update()` 里按类型分发处理，在 `View()` 里按类型渲染。

这个架构本身没问题，和 Claude Code 的架构类似。问题不在事件总线，而在**渲染策略**。

### 数据结构

```go
// app.go:277 — 工具活动条目
type toolActivityItem struct {
    ToolName string    // 工具名：Read, Grep, Bash, Edit 等
    ToolID   string    // API 返回的 tool_call ID
    Status   string    // "running" / "done" / "error"
    Detail   string    // 工具输入的截断摘要（一行）
    Started  time.Time // 开始时间
    Elapsed  time.Duration // 完成耗时
    IsError  bool
}
```

### 事件流缺陷（关键发现）

```
query.Session → cli.go → TUI

OnToolCall 回调（cli.go:1015-1017）：
  ToolCallEvent{ID: block.ID, Name: block.Name}  ← 只有 ID 和 Name，没有 Input！
  → StreamEvent{Type: StreamToolStart, ToolID: event.ID, ToolName: event.Name}
  → TUI 收到：工具名 + ID，不知道输入参数

OnToolResult 回调（cli.go:1019-1020）：
  ToolTrace{ID, Name, Input, Output, IsError}  ← 有 Input 和 Output
  → StreamEvent{Type: StreamToolResult, ToolID: trace.ID, ToolName: trace.Name, Output: trace.Output, IsError: trace.IsError}
  → TUI 收到：工具名 + ID + Output + IsError，但 Input 没传！
```

`StreamToolStart` 事件不携带工具输入参数，`StreamToolResult` 事件也不携带工具输入参数。TUI 在工具开始时只知道"正在用 Read"，不知道"正在读哪个文件"。

### 生命周期

```
StreamToolStart 事件（只有 ToolName + ToolID）
  → startToolActivity() (app.go:2527)
  → 创建 toolActivityItem{Status: "running", Detail: truncate(oneLine(event.Output))}
  → 但 event.Output 在 StreamToolStart 时通常是空的！
  → 所以 Detail 在工具开始时通常是空的

StreamToolResult 事件（有 ToolName + ToolID + Output + IsError）
  → finishToolActivity() (app.go:2553)
  → 覆盖 Detail = truncate(oneLine(event.Output), max(24, m.width-44))
  → 所以 Detail 只在工具完成后才有内容，且内容是工具输出的截断

StreamUsage 事件（turn 完成）
  → archiveToolActivityToAssistant() (app.go:2740)
  → toolActivity 移入 message.tools
  → toolActivityView() 返回空（无 running 工具）
```

### 当前渲染策略（核心问题所在）

go-claude 有**两个阶段**的 tools 渲染，但它们是**割裂的**：

**阶段 1：实时面板**（viewport 下方，独立区域）

```
[viewport: 对话内容]
─────────────────────────
Tools
├ Read    running 3s
└ Grep    running 1s
─────────────────────────
[底部状态栏: Go Claude is using Read... 3s 1.2k tokens]
```

- `toolActivityView()` (app.go:2614)：只显示 `Status == "running"` 的条目
- 最多显示 5 条（`toolActivityRenderLimit`），超出显示 `... +N tool uses`
- 全部用 `statusStyle`（颜色 "240"，暗灰色）渲染
- 每秒通过 `tickMsg` 刷新耗时计数
- running 状态时 Detail 通常为空，所以实时面板只显示工具名+耗时
- **位置在 viewport 下方**——不在对话流内，用户需要视线跳转

**阶段 2：归档面板**（assistant 消息底部追加）

```
Go Claude
分析结果：当前值 209（亮橙），建议改为 180（淡紫）。

Tools
├ Read    done 0.3s  app.go:3585
└ Grep    done 0.1s  inlineCodeColor
└ Bash    done 0.5s  tput colors
```

- `messageToolActivityView()` (app.go:3650)：结构与实时面板相同，但显示已完成条目
- Detail 在完成后才有内容（来自工具输出的截断）
- 同样最多 5 条，同样暗灰色
- **位置在消息底部追加**——不在对话流内，是事后追加的附属信息

### 当前渲染策略的三大缺陷

| 缺陷 | 说明 | Claude Code 的做法 |
|------|------|-------------------|
| **割裂** | running 在下方面板，完成后跳到消息底部追加，视觉不连续。两个阶段在不同位置，用户需要视线跳转 | 工具调用内嵌在对话流中，状态在同一位置就地转换 |
| **空洞** | running 时只有工具名+耗时，没有输入摘要（因为 `StreamToolStart` 不带 Input）。完成后 Detail 是输出截断，丢失了输入信息 | running 时就有语义化摘要（Read app.go:3585），完成后保留输入摘要+追加输出摘要 |
| **扁平** | 所有工具同一颜色，无折叠展开，无并行分组，无状态图标 | 颜色区分、折叠展开、并行分组、状态图标（✓/✗/⋯） |

**割裂是最核心的问题**。它导致：

1. 用户在 running 时看不到工具输入（在下方面板，且 Detail 为空）
2. 工具完成后，信息从下方面板消失，跳到消息底部追加——视觉断裂
3. 用户无法在同一位置追踪一个工具从 running → done 的完整生命周期

## Claude Code 的渲染策略（参考）

Claude Code 的工具调用**内嵌在对话流中**，不是单独面板：

```
Go Claude
让我分析 inline code 颜色问题...

  Read app.go:3585 ⋯          ← running（省略号/spinner，颜色鲜明）
  Grep inlineCodeColor in internal/tui ⋯

  Read app.go:3585 ✓ → 120 lines  ← done（就地转换，颜色变淡，视觉降级）
  Grep inlineCodeColor in internal/tui ✓ → 3 matches

分析结果：当前值 209（亮橙），建议改为 180（淡紫）。
```

关键特征：

1. **内嵌在对话流**：工具调用是 assistant 消息的一部分，不是单独面板
2. **就地状态转换**：running → done 在同一位置转换，不需要视线跳转
3. **语义化摘要**：running 时就有输入摘要，完成后追加输出摘要
4. **颜色区分**：Read 蓝色、Grep 紫色、Bash 黄色、Edit 绿色
5. **状态图标**：⋯（running）→ ✓（done）→ ✗（error）
6. **折叠展开**：默认折叠为一行摘要，按键展开查看完整输入/输出
7. **并行分组**：同一轮并行调用在视觉上分组显示

### Claude Code 的渲染细节（v3 文档之前遗漏的）

#### 已完成工具的视觉降级

Claude Code 的已完成工具不是和 running 工具同等亮度显示的。完成后有**视觉降级**：

- **running**：工具名颜色鲜明，状态图标用动画 spinner（⋯ 循环闪烁），整行亮度高，占据视觉焦点
- **done**：工具名颜色变淡/暗，状态图标 ✓，整行用暗色渲染，视觉上"退到背景"
- **error**：✗ 用红色，整行保持可见但不突出

设计原则：**正在进行的任务占据视觉焦点，已完成的任务退到背景**。用户一眼就能区分"还在跑"和"已经跑完了"。

#### 默认折叠已完成工具，展开正在进行的工具

Claude Code 的折叠逻辑不是"全部折叠"或"全部展开"，而是**按状态区分**：

- **running 的工具**：默认展开（显示摘要行，颜色鲜明）
- **done 的工具**：默认折叠为更暗的一行，或完全隐藏（只显示数量 "3 tools completed ✓"）

这样页面上主要显示的是正在进行的任务，已完成的退到背景。用户按键可以展开查看已完成工具的详情。

#### 展开后的详情格式

按键展开后，显示完整输入和输出：

```
  Read app.go:3585 ✓ → 120 lines
  ┌ Input:
  │ {"file_path": "/path/to/app.go", "offset": 3580, "limit": 20}
  └ Output:
  │ 3580: func simpleSystemSection() string {
  │ 3581:   return strings.TrimSpace(`
  │ ...
  └ 120 lines total
```

折叠时只占一行摘要，展开时才显示完整内容。

#### 底部状态栏的实时任务描述

Claude Code 的底部状态栏不只是 "Go Claude is using Read..."，而是更具体的：

- running 时：`Read app.go:3585 ⋯`（直接显示工具名+输入摘要）
- 多个工具并行时：`3 tools running ⋯`
- thinking 时：`Thinking ⋯`
- writing 时：`Writing ⋯`

go-claude 当前只显示 "Go Claude is using Read..."，没有输入摘要。

## 增强方案（v3 — 渲染策略重构）

v2 的改动 1-4 是在当前渲染策略（下方面板+消息底部追加）上做增量优化。v3 的核心改动是**重构渲染策略**——把工具调用从"割裂的两阶段面板"改为"内嵌在对话流中的就地状态转换"。

### 前置改动：事件流补全 Input（必须先做）

与 v2 相同，这是所有后续改动的基础。

**改动点 1：扩展 `ToolCallEvent` 结构**

```go
// internal/query/query.go:419 — 当前
type ToolCallEvent struct {
    ID   string
    Name string
}

// 修改后
type ToolCallEvent struct {
    ID    string
    Name  string
    Input json.RawMessage  // 新增：工具输入参数（JSON 格式）
}
```

**改动点 2：`onToolCall` 回调传递 Input**

```go
// internal/query/query.go:389-393 — 当前
onToolCall: func(block anthropic.ContentBlock) error {
    if cb.OnToolCall == nil {
        return nil
    }
    return cb.OnToolCall(ToolCallEvent{ID: block.ID, Name: block.Name})
}

// 修改后
onToolCall: func(block anthropic.ContentBlock) error {
    if cb.OnToolCall == nil {
        return nil
    }
    return cb.OnToolCall(ToolCallEvent{ID: block.ID, Name: block.Name, Input: block.Input})
}
```

同样修改 `internal/query/query.go:643` 处的另一个 `onToolCall` 回调。

**改动点 3：`StreamToolStart` 事件携带 Input**

```go
// internal/cli/cli.go:1015-1017 — 当前
OnToolCall: func(event query.ToolCallEvent) error {
    events <- tui.StreamEvent{Type: tui.StreamToolStart, ToolID: event.ID, ToolName: event.Name}
    return nil
}

// 修改后
OnToolCall: func(event query.ToolCallEvent) error {
    events <- tui.StreamEvent{Type: tui.StreamToolStart, ToolID: event.ID, ToolName: event.Name, Output: string(event.Input)}
    return nil
}
```

用 `event.Output` 字段传递 Input（复用现有字段，避免扩展 `StreamEvent` 结构）。

**改动点 4：`StreamToolResult` 事件也携带 Input**

```go
// internal/cli/cli.go:1019-1020 — 当前
OnToolResult: func(trace query.ToolTrace) error {
    events <- tui.StreamEvent{Type: tui.StreamToolResult, ToolID: trace.ID, ToolName: trace.Name, Output: trace.Output, IsError: trace.IsError}
    return nil
}

// 修改后
OnToolResult: func(trace query.ToolTrace) error {
    events <- tui.StreamEvent{Type: tui.StreamToolResult, ToolID: trace.ID, ToolName: trace.Name, Output: trace.Output, IsError: trace.IsError, Input: trace.Input}
    return nil
}
```

需要扩展 `StreamEvent` 结构，新增 `Input string` 字段。

### 核心改动：渲染策略重构 — 工具调用内嵌在对话流

这是 v3 的核心改动，取代 v2 的"下方面板+消息底部追加"增量优化。

**当前渲染流程**：

```
View() 组装：
  1. viewport.View()          ← 对话内容（liveTranscriptView）
  2. toolActivityView()       ← 下方面板（running 工具）
  3. usageView()              ← 使用统计
  4. inputBoxView()           ← 底部输入栏

renderMessage() 组装 assistant 消息：
  1. "Go Claude" 标题
  2. markdown 内容
  3. meta（使用统计）
  4. messageToolActivityView() ← 消息底部追加（已完成工具）
  5. messageAgentProgressView() ← 消息底部追加（子 agent）
```

**重构后渲染流程**：

```
View() 组装：
  1. viewport.View()          ← 对话内容（liveTranscriptView）
     └─ 工具调用内嵌在 assistant 消息中，不再有下方面板
  2. usageView()              ← 使用统计
  3. inputBoxView()           ← 底部输入栏

renderMessage() 组装 assistant 消息：
  1. "Go Claude" 标题
  2. markdown 内容
  3. toolCallInlineView()     ← 内嵌工具调用（取代 messageToolActivityView）
     └─ 每个工具调用一行：工具名(颜色) + 输入摘要 + 状态图标 + 输出摘要
     └─ running 时显示 ⋯，done 时就地转为 ✓，error 时转为 ✗
  4. meta（使用统计）
  5. messageAgentProgressView() ← 子 agent（保持不变）
```

**关键变化**：

1. **删除 `toolActivityView()`**：不再有 viewport 下方的独立工具面板
2. **删除 `messageToolActivityView()`**：不再有消息底部追加的工具列表
3. **新增 `toolCallInlineView()`**：工具调用内嵌在 assistant 消息中，一行一个工具
4. **工具状态就地转换**：running → done 在同一位置转换，不需要视线跳转

### 改动 1：工具输入摘要语义化

前置改动完成后，`startToolActivity()` 收到的 `event.Output` 就是工具输入参数（JSON 格式）。

```go
// 新增函数：toolInputSummary(toolName, input string) string
func toolInputSummary(toolName, rawInput string) string {
    switch toolName {
    case "Read":
        // 输入: {"file_path": "/path/to/file", "offset": 100, "limit": 50}
        // 输出: "file.go:100-150" 或 "file.go"（无 offset 时）
    case "Grep":
        // 输入: {"pattern": "inlineCodeColor", "path": "internal/tui"}
        // 输出: "inlineCodeColor in internal/tui"
    case "Bash":
        // 输入: {"command": "go test ./internal/query/..."}
        // 输出: "go test ./internal/query/..."
    case "Edit":
        // 输入: {"file_path": "app.go", "old_string": "...", "new_string": "..."}
        // 输出: "app.go: old → new"（截断 old/new 到 20 字符）
    case "Write":
        // 输入: {"file_path": "docs/xxx.md", "content": "..."}
        // 输出: "docs/xxx.md"
    case "Glob":
        // 输入: {"pattern": "**/*.go"}
        // 输出: "**/*.go"
    default:
        return truncate(oneLine(rawInput), 60)
    }
}
```

**改动点**：`startToolActivity()` 中把 `Detail` 的赋值从 `truncate(oneLine(event.Output))` 改为 `toolInputSummary(event.ToolName, event.Output)`。

### 改动 2：工具类型颜色区分 + 状态图标 + 视觉降级

```go
// 工具类型颜色映射（running 时用鲜明色，done 时用暗色）
var toolColorMap = map[string]string{
    "Read":      "69",   // 蓝色 — 读取
    "Grep":      "183",  // 紫色 — 搜索
    "Glob":      "183",  // 紫色 — 搜索
    "Bash":      "220",  // 黄色 — 执行
    "Edit":      "114",  // 绿色 — 修改
    "Write":     "114",  // 绿色 — 写入
    "MultiEdit": "114",  // 绿色 — 修改
}

// 视觉降级：done/error 时工具名颜色变暗
func toolNameStyle(toolName string, status string) lipgloss.Style {
    color, ok := toolColorMap[toolName]
    if !ok {
        color = "240" // 默认暗灰色
    }
    style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
    if status == "done" || status == "error" {
        // 已完成/出错时颜色变暗：用 "240"（暗灰色）替代鲜明色
        // 只保留工具名的颜色区分，但亮度大幅降低
        style = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
    }
    return style
}

// 状态图标
func toolStatusIcon(item toolActivityItem) string {
    switch item.Status {
    case "running":
        return "⋯"  // 省略号表示进行中（配合 tickMsg 动画闪烁）
    case "done":
        return "✓"  // 勾号表示完成
    case "error":
        return "✗"  // 叉号表示错误（红色渲染）
    default:
        return ""
    }
}

// 状态图标颜色
func toolStatusIconStyle(item toolActivityItem) lipgloss.Style {
    switch item.Status {
    case "running":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // 中灰色，配合闪烁
    case "done":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // 暗灰色，视觉降级
    case "error":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // 红色，突出错误
    default:
        return lipgloss.NewStyle()
    }
}
```

**视觉降级原则**：正在进行的任务占据视觉焦点（鲜明颜色+闪烁图标），已完成的任务退到背景（暗灰色+静态图标）。

### 改动 3：工具结果摘要

```go
// 扩展 toolActivityItem 结构
type toolActivityItem struct {
    ToolName  string
    ToolID    string
    Status    string
    Detail    string        // 工具输入摘要（语义化，完成后不覆盖）
    Result    string        // 工具输出摘要（新增）
    Started   time.Time
    Elapsed   time.Duration
    IsError   bool
}

// 新增函数：toolResultSummary(toolName, output string) string
func toolResultSummary(toolName, rawOutput string) string {
    switch toolName {
    case "Grep":
        // 提取: "3 matches" 或 "no matches"
    case "Read":
        // 提取: "120 lines" 或行数范围
    case "Bash":
        // 提取: "exit 0" 或 "exit 1: error message"
    case "Edit":
        // 提取: "ok" 或 "error: old_string not found"
    case "Glob":
        // 提取: "5 files" 或 "no files"
    default:
        return ""
    }
}
```

**改动点**：`finishToolActivity()` 中新增 `Result` 字段赋值，且不覆盖已有的 `Detail`。

### 改动 4：内嵌渲染函数 `toolCallInlineView()`

这是 v3 的核心新增函数，取代 `toolActivityView()` 和 `messageToolActivityView()`。

**关键设计原则**：
- **按状态区分折叠**：running 工具默认展开（颜色鲜明），done 工具默认折叠为暗色一行或只显示数量
- **视觉降级**：done 工具整行用暗灰色渲染，running 工具用鲜明颜色
- **展开详情格式**：按键展开后显示完整输入和输出

```go
// 新增函数：toolCallInlineView(items []toolActivityItem) string
// 渲染工具调用内嵌在 assistant 消息中
func (m Model) toolCallInlineView(items []toolActivityItem) string {
    if len(items) == 0 {
        return ""
    }

    // 按状态分组：running 展开，done 折叠
    runningItems := []toolActivityItem{}
    doneItems := []toolActivityItem{}
    for _, item := range items {
        if item.Status == "running" {
            runningItems = append(runningItems, item)
        } else {
            doneItems = append(doneItems, item)
        }
    }

    var b strings.Builder
    width := max(24, m.width-8)

    // 1. 渲染 running 工具（全部展开，颜色鲜明）
    for i, item := range runningItems {
        name := toolNameStyle(item.ToolName, item.Status).Render(firstNonEmpty(item.ToolName, "tool"))
        icon := toolStatusIconStyle(item).Render(toolStatusIcon(item))
        detail := item.Detail
        line := fmt.Sprintf("  %s %s %s", name, detail, icon)
        b.WriteString(truncate(line, width))
        if i < len(runningItems)-1 || len(doneItems) > 0 {
            b.WriteString("\n")
        }
    }

    // 2. 渲染 done 工具（默认折叠，视觉降级）
    if m.toolPanelExpanded {
        // 展开模式：逐行显示所有 done 工具（暗灰色）
        for i, item := range doneItems {
            name := toolNameStyle(item.ToolName, item.Status).Render(firstNonEmpty(item.ToolName, "tool"))
            icon := toolStatusIconStyle(item).Render(toolStatusIcon(item))
            detail := item.Detail
            result := ""
            if item.Result != "" {
                result = " → " + item.Result
            }
            elapsed := ""
            if item.Elapsed > 0 {
                elapsed = " " + runningDurationLabel(item.Elapsed)
            }
            line := fmt.Sprintf("  %s %s %s%s%s", name, detail, icon, result, elapsed)
            b.WriteString(statusStyle.Render(truncate(line, width)))
            if i < len(doneItems)-1 {
                b.WriteString("\n")
            }
        }
        // 展开时显示 collapse 提示
        if len(doneItems) > 3 {
            b.WriteString("\n")
            b.WriteString(statusStyle.Render("  ctrl+t collapse"))
        }
    } else {
        // 折叠模式：只显示数量摘要（暗灰色）
        if len(doneItems) > 0 {
            b.WriteString(statusStyle.Render(fmt.Sprintf("  %d tools completed ✓  ctrl+t expand", len(doneItems))))
        }
    }

    return b.String()
}
```

渲染效果：

```
running 时（内嵌在对话流中，颜色鲜明）：
Go Claude
  Read app.go:3585 ⋯              ← 蓝色工具名，中灰图标，鲜明
  Grep inlineCodeColor in internal/tui ⋯  ← 紫色工具名，中灰图标，鲜明

done 时（就地转换，视觉降级）：
Go Claude
  Read app.go:3585 ⋯              ← 蓝色工具名，中灰图标，鲜明（还在跑）
  2 tools completed ✓  ctrl+t expand  ← 暗灰色，退到背景

全部完成时（默认折叠已完成工具）：
Go Claude
  3 tools completed ✓  ctrl+t expand  ← 暗灰色，退到背景

分析结果：当前值 209（亮橙），建议改为 180（淡紫）。

展开已完成工具时（ctrl+t）：
Go Claude
  Read app.go:3585 ✓ → 120 lines 0.3s   ← 暗灰色，视觉降级
  Grep inlineCodeColor ✓ → 3 matches 0.1s ← 暗灰色
  Bash tput colors ✓ → exit 0 0.5s       ← 暗灰色
  ctrl+t collapse

分析结果：当前值 209（亮橙），建议改为 180（淡紫）。
```

### 改动 5：重构 `renderMessage()` 和 `liveTranscriptView()`

**`renderMessage()` 修改**：

```go
// 当前（app.go:3049-3068）
case "assistant":
    var b strings.Builder
    b.WriteString(assistantStyle.Render("Go Claude"))
    if content != "" {
        b.WriteString("\n")
        b.WriteString(m.renderMarkdown(content))
    }
    if msg.meta != "" {
        b.WriteString("\n")
        b.WriteString(statusStyle.Render(wrapPrefixedLine("* ", msg.meta, m.contentWidth())))
    }
    if tools := m.messageToolActivityView(msg.tools); tools != "" {
        b.WriteString("\n")
        b.WriteString(tools)
    }
    if agents := m.messageAgentProgressView(msg.agents); agents != "" {
        b.WriteString("\n")
        b.WriteString(agents)
    }
    return b.String()

// 重构后
case "assistant":
    var b strings.Builder
    b.WriteString(assistantStyle.Render("Go Claude"))
    // 工具调用内嵌在文本之前（先展示过程，再展示结论）
    if tools := m.toolCallInlineView(msg.tools); tools != "" {
        b.WriteString("\n")
        b.WriteString(tools)
    }
    if content != "" {
        b.WriteString("\n")
        b.WriteString(m.renderMarkdown(content))
    }
    if msg.meta != "" {
        b.WriteString("\n")
        b.WriteString(statusStyle.Render(wrapPrefixedLine("* ", msg.meta, m.contentWidth())))
    }
    if agents := m.messageAgentProgressView(msg.agents); agents != "" {
        b.WriteString("\n")
        b.WriteString(agents)
    }
    return b.String()
```

**`liveTranscriptView()` 修改**：

```go
// 当前（app.go:1047-1066）
func (m Model) liveTranscriptView() string {
    if m.pendingPermission != nil {
        return permissionPromptView(*m.pendingPermission)
    }
    if msg, ok := m.lastLiveMessage(); ok {
        live := m.renderMessage(msg)
        if agents := m.agentProgressView(); agents != "" && len(msg.agents) == 0 {
            live = strings.TrimRight(live, "\n") + "\n" + agents
        }
        return live
    }
    if m.busy {
        live := statusStyle.Render(m.currentRunningStatus())
        if agents := m.agentProgressView(); agents != "" {
            live += "\n" + agents
        }
        return live
    }
    return ""
}

// 重构后：running 工具也内嵌在 live message 中
func (m Model) liveTranscriptView() string {
    if m.pendingPermission != nil {
        return permissionPromptView(*m.pendingPermission)
    }
    if msg, ok := m.lastLiveMessage(); ok {
        live := m.renderMessage(msg)
        // running 工具内嵌在 live message 中（不再用下方面板）
        if running := m.toolCallInlineView(m.toolActivity); running != "" {
            live = strings.TrimRight(live, "\n") + "\n" + running
        }
        if agents := m.agentProgressView(); agents != "" && len(msg.agents) == 0 {
            live = strings.TrimRight(live, "\n") + "\n" + agents
        }
        return live
    }
    if m.busy {
        // 无 live message 时，running 工具直接显示
        var b strings.Builder
        b.WriteString(statusStyle.Render(m.currentRunningStatus()))
        if running := m.toolCallInlineView(m.toolActivity); running != "" {
            b.WriteString("\n")
            b.WriteString(running)
        }
        if agents := m.agentProgressView(); agents != "" {
            b.WriteString("\n")
            b.WriteString(agents)
        }
        return b.String()
    }
    return ""
}
```

**`View()` 修改**：

```go
// 当前（app.go:938-946）
parts := make([]string, 0, 6)
hasLive := false
if live := m.liveTranscriptView(); live != "" {
    parts = append(parts, m.viewport.View())
    hasLive = true
}
if tools := m.toolActivityView(); tools != "" {
    parts = append(parts, tools)  // ← 下方面板
}

// 重构后：删除下方面板
parts := make([]string, 0, 6)
hasLive := false
if live := m.liveTranscriptView(); live != "" {
    parts = append(parts, m.viewport.View())
    hasLive = true
}
// 不再有 toolActivityView() 下方面板
```

### 改动 6：折叠/展开交互

```go
// 新增字段
toolPanelExpanded bool

// 新增按键绑定（在 Update 中）
case key.Matches(msg, m.keyMap.ExpandTools):
    m.toolPanelExpanded = !m.toolPanelExpanded
```

按键 `ctrl+t`（Tools），与已有的 `ctrl+e`（Expand agents）形成系列。

### 改动 7：底部状态栏增强

当前底部状态栏只显示 "Go Claude is using Read..."，没有输入摘要。增强后显示更具体的任务描述：

```go
// 当前（app.go:2253）
m.runningStatus = "Go Claude is using " + firstNonEmpty(event.ToolName, event.ToolID, "a tool") + "..."

// 增强后：显示工具名+输入摘要
m.runningStatus = "Go Claude is using " + firstNonEmpty(event.ToolName, event.ToolID, "a tool") + "..."
if detail := toolInputSummary(event.ToolName, event.Output); detail != "" {
    m.runningStatus = detail  // 直接显示语义化摘要，如 "Read app.go:3585"
}

// 多个工具并行时
running := m.runningToolActivity()
if len(running) > 1 {
    m.runningStatus = fmt.Sprintf("%d tools running", len(running))
}
```

渲染效果对比：

```
当前：
Go Claude is using Read...  3s  1.2k tokens

增强后：
Read app.go:3585  3s  1.2k tokens          ← 单工具，显示摘要
3 tools running  3s  1.2k tokens            ← 多工具并行，显示数量
```

按键 `ctrl+t`（Tools），与已有的 `ctrl+e`（Expand agents）形成系列。

### 改动 8：并行工具分组（可选，优先级低）

当多个工具同时 running 时，在视觉上分组：

```
  Read app.go:3585 ⋯
  Grep inlineCodeColor in internal/tui ⋯
  Bash tput colors ⋯
```

三个工具在同一组，视觉上表示"这是同一轮并行调用"。可以通过在 `toolCallInlineView()` 中检测同一时间窗口内启动的工具来分组。

## 任务进度展示方案（新增 — 独立功能）

### 背景

go-claude 已有 `TodoWrite`/`TodoRead` 工具（`internal/tools/todowrite/todowrite.go`），LLM 可以创建任务列表并写入 `.claude/todos.json`。但**完全没有 TUI 渲染层**——没有事件类型、没有渲染视图、没有进度展示。LLM 写了 todo，用户在 TUI 上看不到任何任务进度。

Claude Code 的做法：LLM 在执行多步骤任务时，通过 `TaskCreate`/`TaskUpdate` 工具创建任务列表，TUI 在底部渲染实时进度面板，用户可以看到任务拆分、当前进度、已完成/进行中/待做的状态。

### 当前 go-claude 的 TodoWrite 工具

```go
// internal/tools/todowrite/todowrite.go
type Todo struct {
    ID       string `json:"id,omitempty"`     // 如 "todo-1"
    Content  string `json:"content"`          // 如 "修复 inline code 颜色"
    Status   string `json:"status"`           // "pending" / "in_progress" / "completed"
    Priority string `json:"priority,omitempty"` // "low" / "medium" / "high"
}
```

数据持久化在 `.claude/todos.json`，但 TUI 不读取也不渲染这个文件。

### Claude Code 的任务进度展示细节

Claude Code 的任务进度面板在**底部输入栏上方**，是一个独立的渲染区域：

```
[viewport: 对话内容]
─────────────────────────
Tasks
☐ 修复 inline code 颜色          ← pending（暗灰色）
☐ 添加提示词优先级规则            ← pending（暗灰色）
► 运行测试验证                    ← in_progress（鲜明色+spinner）
✓ 分析根因                       ← completed（暗灰色+✓）
─────────────────────────
[底部输入栏: Go Claude  3s  1.2k tokens]
```

关键渲染细节：

1. **位置**：底部输入栏上方，viewport 下方。独立区域，不内嵌在对话流中（和工具调用不同——工具调用是"过程"，任务是"计划"，两者渲染位置不同）
2. **状态图标**：☐（pending）→ ►（in_progress，配合 spinner 动画）→ ✓（completed）
3. **视觉降级**：pending 和 completed 用暗灰色，in_progress 用鲜明颜色（如绿色 "114"）
4. **默认折叠**：只显示 in_progress 的任务（1 行），其他任务折叠为数量摘要。按键展开查看全部
5. **进度百分比**：面板标题显示 "Tasks 2/4"（已完成 2 个，总共 4 个）
6. **就地状态转换**：pending → in_progress → completed 在同一位置转换，不需要视线跳转
7. **自动消失**：所有任务 completed 后，面板自动消失（或折叠为 "All tasks completed ✓"）
8. **与工具调用面板的关系**：任务面板和工具调用面板是两个独立区域。任务面板在上方（计划层），工具调用在对话流内（执行层）

### 实现方案

#### 改动 A1：Todo 事件触发机制

不需要新增 `StreamTodoUpdate` 事件类型。直接在 `applyStreamEvent()` 的 `StreamToolResult` 处理中拦截 TodoWrite/TodoRead 的结果，调用 `updateTodoList()` 即可。

**关键细节**：TodoWrite 和 TodoRead 的 Output 格式不同：
- `TodoWrite` 的 Output 是 `"Saved 3 todos to .claude/todos.json"`（纯文本，不是 JSON）
- `TodoRead` 的 Output 是 `[{"id":"todo-1","content":"...","status":"in_progress","priority":"high"}]`（JSON 数组）

因此 `updateTodoList()` 需要先尝试 JSON 解析 Output，失败时从 `.claude/todos.json` 文件读取。

```go
// internal/tui/app.go — applyStreamEvent() 中 StreamToolResult 处理修改
// 当前（app.go:2482）：
case StreamToolResult:
    m.runningStatus = "Go Claude is processing tool results..."
    m.finishToolActivity(event)

// 修改后：
case StreamToolResult:
    m.runningStatus = "Go Claude is processing tool results..."
    m.finishToolActivity(event)
    // 新增：TodoWrite/TodoRead 结果触发 todo 更新
    if event.ToolName == "TodoWrite" || event.ToolName == "TodoRead" {
        m.updateTodoList(event.Output)
    }
```

#### 改动 A2：新增 Todo 数据结构和状态管理

```go
// internal/tui/app.go — 新增数据结构
type todoItem struct {
    ID       string
    Content  string
    Status   string    // "pending" / "in_progress" / "completed"
    Priority string    // "low" / "medium" / "high"
}

// Model 新增字段
todos          []todoItem    // 当前任务列表
todoPanelExpanded bool        // 折叠/展开状态
```

```go
// 新增函数：updateTodoList(output string)
func (m *Model) updateTodoList(output string) {
    var items []todoItem
    if err := json.Unmarshal([]byte(output), &items); err != nil {
        // TodoWrite 的 Output 是 "Saved 3 todos to .claude/todos.json"（纯文本）
        // 需要从 .claude/todos.json 文件重新读取
        path := filepath.Join(m.welcome.CWD, ".claude", "todos.json")
        data, err := os.ReadFile(path)
        if err != nil {
            return
        }
        if err := json.Unmarshal(data, &items); err != nil {
            return
        }
    }
    m.todos = items
    m.refreshViewport()
}
```

#### 改动 A3：新增 Todo 进度面板渲染

```go
// 新增函数：todoProgressView() string
func (m Model) todoProgressView() string {
    if len(m.todos) == 0 {
        return ""
    }

    // 计算进度
    completed := 0
    inProgress := 0
    pending := 0
    for _, t := range m.todos {
        switch t.Status {
        case "completed":
            completed++
        case "in_progress":
            inProgress++
        default:
            pending++
        }
    }

    // 全部完成时：折叠为一行
    if completed == len(m.todos) && !m.todoPanelExpanded {
        return statusStyle.Render(fmt.Sprintf("All tasks completed ✓ (%d/%d)  ctrl+y expand", completed, len(m.todos)))
    }

    var b strings.Builder
    width := max(24, m.width-8)

    // 标题：Tasks + 进度百分比
    title := fmt.Sprintf("Tasks %d/%d", completed, len(m.todos))
    b.WriteString(statusStyle.Render(title))

    if m.todoPanelExpanded {
        // 展开模式：逐行显示所有任务
        for i, t := range m.todos {
            b.WriteString("\n")
            icon := todoStatusIcon(t.Status)
            iconStyle := todoStatusIconStyle(t.Status)
            contentStyle := todoContentStyle(t.Status)
            line := fmt.Sprintf("  %s %s", iconStyle.Render(icon), contentStyle.Render(truncate(t.Content, width-6)))
            b.WriteString(line)
        }
        b.WriteString("\n")
        b.WriteString(statusStyle.Render("  ctrl+y collapse"))
    } else {
        // 折叠模式：只显示 in_progress 的任务 + 数量摘要
        for _, t := range m.todos {
            if t.Status == "in_progress" {
                b.WriteString("\n")
                icon := todoStatusIcon(t.Status)
                iconStyle := todoStatusIconStyle(t.Status)
                contentStyle := todoContentStyle(t.Status)
                line := fmt.Sprintf("  %s %s", iconStyle.Render(icon), contentStyle.Render(truncate(t.Content, width-6)))
                b.WriteString(line)
            }
        }
        // 数量摘要
        others := pending + completed
        if others > 0 {
            b.WriteString("\n")
            b.WriteString(statusStyle.Render(fmt.Sprintf("  %d pending, %d completed  ctrl+y expand", pending, completed)))
        }
    }

    return b.String()
}

// 任务状态图标
func todoStatusIcon(status string) string {
    switch status {
    case "pending":
        return "☐"  // 空方框
    case "in_progress":
        return "►"  // 箭头（配合 spinner 动画）
    case "completed":
        return "✓"  // 勾号
    default:
        return "☐"
    }
}

// 任务状态图标颜色
func todoStatusIconStyle(status string) lipgloss.Style {
    switch status {
    case "pending":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))  // 暗灰
    case "in_progress":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("114"))  // 绿色，鲜明
    case "completed":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))  // 暗灰，视觉降级
    default:
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
    }
}

// 任务内容颜色
func todoContentStyle(status string) lipgloss.Style {
    switch status {
    case "pending":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))  // 暗灰
    case "in_progress":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("252"))  // 白色，鲜明
    case "completed":
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))  // 暗灰，视觉降级
    default:
        return lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
    }
}
```

渲染效果：

```
折叠模式（默认）—— 只显示 in_progress 任务：
─────────────────────────
Tasks 2/4
  ► 运行测试验证                    ← 绿色图标+白色内容，鲜明
  1 pending, 1 completed  ctrl+y expand  ← 暗灰摘要
─────────────────────────

展开模式（ctrl+y）—— 显示全部任务：
─────────────────────────
Tasks 2/4
  ☐ 修复 inline code 颜色          ← 暗灰，视觉降级
  ► 运行测试验证                    ← 绿色+白色，鲜明
  ✓ 分析根因                       ← 暗灰+✓，视觉降级
  ☐ 添加提示词优先级规则            ← 暗灰
  ctrl+y collapse
─────────────────────────

全部完成时：
─────────────────────────
All tasks completed ✓ (4/4)  ctrl+y expand  ← 暗灰，退到背景
─────────────────────────
```

#### 改动 A4：View() 中插入 Todo 进度面板

```go
// internal/tui/app.go — View() 修改
// 当前：
parts := make([]string, 0, 6)
hasLive := false
if live := m.liveTranscriptView(); live != "" {
    parts = append(parts, m.viewport.View())
    hasLive = true
}
// 不再有 toolActivityView() 下方面板

// 修改后：在 viewport 和 usageView 之间插入 todo 进度面板
parts := make([]string, 0, 6)
hasLive := false
if live := m.liveTranscriptView(); live != "" {
    parts = append(parts, m.viewport.View())
    hasLive = true
}
if todos := m.todoProgressView(); todos != "" {
    parts = append(parts, todos)  // ← 新增：任务进度面板
}
if usage := m.usageView(); usage != "" {
    ...
}
```

#### 改动 A5：ctrl+y 折叠/展开交互

```go
// Model 新增字段（已在 A2 中定义）
todoPanelExpanded bool

// Update() 中新增按键绑定（与 ctrl+e/ctrl+t 同模式，用 msg.String() 匹配）
case "ctrl+y":
    if len(m.todos) > 0 {
        m.todoPanelExpanded = !m.todoPanelExpanded
        m.refreshViewport()
        return m, nil
    }
```

按键 `ctrl+y`（Y = Yes/进度），与已有的 `ctrl+e`（Expand agents）和 `ctrl+t`（Expand tools）形成系列。

同时需要在 `modeHintView()` 中添加 ctrl+y 提示：
```go
if len(m.todos) > 0 {
    parts = append(parts, "ctrl+y tasks")
}
```

#### 改动 A6：底部状态栏显示当前任务

```go
// applyStreamEvent() 中 StreamToolStart 修改
// 当前：
m.runningStatus = "Go Claude is using " + firstNonEmpty(event.ToolName, event.ToolID, "a tool") + "..."

// 增强后：如果有 in_progress 任务，显示当前任务内容
if inProgress := m.currentTodo(); inProgress != nil {
    m.runningStatus = truncate(inProgress.Content, max(24, m.width-24))
} else {
    m.runningStatus = "Go Claude is using " + firstNonEmpty(event.ToolName, event.ToolID, "a tool") + "..."
}

// 辅助函数
func (m Model) currentTodo() *todoItem {
    for i := range m.todos {
        if m.todos[i].Status == "in_progress" {
            return &m.todos[i]
        }
    }
    return nil
}
```

渲染效果：

```
当前：
Go Claude is using Read...  3s  1.2k tokens

增强后（有任务时）：
运行测试验证  3s  1.2k tokens              ← 显示当前任务内容
Read app.go:3585  3s  1.2k tokens          ← 无任务时显示工具摘要
```

#### 改动 A7：提示词层引导 LLM 使用任务列表

```go
// internal/query/query.go — doingTasksSection() 中修改
// 当前（query.go:3761）已有：
"- For sequential work (read → edit → verify), use TodoWrite to track progress."

// 修改为更详细的引导：
"- For multi-step tasks, use TodoWrite to create a task list before starting. \
  Break the work into small, verifiable steps. Keep at most one item in_progress \
  at a time. Mark each step completed as you finish it. \
  This helps the user see your progress in the UI."
```

这条提示词规则引导 LLM 在执行多步骤任务时主动创建任务列表，让 TUI 能渲染进度。
注意：当前已有简短的 TodoWrite 提示词（query.go:3761），需要扩展为更详细的版本。

#### 改动 A8：会话恢复时加载已有 todo

```go
// internal/tui/app.go — applySessionResume() 中新增（app.go:2497）
// 恢复会话时，从 .claude/todos.json 加载已有任务列表
func (m *Model) applySessionResume(event StreamEvent) {
    // ... 现有逻辑 ...
    // 新增：加载 todo
    path := filepath.Join(m.welcome.CWD, ".claude", "todos.json")
    data, err := os.ReadFile(path)
    if err == nil {
        var items []todoItem
        if json.Unmarshal(data, &items) == nil {
            m.todos = items
        }
    }
}
```

注意：Model 没有 `cwd` 字段，CWD 存储在 `m.welcome.CWD` 中。

### 任务进度面板与工具调用面板的关系

两者是独立的渲染区域，位置不同，功能不同：

```
[viewport: 对话内容]
  Go Claude
    Read app.go:3585 ⋯              ← 工具调用（内嵌在对话流，执行层）
    Grep inlineCodeColor ⋯

    Read app.go:3585 ✓ → 120 lines  ← 工具调用（就地转换）
    2 tools completed ✓  ctrl+t expand

    分析结果：当前值 209（亮橙）...
─────────────────────────
Tasks 2/4                          ← 任务进度（底部面板，计划层）
  ► 运行测试验证
  1 pending, 1 completed  ctrl+y expand
─────────────────────────
[底部输入栏: 运行测试验证  3s  1.2k tokens]
```

- **工具调用**：内嵌在对话流中，展示"正在执行什么操作"（微观层）
- **任务进度**：底部独立面板，展示"整体计划进展到哪一步"（宏观层）
- **底部状态栏**：显示当前任务内容或当前工具摘要（最精简的一行）

## 实现优先级总表（v3 — 含任务进度展示）

### 工具调用渲染改动

| 优先级 | 改动 | 效果 | 工作量 | 备注 |
|--------|------|------|--------|------|
| **前置** | 事件流补全 Input | 让 TUI 能拿到工具输入参数 | 小（改 3 个文件约 10 行） | **必须先做** |
| **P0** | 核心改动：渲染策略重构 | 工具调用内嵌在对话流，就地状态转换 | 中 | **v3 核心** |
| **P0** | 改动 1：工具输入摘要语义化 | running 时就有输入摘要 | 小 | 依赖前置改动 |
| **P0** | 改动 2：颜色区分 + 状态图标 + 视觉降级 | running 鲜明，done 暗灰 | 小 | 无依赖 |
| **P1** | 改动 3：工具结果摘要 | done 时追加输出摘要 | 中 | 依赖前置改动 |
| **P1** | 改动 4：toolCallInlineView | 内嵌渲染函数 | 中 | 依赖核心改动 |
| **P2** | 改动 5-7：renderMessage + 折叠展开 + 状态栏 | 渲染顺序 + ctrl+t + 状态栏 | 小 | 依赖核心改动 |
| **P3** | 改动 8：并行分组 | 同一轮调用视觉分组 | 中 | 无依赖 |

### 任务进度展示改动



| 优先级 | 改动 | 效果 | 工作量 | 备注 |
|--------|------|------|--------|------|
| **P0** | A1: Todo 事件触发机制 | LLM 写 todo 时 TUI 收到更新 | 小（applyStreamEvent 中拦截 TodoWrite/TodoRead 结果） | 依赖前置改动（StreamEvent 已有 Input 字段） |
| **P0** | A2: Todo 数据结构 + 状态管理 | Model.todos + updateTodoList | 小（新增结构体 + 2 个字段 + 1 个函数） | 无依赖 |
| **P0** | A3: todoProgressView 渲染 | 底部任务进度面板 | 中（新增渲染函数 + 3 个辅助函数） | 依赖 A2 |
| **P1** | A4: View() 插入面板 | 任务面板出现在 UI 上 | 小（改 1 处 View 组装） | 依赖 A3 |
| **P1** | A5: ctrl+y 折叠/展开 | 交互控制 | 小（新增按键绑定） | 依赖 A2 |
| **P1** | A6: 底部状态栏显示当前任务 | 状态栏显示任务内容 | 小（改 runningStatus 赋值） | 依赖 A2 |
| **P2** | A7: 提示词引导 LLM 使用任务列表 | LLM 主动创建任务列表 | 小（doingTasksSection 加 1 行） | 无依赖 |
| **P2** | A8: 会话恢复加载 todo | 恢复会话时保留任务进度 | 小（applySessionResume 加 5 行） | 依赖 A2 |

## v1 → v2 → v3 修订要点

| 版本 | 核心问题 | 修订 |
|------|---------|------|
| v1 | 假设 StreamToolStart 携带 Input | v2 修正：新增前置改动补全 Input |
| v1 | 假设 startToolActivity 的 Detail 来自工具输入 | v2 修正：实际来自 event.Output，StreamToolStart 时为空 |
| v2 | finishToolActivity 覆盖 Detail | v2 修正：不覆盖，新增 Result 字段 |
| v2 | 在当前渲染策略（下方面板+消息底部追加）上做增量优化 | **v3 修正：增量优化无法解决割裂问题，需要重构渲染策略** |
| v2 | 改动 5（实时面板增强）标为 P3 | **v3 取消**：重构后不再有下方面板，running 工具内嵌在对话流中 |
| v2 | 所有工具同等亮度渲染 | **v3 修正：running 鲜明，done 暗灰（视觉降级）** |
| v2 | 折叠逻辑是"全部折叠/全部展开" | **v3 修正：按状态区分——running 展开，done 折叠为数量摘要** |
| v2 | 未描述展开后的详情格式 | **v3 补充：展开后显示完整输入和输出** |
| v2 | 底部状态栏只显示 "Go Claude is using Read..." | **v3 补充：显示工具名+输入摘要，多工具显示数量** |
| v1-v3 | 未包含任务进度展示 | **v3 补充：新增改动 A1-A8，任务进度面板（底部独立区域，折叠/展开+视觉降级+进度百分比+状态图标☐/►/✓）** |
| v3 A1 | 定义了 StreamTodoUpdate 事件类型 | **修正：不需要新事件类型，直接在 StreamToolResult 处理中拦截 TodoWrite/TodoRead 结果** |
| v3 A1 | 未说明 TodoWrite/TodoRead Output 格式差异 | **修正：TodoWrite Output 是纯文本（"Saved 3 todos..."），TodoRead Output 是 JSON 数组** |
| v3 A2 | 用 m.cwd 访问工作目录 | **修正：Model 没有 cwd 字段，CWD 在 m.welcome.CWD 中** |
| v3 A5 | 用 key.Matches 语法 | **修正：实际代码用 msg.String() 匹配，与 ctrl+e/ctrl+t 同模式** |
| v3 A6 | currentTodo() 返回循环变量副本地址 | **修正：用 &m.todos[i] 返回 slice 元素地址** |
| v3 A7 | 假设 doingTasksSection 中没有 TodoWrite 提示词 | **修正：已有简短提示词（query.go:3761），需扩展而非新增** |

## 与提示词层的关系

```
提示词层（已修复）：
  toneAndStyleSection() 新增：
  "Do not narrate your tool-use plan before executing tools.
   Call tools directly; present results after."
  → LLM 不口述计划，直接调工具

TUI 层（v3 方案）：
  前置：事件流补全 Input
  核心：渲染策略重构（内嵌对话流 + 就地状态转换）
  改动 1-7：语义化摘要 + 颜色区分 + 状态图标 + 结果摘要 + 折叠展开
  → 用户在对话流中看到实时过程，不需要 LLM 用文字描述
```

两层配合：LLM 安静地干活（不口述），TUI 在对话流中实时展示它在干什么。用户既能看到过程，又不浪费 token，也不干扰最终输出的阅读体验。

## 验证方法

1. 编译：`$HOME/go/go1.26.4/bin/go build -o /tmp/golang-cc-test ./cmd/golang-cc/`
2. 启动 `/tmp/golang-cc-test`
3. 测试场景：让 go-claude 分析一个代码问题（如"inline code 颜色过亮"）
4. 验证点：
   - LLM 不口述"让我先找到..."，直接调工具
   - 工具调用内嵌在对话流中，不在 viewport 下方的独立面板
   - running 时显示语义化摘要 + ⋯ 图标：`Read app.go:3585 ⋯`
   - done 时就地转为 ✓ + 输出摘要：`Read app.go:3585 ✓ → 120 lines`
   - 不同工具类型有颜色区分（Read 蓝色、Grep 紫色、Bash 黄色、Edit 绿色）
   - ctrl+t 可折叠/展开工具调用列表
5. 单元测试：`$HOME/go/go1.26.4/bin/go test ./internal/tui/... ./internal/query/... ./internal/cli/...`
6. 金标测试：`$HOME/go/go1.26.4/bin/go test ./internal/query/... -run Golden`

## 实现进度

### 已完成（commit 8e0e523）

前置改动 + 新增机制已全部实现，与旧机制并存：

| 改动 | 状态 |
|------|------|
| 前置 1: ToolCallEvent 加 Input json.RawMessage | ✓ |
| 前置 2: onToolCall 回调传递 block.Input | ✓ |
| 前置 3: StreamEvent 加 Input string | ✓ |
| 前置 4: cli.go OnToolCall 用 Output 传 Input | ✓ |
| 前置 5: cli.go OnToolResult 传 Input | ✓ |
| 前置 6: finishToolActivity 不覆盖 Detail + Result | ✓ |
| 新增: toolActivityItem Result 字段 | ✓ |
| 新增: toolInputSummary() 语义化输入摘要 | ✓ |
| 新增: toolResultSummary() 输出摘要 | ✓ |
| 新增: toolColorMap + toolNameStyle + toolStatusIcon | ✓ |
| 新增: toolCallInlineView() 内嵌渲染函数 | ✓ |
| 重构: renderMessage() 新增 toolCallInlineView | ✓ |
| 重构: liveTranscriptView() 新增 running 工具内嵌 | ✓ |
| 新增: ctrl+t 折叠/展开 + toolPanelExpanded | ✓ |
| 修改: startToolActivity 用 toolInputSummary | ✓ |

### TODO — 待观察：删除旧机制

当前新旧机制并存，工具调用信息会**重复显示**（内嵌 + 下方面板 + 消息底部追加）。
删除旧机制前需先验证新机制在真实场景下渲染正常。

**删除项（待确认）：**

| 删除项 | 位置 | 说明 |
|--------|------|------|
| `toolActivityView()` 函数 | app.go:2792 | 下方面板渲染函数 |
| `messageToolActivityView()` 函数 | app.go:3802 | 消息底部追加渲染函数 |
| `View()` 中 `toolActivityView` 调用 | app.go:944 | viewport 下方的独立工具面板 |
| `renderMessage()` 中 `messageToolActivityView` 调用 | app.go:3212 | 消息底部追加的工具列表 |

**删除后效果：**
- Running 时：工具只在对话流内嵌显示，下方面板消失
- Done 时：工具只在消息顶部内嵌显示，底部不再追加 "Tools" 区块
- 信息不再重复，视觉连续（running → done 就地转换）

**潜在风险：**
1. 无 fallback：如果 toolCallInlineView 有渲染 bug，用户看不到任何工具信息
2. 滚动问题：内嵌工具随消息滚动，长消息可能滚出视野（旧下方面板固定在 viewport 下方不随滚动消失）
3. 视觉习惯：旧机制的树形结构（├/└）有些用户可能习惯了

**验证步骤：**
1. 编译 `/tmp/golang-cc-test` 并实际使用
2. 确认内嵌渲染在 running/done/error 三种状态下正常
3. 确认 ctrl+t 折叠/展开正常
4. 确认无渲染 bug 后再删除旧机制

### 已完成 — Task/Agent 工具渲染去冗余

修复前 Task 工具完成后的渲染效果：
```
  Task 简单测试 subagent ✓ → completed 2s
```

问题：
- ✓ 已经表示完成，再追加 `→ completed` 是冗余信息
- subagent 任务和普通工具调用（Read/Grep/Bash）应该有不同的视觉风格
- Claude Code 原版对 subagent 的展示更简洁，不显示 `→ completed`

已完成：
- `toolResultSummary()` 对 Task/Agent 系列返回空字符串，不再显示 `→ completed`、`→ launched` 等冗余状态。
- `toolCallInlineView()` 继续保留工具名、输入摘要、状态图标和耗时，例如 `Task 简单测试 subagent ✓ 2s`。
- Task/Agent 系列继续使用独立颜色，和 Read/Grep/Bash/Edit 区分。

验证：新增 `TestModelTaskToolInlineViewOmitsRedundantCompletedSummary`。

### 已完成 — 任务进度展示 A1-A8

任务进度面板已按 A1-A8 落地，旧工具面板仍保持并存：

| 改动 | 状态 | 说明 |
|------|------|------|
| A1 Todo 事件触发机制 | ✓ | 在 `StreamToolResult` 中拦截 `TodoWrite` 输入和 `TodoRead` 输出。 |
| A2 Todo 数据结构和状态管理 | ✓ | `Model.todos`、`todoItem`、`setTodos()`、状态/优先级规范化。 |
| A3 Todo 进度面板渲染 | ✓ | `todoProgressView()` 显示 `Tasks done/total`、当前任务、pending/completed 摘要。 |
| A4 View 插入面板 | ✓ | 面板位于旧工具面板和 usage 之间。 |
| A5 ctrl+y 折叠/展开 | ✓ | 展开显示全部任务，折叠只显示 in_progress 和摘要。 |
| A6 底部状态栏显示当前任务 | ✓ | 工具开始时优先显示 `Task: <当前 in_progress>`。 |
| A7 提示词引导 TodoWrite | ✓ | `usingToolsSection()` 要求多步骤任务创建简洁 todo、单一 in_progress、完成即标记 completed。 |
| A8 会话恢复加载 todo | ✓ | `NewModel` 和 `StreamSessionResume` 从 `Welcome.CWD/.claude/todos.json` 恢复。 |

验证：
- `TestModelTodoWriteUpdatesTaskProgressPanel`
- `TestModelTodoPanelToggleExpandsAllTasks`
- `TestModelRunningStatusUsesCurrentTodoOnToolStart`
- `TestModelLoadsTodosFromWelcomeCWD`
- `TestModelReloadsTodosFromResumeWelcomeCWD`

### 已完成 — 等待期动画心跳（修复"长任务主体区无实时状态"）

**问题**：长任务里绝大部分时间其实是**等模型 API 返回**（工具已跑完、下一条 assistant 消息未到）。此阶段 `liveTranscriptView()` 因 `timelineLiveView()` 非空而提前 `return`，跳过了唯一绘制动画状态行的 `if m.busy` 分支——主体区只剩静止的已完成片段。盲文 spinner 此前只作为 **running 工具/子代理的图标**存在，覆盖不到"等模型"这段最耗时的空档。用户体感为"持续 10 分钟主体区什么都没显示"，唯一在动的是底部 chrome 里不起眼的三点省略号。

**修复**：
- 把 `liveTranscriptView()` 拆为 `liveTranscriptBody()`（原分支体）+ 外层 `appendLiveWorkingLine()`。busy 且无活跃动画片段时，在主体区底部追加一行动画心跳，与工具/todo 行同为 2 空格缩进：

  ```
    ✓ 运行命令  命令：echo hi  完成：命令成功
    ⠙ Go Claude is processing tool results...  1m12s
  ```

- 新增 `liveWorkingLine()`：`  ` + `progressSpinnerFrame(m.now())`（盲文圈）+ `currentRunningStatus()`（阶段文案 + 省略号动画）+ `runningDurationLabel(整轮 elapsed)`；idle 或有活跃片段时返回 `""`。`turnStopping` 时文案切为 `Stopping...`。
- 新增 `hasActiveAnimatedSegment()`：running 工具 / running 子代理 / streaming 文本任一存在即为 true → 抑制心跳，避免双 spinner（那些片段本身就在动）。
- 心跳是**纯 viewport 覆盖层**：busy 结束即消失，永不写入 scrollback；动画由既有 120ms `spinnerTick` 循环驱动（该循环只看 `m.busy`、与片段无关地重绘），无需改动画驱动。
- 旧 `if m.busy` fallback 内的 `currentRunningStatus()` 直写移除，改由 `appendLiveWorkingLine()` 统一负责，避免空 body 时状态重复。

**验证**（TDD RED→GREEN，`go test ./internal/tui/... ./internal/query/... ./internal/cli/...` 全绿）：
- `TestLiveWorkingLineShowsHeartbeatWhileWaiting`
- `TestLiveWorkingLineSuppressedWhileToolRunning`
- `TestLiveWorkingLineHiddenWhenIdle`

### Claude Code 原版渲染结构参考

Claude Code 原版（2026 年 7 月）的工具调用渲染结构，和 go-claude 当前实现完全不同：

**整体结构：所有事件统一用 `⏺` 前缀**

```
⏺ 文本回复内容...

⏺ Read 1 file (ctrl+o to expand)

⏺ Update(docs/prompt_logic/tui_tool_progress_enhancement.md)
  ⎿  Added 19 lines
      1316  2. 确认内嵌渲染在 running/done/error 三种状态下正常
      ...

⏺ Read 1 file, recalled 1 memory, wrote 2 memories (ctrl+o to expand)

⏺ Bash(git add -A && git commit -m "docs: ...")
  ⎿  [main 1d7f584] docs: 追加 Task/Agent 渲染改进 TODO + 审阅修正记录
      1 file changed, 19 insertions(+)

⏺ ---
```

**关键特征：**

| 元素 | 渲染格式 | 示例 |
|------|----------|------|
| 文本回复 | `⏺` + 正文 | `⏺ 你说得对，...` |
| Read 工具 | `⏺` + 动作摘要 + `(ctrl+o to expand)` | `Read 1 file (ctrl+o to expand)` |
| Edit/Write/Update 工具 | `⏺` + `ToolName(filepath)` + `⎿` diff 摘要 | `Update(docs/...) ⎿ Added 19 lines` |
| Memory 操作 | 合并为一行摘要 | `Read 1 file, recalled 1 memory, wrote 2 memories` |
| Bash 工具 | `⏺` + `Bash(command)` + `⎿` 输出摘要 | `Bash(git add...) ⎿ [main 1d7f584]...` |
| 分隔线 | `⏺ ---` | `⏺ ---` |

**与 go-claude 当前实现的差异：**

| 维度 | Claude Code 原版 | go-claude 当前 |
|------|-----------------|---------------|
| 前缀 | 统一 `⏺`（不区分文本/工具） | 文本无前缀，工具用颜色区分 |
| 工具名 | `ToolName(args_summary)` 一行 | `ToolName detail icon → result elapsed` |
| diff/输出 | `⎿` 下面折叠展示 | 无折叠，截断显示 |
| 合并 | 同类操作合并为一行 | 每个工具单独一行 |
| 展开 | `(ctrl+o to expand)` | `ctrl+t expand` |
| 状态图标 | 无（完成即展示结果） | `⋯/✓/✗` |
| 耗时 | 不显示 | 显示 `2s` |

**go-claude 应该保留的差异（有价值的改进）：**

- 状态图标 `⋯/✓/✗`：Claude Code 原版没有 running 状态图标，go-claude 加了更好
- 耗时显示：Claude Code 原版不显示耗时，go-claude 加了更好
- 颜色区分：Claude Code 原版无颜色区分，go-claude 加了更好

**go-claude 应该学习的差异（需要改进）：**

- `⎿` 折叠展示 diff/输出：比截断显示更优雅
- 同类操作合并：`Read 3 files` 比 3 行 `Read xxx ✓` 更简洁
- 工具名+参数摘要一行：`Bash(git add -A)` 比 `Bash git add -A && git commit... ✓` 更简洁
- `(ctrl+o to expand)` 比 `ctrl+t expand` 更自然（o = open/expand）

这些差异是后续迭代的参考方向，当前先聚焦于"删除旧机制"和"实现任务进度展示"。
