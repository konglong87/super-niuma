package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

var defaultRequiredScenarios = []string{
	"task-capability-loop",
	"agent-capability-loop",
	"agent-capability-loop-resume",
	"agent-capability-loop-compact",
	"agent-capability-loop-compact-facts",
	"agent-capability-loop-failed-compact",
	"agent-capability-loop-cancelled-compact",
	"agent-long-output-compact-resume",
	"agent-detached-running-resume",
	"agent-message-worktree-resume",
	"resume-replacement",
	"tool-result-heavy",
	"read-tool-result",
}

type matrixReport struct {
	OK            bool             `json:"ok"`
	Status        string           `json:"status"`
	OutDir        string           `json:"out_dir"`
	ScenarioCount int              `json:"scenario_count"`
	SummaryJSONL  string           `json:"summary_jsonl"`
	Scenarios     []matrixScenario `json:"scenarios"`
}

type matrixScenario struct {
	Scenario string `json:"scenario"`
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Dump     string `json:"dump,omitempty"`
	Report   string `json:"report,omitempty"`
	Log      string `json:"log,omitempty"`
	Error    string `json:"error,omitempty"`
}

type finalReportScore struct {
	OK              bool              `json:"ok"`
	Score           int               `json:"score"`
	RequiredScore   int               `json:"required_score"`
	RunLog          string            `json:"run_log"`
	ScoredScope     string            `json:"scored_scope"`
	Missing         []string          `json:"missing,omitempty"`
	Anchors         []string          `json:"anchors,omitempty"`
	FunctionAnchors []string          `json:"function_anchors,omitempty"`
	SectionAnchors  map[string]string `json:"section_anchors,omitempty"`
	ForbiddenHits   []string          `json:"forbidden_hits,omitempty"`
}

type abScorecard struct {
	OK        bool         `json:"ok"`
	Totals    abTotals     `json:"totals"`
	Scenarios []abScenario `json:"scenarios"`
}

type abTotals struct {
	ScenarioCount                int `json:"scenario_count"`
	OKCount                      int `json:"ok_count"`
	CapabilityAdvantageCount     int `json:"capability_advantage_count"`
	CapabilityUnderperformCount  int `json:"capability_underperform_count"`
	CompareErrors                int `json:"compare_errors"`
	CompareWarnings              int `json:"compare_warnings"`
	CompareInfo                  int `json:"compare_info"`
	ContextHygieneAdvantageCount int `json:"context_hygiene_advantage_count"`
}

type abScenario struct {
	Scenario string   `json:"scenario"`
	OK       bool     `json:"ok"`
	Failures []string `json:"failures,omitempty"`
	Summary  string   `json:"summary,omitempty"`
}

