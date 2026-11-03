package channel

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeFingerprintIsStableAndVersioned(t *testing.T) {
	t.Parallel()

	first, err := RuntimeFingerprint(testRuntimeFingerprintInput())
	if err != nil {
		t.Fatalf("RuntimeFingerprint() error = %v", err)
	}
	second, err := RuntimeFingerprint(testRuntimeFingerprintInput())
	if err != nil {
		t.Fatalf("RuntimeFingerprint() second error = %v", err)
	}
	const want = "1c27827a48f2568c91311343fd7f6079525677ad2c7bee533cc35d3670c0492d"
	if first != want {
		t.Fatalf("RuntimeFingerprint() = %q, want %q", first, want)
	}
	if second != first {
		t.Fatalf("RuntimeFingerprint() is unstable: first=%q second=%q", first, second)
	}
	if len(first) != 64 || strings.ToLower(first) != first {
		t.Fatalf("RuntimeFingerprint() = %q, want lowercase 64-character hex", first)
	}
	if RuntimeFingerprintVersion != 1 {
		t.Fatalf("RuntimeFingerprintVersion = %d, want 1", RuntimeFingerprintVersion)
	}
}

func TestRuntimeFingerprintRejectsZeroValueAndEachMissingRequiredField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RuntimeFingerprintInput)
	}{
		{name: "zero value", mutate: func(in *RuntimeFingerprintInput) { *in = RuntimeFingerprintInput{} }},
		{name: "agent kind", mutate: func(in *RuntimeFingerprintInput) { in.AgentKind = "" }},
		{name: "prompt mode", mutate: func(in *RuntimeFingerprintInput) { in.PromptMode = "" }},
		{name: "model provider", mutate: func(in *RuntimeFingerprintInput) { in.ModelProvider = "" }},
		{name: "model policy", mutate: func(in *RuntimeFingerprintInput) { in.ModelPolicy = "" }},
		{name: "permission mode", mutate: func(in *RuntimeFingerprintInput) { in.PermissionMode = "" }},
		{name: "sandbox policy", mutate: func(in *RuntimeFingerprintInput) { in.SandboxPolicy = "" }},
		{name: "access policy digest", mutate: func(in *RuntimeFingerprintInput) { in.AccessPolicyDigest = "" }},
		{name: "resource scope digest", mutate: func(in *RuntimeFingerprintInput) { in.ResourceScopeDigest = "" }},
		{name: "attachment policy shape digest", mutate: func(in *RuntimeFingerprintInput) { in.AttachmentPolicyShapeDigest = "" }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := testRuntimeFingerprintInput()
			tt.mutate(&input)
			if _, err := RuntimeFingerprint(input); err == nil {
				t.Fatal("RuntimeFingerprint() error = nil, want required-field validation error")
			}
		})
	}
}

func TestRuntimeFingerprintRejectsInvalidUTF8InEveryStringField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RuntimeFingerprintInput)
	}{
		{name: "agent kind", mutate: func(in *RuntimeFingerprintInput) { in.AgentKind = "\xff" }},
		{name: "prompt mode", mutate: func(in *RuntimeFingerprintInput) { in.PromptMode = "\xff" }},
		{name: "model provider", mutate: func(in *RuntimeFingerprintInput) { in.ModelProvider = "\xff" }},
		{name: "model policy", mutate: func(in *RuntimeFingerprintInput) { in.ModelPolicy = "\xff" }},
		{name: "workspace realpath", mutate: func(in *RuntimeFingerprintInput) { in.WorkspaceRealpath = "\xff" }},
		{name: "permission mode", mutate: func(in *RuntimeFingerprintInput) { in.PermissionMode = "\xff" }},
		{name: "sandbox policy", mutate: func(in *RuntimeFingerprintInput) { in.SandboxPolicy = "\xff" }},
		{name: "access policy digest", mutate: func(in *RuntimeFingerprintInput) { in.AccessPolicyDigest = "\xff" }},
		{name: "resource scope digest", mutate: func(in *RuntimeFingerprintInput) { in.ResourceScopeDigest = "\xff" }},
		{name: "attachment policy shape digest", mutate: func(in *RuntimeFingerprintInput) { in.AttachmentPolicyShapeDigest = "\xff" }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			input := testRuntimeFingerprintInput()
			tt.mutate(&input)
			if _, err := RuntimeFingerprint(input); err == nil {
				t.Fatal("RuntimeFingerprint() error = nil, want invalid UTF-8 error")
			}
		})
	}
}

