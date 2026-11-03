package query

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

const confirmedGitWorkflowProposal = `git commit 被安全策略拦截了。当前需要执行：

` + "```bash" + `
git commit --author="konglong87 <>" -m "docs: add revisit reference"
git push origin master
` + "```" + `

你确认授权我执行这两条命令吗？`

func TestResolveContinuationIntentRecoversConfirmedGitWorkflow(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{
			Type: blockTypeText,
			Text: confirmedGitWorkflowProposal,
		}},
	}}

	intent := resolveContinuationIntent("执行", history)
	if !intent.Active || intent.Decision != "confirmed_pending_shared_state" {
		t.Fatalf("intent = %+v, want confirmed shared-state continuation", intent)
	}
	authorization := gitpolicy.AuthorizationFromEffects(intent.SharedStateEffects)
	for _, command := range []string{
		`git commit --author="konglong87 <>" -m "docs: add revisit reference"`,
		"git push origin master",
	} {
		if violation := gitpolicy.Check(authorization, gitpolicy.Analyze(command)); violation != nil {
			t.Fatalf("confirmed command %q was not authorized: %+v intent=%+v", command, violation, intent)
		}
	}
	if violation := gitpolicy.Check(authorization, gitpolicy.Analyze("git push backup dev")); violation == nil {
		t.Fatalf("confirmed origin/master workflow broadened to backup/dev: %+v", authorization)
	}

	contract := continuationTaskContract(buildTaskContract("执行"), intent)
	if contract.ClosureLevel != closureLevelSharedStateChange {
		t.Fatalf("contract = %+v, want shared-state closure", contract)
	}
	if !reflectSharedStateOperations(contract.SharedStateOperations, []sharedStateOperation{sharedStateCommit, sharedStatePush}) {
		t.Fatalf("operations = %+v, want commit+push", contract.SharedStateOperations)
	}
}

func TestResolveContinuationIntentRecoversInlineGitWorkflowWithAuthorization(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: `commit 被权限拦截了。需要你授权，我执行这两步：

1. ` + "`git add skills/01-demand-insight/pm-search/SKILL.md && git commit -m \"fix: pm-search route\"`" + `
2. ` + "`git push`" + `

授权后我立即执行。`}},
	}}

	intent := resolveContinuationIntent("授权", history)
	if !intent.Active || intent.Decision != "confirmed_pending_shared_state" {
		t.Fatalf("intent = %+v, want confirmed shared-state continuation", intent)
	}
	if len(intent.SharedStateEffects) != 2 {
		t.Fatalf("effects = %+v, want commit and push effects", intent.SharedStateEffects)
	}
	authorization := gitpolicy.AuthorizationFromEffects(intent.SharedStateEffects)
	for _, command := range []string{
		`git commit -m "fix: pm-search route"`,
		"git push",
	} {
		if violation := gitpolicy.Check(authorization, gitpolicy.Analyze(command)); violation != nil {
			t.Fatalf("confirmed command %q was not authorized: %+v", command, violation)
		}
	}
}

func TestResolveContinuationIntentDoesNotAuthorizeUnconfirmedGitExample(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: `For reference, this command would publish main:

` + "```bash" + `
git push origin main
` + "```"}},
	}}

	intent := resolveContinuationIntent("执行", history)
	if len(intent.SharedStateEffects) != 0 {
		t.Fatalf("plain example minted shared-state authorization: %+v", intent)
	}
}

func TestResolveContinuationIntentDoesNotAuthorizeInlineGitExample(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role:    "assistant",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "For reference, `git push origin main` publishes the current branch."}},
	}}

	intent := resolveContinuationIntent("授权", history)
	if len(intent.SharedStateEffects) != 0 {
		t.Fatalf("plain inline example minted shared-state authorization: %+v", intent)
	}
}

