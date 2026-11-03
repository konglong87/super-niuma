package query

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

const computerPrivacySecret = "private-input-6ce38a"
const computerPrivacyInput = `{"action":"type","session_id":"private-session","text":"private-input-6ce38a","key":"private-key","keys":["private-key-array"],"unknown":"private-unknown-field"}`

// The image is a complete PNG, not a marker pretending to be image data.
var computerPrivacyPNG = func() string {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}()

type computerPrivacyTool struct {
	name  string
	input json.RawMessage
	fail  bool
}

func (t *computerPrivacyTool) Name() string      { return t.name }
func (*computerPrivacyTool) Description() string { return "privacy boundary fixture" }
func (*computerPrivacyTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *computerPrivacyTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	t.input = append(json.RawMessage(nil), input...)
	return tools.Result{Content: `{"receipt":{"outcome":"outcome_unknown","after_observation_id":"after-1"}}`, IsError: t.fail,
		ContextMessages: []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: computerPrivacyPNG}}}}}}
}

type computerPrivacyStreamer struct {
	name  string
	calls int
	next  []anthropic.MessageParam
}

func (s *computerPrivacyStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeToolUse, ID: "computer-call", Name: s.name, Input: json.RawMessage(computerPrivacyInput)}}}, StopReason: "tool_use"}, nil
	}
	s.next = req.Messages
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "done"}}}, StopReason: "end_turn"}, nil
}
func assertComputerPrivacy(t *testing.T, label string, value any) {
	t.Helper()
	var data []byte
	switch v := value.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, secret := range []string{computerPrivacySecret, "private-session", "private-key", "private-key-array", "private-unknown-field", "hook-secret-rewrite"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("%s exposed %q: %s", label, secret, data)
		}
	}
}
func newComputerPrivacyRecorder(t *testing.T, v2 bool) *session.Recorder {
	t.Helper()
	recorder, err := (session.Store{Root: t.TempDir(), SchemaV2: v2}).NewRecorderWithID(t.TempDir(), "64646464-6464-4646-8646-646464646464")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	return recorder
}
func assertComputerPrivacyFiles(t *testing.T, recorder *session.Recorder) {
	t.Helper()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(filepath.Dir(recorder.Path), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assertComputerPrivacy(t, path, data)
		if strings.HasSuffix(path, ".b64") || strings.Contains(string(data), computerPrivacyPNG) {
			t.Errorf("computer image persisted in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestComputerPrivacyCallbacksRecordersAndFailedImage(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%v", v2), func(t *testing.T) {
			recorder := newComputerPrivacyRecorder(t, v2)
			tool := &computerPrivacyTool{name: "ComputerUse", fail: true}
			streamer := &computerPrivacyStreamer{name: tool.Name()}
			s := New(streamer, tools.NewRegistry(tool), Options{Model: "test", MaxTurns: 2, CWD: t.TempDir(), Recorder: recorder})
			result, err := s.RunWithCallbacks(context.Background(), "perform action", io.Discard, RunCallbacks{
				OnToolCall:   func(event ToolCallEvent) error { assertComputerPrivacy(t, "call callback", event); return nil },
				OnToolResult: func(trace ToolTrace) error { assertComputerPrivacy(t, "result callback", trace); return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if string(tool.input) != computerPrivacyInput {
				t.Fatalf("execution input changed: %s", tool.input)
			}
			assertComputerPrivacy(t, "result", result)
			// Live arguments are already known to this model and must remain canonical
			// in this query's ephemeral protocol. All persistence stays redacted.
			if len(streamer.next) == 0 {
				t.Fatal("live model history missing")
			}
			assertComputerPrivacy(t, "summary boundary", s.computerAuditHistory(streamer.next))
			imageFound := false
			for _, message := range streamer.next {
				for _, block := range message.Content {
					if block.Type == blockTypeImage && block.Source != nil && block.Source.Data == computerPrivacyPNG {
						imageFound = true
					}
				}
			}
			if !imageFound {
				t.Error("failed computer after image missing from next model turn")
			}
			// Compaction can re-record retained messages; its image path must also opt out.
			for _, message := range streamer.next {
				s.recordCompactMessage(message)
			}
			assertComputerPrivacyFiles(t, recorder)
		})
	}
}
func TestComputerPrivacyStreamJSONAndHookRewriteIsolation(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	post := filepath.Join(dir, "post.json")
	tool := &computerPrivacyTool{name: "ComputerUse"}
	streamer := &computerPrivacyStreamer{name: tool.Name()}
	s := New(streamer, tools.NewRegistry(tool), Options{Model: "test", MaxTurns: 2, CWD: dir, IncludeHookEvents: true, Hooks: hooks.New(map[string][]config.HookCommand{
		hooks.PreToolUse: {
			{Command: fmt.Sprintf(`cat > %q; printf '%%s' '{"updatedInput":{"text":"hook-secret-rewrite"},"additionalContext":"hook-secret-rewrite"}'`, first)},
			{Command: fmt.Sprintf(`cat > %q`, second)},
		},
		hooks.PostToolUse: {{Command: fmt.Sprintf(`cat > %q`, post)}},
	})})
	var output strings.Builder
	if err := s.RunStreamJSON(context.Background(), "perform action", &output); err != nil {
		t.Fatal(err)
	}
	assertComputerPrivacy(t, "streamJSON", output.String())
	if string(tool.input) != computerPrivacyInput {
		t.Errorf("hook changed execution input: %s", tool.input)
	}
	for _, path := range []string{first, second, post} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		assertComputerPrivacy(t, path, data)
	}
}
func TestComputerPrivacyOrdinaryToolFailureStillDropsContext(t *testing.T) {
	tool := &computerPrivacyTool{name: "OrdinaryTool", fail: true}
	streamer := &computerPrivacyStreamer{name: tool.Name()}
	s := New(streamer, tools.NewRegistry(tool), Options{Model: "test", MaxTurns: 2, CWD: t.TempDir()})
	result, err := s.Run(context.Background(), "perform action", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Input != computerPrivacyInput {
		t.Fatalf("ordinary tool changed: %+v", result)
	}
	for _, m := range streamer.next {
		for _, b := range m.Content {
			if b.Type == blockTypeImage {
				t.Fatal("ordinary failed tool got new image behavior")
			}
		}
	}
}
func TestComputerPrivacyFixturePNG(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(computerPrivacyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}

func TestComputerPrivacyRecorderRejectsUnknownModelStrings(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%v", v2), func(t *testing.T) {
			recorder := newComputerPrivacyRecorder(t, v2)
			s := New(nil, nil, Options{Model: "test", Recorder: recorder})
			for _, input := range []string{computerPrivacyInput, `{"action":"private-unknown-field","keys":["private-key-array"]}`, `{"text":"private-input-6ce38a"`} {
				message := anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeToolUse, Name: "ComputerUse", ID: "call", Input: json.RawMessage(input)}}}
				s.recordAssistant(message.Content)
				s.recordCompactMessage(message)
			}
			assertComputerPrivacyFiles(t, recorder)
		})
	}
}
func TestComputerPrivacyCancelledAndBlockedTraces(t *testing.T) {
	block := anthropic.ContentBlock{Name: "ComputerUse", ID: "call", Input: json.RawMessage(computerPrivacyInput)}
	assertComputerPrivacy(t, "blocked", blockedToolTrace(block, "blocked"))
	assertComputerPrivacy(t, "cancelled", cancelledToolTrace(block, context.Canceled))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := &computerPrivacyTool{name: "ComputerUse"}
	s := New(nil, tools.NewRegistry(tool), Options{Model: "test"})
	trace := s.runTool(ctx, s.registry, block, runCallbacks{})
	assertComputerPrivacy(t, "early cancellation", trace)
	if !trace.IsError || tool.input != nil {
		t.Fatal("cancelled input ran")
	}
}
func TestComputerPrivacyPostFailureHookAndHookError(t *testing.T) {
	for _, failTool := range []bool{false, true} {
		t.Run(fmt.Sprintf("toolError=%v", failTool), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "payload.json")
			event := hooks.PostToolUse
			if failTool {
				event = hooks.PostToolUseFailure
			}
			tool := &computerPrivacyTool{name: "ComputerUse", fail: failTool}
			streamer := &computerPrivacyStreamer{name: tool.Name()}
			s := New(streamer, tools.NewRegistry(tool), Options{Model: "test", MaxTurns: 2, CWD: dir, IncludeHookEvents: true, Hooks: hooks.New(map[string][]config.HookCommand{
				event: {{Command: fmt.Sprintf(`cat > %q; printf 'hook-secret-rewrite'; exit 1`, path)}},
			})})
			var output strings.Builder
			if err := s.RunStreamJSON(context.Background(), "perform action", &output); err != nil {
				t.Fatal(err)
			}
			assertComputerPrivacy(t, "hook error stream", output.String())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertComputerPrivacy(t, "post hook stdin", data)
			var payload hooks.Payload
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.IsError != failTool {
				t.Fatalf("wrong post event: %+v", payload)
			}
			found := false
			for _, message := range streamer.next {
				for _, block := range message.Content {
					if block.Type == blockTypeImage {
						found = true
					}
				}
			}
			if !found {
				t.Fatal("post hook failure lost after image")
			}
		})
	}
}
func TestComputerPrivacyPermissionBoundaries(t *testing.T) {
	for _, v2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("v2=%v", v2), func(t *testing.T) {
			recorder := newComputerPrivacyRecorder(t, v2)
			called := false
			s := New(nil, nil, Options{Model: "test", Recorder: recorder, CWD: t.TempDir(), PermissionPrompt: func(_ context.Context, req tools.PermissionPromptRequest) tools.PermissionPromptResponse {
				called = true
				assertComputerPrivacy(t, "permission prompt", req)
				if !req.OneShot {
					t.Fatal("not a one-shot computer prompt")
				}
				return tools.PermissionPromptResponse{Allowed: true, Destination: "project", Rule: computerPrivacySecret, Reason: computerPrivacySecret, Payload: json.RawMessage(computerPrivacyInput)}
			}})
			response := s.runPermissionPrompt(context.Background(), tools.PermissionPromptRequest{ToolName: "ComputerUse", Input: json.RawMessage(computerPrivacyInput), Request: computerPrivacyInput, Rule: computerPrivacySecret, Reason: computerPrivacySecret, Source: computerPrivacySecret})
			if !called || !response.Allowed || response.Destination != "once" {
				t.Fatalf("response=%+v", response)
			}
			assertComputerPrivacy(t, "permission response", response)
			s.recordPermission(context.Background(), tools.PermissionAudit{ToolName: "ComputerUse", Request: computerPrivacyInput, Rule: computerPrivacySecret, Reason: computerPrivacySecret, Source: computerPrivacySecret})
			if err := s.applyPermissionUpdate(tools.PermissionUpdate{ToolName: "ComputerUse", Input: json.RawMessage(computerPrivacyInput), Request: computerPrivacyInput, Rule: computerPrivacySecret, Reason: computerPrivacySecret, Destination: "project", Decision: "allow"}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(config.ProjectSettingsPath(s.options.CWD, false)); !os.IsNotExist(err) {
				t.Fatalf("computer rule persisted: %v", err)
			}
			if len(s.sessionAllow) != 0 {
				t.Fatal("redacted rule broadened authority")
			}
			assertComputerPrivacyFiles(t, recorder)
		})
	}
}

