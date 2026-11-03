# TUI Completed Assistant Tail Anchor Fix Plan

本文档记录 TUI 模式下“assistant 已完整输出，但当前屏最后几句仍显示不全”的第二阶段根因和彻底修复方案。第一阶段的底部 chrome 覆盖问题已由 `docs/tui/tui_transcript_bottom_chrome_fix_plan.md` 覆盖；本文档处理的是 recap/status 追加后 live viewport 失去完成态 assistant 尾部锚点的问题。

## 背景

复现场景：

```bash
go run cmd/golang-cc/* --cwd $HOME/GolandProjects/anything-ai
```

用户提问：

```text
你怎么看待当前项目？有啥用？有啥优化的地方？
```

问题 session：

```text
37a7791c-8d80-4643-98f1-8a45fc71efc1
```

截图中当前屏停在：

```text
核心风险：广度铺开了，深度和
```

但 JSONL transcript 中 assistant message 已完整落盘，尾部实际包含：

```text
**核心风险**：广度铺开了，深度和可持续性是最大挑战。16个角色×15个技能×双语，内容维护是个长期负担。

**一句话建议**：与其继续铺面，不如先挑3-5个最有价值的方向做深，做出真正有人引用和推荐的内容，再逐步扩展。
```

同一 transcript 最后一条是 `recap_summary`，来源为 `away`。这说明问题不是模型截断，也不是 recorder 丢内容，而是 TUI 当前屏渲染状态丢失了“最后完整 assistant 回复”的显示锚点。

## 根因

当前 TUI 混合了两层输出：

1. 完成态 transcript：通过 `flushTranscriptCmd()` 和 `tea.Println(...)` 写入 terminal scrollback。
2. 当前屏 live viewport：通过 Bubble Tea `View()` 重绘，显示 live 内容、Usage、输入框和 status/controls。

上一轮修复已加入 `completedAssistantTailView()`，用于在回答完成后把最后一条 assistant 的尾部留在 live viewport。但它有两个过窄条件：

```go
if len(m.messages) == 0 || m.transcriptPrintedCount < len(m.messages) {
    return ""
}
msg := m.messages[len(m.messages)-1]
if msg.role != "assistant" {
    return ""
}
```

真实会话里 assistant 完成后会触发 away recap，`applyRecap(...)` 会追加 `message{role:"recap"}`。此时：

- 最后一条 message 不再是 assistant。
- recap 本身不会进入 transcript flush。
- `shouldShowLiveRecap()` 又会因为 Usage 已存在而返回 false，避免 recap 覆盖回答。
- 结果 `liveTranscriptView()` 没有可显示内容，或者留下旧 viewport 画面。

因此用户看到的是“像被截断或被覆盖”的当前屏残留，而不是模型输出不完整。

## 修复目标

P0 目标：

1. assistant 已完成且 transcript 已打印后，当前屏必须稳定显示最近一条完整 assistant 的尾部。
2. 追加 recap/status/system 等非回答消息后，不能让 completed assistant tail 消失。
3. Usage、输入框、status/controls 保持可见。
4. 不把 recap 内容强行覆盖在最终回答上。
5. 不污染 JSONL transcript，不修改模型原始输出。

非目标：

1. 不处理 provider 真的返回半句、网络中断或 `max_tokens` 截断。
2. 不把完整历史长期塞回 viewport。
3. 不改变 away recap 的持久化语义。
4. 不调整 session id / resume 语义。

## 方案

### 1. 用“最近完成 assistant”替代“最后一条 message”

新增一个内部查找函数：

```go
func (m Model) latestCompletedAssistantMessage() (message, bool)
```

查找规则：

- 从 `m.messages` 尾部向前扫描。
- 找到最近一条 `role == "assistant"` 且 content 非空的消息。
- 忽略 `recap`、`status`、`system` 等非回答消息。
- 如果遇到尚未打印完成的 live assistant，则仍由 `lastLiveMessage()` 处理，不走 completed tail。

### 2. 放宽 completed tail 激活条件

`completedAssistantTailView()` 不再要求 `m.messages[len-1]` 是 assistant。它只要求：

