# AskUserQuestion 真交互实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** TUI 模式下 AskUserQuestion 弹出问题+选项/自由输入，用户作答后以正常（非 error）tool_result 返回；非交互模式保持现状。

**Architecture:** 完整镜像 PermissionPrompt 四层链路：`tools.Context` 新增 `UserQuestion` 回调 → `query.Options.UserQuestionPrompt` 透传 → `cli/interactive.go` 发 `StreamUserQuestion` 事件并阻塞等 buffered reply channel → TUI `pendingQuestion` 弹窗收答案回填。

**Tech Stack:** Go 1.26（`~/go/sdk` 下，见 memory go-toolchain-path）、Bubble Tea TUI、标准库 testing。

**Spec:** docs/askuserquestion_interactive_design.md

## Global Constraints

- commit 只用 konglong 身份，不带任何 AI 署名（无 Co-Authored-By / Generated with）
- 代码改动必须测试通过后才 commit；每个任务结束时全仓库可编译
- 结果文案（工具协议侧，英文）：作答 `User answered: <answer>`；取消/非交互维持 `User input required: ...`（IsError=true）
- TUI 文案（用户侧，中文）："其他（自由输入）"、"用户选择：<answer>"、"等待用户输入"
- 中文文本一律用 `truncateDisplay`（rune 安全）截断，不用字节版 `truncate`

---

### Task 1: tools 层 — Context 回调类型 + 工具行为

**Files:**
- Modify: `internal/tools/tool.go`（Context 结构 17-39 行区域 + 类型定义）
- Modify: `internal/tools/askuserquestion/askuserquestion.go`
- Test: `internal/tools/askuserquestion/askuserquestion_test.go`

**Interfaces:**
- Produces（后续任务依赖）:
  - `tools.UserQuestionRequest{Question string; Choices []string}`
  - `tools.UserQuestionResponse{Answered bool; Answer string}`
  - `tools.Context.UserQuestion func(context.Context, UserQuestionRequest) UserQuestionResponse`
  - 作答结果内容：`"User answered: " + answer`（IsError=false）

- [ ] **Step 1: 写失败测试**（追加到 askuserquestion_test.go）

```go
func TestAskUserQuestionInteractiveAnswered(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"question": "Which path?", "choices": []string{"A", "B"}})
	var got tools.UserQuestionRequest
	res := New().Run(context.Background(), input, tools.Context{
		UserQuestion: func(_ context.Context, req tools.UserQuestionRequest) tools.UserQuestionResponse {
			got = req
			return tools.UserQuestionResponse{Answered: true, Answer: "B"}
		},
	})
	if res.IsError || res.Content != "User answered: B" {
		t.Fatalf("result = %+v", res)
	}
	if got.Question != "Which path?" || len(got.Choices) != 2 || got.Choices[1] != "B" {
		t.Fatalf("request = %+v", got)
	}
}

func TestAskUserQuestionInteractiveCancelledFallsBack(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"question": "Which path?", "choices": []string{"A"}})
	res := New().Run(context.Background(), input, tools.Context{
		UserQuestion: func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
			return tools.UserQuestionResponse{}
		},
	})
	if !res.IsError || !strings.Contains(res.Content, "User input required: Which path?") || !strings.Contains(res.Content, "- A") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAskUserQuestionInteractiveBlankAnswerFallsBack(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"question": "Which path?"})
	res := New().Run(context.Background(), input, tools.Context{
		UserQuestion: func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
			return tools.UserQuestionResponse{Answered: true, Answer: "   "}
		},
	})
	if !res.IsError || !strings.Contains(res.Content, "User input required: Which path?") {
		t.Fatalf("result = %+v", res)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/tools/askuserquestion/ -run TestAskUserQuestionInteractive -v`
Expected: 编译错误 `tools.UserQuestionRequest` 未定义（等价于失败）

- [ ] **Step 3: 实现**

`internal/tools/tool.go` — Context 结构中 `RuntimePermissionMode func() string` 之后加一行：

```go
	UserQuestion          func(context.Context, UserQuestionRequest) UserQuestionResponse
```

`PermissionAudit` 类型定义之前加：

```go
type UserQuestionRequest struct {
	Question string
	Choices  []string
}

type UserQuestionResponse struct {
	Answered bool
	Answer   string
}
```

`internal/tools/askuserquestion/askuserquestion.go` — Run() 全量替换为：