// Audit projections deliberately do not match ComputerUse's executable schema.
// Presenting them as past tool arguments teaches the model invalid calls.
func TestComputerPrivacyLiveHistoryPreservesCanonicalToolExchange(t *testing.T) {
	tool := &computerPrivacyTool{name: "ComputerUse"}
	streamer := &computerPrivacyStreamer{name: tool.Name()}
	s := New(streamer, tools.NewRegistry(tool), Options{Model: "test", MaxTurns: 2, CWD: t.TempDir()})
	if _, err := s.Run(context.Background(), "perform action", io.Discard); err != nil {
		t.Fatal(err)
	}
	foundCall, foundResult := false, false
	for _, message := range streamer.next {
		for _, block := range message.Content {
			if block.Type == blockTypeToolUse && tools.IsComputerUseTool(block.Name) {
				foundCall = true
				if string(block.Input) != computerPrivacyInput {
					t.Fatal("live arguments replaced with non-executable audit summary")
				}
			}
			if block.Type == blockTypeToolResult && block.ToolUseID == "computer-call" {
				foundResult = true
			}
		}
	}
	if !foundCall || !foundResult {
		t.Fatal("live tool call/result protocol was replaced with historical text")
	}
	assertComputerPrivacy(t, "audit history", s.computerAuditHistory(streamer.next))
	if string(tool.input) != computerPrivacyInput {
		t.Fatal("live executable input changed")
	}
}

