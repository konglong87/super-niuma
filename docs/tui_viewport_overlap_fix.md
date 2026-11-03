# TUI AI 回复末尾被工具栏遮挡 — 根因分析与修复方案

## 问题描述

TUI 模式下，AI 回复的最后几行在屏幕上显示不全，被底部的「Tools ... +7 tool uses」工具栏覆盖。

## 根因分析

核心原因：**`View()` 方法直接拼接 `liveTranscriptView()` 的原始文本，没有使用 `m.viewport.View()` 来裁剪和滚动内容。**

### 代码路径详解

#### 1. View() 渲染布局（`app.go:923`）

```go
func (m Model) View() string {
    parts := make([]string, 0, 6)
    if live := m.liveTranscriptView(); live != "" {
        parts = append(parts, live)          // ← AI 回复全文，无裁剪
    }
    if tools := m.toolActivityView(); tools != "" {
        parts = append(parts, tools)         // ← 工具栏
    }
    if usage := m.usageView(); usage != "" {
        parts = append(parts, usage)         // ← usage 信息
    }
    // ... suggestions, picker, attachments
    parts = append(parts, m.inputBoxView(status)) // ← 输入框
    return strings.Join(parts, "\n")
}
```

**关键问题**：`liveTranscriptView()` 返回的是 AI 回复的**完整原始文本**，没有任何高度限制。所有 parts 拼接后直接输出到终端，总行数可能远超终端高度。

#### 2. viewport 被设置但从未渲染

`refreshViewport()`（`app.go:2926`）正确地把内容设进 viewport 并计算了高度：

```go
func (m *Model) refreshViewport() {
    content := m.liveTranscriptView()
    m.viewport.Height = m.viewportHeightForContent(content) // ← 正确计算
    m.viewport.SetContent(content)                          // ← 正确设内容
    if m.stickToBottom {
        m.viewport.GotoBottom()
    }
}
```

但 `View()` 中**从未调用 `m.viewport.View()`**。viewport 的裁剪和滚动功能完全被绕过。

搜索 `m.viewport.View` 在整个 `app.go` 中无任何调用点。

#### 3. viewport 滚动键被吞掉

`Update()` 中（`app.go:660`）：

```go
if m.isViewportScrollKey(msg) {
    return m, nil  // ← 直接返回，没有把按键传给 viewport
}
```

用户按 pgup/pgdown 等滚动键时，viewport 完全不响应。

#### 4. 高度计算逻辑（虽然正确但无用）

`fixedChromeHeight()`（`app.go:2961`）正确计算了 chrome（工具栏 + usage + 输入框）的高度：

```go
func (m Model) fixedChromeHeight() int {
    toolHeight := lineCount(m.toolActivityView())
    usageHeight := lineCount(m.usageView())
    inputHeight := lineCount(m.inputBoxView("Ready"))
    return toolHeight + usageHeight + liveUsageSpacerHeight + inputHeight + 2
}
```

`normalViewportHeight()`（`app.go:2946`）正确地从总高度中扣除 chrome：

```go
func (m Model) normalViewportHeight() int {
    return max(3, m.height - m.fixedChromeHeight())
}
```

这些计算都是正确的，但因为 `View()` 不使用 viewport，这些计算结果**完全不起作用**。

#### 5. 工具栏归档机制加剧问题

AI 回复期间，工具栏在 viewport 外（chrome 区域），占据底部空间。
AI 回复完成后，`archiveToolActivityToAssistant()`（`app.go:2736`）把工具栏归档到 message 内部：

```go
func (m *Model) archiveToolActivityToAssistant() {
    m.messages[i].tools = archived  // ← 工具信息变成 message 内容的一部分
    m.toolActivity = nil            // ← chrome 区域的工具栏消失
}
```

归档后，`renderMessage()`（`app.go:3037`）把工具信息渲染在 assistant message 内部：

```go
if tools := m.messageToolActivityView(msg.tools); tools != "" {
    b.WriteString("\n")
    b.WriteString(tools)  // ← 工具信息变成 AI 回复的一部分
}
```

这意味着归档后，AI 回复文本变得更长（多了工具信息行），但 viewport 仍然不裁剪，末尾仍然被下方的 usage + 输入框覆盖。

### 问题链路总结

```
AI 回复全文（无裁剪）
  ↓
直接拼接到 View() 输出
  ↓
下方紧跟 toolActivityView / usageView / inputBoxView
  ↓
总行数 > 终端高度
  ↓
AI 回复末尾被底部 chrome 覆盖
```

viewport 本应解决这个问题（裁剪内容到可用高度 + 滚动），但 `View()` 完全绕过了 viewport。

## 修复方案

### 方案 A：让 View() 使用 viewport（推荐）

**改动最小，效果最直接。**

