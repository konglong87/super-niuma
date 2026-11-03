package agentruntime

import (
	"context"
	"math"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

// cacheHeavySubagentStreamer reports the shape a long sub-agent turn actually
// has: a small uncached tail on top of a large cache read.
type cacheHeavySubagentStreamer struct{}

func (cacheHeavySubagentStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		if err := cb.OnText("done"); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done"}}},
		StopReason: "end_turn",
		Usage: anthropic.Usage{
			InputTokens:          1_000,
			CacheReadInputTokens: 200_000,
			OutputTokens:         0,
		},
	}, nil
}

// AUDIT-P0-15: the sub-agent cost must price the cache read at 0.1x the input
// rate. Charging the whole prompt at the full input rate inflated this ~10x.
func TestSubagentCostPricesCacheReadAtTieredRate(t *testing.T) {
	project := t.TempDir()
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    cacheHeavySubagentStreamer{},
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "claude-sonnet-4-6",
		TaskStore: store,
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "cache heavy", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}

	// claude-sonnet-4-6: $3/MTok input.
	const inputPerMTok = 3.0
	wantTiered := 1_000.0/1_000_000*inputPerMTok + 200_000.0/1_000_000*inputPerMTok*0.1
	flat := 201_000.0 / 1_000_000 * inputPerMTok

	if math.Abs(result.CostUSD-wantTiered) > 1e-9 {
		t.Fatalf("CostUSD = %v, want %v (flat-rate bug would give %v)", result.CostUSD, wantTiered, flat)
	}
	if !result.CostKnown {
		t.Fatal("CostKnown = false for a priced Anthropic model")
	}
	// InputTokens must stay the whole prompt: the context-window estimator is
	// calibrated against it.
	if result.Usage.InputTokens != 201_000 {
		t.Fatalf("Usage.InputTokens = %d, want 201000 (the full prompt size)", result.Usage.InputTokens)
	}
}

// An unpriced model must be reported as unpriced, not as free.
func TestSubagentCostFlagsUnknownModelPricing(t *testing.T) {
	project := t.TempDir()
	store := &fakeTaskStore{}
	runtime := Runtime{
		Client:    cacheHeavySubagentStreamer{},
		Registry:  tools.NewRegistry(echoTool{}),
		Model:     "my-sonnet-4-proxy",
		TaskStore: store,
	}
	result, err := runtime.Run(context.Background(), Request{Prompt: "do it", Description: "unpriced", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if result.CostKnown {
		t.Fatal("CostKnown = true for a third-party gateway name with no configured price")
	}
	if result.CostUSD != 0 {
		t.Fatalf("CostUSD = %v, want 0 alongside CostKnown=false", result.CostUSD)
	}
}
