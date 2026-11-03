package runtimecompose

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/promptdump"
)

// Exercise the actual core accounting function without a Store, fake billing or
// model calls. Unknown consumption must retain the unmeasured admission budget.
func uncertainFor(result workflow.Result, reserved int) int {
	return workflow.UncertainTokens(workflow.Run{
		Usage: result.Usage,
		Nodes: []workflow.NodeRun{{NodeID: "node", Attempts: []workflow.Attempt{{Usage: result.Usage, UsageKnown: result.UsageKnown, BudgetReserved: reserved}}}},
	})
}

func TestCompleteTextKnownAndUnknownUsageConservativeAccounting(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	tests := []struct {
		name  string
		raw   anthropic.Usage
		want  workflow.Usage
		known bool
	}{
		{"complete", anthropic.Usage{InputTokens: 11, OutputTokens: 7}, workflow.Usage{InputTokens: 11, OutputTokens: 7}, true},
		{"complete cache tiers", anthropic.Usage{InputTokens: 11, OutputTokens: 7, CacheReadInputTokens: 3, CacheCreationInputTokens: 2}, workflow.Usage{InputTokens: 11, OutputTokens: 7, CacheReadTokens: 3, CacheWriteTokens: 2}, true},
		{"cache inclusive", anthropic.Usage{InputTokens: 11, OutputTokens: 7, CacheReadInputTokens: 3, InputTokensIncludeCacheRead: true}, workflow.Usage{InputTokens: 8, OutputTokens: 7, CacheReadTokens: 3}, true},
		{"fully cached input", anthropic.Usage{InputTokens: 3, OutputTokens: 7, CacheReadInputTokens: 3, InputTokensIncludeCacheRead: true}, workflow.Usage{OutputTokens: 7, CacheReadTokens: 3}, true},
		{"missing usage", anthropic.Usage{}, workflow.Usage{}, false},
		{"missing input", anthropic.Usage{OutputTokens: 7}, workflow.Usage{OutputTokens: 7}, false},
		{"missing output", anthropic.Usage{InputTokens: 11, CacheReadInputTokens: 3, CacheCreationInputTokens: 2}, workflow.Usage{InputTokens: 11, CacheReadTokens: 3, CacheWriteTokens: 2}, false},
		{"positive usage beyond run budget is not erased", anthropic.Usage{InputTokens: workflow.MaxTotalTokens + 1, OutputTokens: 7}, workflow.Usage{InputTokens: workflow.MaxTotalTokens + 1, OutputTokens: 7}, true},
	}
	const reserved = MaxInputTokens + DefaultMaxOutputTokens
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := fakeExecutor(testConfig(), func(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
				r := response("safe final text")
				r.Usage = tt.raw
				return r, nil
			})
			var events []workflow.Event
			got, err := e.Execute(context.Background(), testInput(), func(ev workflow.Event) { events = append(events, ev) })
			if err != nil || got.Status != workflow.Completed || got.Output != "safe final text" || got.Failure != nil {
				t.Fatalf("legitimate text completion rejected: %+v err=%v", got, err)
			}
			if got.UsageKnown != tt.known || got.Usage != tt.want {
				t.Fatalf("usage replaced or certainty incorrect: %+v want=%+v known=%t", got, tt.want, tt.known)
			}
			uncertain := uncertainFor(got, reserved)
			if tt.known {
				if uncertain != 0 {
					t.Fatalf("known usage retained reservation: %d", uncertain)
				}
			} else if uncertain != reserved-got.Usage.Total() || got.Usage.Total()+uncertain != reserved {
				t.Fatalf("unknown usage was mistaken for zero cost: actual=%+v uncertain=%d reserved=%d", got.Usage, uncertain, reserved)
			}
			if len(events) != 2 || events[0].Type != workflow.EventPhase || events[1].Type != workflow.EventDelta {
				t.Fatalf("completion events changed: %+v", events)
			}
		})
	}
}

func TestPartialFailureKeepsActualKnownOrUnknownUsage(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	tests := []struct {
		name  string
		raw   anthropic.Usage
		known bool
	}{
		{"complete usage", anthropic.Usage{InputTokens: 11, OutputTokens: 7, CacheReadInputTokens: 3, CacheCreationInputTokens: 2}, true},
		{"missing output", anthropic.Usage{InputTokens: 11, CacheReadInputTokens: 3, CacheCreationInputTokens: 2}, false},
		{"missing input", anthropic.Usage{OutputTokens: 7}, false},
		{"missing all", anthropic.Usage{}, false},
	}
	const reserved = MaxInputTokens + DefaultMaxOutputTokens
	for _, tt := range tests {
		for _, wrapped := range []bool{false, true} {
			name := tt.name + " direct"
			if wrapped {
				name = tt.name + " PartialStreamError"
			}
			t.Run(name, func(t *testing.T) {
				e := fakeExecutor(testConfig(), func(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
					if err := cb.OnText("uncommitted partial text"); err != nil {
						return nil, err
					}
					r := response("uncommitted partial text")
					r.Usage = tt.raw
					providerErr := errors.New("HTTP 503 private-partial-provider-body")
					if wrapped {
						return nil, &anthropic.PartialStreamError{Err: providerErr, Partial: r}
					}
					return r, providerErr
				})
				var events []workflow.Event
				got, err := e.Execute(context.Background(), testInput(), func(ev workflow.Event) { events = append(events, ev) })
				requireCode(t, err, CodeTransient)
				if got.Status != workflow.Failed || got.Output != "" || got.UsageKnown != tt.known || got.Usage != mapUsage(tt.raw) {
					t.Fatalf("failed partial measurement was erased or fabricated: %+v err=%v", got, err)
				}
				if len(events) != 1 || events[0].Type != workflow.EventPhase || strings.Contains(err.Error(), "private-partial-provider-body") {
					t.Fatalf("partial output or raw error leaked: %+v err=%v", events, err)
				}
				wantUncertain := 0
				if !tt.known {
					wantUncertain = reserved - got.Usage.Total()
				}
				if uncertain := uncertainFor(got, reserved); uncertain != wantUncertain {
					t.Fatalf("partial admission reserve=%d want=%d", uncertain, wantUncertain)
				}
			})
		}
	}
}