1. **`View()` 中用 `m.viewport.View()` 替代直接拼接 `liveTranscriptView()`**

   ```go
   // 修改前
   if live := m.liveTranscriptView(); live != "" {
       parts = append(parts, live)
   }

   // 修改后
   if m.viewport.Height > 0 {
       parts = append(parts, m.viewport.View())
   }
   ```

2. **`Update()` 中把滚动键传给 viewport**

   ```go
   // 修改前
   if m.isViewportScrollKey(msg) {
       return m, nil
   }

   // 修改后
   if m.isViewportScrollKey(msg) {
       var cmd tea.Cmd
       m.viewport, cmd = m.viewport.Update(msg)
       return m, cmd
   }
   ```

3. **确保 `refreshViewport()` 在所有内容变化时被调用**

   当前 `refreshViewport()` 已在多处被调用（streamEventMsg、windowResize 等），需确认覆盖所有场景。

**优点**：
- viewport 的裁剪和滚动功能天然解决遮挡问题
- 改动集中在 2 处，风险低
- 与现有 `fixedChromeHeight()` / `normalViewportHeight()` 计算逻辑完美配合

**风险点**：
- viewport 只包含 `liveTranscriptView()`（最后一条未打印消息），历史消息已通过 `flushTranscriptCmd()` 输出到终端。需确认 viewport 滚动时不会出现空白（因为内容只有一条消息）。
- viewport 的 `SetContent` 在 `refreshViewport()` 中调用，需确认 `View()` 渲染时内容是最新的。

### 方案 B：在 View() 中手动裁剪 liveTranscriptView

不使用 viewport，在 `View()` 中手动计算可用高度并裁剪 `liveTranscriptView()` 的输出。

```go
if live := m.liveTranscriptView(); live != "" {
    availableHeight := m.height - m.fixedChromeHeight()
    lines := strings.Split(live, "\n")
    if len(lines) > availableHeight {
        // 只显示最后 availableHeight 行（末尾优先）
        lines = lines[len(lines)-availableHeight:]
    }
    parts = append(parts, strings.Join(lines, "\n"))
}
```

**优点**：
- 不依赖 viewport，逻辑更直观
- 不需要处理 viewport 滚动键

**缺点**：
- 用户无法滚动查看被裁剪的内容
- 需要自己处理 ANSI escape code 跨行截断问题（lipgloss 样式可能跨行）
- 每次渲染都要重新 split/join，性能不如 viewport

### 方案 C：调整渲染顺序（不推荐）

把 toolActivityView 渲染在 liveTranscriptView 之前（上方），让 AI 回复紧贴输入框。

**缺点**：
- 破坏视觉布局（工具栏在 AI 回复上方不符合直觉）
- AI 回复末尾仍然可能被 usage + 输入框覆盖
- 没有解决根本问题（内容无裁剪）

## 推荐方案

**方案 A**（让 View() 使用 viewport）是最佳选择：

1. 改动最小（2 处核心改动）
2. 利用已有的 viewport 机制，不需要重新实现裁剪逻辑
3. 支持滚动，用户可以查看被遮挡的内容
4. 与现有高度计算逻辑（`fixedChromeHeight` / `normalViewportHeight`）完美配合

### 需要额外验证的点

1. **viewport 内容同步**：确认 `View()` 调用时 viewport 内容与 `liveTranscriptView()` 一致。当前 `refreshViewport()` 在 stream 事件时被调用，但 `View()` 每帧都会调用，需确认没有时序问题。

2. **viewport 滚动范围**：viewport 只包含一条消息的内容，滚动范围有限。历史消息已通过 `tea.Println` 输出到终端上方（不可滚动回看）。这是现有设计，不是本次修复的范围。

3. **ANSI 样式跨行**：viewport 的 `SetContent` 接收带 lipgloss 样式的文本，bubbles viewport 内部处理了 ANSI escape code 的行分割，应该没有问题。但需实际测试确认。

4. **stickToBottom 行为**：当前 `refreshViewportAtBottom()` 设置 `stickToBottom = true` 并调用 `viewport.GotoBottom()`。使用 viewport 渲染后，新内容到来时 viewport 应自动滚到底部，需确认这个行为保持一致。

## 涉及文件

| 文件 | 改动点 |
|------|--------|
| `internal/tui/app.go:923-960` | `View()` 方法：用 `m.viewport.View()` 替代 `liveTranscriptView()` |
| `internal/tui/app.go:660` | `Update()` 方法：把滚动键传给 viewport |

## 测试验证

修复后需验证：

1. AI 回复末尾不再被工具栏/输入框覆盖
2. AI 回复很长时，viewport 自动裁剪并滚到底部
3. pgup/pgdown 可以滚动查看 AI 回复的上方内容
4. 工具栏归档后（从 chrome 移到 message 内），viewport 高度正确增大
5. 窄终端（如 24 行）下表现正常
6. 现有测试 `go test ./internal/tui/... -count=1` 全部通过
