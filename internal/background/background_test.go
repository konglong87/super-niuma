package background

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/defaults"
)

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

func TestStoreCreateKillLogs(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.CreateWithOptions(Options{
		Prompt:          "hello",
		CWD:             "/tmp",
		Model:           "model",
		Provider:        "selected",
		OutputFormat:    "json",
		MaxTurns:        3,
		MaxTokens:       123,
		Resume:          "session-id",
		SessionID:       "11111111-1111-4111-8111-111111111111",
		SessionName:     "named",
		NoPersistence:   true,
		SystemPrompt:    "base",
		AppendSystem:    "extra",
		AllowedTools:    []string{"Read"},
		DeniedTools:     []string{"Bash"},
		PermissionMode:  "deny",
		AdditionalDirs:  []string{"/tmp/extra"},
		SkipPermissions: true,
		RuntimeProfile:  "bare",
		PromptMode:      "code",
		Agent:           "Plan",
		ToolsSpecified:  true,
		EnabledTools:    []string{"Read", "Bash"},
		SettingsInputs:  []string{"settings.json"},
		MCPConfigInputs: []string{"mcp.json"},
		StrictMCPConfig: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Status != "queued" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if jobs[0].Model != "model" || jobs[0].Provider != "selected" || jobs[0].OutputFormat != "json" || jobs[0].MaxTurns != 3 || jobs[0].MaxTokens != 123 || jobs[0].Resume != "session-id" || jobs[0].SessionID != "11111111-1111-4111-8111-111111111111" || jobs[0].SessionName != "named" || !jobs[0].NoPersistence || jobs[0].SystemPrompt != "base" || jobs[0].AppendSystem != "extra" || len(jobs[0].AllowedTools) != 1 || len(jobs[0].DeniedTools) != 1 || jobs[0].PermissionMode != "deny" || len(jobs[0].AdditionalDirs) != 1 || !jobs[0].SkipPermissions || jobs[0].RuntimeProfile != "bare" || jobs[0].PromptMode != "code" || jobs[0].Agent != "Plan" || !jobs[0].ToolsSpecified || strings.Join(jobs[0].EnabledTools, ",") != "Read,Bash" || strings.Join(jobs[0].SettingsInputs, ",") != "settings.json" || strings.Join(jobs[0].MCPConfigInputs, ",") != "mcp.json" || !jobs[0].StrictMCPConfig {
		t.Fatalf("job options = %+v", jobs[0])
	}
	ok, err := store.Kill(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("kill returned false")
	}
	logs, ok, err := store.Logs(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || logs != "" {
		t.Fatalf("logs=%q ok=%v", logs, ok)
	}
}

func TestStoreCreateDefaultsMaxTurns(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("hello", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if job.MaxTurns != defaults.MaxTurns {
		t.Fatalf("MaxTurns = %d, want %d", job.MaxTurns, defaults.MaxTurns)
	}
}

func TestStoreCreateLoopJobRecordsScheduleFields(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.CreateWithOptions(Options{Prompt: "check deploy", CWD: t.TempDir(), Kind: "loop", IntervalSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != "loop" || job.IntervalSeconds != 600 {
		t.Fatalf("job = %+v", job)
	}
	next := time.Now().UTC().Add(10 * time.Minute)
	updated, ok, err := store.RecordLoopRun(job.ID, &next)
	if err != nil || !ok {
		t.Fatalf("RecordLoopRun ok=%v err=%v", ok, err)
	}
	if updated.RunCount != 1 || updated.LastRunAt == nil || updated.NextRunAt == nil || updated.Status != "running" {
		t.Fatalf("updated = %+v", updated)
	}
}

func TestStoreFinishKeepsLoopJobsRunning(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.CreateWithOptions(Options{Prompt: "check deploy", CWD: t.TempDir(), Kind: "loop", IntervalSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	finished, ok, err := store.Finish(job.ID, 0, "")
	if err != nil || !ok {
		t.Fatalf("Finish ok=%v err=%v", ok, err)
	}
	if finished.Status != "running" || finished.FinishedAt == nil {
		t.Fatalf("finished = %+v", finished)
	}
	killed, err := store.Kill(job.ID)
	if err != nil || !killed {
		t.Fatalf("Kill killed=%v err=%v", killed, err)
	}
	found, ok, err := store.Find(job.ID)
	if err != nil || !ok {
		t.Fatalf("Find ok=%v err=%v", ok, err)
	}
	if found.Status != "killed" {
		t.Fatalf("found = %+v", found)
	}
	next := time.Now().UTC().Add(10 * time.Minute)
	recorded, ok, err := store.RecordLoopRun(job.ID, &next)
	if err != nil || !ok {
		t.Fatalf("RecordLoopRun ok=%v err=%v", ok, err)
	}
	if recorded.Status != "killed" || recorded.RunCount != 0 {
		t.Fatalf("recorded = %+v", recorded)
	}
}

func TestStoreStartWritesLogsAndRecordsPID(t *testing.T) {
	store := Store{Root: t.TempDir()}
	job, err := store.Create("hello", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.Start(job.ID, executable, []string{"-test.run=TestBackgroundHelperProcess", "--", "ok"}, append(os.Environ(), "GO_WANT_BACKGROUND_HELPER=1"))
	if err != nil {
		t.Fatal(err)
	}
	if started.PID == 0 || started.Status != "running" || started.StartedAt == nil {
		t.Fatalf("started = %+v", started)
	}

	var logs string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		logs, _, err = store.Logs(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(logs, "helper ok") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("logs = %q", logs)
}

func TestBackgroundHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_BACKGROUND_HELPER") != "1" {
		return
	}
	fmt.Println("helper ok")
	os.Exit(0)
}
