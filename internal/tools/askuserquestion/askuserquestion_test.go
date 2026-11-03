package askuserquestion

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestAskUserQuestionRequiresInput(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"question": "Which path?", "choices": []string{"A", "B"}})
	res := New().Run(context.Background(), input, tools.Context{})
	if !res.IsError || !strings.Contains(res.Content, "Which path?") || !strings.Contains(res.Content, "- A") {
		t.Fatalf("result = %+v", res)
	}
}

func TestAskUserQuestionInteractiveAnswered(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"question": "Which path?", "choices": []string{"A", "B"}})
	var got tools.UserQuestionRequest
	res := New().Run(context.Background(), input, tools.Context{
		Invocation: tools.Invocation{ToolUseID: "ask-call-1"},
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
	if got.ToolUseID != "ask-call-1" {
		t.Fatalf("question lost original tool identity: %+v", got)
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

func TestAskUserQuestionPendingReturnsInteractionWithoutToolError(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"question": "Which path?", "choices": []string{"A", "B"}})
	res := New().Run(context.Background(), input, tools.Context{
		UserQuestion: func(_ context.Context, req tools.UserQuestionRequest) tools.UserQuestionResponse {
			if req.Question != "Which path?" || len(req.Choices) != 2 {
				t.Fatalf("request = %+v", req)
			}
			return tools.UserQuestionResponse{Pending: true, InteractionID: "interaction-1"}
		},
	})
	if res.IsError || res.Interaction == nil || res.Interaction.ID != "interaction-1" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAskUserQuestionReportsClosedInteractionWithoutPretendingToWait(t *testing.T) {
	result := New().Run(context.Background(), json.RawMessage(`{"question":"Which path?"}`), tools.Context{
		UserQuestion: func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
			return tools.UserQuestionResponse{Error: "question expired"}
		},
	})
	if !result.IsError || result.Content != "question expired" || result.Interaction != nil {
		t.Fatalf("closed interaction = %+v", result)
	}
}
