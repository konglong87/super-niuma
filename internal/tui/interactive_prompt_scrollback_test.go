package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// 交互卡片（AskUserQuestion / 权限）弹出时必须只占底部若干行，不能把本轮已产生的输出
// 从屏幕上抹掉。修复前 liveTranscriptView() 在 pending 非空时直接 return 卡片，
// viewport 内容和高度一起塌缩，inline 模式下上一帧被清掉。

const liveBodySentinel = "LIVE_BODY_SENTINEL"

// newStreamingModelWithLiveBody 造一个「本轮已经流出内容、还没结束」的 Model。
func newStreamingModelWithLiveBody(t *testing.T) Model {
	t.Helper()
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 38})
	model = updated.(Model)
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true
	updated, _ = model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamText, Text: liveBodySentinel},
		ch:    make(chan StreamEvent, 1),
	})
	model = updated.(Model)
	if !strings.Contains(stripANSI(model.View()), liveBodySentinel) {
		t.Fatalf("precondition: live body should be on screen before the prompt:\n%s", stripANSI(model.View()))
	}
	return model
}

func raiseQuestion(t *testing.T, model Model) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(streamEventMsg{
		event: StreamEvent{
			Type:          StreamUserQuestion,
			Question:      &UserQuestionRequest{Question: "选哪个方案？", Choices: []string{"方案A", "方案B"}},
			QuestionReply: make(chan UserQuestionAnswer, 1),
		},
		ch: make(chan StreamEvent, 1),
	})
	model = updated.(Model)
	if model.pendingQuestion == nil {
		t.Fatal("question should be pending")
	}
	return model, cmd
}

func TestQuestionPromptKeepsLiveTranscript(t *testing.T) {
	model, _ := raiseQuestion(t, newStreamingModelWithLiveBody(t))

	if live := stripANSI(model.liveTranscriptView()); !strings.Contains(live, liveBodySentinel) {
		t.Fatalf("live transcript must survive the question card, got:\n%s", live)
	}
	view := stripANSI(model.View())
	if !strings.Contains(view, liveBodySentinel) {
		t.Fatalf("this turn's output must stay on screen while the card is up:\n%s", view)
	}
	if !strings.Contains(view, "选哪个方案？") {
		t.Fatalf("the card itself must still render:\n%s", view)
	}
}

func TestQuestionPromptRendersInBottomChrome(t *testing.T) {
	model, _ := raiseQuestion(t, newStreamingModelWithLiveBody(t))

	chrome := stripANSI(strings.Join(model.bottomChromeParts(model.viewStatus(), true), "\n"))
	for _, want := range []string{"需要你的回答", "选哪个方案？", "方案A", "方案B"} {
		if !strings.Contains(chrome, want) {
			t.Fatalf("card must render in bottom chrome (missing %q):\n%s", want, chrome)
		}
	}
	// 卡片进了 chrome，就不该再占着 viewport 的内容。
	if strings.Contains(stripANSI(model.viewport.View()), "需要你的回答") {
		t.Fatalf("card must not occupy the viewport:\n%s", stripANSI(model.viewport.View()))
	}
}

func TestPermissionPromptKeepsLiveTranscript(t *testing.T) {
	model := newStreamingModelWithLiveBody(t)
	updated, _ := model.Update(streamEventMsg{
		event: StreamEvent{
			Type:       StreamPermissionRequest,
			Permission: &PermissionRequest{ToolName: "Bash", Request: "Bash:go test", Reason: "requires permission approval"},
			Reply:      make(chan PermissionDecision, 1),
		},
		ch: make(chan StreamEvent, 1),
	})
	model = updated.(Model)
	if model.pendingPermission == nil {
		t.Fatal("permission should be pending")
	}

	view := stripANSI(model.View())
	if !strings.Contains(view, liveBodySentinel) {
		t.Fatalf("permission card must not wipe this turn's output:\n%s", view)
	}
	chrome := stripANSI(strings.Join(model.bottomChromeParts(model.viewStatus(), true), "\n"))
	if !strings.Contains(chrome, "Permission request") {
		t.Fatalf("permission card must render in bottom chrome:\n%s", chrome)
	}
}

// 被挤出 viewport 的已完成内容必须进真实 scrollback，否则终端原生滚动也找不回来。
func TestInteractivePromptFlushesCompletedBlocks(t *testing.T) {
	model := newStreamingModelWithLiveBody(t)
	// 这一轮的输出已经完结（工具调用前助手文本已收尾），因此对 transcript 就绪。
	model.streamingActive = false
	if len(model.pendingTranscriptBlocks()) == 0 {
		t.Fatal("precondition: expected a completed block waiting to be flushed")
	}

	_, cmd := raiseQuestion(t, model)
	if cmd == nil {
		t.Fatal("raising a question must flush completed blocks into scrollback")
	}
	if _, ok := cmd().(transcriptFlushCommitMsg); !ok {
		t.Fatalf("expected a transcript flush command, got %T", cmd())
	}
}
