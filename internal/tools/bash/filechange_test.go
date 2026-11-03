package bash

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

func runBashCapture(t *testing.T, cwd string, writableRoots []string, command string) ([]tools.FileChange, tools.Result) {
	t.Helper()
	var changes []tools.FileChange
	input, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	res := New().Run(context.Background(), input, tools.Context{
		CWD:           cwd,
		WritableRoots: writableRoots,
		FileChange:    func(c tools.FileChange) { changes = append(changes, c) },
	})
	return changes, res
}

func findChange(changes []tools.FileChange, path string) *tools.FileChange {
	for i := range changes {
		if changes[i].Path == path {
			return &changes[i]
		}
	}
	return nil
}

func TestBashCapturesFileCreate(t *testing.T) {
	cwd := t.TempDir()
	changes, res := runBashCapture(t, cwd, nil, "printf hello > created.txt")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, filepath.Join(cwd, "created.txt"))
	if c == nil {
		t.Fatalf("no change captured, got %+v", changes)
	}
	if c.BeforeExists {
		t.Errorf("before should not exist")
	}
	if !c.AfterExists {
		t.Errorf("after should exist")
	}
	if c.After != "hello" {
		t.Errorf("after content = %q", c.After)
	}
}

func TestBashCapturesFileModify(t *testing.T) {
	cwd := t.TempDir()
	p := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(p, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, res := runBashCapture(t, cwd, nil, "printf updated > note.txt")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, p)
	if c == nil {
		t.Fatalf("no change captured, got %+v", changes)
	}
	if !c.BeforeExists || c.Before != "original" {
		t.Errorf("before = %q exists=%v", c.Before, c.BeforeExists)
	}
	if !c.AfterExists || c.After != "updated" {
		t.Errorf("after = %q exists=%v", c.After, c.AfterExists)
	}
}

func TestBashCapturesFileDelete(t *testing.T) {
	cwd := t.TempDir()
	p := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(p, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, res := runBashCapture(t, cwd, nil, "rm note.txt")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, p)
	if c == nil {
		t.Fatalf("no change captured, got %+v", changes)
	}
	if !c.BeforeExists || c.Before != "original" {
		t.Errorf("before = %q exists=%v", c.Before, c.BeforeExists)
	}
	if c.AfterExists {
		t.Errorf("after should not exist")
	}
}

func TestBashCapturesChmod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes unsupported on Windows")
	}
	cwd := t.TempDir()
	p := filepath.Join(cwd, "script.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, res := runBashCapture(t, cwd, nil, "chmod 755 script.sh")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, p)
	if c == nil {
		t.Fatalf("no change captured, got %+v", changes)
	}
	if !c.ModeChanged {
		t.Errorf("mode should be marked changed: %+v", c)
	}
	if c.BeforeMode.Perm() != 0644 || c.AfterMode.Perm() != 0755 {
		t.Errorf("before=%o after=%o", c.BeforeMode.Perm(), c.AfterMode.Perm())
	}
}

func TestBashCapturesSymlinkCreate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, res := runBashCapture(t, cwd, nil, "ln -s note.txt link.txt")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, filepath.Join(cwd, "link.txt"))
	if c == nil {
		t.Fatalf("no change captured, got %+v", changes)
	}
	if c.BeforeExists {
		t.Errorf("before should not exist")
	}
	if !c.AfterExists || !c.AfterIsSymlink || c.AfterLinkTarget != "note.txt" {
		t.Errorf("after symlink = %+v", c)
	}
}

