package nextsteps

import (
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type fakeStreamer struct {
	streamed string
	blocks   []anthropic.ContentBlock
	err      error
	gotReq   anthropic.MessagesRequest
	calls    int
}

func (f *fakeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	f.gotReq = req
	if f.err != nil {
		return nil, f.err
	}
	if f.streamed != "" && cb.OnText != nil {
		if err := cb.OnText(f.streamed); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Content: f.blocks}}, nil
}

func tailInput() Input {
	return Input{UserPrompt: "改 parse.go", AssistantResponse: "改好了", ToolNames: []string{"Edit"}}
}

func TestGenerateReturnsParsedSuggestions(t *testing.T) {
	streamer := &fakeStreamer{streamed: "跑测试\n补文档\n提交"}
	got, err := Generate(context.Background(), streamer, tailInput(), Config{Enabled: true, Model: "m", Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "跑测试" {
		t.Fatalf("got %q, want 3 parsed suggestions", got)
	}
}

func TestGeneratePassesModelAndTokenCapToProvider(t *testing.T) {
	streamer := &fakeStreamer{streamed: "跑测试"}
	if _, err := Generate(context.Background(), streamer, tailInput(), Config{Enabled: true, Model: "tiny-model", Count: 1}); err != nil {
		t.Fatal(err)
	}
	if streamer.gotReq.Model != "tiny-model" {
		t.Errorf("Model = %q, want tiny-model", streamer.gotReq.Model)
	}
	if streamer.gotReq.MaxTokens != DefaultMaxTokens {
		t.Errorf("MaxTokens = %d, want %d", streamer.gotReq.MaxTokens, DefaultMaxTokens)
	}
}

// 流式回调没拿到文本时，回扫最终消息的 text block。
func TestGenerateFallsBackToFinalContentBlocks(t *testing.T) {
	streamer := &fakeStreamer{blocks: []anthropic.ContentBlock{{Type: "text", Text: "跑测试\n提交"}}}
	got, err := Generate(context.Background(), streamer, tailInput(), Config{Enabled: true, Model: "m", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %q, want 2 suggestions from content blocks", got)
	}
}

// 空输出不是错误：静默不展示优于给一条错误引导。
func TestGenerateReturnsNoErrorForEmptyOutput(t *testing.T) {
	got, err := Generate(context.Background(), &fakeStreamer{}, tailInput(), Config{Enabled: true, Model: "m", Count: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestGeneratePropagatesProviderError(t *testing.T) {
	want := errors.New("provider down")
	if _, err := Generate(context.Background(), &fakeStreamer{err: want}, tailInput(), Config{Enabled: true, Model: "m", Count: 3}); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// 关闭时、没有 assistant 回复时、streamer 为 nil 时，一次请求都不该发出。
func TestGenerateSkipsRequestWhenThereIsNothingToDo(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		cfg  Config
	}{
		{"disabled", tailInput(), Config{Enabled: false, Model: "m", Count: 3}},
		{"no response", Input{UserPrompt: "只有提问"}, Config{Enabled: true, Model: "m", Count: 3}},
	}
	for _, tc := range cases {
		streamer := &fakeStreamer{streamed: "跑测试"}
		got, err := Generate(context.Background(), streamer, tc.in, tc.cfg)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if len(got) != 0 {
			t.Errorf("%s: got %q, want empty", tc.name, got)
		}
		if streamer.calls != 0 {
			t.Errorf("%s: made %d provider calls, want 0", tc.name, streamer.calls)
		}
	}
	if _, err := Generate(context.Background(), nil, tailInput(), Config{Enabled: true, Model: "m", Count: 3}); err == nil {
		t.Error("nil streamer: want an error")
	}
}
