package todowrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestTodoWriteTool(t *testing.T) {
	tmp := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"todos": []map[string]string{{
			"content":    "write tests",
			"activeForm": "writing tests",
			"status":     "in_progress",
		}},
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	for _, want := range []string{
		"Todos have been modified successfully",
		"continue to use the todo list",
		"Summary: 1 total, 0 pending, 1 in_progress, 0 completed",
		"State file:",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
	data, err := os.ReadFile(filepath.Join(tmp, ".niuma", "todos.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "write tests") || !strings.Contains(string(data), "writing tests") || !strings.Contains(string(data), "todo-1") {
		t.Fatalf("todos file = %s", data)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".claude", "todos.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy todo file should not be written, err=%v", err)
	}
	res = NewRead().Run(context.Background(), []byte(`{}`), tools.Context{CWD: tmp})
	if res.IsError || !strings.Contains(res.Content, "write tests") {
		t.Fatalf("read result = %+v", res)
	}
}

func TestTodoWriteUsesConfiguredIdentityStatePath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR_NAME", ".go-code")

	res := New().Run(context.Background(), json.RawMessage(`{"todos":[{"content":"x","status":"pending"}]}`), tools.Context{CWD: tmp, WritableRoots: []string{tmp}})
	if res.IsError {
		t.Fatalf("TodoWrite error: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".go-code", "todos.json")); err != nil {
		t.Fatalf("expected configured todo path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".niuma", "todos.json")); !os.IsNotExist(err) {
		t.Fatalf("default todo path should not be written, err=%v", err)
	}
}

func TestTodoWriteClearsPersistedStateWhenAllCompleted(t *testing.T) {
	tmp := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"todos": []map[string]string{{
			"content": "write tests",
			"status":  "completed",
		}, {
			"content": "run tests",
			"status":  "completed",
		}},
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	for _, want := range []string{
		"Summary: 2 total, 0 pending, 0 in_progress, 2 completed",
		"persisted session todo state was cleared",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
	data, err := os.ReadFile(filepath.Join(tmp, ".niuma", "todos.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("todos file = %s, want []", data)
	}
	res = NewRead().Run(context.Background(), []byte(`{}`), tools.Context{CWD: tmp})
	if res.IsError || strings.TrimSpace(res.Content) != "[]" {
		t.Fatalf("read result = %+v", res)
	}
}

func TestTodoReadMissingFile(t *testing.T) {
	res := NewRead().Run(context.Background(), []byte(`{}`), tools.Context{CWD: t.TempDir()})
	if res.IsError || strings.TrimSpace(res.Content) != "[]" {
		t.Fatalf("read result = %+v", res)
	}
}

func TestTodoWriteValidatesPriorityAndSingleInProgress(t *testing.T) {
	tmp := t.TempDir()
	input, _ := json.Marshal(map[string]any{
		"todos": []map[string]string{{
			"content":  "one",
			"status":   "pending",
			"priority": "urgent",
		}},
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "invalid priority") {
		t.Fatalf("result = %+v", res)
	}

	input, _ = json.Marshal(map[string]any{
		"todos": []map[string]string{
			{"content": "one", "status": "in_progress"},
			{"content": "two", "status": "in_progress"},
		},
	})
	res = New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "only one todo") {
		t.Fatalf("result = %+v", res)
	}

	input, _ = json.Marshal(map[string]any{
		"todos": []map[string]string{{
			"content":    "one",
			"activeForm": "   ",
			"status":     "pending",
		}},
	})
	res = New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "activeForm is blank") {
		t.Fatalf("result = %+v", res)
	}
}

func TestTodoWriteDescriptionWarnsAgainstReadOnlyUse(t *testing.T) {
	desc := New().Description()
	for _, want := range []string{
		"writes .niuma/todos.json",
		"activeForm",
		"Only use TodoWrite when file writes are allowed",
		"read-only analysis",
		"do not modify files",
		"TodoRead",
		"assistant response",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}

	schema := string(New().InputSchema())
	for _, want := range []string{
		"writes .niuma/todos.json",
		"activeForm",
		"read-only",
		"no-modify",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
}
