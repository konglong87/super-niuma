# 修复计划：TUI AI 回复末尾被工具栏遮挡

## 核心改动（2 处）

### 1. `View()` 中用 `m.viewport.View()` 替代 `liveTranscriptView()` 直接拼接

**文件**: `internal/tui/app.go:934-939`

```go
// 修改前
hasLive := false
if live := m.liveTranscriptView(); live != "" {
    parts = append(parts, live)
    hasLive = true
}

// 修改后
hasLive := false
if live := m.liveTranscriptView(); live != "" {
    parts = append(parts, m.viewport.View())
    hasLive = true
}
```

关键点：
- 只在 `liveTranscriptView()` 有内容时才使用 viewport（空内容时跳过，和当前行为一致）
- viewport 高度由 `viewportHeightForContent()` 计算 = `min(normalHeight, contentHeight)`，短内容时等于内容高度，不会有多余空白
- viewport `View()` 用 lipgloss `Height(contentHeight)` 填充，当 viewport 高度 = 内容高度时无多余空白
- 长内容时 viewport 裁剪到 `normalViewportHeight()` 行，底部 chrome 始终可见
- 不需要在 `View()` 中调用 `refreshViewport()`（`View()` 是值接收者，不能调用指针方法），所有状态变化点都已调用 `refreshViewport()`

### 2. `Update()` 中把滚动键传给 viewport

**文件**: `internal/tui/app.go:660-662`

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

## 测试调整

### 需要修改的现有测试

1. **`TestModelMainChatDoesNotCaptureHistoryScrollKeys`** (line 986): 当前验证 pgup 不影响 viewport offset。改为验证 pgup 确实滚动 viewport（offset 变化）。

2. **`TestModelMainChatDoesNotCaptureMouseWheel`** (line 1008): 当前验证鼠标滚轮不影响 viewport。改为验证鼠标滚轮确实滚动 viewport（当 mouseTracking 开启时）。

### 需要新增的测试

1. **`TestModelViewportClipsLongContent`**: 验证长 AI 回复被裁剪到 viewport 高度内，不被底部 chrome 覆盖。具体：设置 40 条 assistant message，验证 `View()` 的总行数不超过 `m.height`。

## 验证步骤

1. `go test ./internal/tui/... -count=1` 全部通过
2. 手动测试：AI 回复很长时，末尾不被工具栏覆盖
3. 手动测试：AI 回复很短时，布局紧凑无多余空白
4. 手动测试：pgup/pgdown 可以滚动查看 AI 回复上方内容
5. 手动测试：窄终端（24 行）下表现正常
