package query

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/capabilityloop"
	"github.com/konglong87/super-niuma/internal/compact"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/repair"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/skills"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
	agenttool "github.com/konglong87/super-niuma/internal/tools/agent"
	"github.com/konglong87/super-niuma/internal/tools/askuserquestion"
	bashtool "github.com/konglong87/super-niuma/internal/tools/bash"
	"github.com/konglong87/super-niuma/internal/tools/fileedit"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
	skilltool "github.com/konglong87/super-niuma/internal/tools/skill"
	tasktool "github.com/konglong87/super-niuma/internal/tools/task"
	"github.com/konglong87/super-niuma/internal/tools/todowrite"
)

type fakeStreamer struct {
	calls   int
	systems []string
}

type invocationCaptureTool struct {
	name        string
	invocations *[]tools.Invocation
	cancel      context.CancelFunc
}

func (t invocationCaptureTool) Name() string      { return t.name }
func (invocationCaptureTool) Description() string { return "capture runtime invocation" }
func (invocationCaptureTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t invocationCaptureTool) Run(_ context.Context, _ json.RawMessage, tc tools.Context) tools.Result {
	*t.invocations = append(*t.invocations, tc.Invocation)
	if t.cancel != nil {
		t.cancel()
	}
	return tools.Result{Content: "captured"}
}

type invocationCaptureStreamer struct{ calls int }

func (s *invocationCaptureStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	switch s.calls {
	case 1:
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
			{Type: blockTypeToolUse, ID: "tool-a", Name: "CaptureOne", Input: json.RawMessage(`{}`)},
			{Type: blockTypeToolUse, ID: "tool-b", Name: "CaptureTwo", Input: json.RawMessage(`{}`)},
		}}, StopReason: "tool_use"}, nil
	case 2:
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
			{Type: blockTypeToolUse, ID: "tool-c", Name: "CaptureOne", Input: json.RawMessage(`{}`)},
		}}, StopReason: "tool_use"}, nil
	default:
		if cb.OnText != nil {
			if err := cb.OnText("done"); err != nil {
				return nil, err
			}
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "done"}}}, StopReason: "end_turn"}, nil
	}
}

type deadlineProbeStreamer struct {
	deadline    time.Time
	hasDeadline bool
}

func (s *deadlineProbeStreamer) StreamMessages(ctx context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.deadline, s.hasDeadline = ctx.Deadline()
	return nil, errors.New("deadline probe")
}

type fastPathContextTool struct {
	got *bool
}

func (t fastPathContextTool) Name() string      { return "CaptureFastPath" }
func (fastPathContextTool) Description() string { return "capture ComputerUse fast-path wiring" }
func (fastPathContextTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t fastPathContextTool) Run(_ context.Context, _ json.RawMessage, tc tools.Context) tools.Result {
	*t.got = tc.ComputerUseFastPath
	return tools.Result{Content: "captured"}
}

type fastPathContextStreamer struct{ calls int }

func (s *fastPathContextStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: blockTypeToolUse, ID: "fast-path-tool", Name: "CaptureFastPath", Input: json.RawMessage(`{}`),
		}}}, StopReason: "tool_use"}, nil
	}
	if cb.OnText != nil {
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "done"}}}, StopReason: "end_turn"}, nil
}

type fastPathComputerService struct{}

func (fastPathComputerService) Capabilities(context.Context, cu.SessionOwner, string) (cu.Capabilities, error) {
	return cu.Capabilities{}, nil
}
func (fastPathComputerService) Observe(context.Context, cu.SessionOwner, cu.ObserveRequest) (cu.Observation, error) {
	return cu.Observation{}, nil
}
func (fastPathComputerService) Execute(context.Context, cu.SessionOwner, cu.Action) (cu.ActionReceipt, error) {
	return cu.ActionReceipt{}, nil
}
func (fastPathComputerService) Pause(context.Context, cu.SessionOwner, string) error  { return nil }
func (fastPathComputerService) Resume(context.Context, cu.SessionOwner, string) error { return nil }
func (fastPathComputerService) Stop(context.Context, cu.SessionOwner, string) error   { return nil }

func TestSimpleComputerUsePromptAutoEnablesOnlyForWorkBuddyGUIIntent(t *testing.T) {
	for _, test := range []struct {
		name   string
		prompt string
		want   bool
	}{
		{name: "simple WorkBuddy GUI flow", prompt: "In WorkBuddy, type hello and click Send.", want: true},
		{name: "simple Chinese WorkBuddy GUI flow", prompt: "帮我在WorkBuddy中新建会话，输入1+1=2并点击发送。", want: true},
		{name: "normal code query", prompt: "Fix the WorkBuddy integration code in the repository and add a test.", want: false},
		{name: "generic GUI prompt", prompt: "Click Send in another desktop app.", want: true},
		{name: "positive Bash automation request", prompt: "In WorkBuddy, use Bash to automate a click and send the message.", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isSimpleComputerUsePrompt(test.prompt); got != test.want {
				t.Fatalf("isSimpleComputerUsePrompt(%q)=%v, want %v", test.prompt, got, test.want)
			}
		})
	}
}

func TestSimpleComputerUsePromptCountsUnicodeCharactersForFastPath(t *testing.T) {
	prompt := strings.Repeat("请只使用 ComputerUse 在 WorkBuddy 中完成操作。", 40) + " 输入 1+1=2，然后点击发送。"
	if len(prompt) <= computerUseSimplePromptRuneLimit || utf8.RuneCountInString(prompt) > computerUseSimplePromptRuneLimit {
		t.Fatalf("test prompt does not exercise UTF-8 byte boundary: bytes=%d runes=%d", len(prompt), utf8.RuneCountInString(prompt))
	}
	if !isSimpleComputerUsePrompt(prompt) {
		t.Fatal("Chinese WorkBuddy prompt should use the fast path")
	}
}

func TestSimpleComputerUsePromptAllowsTrustedAcceptancePreamble(t *testing.T) {
	prompt := "请只使用 ComputerUse 完成这个真实桌面闭环，并在 120 秒内结束：目标应用使用通用 target_id=workbuddy，WorkBuddy 当前未启动。先使用 action=observe 建立 Computer Use 会话；如果目标应用未启动，使用 action=launch_app、target_id=workbuddy 启动并绑定目标窗口，然后 observe 绑定的目标窗口。必须等待内容 ready，不能只因为窗口出现就操作；如果白屏，最多等待 12 秒并重新 observe，仍白屏就 stop。内容 ready 后点击目标应用的新建会话或新建任务，重新 observe，定位输入框，输入 1+1=2，重新 observe，真实点击发送/提交按钮，等待一次回复，重新 observe 并确认页面出现真实回复，最后使用 ComputerUse stop。"
	if got := utf8.RuneCountInString(prompt); got > computerUseSimplePromptRuneLimit {
		t.Fatalf("preamble fixture rune count=%d", got)
	}
	if !isSimpleComputerUsePrompt(prompt) {
		t.Fatal("trusted desktop acceptance preamble should use the fast path")
	}
}

func TestSimpleComputerUsePromptAcceptsFullAcceptanceHarnessPreamble(t *testing.T) {
	promptData, err := os.ReadFile("../computeracceptance/assets/model-closure-prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(promptData)
	if got := utf8.RuneCountInString(prompt); got <= 1200 {
		t.Fatalf("acceptance prompt fixture must exercise the previous limit: runes=%d previous_limit=%d", got, 1200)
	}
	if !isSimpleComputerUsePrompt(prompt) {
		t.Fatal("full acceptance harness prompt must use the Computer Use fast path")
	}
	probe := &deadlineProbeStreamer{}
	session := New(probe, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 20, CWD: t.TempDir(),
		ComputerUse: cu.Service(fastPathComputerService{}), ComputerUseImageSupported: true,
	})
	if _, err := session.Run(context.Background(), prompt, io.Discard); err == nil || err.Error() != "deadline probe" {
		t.Fatalf("full acceptance prompt Run error=%v, want deadline probe", err)
	}
	if !session.options.ComputerUseFastPath || session.options.MaxTurns != computerUseFastPathMaxTurns || session.options.MaxTokens != computerUseFastPathMaxTokens {
		t.Fatalf("full acceptance prompt fast path state: enabled=%v max_turns=%d max_tokens=%d", session.options.ComputerUseFastPath, session.options.MaxTurns, session.options.MaxTokens)
	}
	if session.options.RuntimeProfile != runtimeprofile.ProfileBare || probe.hasDeadline {
		t.Fatalf("full acceptance prompt runtime state: profile=%q deadline=%v", session.options.RuntimeProfile, probe.hasDeadline)
	}
}

func TestSimpleComputerUsePromptActivatesBoundedRunWithoutChangingCodeQueries(t *testing.T) {
	fastProbe := &deadlineProbeStreamer{}
	fast := New(fastProbe, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 20, CWD: t.TempDir(),
		ComputerUse: cu.Service(fastPathComputerService{}), ComputerUseImageSupported: true,
	})
	if _, err := fast.Run(context.Background(), "In WorkBuddy, type hello and click Send.", io.Discard); err == nil || err.Error() != "deadline probe" {
		t.Fatalf("WorkBuddy Run error=%v, want deadline probe", err)
	}
	if !fast.options.ComputerUseFastPath || fast.options.MaxTurns != 16 || fastProbe.hasDeadline {
		t.Fatalf("WorkBuddy fast path state: enabled=%v max_turns=%d deadline=%v", fast.options.ComputerUseFastPath, fast.options.MaxTurns, fastProbe.hasDeadline)
	}
	if fast.options.RuntimeProfile != runtimeprofile.ProfileBare {
		t.Fatalf("WorkBuddy fast path runtime profile=%q, want bare", fast.options.RuntimeProfile)
	}

	codeProbe := &deadlineProbeStreamer{}
	code := New(codeProbe, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 20, CWD: t.TempDir(),
		ComputerUse: cu.Service(fastPathComputerService{}), ComputerUseImageSupported: true,
	})
	if _, err := code.Run(context.Background(), "Fix the WorkBuddy integration code in the repository and add a test.", io.Discard); err == nil || err.Error() != "deadline probe" {
		t.Fatalf("code Run error=%v, want deadline probe", err)
	}
	if code.options.ComputerUseFastPath || code.options.MaxTurns != 20 || codeProbe.hasDeadline {
		t.Fatalf("code query changed by classifier: enabled=%v max_turns=%d deadline=%v", code.options.ComputerUseFastPath, code.options.MaxTurns, codeProbe.hasDeadline)
	}
}

func TestComputerUseFastPathPropagatesToToolContext(t *testing.T) {
	var fastPath bool
	session := New(&fastPathContextStreamer{}, tools.NewRegistry(fastPathContextTool{got: &fastPath}), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(), ComputerUseFastPath: true,
	})
	if _, err := session.Run(context.Background(), "capture", io.Discard); err != nil {
		t.Fatal(err)
	}
	if !fastPath {
		t.Fatal("ComputerUseFastPath was not propagated to tools.Context")
	}
}

func TestComputerUseFastPathCapsProviderOutputBudget(t *testing.T) {
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 1, MaxTokens: defaults.CodeMaxTokens, Effort: "high",
		CWD: t.TempDir(), ComputerUseFastPath: true,
	})
	if _, err := session.Run(context.Background(), "probe", io.Discard); err != nil {
		t.Fatal(err)
	}
	if session.options.MaxTokens != computerUseFastPathMaxTokens {
		t.Fatalf("fast path max_tokens=%d, want %d", session.options.MaxTokens, computerUseFastPathMaxTokens)
	}
	if len(streamer.maxTokens) != 1 || streamer.maxTokens[0] != session.options.MaxTokens {
		t.Fatalf("provider max_tokens=%v session=%d", streamer.maxTokens, session.options.MaxTokens)
	}
	if len(streamer.thinking) != 1 || streamer.thinking[0] == nil || streamer.thinking[0].Effort != "high" {
		t.Fatalf("fast path changed requested effort: thinking=%+v", streamer.thinking)
	}
	if streamer.thinking[0].BudgetTokens >= defaults.CodeMaxTokens {
		t.Fatalf("fast path thinking budget=%d, want bounded budget", streamer.thinking[0].BudgetTokens)
	}
}

func TestComputerUseFastPathBoundsTurnsWithoutProductDeadline(t *testing.T) {
	fast := New(&deadlineProbeStreamer{}, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 20, CWD: t.TempDir(), ComputerUseFastPath: true,
	})
	if fast.options.MaxTurns != 16 {
		t.Fatalf("fast path max turns=%d, want 16", fast.options.MaxTurns)
	}
	limited := New(&deadlineProbeStreamer{}, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 3, CWD: t.TempDir(), ComputerUseFastPath: true,
	})
	if limited.options.MaxTurns != 3 {
		t.Fatalf("fast path raised configured max turns to %d", limited.options.MaxTurns)
	}

	probe := &deadlineProbeStreamer{}
	session := New(probe, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 1, CWD: t.TempDir(), ComputerUseFastPath: true,
	})
	if _, err := session.Run(context.Background(), "probe", io.Discard); err == nil || err.Error() != "deadline probe" {
		t.Fatalf("Run error=%v, want deadline probe", err)
	}
	if probe.hasDeadline {
		t.Fatalf("fast path imposed a product deadline: %s", time.Until(probe.deadline))
	}
}

func TestNormalQueryKeepsConfiguredTurnsAndNoFastPathDeadline(t *testing.T) {
	probe := &deadlineProbeStreamer{}
	session := New(probe, tools.NewRegistry(), Options{
		Model: "test", MaxTurns: 20, CWD: t.TempDir(), ComputerUseFastPath: false,
	})
	if session.options.MaxTurns != 20 {
		t.Fatalf("normal max turns=%d, want 20", session.options.MaxTurns)
	}
	if _, err := session.Run(context.Background(), "probe", io.Discard); err == nil || err.Error() != "deadline probe" {
		t.Fatalf("Run error=%v, want deadline probe", err)
	}
	if probe.hasDeadline {
		t.Fatal("normal query unexpectedly received a fast-path deadline")
	}
}

func TestSequentialToolsReceiveRuntimeInvocationAndPerTurnBatch(t *testing.T) {
	var invocations []tools.Invocation
	streamer := &invocationCaptureStreamer{}
	runtime := New(streamer, tools.NewRegistry(
		invocationCaptureTool{name: "CaptureOne", invocations: &invocations},
		invocationCaptureTool{name: "CaptureTwo", invocations: &invocations},
	), Options{Model: "test", MaxTurns: 3, CWD: t.TempDir(), RunID: "run-123", MaxParallelReadOnlyTools: -1})

	if _, err := runtime.Run(context.Background(), "capture invocation", io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []tools.Invocation{
		{RunID: "run-123", ToolUseID: "tool-a", BatchID: "145820fa9a0724d38e7c323f79cf82840cff55ea552f38ba12d9ddefd14d9c64"},
		{RunID: "run-123", ToolUseID: "tool-b", BatchID: "145820fa9a0724d38e7c323f79cf82840cff55ea552f38ba12d9ddefd14d9c64"},
		{RunID: "run-123", ToolUseID: "tool-c", BatchID: "e254840e05e58f844b142fbe99115e7edb487972baed939b75f3ed90bfb9f665"},
	}
	if !reflect.DeepEqual(invocations, want) {
		t.Fatalf("invocations = %+v, want %+v", invocations, want)
	}
}

type reusableInvocationStreamer struct{ calls int }

func (s *reusableInvocationStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls%2 == 1 {
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: blockTypeToolUse, ID: fmt.Sprintf("tool-run-%d", s.calls), Name: "Capture", Input: json.RawMessage(`{}`),
		}}}, StopReason: "tool_use"}, nil
	}
	if cb.OnText != nil {
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "done"}}}, StopReason: "end_turn"}, nil
}

func TestSessionRunCreatesUniqueFallbackInvocationIdentity(t *testing.T) {
	var invocations []tools.Invocation
	streamer := &reusableInvocationStreamer{}
	runtime := New(streamer, tools.NewRegistry(invocationCaptureTool{name: "Capture", invocations: &invocations}), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(), MaxParallelReadOnlyTools: -1,
	})
	for index := 0; index < 2; index++ {
		if _, err := runtime.Run(context.Background(), "capture invocation", io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if len(invocations) != 2 {
		t.Fatalf("invocations = %+v", invocations)
	}
	if invocations[0].RunID == "" || invocations[1].RunID == "" || invocations[0].RunID == invocations[1].RunID {
		t.Fatalf("fallback RunIDs must be unique per Run: %+v", invocations)
	}
	if invocations[0].BatchID == "" || invocations[1].BatchID == "" || invocations[0].BatchID == invocations[1].BatchID {
		t.Fatalf("fallback BatchIDs must be unique per Run: %+v", invocations)
	}
}

type cancelledToolTurnStreamer struct{ calls int }

func (s *cancelledToolTurnStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
		{Type: blockTypeToolUse, ID: "tool-first", Name: "Capture", Input: json.RawMessage(`{}`)},
		{Type: blockTypeToolUse, ID: "tool-skipped-1", Name: "Capture", Input: json.RawMessage(`{}`)},
		{Type: blockTypeToolUse, ID: "tool-skipped-2", Name: "Capture", Input: json.RawMessage(`{}`)},
	}}, StopReason: "tool_use"}, nil
}

func TestExpiredContextSkipsRemainingSequentialToolsAndPreservesPartialResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var invocations []tools.Invocation
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "52525252-5252-4525-8525-525252525252")
	if err != nil {
		t.Fatal(err)
	}
	streamer := &cancelledToolTurnStreamer{}
	runtime := New(streamer, tools.NewRegistry(invocationCaptureTool{name: "Capture", invocations: &invocations, cancel: cancel}), Options{
		Model: "test", MaxTurns: 3, CWD: t.TempDir(), Recorder: recorder, RunID: "run-cancel", MaxParallelReadOnlyTools: -1,
	})
	var callCallbacks, resultCallbacks int
	result, runErr := runtime.RunWithCallbacks(ctx, "cancel after first tool", io.Discard, RunCallbacks{
		OnToolCall:   func(ToolCallEvent) error { callCallbacks++; return nil },
		OnToolResult: func(ToolTrace) error { resultCallbacks++; return nil },
	})
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", runErr)
	}
	if len(invocations) != 1 || invocations[0].ToolUseID != "tool-first" {
		t.Fatalf("executed invocations = %+v, want only tool-first", invocations)
	}
	if callCallbacks != 1 || resultCallbacks != 1 {
		t.Fatalf("callbacks = call:%d result:%d, want 1/1", callCallbacks, resultCallbacks)
	}
	if len(result.ToolCalls) != 3 || result.ToolCalls[0].IsError || !result.ToolCalls[1].IsError || !result.ToolCalls[2].IsError {
		t.Fatalf("partial tool calls = %+v", result.ToolCalls)
	}
	for _, index := range []int{1, 2} {
		if result.ToolCalls[index].Output != context.Canceled.Error() {
			t.Fatalf("cancelled trace %d output = %q", index, result.ToolCalls[index].Output)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	paired := map[string]bool{}
	for _, entry := range entries {
		if entry.Type == blockTypeToolResult {
			paired[entry.ToolID] = true
		}
	}
	for _, id := range []string{"tool-first", "tool-skipped-1", "tool-skipped-2"} {
		if !paired[id] {
			t.Fatalf("missing paired tool result for %s: %+v", id, entries)
		}
	}
}

type threeToolUseStreamer struct {
	name  string
	input json.RawMessage
}

func (s threeToolUseStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	input := s.input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
		{Type: blockTypeToolUse, ID: "tool-first", Name: s.name, Input: input},
		{Type: blockTypeToolUse, ID: "tool-second", Name: s.name, Input: input},
		{Type: blockTypeToolUse, ID: "tool-third", Name: s.name, Input: input},
	}}, StopReason: "tool_use"}, nil
}

type effectCountTool struct {
	name string
	runs *int
}

func (t effectCountTool) Name() string               { return t.name }
func (effectCountTool) Description() string          { return "count tool effects" }
func (effectCountTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t effectCountTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	*t.runs++
	return tools.Result{Content: "effect completed"}
}

func cancellationRecorder(t *testing.T) *session.Recorder {
	t.Helper()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(t.TempDir(), "53535353-5353-4535-8535-535353535353")
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

func assertCancelledTurnPairing(t *testing.T, recorder *session.Recorder, result Result, ids ...string) {
	t.Helper()
	if len(result.ToolCalls) != len(ids) {
		t.Fatalf("tool calls = %+v, want %d", result.ToolCalls, len(ids))
	}
	resultCounts := map[string]int{}
	for _, trace := range result.ToolCalls {
		resultCounts[trace.ID]++
		if !trace.IsError || trace.Output != context.Canceled.Error() {
			t.Fatalf("trace = %+v, want cancelled", trace)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	transcriptCounts := map[string]int{}
	for _, entry := range entries {
		if entry.Type == blockTypeToolResult {
			transcriptCounts[entry.ToolID]++
		}
	}
	for _, id := range ids {
		if resultCounts[id] != 1 || transcriptCounts[id] != 1 {
			t.Fatalf("pair count for %s = result:%d transcript:%d entries=%+v", id, resultCounts[id], transcriptCounts[id], entries)
		}
	}
}

func TestOnToolCallCancellationSkipsCurrentAndRemainingSequentialEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	recorder := cancellationRecorder(t)
	runtime := New(threeToolUseStreamer{name: "Effect"}, tools.NewRegistry(effectCountTool{name: "Effect", runs: &runs}), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(), Recorder: recorder, MaxParallelReadOnlyTools: -1,
	})
	var calls, results []string
	result, err := runtime.RunWithCallbacks(ctx, "cancel in callback", io.Discard, RunCallbacks{
		OnToolCall: func(event ToolCallEvent) error {
			calls = append(calls, event.ID)
			cancel()
			return nil
		},
		OnToolResult: func(trace ToolTrace) error { results = append(results, trace.ID); return nil },
	})
	if !errors.Is(err, context.Canceled) || runs != 0 {
		t.Fatalf("err=%v runs=%d result=%+v", err, runs, result)
	}
	if !reflect.DeepEqual(calls, []string{"tool-first"}) || !reflect.DeepEqual(results, []string{"tool-first"}) {
		t.Fatalf("callbacks calls=%v results=%v", calls, results)
	}
	assertCancelledTurnPairing(t, recorder, result, "tool-first", "tool-second", "tool-third")
}

func TestPermissionPromptCancellationSkipsApprovedSequentialEffect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	recorder := cancellationRecorder(t)
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "ask"})
	runtime := New(threeToolUseStreamer{name: "Bash", input: json.RawMessage(`{"command":"echo hello"}`)}, tools.NewRegistry(tools.Guard(effectCountTool{name: "Bash", runs: &runs}, policy)), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(), Recorder: recorder, MaxParallelReadOnlyTools: -1,
		PermissionPrompt: func(context.Context, tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			cancel()
			return tools.PermissionPromptResponse{Allowed: true, Destination: "once"}
		},
	})
	var calls, results []string
	result, err := runtime.RunWithCallbacks(ctx, "cancel in permission", io.Discard, RunCallbacks{
		OnToolCall:   func(event ToolCallEvent) error { calls = append(calls, event.ID); return nil },
		OnToolResult: func(trace ToolTrace) error { results = append(results, trace.ID); return nil },
	})
	if !errors.Is(err, context.Canceled) || runs != 0 {
		t.Fatalf("err=%v runs=%d result=%+v", err, runs, result)
	}
	if !reflect.DeepEqual(calls, []string{"tool-first"}) || !reflect.DeepEqual(results, []string{"tool-first"}) {
		t.Fatalf("callbacks calls=%v results=%v", calls, results)
	}
	assertCancelledTurnPairing(t, recorder, result, "tool-first", "tool-second", "tool-third")
}

func TestPreToolHookCancellationSkipsCurrentAndRemainingSequentialEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs := 0
	recorder := cancellationRecorder(t)
	runtime := New(threeToolUseStreamer{name: "Effect"}, tools.NewRegistry(effectCountTool{name: "Effect", runs: &runs}), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(), Recorder: recorder, MaxParallelReadOnlyTools: -1,
	})
	var calls, results []string
	result, err := runtime.run(ctx, "cancel in hook", runCallbacks{
		onToolCall:   func(block anthropic.ContentBlock) error { calls = append(calls, block.ID); return nil },
		onToolResult: func(trace ToolTrace) error { results = append(results, trace.ID); return nil },
		onHookStart: func(event, _ string) error {
			if event == hooks.PreToolUse {
				cancel()
			}
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) || runs != 0 {
		t.Fatalf("err=%v runs=%d result=%+v", err, runs, result)
	}
	if !reflect.DeepEqual(calls, []string{"tool-first"}) || !reflect.DeepEqual(results, []string{"tool-first"}) {
		t.Fatalf("callbacks calls=%v results=%v", calls, results)
	}
	assertCancelledTurnPairing(t, recorder, result, "tool-first", "tool-second", "tool-third")
}

type pendingQuestionStreamer struct {
	calls int
}

func (s *pendingQuestionStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu_question",
			Name:  "AskUserQuestion",
			Input: json.RawMessage(`{"question":"Which path?","choices":["A","B"]}`),
		}}},
		StopReason: "tool_use",
	}, nil
}

type resumeProbeStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *resumeProbeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(req.Messages) < 3 {
		return nil, fmt.Errorf("resume request missing tool continuation: %d messages", len(req.Messages))
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" || len(last.Content) != 1 || last.Content[0].Type != "tool_result" || last.Content[0].Content != "B" {
		return nil, fmt.Errorf("unexpected resume tail: %+v", req.Messages[len(req.Messages)-2:])
	}
	if err := cb.OnText("resumed"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "resumed"}}},
		StopReason: "end_turn",
	}, nil
}

func TestResumeToolResultDoesNotAddOrdinaryUserPrompt(t *testing.T) {
	streamer := &resumeProbeStreamer{}
	session := New(streamer, tools.NewRegistry(), Options{
		Model: "test",
		CWD:   t.TempDir(),
		InitialMessages: []anthropic.MessageParam{{
			Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "ask"}},
		}},
	})
	result, err := session.RunWithResume(context.Background(), ResumeInput{
		AssistantMessage: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_question", Name: "AskUserQuestion", Input: json.RawMessage(`{"question":"Which path?"}`)}}},
		ToolResult:       anthropic.ContentBlock{Type: "tool_result", ToolUseID: "toolu_question", Content: "B"},
	}, io.Discard, RunCallbacks{})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if result.Response != "resumed" || len(streamer.requests) != 1 {
		t.Fatalf("result=%+v requests=%d", result, len(streamer.requests))
	}
}

func TestPendingQuestionStopsQueryBeforeSecondProviderRequest(t *testing.T) {
	streamer := &pendingQuestionStreamer{}
	session := New(streamer, tools.NewRegistry(askuserquestion.New()), Options{
		Model: "test",
		CWD:   t.TempDir(),
		UserQuestionPrompt: func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse {
			return tools.UserQuestionResponse{Pending: true, InteractionID: "interaction-1"}
		},
	})
	result, err := session.RunWithCallbacks(context.Background(), "ask", io.Discard, RunCallbacks{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if streamer.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", streamer.calls)
	}
	if result.StopReason != "waiting_input" || result.PendingInteraction == nil || result.PendingInteraction.ID != "interaction-1" {
		t.Fatalf("result = %+v", result)
	}
}

func (f *fakeStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	f.systems = append(f.systems, req.System)
	if f.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"hello"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, "hello") {
		return nil, io.ErrUnexpectedEOF
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type usageLoopStreamer struct {
	calls int
}

func TestUsageFromAnthropicKeepsProviderPromptTokensForOpenAICompatible(t *testing.T) {
	usage := usageFromAnthropic(anthropic.Usage{
		InputTokens:                 20394,
		OutputTokens:                15,
		CacheReadInputTokens:        20224,
		InputTokensIncludeCacheRead: true,
	})
	if usage.InputTokens != 20394 || usage.CacheReadInputTokens != 20224 || usage.OutputTokens != 15 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestUsageFromAnthropicTotalsAnthropicCacheTokenColumns(t *testing.T) {
	usage := usageFromAnthropic(anthropic.Usage{
		InputTokens:              100,
		OutputTokens:             5,
		CacheCreationInputTokens: 20,
		CacheReadInputTokens:     30,
	})
	if usage.InputTokens != 150 || usage.CacheCreationInputTokens != 20 || usage.CacheReadInputTokens != 30 || usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", usage)
	}
}

func (s *usageLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"hello"}`),
			}}},
			StopReason: "tool_use",
			Usage:      anthropic.Usage{InputTokens: 100, OutputTokens: 5},
		}, nil
	}
	if !lastToolResultContains(req.Messages, "hello") {
		return nil, io.ErrUnexpectedEOF
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: 200, OutputTokens: 10},
	}, nil
}

type completionVerificationStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *completionVerificationStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Tests passed.\nENGINEERING_EMAIL_MASK_DONE"}}},
			StopReason: "end_turn",
		}, nil
	case 2:
		if !strings.Contains(allMessageText(req.Messages), "Completion is blocked") {
			return nil, fmt.Errorf("missing completion verification nudge")
		}
		for _, want := range []string{"go test", "git diff --check"} {
			if !strings.Contains(allMessageText(req.Messages), want) {
				return nil, fmt.Errorf("nudge missing %q", want)
			}
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_verify",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"go test ./... -count=1 && git diff --check"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		if !lastToolResultContains(req.Messages, "verification ok") {
			return nil, fmt.Errorf("missing verification tool result")
		}
		if err := cb.OnText("Verified now.\nENGINEERING_EMAIL_MASK_DONE"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Verified now.\nENGINEERING_EMAIL_MASK_DONE"}}},
			StopReason: "end_turn",
		}, nil
	}
}

type continuationAckStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *continuationAckStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		if !strings.Contains(allMessageText(req.Messages), "Continuation intent detected") {
			return nil, fmt.Errorf("missing continuation intent reminder")
		}
		if err := cb.OnText("收到。有什么需要做的随时说。"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "收到。有什么需要做的随时说。"}}},
			StopReason: "end_turn",
		}, nil
	case 2:
		text := allMessageText(req.Messages)
		if !strings.Contains(text, "Completion is blocked") || !strings.Contains(text, "confirmed a pending action") {
			return nil, fmt.Errorf("missing continuation completion gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_continue",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"continued pending action"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		if !lastToolResultContains(req.Messages, "continued pending action") {
			return nil, fmt.Errorf("missing continuation tool result")
		}
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
		}, nil
	}
}

type verificationFailureRecoveryStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *verificationFailureRecoveryStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_verify_failed",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"go test ./... -count=1"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, "Verification command failed") {
		return nil, fmt.Errorf("missing verification failure recovery reminder")
	}
	if !lastToolResultContains(req.Messages, "expected/actual mismatch") {
		return nil, fmt.Errorf("missing expected/actual recovery guidance")
	}
	if !lastToolResultContains(req.Messages, "Do not create temporary test files") {
		return nil, fmt.Errorf("missing scratch test file recovery guidance")
	}
	if !lastToolResultContains(req.Messages, "read-only deterministic shell pipeline") {
		return nil, fmt.Errorf("missing read-only deterministic verification guidance")
	}
	if !lastToolResultContains(req.Messages, "copy the exact expected literal") {
		return nil, fmt.Errorf("missing exact expected literal recovery guidance")
	}
	if !lastToolResultContains(req.Messages, "Static /dev/null output discard is allowed") {
		return nil, fmt.Errorf("missing null-device redirect guidance")
	}
	if !lastToolResultContains(req.Messages, "paths relative to the current workspace") {
		return nil, fmt.Errorf("missing workspace-relative path recovery guidance")
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "will fix"}}},
		StopReason: "end_turn",
	}, nil
}

type editPostReminderStreamer struct {
	toolInput json.RawMessage
	wants     []string
	requests  []anthropic.MessagesRequest
}

func (s *editPostReminderStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_edit",
				Name:  "Edit",
				Input: s.toolInput,
			}}},
			StopReason: "tool_use",
		}, nil
	}
	for _, want := range s.wants {
		if !lastToolResultContains(req.Messages, want) {
			return nil, fmt.Errorf("post-edit reminder missing %q", want)
		}
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type todoWriteResultStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *todoWriteResultStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_todo",
				Name:  "TodoWrite",
				Input: json.RawMessage(`{"todos":[{"content":"Collect TodoWrite request evidence","activeForm":"Collecting TodoWrite request evidence","status":"in_progress","priority":"high"},{"content":"Run TodoWrite tests","status":"pending","priority":"medium"}]}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type finalTurnToolDisableStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *finalTurnToolDisableStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"evidence"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if len(req.Tools) != 0 {
		return nil, fmt.Errorf("final turn tools were exposed: %+v", req.Tools)
	}
	if !strings.Contains(allMessageText(req.Messages), "## Turn budget reminder") {
		return nil, fmt.Errorf("final turn missing turn budget reminder")
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type partialTextStreamErrorStreamer struct{}

func (s *partialTextStreamErrorStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		if err := cb.OnText("partial answer"); err != nil {
			return nil, err
		}
	}
	return nil, &anthropic.PartialStreamError{
		Err: io.ErrUnexpectedEOF,
		Partial: &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "partial answer"}}},
		},
	}
}

type partialToolUseStreamErrorStreamer struct{}

func (s *partialToolUseStreamErrorStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		if err := cb.OnText("partial"); err != nil {
			return nil, err
		}
	}
	return nil, &anthropic.PartialStreamError{
		Err: io.ErrUnexpectedEOF,
		Partial: &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
				{Type: "text", Text: "partial"},
				{Type: "tool_use", ID: "toolu_partial", Name: "Echo", Input: json.RawMessage(`{"text"`)},
			}},
		},
	}
}

type trivialMathStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *trivialMathStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	text := allMessageText(req.Messages)
	for _, unexpected := range []string{"Repository health audit strategy", "Repair safety strategy", "Completion is blocked", "Post-edit verification required"} {
		if strings.Contains(text, unexpected) {
			return nil, fmt.Errorf("trivial prompt unexpectedly injected %q", unexpected)
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "2"}}},
		StopReason: "end_turn",
	}, nil
}

type readmeNoReadClosureStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *readmeNoReadClosureStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "I read README.md and summarized it."}}},
			StopReason: "end_turn",
		}, nil
	case 2:
		if !strings.Contains(allMessageText(req.Messages), "Read Scope Gate") {
			return nil, fmt.Errorf("missing read scope gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_readme",
				Name:  "Read",
				Input: json.RawMessage(`{"file_path":"README.md"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		if !lastToolResultContains(req.Messages, "Project README") {
			return nil, fmt.Errorf("missing README tool result")
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "README.md summary: Project README."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type wrongFileReadClosureStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *wrongFileReadClosureStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_extra",
				Name:  "Read",
				Input: json.RawMessage(`{"file_path":"EXTRA.md"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "I read README.md and summarized it."}}},
			StopReason: "end_turn",
		}, nil
	case 3:
		if !strings.Contains(allMessageText(req.Messages), "missing required read evidence for: README.md") {
			return nil, fmt.Errorf("missing wrong-file read scope reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_readme",
				Name:  "Read",
				Input: json.RawMessage(`{"file_path":"README.md"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "README.md summary: Project README."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type partialReadClosureStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *partialReadClosureStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_partial",
				Name:  "Read",
				Input: json.RawMessage(`{"file_path":"README.md","offset":1,"limit":2}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Full file summary of README.md: Project README."}}},
			StopReason: "end_turn",
		}, nil
	default:
		if !strings.Contains(allMessageText(req.Messages), "only partial read evidence is present for: README.md") {
			return nil, fmt.Errorf("missing partial read scope reminder")
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Partial README.md summary from the read range: Project README."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type claimGateStreamer struct {
	first    string
	second   string
	reminder string
	requests []anthropic.MessagesRequest
}

func (s *claimGateStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: s.first}}},
			StopReason: "end_turn",
		}, nil
	}
	if !strings.Contains(allMessageText(req.Messages), s.reminder) {
		return nil, fmt.Errorf("missing claim gate reminder %q", s.reminder)
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: s.second}}},
		StopReason: "end_turn",
	}, nil
}

type failedAuditGateStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *failedAuditGateStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_broad_audit",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"for dir in 0-start-here 1-understand-ai 3-ai-agents; do total=$(find \"$dir\" -name \"*.md\" 2>/dev/null | wc -l); echo \"$dir: $total\"; done"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		if !lastToolResultContains(req.Messages, "write path /dev/null") {
			return nil, fmt.Errorf("missing failed broad audit result")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_scoped_audit",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"find 3-ai-agents -name \"*.md\" | while read f; do echo \"$(wc -l < \"$f\") $f\"; done"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 3:
		if !lastToolResultContains(req.Messages, "3-ai-agents/hermes-agent/README.md") {
			return nil, fmt.Errorf("missing scoped audit result")
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "数据摆完了，3-ai-agents 是六大主目录里唯一只有 1 个文件的目录。"}}},
			StopReason: "end_turn",
		}, nil
	default:
		if !strings.Contains(allMessageText(req.Messages), "Failed Audit Evidence Gate") {
			return nil, fmt.Errorf("missing failed audit gate reminder")
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "全局统计命令失败，所以这里只能确认 3-ai-agents 的局部证据：当前成功读取到 1 个 md 文件。"}}},
			StopReason: "end_turn",
		}, nil
	}
}

type postEditDeltaGateStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *postEditDeltaGateStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_edit",
				Name:  "Edit",
				Input: json.RawMessage(`{"file_path":"config.txt","old_string":"old","new_string":"new"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Fixed."}}},
			StopReason: "end_turn",
		}, nil
	case 3:
		if !strings.Contains(allMessageText(req.Messages), "Post-Action Delta Gate") {
			return nil, fmt.Errorf("missing post-action delta gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_verify_delta",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short && git diff --name-status && git diff --summary && go test ./internal/query -count=1"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Fixed and verified."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type preCommitBlockedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *preCommitBlockedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_commit",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git commit -m phase-test"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, "<gate-preflight") || !lastToolResultContains(req.Messages, "Original command was not executed") {
		return nil, fmt.Errorf("missing pre-commit preflight tool_result")
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Commit was blocked pending scope verification."}}},
		StopReason: "end_turn",
	}, nil
}

type chainedPreCommitBlockedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *chainedPreCommitBlockedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_chained_commit",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git diff --cached --name-status && git commit -m chained"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	text := lastToolResultText(req.Messages)
	for _, want := range []string{
		"Pre-Commit Scope Gate",
		"git status --short --branch && git diff --name-status && git diff --cached --name-status",
		"separate Bash tool call",
		"Do not chain this verification with git commit",
	} {
		if !strings.Contains(text, want) {
			return nil, fmt.Errorf("missing chained-commit recovery text %q in %q", want, text)
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Commit was blocked pending standalone scope verification."}}},
		StopReason: "end_turn",
	}, nil
}

type partialStagedScopeBlockedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *partialStagedScopeBlockedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_partial_staged_scope",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short && git diff --cached --name-status"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_commit_after_partial_scope",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git commit -m partial-scope"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		if !lastToolResultContains(req.Messages, "<gate-preflight") || !lastToolResultContains(req.Messages, "Original command was not executed") {
			return nil, fmt.Errorf("missing pre-commit preflight after partial staged scope")
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Partial staged scope was not enough to commit."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type pushClaimGateStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *pushClaimGateStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_push_precheck",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short --branch && git rev-parse HEAD && git rev-parse --abbrev-ref --symbolic-full-name @{u}"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_push",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git push origin HEAD"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 3:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Pushed to origin."}}},
			StopReason: "end_turn",
		}, nil
	case 4:
		if !strings.Contains(allMessageText(req.Messages), "post-push remote/branch verification") {
			return nil, fmt.Errorf("missing post-push claim gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_push_verify",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short --branch && git ls-remote origin HEAD"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Pushed to origin and verified remote state."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type externalActionBlockedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *externalActionBlockedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_deploy",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"deploy production"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, "External Action Gate") {
		return nil, fmt.Errorf("missing external action gate tool_result")
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "External action was blocked pending explicit authorization."}}},
		StopReason: "end_turn",
	}, nil
}

type directoryNoReadClosureStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *directoryNoReadClosureStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "I inspected internal/query and summarized the directory."}}},
			StopReason: "end_turn",
		}, nil
	case 2:
		if !strings.Contains(allMessageText(req.Messages), "Read Scope Gate") {
			return nil, fmt.Errorf("missing directory read scope gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_ls_dir",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"ls internal/query"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "internal/query summary is based on the observed directory listing."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type gitTagListAllowedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *gitTagListAllowedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_tag_list",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git tag --list"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Listed tags."}}},
		StopReason: "end_turn",
	}, nil
}

type forcePushBlockedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *forcePushBlockedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_force_push",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git push --force origin main"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, "Destructive Shared-State Gate") {
		return nil, fmt.Errorf("missing destructive shared-state gate tool_result")
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Force push was blocked pending explicit authorization."}}},
		StopReason: "end_turn",
	}, nil
}

type stagedCommitAllowedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *stagedCommitAllowedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_staged_scope",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_commit",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git commit -m staged-scope"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Committed staged changes."}}},
			StopReason: "end_turn",
		}, nil
	}
}

// compoundPushOneLinerStreamer verifies that staging cannot occur after the
// scope readback in the same command that commits and pushes.
type compoundPushOneLinerStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *compoundPushOneLinerStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	const oneLiner = `{"command":"git add README.md && git commit -m bump && git push"}`
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_oneliner_1",
				Name:  "Bash",
				Input: json.RawMessage(oneLiner),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		if !lastToolResultContains(req.Messages, "Do not chain this verification with git commit") {
			return nil, fmt.Errorf("expected compound staging/commit command to require a separate scope readback")
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "改动尚未提交。"}}},
			StopReason: "end_turn",
		}, nil
	default:
		return nil, fmt.Errorf("unexpected request count %d", len(s.requests))
	}
}

type commitPostcheckGateStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *commitPostcheckGateStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_staged_scope",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_commit",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git commit -m p1-test"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 3:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Committed."}}},
			StopReason: "end_turn",
		}, nil
	case 4:
		if !strings.Contains(allMessageText(req.Messages), "post-commit git status/log verification") {
			return nil, fmt.Errorf("missing post-commit claim gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_commit_verify",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short --branch && git log -1 --oneline"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Committed and verified."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type tagPrecheckBlockedStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *tagPrecheckBlockedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_tag",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git tag v1.2.3"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, `<gate-preflight rule_id="shared_state_git_tag"`) || !lastToolResultContains(req.Messages, "Original command was not executed") {
		return nil, fmt.Errorf("missing tag preflight tool_result")
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Tag creation is pending tag-state precheck review."}}},
		StopReason: "end_turn",
	}, nil
}

type tagPostcheckGateStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *tagPostcheckGateStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_tag_precheck",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short --branch && git rev-parse HEAD && git tag --list v1.2.3"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_tag",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git tag v1.2.3"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 3:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Created tag v1.2.3."}}},
			StopReason: "end_turn",
		}, nil
	case 4:
		if !strings.Contains(allMessageText(req.Messages), "post-tag object verification") {
			return nil, fmt.Errorf("missing post-tag claim gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_tag_verify",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git rev-parse HEAD && git rev-parse refs/tags/v1.2.3"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Created tag v1.2.3 and verified object state."}}},
			StopReason: "end_turn",
		}, nil
	}
}

type unknownRiskyBashDeltaStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *unknownRiskyBashDeltaStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch len(s.requests) {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_unknown_script",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"./scripts/custom-wrapper.sh --maybe-mutates"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Completed."}}},
			StopReason: "end_turn",
		}, nil
	case 3:
		if !strings.Contains(allMessageText(req.Messages), "Post-Action Delta Gate") {
			return nil, fmt.Errorf("missing unknown-risky post-action delta gate reminder")
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_unknown_verify",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"git status --short && git diff --name-status && git diff --summary && bash -n scripts/custom-wrapper.sh"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Completed after post-action verification."}}},
			StopReason: "end_turn",
		}, nil
	}
}

func TestSessionDefaultsMaxTurns(t *testing.T) {
	session := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", CWD: t.TempDir()})
	if session.options.MaxTurns != defaults.MaxTurns {
		t.Fatalf("MaxTurns = %d, want %d", session.options.MaxTurns, defaults.MaxTurns)
	}
}

func TestTrivialAnswerDoesNotInjectHeavyClosureOrUseTools(t *testing.T) {
	streamer := &trivialMathStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "1+1=?", &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(streamer.requests))
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("tool calls = %+v, want none", result.ToolCalls)
	}
	if strings.TrimSpace(result.Response) != "2" {
		t.Fatalf("response = %q, want 2", result.Response)
	}
}

func TestReadOnlyClosureBlocksReadmeSummaryWithoutReadEvidence(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("Project README\n"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &readmeNoReadClosureStreamer{}
	querySession := New(streamer, tools.NewRegistry(fileread.New()), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      cwd,
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "读取 README.md 并总结项目做什么", &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(streamer.requests))
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "Read" {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if strings.Contains(result.Response, "I read README.md and summarized it.") {
		t.Fatalf("premature read summary leaked into result: %q", result.Response)
	}
	if strings.Contains(out.String(), "I read README.md and summarized it.") {
		t.Fatalf("premature read summary leaked into output stream: %q", out.String())
	}
	if !strings.Contains(result.Response, "README.md summary") {
		t.Fatalf("missing accepted summary: %q", result.Response)
	}
}

func TestReadOnlyClosureBlocksWrongFileReadForRequestedReadme(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("Project README\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "EXTRA.md"), []byte("Extra doc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &wrongFileReadClosureStreamer{}
	querySession := New(streamer, tools.NewRegistry(fileread.New()), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      cwd,
	})
	result, err := querySession.Run(context.Background(), "读取 README.md 并总结", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(streamer.requests))
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v, want wrong read plus README read", result.ToolCalls)
	}
	if strings.Contains(result.Response, "I read README.md and summarized it.") {
		t.Fatalf("wrong-file premature summary leaked into result: %q", result.Response)
	}
}

func TestReadOnlyClosureBlocksFullFileClaimAfterPartialRead(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("Project README\nLine 2\nLine 3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &partialReadClosureStreamer{}
	querySession := New(streamer, tools.NewRegistry(fileread.New()), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      cwd,
	})
	result, err := querySession.Run(context.Background(), "读取 README.md 并总结", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(streamer.requests))
	}
	if strings.Contains(result.Response, "Full file summary") {
		t.Fatalf("full-file claim leaked into result: %q", result.Response)
	}
	if !strings.Contains(result.Response, "Partial README.md summary") {
		t.Fatalf("missing bounded partial summary: %q", result.Response)
	}
}

func TestFinalClaimGateBlocksTestsPassedWithoutTestEvidence(t *testing.T) {
	streamer := &claimGateStreamer{
		first:    "Tests passed.",
		second:   "Tests were not run.",
		reminder: "tests/checks are claimed",
	}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Summarize the status.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Response, "Tests passed") {
		t.Fatalf("unverified test claim leaked into result: %q", result.Response)
	}
	if !strings.Contains(result.Response, "Tests were not run") {
		t.Fatalf("missing bounded final response: %q", result.Response)
	}
}

// The claim matchers used to run containsAny over the whole final text, so a
// truthful "I have not committed the changes" matched "committed" and the gate
// demanded commit evidence for a report that nothing was committed. The more
// honest the model, the more likely it was blocked, and each retry burned a turn.
func TestFinalTextClaimMatchersIgnoreNegatedClauses(t *testing.T) {
	matchers := map[string]func(string) bool{
		"tested":     finalTextClaimsTested,
		"committed":  finalTextClaimsCommitted,
		"pushed":     finalTextClaimsPushed,
		"tagged":     finalTextClaimsTagged,
		"read":       finalTextClaimsReadEvidence,
		"noIssues":   finalTextClaimsNoIssuesFound,
		"fixed":      finalTextClaimsFixed,
		"searched":   finalTextClaimsSearched,
		"fullRepo":   finalTextClaimsFullRepositoryScope,
		"fullScope":  finalTextClaimsFullScope,
		"readSumm":   finalTextClaimsReadSummary,
		"auditScope": finalTextClaimsFullRepositoryScope,
	}
	tests := []struct {
		matcher string
		text    string
		want    bool
	}{
		{"tested", "Tests passed.", true},
		{"tested", "Tests did not pass.", false},
		{"tested", "I have not run the tests, so tests passed is not something I can say.", false},
		{"tested", "测试通过。", true},
		{"tested", "测试没有通过。", false},

		{"committed", "Committed the change.", true},
		{"committed", "I have not committed the changes.", false},
		{"committed", "I did not commit; nothing was committed.", false},
		{"committed", "已提交。", true},
		{"committed", "我没有提交，也没有已提交的记录。", false},

		{"pushed", "Pushed to main.", true},
		{"pushed", "I have not pushed anything.", false},
		{"pushed", "Never pushed this branch.", false},

		{"tagged", "Tag created.", true},
		{"tagged", "No tag created; I was unable to create the tag.", false},

		{"read", "I read the file.", true},
		{"read", "I did not read the file.", false},

		{"noIssues", "No issues found.", true},
		{"noIssues", "I cannot say no issues found without reading the code.", false},

		{"fixed", "Fixed the bug.", true},
		{"fixed", "The bug is not fixed.", false},

		{"searched", "Searched the package.", true},
		{"searched", "I have not searched the package.", false},

		{"fullRepo", "Scanned the entire repository.", true},
		{"fullRepo", "I did not scan the entire repository.", false},

		{"fullScope", "Read the entire file.", true},
		{"fullScope", "I skipped the entire file.", false},

		{"readSumm", "Summary of the module.", true},
		{"readSumm", "I cannot give a summary yet.", false},

		// A negation governs only its own clause: the commit claim survives, the
		// push claim does not.
		{"committed", "I committed the changes but did not push.", true},
		{"pushed", "I committed the changes but did not push.", false},
		{"committed", "已提交，但没有推送。", true},
		{"pushed", "已提交，但没有推送。", false},

		// One negation in front of a list must suppress every item, which is why
		// commas are not clause separators.
		{"committed", "I have not committed, pushed, or tagged anything.", false},
		{"pushed", "I have not committed, pushed, or tagged anything.", false},
		{"tagged", "I have not committed, pushed, or tagged anything.", false},

		// A negation after the phrase does not retroactively suppress it.
		{"tested", "Tests passed and nothing is not working.", true},
	}
	for _, tt := range tests {
		matcher, ok := matchers[tt.matcher]
		if !ok {
			t.Fatalf("unknown matcher %q", tt.matcher)
		}
		if got := matcher(tt.text); got != tt.want {
			t.Errorf("%s(%q) = %v, want %v", tt.matcher, tt.text, got, tt.want)
		}
	}
}

// Pins the accepted limitation documented on claimedInAffirmativeClause: a
// negation that governs something else in the same clause still suppresses the
// claim. This loses gate coverage on an unusual phrasing, which is the deliberate
// trade for not blocking every honest negative report. Change it consciously.
func TestFinalTextClaimMatchersOverSuppressUnrelatedNegationInSameClause(t *testing.T) {
	if finalTextClaimsTested("This is not a refactor and tests passed") {
		t.Fatal("unrelated in-clause negation no longer suppresses the claim; " +
			"if that is intended, update claimedInAffirmativeClause's documented limitation")
	}
	// A sentence boundary is enough to restore detection.
	if !finalTextClaimsTested("This is not a refactor. Tests passed.") {
		t.Fatal("sentence boundary should isolate the negation")
	}
}

func TestFinalClaimGateAllowsProjectDeploymentStatusDescription(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			name: "status in sentence",
			text: "项目已经通过 VitePress 构建了静态网站，已部署在 GitHub Pages 上：https://example.com",
		},
		{
			name: "status at sentence start",
			text: "已部署在 GitHub Pages 上的是项目官网，不是我刚执行的部署。",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streamer := &claimGateStreamer{
				first:    tt.text,
				second:   "should not be requested",
				reminder: "external deploy/publish/send",
			}
			querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
				Model:    "test",
				MaxTurns: 2,
				CWD:      t.TempDir(),
			})
			result, err := querySession.Run(context.Background(), "这个项目是干嘛的？", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if len(streamer.requests) != 1 {
				t.Fatalf("requests = %d, want 1", len(streamer.requests))
			}
			if !strings.Contains(result.Response, "已部署在 GitHub Pages") {
				t.Fatalf("project deployment status should pass through: %q", result.Response)
			}
		})
	}
}

func TestFinalClaimGateStillBlocksFirstPersonExternalActionClaim(t *testing.T) {
	streamer := &claimGateStreamer{
		first:    "I deployed it to production.",
		second:   "I did not deploy it.",
		reminder: "external deploy/publish/send",
	}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Summarize the status.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	if strings.Contains(result.Response, "I deployed it to production.") {
		t.Fatalf("unsupported external action claim leaked into result: %q", result.Response)
	}
	if !strings.Contains(result.Response, "I did not deploy it.") {
		t.Fatalf("missing bounded final response: %q", result.Response)
	}
}

func TestFailedAuditEvidenceGateBlocksUnsupportedWholeScopeConclusion(t *testing.T) {
	streamer := &failedAuditGateStreamer{}
	bash := &failedAuditBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "你感觉现在缺啥内容？最缺的？为啥？", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(streamer.requests))
	}
	if len(bash.commands) != 2 {
		t.Fatalf("commands = %+v, want failed broad audit plus scoped audit", bash.commands)
	}
	if strings.Contains(result.Response, "数据摆完了") || strings.Contains(result.Response, "唯一只有 1 个文件") {
		t.Fatalf("unsupported whole-scope claim leaked into result: %q", result.Response)
	}
	if !strings.Contains(result.Response, "全局统计命令失败") || !strings.Contains(result.Response, "局部证据") {
		t.Fatalf("missing bounded final response: %q", result.Response)
	}
}

func TestCompletionGateBlockedCandidateIsNotRecordedAsAcceptedAssistant(t *testing.T) {
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(t.TempDir(), "62626262-6262-4626-8626-626262626262")
	if err != nil {
		t.Fatal(err)
	}
	streamer := &claimGateStreamer{
		first:    "Tests passed.",
		second:   "Tests were not run.",
		reminder: "tests/checks are claimed",
	}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
		Recorder: recorder,
	})
	if _, err := querySession.Run(context.Background(), "Summarize the status.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	transcript := string(raw)
	if strings.Contains(transcript, `"role":"assistant","content":"Tests passed."`) {
		t.Fatalf("blocked candidate was recorded as accepted assistant:\n%s", transcript)
	}
	if !strings.Contains(transcript, `"role":"assistant","content":"Tests were not run."`) {
		t.Fatalf("accepted bounded answer missing from transcript:\n%s", transcript)
	}
	for _, want := range []string{`\"rule_id\":\"final_claim\"`, `\"missing_evidence\":[`, `tests/checks are claimed`} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("completion gate missing structured detail %s:\n%s", want, transcript)
		}
	}
}

func TestCompletionGateReturnsRuleIDsAndMissingEvidence(t *testing.T) {
	tests := []struct {
		name      string
		contract  taskContract
		calls     []ToolTrace
		finalText string
		wantRule  string
		wantMiss  string
	}{
		{
			name:      "read scope missing evidence",
			contract:  taskContract{ClosureLevel: closureLevelReadOnlyScoped, RequiredReadPath: []string{"README.md"}},
			finalText: "I read README.md and summarized the full file.",
			wantRule:  "read_scope",
			wantMiss:  "required read evidence: README.md",
		},
		{
			name: "post action delta missing checks",
			calls: []ToolTrace{{
				Name:        "Edit",
				FileChanges: []tools.FileChange{{Path: "config.txt", BeforeExists: true, AfterExists: true}},
			}},
			finalText: "Fixed.",
			wantRule:  "post_action_delta",
			wantMiss:  "scope check: git status --short and git diff --name-status",
		},
		{
			name:      "final claim missing test evidence",
			finalText: "Tests passed.",
			wantRule:  "final_claim",
			wantMiss:  "tests/checks are claimed",
		},
		{
			name: "failed broad audit missing replacement",
			contract: taskContract{
				ClosureLevel: closureLevelInvestigation,
			},
			calls: []ToolTrace{
				{
					Name:    "Bash",
					Input:   `{"command":"for dir in 0-start-here 1-understand-ai 3-ai-agents; do total=$(find \"$dir\" -name \"*.md\" 2>/dev/null | wc -l); echo \"$dir: $total\"; done"}`,
					Output:  "write path /dev/null is outside the current workspace and configured additional directories",
					IsError: true,
				},
				{
					Name:   "Bash",
					Input:  `{"command":"find 3-ai-agents -name \"*.md\" | wc -l"}`,
					Output: "1",
				},
			},
			finalText: "数据摆完了，3-ai-agents 是六大主目录里唯一只有 1 个文件的目录。",
			wantRule:  "failed_audit_evidence",
			wantMiss:  "successful broad read/search audit after failed command",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := completionGate(tt.contract, tt.calls, tt.finalText)
			if got.Severity != gateSeverityBlockContinue {
				t.Fatalf("Severity = %s, want %s; result=%+v", got.Severity, gateSeverityBlockContinue, got)
			}
			if got.RuleID != tt.wantRule {
				t.Fatalf("RuleID = %q, want %q; reminder=%s", got.RuleID, tt.wantRule, got.Reminder)
			}
			if !containsSubstring(got.MissingEvidence, tt.wantMiss) {
				t.Fatalf("MissingEvidence = %+v, want substring %q", got.MissingEvidence, tt.wantMiss)
			}
		})
	}
}

func TestFailedAuditEvidenceGateAllowsReplacementOrDisclosure(t *testing.T) {
	failedBroad := ToolTrace{
		Name:    "Bash",
		Input:   `{"command":"for dir in 0-start-here 1-understand-ai 3-ai-agents; do find \"$dir\" -name \"*.md\" 2>/dev/null | wc -l; done"}`,
		Output:  "write path /dev/null is outside the current workspace and configured additional directories",
		IsError: true,
	}
	successBroad := ToolTrace{
		Name:   "Bash",
		Input:  `{"command":"for dir in 0-start-here 1-understand-ai 3-ai-agents; do find \"$dir\" -name \"*.md\" | wc -l; done"}`,
		Output: "0-start-here 15\n1-understand-ai 32\n3-ai-agents 1",
	}
	contract := taskContract{ClosureLevel: closureLevelInvestigation}
	if got := completionGate(contract, []ToolTrace{failedBroad, successBroad}, "数据摆完了，3-ai-agents 是最缺的。"); got.Severity != gateSeverityAllow {
		t.Fatalf("replacement broad audit should allow completion: %+v", got)
	}
	if got := completionGate(contract, []ToolTrace{failedBroad}, "全局统计命令失败，所以这里只能给出局部证据结论。"); got.Severity != gateSeverityAllow {
		t.Fatalf("explicit failed-audit disclosure should allow bounded completion: %+v", got)
	}
}

func containsSubstring(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}

func TestFinalClaimGateBlocksPushedWithoutPushEvidence(t *testing.T) {
	streamer := &claimGateStreamer{
		first:    "Pushed to origin.",
		second:   "Remote push remains unverified.",
		reminder: "push is claimed",
	}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Summarize the git status.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Response, "Pushed to origin") {
		t.Fatalf("unverified push claim leaked into result: %q", result.Response)
	}
	if !strings.Contains(result.Response, "Remote push remains unverified") {
		t.Fatalf("missing bounded final response: %q", result.Response)
	}
}

func TestPostActionDeltaGateBlocksCompletionUntilChecksRun(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "config.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &postEditDeltaGateStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(fileedit.New(), bash), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      cwd,
	})
	result, err := querySession.Run(context.Background(), "Fix config.txt", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(streamer.requests))
	}
	if strings.Contains(result.Response, "Fixed.") && !strings.Contains(result.Response, "Fixed and verified") {
		t.Fatalf("unverified fixed claim leaked: %q", result.Response)
	}
	if len(bash.commands) != 1 || !strings.Contains(bash.commands[0], "git diff --summary") {
		t.Fatalf("delta verification command not run: %+v", bash.commands)
	}
}

func TestPreCommitScopeGateBlocksCommitToolCall(t *testing.T) {
	cwd := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(cwd, "64646464-6464-4646-8646-646464646464")
	if err != nil {
		t.Fatal(err)
	}
	streamer := &preCommitBlockedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      cwd,
		Recorder: recorder,
	})
	result, err := querySession.Run(context.Background(), "Commit the current change.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 1 || bash.commands[0] != preCommitScopePreflightCommand {
		t.Fatalf("git commit should run only the preflight before retry, commands=%+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, `<gate-preflight rule_id="pre_commit_scope"`) || !strings.Contains(result.ToolCalls[0].Output, "Original command was not executed") {
		t.Fatalf("preflight commit trace = %+v", result.ToolCalls)
	}
	if got := bashCommandFromTrace(result.ToolCalls[0]); got != preCommitScopePreflightCommand {
		t.Fatalf("preflight trace command = %q, want %q", got, preCommitScopePreflightCommand)
	}
	raw, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `\"rule_id\":\"pre_commit_scope\"`) {
		t.Fatalf("pre-tool gate transcript missing rule_id:\n%s", string(raw))
	}
	if !strings.Contains(string(raw), `\"action\":\"auto_preflight\"`) {
		t.Fatalf("pre-tool gate transcript missing auto_preflight action:\n%s", string(raw))
	}
	for _, want := range []string{
		`<gate-preflight rule_id="pre_commit_scope"`,
		preCommitScopePreflightCommand,
		"Original command was not executed",
	} {
		if !strings.Contains(result.ToolCalls[0].Output, want) {
			t.Fatalf("pre-commit reminder missing %q:\n%s", want, result.ToolCalls[0].Output)
		}
	}
}

func TestPreCommitScopeGateBlocksChainedVerificationAndCommit(t *testing.T) {
	streamer := &chainedPreCommitBlockedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Verify staged files and commit.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 0 {
		t.Fatalf("chained verify-and-commit should have been blocked before Bash execution, commands=%+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, "Do not chain this verification with git commit") {
		t.Fatalf("blocked chained commit trace = %+v", result.ToolCalls)
	}
}

func TestPreCommitScopeGateRejectsPartialStagedScopeVerification(t *testing.T) {
	streamer := &partialStagedScopeBlockedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "提交当前已经 staged 的改动", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands := []string{"git status --short && git diff --cached --name-status", preCommitScopePreflightCommand}
	if !reflect.DeepEqual(bash.commands, wantCommands) {
		t.Fatalf("partial staged verification should be followed by full preflight only, commands=%+v", bash.commands)
	}
	if len(result.ToolCalls) != 2 || result.ToolCalls[1].IsError || !strings.Contains(result.ToolCalls[1].Output, `<gate-preflight rule_id="pre_commit_scope"`) || !strings.Contains(result.ToolCalls[1].Output, "Original command was not executed") {
		t.Fatalf("commit after partial staged scope should run full preflight without committing, traces=%+v", result.ToolCalls)
	}
}

func TestPushClaimGateRequiresPostPushVerification(t *testing.T) {
	streamer := &pushClaimGateStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 5,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Push the current branch.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 3 {
		t.Fatalf("commands = %+v, want precheck push postcheck", bash.commands)
	}
	if !strings.Contains(bash.commands[0], "@{u}") {
		t.Fatalf("push precheck did not include upstream/remote evidence: %+v", bash.commands)
	}
	if strings.Contains(result.Response, "Pushed to origin.") && !strings.Contains(result.Response, "verified remote state") {
		t.Fatalf("unverified push final leaked: %q", result.Response)
	}
}

func TestExternalActionGateBlocksUnauthorizedDeployCommand(t *testing.T) {
	streamer := &externalActionBlockedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Summarize status only.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 0 {
		t.Fatalf("external deploy should have been blocked before Bash execution, commands=%+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || !strings.Contains(result.ToolCalls[0].Output, "External Action Gate") {
		t.Fatalf("blocked external action trace = %+v", result.ToolCalls)
	}
}

func TestReadOnlyClosureBlocksDirectorySummaryWithoutReadEvidence(t *testing.T) {
	streamer := &directoryNoReadClosureStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "读取 internal/query 并总结", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(streamer.requests))
	}
	if len(bash.commands) != 1 || bash.commands[0] != "ls internal/query" {
		t.Fatalf("directory evidence command not run: %+v", bash.commands)
	}
	if strings.Contains(result.Response, "I inspected internal/query") {
		t.Fatalf("premature directory summary leaked into result: %q", result.Response)
	}
}

func TestReadOnlyGitTagListIsNotBlockedAsSharedStateMutation(t *testing.T) {
	streamer := &gitTagListAllowedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "列出当前仓库 tag，不要修改", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 1 || bash.commands[0] != "git tag --list" {
		t.Fatalf("git tag --list should have executed, commands=%+v", bash.commands)
	}
	if strings.Contains(result.Response, "Shared-State Git Gate") {
		t.Fatalf("read-only tag list was blocked: %q", result.Response)
	}
}

func TestDestructiveGitGateBlocksUnauthorizedForcePush(t *testing.T) {
	streamer := &forcePushBlockedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Push current branch.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 0 {
		t.Fatalf("force push should have been blocked before Bash execution, commands=%+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || !strings.Contains(result.ToolCalls[0].Output, "Destructive Shared-State Gate") {
		t.Fatalf("blocked force-push trace = %+v", result.ToolCalls)
	}
}

func TestPreToolClosureGateReturnsRuleIDs(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		command string
		want    string
	}{
		{
			name:    "commit requires scope check",
			prompt:  "Commit current changes.",
			command: "git commit -m test",
			want:    "pre_commit_scope",
		},
		{
			name:    "destructive shared state wins before push precheck",
			prompt:  "Push current branch.",
			command: "git push --force-with-lease origin main",
			want:    "destructive_shared_state",
		},
		{
			name:    "authorized destructive push still requires shared-state evidence",
			prompt:  "Force push current branch with --force-with-lease.",
			command: "git push --force-with-lease origin main",
			want:    "shared_state_git_push",
		},
		{
			name:    "tag mutation requires tag evidence",
			prompt:  "Create release tag.",
			command: "git tag v1.2.3",
			want:    "shared_state_git_tag",
		},
		{
			name:    "external action requires prompt authorization",
			prompt:  "Summarize status only.",
			command: "gh release create v1.2.3",
			want:    "external_action_authorization",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"command":%q}`, tt.command)
			got := preToolClosureGate(taskContract{}, tt.prompt, nil, "Bash", input)
			if got.Severity != gateSeverityBlockTool {
				t.Fatalf("Severity = %s, want %s; result=%+v", got.Severity, gateSeverityBlockTool, got)
			}
			if got.RuleID != tt.want {
				t.Fatalf("RuleID = %q, want %q; reminder=%s", got.RuleID, tt.want, got.Reminder)
			}
		})
	}
}

func TestPreToolGateRulesArePriorityOrdered(t *testing.T) {
	if len(preToolGateRules) == 0 {
		t.Fatal("preToolGateRules is empty")
	}
	for i := 1; i < len(preToolGateRules); i++ {
		prev := preToolGateRules[i-1]
		cur := preToolGateRules[i]
		if prev.Priority < cur.Priority {
			t.Fatalf("preToolGateRules not sorted by descending priority at %d: %s=%d before %s=%d", i, prev.ID, prev.Priority, cur.ID, cur.Priority)
		}
	}
}

func TestPreCommitScopeGateAllowsExistingStagedScopeAfterVerification(t *testing.T) {
	streamer := &stagedCommitAllowedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "提交当前已经 staged 的改动", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 2 {
		t.Fatalf("commands = %+v, want staged scope check and commit", bash.commands)
	}
	if !strings.Contains(bash.commands[1], "git commit") {
		t.Fatalf("commit did not execute after staged scope verification: %+v", bash.commands)
	}
	if !strings.Contains(result.Response, "Committed staged changes") {
		t.Fatalf("missing committed response: %q", result.Response)
	}
}

// Fix A: a git add that re-stages an already-edited file must not invalidate a
// completed full staged-scope verification. This reproduces the version-bump
// loop where edit → full-verify → git add → commit was blocked forever because
// git add reset the "last delta" pointer past the verification.
func TestPreCommitScopeGateAllowsCommitWhenGitAddFollowsFullVerification(t *testing.T) {
	calls := []ToolTrace{
		{Name: "Write", FileChanges: []tools.FileChange{{Path: "README.md", BeforeExists: true, AfterExists: true}}},
		{Name: "Bash", Input: `{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status"}`},
		{Name: "Bash", Input: `{"command":"git add README.md"}`},
	}
	got := preToolClosureGate(taskContract{}, "把 README 版本号改成 1.0.1 然后提交", calls, "Bash", `{"command":"git commit -m bump"}`)
	if got.Severity != gateSeverityAllow {
		t.Fatalf("git add after a full scope verification must not re-block the commit; got severity=%s rule=%q", got.Severity, got.RuleID)
	}
}

// A commit with no staged-scope verification at all must still block, so Fix A
// does not weaken the gate's core purpose.
func TestPreCommitScopeGateStillBlocksCommitWithoutAnyVerification(t *testing.T) {
	calls := []ToolTrace{
		{Name: "Write", FileChanges: []tools.FileChange{{Path: "README.md", BeforeExists: true, AfterExists: true}}},
		{Name: "Bash", Input: `{"command":"git add README.md"}`},
	}
	got := preToolClosureGate(taskContract{}, "把 README 版本号改成 1.0.1 然后提交", calls, "Bash", `{"command":"git commit -m bump"}`)
	if got.Severity != gateSeverityBlockTool || got.RuleID != "pre_commit_scope" {
		t.Fatalf("commit without any scope verification must still block; got severity=%s rule=%q", got.Severity, got.RuleID)
	}
}

// Fix B: auto-preflight must fire for git-only compound commands (the model's
// natural `cd … && git add … && git commit … && git push` one-liner), while
// still refusing to auto-preflight a chained read-only verification+commit
// (which must keep its "do not chain" teaching block) or any pipeline that
// mixes in a non-git command.
func TestCanAutoPreflightGitCommandHandlesGitOnlyCompoundCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		sub     string
		want    bool
	}{
		{"single commit still auto-preflights", "git commit -m x", "commit", true},
		{"cd + add + commit requires post-stage readback", "cd /repo && git add README.md && git commit -m x", "commit", false},
		{"cd + add + commit + push auto-preflights push", "cd /repo && git add README.md && git commit -m x && git push", "push", true},
		{"chained read-only verify + commit is not auto-preflighted", "git diff --cached --name-status && git commit -m x", "commit", false},
		{"non-git dangerous segment is rejected", "rm -rf build && git commit -m x", "commit", false},
		{"target subcommand absent", "cd /repo && git add README.md && git commit -m x", "push", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canAutoPreflightGitCommand(tt.command, tt.sub); got != tt.want {
				t.Fatalf("canAutoPreflightGitCommand(%q, %q) = %v, want %v", tt.command, tt.sub, got, tt.want)
			}
		})
	}
}

func TestCompoundGitPushOneLinerCannotStageAfterScopeReadback(t *testing.T) {
	streamer := &compoundPushOneLinerStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "把 README 版本号改成 1.0.1，然后提交并 push", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 0 {
		t.Fatalf("compound staging/commit command must not execute before a post-stage readback: %+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, "Do not chain") {
		t.Fatalf("compound staging/commit trace=%+v", result.ToolCalls)
	}
}

func TestCommitClaimGateRequiresPostCommitVerification(t *testing.T) {
	streamer := &commitPostcheckGateStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 5,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "提交当前已经 staged 的改动", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 3 {
		t.Fatalf("commands = %+v, want staged scope, commit, postcheck", bash.commands)
	}
	if strings.Contains(result.Response, "Committed.") && !strings.Contains(result.Response, "verified") {
		t.Fatalf("unverified commit final leaked: %q", result.Response)
	}
}

func TestTagMutationRequiresTagStatePrecheck(t *testing.T) {
	streamer := &tagPrecheckBlockedStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "创建 v1.2.3 tag", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	wantPreflight := "git status --short --branch && git rev-parse HEAD && (git tag --list || git show-ref --tags)"
	if len(bash.commands) != 1 || bash.commands[0] != wantPreflight {
		t.Fatalf("tag mutation should run only tag-state preflight, commands=%+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, `<gate-preflight rule_id="shared_state_git_tag"`) || !strings.Contains(result.ToolCalls[0].Output, "Original command was not executed") {
		t.Fatalf("tag preflight trace = %+v", result.ToolCalls)
	}
}

func TestTagClaimGateRequiresPostTagVerification(t *testing.T) {
	streamer := &tagPostcheckGateStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 5,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "创建 v1.2.3 tag", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 3 {
		t.Fatalf("commands = %+v, want tag precheck, tag, postcheck", bash.commands)
	}
	if strings.Contains(result.Response, "Created tag v1.2.3.") && !strings.Contains(result.Response, "verified object state") {
		t.Fatalf("unverified tag final leaked: %q", result.Response)
	}
}

func TestUnknownRiskyBashRequiresPostActionVerificationBeforeCompletion(t *testing.T) {
	streamer := &unknownRiskyBashDeltaStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "Run the local wrapper and finish.", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(bash.commands) != 2 {
		t.Fatalf("commands = %+v, want risky command plus verification", bash.commands)
	}
	if strings.Contains(result.Response, "Completed.") && !strings.Contains(result.Response, "post-action verification") {
		t.Fatalf("unverified unknown-risky completion leaked: %q", result.Response)
	}
}

func TestGitTagCommandClassificationKeepsReadOnlyListingSeparate(t *testing.T) {
	tests := []struct {
		command  string
		mutates  bool
		destroys bool
	}{
		{command: "git tag --list", mutates: false},
		{command: "git tag --list 'v*'", mutates: false},
		{command: "git tag -n99", mutates: false},
		{command: "git tag --points-at HEAD", mutates: false},
		{command: "git tag v1.2.3", mutates: true},
		{command: "git tag -a v1.2.3 -m release", mutates: true},
		{command: "git tag -f v1.2.3 HEAD", mutates: true, destroys: true},
		{command: "git push --force origin main", mutates: true, destroys: true},
		{command: "git push --dry-run origin main", mutates: false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if got := looksLikeMutatingGitSharedStateCommand(tt.command); got != tt.mutates {
				t.Fatalf("mutating = %v, want %v", got, tt.mutates)
			}
			if got := looksLikeDestructiveGitSharedStateCommand(tt.command); got != tt.destroys {
				t.Fatalf("destructive = %v, want %v", got, tt.destroys)
			}
		})
	}
}

func TestShellIntentClassifiesHighRiskBashCommands(t *testing.T) {
	tests := []struct {
		command     string
		wantKind    shellIntentKind
		readOnly    bool
		mutates     bool
		destructive bool
		external    bool
		dryRun      bool
	}{
		{command: "git status --short --branch", wantKind: shellIntentReadOnly, readOnly: true},
		{command: "git diff --cached --name-status", wantKind: shellIntentReadOnly, readOnly: true},
		{command: "git tag --list 'v*'", wantKind: shellIntentGitTagRead, readOnly: true},
		{command: "git -C /repo tag --list 'v*'", wantKind: shellIntentGitTagRead, readOnly: true},
		{command: "git tag -a v1.2.3 -m release", wantKind: shellIntentGitTagMutate, mutates: true},
		{command: "/usr/bin/git commit -m release", wantKind: shellIntentGitCommit, mutates: true},
		{command: "git push origin HEAD", wantKind: shellIntentGitPush, mutates: true},
		{command: "git push --force-with-lease origin main", wantKind: shellIntentGitDestructiveSharedState, mutates: true, destructive: true},
		{command: "npm publish --dry-run", wantKind: shellIntentExternalWriteDryRunOrPreview, readOnly: true, external: true, dryRun: true},
		{command: "gh release create v1.2.3", wantKind: shellIntentExternalWrite, mutates: true, external: true},
		{command: "curl -X POST https://example.invalid/hook -d '{}'", wantKind: shellIntentExternalWrite, mutates: true, external: true},
		{command: "wc -c 4-advanced-topics/model-deployment.md", wantKind: shellIntentReadOnly, readOnly: true},
		{command: "rg publish docs/release-notes.md", wantKind: shellIntentReadOnly, readOnly: true},
		{command: "sed -i '' 's/a/b/' README.md", wantKind: shellIntentWorkspaceWrite, mutates: true},
		{command: "go test ./internal/query -count=1", wantKind: shellIntentVerification, readOnly: true},
		{command: "./scripts/custom-wrapper.sh --maybe-mutates", wantKind: shellIntentUnknownRisky},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := classifyShellIntent(tt.command)
			if got.Kind != tt.wantKind {
				t.Fatalf("kind = %s, want %s", got.Kind, tt.wantKind)
			}
			if got.ReadOnly != tt.readOnly || got.Mutates != tt.mutates || got.Destructive != tt.destructive || got.External != tt.external || got.DryRun != tt.dryRun {
				t.Fatalf("intent flags = %+v, want readOnly=%v mutates=%v destructive=%v external=%v dryRun=%v", got, tt.readOnly, tt.mutates, tt.destructive, tt.external, tt.dryRun)
			}
		})
	}
}

func TestShellIntentDoesNotTreatQuotedGitAsSharedState(t *testing.T) {
	got := classifyShellIntent("rg -n 'git commit' internal/query")
	switch got.Kind {
	case shellIntentGitCommit, shellIntentGitPush, shellIntentGitTagMutate, shellIntentGitDestructiveSharedState:
		t.Fatalf("quoted Git text classified as shared-state intent: %+v", got)
	}
}

func TestPathsForTraceIgnoresGitCommitAuthorEmail(t *testing.T) {
	trace := ToolTrace{
		Name:  "Bash",
		Input: `{"command":"git commit --author=\"konglong87 <developer@example.com>\" -m \"feat: test\""}`,
	}
	if got := pathsForTrace(trace); len(got) != 0 {
		t.Fatalf("git commit author email should not be recorded as paths, got %+v", got)
	}
}

func TestClosureEventsAreRecordedInTranscript(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "config.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(cwd, "63636363-6363-4636-8636-636363636363")
	if err != nil {
		t.Fatal(err)
	}
	streamer := &postEditDeltaGateStreamer{}
	bash := &closureBashTool{}
	querySession := New(streamer, tools.NewRegistry(fileedit.New(), bash), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      cwd,
		Recorder: recorder,
	})
	if _, err := querySession.Run(context.Background(), "Fix config.txt", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	transcript := string(raw)
	for _, want := range []string{`"type":"task_contract"`, `"type":"action_record"`, `"type":"evidence_record"`, `"type":"delta_record"`, `"type":"completion_gate"`} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript missing %s:\n%s", want, transcript)
		}
	}
	for _, want := range []string{`\"Reasons\":[`, `\"rule_id\":\"code_change\"`, `\"Confidence\":\"high\"`} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("task_contract missing classification detail %s:\n%s", want, transcript)
		}
	}
}

func TestCompletionVerificationNudgeRequiresRequestedCommands(t *testing.T) {
	streamer := &completionVerificationStreamer{}
	querySession := New(streamer, tools.NewRegistry(verificationBashTool{}), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "Fix email masking. Run go test ./... -count=1 and git diff --check before finishing. Final answer must include ENGINEERING_EMAIL_MASK_DONE.", &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(streamer.requests))
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "Bash" || result.ToolCalls[0].IsError {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if !strings.Contains(result.ToolCalls[0].Input, "go test ./... -count=1") || !strings.Contains(result.ToolCalls[0].Input, "git diff --check") {
		t.Fatalf("verification command not run: %+v", result.ToolCalls[0])
	}
	if strings.Contains(result.Response, "Tests passed.\nENGINEERING_EMAIL_MASK_DONE") {
		t.Fatalf("premature completion leaked into result response: %q", result.Response)
	}
	if !strings.Contains(result.Response, "Verified now") || !strings.Contains(out.String(), "Verified now") {
		t.Fatalf("final verified response missing: result=%q out=%q", result.Response, out.String())
	}
}

// loopingToolStreamer always returns the identical tool call, reproducing the
// degenerate repetition loop observed with weak providers (deepseek-v4-flash).
type loopingToolStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *loopingToolStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    fmt.Sprintf("toolu_%d", len(s.requests)),
			Name:  "Echo",
			Input: json.RawMessage(`{"text":"same"}`),
		}}},
		StopReason: "tool_use",
	}, nil
}

func TestLoopGuardAbortsPersistentIdenticalToolCall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &loopingToolStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   20,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "loop please", io.Discard)
	if err == nil {
		t.Fatalf("expected loop guard abort error, got nil (result=%+v)", result)
	}
	if !strings.Contains(err.Error(), "loop") {
		t.Fatalf("error %q does not look like a loop guard abort", err.Error())
	}
	// Two-stage guard: soft nudge at the 3rd no-progress turn, hard abort at the
	// 6th. The model must be called exactly 6 times, never running to MaxTurns.
	if len(streamer.requests) != 6 {
		t.Fatalf("model calls = %d, want 6 (hard limit), MaxTurns=20", len(streamer.requests))
	}
	// turn 4's request (index 3) carries the escalating loop-awareness section:
	if !strings.Contains(allMessageText(streamer.requests[3].Messages), "Loop check") {
		t.Fatalf("loop-awareness section missing from turn 4 request:\n%s", allMessageText(streamer.requests[3].Messages))
	}
}

// loopThenRecoverStreamer repeats the identical tool call until it sees the
// loop-awareness section, then produces a final answer — the intended
// recovery path.
type loopThenRecoverStreamer struct {
	requests []anthropic.MessagesRequest
}

// streamer: recover when it sees the loop-awareness section
func (s *loopThenRecoverStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if strings.Contains(allMessageText(req.Messages), "Loop check") {
		if err := cb.OnText("好的，我改用直接回答。答案是 42。"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "好的，我改用直接回答。答案是 42。"}}},
			StopReason: "end_turn",
		}, nil
	}
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: "tool_use", ID: fmt.Sprintf("toolu_%d", len(s.requests)), Name: "Echo", Input: json.RawMessage(`{"text":"same"}`),
		}}},
		StopReason: "tool_use",
	}, nil
}

func TestLoopGuardNudgeLetsModelRecover(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &loopThenRecoverStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   20,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "loop please", &out)
	if err != nil {
		t.Fatalf("recovery run should not error: %v", err)
	}
	// Loop-awareness section appears once the streak reaches 2, i.e. on turn
	// 3's request; the model recovers immediately on that turn.
	if len(streamer.requests) != 3 {
		t.Fatalf("model calls = %d, want 3 (2 loops + recovery)", len(streamer.requests))
	}
	if !strings.Contains(result.Response, "答案是 42") || !strings.Contains(out.String(), "答案是 42") {
		t.Fatalf("final recovered answer missing: result=%q out=%q", result.Response, out.String())
	}
}

// alternatingLoopStreamer oscillates between two distinct tool calls (A, B, A,
// B, …), each returning a constant result — the classic A-B-A-B loop that makes
// no progress but is not a single repeated call.
type alternatingLoopStreamer struct{ requests []anthropic.MessagesRequest }

func (s *alternatingLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	text := "A"
	if len(s.requests)%2 == 0 {
		text = "B"
	}
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    fmt.Sprintf("toolu_%d", len(s.requests)),
			Name:  "Echo",
			Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, text)),
		}}},
		StopReason: "tool_use",
	}, nil
}

func TestLoopGuardAbortsAlternatingToolCallLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &alternatingLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 20,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "alternate please", io.Discard)
	if err == nil {
		t.Fatalf("expected loop guard abort for A-B-A-B loop, got nil (result=%+v)", result)
	}
	if result.StopReason != "loop_guard_abort" {
		t.Fatalf("StopReason = %q, want loop_guard_abort", result.StopReason)
	}
	// A,B,A,B,A,B,A: first repeat at turn 3, hard limit (6) reached at turn 7.
	if len(streamer.requests) != 7 {
		t.Fatalf("model calls = %d, want 7 — A-B-A-B alternating loop not bounded", len(streamer.requests))
	}
}

// cyclicLoopStreamer cycles through four distinct tool calls (A,B,C,D,A,B,C,D…),
// each with a constant result — a period-4 loop that a single windowed
// no-progress rule must catch just like A-B-A-B, without any period-specific
// logic.
type cyclicLoopStreamer struct{ requests []anthropic.MessagesRequest }

func (s *cyclicLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	text := []string{"A", "B", "C", "D"}[(len(s.requests)-1)%4]
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    fmt.Sprintf("toolu_%d", len(s.requests)),
			Name:  "Echo",
			Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, text)),
		}}},
		StopReason: "tool_use",
	}, nil
}

func TestLoopGuardAbortsPeriodFourToolCallLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &cyclicLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 30,
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "cycle please", io.Discard)
	if err == nil {
		t.Fatalf("expected loop guard abort for A-B-C-D loop, got nil (result=%+v)", result)
	}
	if result.StopReason != "loop_guard_abort" {
		t.Fatalf("StopReason = %q, want loop_guard_abort", result.StopReason)
	}
	// A,B,C,D,A,B,C,D,A: first repeat at turn 5, hard limit (6) reached at turn 9.
	if len(streamer.requests) != 9 {
		t.Fatalf("model calls = %d, want 9 — period-4 loop not bounded by the windowed rule", len(streamer.requests))
	}
}

// pollingTool returns a different result on every call with the same input,
// emulating a legitimate poll/wait on external state that is making progress.
type pollingTool struct{ calls *int }

func (pollingTool) Name() string        { return "Poll" }
func (pollingTool) Description() string { return "poll changing status" }
func (pollingTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
}
func (t pollingTool) Run(_ context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
	*t.calls++
	return tools.Result{Content: fmt.Sprintf("status: attempt %d, still running", *t.calls)}
}

// pollingStreamer issues the identical Poll call for several turns, then a final
// answer. Because the tool's result changes each turn, this is progress, not a
// stuck loop.
type pollingStreamer struct{ requests []anthropic.MessagesRequest }

func (s *pollingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) <= 8 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    fmt.Sprintf("toolu_%d", len(s.requests)),
				Name:  "Poll",
				Input: json.RawMessage(`{"id":"job-1"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done polling"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done polling"}}},
		StopReason: "end_turn",
	}, nil
}

func TestLoopGuardIgnoresPollingWithChangingResults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &pollingStreamer{}
	querySession := New(streamer, tools.NewRegistry(pollingTool{calls: new(int)}), Options{
		Model:    "test",
		MaxTurns: 20,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "poll job-1 until done", &out)
	if err != nil {
		t.Fatalf("polling with changing results must not be aborted: %v", err)
	}
	// 8 identical-input polls (results differ each turn) + 1 final answer. The
	// guard must NOT fire: same input but real progress (new results).
	if len(streamer.requests) != 9 {
		t.Fatalf("model calls = %d, want 9 (8 polls + final); guard falsely aborted a progressing poll", len(streamer.requests))
	}
	if !strings.Contains(result.Response, "done polling") {
		t.Fatalf("final answer missing: %q", result.Response)
	}
}

// staticReadTool mimics reading an unchanging file: same content every call,
// like the static SKILL.md in the real deepseek-v4-flash loop.
type staticReadTool struct{}

func (staticReadTool) Name() string        { return "Read" }
func (staticReadTool) Description() string { return "read a file" }
func (staticReadTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"},"limit":{"type":"number"}}}`)
}
func (staticReadTool) Run(_ context.Context, _ json.RawMessage, _ tools.Context) tools.Result {
	return tools.Result{Content: "--- name: steve-jobs-perspective\ndescription: |\n  ...static template content...\n"}
}

// realWorldLoopStreamer faithfully replays the observed bug: the identical Read
// of a static file every turn, each with a fresh (unique) tool-call id, exactly
// as deepseek-v4-flash produced 77 times in session aa8e8f5b.
type realWorldLoopStreamer struct{ requests []anthropic.MessagesRequest }

func (s *realWorldLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
			{Type: "text", Text: "我已经收集了足够的梁文锋信息，现在创建他的 skill。让我先查看现有专家的结构模板："},
			{
				Type:  "tool_use",
				ID:    fmt.Sprintf("call_%d", len(s.requests)), // unique id per turn, like the real provider
				Name:  "Read",
				Input: json.RawMessage(`{"file_path":"experts/steve-jobs-perspective/SKILL.md","limit":200}`),
			},
		}},
		StopReason: "tool_use",
	}, nil
}

func TestLoopGuardBoundsRealWorldSkillReadLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &realWorldLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(staticReadTool{}), Options{
		Model:    "deepseek-v4-flash", // the model from the real incident
		MaxTurns: 100,                 // the real default that let it reach 77 loops
		CWD:      t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), "继续帮我创建 梁文锋 skill", io.Discard)
	if err == nil {
		t.Fatalf("expected loop guard abort, got nil (result=%+v)", result)
	}
	// Was 77+ (heading to 100) before the fix; must now be bounded at 6.
	if len(streamer.requests) != 6 {
		t.Fatalf("model calls = %d, want 6 — real-world identical Read loop not bounded", len(streamer.requests))
	}
	if result.StopReason != "loop_guard_abort" {
		t.Fatalf("StopReason = %q, want loop_guard_abort", result.StopReason)
	}
}

// gatedTextLoopStreamer never calls a tool and always closes with the same
// completion claim, so the completion gate rejects every turn. This is the
// text-only degenerate loop: it never reaches the tool-execution loop guard.
type gatedTextLoopStreamer struct{ requests []anthropic.MessagesRequest }

func (s *gatedTextLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	const text = "Tests passed.\nENGINEERING_EMAIL_MASK_DONE"
	if err := cb.OnText(text); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

const gatedLoopPrompt = "Fix email masking. Run go test ./... -count=1 and git diff --check before finishing. Final answer must include ENGINEERING_EMAIL_MASK_DONE."

func TestLoopGuardAbortsGatedTextOnlyLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &gatedTextLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(verificationBashTool{}), Options{
		Model:      "test",
		MaxTurns:   30,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	result, err := querySession.Run(context.Background(), gatedLoopPrompt, io.Discard)
	if err == nil {
		t.Fatalf("expected loop guard abort for a gated text-only loop, got nil (result=%+v)", result)
	}
	if !strings.Contains(err.Error(), "loop guard") {
		t.Fatalf("error %q does not look like a loop guard abort; a text-only loop ran to MaxTurns", err.Error())
	}
	if result.StopReason != "loop_guard_abort" {
		t.Fatalf("StopReason = %q, want loop_guard_abort", result.StopReason)
	}
	// Same escalation as a tool loop: hard abort on the 6th no-progress turn
	// instead of burning all 30 turns silently.
	if len(streamer.requests) != 6 {
		t.Fatalf("model calls = %d, want 6 (hard limit), MaxTurns=30", len(streamer.requests))
	}
	// The streak reaches 2 on turn 2, so turn 3's request must already carry the
	// escalating Loop check — the model gets a chance to change course.
	if !strings.Contains(allMessageText(streamer.requests[2].Messages), "Loop check") {
		t.Fatalf("loop-awareness section missing from turn 3 request:\n%s", allMessageText(streamer.requests[2].Messages))
	}
}

func TestCompletionGateNudgeDoesNotAccumulateAcrossTurns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &gatedTextLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(verificationBashTool{}), Options{
		Model:      "test",
		MaxTurns:   30,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	_, _ = querySession.Run(context.Background(), gatedLoopPrompt, io.Discard)
	if len(streamer.requests) < 3 {
		t.Fatalf("precondition: need at least 3 gated turns to observe accumulation, got %d", len(streamer.requests))
	}
	for i, req := range streamer.requests {
		text := allMessageText(req.Messages)
		if n := strings.Count(text, "Completion is blocked"); n > 1 {
			t.Fatalf("turn %d request carries %d copies of the completion-gate reminder, want at most 1 — reminders are accumulating in the conversation", i+1, n)
		}
		// The rejected draft is retracted everywhere else (UI, result, transcript);
		// leaving copies in the request is the other half of the same leak.
		if n := strings.Count(text, "Tests passed."); n > 1 {
			t.Fatalf("turn %d request carries %d copies of the retracted draft, want at most 1", i+1, n)
		}
		assertNoAdjacentAssistantMessages(t, i+1, req.Messages)
	}
}

func assertNoAdjacentAssistantMessages(t *testing.T, turn int, messages []anthropic.MessageParam) {
	t.Helper()
	for i := 1; i < len(messages); i++ {
		if messages[i].Role == "assistant" && messages[i-1].Role == "assistant" {
			t.Fatalf("turn %d request has two adjacent assistant messages at %d/%d; retracting the gated draft must not break role alternation", turn, i-1, i)
		}
	}
}

// gatedTextThenComplyStreamer emits a DIFFERENT non-compliant closing text on
// each of the first three turns, then runs the required verification. Each turn
// brings new information, so the loop guard must leave it alone — the mirror
// image of TestLoopGuardIgnoresPollingWithChangingResults for the text path.
type gatedTextThenComplyStreamer struct{ requests []anthropic.MessagesRequest }

func (s *gatedTextThenComplyStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	switch n := len(s.requests); {
	case n <= 3:
		text := fmt.Sprintf("Attempt %d: tests passed.\nENGINEERING_EMAIL_MASK_DONE", n)
		if err := cb.OnText(text); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
			StopReason: "end_turn",
		}, nil
	case n == 4:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_verify",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"go test ./... -count=1 && git diff --check"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		text := "Verified now.\nENGINEERING_EMAIL_MASK_DONE"
		if err := cb.OnText(text); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
			StopReason: "end_turn",
		}, nil
	}
}

func TestLoopGuardKeepsGatedTextRetriesThatChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &gatedTextThenComplyStreamer{}
	querySession := New(streamer, tools.NewRegistry(verificationBashTool{}), Options{
		Model:      "test",
		MaxTurns:   30,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), gatedLoopPrompt, &out)
	if err != nil {
		t.Fatalf("distinct gated drafts are progress and must not be aborted: %v", err)
	}
	if len(streamer.requests) != 5 {
		t.Fatalf("model calls = %d, want 5 (3 distinct drafts + verification + final)", len(streamer.requests))
	}
	if !strings.Contains(result.Response, "Verified now") {
		t.Fatalf("final verified response missing: %q", result.Response)
	}
}

func TestFailedVerificationBashResultIncludesRecoveryReminder(t *testing.T) {
	streamer := &verificationFailureRecoveryStreamer{}
	querySession := New(streamer, tools.NewRegistry(failingVerificationBashTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "Fix code. Run go test ./... -count=1 before finishing.", &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if !strings.Contains(result.ToolCalls[0].Output, "Verification command failed") {
		t.Fatalf("tool output missing recovery reminder:\n%s", result.ToolCalls[0].Output)
	}
}

func TestPostEditReminderRequiresScopeAndMetadataChecks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "config.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &editPostReminderStreamer{
		toolInput: json.RawMessage(`{"file_path":"config.txt","old_string":"old","new_string":"new"}`),
		wants: []string{
			"## Post-edit verification required",
			"git status --short",
			"git diff --name-status",
			"git diff --summary",
			"required_semantic_check",
			"completion_blocker",
		},
	}
	querySession := New(streamer, tools.NewRegistry(fileedit.New()), Options{
		Model:      "test",
		MaxTurns:   2,
		PromptMode: "code",
		CWD:        cwd,
	})
	if _, err := querySession.Run(context.Background(), "修复 config.txt 的旧值", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
}

func TestScriptEditReminderRequiresExecutableVerification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "hooks", "session-start.sh"), []byte("#!/usr/bin/env bash\n/pm-status\n"), 0755); err != nil {
		t.Fatal(err)
	}
	streamer := &editPostReminderStreamer{
		toolInput: json.RawMessage(`{"file_path":"hooks/session-start.sh","old_string":"/pm-status","new_string":"/pm-selfcheck"}`),
		wants: []string{
			"required_script_hook_check",
			"executable bit",
			"referenced commands/scripts exist",
			"bash -n",
		},
	}
	querySession := New(streamer, tools.NewRegistry(fileedit.New()), Options{
		Model:      "test",
		MaxTurns:   2,
		PromptMode: "code",
		CWD:        cwd,
	})
	if _, err := querySession.Run(context.Background(), "先只修复 /pm-status 幽灵引用", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	info, err := os.Stat(filepath.Join(cwd, "hooks", "session-start.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}

func TestPackageEditReminderRequiresMetadataConsistency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "package.json"), []byte(`{"engines":{"claude-code":">=0.15.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &editPostReminderStreamer{
		toolInput: json.RawMessage(`{"file_path":"package.json","old_string":">=0.15.0","new_string":">=2.0.0"}`),
		wants: []string{
			"required_metadata_consistency_check",
			"metadata",
			"source-of-truth",
			"docs",
			"manifests",
		},
	}
	querySession := New(streamer, tools.NewRegistry(fileedit.New()), Options{
		Model:      "test",
		MaxTurns:   2,
		PromptMode: "code",
		CWD:        cwd,
	})
	if _, err := querySession.Run(context.Background(), "在修复 engines 问题", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
}

func TestSessionRecoversPartialTextStreamError(t *testing.T) {
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(t.TempDir(), "60606060-6060-4606-8606-606060606060")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	querySession := New(&partialTextStreamErrorStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
		Recorder: recorder,
	})

	var out strings.Builder
	result, err := querySession.Run(context.Background(), "answer", &out)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "partial_stream_error" || result.Response != "partial answer" || out.String() != "partial answer" {
		t.Fatalf("result = %+v out=%q", result, out.String())
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Type == "message" && entry.Role == "assistant" && entry.Content == "partial answer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial assistant message not recorded: %+v", entries)
	}
}

func TestSessionDoesNotRecoverPartialToolUseStreamError(t *testing.T) {
	querySession := New(&partialToolUseStreamErrorStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
	})

	var out strings.Builder
	_, err := querySession.Run(context.Background(), "answer", &out)
	if err == nil {
		t.Fatal("expected partial tool-use stream error")
	}
	if out.String() != "partial" {
		t.Fatalf("streamed text = %q", out.String())
	}
}

func TestSessionSystemPromptOverrideAndAppend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:          "test",
		MaxTurns:       1,
		MaxTokens:      123,
		SystemPrompt:   "base",
		SystemAddendum: "extra",
		CWD:            t.TempDir(),
	})
	var out strings.Builder
	if _, err := session.Run(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 1 || streamer.systems[0] != "base\n\nextra" {
		t.Fatalf("systems = %+v", streamer.systems)
	}
	if len(streamer.maxTokens) != 1 || streamer.maxTokens[0] != 123 {
		t.Fatalf("maxTokens = %+v", streamer.maxTokens)
	}
}

func TestPromptDumpDisabledDoesNotCreateFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(promptDumpPathEnv, "")
	t.Setenv(promptDumpFullEnv, "")
	dumpPath := filepath.Join(t.TempDir(), "prompt.jsonl")
	session := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dumpPath); !os.IsNotExist(err) {
		t.Fatalf("dump file exists when env is disabled: %v", err)
	}
}

func TestPromptDumpWritesSummaryWithoutRawPromptText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(promptDumpFullEnv, "")
	dumpPath := filepath.Join(t.TempDir(), "prompt.jsonl")
	t.Setenv(promptDumpPathEnv, dumpPath)

	session := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:                   "test-model",
		MaxTurns:                2,
		MaxTokens:               321,
		SystemPrompt:            "base secret marker",
		PromptMode:              "code",
		QuerySource:             "unit-test",
		CWD:                     t.TempDir(),
		ToolResultLimit:         12345,
		ToolResultMessageBudget: 23456,
		ToolResultHistoryBudget: 34567,
	})
	if _, err := session.Run(context.Background(), "user secret marker", io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, leaked := range []string{"base secret marker", "user secret marker", `"text":"hello"`} {
		if strings.Contains(text, leaked) {
			t.Fatalf("summary dump leaked raw content %q:\n%s", leaked, text)
		}
	}
	records := parsePromptDumpRecords(t, raw)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), text)
	}
	first := records[0]
	if first.SchemaVersion != 1 || first.Model != "test-model" || first.MaxTokens != 321 || first.PromptMode != "code" || first.QuerySource != "unit-test" {
		t.Fatalf("first record = %+v", first)
	}
	if first.SystemBytes == 0 || first.SystemHash == "" || first.MessageCount == 0 || first.ToolCount != 1 {
		t.Fatalf("missing summary fields: %+v", first)
	}
	if first.Request != nil || first.RequestRedaction.RawRequestIncluded || first.RequestRedaction.Mode != "summary" || !first.RequestRedaction.TextOmitted {
		t.Fatalf("redaction = %+v request=%+v", first.RequestRedaction, first.Request)
	}
	second := records[1]
	if second.ToolResultStats.Count != 1 || second.ToolResultStats.TotalBytes != len("hello") {
		t.Fatalf("tool result stats = %+v", second.ToolResultStats)
	}
	if second.ToolResultStats.Limit != 12345 || second.ToolResultStats.MessageBudget != 23456 || second.ToolResultStats.HistoryBudget != 34567 {
		t.Fatalf("tool result limits = %+v", second.ToolResultStats)
	}
	foundToolResult := false
	for _, msg := range second.MessagesSummary {
		for _, block := range msg.Blocks {
			if block.Type == "tool_result" && block.ToolName == "Echo" {
				foundToolResult = true
			}
		}
	}
	if !foundToolResult {
		t.Fatalf("missing Echo tool result summary = %+v", second.MessagesSummary)
	}
}

func TestPromptDumpFullIncludesRawRequestWhenExplicit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dumpPath := filepath.Join(t.TempDir(), "prompt.jsonl")
	t.Setenv(promptDumpPathEnv, dumpPath)
	t.Setenv(promptDumpFullEnv, "true")

	session := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:        "test",
		MaxTurns:     1,
		SystemPrompt: "full system marker",
		CWD:          t.TempDir(),
	})
	if _, err := session.Run(context.Background(), "full prompt marker", io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "full system marker") || !strings.Contains(text, "full prompt marker") {
		t.Fatalf("full dump missing raw request content:\n%s", text)
	}
	records := parsePromptDumpRecords(t, raw)
	if len(records) != 1 || records[0].Request == nil || !records[0].RequestRedaction.RawRequestIncluded || records[0].RequestRedaction.Mode != "full" {
		t.Fatalf("full record = %+v", records)
	}
}

func TestPromptDumpFullIncludesSkillCatalogContract(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	dumpPath := filepath.Join(t.TempDir(), "prompt.jsonl")
	t.Setenv(promptDumpPathEnv, dumpPath)
	t.Setenv(promptDumpFullEnv, "true")
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "path-skill"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "path-skill", "SKILL.md"), []byte("---\nname: path-skill\ndescription: Path matched skill\npaths: internal/**\n---\n# Path Skill\n\nSecret body"), 0644); err != nil {
		t.Fatal(err)
	}

	session := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		PromptMode: "code",
		CWD:        project,
	})
	if _, err := session.Run(context.Background(), "inspect internal/query/query.go", io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	records := parsePromptDumpRecords(t, raw)
	if len(records) != 1 || records[0].Request == nil {
		t.Fatalf("records = %+v", records)
	}
	system := records[0].Request.System
	for _, want := range []string{"path-skill: Path matched skill", "blocking requirement", "before taking task-specific actions"} {
		if !strings.Contains(system, want) {
			t.Fatalf("full dump request missing %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "Secret body") {
		t.Fatalf("skill catalog leaked body in request:\n%s", system)
	}
}

func TestSessionInjectsRepoHealthAuditStrategyForAuditPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	prompt := "熟悉当前项目，然后客观分析是否有需要优化的地方，并说出原因"
	if _, err := session.Run(context.Background(), prompt, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 {
		t.Fatalf("messages = %d", len(streamer.messages))
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"## Repository health audit strategy",
		"repository health / release readiness audit",
		"git status/log/tags",
		"package/version/engine metadata",
		"hooks/slash commands/user entrypoints",
		"declared version vs git tags",
		"P0 release/install/update/entrypoint breakage",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("repo audit strategy missing %q:\n%s", want, text)
		}
	}
}

func TestRepoHealthAuditStrategyCarriesSyntheticROIRankingContract(t *testing.T) {
	section := runtimeTaskStrategySection("熟悉当前项目，然后客观分析是否有需要优化的地方，并说出原因")
	for _, want := range []string{
		"declared version vs git tags",
		"package engine",
		"missing entrypoint",
		"above CHANGELOG lag, stale docs, or structural cleanup",
		"P0 release/install/update/entrypoint breakage",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("repo audit strategy missing ROI contract %q:\n%s", want, section)
		}
	}
}

func TestReleaseReadinessAuditStrategyCarriesEntrypointROIRankingContract(t *testing.T) {
	section := runtimeTaskStrategySection("熟悉当前项目，按 P0/P1/P2 排序，优先关注发布、安装、更新、入口命令和首次体验风险")
	for _, want := range []string{
		"first-run entrypoint state",
		"documented startup or slash commands vs registered skills/scripts",
		"P0 cannot install/upgrade/publish, discovers the wrong version, or hits a documented startup/entrypoint command that cannot execute",
		"missing documented startup or slash-command findings above CHANGELOG lag, stale docs, or structural cleanup",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("release readiness strategy missing ROI contract %q:\n%s", want, section)
		}
	}
}

func TestSessionInjectsSpecificAuditStrategies(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "release readiness",
			prompt: "Check release readiness before we publish this package.",
			want:   "## Release readiness audit strategy",
		},
		{
			name:   "entrypoint audit",
			prompt: "Audit this repository for user entrypoint risks and missing commands.",
			want:   "## User entrypoint audit strategy",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			streamer := &systemStreamer{}
			session := New(streamer, tools.NewRegistry(echoTool{}), Options{
				Model:      "test",
				MaxTurns:   1,
				PromptMode: "code",
				CWD:        t.TempDir(),
			})
			if _, err := session.Run(context.Background(), tc.prompt, io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(streamer.messages) != 1 {
				t.Fatalf("messages = %d", len(streamer.messages))
			}
			text := allMessageText(streamer.messages[0])
			if !strings.Contains(text, tc.want) {
				t.Fatalf("strategy missing %q:\n%s", tc.want, text)
			}
		})
	}
}

func TestSessionDoesNotInjectRepoHealthAuditStrategyForReadmeSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	if _, err := session.Run(context.Background(), "读取 README.md，总结项目做什么", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 {
		t.Fatalf("messages = %d", len(streamer.messages))
	}
	text := allMessageText(streamer.messages[0])
	if strings.Contains(text, "audit strategy") {
		t.Fatalf("README summary should not inject audit strategy:\n%s", text)
	}
}

func TestSessionDoesNotInjectAuditStrategyForFocusedCodeChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		PromptMode: "code",
		CWD:        t.TempDir(),
	})
	if _, err := session.Run(context.Background(), "Fix project issue #123: parser panics on empty input.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 {
		t.Fatalf("messages = %d", len(streamer.messages))
	}
	text := allMessageText(streamer.messages[0])
	if strings.Contains(text, "audit strategy") {
		t.Fatalf("focused code change should not inject audit strategy:\n%s", text)
	}
}

func TestRuntimeTaskClassifierMatrix(t *testing.T) {
	tests := []struct {
		name string
		text string
		want runtimeTaskType
	}{
		{
			name: "chinese current project audit",
			text: "熟悉当前项目，然后客观分析是否有需要优化的地方，并说出原因",
			want: runtimeTaskRepoHealthAudit,
		},
		{
			name: "english repo health audit",
			text: "Run a repo health audit and find the highest ROI gaps.",
			want: runtimeTaskRepoHealthAudit,
		},
		{
			name: "release readiness audit",
			text: "Check release readiness before we publish this package.",
			want: runtimeTaskReleaseReadinessAudit,
		},
		{
			name: "mixed chinese replay prompt prioritizes release readiness with entrypoint contract",
			text: "熟悉当前项目，然后客观分析是否有需要优化的地方；请按 P0/P1/P2 排序，优先关注发布、安装、更新、入口命令和首次体验风险。",
			want: runtimeTaskReleaseReadinessAudit,
		},
		{
			name: "scoped project optimization",
			text: "这个项目有哪些需要优化的地方，按影响面排序",
			want: runtimeTaskRepoHealthAudit,
		},
		{
			name: "repository entrypoint risk",
			text: "Audit this repository for user entrypoint risks and missing commands.",
			want: runtimeTaskEntrypointAudit,
		},
		{
			name: "branch review url does not become repair",
			text: "review这个分支改动https://github.com/example/repo/blob/feature/x",
			want: runtimeTaskCodeReview,
		},
		{
			name: "url slash alone does not imply entrypoint repair",
			text: "review this branch https://github.com/example/repo/pull/42",
			want: runtimeTaskCodeReview,
		},
		{
			name: "generic diff review",
			text: "Review the current diff and report correctness issues.",
			want: runtimeTaskCodeReview,
		},
		{
			name: "explicit repair of review findings remains repair",
			text: "修复 review 中的 1、2 两个配置问题",
			want: runtimeTaskTargetedRepair,
		},
		{
			name: "read only repair analysis remains unknown",
			text: "先分析这个修复为什么失败，不要修改",
			want: runtimeTaskUnknown,
		},
		{
			name: "readme summary",
			text: "读取 README.md，总结项目做什么",
			want: runtimeTaskDocReview,
		},
		{
			name: "focused function optimization",
			text: "分析这个函数需要优化的地方，不要审计整个项目",
			want: runtimeTaskUnknown,
		},
		{
			name: "focused code fix with project issue",
			text: "Fix project issue #123: parser panics on empty input.",
			want: runtimeTaskCodeChange,
		},
		{
			name: "entrypoint repair",
			text: "先只修复 /pm-status 幽灵引用",
			want: runtimeTaskEntrypointRepair,
		},
		{
			name: "metadata release repair",
			text: "在修复 engines 问题",
			want: runtimeTaskReleaseRepair,
		},
		{
			name: "tag push release repair",
			text: "创建 v2.4.3 tag 并推送",
			want: runtimeTaskReleaseRepair,
		},
		{
			name: "documentation polish",
			text: "润色 README 文案，让表达更自然",
			want: runtimeTaskDocReview,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRuntimeTaskPrompt(tc.text).Type; got != tc.want {
				t.Fatalf("classifyRuntimeTaskPrompt(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestTaskContractClassifierMatrix(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		level     closureLevel
		readPaths []string
		ops       []sharedStateOperation
		ruleID    string
		conf      classificationConfidence
	}{
		{
			name:   "trivial arithmetic",
			prompt: "1+1=?",
			level:  closureLevelTrivialAnswer,
			ruleID: "trivial_arithmetic",
			conf:   classificationConfidenceHigh,
		},
		{
			name:      "read only scoped file",
			prompt:    "帮我看 internal/query/closure_gate.go 的闭环逻辑并总结",
			level:     closureLevelReadOnlyScoped,
			readPaths: []string{"internal/query/closure_gate.go"},
			ruleID:    "read_only_scoped",
			conf:      classificationConfidenceHigh,
		},
		{
			name:      "read scope wins over readme doc review",
			prompt:    "读取 README.md，总结项目做什么",
			level:     closureLevelReadOnlyScoped,
			readPaths: []string{"README.md"},
			ruleID:    "read_only_scoped",
			conf:      classificationConfidenceHigh,
		},
		{
			name:   "repo health audit",
			prompt: "Run a repo health audit and find the highest ROI gaps.",
			level:  closureLevelInvestigation,
			ruleID: string(runtimeTaskRepoHealthAudit),
			conf:   classificationConfidenceHigh,
		},
		{
			name:   "branch review is investigation",
			prompt: "review这个分支改动https://github.com/example/repo/blob/feature/x",
			level:  closureLevelInvestigation,
			ruleID: string(runtimeTaskCodeReview),
			conf:   classificationConfidenceHigh,
		},
		{
			name:   "focused code change",
			prompt: "Fix project issue #123: parser panics on empty input.",
			level:  closureLevelLocalChange,
			ruleID: string(runtimeTaskCodeChange),
			conf:   classificationConfidenceHigh,
		},
		{
			name:   "shared state push",
			prompt: "Push this branch to the remote.",
			level:  closureLevelSharedStateChange,
			ops:    []sharedStateOperation{sharedStatePush},
			ruleID: "shared_state_request",
			conf:   classificationConfidenceHigh,
		},
		{
			name:   "readme link is a local metadata change",
			prompt: "把 README.en.md 链接到市场文档的英文版入口",
			level:  closureLevelLocalChange,
			ruleID: string(runtimeTaskEntrypointRepair),
			conf:   classificationConfidenceHigh,
		},
		{
			name:   "readme link how-to remains an answer",
			prompt: "How do I add a link to README.md?",
			level:  closureLevelNoToolAnswer,
			ruleID: "default_no_tool",
			conf:   classificationConfidenceLow,
		},
		{
			name:   "negated commit is not authorization",
			prompt: "不要提交 CLAUDE.md，把它从 staged 区移除",
			level:  closureLevelNoToolAnswer,
			ruleID: "default_no_tool",
			conf:   classificationConfidenceLow,
		},
		{
			name:   "commit discussion is not authorization",
			prompt: "我没有让你提交 CLAUDE.md，为啥最开始你要提交？",
			level:  closureLevelNoToolAnswer,
			ruleID: "default_no_tool",
			conf:   classificationConfidenceLow,
		},
		{
			name:   "release repair wins over shared state request",
			prompt: "创建 v2.4.3 tag 并推送",
			level:  closureLevelLocalChange,
			ruleID: string(runtimeTaskReleaseRepair),
			conf:   classificationConfidenceHigh,
		},
		{
			name:   "explicit non audit stays light",
			prompt: "分析这个函数需要优化的地方，不要审计整个项目",
			level:  closureLevelNoToolAnswer,
			ruleID: "default_no_tool",
			conf:   classificationConfidenceLow,
		},
		{
			name:   "default no tool",
			prompt: "解释一下 goroutine 是什么",
			level:  closureLevelNoToolAnswer,
			ruleID: "default_no_tool",
			conf:   classificationConfidenceLow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildTaskContract(tc.prompt)
			if got.ClosureLevel != tc.level {
				t.Fatalf("ClosureLevel = %s, want %s; contract=%+v", got.ClosureLevel, tc.level, got)
			}
			if !reflect.DeepEqual(got.RequiredReadPath, tc.readPaths) {
				t.Fatalf("RequiredReadPath = %+v, want %+v", got.RequiredReadPath, tc.readPaths)
			}
			if !reflect.DeepEqual(got.SharedStateOperations, tc.ops) {
				t.Fatalf("SharedStateOperations = %+v, want %+v", got.SharedStateOperations, tc.ops)
			}
			if got.Confidence != tc.conf {
				t.Fatalf("Confidence = %s, want %s", got.Confidence, tc.conf)
			}
			if len(got.Reasons) == 0 {
				t.Fatalf("Reasons empty for contract=%+v", got)
			}
			if got.Reasons[0].RuleID != tc.ruleID {
				t.Fatalf("Reasons[0].RuleID = %q, want %q; reasons=%+v", got.Reasons[0].RuleID, tc.ruleID, got.Reasons)
			}
		})
	}
}

func TestSharedStateAuthorizationGateUsesCurrentTurnOnly(t *testing.T) {
	verified := []ToolTrace{
		{Name: "Bash", Input: `{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status && git rev-parse HEAD && git rev-parse @{u}"}`},
	}
	tests := []struct {
		name    string
		prompt  string
		command string
	}{
		{
			name:    "commit from a read only follow up",
			prompt:  "把 README.en.md 链接到市场文档的英文版入口",
			command: "git add README.en.md && git commit -m docs",
		},
		{
			name:    "push from an impact question",
			prompt:  "skills/06-experts 和新增目录都是 06 开头，这影响吗？",
			command: "git push origin main",
		},
		{
			name:    "negated commit",
			prompt:  "不要提交 CLAUDE.md，把它从 staged 区移除",
			command: "git commit -m rename",
		},
		{
			name:    "commit discussion",
			prompt:  "我没有让你提交 CLAUDE.md，为啥最开始你要提交？",
			command: "git commit -m rename",
		},
		{
			name:    "commit status question",
			prompt:  "CLAUDE.md 提交了吗？",
			command: "git commit -m rename",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"command":%q}`, tc.command)
			got := preToolClosureGate(buildTaskContract(tc.prompt), tc.prompt, verified, "Bash", input)
			if got.Severity != gateSeverityBlockTool || got.RuleID != "shared_state_authorization" {
				t.Fatalf("unauthorized shared-state operation must block; got severity=%q rule=%q reminder=%q", got.Severity, got.RuleID, got.Reminder)
			}
		})
	}
}

func TestSharedStateAuthorizationGatePreservesExplicitWorkflow(t *testing.T) {
	calls := []ToolTrace{
		{Name: "Bash", Input: `{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status && git rev-parse HEAD && git rev-parse @{u}"}`},
	}
	prompt := "好，你帮我提交并 push"
	input := `{"command":"git commit -m change && git push origin main"}`
	got := preToolClosureGate(buildTaskContract(prompt), prompt, calls, "Bash", input)
	if got.Severity != gateSeverityAllow {
		t.Fatalf("explicitly authorized commit/push must preserve the existing verified workflow; got severity=%q rule=%q reminder=%q", got.Severity, got.RuleID, got.Reminder)
	}
}

func TestSharedStateAuthorizationGateExplainsExactScopeMismatchRecovery(t *testing.T) {
	verified := []ToolTrace{
		{Name: "Bash", Input: `{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status && git rev-parse HEAD && git rev-parse @{u}"}`},
	}
	prompt := `我明确授权执行：

` + "```bash" + `
git commit --author="konglong87 <>" -m "<原定提交信息>"
git push origin master
` + "```"
	actualMessage := "docs: 补充下次复诊建议项新表文档 33_NEXT_REVISIT_SUGGESTION.md"
	command := `git commit --author="konglong87 <>" -m "` + actualMessage + `"`
	input := fmt.Sprintf(`{"command":%q}`, command)

	got := preToolClosureGate(buildTaskContract(prompt), prompt, verified, "Bash", input)
	if got.Severity != gateSeverityBlockTool || got.RuleID != "shared_state_authorization" {
		t.Fatalf("placeholder mismatch must block; severity=%q rule=%q reminder=%q", got.Severity, got.RuleID, got.Reminder)
	}
	for _, want := range []string{
		"commit message does not match",
		"Placeholders such as <message> are literal values, not wildcards",
		"Ask the user to authorize the exact intended command in a new message",
		"or tell the user to run it manually",
	} {
		if !strings.Contains(got.Reminder, want) {
			t.Fatalf("reminder %q does not contain %q", got.Reminder, want)
		}
	}

	exactPrompt := `我明确授权执行 git commit --author="konglong87 <>" -m "` + actualMessage + `"`
	got = preToolClosureGate(buildTaskContract(exactPrompt), exactPrompt, verified, "Bash", input)
	if got.Severity != gateSeverityAllow {
		t.Fatalf("exact current-turn authorization must allow; severity=%q rule=%q reminder=%q", got.Severity, got.RuleID, got.Reminder)
	}
}

func TestStructuredGitAuthorizationGateIntegration(t *testing.T) {
	verified := []ToolTrace{
		{Name: "Bash", Input: `{"command":"git status --short --branch && git diff --name-status && git diff --cached --name-status && git rev-parse HEAD && git rev-parse @{u}"}`},
	}
	tests := []struct {
		name     string
		prompt   string
		command  string
		wantRule string
	}{
		{name: "quoted example is not executed Git", prompt: "Search for the quoted command.", command: "rg -n 'git push' internal/query"},
		{name: "global option cannot bypass authorization", prompt: "Inspect the repository only.", command: "git -C /repo commit -m x", wantRule: "shared_state_authorization"},
		{name: "destructive refspec needs destructive authorization", prompt: "Push current branch.", command: "git push origin :refs/heads/main", wantRule: "destructive_shared_state"},
		{name: "explicit push scope is binding", prompt: "push dev-c/foo 到 origin", command: "git push backup main", wantRule: "shared_state_authorization"},
		{name: "indirect force add cannot be authorized", prompt: "明确允许执行 git add -f paths.txt", command: "git add -f --pathspec-from-file paths.txt", wantRule: "force_add_ignored_path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"command":%q}`, tc.command)
			got := preToolClosureGate(buildTaskContract(tc.prompt), tc.prompt, verified, "Bash", input)
			if tc.wantRule == "" {
				if got.Severity != gateSeverityAllow {
					t.Fatalf("non-mutating command blocked: severity=%q rule=%q reminder=%q", got.Severity, got.RuleID, got.Reminder)
				}
				return
			}
			if got.Severity != gateSeverityBlockTool || got.RuleID != tc.wantRule {
				t.Fatalf("severity=%q rule=%q, want block rule %q; reminder=%q", got.Severity, got.RuleID, tc.wantRule, got.Reminder)
			}
		})
	}
}

func TestForceAddIgnoredPathRequiresExactAuthorization(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		command  string
		wantRule string
	}{
		{
			name:     "generic commit does not authorize force add",
			prompt:   "提交并 push 当前改动",
			command:  "git add -f CLAUDE.md",
			wantRule: "force_add_ignored_path",
		},
		{
			name:     "path prefix is not exact authorization",
			prompt:   "明确允许执行 git add -f CLAUDE.md.bak",
			command:  "git add -f CLAUDE.md",
			wantRule: "force_add_ignored_path",
		},
		{
			name:     "all force add paths need authorization",
			prompt:   "明确允许执行 git add -f CLAUDE.md",
			command:  "git add -f CLAUDE.md && git add --force secrets.env",
			wantRule: "force_add_ignored_path",
		},
		{
			name:    "exact force add is authorized",
			prompt:  "明确允许执行 git add -f CLAUDE.md，然后提交",
			command: "git add -f CLAUDE.md",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"command":%q}`, tc.command)
			got := preToolClosureGate(buildTaskContract(tc.prompt), tc.prompt, nil, "Bash", input)
			if tc.wantRule == "" {
				if got.Severity != gateSeverityAllow {
					t.Fatalf("explicit exact force-add authorization must allow; got severity=%q rule=%q", got.Severity, got.RuleID)
				}
				return
			}
			if got.Severity != gateSeverityBlockTool || got.RuleID != tc.wantRule {
				t.Fatalf("force add without exact authorization must block; got severity=%q rule=%q", got.Severity, got.RuleID)
			}
		})
	}
}

func TestRepairEvidenceGateIsScopedAndRolloutControlled(t *testing.T) {
	contract := taskContract{
		TaskType:     runtimeTaskTargetedRepair,
		ClosureLevel: closureLevelLocalChange,
		Confidence:   classificationConfidenceHigh,
	}
	grepOnly := []ToolTrace{
		{Name: "Write"},
		{Name: "Bash", Input: `{"command":"grep -n invariant file.go"}`},
	}

	t.Setenv(repair.EnforcementModeEnv, "observe")
	if _, applies := repairCompletionGateResult(contract, grepOnly, "修复完成"); applies {
		t.Fatal("observe mode must not alter completion behavior")
	}

	t.Setenv(repair.EnforcementModeEnv, "enforce")
	gate, applies := repairCompletionGateResult(contract, grepOnly, "修复完成")
	if !applies || gate.Severity != gateSeverityBlockContinue {
		t.Fatalf("plain grep must not satisfy enforced repair evidence: %+v applies=%v", gate, applies)
	}

	verified := append(append([]ToolTrace(nil), grepOnly...), ToolTrace{
		Name: "Bash", Input: `{"command":"go test ./internal/query -run TestInvariant -count=1"}`,
	})
	if gate, applies := repairCompletionGateResult(contract, verified, "修复完成"); applies {
		t.Fatalf("behavioral test should satisfy current repair evidence: %+v", gate)
	}

	stale := append(append([]ToolTrace(nil), verified...), ToolTrace{Name: "Edit"})
	if gate, applies := repairCompletionGateResult(contract, stale, "修复完成"); !applies || gate.Severity != gateSeverityBlockContinue {
		t.Fatalf("new content must stale prior evidence: %+v applies=%v", gate, applies)
	}

	mainstream := contract
	mainstream.TaskType = runtimeTaskCodeChange
	if gate, applies := repairCompletionGateResult(mainstream, grepOnly, "实现完成"); applies {
		t.Fatalf("mainstream code change must stay outside strict repair gate: %+v", gate)
	}
}

func TestRepairWarnModeDoesNotBypassExistingDeltaGate(t *testing.T) {
	t.Setenv(repair.EnforcementModeEnv, "warn")
	contract := taskContract{
		TaskType: runtimeTaskTargetedRepair, ClosureLevel: closureLevelLocalChange,
		Confidence: classificationConfidenceHigh,
	}
	calls := []ToolTrace{
		{Name: "Write", FileChanges: []tools.FileChange{{Path: "file.go"}}},
		{Name: "Bash", Input: `{"command":"grep -n invariant file.go"}`},
	}
	gate := completionGate(contract, calls, "修复完成")
	if gate.Severity != gateSeverityBlockContinue || gate.RuleID != "post_action_delta" {
		t.Fatalf("warn-only repair rule bypassed existing delta gate: %+v", gate)
	}
}

func TestRepairEvidenceSurvivesGitStateChangesButBlocksPreCommitWhenMissing(t *testing.T) {
	t.Setenv(repair.EnforcementModeEnv, "enforce")
	contract := taskContract{
		TaskType:     runtimeTaskEntrypointRepair,
		ClosureLevel: closureLevelLocalChange,
		Confidence:   classificationConfidenceHigh,
	}
	verified := []ToolTrace{
		{Name: "Edit"},
		{Name: "Bash", Input: `{"command":"test -x scripts/check.sh"}`},
		{Name: "Bash", Input: `{"command":"git add scripts/check.sh"}`},
	}
	if gate, applies := repairPreCommitGateResult(contract, verified); applies {
		t.Fatalf("git add must not stale semantic evidence: %+v", gate)
	}
	afterCommit := append(append([]ToolTrace(nil), verified...), ToolTrace{Name: "Bash", Input: `{"command":"git commit -m verified"}`})
	if gate, applies := repairCompletionGateResult(contract, afterCommit, "修复完成"); applies {
		t.Fatalf("git commit without hook content changes must not stale semantic evidence: %+v", gate)
	}
	missing := []ToolTrace{{Name: "Edit"}, {Name: "Bash", Input: `{"command":"rg check scripts/check.sh"}`}}
	if gate, applies := repairPreCommitGateResult(contract, missing); !applies || gate.Severity != gateSeverityBlockTool {
		t.Fatalf("pre-commit must require current assertive evidence: %+v applies=%v", gate, applies)
	}
}

func TestRepairDefinitionDeletionRequiresFullReadAndReferenceSearch(t *testing.T) {
	t.Setenv(repair.EnforcementModeEnv, "enforce")
	contract := taskContract{
		TaskType:     runtimeTaskTargetedRepair,
		ClosureLevel: closureLevelLocalChange,
		Confidence:   classificationConfidenceHigh,
	}
	editInput := `{"file_path":"skills/check/SKILL.md","old_string":"BUNDLE_ROOT=\"$PWD\"\nprintf '%s' \"$BUNDLE_ROOT\"","new_string":"printf '%s' \"$BUNDLE_ROOT\""}`
	partial := []ToolTrace{{Name: "Read", Input: `{"file_path":"skills/check/SKILL.md","limit":25}`}}
	gate := preToolClosureGate(contract, "修复配置问题", partial, "Edit", editInput)
	if gate.Severity != gateSeverityBlockTool || len(gate.MissingEvidence) != 2 {
		t.Fatalf("partial read deletion gate = %+v", gate)
	}

	evidence := []ToolTrace{
		{Name: "Read", Input: `{"file_path":"skills/check/SKILL.md"}`, Output: "full file"},
		{Name: "Grep", Input: `{"pattern":"BUNDLE_ROOT","path":"."}`, Output: "references"},
	}
	if gate := preToolClosureGate(contract, "修复配置问题", evidence, "Edit", editInput); gate.Severity != gateSeverityAllow {
		t.Fatalf("complete impact evidence should allow definition deletion: %+v", gate)
	}

	mainstream := contract
	mainstream.TaskType = runtimeTaskCodeChange
	if gate := preToolClosureGate(mainstream, "implement feature", partial, "Edit", editInput); gate.Severity != gateSeverityAllow {
		t.Fatalf("mainstream code change must not inherit strict repair deletion gate: %+v", gate)
	}
}

func TestExpectedFailBaselineKeepsRealErrorWithoutRecoveryNoise(t *testing.T) {
	cwd := t.TempDir()
	registry := tools.NewRegistry(bashtool.New())
	session := New(&systemStreamer{}, registry, Options{CWD: cwd})
	trace := session.runTool(context.Background(), registry, anthropic.ContentBlock{
		Type: "tool_use", ID: "baseline", Name: "Bash",
		Input: json.RawMessage(`{"command":"test 0 -gt 0","verification":{"probe_id":"nonempty","phase":"baseline","targets":["file"],"expect_exit":"nonzero"}}`),
	}, runCallbacks{})
	if !trace.IsError || trace.verification == nil || !trace.verification.MatchedExpectation {
		t.Fatalf("baseline trace = %+v verification=%+v", trace, trace.verification)
	}
	if strings.Contains(trace.Output, "Verification command failed") {
		t.Fatalf("expected baseline failure received generic recovery reminder: %s", trace.Output)
	}
}

func TestToolTraceRepairEvidenceDoesNotChangePublicJSONShape(t *testing.T) {
	trace := ToolTrace{
		ID: "tool", Name: "Bash", Output: "ok",
		verification: &repair.VerificationResult{Grade: repair.EvidenceGradeBehavioralTest},
	}
	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "verification") {
		t.Fatalf("internal repair evidence leaked into ToolTrace JSON: %s", data)
	}
}

func TestResolveContinuationIntent(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{Type: "text", Text: `如果继续推进，优先级建议：
1. 补强 3-ai-agents — 这是最大缺口
2. 扩充 prompts

你想聊哪个方向？`}},
	}}
	tests := []struct {
		name   string
		prompt string
		active bool
	}{
		{name: "simple chinese confirmation", prompt: "好", active: true},
		{name: "explicit start", prompt: "好，开始", active: true},
		{name: "english ok", prompt: "ok", active: true},
		{name: "status question", prompt: "好了吗", active: false},
		{name: "negation", prompt: "不可以，先不要开始", active: false},
		{name: "question with modification", prompt: "可以修改第一课吗？", active: false},
		{name: "plan first modifier", prompt: "好，先说方案", active: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveContinuationIntent(tc.prompt, history)
			if got.Active != tc.active {
				t.Fatalf("Active = %v, want %v; intent=%+v", got.Active, tc.active, got)
			}
			if tc.active && !strings.Contains(got.PendingAction, "补强 3-ai-agents") {
				t.Fatalf("PendingAction = %q, want 3-ai-agents action", got.PendingAction)
			}
		})
	}
}

func TestResolveContinuationIntentRequiresActionableHistory(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role:    "assistant",
		Content: []anthropic.ContentBlock{{Type: "text", Text: "收到。有什么需要做的随时说。"}},
	}}
	if got := resolveContinuationIntent("好", history); got.Active {
		t.Fatalf("Active = true for idle assistant history: %+v", got)
	}
}

func TestConfirmedPendingActionBlocksAckAndContinues(t *testing.T) {
	cwd := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(cwd, "92929292-9292-4929-8929-929292929292")
	if err != nil {
		t.Fatal(err)
	}
	streamer := &continuationAckStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 4,
		CWD:      cwd,
		Recorder: recorder,
		InitialMessages: []anthropic.MessageParam{{
			Role: "assistant",
			Content: []anthropic.ContentBlock{{Type: "text", Text: `如果继续推进，优先级建议：
1. 补强 3-ai-agents — 这是最大缺口
2. 扩充 prompts

你想聊哪个方向？`}},
		}},
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "好", &out)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if result.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", result.Turns)
	}
	if len(streamer.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(streamer.requests))
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "Echo" || result.ToolCalls[0].IsError {
		t.Fatalf("tool calls = %+v", result.ToolCalls)
	}
	if strings.Contains(result.Response, "收到。有什么需要做的随时说。") || strings.Contains(out.String(), "收到。有什么需要做的随时说。") {
		t.Fatalf("premature acknowledgement leaked: result=%q out=%q", result.Response, out.String())
	}
	if !strings.Contains(result.Response, "done") || !strings.Contains(out.String(), "done") {
		t.Fatalf("final response missing: result=%q out=%q", result.Response, out.String())
	}
	raw, err := os.ReadFile(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	transcript := string(raw)
	for _, want := range []string{`"type":"continuation_intent"`, "补强 3-ai-agents", `"type":"completion_gate"`, "confirmed_pending_action_requires_progress"} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript missing %q:\n%s", want, transcript)
		}
	}
}

func TestTaskClassifierRulesArePriorityOrdered(t *testing.T) {
	if len(taskClassifierRules) == 0 {
		t.Fatal("taskClassifierRules is empty")
	}
	for i := 1; i < len(taskClassifierRules); i++ {
		prev := taskClassifierRules[i-1]
		cur := taskClassifierRules[i]
		if prev.Priority < cur.Priority {
			t.Fatalf("taskClassifierRules not sorted by descending priority at %d: %s=%d before %s=%d", i, prev.ID, prev.Priority, cur.ID, cur.Priority)
		}
	}
}

func TestCompletionGateRulesArePriorityOrdered(t *testing.T) {
	if len(completionGateRules) == 0 {
		t.Fatal("completionGateRules is empty")
	}
	for i := 1; i < len(completionGateRules); i++ {
		prev := completionGateRules[i-1]
		cur := completionGateRules[i]
		if prev.Priority < cur.Priority {
			t.Fatalf("completionGateRules not sorted by descending priority at %d: %s=%d before %s=%d", i, prev.ID, prev.Priority, cur.ID, cur.Priority)
		}
	}
}

func TestRepairStrategyClassifierInjectsSafetyContract(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   []string
	}{
		{
			name:   "entrypoint repair",
			prompt: "先只修复 /pm-status 幽灵引用",
			want: []string{
				"## Repair safety strategy",
				"repair_type: entrypoint_repair",
				"git diff --name-status",
				"git diff --summary",
				"preserve executable mode",
				"referenced command exists",
			},
		},
		{
			name:   "metadata release repair",
			prompt: "在修复 engines 问题",
			want: []string{
				"## Repair safety strategy",
				"repair_type: release_repair",
				"local HEAD",
				"remote tag object",
				"version/package files",
			},
		},
		{
			name:   "tag push repair",
			prompt: "创建 v2.4.3 tag 并推送",
			want: []string{
				"## Repair safety strategy",
				"repair_type: release_repair",
				"branch push",
				"tag push",
				"remaining remote/manual actions",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			section := runtimeTaskStrategySection(tc.prompt)
			for _, want := range tc.want {
				if !strings.Contains(section, want) {
					t.Fatalf("repair strategy missing %q:\n%s", want, section)
				}
			}
		})
	}
}

func TestRepairStrategyDoesNotInjectForReadOnlyOrExplanationPrompts(t *testing.T) {
	for _, prompt := range []string{
		"先不改，只分析修复方案",
		"解释这个函数为什么慢",
	} {
		if section := runtimeTaskStrategySection(prompt); strings.Contains(section, "Repair safety strategy") {
			t.Fatalf("prompt %q unexpectedly injected repair strategy:\n%s", prompt, section)
		}
	}
}

func TestPromptDumpWriteErrorStopsBeforeModelRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(promptDumpPathEnv, t.TempDir())
	t.Setenv(promptDumpFullEnv, "")
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
	})
	_, err := session.Run(context.Background(), "hi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "prompt dump") {
		t.Fatalf("err = %v, want prompt dump error", err)
	}
	if len(streamer.messages) != 0 {
		t.Fatalf("model was called despite dump failure: %+v", streamer.messages)
	}
}

func TestSessionPrependsInitialPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(), Options{
		Model:         "test",
		MaxTurns:      1,
		CWD:           t.TempDir(),
		InitialPrompt: "first instruction",
	})
	if _, err := session.Run(context.Background(), "user task", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) == 0 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	text := messageText(streamer.messages[0][len(streamer.messages[0])-1])
	if !strings.Contains(text, "first instruction\n\nuser task") {
		t.Fatalf("user message = %q", text)
	}
}

func TestSessionInjectsSkillCatalogMetadataOnly(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "go-review"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "go-review", "SKILL.md"), []byte("---\nname: go-review\ndescription: Review Go changes\n---\n# Go Review\n\nSecret body"), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:        "test",
		MaxTurns:     1,
		SystemPrompt: "base",
		CWD:          project,
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 1 {
		t.Fatalf("systems = %+v", streamer.systems)
	}
	system := streamer.systems[0]
	for _, want := range []string{"# Available Skills", "go-review: Review Go changes", "blocking requirement", "before taking task-specific actions"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "Secret body") {
		t.Fatalf("system prompt leaked skill body:\n%s", system)
	}
}

func TestClaudeCompatiblePromptProfileSkipsInitialSkillSystemCatalog(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(promptProfileEnv, promptProfileClaudeCompatible)
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "go-review"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "go-review", "SKILL.md"), []byte("---\nname: go-review\ndescription: Review Go changes\n---\n# Go Review\n\nSecret body"), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		PromptMode: "code",
		CWD:        project,
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 1 || len(streamer.blocks) != 1 {
		t.Fatalf("systems=%+v blocks=%+v", streamer.systems, streamer.blocks)
	}
	system := streamer.systems[0]
	if !strings.Contains(system, agentSDKPrefix) {
		t.Fatalf("compatible system missing Claude SDK attribution:\n%s", system)
	}
	if strings.Contains(system, defaultCLISystemPrompt) || strings.Contains(system, "Your runtime identity is niuma") {
		t.Fatalf("compatible system kept niuma-only identity:\n%s", system)
	}
	for _, want := range []string{
		"interactive agent that helps users with software engineering tasks",
		"# Safety",
		"authorized security testing",
		"Do not generate or guess URLs",
		"Prefer editing an existing file to creating a new one",
		"Avoid giving time estimates or predictions",
		"Defer to the user's judgment",
		"appears to rest on a misconception",
		"Do not add validation, fallbacks, or error handling",
		"Do not create helpers or abstractions for one-time operations",
		"Avoid backwards-compatibility hacks",
		"Never claim tests pass",
		"If an approach fails, diagnose the error",
		"Treat approval as scoped to the specific action and context",
		"Before your first tool call",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("compatible system missing %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "# Available Skills") || strings.Contains(system, "go-review: Review Go changes") || strings.Contains(system, "Secret body") {
		t.Fatalf("compatible system included initial local skills catalog:\n%s", system)
	}
	for _, block := range streamer.blocks[0] {
		if block.Source == "skills_catalog" {
			t.Fatalf("compatible system blocks included skills catalog: %+v", streamer.blocks[0])
		}
	}
}

func TestSessionInjectsDynamicSkillCatalogFromToolEvidence(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "path-skill"), 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: path-skill\ndescription: Path matched skill\npaths: internal/secret/**\n---\n# Path Skill\n\nSecret body"
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "path-skill", "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &dynamicSkillCatalogStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      project,
	})
	if _, err := session.Run(context.Background(), "start without a matching path", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	if strings.Contains(streamer.requests[0].System, "path-skill") {
		t.Fatalf("first request should not include path-scoped skill:\n%s", streamer.requests[0].System)
	}
	second := allMessageText(streamer.requests[1].Messages)
	for _, want := range []string{"Additional skills became relevant", "call the Skill tool with its exact name before continuing", "# Available Skills", "path-skill: Path matched skill", "blocking requirement"} {
		if !strings.Contains(second, want) {
			t.Fatalf("second request missing %q:\n%s", want, second)
		}
	}
	if strings.Contains(second, "Secret body") {
		t.Fatalf("dynamic catalog leaked skill body:\n%s", second)
	}
}

func TestClaudeCompatiblePromptProfileSkipsDynamicSkillCatalog(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_PROMPT_PROFILE", "claude-compatible")
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "path-skill"), 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: path-skill\ndescription: Path matched skill\npaths: internal/secret/**\n---\n# Path Skill\n\nSecret body"
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "path-skill", "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &dynamicSkillCatalogStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      project,
	})
	if _, err := session.Run(context.Background(), "start without a matching path", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	second := allMessageText(streamer.requests[1].Messages)
	for _, forbidden := range []string{"Additional skills became relevant", "# Available Skills", "path-skill: Path matched skill", "Secret body"} {
		if strings.Contains(second, forbidden) {
			t.Fatalf("compatible second request included %q:\n%s", forbidden, second)
		}
	}
}

func TestSessionAddsLoadedSkillContentAsContextMessage(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "demo"), 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: Demo skill\n---\n# Demo\n\nUse demo instructions."
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "demo", "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &skillContextStreamer{}
	session := New(streamer, tools.NewRegistry(skilltool.New()), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      project,
	})
	if _, err := session.Run(context.Background(), "use demo skill", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	second := streamer.requests[1].Messages
	if len(second) < 3 {
		t.Fatalf("second request messages = %+v", second)
	}
	var toolResult, contextText string
	for _, msg := range second {
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_result":
				if block.ToolUseID == "toolu_skill" {
					toolResult = block.Content
				}
			case "text":
				if strings.Contains(block.Text, "Skill demo instructions are now active") {
					contextText = block.Text
				}
			}
		}
	}
	if !strings.Contains(toolResult, "Skill demo loaded") || strings.Contains(toolResult, "Use demo instructions") {
		t.Fatalf("tool result should be short and not contain skill body:\n%s", toolResult)
	}
	if !strings.Contains(contextText, "Skill demo instructions are now active") || !strings.Contains(contextText, "Use demo instructions") {
		t.Fatalf("skill context missing instructions:\n%s", contextText)
	}
}

func TestSessionInjectsTenantSkillCatalogInChatMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	provider := memorySkillProvider{items: map[string]skills.Skill{
		"tenant-review": skills.SkillFromContent("tenant-review", "tenant-review", "---\ndescription: Tenant review skill\n---\n# Tenant Review\n\nSecret tenant body"),
	}}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:         "test",
		MaxTurns:      1,
		PromptMode:    "chat",
		CWD:           t.TempDir(),
		SkillProvider: provider,
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 1 {
		t.Fatalf("systems = %+v", streamer.systems)
	}
	system := streamer.systems[0]
	if !strings.Contains(system, "tenant-review: Tenant review skill") {
		t.Fatalf("system prompt missing tenant skill metadata:\n%s", system)
	}
	if strings.Contains(system, "Secret tenant body") {
		t.Fatalf("system prompt leaked tenant skill body:\n%s", system)
	}
}

func TestSessionStructuredFastPathInlinesTenantSkillAndDisablesTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	provider := memorySkillProvider{items: map[string]skills.Skill{
		"teach": skills.SkillFromContent("teach", "teach", "---\ndescription: Teach skill\n---\n# Teach\n\nprofile_item_upsert.payload must use item_type, concept, note."),
	}}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:              "test",
		MaxTurns:           1,
		PromptMode:         "chat",
		CWD:                t.TempDir(),
		SkillProvider:      provider,
		DisableTools:       true,
		InlineTenantSkills: []string{"teach"},
		ResponseFormat: &anthropic.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &anthropic.ResponseFormatSchema{
				Name:   "teach_decision_v1",
				Strict: true,
				Schema: json.RawMessage(`{"type":"object"}`),
			},
		},
	})
	if _, err := session.Run(context.Background(), `{"contract_version":"teach_context_v1"}`, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 1 || !strings.Contains(streamer.systems[0], "profile_item_upsert.payload must use item_type, concept, note") {
		t.Fatalf("system did not inline tenant skill: %+v", streamer.systems)
	}
	if len(streamer.tools) != 1 || len(streamer.tools[0]) != 0 {
		t.Fatalf("tools = %+v", streamer.tools)
	}
	if len(streamer.responseFormats) != 1 || streamer.responseFormats[0] == nil || streamer.responseFormats[0].JSONSchema.Name != "teach_decision_v1" {
		t.Fatalf("response formats = %+v", streamer.responseFormats)
	}
}

func TestSkillToolLoadsTenantSkillBeforeLocalFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	provider := memorySkillProvider{items: map[string]skills.Skill{
		"tenant-only": skills.SkillFromContent("tenant-only", "tenant-only", "# Tenant Only\n\nTenant instructions"),
	}}
	res := skilltool.New().Run(context.Background(), json.RawMessage(`{"name":"tenant-only"}`), tools.Context{
		CWD:           t.TempDir(),
		SkillProvider: provider,
	})
	if res.IsError || !strings.Contains(res.Content, "Skill tenant-only loaded") {
		t.Fatalf("result = %+v", res)
	}
	if len(res.ContextMessages) != 1 || !strings.Contains(res.ContextMessages[0].Content[0].Text, "Tenant instructions") {
		t.Fatalf("context messages = %+v", res.ContextMessages)
	}
}

func TestSessionReinjectsActiveSkillContextAfterCompaction(t *testing.T) {
	skillMessage := anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: "<system-reminder>\nSkill demo instructions are now active.\n\nMATRIX_SKILL_ACTIVE\n</system-reminder>",
		}},
	}
	sess := &Session{}
	sess.rememberActiveSkillMessages([]anthropic.MessageParam{skillMessage})

	compactedMessages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: "Conversation summary so far:\n## Current Goal\nContinue."}},
	}}
	got := sess.withActiveSkillContextMessages(compactedMessages)

	if len(got) != 2 {
		t.Fatalf("messages len = %d, want compact summary plus active skill", len(got))
	}
	if !strings.Contains(allMessageText(got), "MATRIX_SKILL_ACTIVE") {
		t.Fatalf("active skill context was not re-injected: %+v", got)
	}
}

func TestSessionDoesNotDuplicatePreservedActiveSkillContext(t *testing.T) {
	skillMessage := anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: "<system-reminder>\nSkill demo instructions are now active.\n\nMATRIX_SKILL_ACTIVE\n</system-reminder>",
		}},
	}
	sess := &Session{}
	sess.rememberActiveSkillMessages([]anthropic.MessageParam{skillMessage})

	got := sess.withActiveSkillContextMessages([]anthropic.MessageParam{skillMessage})

	if len(got) != 1 {
		t.Fatalf("messages len = %d, want no duplicate active skill context", len(got))
	}
	if strings.Count(allMessageText(got), "MATRIX_SKILL_ACTIVE") != 1 {
		t.Fatalf("active skill context duplicated: %+v", got)
	}
}

func TestInitialActiveSkillMessagesReinjectedOnResume(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	skillMessage := anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: "<system-reminder>\nSkill resume-skill instructions are now active.\n\nRESUME_SKILL_ACTIVE\n</system-reminder>",
		}},
	}
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:                      "test",
		MaxTurns:                   1,
		CWD:                        project,
		PromptMode:                 "code",
		InitialMessages:            []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Conversation summary so far:\n## Current Goal\nResume compacted work."}}}},
		InitialActiveSkillMessages: []anthropic.MessageParam{skillMessage},
	})
	if _, err := querySession.Run(context.Background(), "continue", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	if !strings.Contains(text, "RESUME_SKILL_ACTIVE") {
		t.Fatalf("resumed request missing active skill context:\n%s", text)
	}
}

func TestSessionTenantSkillRuntimeMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	provider := memorySkillProvider{items: map[string]skills.Skill{
		"scoped": skills.SkillFromContent("scoped", "scoped", "---\nversion: 1.2.3\neffort: high\nallowed-tools:\n  - Echo\n---\n# Scoped\n"),
	}}
	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	streamer := &skillHookStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}, skilltool.New()), Options{
		Model:         "test",
		MaxTokens:     5000,
		MaxTurns:      3,
		CWD:           t.TempDir(),
		SkillProvider: provider,
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if streamer.calls < 2 {
		t.Fatalf("calls = %d", streamer.calls)
	}
	if streamer.thinking[1] == nil || streamer.thinking[1].Effort != "high" {
		t.Fatalf("thinking = %+v", streamer.thinking)
	}
	event := findTelemetryEventByTool(events, "tool.execution.finished", "Echo")
	if event.Name == "" {
		t.Fatalf("missing Echo telemetry event: %+v", events)
	}
	if got := event.Properties["active_skill"]; got != "scoped" {
		t.Fatalf("active_skill = %v", got)
	}
	if got := event.Properties["active_skill_source"]; got != "tenant" {
		t.Fatalf("active_skill_source = %v", got)
	}
	if got := event.Properties["active_skill_version"]; got != "1.2.3" {
		t.Fatalf("active_skill_version = %v", got)
	}
	if got := event.Properties["active_skill_fallback"]; got != false {
		t.Fatalf("active_skill_fallback = %v", got)
	}
}

func TestSessionSkillRuntimeMarksLocalFallback(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "scoped"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "scoped", "SKILL.md"), []byte("---\nversion: local-v1\neffort: medium\n---\n# Scoped\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	session := New(&skillHookStreamer{}, tools.NewRegistry(echoTool{}, skilltool.New()), Options{
		Model:         "test",
		MaxTurns:      3,
		CWD:           project,
		SkillProvider: memorySkillProvider{items: map[string]skills.Skill{}},
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	event := findTelemetryEventByTool(events, "tool.execution.finished", "Echo")
	if event.Name == "" {
		t.Fatalf("missing Echo telemetry event: %+v", events)
	}
	if got := event.Properties["active_skill_source"]; got != "project" {
		t.Fatalf("active_skill_source = %v", got)
	}
	if got := event.Properties["active_skill_version"]; got != "local-v1" {
		t.Fatalf("active_skill_version = %v", got)
	}
	if got := event.Properties["active_skill_fallback"]; got != true {
		t.Fatalf("active_skill_fallback = %v", got)
	}
}

func TestSessionSkillRuntimeUsesFilesystemDirectoryWithClaudeCompatibleInput(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	directory := filepath.Join(project, ".claude", "skills", "scoped")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\nname: scoped\n---\n# Scoped\n"), 0644); err != nil {
		t.Fatal(err)
	}
	session := New(&systemStreamer{}, tools.NewRegistry(), Options{CWD: project})
	runtime := session.applySkillRuntime(context.Background(), "Skill", json.RawMessage(`{"skill":"scoped"}`))
	if runtime == nil {
		t.Fatal("expected active Skill runtime")
	}
	if !runtime.FilesystemBacked || runtime.Directory != directory {
		t.Fatalf("runtime directory = %q filesystem_backed=%v, want %q true", runtime.Directory, runtime.FilesystemBacked, directory)
	}
}

func TestSessionTenantInlineSkillRuntimeHasNoFilesystemDirectory(t *testing.T) {
	fakePath := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(fakePath, []byte("# Untrusted path metadata\n"), 0644); err != nil {
		t.Fatal(err)
	}
	inline := skills.SkillFromContent("inline", "inline", "# Inline\n")
	inline.Path = fakePath
	inline.Root = filepath.Dir(fakePath)
	provider := memorySkillProvider{items: map[string]skills.Skill{
		"inline": inline,
	}}
	session := New(&systemStreamer{}, tools.NewRegistry(), Options{CWD: t.TempDir(), SkillProvider: provider})
	runtime := session.applySkillRuntime(context.Background(), "Skill", json.RawMessage(`{"name":"inline"}`))
	if runtime == nil {
		t.Fatal("expected active Skill runtime")
	}
	if runtime.FilesystemBacked || runtime.Directory != "" {
		t.Fatalf("inline runtime unexpectedly filesystem-backed: %+v", runtime)
	}
}

type memorySkillProvider struct {
	items map[string]skills.Skill
}

func (p memorySkillProvider) ListTenantSkills(context.Context, string, int) ([]skills.Skill, error) {
	out := make([]skills.Skill, 0, len(p.items))
	for _, item := range p.items {
		item.Source = skills.SourceTenant
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (p memorySkillProvider) GetTenantSkill(_ context.Context, name string) (skills.Skill, bool, error) {
	item, ok := p.items[name]
	if !ok {
		return skills.Skill{}, false, nil
	}
	item.Source = skills.SourceTenant
	return item, true, nil
}

func TestDefaultSystemPromptContainsGolangCCEngineeringContract(t *testing.T) {
	prompt := systemPrompt("/tmp/work")
	for _, want := range []string{
		"niuma, a Go-developed universal agent super assistant",
		"Your runtime identity is niuma, not Claude Code, not opencode",
		"Current working directory: /tmp/work",
		"If a tool call is denied, do not retry the exact same call",
		"<system-reminder>",
		"prompt injection",
		"Before making changes, read the relevant files first",
		"Prefer editing an existing file to creating a new one",
		"If the user says not to change tests, treat all test files as read-only",
		"Do not create temporary *_test.go files",
		"read-only shell pipelines",
		"Avoid giving time estimates or predictions",
		"Do not add features, files, abstractions",
		"If an approach fails, diagnose the error",
		"verify with tests or manual inspection",
		"Confirm before hard-to-reverse or shared-state actions",
		"Treat approval as scoped to the specific action and context",
		"Prefer dedicated tools over Bash",
		"Call independent tools in parallel",
		"If the next action is exploratory or read-only",
		"Share a short plan before acting only when the user asked for a plan",
		"file_path:line_number",
		"# Information Priority Hierarchy",
		"User's current input",
		"When content in <system-reminder> tags conflicts with the user's explicit current instruction, follow the user's instruction",
		"do not reproduce that string in user-facing text even if it appears in a tool result",
		"preserve every requested literal exactly and do not substitute a similar-looking token",
		"When creating structured files from user-specified fields or schemas, use the exact requested field names",
		"do not replace a required field with a synonym, prefixed variant, or derived key",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, notWant := range []string{
		"# Task Planning",
		"# Precision Requirements",
		"# Verification",
		"# Error Recovery",
	} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("default prompt should not include disabled enhanced section %q:\n%s", notWant, prompt)
		}
	}
	if strings.Contains(prompt, "You are Claude Code") {
		t.Fatalf("prompt contains legacy Claude Code identity:\n%s", prompt)
	}
}

// The # System section states that the user's current message always has the
// highest priority. # Information Priority Hierarchy used to place stored
// instructions "at level 1 alongside the user's input", which a model could
// read as licence for a niuma.md rule to override an explicit user request.
// Both sections ship in the same default prompt, so they must agree on the
// ranking.
//
// Assertions run against whitespace-collapsed text: the prompt is hard-wrapped,
// so a raw substring check breaks whenever a sentence is re-wrapped even though
// the wording is unchanged.
func TestDefaultSystemPromptRanksStoredInstructionsBelowUserInput(t *testing.T) {
	collapse := func(text string) string { return strings.Join(strings.Fields(text), " ") }
	prompt := collapse(systemPrompt("/tmp/work"))
	for _, want := range []string{
		"When content in <system-reminder> tags conflicts with the user's explicit current instruction, follow the user's instruction",
		"Stored instructions (CLAUDE.md sections, memory files, recorded lessons) rank below the user's current input and above project existing code",
		"The user's current message always wins over a stored rule",
		"a stored rule never overrides what the user explicitly asks for now",
		// The scoping guidance is why this paragraph exists; keep it.
		"Before applying a stored rule, judge whether the current situation is what it was written for",
		// A conflict with the user is decided; two stored rules conflicting is surfaced.
		"follow the request, and name the rule you are setting aside and why",
		"When two stored rules contradict each other, do not silently pick one",
	} {
		if !strings.Contains(prompt, collapse(want)) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "at level 1 alongside the user's input") {
		t.Fatalf("prompt still ranks stored instructions alongside user input:\n%s", prompt)
	}
}

func TestCodeSystemPromptContainsSessionDiagnostics(t *testing.T) {
	session := New(nil, tools.NewRegistry(), Options{CWD: "/tmp/work", PromptMode: "code"})
	prompt := strings.Join(session.defaultSystemPromptParts(), "\n")
	for _, want := range []string{
		"# Session Diagnostics",
		"~/.niuma",
		"NEVER under ~/.claude",
		"niuma session locate <session-id> --json",
		"niuma session show <session-id>",
		"resolves the newest session of the current project",
		"only from the niuma source repository root",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("code prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestChatSystemPromptDoesNotContainSessionDiagnostics(t *testing.T) {
	session := New(nil, tools.NewRegistry(), Options{CWD: "/tmp/work", PromptMode: "chat"})
	prompt := strings.Join(session.defaultSystemPromptParts(), "\n")
	for _, notWant := range []string{"# Session Diagnostics", "session locate <session-id>", "~/.niuma"} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("chat prompt contains local diagnostic guidance %q:\n%s", notWant, prompt)
		}
	}
}

func TestSystemPromptBlocksExposeDiagnosticSourcesWithoutSerializingThem(t *testing.T) {
	blocks := systemPromptBlocks("/tmp/work", "", "")
	if len(blocks) < 3 {
		t.Fatalf("system blocks = %+v", blocks)
	}
	gotSources := make([]string, 0, len(blocks))
	for _, block := range blocks {
		gotSources = append(gotSources, block.Source)
	}
	got := strings.Join(gotSources, ",")
	for _, want := range []string{"identity", "static_prompt", "dynamic_prompt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sources missing %q: %q", want, got)
		}
	}
	withSkill := appendSystemBlockWithSource(blocks, "# Available Skills\n- test", "skills_catalog")
	if withSkill[len(withSkill)-1].Source != "skills_catalog" {
		t.Fatalf("skill block source = %+v", withSkill[len(withSkill)-1])
	}
	encoded, err := json.Marshal(withSkill)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "static_prompt") || strings.Contains(string(encoded), "dynamic_prompt") || strings.Contains(string(encoded), "skills_catalog") {
		t.Fatalf("diagnostic source leaked into request JSON: %s", encoded)
	}
}

func TestMaybeMoveSkillsCatalogIntoStablePrefixIsFeatureGated(t *testing.T) {
	blocks := []anthropic.SystemBlock{
		{Type: "text", Text: "identity", Source: "identity"},
		{Type: "text", Text: "static", Source: "static_prompt"},
		{Type: "text", Text: "dynamic", Source: "dynamic_prompt"},
		{Type: "text", Text: "tenant", Source: "tenant_skills_catalog"},
		{Type: "text", Text: "skills", Source: "skills_catalog"},
	}

	t.Setenv("NIUMA_STABLE_PREFIX_SKILLS", "0")
	got := maybeMoveSkillsCatalogIntoStablePrefix(blocks)
	if strings.Join(systemBlockSources(got), ",") != "identity,static_prompt,dynamic_prompt,tenant_skills_catalog,skills_catalog" {
		t.Fatalf("feature disabled moved blocks: %+v", systemBlockSources(got))
	}

	t.Setenv("NIUMA_STABLE_PREFIX_SKILLS", "1")
	got = maybeMoveSkillsCatalogIntoStablePrefix(blocks)
	if strings.Join(systemBlockSources(got), ",") != "identity,static_prompt,skills_catalog,dynamic_prompt,tenant_skills_catalog" {
		t.Fatalf("feature enabled order = %+v", systemBlockSources(got))
	}
	if got[2].Text != "skills" || got[4].Text != "tenant" {
		t.Fatalf("unexpected block contents after move: %+v", got)
	}
}

func TestSessionCanPlaceSkillsCatalogBeforeDynamicPromptWhenStablePrefixGateEnabled(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "go-review"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "go-review", "SKILL.md"), []byte("---\nname: go-review\ndescription: Review Go changes\n---\n# Go Review\n\nSecret body"), 0644); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, gate string) []anthropic.SystemBlock {
		t.Helper()
		t.Setenv("NIUMA_STABLE_PREFIX_SKILLS", gate)
		streamer := &systemStreamer{}
		session := New(streamer, tools.NewRegistry(echoTool{}), Options{
			Model:      "test",
			MaxTurns:   1,
			CWD:        project,
			PromptMode: "code",
		})
		if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
			t.Fatal(err)
		}
		if len(streamer.systems) != 1 || len(streamer.blocks) != 1 {
			t.Fatalf("systems=%+v blocks=%+v", streamer.systems, streamer.blocks)
		}
		if joined := joinSystemBlocks(streamer.blocks[0]); joined != streamer.systems[0] {
			t.Fatalf("system string diverged from blocks\njoined:\n%s\nsystem:\n%s", joined, streamer.systems[0])
		}
		return streamer.blocks[0]
	}

	defaultBlocks := run(t, "0")
	if dynamic, skills := systemBlockSourceIndex(defaultBlocks, "dynamic_prompt"), systemBlockSourceIndex(defaultBlocks, "skills_catalog"); dynamic < 0 || skills < 0 || dynamic >= skills {
		t.Fatalf("default order dynamic=%d skills=%d sources=%+v", dynamic, skills, systemBlockSources(defaultBlocks))
	}

	gatedBlocks := run(t, "1")
	if dynamic, skills := systemBlockSourceIndex(gatedBlocks, "dynamic_prompt"), systemBlockSourceIndex(gatedBlocks, "skills_catalog"); dynamic < 0 || skills < 0 || skills >= dynamic {
		t.Fatalf("gated order dynamic=%d skills=%d sources=%+v", dynamic, skills, systemBlockSources(gatedBlocks))
	}
}

func TestSessionRedactsExplicitForbiddenOutputLiteralFromStreamAndResult(t *testing.T) {
	streamer := &forbiddenOutputStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	res, err := session.Run(context.Background(), "Read the files. Print APG_PUBLIC_SUMMARY_MARKER and PUBLIC_SUMMARY_DONE, but do not print APG_PRIVATE_MARKER_2.", &out)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{out.String(), res.Response} {
		if strings.Contains(got, "APG_PRIVATE_MARKER_2") {
			t.Fatalf("forbidden marker leaked in %q", got)
		}
	}
	if !strings.Contains(out.String(), "APG_PUBLIC_SUMMARY_MARKER") || !strings.Contains(out.String(), "PUBLIC_SUMMARY_DONE") {
		t.Fatalf("stdout missing public output: %q", out.String())
	}
	if !strings.Contains(res.Response, "[redacted]") {
		t.Fatalf("response missing redaction marker: %q", res.Response)
	}
}

func TestSessionCompletesExplicitExactOutputLiterals(t *testing.T) {
	streamer := &exactOutputStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	res, err := session.Run(context.Background(), "Read public.txt and print exactly APG_PUBLIC_MARKER_1 and PUBLIC_READ_DONE.", &out)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{out.String(), res.Response} {
		if !strings.Contains(got, "APG_PUBLIC_MARKER_1") || !strings.Contains(got, "PUBLIC_READ_DONE") {
			t.Fatalf("exact output literals not preserved in %q", got)
		}
	}
	if strings.Contains(out.String(), "PUBLIC_READ_DONE\nPUBLIC_READ_DONE") {
		t.Fatalf("required literal duplicated in stdout: %q", out.String())
	}
}

func TestSessionCompletesExplicitMustIncludeOutputLiterals(t *testing.T) {
	streamer := &exactOutputStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	prompt := "Create reports. Final answer must include PARENT_FOLLOW_UP_RESOLVED, FOLLOW_UP_NEXT_ACTION, and verification/canary-log.txt."
	res, err := session.Run(context.Background(), prompt, &out)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{out.String(), res.Response} {
		for _, want := range []string{"PARENT_FOLLOW_UP_RESOLVED", "FOLLOW_UP_NEXT_ACTION", "verification/canary-log.txt"} {
			if !strings.Contains(got, want) {
				t.Fatalf("must-include literal %q not preserved in %q", want, got)
			}
		}
	}
}

func TestSessionDoesNotCompleteForbiddenMustIncludeLiteral(t *testing.T) {
	streamer := &exactOutputStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
	})
	var out strings.Builder
	prompt := "Print exactly PUBLIC_READ_DONE. Do not include PRIVATE_SECRET_MARKER."
	res, err := session.Run(context.Background(), prompt, &out)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []string{out.String(), res.Response} {
		if !strings.Contains(got, "PUBLIC_READ_DONE") {
			t.Fatalf("public marker missing in %q", got)
		}
		if strings.Contains(got, "PRIVATE_SECRET_MARKER") {
			t.Fatalf("forbidden marker was completed in %q", got)
		}
	}
}

type systemStreamer struct {
	systems         []string
	blocks          [][]anthropic.SystemBlock
	messages        [][]anthropic.MessageParam
	maxTokens       []int
	thinking        []*anthropic.ThinkingConfig
	tools           [][]anthropic.ToolDefinition
	responseFormats []*anthropic.ResponseFormat
}

func (s *systemStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.systems = append(s.systems, req.System)
	s.blocks = append(s.blocks, req.SystemBlocks)
	s.messages = append(s.messages, req.Messages)
	s.maxTokens = append(s.maxTokens, req.MaxTokens)
	s.thinking = append(s.thinking, req.Thinking)
	s.tools = append(s.tools, req.Tools)
	s.responseFormats = append(s.responseFormats, req.ResponseFormat)
	if err := cb.OnText("ok"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}},
		StopReason: "end_turn",
	}, nil
}

func systemBlockSources(blocks []anthropic.SystemBlock) []string {
	sources := make([]string, 0, len(blocks))
	for _, block := range blocks {
		sources = append(sources, block.Source)
	}
	return sources
}

func systemBlockSourceIndex(blocks []anthropic.SystemBlock, source string) int {
	for i, block := range blocks {
		if block.Source == source {
			return i
		}
	}
	return -1
}

type forbiddenOutputStreamer struct {
	calls int
}

func (s *forbiddenOutputStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_echo",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"The private marker is APG_PRIVATE_MARKER_2."}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if cb.OnText != nil {
		if err := cb.OnText("APG_PUBLIC_SUMMARY_MARKER\nAPG_PRIVATE_"); err != nil {
			return nil, err
		}
		if err := cb.OnText("MARKER_2\nPUBLIC_SUMMARY_DONE"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: "APG_PUBLIC_SUMMARY_MARKER\nAPG_PRIVATE_MARKER_2\nPUBLIC_SUMMARY_DONE",
		}}},
		StopReason: "end_turn",
	}, nil
}

type exactOutputStreamer struct {
	calls int
}

func (s *exactOutputStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_echo",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"APG_PUBLIC_MARKER_1"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if cb.OnText != nil {
		if err := cb.OnText("APG_PUBLIC_MARKER_1\n\nPUBLIC_MARKER_DONE"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: "APG_PUBLIC_MARKER_1\n\nPUBLIC_MARKER_DONE",
		}}},
		StopReason: "end_turn",
	}, nil
}

func TestSessionDefaultMaxTokensByPromptMode(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(), Options{Model: "test", MaxTurns: 1, CWD: project, PromptMode: "code"})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := streamer.maxTokens[0]; got != defaults.CodeMaxTokens {
		t.Fatalf("code max_tokens = %d, want %d", got, defaults.CodeMaxTokens)
	}

	streamer = &systemStreamer{}
	session = New(streamer, tools.NewRegistry(), Options{Model: "test", MaxTurns: 1, CWD: project, PromptMode: "chat"})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := streamer.maxTokens[0]; got != defaults.ChatMaxTokens {
		t.Fatalf("chat max_tokens = %d, want %d", got, defaults.ChatMaxTokens)
	}

	streamer = &systemStreamer{}
	session = New(streamer, tools.NewRegistry(), Options{Model: "test", MaxTurns: 1, MaxTokens: 123, CWD: project, PromptMode: "code"})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := streamer.maxTokens[0]; got != 123 {
		t.Fatalf("explicit max_tokens = %d, want 123", got)
	}
}

type compactingStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *compactingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		summary := strings.Join([]string{
			"## Current Goal",
			"Continue implementation.",
			"## User Preferences / Constraints",
			"Keep tests updated.",
			"## Decisions Made",
			"Use auto compact.",
			"## Files / Code Changed",
			"internal/query/query.go",
			"/tmp/session/tool-results/toolu_task.txt",
			"## Commands / Test Results",
			"go test ./internal/query",
			"go test ./internal/query -run TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary -count=1",
			"## Open Tasks",
			"Run tests.",
			"## Known Issues / Risks",
			"None.",
			"## Important Raw Facts",
			"internal/query/query.go and go test ./internal/query are important.",
		}, "\n")
		if cb.OnText != nil {
			if err := cb.OnText(summary); err != nil {
				return nil, err
			}
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: summary}}}, StopReason: "end_turn"}, nil
	}
	if cb.OnText != nil {
		if err := cb.OnText("ok"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}}, StopReason: "end_turn"}, nil
}

type singleToolUseStreamer struct {
	toolName string
	input    json.RawMessage
}

func (s *singleToolUseStreamer) StreamMessages(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu_1",
			Name:  s.toolName,
			Input: s.input,
		}}},
		StopReason: "tool_use",
	}, nil
}

type dynamicSkillCatalogStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *dynamicSkillCatalogStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_path",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"internal/secret/file.go"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type skillContextStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *skillContextStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_skill",
				Name:  "Skill",
				Input: json.RawMessage(`{"name":"demo"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func TestSessionAutoCompactsBeforeModelRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	streamer := &compactingStreamer{}
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:    "gpt-5.5",
		MaxTurns: 1,
		CWD:      t.TempDir(),
		Recorder: recorder,
		InitialMessages: []anthropic.MessageParam{
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Please inspect internal/query/query.go and run go test ./internal/query."}}},
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "I will do that."}}},
		},
		AutoCompact: compact.Config{
			Enabled:               true,
			DefaultThresholdRatio: 0.05,
			PreserveRecentRounds:  1,
			ModelContext:          map[string]int{"gpt-5.5": 500},
		},
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "Continue.", &out); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want summary + main request", len(streamer.requests))
	}
	mainReq := streamer.requests[1]
	if len(mainReq.Messages) != 2 {
		t.Fatalf("main request messages = %+v", mainReq.Messages)
	}
	if !strings.Contains(mainReq.Messages[0].Content[0].Text, "Conversation summary so far:") {
		t.Fatalf("missing compact summary in main request: %+v", mainReq.Messages)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Type == "compact_summary" {
			found = true
			if !strings.Contains(entry.Content, "## Current Goal") || !strings.Contains(entry.Content, "## Runtime Extracted Facts") || len(entry.CompactMetadata) == 0 {
				t.Fatalf("compact entry = %+v", entry)
			}
		}
	}
	if !found {
		t.Fatalf("compact_summary not recorded: %+v", entries)
	}
	resumeMessages := MessagesFromTranscript(entries)
	var hasCurrentPrompt, hasRuntimeFacts bool
	for _, message := range resumeMessages {
		for _, block := range message.Content {
			if block.Type == "text" && block.Text == "Continue." {
				hasCurrentPrompt = true
			}
			if block.Type == "text" && strings.Contains(block.Text, "## Runtime Extracted Facts") && strings.Contains(block.Text, "Files:\n- ./internal/query") {
				hasRuntimeFacts = true
			}
		}
	}
	if !hasCurrentPrompt {
		t.Fatalf("resume lost preserved prompt after compact: %+v", resumeMessages)
	}
	if !hasRuntimeFacts {
		t.Fatalf("resume lost runtime facts after compact: %+v", resumeMessages)
	}
}

func TestSessionAutoCompactPreservesBackgroundAgentRuntimeStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &compactingStreamer{}
	taskStore := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          77,
		AgentName:   "auditor",
		Status:      agenttasks.StatusCompleted,
		Description: "Summarize compacted agent evidence",
		ResultJSON:  `{"content":"COMPACT_AGENT_RESULT_MARKER","output_file":"/tmp/compact-agent.output","turns":3}`,
	}}}
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:      "gpt-5.5",
		MaxTurns:   1,
		CWD:        t.TempDir(),
		PromptMode: "code",
		TaskStore:  taskStore,
		InitialMessages: []anthropic.MessageParam{
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Please inspect internal/query/query.go and run go test ./internal/query."}}},
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "I will do that."}}},
		},
		AutoCompact: compact.Config{
			Enabled:               true,
			DefaultThresholdRatio: 0.05,
			PreserveRecentRounds:  1,
			ModelContext:          map[string]int{"gpt-5.5": 500},
		},
	})
	if _, err := querySession.Run(context.Background(), "Continue.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want summary + main request", len(streamer.requests))
	}
	text := allMessageText(streamer.requests[1].Messages)
	for _, want := range []string{
		"Conversation summary so far:",
		"## Background agent tasks",
		"#77 auditor completed: Summarize compacted agent evidence",
		"completion notification: result is ready; use the result preview, transcript_path, and output_file below because AgentGet is not available",
		"result preview: COMPACT_AGENT_RESULT_MARKER",
		"output_file: /tmp/compact-agent.output",
		"turns: 3",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compacted request missing %q:\n%s", want, text)
		}
	}
}

func TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "61616161-6161-4616-8616-616161616161")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	streamer := &compactingStreamer{}
	persistedCapabilitySummary := strings.Join([]string{
		"<persisted-output>",
		"Output too large (4096 bytes). Full output saved to: /tmp/session/tool-results/toolu_task.txt",
		"",
		"Capability loop summary preserved from full output:",
		"- evidence: PERSISTED_QUERY_COMPACT_EVIDENCE: persisted summary reached compact facts.",
		"- assumptions: Parent sees persisted-output summary in history.",
		"- unknowns: Full output was not reread after persistence.",
		"- verification: go test ./internal/query -run TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary -count=1",
		"- risks: Compact could otherwise drop persisted-output next_action.",
		"- next_action: PERSISTED_QUERY_COMPACT_NEXT_ACTION: continue from compact facts.",
		"",
		"Preview (first 2000 bytes):",
		strings.Repeat("history-prefix ", 80),
		"</persisted-output>",
	}, "\n")
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:      "gpt-5.5",
		MaxTurns:   1,
		CWD:        t.TempDir(),
		PromptMode: "code",
		Recorder:   recorder,
		InitialMessages: []anthropic.MessageParam{
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Please inspect a long Task result."}}},
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_task", Name: "Task", Input: json.RawMessage(`{"description":"long task","prompt":"inspect"}`)}}},
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_task", Content: persistedCapabilitySummary}}},
		},
		AutoCompact: compact.Config{
			Enabled:               true,
			DefaultThresholdRatio: 0.05,
			PreserveRecentRounds:  1,
			ModelContext:          map[string]int{"gpt-5.5": 500},
		},
	})
	if _, err := querySession.Run(context.Background(), "Continue.", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want summary + main request", len(streamer.requests))
	}
	text := allMessageText(streamer.requests[1].Messages)
	for _, want := range []string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"Capability Loop Evidence:",
		"PERSISTED_QUERY_COMPACT_EVIDENCE: persisted summary reached compact facts.",
		"Capability Loop Unknowns:",
		"Full output was not reread after persistence.",
		"Capability Loop Verification:",
		"go test ./internal/query -run TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary -count=1",
		"Capability Loop Risks:",
		"Compact could otherwise drop persisted-output next_action.",
		"Capability Loop Next Actions:",
		"PERSISTED_QUERY_COMPACT_NEXT_ACTION: continue from compact facts.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compacted request missing %q:\n%s", want, text)
		}
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	resumeText := allMessageText(MessagesFromTranscript(entries))
	for _, want := range []string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"Capability Loop Evidence:",
		"PERSISTED_QUERY_COMPACT_EVIDENCE: persisted summary reached compact facts.",
		"Capability Loop Unknowns:",
		"Full output was not reread after persistence.",
		"Capability Loop Verification:",
		"go test ./internal/query -run TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary -count=1",
		"Capability Loop Risks:",
		"Compact could otherwise drop persisted-output next_action.",
		"Capability Loop Next Actions:",
		"PERSISTED_QUERY_COMPACT_NEXT_ACTION: continue from compact facts.",
		"Continue.",
	} {
		if !strings.Contains(resumeText, want) {
			t.Fatalf("resumed request missing %q:\n%s", want, resumeText)
		}
	}
}

func TestSessionAddsImageAttachmentsAsContentBlocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	imagePath := filepath.Join(t.TempDir(), "clip.png")
	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(), Options{
		Model:     "test",
		MaxTurns:  1,
		MaxTokens: 123,
		Attachments: []Attachment{{
			ID:        "1",
			Type:      "image",
			MediaType: "image/png",
			Name:      "clip.png",
			Path:      imagePath,
		}},
	})
	if _, err := session.Run(context.Background(), "describe [Image #1]", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	content := streamer.messages[0][0].Content
	if len(content) != 2 {
		t.Fatalf("content = %+v", content)
	}
	if content[0].Type != "text" || !strings.Contains(content[0].Text, "describe [Image #1]") {
		t.Fatalf("text block = %+v", content[0])
	}
	if content[1].Type != "image" || content[1].Source == nil || content[1].Source.Type != "base64" || content[1].Source.MediaType != "image/png" || content[1].Source.Data == "" {
		t.Fatalf("image block = %+v", content[1])
	}
}

func TestSessionAddsInlineImageAttachmentAsContentBlock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(), Options{
		Model:    "test",
		MaxTurns: 1,
		Attachments: []Attachment{{
			Type:       "image",
			MediaType:  "image/png",
			Name:       "clip.png",
			InlineData: base64.StdEncoding.EncodeToString([]byte("png-bytes")),
		}},
	})
	if _, err := session.Run(context.Background(), "describe the image", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 || len(streamer.messages[0][0].Content) != 2 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	image := streamer.messages[0][0].Content[1]
	if image.Type != "image" || image.Source == nil || image.Source.Type != "base64" || image.Source.Data == "" || image.Source.MediaType != "image/png" {
		t.Fatalf("image block = %+v", image)
	}
}

func TestSessionInjectsOutputStyleLanguageAndCacheBlocks(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("NIUMA_PROMPT_CACHE_1H_ALLOWLIST", "repl_main_thread*")
	if err := os.MkdirAll(filepath.Join(project, ".claude", "output-styles"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "output-styles", "focused.md"), []byte("---\nname: Focused\nkeep-coding-instructions: false\n---\nAnswer tersely."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "settings.json"), []byte(`{"outputStyle":"Focused","language":"Chinese"}`), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:     "test",
		MaxTurns:  1,
		MaxTokens: 123,
		CWD:       project,
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 1 || len(streamer.blocks) != 1 {
		t.Fatalf("systems=%d blocks=%d", len(streamer.systems), len(streamer.blocks))
	}
	system := streamer.systems[0]
	for _, want := range []string{"# Output Style: Focused", "Answer tersely.", "# Language", "Always respond in Chinese"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "# Doing Tasks") {
		t.Fatalf("keep-coding-instructions=false should omit coding section:\n%s", system)
	}
	for _, notWant := range []string{"# Task Planning", "# Precision Requirements", "# Information Priority Hierarchy", "# Verification", "# Error Recovery", "# Workflow Closure"} {
		if strings.Contains(system, notWant) {
			t.Fatalf("keep-coding-instructions=false should omit enhanced coding section %q:\n%s", notWant, system)
		}
	}
	blocks := streamer.blocks[0]
	if len(blocks) < 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !hasCacheTTL(blocks, "1h") || !hasCacheScope(blocks, "global") {
		t.Fatalf("cache blocks = %+v", blocks)
	}
	for _, block := range blocks {
		if strings.Contains(block.Text, "# Language") && block.CacheControl != nil {
			t.Fatalf("dynamic block should not carry global cache control: %+v", block)
		}
	}
}

func TestSystemPromptSectionRegistryFeatureGates(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("NIUMA_FEATURE_TOKEN_BUDGET", "true")
	t.Setenv("NIUMA_FEATURE_CACHED_MICROCOMPACT", "true")
	t.Setenv("NIUMA_FEATURE_KAIROS_BRIEF", "true")
	t.Setenv("NIUMA_FEATURE_ENHANCED_SYSTEM_PROMPT", "true")
	t.Setenv("NIUMA_FEATURE_ENHANCED_BEHAVIOR_CONSTRAINTS", "true")
	t.Setenv("NIUMA_BRIEF", "true")
	t.Setenv("NIUMA_SCRATCHPAD_DIR", filepath.Join(project, ".claude", "scratch"))
	t.Setenv("NIUMA_ANT_MODEL_OVERRIDE_SUFFIX", "Ant-only suffix")
	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "settings.json"), []byte(`{"mcpServers":{"docs":{"command":"docs-mcp"}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "claude-sonnet-4-6",
		MaxTurns: 1,
		CWD:      project,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"# Session Guidance",
		"Enabled tools for this session: Echo",
		"# Environment",
		"Ant-only suffix",
		"# MCP Server Instructions",
		"Configured MCP servers: docs",
		"# Scratchpad Directory",
		"# Function Result Clearing",
		"Length limits: keep text between tool calls",
		"token target",
		"# Brief Mode",
		"# Task Planning",
		"# Precision Requirements",
		"# Information Priority Hierarchy",
		"# Verification",
		"# Error Recovery",
		"# Workflow Closure",
		"identify the source of truth and any derived files",
		"git rebase --continue",
		"GIT_EDITOR=true",
		"git status --short --branch",
		"Do not automatically abort or skip a rebase",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q:\n%s", want, system)
		}
	}
	event := findTelemetryEvent(events, "query.prompt_context")
	if event.Name == "" {
		t.Fatalf("missing query.prompt_context event: %+v", events)
	}
	manifest, ok := event.Properties["context_manifest"].(ContextManifest)
	if !ok {
		t.Fatalf("missing context manifest: %+v", event.Properties)
	}
	if got := manifest.CodeContext.FeatureSections; !containsString(got, "enhanced_system_prompt") || !containsString(got, "enhanced_behavior_constraints") || !containsString(got, "workflow_closure") {
		t.Fatalf("feature sections = %+v", got)
	}
}

func TestPromptContextManifestRecordsWorkflowRules(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	child := filepath.Join(project, "docs")
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_FEATURE_ENHANCED_SYSTEM_PROMPT", "true")
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project guidance")
	mustWriteQueryTest(t, filepath.Join(project, "AGENTS.md"), "project workflow rule should not load")
	mustWriteQueryTest(t, filepath.Join(child, "AGENTS.md"), "docs workflow rule")
	mustWriteQueryTest(t, filepath.Join(project, ".claude", "workflows", "docs.md"), "---\npaths: [docs/**]\n---\ndocs workflow rule")

	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        child,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "update docs/api.md", io.Discard); err != nil {
		t.Fatal(err)
	}
	event := findTelemetryEvent(events, "query.prompt_context")
	if event.Name == "" {
		t.Fatalf("missing query.prompt_context event: %+v", events)
	}
	manifest, ok := event.Properties["context_manifest"].(ContextManifest)
	if !ok {
		t.Fatalf("missing manifest: %+v", event.Properties)
	}
	if manifest.CodeContext.WorkflowRules != 2 || manifest.CodeContext.ByType["Workflow"] != 2 {
		t.Fatalf("code context = %+v", manifest.CodeContext)
	}
	if manifest.CodeContext.DocumentBytes == 0 || manifest.CodeContext.WorkflowBytes == 0 || manifest.CodeContext.ByTypeBytes["Workflow"] == 0 {
		t.Fatalf("missing code context byte accounting = %+v", manifest.CodeContext)
	}
	var workflowDocs, projectGuidance int
	for _, doc := range manifest.CodeContext.DocumentSummary {
		if doc.Path != "" || doc.Parent != "" {
			t.Fatalf("telemetry document summary leaked path data: %+v", manifest.CodeContext.DocumentSummary)
		}
		switch {
		case doc.Workflow && doc.Type == "Workflow" && doc.Bytes == len("docs workflow rule"):
			workflowDocs++
		case doc.Type == "Project" && doc.Bytes == len("project guidance"):
			projectGuidance++
		}
	}
	if workflowDocs != 2 || projectGuidance != 1 {
		t.Fatalf("document summary = %+v", manifest.CodeContext.DocumentSummary)
	}
	if got := event.Properties["code_context.workflow_rules"]; got != 2 {
		t.Fatalf("workflow property = %#v", got)
	}
	if !containsString(manifest.CodeContext.FeatureSections, "workflow_closure") {
		t.Fatalf("feature sections = %+v", manifest.CodeContext.FeatureSections)
	}
}

func TestSystemPromptSectionRegistryUsesGrowthBookConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("NIUMA_BRIEF", "true")
	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "settings.json"), []byte(`{"growthbook":{"enabled":true,"features":{"TOKEN_BUDGET":{"enabled":true,"userTypes":["ant"],"querySources":["repl_*"]},"CACHED_MICROCOMPACT":{"enabled":true,"models":["claude-*"]},"KAIROS_BRIEF":{"enabled":true,"modes":["repl_main_thread"]}}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "claude-sonnet-4-6",
		MaxTurns: 1,
		CWD:      project,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	system := streamer.systems[0]
	for _, want := range []string{"token target", "# Function Result Clearing", "# Brief Mode"} {
		if !strings.Contains(system, want) {
			t.Fatalf("growthbook feature missing %q:\n%s", want, system)
		}
	}
}

func TestSystemPromptSectionRegistryProactivePath(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_FEATURE_PROACTIVE", "true")
	t.Setenv("NIUMA_PROACTIVE_ACTIVE", "true")
	t.Setenv("NIUMA_LANGUAGE", "Chinese")
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("proactive-memory"), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 1, CWD: project})
	if _, err := querySession.Run(context.Background(), "tick", io.Discard); err != nil {
		t.Fatal(err)
	}
	system := streamer.systems[0]
	for _, want := range []string{
		"autonomous agent",
		"# Cyber Safety",
		"# Environment",
		"# Language",
		"# Proactive Work",
		"# Error Recovery",
		"GIT_EDITOR=true git rebase --continue",
		"# Session Diagnostics",
		"niuma session locate <session-id> --json",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("proactive system missing %q:\n%s", want, system)
		}
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 || !strings.Contains(messageText(streamer.messages[0][0]), "proactive-memory") {
		t.Fatalf("proactive memory user context missing: %+v", streamer.messages)
	}
	if strings.Contains(system, "# Doing Tasks") {
		t.Fatalf("proactive simple path should not include normal static coding sections:\n%s", system)
	}
}

func TestSystemPromptSectionRegistryCachesStableSectionsAndRefreshesUncached(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", filepath.Join(project, "config"))
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("memory-v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "settings.json"), []byte(`{"mcpServers":{"one":{"command":"one"}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 1, CWD: project})
	if _, err := querySession.Run(context.Background(), "first", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("memory-v2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "settings.json"), []byte(`{"mcpServers":{"two":{"command":"two"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := querySession.Run(context.Background(), "second", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.systems) != 2 {
		t.Fatalf("systems = %d", len(streamer.systems))
	}
	first, second := streamer.systems[0], streamer.systems[1]
	if !strings.Contains(first, "Configured MCP servers: one") {
		t.Fatalf("first system unexpected:\n%s", first)
	}
	if !strings.Contains(second, "Configured MCP servers: two") || strings.Contains(second, "Configured MCP servers: one") {
		t.Fatalf("uncached MCP section should refresh:\n%s", second)
	}
	if !strings.Contains(messageText(streamer.messages[0][0]), "memory-v1") {
		t.Fatalf("first memory user context missing: %+v", streamer.messages[0])
	}
	if !strings.Contains(messageText(streamer.messages[1][0]), "memory-v2") {
		t.Fatalf("second memory user context should refresh: %+v", streamer.messages[1])
	}
}

func TestSessionUsesForcedPluginOutputStyle(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(project, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "config", "config.yaml"), []byte("outputStyle: Explanatory\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "plugins", "demo", "output-styles"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "plugins", "demo", ".codex-plugin", "plugin.json"), []byte(`{"name":"demo"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".claude", "plugins", "demo", "output-styles", "forced.md"), []byte("---\nname: Forced\nforce-for-plugin: true\n---\nPlugin forced style."), 0644); err != nil {
		t.Fatal(err)
	}

	streamer := &systemStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      project,
	})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	system := streamer.systems[0]
	if !strings.Contains(system, "# Output Style: demo:Forced") || !strings.Contains(system, "Plugin forced style.") {
		t.Fatalf("system = %s", system)
	}
	if strings.Contains(system, "# Explanatory Style Active") {
		t.Fatalf("forced plugin style should override configured style:\n%s", system)
	}
}

func TestPromptModeCodeLoadsMemoryAsUserContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project instruction")

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	firstMessage := streamer.messages[0][0]
	first := messageText(firstMessage)
	if firstMessage.Role != "user" || len(firstMessage.Content) != 2 || !strings.Contains(first, "project instruction") || !strings.Contains(first, "<system-reminder>") || firstMessage.Content[1].Text != "hi" {
		t.Fatalf("first message = role %q content=%+v text %q", firstMessage.Role, firstMessage.Content, first)
	}
}

func TestClaudeCompatiblePromptProfileLoadsMemoryIntoSystemBlock(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(promptProfileEnv, promptProfileClaudeCompatible)
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project instruction")

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	if got := messageText(streamer.messages[0][0]); !strings.Contains(got, "# claudeMd") || !strings.Contains(got, "project instruction") || !strings.Contains(got, "# currentDate") {
		t.Fatalf("compatible profile user context missing workspace guidance: %q", got)
	}
	if got := messageText(streamer.messages[0][0]); strings.Contains(got, "# auto memory") {
		t.Fatalf("compatible profile duplicated auto memory protocol in user message: %q", got)
	}
	if len(streamer.blocks) != 1 || len(streamer.blocks[0]) != 3 {
		t.Fatalf("system blocks = %+v", streamer.blocks)
	}
	last := streamer.blocks[0][2]
	if !strings.Contains(last.Text, "# auto memory") {
		t.Fatalf("memory system block missing protocol: %+v", last)
	}
	if strings.Contains(last.Text, "project instruction") {
		t.Fatalf("memory system block duplicated workspace guidance: %+v", last)
	}
	wantMemoryDir := memory.ClaudeCodeProjectMemoryDir(project)
	for _, want := range []string{
		wantMemoryDir,
		"This directory already exists - write to it directly with the Write tool",
		"## Types of memory",
		"<types>",
		"<name>user</name>",
		"senior software engineer differently than a student",
		"Avoid writing memories about the user that could be viewed as a negative judgement",
		"<name>feedback</name>",
		"Record from failure and success",
		"confirmations are quieter",
		"<name>project</name>",
		"<name>reference</name>",
		"If the user explicitly asks you to remember something, save it immediately",
		"For explicitly read-only tasks, do not write memory unless the user directly asks you to remember or forget something.",
		"## What NOT to save in memory",
		"These exclusions apply even when the user explicitly asks you to save",
		"PR list or activity summary",
		"surprising or non-obvious",
		"Saving a memory is a two-step process",
		"metadata:",
		"  type: {{user, feedback, project, reference}}",
		"`MEMORY.md` is an index, not a memory",
		"## Before recommending from memory",
		"A memory that names a specific function, file, or flag is a claim",
		"If the memory names a file path: check the file exists.",
		"If the memory names a function or flag: grep for it.",
		"If the user says to ignore or not use memory",
		"proceed as if MEMORY.md were empty",
		"Do not apply remembered facts",
		"not just asking about history",
		"trust what you observe now rather than acting on the stale memory",
		"use a plan rather than saving this information to memory",
		"use tasks or progress notes instead of saving to memory",
	} {
		if !strings.Contains(last.Text, want) {
			t.Fatalf("memory system block missing %q:\n%s", want, last.Text)
		}
	}
	if last.Source != "dynamic_prompt" {
		t.Fatalf("memory system block source = %q", last.Source)
	}
	if memoryIdx, envIdx := strings.Index(last.Text, "# auto memory"), strings.Index(last.Text, "# Environment"); memoryIdx < 0 || envIdx < 0 || memoryIdx > envIdx {
		t.Fatalf("compatible auto memory should appear before environment; memory=%d env=%d:\n%s", memoryIdx, envIdx, last.Text)
	}
}

func TestClaudeCompatiblePromptProfileInjectsAutoMemoryWithoutWorkspaceDocs(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(promptProfileEnv, promptProfileClaudeCompatible)

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.blocks) != 1 || len(streamer.blocks[0]) != 3 {
		t.Fatalf("system blocks = %+v", streamer.blocks)
	}
	last := streamer.blocks[0][2]
	if !strings.Contains(last.Text, "# auto memory") {
		t.Fatalf("compatible profile should inject auto memory protocol even without workspace docs:\n%s", last.Text)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	if got := messageText(streamer.messages[0][0]); strings.Contains(got, "# claudeMd") {
		t.Fatalf("workspace docs should not create a claudeMd user context message: %q", got)
	}
}

func TestClaudeCompatiblePromptProfileFormatsGitStatusAfterEnvironment(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(promptProfileEnv, promptProfileClaudeCompatible)
	runQueryTestGit(t, project, "init")
	mustWriteQueryTest(t, filepath.Join(project, "tracked.txt"), "git status fixture\n")

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.blocks) != 1 || len(streamer.blocks[0]) != 3 {
		t.Fatalf("system blocks = %+v", streamer.blocks)
	}
	text := streamer.blocks[0][2].Text
	if strings.Contains(text, "# Git Snapshot") {
		t.Fatalf("compatible git context should use upstream-style gitStatus label, not Go-only heading:\n%s", text)
	}
	envIdx := strings.Index(text, "# Environment")
	gitIdx := strings.Index(text, "gitStatus:")
	if envIdx < 0 || gitIdx < 0 || envIdx > gitIdx {
		t.Fatalf("compatible git status should appear after environment; env=%d git=%d:\n%s", envIdx, gitIdx, text)
	}
	if !strings.Contains(text, "This is the git status at the start of the conversation") || !strings.Contains(text, "tracked.txt") {
		t.Fatalf("compatible git status missing expected snapshot details:\n%s", text)
	}
}

func TestClaudeCompatibleSessionGuidanceMatchesAgentToolSurface(t *testing.T) {
	text := compatibleSessionGuidanceSection([]string{"Agent", "Grep", "Read"})
	for _, want := range []string{
		"# Session-specific guidance",
		"Use the Agent tool with specialized agents",
		"protecting the main context window from excessive results",
		"For simple, directed codebase searches",
		"use Grep directly",
		"subagent_type=general-purpose",
		"more than 3 queries",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compatible session guidance missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# Session Guidance") {
		t.Fatalf("compatible Agent guidance should use upstream-like heading:\n%s", text)
	}
}

func TestPromptModeCodeAllowsProjectMemoryWritableRoot(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:         "test",
		CWD:           project,
		PromptMode:    "code",
		WritableRoots: []string{"/tmp/existing"},
	})

	want := memory.ClaudeCodeProjectMemoryDir(project)
	if !containsString(querySession.options.WritableRoots, "/tmp/existing") {
		t.Fatalf("existing writable root was not preserved: %+v", querySession.options.WritableRoots)
	}
	if !containsString(querySession.options.WritableRoots, want) {
		t.Fatalf("project memory writable root missing %q from %+v", want, querySession.options.WritableRoots)
	}
}

func TestPromptModeChatDoesNotAllowProjectMemoryWritableRoot(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		CWD:        project,
		PromptMode: "chat",
	})

	if got := memory.ClaudeCodeProjectMemoryDir(project); containsString(querySession.options.WritableRoots, got) {
		t.Fatalf("chat mode should not add project memory writable root %q: %+v", got, querySession.options.WritableRoots)
	}
}

func TestPromptModeCodeKeepsContextSeparateWhenResuming(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project instruction")

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
		InitialMessages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "previous message"}},
		}},
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 3 {
		t.Fatalf("messages = %+v", streamer.messages)
	}
	if got := messageText(streamer.messages[0][1]); !strings.Contains(got, "project instruction") || streamer.messages[0][2].Content[0].Text != "hi" {
		t.Fatalf("messages = %+v", streamer.messages[0])
	}
}

func TestPromptModeCodeInjectsRuntimeStatusAsRequestContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "todos.json"), `[
  {"id":"todo-1","content":"Implement runtime status attachment","status":"in_progress","priority":"high"},
  {"id":"todo-2","content":"Run prompt parity verification","status":"pending","priority":"medium"},
  {"id":"todo-3","content":"Old completed item","status":"completed","priority":"low"}
]`)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "plan_mode.json"), `{"active":true,"plan":"Keep runtime status request-only and code-mode scoped."}`)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          42,
		AgentName:   "reviewer",
		Status:      agenttasks.StatusRunning,
		Description: "Inspect prompt assembly evidence",
	}, {
		ID:          43,
		AgentName:   "auditor",
		Status:      agenttasks.StatusCompleted,
		Description: "Summarize Task evidence",
		ResultJSON:  `{"content":"agent found the Task evidence","transcript_path":"/tmp/subagent.jsonl","output_file":"/tmp/subagent.output","turns":2,"capability_loop":{"evidence":["internal/query/query.go:3909 carries result hints"],"assumptions":["parent has task store"],"unknowns":["live transcript not checked"],"verification":["go test ./internal/query -count=1"],"risks":["preview may be incomplete"],"next_action":"inspect stored evidence before final synthesis"}}`,
	}, {
		ID:          44,
		AgentName:   "reviewer",
		Status:      agenttasks.StatusFailed,
		Description: "Check failed task",
		ResultJSON:  `{"content":"failed task kept partial evidence","status":"failed","session_id":"failed-session","transcript_path":"/tmp/failed-task.jsonl","output_file":"/tmp/failed-task.output","turns":1,"capability_loop":{"evidence":["failed task found provider fallback bug"],"unknowns":["retry state not checked"],"verification":["run fallback acceptance"],"risks":["parent may otherwise ignore failed partial evidence"],"next_action":"patch fallback and rerun focused acceptance"}}`,
	}, {
		ID:          45,
		AgentName:   "reviewer",
		Status:      agenttasks.StatusCancelled,
		Description: "Check cancelled task",
	}, {
		ID:          46,
		AgentName:   "auditor",
		Status:      agenttasks.StatusTimeout,
		Description: "Check timeout task",
		ResultJSON:  `{"content":"timeout task kept partial evidence","status":"timeout","session_id":"timeout-session","transcript_path":"/tmp/timeout-task.jsonl","output_file":"/tmp/timeout-task.output","turns":2}`,
	}}}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
		TaskStore:  store,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"Current runtime status for this coding session",
		"## Active todos",
		"[in_progress/high] Implement runtime status attachment",
		"[pending/medium] Run prompt parity verification",
		"completed: 1 hidden",
		"## Plan mode",
		"Keep runtime status request-only and code-mode scoped.",
		"## Background agent tasks",
		"Treat running rows as status only; terminal rows with result preview or capability_loop are parent decision inputs.",
		"Use terminal task capability_loop fields to carry forward evidence, assumptions, unknowns, verification, risks, and next_action before finalizing or choosing recovery.",
		"Failed, cancelled, or timeout terminal tasks may still contain partial evidence; preserve it as partial evidence and surface remaining unknowns/risks instead of discarding the task.",
		"If a task is running, do not infer its result",
		"Completed, failed, cancelled, or timeout tasks are notifications",
		"#42 reviewer running: Inspect prompt assembly evidence",
		"#43 auditor completed: Summarize Task evidence; completion notification: result is ready; use the result preview, transcript_path, and output_file below because AgentGet is not available",
		"result preview: agent found the Task evidence",
		"capability_loop: evidence: internal/query/query.go:3909 carries result hints | assumptions: parent has task store | unknowns: live transcript not checked | verification: go test ./internal/query -count=1 | risks: preview may be incomplete | next_action: inspect stored evidence before final synthesis",
		"transcript_path: /tmp/subagent.jsonl",
		"output_file: /tmp/subagent.output",
		"turns: 2",
		"#44 reviewer failed: Check failed task; failure notification: use the status, result preview, transcript_path, and output_file below because AgentGet is not available",
		"result preview: failed task kept partial evidence",
		"capability_loop: evidence: failed task found provider fallback bug | unknowns: retry state not checked | verification: run fallback acceptance | risks: parent may otherwise ignore failed partial evidence | next_action: patch fallback and rerun focused acceptance",
		"#45 reviewer cancelled: Check cancelled task; cancellation notification: use the status, result preview, transcript_path, and output_file below because AgentGet is not available",
		"#46 auditor timeout: Check timeout task; timeout notification: preserve partial evidence; inspect result preview, transcript_path, or output_file and decide whether to retry with a longer profile or narrow the scope",
		"result preview: timeout task kept partial evidence",
		"transcript_path: /tmp/timeout-task.jsonl",
		"output_file: /tmp/timeout-task.output",
		"## Recent agent evidence decision context",
		"#43 auditor completed: Summarize Task evidence",
		"evidence_source: terminal_agent_task_store",
		"artifacts: transcript_path: /tmp/subagent.jsonl | output_file: /tmp/subagent.output",
		"#44 reviewer failed: Check failed task",
		"capability_loop: evidence: failed task found provider fallback bug",
		"artifacts: session_id: failed-session | transcript_path: /tmp/failed-task.jsonl | output_file: /tmp/failed-task.output",
		"## Agent capability follow-up gate",
		"pending_follow_up: #44 reviewer failed: Check failed task",
		"evidence_source: terminal_agent_task_store",
		"source_action: This is fallback terminal task-store evidence; inspect the listed artifacts or rerun verification before relying on it as complete.",
		"must_handle_next_action: patch fallback and rerun focused acceptance",
		"verification_required: run fallback acceptance",
		"unknown_to_resolve_or_disclose: retry state not checked",
		"risk_to_account_for: parent may otherwise ignore failed partial evidence",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime status missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "call AgentGet") {
		t.Fatalf("runtime status mentioned unavailable AgentGet:\n%s", text)
	}
}

func TestRuntimeStatusMarksRunningAgentProcessAttachment(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          42,
		AgentName:   "attached",
		Status:      agenttasks.StatusRunning,
		Description: "Currently controlled by this process",
	}, {
		ID:          43,
		AgentName:   "detached",
		Status:      agenttasks.StatusRunning,
		Description: "Restored from a persistent store",
	}}}
	controller := agenttasks.NewController()
	controller.Register(42, func() {})

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(agenttool.NewGet()), Options{
		Model:          "test",
		MaxTurns:       1,
		CWD:            project,
		PromptMode:     "code",
		TaskStore:      store,
		TaskController: controller,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"#42 attached running: Currently controlled by this process; process_attachment: attached_to_current_process",
		"#43 detached running: Restored from a persistent store; process_attachment: not_attached_to_current_process",
		"the task may be running in another process or interrupted; do not wait indefinitely without checking progress",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime status missing %q:\n%s", want, text)
		}
	}
}

func TestAgentGetAcknowledgesTerminalRuntimeNotification(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          43,
		AgentName:   "auditor",
		Status:      agenttasks.StatusCompleted,
		Description: "Summarize Task evidence",
		ResultJSON:  `{"content":"agent found the Task evidence","status":"completed","agent_name":"auditor","session_id":"sub-session","transcript_path":"/tmp/subagent.jsonl","output_file":"/tmp/subagent.output","worktree_path":"/tmp/subagent-worktree","worktree_branch":"agent/audit","turns":2,"capability_loop":{"evidence":["internal/query/query.go keeps AgentGet evidence in runtime status"],"assumptions":["parent has called AgentGet"],"unknowns":["external baseline not checked"],"verification":["go test ./internal/query -count=1"],"risks":["tool_result history can be budgeted later"],"next_action":"synthesize using AgentGet evidence before answering"}}`,
	}}}

	streamer := &agentGetAcknowledgementStreamer{}
	querySession := New(streamer, tools.NewRegistry(agenttool.NewGet()), Options{
		Model:      "test",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
		TaskStore:  store,
	})
	if _, err := querySession.Run(context.Background(), "check agent", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	firstText := allMessageText(streamer.requests[0].Messages)
	for _, want := range []string{
		"## Background agent tasks",
		"#43 auditor completed: Summarize Task evidence",
		"completion notification: result is ready; call AgentGet before using findings",
	} {
		if !strings.Contains(firstText, want) {
			t.Fatalf("first request missing %q:\n%s", want, firstText)
		}
	}
	secondText := allMessageText(streamer.requests[1].Messages)
	if !lastToolResultContains(streamer.requests[1].Messages, `"status": "completed"`) || !lastToolResultContains(streamer.requests[1].Messages, "agent found the Task evidence") {
		t.Fatalf("second request missing AgentGet result:\n%+v", streamer.requests[1].Messages)
	}
	for _, want := range []string{
		"## Recent agent evidence decision context",
		"These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs after sub-agent results are observed.",
		"#43 auditor completed: Summarize Task evidence",
		"evidence_source: agent_get",
		"capability_loop: evidence: internal/query/query.go keeps AgentGet evidence in runtime status",
		"assumptions: parent has called AgentGet",
		"unknowns: external baseline not checked",
		"verification: go test ./internal/query -count=1",
		"risks: tool_result history can be budgeted later",
		"next_action: synthesize using AgentGet evidence before answering",
		"artifacts: session_id: sub-session | transcript_path: /tmp/subagent.jsonl | output_file: /tmp/subagent.output | worktree_path: /tmp/subagent-worktree | worktree_branch: agent/audit",
	} {
		if !strings.Contains(secondText, want) {
			t.Fatalf("second request missing recent agent evidence context %q:\n%s", want, secondText)
		}
	}
	for _, notWant := range []string{
		"#43 auditor completed: Summarize Task evidence",
		"completion notification: result is ready; call AgentGet before using findings",
	} {
		if notWant == "#43 auditor completed: Summarize Task evidence" && strings.Contains(secondText, notWant+"; completion notification") {
			t.Fatalf("second request repeated acknowledged task notification %q:\n%s", notWant, secondText)
		}
		if notWant != "#43 auditor completed: Summarize Task evidence" && strings.Contains(secondText, notWant) {
			t.Fatalf("second request repeated acknowledged task notification %q:\n%s", notWant, secondText)
		}
	}
}

func TestTaskCapabilityLoopBecomesRecentEvidenceContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	streamer := &taskCapabilityLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(capabilityTaskTool{}), Options{
		Model:      "test",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "delegate and continue", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	secondText := allMessageText(streamer.requests[1].Messages)
	if !lastToolResultContains(streamer.requests[1].Messages, "TASK_LOOP_EVIDENCE: synchronous Task evidence reached parent runtime status.") {
		t.Fatalf("second request missing Task tool_result:\n%+v", streamer.requests[1].Messages)
	}
	for _, want := range []string{
		"## Recent agent evidence decision context",
		"These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs after sub-agent results are observed.",
		"Task result completed: sync evidence audit",
		"evidence_source: task_tool",
		"capability_loop: evidence: TASK_LOOP_EVIDENCE: synchronous Task evidence reached parent runtime status.",
		"assumptions: parent will plan from Task result",
		"unknowns: browser validation not sampled",
		"verification: go test ./internal/query -run TestTaskCapabilityLoopBecomesRecentEvidenceContext -count=1",
		"risks: raw Task result may be long or budgeted",
		"next_action: TASK_LOOP_NEXT_ACTION: cite Task evidence before final synthesis",
		"artifacts: session_id: sync-task-session | transcript_path: /tmp/sync-task.jsonl | output_file: /tmp/sync-task.output | worktree_path: /tmp/sync-task-worktree | worktree_branch: agent/sync-task",
		"## Agent capability follow-up gate",
		"Do not finalize until each listed sub-agent next_action is executed",
		"pending_follow_up: Task result completed: sync evidence audit",
		"evidence_source: task_tool",
		"source_action: Synchronous tool_result evidence is already in the parent turn; carry it forward and handle next_action before finalizing.",
		"must_handle_next_action: TASK_LOOP_NEXT_ACTION: cite Task evidence before final synthesis",
		"verification_required: go test ./internal/query -run TestTaskCapabilityLoopBecomesRecentEvidenceContext -count=1",
		"unknown_to_resolve_or_disclose: browser validation not sampled",
		"risk_to_account_for: raw Task result may be long or budgeted",
	} {
		if !strings.Contains(secondText, want) {
			t.Fatalf("second request missing Task recent evidence context %q:\n%s", want, secondText)
		}
	}
}

func TestPartialTaskResultBecomesFailedRecentEvidenceContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	streamer := &partialTaskParentStreamer{}
	querySession := New(streamer, tools.NewRegistry(tasktool.New(partialTaskChildStreamer{}, "test")), Options{
		Model:      "test",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "delegate and recover", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	if !lastToolResultContains(streamer.requests[1].Messages, "PARTIAL_TASK_STREAM_EVIDENCE") || !lastToolResultContains(streamer.requests[1].Messages, "<capability_loop>") {
		t.Fatalf("second request missing partial Task capability tool_result:\n%+v", streamer.requests[1].Messages)
	}
	secondText := allMessageText(streamer.requests[1].Messages)
	for _, want := range []string{
		"## Recent agent evidence decision context",
		"Task result failed: partial stream audit",
		"evidence_source: task_tool",
		"capability_loop: evidence: Partial sub-agent content before failed:",
		"PARTIAL_TASK_STREAM_EVIDENCE",
		"unknowns: Whether the partial sub-agent result covered all requested evidence before failure.",
		"verification: Inspect partial answer, completed tool calls, transcript_path, or rerun a narrower verification before relying on findings.",
		"risks: Partial sub-agent result may be incomplete, stale, or missing later evidence due to the failure.",
		"next_action: Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation.",
		"artifacts: session_id:",
		"transcript_path:",
		"output_file:",
		"## Agent capability follow-up gate",
		"pending_follow_up: Task result failed: partial stream audit",
		"evidence_source: task_tool",
		"source_action: Synchronous tool_result evidence is already in the parent turn; carry it forward and handle next_action before finalizing.",
		"must_handle_next_action: Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation.",
		"verification_required: Inspect partial answer, completed tool calls, transcript_path, or rerun a narrower verification before relying on findings.",
		"risk_to_account_for: Partial sub-agent result may be incomplete, stale, or missing later evidence due to the failure.",
	} {
		if !strings.Contains(secondText, want) {
			t.Fatalf("second request missing partial Task recent evidence context %q:\n%s", want, secondText)
		}
	}
	if strings.Contains(secondText, "Task result completed: partial stream audit") {
		t.Fatalf("partial Task evidence was mislabeled completed:\n%s", secondText)
	}
}

func TestAgentEvidenceFollowUpResolutionClearsSupersededPending(t *testing.T) {
	querySession := &Session{recentAgentEvidence: []capabilityloop.DecisionContext{
		{
			ToolUseID:      "toolu_unresolved",
			ToolName:       "Task",
			Description:    "unresolved audit",
			Status:         "completed",
			EvidenceSource: "task_tool",
			Loop: capabilityloop.HintData{
				Evidence:   []string{"UNRESOLVED_EVIDENCE: old evidence remains useful."},
				NextAction: "UNRESOLVED_NEXT_ACTION: still pending.",
			},
		},
		{
			ToolUseID:      "toolu_resolved",
			ToolName:       "Task",
			Description:    "resolved audit",
			Status:         "completed",
			EvidenceSource: "task_tool",
			Loop: capabilityloop.HintData{
				Evidence:   []string{"RESOLVED_EVIDENCE: old evidence was handled."},
				NextAction: "RESOLVED_NEXT_ACTION: should disappear.",
			},
		},
		{
			ToolUseID:      "toolu_resolution",
			ToolName:       "Task",
			Description:    "resolution audit",
			Status:         "completed",
			EvidenceSource: "task_tool",
			Loop: capabilityloop.HintData{
				Evidence:             []string{"RESOLUTION_EVIDENCE: inspected old Task result."},
				Verification:         []string{"RESOLUTION_VERIFICATION: focused acceptance passed."},
				ResolvedFollowUp:     "RESOLUTION_DONE: old next_action handled.",
				SupersedesEvidenceID: "tool:toolu_resolved",
			},
		},
	}}
	recent := querySession.runtimeRecentAgentEvidenceStatus()
	for _, want := range []string{
		"Task result completed: resolution audit",
		"capability_loop: evidence: RESOLUTION_EVIDENCE",
		"resolved_follow_up: RESOLUTION_DONE: old next_action handled.",
		"supersedes_evidence_id: tool:toolu_resolved",
	} {
		if !strings.Contains(recent, want) {
			t.Fatalf("recent evidence context missing %q:\n%s", want, recent)
		}
	}
	gate := querySession.runtimeAgentEvidenceFollowUpReminderStatus()
	if strings.Contains(gate, "pending_follow_up: Task result completed: resolved audit") ||
		strings.Contains(gate, "RESOLVED_NEXT_ACTION: should disappear.") {
		t.Fatalf("gate should skip superseded follow-up:\n%s", gate)
	}
	for _, want := range []string{
		"pending_follow_up: Task result completed: unresolved audit",
		"follow_up_id: tool:toolu_unresolved",
		"UNRESOLVED_NEXT_ACTION: still pending.",
	} {
		if !strings.Contains(gate, want) {
			t.Fatalf("gate should retain unresolved follow-up %q:\n%s", want, gate)
		}
	}
}

func TestAgentEvidenceFollowUpResolutionRequiresProof(t *testing.T) {
	for name, resolver := range map[string]capabilityloop.DecisionContext{
		"failed_resolution": {
			ToolUseID:      "toolu_failed_resolution",
			ToolName:       "Task",
			Description:    "failed resolution audit",
			Status:         "failed",
			EvidenceSource: "task_tool",
			Loop: capabilityloop.HintData{
				Evidence:             []string{"WEAK_RESOLUTION_EVIDENCE: failed child claimed resolution."},
				Verification:         []string{"WEAK_RESOLUTION_VERIFICATION: failed run."},
				ResolvedFollowUp:     "WEAK_RESOLUTION_DONE: not trusted.",
				SupersedesEvidenceID: "tool:toolu_pending",
			},
		},
		"missing_proof": {
			ToolUseID:      "toolu_weak_resolution",
			ToolName:       "Task",
			Description:    "weak resolution audit",
			Status:         "completed",
			EvidenceSource: "task_tool",
			Loop: capabilityloop.HintData{
				ResolvedFollowUp:     "WEAK_RESOLUTION_DONE: no evidence.",
				SupersedesEvidenceID: "tool:toolu_pending",
			},
		},
		"wrong_id": {
			ToolUseID:      "toolu_wrong_resolution",
			ToolName:       "Task",
			Description:    "wrong resolution audit",
			Status:         "completed",
			EvidenceSource: "task_tool",
			Loop: capabilityloop.HintData{
				Evidence:             []string{"WEAK_RESOLUTION_EVIDENCE: inspected another result."},
				Verification:         []string{"WEAK_RESOLUTION_VERIFICATION: another check passed."},
				ResolvedFollowUp:     "WEAK_RESOLUTION_DONE: wrong id.",
				SupersedesEvidenceID: "tool:toolu_other",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			querySession := &Session{recentAgentEvidence: []capabilityloop.DecisionContext{
				{
					ToolUseID:      "toolu_pending",
					ToolName:       "Task",
					Description:    "pending audit",
					Status:         "completed",
					EvidenceSource: "task_tool",
					Loop: capabilityloop.HintData{
						Verification: []string{"PENDING_VERIFICATION: rerun focused acceptance."},
						NextAction:   "PENDING_NEXT_ACTION: inspect old Task evidence.",
					},
				},
				resolver,
			}}
			gate := querySession.runtimeAgentEvidenceFollowUpReminderStatus()
			for _, want := range []string{
				"pending_follow_up: Task result completed: pending audit",
				"follow_up_id: tool:toolu_pending",
				"PENDING_NEXT_ACTION: inspect old Task evidence.",
			} {
				if !strings.Contains(gate, want) {
					t.Fatalf("weak resolution should not clear pending follow-up %q:\n%s", want, gate)
				}
			}
		})
	}
}

func TestInitialAgentGetResultRestoresRecentEvidenceContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	initial := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu_agent_get_failed",
			Name:  "AgentGet",
			Input: json.RawMessage(`{"task_id":43}`),
		}},
	}, {
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:      "tool_result",
			ToolUseID: "toolu_agent_get_failed",
			Content:   `{"task":{"id":43,"agent_name":"auditor","description":"failed recovery probe","status":"failed"},"result":{"status":"failed","agent_name":"auditor","session_id":"failed-sub-session","transcript_path":"/tmp/failed-subagent.jsonl","output_file":"/tmp/failed-subagent.output","worktree_path":"/tmp/failed-worktree","worktree_branch":"agent/failed","capability_loop":{"evidence":["FAILED_RESUME_EVIDENCE: output_file captured partial root cause"],"assumptions":["AgentGet was called before resume"],"unknowns":["live child process is gone"],"verification":["scripts/failed-agent-resume-acceptance.sh"],"risks":["partial evidence may be incomplete"],"next_action":"explain failure using partial evidence and remaining unknowns"}}}`,
		}},
	}}
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(agenttool.NewGet()), Options{
		Model:           "test",
		MaxTurns:        1,
		CWD:             project,
		PromptMode:      "code",
		InitialMessages: initial,
	})
	if _, err := querySession.Run(context.Background(), "continue after resume", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"## Recent agent evidence decision context",
		"#43 auditor failed: failed recovery probe",
		"evidence_source: agent_get",
		"capability_loop: evidence: FAILED_RESUME_EVIDENCE: output_file captured partial root cause",
		"assumptions: AgentGet was called before resume",
		"unknowns: live child process is gone",
		"verification: scripts/failed-agent-resume-acceptance.sh",
		"risks: partial evidence may be incomplete",
		"next_action: explain failure using partial evidence and remaining unknowns",
		"artifacts: session_id: failed-sub-session | transcript_path: /tmp/failed-subagent.jsonl | output_file: /tmp/failed-subagent.output | worktree_path: /tmp/failed-worktree | worktree_branch: agent/failed",
		"## Agent capability follow-up gate",
		"pending_follow_up: #43 auditor failed: failed recovery probe",
		"evidence_source: agent_get",
		"source_action: AgentGet result was explicitly retrieved; use it as the current sub-agent result, but still run or disclose listed verification and risks before finalizing.",
		"must_handle_next_action: explain failure using partial evidence and remaining unknowns",
		"verification_required: scripts/failed-agent-resume-acceptance.sh",
		"risk_to_account_for: partial evidence may be incomplete",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("resumed request missing %q:\n%s", want, text)
		}
	}
}

func TestInitialCompactSummaryRestoresFollowUpGate(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	summary := strings.Join([]string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"Capability Loop Evidence:",
		"- COMPACT_RESUME_GATE_EVIDENCE",
		"Capability Loop Assumptions:",
		"- COMPACT_RESUME_GATE_ASSUMPTION",
		"Capability Loop Unknowns:",
		"- COMPACT_RESUME_GATE_UNKNOWN",
		"Capability Loop Verification:",
		"- COMPACT_RESUME_GATE_VERIFICATION",
		"Capability Loop Risks:",
		"- COMPACT_RESUME_GATE_RISK",
		"Capability Loop Next Actions:",
		"- COMPACT_RESUME_GATE_NEXT_ACTION",
		"Capability Loop Artifacts:",
		"- session_id: compact-session",
		"- transcript_path: /tmp/compact.jsonl",
		"- output_file: /tmp/compact.output",
		"- worktree_path: /tmp/compact-worktree",
		"- worktree_branch: agent/compact",
	}, "\n")
	initial := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: summary}},
	}}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:           "test",
		MaxTurns:        1,
		CWD:             project,
		PromptMode:      "code",
		InitialMessages: initial,
	})
	if _, err := querySession.Run(context.Background(), "continue after compact resume", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"## Recent agent evidence decision context",
		"compact_summary result compacted: Recovered capability loop facts from compact summary",
		"evidence_source: compact_summary",
		"capability_loop: evidence: COMPACT_RESUME_GATE_EVIDENCE",
		"assumptions: COMPACT_RESUME_GATE_ASSUMPTION",
		"unknowns: COMPACT_RESUME_GATE_UNKNOWN",
		"verification: COMPACT_RESUME_GATE_VERIFICATION",
		"risks: COMPACT_RESUME_GATE_RISK",
		"next_action: COMPACT_RESUME_GATE_NEXT_ACTION",
		"artifacts: session_id: compact-session | transcript_path: /tmp/compact.jsonl | output_file: /tmp/compact.output | worktree_path: /tmp/compact-worktree | worktree_branch: agent/compact",
		"## Agent capability follow-up gate",
		"pending_follow_up: compact_summary result compacted: Recovered capability loop facts from compact summary",
		"evidence_source: compact_summary",
		"source_action: This evidence was recovered from compact summary; treat it as condensed context and re-verify or disclose compaction limits for unresolved unknowns/risks.",
		"must_handle_next_action: COMPACT_RESUME_GATE_NEXT_ACTION",
		"verification_required: COMPACT_RESUME_GATE_VERIFICATION",
		"unknown_to_resolve_or_disclose: COMPACT_RESUME_GATE_UNKNOWN",
		"risk_to_account_for: COMPACT_RESUME_GATE_RISK",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact-resumed request missing %q:\n%s", want, text)
		}
	}
}

func TestInitialCompactSummaryRestoresFollowUpResolution(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	summary := strings.Join([]string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"Capability Loop Evidence:",
		"- COMPACT_RESOLUTION_EVIDENCE",
		"Capability Loop Verification:",
		"- COMPACT_RESOLUTION_VERIFICATION",
		"Capability Loop Next Actions:",
		"- COMPACT_RESOLVED_NEXT_ACTION_SHOULD_NOT_GATE",
		"Capability Loop Follow Up IDs:",
		"- tool:compact-task",
		"Capability Loop Resolved Follow Ups:",
		"- COMPACT_RESOLUTION_DONE",
		"Capability Loop Supersedes:",
		"- tool:compact-task",
		"Capability Loop Artifacts:",
		"- transcript_path: /tmp/compact-resolution.jsonl",
	}, "\n")
	initial := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: summary}},
	}}
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:           "test",
		MaxTurns:        1,
		CWD:             project,
		PromptMode:      "code",
		InitialMessages: initial,
	})
	if _, err := querySession.Run(context.Background(), "continue after compact resolution", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"## Recent agent evidence decision context",
		"compact_summary result compacted: Recovered capability follow-up resolution from compact summary",
		"capability_loop: evidence: COMPACT_RESOLUTION_EVIDENCE",
		"verification: COMPACT_RESOLUTION_VERIFICATION",
		"resolved_follow_up: COMPACT_RESOLUTION_DONE",
		"supersedes_evidence_id: tool:compact-task",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact resolution request missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{
		"pending_follow_up: compact_summary result compacted: Recovered pending capability follow-up from compact summary",
		"must_handle_next_action: COMPACT_RESOLVED_NEXT_ACTION_SHOULD_NOT_GATE",
	} {
		if strings.Contains(text, notWant) {
			t.Fatalf("compact resolution should not resurrect superseded pending %q:\n%s", notWant, text)
		}
	}
}

func TestInitialTaskCapabilityLoopWrapperSurvivesResumeRequest(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	taskResult := strings.Join([]string{
		"Summary: checked structured context.",
		"",
		"<capability_loop>",
		`{"capability_loop":{"evidence":["TASK_RESUME_EVIDENCE: synchronous Task result survived resume."],"assumptions":["Task tool_result was replayed from transcript."],"unknowns":["No browser resume flow sampled."],"verification":["go test ./internal/query -run TestInitialTaskCapabilityLoopWrapperSurvivesResumeRequest -count=1"],"risks":["Tool result budget could replace very large outputs."],"next_action":"TASK_RESUME_NEXT_ACTION: parent should continue from resumed Task evidence."}}`,
		"</capability_loop>",
		"These structured fields are parent decision context.",
	}, "\n")
	initial := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    "toolu_task_resume",
			Name:  "Task",
			Input: json.RawMessage(`{"description":"resume evidence","prompt":"inspect"}`),
		}},
	}, {
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:      "tool_result",
			ToolUseID: "toolu_task_resume",
			Content:   taskResult,
		}},
	}}
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(), Options{
		Model:           "test",
		MaxTurns:        1,
		CWD:             project,
		PromptMode:      "code",
		InitialMessages: initial,
	})
	if _, err := querySession.Run(context.Background(), "continue after Task resume", io.Discard); err != nil {
		t.Fatal(err)
	}
	toolResultText := requestToolResultText(streamer.messages[0], "toolu_task_resume")
	for _, want := range []string{
		"<capability_loop>",
		`"capability_loop"`,
		"TASK_RESUME_EVIDENCE: synchronous Task result survived resume.",
		"Task tool_result was replayed from transcript.",
		"No browser resume flow sampled.",
		"go test ./internal/query -run TestInitialTaskCapabilityLoopWrapperSurvivesResumeRequest -count=1",
		"Tool result budget could replace very large outputs.",
		"TASK_RESUME_NEXT_ACTION: parent should continue from resumed Task evidence.",
	} {
		if !strings.Contains(toolResultText, want) {
			t.Fatalf("resumed Task tool_result missing %q:\n%s", want, toolResultText)
		}
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"## Recent agent evidence decision context",
		"These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs after sub-agent results are observed.",
		"Task result completed: resume evidence",
		"evidence_source: task_tool",
		"capability_loop: evidence: TASK_RESUME_EVIDENCE: synchronous Task result survived resume.",
		"assumptions: Task tool_result was replayed from transcript.",
		"unknowns: No browser resume flow sampled.",
		"verification: go test ./internal/query -run TestInitialTaskCapabilityLoopWrapperSurvivesResumeRequest -count=1",
		"risks: Tool result budget could replace very large outputs.",
		"next_action: TASK_RESUME_NEXT_ACTION: parent should continue from resumed Task evidence.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("resumed request missing Task recent evidence context %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "continue after Task resume") {
		t.Fatalf("resumed request missing current prompt:\n%s", text)
	}
}

func TestAgentGetLongResultKeepsOutputFileVisible(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	longContent := strings.Repeat("AGENT_LONG_RESULT_BODY ", 5000) + "AGENT_LONG_RESULT_TAIL_MARKER"
	resultJSON, err := json.Marshal(map[string]any{
		"content":         longContent,
		"status":          agenttasks.StatusCompleted,
		"agent_name":      "auditor",
		"transcript_path": "/tmp/subagent-long.jsonl",
		"output_file":     "/tmp/subagent-long.output",
		"turns":           4,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          43,
		AgentName:   "auditor",
		Status:      agenttasks.StatusCompleted,
		Description: "Summarize long Task evidence",
		ResultJSON:  string(resultJSON),
	}}}

	streamer := &agentGetAcknowledgementStreamer{}
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "67676767-6767-4676-8676-676767676767")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	querySession := New(streamer, tools.NewRegistry(agenttool.NewGet()), Options{
		Model:           "test",
		MaxTurns:        2,
		CWD:             project,
		PromptMode:      "code",
		TaskStore:       store,
		Recorder:        recorder,
		ToolResultLimit: 10_000,
	})
	if _, err := querySession.Run(context.Background(), "check agent", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	agentGetResult := lastToolResultText(streamer.requests[1].Messages)
	for _, want := range []string{
		`"content_preview"`,
		`"content_truncated": true`,
		`"content_bytes"`,
		`"output_file": "/tmp/subagent-long.output"`,
	} {
		if !strings.Contains(agentGetResult, want) {
			t.Fatalf("AgentGet long result missing %q:\n%.4000s", want, agentGetResult)
		}
	}
	for _, notWant := range []string{
		"<persisted-output>",
		"AGENT_LONG_RESULT_TAIL_MARKER",
	} {
		if strings.Contains(agentGetResult, notWant) {
			t.Fatalf("AgentGet long result leaked %q:\n%.4000s", notWant, agentGetResult)
		}
	}
}

func TestInitialAcknowledgedAgentTasksSkipTerminalRuntimeNotification(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          43,
		AgentName:   "auditor",
		Status:      agenttasks.StatusCompleted,
		Description: "Summarize Task evidence",
		ResultJSON:  `{"content":"agent found the Task evidence","status":"completed","turns":2}`,
	}}}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(agenttool.NewGet()), Options{
		Model:                         "test",
		MaxTurns:                      1,
		CWD:                           project,
		PromptMode:                    "code",
		TaskStore:                     store,
		InitialAcknowledgedAgentTasks: []uint64{43},
	})
	if _, err := querySession.Run(context.Background(), "check agent", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, notWant := range []string{
		"## Background agent tasks",
		"#43 auditor completed: Summarize Task evidence",
		"completion notification: result is ready; call AgentGet before using findings",
	} {
		if strings.Contains(text, notWant) {
			t.Fatalf("request repeated initially acknowledged task notification %q:\n%s", notWant, text)
		}
	}
}

func TestTodoWriteResultAndRuntimeTodosEnterNextRequest(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)

	streamer := &todoWriteResultStreamer{}
	querySession := New(streamer, tools.NewRegistry(todowrite.New()), Options{
		Model:      "test",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "track this work", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	for _, want := range []string{
		"Todos have been modified successfully",
		"continue to use the todo list",
		"Summary: 2 total, 1 pending, 1 in_progress, 0 completed",
	} {
		if !lastToolResultContains(streamer.requests[1].Messages, want) {
			t.Fatalf("second request tool_result missing %q:\n%+v", want, streamer.requests[1].Messages)
		}
	}
	text := allMessageText(streamer.requests[1].Messages)
	for _, want := range []string{
		"## Active todos",
		"[in_progress/high] Collecting TodoWrite request evidence",
		"[pending/medium] Run TodoWrite tests",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("second request missing %q:\n%s", want, text)
		}
	}
}

func TestPromptDumpSummarizesRuntimeStatusFromQueryRequest(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	dumpPath := filepath.Join(t.TempDir(), "prompt.jsonl")
	t.Setenv(promptDumpPathEnv, dumpPath)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "todos.json"), `[{"content":"dump todo","status":"in_progress","priority":"high"}]`)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "plan_mode.json"), `{"active":true,"plan":"dump plan"}`)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          7,
		AgentName:   "auditor",
		Status:      agenttasks.StatusRunning,
		Description: "Check request status",
	}}}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
		TaskStore:  store,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	records := parsePromptDumpRecords(t, raw)
	if len(records) != 1 {
		t.Fatalf("records = %+v", records)
	}
	status := records[0].RuntimeStatus
	if !status.Present || status.TextBytes == 0 {
		t.Fatalf("runtime status = %+v", status)
	}
	want := []string{"active_todos", "plan_mode", "background_agent_tasks"}
	if strings.Join(status.Sections, ",") != strings.Join(want, ",") {
		t.Fatalf("runtime sections = %+v, want %+v", status.Sections, want)
	}
	if records[0].Request != nil || records[0].RequestRedaction.RawRequestIncluded {
		t.Fatalf("summary dump leaked raw request: redaction=%+v request=%+v", records[0].RequestRedaction, records[0].Request)
	}
}

func TestPromptModeCodeInjectsPermissionContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	extraDir := filepath.Join(t.TempDir(), "extra")
	t.Setenv("HOME", home)

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   project,
		PromptMode:            "code",
		WritableRoots:         []string{extraDir, memory.ClaudeCodeProjectMemoryDir(project)},
		RuntimePermissionMode: func() string { return "dontAsk" },
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := allMessageText(streamer.messages[0])
	for _, want := range []string{
		"Current runtime status for this coding session",
		"## Permission context",
		"- mode: deny",
		"If a tool call is denied, do not retry the exact same call",
		"- additional writable directories:",
		"  - " + extraDir,
		"These directories are writable roots in addition to the current working directory",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("permission runtime status missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, memory.ClaudeCodeProjectMemoryDir(project)) {
		t.Fatalf("permission runtime status duplicated project memory root:\n%s", text)
	}
}

func TestPromptModeChatSkipsRuntimeStatus(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma-legacy", "todos.json"), `[{"content":"local code todo must stay private","status":"in_progress","priority":"high"}]`)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma-legacy", "plan_mode.json"), `{"active":true,"plan":"local plan must stay private"}`)
	store := &queryAgentMessageStore{tasks: []mysqlstore.AgentTask{{
		ID:          42,
		AgentName:   "reviewer",
		Status:      agenttasks.StatusRunning,
		Description: "local agent status must stay private",
	}}}

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   project,
		PromptMode:            "chat",
		TaskStore:             store,
		RuntimePermissionMode: func() string { return "deny" },
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	text := streamer.systems[0] + "\n" + allMessageText(streamer.messages[0])
	for _, notWant := range []string{"local code todo must stay private", "local plan must stay private", "local agent status must stay private", "Current runtime status for this coding session", "## Permission context"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("chat mode leaked runtime status %q:\n%s", notWant, text)
		}
	}
}

func TestToolPlanningReminderIsCodeModeRequestOnly(t *testing.T) {
	project := t.TempDir()
	messages := toolPlanningReminderMessages()
	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	got := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{})
	if len(got) != len(messages)+1 {
		t.Fatalf("messages len = %d, want %d", len(got), len(messages)+1)
	}
	text := messageText(got[len(got)-1])
	for _, want := range []string{
		"## Tool planning reminder",
		"already gathered 8 tool results from 10 tool calls",
		"stop exploring and synthesize",
		"paths returned by LS, Glob, or Grep",
		`output_mode:"files_with_matches"`,
		"read only the few files or line ranges needed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tool planning reminder missing %q:\n%s", want, text)
		}
	}
}

func TestToolPlanningReminderSkipsChatMode(t *testing.T) {
	project := t.TempDir()
	messages := toolPlanningReminderMessages()
	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "chat",
	})
	got := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{})
	if len(got) != len(messages) {
		t.Fatalf("chat mode injected runtime status: %+v", got)
	}
}

func TestTurnBudgetReminderIsCodeModeFinalTurnOnly(t *testing.T) {
	project := t.TempDir()
	messages := toolPlanningReminderMessages()
	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   6,
		CWD:        project,
		PromptMode: "code",
	})

	beforeFinal := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{Turn: 5, MaxTurns: 6})
	if text := messageText(beforeFinal[len(beforeFinal)-1]); strings.Contains(text, "## Turn budget reminder") {
		t.Fatalf("non-final turn injected turn budget status:\n%s", text)
	}

	final := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{Turn: 6, MaxTurns: 6})
	if len(final) != len(messages)+1 {
		t.Fatalf("final turn messages len = %d, want %d", len(final), len(messages)+1)
	}
	text := messageText(final[len(final)-1])
	for _, want := range []string{
		"## Turn budget reminder",
		"model turn 6 of 6",
		"last turn allowed",
		"produce the final answer now",
		"Tools are disabled for this final request",
		"synthesize the best answer from the available context",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("turn budget reminder missing %q:\n%s", want, text)
		}
	}
}

func TestTurnBudgetReminderInjectsOnFinalTurnWithAnyToolContext(t *testing.T) {
	project := t.TempDir()
	messages := []anthropic.MessageParam{
		{
			Role: "assistant",
			Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Read",
				Input: json.RawMessage(`{"file_path":"README.md"}`),
			}},
		},
		{
			Role: "user",
			Content: []anthropic.ContentBlock{{
				Type:      "tool_result",
				ToolUseID: "toolu_1",
				Content:   "evidence",
			}},
		},
	}
	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   3,
		CWD:        project,
		PromptMode: "code",
	})

	beforeFinal := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{Turn: 2, MaxTurns: 3})
	if len(beforeFinal) != len(messages) {
		t.Fatalf("non-final turn injected runtime status: %+v", beforeFinal)
	}

	final := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{Turn: 3, MaxTurns: 3})
	if len(final) != len(messages)+1 {
		t.Fatalf("final turn messages len = %d, want %d", len(final), len(messages)+1)
	}
	text := messageText(final[len(final)-1])
	if !strings.Contains(text, "## Turn budget reminder") || strings.Contains(text, "## Tool planning reminder") {
		t.Fatalf("final low-tool runtime status = %s", text)
	}
}

func TestFinalTurnWithToolContextDisablesTools(t *testing.T) {
	project := t.TempDir()
	streamer := &finalTurnToolDisableStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
	})

	var out strings.Builder
	result, err := querySession.Run(context.Background(), "inspect", &out)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" || strings.TrimSpace(out.String()) != "done" {
		t.Fatalf("result = %+v out=%q", result, out.String())
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d", len(streamer.requests))
	}
	if len(streamer.requests[0].Tools) == 0 {
		t.Fatalf("first turn tools were unexpectedly disabled")
	}
	if len(streamer.requests[1].Tools) != 0 {
		t.Fatalf("final turn tools = %+v", streamer.requests[1].Tools)
	}
}

func TestTurnBudgetReminderSkipsChatMode(t *testing.T) {
	project := t.TempDir()
	messages := toolPlanningReminderMessages()
	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   6,
		CWD:        project,
		PromptMode: "chat",
	})
	got := querySession.withRuntimeStatusMessages(context.Background(), messages, runtimeStatusRequest{Turn: 6, MaxTurns: 6})
	if len(got) != len(messages) {
		t.Fatalf("chat mode injected turn budget status: %+v", got)
	}
}

func TestRuntimeStatusIsRequestOnlyAcrossTurns(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, ".niuma", "todos.json"), `[{"content":"request-only todo","status":"in_progress","priority":"high"}]`)
	streamer := &runtimeStatusLoopStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   2,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(streamer.requests))
	}
	firstCount := strings.Count(allMessageText(streamer.requests[0].Messages), "Current runtime status for this coding session")
	secondCount := strings.Count(allMessageText(streamer.requests[1].Messages), "Current runtime status for this coding session")
	if firstCount != 1 || secondCount != 1 {
		t.Fatalf("runtime status counts first=%d second=%d\nfirst:\n%s\nsecond:\n%s", firstCount, secondCount, allMessageText(streamer.requests[0].Messages), allMessageText(streamer.requests[1].Messages))
	}
}

func TestClaudeCompatibleStrictPromptProfileSkipsAgentsFallback(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(promptProfileEnv, promptProfileClaudeCompatibleStrict)
	mustWriteQueryTest(t, filepath.Join(project, "AGENTS.md"), "agents fallback should not enter strict compatible context")

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.blocks) != 1 || len(streamer.blocks[0]) != 3 {
		t.Fatalf("system blocks = %+v", streamer.blocks)
	}
	if !strings.Contains(streamer.blocks[0][0].Text, agentSDKPrefix) {
		t.Fatalf("strict compatible profile did not use Claude-compatible system blocks: %+v", streamer.blocks[0])
	}
	if got := allMessageText(streamer.messages[0]); strings.Contains(got, "agents fallback should not enter strict compatible context") || strings.Contains(got, "AGENTS.md") {
		t.Fatalf("strict compatible profile loaded AGENTS.md fallback:\n%s", got)
	}
}

func TestPromptContextBudgetsLargeWorkflowDocuments(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_WORKFLOW_PROMPT_BUDGET_BYTES", "900")
	mustWriteQueryTest(t, filepath.Join(project, "AGENTS.md"), strings.Join([]string{
		"# Project Rules",
		"## Testing",
		"- MUST run tests before submit.",
		"- " + strings.Repeat("filler ", 400),
		"## Security",
		"- 不要把 secrets 写入日志。",
	}, "\n"))

	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "inspect", io.Discard); err != nil {
		t.Fatal(err)
	}
	got := allMessageText(streamer.messages[0])
	for _, want := range []string{"Large workflow guidance has been budgeted", "Full file:", "MUST run tests", "不要把 secrets"} {
		if !strings.Contains(got, want) {
			t.Fatalf("budgeted context missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "filler filler filler") {
		t.Fatalf("budgeted context leaked filler:\n%s", got)
	}
	event := findTelemetryEvent(events, "query.prompt_context")
	if event.Name == "" {
		t.Fatalf("missing query.prompt_context event: %+v", events)
	}
	manifest, ok := event.Properties["context_manifest"].(ContextManifest)
	if !ok {
		t.Fatalf("missing manifest: %+v", event.Properties)
	}
	if manifest.CodeContext.BudgetedDocs != 1 || manifest.CodeContext.PromptBytes >= manifest.CodeContext.DocumentBytes {
		t.Fatalf("budget manifest = %+v", manifest.CodeContext)
	}
	if len(manifest.CodeContext.DocumentSummary) != 1 || !manifest.CodeContext.DocumentSummary[0].Budgeted || manifest.CodeContext.DocumentSummary[0].PromptBytes >= manifest.CodeContext.DocumentSummary[0].Bytes {
		t.Fatalf("document summary = %+v", manifest.CodeContext.DocumentSummary)
	}
}

func TestPromptContextManifestTelemetryRecordsSourcesWithoutContent(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project secret instruction")
	mustWriteQueryTest(t, filepath.Join(project, ".claude", "rules", "go.md"), "go rule secret")

	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	event := findTelemetryEvent(events, "query.prompt_context")
	if event.Name == "" {
		t.Fatalf("missing query.prompt_context event: %+v", events)
	}
	if _, ok := event.Properties["context_manifest"]; !ok {
		t.Fatalf("missing manifest properties: %+v", event.Properties)
	}
	encoded, err := json.Marshal(event.Properties)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, notWant := range []string{"project secret instruction", "go rule secret", "CLAUDE.md"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("manifest leaked %q: %s", notWant, text)
		}
	}
	if !strings.Contains(text, `"code_memory"`) || !strings.Contains(text, `"skills_catalog"`) {
		t.Fatalf("manifest missing source summary: %s", text)
	}
}

func TestPromptContextManifestTelemetryRecordsTenantCounts(t *testing.T) {
	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:          "test",
		MaxTurns:       1,
		CWD:            t.TempDir(),
		PromptMode:     "chat",
		SystemAddendum: "# Tenant Context\ncontext",
		TenantContextManifest: TenantContextManifest{
			Active:          true,
			Addendum:        true,
			MemoryItems:     2,
			Profile:         true,
			Documents:       1,
			KnowledgeChunks: 3,
		},
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	event := findTelemetryEvent(events, "query.prompt_context")
	encoded, err := json.Marshal(event.Properties)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{`"knowledge_chunks":3`, `"memory_items":2`, `"documents":1`, `"profile":true`, `"tenant_context"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in manifest: %s", want, text)
		}
	}
}

func TestPromptContextManifestRecordsInlineTenantSkillMetadata(t *testing.T) {
	var events []telemetry.Event
	restore := telemetry.SetDefaultEmitter(telemetry.NewEmitter(telemetry.SinkFunc(func(_ context.Context, event telemetry.Event) error {
		events = append(events, event)
		return nil
	})))
	defer restore()

	provider := memorySkillProvider{items: map[string]skills.Skill{
		"teach-v2": func() skills.Skill {
			skill := skills.SkillFromContent("teach-v2", "teach-v2", "# Teach V2\n\nsecret inline body")
			skill.Version = "2"
			skill.PackageSHA256 = "abc123"
			skill.PackageRef = "file:///pkg.zip"
			skill.RuntimeRef = "file:///runtime.md"
			return skill
		}(),
	}}
	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:                   "test",
		MaxTurns:                1,
		CWD:                     t.TempDir(),
		PromptMode:              "chat",
		SkillProvider:           provider,
		DisableTools:            true,
		InlineTenantSkills:      []string{"teach-v2"},
		InlineTenantSkillSource: "header",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	event := findTelemetryEvent(events, "query.prompt_context")
	encoded, err := json.Marshal(event.Properties)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{`"tenant_skill_inline"`, `"selector":"header"`, `"skill_keys":["teach-v2"]`, `"loaded":true`, `"loaded_keys":["teach-v2"]`, `"versions":["2"]`, `"package_sha256":["abc123"]`, `"package_refs":["file:///pkg.zip"]`, `"tenant_runtime":{"active":true,"resolved":true`, `"tenant_context":{"active":false}`, `"tenant_runtime.active":true`, `"tenant_runtime.resolved":true`, `"tenant_runtime.package_sha256":"abc123"`, `"tenant_skill_inline.package_sha256":"abc123"`, `"tenant_skill_inline.loaded":true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in manifest: %s", want, text)
		}
	}
	if strings.Contains(text, "secret inline body") {
		t.Fatalf("manifest leaked inline skill body: %s", text)
	}
}

func TestPromptContextManifestTranscriptEntry(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project instruction")
	recorder, err := (session.Store{Root: root}).NewRecorder(project)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()

	querySession := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "code",
		Recorder:   recorder,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, entry := range entries {
		if entry.Type != "prompt_context" {
			continue
		}
		found = true
		var manifest ContextManifest
		if err := json.Unmarshal(entry.Metadata, &manifest); err != nil {
			t.Fatalf("metadata = %s err=%v", entry.Metadata, err)
		}
		if manifest.Profile != "code" || !manifest.CodeContext.Active || manifest.CodeContext.Documents == 0 {
			t.Fatalf("manifest = %+v", manifest)
		}
	}
	if !found {
		t.Fatalf("missing prompt_context entry: %+v", entries)
	}
}

func findTelemetryEvent(events []telemetry.Event, name string) telemetry.Event {
	for _, event := range events {
		if event.Name == name {
			return event
		}
	}
	return telemetry.Event{}
}

func findTelemetryEventByTool(events []telemetry.Event, name, toolName string) telemetry.Event {
	for _, event := range events {
		if event.Name == name && event.ToolName == toolName {
			return event
		}
	}
	return telemetry.Event{}
}

func TestPromptModeChatExcludesLocalCodeContext(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	mustWriteQueryTest(t, filepath.Join(project, "CLAUDE.md"), "project-secret-guidance")
	mustWriteQueryTest(t, filepath.Join(project, ".mcp.json"), `{"mcpServers":{"local":{"command":"echo"}}}`)

	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:      "test",
		MaxTurns:   1,
		CWD:        project,
		PromptMode: "chat",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.messages) != 1 || len(streamer.messages[0]) != 1 {
		t.Fatalf("chat mode should only send the actual user message, got %+v", streamer.messages)
	}
	system := streamer.systems[0]
	for _, notWant := range []string{"project-secret-guidance", "Git Snapshot", "Current branch:", "Configured MCP servers:"} {
		if strings.Contains(system, notWant) {
			t.Fatalf("chat system leaked %q:\n%s", notWant, system)
		}
	}
	if !strings.Contains(system, "tenant chat session") {
		t.Fatalf("chat boundary missing:\n%s", system)
	}
}

func TestPromptModePriorityMatchesUpstreamOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   project,
		SystemPrompt:          "custom",
		CoordinatorPrompt:     "coordinator",
		MainThreadAgentPrompt: "agent",
		SystemAddendum:        "append",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := streamer.systems[0]; !strings.Contains(got, "agent") || strings.Contains(got, "coordinator") || strings.Contains(got, "custom") || !strings.Contains(got, "append") {
		t.Fatalf("main-thread priority system = %q", got)
	}

	streamer = &systemStreamer{}
	querySession = New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:             "test",
		MaxTurns:          1,
		CWD:               project,
		SystemPrompt:      "custom",
		CoordinatorPrompt: "coordinator",
		SystemAddendum:    "append",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := streamer.systems[0]; !strings.Contains(got, "coordinator") || strings.Contains(got, "custom") || !strings.Contains(got, "append") {
		t.Fatalf("coordinator priority system = %q", got)
	}

	streamer = &systemStreamer{}
	querySession = New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                "test",
		MaxTurns:             1,
		CWD:                  project,
		OverrideSystemPrompt: "override",
		SystemAddendum:       "append",
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := streamer.systems[0]; got != "override" {
		t.Fatalf("override system = %q", got)
	}
}

func TestCoordinatorModeRestrictsTools(t *testing.T) {
	registry := tools.NewRegistry(
		namedQueryTool{name: "AgentCreate"},
		namedQueryTool{name: "AgentMessage"},
		namedQueryTool{name: "TaskOutput"},
		namedQueryTool{name: "Bash"},
		namedQueryTool{name: "Read"},
	)
	session := New(&systemStreamer{}, registry, Options{
		Model:             "test",
		MaxTurns:          1,
		CWD:               t.TempDir(),
		CoordinatorPrompt: "coordinate only",
	})
	names := toolDefinitionNames(session.ToolDefinitions())
	for _, want := range []string{"AgentCreate", "AgentMessage", "TaskOutput"} {
		if !containsString(names, want) {
			t.Fatalf("coordinator tool definitions = %+v, missing %s", names, want)
		}
	}
	for _, notWant := range []string{"Bash", "Read"} {
		if containsString(names, notWant) {
			t.Fatalf("coordinator tool definitions leaked %s: %+v", notWant, names)
		}
	}
}

func TestCoordinatorModeDoesNotOverrideMainThreadAgentTools(t *testing.T) {
	registry := tools.NewRegistry(namedQueryTool{name: "AgentCreate"}, namedQueryTool{name: "Bash"})
	session := New(&systemStreamer{}, registry, Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   t.TempDir(),
		CoordinatorPrompt:     "coordinate",
		MainThreadAgentPrompt: "main agent",
	})
	names := toolDefinitionNames(session.ToolDefinitions())
	if !containsString(names, "Bash") {
		t.Fatalf("main-thread agent tool definitions = %+v, want unfiltered registry", names)
	}
}

func TestCoordinatorModeBlocksDirectOrdinaryToolCall(t *testing.T) {
	streamer := &singleToolUseStreamer{
		toolName: "Bash",
		input:    json.RawMessage(`{"command":"pwd"}`),
	}
	session := New(streamer, tools.NewRegistry(namedQueryTool{name: "AgentCreate"}, namedQueryTool{name: "Bash"}), Options{
		Model:             "test",
		MaxTurns:          1,
		CWD:               t.TempDir(),
		CoordinatorPrompt: "coordinate only",
	})
	res, err := session.Run(context.Background(), "hi", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "max turns reached") {
		t.Fatalf("err=%v, want max turns after blocked tool call", err)
	}
	if len(res.ToolCalls) != 1 || !res.ToolCalls[0].IsError || !strings.Contains(res.ToolCalls[0].Output, "unknown tool: Bash") {
		t.Fatalf("tool calls = %+v", res.ToolCalls)
	}
}

func messageText(msg anthropic.MessageParam) string {
	var parts []string
	for _, block := range msg.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func allMessageText(messages []anthropic.MessageParam) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if text := messageText(msg); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolPlanningReminderMessages() []anthropic.MessageParam {
	var toolUses []anthropic.ContentBlock
	var toolResults []anthropic.ContentBlock
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("toolu_%d", i)
		toolUses = append(toolUses, anthropic.ContentBlock{Type: "tool_use", ID: id, Name: "Read"})
		if i < 8 {
			toolResults = append(toolResults, anthropic.ContentBlock{Type: "tool_result", ToolUseID: id, Content: "result"})
		}
	}
	return []anthropic.MessageParam{
		{Role: "assistant", Content: toolUses},
		{Role: "user", Content: toolResults},
	}
}

func lastToolResultContains(messages []anthropic.MessageParam, want string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		sawToolResult := false
		for _, block := range messages[i].Content {
			if block.Type != "tool_result" {
				continue
			}
			sawToolResult = true
			if strings.Contains(block.Content, want) {
				return true
			}
		}
		if sawToolResult {
			return false
		}
	}
	return false
}

func lastToolResultText(messages []anthropic.MessageParam) string {
	for i := len(messages) - 1; i >= 0; i-- {
		for _, block := range messages[i].Content {
			if block.Type == "tool_result" {
				return block.Content
			}
		}
	}
	return ""
}

func lastToolResultTexts(messages []anthropic.MessageParam) []string {
	for i := len(messages) - 1; i >= 0; i-- {
		out := []string{}
		for _, block := range messages[i].Content {
			if block.Type == "tool_result" {
				out = append(out, block.Content)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func allToolResultTexts(messages []anthropic.MessageParam) []string {
	out := []string{}
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "tool_result" {
				out = append(out, block.Content)
			}
		}
	}
	return out
}

func lastToolResultIsError(messages []anthropic.MessageParam) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		sawToolResult := false
		for _, block := range messages[i].Content {
			if block.Type != "tool_result" {
				continue
			}
			sawToolResult = true
			if block.IsError {
				return true
			}
		}
		if sawToolResult {
			return false
		}
	}
	return false
}

func mustWriteQueryTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func runQueryTestGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func TestMessageCacheBreakpointOnLastRequestMessage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("NIUMA_PROMPT_CACHE_1H_ALLOWLIST", "repl_main_thread*")
	streamer := &systemStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "claude-sonnet-4-6",
		MaxTurns: 1,
		CWD:      t.TempDir(),
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	messages := streamer.messages[0]
	last := messages[len(messages)-1].Content
	if last[len(last)-1].CacheControl == nil || last[len(last)-1].CacheControl.Type != "ephemeral" {
		t.Fatalf("messages = %+v", messages)
	}
	if last[len(last)-1].CacheControl.TTL != "1h" {
		t.Fatalf("message cache ttl = %+v", last[len(last)-1].CacheControl)
	}
}

func TestPromptCache1hTTLMatchesEligibilityAndQuerySourceAllowlist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER_TYPE", "ant")
	t.Setenv("NIUMA_PROMPT_CACHE_1H_ALLOWLIST", `["sdk","agent:*"]`)
	if shouldUsePromptCache1hTTL("repl_main_thread") {
		t.Fatal("repl source should not match sdk/agent allowlist")
	}
	if !shouldUsePromptCache1hTTL("agent:custom") || !shouldUsePromptCache1hTTL("sdk") {
		t.Fatal("sdk and agent prefix should match allowlist")
	}
	cache := cacheControlForScope("global", "agent:custom")
	if cache.Type != "ephemeral" || cache.TTL != "1h" || cache.Scope != "global" {
		t.Fatalf("cache control = %+v", cache)
	}
}

func TestPromptCache1hTTLOmittedWhenUserNotEligible(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NIUMA_PROMPT_CACHE_1H_ALLOWLIST", "*")
	if shouldUsePromptCache1hTTL("repl_main_thread") {
		t.Fatal("non-eligible user should not get 1h ttl")
	}
	cache := cacheControlForScope("org", "repl_main_thread")
	if cache.Type != "ephemeral" || cache.TTL != "" || cache.Scope != "" {
		t.Fatalf("cache control = %+v", cache)
	}
}

func hasCacheTTL(blocks []anthropic.SystemBlock, ttl string) bool {
	for _, block := range blocks {
		if block.CacheControl != nil && block.CacheControl.Type == "ephemeral" && block.CacheControl.TTL == ttl {
			return true
		}
	}
	return false
}

func hasCacheScope(blocks []anthropic.SystemBlock, scope string) bool {
	for _, block := range blocks {
		if block.CacheControl != nil && block.CacheControl.Scope == scope {
			return true
		}
	}
	return false
}

type echoTool struct{}

func (echoTool) Name() string        { return "Echo" }
func (echoTool) Description() string { return "echo input text" }
func (echoTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)
}
func (echoTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(input, &params)
	return tools.Result{Content: params.Text}
}

type closureBashTool struct {
	commands []string
}

func (t *closureBashTool) Name() string        { return "Bash" }
func (t *closureBashTool) Description() string { return "closure bash" }
func (t *closureBashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (t *closureBashTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &params)
	t.commands = append(t.commands, params.Command)
	return tools.Result{Content: "ok"}
}

type failedAuditBashTool struct {
	commands []string
}

func (t *failedAuditBashTool) Name() string        { return "Bash" }
func (t *failedAuditBashTool) Description() string { return "failed audit bash" }
func (t *failedAuditBashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (t *failedAuditBashTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &params)
	t.commands = append(t.commands, params.Command)
	if strings.Contains(params.Command, "for dir in") {
		return tools.Result{Content: "write path /dev/null is outside the current workspace and configured additional directories", IsError: true}
	}
	return tools.Result{Content: "401 3-ai-agents/hermes-agent/README.md"}
}

type verificationBashTool struct{}

func (verificationBashTool) Name() string        { return "Bash" }
func (verificationBashTool) Description() string { return "verification bash" }
func (verificationBashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (verificationBashTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &params)
	if strings.Contains(params.Command, "go test") && strings.Contains(params.Command, "git diff --check") {
		return tools.Result{Content: "verification ok"}
	}
	return tools.Result{Content: "missing verification command", IsError: true}
}

type failingVerificationBashTool struct{}

func (failingVerificationBashTool) Name() string        { return "Bash" }
func (failingVerificationBashTool) Description() string { return "failing verification bash" }
func (failingVerificationBashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)
}
func (failingVerificationBashTool) Run(_ context.Context, input json.RawMessage, _ tools.Context) tools.Result {
	var params struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &params)
	return tools.Result{Content: "exit status 1\n--- FAIL: TestExample\n    example_test.go:11: got actual, want expected\nFAIL", IsError: true}
}

type capabilityTaskTool struct{}

func (capabilityTaskTool) Name() string        { return "Task" }
func (capabilityTaskTool) Description() string { return "task with capability loop output" }
func (capabilityTaskTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"description":{"type":"string"},"prompt":{"type":"string"}}}`)
}
func (capabilityTaskTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: strings.Join([]string{
		"Summary: checked synchronous Task evidence.",
		"",
		"<capability_loop>",
		`{"session_id":"sync-task-session","transcript_path":"/tmp/sync-task.jsonl","output_file":"/tmp/sync-task.output","worktree_path":"/tmp/sync-task-worktree","worktree_branch":"agent/sync-task","capability_loop":{"evidence":["TASK_LOOP_EVIDENCE: synchronous Task evidence reached parent runtime status."],"assumptions":["parent will plan from Task result"],"unknowns":["browser validation not sampled"],"verification":["go test ./internal/query -run TestTaskCapabilityLoopBecomesRecentEvidenceContext -count=1"],"risks":["raw Task result may be long or budgeted"],"next_action":"TASK_LOOP_NEXT_ACTION: cite Task evidence before final synthesis"}}`,
		"</capability_loop>",
		"These structured fields are parent decision context.",
	}, "\n")}
}

type largeResultTool struct {
	content       string
	maxResultSize int
}

func (largeResultTool) Name() string        { return "LongTool" }
func (largeResultTool) Description() string { return "long output tool" }
func (largeResultTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}
func (t largeResultTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: t.content}
}
func (t largeResultTool) MaxResultSizeChars() int { return t.maxResultSize }

type persistedToolResultStreamer struct {
	calls      int
	toolResult string
}

func (s *persistedToolResultStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_large",
				Name:  "LongTool",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	s.toolResult = lastToolResultText(req.Messages)
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type aggregateBudgetStreamer struct {
	calls       int
	large       string
	medium      string
	small       string
	toolResults []string
}

func (s *aggregateBudgetStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
				{Type: "tool_use", ID: "toolu_large", Name: "Echo", Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, s.large))},
				{Type: "tool_use", ID: "toolu_medium", Name: "Echo", Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, s.medium))},
				{Type: "tool_use", ID: "toolu_small", Name: "Echo", Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, s.small))},
			}},
			StopReason: "tool_use",
		}, nil
	}
	s.toolResults = lastToolResultTexts(req.Messages)
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type historyBudgetStreamer struct {
	calls       int
	old         string
	fresh       string
	toolResults []string
}

func (s *historyBudgetStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	switch s.calls {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_old",
				Name:  "Echo",
				Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, s.old)),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		if !lastToolResultContains(req.Messages, s.old) {
			return nil, io.ErrUnexpectedEOF
		}
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_fresh",
				Name:  "Echo",
				Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, s.fresh)),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		s.toolResults = allToolResultTexts(req.Messages)
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
		}, nil
	}
}

type readBudgetSkipStreamer struct {
	calls        int
	filePath     string
	echo         string
	toolResults  map[string]string
	requestCount int
}

func (s *readBudgetSkipStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	s.requestCount = len(req.Messages)
	if s.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
				{Type: "tool_use", ID: "toolu_read", Name: "Read", Input: json.RawMessage(fmt.Sprintf(`{"file_path":%q}`, s.filePath))},
				{Type: "tool_use", ID: "toolu_echo", Name: "Echo", Input: json.RawMessage(fmt.Sprintf(`{"text":%q}`, s.echo))},
			}},
			StopReason: "tool_use",
		}, nil
	}
	s.toolResults = map[string]string{}
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if block.Type == "tool_result" {
				s.toolResults[block.ToolUseID] = block.Content
			}
		}
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type bashBackgroundReadStreamer struct {
	calls      int
	bashResult string
	logPath    string
	readResult string
}

func (s *bashBackgroundReadStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.calls++
	switch s.calls {
	case 1:
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_bash",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"printf bg-ready; sleep 0.05; printf bg-done","description":"Run short background verification command","run_in_background":true,"timeout":3000}`),
			}}},
			StopReason: "tool_use",
		}, nil
	case 2:
		s.bashResult = requestToolResultText(req.Messages, "toolu_bash")
		if s.bashResult == "" {
			return nil, fmt.Errorf("missing Bash tool_result")
		}
		var payload struct {
			LogPath string `json:"log_path"`
		}
		if err := json.Unmarshal([]byte(s.bashResult), &payload); err != nil {
			return nil, fmt.Errorf("parse Bash tool_result: %w; content=%s", err, s.bashResult)
		}
		if payload.LogPath == "" {
			return nil, fmt.Errorf("Bash tool_result missing log_path: %s", s.bashResult)
		}
		if !strings.Contains(s.bashResult, "BashOutput") {
			return nil, fmt.Errorf("Bash tool_result missing BashOutput polling guidance: %s", s.bashResult)
		}
		if err := waitFileContains(payload.LogPath, "bg-done", 2*time.Second); err != nil {
			return nil, err
		}
		s.logPath = payload.LogPath
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_read",
				Name:  "Read",
				Input: json.RawMessage(fmt.Sprintf(`{"file_path":%q}`, s.logPath)),
			}}},
			StopReason: "tool_use",
		}, nil
	default:
		s.readResult = requestToolResultText(req.Messages, "toolu_read")
		if !strings.Contains(s.readResult, "bg-done") {
			return nil, fmt.Errorf("Read tool_result missing background output: %.300q", s.readResult)
		}
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{
			Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
			StopReason: "end_turn",
		}, nil
	}
}

func waitFileContains(path, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			last = string(data)
			if strings.Contains(last, want) {
				return nil
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("file %s did not contain %q before timeout; last=%q", path, want, last)
}

type namedQueryTool struct {
	name string
}

func (t namedQueryTool) Name() string        { return t.name }
func (t namedQueryTool) Description() string { return t.name + " tool" }
func (t namedQueryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":true}`)
}
func (t namedQueryTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: t.name}
}

func toolDefinitionNames(defs []anthropic.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	sort.Strings(names)
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type queryAgentMessageStore struct {
	events    []mysqlstore.AgentTaskEvent
	tasks     []mysqlstore.AgentTask
	listCalls int
}

func (s *queryAgentMessageStore) CreateAgentTask(context.Context, agenttasks.TaskInput) (uint64, error) {
	return 1, nil
}

func (s *queryAgentMessageStore) FinishAgentTask(context.Context, uint64, string, string) error {
	return nil
}

func (s *queryAgentMessageStore) AppendAgentTaskEvent(context.Context, agenttasks.EventInput) (uint64, error) {
	return 1, nil
}

func (s *queryAgentMessageStore) ListAgentTaskEvents(_ context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	s.listCalls++
	out := make([]mysqlstore.AgentTaskEvent, 0)
	for _, event := range s.events {
		if event.TaskID == taskID {
			out = append(out, event)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *queryAgentMessageStore) ListAgentTasks(context.Context, int) ([]mysqlstore.AgentTask, error) {
	return append([]mysqlstore.AgentTask(nil), s.tasks...), nil
}

type runtimeStatusLoopStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *runtimeStatusLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"hello"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type agentGetAcknowledgementStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *agentGetAcknowledgementStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_agent_get_43",
				Name:  "AgentGet",
				Input: json.RawMessage(`{"task_id":43}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type taskCapabilityLoopStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *taskCapabilityLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_sync_task",
				Name:  "Task",
				Input: json.RawMessage(`{"description":"sync evidence audit","prompt":"inspect capability loop"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type partialTaskParentStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *partialTaskParentStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if len(s.requests) == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_partial_task",
				Name:  "Task",
				Input: json.RawMessage(`{"description":"partial stream audit","prompt":"collect partial evidence before failure"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type partialTaskChildStreamer struct{}

func (partialTaskChildStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		if err := cb.OnText("PARTIAL_TASK_STREAM_EVIDENCE: child found evidence before stream failure."); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("child stream failed after partial evidence")
}

func TestSessionRunsToolLoop(t *testing.T) {
	session := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 3, CWD: t.TempDir()})
	var out strings.Builder
	res, err := session.Run(context.Background(), "hi", &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "done" || res.Response != "done" {
		t.Fatalf("out=%q response=%q, want done", out.String(), res.Response)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Output != "hello" {
		t.Fatalf("tool calls = %+v", res.ToolCalls)
	}
}

func TestSessionUsageTracksLastModelRequestInput(t *testing.T) {
	session := New(&usageLoopStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 3, CWD: t.TempDir()})
	res, err := session.Run(context.Background(), "hi", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens != 300 {
		t.Fatalf("InputTokens = %d, want cumulative 300", res.Usage.InputTokens)
	}
	if res.Usage.LastInputTokens != 200 {
		t.Fatalf("LastInputTokens = %d, want last model request 200", res.Usage.LastInputTokens)
	}
	if res.Usage.OutputTokens != 15 {
		t.Fatalf("OutputTokens = %d, want cumulative 15", res.Usage.OutputTokens)
	}
}

func TestSessionReadsAgentMessagesFromTaskEventsOnce(t *testing.T) {
	payload, err := json.Marshal(agenttasks.MessageInput{TaskID: 42, FromAgent: "coordinator", Content: "please continue"})
	if err != nil {
		t.Fatal(err)
	}
	store := &queryAgentMessageStore{events: []mysqlstore.AgentTaskEvent{
		{ID: 1, TaskID: 42, EventType: agenttasks.EventMessage, PayloadJSON: string(payload), TraceID: "trace-1"},
		{ID: 2, TaskID: 42, EventType: agenttasks.EventTurnStart, PayloadJSON: `{}`},
	}}
	querySession := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", CWD: t.TempDir(), TaskStore: store})
	first := querySession.pendingAgentMessages(context.Background(), 42)
	if len(first) != 1 || first[0].Content != "please continue" || first[0].FromAgent != "coordinator" || first[0].TraceID != "trace-1" {
		t.Fatalf("first pending messages = %+v", first)
	}
	second := querySession.pendingAgentMessages(context.Background(), 42)
	if len(second) != 0 {
		t.Fatalf("second pending messages = %+v, want no duplicate", second)
	}
	if store.listCalls != 2 {
		t.Fatalf("listCalls=%d, want 2", store.listCalls)
	}
}

func TestRunStreamJSONIncludesNestedAgentProgress(t *testing.T) {
	querySession := New(&taskProgressStreamer{}, tools.NewRegistry(progressTaskTool{}), Options{Model: "test", MaxTurns: 2, CWD: t.TempDir()})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"type":"nested_agent_progress"`) || !strings.Contains(out.String(), `"event":"turn_start"`) {
		t.Fatalf("stream-json output = %s", out.String())
	}
}

func TestRunReportsNestedAgentProgressCallback(t *testing.T) {
	var events []agenttasks.EventInput
	querySession := New(&taskProgressStreamer{}, tools.NewRegistry(progressTaskTool{}), Options{
		Model: "test", MaxTurns: 2, CWD: t.TempDir(),
		NestedAgentProgress: func(event agenttasks.EventInput) {
			events = append(events, event)
		},
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].TaskID != 77 || events[0].EventType != agenttasks.EventStarted {
		t.Fatalf("events = %+v", events)
	}
}

func TestRunStreamJSONIncludesMessageBlockAndUsageEvents(t *testing.T) {
	querySession := New(&usageStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 1, CWD: t.TempDir()})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"message_start"`, `"type":"content_block_start"`, `"type":"content_block_stop"`, `"type":"usage_delta"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludesUpstreamEnvelopeFields(t *testing.T) {
	querySession := New(&usageStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test-model", MaxTurns: 1, CWD: t.TempDir()})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"message":{"id":"turn_1","type":"message","role":"assistant","model":"test-model"`,
		`"content_block":{"type":"text"`,
		`"delta":{"type":"text_delta","text":"ok"}`,
		`"type":"message_delta"`,
		`"type":"message_stop"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONCanEmitUpstreamStreamEventEnvelopes(t *testing.T) {
	querySession := New(&usageStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test-model", MaxTurns: 1, CWD: t.TempDir(), IncludeStreamEvents: true})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type":"stream_event"`,
		`"event":{"message"`,
		`"session_id":"local"`,
		`"parent_tool_use_id":null`,
		`"type":"message_start"`,
		`"ttftMs":0`,
		`"type":"content_block_delta"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludesResultEnvelope(t *testing.T) {
	querySession := New(&usageStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test-model", MaxTurns: 1, CWD: t.TempDir(), TraceID: "trace-123"})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type":"result"`,
		`"subtype":"success"`,
		`"is_error":false`,
		`"num_turns":1`,
		`"result":"ok"`,
		`"session_id":"trace-123"`,
		`"modelUsage":{"test-model"`,
		`"permission_denials":[]`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludesToolInputDelta(t *testing.T) {
	querySession := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 3, CWD: t.TempDir()})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"input_json_delta"`, `"partial_json":"{\"text\":\"hello\"}"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludesHookDecisionAndUpdatedInput(t *testing.T) {
	querySession := New(&hookDecisionStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:             "test",
		MaxTurns:          2,
		CWD:               t.TempDir(),
		IncludeHookEvents: true,
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.PreToolUse: {{Command: `printf '{"permissionDecision":"deny","permissionDecisionReason":"blocked","updatedInput":{"text":"rewritten"}}'`}},
		}),
	})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"hook_result"`, `"permissionDecision":"deny"`, `"permissionDecisionReason":"blocked"`, `"updatedInput":{"text":"rewritten"}`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunToolRechecksGitAuthorizationAfterHookRewrite(t *testing.T) {
	bash := &closureBashTool{}
	querySession := New(&usageStreamer{}, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.PreToolUse: {{Command: `printf '%s' '{"updatedInput":{"command":"git commit -m hook"}}'`}},
		}),
	})
	block := anthropic.ContentBlock{ID: "toolu_hook_git", Name: "Bash", Input: json.RawMessage(`{"command":"git status --short"}`)}
	trace := querySession.runTool(context.Background(), tools.NewRegistry(bash), block, runCallbacks{})
	if !trace.IsError || len(bash.commands) != 0 || !strings.Contains(trace.Output, "Shared-State Authorization Gate") {
		t.Fatalf("hook-rewritten commit bypassed authorization: trace=%+v commands=%+v", trace, bash.commands)
	}
}

func TestUserPromptSubmitHookCanRewritePrompt(t *testing.T) {
	streamer := &capturingStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.UserPromptSubmit: {{Command: `printf '{"updatedInput":{"prompt":"rewritten prompt"}}'`}},
		}),
	})
	if _, err := querySession.Run(context.Background(), "original prompt", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(streamer.prompts) != 1 || streamer.prompts[0] != "rewritten prompt" {
		t.Fatalf("prompts = %+v", streamer.prompts)
	}
}

func TestRunStreamJSONIncludesUserPromptSubmitAndStopHooks(t *testing.T) {
	querySession := New(&usageStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:             "test-model",
		MaxTurns:          1,
		CWD:               t.TempDir(),
		IncludeHookEvents: true,
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.UserPromptSubmit: {{Command: `printf '{"message":"prompt-ok"}'`}},
			hooks.Stop:             {{Command: `printf '{"message":"stop-ok"}'`}},
		}),
	})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type":"hook_start"`,
		`"event":"UserPromptSubmit"`,
		`"name":"UserPromptSubmit"`,
		`"type":"hook_result"`,
		`"output":"prompt-ok"`,
		`"event":"Stop"`,
		`"name":"Stop"`,
		`"output":"stop-ok"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludesThinkingAndPartialEvents(t *testing.T) {
	querySession := New(&thinkingStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 1, CWD: t.TempDir(), IncludePartialMessages: true})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"thinking_delta"`, `"type":"partial_message"`, `"text":"hello"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludesLongTailDeltaEvents(t *testing.T) {
	querySession := New(&longTailDeltaStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 1, CWD: t.TempDir(), IncludeStreamEvents: true})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type":"signature_delta"`,
		`"signature":"sig_123"`,
		`"type":"citations_delta"`,
		`"citation":{"type":"char_location"`,
		`"type":"connector_text_delta"`,
		`"connector_text":"linked"`,
		`"type":"stream_event"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
}

func TestRunStreamJSONIncludeFlagsGateOptionalEvents(t *testing.T) {
	querySession := New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 3, CWD: t.TempDir()})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"type":"hook_start"`) || strings.Contains(out.String(), `"type":"partial_message"`) {
		t.Fatalf("optional events should be disabled by default: %s", out.String())
	}

	querySession = New(&fakeStreamer{}, tools.NewRegistry(echoTool{}), Options{
		Model:                  "test",
		MaxTurns:               3,
		CWD:                    t.TempDir(),
		IncludeHookEvents:      true,
		IncludePartialMessages: true,
	})
	out.Reset()
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"type":"hook_start"`) || !strings.Contains(out.String(), `"type":"partial_message"`) {
		t.Fatalf("optional events were not emitted: %s", out.String())
	}
}

func TestRunStreamJSONEmitsErrorEnvelope(t *testing.T) {
	querySession := New(&errorStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", MaxTurns: 1, CWD: t.TempDir()})
	var out strings.Builder
	if err := querySession.RunStreamJSON(context.Background(), "hi", &out); err == nil {
		t.Fatal("expected stream error")
	}
	if !strings.Contains(out.String(), `"type":"error"`) || !strings.Contains(out.String(), `"message":"boom"`) || !strings.Contains(out.String(), `"subtype":"error_during_execution"`) || !strings.Contains(out.String(), `"errors":["boom"]`) || !strings.Contains(out.String(), `"type":"done"`) {
		t.Fatalf("stream-json output = %s", out.String())
	}
}

type errorStreamer struct{}

func (s *errorStreamer) StreamMessages(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	return nil, fmt.Errorf("boom")
}

type thinkingStreamer struct{}

func (s *thinkingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnThinking != nil {
		if err := cb.OnThinking("considering"); err != nil {
			return nil, err
		}
	}
	if cb.OnText != nil {
		if err := cb.OnText("he"); err != nil {
			return nil, err
		}
		if err := cb.OnText("llo"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "thinking", Thinking: "considering"}, {Type: "text", Text: "hello"}}},
		StopReason: "end_turn",
	}, nil
}

type longTailDeltaStreamer struct{}

func (s *longTailDeltaStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnThinking != nil {
		if err := cb.OnThinking("consider"); err != nil {
			return nil, err
		}
	}
	if cb.OnSignature != nil {
		if err := cb.OnSignature("sig_123"); err != nil {
			return nil, err
		}
	}
	if cb.OnText != nil {
		if err := cb.OnText("answer"); err != nil {
			return nil, err
		}
	}
	citation := json.RawMessage(`{"type":"char_location","cited_text":"answer","document_index":0,"document_title":"doc","start_char_index":0,"end_char_index":6}`)
	if cb.OnCitation != nil {
		if err := cb.OnCitation(citation); err != nil {
			return nil, err
		}
	}
	if cb.OnConnectorText != nil {
		if err := cb.OnConnectorText("linked"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{
			{Type: "thinking", Thinking: "consider", Signature: "sig_123"},
			{Type: "text", Text: "answer", Citations: []json.RawMessage{citation}},
			{Type: "connector_text", ConnectorText: "linked"},
		}},
		StopReason: "end_turn",
	}, nil
}

type usageStreamer struct{}

func (s *usageStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if err := cb.OnText("ok"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}},
		StopReason: "end_turn",
		Usage:      anthropic.Usage{InputTokens: 2, OutputTokens: 1},
	}, nil
}

type capturingStreamer struct {
	prompts []string
}

func (s *capturingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if len(req.Messages) > 0 && len(req.Messages[len(req.Messages)-1].Content) > 0 {
		s.prompts = append(s.prompts, lastTextBlock(req.Messages[len(req.Messages)-1]))
	}
	if err := cb.OnText("ok"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}},
		StopReason: "end_turn",
	}, nil
}

func lastTextBlock(message anthropic.MessageParam) string {
	for i := len(message.Content) - 1; i >= 0; i-- {
		if message.Content[i].Type == "text" {
			return message.Content[i].Text
		}
	}
	return ""
}

type requestCapturingStreamer struct {
	requests []anthropic.MessagesRequest
}

func (s *requestCapturingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if err := cb.OnText("ok"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}},
		StopReason: "end_turn",
	}, nil
}

type taskProgressStreamer struct {
	calls int
}

func (f *taskProgressStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	if f.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_task",
				Name:  "Task",
				Input: json.RawMessage(`{"prompt":"work"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type hookDecisionStreamer struct {
	calls int
}

func (f *hookDecisionStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	if f.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_hook",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"hello"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultIsError(req.Messages) {
		return nil, io.ErrUnexpectedEOF
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type progressTaskTool struct{}

func (progressTaskTool) Name() string                 { return "Task" }
func (progressTaskTool) Description() string          { return "progress task" }
func (progressTaskTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (progressTaskTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	if toolContext.TaskProgress != nil {
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventStarted, PayloadJSON: `{"agent_name":"reviewer","model":"test-model"}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventTurnStart, PayloadJSON: `{"turn":1}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"text":"checking"}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventToolCall, PayloadJSON: `{"tool_name":"Read","tool_id":"toolu_read"}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventToolResult, PayloadJSON: `{"tool_name":"Read","tool_id":"toolu_read","is_error":false}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventTurnStart, PayloadJSON: `{"turn":2}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"text":"done"}`})
		toolContext.TaskProgress(agenttasks.EventInput{TaskID: 77, EventType: agenttasks.EventCompleted, PayloadJSON: `{"turns":2,"tool_calls":1}`})
	}
	return tools.Result{Content: "task done"}
}

func TestSkillScopedHookRunsAfterSkillActivation(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(project, "hook-ran")
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "scoped"), 0755); err != nil {
		t.Fatal(err)
	}
	skillBody := "---\nname: scoped\ndescription: Scoped hook\nhooks:\n  PreToolUse:\n    - command: \"printf ran > " + marker + "; printf '{}'\"\n---\n# Scoped\n"
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "scoped", "SKILL.md"), []byte(skillBody), 0644); err != nil {
		t.Fatal(err)
	}
	session := New(&skillHookStreamer{}, tools.NewRegistry(echoTool{}, skillLoaderTool{}), Options{Model: "test", MaxTurns: 3, CWD: project})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "ran" {
		t.Fatalf("marker = %q", string(data))
	}
}

type skillHookStreamer struct {
	calls    int
	thinking []*anthropic.ThinkingConfig
}

func (f *skillHookStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	f.thinking = append(f.thinking, req.Thinking)
	switch f.calls {
	case 1:
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: "tool_use", ID: "toolu_skill", Name: "Skill", Input: json.RawMessage(`{"name":"scoped"}`),
		}}}, StopReason: "tool_use"}, nil
	case 2:
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type: "tool_use", ID: "toolu_echo", Name: "Echo", Input: json.RawMessage(`{"text":"hello"}`),
		}}}, StopReason: "tool_use"}, nil
	default:
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
		return &anthropic.StreamResult{Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}}, StopReason: "end_turn"}, nil
	}
}

type skillLoaderTool struct{}

func (skillLoaderTool) Name() string                 { return "Skill" }
func (skillLoaderTool) Description() string          { return "load skill" }
func (skillLoaderTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (skillLoaderTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: "loaded"}
}

func TestSessionMapsActiveSkillEffortToThinkingConfig(t *testing.T) {
	project := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "scoped"), 0755); err != nil {
		t.Fatal(err)
	}
	skillBody := "---\nname: scoped\neffort: high\n---\n# Scoped\n"
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "scoped", "SKILL.md"), []byte(skillBody), 0644); err != nil {
		t.Fatal(err)
	}
	streamer := &skillHookStreamer{}
	session := New(streamer, tools.NewRegistry(echoTool{}, skilltool.New()), Options{Model: "test", MaxTokens: 5000, MaxTurns: 3, CWD: project})
	if _, err := session.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if streamer.calls < 2 {
		t.Fatalf("calls = %d", streamer.calls)
	}
	if len(streamer.thinking) < 3 || streamer.thinking[0] == nil || streamer.thinking[0].Effort != defaults.Effort || streamer.thinking[1] == nil || streamer.thinking[1].Effort != "high" || streamer.thinking[1].BudgetTokens != 4096 || streamer.thinking[2] == nil {
		t.Fatalf("thinking = %+v", streamer.thinking)
	}
}

func TestSessionRecordsPermissionAudit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorder(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "deny"})
	querySession := New(&permissionAuditStreamer{}, tools.NewRegistry(tools.Guard(echoTool{}, policy)), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
		Recorder: recorder,
	})
	if _, err := querySession.Run(context.Background(), "hi", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var sawPermission, sawDeniedResult bool
	for _, entry := range entries {
		if entry.Type == "permission" && entry.ToolName == "Echo" && entry.IsError && strings.Contains(entry.Content, `"allowed":false`) {
			sawPermission = true
		}
		if entry.Type == "tool_result" && entry.ToolName == "Echo" && entry.IsError && strings.Contains(entry.Content, "not in allow list") {
			sawDeniedResult = true
		}
	}
	if !sawPermission || !sawDeniedResult {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestPermissionPromptResponseParsesPersistentBehaviors(t *testing.T) {
	res := parsePermissionPromptResponse(`{"behavior":"allow_session","reason":"ok","rule":"Write:a.txt","payload":{"decision":"allow"}}`)
	if !res.Allowed || res.Destination != "session" || res.Decision != "allow" || res.Rule != "Write:a.txt" {
		t.Fatalf("allow_session = %+v", res)
	}
	if string(res.Payload) != `{"decision":"allow"}` {
		t.Fatalf("payload = %s", res.Payload)
	}
	res = parsePermissionPromptResponse(`{"behavior":"deny_project","reason":"no"}`)
	if res.Allowed || res.Destination != "project" || res.Decision != "deny" {
		t.Fatalf("deny_project = %+v", res)
	}
}

func TestSessionUsesInjectedPermissionPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	policy := permissions.FromSettings(config.PermissionSettings{DefaultMode: "ask"})
	called := false
	querySession := New(&bashPermissionStreamer{}, tools.NewRegistry(tools.Guard(permissionBashTool{}, policy)), Options{
		Model:    "test",
		MaxTurns: 2,
		CWD:      t.TempDir(),
		PermissionPrompt: func(ctx context.Context, req tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			called = true
			if req.ToolName != "Bash" {
				t.Fatalf("req = %+v", req)
			}
			return tools.PermissionPromptResponse{Allowed: true, Destination: "once", Reason: "ok"}
		},
	})
	result, err := querySession.Run(context.Background(), "hi", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !called || len(result.ToolCalls) != 1 || result.ToolCalls[0].IsError {
		t.Fatalf("called=%v result=%+v", called, result)
	}
}

type bashPermissionStreamer struct {
	calls int
}

func (f *bashPermissionStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	if f.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_bash",
				Name:  "Bash",
				Input: json.RawMessage(`{"command":"echo hello"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultContains(req.Messages, "approved") {
		return nil, io.ErrUnexpectedEOF
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

type permissionBashTool struct{}

func (permissionBashTool) Name() string                 { return "Bash" }
func (permissionBashTool) Description() string          { return "test bash" }
func (permissionBashTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (permissionBashTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	return tools.Result{Content: "approved"}
}

func TestSessionPermissionUpdateSessionAndProject(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	session := New(&systemStreamer{}, tools.NewRegistry(echoTool{}), Options{Model: "test", CWD: project})
	if err := session.applyPermissionUpdate(tools.PermissionUpdate{ToolName: "Write", Request: "a.txt", Decision: "allow", Destination: "session"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(session.sessionAllow, ",") != "Write(a.txt)" {
		t.Fatalf("session allow = %+v", session.sessionAllow)
	}
	if err := session.applyPermissionUpdate(tools.PermissionUpdate{ToolName: "Bash", Request: "rm *", Decision: "deny", Destination: "project"}); err != nil {
		t.Fatal(err)
	}
	settings := config.LoadSettingsFile(filepath.Join(project, ".niuma", "settings.json"))
	if strings.Join(settings.Permissions.Deny, ",") != "Bash(rm *)" {
		t.Fatalf("project permissions = %+v", settings.Permissions)
	}
	if strings.Join(session.sessionDeny, ",") != "Bash(rm *)" {
		t.Fatalf("session deny = %+v", session.sessionDeny)
	}
	if err := session.applyPermissionUpdate(tools.PermissionUpdate{ToolName: "Read", Request: "README.md", Decision: "allow", Destination: "global"}); err != nil {
		t.Fatal(err)
	}
	global := config.LoadGlobalSettings()
	if strings.Join(global.Permissions.Allow, ",") != "Read(README.md)" {
		t.Fatalf("global permissions = %+v", global.Permissions)
	}
	if strings.Join(session.sessionAllow, ",") != "Write(a.txt),Read(README.md)" {
		t.Fatalf("session allow = %+v", session.sessionAllow)
	}
	todoRequest := `{"todos":[{"content":"read files","status":"completed"},{"content":"edit schema","status":"in_progress"}]}`
	if err := session.applyPermissionUpdate(tools.PermissionUpdate{ToolName: "TodoWrite", Request: todoRequest, Decision: "allow", Destination: "global"}); err != nil {
		t.Fatal(err)
	}
	global = config.LoadGlobalSettings()
	if strings.Join(global.Permissions.Allow, ",") != "Read(README.md),TodoWrite" {
		t.Fatalf("global permissions = %+v", global.Permissions)
	}
	if strings.Join(session.sessionAllow, ",") != "Write(a.txt),Read(README.md),TodoWrite" {
		t.Fatalf("session allow = %+v", session.sessionAllow)
	}
	if err := session.applyPermissionUpdate(tools.PermissionUpdate{ToolName: "Write", Request: "local.txt", Decision: "deny", Destination: "local"}); err != nil {
		t.Fatal(err)
	}
	local := config.LoadSettingsFile(filepath.Join(project, ".niuma", "settings.local.json"))
	if strings.Join(local.Permissions.Deny, ",") != "Write(local.txt)" {
		t.Fatalf("local permissions = %+v", local.Permissions)
	}
}

type permissionAuditStreamer struct {
	calls int
}

func (f *permissionAuditStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	if f.calls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_1",
				Name:  "Echo",
				Input: json.RawMessage(`{"text":"hello"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if !lastToolResultIsError(req.Messages) {
		return nil, io.ErrUnexpectedEOF
	}
	if err := cb.OnText("done"); err != nil {
		return nil, err
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func TestMessagesFromTranscript(t *testing.T) {
	messages := MessagesFromTranscript([]session.Entry{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "tool_call", ToolID: "toolu_1", ToolName: "Read", Content: `{"file_path":"README.md"}`},
		{Type: "tool_result", ToolID: "toolu_1", ToolName: "Read", Content: "ok"},
	})
	if len(messages) != 3 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[1].Content[0].Type != "tool_use" || messages[2].Content[0].ToolUseID != "toolu_1" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestMessagesFromTranscriptIgnoresMetadataInsideToolTurn(t *testing.T) {
	messages, report := MessagesFromTranscriptWithReport([]session.Entry{
		{Type: "message", Role: "user", Content: "run tools"},
		{Type: "prompt_context", Role: "system", Content: "manifest"},
		{Type: "usage", Model: "test-model", InputTokens: 10, OutputTokens: 5},
		{Type: "tool_call", ToolID: "toolu_a", ToolName: "Bash", Content: `{"command":"a"}`},
		{Type: "tool_call", ToolID: "toolu_b", ToolName: "Bash", Content: `{"command":"b"}`},
		{Type: "permission", ToolID: "toolu_a", ToolName: "Bash", Content: `{"allowed":true}`},
		{Type: "tool_result", ToolID: "toolu_a", ToolName: "Bash", Content: "actual-a"},
		{Type: "file_change", Content: `{"path":"tmp.txt"}`},
		{Type: "permission", ToolID: "toolu_b", ToolName: "Bash", Content: `{"allowed":true}`},
		{Type: "tool_result", ToolID: "toolu_b", ToolName: "Bash", Content: "actual-b"},
		{Type: "content_replacement", Replacements: []session.ReplacementRecord{{
			Kind:        "tool-result",
			ToolUseID:   "toolu_a",
			Replacement: "<persisted-output>preview</persisted-output>",
		}}},
		{Type: "usage", Model: "test-model", InputTokens: 20, OutputTokens: 3},
		{Type: "message", Role: "assistant", Content: "done"},
	})
	if report.Interrupted || report.SyntheticToolResults != 0 || report.DroppedOrphanedToolResults != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(messages) != 4 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[1].Role != "assistant" || len(messages[1].Content) != 2 {
		t.Fatalf("tool calls not merged: %+v", messages)
	}
	if messages[2].Role != "user" || len(messages[2].Content) != 2 {
		t.Fatalf("tool results not merged: %+v", messages)
	}
	got := []string{messages[2].Content[0].Content, messages[2].Content[1].Content}
	if !reflect.DeepEqual(got, []string{"actual-a", "actual-b"}) {
		t.Fatalf("tool result content = %+v", got)
	}
	if messages[3].Role != "assistant" || messages[3].Content[0].Text != "done" {
		t.Fatalf("assistant message = %+v", messages[3])
	}
}

func TestMessagesFromTranscriptUsesLatestCompactSummary(t *testing.T) {
	messages := MessagesFromTranscript([]session.Entry{
		{Type: "message", Role: "user", Content: "old"},
		{Type: "compact_summary", Content: "summary"},
		{Type: "message", Role: "assistant", Content: "new"},
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	if !strings.Contains(messages[0].Content[0].Text, "summary") || messages[1].Content[0].Text != "new" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestMessagesFromTranscriptIgnoresRecapSummary(t *testing.T) {
	messages := MessagesFromTranscript([]session.Entry{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "recap_summary", Role: "system", Content: "本次会话目标：不应进入上下文"},
		{Type: "message", Role: "assistant", Content: "hello"},
	})
	if len(messages) != 2 {
		t.Fatalf("messages = %+v", messages)
	}
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "不应进入上下文") {
				t.Fatalf("recap leaked into messages: %+v", messages)
			}
		}
	}
}

func TestMessagesFromTranscriptRepairsInterruptedToolUse(t *testing.T) {
	messages := MessagesFromTranscript([]session.Entry{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "message", Role: "assistant", Content: "I will inspect"},
		{Type: "tool_call", ToolID: "toolu_missing", ToolName: "Read", Content: `{"file_path":"README.md"}`},
	})
	if len(messages) != 3 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[1].Role != "assistant" || len(messages[1].Content) != 2 || messages[1].Content[1].Type != "tool_use" {
		t.Fatalf("assistant content not merged with repaired tool_use: %+v", messages)
	}
	if messages[2].Role != "user" || len(messages[2].Content) != 2 {
		t.Fatalf("missing synthetic tool_result and continuation: %+v", messages)
	}
	if messages[2].Content[0].Type != "tool_result" || !messages[2].Content[0].IsError || messages[2].Content[0].ToolUseID != "toolu_missing" {
		t.Fatalf("synthetic tool_result = %+v", messages[2].Content[0])
	}
	if messages[2].Content[1].Text != resumeContinuePrompt {
		t.Fatalf("continuation = %+v", messages[2].Content[1])
	}
}

func TestMessagesFromTranscriptDropsOrphanedToolResult(t *testing.T) {
	messages := MessagesFromTranscript([]session.Entry{
		{Type: "message", Role: "user", Content: "hi"},
		{Type: "tool_result", ToolID: "toolu_orphan", ToolName: "Read", Content: "orphaned"},
		{Type: "tool_call", ToolID: "toolu_1", ToolName: "Read", Content: `{"file_path":"README.md"}`},
		{Type: "tool_result", ToolID: "toolu_1", ToolName: "Read", Content: "ok"},
	})
	if len(messages) != 3 || messages[1].Content[0].Type != "tool_use" || messages[2].Content[0].ToolUseID != "toolu_1" || messages[2].Content[1].Text != resumeContinuePrompt {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestMessagesFromTranscriptAddsNoResponseSentinelForTrailingUserPrompt(t *testing.T) {
	messages := MessagesFromTranscript([]session.Entry{
		{Type: "message", Role: "user", Content: "previous prompt"},
	})
	if len(messages) != 2 || messages[1].Role != "assistant" || messages[1].Content[0].Text != resumeNoResponseRequested {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestMessagesFromTranscriptPreservesNonTrailingThinking(t *testing.T) {
	messages, report := MessagesFromTranscriptWithReport([]session.Entry{
		{Type: "message", Role: "user", Content: "think"},
		{Type: "thinking", Role: "assistant", Content: "consider", Signature: "sig_123"},
		{Type: "message", Role: "assistant", Content: "done"},
	})
	if report.DroppedThinking != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(messages) != 2 || len(messages[1].Content) != 2 || messages[1].Content[0].Type != "thinking" || messages[1].Content[0].Signature != "sig_123" || messages[1].Content[1].Text != "done" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestMessagesFromTranscriptDropsTrailingThinking(t *testing.T) {
	messages, report := MessagesFromTranscriptWithReport([]session.Entry{
		{Type: "message", Role: "user", Content: "think"},
		{Type: "thinking", Role: "assistant", Content: "partial", Signature: "bad"},
	})
	if report.DroppedThinking != 1 || !report.Interrupted {
		t.Fatalf("report = %+v", report)
	}
	if len(messages) != 2 || messages[1].Role != "assistant" || messages[1].Content[0].Text != resumeNoResponseRequested {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestToolResultTruncation(t *testing.T) {
	got := truncateToolResult("abcdefghijklmnopqrstuvwxyz", 10)
	if !strings.Contains(got, "abcdefghij") || !strings.Contains(got, "truncated") {
		t.Fatalf("truncated result = %q", got)
	}
}

func TestSessionPersistsLargeToolResultPreview(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	content := strings.Repeat("large-output-", 500)
	streamer := &persistedToolResultStreamer{}
	sess := New(streamer, tools.NewRegistry(largeResultTool{content: content}), Options{
		Model:           "test",
		CWD:             project,
		Recorder:        recorder,
		MaxTurns:        2,
		ToolResultLimit: 100,
	})
	var out strings.Builder
	if _, err := sess.Run(context.Background(), "run long tool", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(streamer.toolResult, "<persisted-output>") || !strings.Contains(streamer.toolResult, "Full output saved to:") {
		t.Fatalf("tool result = %q", streamer.toolResult)
	}
	if strings.Contains(streamer.toolResult, content) {
		t.Fatalf("tool result contains full content")
	}
	path := filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_large.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("persisted content mismatch")
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Type == "tool_result" && strings.Contains(entry.Content, "<persisted-output>") {
			found = true
		}
	}
	if !found {
		t.Fatalf("persisted tool result not recorded: %+v", entries)
	}
}

func TestSessionPersistsLargeToolResultWithCapabilityLoopSummary(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "54545454-5454-4545-8545-545454545454")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	content := strings.Repeat("preview-only-prefix ", 180) + strings.Join([]string{
		"",
		"<capability_loop>",
		`{"status":"failed","capability_loop":{"evidence":["QUERY_PERSIST_EVIDENCE: parent request kept long tool capability evidence."],"assumptions":["tool_result was persisted before the next request"],"unknowns":["whether full output was inspected"],"verification":["go test ./internal/query -run TestSessionPersistsLargeToolResultWithCapabilityLoopSummary -count=1"],"risks":["long output tail could otherwise hide next_action"],"next_action":"QUERY_PERSIST_NEXT_ACTION: continue from preserved capability summary."}}`,
		"</capability_loop>",
	}, "\n")
	streamer := &persistedToolResultStreamer{}
	sess := New(streamer, tools.NewRegistry(largeResultTool{content: content}), Options{
		Model:           "test",
		CWD:             project,
		Recorder:        recorder,
		MaxTurns:        2,
		ToolResultLimit: 100,
	})
	var out strings.Builder
	if _, err := sess.Run(context.Background(), "run long capability tool", &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<persisted-output>",
		"Capability loop summary preserved from full output:",
		"status: failed",
		"evidence: QUERY_PERSIST_EVIDENCE: parent request kept long tool capability evidence.",
		"unknowns: whether full output was inspected",
		"verification: go test ./internal/query -run TestSessionPersistsLargeToolResultWithCapabilityLoopSummary -count=1",
		"risks: long output tail could otherwise hide next_action",
		"next_action: QUERY_PERSIST_NEXT_ACTION: continue from preserved capability summary.",
	} {
		if !strings.Contains(streamer.toolResult, want) {
			t.Fatalf("persisted request tool_result missing %q:\n%s", want, streamer.toolResult)
		}
	}
}

func TestSessionUsesToolSpecificResultLimit(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "58585858-5858-4585-8585-585858585858")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	content := strings.Repeat("tool-specific-output-", 200)
	streamer := &persistedToolResultStreamer{}
	sess := New(streamer, tools.NewRegistry(largeResultTool{content: content, maxResultSize: 10}), Options{
		Model:           "test",
		CWD:             project,
		Recorder:        recorder,
		MaxTurns:        2,
		ToolResultLimit: 1_000,
	})
	var out strings.Builder
	if _, err := sess.Run(context.Background(), "run long tool", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(streamer.toolResult, "<persisted-output>") {
		t.Fatalf("tool-specific limit was not applied: %.120q", streamer.toolResult)
	}
	if strings.Contains(streamer.toolResult, content) {
		t.Fatalf("tool result contains full content")
	}
}

func TestSessionAppliesToolResultMessageBudgetBeforeRequest(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "56565656-5656-4565-8565-565656565656")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	large := strings.Repeat("L", 9000)
	medium := strings.Repeat("M", 6000)
	small := strings.Repeat("S", 2000)
	streamer := &aggregateBudgetStreamer{large: large, medium: medium, small: small}
	sess := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                   "test",
		CWD:                     project,
		Recorder:                recorder,
		MaxTurns:                2,
		ToolResultLimit:         10_000,
		ToolResultMessageBudget: 12_000,
	})
	var out strings.Builder
	if _, err := sess.Run(context.Background(), "run tools", &out); err != nil {
		t.Fatal(err)
	}
	if len(streamer.toolResults) != 3 {
		t.Fatalf("tool results = %d", len(streamer.toolResults))
	}
	if !strings.Contains(streamer.toolResults[0], "<persisted-output>") {
		t.Fatalf("largest result was not persisted: %.120q", streamer.toolResults[0])
	}
	if streamer.toolResults[1] != medium || streamer.toolResults[2] != small {
		t.Fatalf("unexpected replacements")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != large {
		t.Fatalf("persisted content mismatch")
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var records []toolresult.ReplacementRecord
	for _, entry := range entries {
		if entry.Type == "content_replacement" {
			for _, replacement := range entry.Replacements {
				records = append(records, toolresult.ReplacementRecord{
					Kind:        replacement.Kind,
					ToolUseID:   replacement.ToolUseID,
					Replacement: replacement.Replacement,
				})
			}
		}
	}
	if len(records) != 1 || records[0].ToolUseID != "toolu_large" || records[0].Replacement != streamer.toolResults[0] {
		t.Fatalf("content replacement records = %+v", records)
	}
}

func TestSessionFreezesLiveToolResultAfterFirstBudgetPass(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "57575757-5757-4575-8575-575757575757")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	old := strings.Repeat("O", 9000)
	fresh := strings.Repeat("F", 7000)
	streamer := &historyBudgetStreamer{old: old, fresh: fresh}
	sess := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                   "test",
		CWD:                     project,
		Recorder:                recorder,
		MaxTurns:                3,
		ToolResultLimit:         10_000,
		ToolResultMessageBudget: 20_000,
		ToolResultHistoryBudget: 12_000,
	})
	var out strings.Builder
	if _, err := sess.Run(context.Background(), "run tools", &out); err != nil {
		t.Fatal(err)
	}
	if len(streamer.toolResults) != 2 {
		t.Fatalf("tool results = %d", len(streamer.toolResults))
	}
	if streamer.toolResults[0] != old {
		t.Fatalf("old result should stay frozen after first budget pass: %.120q", streamer.toolResults[0])
	}
	if streamer.toolResults[1] != fresh {
		t.Fatalf("fresh result changed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_old.txt")); !os.IsNotExist(err) {
		t.Fatalf("live seen old result should not be newly persisted, stat err=%v", err)
	}
}

func TestSessionSkipsReadResultsInToolResultMessageBudget(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "59595959-5959-4595-8595-595959595959")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	readContent := strings.Repeat("R", 30000)
	readPath := filepath.Join(project, "large-read.txt")
	if err := os.WriteFile(readPath, []byte(readContent), 0600); err != nil {
		t.Fatal(err)
	}
	echoOutput := strings.Repeat("E", 12000)
	streamer := &readBudgetSkipStreamer{filePath: readPath, echo: echoOutput}
	sess := New(streamer, tools.NewRegistry(fileread.New(), echoTool{}), Options{
		Model:                   "test",
		CWD:                     project,
		Recorder:                recorder,
		MaxTurns:                2,
		ToolResultLimit:         100_000,
		ToolResultMessageBudget: 10_000,
	})
	var out strings.Builder
	if _, err := sess.Run(context.Background(), "run read and echo", &out); err != nil {
		t.Fatal(err)
	}
	readResult := streamer.toolResults["toolu_read"]
	if !strings.Contains(readResult, readContent) {
		t.Fatalf("Read result should remain raw in request, got %.120q", readResult)
	}
	if strings.Contains(readResult, "<persisted-output>") {
		t.Fatalf("Read result should not be persisted by aggregate budget: %.120q", readResult)
	}
	echoResult := streamer.toolResults["toolu_echo"]
	if !strings.Contains(echoResult, "<persisted-output>") {
		t.Fatalf("eligible Echo result should be persisted: %.120q", echoResult)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_read.txt")); !os.IsNotExist(err) {
		t.Fatalf("Read output should not be persisted, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_echo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != echoOutput {
		t.Fatalf("persisted Echo content mismatch")
	}
}

func TestSessionSkipsReadResultsInIndividualToolResultBudget(t *testing.T) {
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "60606060-6060-4606-8606-606060606060")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	readContent := strings.Repeat("R", 60_000)
	readPath := filepath.Join(project, "large-read.txt")
	if err := os.WriteFile(readPath, []byte(readContent), 0600); err != nil {
		t.Fatal(err)
	}
	streamer := &readBudgetSkipStreamer{filePath: readPath, echo: "ok"}
	sess := New(streamer, tools.NewRegistry(fileread.New(), echoTool{}), Options{
		Model:           "test",
		CWD:             project,
		Recorder:        recorder,
		MaxTurns:        2,
		ToolResultLimit: 10_000,
	})
	if _, err := sess.Run(context.Background(), "read large file", io.Discard); err != nil {
		t.Fatal(err)
	}
	readResult := streamer.toolResults["toolu_read"]
	if !strings.Contains(readResult, readContent) {
		t.Fatalf("Read result should bypass individual result limit, got %.120q", readResult)
	}
	if strings.Contains(readResult, "<persisted-output>") || strings.Contains(readResult, "[Tool output truncated:") {
		t.Fatalf("Read result should not be persisted or truncated: %.120q", readResult)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_read.txt")); !os.IsNotExist(err) {
		t.Fatalf("Read output should not be persisted by individual limit, stat err=%v", err)
	}
}

func TestClaudeCompatibleReadToolResultCarriesLineNumbersToNextRequest(t *testing.T) {
	t.Setenv("NIUMA_PROMPT_PROFILE", "claude-compatible")
	project := t.TempDir()
	readPath := filepath.Join(project, "sample.txt")
	if err := os.WriteFile(readPath, []byte("alpha\nbeta\n"), 0600); err != nil {
		t.Fatal(err)
	}
	streamer := &readBudgetSkipStreamer{filePath: readPath, echo: "ok"}
	sess := New(streamer, tools.NewRegistry(fileread.New(), echoTool{}), Options{
		Model:    "test",
		CWD:      project,
		MaxTurns: 2,
	})
	if _, err := sess.Run(context.Background(), "read sample", io.Discard); err != nil {
		t.Fatal(err)
	}
	readResult := streamer.toolResults["toolu_read"]
	if !strings.Contains(readResult, "     1: alpha\n     2: beta") {
		t.Fatalf("compatible Read tool_result missing cat-n line numbers: %.200q", readResult)
	}
	if !strings.Contains(readResult, "Whenever you read a file") {
		t.Fatalf("compatible Read tool_result missing safety reminder: %.200q", readResult)
	}
}

func TestBashBackgroundResultCanDriveReadLogPath(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	project := t.TempDir()
	streamer := &bashBackgroundReadStreamer{}
	sess := New(streamer, tools.NewRegistry(bashtool.New(), fileread.New()), Options{
		Model:    "test",
		CWD:      project,
		MaxTurns: 3,
	})
	if _, err := sess.Run(context.Background(), "run background command and read its log", io.Discard); err != nil {
		t.Fatal(err)
	}
	if streamer.logPath == "" {
		t.Fatal("streamer did not parse bash log_path")
	}
	// log_path stays readable, but the payload now points the model at
	// BashOutput for polling (AUDIT-P1-13); Read replays the whole log.
	if !strings.Contains(streamer.bashResult, `"background":true`) || !strings.Contains(streamer.bashResult, "BashOutput") {
		t.Fatalf("bash tool_result did not expose background/read contract: %s", streamer.bashResult)
	}
	if !strings.Contains(streamer.readResult, "bg-ready") || !strings.Contains(streamer.readResult, "bg-done") {
		t.Fatalf("Read did not receive background log output: %.300q", streamer.readResult)
	}
}

func TestSessionReappliesTranscriptToolResultReplacement(t *testing.T) {
	original := strings.Repeat("R", 9000)
	replacement := "<persisted-output>\nresume preview\n</persisted-output>"
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "first"},
		{Type: "tool_call", ToolID: "toolu_resume", ToolName: "Echo", Content: `{"text":"large"}`},
		{Type: "tool_result", ToolID: "toolu_resume", ToolName: "Echo", Content: original},
		{Type: "content_replacement", Replacements: []session.ReplacementRecord{{
			Kind:        "tool-result",
			ToolUseID:   "toolu_resume",
			Replacement: replacement,
		}}},
	}
	streamer := &requestCapturingStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                         "test",
		CWD:                           t.TempDir(),
		MaxTurns:                      1,
		ToolResultMessageBudget:       20_000,
		InitialMessages:               MessagesFromTranscript(entries),
		InitialToolResultReplacements: ToolResultReplacementsFromTranscript(entries),
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "resume", &out); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("requests = %d", len(streamer.requests))
	}
	text := requestToolResultText(streamer.requests[0].Messages, "toolu_resume")
	if text != replacement {
		t.Fatalf("resumed request tool result = %.120q, want replacement", text)
	}
	if strings.Contains(text, original) {
		t.Fatalf("resumed request still contains raw original")
	}
}

func TestSessionFreezesTranscriptToolResultWithoutReplacement(t *testing.T) {
	original := strings.Repeat("F", 9000)
	entries := []session.Entry{
		{Type: "message", Role: "user", Content: "first"},
		{Type: "tool_call", ToolID: "toolu_frozen", ToolName: "Echo", Content: `{"text":"large"}`},
		{Type: "tool_result", ToolID: "toolu_frozen", ToolName: "Echo", Content: original},
	}
	project := t.TempDir()
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(project, "58585858-5858-4585-8585-585858585858")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	streamer := &requestCapturingStreamer{}
	querySession := New(streamer, tools.NewRegistry(echoTool{}), Options{
		Model:                         "test",
		CWD:                           project,
		MaxTurns:                      1,
		Recorder:                      recorder,
		ToolResultMessageBudget:       100,
		ToolResultHistoryBudget:       100,
		InitialMessages:               MessagesFromTranscript(entries),
		InitialToolResultReplacements: ToolResultReplacementsFromTranscript(entries),
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "resume", &out); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("requests = %d", len(streamer.requests))
	}
	text := requestToolResultText(streamer.requests[0].Messages, "toolu_frozen")
	if text != original {
		t.Fatalf("frozen resumed request tool result = %.120q, want original", text)
	}
	if strings.Contains(text, "<persisted-output>") {
		t.Fatalf("frozen resumed request was replaced: %.120q", text)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(recorder.Path), recorder.SessionID, "tool-results", "toolu_frozen.txt")); !os.IsNotExist(err) {
		t.Fatalf("frozen resumed result should not be newly persisted, stat err=%v", err)
	}
}

func requestToolResultText(messages []anthropic.MessageParam, toolUseID string) string {
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "tool_result" && block.ToolUseID == toolUseID {
				return block.Content
			}
		}
	}
	return ""
}

func parsePromptDumpRecords(t *testing.T, raw []byte) []promptDumpRecord {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	records := make([]promptDumpRecord, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record promptDumpRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal prompt dump record: %v\nline=%s", err, line)
		}
		records = append(records, record)
	}
	return records
}

func TestSessionGuidanceSectionMentionsSessionShow(t *testing.T) {
	// The runtime ships a `session show` CLI command; the system prompt must
	// tell the agent about it so it locates a transcript by session id in one
	// command instead of blindly searching the filesystem.
	got := sessionGuidanceSection([]string{"Bash", "Read"})
	if !strings.Contains(got, "session show") {
		t.Fatalf("session guidance section missing transcript-locating hint; got:\n%s", got)
	}
}

func TestSystemSectionsStateToolResultsHiddenFromUser(t *testing.T) {
	// Web/TUI 前端只展示每次工具调用的一行摘要，不展示原始工具输出。
	// 系统提示词必须告知模型这一契约，否则模型会以为用户能看到工具结果，
	// 只回一句"以上就是完整的 diff"而不把内容转述进回复文本。
	if !strings.Contains(toolResultVisibilityLine, "raw tool results") {
		t.Fatalf("toolResultVisibilityLine lost its core statement: %s", toolResultVisibilityLine)
	}
	sections := map[string]string{
		"simple":           simpleSystemSection(),
		"claudeCompatible": claudeCompatibleSystemSection(),
	}
	for name, section := range sections {
		if !strings.Contains(section, toolResultVisibilityLine) {
			t.Errorf("%s system section must state the user cannot see raw tool results; got:\n%s", name, section)
		}
	}
}

// TestGatePreflightOutputInstructsFullVerbatimResendWithBrake pins the "带刹车的 A"
// contract for the auto-preflight echo: after the runtime runs the read-only preflight
// (original command not executed), the reminder must (1) tell the model to re-send the
// ENTIRE original command verbatim without dropping any step — the fix for weak models
// that silently dropped `&& git push` on retry — while (2) keeping the inspection brake:
// proceed only if the preflight output is clean (e.g. remote has not diverged), otherwise
// resolve first and never blindly re-push past a divergence.
func TestGatePreflightOutputInstructsFullVerbatimResendWithBrake(t *testing.T) {
	gate := completionGateResult{
		RuleID:           "shared_state_git_push",
		PreflightTitle:   "推送前状态检查",
		PreflightSummary: "已完成推送前检查，原推送命令尚未执行",
		PreflightCommand: "git status --short --branch && git rev-parse HEAD && (git rev-parse @{u} || git branch -vv)",
	}
	original := "git add README.md && git commit -m bump && git push"
	out := gatePreflightToolOutput(gate, original, "## main...origin/main", false)
	lower := strings.ToLower(out)

	// Fix intact: full original command + stable marker are still present.
	if !strings.Contains(out, original) {
		t.Fatalf("preflight output dropped the original command:\n%s", out)
	}
	if !strings.Contains(out, GatePreflightMarker) {
		t.Fatalf("preflight output must keep the stable %q marker:\n%s", GatePreflightMarker, out)
	}

	// (1) Re-send the ENTIRE original verbatim, do not drop steps.
	for _, want := range []string{"re-send", "entire", "verbatim", "do not drop"} {
		if !strings.Contains(lower, want) {
			t.Errorf("reminder must instruct a full verbatim re-send (missing %q):\n%s", want, out)
		}
	}

	// (2) Brake preserved: inspect + divergence-aware, resolve-first, never blind re-push.
	for _, want := range []string{"inspect", "diverg", "pull --rebase", "never"} {
		if !strings.Contains(lower, want) {
			t.Errorf("reminder must keep the inspection/divergence brake (missing %q):\n%s", want, out)
		}
	}
}

// TestUnauthorizedDestructiveGitAlwaysHardBlocksNeverAutoPreflight pins the safety
// invariant that makes hardening canAutoPreflightGitCommand itself unnecessary:
// an unauthorized destructive shared-state git op (force-push, remote tag deletion)
// is caught by the destructive_shared_state gate — which runs BEFORE the push/tag
// rules — and is hard-blocked with an EMPTY PreflightCommand (never auto-preflighted).
// If a future edit reorders preToolGateRules or lets a destructive op reach the
// auto-preflight path, this test fails. The prompt intentionally does not authorize
// any destructive operation.
func TestUnauthorizedDestructiveGitAlwaysHardBlocksNeverAutoPreflight(t *testing.T) {
	const prompt = "把改动推送到远程"
	cases := []struct {
		name    string
		command string
	}{
		{"force push", "git push --force origin main"},
		{"force-with-lease push", "git push --force-with-lease origin main"},
		{"remote tag delete via refspec", "git push origin :refs/tags/v1.0.0"},
		{"remote tag delete via --delete", "git push --delete origin v1.0.0"},
		{"compound ending in force push", "git add . && git commit -m x && git push --force"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"command":%q}`, tc.command)
			gate := preToolClosureGate(taskContract{}, prompt, nil, "Bash", input)
			if gate.Severity != gateSeverityBlockTool {
				t.Fatalf("expected block_tool, got severity=%q (rule_id=%q)", gate.Severity, gate.RuleID)
			}
			if gate.RuleID != "destructive_shared_state" {
				t.Errorf("destructive op must be caught by destructive_shared_state gate before push/tag rules, got rule_id=%q", gate.RuleID)
			}
			if gate.PreflightCommand != "" {
				t.Errorf("destructive op must NEVER be auto-preflighted, got PreflightCommand=%q", gate.PreflightCommand)
			}
		})
	}
}

// The loop-awareness text tiers are now covered by
// internal/loopguard.TestAwarenessTiers, where the renderer lives.

func TestCurrentThinkingConfigUsesDefaultEffort(t *testing.T) {
	s := New(nil, tools.NewRegistry(), Options{})
	cfg := s.currentThinkingConfig()
	if cfg == nil || cfg.Effort != defaults.Effort || cfg.BudgetTokens != 4096 {
		t.Fatalf("cfg = %+v, want default effort %q with 4096 tokens", cfg, defaults.Effort)
	}
}

func TestCurrentThinkingConfigUsesSessionEffort(t *testing.T) {
	s := New(nil, tools.NewRegistry(), Options{Effort: "medium"})
	cfg := s.currentThinkingConfig()
	if cfg == nil || cfg.BudgetTokens != 2048 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestCurrentThinkingConfigSkillEffortWins(t *testing.T) {
	s := New(nil, tools.NewRegistry(), Options{Effort: "medium"})
	s.activeSkill = &tools.SkillRuntime{Effort: "high"}
	cfg := s.currentThinkingConfig()
	if cfg == nil || cfg.BudgetTokens != 4096 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestCurrentThinkingConfigSkillInheritFallsBackToSession(t *testing.T) {
	s := New(nil, tools.NewRegistry(), Options{Effort: "low"})
	s.activeSkill = &tools.SkillRuntime{Effort: "inherit"}
	cfg := s.currentThinkingConfig()
	if cfg == nil || cfg.BudgetTokens != 1024 {
		t.Fatalf("skill inherit should fall back to session effort, cfg = %+v", cfg)
	}
}

func TestCurrentThinkingConfigSkillOffForcesDisable(t *testing.T) {
	s := New(nil, tools.NewRegistry(), Options{Effort: "medium"})
	s.activeSkill = &tools.SkillRuntime{Effort: "off"}
	if cfg := s.currentThinkingConfig(); cfg != nil {
		t.Fatalf("skill off should disable thinking, got %+v", cfg)
	}
}

func TestCurrentThinkingConfigNilForSmallMaxTokensMetaCalls(t *testing.T) {
	// Server meta calls (title generation MaxTokens=32, next-step suggestions
	// MaxTokens=256) share newQuerySession with the user main loop and so
	// receive Options.Effort. They must never gain thinking: the budget clamp
	// in ThinkingConfigFromEffort (budget -> maxTokens-1, then <1024 -> nil)
	// is the guarantee this test pins.
	for _, maxTokens := range []int{32, 256, 1024} {
		for _, effort := range []string{"low", "medium", "high", "max", "8192"} {
			s := New(nil, tools.NewRegistry(), Options{Effort: effort, MaxTokens: maxTokens})
			if cfg := s.currentThinkingConfig(); cfg != nil {
				t.Fatalf("maxTokens=%d effort=%q: expected nil thinking config, got %+v", maxTokens, effort, cfg)
			}
		}
	}
}

func TestRecordUsageWritesReasoningTokensAndTurn(t *testing.T) {
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	s := New(nil, tools.NewRegistry(), Options{Model: "glm-5.1", Recorder: recorder})
	s.recordUsage(anthropic.Usage{InputTokens: 10, OutputTokens: 5, ReasoningOutputTokens: 3}, 2)
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Type != "usage" {
			continue
		}
		found = true
		if entry.ReasoningOutputTokens != 3 {
			t.Fatalf("reasoning tokens = %d", entry.ReasoningOutputTokens)
		}
		if entry.Turn != 2 {
			t.Fatalf("usage turn = %d, want 2", entry.Turn)
		}
	}
	if !found {
		t.Fatal("usage entry missing")
	}
}

func TestEnvInfoSectionIncludesSessionIdentity(t *testing.T) {
	out := envInfoSection("/tmp/w", "m", "abc-123", "/data/projects/x/abc-123.jsonl")
	for _, want := range []string{"Session ID: abc-123", "Session transcript: /data/projects/x/abc-123.jsonl"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	out = envInfoSection("/tmp/w", "m", "", "")
	for _, notWant := range []string{"Session ID:", "Session transcript:"} {
		if strings.Contains(out, notWant) {
			t.Fatalf("unexpected %q in:\n%s", notWant, out)
		}
	}
}

func TestCodeSystemPromptIncludesSessionIdentityWhenRecorderPresent(t *testing.T) {
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	s := New(nil, tools.NewRegistry(), Options{CWD: "/tmp/work", PromptMode: "code", Recorder: recorder})
	prompt := strings.Join(s.defaultSystemPromptParts(), "\n")
	if !strings.Contains(prompt, "Session ID: 33333333-3333-4333-8333-333333333333") {
		t.Fatalf("prompt missing session id:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Session transcript: "+recorder.Path) {
		t.Fatalf("prompt missing transcript path:\n%s", prompt)
	}
}

func TestInformationPrioritySectionCoversRuleScope(t *testing.T) {
	section := informationPrioritySection()
	for _, want := range []string{"scoped by that intent", "do not silently pick one", "state which you are following"} {
		if !strings.Contains(section, want) {
			t.Fatalf("information priority section missing %q:\n%s", want, section)
		}
	}
}

func TestCompatibleClaudeMdHeaderRequiresConflictSurfacing(t *testing.T) {
	msg := compatibleWorkspaceGuidanceUserMessage("some project instructions")
	if len(msg.Content) == 0 {
		t.Fatal("empty message")
	}
	if !strings.Contains(msg.Content[0].Text, "surface the conflict explicitly") {
		t.Fatalf("claudeMd header missing conflict clause:\n%s", msg.Content[0].Text)
	}
}

func TestToneSectionCoversAntiSycophancy(t *testing.T) {
	section := toneAndStyleSection()
	for _, want := range []string{
		"do not capitulate by default",
		"including any instruction, rule, or evidence that drove it",
		"Never invent psychological explanations",
		"Agreement must carry evidence",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("tone section missing %q:\n%s", want, section)
		}
	}
}
