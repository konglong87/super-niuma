package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestBuildReportAggregatesBeforeAfter(t *testing.T) {
	dir := t.TempDir()
	before := writeArtifact(t, dir, "before.json", "completed", 100, 70, 20, 1000, 3, 4)
	afterA := writeArtifact(t, dir, "after-a.json", "completed", 60, 40, 10, 700, 2, 3)
	afterB := writeArtifact(t, dir, "after-b.json", "failed", 80, 50, 15, 900, 3, 5)

	report, err := buildReport("smoke", []string{before}, []string{afterA, afterB})
	if err != nil {
		t.Fatal(err)
	}
	if report.Before.TaskWallP50MS != 100 || report.After.TaskWallP50MS != 60 || report.After.TaskWallP95MS != 80 {
		t.Fatalf("task wall aggregates = before %+v after %+v", report.Before, report.After)
	}
	if report.Before.CompletionRate != 1 || report.After.CompletionRate != 0.5 || report.Delta.CompletionRatePoints != -0.5 {
		t.Fatalf("completion aggregates = %+v", report)
	}
	if report.After.AverageTokens != 800 || report.Delta.AverageTurns != -0.5 {
		t.Fatalf("averages = %+v", report)
	}
}

func TestLoadDatasetRequiresExactlyPairedCaseIDs(t *testing.T) {
	tests := []struct {
		name string
		runs string
	}{
		{name: "missing after", runs: `[{"case_id":"read","group":"before","artifact":"before.json"}]`},
		{name: "mismatched cases", runs: `[{"case_id":"read","group":"before","artifact":"before.json"},{"case_id":"write","group":"after","artifact":"after.json"}]`},
		{name: "duplicate before", runs: `[{"case_id":"read","group":"before","artifact":"before-a.json"},{"case_id":"read","group":"before","artifact":"before-b.json"},{"case_id":"read","group":"after","artifact":"after.json"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			manifestPath := filepath.Join(dir, "dataset.json")
			data := []byte(`{"schema_version":"runtime-trace-dataset-v1","name":"fixed","runs":` + tt.runs + `}`)
			if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			var before, after pathList
			if _, err := loadDataset(manifestPath, &before, &after); err == nil {
				t.Fatal("invalid dataset unexpectedly accepted")
			}
		})
	}
}

func TestSummarizeSeparatesRuntimeCompletionFromVerifiedOutcome(t *testing.T) {
	items := []server.RuntimeTraceArtifact{
		{Run: server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatusCompleted}, Quality: server.RuntimeTraceQuality{CompletionVerified: true, TestsRun: true, TestsPassed: true}},
		{Run: server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatusCompleted}},
		{Run: server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatusCompleted}, Quality: server.RuntimeTraceQuality{CompletionVerified: true, TestsRun: true}},
		{Run: server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatusFailed}},
	}
	got := summarize(items)
	if got.Completions != 3 || got.CompletionRate != 0.75 || got.VerifiedRuns != 1 || got.VerifiedRate != 0.25 {
		t.Fatalf("aggregate = %+v", got)
	}
}

func TestSummarizePrefersExplicitFinalVerification(t *testing.T) {
	passed, failed := true, false
	items := []server.RuntimeTraceArtifact{
		{Run: server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatusCompleted}, Quality: server.RuntimeTraceQuality{CompletionVerified: true, TestsRun: true, FinalVerificationPassed: &passed}},
		{Run: server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatusCompleted}, Quality: server.RuntimeTraceQuality{CompletionVerified: true, TestsRun: true, TestsPassed: true, FinalVerificationPassed: &failed}},
	}
	got := summarize(items)
	if got.VerifiedRuns != 1 || got.VerifiedRate != 0.5 {
		t.Fatalf("aggregate = %+v", got)
	}
}

func TestLoadDatasetResolvesRelativeArtifacts(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "dataset.json")
	data := []byte(`{"schema_version":"runtime-trace-dataset-v1","name":"fixed","runs":[{"case_id":"read","group":"before","artifact":"before.json"},{"case_id":"read","group":"after","artifact":"after.json"}]}`)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var before, after pathList
	name, err := loadDataset(manifestPath, &before, &after)
	if err != nil {
		t.Fatal(err)
	}
	if name != "fixed" || before[0] != filepath.Join(dir, "before.json") || after[0] != filepath.Join(dir, "after.json") {
		t.Fatalf("dataset = %q before=%v after=%v", name, before, after)
	}
}

func writeArtifact(t *testing.T, dir, name, status string, task, model, tool int64, tokens, turns, calls int) string {
	t.Helper()
	artifact := server.RuntimeTraceArtifact{
		SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
		Run:           server.RuntimeTraceRun{Status: server.RuntimeTraceRunStatus(status)},
		Summary: server.RuntimeTraceSummary{
			TaskWallMS: task, ModelWallMS: model, ToolWallMS: tool,
			TotalTokens: tokens, Turns: turns, ToolCalls: calls,
		},
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
