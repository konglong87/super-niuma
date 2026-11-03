package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/background"
)

func TestStoreCreateFindDisableAndRecordRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "check deploy", CWD: t.TempDir(), Kind: "loop", IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Root: root}
	schedule, err := store.Create(Options{
		Prompt:          "check deploy",
		CWD:             bg.CWD,
		Kind:            "loop",
		Spec:            "@every 5m",
		IntervalSeconds: 300,
		Model:           "test-model",
		Provider:        "test-provider",
		MaxTurns:        3,
	}, bg)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.ID == "" || schedule.BackgroundID != bg.ID || !schedule.Enabled || schedule.Spec != "@every 5m" || schedule.Provider != "test-provider" {
		t.Fatalf("schedule = %+v", schedule)
	}
	found, ok, err := store.Find(bg.ID)
	if err != nil || !ok {
		t.Fatalf("Find ok=%v err=%v", ok, err)
	}
	if found.ID != schedule.ID {
		t.Fatalf("found = %+v", found)
	}
	next := time.Now().UTC().Add(5 * time.Minute)
	updated, ok, err := store.RecordRun(schedule.ID, &next, "")
	if err != nil || !ok {
		t.Fatalf("RecordRun ok=%v err=%v", ok, err)
	}
	if updated.RunCount != 1 || updated.LastRunAt == nil || updated.NextRunAt == nil {
		t.Fatalf("updated = %+v", updated)
	}
	disabled, ok, err := store.Disable(bg.ID)
	if err != nil || !ok {
		t.Fatalf("Disable ok=%v err=%v", ok, err)
	}
	if disabled.Enabled {
		t.Fatalf("disabled = %+v", disabled)
	}
}

func TestStoreUpdatesSessionMonitorProjectionWithoutDuplicate(t *testing.T) {
	root := t.TempDir()
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "session monitor", Kind: "session-monitor", IntervalSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Root: root}
	created, err := store.Create(Options{Prompt: "session monitor", Kind: "session-monitor", Spec: "@every 5m", IntervalSeconds: 300, AllowedTools: []string{"SessionGet", "ChannelReport"}}, bg)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok, err := store.UpdateLoop(created.ID, Options{Kind: "session-monitor", Spec: "@every 10m", IntervalSeconds: 600, AllowedTools: []string{"SessionGet", "ChannelReport"}})
	if err != nil || !ok {
		t.Fatalf("update ok=%v err=%v", ok, err)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if updated.Kind != "session-monitor" || updated.Spec != "@every 10m" || updated.IntervalSeconds != 600 || len(updated.AllowedTools) != 2 {
		t.Fatalf("updated=%#v", updated)
	}
}

