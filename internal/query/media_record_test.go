package query

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

const testImageBase64 = "iVBORw0KGgoAAAANSUhEUg" // 够短，方便断言里直接找它

func newMediaRecorder(t *testing.T) *session.Recorder {
	t.Helper()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(t.TempDir(), "63636363-6363-4636-8636-363636363636")
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

func mcpImageMessage(payload string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: blockTypeText, Text: "Image content returned by screenshot (1 attachment(s)):"},
			{
				Type:   blockTypeImage,
				Text:   "[image content: image/png, 22 base64 chars]",
				Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: payload},
			},
		},
	}
}

// TODO-080：MCP 图片必须在会话日志里留下痕迹，且留下的不是 base64 本体。
func TestRecordMessagePersistsImageAsSidecarReference(t *testing.T) {
	recorder := newMediaRecorder(t)
	querySession := New(nil, nil, Options{Model: "test", Recorder: recorder})

	querySession.recordMessage(mcpImageMessage(testImageBase64))
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testImageBase64) {
		t.Fatalf("transcript inlined the base64 payload:\n%s", raw)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var image session.Entry
	for _, entry := range entries {
		if entry.Type == blockTypeImage {
			image = entry
		}
	}
	if image.Type != blockTypeImage {
		t.Fatalf("no image entry recorded: %+v", entries)
	}
	ref, err := session.UnmarshalMediaRef(image.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if ref.MediaType != "image/png" || ref.Base64Bytes != len(testImageBase64) {
		t.Fatalf("ref = %+v", ref)
	}
	payload, err := session.LoadMedia(ref)
	if err != nil {
		t.Fatal(err)
	}
	if payload != testImageBase64 {
		t.Fatalf("sidecar payload = %q", payload)
	}
	// 解释性文本块照旧留下，降级仍然是平滑的。
	if !strings.Contains(string(raw), "Image content returned by screenshot") {
		t.Fatalf("explanatory text was lost:\n%s", raw)
	}
}

// resume 之后图片要真的回到消息里，而不是只剩一行占位文本。
func TestMessagesFromTranscriptRehydratesImageBlocks(t *testing.T) {
	recorder := newMediaRecorder(t)
	querySession := New(nil, nil, Options{Model: "test", Recorder: recorder})
	querySession.recordMessage(mcpImageMessage(testImageBase64))
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}

	messages := MessagesFromTranscript(entries)
	var image *anthropic.ContentBlock
	for i := range messages {
		for j := range messages[i].Content {
			if messages[i].Content[j].Type == blockTypeImage {
				image = &messages[i].Content[j]
			}
		}
	}
	if image == nil {
		t.Fatalf("resume dropped the image block: %+v", messages)
	}
	if image.Source == nil || image.Source.Data != testImageBase64 || image.Source.MediaType != "image/png" {
		t.Fatalf("image block = %+v", image)
	}
	if image.Source.Type != "base64" {
		t.Fatalf("image source type = %q", image.Source.Type)
	}
}

// 侧车文件没了（会话目录被清理、跨机 resume）时降级成占位文本，且占位文本要说
// 明图片已不可用 —— 原来那句 "attached below" 在这种情况下是句谎话。
func TestMessagesFromTranscriptDegradesWhenSidecarIsMissing(t *testing.T) {
	recorder := newMediaRecorder(t)
	querySession := New(nil, nil, Options{Model: "test", Recorder: recorder})
	querySession.recordMessage(mcpImageMessage(testImageBase64))
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID)); err != nil {
		t.Fatal(err)
	}

	messages := MessagesFromTranscript(entries)
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == blockTypeImage {
				t.Fatalf("resume kept an image block with no payload: %+v", block)
			}
		}
	}
	joined := ""
	for _, message := range messages {
		for _, block := range message.Content {
			joined += block.Text
		}
	}
	if !strings.Contains(joined, "no longer available") {
		t.Fatalf("degraded text does not explain the loss: %q", joined)
	}
}

// 压缩保留下来的消息走的是 recordCompactMessage，图片同样不能在那条路径上蒸发。
func TestRecordCompactMessagePersistsImages(t *testing.T) {
	recorder := newMediaRecorder(t)
	querySession := New(nil, nil, Options{Model: "test", Recorder: recorder})

	querySession.recordCompactMessage(mcpImageMessage(testImageBase64))
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Type == blockTypeImage {
			return
		}
	}
	t.Fatalf("compact path dropped the image: %+v", entries)
}

// screenshotTool 模拟一个经 Result.ContextMessages 投递图片的 MCP 工具（TODO-061 的通路）。
type screenshotTool struct{}

func (screenshotTool) Name() string        { return "Screenshot" }
func (screenshotTool) Description() string { return "take a screenshot" }
func (screenshotTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (screenshotTool) Run(_ context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
	return tools.Result{
		Content:         "[image content attached below: image/png, 22 base64 chars]",
		ContextMessages: []anthropic.MessageParam{mcpImageMessage(testImageBase64)},
	}
}

type screenshotStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *screenshotStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  blockTypeToolUse,
				ID:    "toolu_shot",
				Name:  "Screenshot",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "I see a login page."}}},
		StopReason: "end_turn",
	}, nil
}

// 一整轮真实的图片工具调用之后 resume：图片回得来，而且不能把这一轮误判成
// 「中途被打断」。image 走 sanitizeResumeEntries 的 default 分支，那个分支会给
// 未闭合的 tool_use 补合成错误结果 —— 如果 image 行落在 tool_result 之前，
// 每次 resume 都会凭空多出一条 synthetic tool_result。
func TestResumeAfterAnImageToolTurnIsNotReportedAsInterrupted(t *testing.T) {
	recorder := newMediaRecorder(t)
	querySession := New(&screenshotStreamer{}, tools.NewRegistry(screenshotTool{}), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      t.TempDir(),
		Recorder: recorder,
	})
	if _, err := querySession.Run(context.Background(), "Take a screenshot.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	entries, format, err := session.LoadWithFormat(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ValidateResumeFormat(format); err != nil {
		t.Fatalf("image entry made the transcript unresumable: %v (format %s)", err, format)
	}
	messages, report := MessagesFromTranscriptWithReport(entries)
	if report.Interrupted || report.SyntheticToolResults != 0 {
		t.Fatalf("report = %+v", report)
	}
	found := false
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == blockTypeImage && block.Source != nil && block.Source.Data == testImageBase64 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("resume lost the tool image: %+v", messages)
	}
}

// 没有 Recorder（或图片没有载荷）时不能 panic，也不该留下空引用。
func TestRecordMessageSkipsImageWithoutPayload(t *testing.T) {
	recorder := newMediaRecorder(t)
	querySession := New(nil, nil, Options{Model: "test", Recorder: recorder})

	querySession.recordMessage(anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: blockTypeImage, Text: "[image content omitted: no data]"},
		},
	})
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Type == blockTypeImage {
			t.Fatalf("recorded an image entry with no payload: %+v", entry)
		}
	}
}
