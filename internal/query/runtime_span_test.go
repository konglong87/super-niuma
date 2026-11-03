package query

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/tools"
)

type runtimeSpanStreamer struct {
	calls int
}

func (s *runtimeSpanStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	startedAt := time.Now()
	time.Sleep(2 * time.Millisecond)
	telemetry.EmitCompletedSpan(ctx, startedAt, telemetry.Event{
		Name:      telemetry.EventModelPhase + "deterministic_provider",
		Category:  telemetry.CategoryModel,
		Source:    "query.runtimeSpanStreamer",
		Status:    telemetry.StatusOK,
		Model:     req.Model,
		SessionID: req.TenantSessionID,
		Properties: map[string]any{
			"phase":  "deterministic_provider",
			"secret": "provider-secret",
		},
	})
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu-runtime",
				Name:  "SlowEcho",
				Input: json.RawMessage(`{"text":"tool-secret"}`),
			}}},
			StopReason: "tool_use",
			Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 2},
		}, nil
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: 20, OutputTokens: 3},
	}, nil
}

type slowEchoTool struct{}

func (slowEchoTool) Name() string        { return "SlowEcho" }
func (slowEchoTool) Description() string { return "echo after a deterministic delay" }
func (slowEchoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}

type runtimeSpanErrorStreamer struct{}

func (runtimeSpanErrorStreamer) StreamMessages(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	return nil, errors.New("provider-secret-error")
}
func (slowEchoTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	time.Sleep(2 * time.Millisecond)
	var params struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(input, &params)
	return tools.Result{Content: params.Text}
}

func TestQueryPersistsEndToEndNativeRuntimeSpans(t *testing.T) {
	transcriptRoot := strings.TrimSpace(os.Getenv("NIUMA_RUNTIME_SPAN_E2E_ROOT"))
	if transcriptRoot == "" {
		transcriptRoot = t.TempDir()
	}
	store := session.Store{TranscriptProjectsRoot: transcriptRoot}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "92929292-9292-4292-8292-929292929292")
	if err != nil {
		t.Fatal(err)
	}
	runtime := New(&runtimeSpanStreamer{}, tools.NewRegistry(slowEchoTool{}), Options{
		Model:    "runtime-test-model",
		MaxTurns: 3,
		CWD:      t.TempDir(),
		Recorder: recorder,
	})
	result, err := runtime.Run(context.Background(), "prompt-secret", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Turns != 2 || len(result.ToolCalls) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}

	spans := map[string]telemetry.Event{}
	spanNames := map[string]int{}
	var runtimeEntries int
	for _, entry := range entries {
		if entry.Type != session.EntryTypeRuntimeSpan {
			continue
		}
		runtimeEntries++
		for _, secret := range []string{"prompt-secret", "tool-secret", "provider-secret"} {
			if strings.Contains(entry.Content, secret) {
				t.Fatalf("runtime span leaked %q: %s", secret, entry.Content)
			}
		}
		event, err := session.DecodeRuntimeSpanEntry(entry)
		if err != nil {
			t.Fatal(err)
		}
		spans[event.SpanID] = event
		spanNames[event.Name]++
	}
	if runtimeEntries < 6 {
		t.Fatalf("runtime spans = %d, want at least query + model + tool + provider", runtimeEntries)
	}
	for _, name := range []string{
		telemetry.EventHookExecution + telemetry.SpanFinishedSuffix,
		telemetry.EventGateEvaluation + telemetry.SpanFinishedSuffix,
		telemetry.EventPersistence + telemetry.SpanFinishedSuffix,
		telemetry.EventRenderer + telemetry.SpanFinishedSuffix,
	} {
		if spanNames[name] == 0 {
			t.Fatalf("missing runtime phase %s; names=%+v", name, spanNames)
		}
	}

	var root telemetry.Event
	for _, event := range spans {
		if event.Name == telemetry.EventQueryRun+telemetry.SpanFinishedSuffix {
			root = event
			break
		}
	}
	if root.SpanID == "" || root.ParentSpanID != "" || root.DurationMS <= 0 {
		t.Fatalf("root span = %+v", root)
	}
	if root.Properties["input_bytes"] != float64(len("prompt-secret")) {
		t.Fatalf("root input_bytes = %#v", root.Properties["input_bytes"])
	}
	modelParents := map[int]string{}
	for _, event := range spans {
		if event.Name == telemetry.EventModelRequest+telemetry.SpanFinishedSuffix {
			if event.ParentSpanID != root.SpanID || event.TurnIndex < 1 || event.DurationMS <= 0 {
				t.Fatalf("model span = %+v root=%+v", event, root)
			}
			modelParents[event.TurnIndex] = event.SpanID
		}
	}
	for _, event := range spans {
		switch {
		case event.Name == telemetry.EventToolExecution+telemetry.SpanFinishedSuffix:
			if event.ParentSpanID != root.SpanID || event.TurnIndex != 1 || event.DurationMS <= 0 {
				t.Fatalf("tool span = %+v root=%+v", event, root)
			}
		case strings.HasPrefix(event.Name, telemetry.EventModelPhase):
			if event.ParentSpanID != modelParents[event.TurnIndex] || event.DurationMS <= 0 {
				t.Fatalf("provider span = %+v model parents=%+v", event, modelParents)
			}
		}
	}
	if len(MessagesFromTranscript(entries)) != 4 {
		t.Fatalf("runtime spans changed resume messages: %+v", MessagesFromTranscript(entries))
	}
}