func TestRuntimeFingerprintRejectsBoundaryWhitespaceOnRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RuntimeFingerprintInput, string)
	}{
		{name: "agent kind", mutate: func(in *RuntimeFingerprintInput, value string) { in.AgentKind = value }},
		{name: "prompt mode", mutate: func(in *RuntimeFingerprintInput, value string) { in.PromptMode = value }},
		{name: "model provider", mutate: func(in *RuntimeFingerprintInput, value string) { in.ModelProvider = value }},
		{name: "model policy", mutate: func(in *RuntimeFingerprintInput, value string) { in.ModelPolicy = value }},
		{name: "permission mode", mutate: func(in *RuntimeFingerprintInput, value string) { in.PermissionMode = value }},
		{name: "sandbox policy", mutate: func(in *RuntimeFingerprintInput, value string) { in.SandboxPolicy = value }},
		{name: "access policy digest", mutate: func(in *RuntimeFingerprintInput, value string) { in.AccessPolicyDigest = value }},
		{name: "resource scope digest", mutate: func(in *RuntimeFingerprintInput, value string) { in.ResourceScopeDigest = value }},
		{name: "attachment policy shape digest", mutate: func(in *RuntimeFingerprintInput, value string) { in.AttachmentPolicyShapeDigest = value }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, value := range []string{" value", "value "} {
				input := testRuntimeFingerprintInput()
				tt.mutate(&input, value)
				if _, err := RuntimeFingerprint(input); err == nil {
					t.Fatalf("RuntimeFingerprint(%q) error = nil, want boundary-whitespace error", value)
				}
			}
		})
	}
}

func TestRuntimeFingerprintPreservesWorkspaceRealpathBytes(t *testing.T) {
	t.Parallel()

	base := testRuntimeFingerprintInput()
	base.WorkspaceRealpath = "/workspace/app"
	withTrailingSpace := base
	withTrailingSpace.WorkspaceRealpath = "/workspace/app "
	empty := base
	empty.WorkspaceRealpath = ""

	first, err := RuntimeFingerprint(base)
	if err != nil {
		t.Fatalf("RuntimeFingerprint(base) error = %v", err)
	}
	second, err := RuntimeFingerprint(withTrailingSpace)
	if err != nil {
		t.Fatalf("RuntimeFingerprint(withTrailingSpace) error = %v", err)
	}
	if first == second {
		t.Fatalf("workspace trailing byte was ignored: %q", first)
	}
	if _, err := RuntimeFingerprint(empty); err != nil {
		t.Fatalf("RuntimeFingerprint(empty workspace) error = %v, want nil", err)
	}
}

func TestRuntimeFingerprintCanonicalEnvelopeJSONParity(t *testing.T) {
	t.Parallel()

	input := testRuntimeFingerprintInput()
	payload, err := json.Marshal(runtimeFingerprintEnvelope{
		Version:                 RuntimeFingerprintVersion,
		RuntimeFingerprintInput: input,
	})
	if err != nil {
		t.Fatalf("json.Marshal(runtimeFingerprintEnvelope) error = %v", err)
	}
	const wantJSON = `{"version":1,"agent_kind":"coding","prompt_mode":"code","model_provider":"anthropic","model_policy":"balanced","workspace_realpath":"/workspace/project","permission_mode":"ask","sandbox_policy":"workspace-write","access_policy_digest":"access-v1","resource_scope_digest":"resource-v1","attachment_policy_shape_digest":"attachment-v1"}`
	if string(payload) != wantJSON {
		t.Fatalf("canonical envelope JSON = %s, want %s", payload, wantJSON)
	}
	hash, err := RuntimeFingerprint(input)
	if err != nil {
		t.Fatalf("RuntimeFingerprint() error = %v", err)
	}
	if hash != "1c27827a48f2568c91311343fd7f6079525677ad2c7bee533cc35d3670c0492d" {
		t.Fatalf("RuntimeFingerprint() = %q, want envelope parity hash", hash)
	}
}

