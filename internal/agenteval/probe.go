package agenteval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tools/bash"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
)

// runGoldenProbeProfile 用真实模型（settings.json 的 provider 配置）跑黄金 git
// 工作流 fixture，度量提示遵从度：GatePreflights、轮次、工作流是否完成。
// 探针模式不做预算断言——模型不遵从是数据，不是失败；只有基础设施错误
// （fixture 构建失败、API 传输错误）才标记 failed。口径与决策门见
// docs/superpowers/plans/2026-07-16-gate-trigger-rate-measurement.md。
func runGoldenProbeProfile(ctx context.Context, opts Options, started time.Time) (Report, error) {
	report := Report{
		SuiteID:     "niuma-golden-probe",
		Status:      "passed",
		StartedAt:   started.UTC().Format(time.RFC3339Nano),
		Environment: Environment{CWD: opts.CWD, Suite: "golden", Mode: "live-model-probe"},
	}
	cfg := config.LoadForCWD(opts.CWD)
	if strings.TrimSpace(opts.Provider) != "" {
		selected, err := cfg.SelectProvider(opts.Provider)
		if err != nil {
			return Report{}, err
		}
		cfg = selected
	}
	model := strings.TrimSpace(firstNonEmpty(cfg.SelectedProviderModel, config.ResolveModel(opts.CWD, "")))
	if strings.TrimSpace(firstNonEmpty(cfg.APIKey, cfg.AuthToken)) == "" || model == "" {
		report.Status = "skipped"
		report.Total = 1
		report.Skipped = 1
		report.Cases = []CaseResult{{
			ID:     "golden_probe_environment",
			Type:   "golden_probe",
			Status: "skipped",
			Checks: []CheckResult{{
				Name:   "env.provider",
				Status: "skipped",
				Detail: "configure a provider (settings.json or --provider) with model and apiKey/authToken to run the golden probe",
			}},
		}}
		return finishReport(report, started, opts)
	}
	client := anthropic.NewClient(cfg)
	runs := opts.Runs
	if runs <= 0 {
		runs = 3
	}
	cases := GoldenSuite().Cases
	report.Total = len(cases) * runs
	report.Cases = make([]CaseResult, 0, report.Total)
	for _, testCase := range cases {
		for run := 1; run <= runs; run++ {
			result := runGoldenProbeRun(ctx, client, model, testCase, run)
			report.Cases = append(report.Cases, result)
			if result.Status == "passed" {
				report.Passed++
			} else {
				report.Failed++
				report.Status = "failed"
			}
		}
	}
	return finishReport(report, started, opts)
}

func runGoldenProbeRun(ctx context.Context, client query.MessageStreamer, model string, testCase Case, run int) CaseResult {
	runID := fmt.Sprintf("%s_run%d", testCase.ID, run)
	result := CaseResult{ID: runID, Type: "golden_probe", Status: "passed", Evidence: map[string]any{"model": model}}
	runCtx := observability.WithRequestValues(ctx, "eval-probe-"+runID, "eval-user", "eval-tenant")

	// 目录名保留 niuma-agent-eval 标记，让触发率统计脚本自动排除探针 transcript。
	workspace, err := os.MkdirTemp("", "niuma-agent-eval-probe-*")
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer os.RemoveAll(workspace)
	repoDir, originDir, err := setupGitWorkflowFixture(workspace, testCase.Type == "git_conflict_recovery")
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	registry := tools.NewRegistry(fileread.New(), bash.New())
	recorder, err := session.DefaultStore().NewRecorder(repoDir)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer recorder.Close()
	querySession := query.New(client, registry, query.Options{
		Model:           model,
		MaxTurns:        24,
		CWD:             repoDir,
		Recorder:        recorder,
		TraceID:         observability.TraceID(runCtx),
		ToolResultLimit: 32 * 1024,
	})
	response, err := querySession.Run(runCtx, testCase.Prompt, io.Discard)
	turnsExhausted := err != nil && strings.Contains(err.Error(), "max turns reached")
	if err != nil && !turnsExhausted {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}

	metrics := collectCaseMetrics(response)
	result.Metrics = &metrics
	status, statusErr := gitOutput(repoDir, "status", "--porcelain")
	wantCommits := "2"
	if testCase.Type == "git_conflict_recovery" {
		wantCommits = "3"
	}
	commits, commitsErr := gitOutput(originDir, "rev-list", "--count", "main")
	completed := statusErr == nil && status == "" && commitsErr == nil && commits == wantCommits

	// 遵从度观测量全部进 Evidence，不进 addCheck：模型不遵从不算探针失败。
	// gate 摩擦 = auto-preflight（有 marker）+ plain block（无 marker、只有
	// reminder 文本）两类；只数 GatePreflights 会漏掉 plain block（例如模型
	// 把核查与 commit 链在一条命令里被拦），导致假的"零摩擦"。
	gateBlocks := countGateBlocks(response.ToolCalls)
	frictionTotal := metrics.GatePreflights + gateBlocks
	result.Evidence["gate_preflights"] = metrics.GatePreflights
	result.Evidence["gate_blocks"] = gateBlocks
	result.Evidence["gate_friction_total"] = frictionTotal
	result.Evidence["turns"] = metrics.Turns
	result.Evidence["turns_exhausted"] = turnsExhausted
	result.Evidence["workflow_completed"] = completed
	result.Evidence["origin_commits"] = commits
	result.Evidence["git_status"] = status
	result.Evidence["gate_free_and_completed"] = completed && frictionTotal == 0
	result.Evidence["tool_commands"] = bashCommands(response.ToolCalls)
	result.Evidence["response"] = response.Response
	return result
}

// countGateBlocks 统计未走 auto-preflight 的 gate 拦截：这类拦截的工具输出是
// 纯 reminder 文本（含 "Tool blocked by"），不含 GatePreflightMarker。
func countGateBlocks(calls []query.ToolTrace) int {
	blocks := 0
	for _, call := range calls {
		if strings.Contains(call.Output, "Tool blocked by") && !strings.Contains(call.Output, query.GatePreflightMarker) {
			blocks++
		}
	}
	return blocks
}

func bashCommands(calls []query.ToolTrace) []string {
	commands := make([]string, 0, len(calls))
	for _, call := range calls {
		if !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		var input struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(call.Input), &input); err == nil && input.Command != "" {
			commands = append(commands, input.Command)
		}
	}
	return commands
}
