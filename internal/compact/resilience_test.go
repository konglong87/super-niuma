package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
)

type failingStreamer struct {
	calls int
	err   error
}

func (f *failingStreamer) StreamMessages(context.Context, anthropic.MessagesRequest, anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.calls++
	return nil, f.err
}

// compactableMessages builds a history long enough to satisfy the message-count
// gate with a single preserved round.
func compactableMessages() []anthropic.MessageParam {
	return []anthropic.MessageParam{
		textMessage("user", "Please inspect internal/query/query.go and run go test ./internal/query."),
		textMessage("assistant", "I will inspect it."),
		textMessage("assistant", "The query loop builds request messages."),
		textMessage("user", "Now continue with the implementation."),
	}
}

func overflowTestConfig() Config {
	return Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.05,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 1000},
	}
}

// AUDIT-P1-05: a base64 image must not be estimated as if it were text. Before
// the fix a 1 MB payload produced ~350k phantom tokens and one screenshot could
// force a compaction on its own.
func TestEstimateDoesNotBillBase64ImagesAsText(t *testing.T) {
	payload := strings.Repeat("A", 1<<20) // 1 MiB of base64
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:   "image",
			Source: &anthropic.ContentSource{Type: "base64", MediaType: "image/png", Data: payload},
		}},
	}}
	got := RoughTokenCounter{}.EstimateMessages("gpt-5.5", messages)
	if got > 4_000 {
		t.Fatalf("image estimated at %d tokens; base64 payload is being billed as text", got)
	}
	if got < estimatedImageBlockTokens {
		t.Fatalf("image estimated at %d tokens, want at least the per-image floor %d", got, estimatedImageBlockTokens)
	}
	asText := estimateTextTokens(payload)
	if asText <= got*10 {
		t.Fatalf("text estimate %d is not meaningfully larger than image estimate %d; test no longer proves anything", asText, got)
	}
}

// A non-image inline attachment (PDF) still scales with size, because page count
// — not pixel dimensions — drives its real cost.
func TestEstimateScalesNonImageInlineSourcesWithSize(t *testing.T) {
	small := estimateInlineSourceTokens("application/pdf", strings.Repeat("A", 1_000))
	large := estimateInlineSourceTokens("application/pdf", strings.Repeat("A", 4<<20))
	if small != estimatedImageBlockTokens {
		t.Fatalf("small document = %d, want the floor %d", small, estimatedImageBlockTokens)
	}
	if large <= small {
		t.Fatalf("large document = %d, small = %d; document estimate must scale with size", large, small)
	}
}

// AUDIT-P1-05: the provider reports the exact prompt size, so a systematic
// estimator bias must be corrected from real usage rather than left as a static
// guess.
func TestObservePromptTokensCalibratesEstimate(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	// Threshold is 100_000 tokens: the raw estimate stays below it, but a
	// provider reporting 4x the estimate must push the calibrated value over.
	compactor := New(Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.5,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 200_000},
	}, streamer, RoughTokenCounter{})
	messages := []anthropic.MessageParam{
		textMessage("user", "Please inspect internal/query/query.go and run go test ./internal/query."),
		textMessage("assistant", strings.Repeat("detail ", 20_000)), // ~40k estimated tokens
		textMessage("assistant", "The query loop builds request messages."),
		textMessage("user", "Now continue with the implementation."),
	}

	first, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if first.Compacted {
		t.Fatalf("did not expect compaction on the raw estimate (usage=%d)", first.EstimatedUsage)
	}
	if compactor.lastEstimate == 0 {
		t.Fatal("below-threshold turn did not remember its estimate; calibration can never run")
	}

	// Provider says the prompt was 4x larger than estimated.
	compactor.ObservePromptTokens(first.EstimatedUsage * 4)

	second, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Compacted {
		t.Fatalf("expected calibrated estimate to cross the threshold, got skip=%s usage=%d (raw usage was %d)", second.SkippedReason, second.EstimatedUsage, first.EstimatedUsage)
	}
	if second.EstimatedUsage <= first.EstimatedUsage {
		t.Fatalf("calibrated usage %d did not exceed raw usage %d", second.EstimatedUsage, first.EstimatedUsage)
	}
}

