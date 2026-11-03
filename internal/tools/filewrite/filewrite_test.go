package filewrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestWriteTool(t *testing.T) {
	tmp := t.TempDir()
	input, _ := json.Marshal(map[string]string{
		"file_path": "nested/hello.txt",
		"content":   "hello",
	})
	var changes []tools.FileChange
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp, FileChange: func(change tools.FileChange) {
		changes = append(changes, change)
	}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "nested", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("file content = %q, want hello", data)
	}
	if len(changes) != 1 || changes[0].BeforeExists || !changes[0].AfterExists || changes[0].After != "hello" {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].BeforeModeKnown || changes[0].AfterMode != 0644 || !changes[0].AfterModeKnown || changes[0].ModeChanged {
		t.Fatalf("mode metadata = before %04o after %04o changed %v", changes[0].BeforeMode, changes[0].AfterMode, changes[0].ModeChanged)
	}
}

func TestWriteToolRecordsExistingModeMetadata(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "script.sh")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{
		"file_path": "script.sh",
		"content":   "new",
	})
	var changes []tools.FileChange
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp, FileChange: func(change tools.FileChange) {
		changes = append(changes, change)
	}})
	if res.IsError {
		t.Fatalf("Run() error result: %s", res.Content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
	if len(changes) != 1 || !changes[0].BeforeExists || changes[0].BeforeMode != 0755 || changes[0].AfterMode != 0755 || !changes[0].BeforeModeKnown || !changes[0].AfterModeKnown || changes[0].ModeChanged {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestWriteToolDeniesOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	other := t.TempDir()
	input, _ := json.Marshal(map[string]string{
		"file_path": filepath.Join(other, "hello.txt"),
		"content":   "hello",
	})
	res := New().Run(context.Background(), input, tools.Context{CWD: tmp})
	if !res.IsError {
		t.Fatalf("Run() = %+v, want error", res)
	}
}