- 当前不 busy。
- 当前没有 error、permission、resume picker、rewind picker。
- 已完成 transcript 打印，即不存在待打印的非 recap transcript block。
- 能找到最近一条完整 assistant。

示意：

```go
func (m Model) completedAssistantTailView() string {
    if m.busy || m.err != nil || m.pendingPermission != nil || m.resumePickerActive() || m.rewindPickerActive() {
        return ""
    }
    if m.hasPendingTranscriptBlock() {
        return ""
    }
    msg, ok := m.latestCompletedAssistantMessage()
    if !ok {
        return ""
    }
    return m.renderCompletedAssistantTail(msg)
}
```

### 3. completed tail 强制锚到底部

`refreshViewport()` 当前只在 `stickToBottom` 为 true 时 `GotoBottom()`。completed tail 是最终屏保护层，不应受用户过去滚动状态影响，否则同样可能停在中段。

新增判断：

```go
shouldAnchor := m.stickToBottom || m.completedAssistantTailActive()
if shouldAnchor {
    m.viewport.GotoBottom()
}
```

实现时避免重复渲染昂贵 markdown，可通过 `liveTranscriptView()` 返回的内容和一个轻量状态判断配合完成。

### 4. recap 保持低优先级

`shouldShowLiveRecap()` 保持现有保守逻辑：有 Usage 时不显示 recap。recap 是恢复上下文和下轮提示的辅助信息，不应该抢当前回答的最终可见性。

### 5. 测试和 PTY 验收

新增回归测试：

1. `TestModelCompletedAssistantTailSurvivesAwayRecap`
   - 构造长 assistant 完成态。
   - `transcriptPrintedCount == len(messages)` 后追加 recap。
   - 断言 `View()` 和 `viewport.View()` 仍包含 assistant 最终尾句。
   - 断言不显示 recap 覆盖回答。

2. `TestModelCompletedAssistantTailAnchorsToBottom`
   - 构造 viewport 高度小于 assistant 内容高度。
   - 手动令 `stickToBottom=false`。
   - `refreshViewport()` 后仍应显示最终尾句。

真实 PTY 验收：

- 使用 tmux 启动临时复现程序。
- 同一段 37a assistant 文本。
- A/B 比较：
  - 禁用 recap 时尾句可见。
  - 启用 recap 后尾句仍可见。
- 保存当前屏文本和截图到 `/tmp/gocc-tui-tail-anchor-*`。

验收关键字：

```text
深度和可持续性是最大挑战
一句话建议
Usage
status  Ready
```

## 为什么这是彻底修复

这次问题的本质不是“需要再加几行空白”，而是最终屏渲染没有稳定锚点。只要 live viewport 依赖“最后一条 message 必须是 assistant”，未来任何 recap/status/system/summary 类消息追加到 assistant 后面，都可能再次让尾部消失。

本方案把最终屏锚点从“最后一条 message”升级为“最近一条已完成 assistant”。这符合用户认知：一次回答完成后，当前屏最重要的是让用户看到回答尾部，而不是让内部 recap/status 影响展示优先级。

扩展性：

1. 后续新增 `compact_summary`、`goal_status`、`background_status` 等内部消息，不会破坏最终 assistant 可见性。
2. 后续要展示 recap，可以在单独轻量区域或 `/recap` 命令里展示，不会争夺回答尾部。
3. 后续若将 transcriptPrintedCount 升级为 message id，本方案只需要替换 pending 判断，不改变锚点语义。
4. 与底部 chrome guard 兼容：guard 保护 scrollback，completed tail anchor 保护当前屏，两者职责分离。

## 实施步骤

1. 在 `internal/tui/app.go` 新增最近完成 assistant 查找和 pending transcript 判断。
2. 更新 `completedAssistantTailView()`，忽略 recap/status 后缀。
3. 更新 `refreshViewport()`，completed tail 激活时强制滚到底部。
4. 在 `internal/tui/app_test.go` 增加 assistant+recap 和 stickToBottom=false 回归测试。
5. 执行：

```bash
go test ./internal/tui -count=1
go test ./... -count=1
git diff --check
```

6. 执行真实 PTY 截图验证，保存 evidence 路径。