func TestComputerModelHistoryPreservesOtherToolsAndSource(t *testing.T) {
	original := []anthropic.MessageParam{
		{Role: "assistant", Content: []anthropic.ContentBlock{
			{Type: blockTypeToolUse, ID: "computer", Name: "ComputerUse", Input: json.RawMessage(computerPrivacyInput)},
			{Type: blockTypeToolUse, ID: "other", Name: "Other", Input: json.RawMessage(`{"value":"ordinary"}`)},
		}},
		{Role: "user", Content: []anthropic.ContentBlock{
			{Type: blockTypeToolResult, ToolUseID: "computer", Content: `{"receipt":{"outcome":"executed"}}`},
			{Type: blockTypeToolResult, ToolUseID: "other", Content: "ordinary result"},
			{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: computerPrivacyPNG}},
		}},
	}
	before, _ := json.Marshal(original)
	projected := computerModelHistory(original)
	after, _ := json.Marshal(original)
	if !bytes.Equal(before, after) {
		t.Fatal("source history mutated")
	}
	assertComputerPrivacy(t, "projected model history", projected)
	if projected[0].Content[0].Type != blockTypeText || projected[1].Content[0].Type != blockTypeText {
		t.Fatal("computer protocol not paired as text")
	}
	if projected[0].Content[1].Type != blockTypeToolUse || projected[1].Content[1].Type != blockTypeToolResult || projected[1].Content[2].Source.Data != computerPrivacyPNG {
		t.Fatal("ordinary tools or image changed")
	}
	twice, _ := json.Marshal(computerModelHistory(projected))
	once, _ := json.Marshal(projected)
	if !bytes.Equal(once, twice) {
		t.Fatal("model history projection is not idempotent")
	}
}

