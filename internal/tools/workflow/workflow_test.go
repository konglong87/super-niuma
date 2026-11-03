package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestWorkflowListShowRunDryRun(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".claude", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.yaml"), []byte(`name: verify
description: Run verification
steps:
  - name: unit
    command: go test ./...
`), 0644); err != nil {
		t.Fatal(err)
	}
	tool := New()
	res := tool.Run(context.Background(), json.RawMessage(`{"action":"list"}`), tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, "verify") {
		t.Fatalf("list = %+v", res)
	}
	res = tool.Run(context.Background(), json.RawMessage(`{"action":"run","name":"verify","dry_run":true}`), tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, `"dry_run": true`) || !strings.Contains(res.Content, "go test ./...") {
		t.Fatalf("run = %+v", res)
	}
}

func TestWorkflowRejectsUnsafeWriteStep(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	dir := filepath.Join(tmp, ".claude", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unsafe.yaml"), []byte(`name: unsafe
steps:
  - name: outside
    command: touch `+filepath.Join(other, "out.txt")+`
`), 0644); err != nil {
		t.Fatal(err)
	}
	res := New().Run(context.Background(), json.RawMessage(`{"action":"run","name":"unsafe","dry_run":true}`), tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("workflow returns structured failure, not tool error: %+v", res)
	}
	if !strings.Contains(res.Content, `"ok": false`) || !strings.Contains(res.Content, "outside the current workspace") {
		t.Fatalf("run = %+v", res)
	}
}

// TestWorkflowRunDoesNotLeakSecretsToSteps locks AUDIT-P1-16 for the workflow path.
func TestWorkflowRunDoesNotLeakSecretsToSteps(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".claude", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leak.yaml"), []byte(`name: leak
description: report secrets
steps:
  - name: report
    command: printf 'A=%s G=%s W=%s O=%s P=%s' "$ANTHROPIC_API_KEY" "$GITHUB_TOKEN" "$AWS_SECRET_ACCESS_KEY" "$OPENAI_API_KEY" "$GOPATH"
`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "leaked-"+key)
	}
	t.Setenv("GOPATH", "/keep/gopath")

	res := New().Run(context.Background(), json.RawMessage(`{"action":"run","name":"leak"}`), tools.Context{CWD: tmp, WritableRoots: []string{tmp}})
	if res.IsError {
		t.Fatalf("run = %+v", res)
	}
	if !strings.Contains(res.Content, `A= G= W= O= P=/keep/gopath`) {
		t.Fatalf("workflow step saw secrets: %s", res.Content)
	}
}

func writeWorkflow(t *testing.T, cwd, name, body string) {
	t.Helper()
	dir := filepath.Join(cwd, ".claude", "workflows")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowShowAndNotFound(t *testing.T) {
	tmp := t.TempDir()
	writeWorkflow(t, tmp, "verify.yaml", "name: verify\ndescription: Run verification\nsteps:\n  - name: unit\n    command: true\n")
	tool := New()
	ctx := tools.Context{CWD: tmp, WritableRoots: []string{tmp}}

	res := tool.Run(context.Background(), json.RawMessage(`{"action":"show","name":"verify"}`), ctx)
	if res.IsError || !strings.Contains(res.Content, "Run verification") {
		t.Fatalf("show = %+v", res)
	}
	for _, action := range []string{"show", "run"} {
		res := tool.Run(context.Background(), json.RawMessage(`{"action":"`+action+`","name":"ghost"}`), ctx)
		if !res.IsError || !strings.Contains(res.Content, "workflow not found: ghost") {
			t.Fatalf("%s missing = %+v", action, res)
		}
	}
}

func TestWorkflowRejectsBadInput(t *testing.T) {
	tmp := t.TempDir()
	tool := New()
	ctx := tools.Context{CWD: tmp}

	res := tool.Run(context.Background(), json.RawMessage(`{`), ctx)
	if !res.IsError || !strings.Contains(res.Content, "unexpected end") {
		t.Fatalf("malformed = %+v", res)
	}
	res = tool.Run(context.Background(), json.RawMessage(`{"action":"detonate"}`), ctx)
	if !res.IsError || !strings.Contains(res.Content, "unsupported Workflow action") {
		t.Fatalf("unknown action = %+v", res)
	}
}

func TestWorkflowSurfacesMalformedYAML(t *testing.T) {
	tmp := t.TempDir()
	writeWorkflow(t, tmp, "broken.yaml", "name: broken\nsteps:\n  - this is: not\n   valid yaml\n")
	res := New().Run(context.Background(), json.RawMessage(`{"action":"list"}`), tools.Context{CWD: tmp})
	if !res.IsError {
		t.Fatalf("malformed YAML must surface as an error, got %+v", res)
	}
}

func TestWorkflowListIsEmptyWithoutWorkflowDir(t *testing.T) {
	res := New().Run(context.Background(), json.RawMessage(`{"action":"list"}`), tools.Context{CWD: t.TempDir()})
	if res.IsError {
		t.Fatalf("list without a workflow dir must not error: %+v", res)
	}
}

func TestWorkflowStopsAtFirstFailingStep(t *testing.T) {
	tmp := t.TempDir()
	writeWorkflow(t, tmp, "chain.yaml", "name: chain\nsteps:\n  - name: ok\n    command: printf first\n  - name: boom\n    command: exit 3\n  - name: never\n    command: printf third\n")
	res := New().Run(context.Background(), json.RawMessage(`{"action":"run","name":"chain"}`), tools.Context{CWD: tmp, WritableRoots: []string{tmp}})
	// Workflow reports failure structurally rather than as a tool error; see
	// TestWorkflowRejectsUnsafeWriteStep.
	if !strings.Contains(res.Content, `"ok": false`) {
		t.Fatalf("a failing step must mark the run not-ok: %+v", res)
	}
	if !strings.Contains(res.Content, `"failed_step": "boom"`) {
		t.Fatalf("content = %s", res.Content)
	}
	if strings.Contains(res.Content, "third") {
		t.Fatalf("steps after the failure still ran: %s", res.Content)
	}
}

func TestWorkflowStepTimeoutIsHonored(t *testing.T) {
	tmp := t.TempDir()
	writeWorkflow(t, tmp, "slow.yaml", "name: slow\nsteps:\n  - name: sleeper\n    command: sleep 5\n")
	res := New().Run(context.Background(), json.RawMessage(`{"action":"run","name":"slow","timeout_ms":200}`), tools.Context{CWD: tmp, WritableRoots: []string{tmp}})
	if !strings.Contains(res.Content, `"ok": false`) || !strings.Contains(res.Content, `"failed_step": "sleeper"`) {
		t.Fatalf("a step exceeding timeout_ms must fail the run: %+v", res)
	}
}

func TestWorkflowMetadata(t *testing.T) {
	tool := New()
	if tool.Name() != "Workflow" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if tool.MaxResultSizeChars() != tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars {
		t.Fatalf("MaxResultSizeChars() = %d", tool.MaxResultSizeChars())
	}
	if tool.Description() == "" {
		t.Fatal("Description() is empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
}
