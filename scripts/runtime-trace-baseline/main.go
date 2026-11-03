package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/server"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const datasetSchemaVersion = "runtime-trace-dataset-v1"

type pathList []string

func (p *pathList) String() string { return strings.Join(*p, ",") }
func (p *pathList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("artifact path cannot be empty")
	}
	*p = append(*p, value)
	return nil
}

type dataset struct {
	SchemaVersion string       `json:"schema_version"`
	Name          string       `json:"name"`
	Runs          []datasetRun `json:"runs"`
}

type datasetRun struct {
	CaseID   string `json:"case_id"`
	Group    string `json:"group"`
	Artifact string `json:"artifact"`
}

type aggregate struct {
	Runs             int     `json:"runs"`
	Completions      int     `json:"completions"`
	CompletionRate   float64 `json:"completion_rate"`
	VerifiedRuns     int     `json:"verified_runs"`
	VerifiedRate     float64 `json:"verified_rate"`
	TaskWallP50MS    int64   `json:"task_wall_p50_ms"`
	TaskWallP95MS    int64   `json:"task_wall_p95_ms"`
	ModelWallP50MS   int64   `json:"model_wall_p50_ms"`
	ModelWallP95MS   int64   `json:"model_wall_p95_ms"`
	ToolWallP50MS    int64   `json:"tool_wall_p50_ms"`
	ToolWallP95MS    int64   `json:"tool_wall_p95_ms"`
	AverageTokens    float64 `json:"average_tokens"`
	AverageTurns     float64 `json:"average_turns"`
	AverageToolCalls float64 `json:"average_tool_calls"`
}

type delta struct {
	CompletionRatePoints float64 `json:"completion_rate_points"`
	VerifiedRatePoints   float64 `json:"verified_rate_points"`
	TaskWallP50MS        int64   `json:"task_wall_p50_ms"`
	TaskWallP95MS        int64   `json:"task_wall_p95_ms"`
	ModelWallP50MS       int64   `json:"model_wall_p50_ms"`
	ToolWallP50MS        int64   `json:"tool_wall_p50_ms"`
	AverageTokens        float64 `json:"average_tokens"`
	AverageTurns         float64 `json:"average_turns"`
	AverageToolCalls     float64 `json:"average_tool_calls"`
}

type report struct {
	SchemaVersion string    `json:"schema_version"`
	Dataset       string    `json:"dataset,omitempty"`
	Before        aggregate `json:"before"`
	After         aggregate `json:"after"`
	Delta         delta     `json:"delta_after_minus_before"`
}

func main() {
	var before, after pathList
	datasetPath := flag.String("dataset", "", "runtime-trace-dataset-v1 manifest")
	outputPath := flag.String("output", "", "optional JSON report path")
	flag.Var(&before, "before", "before artifact file or directory; may be repeated")
	flag.Var(&after, "after", "after artifact file or directory; may be repeated")
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	name, err := loadDataset(*datasetPath, &before, &after)
	if err != nil {
		fatalf("load dataset: %v", err)
	}
	if len(before) == 0 || len(after) == 0 {
		fatalf("both --before and --after artifacts are required")
	}
	result, err := buildReport(name, before, after)
	if err != nil {
		fatalf("build report: %v", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatalf("marshal report: %v", err)
	}
	data = append(data, '\n')
	if strings.TrimSpace(*outputPath) == "" {
		_, _ = os.Stdout.Write(data)
		return
	}
	if err := os.WriteFile(*outputPath, data, 0o600); err != nil {
		fatalf("write report: %v", err)
	}
}

func loadDataset(path string, before, after *pathList) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest dataset
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.SchemaVersion != datasetSchemaVersion {
		return "", fmt.Errorf("unsupported dataset schema %q", manifest.SchemaVersion)
	}
	base := filepath.Dir(path)
	type caseGroups struct{ before, after bool }
	cases := make(map[string]caseGroups, len(manifest.Runs))
	for index, run := range manifest.Runs {
		artifact := strings.TrimSpace(run.Artifact)
		caseID := strings.TrimSpace(run.CaseID)
		if caseID == "" || artifact == "" {
			return "", fmt.Errorf("run %d requires case_id and artifact", index+1)
		}
		if !filepath.IsAbs(artifact) {
			artifact = filepath.Join(base, artifact)
		}
		groups := cases[caseID]
		switch strings.ToLower(strings.TrimSpace(run.Group)) {
		case "before":
			if groups.before {
				return "", fmt.Errorf("duplicate before run for case_id %q", caseID)
			}
			groups.before = true
			*before = append(*before, artifact)
		case "after":
			if groups.after {
				return "", fmt.Errorf("duplicate after run for case_id %q", caseID)
			}
			groups.after = true
			*after = append(*after, artifact)
		default:
			return "", fmt.Errorf("run %d has invalid group %q", index+1, run.Group)
		}
		cases[caseID] = groups
	}
	for caseID, groups := range cases {
		if !groups.before || !groups.after {
			return "", fmt.Errorf("case_id %q requires exactly one before and one after run", caseID)
		}
	}
	return manifest.Name, nil
}