func TestStoreRejectsInvalidScheduleSpecBeforePersistence(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if _, err := store.EnsureProjection("bad", Options{Kind: "session-monitor", Spec: "@every 10000000000s"}); err == nil {
		t.Fatal("EnsureProjection() error = nil")
	}
	items, err := store.List()
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestDefaultStoreIgnoresClaudeConfigDirForWrites(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(t.TempDir(), ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", legacy)

	store := DefaultStore()
	want := filepath.Join(home, ".niuma")
	if store.Root != want {
		t.Fatalf("Root = %q, want %q", store.Root, want)
	}
}

func TestEnsureDaemonReturnsExistingLivePID(t *testing.T) {
	store := Store{Root: t.TempDir()}
	previousMatcher := isSchedulerDaemonProcess
	isSchedulerDaemonProcess = func(int, daemonMeta) bool { return true }
	t.Cleanup(func() { isSchedulerDaemonProcess = previousMatcher })
	if err := os.MkdirAll(filepath.Dir(store.DaemonPIDPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.DaemonPIDPath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	size, mtime := executableFingerprint("unused")
	if err := store.writeDaemonMeta(daemonMeta{PID: os.Getpid(), Executable: "unused", ExecutableSize: size, ExecutableMTime: mtime, EnvironmentFingerprint: daemonEnvironmentFingerprint(os.Environ())}); err != nil {
		t.Fatal(err)
	}
	pid, started, err := store.EnsureDaemon("unused", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() || started {
		t.Fatalf("pid=%d started=%v", pid, started)
	}
}

func TestEnsureDaemonDoesNotReusePIDWithWrongProcessIdentity(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(store.DaemonPIDPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.DaemonPIDPath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	size, mtime := executableFingerprint("unused")
	if err := store.writeDaemonMeta(daemonMeta{PID: os.Getpid(), Executable: "unused", ExecutableSize: size, ExecutableMTime: mtime, EnvironmentFingerprint: daemonEnvironmentFingerprint(os.Environ())}); err != nil {
		t.Fatal(err)
	}
	pid, started, err := store.EnsureDaemon("unused", os.Environ())
	if !errors.Is(err, ErrDaemonEnvironmentMismatch) || pid != 0 || started {
		t.Fatalf("pid=%d started=%v err=%v", pid, started, err)
	}
}

func TestReusableDaemonPIDRejectsMissingMetadataForOtherProcess(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(store.DaemonPIDPath()), 0755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=TestSchedulerSleepHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_SCHEDULER_SLEEP_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	if err := os.WriteFile(store.DaemonPIDPath(), []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if pid, ok, err := store.reusableDaemonPID("/new/path", daemonEnvironmentFingerprint(os.Environ())); ok || pid != 0 || !errors.Is(err, ErrDaemonEnvironmentMismatch) {
		t.Fatalf("pid=%d ok=%v err=%v", pid, ok, err)
	}
	if _, err := os.Stat(store.DaemonPIDPath()); err != nil {
		t.Fatalf("pid file should remain for fail-closed diagnosis, err=%v", err)
	}
}

func TestSchedulerSleepHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SCHEDULER_SLEEP_HELPER") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
}

func TestEnsureDaemonCanBeDisabledForTests(t *testing.T) {
	store := Store{Root: t.TempDir()}
	t.Setenv("NIUMA_DISABLE_SCHEDULER", "1")
	pid, started, err := store.EnsureDaemon("unused", os.Environ())
	if !errors.Is(err, ErrDaemonDisabled) || pid != 0 || started {
		t.Fatalf("pid=%d started=%v err=%v", pid, started, err)
	}
}

func TestEnsureDaemonRejectsStaleEnvironmentForLiveProcess(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(store.DaemonPIDPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.DaemonPIDPath(), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	oldEnv := append(os.Environ(), "NIUMA_MYSQL_DSN=mysql://old-secret")
	newEnv := append(os.Environ(), "NIUMA_MYSQL_DSN=mysql://new-secret")
	size, mtime := executableFingerprint("unused")
	if err := store.writeDaemonMeta(daemonMeta{PID: os.Getpid(), Executable: "unused", ExecutableSize: size, ExecutableMTime: mtime, EnvironmentFingerprint: daemonEnvironmentFingerprint(oldEnv)}); err != nil {
		t.Fatal(err)
	}
	pid, started, err := store.EnsureDaemon("unused", newEnv)
	if !errors.Is(err, ErrDaemonEnvironmentMismatch) || pid != 0 || started {
		t.Fatalf("pid=%d started=%v err=%v", pid, started, err)
	}
	meta, ok := store.readDaemonMeta()
	if !ok || meta.EnvironmentFingerprint != daemonEnvironmentFingerprint(oldEnv) {
		t.Fatalf("metadata should remain unchanged for fail-closed repair: %#v ok=%v", meta, ok)
	}
}

func TestEnsureDaemonStoresRedactedEnvironmentFingerprint(t *testing.T) {
	store := Store{Root: t.TempDir()}
	env := append(os.Environ(),
		"NIUMA_MYSQL_DSN=mysql://user:dsn-secret@db/golang_cc",
		"NIUMA_CHANNEL_PAYLOAD_KEY=payload-secret",
		"NIUMA_CHANNEL_MODEL_PROVIDER=provider-a",
		"NIUMA_CHANNEL_MODEL=model-a",
	)
	previous := startSchedulerDaemon
	startSchedulerDaemon = func(string, []string, io.Writer) (int, error) { return os.Getpid(), nil }
	t.Cleanup(func() { startSchedulerDaemon = previous })
	if _, started, err := store.EnsureDaemon("scheduler", env); err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	data, err := os.ReadFile(store.DaemonMetaPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || strings.Contains(string(data), "payload-secret") || strings.Contains(string(data), "mysql://user:dsn-secret@db/golang_cc") {
		t.Fatalf("daemon metadata exposed environment value: %s", data)
	}
	meta, ok := store.readDaemonMeta()
	if !ok || meta.EnvironmentFingerprint != daemonEnvironmentFingerprint(env) {
		t.Fatalf("meta=%#v ok=%v", meta, ok)
	}
}

func TestEnsureDaemonConcurrentCallsStartOnce(t *testing.T) {
	store := Store{Root: t.TempDir()}
	previous := startSchedulerDaemon
	previousMatcher := isSchedulerDaemonProcess
	var startMu sync.Mutex
	starts := 0
	startSchedulerDaemon = func(string, []string, io.Writer) (int, error) {
		startMu.Lock()
		starts++
		startMu.Unlock()
		time.Sleep(25 * time.Millisecond)
		return os.Getpid(), nil
	}
	isSchedulerDaemonProcess = func(int, daemonMeta) bool { return true }
	t.Cleanup(func() {
		startSchedulerDaemon = previous
		isSchedulerDaemonProcess = previousMatcher
	})

	const callers = 8
	results := make(chan error, callers)
	start := make(chan struct{})
	for range callers {
		go func() {
			<-start
			pid, _, err := store.EnsureDaemon("scheduler", os.Environ())
			if err == nil && pid != os.Getpid() {
				err = fmt.Errorf("pid=%d, want %d", pid, os.Getpid())
			}
			results <- err
		}()
	}
	close(start)
	for range callers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	startMu.Lock()
	defer startMu.Unlock()
	if starts != 1 {
		t.Fatalf("daemon starts=%d, want 1", starts)
	}
}

func TestEnsureDaemonStopsStartedProcessWhenIdentityPublicationFails(t *testing.T) {
	store := Store{Root: t.TempDir()}
	previousStart := startSchedulerDaemon
	previousStop := stopSchedulerDaemon
	startSchedulerDaemon = func(string, []string, io.Writer) (int, error) {
		dir := filepath.Dir(store.DaemonPIDPath())
		if err := os.RemoveAll(dir); err != nil {
			return 0, err
		}
		if err := os.WriteFile(dir, []byte("not-a-directory"), 0600); err != nil {
			return 0, err
		}
		return 4321, nil
	}
	stopped := 0
	stopSchedulerDaemon = func(pid int) error {
		stopped = pid
		return nil
	}
	t.Cleanup(func() {
		startSchedulerDaemon = previousStart
		stopSchedulerDaemon = previousStop
	})

	pid, started, err := store.EnsureDaemon("scheduler", os.Environ())
	if err == nil || pid != 4321 || !started {
		t.Fatalf("pid=%d started=%v err=%v", pid, started, err)
	}
	if stopped != 4321 {
		t.Fatalf("stopped pid=%d, want 4321", stopped)
	}
}

type recordingExecutor struct {
	ch chan string
}

func (e recordingExecutor) RunSchedule(ctx context.Context, schedule Schedule) error {
	select {
	case e.ch <- schedule.ID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRunnerExecutesDueSchedule(t *testing.T) {
	root := t.TempDir()
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "tick", CWD: t.TempDir(), Kind: "loop", IntervalSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Root: root}
	schedule, err := store.Create(Options{Prompt: "tick", CWD: bg.CWD, Kind: "loop", Spec: "@every 1s", IntervalSeconds: 1}, bg)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Store: &store, Executor: recordingExecutor{ch: ch}, MaxParallel: 1, LogWriter: io.Discard}).Run(ctx)
	}()
	select {
	case got := <-ch:
		if got != schedule.ID {
			t.Fatalf("schedule id = %s, want %s", got, schedule.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not execute schedule")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRunnerExecutesRecurringScheduleMoreThanOnce(t *testing.T) {
	root := t.TempDir()
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "tick", CWD: t.TempDir(), Kind: "loop", IntervalSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Root: root}
	schedule, err := store.Create(Options{Prompt: "tick", CWD: bg.CWD, Kind: "loop", Spec: "@every 1s", IntervalSeconds: 1}, bg)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Store: &store, Executor: recordingExecutor{ch: ch}, MaxParallel: 1, LogWriter: io.Discard}).Run(ctx)
	}()
	for i := 0; i < 2; i++ {
		select {
		case got := <-ch:
			if got != schedule.ID {
				t.Fatalf("schedule id = %s, want %s", got, schedule.ID)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("runner executed schedule %d times, want at least 2", i)
		}
	}
	var updated Schedule
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		updated, ok, err = store.Find(schedule.ID)
		if err != nil || !ok {
			t.Fatalf("Find schedule ok=%v err=%v", ok, err)
		}
		if updated.RunCount >= 2 && updated.LastRunAt != nil && updated.NextRunAt != nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if updated.RunCount < 2 || updated.LastRunAt == nil || updated.NextRunAt == nil {
		t.Fatalf("updated = %+v", updated)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRunnerReloadsNewSchedules(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	ch := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Store: &store, Executor: recordingExecutor{ch: ch}, MaxParallel: 1, ReloadInterval: 50 * time.Millisecond, LogWriter: io.Discard}).Run(ctx)
	}()
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "reload", CWD: t.TempDir(), Kind: "loop", IntervalSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := store.Create(Options{Prompt: "reload", CWD: bg.CWD, Kind: "loop", Spec: "@every 1s", IntervalSeconds: 1}, bg)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		if got != schedule.ID {
			t.Fatalf("schedule id = %s, want %s", got, schedule.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not reload new schedule")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestRunnerReloadsChangedSessionMonitorSpec(t *testing.T) {
	root := t.TempDir()
	bgStore := background.Store{Root: root}
	bg, err := bgStore.CreateWithOptions(background.Options{Prompt: "session monitor", CWD: t.TempDir(), Kind: "session-monitor", IntervalSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Root: root}
	schedule, err := store.Create(Options{Prompt: "session monitor", CWD: bg.CWD, Kind: "session-monitor", Spec: "@every 10s", IntervalSeconds: 10}, bg)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Store: &store, Executor: recordingExecutor{ch: ch}, MaxParallel: 1, ReloadInterval: 25 * time.Millisecond, LogWriter: io.Discard}).Run(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	if _, ok, err := store.UpdateLoop(schedule.ID, Options{Kind: "session-monitor", Spec: "@every 1s", IntervalSeconds: 1}); err != nil || !ok {
		t.Fatalf("update ok=%v err=%v", ok, err)
	}
	select {
	case got := <-ch:
		if got != schedule.ID {
			t.Fatalf("schedule id = %s, want %s", got, schedule.ID)
		}
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("runner did not reload changed monitor spec")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop")
	}
}

func TestStoreEventsAndRunHistory(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}
	if err := store.AppendEvent(Event{Type: "run_started", ScheduleID: "sched_1", BackgroundID: "bg_1", Prompt: "tick", CWD: root}); err != nil {
		t.Fatal(err)
	}
	events, offset, err := store.ReadEventsAfter(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "run_started" || offset <= 0 {
		t.Fatalf("events=%+v offset=%d", events, offset)
	}
	endOffset, err := store.EventsEndOffset()
	if err != nil {
		t.Fatal(err)
	}
	if endOffset != offset {
		t.Fatalf("end offset = %d, want %d", endOffset, offset)
	}
	events, next, err := store.ReadEventsAfter(offset, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || next != offset {
		t.Fatalf("events after offset=%+v next=%d offset=%d", events, next, offset)
	}
	finished := time.Now().UTC()
	if err := store.AppendRun(RunRecord{ScheduleID: "sched_1", BackgroundID: "bg_1", Prompt: "tick", CWD: root, Status: "completed", StartedAt: finished.Add(-time.Second), FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRuns("bg_1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ScheduleID != "sched_1" || runs[0].Status != "completed" {
		t.Fatalf("runs=%+v", runs)
	}
}