func TestBashCapturesRename(t *testing.T) {
	cwd := t.TempDir()
	a := filepath.Join(cwd, "a.txt")
	b := filepath.Join(cwd, "b.txt")
	if err := os.WriteFile(a, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, res := runBashCapture(t, cwd, nil, "mv a.txt b.txt")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	ca := findChange(changes, a)
	cb := findChange(changes, b)
	if ca == nil || cb == nil {
		t.Fatalf("expected changes for both paths, got %+v", changes)
	}
	if !ca.BeforeExists || ca.AfterExists {
		t.Errorf("source change wrong: %+v", ca)
	}
	if cb.BeforeExists || !cb.AfterExists || cb.After != "content" {
		t.Errorf("dest change wrong: %+v", cb)
	}
}

func TestBashCapturesChangeOnFailedExit(t *testing.T) {
	cwd := t.TempDir()
	p := filepath.Join(cwd, "partial.txt")
	changes, res := runBashCapture(t, cwd, nil, "printf x > partial.txt; exit 3")
	if !res.IsError {
		t.Fatalf("expected error exit")
	}
	c := findChange(changes, p)
	if c == nil {
		t.Fatalf("change should be captured despite failure, got %+v", changes)
	}
	if !c.AfterExists || c.BeforeExists {
		t.Errorf("change wrong: %+v", c)
	}
}

func TestBashCapturesAdditionalWritableRoot(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	target := filepath.Join(other, "out.txt")
	command := fmt.Sprintf("printf data > %q", target)
	changes, res := runBashCapture(t, cwd, []string{other}, command)
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, target)
	if c == nil {
		t.Fatalf("no change captured for writable root, got %+v", changes)
	}
	if !c.AfterExists || c.After != "data" {
		t.Errorf("change wrong: %+v", c)
	}
}

func TestBashCapturesDirectoryCreate(t *testing.T) {
	cwd := t.TempDir()
	changes, res := runBashCapture(t, cwd, nil, "mkdir sub && printf x > sub/inner.txt")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	dir := findChange(changes, filepath.Join(cwd, "sub"))
	if dir == nil || !dir.AfterIsDir || dir.BeforeExists {
		t.Fatalf("directory create not captured: %+v", changes)
	}
	inner := findChange(changes, filepath.Join(cwd, "sub", "inner.txt"))
	if inner == nil || !inner.AfterExists || inner.BeforeExists {
		t.Fatalf("nested file create not captured: %+v", changes)
	}
}

func TestBashReadOnlyCommandCapturesNothing(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "keep.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, res := runBashCapture(t, cwd, nil, "echo hello")
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	if len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

func TestBashScriptIndirectModificationCaptured(t *testing.T) {
	cwd := t.TempDir()
	target := filepath.Join(cwd, "written-by-script.txt")
	script := "cat > run.sh <<'EOF'\n#!/bin/sh\nprintf indirect > written-by-script.txt\nEOF\nsh run.sh"
	changes, res := runBashCapture(t, cwd, nil, script)
	if res.IsError {
		t.Fatalf("run error: %s", res.Content)
	}
	c := findChange(changes, target)
	if c == nil {
		t.Fatalf("indirect change not captured, got %+v", changes)
	}
	if !c.AfterExists || c.After != "indirect" {
		t.Errorf("change wrong: %+v", c)
	}
}

// TestBashRewindEndToEnd proves the full loop: Bash captures file changes,
// they are persisted as file_change entries, and a real Rewind restores the
// workspace to its pre-command state (content, deletion, and creation).
func TestBashRewindEndToEnd(t *testing.T) {
	store := session.Store{Root: t.TempDir()}
	project := t.TempDir()
	modified := filepath.Join(project, "modify.txt")
	deleted := filepath.Join(project, "delete.txt")
	if err := os.WriteFile(modified, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deleted, []byte("present"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder, err := store.NewRecorderWithID(project, "51515151-5151-4151-8151-515151515151")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Checkpoint("before-bash", ""); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{
		"command": "printf changed > modify.txt; rm delete.txt; printf brand-new > create.txt",
	})
	res := New().Run(context.Background(), input, tools.Context{
		CWD: project,
		FileChange: func(c tools.FileChange) {
			data, marshalErr := json.Marshal(c)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if appendErr := recorder.Append(session.Entry{Type: "file_change", Content: string(data)}); appendErr != nil {
				t.Fatal(appendErr)
			}
		},
	})
	if res.IsError {
		t.Fatalf("bash error: %s", res.Content)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	created := filepath.Join(project, "create.txt")
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("create.txt should exist after command: %v", err)
	}
	result, ok, err := store.Rewind(recorder.SessionID, "before-bash")
	if err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	if result.FilesRestored != 3 {
		t.Fatalf("expected 3 files restored, got %d", result.FilesRestored)
	}
	if data, err := os.ReadFile(modified); err != nil || string(data) != "original" {
		t.Fatalf("modify.txt = %q err=%v", data, err)
	}
	if data, err := os.ReadFile(deleted); err != nil || string(data) != "present" {
		t.Fatalf("delete.txt not restored: %q err=%v", data, err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("create.txt should be removed by rewind, err=%v", err)
	}
}