type categoryReport struct {
	Total   int      `json:"total"`
	Passed  int      `json:"passed"`
	Failed  []string `json:"failed,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

type scorecardReport struct {
	OK                  bool                      `json:"ok"`
	GeneratedAt         string                    `json:"generated_at"`
	PromptMatrix        string                    `json:"prompt_matrix,omitempty"`
	FinalReportScore    string                    `json:"final_report_score,omitempty"`
	ABScorecard         string                    `json:"ab_scorecard,omitempty"`
	RequireFinalReport  bool                      `json:"require_final_report"`
	RequireAB           bool                      `json:"require_ab"`
	MinABAdvantages     int                       `json:"min_ab_advantages"`
	MinABScenarios      int                       `json:"min_ab_scenarios"`
	RequiredScenarios   []string                  `json:"required_scenarios"`
	RequiredABScenarios []string                  `json:"required_ab_scenarios,omitempty"`
	Totals              scorecardTotals           `json:"totals"`
	Categories          map[string]categoryReport `json:"categories"`
	Failures            []string                  `json:"failures,omitempty"`
	Matrix              *matrixSummary            `json:"matrix,omitempty"`
	FinalReport         *finalReportSummary       `json:"final_report,omitempty"`
	AB                  *abSummary                `json:"ab,omitempty"`
}

type scorecardTotals struct {
	RequiredScenarioCount int `json:"required_scenario_count"`
	PassedScenarioCount   int `json:"passed_scenario_count"`
	MissingScenarioCount  int `json:"missing_scenario_count"`
	FailedScenarioCount   int `json:"failed_scenario_count"`
	CategoryCount         int `json:"category_count"`
	PassedCategoryCount   int `json:"passed_category_count"`
}

type matrixSummary struct {
	OK            bool             `json:"ok"`
	Status        string           `json:"status"`
	OutDir        string           `json:"out_dir,omitempty"`
	ScenarioCount int              `json:"scenario_count"`
	Failed        []matrixScenario `json:"failed,omitempty"`
}

type finalReportSummary struct {
	OK              bool     `json:"ok"`
	Score           int      `json:"score"`
	RequiredScore   int      `json:"required_score"`
	RunLog          string   `json:"run_log,omitempty"`
	ScoredScope     string   `json:"scored_scope,omitempty"`
	AnchorCount     int      `json:"anchor_count"`
	FunctionAnchors int      `json:"function_anchor_count"`
	Sections        []string `json:"sections,omitempty"`
	Missing         []string `json:"missing,omitempty"`
	ForbiddenHits   []string `json:"forbidden_hits,omitempty"`
}

type abSummary struct {
	OK                           bool         `json:"ok"`
	ScenarioCount                int          `json:"scenario_count"`
	OKCount                      int          `json:"ok_count"`
	CapabilityAdvantageCount     int          `json:"capability_advantage_count"`
	CapabilityUnderperformCount  int          `json:"capability_underperform_count"`
	ContextHygieneAdvantageCount int          `json:"context_hygiene_advantage_count"`
	CompareErrors                int          `json:"compare_errors"`
	CompareWarnings              int          `json:"compare_warnings"`
	CompareInfo                  int          `json:"compare_info"`
	MissingRequiredScenarios     []string     `json:"missing_required_scenarios,omitempty"`
	FailedRequiredScenarios      []string     `json:"failed_required_scenarios,omitempty"`
	Scenarios                    []abScenario `json:"scenarios,omitempty"`
}

func main() {
	matrixPath := flag.String("prompt-matrix", "", "path to prompt-acceptance-matrix matrix-report.json")
	finalScorePath := flag.String("final-report-score", "", "optional final-report evidence score JSON")
	abScorecardPath := flag.String("ab-scorecard", "", "optional capability A/B scorecard JSON from scripts/capability-ab-eval-matrix.sh")
	outPath := flag.String("out", "", "optional path to write scorecard JSON")
	requiredCSV := flag.String("require-scenarios", strings.Join(defaultRequiredScenarios, ","), "comma-separated scenario names required from the prompt matrix")
	requireFinalReport := flag.Bool("require-final-report", false, "fail if --final-report-score is missing or not ok")
	requireAB := flag.Bool("require-ab", false, "fail if --ab-scorecard is missing or not ok")
	minABAdvantages := flag.Int("min-ab-advantages", 0, "minimum capability/context advantage count required when --ab-scorecard is provided")
	minABScenarios := flag.Int("min-ab-scenarios", 0, "minimum A/B scenario count required when --ab-scorecard is provided")
	requiredABScenariosCSV := flag.String("require-ab-scenarios", "", "comma-separated A/B scenario names required from --ab-scorecard")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./scripts/agent-capability-scorecard --prompt-matrix <matrix-report.json> [flags]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *matrixPath == "" || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	required := splitCSV(*requiredCSV)
	requiredABScenarios := splitCSV(*requiredABScenariosCSV)
	rep, err := buildScorecard(scorecardInputs{
		MatrixPath:          *matrixPath,
		FinalScorePath:      *finalScorePath,
		ABScorecardPath:     *abScorecardPath,
		RequiredScenarios:   required,
		RequiredABScenarios: requiredABScenarios,
		RequireFinalReport:  *requireFinalReport,
		RequireAB:           *requireAB,
		MinABAdvantages:     *minABAdvantages,
		MinABScenarios:      *minABScenarios,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode scorecard: %v\n", err)
		os.Exit(2)
	}
	data = append(data, '\n')
	if *outPath != "" {
		if err := os.WriteFile(*outPath, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write scorecard: %v\n", err)
			os.Exit(2)
		}
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "write stdout: %v\n", err)
		os.Exit(2)
	}
	if !rep.OK {
		os.Exit(1)
	}
}

type scorecardInputs struct {
	MatrixPath          string
	FinalScorePath      string
	ABScorecardPath     string
	RequiredScenarios   []string
	RequiredABScenarios []string
	RequireFinalReport  bool
	RequireAB           bool
	MinABAdvantages     int
	MinABScenarios      int
}

func buildScorecard(inputs scorecardInputs) (scorecardReport, error) {
	requiredScenarios := append([]string(nil), inputs.RequiredScenarios...)
	requiredABScenarios := append([]string(nil), inputs.RequiredABScenarios...)
	matrix, err := readJSON[matrixReport](inputs.MatrixPath)
	if err != nil {
		return scorecardReport{}, fmt.Errorf("read prompt matrix: %w", err)
	}
	byScenario := map[string]matrixScenario{}
	var failedMatrix []matrixScenario
	for _, scenario := range matrix.Scenarios {
		byScenario[scenario.Scenario] = scenario
		if scenario.Status != "ok" {
			failedMatrix = append(failedMatrix, scenario)
		}
	}

	categories := map[string][]string{
		"capability_loop_runtime": {
			"task-capability-loop",
			"agent-capability-loop",
			"agent-capability-loop-resume",
			"agent-capability-loop-compact",
			"agent-capability-loop-compact-facts",
			"agent-capability-loop-failed-compact",
			"agent-capability-loop-cancelled-compact",
		},
		"context_stability": {
			"agent-long-output-compact-resume",
			"agent-detached-running-resume",
			"agent-message-worktree-resume",
			"resume-replacement",
		},
		"tool_result_hygiene": {
			"tool-result-heavy",
			"read-tool-result",
		},
	}
	categoryReports := make(map[string]categoryReport, len(categories))
	for name, scenarios := range categories {
		categoryReports[name] = evaluateCategory(scenarios, byScenario)
	}

	var failures []string
	missing, failed := evaluateRequired(requiredScenarios, byScenario)
	for _, scenario := range missing {
		failures = append(failures, "missing required scenario: "+scenario)
	}
	for _, scenario := range failed {
		failures = append(failures, "failed required scenario: "+scenario)
	}
	if !matrix.OK {
		failures = append(failures, "prompt matrix ok=false")
	}

	finalSummary, err := loadFinalReportSummary(inputs.FinalScorePath, inputs.RequireFinalReport)
	if err != nil {
		return scorecardReport{}, err
	}
	if inputs.RequireFinalReport && finalSummary == nil {
		failures = append(failures, "final-report score required but not provided")
	}
	if finalSummary != nil && !finalSummary.OK {
		failures = append(failures, "final-report score ok=false")
	}

	ab, err := loadABSummary(inputs.ABScorecardPath, requiredABScenarios)
	if err != nil {
		return scorecardReport{}, err
	}
	if inputs.RequireAB && ab == nil {
		failures = append(failures, "A/B scorecard required but not provided")
	}
	if ab != nil {
		if !ab.OK {
			failures = append(failures, "A/B scorecard ok=false")
		}
		if ab.CompareErrors > 0 {
			failures = append(failures, "A/B compare errors present")
		}
		if ab.CapabilityUnderperformCount > 0 {
			failures = append(failures, "A/B capability underperform present")
		}
		if inputs.MinABAdvantages > 0 && ab.CapabilityAdvantageCount < inputs.MinABAdvantages {
			failures = append(failures, fmt.Sprintf("A/B capability advantage count below %d", inputs.MinABAdvantages))
		}
		if inputs.MinABScenarios > 0 && ab.ScenarioCount < inputs.MinABScenarios {
			failures = append(failures, fmt.Sprintf("A/B scenario count below %d", inputs.MinABScenarios))
		}
		if len(ab.MissingRequiredScenarios) > 0 {
			for _, scenario := range ab.MissingRequiredScenarios {
				failures = append(failures, "missing required A/B scenario: "+scenario)
			}
		}
		if len(ab.FailedRequiredScenarios) > 0 {
			for _, scenario := range ab.FailedRequiredScenarios {
				failures = append(failures, "failed required A/B scenario: "+scenario)
			}
		}
	}

	passedCategories := 0
	for name, category := range categoryReports {
		if len(category.Failed) == 0 && len(category.Missing) == 0 && category.Total > 0 {
			passedCategories++
			continue
		}
		failures = append(failures, "category not passing: "+name)
	}
	sort.Strings(failures)
	sort.Strings(requiredScenarios)
	sort.Strings(requiredABScenarios)

	return scorecardReport{
		OK:                  len(failures) == 0,
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339),
		PromptMatrix:        inputs.MatrixPath,
		FinalReportScore:    inputs.FinalScorePath,
		ABScorecard:         inputs.ABScorecardPath,
		RequireFinalReport:  inputs.RequireFinalReport,
		RequireAB:           inputs.RequireAB,
		MinABAdvantages:     inputs.MinABAdvantages,
		MinABScenarios:      inputs.MinABScenarios,
		RequiredScenarios:   requiredScenarios,
		RequiredABScenarios: requiredABScenarios,
		Totals: scorecardTotals{
			RequiredScenarioCount: len(requiredScenarios),
			PassedScenarioCount:   len(requiredScenarios) - len(missing) - len(failed),
			MissingScenarioCount:  len(missing),
			FailedScenarioCount:   len(failed),
			CategoryCount:         len(categoryReports),
			PassedCategoryCount:   passedCategories,
		},
		Categories: categoryReports,
		Failures:   failures,
		Matrix: &matrixSummary{
			OK:            matrix.OK,
			Status:        matrix.Status,
			OutDir:        matrix.OutDir,
			ScenarioCount: matrix.ScenarioCount,
			Failed:        failedMatrix,
		},
		FinalReport: finalSummary,
		AB:          ab,
	}, nil
}

func readJSON[T any](path string) (T, error) {
	var value T
	data, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func evaluateRequired(required []string, byScenario map[string]matrixScenario) ([]string, []string) {
	var missing []string
	var failed []string
	for _, want := range required {
		scenario, ok := byScenario[want]
		if !ok {
			missing = append(missing, want)
			continue
		}
		if scenario.Status != "ok" {
			failed = append(failed, want)
		}
	}
	sort.Strings(missing)
	sort.Strings(failed)
	return missing, failed
}

func evaluateCategory(scenarios []string, byScenario map[string]matrixScenario) categoryReport {
	report := categoryReport{Total: len(scenarios)}
	for _, want := range scenarios {
		scenario, ok := byScenario[want]
		if !ok {
			report.Missing = append(report.Missing, want)
			continue
		}
		if scenario.Status != "ok" {
			report.Failed = append(report.Failed, want)
			continue
		}
		report.Passed++
	}
	sort.Strings(report.Missing)
	sort.Strings(report.Failed)
	return report
}

func loadFinalReportSummary(path string, requireFinal bool) (*finalReportSummary, error) {
	if path == "" {
		return nil, nil
	}
	score, err := readJSON[finalReportScore](path)
	if err != nil {
		return nil, fmt.Errorf("read final report score: %w", err)
	}
	sections := make([]string, 0, len(score.SectionAnchors))
	for section := range score.SectionAnchors {
		sections = append(sections, section)
	}
	sort.Strings(sections)
	if requireFinal && score.RequiredScore == 0 {
		return nil, fmt.Errorf("final report score missing required_score")
	}
	return &finalReportSummary{
		OK:              score.OK,
		Score:           score.Score,
		RequiredScore:   score.RequiredScore,
		RunLog:          score.RunLog,
		ScoredScope:     score.ScoredScope,
		AnchorCount:     len(score.Anchors),
		FunctionAnchors: len(score.FunctionAnchors),
		Sections:        sections,
		Missing:         score.Missing,
		ForbiddenHits:   score.ForbiddenHits,
	}, nil
}

func loadABSummary(path string, required []string) (*abSummary, error) {
	if path == "" {
		return nil, nil
	}
	scorecard, err := readJSON[abScorecard](path)
	if err != nil {
		return nil, fmt.Errorf("read A/B scorecard: %w", err)
	}
	byScenario := map[string]abScenario{}
	for _, scenario := range scorecard.Scenarios {
		byScenario[scenario.Scenario] = scenario
	}
	var missing []string
	var failed []string
	for _, want := range required {
		scenario, ok := byScenario[want]
		if !ok {
			missing = append(missing, want)
			continue
		}
		if !scenario.OK {
			failed = append(failed, want)
		}
	}
	sort.Strings(missing)
	sort.Strings(failed)
	scenarios := append([]abScenario(nil), scorecard.Scenarios...)
	sort.Slice(scenarios, func(i, j int) bool {
		return scenarios[i].Scenario < scenarios[j].Scenario
	})
	return &abSummary{
		OK:                           scorecard.OK,
		ScenarioCount:                scorecard.Totals.ScenarioCount,
		OKCount:                      scorecard.Totals.OKCount,
		CapabilityAdvantageCount:     scorecard.Totals.CapabilityAdvantageCount,
		CapabilityUnderperformCount:  scorecard.Totals.CapabilityUnderperformCount,
		ContextHygieneAdvantageCount: scorecard.Totals.ContextHygieneAdvantageCount,
		CompareErrors:                scorecard.Totals.CompareErrors,
		CompareWarnings:              scorecard.Totals.CompareWarnings,
		CompareInfo:                  scorecard.Totals.CompareInfo,
		MissingRequiredScenarios:     missing,
		FailedRequiredScenarios:      failed,
		Scenarios:                    scenarios,
	}, nil
}

func splitCSV(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}
