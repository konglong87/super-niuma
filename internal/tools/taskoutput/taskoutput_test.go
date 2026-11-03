package taskoutput

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestTaskOutput(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"status": "complete", "summary": "done", "files": []string{"README.md"}})
	res := New().Run(context.Background(), input, tools.Context{})
	if res.IsError || !strings.Contains(res.Content, "README.md") {
		t.Fatalf("result = %+v", res)
	}
}

func TestTaskOutputMarksFailureStatusesAsErrors(t *testing.T) {
	for _, status := range []string{"failed", "blocked"} {
		t.Run(status, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"status": status, "summary": "nope"})
			res := New().Run(context.Background(), input, tools.Context{})
			if !res.IsError {
				t.Fatalf("status %q must produce an error result: %+v", status, res)
			}
			if !strings.Contains(res.Content, status) || !strings.Contains(res.Content, "nope") {
				t.Fatalf("content = %s", res.Content)
			}
		})
	}
}

func TestTaskOutputRejectsIncompleteInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"malformed json", `{`, "unexpected end"},
		{"missing status", `{"summary":"done"}`, "status and summary are required"},
		{"missing summary", `{"status":"complete"}`, "status and summary are required"},
		{"blank after trim", `{"status":"  ","summary":"  "}`, "status and summary are required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := New().Run(context.Background(), json.RawMessage(tc.input), tools.Context{})
			if !res.IsError || !strings.Contains(res.Content, tc.want) {
				t.Fatalf("res = %+v, want error containing %q", res, tc.want)
			}
		})
	}
}

func TestTaskOutputOmitsFilesWhenAbsent(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"status": "complete", "summary": "done"})
	res := New().Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("res = %+v", res)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if decoded["status"] != "complete" || decoded["summary"] != "done" {
		t.Fatalf("decoded = %v", decoded)
	}
}

func TestTaskOutputMetadata(t *testing.T) {
	tool := New()
	if tool.Name() != "TaskOutput" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if tool.MaxResultSizeChars() != tools.ClaudeCodeDefaultDeclaredMaxResultSizeChars {
		t.Fatalf("MaxResultSizeChars() = %d", tool.MaxResultSizeChars())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
	if tool.Description() == "" {
		t.Fatal("Description() is empty")
	}
}