func TestResolveContinuationIntentRequiresLatestExplicitAuthorizationRequest(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		history []anthropic.MessageParam
	}{
		{
			name:    "no authorization challenge",
			prompt:  "执行",
			history: []anthropic.MessageParam{{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "Run this example:\n```bash\ngit push origin main\n```"}}}},
		},
		{
			name:   "older request is stale",
			prompt: "执行",
			history: []anthropic.MessageParam{
				{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}}},
				{Role: "user", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "先等等"}}},
				{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "好的，我先不执行。"}}},
			},
		},
		{
			name:    "question is not confirmation",
			prompt:  "执行吗？",
			history: []anthropic.MessageParam{{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}}}},
		},
		{
			name:    "modification is not confirmation",
			prompt:  "执行，但改成 push dev",
			history: []anthropic.MessageParam{{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}}}},
		},
		{
			name:    "non shell fence is not executable",
			prompt:  "执行",
			history: []anthropic.MessageParam{{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "```text\ngit push origin main\n```\n你确认授权我执行吗？"}}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			intent := resolveContinuationIntent(tc.prompt, tc.history)
			if len(intent.SharedStateEffects) != 0 {
				t.Fatalf("unconfirmed input minted authorization: %+v", intent)
			}
		})
	}
}

func TestResolveContinuationIntentAuthorizesOnlyFenceNearestRequest(t *testing.T) {
	history := []anthropic.MessageParam{{
		Role: "assistant",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: `Unrelated example:
` + "```bash" + `
git push backup dev
` + "```" + `

The requested workflow is:
` + "```bash" + `
git push origin main
` + "```" + `
Do you authorize me to run that command?`}},
	}}
	intent := resolveContinuationIntent("yes", history)
	authorization := gitpolicy.AuthorizationFromEffects(intent.SharedStateEffects)
	if violation := gitpolicy.Check(authorization, gitpolicy.Analyze("git push origin main")); violation != nil {
		t.Fatalf("nearest confirmed fence denied: %+v", violation)
	}
	if violation := gitpolicy.Check(authorization, gitpolicy.Analyze("git push backup dev")); violation == nil {
		t.Fatal("unrelated example fence inherited authorization")
	}
}

func TestContinuationAuthorizationGrantConsumesEachEffectOnce(t *testing.T) {
	intent := resolveContinuationIntent("执行", []anthropic.MessageParam{{
		Role:    "assistant",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}},
	}})
	grant := newContinuationAuthorizationGrant(intent.SharedStateEffects)
	direct := gitpolicy.Authorization{}

	commit := gitpolicy.Analyze(`git commit --author="konglong87 <>" -m "docs: add revisit reference"`)
	commitAuthorization, commitKeys := grant.authorizationFor(direct, commit)
	if violation := gitpolicy.Check(commitAuthorization, commit); violation != nil {
		t.Fatalf("first confirmed commit denied: %+v", violation)
	}
	grant.consume(commitKeys)
	if violation := gitpolicy.Check(grant.authorizationForOnly(direct, commit), commit); violation == nil {
		t.Fatal("confirmed commit authorization was reusable after consumption")
	}

	push := gitpolicy.Analyze("git push origin master")
	pushAuthorization, pushKeys := grant.authorizationFor(direct, push)
	if violation := gitpolicy.Check(pushAuthorization, push); violation != nil {
		t.Fatalf("commit consumption also consumed push: %+v", violation)
	}
	grant.consume(pushKeys)
	if violation := gitpolicy.Check(grant.authorizationForOnly(direct, push), push); violation == nil {
		t.Fatal("confirmed push authorization was reusable after consumption")
	}
}

func TestContinuationAuthorizationGrantRejectsDuplicateEffectBeyondCount(t *testing.T) {
	effect := gitpolicy.Analyze(`git commit -m "one"`).Effects[0]
	grant := newContinuationAuthorizationGrant([]gitpolicy.Effect{effect})
	duplicate := gitpolicy.Analyze(`git commit -m "one" && git commit -m "one"`)
	authorization, keys := grant.authorizationFor(gitpolicy.Authorization{}, duplicate)
	if len(keys) != 0 {
		t.Fatalf("insufficient grant returned consumable keys: %+v", keys)
	}
	if violation := gitpolicy.Check(authorization, duplicate); violation == nil {
		t.Fatal("one confirmed effect authorized two identical executions")
	}
}

