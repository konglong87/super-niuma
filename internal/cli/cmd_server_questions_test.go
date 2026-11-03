package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

type serverQuestionSinkFake struct {
	err error
	got tools.UserQuestionRequest
}

func (s *serverQuestionSinkFake) OnUserQuestion(_ context.Context, req tools.UserQuestionRequest) (tools.UserQuestionResponse, error) {
	s.got = req
	return tools.UserQuestionResponse{Answered: s.err == nil, Answer: "Architecture"}, s.err
}

func TestServerQuestionWiringForwardsAnswerAndFailure(t *testing.T) {
	for _, cause := range []error{nil, errors.New("question expired")} {
		sink := &serverQuestionSinkFake{err: cause}
		opts := options{}
		applyServerUserQuestionPrompt(&opts, sink)
		if opts.userQuestionPrompt == nil {
			t.Fatal("server question callback missing")
		}
		answer := opts.userQuestionPrompt(context.Background(), tools.UserQuestionRequest{Question: "Diagram?", Choices: []string{"Architecture"}})
		if sink.got.Question != "Diagram?" {
			t.Fatal("question not forwarded")
		}
		if cause == nil && (!answer.Answered || answer.Answer != "Architecture") {
			t.Fatalf("answer=%+v", answer)
		}
		if cause != nil && (answer.Answered || answer.Error != "User question could not be answered: "+cause.Error()) {
			t.Fatalf("failure=%+v", answer)
		}
	}
}
