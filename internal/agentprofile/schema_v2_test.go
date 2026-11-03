package agentprofile

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func textProfileV2() ProfileDocument {
	return ProfileDocument{
		SchemaVersion: SchemaVersionV2,
		Identity:      IdentityPolicy{RoleType: RoleTypeGeneral},
		Prompt:        PromptPolicy{Mode: PromptModeChat, Language: "zh-CN"},
		Execution:     ExecutionPolicy{Kind: ExecutionKindText},
		Safety:        SafetyPolicy{PermissionMode: PermissionModeAsk, Sandbox: SandboxRequired},
	}
}

// These hashes were captured from the unmodified V1 implementation before
// adding any metadata fields. They lock byte ordering, omission and defaults.
func TestV1BuiltinCanonicalHashesRemainStable(t *testing.T) {
	golden := map[string]string{
		ProfileChatAssistant:       "66b6179c6fb1c041fc0faff74bc8439169e22adb5986d7d23a0b82c5c431fad1",
		ProfileCopywriter:          "fa74139e038f3272fb2bab0226d93bb8e6fc30c354d23cfe4119272bc6809c74",
		ProfileCoder:               "07ecf9f93a277d886f7bb7bbaf9f70f0e445d41d549bc15bcf68db111208141a",
		ProfileWebUIV2Orchestrator: "4a0bf70080c1a42f2f70ce9501c4e14197570196d16be2ce0173c6d3b186242e",
	}
	for key, want := range golden {
		t.Run(key, func(t *testing.T) {
			document := BuiltinProfiles()[key].Config
			canonical, err := document.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(canonical), "role_type") || strings.Contains(string(canonical), `"kind"`) {
				t.Fatalf("V2 metadata leaked into V1: %s", canonical)
			}
			decoded, err := DecodeProfileDocument(canonical)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []ProfileDocument{document, decoded, decoded.Normalize()} {
				got, err := candidate.CanonicalHash()
				if err != nil || got != want {
					t.Fatalf("V1 canonical hash = %s, err=%v; want %s", got, err, want)
				}
			}
		})
	}
}

func TestV2ProfileRoundTripNormalizesMetadataWithoutGrantingCapabilities(t *testing.T) {
	document := textProfileV2()
	document.Identity.RoleType = " ReSeArChEr "
	document.Execution.Kind = " TEXT "
	document.Identity.DisplayName = " Research assistant "
	document.Execution.Provider = " provider-ref "
	document.Execution.Model = " model-ref "
	if report := Validate(document, ValidationContext{AllowedTools: StringSet{}}); !report.Valid {
		t.Fatalf("safe no-tool V2 rejected: %+v", report)
	}
	canonical, err := document.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProfileDocument(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, document.Normalize()) {
		t.Fatalf("round trip = %+v, want %+v", decoded, document.Normalize())
	}
	if decoded.Identity.RoleType != RoleTypeResearcher || decoded.Execution.Kind != ExecutionKindText {
		t.Fatalf("metadata not normalized: %+v", decoded)
	}
	if len(decoded.Capabilities.Tools.Allow) != 0 || decoded.Capabilities.AllowAgents || decoded.Context.Workspace {
		t.Fatalf("role granted capabilities: %+v", decoded)
	}
	before, _ := document.CanonicalHash()
	after, _ := decoded.CanonicalHash()
	if before != after {
		t.Fatalf("V2 round trip changed hash: %s != %s", before, after)
	}
	decoded.Identity.RoleType = RoleTypePlanner
	changedRole, _ := decoded.CanonicalHash()
	decoded.Identity.RoleType = RoleTypeResearcher
	decoded.Execution.Kind = ExecutionKindImage
	changedKind, _ := decoded.CanonicalHash()
	if changedRole == before || changedKind == before {
		t.Fatal("typed metadata missing from V2 hash")
	}
}

func TestV2ValidationRejectsUnknownAndMissingMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProfileDocument)
		code   string
		field  string
	}{
		{"unknown role", func(d *ProfileDocument) { d.Identity.RoleType = "admin" }, IssueRoleTypeInvalid, "identity.role_type"},
		{"missing role", func(d *ProfileDocument) { d.Identity.RoleType = "" }, IssueRoleTypeInvalid, "identity.role_type"},
		{"unknown kind", func(d *ProfileDocument) { d.Execution.Kind = "shell" }, IssueExecutionKindInvalid, "execution.kind"},
		{"missing kind", func(d *ProfileDocument) { d.Execution.Kind = "" }, IssueExecutionKindInvalid, "execution.kind"},
		{"unknown version", func(d *ProfileDocument) { d.SchemaVersion = 99 }, IssueSchemaVersion, "schema_version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := textProfileV2()
			test.mutate(&document)
			report := Validate(document, ValidationContext{})
			if report.Valid || len(report.Issues) != 1 || report.Issues[0].Code != test.code || report.Issues[0].Field != test.field {
				t.Fatalf("unexpected report: %+v", report)
			}
		})
	}
}

