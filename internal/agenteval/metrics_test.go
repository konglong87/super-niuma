package agenteval

import (
	"context"
	"os"
	"testing"
)

func TestQueryCaseRecordsMetrics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))

	result := runCase(context.Background(), Options{CWD: t.TempDir()}, Case{
		ID:              "tool_metrics",
		Type:            "tool",
		Prompt:          "Read eval_target.txt.",
		ExpectToolCalls: []string{"Read"},
	})
	if result.Status != "passed" {
		t.Fatalf("result = %+v", result)
	}
	if result.Metrics == nil {
		t.Fatal("expected case metrics, got nil")
	}
	// tool 案例固定两轮：第 1 轮发起 Read 工具调用，第 2 轮消费工具结果给出最终答复。
	if result.Metrics.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Metrics.Turns)
	}
	if result.Metrics.ToolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", result.Metrics.ToolCalls)
	}
	if result.Metrics.OutputTokens == 0 {
		t.Fatal("expected non-zero output tokens")
	}
}

func TestQueryCaseFailsWhenTurnBudgetExceeded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("NIUMA_CONFIG_DIR", os.Getenv("CLAUDE_CONFIG_DIR"))

	result := runCase(context.Background(), Options{CWD: t.TempDir()}, Case{
		ID:             "tool_budget",
		Type:           "tool",
		Prompt:         "Read eval_target.txt.",
		ExpectMaxTurns: 1, // tool 案例实际要 2 轮，预算 1 必然超
	})
	if result.Status != "failed" {
		t.Fatalf("expected budget failure, result = %+v", result)
	}
	found := false
	for _, check := range result.Checks {
		if check.Name == "budget.max_turns" && check.Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected failed budget.max_turns check, checks = %+v", result.Checks)
	}
	// 预算超标只应让 budget 检查失败，指标仍需照常采集（两轮），供报告使用。
	if result.Metrics == nil || result.Metrics.Turns != 2 {
		t.Fatalf("expected metrics with 2 turns even on budget failure, got %+v", result.Metrics)
	}
}