```go
func (Tool) Run(ctx context.Context, input json.RawMessage, tc tools.Context) tools.Result {
	var params struct {
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tools.Result{Content: err.Error(), IsError: true}
	}
	question := strings.TrimSpace(params.Question)
	if question == "" {
		return tools.Result{Content: "question is required", IsError: true}
	}
	choices := make([]string, 0, len(params.Choices))
	for _, choice := range params.Choices {
		if c := strings.TrimSpace(choice); c != "" {
			choices = append(choices, c)
		}
	}
	if tc.UserQuestion != nil {
		resp := tc.UserQuestion(ctx, tools.UserQuestionRequest{Question: question, Choices: choices})
		if answer := strings.TrimSpace(resp.Answer); resp.Answered && answer != "" {
			return tools.Result{Content: "User answered: " + answer}
		}
	}
	var b strings.Builder
	b.WriteString("User input required: ")
	b.WriteString(question)
	for _, choice := range choices {
		b.WriteString("\n- ")
		b.WriteString(choice)
	}
	return tools.Result{Content: b.String(), IsError: true}
}
```

注意：Run 签名第一个参数由 `_` 改为 `ctx`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/tools/askuserquestion/ -v && go build ./...`
Expected: 全部 PASS（含既有 TestAskUserQuestionRequiresInput），编译通过

- [ ] **Step 5: Commit**

```bash
git add internal/tools/tool.go internal/tools/askuserquestion/
git commit -m "feat: tools.Context 新增 UserQuestion 回调，AskUserQuestion 支持交互作答返回正常结果"
```

---

### Task 2: query 层 — Options 透传

**Files:**
- Modify: `internal/query/query.go`（Options 定义 ~66 行 PermissionPrompt 旁 + runTool Context 组装 ~2742）

**Interfaces:**
- Consumes: `tools.UserQuestionRequest/Response`、`tools.Context.UserQuestion`（Task 1）
- Produces: `query.Options.UserQuestionPrompt func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse`

- [ ] **Step 1: Options 加字段**（`PermissionPrompt` 字段下一行）

```go
	UserQuestionPrompt            func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse
```

- [ ] **Step 2: runTool 组装 tools.Context 时透传**（`RuntimePermissionMode: s.options.RuntimePermissionMode,` 下一行）

```go
		UserQuestion:          s.options.UserQuestionPrompt,
```

- [ ] **Step 3: 验证**

Run: `go build ./... && go test ./internal/query/ -count=1`
Expected: 编译通过，query 既有测试全绿（一行 nil-safe 透传，无独立单测；行为由 Task 1 工具测试 + Task 4 cli 测试覆盖）

- [ ] **Step 4: Commit**

```bash
git add internal/query/query.go
git commit -m "feat: query.Options 新增 UserQuestionPrompt，runTool 透传至 tools.Context"
```

---

### Task 3: TUI 层 — 事件、状态、按键、弹窗视图

**Files:**
- Modify: `internal/tui/app.go`
- Test: `internal/tui/app_test.go`

**Interfaces:**
- Produces（Task 4 依赖）:
  - `tui.UserQuestionRequest{Question string; Choices []string}`
  - `tui.UserQuestionAnswer{Answered bool; Answer string}`
  - `tui.StreamUserQuestion StreamEventType = "user_question"`
  - `StreamEvent.Question *UserQuestionRequest`、`StreamEvent.QuestionReply chan UserQuestionAnswer`
- 行为：事件到达→弹窗；Enter/数字选择→reply 收 `{Answered:true, Answer:<choice>}`；"其他（自由输入）"/无 choices→文本模式；Esc→`{Answered:false}`（文本模式且有 choices 时 Esc 先返回列表）

- [ ] **Step 1: 写失败测试**（追加到 app_test.go，紧跟 TestModelPermissionPromptDecision 系列之后）

```go
func newUserQuestionModel(t *testing.T, choices []string) (Model, chan UserQuestionAnswer) {
	t.Helper()
	reply := make(chan UserQuestionAnswer, 1)
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	event := StreamEvent{
		Type:          StreamUserQuestion,
		Question:      &UserQuestionRequest{Question: "选哪个方案？", Choices: choices},
		QuestionReply: reply,
	}
	updated, cmd := model.Update(streamEventMsg{event: event, ch: make(chan StreamEvent)})
	model = updated.(Model)
	if cmd != nil || model.pendingQuestion == nil {
		t.Fatalf("cmd=%v pendingQuestion=%+v", cmd, model.pendingQuestion)
	}
	return model, reply
}