func TestV2RecognizesDeferredKindsWithoutEnablingWorkflowExecution(t *testing.T) {
	for _, descriptor := range ExecutionKindDescriptors() {
		document := textProfileV2()
		document.Execution.Kind = descriptor.Kind
		if report := Validate(document, ValidationContext{}); !report.Valid {
			t.Fatalf("recognized kind %s rejected at schema level: %+v", descriptor.Kind, report)
		}
		if descriptor.WorkflowSupported != (descriptor.Kind == ExecutionKindText) {
			t.Fatalf("MVP execution availability incorrect: %+v", descriptor)
		}
	}
}

func TestV1RejectsV2MetadataAtDecodeAndValidationBoundaries(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":1,"identity":{"role_type":"general"}}`,
		`{"schema_version":1,"identity":{"role_type":""}}`,
		`{"schema_version":1,"identity":{"ROLE_TYPE":"general"}}`,
		`{"schema_version":1,"identity":{"role_type":null}}`,
		`{"schema_version":1,"execution":{"kind":"text"}}`,
		`{"schema_version":1,"execution":{"kind":""}}`,
		`{"schema_version":1,"execution":{"KIND":"text"}}`,
		`{"schema_version":1,"execution":{"kind":null}}`,
	} {
		if _, err := DecodeProfileDocument([]byte(raw)); err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("V1 decode accepted V2 metadata %s: %v", raw, err)
		}
	}
	document := textProfileV2()
	document.SchemaVersion = SchemaVersionV1
	report := Validate(document, ValidationContext{})
	if report.Valid || !report.Has(IssueRoleTypeInvalid) || !report.Has(IssueExecutionKindInvalid) {
		t.Fatalf("struct-built V1 accepted V2 metadata: %+v", report)
	}
}

func TestV2DecodeRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":2,"future":true}`,
		`{"schema_version":2,"identity":{"role":"general"}}`,
		`{"schema_version":2,"execution":{"kind":"text","secret":"no"}}`,
		`{"schema_version":2,"execution":{"kind":42}}`,
		`{"schema_version":2} {}`,
		`{"schema_version":2} trailing`,
		`{"schema_version":2} {`,
		strings.Repeat(" ", MaxProfileJSONBytes+1),
	} {
		if _, err := DecodeProfileDocument([]byte(raw)); err == nil {
			t.Fatalf("invalid V2 JSON accepted: %.160s", raw)
		}
	}
}

func TestV2RunsEverySharedV1SecurityAndPolicyValidation(t *testing.T) {
	document := textProfileV2()
	document.Identity.DisplayName = strings.Repeat("x", MaxDisplayNameBytes+1)
	document.Identity.Description = strings.Repeat("x", MaxDescriptionBytes+1)
	document.Prompt.Persona = strings.Repeat("x", MaxPersonaBytes+1)
	document.Prompt.SystemAddendum = strings.Repeat("x", MaxSystemAddendumBytes+1)
	document.Context = ContextPolicy{Workspace: true, Git: true}
	document.Safety = SafetyPolicy{PermissionMode: PermissionModeBypass, AllowUnsandboxedCommands: true}
	document.Capabilities = CapabilityPolicy{
		Tools: ToolPolicy{Allow: []string{"Bash"}}, Skills: []string{"unregistered"}, MCPServers: []string{"external"},
	}
	document.Execution.MaxTurns = 9
	document.Execution.MaxTokens = 9000
	document.Execution.MaxParallelReadOnlyTools = 8
	policy := ValidationContext{
		AllowedTools: StringSet{}, AllowedSkills: StringSet{}, AllowedMCPServers: StringSet{},
		MaxTurns: 3, MaxTokens: 2048, MaxParallelWorkers: 2,
	}
	v2 := Validate(document, policy)
	document.SchemaVersion = SchemaVersionV1
	document.Identity.RoleType = ""
	document.Execution.Kind = ""
	v1 := Validate(document, policy)
	if v2.Valid || !reflect.DeepEqual(v2, v1) {
		t.Fatalf("V2 skipped/changed V1 checks:\nV2: %+v\nV1: %+v", v2, v1)
	}
	if len(v2.Issues) != 14 {
		t.Fatalf("expected all 14 shared checks: %+v", v2)
	}
	document.SchemaVersion = SchemaVersionV2
	document.Identity.RoleType = RoleTypeGeneral
	document.Execution.Kind = ExecutionKindText
	document.Prompt.Mode = "unknown"
	if report := Validate(document, policy); !report.Has(IssueModeInvalid) {
		t.Fatalf("V2 skipped prompt mode check: %+v", report)
	}
}

func TestV2StorageRoundTripPreservesMetadata(t *testing.T) {
	// Persisted ProfileDocument bytes use the same decoder as the existing
	// MySQL source; metadata must survive ordinary JSON serialization too.
	document := textProfileV2()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProfileDocument(encoded)
	if err != nil || !reflect.DeepEqual(decoded, document) {
		t.Fatalf("storage round trip = %+v, err=%v", decoded, err)
	}
}
