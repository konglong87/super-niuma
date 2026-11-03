package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildScorecardPassesRequiredScenariosAndFinalReport(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	scorePath := writeFinalScore(t, dir, true)

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:         matrixPath,
		FinalScorePath:     scorePath,
		RequiredScenarios:  defaultRequiredScenarios,
		RequireFinalReport: true,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if !report.OK {
		t.Fatalf("report.OK = false, failures = %v", report.Failures)
	}
	if report.Totals.PassedScenarioCount != len(defaultRequiredScenarios) {
		t.Fatalf("passed scenarios = %d, want %d", report.Totals.PassedScenarioCount, len(defaultRequiredScenarios))
	}
	if report.FinalReport == nil || !report.FinalReport.OK || report.FinalReport.Score != 23 {
		t.Fatalf("final report summary = %+v", report.FinalReport)
	}
}

func TestBuildScorecardFailsWhenRequiredScenarioMissing(t *testing.T) {
	dir := t.TempDir()
	scenarios := removeScenario(defaultRequiredScenarios, "agent-capability-loop-compact")
	matrixPath := writeMatrix(t, dir, scenarios, nil)

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:        matrixPath,
		RequiredScenarios: defaultRequiredScenarios,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "missing required scenario: agent-capability-loop-compact")
	category := report.Categories["capability_loop_runtime"]
	assertContains(t, category.Missing, "agent-capability-loop-compact")
}

func TestBuildScorecardFailsWhenRequiredFinalReportScoreFails(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	scorePath := writeFinalScore(t, dir, false)

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:         matrixPath,
		FinalScorePath:     scorePath,
		RequiredScenarios:  defaultRequiredScenarios,
		RequireFinalReport: true,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "final-report score ok=false")
	if report.FinalReport == nil || report.FinalReport.OK {
		t.Fatalf("final report summary = %+v", report.FinalReport)
	}
}

func TestBuildScorecardFailsWhenRequiredFinalReportScoreMissing(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:         matrixPath,
		RequiredScenarios:  defaultRequiredScenarios,
		RequireFinalReport: true,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "final-report score required but not provided")
}

