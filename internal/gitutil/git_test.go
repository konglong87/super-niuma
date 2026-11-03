package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchAndDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	run(t, tmp, "git", "init")
	run(t, tmp, "git", "config", "user.email", "test@example.com")
	run(t, tmp, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run(t, tmp, "git", "add", ".")
	run(t, tmp, "git", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	branch, err := Branch(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if branch == "" {
		t.Fatal("branch is empty")
	}
	status, err := StatusShort(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "a.txt") {
		t.Fatalf("status = %s", status)
	}
	diff, err := Diff(context.Background(), tmp, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "-one") || !strings.Contains(diff, "+two") {
		t.Fatalf("diff = %s", diff)
	}
	run(t, tmp, "git", "add", "a.txt")
	cachedDiff, err := DiffWithOptions(context.Background(), tmp, DiffOptions{Cached: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cachedDiff, "-one") || !strings.Contains(cachedDiff, "+two") {
		t.Fatalf("cached diff = %s", cachedDiff)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
