package background

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, job Job, text string) {
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

// startSleeper starts a real child process the test can kill. The returned
// func blocks until the child is reaped, which is the only way to observe the
// kill: an unreaped child stays visible to a signal-0 probe as a zombie.
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
			t.Fatal("process was still running 5s after Terminate")
		}
	}
}

// stalePID returns the PID of a process that has already exited and been
// reaped, so probing it must report "not running".
func stalePID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	return pid
}

// ReadNewOutput must advance a persisted read cursor: the model polls a
// background job repeatedly and re-reading the whole log every time would
// re-feed the same bytes into the context window on every poll.
func TestReadNewOutputReturnsOnlyBytesAppendedSinceLastRead(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeLog(t, job, "first\n")

	chunk, ok, err := store.ReadNewOutput(job.ID)
	if err != nil || !ok {
		t.Fatalf("ReadNewOutput ok=%v err=%v", ok, err)
	}
	if chunk.Text != "first\n" {
		t.Fatalf("first read Text = %q, want %q", chunk.Text, "first\n")
	}

	again, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Text != "" {
		t.Fatalf("second read Text = %q, want empty (no new output)", again.Text)
	}

	writeLog(t, job, "second\n")
	third, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if third.Text != "second\n" {
		t.Fatalf("third read Text = %q, want only the appended line", third.Text)
	}
}

// A single call must not hand back an unbounded log. The cursor may only
// advance past bytes actually returned, otherwise the tail is lost forever.
func TestReadNewOutputChunksLargeLogsWithoutLosingBytes(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", MaxOutputChunkBytes+500)
	writeLog(t, job, body)

	first, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Text) != MaxOutputChunkBytes || !first.MoreOutput {
		t.Fatalf("first chunk len=%d more=%v, want len=%d more=true", len(first.Text), first.MoreOutput, MaxOutputChunkBytes)
	}
	second, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Text) != 500 || second.MoreOutput {
		t.Fatalf("second chunk len=%d more=%v, want len=500 more=false", len(second.Text), second.MoreOutput)
	}
	if first.Text+second.Text != body {
		t.Fatal("chunked reads did not reassemble into the full log")
	}
}

func TestReadNewOutputReportsMissingJob(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if _, ok, err := store.ReadNewOutput("bg_missing"); ok || err != nil {
		t.Fatalf("ReadNewOutput ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// A truncated/rotated log must restart from zero instead of returning nothing
// forever because the cursor sits past EOF.
func TestReadNewOutputRestartsWhenLogShrinks(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeLog(t, job, "aaaaaaaaaa")
	if _, _, err := store.ReadNewOutput(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(job.LogPath, []byte("bb"), 0600); err != nil {
		t.Fatal(err)
	}
	chunk, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Text != "bb" {
		t.Fatalf("Text = %q, want %q after the log shrank", chunk.Text, "bb")
	}
}

// Running must reflect the real process, not just the recorded status: a job
// whose supervising process died keeps status "running" forever.
func TestReadNewOutputReportsRunningFromLiveProcess(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.MarkRunning(job.ID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	chunk, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !chunk.Running {
		t.Fatal("Running = false for a live PID with status running")
	}

	if _, _, err := store.Finish(job.ID, 0, ""); err != nil {
		t.Fatal(err)
	}
	done, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Running {
		t.Fatal("Running = true for a completed job")
	}
}

func TestReadNewOutputReportsNotRunningForStalePID(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Status stays "running" while the PID no longer exists: the supervising
	// process died without recording an exit.
	if _, _, err := store.MarkRunning(job.ID, stalePID(t)); err != nil {
		t.Fatal(err)
	}
	chunk, _, err := store.ReadNewOutput(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Running {
		t.Fatal("Running = true for a status-running job whose process is gone")
	}
}

func TestTerminateKillsLiveProcess(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pid, waitReaped := startSleeper(t)
	if _, _, err := store.MarkRunning(job.ID, pid); err != nil {
		t.Fatal(err)
	}
	killed, outcome, err := store.Terminate(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != TerminateKilled {
		t.Fatalf("outcome = %q, want %q", outcome, TerminateKilled)
	}
	if killed.Status != "killed" || killed.FinishedAt == nil {
		t.Fatalf("job = %+v", killed)
	}
	waitReaped()
}

func TestTerminateReportsAlreadyFinishedJob(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Finish(job.ID, 0, ""); err != nil {
		t.Fatal(err)
	}
	after, outcome, err := store.Terminate(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != TerminateNotRunning {
		t.Fatalf("outcome = %q, want %q", outcome, TerminateNotRunning)
	}
	if after.Status != "completed" {
		t.Fatalf("Terminate rewrote the status of a finished job: %+v", after)
	}
}

// A job still marked running whose process is already gone was not killed by
// us; reporting "killed" would tell the model it stopped something it did not.
func TestTerminateReportsNotRunningForStalePID(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.MarkRunning(job.ID, stalePID(t)); err != nil {
		t.Fatal(err)
	}
	after, outcome, err := store.Terminate(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != TerminateNotRunning {
		t.Fatalf("outcome = %q, want %q", outcome, TerminateNotRunning)
	}
	if after.Status != "killed" {
		t.Fatalf("a job that is no longer running must stop being reported as running: %+v", after)
	}
}

func TestTerminateReportsMissingJob(t *testing.T) {
	store := Store{Root: t.TempDir()}
	_, outcome, err := store.Terminate("bg_missing")
	if err != nil {
		t.Fatal(err)
	}
	if outcome != TerminateNotFound {
		t.Fatalf("outcome = %q, want %q", outcome, TerminateNotFound)
	}
}

// Kill is the pre-existing API used by `/kill` and the runtime HTTP route; it
// must keep its contract while delegating to Terminate.
func TestKillStillReportsFoundJobs(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("job", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.Kill(job.ID)
	if err != nil || !ok {
		t.Fatalf("Kill ok=%v err=%v", ok, err)
	}
	missing, err := store.Kill("bg_missing")
	if err != nil || missing {
		t.Fatalf("Kill(missing) ok=%v err=%v", missing, err)
	}
}