func TestConfirmedAuthorizationCannotBeBroadenedByHookRewrite(t *testing.T) {
	effect := gitpolicy.Analyze(`git commit --author="konglong87 <>" -m "docs: exact"`).Effects[0]
	grant := newContinuationAuthorizationGrant([]gitpolicy.Effect{effect})
	analysis := gitpolicy.Analysis{Effects: []gitpolicy.Effect{effect}}
	authorization, keys := grant.authorizationFor(gitpolicy.Authorization{}, analysis)
	grant.consume(keys)

	bash := &closureBashTool{}
	session := New(&usageStreamer{}, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.PreToolUse: {{Command: `printf '%s' '{"updatedInput":{"command":"git commit --author=\"konglong87 <old@example.com>\" -m \"docs: changed\""}}'`}},
		}),
	})
	block := anthropic.ContentBlock{
		ID:    "toolu_confirmed_hook_rewrite",
		Name:  "Bash",
		Input: json.RawMessage(`{"command":"git commit --author=\"konglong87 <>\" -m \"docs: exact\""}`),
	}
	trace := session.runToolWithAuthorization(context.Background(), tools.NewRegistry(bash), block, runCallbacks{}, authorization)
	if !trace.IsError || len(bash.commands) != 0 || !strings.Contains(trace.Output, "Shared-State Authorization Gate") {
		t.Fatalf("hook broadened confirmed scope: trace=%+v commands=%+v", trace, bash.commands)
	}
}

type confirmedGitWorkflowStreamer struct {
	requests []anthropic.MessagesRequest
}

type confirmedCommitFirstStreamer struct{}

func (s *confirmedCommitFirstStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  blockTypeToolUse,
			ID:    "toolu_confirmed_commit_first",
			Name:  "Bash",
			Input: json.RawMessage(`{"command":"git commit --author=\"konglong87 <>\" -m \"docs: add revisit reference\""}`),
		}}},
		StopReason: "tool_use",
	}, nil
}

func (s *confirmedGitWorkflowStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	commands := []string{
		"git status --short --branch && git diff --name-status && git diff --cached --name-status",
		`git commit --author="konglong87 <>" -m "docs: add revisit reference"`,
		"git status --short --branch && git rev-parse HEAD && git rev-parse @{u} && git branch -vv",
		"git push origin master",
		"git status --short --branch && git rev-parse HEAD && git rev-parse @{u} && git diff --name-status && git diff --summary && test -f README.md",
	}
	if len(s.requests) <= len(commands) {
		input, _ := json.Marshal(map[string]string{
			"command":     commands[len(s.requests)-1],
			"description": "confirmed workflow step",
		})
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  blockTypeToolUse,
				ID:    "toolu_confirmed_" + string(rune('0'+len(s.requests))),
				Name:  "Bash",
				Input: input,
			}}},
			StopReason: "tool_use",
		}, nil
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: "done"}}},
		StopReason: "end_turn",
	}, nil
}

func TestConfirmedGitWorkflowExecutesCommitAndPushEndToEnd(t *testing.T) {
	streamer := &confirmedGitWorkflowStreamer{}
	bash := &closureBashTool{}
	session := New(streamer, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 6,
		CWD:      t.TempDir(),
		InitialMessages: []anthropic.MessageParam{{
			Role:    "assistant",
			Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}},
		}},
	})

	result, err := session.Run(context.Background(), "执行", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" || len(bash.commands) != 5 {
		t.Fatalf("result=%+v commands=%+v", result, bash.commands)
	}
	if !strings.Contains(bash.commands[1], "git commit") || !strings.Contains(bash.commands[3], "git push origin master") {
		t.Fatalf("confirmed shared-state effects did not execute: %+v", bash.commands)
	}
	for _, trace := range result.ToolCalls {
		if trace.IsError && strings.Contains(trace.Output, "Shared-State Authorization Gate") {
			t.Fatalf("confirmed workflow was authorization-blocked: %+v", trace)
		}
	}
}

func TestNormalPermissionModeChatAuthorizationStillExecutesExactWorkflow(t *testing.T) {
	streamer := &confirmedGitWorkflowStreamer{}
	bash := &closureBashTool{}
	session := New(streamer, tools.NewRegistry(bash), Options{
		Model:                 "test",
		MaxTurns:              6,
		CWD:                   t.TempDir(),
		RuntimePermissionMode: func() string { return "ask" },
		InitialMessages: []anthropic.MessageParam{{
			Role:    "assistant",
			Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}},
		}},
	})

	result, err := session.Run(context.Background(), "授权", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" || len(bash.commands) != 5 {
		t.Fatalf("result=%+v commands=%+v", result, bash.commands)
	}
	for _, trace := range result.ToolCalls {
		if trace.IsError && strings.Contains(trace.Output, "Shared-State Authorization Gate") {
			t.Fatalf("normal chat authorization was blocked: %+v", trace)
		}
	}
}

