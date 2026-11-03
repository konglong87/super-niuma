# AskUserQuestion 真交互设计

日期：2026-07-25
状态：已实施（2026-07-25，f6bca00 / 856b546 / a24fe27 / a19b434 / da5542f）

## 背景

AskUserQuestion 工具当前无交互实现：`internal/tools/askuserquestion/askuserquestion.go` 的
`Run()` 无条件返回 `IsError: true`，内容为 `User input required: <question> + 选项列表`，
以"错误结果"承载"停下来等用户输入"的语义。TUI 把所有 `is_error` 工具结果统一渲染为
`× 未完成：操作未完成`（`toolResultPrefix` / `errorResultSummary` 的 default 分支），
用户看到的是一次"失败"，实际是设计如此 + 渲染误导。

实测 session：`~/.go-claude/projects/Users-example-GolandProjects-superPM/88c456fd-3b04-4938-90c1-1206f39ed8ad.jsonl`
第 30–33 行，`tool_result` 带 `is_error: true`、`action_record` 记 `success: false`。

## 目标

TUI 模式下拦截 AskUserQuestion 调用，弹出问题 + 选项（含自由文本输入），用户作答后以
**正常（非 error）tool_result** 返回给模型；非交互模式（print/headless/子代理）保持现状。

## 方案选择

- **A. `tools.Context` 回调（采用）**：完整镜像现有 PermissionPrompt 四层链路
  （tools → query → cli → tui）。工具内判断回调存在与否，不存在走现有 error 兜底。
  引擎不 hardcode 工具名，非交互模式零改动。
- B. query 循环特判工具名拦截：不改工具，但把单一工具语义泄漏进引擎 dispatch，
  行为分裂两处，与权限模式不同构。不采用。

## 分层设计

### 1. internal/tools/tool.go

`Context` 新增回调与类型：

```go
UserQuestion func(context.Context, UserQuestionRequest) UserQuestionResponse

type UserQuestionRequest struct {
    Question string
    Choices  []string
}

type UserQuestionResponse struct {
    Answered bool
    Answer   string
}
```

### 2. internal/tools/askuserquestion

`Run()` 中：

- `tc.UserQuestion != nil` → 调用并阻塞等答案：
  - `Answered == true` → 正常结果（IsError=false），内容 `User answered: <answer>`
  - `Answered == false`（用户 Esc 取消 / ctx 取消）→ 退回现有
    `User input required: ...` error 结果（模型文字转述问题，平滑降级）
- 回调为 nil → 现状完全不变

### 3. internal/query/query.go

- `Options` 新增 `UserQuestionPrompt func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse`
- `runTool` 组装 `tools.Context` 时透传（与 query.go:2757 PermissionPrompt 同款写法；
  nil 则不设）
- 子代理（Task 工具）建会话时不设置该字段，天然走兜底路径（与 PermissionPrompt 行为一致）

### 4. internal/cli/interactive.go

- `tuiUserQuestionPrompt(events)` 镜像 `tuiPermissionPrompt`（interactive.go:1046）：
  新 StreamEvent 类型 `StreamUserQuestion`，携带 `Question *tui.UserQuestionRequest` 与
  buffered reply channel `QuestionReply chan tui.UserQuestionAnswer`；发事件后阻塞等回复，
  `ctx.Done()` 兜底返回未回答
- 接线点：interactive.go:204 附近，`streamOpts.userQuestionPrompt = tuiUserQuestionPrompt(events)`，
  经 cli.go options → query.Options 透传

### 5. internal/tui/app.go

新增 `pendingQuestion *pendingUserQuestion` 状态，全面复用 `pendingPermission` 模式：

- **事件接收**：Update() streamEventMsg 分支（app.go:1877 旁）识别 `StreamUserQuestion`，
  设置 pendingQuestion，暂停 waitForStreamEvent（query goroutine 阻塞中）
- **弹窗渲染**：`questionPromptView` 镜像 `permissionPromptView`（app.go:8352），
  显示问题 + 选项列表；末尾固定追加"其他（自由输入）"项；bottom chrome 渲染点在
  app.go:2950 pendingPermission 旁
- **按键**：`handleQuestionKey` 镜像 `handlePermissionKey`（app.go:6114）：
  ↑/↓/tab 导航、1-9 快选、Enter 确认、Esc 取消（→未回答）；
  选中"其他"或模型未给 choices 时进入单行文本输入模式
  （可打印字符入缓冲、backspace 删除、Enter 提交非空文本；Esc 在有 choices 时
  返回选项列表，无 choices 时直接取消→未回答）
- **输入互斥**：pendingQuestion 期间主输入框禁用，与 pendingPermission 同一批 gate 点
  （app.go:2176、2268、3276、3516 等）
- **清理**：中断/回合结束清 pendingQuestion（app.go:1991、4853 旁）；
  reply channel 有缓冲且 query 侧 select ctx.Done，不死锁
- **结果渲染**：
  - `friendlyToolResult` 加 AskUserQuestion case → `用户选择：<answer>`
  - 工具调用行 detail（`friendlyToolDetail` app.go:1458 的 switch）加 askuserquestion
    case，显示问题文本而非原始 JSON

## 交互迭代（2026-07-25 第二版）

原设计的"其他（自由输入）→ Enter 进入文本模式"两段式已改为**常驻输入框**：

- 选项列表末尾固定渲染一行输入框 `N. 输入: _▌`，无独立文本模式（textMode 移除）
- 焦点在输入框上时打字直接录入；在任意选项行上敲**非数字**可打印字符会自动跳到输入框并录入
- 数字 1-9 在选项行上仍是快选；Enter 在输入框上提交非空文本（空则仅聚焦）
- Esc：输入框有内容先清空，否则取消提问；无 choices 时输入框即唯一行、默认聚焦

## 结果格式

- 作答：`User answered: <answer>`（英文，与工具协议一致）；transcript 的
  `action_record` `success=true` 自动成立
- 取消/非交互：维持 `User input required: <question>\n- <choice>...`（IsError=true）

## 测试

- 工具单测（askuserquestion_test.go）：有回调→正常结果；回调返回未回答→error 兜底；
  无回调→现状不变
- TUI 测试（app_test.go，照搬权限弹窗测试模式：buffered reply channel + KeyMsg 驱动）：
  事件→弹窗渲染；导航+Enter→reply 收到正确答案；数字快选；Esc→未回答；
  文本模式输入与提交；无 choices 直接进文本模式
- query 测试：Options.UserQuestionPrompt 透传进 tools.Context
- 回归：`go test ./...` 全绿后才提交
