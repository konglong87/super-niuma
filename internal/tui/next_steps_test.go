package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func nextStepsEvent(suggestions ...string) StreamEvent {
	payload, err := json.Marshal(suggestions)
	if err != nil {
		panic(err)
	}
	return StreamEvent{Type: StreamNextSteps, Payload: payload}
}

func TestApplyNextStepsShowsSuggestionsOnEmptyInput(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("跑一遍测试", "补单元测试"))
	if !model.nextStepsActive() {
		t.Fatal("nextStepsActive() = false, want true on an empty input box")
	}
	view := model.nextStepsView()
	for _, want := range []string{"跑一遍测试", "补单元测试", "1", "2"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n---\n%s", want, view)
		}
	}
}

// 候选是异步到的，用户可能已经在敲字。抢占输入框会毁掉正常输入。
func TestNextStepsHiddenWhileUserIsTyping(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("跑一遍测试"))
	model.setTextareaValue("我自己想问的问题")
	if model.nextStepsActive() {
		t.Error("nextStepsActive() = true, want false while the input box has text")
	}
	if view := model.nextStepsView(); view != "" {
		t.Errorf("nextStepsView() = %q, want empty while typing", view)
	}
}

// 清空输入框后候选回来——隐藏是展示条件，不是销毁。
func TestNextStepsReturnAfterInputCleared(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("跑一遍测试"))
	model.setTextareaValue("x")
	model.setTextareaValue("")
	if !model.nextStepsActive() {
		t.Error("nextStepsActive() = false, want true after the input box was cleared")
	}
}

func TestNextStepsHiddenWhileBusyOrPrompting(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("跑一遍测试"))

	model.busy = true
	if model.nextStepsActive() {
		t.Error("nextStepsActive() = true while busy, want false")
	}
	model.busy = false

	model.slashSuggestions = []SlashCommand{{Name: "/help"}}
	if model.nextStepsActive() {
		t.Error("nextStepsActive() = true while slash suggestions are open, want false")
	}
	model.slashSuggestions = nil

	model.pendingQuestion = &pendingUserQuestion{}
	if model.nextStepsActive() {
		t.Error("nextStepsActive() = true while a question card is open, want false")
	}
}

func TestApplyNextStepSuggestionFillsInputAndDismisses(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("跑一遍测试", "补单元测试"))
	if !model.applyNextStepSuggestion(1) {
		t.Fatal("applyNextStepSuggestion(1) = false, want true")
	}
	if got := model.textarea.Value(); got != "补单元测试" {
		t.Errorf("input = %q, want 补单元测试", got)
	}
	if model.nextStepsActive() {
		t.Error("panel still active after applying a suggestion")
	}
}

func TestApplyNextStepSuggestionRejectsOutOfRange(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("跑一遍测试"))
	for _, idx := range []int{-1, 1, 9} {
		if model.applyNextStepSuggestion(idx) {
			t.Errorf("applyNextStepSuggestion(%d) = true, want false", idx)
		}
	}
	if got := model.textarea.Value(); got != "" {
		t.Errorf("input = %q, want it untouched", got)
	}
}

func TestApplyNextStepsIgnoresUnusablePayload(t *testing.T) {
	for _, event := range []StreamEvent{
		{Type: StreamNextSteps},
		{Type: StreamNextSteps, Payload: json.RawMessage(`not json`)},
		{Type: StreamNextSteps, Payload: json.RawMessage(`[]`)},
		{Type: StreamNextSteps, Payload: json.RawMessage(`["", "   "]`)},
	} {
		model := NewModel(context.Background(), Options{})
		model.applyNextSteps(event)
		if model.nextStepsActive() {
			t.Errorf("payload %q produced an active panel", string(event.Payload))
		}
	}
}

// 新一轮开始收尾时清掉上一轮候选，否则本轮没产出建议时会显示过期内容。
func TestStreamFinishedClearsPreviousSuggestions(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyNextSteps(nextStepsEvent("上一轮的建议"))
	model.applyStreamFinishedEvent(StreamEvent{Type: StreamFinished})
	if model.nextStepsActive() {
		t.Error("stale suggestions survived the next turn's completion")
	}
}

