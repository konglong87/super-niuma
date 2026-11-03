package server

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/konglong87/super-niuma/internal/query"
)

func TestOpenAICompatibleServerGoOpenAIClientChatCompletion(t *testing.T) {
	seen := make(chan QueryRequest, 1)
	server := httptest.NewServer(NewHandler(Options{AuthToken: "token", Workspace: "/tmp/work"}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		seen <- req
		return query.Result{
			Response:   "Hi there",
			Model:      "claude-test",
			StopReason: "end_turn",
			Usage:      query.Usage{InputTokens: 4, OutputTokens: 2},
		}, nil
	}))
	defer server.Close()

	resp, err := newGoOpenAITestClient(server.URL).CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "gpt-test",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hi there" || resp.Choices[0].FinishReason != openai.FinishReasonStop {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Model != "claude-test" || resp.Usage.PromptTokens != 4 || resp.Usage.CompletionTokens != 2 {
		t.Fatalf("model/usage = %s %+v", resp.Model, resp.Usage)
	}
	req := <-seen
	if req.Model != "gpt-test" || req.Prompt != "Hello" || req.CWD != "/tmp/work" {
		t.Fatalf("query request = %+v", req)
	}
}

func TestOpenAICompatibleServerGoOpenAIClientToolCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler(Options{AuthToken: "token", Workspace: "/tmp/work"}, func(_ context.Context, req QueryRequest) (query.Result, error) {
		return query.Result{
			Model:      "claude-test",
			StopReason: "tool_use",
			ToolCalls: []query.ToolTrace{{
				ID:    "toolu_1",
				Name:  "Read",
				Input: `{"file_path":"README.md"}`,
			}},
		}, nil
	}))
	defer server.Close()

	resp, err := newGoOpenAITestClient(server.URL).CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "gpt-test",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "Use tool",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].FinishReason != openai.FinishReasonToolCalls {
		t.Fatalf("response = %+v", resp)
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 1 || calls[0].ID != "toolu_1" || calls[0].Type != openai.ToolTypeFunction || calls[0].Function.Name != "Read" || calls[0].Function.Arguments != `{"file_path":"README.md"}` {
		t.Fatalf("tool calls = %+v", calls)
	}
}

func TestOpenAICompatibleServerGoOpenAIClientChatCompletionStream(t *testing.T) {
	seen := make(chan QueryRequest, 1)
	server := httptest.NewServer(NewHandler(Options{
		AuthToken: "token",
		Workspace: "/tmp/work",
		StreamQueryFunc: func(_ context.Context, req QueryRequest, sink io.Writer) (query.Result, error) {
			seen <- req
			_, _ = sink.Write([]byte("H"))
			_, _ = sink.Write([]byte("i"))
			return query.Result{Model: "claude-test", StopReason: "end_turn"}, nil
		},
	}, nil))
	defer server.Close()

	stream, err := newGoOpenAITestClient(server.URL).CreateChatCompletionStream(context.Background(), openai.ChatCompletionRequest{
		Model: "gpt-test",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var got string
	var sawAssistantRole bool
	var finish openai.FinishReason
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.Choices) == 0 {
			continue
		}
		choice := resp.Choices[0]
		if choice.Delta.Role == openai.ChatMessageRoleAssistant {
			sawAssistantRole = true
		}
		got += choice.Delta.Content
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
	}
	if !sawAssistantRole || got != "Hi" || finish != openai.FinishReasonStop {
		t.Fatalf("stream role=%v content=%q finish=%q", sawAssistantRole, got, finish)
	}
	req := <-seen
	if req.Model != "gpt-test" || req.Prompt != "Hello" || req.CWD != "/tmp/work" {
		t.Fatalf("query request = %+v", req)
	}
}

func newGoOpenAITestClient(serverURL string) *openai.Client {
	config := openai.DefaultConfig("token")
	config.BaseURL = serverURL + "/v1"
	return openai.NewClientWithConfig(config)
}