func TestQueryPersistsSanitizedErrorRuntimeSpans(t *testing.T) {
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "94949494-9494-4494-8494-949494949494")
	if err != nil {
		t.Fatal(err)
	}
	runtime := New(runtimeSpanErrorStreamer{}, tools.NewRegistry(), Options{Model: "runtime-error-model", MaxTurns: 1, CWD: t.TempDir(), Recorder: recorder})
	if _, err := runtime.Run(context.Background(), "prompt-secret", io.Discard); err == nil {
		t.Fatal("expected provider error")
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var errorSpans int
	for _, entry := range entries {
		if entry.Type != session.EntryTypeRuntimeSpan {
			continue
		}
		if strings.Contains(entry.Content, "provider-secret-error") || strings.Contains(entry.Content, "prompt-secret") {
			t.Fatalf("error runtime span leaked sensitive text: %s", entry.Content)
		}
		event, err := session.DecodeRuntimeSpanEntry(entry)
		if err != nil {
			t.Fatal(err)
		}
		if event.Status == telemetry.StatusError {
			errorSpans++
		}
	}
	if errorSpans != 2 {
		t.Fatalf("error spans = %d, want model and query", errorSpans)
	}
}

func TestQueryPersistsAllowedAndDeniedPermissionWaitSpans(t *testing.T) {
	for _, tc := range []struct {
		name       string
		allowed    bool
		wantStatus string
	}{
		{name: "allowed", allowed: true, wantStatus: telemetry.StatusOK},
		{name: "denied", allowed: false, wantStatus: telemetry.StatusDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := session.Store{TranscriptProjectsRoot: t.TempDir()}
			recorder, err := store.NewRecorderWithID(t.TempDir(), "93939393-9393-4393-8393-939393939393")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = recorder.Close() })
			policy := permissions.FromSettings(config.PermissionSettings{Ask: []string{"SlowEcho"}, DefaultMode: permissions.ModeAsk})
			promptCalls := 0
			runtime := New(&runtimeSpanStreamer{}, tools.NewRegistry(tools.Guard(slowEchoTool{}, policy)), Options{
				Model:    "runtime-test-model",
				MaxTurns: 3,
				CWD:      t.TempDir(),
				Recorder: recorder,
				PermissionPrompt: func(context.Context, tools.PermissionPromptRequest) tools.PermissionPromptResponse {
					promptCalls++
					return tools.PermissionPromptResponse{Allowed: tc.allowed, Destination: "once", Reason: tc.name}
				},
			})
			if _, err := runtime.Run(context.Background(), "prompt-secret", io.Discard); err != nil {
				t.Fatal(err)
			}
			if promptCalls != 1 {
				t.Fatalf("permission prompt calls = %d, want 1", promptCalls)
			}
			var permission telemetry.Event
			for _, event := range loadRuntimeSpanEvents(t, recorder.Path) {
				if event.Name == telemetry.EventPermissionWait+telemetry.SpanFinishedSuffix {
					permission = event
					break
				}
			}
			if permission.SpanID == "" || permission.ParentSpanID == "" || permission.ToolName != "SlowEcho" || permission.Status != tc.wantStatus {
				t.Fatalf("permission span = %+v, want status %q with tool parent", permission, tc.wantStatus)
			}
		})
	}
}

func loadRuntimeSpanEvents(t *testing.T, transcriptPath string) []telemetry.Event {
	t.Helper()
	entries, err := session.Load(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]telemetry.Event, 0)
	for _, entry := range entries {
		if entry.Type != session.EntryTypeRuntimeSpan {
			continue
		}
		event, err := session.DecodeRuntimeSpanEntry(entry)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}