func TestInvalidUsageStillFailsAndKeepsBudgetUncertain(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	maxInt := int(^uint(0) >> 1)
	for _, tt := range []struct {
		name string
		raw  anthropic.Usage
	}{
		{"negative input", anthropic.Usage{InputTokens: -1, OutputTokens: 7}},
		{"negative output", anthropic.Usage{InputTokens: 11, OutputTokens: -1}},
		{"negative cache read", anthropic.Usage{InputTokens: 11, OutputTokens: 7, CacheReadInputTokens: -1}},
		{"negative cache write", anthropic.Usage{InputTokens: 11, OutputTokens: 7, CacheCreationInputTokens: -1}},
		{"negative cache detail", anthropic.Usage{InputTokens: 11, OutputTokens: 7, CacheCreationEphemeral1hInputTokens: -1}},
		{"negative reasoning", anthropic.Usage{InputTokens: 11, OutputTokens: 7, ReasoningOutputTokens: -1}},
		{"inconsistent cache inclusive", anthropic.Usage{InputTokens: 1, OutputTokens: 7, CacheReadInputTokens: 2, InputTokensIncludeCacheRead: true}},
		{"input output overflow", anthropic.Usage{InputTokens: maxInt, OutputTokens: 1}},
		{"cache overflow", anthropic.Usage{InputTokens: maxInt - 1, OutputTokens: 1, CacheReadInputTokens: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, partial := range []bool{false, true} {
				e := fakeExecutor(testConfig(), func(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
					r := response("not accepted")
					r.Usage = tt.raw
					if partial {
						return nil, &anthropic.PartialStreamError{Err: errors.New("HTTP 503"), Partial: r}
					}
					return r, nil
				})
				var delta bool
				got, err := e.Execute(context.Background(), testInput(), func(ev workflow.Event) { delta = delta || ev.Type == workflow.EventDelta })
				requireCode(t, err, CodeUsage)
				if got.Status != workflow.Failed || got.UsageKnown || got.Output != "" || delta {
					t.Fatalf("invalid counters accepted: %+v err=%v", got, err)
				}
				if got.Usage != (workflow.Usage{}) || uncertainFor(got, DefaultMaxOutputTokens) != DefaultMaxOutputTokens {
					t.Fatalf("invalid measurement poisoned accounting or released reserve: %+v", got)
				}
			}
		})
	}
}

func TestMissingUsageDoesNotBypassCompletionSafety(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	for _, tt := range []struct{ stop, text, code string }{
		{"max_tokens", "partial", CodeStopReason}, {"tool_use", "tool request", CodeUnsupported},
		{"content_filter", "refused", CodePolicy}, {"", "text", CodeStopReason},
		{"end_turn", "", CodeEmptyOutput}, {"end_turn", " \n", CodeEmptyOutput},
	} {
		t.Run(tt.code+tt.stop+tt.text, func(t *testing.T) {
			e := fakeExecutor(testConfig(), func(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
				r := response(tt.text)
				r.StopReason = tt.stop
				r.Usage = anthropic.Usage{}
				return r, nil
			})
			got, err := e.Execute(context.Background(), testInput(), nil)
			requireCode(t, err, tt.code)
			if got.Status == workflow.Completed || got.UsageKnown || got.Output != "" {
				t.Fatalf("missing usage bypassed safety gate: %+v", got)
			}
		})
	}
}

func TestRealCompatibleClientWithoutUsageKeepsReservation(t *testing.T) {
	t.Setenv(promptdump.PathEnv, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture := httptest.NewRecorder()
		chatSSE(fixture, "compatible provider final text")
		w.Header().Set("Content-Type", "text/event-stream")
		// The compatibility fixture omits the optional usage-only SSE frame,
		// preserving the real text, finish_reason and [DONE] protocol events.
		for _, line := range strings.Split(fixture.Body.String(), "\n") {
			if !strings.Contains(line, `"usage":`) {
				fmt.Fprintln(w, line)
			}
		}
	}))
	defer server.Close()
	cfg := testConfig()
	cfg.Provider, cfg.ProviderProtocol = "openai-compatible", config.ProviderProtocolOpenAIChatCompletions
	cfg.BaseURL = server.URL + "/v1"
	got, err := NewTextExecutor(resolved(cfg)).Execute(context.Background(), testInput(), nil)
	if err != nil || got.Status != workflow.Completed || got.Output != "compatible provider final text" || got.UsageKnown || got.Usage != (workflow.Usage{}) {
		t.Fatalf("real compatible client without usage rejected or treated as free: %+v err=%v", got, err)
	}
	if uncertainFor(got, DefaultMaxOutputTokens) != DefaultMaxOutputTokens {
		t.Fatal("missing real provider usage released the admission reserve")
	}
}
