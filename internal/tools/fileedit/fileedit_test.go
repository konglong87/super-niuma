package fileedit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestEditTool(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{
		"file_path":  "hello.txt",
		"old_string": "world",
		"new_string": "go",
	})
	var changes []tools.FileChange
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp, FileChange: func(change tools.FileChange) {
		changes = append(changes, change)
	}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello go" {
		t.Fatalf("file content = %q, want hello go", data)
	}
	if len(changes) != 1 || changes[0].Before != "hello world" || changes[0].After != "hello go" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestEditToolRequiresExactlyOneMatch(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("x x"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{
		"file_path":  "hello.txt",
		"old_string": "x",
		"new_string": "y",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError || !strings.Contains(res.Content, "exactly once") {
		t.Fatalf("Run() = %+v, want exactly-once error", res)
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(path, []byte("x x"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{
		"file_path":   "hello.txt",
		"old_string":  "x",
		"new_string":  "y",
		"replace_all": true,
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "y y" {
		t.Fatalf("file content = %q, want y y", data)
	}
}

func TestEditToolAllowsAdditionalWritableRoot(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	path := filepath.Join(other, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{
		"file_path":  path,
		"old_string": "world",
		"new_string": "go",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp, WritableRoots: []string{other}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello go" {
		t.Fatalf("file content = %q, want hello go", data)
	}
}

func TestMultiEditAppliesEditsAtomically(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{
		"file_path": "a.txt",
		"edits": []map[string]any{
			{"old_string": "one", "new_string": "1"},
			{"old_string": "three", "new_string": "3"},
		},
	})
	res := NewMulti().Run(context.Background(), input, tools.Context{CWD: tmp})
	if res.IsError {
		t.Fatalf("Run() = %+v", res)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1\ntwo\n3\n" {
		t.Fatalf("data = %q", data)
	}

	input, _ = json.Marshal(map[string]any{
		"file_path": "a.txt",
		"edits": []map[string]any{
			{"old_string": "missing", "new_string": "x"},
		},
	})
	res = NewMulti().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError {
		t.Fatalf("Run() = %+v, want error", res)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1\ntwo\n3\n" {
		t.Fatalf("data changed after failed edit = %q", data)
	}
}

func TestEditToolPreservesExecutableMode(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "script.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\necho old\n"), 0755); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{
		"file_path":  "script.sh",
		"old_string": "old",
		"new_string": "new",
	})
	var changes []tools.FileChange
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp, FileChange: func(change tools.FileChange) {
		changes = append(changes, change)
	}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	assertMode(t, path, 0755)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].BeforeMode != 0755 || changes[0].AfterMode != 0755 || !changes[0].BeforeModeKnown || !changes[0].AfterModeKnown || changes[0].ModeChanged {
		t.Fatalf("mode metadata = before %04o after %04o changed %v", changes[0].BeforeMode, changes[0].AfterMode, changes[0].ModeChanged)
	}
}

func TestMultiEditPreservesExecutableMode(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "script.sh")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\necho old\necho again\n"), 0755); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{
		"file_path": "script.sh",
		"edits": []map[string]string{
			{"old_string": "old", "new_string": "new"},
			{"old_string": "again", "new_string": "done"},
		},
	})
	var changes []tools.FileChange
	res := NewMulti().Run(context.Background(), input, tools.Context{CWD: tmp, FileChange: func(change tools.FileChange) {
		changes = append(changes, change)
	}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	assertMode(t, path, 0755)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].BeforeMode != 0755 || changes[0].AfterMode != 0755 || !changes[0].BeforeModeKnown || !changes[0].AfterModeKnown || changes[0].ModeChanged {
		t.Fatalf("mode metadata = before %04o after %04o changed %v", changes[0].BeforeMode, changes[0].AfterMode, changes[0].ModeChanged)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %04o, want %04o", got, want)
	}
}