// runNextStepsCmd 必须自己持有 channel：主 stream channel 在 StreamFinished
// 后已被关闭且不再被读取，异步结果送不进去，往已关闭的 channel 发送还会 panic。
func TestRunNextStepsCmdDeliversSuggestionsThroughItsOwnChannel(t *testing.T) {
	var gotPrompt string
	var gotResponse string
	model := NewModel(context.Background(), Options{
		RunNextSteps: func(_ context.Context, prompt string, result QueryResult, events chan<- StreamEvent) error {
			gotPrompt = prompt
			gotResponse = result.Response
			payload, err := json.Marshal([]string{"跑一遍测试", "补单元测试"})
			if err != nil {
				return err
			}
			events <- StreamEvent{Type: StreamNextSteps, Payload: payload}
			return nil
		},
	})
	cmd := model.runNextStepsCmd("改 parse.go", QueryResult{Response: "改好了"})
	if cmd == nil {
		t.Fatal("runNextStepsCmd returned nil with a runner configured")
	}
	msg, ok := cmd().(nextStepsMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want nextStepsMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if gotPrompt != "改 parse.go" || gotResponse != "改好了" {
		t.Fatalf("runner saw prompt=%q response=%q", gotPrompt, gotResponse)
	}
	updated, _ := model.Update(msg)
	model = updated.(Model)
	if !model.nextStepsActive() {
		t.Fatal("suggestions did not reach the panel")
	}
}

func TestRunNextStepsCmdIsNilWithoutRunner(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	if cmd := model.runNextStepsCmd("p", QueryResult{Response: "r"}); cmd != nil {
		t.Error("runNextStepsCmd returned a cmd with no runner configured")
	}
}

// 生成失败必须静默：一条错误的引导比没有引导更糟，也不该弹错误行打扰用户。
func TestNextStepsFailureStaysSilent(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunNextSteps: func(_ context.Context, _ string, _ QueryResult, _ chan<- StreamEvent) error {
			return errors.New("provider down")
		},
	})
	msg := model.runNextStepsCmd("p", QueryResult{Response: "r"})().(nextStepsMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)
	if model.nextStepsActive() {
		t.Error("panel active after a failed generation")
	}
	if model.err != nil {
		t.Errorf("err = %v, want nil — failures must stay silent", model.err)
	}
}

// execAllCmds runs cmd and, recursively, every sub-command of any tea.BatchMsg
// it produces. Update wraps commands in tea.Batch only when more than one
// survives compactCmds's nil filtering, so the trigger cmd may arrive either
// bare or batched alongside the transcript-flush cmd — this handles both.
func execAllCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, sub := range batch {
			msgs = append(msgs, execAllCmds(sub)...)
		}
		return msgs
	}
	return []tea.Msg{msg}
}

// 通过 Update 而不是直接调用 cmd 驱动 StreamFinished 分支，钉住三件事同时成立：
// 一轮正常结束会触发候选请求，prompt 取自 lastTurnPrompt，result 取自
// applyStreamFinishedEvent 清空前的 pendingStreamResult——三者中任何一个被悄悄
// 删掉（例如误删 msg.event.Err == nil 这个 gate），这个测试都会失败。
func TestStreamFinishedTriggersNextStepsWithCapturedPromptAndResult(t *testing.T) {
	var invoked bool
	var gotPrompt string
	var gotResponse string
	model := NewModel(context.Background(), Options{
		RunNextSteps: func(_ context.Context, prompt string, result QueryResult, _ chan<- StreamEvent) error {
			invoked = true
			gotPrompt = prompt
			gotResponse = result.Response
			return nil
		},
	})
	model.lastTurnPrompt = "改 parse.go"
	model.pendingStreamResult = QueryResult{Response: "改好了"}

	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}})
	model = updated.(Model)
	execAllCmds(cmd)

	if !invoked {
		t.Fatal("a successful StreamFinished did not trigger next-steps generation")
	}
	if gotPrompt != "改 parse.go" {
		t.Errorf("prompt = %q, want lastTurnPrompt %q", gotPrompt, "改 parse.go")
	}
	if gotResponse != "改好了" {
		t.Errorf("response = %q, want the pre-clear pendingStreamResult %q", gotResponse, "改好了")
	}
}

// 失败/取消的一轮没有可引导的下一步——不该发起候选请求。
func TestStreamFinishedWithErrDoesNotTriggerNextSteps(t *testing.T) {
	var invoked bool
	model := NewModel(context.Background(), Options{
		RunNextSteps: func(_ context.Context, _ string, _ QueryResult, _ chan<- StreamEvent) error {
			invoked = true
			return nil
		},
	})
	model.lastTurnPrompt = "p"
	model.pendingStreamResult = QueryResult{Response: "r"}

	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished, Err: errors.New("boom")}})
	model = updated.(Model)
	execAllCmds(cmd)

	if invoked {
		t.Error("a failed StreamFinished triggered next-steps generation")
	}
}

