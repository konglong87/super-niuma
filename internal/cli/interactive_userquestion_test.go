package cli

import (
	"context"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tui"
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
