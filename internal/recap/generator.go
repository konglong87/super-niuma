package recap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/session"
)

type Streamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

type Result struct {
	Text     string
	Entry    session.Entry
	Metadata Metadata
}

type Metadata struct {
	Version             int                       `json:"version"`
	Source              string                    `json:"source"`
	SummarizesEntryID   string                    `json:"summarizes_entry_id,omitempty"`
	RecentMessageWindow int                       `json:"recent_message_window"`
	DurationMS          int64                     `json:"duration_ms"`
	Status              string                    `json:"status"`
	CreatedAt           time.Time                 `json:"created_at"`
	Provenance          session.SummaryProvenance `json:"provenance"`
}

func Generate(ctx context.Context, streamer Streamer, entries []session.Entry, cfg Config, source, defaultModel string) (Result, error) {
	if streamer == nil {
		return Result{}, fmt.Errorf("recap streamer is nil")
	}
	cfg = cfg.WithDefaults(defaultModel)
	source = strings.TrimSpace(source)
	if source == "" {
		source = ModeManual
	}
	start := time.Now()
	prompt := BuildPrompt(entries, cfg)
	if strings.TrimSpace(prompt) == "" {
		return Result{}, fmt.Errorf("recap prompt is empty")
	}
	var streamed strings.Builder
	res, err := streamer.StreamMessages(ctx, anthropic.MessagesRequest{
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: prompt}},
		}},
	}, anthropic.StreamCallbacks{
		OnText: func(text string) error {
			streamed.WriteString(text)
			return nil
		},
	})
	if err != nil {
		return Result{}, err
	}
	text := strings.TrimSpace(streamed.String())
	if text == "" && res != nil {
		for _, block := range res.Message.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
		text = strings.TrimSpace(text)
	}
	fallbackUsed := false
	if text == "" {
		text = fallbackRecapText(entries)
		fallbackUsed = text != ""
	}
	if text == "" {
		return Result{}, fmt.Errorf("recap response is empty")
	}
	text = sanitizeRecapText(text)
	status := "ok"
	if fallbackUsed {
		status = "fallback_empty_response"
	}
	contextEntries := ContextEntries(entries, cfg)
	metadata := Metadata{
		Version:             1,
		Source:              source,
		SummarizesEntryID:   latestConversationEntryID(entries),
		RecentMessageWindow: cfg.RecentMessageWindow,
		DurationMS:          time.Since(start).Milliseconds(),
		Status:              status,
		CreatedAt:           time.Now().UTC(),
		Provenance:          session.NewSummaryProvenance(contextEntries, contextEntries),
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return Result{}, err
	}
	entry := session.Entry{
		Type:      EntryType,
		Role:      "system",
		Content:   text,
		Model:     cfg.Model,
		Metadata:  raw,
		Timestamp: time.Now().UTC(),
	}
	return Result{Text: text, Entry: entry, Metadata: metadata}, nil
}

func fallbackRecapText(entries []session.Entry) string {
	contextEntries := ContextEntries(entries, Config{RecentMessageWindow: DefaultRecentMessageWindow})
	if len(contextEntries) == 0 {
		return ""
	}
	var lastUser, lastAssistant string
	for _, entry := range contextEntries {
		if entry.Type != "message" {
			continue
		}
		switch entry.Role {
		case "user":
			lastUser = strings.TrimSpace(entry.Content)
		case "assistant":
			lastAssistant = strings.TrimSpace(entry.Content)
		}
	}
	lastUser = truncateMiddle(redactSensitive(lastUser), 120)
	lastAssistant = truncateMiddle(redactSensitive(lastAssistant), 120)
	if lastUser == "" && lastAssistant == "" {
		return ""
	}
	topic := "继续当前 TUI 会话"
	if lastUser != "" {
		topic = "处理“" + lastUser + "”"
	}
	progress := "已记录最近一轮对话"
	if lastAssistant != "" {
		progress = "assistant 已回复“" + lastAssistant + "”"
	}
	return topic + "，" + progress + "；当前没有明确后续动作，等用户继续补充需求或任务。"
}

func AppendResult(recorder *session.Recorder, result Result) error {
	if recorder == nil || recorder.Path == "" {
		return fmt.Errorf("recap has no active session")
	}
	return recorder.Append(result.Entry)
}

func AppendToPath(path string, result Result) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("recap has no active session")
	}
	entry := result.Entry
	if entry.ID == "" {
		entry.ID = session.NewEntryID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	// Tag as v2 when the transcript is a message graph so the file never reads as
	// schema-mixed. Recap is a session-wide UI artifact, not a conversation node,
	// so it is intentionally NOT chained (no parent_id) and its consumers locate it
	// across the whole file rather than on the current chain.
	return session.AppendEntryToPath(path, entry)
}

// AppendToPathIfCurrent persists an asynchronously generated recap only while
// the conversation still ends at the entry captured by Generate. Cancellation
// covers in-process user activity; the head check also catches external writes,
// resume/rewind changes, and another producer updating the same transcript.
func AppendToPathIfCurrent(ctx context.Context, path string, result Result) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	entry := result.Entry
	if entry.ID == "" {
		entry.ID = session.NewEntryID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	appended, err := session.AppendEntryToPathIfCurrent(path, result.Metadata.SummarizesEntryID, entry)
	if err != nil {
		return false, err
	}
	return appended, nil
}

func latestConversationEntryID(entries []session.Entry) string {
	return session.LatestConversationEntryID(entries)
}

func sanitizeRecapText(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(strings.ToLower(text), "※ recap:") {
		text = strings.TrimSpace(text[len("※ recap:"):])
	}
	return redactSensitive(text)
}
