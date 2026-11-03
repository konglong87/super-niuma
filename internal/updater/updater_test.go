package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
)

func TestScheduledCheckStatePathIgnoresClaudeConfigDirForWrites(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(t.TempDir(), ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", legacy)
	t.Setenv("NIUMA_UPDATE_STATE_DIR", "")

	want := filepath.Join(home, ".niuma", "update", "last_check")
	if got := scheduledCheckStatePath(); got != want {
		t.Fatalf("scheduledCheckStatePath = %q, want %q", got, want)
	}
}

type fakeRunner struct {
	outputs map[string][]byte
	errs    map[string]error
	calls   []string
}

func (f *fakeRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if err := f.errs[call]; err != nil {
		return f.outputs[call], err
	}
	return f.outputs[call], nil
}

func TestRunSkipsWhenDisabled(t *testing.T) {
	disabled := false
	runner := &fakeRunner{}
	res := Run(context.Background(), Options{Settings: config.UpdateSettings{Enabled: &disabled}, CWD: "/repo"}, runner)
	if !res.Skipped || res.Reason != "disabled" || len(runner.calls) != 0 {
		t.Fatalf("result=%+v calls=%v", res, runner.calls)
	}
}

func TestRunSkipsDirtyWorktree(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"git rev-parse --is-inside-work-tree": []byte("true\n"),
		"git status --porcelain":              []byte(" M README.md\n"),
	}}
	res := Run(context.Background(), Options{Settings: config.UpdateSettings{RepoDir: "/repo"}, CWD: "/repo"}, runner)
	if !res.Skipped || res.Reason != "dirty worktree" || !strings.Contains(res.Output, "README.md") {
		t.Fatalf("result=%+v", res)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestRunPullsFastForwardWhenClean(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"git rev-parse --is-inside-work-tree": []byte("true\n"),
		"git status --porcelain":              []byte(""),
		"git fetch --quiet":                   []byte(""),
		"git pull --ff-only --quiet":          []byte("updated\n"),
	}}
	res := Run(context.Background(), Options{Settings: config.UpdateSettings{RepoDir: "/repo"}, CWD: "/repo"}, runner)
	if !res.Updated || res.Skipped || !strings.Contains(res.Output, "updated") {
		t.Fatalf("result=%+v", res)
	}
	wantLast := "git pull --ff-only --quiet"
	if runner.calls[len(runner.calls)-1] != wantLast {
		t.Fatalf("last call=%q, want %q", runner.calls[len(runner.calls)-1], wantLast)
	}
}

func TestRunCheckOnlyFetchesWithoutPull(t *testing.T) {
	autoPull := false
	runner := &fakeRunner{outputs: map[string][]byte{
		"git rev-parse --is-inside-work-tree": []byte("true\n"),
		"git status --porcelain":              []byte(""),
		"git fetch --quiet":                   []byte(""),
	}}
	res := Run(context.Background(), Options{Settings: config.UpdateSettings{AutoPull: &autoPull, RepoDir: "/repo"}, CWD: "/repo"}, runner)
	if res.Updated || res.Skipped {
		t.Fatalf("result=%+v", res)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "pull") {
			t.Fatalf("unexpected pull call: %v", runner.calls)
		}
	}
}

func TestRunCheckOnlyUsesVersionSourceWithoutGit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("v9.9.9"))
	}))
	defer server.Close()
	checkOnly := true
	runner := &fakeRunner{}
	res := Run(context.Background(), Options{
		Settings: config.UpdateSettings{CheckOnly: &checkOnly, VersionSourceURL: server.URL},
		Version:  "v1.0.0",
		CWD:      "/repo",
	}, runner)
	if res.Skipped || res.Updated || !strings.Contains(res.Output, "current=v1.0.0 remote=v9.9.9") {
		t.Fatalf("result=%+v", res)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("git should not run for version-source check-only: %v", runner.calls)
	}
}

func TestRunUsesCustomUpdateCommand(t *testing.T) {
	runner := &fakeRunner{outputs: map[string][]byte{
		"git rev-parse --is-inside-work-tree": []byte("true\n"),
		"git status --porcelain":              []byte(""),
		"sh -c make update":                   []byte("custom updated\n"),
	}}
	res := Run(context.Background(), Options{Settings: config.UpdateSettings{RepoDir: "/repo", CustomCommand: "make update"}, CWD: "/repo"}, runner)
	if !res.Updated || res.Skipped || !strings.Contains(res.Output, "custom updated") {
		t.Fatalf("result=%+v", res)
	}
	if got := runner.calls[len(runner.calls)-1]; got != "sh -c make update" {
		t.Fatalf("last call=%q", got)
	}
}

func TestCheckOnStartupHonorsScheduleInterval(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("NIUMA_UPDATE_STATE_DIR", stateDir)
	if err := os.WriteFile(scheduledCheckStatePath(), []byte("not a time"), 0o644); err != nil {
		t.Fatal(err)
	}
	if shouldSkipScheduledCheck(config.UpdateSettings{ScheduleInterval: "daily"}) {
		t.Fatal("invalid timestamp should not skip")
	}
	if err := os.WriteFile(scheduledCheckStatePath(), []byte("2020-01-01T00:00:00Z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if shouldSkipScheduledCheck(config.UpdateSettings{ScheduleInterval: "daily"}) {
		t.Fatal("old timestamp should not skip")
	}
	if err := os.WriteFile(scheduledCheckStatePath(), []byte("2999-01-01T00:00:00Z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !shouldSkipScheduledCheck(config.UpdateSettings{ScheduleInterval: "daily"}) {
		t.Fatal("future timestamp should skip")
	}
}

func TestRunReportsCommandFailure(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string][]byte{
			"git rev-parse --is-inside-work-tree": []byte("true\n"),
			"git status --porcelain":              []byte(""),
			"git fetch --quiet":                   []byte("network down"),
		},
		errs: map[string]error{
			"git fetch --quiet": errors.New("exit status 1"),
		},
	}
	res := Run(context.Background(), Options{Settings: config.UpdateSettings{RepoDir: "/repo"}, CWD: "/repo"}, runner)
	if !res.Skipped || res.Reason != "update failed" || !strings.Contains(res.Output, "network down") {
		t.Fatalf("result=%+v", res)
	}
}
