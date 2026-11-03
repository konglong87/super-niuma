package bashoutput

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
)

type payload struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Running      bool   `json:"running"`
	Output       string `json:"output"`
	MoreOutput   bool   `json:"more_output"`
	ExitCode     *int   `json:"exit_code"`
	Error        string `json:"error"`
	LogPath      string `json:"log_path"`
	Outcome      string `json:"outcome"`
	Instructions string `json:"instructions"`
}

func newStore(t *testing.T) background.Store {
	t.Helper()
	return background.Store{Root: t.TempDir()}
}

func runTool(t *testing.T, tool tools.Tool, input map[string]any) (tools.Result, payload) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Run(context.Background(), raw, tools.Context{})
	var out payload
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		return res, payload{}
	}
	return res, out
}

func appendLog(t *testing.T, job background.Job, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(job.LogPath), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(job.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// startSleeper starts a real child the test can kill. The returned func blocks
// until it is reaped, which is the only way to observe the kill: an unreaped
// child is still visible to a liveness probe as a zombie.
func startSleeper(t *testing.T) (int, func()) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	reaped := false
	t.Cleanup(func() {
		if reaped {
			return
		}
		_ = cmd.Process.Kill()
		<-waited
	})
	return cmd.Process.Pid, func() {
		t.Helper()
		select {
		case <-waited:
			reaped = true
		case <-time.After(5 * time.Second):
			t.Fatal("process was still running 5s after KillShell")
		}
	}
}

// Polling must not replay: without a cursor every BashOutput call would refeed
// the whole log into the context window.
func TestBashOutputReturnsOnlyOutputAppendedSinceLastCall(t *testing.T) {
	store := newStore(t)
	job, err := store.Create("build", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	appendLog(t, job, "line-one\n")

	tool := Tool{store: store}
	res, first := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if res.IsError {
		t.Fatalf("Run() error: %s", res.Content)
	}
	if first.Output != "line-one\n" || first.ID != job.ID {
		t.Fatalf("first payload = %+v", first)
	}

	appendLog(t, job, "line-two\n")
	_, second := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if second.Output != "line-two\n" {
		t.Fatalf("second Output = %q, want only the newly appended line", second.Output)
	}

	_, third := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if third.Output != "" {
		t.Fatalf("third Output = %q, want empty when nothing was appended", third.Output)
	}
}

func TestBashOutputReportsRunningThenFinishedProcess(t *testing.T) {
	store := newStore(t)
	job, err := store.Create("server", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := startSleeper(t)
	if _, _, err := store.MarkRunning(job.ID, pid); err != nil {
		t.Fatal(err)
	}
	tool := Tool{store: store}
	_, running := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if !running.Running || running.Status != "running" || running.ExitCode != nil {
		t.Fatalf("running payload = %+v", running)
	}

	if _, _, err := store.Finish(job.ID, 3, "boom"); err != nil {
		t.Fatal(err)
	}
	_, done := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if done.Running || done.Status != "failed" || done.ExitCode == nil || *done.ExitCode != 3 || done.Error != "boom" {
		t.Fatalf("finished payload = %+v", done)
	}
}

func TestBashOutputRejectsUnknownAndEmptyID(t *testing.T) {
	tool := Tool{store: newStore(t)}
	res, _ := runTool(t, tool, map[string]any{"bash_id": "bg_missing"})
	if !res.IsError || !strings.Contains(res.Content, "bg_missing") {
		t.Fatalf("unknown id result = %+v", res)
	}
	empty, _ := runTool(t, tool, map[string]any{"bash_id": "  "})
	if !empty.IsError || !strings.Contains(empty.Content, "bash_id") {
		t.Fatalf("empty id result = %+v", empty)
	}
}

// A long log must be handed over in bounded chunks that the model can drain,
// never dropped by the caller's own result truncation.
func TestBashOutputChunksLongLogsAcrossCalls(t *testing.T) {
	store := newStore(t)
	job, err := store.Create("noisy", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	appendLog(t, job, strings.Repeat("y", background.MaxOutputChunkBytes+10))
	tool := Tool{store: store}
	_, first := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if len(first.Output) != background.MaxOutputChunkBytes || !first.MoreOutput {
		t.Fatalf("first Output len=%d more=%v", len(first.Output), first.MoreOutput)
	}
	if !strings.Contains(first.Instructions, "BashOutput") {
		t.Fatalf("Instructions did not tell the model to call again: %q", first.Instructions)
	}
	_, second := runTool(t, tool, map[string]any{"bash_id": job.ID})
	if len(second.Output) != 10 || second.MoreOutput {
		t.Fatalf("second Output len=%d more=%v", len(second.Output), second.MoreOutput)
	}
}

func TestKillShellTerminatesRunningProcess(t *testing.T) {
	store := newStore(t)
	job, err := store.Create("server", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pid, waitReaped := startSleeper(t)
	if _, _, err := store.MarkRunning(job.ID, pid); err != nil {
		t.Fatal(err)
	}
	res, out := runTool(t, KillTool{store: store}, map[string]any{"shell_id": job.ID})
	if res.IsError {
		t.Fatalf("Run() error: %s", res.Content)
	}
	if out.Outcome != "killed" || out.Status != "killed" {
		t.Fatalf("payload = %+v", out)
	}
	waitReaped()
}

func TestKillShellReportsUnknownID(t *testing.T) {
	res, out := runTool(t, KillTool{store: newStore(t)}, map[string]any{"shell_id": "bg_missing"})
	if !res.IsError {
		t.Fatalf("unknown id must be an error result: %+v", res)
	}
	if out.Outcome != "not_found" {
		t.Fatalf("payload = %+v", out)
	}
}

func TestKillShellReportsAlreadyFinishedJobWithoutError(t *testing.T) {
	store := newStore(t)
	job, err := store.Create("build", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Finish(job.ID, 0, ""); err != nil {
		t.Fatal(err)
	}
	res, out := runTool(t, KillTool{store: store}, map[string]any{"shell_id": job.ID})
	if res.IsError {
		t.Fatalf("a job that already exited is not an error: %+v", res)
	}
	if out.Outcome != "not_running" || out.Status != "completed" {
		t.Fatalf("payload = %+v", out)
	}
}

// After a kill, BashOutput must still hand over whatever the process wrote
// before dying, and report it as no longer running.
func TestBashOutputAfterKillDrainsRemainingOutput(t *testing.T) {
	store := newStore(t)
	job, err := store.Create("server", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pid, waitReaped := startSleeper(t)
	if _, _, err := store.MarkRunning(job.ID, pid); err != nil {
		t.Fatal(err)
	}
	appendLog(t, job, "wrote-before-kill\n")
	if res, _ := runTool(t, KillTool{store: store}, map[string]any{"shell_id": job.ID}); res.IsError {
		t.Fatalf("KillShell error: %s", res.Content)
	}
	waitReaped()
	_, out := runTool(t, Tool{store: store}, map[string]any{"bash_id": job.ID})
	if out.Running || out.Status != "killed" || out.Output != "wrote-before-kill\n" {
		t.Fatalf("payload after kill = %+v", out)
	}
}

// AUDIT-P1-19: a tool without MaxResultSizeChars bypasses the result-size
// truncation entirely.
func TestToolsDeclareResultSizeLimit(t *testing.T) {
	for _, tool := range []tools.Tool{New(), NewKillShell()} {
		t.Run(tool.Name(), func(t *testing.T) {
			if got := tools.EffectiveResultLimit(tool, 200_000); got != toolresult.DefaultLimit {
				t.Fatalf("EffectiveResultLimit() = %d, want %d", got, toolresult.DefaultLimit)
			}
		})
	}
}

func TestConstructorsUseTheDefaultBackgroundStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := background.DefaultStore().Root
	if got := New().store.Root; got != want {
		t.Fatalf("BashOutput store root = %q, want %q", got, want)
	}
	if got := NewKillShell().store.Root; got != want {
		t.Fatalf("KillShell store root = %q, want %q", got, want)
	}
}

// The descriptions are the only place the model learns that polling a
// background job goes through BashOutput instead of Read(log_path), and that
// KillShell is how a runaway process is stopped.
func TestDescriptionsTellTheModelWhenToUseThem(t *testing.T) {
	output := New().Description()
	for _, want := range []string{"run_in_background", "Read", "log", "KillShell"} {
		if !strings.Contains(output, want) {
			t.Fatalf("BashOutput description is missing %q:\n%s", want, output)
		}
	}
	kill := NewKillShell().Description()
	for _, want := range []string{"BashOutput", "background"} {
		if !strings.Contains(kill, want) {
			t.Fatalf("KillShell description is missing %q:\n%s", want, kill)
		}
	}
}