func buildReport(name string, beforePaths, afterPaths []string) (report, error) {
	before, err := readArtifacts(beforePaths)
	if err != nil {
		return report{}, err
	}
	after, err := readArtifacts(afterPaths)
	if err != nil {
		return report{}, err
	}
	left, right := summarize(before), summarize(after)
	return report{
		SchemaVersion: "runtime-trace-baseline-v1",
		Dataset:       name,
		Before:        left,
		After:         right,
		Delta: delta{
			CompletionRatePoints: right.CompletionRate - left.CompletionRate,
			VerifiedRatePoints:   right.VerifiedRate - left.VerifiedRate,
			TaskWallP50MS:        right.TaskWallP50MS - left.TaskWallP50MS,
			TaskWallP95MS:        right.TaskWallP95MS - left.TaskWallP95MS,
			ModelWallP50MS:       right.ModelWallP50MS - left.ModelWallP50MS,
			ToolWallP50MS:        right.ToolWallP50MS - left.ToolWallP50MS,
			AverageTokens:        right.AverageTokens - left.AverageTokens,
			AverageTurns:         right.AverageTurns - left.AverageTurns,
			AverageToolCalls:     right.AverageToolCalls - left.AverageToolCalls,
		},
	}, nil
}

func readArtifacts(paths []string) ([]server.RuntimeTraceArtifact, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, path)
			continue
		}
		entries, err := filepath.Glob(filepath.Join(path, "*.json"))
		if err != nil {
			return nil, err
		}
		files = append(files, entries...)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, errors.New("no artifact files found")
	}
	artifacts := make([]server.RuntimeTraceArtifact, 0, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var artifact server.RuntimeTraceArtifact
		if err := json.Unmarshal(data, &artifact); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if artifact.SchemaVersion != telemetry.SchemaVersionRuntimeTraceV1 {
			return nil, fmt.Errorf("%s: unsupported schema %q", path, artifact.SchemaVersion)
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func summarize(items []server.RuntimeTraceArtifact) aggregate {
	task, model, tool := make([]int64, 0, len(items)), make([]int64, 0, len(items)), make([]int64, 0, len(items))
	var out aggregate
	var tokens, turns, calls int64
	out.Runs = len(items)
	for _, item := range items {
		if item.Run.Status == server.RuntimeTraceRunStatusCompleted {
			out.Completions++
			if runtimeTraceVerified(item.Quality) {
				out.VerifiedRuns++
			}
		}
		task = append(task, item.Summary.TaskWallMS)
		model = append(model, item.Summary.ModelWallMS)
		tool = append(tool, item.Summary.ToolWallMS)
		tokens += int64(item.Summary.TotalTokens)
		turns += int64(item.Summary.Turns)
		calls += int64(item.Summary.ToolCalls)
	}
	out.CompletionRate = ratio(out.Completions, out.Runs)
	out.VerifiedRate = ratio(out.VerifiedRuns, out.Runs)
	out.TaskWallP50MS, out.TaskWallP95MS = percentile(task, 0.50), percentile(task, 0.95)
	out.ModelWallP50MS, out.ModelWallP95MS = percentile(model, 0.50), percentile(model, 0.95)
	out.ToolWallP50MS, out.ToolWallP95MS = percentile(tool, 0.50), percentile(tool, 0.95)
	out.AverageTokens, out.AverageTurns, out.AverageToolCalls = average(tokens, out.Runs), average(turns, out.Runs), average(calls, out.Runs)
	return out
}

func runtimeTraceVerified(quality server.RuntimeTraceQuality) bool {
	if quality.FinalVerificationPassed != nil {
		return *quality.FinalVerificationPassed
	}
	return quality.CompletionVerified && (!quality.TestsRun || quality.TestsPassed)
}

func percentile(values []int64, quantile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	return sorted[index]
}

func ratio(value, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) / float64(total)
}

func average(value int64, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(value) / float64(total)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
