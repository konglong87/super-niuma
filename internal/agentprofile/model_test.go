package agentprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProfileDocumentCanonicalHashNormalizesSetFields(t *testing.T) {
	first := ProfileDocument{
		SchemaVersion: SchemaVersionV1,
		Prompt:        PromptPolicy{Mode: PromptModeChat, Language: "zh-CN"},
		Capabilities: CapabilityPolicy{
			Tools:      ToolPolicy{Allow: []string{"WebSearch", "Read", "Read"}, Deny: []string{"Bash"}},
			Skills:     []string{"copywriting", "copywriting"},
			MCPServers: []string{"search", "docs"},
		},
	}
	second := ProfileDocument{
		SchemaVersion: SchemaVersionV1,
		Prompt:        PromptPolicy{Mode: PromptModeChat, Language: "zh-CN"},
		Capabilities: CapabilityPolicy{
			Tools:      ToolPolicy{Allow: []string{"Read", "WebSearch"}, Deny: []string{"Bash"}},
			Skills:     []string{"copywriting"},
			MCPServers: []string{"docs", "search"},
		},
	}

	firstHash, err := first.CanonicalHash()
	if err != nil {
		t.Fatalf("first canonical hash: %v", err)
	}
	secondHash, err := second.CanonicalHash()
	if err != nil {
		t.Fatalf("second canonical hash: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent profiles have different hashes: %s != %s", firstHash, secondHash)
	}

	canonical, err := first.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical json: %v", err)
	}
	if strings.Contains(string(canonical), "  ") || !json.Valid(canonical) {
		t.Fatalf("canonical json is not compact valid JSON: %s", canonical)
	}
}

func TestDecodeProfileDocumentRejectsUnknownFields(t *testing.T) {
	_, err := DecodeProfileDocument([]byte(`{"schema_version":1,"prompt":{"mode":"chat"},"future":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeProfileDocument error = %v, want unknown field error", err)
	}
}

func TestValidateProfileDocumentRejectsUnsafeChatContextAndBypass(t *testing.T) {
	doc := ProfileDocument{
		SchemaVersion: SchemaVersionV1,
		Prompt:        PromptPolicy{Mode: PromptModeChat, SystemAddendum: strings.Repeat("x", MaxSystemAddendumBytes+1)},
		Context:       ContextPolicy{Workspace: true, Git: true},
		Safety:        SafetyPolicy{PermissionMode: PermissionModeBypass, AllowUnsandboxedCommands: true},
	}

	report := Validate(doc, ValidationContext{})
	if report.Valid {
		t.Fatal("unsafe chat profile unexpectedly validated")
	}
	for _, code := range []string{IssueChatWorkspace, IssueChatGit, IssueAddendumTooLarge, IssueBypassPermission, IssueUnsandboxedCommands} {
		if !report.Has(code) {
			t.Fatalf("validation report missing issue %q: %+v", code, report.Issues)
		}
	}
}

func TestValidateProfileDocumentAppliesCatalogAndBudgetLimits(t *testing.T) {
	doc := ProfileDocument{
		SchemaVersion: SchemaVersionV1,
		Prompt:        PromptPolicy{Mode: PromptModeCode},
		Capabilities: CapabilityPolicy{
			Tools:      ToolPolicy{Allow: []string{"Read", "NotRegistered"}},
			Skills:     []string{"missing-skill"},
			MCPServers: []string{"missing-mcp"},
		},
		Execution: ExecutionPolicy{MaxTurns: 9, MaxTokens: 9000, MaxParallelReadOnlyTools: 8},
	}
	report := Validate(doc, ValidationContext{
		AllowedTools:       StringSet{"Read": {}},
		AllowedSkills:      StringSet{},
		AllowedMCPServers:  StringSet{},
		MaxTurns:           3,
		MaxTokens:          2048,
		MaxParallelWorkers: 2,
	})
	if report.Valid {
		t.Fatal("catalog and budget violating profile unexpectedly validated")
	}
	for _, code := range []string{IssueToolNotAllowed, IssueSkillNotAllowed, IssueMCPNotAllowed, IssueMaxTurns, IssueMaxTokens, IssueMaxParallel} {
		if !report.Has(code) {
			t.Fatalf("validation report missing issue %q: %+v", code, report.Issues)
		}
	}
}

func TestBuiltinProfilesAreCompleteAndIndependent(t *testing.T) {
	profiles := BuiltinProfiles()
	for _, key := range []string{ProfileChatAssistant, ProfileCopywriter, ProfileCoder, ProfileWebUIV2Orchestrator} {
		profile, ok := profiles[key]
		if !ok {
			t.Fatalf("builtin profile %q is missing", key)
		}
		if profile.Key != key || profile.Status != StatusPublished || profile.Version == 0 {
			t.Fatalf("builtin profile %q metadata = %+v", key, profile)
		}
	}

	mutated := profiles[ProfileCopywriter]
	mutated.Config.Prompt.Persona = "mutated"
	profiles[ProfileCopywriter] = mutated
	fresh := BuiltinProfiles()
	if fresh[ProfileCopywriter].Config.Prompt.Persona == "mutated" {
		t.Fatal("BuiltinProfiles leaked mutable profile state")
	}
}

func TestWebUIV2OrchestratorProfileIsTheOnlySessionToolCapability(t *testing.T) {
	profile := BuiltinProfiles()[ProfileWebUIV2Orchestrator]
	if !IsWebUIV2Orchestrator(profile) {
		t.Fatalf("builtin orchestrator is not recognized: %+v", profile)
	}
	want := []string{"SessionCreate", "SessionList", "SessionGet", "SessionSend", "SessionStop", "SessionAttach", "SessionMonitor"}
	if got := profile.Config.Capabilities.Tools.Allow; !equalStrings(got, want) {
		t.Fatalf("orchestrator tools = %v, want %v", got, want)
	}
	for _, key := range []string{ProfileChatAssistant, ProfileCopywriter, ProfileCoder} {
		if IsWebUIV2Orchestrator(BuiltinProfiles()[key]) {
			t.Fatalf("normal profile %q gained session tool capability", key)
		}
	}
}

func equalStrings(got, want []string) bool {
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