func TestRuntimeFingerprintRejectsNonCanonicalWorkspacePaths(t *testing.T) {
	t.Parallel()

	valid := testRuntimeFingerprintInput()
	valid.WorkspaceRealpath = "/workspace/app"
	if !filepath.IsAbs(valid.WorkspaceRealpath) {
		t.Fatalf("test workspace path %q is not absolute", valid.WorkspaceRealpath)
	}
	if _, err := RuntimeFingerprint(valid); err != nil {
		t.Fatalf("RuntimeFingerprint(valid workspace) error = %v", err)
	}
	for _, path := range []string{"workspace/app", "/workspace/../app", "/workspace/app/"} {
		input := valid
		input.WorkspaceRealpath = path
		if _, err := RuntimeFingerprint(input); err == nil {
			t.Fatalf("RuntimeFingerprint(workspace=%q) error = nil, want canonical absolute path error", path)
		}
	}
	input := valid
	input.WorkspaceRealpath = ""
	if _, err := RuntimeFingerprint(input); err != nil {
		t.Fatalf("RuntimeFingerprint(empty workspace) error = %v, want nil", err)
	}
}

func TestStoredRuntimeFingerprintVersionIsPersistable(t *testing.T) {
	t.Parallel()

	if got, want := StoredRuntimeFingerprintVersion(), RuntimeFingerprintVersion; got != want {
		t.Fatalf("StoredRuntimeFingerprintVersion() = %d, want %d", got, want)
	}
}

func TestRuntimeFingerprintEveryFieldChangeChangesHash(t *testing.T) {
	t.Parallel()

	base := testRuntimeFingerprintInput()
	baseline, err := RuntimeFingerprint(base)
	if err != nil {
		t.Fatalf("RuntimeFingerprint(base) error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*RuntimeFingerprintInput)
	}{
		{name: "agent kind", mutate: func(in *RuntimeFingerprintInput) { in.AgentKind = "chat" }},
		{name: "prompt mode", mutate: func(in *RuntimeFingerprintInput) { in.PromptMode = "chat" }},
		{name: "model provider", mutate: func(in *RuntimeFingerprintInput) { in.ModelProvider = "openai" }},
		{name: "model policy", mutate: func(in *RuntimeFingerprintInput) { in.ModelPolicy = "fast" }},
		{name: "workspace realpath", mutate: func(in *RuntimeFingerprintInput) { in.WorkspaceRealpath = "/workspace/other" }},
		{name: "permission mode", mutate: func(in *RuntimeFingerprintInput) { in.PermissionMode = "allow" }},
		{name: "sandbox policy", mutate: func(in *RuntimeFingerprintInput) { in.SandboxPolicy = "danger-full-access" }},
		{name: "access policy digest", mutate: func(in *RuntimeFingerprintInput) { in.AccessPolicyDigest = "access-v2" }},
		{name: "resource scope digest", mutate: func(in *RuntimeFingerprintInput) { in.ResourceScopeDigest = "resource-v2" }},
		{name: "attachment policy shape digest", mutate: func(in *RuntimeFingerprintInput) { in.AttachmentPolicyShapeDigest = "attachment-v2" }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			changed := base
			tt.mutate(&changed)
			got, err := RuntimeFingerprint(changed)
			if err != nil {
				t.Fatalf("RuntimeFingerprint() error = %v", err)
			}
			if got == baseline {
				t.Fatalf("RuntimeFingerprint() = %q, want changed fingerprint", got)
			}
		})
	}
}

func testRuntimeFingerprintInput() RuntimeFingerprintInput {
	return RuntimeFingerprintInput{
		AgentKind:                   "coding",
		PromptMode:                  "code",
		ModelProvider:               "anthropic",
		ModelPolicy:                 "balanced",
		WorkspaceRealpath:           "/workspace/project",
		PermissionMode:              "ask",
		SandboxPolicy:               "workspace-write",
		AccessPolicyDigest:          "access-v1",
		ResourceScopeDigest:         "resource-v1",
		AttachmentPolicyShapeDigest: "attachment-v1",
	}
}
