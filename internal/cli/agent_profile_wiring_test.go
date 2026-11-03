package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestApplyAgentProfileToOptionsPreservesExplicitRuntimeContract(t *testing.T) {
	effective := agentprofile.EffectiveProfile{
		Profile: agentprofile.Profile{Key: agentprofile.ProfileCoder, Version: 3},
		Config: agentprofile.ProfileDocument{
			Prompt:    agentprofile.PromptPolicy{Mode: agentprofile.PromptModeCode, Language: "zh-CN", SystemAddendum: "verify before final"},
			Execution: agentprofile.ExecutionPolicy{RuntimeProfile: "default", Model: "coder-model", Provider: "primary", Effort: "high", MaxTurns: 12, MaxTokens: 4096, MaxParallelReadOnlyTools: 2},
			Capabilities: agentprofile.CapabilityPolicy{
				Tools:  agentprofile.ToolPolicy{Allow: []string{"Read", "Edit"}, Deny: []string{"Bash"}},
				Skills: []string{"profile-skill", "shared-skill"},
			},
		},
		Source: "explicit", ProfileKey: agentprofile.ProfileCoder, ProfileVersion: 3,
		RequestedHash: "requested", EffectiveHash: "effective", BlockedOverrides: []agentprofile.BlockedOverride{{Field: "execution.max_tokens"}},
	}
	opts := options{promptMode: "chat"}
	if err := applyAgentProfileToOptions(&opts, effective); err != nil {
		t.Fatalf("apply profile: %v", err)
	}
	if opts.promptMode != "code" || opts.runtimeProfile != runtimeprofile.ProfileDefault || opts.model != "coder-model" || opts.providerName != "primary" {
		t.Fatalf("runtime options = %+v", opts)
	}
	if opts.maxTurns != 12 || opts.maxTokens != 4096 || opts.maxParallelReadOnlyTools != 2 || opts.effort != "high" {
		t.Fatalf("budget options = %+v", opts)
	}
	if !opts.toolsSpecified || len(opts.enabledTools) != 2 || len(opts.deniedTools) != 1 || opts.systemPrompt != "" || opts.appendSystem != "verify before final" {
		t.Fatalf("tool/prompt options = %+v", opts)
	}
	if opts.agentProfileKey != agentprofile.ProfileCoder || opts.agentProfileVersion != 3 || opts.agentProfileEffectiveHash != "effective" || opts.agentProfileBlockedOverrides != 1 {
		t.Fatalf("profile metadata = %+v", opts)
	}
	if got := strings.Join(opts.inlineTenantSkills, ","); got != "profile-skill,shared-skill" {
		t.Fatalf("profile skills = %q", got)
	}
}

func TestApplyAgentProfileToOptionsMergesExplicitSkillsWithoutDuplicates(t *testing.T) {
	profile := agentprofile.Profile{Key: agentprofile.ProfileChatAssistant, Version: 1}
	profile.Config.Capabilities.Skills = []string{"profile-skill", "shared-skill"}
	opts := options{inlineTenantSkills: []string{"shared-skill", "request-skill"}}

	if err := applyAgentProfileToOptions(&opts, agentprofile.EffectiveProfile{
		Profile: profile,
		Config:  profile.Config,
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(opts.inlineTenantSkills, ","); got != "shared-skill,request-skill,profile-skill" {
		t.Fatalf("merged skills = %q", got)
	}
}

func TestWebUIV2OrchestratorProfileEnablesSessionToolCandidateOnly(t *testing.T) {
	profile := agentprofile.BuiltinProfiles()[agentprofile.ProfileWebUIV2Orchestrator]
	opts := options{}
	if err := applyAgentProfileToOptions(&opts, agentprofile.EffectiveProfile{Profile: profile, Config: profile.Config, ProfileKey: profile.Key}); err != nil {
		t.Fatal(err)
	}
	if !opts.sessionControlProfile {
		t.Fatal("orchestrator profile did not enable session tool candidate")
	}

	normal := agentprofile.BuiltinProfiles()[agentprofile.ProfileChatAssistant]
	if err := applyAgentProfileToOptions(&opts, agentprofile.EffectiveProfile{Profile: normal, Config: normal.Config, ProfileKey: normal.Key}); err != nil {
		t.Fatal(err)
	}
	if opts.sessionControlProfile {
		t.Fatal("normal profile enabled session tool candidate")
	}
}

func TestSessionControlToolRegistrationRequiresProfileIdentityRuntimeAndService(t *testing.T) {
	service := cliSessionControlFake{}
	for name, opts := range map[string]options{
		"normal_profile":   {sessionControlService: &service, tenantID: 7, tenantUserID: 11},
		"missing_service":  {sessionControlProfile: true, tenantID: 7, tenantUserID: 11},
		"missing_identity": {sessionControlProfile: true, sessionControlService: &service},
	} {
		if got := sessionControlToolsForOptions(opts); len(got) != 0 {
			t.Fatalf("%s registered %d session tools", name, len(got))
		}
	}
	got := sessionControlToolsForOptions(options{sessionControlProfile: true, sessionControlService: &service, tenantID: 7, tenantUserID: 11})
	if len(got) != 7 {
		t.Fatalf("orchestrator registered %d session tools, want 7", len(got))
	}
}

func TestNormalProfilesKeepTheSameCoreToolDefinitions(t *testing.T) {
	service := cliSessionControlFake{}
	baseline := coreRuntimeToolsWithOptions(config.Settings{}, nil, "test-model", options{})
	normal := coreRuntimeToolsWithOptions(config.Settings{}, nil, "test-model", options{sessionControlService: &service, tenantID: 7, tenantUserID: 11})
	baselineNames := sessionControlTestToolNames(baseline)
	normalNames := sessionControlTestToolNames(normal)
	if !reflect.DeepEqual(normalNames, baselineNames) {
		t.Fatalf("normal profile tool definitions changed:\n got %v\nwant %v", normalNames, baselineNames)
	}
}

func sessionControlTestToolNames(items []tools.Tool) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name())
	}
	return names
}

func TestNonTextProfileCannotSilentlyEnterTextRoute(t *testing.T) {
	for _, kind := range []agentprofile.ExecutionKind{agentprofile.ExecutionKindComputer, agentprofile.ExecutionKindImage} {
		config := agentprofile.ProfileDocument{SchemaVersion: agentprofile.SchemaVersionV2, Execution: agentprofile.ExecutionPolicy{Kind: kind}}
		if err := applyAgentProfileToOptions(&options{}, agentprofile.EffectiveProfile{Config: config}); err == nil {
			t.Fatalf("kind %q entered text runtime", kind)
		}
	}
}