func TestModelUserQuestionChoiceSelection(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	view := model.View()
	for _, want := range []string{"选哪个方案？", "方案A", "方案B", "其他（自由输入）"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in view:\n%s", want, view)
		}
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "方案B" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v pending=%+v", cmd, answer, model.pendingQuestion)
	}
}

func TestModelUserQuestionDigitQuickSelect(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "方案B" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionEscCancels(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A"})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || answer.Answered || answer.Answer != "" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionFreeTextViaOther(t *testing.T) {
	model, reply := newUserQuestionModel(t, []string{"方案A", "方案B"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = updated.(Model)
	if model.pendingQuestion == nil || !model.pendingQuestion.textMode {
		t.Fatalf("pending=%+v", model.pendingQuestion)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("自定义答案")})
	model = updated.(Model)
	if !strings.Contains(model.View(), "自定义答案") {
		t.Fatalf("view missing typed text:\n%s", model.View())
	}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "自定义答案" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}

func TestModelUserQuestionTextModeEscReturnsToList(t *testing.T) {
	model, _ := newUserQuestionModel(t, []string{"方案A"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(Model)
	if model.pendingQuestion == nil || !model.pendingQuestion.textMode {
		t.Fatalf("pending=%+v", model.pendingQuestion)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.pendingQuestion == nil || model.pendingQuestion.textMode {
		t.Fatalf("esc should return to list, pending=%+v", model.pendingQuestion)
	}
}

func TestModelUserQuestionNoChoicesStartsTextMode(t *testing.T) {
	model, reply := newUserQuestionModel(t, nil)
	if !model.pendingQuestion.textMode {
		t.Fatalf("expect textMode, pending=%+v", model.pendingQuestion)
	}
	if !strings.Contains(model.View(), "输入：") {
		t.Fatalf("view missing input line:\n%s", model.View())
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("好")})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	answer := <-reply
	if cmd == nil || !answer.Answered || answer.Answer != "好" || model.pendingQuestion != nil {
		t.Fatalf("cmd=%v answer=%+v", cmd, answer)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/tui/ -run TestModelUserQuestion -v`
Expected: 编译错误（StreamUserQuestion / UserQuestionRequest 未定义）

- [ ] **Step 3: 实现**（全部锚点在 internal/tui/app.go）

3a. 事件类型常量（`StreamPermissionRequest` 之后）：

```go
	StreamUserQuestion      StreamEventType = "user_question"
```

3b. StreamEvent 字段（`Reply chan PermissionDecision` 之后）：

```go
	Question      *UserQuestionRequest
	QuestionReply chan UserQuestionAnswer
```

3c. 类型定义（PermissionDecision 之后）：

```go
type UserQuestionRequest struct {
	Question string
	Choices  []string
}

type UserQuestionAnswer struct {
	Answered bool
	Answer   string
}
```

3d. pending 状态结构（pendingPermission 结构之后）：

```go
type pendingUserQuestion struct {
	request  UserQuestionRequest
	reply    chan UserQuestionAnswer
	ch       <-chan StreamEvent
	selected int
	textMode bool
	textBuf  string
}

const questionOtherLabel = "其他（自由输入）"

func questionOptionLabels(req UserQuestionRequest) []string {
	return append(append([]string(nil), req.Choices...), questionOtherLabel)
}
```

3e. Model 字段（`pendingPermission *pendingPermission` 之后）：

```go
	pendingQuestion            *pendingUserQuestion
```

3f. KeyMsg 路由（`if m.pendingPermission != nil { return m.handlePermissionKey(msg) }` 之后）：

```go
		if m.pendingQuestion != nil {
			return m.handleQuestionKey(msg)
		}
```

3g. streamEventMsg 分支（StreamPermissionRequest 分支之后）：

```go
		if msg.event.Type == StreamUserQuestion && msg.event.Question != nil && msg.event.QuestionReply != nil {
			m.pendingQuestion = &pendingUserQuestion{
				request:  *msg.event.Question,
				reply:    msg.event.QuestionReply,
				ch:       msg.ch,
				textMode: len(msg.event.Question.Choices) == 0,
			}
			m.refreshViewport()
			return m, nil
		}
```

3h. 清理（responseMsg 分支 `m.pendingPermission = nil` 之后，及 applyStreamFinishedEvent 中 `m.pendingPermission = nil` 之后，各加）：

```go
	m.pendingQuestion = nil
```

3i. 五个 gate 点，在 `m.pendingPermission != nil` 旁并列加 `m.pendingQuestion != nil`：
- `maybeStartAwayRecap`（~2176）
- `chromeModeForBudget`（~2268）
- `shouldShowIdleWelcomeLive`（~3063）
- `shouldShowLiveRecap`（~3276）
- `shouldUseQuietConversationHint`（~3516）
- `updateSlashSuggestions`（~3843）

3j. liveTranscriptView（pendingPermission 分支之后）：

```go
	if m.pendingQuestion != nil {
		return questionPromptView(*m.pendingQuestion, m.contentWidth())
	}
```

3k. 按键处理 + 提交辅助（handlePermissionKey 之后）：

```go
func (m Model) handleQuestionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pending := m.pendingQuestion
	if pending == nil {
		return m, nil
	}
	if pending.textMode {
		switch msg.Type {
		case tea.KeyEnter:
			if answer := strings.TrimSpace(pending.textBuf); answer != "" {
				return m.resolveQuestion(UserQuestionAnswer{Answered: true, Answer: answer})
			}
			return m, nil
		case tea.KeyEsc:
			if len(pending.request.Choices) == 0 {
				return m.resolveQuestion(UserQuestionAnswer{})
			}
			pending.textMode = false
			pending.textBuf = ""
			m.pendingQuestion = pending
			m.refreshViewport()
			return m, nil
		case tea.KeyBackspace:
			if runes := []rune(pending.textBuf); len(runes) > 0 {
				pending.textBuf = string(runes[:len(runes)-1])
			}
			m.pendingQuestion = pending
			m.refreshViewport()
			return m, nil
		case tea.KeySpace:
			pending.textBuf += " "
			m.pendingQuestion = pending
			m.refreshViewport()
			return m, nil
		case tea.KeyRunes:
			pending.textBuf += string(msg.Runes)
			m.pendingQuestion = pending
			m.refreshViewport()
			return m, nil
		}
		return m, nil
	}
	labels := questionOptionLabels(pending.request)
	key := strings.ToLower(msg.String())
	switch key {
	case "up", "ctrl+p", "shift+tab":
		pending.selected = wrapIndex(pending.selected-1, len(labels))
		m.pendingQuestion = pending
		m.refreshViewport()
		return m, nil
	case "down", "ctrl+n", "tab":
		pending.selected = wrapIndex(pending.selected+1, len(labels))
		m.pendingQuestion = pending
		m.refreshViewport()
		return m, nil
	case "esc":
		return m.resolveQuestion(UserQuestionAnswer{})
	case "enter":
		return m.confirmQuestionSelection(pending.selected)
	}
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if idx := int(key[0] - '1'); idx < len(labels) {
			return m.confirmQuestionSelection(idx)
		}
	}
	return m, nil
}

func (m Model) confirmQuestionSelection(idx int) (tea.Model, tea.Cmd) {
	pending := m.pendingQuestion
	if pending == nil {
		return m, nil
	}
	if idx >= len(pending.request.Choices) {
		pending.selected = idx
		pending.textMode = true
		m.pendingQuestion = pending
		m.refreshViewport()
		return m, nil
	}
	return m.resolveQuestion(UserQuestionAnswer{Answered: true, Answer: pending.request.Choices[idx]})
}

func (m Model) resolveQuestion(answer UserQuestionAnswer) (tea.Model, tea.Cmd) {
	pending := m.pendingQuestion
	if pending == nil {
		return m, nil
	}
	pending.reply <- answer
	m.pendingQuestion = nil
	m.refreshViewport()
	return m, waitForStreamEvent(pending.ch)
}
```

3l. 弹窗视图（permissionPromptView 系列之后）：

```go
func questionPromptView(p pendingUserQuestion, width int) string {
	var b strings.Builder
	width = max(24, width)
	b.WriteString(titleStyle.Render("需要你的回答"))
	b.WriteString(statusStyle.Render(truncateDisplay(" · AskUserQuestion", max(4, width-runewidth.StringWidth("需要你的回答")))))
	b.WriteString("\n")
	for _, line := range labeledDetailLines("问题", p.request.Question, width) {
		b.WriteString("\n")
		b.WriteString(line)
	}
	b.WriteString("\n")
	if p.textMode {
		for _, line := range labeledDetailLines("输入", p.textBuf+"▌", width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
		b.WriteString("\n\n")
		hint := "enter 提交"
		if len(p.request.Choices) > 0 {
			hint += " · esc 返回选项"
		} else {
			hint += " · esc 取消"
		}
		b.WriteString(statusStyle.Render(hint))
		return b.String()
	}
	for i, label := range questionOptionLabels(p.request) {
		b.WriteString("\n")
		row := fmt.Sprintf("  %d. %s", i+1, label)
		if i == p.selected {
			row = permissionSelectedStyle.Render(fmt.Sprintf("[ ▶ %d. %s ]", i+1, label))
		}
		b.WriteString(truncateDisplay(row, width))
	}
	b.WriteString("\n\n")
	b.WriteString(statusStyle.Render("↑/↓ 选择 · 1-9 快选 · enter 确认 · esc 取消"))
	return b.String()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/tui/ -run TestModelUserQuestion -v && go test ./internal/tui/ -count=1`
Expected: 新测试全 PASS，tui 包既有测试全绿

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat: TUI 新增 AskUserQuestion 交互弹窗（选项/自由输入/取消，镜像权限弹窗模式）"
```

---

### Task 4: cli 层 — 接线

**Files:**
- Modify: `internal/cli/cli.go`（options 结构 ~111 permissionPrompt 旁 + query.Options 映射 ~765）
- Modify: `internal/cli/interactive.go`（~204 接线 + tuiPermissionPrompt 之后新函数）
- Test: `internal/cli/interactive_userquestion_test.go`（新建）

**Interfaces:**
- Consumes: `query.Options.UserQuestionPrompt`（Task 2）、`tui.StreamUserQuestion`/`tui.UserQuestionRequest`/`tui.UserQuestionAnswer`（Task 3）

- [ ] **Step 1: 写失败测试**（新建 interactive_userquestion_test.go，package cli）

```go
package cli

import (
	"context"
	"testing"

	"github.com/konglong87/go-e2e/internal/tools"
	"github.com/konglong87/go-e2e/internal/tui"
)

func TestTUIUserQuestionPromptRoundTrip(t *testing.T) {
	events := make(chan tui.StreamEvent, 1)
	prompt := tuiUserQuestionPrompt(events)
	done := make(chan tools.UserQuestionResponse, 1)
	go func() {
		done <- prompt(context.Background(), tools.UserQuestionRequest{Question: "Q?", Choices: []string{"A"}})
	}()
	event := <-events
	if event.Type != tui.StreamUserQuestion || event.Question == nil || event.Question.Question != "Q?" || event.QuestionReply == nil {
		t.Fatalf("event = %+v", event)
	}
	event.QuestionReply <- tui.UserQuestionAnswer{Answered: true, Answer: "A"}
	resp := <-done
	if !resp.Answered || resp.Answer != "A" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestTUIUserQuestionPromptContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prompt := tuiUserQuestionPrompt(make(chan tui.StreamEvent))
	resp := prompt(ctx, tools.UserQuestionRequest{Question: "Q?"})
	if resp.Answered || resp.Answer != "" {
		t.Fatalf("resp = %+v", resp)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run TestTUIUserQuestion -v`
Expected: 编译错误 `tuiUserQuestionPrompt` 未定义

- [ ] **Step 3: 实现**

`internal/cli/cli.go` options 结构 `permissionPrompt` 字段下一行：

```go
	userQuestionPrompt            func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse
```

`internal/cli/cli.go` query.Options 映射 `PermissionPrompt: opts.permissionPrompt,` 下一行：

```go
		UserQuestionPrompt:            opts.userQuestionPrompt,
```

`internal/cli/interactive.go` `streamOpts.permissionPrompt = tuiPermissionPrompt(events)` 下一行：

```go
			streamOpts.userQuestionPrompt = tuiUserQuestionPrompt(events)
```

`internal/cli/interactive.go` tuiPermissionPrompt 函数之后：

```go
func tuiUserQuestionPrompt(events chan<- tui.StreamEvent) func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
	return func(ctx context.Context, req tools.UserQuestionRequest) tools.UserQuestionResponse {
		reply := make(chan tui.UserQuestionAnswer, 1)
		event := tui.StreamEvent{
			Type: tui.StreamUserQuestion,
			Question: &tui.UserQuestionRequest{
				Question: req.Question,
				Choices:  append([]string(nil), req.Choices...),
			},
			QuestionReply: reply,
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return tools.UserQuestionResponse{}
		}
		select {
		case answer := <-reply:
			return tools.UserQuestionResponse{Answered: answer.Answered, Answer: answer.Answer}
		case <-ctx.Done():
			return tools.UserQuestionResponse{}
		}
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/cli/ -run TestTUIUserQuestion -v && go build ./...`
Expected: PASS，编译通过

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cli.go internal/cli/interactive.go internal/cli/interactive_userquestion_test.go
git commit -m "feat: TUI 模式接线 AskUserQuestion 交互回调（StreamUserQuestion 事件往返）"
```

---

### Task 5: TUI 渲染润色 — 结果/详情/兜底文案

**Files:**
- Modify: `internal/tui/app.go`（`toolInputSummary` ~591 switch、`errorResultSummary` ~1266、`friendlyToolResult` ~1501 switch）
- Test: `internal/tui/app_test.go`

**Interfaces:**
- Consumes: 结果文案约定 `User answered: <answer>` / `User input required: ...`（Task 1）

- [ ] **Step 1: 写失败测试**

```go
func TestAskUserQuestionRenderingSummaries(t *testing.T) {
	if got := toolInputSummary("AskUserQuestion", `{"question":"选哪个方案？","choices":["A"]}`); got != "选哪个方案？" {
		t.Fatalf("toolInputSummary = %q", got)
	}
	if got := errorResultSummary("User input required: 选哪个方案？\n- A"); got != "等待用户输入" {
		t.Fatalf("errorResultSummary = %q", got)
	}
	if got := friendlyToolResult("AskUserQuestion", "User answered: 方案B"); got != "用户选择：方案B" {
		t.Fatalf("friendlyToolResult = %q", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/tui/ -run TestAskUserQuestionRenderingSummaries -v`
Expected: FAIL（三处断言依次失败：JSON 原文、"操作未完成"、原文透传）

- [ ] **Step 3: 实现**

`toolInputSummary` switch（`case "AgentGet", ...` 之后、default 之前）加：

```go
	case "AskUserQuestion":
		question, _ := params["question"].(string)
		if question == "" {
			return ""
		}
		return truncateDisplay(oneLine(question), 60)
```

`errorResultSummary` switch（default 之前）加：

```go
	case strings.Contains(lower, "user input required"):
		return "等待用户输入"
```

`friendlyToolResult` 的 toolName switch（`case "todowrite", "todoread":` 之前）加：

```go
	case "askuserquestion":
		if answer, ok := strings.CutPrefix(result, "User answered: "); ok {
			return "用户选择：" + answer
		}
		return result
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/tui/ -count=1`
Expected: 全绿（注意既有 app_test.go:4119 断言"未完成：操作未完成"用的是 Bash 输出，不受影响）

- [ ] **Step 5: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go
git commit -m "feat: AskUserQuestion 的 TUI 摘要渲染（提问详情/等待用户输入/用户选择）"
```

---

### Task 6: 全量回归 + 手工冒烟

- [ ] **Step 1: 全量测试**

Run: `go test ./... -count=1`
Expected: 全部 PASS

- [ ] **Step 2: 手工冒烟（可选但推荐）**

构建后跑 TUI，让模型调用 AskUserQuestion（如输入"用 AskUserQuestion 问我今天做什么，给三个选项"），验证：弹窗出现、↑/↓/数字/Enter 作答、结果行显示"完成：用户选择：…"、Esc 取消后模型文字转述问题。

- [ ] **Step 3: push**

```bash
git push
```

## Self-Review 记录

- Spec 覆盖：四层改动、自由输入、Esc 语义、结果格式、渲染润色、测试矩阵均有对应任务 ✓
- 占位符：无 TBD/TODO，所有代码步骤含完整代码 ✓
- 类型一致性：`UserQuestionRequest/Response`（tools）、`UserQuestionRequest/Answer`（tui）、`UserQuestionPrompt`（query/cli 字段）、`StreamUserQuestion`、`questionOtherLabel` 各任务引用一致 ✓