func TestComputerImagesAreBoundedAndAuditDumpRemainsPrivate(t *testing.T) {
	s := New(nil, nil, Options{Model: "test", CWD: t.TempDir()})
	var messages []anthropic.MessageParam
	for i := 0; i < 5; i++ {
		m := anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: fmt.Sprintf("computer-frame-%d", i)}}}}
		s.rememberTransientComputerImages([]anthropic.MessageParam{m})
		messages = append(messages, m)
	}
	userImage := anthropic.ContentBlock{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: "ordinary-user-image"}}
	messages = append(messages, anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{userImage}})
	messages = append(messages, anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeToolUse, ID: "call", Name: "ComputerUse", Input: json.RawMessage(computerPrivacyInput)}}})
	before, _ := json.Marshal(messages)
	limited := s.limitComputerImageHistory(messages)
	computerImages, otherImages := 0, 0
	for _, m := range limited {
		for _, b := range m.Content {
			if b.Type == blockTypeImage {
				if s.isTransientComputerImage(b) {
					computerImages++
				} else {
					otherImages++
				}
			}
		}
	}
	if computerImages != maxLiveComputerImages || otherImages != 1 {
		t.Fatalf("images: computer=%d other=%d", computerImages, otherImages)
	}
	if limited[3].Content[0].Source.Data != "computer-frame-3" || limited[4].Content[0].Source.Data != "computer-frame-4" {
		t.Fatal("newest frames not retained")
	}
	after, _ := json.Marshal(messages)
	if !bytes.Equal(before, after) {
		t.Fatal("source mutated")
	}
	dump := buildPromptDumpRecord(s, 2, anthropic.MessagesRequest{Model: "test", Messages: messages}, ContextManifest{}, true)
	assertComputerPrivacy(t, "full prompt dump", dump)
	raw, _ := json.Marshal(dump)
	for i := 0; i < 5; i++ {
		if bytes.Contains(raw, []byte(fmt.Sprintf("computer-frame-%d", i))) {
			t.Fatal("desktop image in dump")
		}
	}
	if !bytes.Contains(raw, []byte("ordinary-user-image")) {
		t.Fatal("ordinary full-dump image unexpectedly changed")
	}
	audit := s.computerAuditHistory(messages)
	raw, _ = json.Marshal(audit)
	if bytes.Contains(raw, []byte("computer-frame-")) {
		t.Fatal("desktop image exposed to summary model")
	}
}

