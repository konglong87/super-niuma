package query

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

// failingStreamer 复现 provider 侧硬失败（网关卡死、超时守卫触发）。
type failingStreamer struct {
	err error
}

func (s *failingStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	return nil, s.err
}

// provider 报错必须落进 transcript，否则事后完全无法定位：会话文件里只剩一个
// prompt_context 后面接空白，看不出这一轮到底失败了还是被用户打断了。
func TestProviderErrorIsRecordedInTranscript(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	store := session.Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	rec, err := store.NewRecorderWithID(t.TempDir(), "faceb00c-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}

	streamFailure := errors.New(`provider sent no response headers after 2m0s: Post "https://gateway.example/v1/chat/completions": context canceled`)
	s := New(&failingStreamer{err: streamFailure}, tools.NewRegistry(), Options{
		Model:    "glm-5.1",
		CWD:      t.TempDir(),
		Recorder: rec,
	})
	if _, err := s.Run(context.Background(), "hello", io.Discard); err == nil {
		t.Fatal("expected Run to surface the provider failure")
	}

	entries, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	var found *session.Entry
	for i := range entries {
		if entries[i].Type == "provider_error" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no provider_error entry in transcript; got types %v", entryTypes(entries))
	}
	if !found.IsError {
		t.Error("provider_error entry should be flagged IsError")
	}
	if !strings.Contains(found.Content, "provider sent no response headers") {
		t.Errorf("entry content = %q, want the provider error text", found.Content)
	}
	if found.Model != "glm-5.1" {
		t.Errorf("entry model = %q, want the model that failed", found.Model)
	}
}

// resume 必须把 provider_error 当作纯诊断记录跳过：它既不是模型上下文，
// 也不能参与 tool_call/tool_result 配对修复。
func TestProviderErrorIsNotResumeContext(t *testing.T) {
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "hello"},
		{Type: "provider_error", Content: "provider sent no response headers after 2m0s", IsError: true},
	}
	sanitized, report := sanitizeResumeEntriesWithReport(entries)
	if report.Interrupted {
		t.Error("a provider_error entry must not be read as an interrupted tool turn")
	}
	for _, entry := range sanitized {
		if entry.Type == "provider_error" {
			t.Fatal("provider_error must be dropped from resume context")
		}
	}
}

func entryTypes(entries []session.Entry) []string {
	types := make([]string, 0, len(entries))
	for _, entry := range entries {
		types = append(types, entry.Type)
	}
	return types
}
