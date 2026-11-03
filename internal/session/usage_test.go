package session

import "testing"

func TestTurnUsageTotalTokensIncludesDisjointCacheTiers(t *testing.T) {
	usage := TurnUsage{
		SessionID:                "session-1",
		Turn:                     3,
		Provider:                 "anthropic",
		Model:                    "claude-sonnet-4-6",
		InputTokens:              100,
		OutputTokens:             20,
		CacheReadInputTokens:     30,
		CacheCreationInputTokens: 10,
		Estimated:                false,
		Source:                   "provider",
	}
	if got := usage.TotalTokens(); got != 160 {
		t.Fatalf("total tokens = %d, want 160", got)
	}
}

func TestTurnUsageAddKeepsIdentityAndCombinesCounters(t *testing.T) {
	base := TurnUsage{SessionID: "session-1", Turn: 2, Provider: "openai", Model: "gpt-5.5", InputTokens: 4, OutputTokens: 3, Estimated: true, Source: "estimate"}
	update := TurnUsage{SessionID: "session-1", Turn: 2, Provider: "openai", Model: "gpt-5.5", InputTokens: 6, OutputTokens: 5, CacheReadInputTokens: 2, Estimated: false, Source: "provider"}
	got, err := base.Add(update)
	if err != nil {
		t.Fatalf("add usage: %v", err)
	}
	if got.InputTokens != 10 || got.OutputTokens != 8 || got.CacheReadInputTokens != 2 {
		t.Fatalf("combined counters = %+v", got)
	}
	if got.Estimated || got.Source != "provider" {
		t.Fatalf("provider update did not replace estimate metadata: %+v", got)
	}
}

func TestTurnUsageAddRejectsDifferentTurnIdentity(t *testing.T) {
	_, err := (TurnUsage{SessionID: "session-1", Turn: 2, Provider: "openai", Model: "gpt-5.5"}).Add(TurnUsage{SessionID: "session-1", Turn: 3, Provider: "openai", Model: "gpt-5.5"})
	if err == nil {
		t.Fatal("expected identity mismatch")
	}
}

func TestNewTurnUsageConvertsReportedWholeInputIntoDisjointTiers(t *testing.T) {
	usage := NewTurnUsage("session-1", 4, "anthropic", "claude-sonnet-4-6", ReportedUsage{
		InputTokens:              150,
		CacheCreationInputTokens: 20,
		CacheReadInputTokens:     30,
		OutputTokens:             5,
	}, false, "provider")
	if usage.InputTokens != 100 || usage.CacheCreationInputTokens != 20 || usage.CacheReadInputTokens != 30 || usage.OutputTokens != 5 {
		t.Fatalf("disjoint usage = %+v", usage)
	}
}
