package quota

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
)

func TestUsageFromTurnUsagePreservesWholeInputAndCacheTiers(t *testing.T) {
	turn := session.TurnUsage{InputTokens: 100, CacheReadInputTokens: 25, CacheCreationInputTokens: 5, OutputTokens: 7, Estimated: true}
	got := UsageFromTurnUsage(turn)
	if got.InputTokens != 130 || got.CacheReadInputTokens != 25 || got.CacheCreationInputTokens != 5 || got.OutputTokens != 7 || !got.Estimated {
		t.Fatalf("quota usage = %+v", got)
	}
}