// Compacted turns must not feed the calibration loop: the provider usage that
// follows describes the post-compaction prompt, so using it would teach the
// estimator that it over-estimates by exactly the amount it just compacted.
func TestObservePromptTokensIgnoresCompactedTurns(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	compactor := New(overflowTestConfig(), streamer, RoughTokenCounter{})
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("expected compaction, got %s", result.SkippedReason)
	}
	compactor.ObservePromptTokens(10)
	if compactor.calibration != 0 {
		t.Fatalf("calibration = %v, want 0: a compacted turn must not calibrate", compactor.calibration)
	}
}

// AUDIT-P1-06: three transient summary failures must not disable compaction for
// the rest of the session.
func TestCircuitBreakerResetsAfterFailureWindow(t *testing.T) {
	now := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	streamer := &failingStreamer{err: errors.New("provider down")}
	cfg := overflowTestConfig()
	cfg.MaxFailures = 2
	cfg.FailureResetAfter = 5 * time.Minute
	compactor := New(cfg, streamer, RoughTokenCounter{})
	compactor.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		compactor.cooldown = 0
		if _, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages()); err == nil {
			t.Fatalf("attempt %d: expected summary failure", i+1)
		}
	}
	compactor.cooldown = 0
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages())
	if err != nil || result.SkippedReason != "circuit_open" {
		t.Fatalf("circuit should be open after MaxFailures: reason=%q err=%v", result.SkippedReason, err)
	}

	// Still inside the reset window: stays open.
	now = now.Add(4 * time.Minute)
	compactor.cooldown = 0
	if result, _ := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages()); result.SkippedReason != "circuit_open" {
		t.Fatalf("circuit reopened too early: reason=%q", result.SkippedReason)
	}

	// Past the reset window: the breaker must let compaction try again.
	now = now.Add(2 * time.Minute)
	streamer.err = nil
	compactor.streamer = &fakeSummaryStreamer{summary: compactSummaryFixture()}
	compactor.cooldown = 0
	result, err = compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("circuit never reset: reason=%q — compaction is permanently disabled for this session", result.SkippedReason)
	}
}

// AUDIT-P1-06: CooldownTurns was the only field missing a default, so the
// configured cooldown was silently zero.
func TestCooldownTurnsHasDefault(t *testing.T) {
	if got := (Config{}).WithDefaults().CooldownTurns; got != DefaultCooldownTurns {
		t.Fatalf("CooldownTurns default = %d, want %d", got, DefaultCooldownTurns)
	}
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	cfg := overflowTestConfig()
	cfg.CooldownTurns = 0 // unset: must fall back to the default, not to "no cooldown"
	compactor := New(cfg, streamer, RoughTokenCounter{})
	if result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages()); err != nil || !result.Compacted {
		t.Fatalf("first compaction failed: reason=%q err=%v", result.SkippedReason, err)
	}
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages())
	if err != nil {
		t.Fatal(err)
	}
	if result.SkippedReason != "cooldown" {
		t.Fatalf("second compaction reason = %q, want cooldown", result.SkippedReason)
	}
}

// AUDIT-P0-08: overflow recovery must ignore both the threshold and an active
// cooldown, because the provider has already rejected the prompt.
func TestForceCompactIgnoresThresholdAndCooldown(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	cfg := overflowTestConfig()
	cfg.DefaultThresholdRatio = 1.0
	cfg.ModelContext = map[string]int{"gpt-5.5": 10_000_000} // threshold far above any estimate
	compactor := New(cfg, streamer, RoughTokenCounter{})
	compactor.cooldown = 5

	if result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages()); err != nil || result.Compacted {
		t.Fatalf("MaybeCompact should skip here: compacted=%v reason=%q err=%v", result.Compacted, result.SkippedReason, err)
	}
	result, err := compactor.ForceCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("ForceCompact skipped: %q — overflow recovery would be a no-op", result.SkippedReason)
	}
}