func TestConfirmedGrantIsNotPassedToGeneratedPreflight(t *testing.T) {
	bash := &closureBashTool{}
	session := New(&confirmedCommitFirstStreamer{}, tools.NewRegistry(bash), Options{
		Model:    "test",
		MaxTurns: 1,
		CWD:      t.TempDir(),
		InitialMessages: []anthropic.MessageParam{{
			Role:    "assistant",
			Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: confirmedGitWorkflowProposal}},
		}},
		Hooks: hooks.New(map[string][]config.HookCommand{
			hooks.PreToolUse: {{Command: `printf '%s' '{"updatedInput":{"command":"git commit --author=\"konglong87 <>\" -m \"docs: add revisit reference\""}}'`}},
		}),
	})
	result, err := session.Run(context.Background(), "执行", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "max turns reached") {
		t.Fatalf("err=%v result=%+v, want run to stop after blocked preflight", err, result)
	}
	if len(bash.commands) != 0 {
		t.Fatalf("hook-rewritten preflight consumed confirmed grant: %+v", bash.commands)
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, "Shared-State Authorization Gate") {
		t.Fatalf("preflight rewrite was not blocked by direct-only authorization: %+v", result.ToolCalls)
	}
}

type changingDescriptionCommitLoopStreamer struct {
	requests int
}

func (s *changingDescriptionCommitLoopStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests++
	input, _ := json.Marshal(map[string]any{
		"command":                   `git commit -m "same effect"`,
		"description":               "attempt " + string(rune('0'+s.requests)),
		"dangerouslyDisableSandbox": s.requests%2 == 0,
	})
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  blockTypeToolUse,
			ID:    "toolu_retry_" + string(rune('0'+s.requests)),
			Name:  "Bash",
			Input: input,
		}}},
		StopReason: "tool_use",
	}, nil
}

func TestSharedStateGateRetryStopsDescriptionVariantLoop(t *testing.T) {
	streamer := &changingDescriptionCommitLoopStreamer{}
	bash := &closureBashTool{}
	recorder, err := (session.Store{Root: t.TempDir()}).NewRecorderWithID(t.TempDir(), "34343434-3434-4434-8434-343434343434")
	if err != nil {
		t.Fatal(err)
	}
	querySession := New(streamer, tools.NewRegistry(bash), Options{Model: "test", MaxTurns: 20, CWD: t.TempDir(), Recorder: recorder})

	result, err := querySession.Run(context.Background(), "Inspect the repository only.", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "shared-state gate retry") {
		t.Fatalf("err=%v result=%+v, want shared-state retry abort", err, result)
	}
	if result.StopReason != "shared_state_gate_retry_abort" {
		t.Fatalf("StopReason=%q, want shared_state_gate_retry_abort", result.StopReason)
	}
	if streamer.requests != sharedStateGateRetryLimit {
		t.Fatalf("requests=%d, want %d", streamer.requests, sharedStateGateRetryLimit)
	}
	if len(bash.commands) != 0 {
		t.Fatalf("blocked commit unexpectedly executed: %+v", bash.commands)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	toolCalls := map[string]int{}
	toolResults := map[string]int{}
	foundAbortEvent := false
	for _, entry := range entries {
		switch entry.Type {
		case "tool_call":
			toolCalls[entry.ToolID]++
		case blockTypeToolResult:
			toolResults[entry.ToolID]++
		case "shared_state_gate_retry":
			foundAbortEvent = strings.Contains(entry.Content, `"rule_id":"shared_state_authorization"`) &&
				strings.Contains(entry.Content, `"repeat":3`) && strings.Contains(entry.Content, `"hard_limit":3`) &&
				strings.Contains(entry.Content, `"effect_fingerprint":"`)
		}
	}
	if !foundAbortEvent {
		t.Fatalf("transcript missing structured shared-state retry abort: %+v", entries)
	}
	for toolID, count := range toolCalls {
		if toolResults[toolID] != count {
			t.Fatalf("tool %s calls=%d results=%d; abort left transcript unpaired", toolID, count, toolResults[toolID])
		}
	}
}

func TestSharedStateGateRetryAbortsOnThirdIdenticalBlock(t *testing.T) {
	tracker := newSharedStateGateRetryTracker()
	analysis := gitpolicy.Analyze(`git commit -m "same effect"`)
	for attempt := 1; attempt <= 3; attempt++ {
		count, _, abort := tracker.observe(string(gitpolicy.ViolationSharedState), analysis)
		if count != attempt {
			t.Fatalf("attempt=%d count=%d", attempt, count)
		}
		if abort != (attempt == 3) {
			t.Fatalf("attempt=%d abort=%v, want abort only on third block", attempt, abort)
		}
	}
}

func TestDangerousPermissionModeApprovesExactSharedStateEffectWithoutChatAuthorization(t *testing.T) {
	streamer := &confirmedCommitFirstStreamer{}
	bash := &closureBashTool{}
	prompted := 0
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   t.TempDir(),
		RuntimePermissionMode: func() string { return "allow" },
		PermissionPrompt: func(_ context.Context, req tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			prompted++
			if req.ToolName != "Bash" || !strings.Contains(req.Reason, "Shared-State Authorization Gate") {
				t.Fatalf("unexpected prompt: %+v", req)
			}
			return tools.PermissionPromptResponse{Allowed: true, Destination: "once", Reason: "approved in dangerous mode"}
		},
	})

	result, err := querySession.Run(context.Background(), "Inspect the repository only.", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "max turns reached") {
		t.Fatalf("err=%v result=%+v, want one approved tool turn", err, result)
	}
	if prompted != 1 || len(bash.commands) != 1 || bash.commands[0] != preCommitScopePreflightCommand {
		t.Fatalf("prompted=%d commands=%+v", prompted, bash.commands)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, `<gate-preflight rule_id="pre_commit_scope"`) {
		t.Fatalf("approved shared-state tool did not advance to preflight: %+v", result.ToolCalls)
	}
}