func TestFastPathKeepsOnlyLatestTransientComputerImageInLiveRequest(t *testing.T) {
	s := New(nil, nil, Options{Model: "test", CWD: t.TempDir(), ComputerUseFastPath: true})
	var messages []anthropic.MessageParam
	for i := 0; i < 3; i++ {
		m := anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{
			{Type: blockTypeText, Text: "observation guidance"},
			{Type: blockTypeImage, Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: fmt.Sprintf("fast-frame-%d", i)}},
		}}
		s.rememberTransientComputerImages([]anthropic.MessageParam{m})
		messages = append(messages, m)
	}
	limited := s.limitComputerImageHistory(messages)
	if len(limited) != 1 || limited[0].Content[1].Source.Data != "fast-frame-2" {
		t.Fatalf("fast-path messages=%+v, want only latest observation", limited)
	}
	count, bytes := s.transientComputerImageStats(limited)
	if count != 1 || bytes != len("fast-frame-2") {
		t.Fatalf("latest image stats=(%d,%d), want (1,%d)", count, bytes, len("fast-frame-2"))
	}
}

func TestComputerFastPathPreservesExplicitPermissionHookDenial(t *testing.T) {
	tool := &computerPrivacyTool{name: "ComputerUse"}
	streamer := &computerPrivacyStreamer{name: tool.Name()}
	runtime := New(streamer, tools.NewRegistry(tool), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(),
		ComputerUse: fastPathComputerService{}, ComputerUseImageSupported: true,
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.PreToolUse: {{Command: `printf '%s' '{"permissionDecision":"deny"}'`}},
		}),
	})
	result, err := runtime.Run(context.Background(), "In a desktop app, type hello.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.options.ComputerUseFastPath {
		t.Fatal("fixture did not automatically enable fast path")
	}
	if len(tool.input) != 0 || len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError {
		t.Fatal("fast-path context optimization bypassed a permission-denying hook")
	}
	if runtime.runtimePolicy().DiscoverWorkspaceContext || runtime.runtimePolicy().DiscoverSkills {
		t.Fatal("honoring explicit hooks must not re-enable unrelated context discovery")
	}
}

func TestComputerFastPathPreservesPromptAuthorizationHook(t *testing.T) {
	probe := &deadlineProbeStreamer{}
	runtime := New(probe, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 1, CWD: t.TempDir(),
		ComputerUse: fastPathComputerService{}, ComputerUseImageSupported: true,
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.UserPromptSubmit: {{Command: `printf '%s' '{"permissionDecision":"deny"}'`}},
		}),
	})
	_, err := runtime.Run(context.Background(), "In a desktop app, type hello.", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "prompt denied by UserPromptSubmit hook") {
		t.Fatal("fast-path context optimization bypassed prompt authorization")
	}
}