// An explicitly disabled compactor must stay disabled even under overflow: the
// user's configuration wins over our recovery attempt.
func TestForceCompactHonorsDisabled(t *testing.T) {
	cfg := overflowTestConfig()
	cfg.Enabled = false
	compactor := New(cfg, &fakeSummaryStreamer{summary: compactSummaryFixture()}, RoughTokenCounter{})
	result, err := compactor.ForceCompact(context.Background(), "gpt-5.5", "", nil, nil, compactableMessages())
	if err != nil {
		t.Fatal(err)
	}
	if result.Compacted || result.SkippedReason != "disabled" {
		t.Fatalf("ForceCompact on a disabled compactor: compacted=%v reason=%q", result.Compacted, result.SkippedReason)
	}
}

func TestIsContextOverflowError(t *testing.T) {
	overflow := []string{
		`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`,
		`{"error":{"code":"context_length_exceeded","message":"This model's maximum context length is 128000 tokens"}}`,
		"input length and `max_tokens` exceed context limit: 199000 + 8000 > 200000",
		"Please reduce the length of the messages",
		"request exceeds the context window",
		"too many tokens in request",
	}
	for _, message := range overflow {
		if !IsContextOverflowError(errors.New(message)) {
			t.Errorf("IsContextOverflowError(%q) = false, want true", message)
		}
	}
	other := []string{
		"rate_limit_error: too many requests",
		"overloaded_error",
		"connection reset by peer",
		"invalid_request_error: unknown model",
		"context canceled",
	}
	for _, message := range other {
		if IsContextOverflowError(errors.New(message)) {
			t.Errorf("IsContextOverflowError(%q) = true, want false", message)
		}
	}
	if IsContextOverflowError(nil) {
		t.Error("IsContextOverflowError(nil) = true")
	}
	// Must see through wrapping.
	if !IsContextOverflowError(fmt.Errorf("stream failed: %w", errors.New("prompt is too long"))) {
		t.Error("wrapped overflow error not detected")
	}
}

// AUDIT-P0-08: an unconfigured session must still have overflow protection.
func TestConfigFromSettingsEnablesAutoCompactByDefault(t *testing.T) {
	if !ConfigFromSettings(config.Settings{}, "gpt-5.5").Enabled {
		t.Fatal("auto-compact disabled with no settings: an out-of-the-box long session has no overflow protection")
	}
	if !ConfigFromSettings(config.Settings{AutoCompact: &config.AutoCompactSettings{}}, "gpt-5.5").Enabled {
		t.Fatal("auto-compact disabled by an autoCompact block that does not mention enabled")
	}
	disabled := false
	if ConfigFromSettings(config.Settings{AutoCompact: &config.AutoCompactSettings{Enabled: &disabled}}, "gpt-5.5").Enabled {
		t.Fatal("explicit enabled:false was ignored")
	}
	enabled := true
	if !ConfigFromSettings(config.Settings{AutoCompact: &config.AutoCompactSettings{Enabled: &enabled}}, "gpt-5.5").Enabled {
		t.Fatal("explicit enabled:true was ignored")
	}
}

// AUDIT-P1-06: hard facts were sorted alphabetically and then truncated, so a
// file first touched late in the session could be dropped purely because its
// path sorts late — while a path from turn one survived.
func TestExtractFactsKeepsRecentFilesOverAlphabeticalOrder(t *testing.T) {
	var messages []anthropic.MessageParam
	// 40 early files that all sort before "z...", filling the limit.
	for i := 0; i < 40; i++ {
		messages = append(messages, textMessage("assistant", fmt.Sprintf("touched a/early%02d/file.go", i)))
	}
	messages = append(messages, textMessage("assistant", "just edited zz/recent/critical.go"))
	facts := ExtractFacts(messages)
	if len(facts.Files) != 40 {
		t.Fatalf("files = %d, want the 40-item limit", len(facts.Files))
	}
	found := false
	for _, file := range facts.Files {
		if file == "zz/recent/critical.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("most recently touched file was truncated away by alphabetical ordering; got %v", facts.Files)
	}
	// Rendering order must still be stable/sorted for reproducible summaries.
	for i := 1; i < len(facts.Files); i++ {
		if facts.Files[i-1] > facts.Files[i] {
			t.Fatalf("facts.Files not sorted at %d: %v", i, facts.Files)
		}
	}
}
