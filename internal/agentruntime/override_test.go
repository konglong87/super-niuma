package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/tools"
)

func TestAgentExecutionOverrideResolvesExplicitValuesAndInheritance(t *testing.T) {
	parent := AgentExecutionOverride{Provider: "anthropic", Model: "claude-sonnet", Effort: "high", MaxOutputTokens: 2048, MaxTurns: 8, TimeoutMS: 5000}
	child := AgentExecutionOverride{Model: "gpt-5.6-sol", MaxTurns: 3}
	resolved := ResolveExecutionOverride(parent, child)
	if resolved.Provider != "anthropic" || resolved.Model != "gpt-5.6-sol" || resolved.Effort != "high" || resolved.MaxOutputTokens != 2048 || resolved.MaxTurns != 3 || resolved.TimeoutMS != 5000 {
		t.Fatalf("resolved override = %+v", resolved)
	}
}

func TestAgentExecutionOverrideClampsInvalidValuesWithReason(t *testing.T) {
	resolved := NormalizeExecutionOverride(AgentExecutionOverride{Effort: "invalid", MaxOutputTokens: -2, MaxTurns: -1, TimeoutMS: -4})
	if resolved.Effort != "medium" || resolved.MaxOutputTokens != 0 || resolved.MaxTurns != 0 || resolved.TimeoutMS != 0 {
		t.Fatalf("normalized override = %+v", resolved)
	}
	if len(resolved.ReasonCodes) != 4 {
		t.Fatalf("reason codes = %+v", resolved.ReasonCodes)
	}
}

func TestRuntimeRunsProviderPreflightBeforeResolver(t *testing.T) {
	calledResolver := false
	runtime := Runtime{
		ClientResolver: func(context.Context, string, string) (MessageStreamer, func(), error) {
			calledResolver = true
			return nil, nil, nil
		},
		ProviderPreflight: func(context.Context, string, string) error { return context.Canceled },
	}
	_, err := runtime.Run(context.Background(), Request{Prompt: "run", Provider: "missing", Model: "model"}, tools.Context{})
	if err == nil || !strings.Contains(err.Error(), "provider preflight") || calledResolver {
		t.Fatalf("err=%v resolver_called=%v", err, calledResolver)
	}
}