// 与 TestStreamFinishedTriggersNextStepsWithCapturedPromptAndResult 对称：
// StreamFinished 分支批处理了两条 cmd（next-steps 和 post-turn recap），这条
// 测试钉住 recap 那一半——删掉 update.go 里 `if recapCmd := m.runPostTurnRecapCmd()`
// 这三行，整个套件仍会全绿（RunPostTurnRecap 就再也不会被调用），因为
// runPostTurnRecapCmd/collectRecap 自身的单元测试并不经过 StreamFinished 分支。
func TestStreamFinishedTriggersPostTurnRecap(t *testing.T) {
	var invoked bool
	model := NewModel(context.Background(), Options{
		RunPostTurnRecap: func(_ context.Context, _ chan<- StreamEvent) error {
			invoked = true
			return nil
		},
	})
	model.lastTurnPrompt = "p"
	model.pendingStreamResult = QueryResult{Response: "r"}

	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}})
	model = updated.(Model)
	execAllCmds(cmd)

	if !invoked {
		t.Fatal("a successful StreamFinished did not trigger post-turn recap generation")
	}
}

// Regression: slash-handled turns (e.g. /help, /model) also reach
// StreamFinished with Err == nil, but never produce an assistant response —
// they never emit a StreamUsage event, so pendingStreamResult stays the
// zero-value QueryResult{} set at turn start. Before this turn had a result
// gate, enabling post-turn recap made every slash command pay for a provider
// call plus a session-file recap entry, which the old call site (after
// RunWithCallbacks) never did because slash-handled turns returned earlier.
func TestStreamFinishedWithNoResultDoesNotTriggerPostTurnRecap(t *testing.T) {
	var invoked bool
	model := NewModel(context.Background(), Options{
		RunPostTurnRecap: func(_ context.Context, _ chan<- StreamEvent) error {
			invoked = true
			return nil
		},
	})
	model.lastTurnPrompt = "/help"
	// pendingStreamResult left at its zero value: no StreamUsage event ever
	// arrived for this (slash-handled) turn.

	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished}})
	model = updated.(Model)
	execAllCmds(cmd)

	if invoked {
		t.Error("a StreamFinished with no turn result triggered post-turn recap generation, want skipped")
	}
}

// post-turn recap 必须由 TUI 的 cmd 驱动、cmd 自己持有 channel。
// 原实现把生产者的 channel 交给一个异步 goroutine，而那个 channel 在结果到达
// 前就被 defer close 关闭了——向已关闭 channel 发送会 panic。
func TestRunPostTurnRecapCmdDeliversRecapThroughItsOwnChannel(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunPostTurnRecap: func(_ context.Context, events chan<- StreamEvent) error {
			events <- StreamEvent{Type: StreamRecap, Text: "本次会话目标：修 recap"}
			return nil
		},
	})
	cmd := model.runPostTurnRecapCmd()
	if cmd == nil {
		t.Fatal("runPostTurnRecapCmd returned nil with a runner configured")
	}
	msg, ok := cmd().(postTurnRecapMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want postTurnRecapMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if msg.text != "本次会话目标：修 recap" {
		t.Fatalf("text = %q, want the generated recap", msg.text)
	}
	updated, _ := model.Update(msg)
	model = updated.(Model)
	if _, ok := model.latestLiveRecapMessage(); !ok {
		t.Fatal("recap did not reach the model")
	}
}

func TestRunPostTurnRecapCmdIsNilWithoutRunner(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	if cmd := model.runPostTurnRecapCmd(); cmd != nil {
		t.Error("runPostTurnRecapCmd returned a cmd with no runner configured")
	}
}

// 生成失败不该打断会话。
func TestPostTurnRecapFailureDoesNotCreateRecap(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunPostTurnRecap: func(_ context.Context, _ chan<- StreamEvent) error {
			return errors.New("provider down")
		},
	})
	msg := model.runPostTurnRecapCmd()().(postTurnRecapMsg)
	updated, _ := model.Update(msg)
	model = updated.(Model)
	if _, ok := model.latestLiveRecapMessage(); ok {
		t.Error("a failed recap generation created a recap message")
	}
}