func TestNormalPermissionModeStillRequiresChatAuthorization(t *testing.T) {
	streamer := &confirmedCommitFirstStreamer{}
	bash := &closureBashTool{}
	prompted := 0
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   t.TempDir(),
		RuntimePermissionMode: func() string { return "ask" },
		PermissionPrompt: func(_ context.Context, _ tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			prompted++
			return tools.PermissionPromptResponse{Allowed: true, Destination: "once"}
		},
	})

	result, err := querySession.Run(context.Background(), "Inspect the repository only.", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "max turns reached") {
		t.Fatalf("err=%v result=%+v, want blocked tool turn", err, result)
	}
	if prompted != 0 || len(bash.commands) != 0 {
		t.Fatalf("normal mode bypassed chat authorization: prompted=%d commands=%+v", prompted, bash.commands)
	}
	if len(result.ToolCalls) != 1 || !result.ToolCalls[0].IsError || !strings.Contains(result.ToolCalls[0].Output, "Shared-State Authorization Gate") {
		t.Fatalf("normal mode did not return authorization challenge: %+v", result.ToolCalls)
	}
}

func TestDangerousPermissionModeDoesNotApproveDestructiveSharedStateEffect(t *testing.T) {
	streamer := &forcePushBlockedStreamer{}
	bash := &closureBashTool{}
	prompted := 0
	querySession := New(streamer, tools.NewRegistry(bash), Options{
		Model:                 "test",
		MaxTurns:              1,
		CWD:                   t.TempDir(),
		RuntimePermissionMode: func() string { return "bypassPermissions" },
		PermissionPrompt: func(_ context.Context, _ tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			prompted++
			return tools.PermissionPromptResponse{Allowed: true, Destination: "once"}
		},
	})

	result, err := querySession.Run(context.Background(), "Push current branch.", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "max turns reached") {
		t.Fatalf("err=%v result=%+v, want destructive effect blocked", err, result)
	}
	if prompted != 0 || len(bash.commands) != 0 {
		t.Fatalf("dangerous mode approved destructive effect: prompted=%d commands=%+v", prompted, bash.commands)
	}
	if len(result.ToolCalls) != 1 || !strings.Contains(result.ToolCalls[0].Output, "Destructive Shared-State Gate") {
		t.Fatalf("destructive effect missing hard block: %+v", result.ToolCalls)
	}
}

func reflectSharedStateOperations(got, want []sharedStateOperation) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
