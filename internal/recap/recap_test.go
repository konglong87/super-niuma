package recap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/session"
)

func TestAppendToPathIfCurrentRejectsChangedConversationHead(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	writeRecapTestEntries(t, path, []session.Entry{
		{ID: "user-1", Type: "message", Role: "user", Content: "first"},
		{ID: "assistant-1", Type: "message", Role: "assistant", Content: "done"},
		{ID: "user-2", Type: "message", Role: "user", Content: "next turn"},
	})

	result := testRecapResult("assistant-1")
	appended, err := AppendToPathIfCurrent(context.Background(), path, result)
	if err != nil {
		t.Fatal(err)
	}
	if appended {
		t.Fatal("stale recap was appended after the conversation head changed")
	}
	loaded, err := session.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loaded {
		if entry.Type == EntryType {
			t.Fatalf("transcript contains stale recap: %+v", loaded)
		}
	}
}

func TestAppendToPathIfCurrentPersistsUnchangedConversation(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	writeRecapTestEntries(t, path, []session.Entry{
		{ID: "user-1", Type: "message", Role: "user", Content: "first"},
		{ID: "assistant-1", Type: "message", Role: "assistant", Content: "done"},
	})

	appended, err := AppendToPathIfCurrent(context.Background(), path, testRecapResult("assistant-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !appended {
		t.Fatal("current recap was not appended")
	}
	loaded, err := session.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := Latest(loaded); !ok || latest.Content != "stale recap" {
		t.Fatalf("latest recap = %+v, ok=%v", latest, ok)
	}
}

func TestAppendToPathIfCurrentHonorsCancellation(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	writeRecapTestEntries(t, path, []session.Entry{
		{ID: "assistant-1", Type: "message", Role: "assistant", Content: "done"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	appended, err := AppendToPathIfCurrent(ctx, path, testRecapResult("assistant-1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if appended {
		t.Fatal("cancelled recap was appended")
	}
	loaded, loadErr := session.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, ok := Latest(loaded); ok {
		t.Fatalf("transcript contains cancelled recap: %+v", loaded)
	}
}

func writeRecapTestEntries(t *testing.T, path string, entries []session.Entry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func testRecapResult(summarizesEntryID string) Result {
	return Result{
		Text:  "stale recap",
		Entry: session.Entry{Type: EntryType, Role: "system", Content: "stale recap"},
		Metadata: Metadata{
			SummarizesEntryID: summarizesEntryID,
		},
	}
}

type fakeStreamer struct {
	request anthropic.MessagesRequest
	text    string
	empty   bool
}

func (f *fakeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.request = req
	if f.empty {
		return &anthropic.StreamResult{}, nil
	}
	if f.text == "" {
		f.text = "正在完善 TUI recap 体验，已生成测试摘要，接下来验证渲染和存储链路。"
	}
	if cb.OnText != nil {
		_ = cb.OnText(f.text)
	}
	return &anthropic.StreamResult{}, nil
}

func TestBuildPromptIgnoresRecapAndRedactsSensitiveLines(t *testing.T) {
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "请总结这个会话"},
		{Type: EntryType, Content: "old recap"},
		{Type: "tool_result", ToolName: "Bash", Content: "ANTHROPIC_API_KEY=secret\nok"},
	}
	prompt := BuildPrompt(entries, Config{RecentMessageWindow: 30})
	if strings.Contains(prompt, "old recap") {
		t.Fatalf("prompt includes old recap:\n%s", prompt)
	}
	if strings.Contains(prompt, "secret") || !strings.Contains(prompt, "[sensitive content redacted]") {
		t.Fatalf("prompt did not redact sensitive line:\n%s", prompt)
	}
	for _, want := range []string{"Claude Code 风格的会话状态提示", "不要写成报告", "不要使用固定字段名"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing quality rule %q:\n%s", want, prompt)
		}
	}
}

func TestGenerateCreatesRecapSummaryEntry(t *testing.T) {
	streamer := &fakeStreamer{}
	result, err := Generate(context.Background(), streamer, []session.Entry{
		{ID: "msg-1", Type: "message", Role: "user", Content: "实现 recap"},
	}, Config{MaxTokens: 128}, ModeManual, "fallback-model")
	if err != nil {
		t.Fatal(err)
	}
	if result.Entry.Type != EntryType || result.Entry.Role != "system" || result.Entry.Model != "fallback-model" {
		t.Fatalf("entry = %+v", result.Entry)
	}
	if streamer.request.MaxTokens != 128 || streamer.request.Model != "fallback-model" {
		t.Fatalf("request = %+v", streamer.request)
	}
	if !strings.Contains(result.Text, "正在完善 TUI recap 体验") {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Metadata.Provenance.SourceEntryCount != 1 ||
		result.Metadata.Provenance.SourceStartID != "msg-1" ||
		result.Metadata.Provenance.SourceEndID != "msg-1" ||
		result.Metadata.Provenance.SourceDigest == "" {
		t.Fatalf("recap provenance = %+v", result.Metadata.Provenance)
	}
	if len(result.Metadata.Provenance.SourceEntryIDs) != 1 || result.Metadata.Provenance.SourceEntryIDs[0] != "msg-1" {
		t.Fatalf("recap source entry IDs = %#v", result.Metadata.Provenance.SourceEntryIDs)
	}
}

func TestGenerateFallsBackWhenModelReturnsEmptyRecap(t *testing.T) {
	streamer := &fakeStreamer{empty: true}
	result, err := Generate(context.Background(), streamer, []session.Entry{
		{ID: "msg-1", Type: "message", Role: "user", Content: "zheshi 这是测试1"},
		{ID: "msg-2", Type: "message", Role: "assistant", Content: "收到，测试1确认。有什么需要帮忙的随时说。"},
	}, Config{MaxTokens: 128}, ModeAway, "fallback-model")
	if err != nil {
		t.Fatal(err)
	}
	if result.Entry.Type != EntryType || result.Metadata.Status != "fallback_empty_response" {
		t.Fatalf("result = %+v metadata=%+v", result.Entry, result.Metadata)
	}
	for _, want := range []string{"处理“zheshi 这是测试1”", "assistant 已回复", "测试1确认", "等用户继续补充需求或任务"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("fallback recap missing %q: %s", want, result.Text)
		}
	}
	for _, notWant := range []string{"本次会话目标：", "已完成：", "下一步："} {
		if strings.Contains(result.Text, notWant) {
			t.Fatalf("fallback recap should not use template label %q: %s", notWant, result.Text)
		}
	}
}

func TestLatestFindsNewestRecap(t *testing.T) {
	entry, ok := Latest([]session.Entry{
		{Type: EntryType, Content: "old"},
		{Type: "message", Role: "user", Content: "hi"},
		{Type: EntryType, Content: "new"},
	})
	if !ok || entry.Content != "new" {
		t.Fatalf("entry=%+v ok=%v", entry, ok)
	}
}

func TestLatestStopsAtInvalidatedRecap(t *testing.T) {
	metadata, err := json.Marshal(map[string]any{"status": "invalidated"})
	if err != nil {
		t.Fatal(err)
	}
	_, ok := Latest([]session.Entry{
		{Type: EntryType, Content: "old"},
		{Type: EntryType, Content: "invalidated", Metadata: metadata},
	})
	if ok {
		t.Fatal("expected invalidated latest recap to suppress older recap")
	}
}

func TestConfigFromSettingsEnablesAwayMode(t *testing.T) {
	enabled := true
	delay := 7
	cfg := ConfigFromSettings(config.Settings{Recap: &config.RecapSettings{
		Enabled:          &enabled,
		Mode:             ModeAway,
		AwayDelaySeconds: &delay,
	}}, "default-model")
	if !cfg.AwayEnabled("default-model") || cfg.PostTurnEnabled("default-model") {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.AwayDelaySeconds != delay {
		t.Fatalf("away delay = %d", cfg.AwayDelaySeconds)
	}
}