func TestBuildScorecardPassesRequiredABScorecard(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecard(t, dir, abTotals{
		ScenarioCount:                1,
		OKCount:                      1,
		CapabilityAdvantageCount:     1,
		ContextHygieneAdvantageCount: 1,
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:        matrixPath,
		ABScorecardPath:   abPath,
		RequiredScenarios: defaultRequiredScenarios,
		RequireAB:         true,
		MinABAdvantages:   1,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if !report.OK {
		t.Fatalf("report.OK = false, failures = %v", report.Failures)
	}
	if report.AB == nil || report.AB.CapabilityAdvantageCount != 1 {
		t.Fatalf("AB summary = %+v", report.AB)
	}
}

func TestBuildScorecardFailsWhenRequiredABScorecardMissing(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:        matrixPath,
		RequiredScenarios: defaultRequiredScenarios,
		RequireAB:         true,
		MinABAdvantages:   1,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "A/B scorecard required but not provided")
}

func TestBuildScorecardFailsOnABUnderperformOrCompareErrors(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecard(t, dir, abTotals{
		ScenarioCount:               2,
		OKCount:                     1,
		CapabilityAdvantageCount:    1,
		CapabilityUnderperformCount: 1,
		CompareErrors:               1,
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:        matrixPath,
		ABScorecardPath:   abPath,
		RequiredScenarios: defaultRequiredScenarios,
		RequireAB:         true,
		MinABAdvantages:   1,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "A/B scorecard ok=false")
	assertContains(t, report.Failures, "A/B compare errors present")
	assertContains(t, report.Failures, "A/B capability underperform present")
}

func TestBuildScorecardFailsWhenABAdvantageBelowMinimum(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecard(t, dir, abTotals{
		ScenarioCount:            1,
		OKCount:                  1,
		CapabilityAdvantageCount: 0,
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:        matrixPath,
		ABScorecardPath:   abPath,
		RequiredScenarios: defaultRequiredScenarios,
		MinABAdvantages:   1,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "A/B capability advantage count below 1")
}

func TestBuildScorecardPassesRequiredABScenarios(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecardWithScenarios(t, dir, abTotals{
		ScenarioCount:            2,
		OKCount:                  2,
		CapabilityAdvantageCount: 2,
	}, []abScenario{
		{Scenario: "agent-child-error-side-by-side", OK: true, Summary: filepath.Join(dir, "child-error", "summary.json")},
		{Scenario: "agent-long-output-side-by-side", OK: true, Summary: filepath.Join(dir, "long-output", "summary.json")},
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:          matrixPath,
		ABScorecardPath:     abPath,
		RequiredScenarios:   defaultRequiredScenarios,
		RequiredABScenarios: []string{"agent-long-output-side-by-side", "agent-child-error-side-by-side"},
		RequireAB:           true,
		MinABScenarios:      2,
		MinABAdvantages:     2,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if !report.OK {
		t.Fatalf("report.OK = false, failures = %v", report.Failures)
	}
	if report.AB == nil || len(report.AB.Scenarios) != 2 {
		t.Fatalf("AB scenarios = %+v", report.AB)
	}
}

func TestBuildScorecardFailsWhenRequiredABScenarioMissing(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecardWithScenarios(t, dir, abTotals{
		ScenarioCount:            1,
		OKCount:                  1,
		CapabilityAdvantageCount: 1,
	}, []abScenario{
		{Scenario: "agent-long-output-side-by-side", OK: true},
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:          matrixPath,
		ABScorecardPath:     abPath,
		RequiredScenarios:   defaultRequiredScenarios,
		RequiredABScenarios: []string{"agent-long-output-side-by-side", "agent-child-error-side-by-side"},
		RequireAB:           true,
		MinABAdvantages:     1,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "missing required A/B scenario: agent-child-error-side-by-side")
	assertContains(t, report.AB.MissingRequiredScenarios, "agent-child-error-side-by-side")
}

func TestBuildScorecardFailsWhenRequiredABScenarioFails(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecardWithScenarios(t, dir, abTotals{
		ScenarioCount:            1,
		OKCount:                  0,
		CapabilityAdvantageCount: 1,
	}, []abScenario{
		{Scenario: "agent-child-error-side-by-side", OK: false, Failures: []string{"scenario_summary_not_ok"}},
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:          matrixPath,
		ABScorecardPath:     abPath,
		RequiredScenarios:   defaultRequiredScenarios,
		RequiredABScenarios: []string{"agent-child-error-side-by-side"},
		RequireAB:           true,
		MinABAdvantages:     1,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "failed required A/B scenario: agent-child-error-side-by-side")
	assertContains(t, report.AB.FailedRequiredScenarios, "agent-child-error-side-by-side")
}

func TestBuildScorecardFailsWhenABScenarioCountBelowMinimum(t *testing.T) {
	dir := t.TempDir()
	matrixPath := writeMatrix(t, dir, defaultRequiredScenarios, nil)
	abPath := writeABScorecardWithScenarios(t, dir, abTotals{
		ScenarioCount:            1,
		OKCount:                  1,
		CapabilityAdvantageCount: 1,
	}, []abScenario{
		{Scenario: "agent-long-output-side-by-side", OK: true},
	})

	report, err := buildScorecard(scorecardInputs{
		MatrixPath:        matrixPath,
		ABScorecardPath:   abPath,
		RequiredScenarios: defaultRequiredScenarios,
		MinABScenarios:    2,
	})
	if err != nil {
		t.Fatalf("buildScorecard() error = %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	assertContains(t, report.Failures, "A/B scenario count below 2")
}

func writeMatrix(t *testing.T, dir string, okScenarios []string, failed map[string]string) string {
	t.Helper()
	status := "ok"
	var scenarios []matrixScenario
	for _, name := range okScenarios {
		scenarioStatus := "ok"
		if failed != nil && failed[name] != "" {
			scenarioStatus = failed[name]
			status = "failed"
		}
		scenarios = append(scenarios, matrixScenario{
			Scenario: name,
			Status:   scenarioStatus,
			Dump:     filepath.Join(dir, name+".jsonl"),
			Report:   filepath.Join(dir, name+"-report.json"),
			Log:      filepath.Join(dir, name+".log"),
		})
	}
	report := matrixReport{
		OK:            status == "ok",
		Status:        status,
		OutDir:        dir,
		ScenarioCount: len(scenarios),
		SummaryJSONL:  filepath.Join(dir, "matrix-summary.jsonl"),
		Scenarios:     scenarios,
	}
	return writeJSON(t, dir, "matrix-report.json", report)
}

func writeFinalScore(t *testing.T, dir string, ok bool) string {
	t.Helper()
	score := finalReportScore{
		OK:              ok,
		Score:           23,
		RequiredScore:   20,
		RunLog:          filepath.Join(dir, "run.log"),
		ScoredScope:     "visible_output_before_json_logs",
		Anchors:         []string{"internal/query/query.go:1", "internal/tools/task/task.go:2", "internal/tools/agent/agent.go:3", "internal/agentruntime/runtime.go:4"},
		FunctionAnchors: []string{"runtimeStatusText", "runtimeTodoStatus", "runtimePlanStatus", "runtimeAgentTaskStatus", "AgentCreate", "AgentGet"},
		SectionAnchors: map[string]string{
			"evidence":     "internal/query/query.go:1",
			"unknowns":     "unknown is explicit",
			"verification": "go test ./... -count=1",
			"risks":        "risk is explicit",
			"next_action":  "rerun acceptance",
		},
	}
	if !ok {
		score.Missing = []string{"section:verification"}
	}
	return writeJSON(t, dir, "final-score.json", score)
}

func writeABScorecard(t *testing.T, dir string, totals abTotals) string {
	t.Helper()
	return writeABScorecardWithScenarios(t, dir, totals, nil)
}

func writeABScorecardWithScenarios(t *testing.T, dir string, totals abTotals, scenarios []abScenario) string {
	t.Helper()
	return writeJSON(t, dir, "ab-scorecard.json", abScorecard{
		OK:        totals.CapabilityUnderperformCount == 0 && totals.CompareErrors == 0,
		Scenarios: scenarios,
		Totals: abTotals{
			ScenarioCount:                totals.ScenarioCount,
			OKCount:                      totals.OKCount,
			CapabilityAdvantageCount:     totals.CapabilityAdvantageCount,
			CapabilityUnderperformCount:  totals.CapabilityUnderperformCount,
			CompareErrors:                totals.CompareErrors,
			CompareWarnings:              totals.CompareWarnings,
			CompareInfo:                  totals.CompareInfo,
			ContextHygieneAdvantageCount: totals.ContextHygieneAdvantageCount,
		},
	})
}

func writeJSON(t *testing.T, dir, name string, value any) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func removeScenario(items []string, remove string) []string {
	var out []string
	for _, item := range items {
		if item != remove {
			out = append(out, item)
		}
	}
	return out
}

func assertContains(t *testing.T, items []string, want string) {
	t.Helper()
	for _, item := range items {
		if item == want {
			return
		}
	}
	t.Fatalf("%q not found in %v", want, items)
}
