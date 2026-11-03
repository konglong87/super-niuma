package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type Streamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

type Compactor struct {
	cfg      Config
	streamer Streamer
	counter  TokenCounter
	failures int
	cooldown int
	// lastFailureAt drives the circuit-breaker reset. Without it `failures`
	// only ever cleared on success, so three transient summary failures
	// disabled compaction for the whole remaining session.
	lastFailureAt time.Time
	now           func() time.Time

	// lastEstimate / calibration form the estimator feedback loop: the provider
	// reports the exact prompt size of every request, so a systematic bias in
	// the local estimator (images, CJK, tool schemas, provider tokenizer
	// differences) can be measured instead of guessed. See calibrated.
	lastEstimate int
	calibration  float64
}

type Result struct {
	Compacted        bool
	Messages         []anthropic.MessageParam
	Summary          string
	PersistedSummary string
	Facts            Facts
	Metadata         Metadata
	SkippedReason    string
	EstimatedUsage   int
}

type Metadata struct {
	Model              string    `json:"model"`
	SummaryModel       string    `json:"summary_model,omitempty"`
	ContextTokens      int       `json:"context_tokens"`
	ThresholdRatio     float64   `json:"threshold_ratio"`
	ThresholdTokens    int       `json:"threshold_tokens"`
	TriggerTokens      int       `json:"trigger_tokens"`
	TokenAfter         int       `json:"token_after"`
	CompactedMessages  int       `json:"compacted_messages"`
	PreservedMessages  int       `json:"preserved_messages"`
	PreservedRounds    int       `json:"preserved_rounds"`
	CreatedAt          time.Time `json:"created_at"`
	SourceEntryStartID string    `json:"source_entry_start_id,omitempty"`
	SourceEntryEndID   string    `json:"source_entry_end_id,omitempty"`
	SourceEntryCount   int       `json:"source_entry_count,omitempty"`
	SourceEntryDigest  string    `json:"source_entry_digest,omitempty"`
}

func New(cfg Config, streamer Streamer, counter TokenCounter) *Compactor {
	if counter == nil {
		counter = RoughTokenCounter{}
	}
	return &Compactor{cfg: cfg.WithDefaults(), streamer: streamer, counter: counter, now: time.Now}
}

func (c *Compactor) MaybeCompact(ctx context.Context, model string, system string, systemBlocks []anthropic.SystemBlock, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam) (Result, error) {
	cfg := c.cfg.WithDefaults()
	if reason := c.gate(cfg, messages, true); reason != "" {
		return Result{SkippedReason: reason}, nil
	}
	raw := c.counter.EstimateRequest(model, system, systemBlocks, tools, messages)
	used := c.calibrated(raw)
	threshold := cfg.ThresholdTokens(model)
	if used < threshold {
		// Only remember estimates for requests that are actually about to be
		// sent unchanged; a compacted request's provider usage describes the
		// post-compaction prompt and would poison the calibration ratio.
		c.lastEstimate = raw
		return Result{SkippedReason: "below_threshold", EstimatedUsage: used}, nil
	}
	return c.compact(ctx, cfg, model, system, systemBlocks, messages, used, threshold)
}

// ForceCompact compacts regardless of the estimated threshold and of any active
// cooldown. It exists for reactive overflow recovery: the provider has already
// rejected the request as too long, so the local estimate is known to be wrong
// and waiting out a cooldown would only fail the session. An explicitly
// disabled compactor and an open circuit are still honored.
func (c *Compactor) ForceCompact(ctx context.Context, model string, system string, systemBlocks []anthropic.SystemBlock, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam) (Result, error) {
	cfg := c.cfg.WithDefaults()
	if reason := c.gate(cfg, messages, false); reason != "" {
		return Result{SkippedReason: reason}, nil
	}
	used := c.calibrated(c.counter.EstimateRequest(model, system, systemBlocks, tools, messages))
	return c.compact(ctx, cfg, model, system, systemBlocks, messages, used, cfg.ThresholdTokens(model))
}

func (c *Compactor) gate(cfg Config, messages []anthropic.MessageParam, applyCooldown bool) string {
	if !cfg.Enabled {
		return "disabled"
	}
	if c.streamer == nil {
		return "missing_streamer"
	}
	if c.circuitOpen(cfg) {
		return "circuit_open"
	}
	if applyCooldown && c.cooldown > 0 {
		c.cooldown--
		return "cooldown"
	}
	if len(messages) <= cfg.PreserveRecentRounds+1 {
		return "not_enough_messages"
	}
	return ""
}

// circuitOpen reports whether repeated summary failures should keep compaction
// off, resetting the counter once FailureResetAfter has elapsed since the last
// failure so a transient provider outage cannot disable compaction permanently.
func (c *Compactor) circuitOpen(cfg Config) bool {
	if c.failures < cfg.MaxFailures {
		return false
	}
	if cfg.FailureResetAfter > 0 && !c.lastFailureAt.IsZero() && c.clock().Sub(c.lastFailureAt) >= cfg.FailureResetAfter {
		c.failures = 0
		c.lastFailureAt = time.Time{}
		return false
	}
	return true
}

func (c *Compactor) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

func (c *Compactor) recordFailure() {
	c.failures++
	c.lastFailureAt = c.clock()
}

// ObservePromptTokens feeds the provider-reported prompt size of the request
// that was last estimated back into the estimator. The ratio is an EWMA and is
// clamped, so one anomalous turn cannot swing the threshold wildly, and it is
// only updated for requests large enough for the comparison to be meaningful.
func (c *Compactor) ObservePromptTokens(actual int) {
	const (
		minEstimateForCalibration = 1_000
		smoothing                 = 0.3
		minRatio                  = 0.25
		maxRatio                  = 4.0
	)
	if actual <= 0 || c.lastEstimate < minEstimateForCalibration {
		return
	}
	ratio := float64(actual) / float64(c.lastEstimate)
	if ratio < minRatio {
		ratio = minRatio
	} else if ratio > maxRatio {
		ratio = maxRatio
	}
	if c.calibration <= 0 {
		c.calibration = ratio
		return
	}
	c.calibration = c.calibration*(1-smoothing) + ratio*smoothing
}

func (c *Compactor) calibrated(raw int) int {
	if c.calibration <= 0 {
		return raw
	}
	return int(float64(raw) * c.calibration)
}

func (c *Compactor) compact(ctx context.Context, cfg Config, model string, system string, systemBlocks []anthropic.SystemBlock, messages []anthropic.MessageParam, used, threshold int) (Result, error) {
	partition := partitionMessages(messages, cfg.PreserveRecentRounds)
	if len(partition.compact) == 0 || len(partition.keep) == 0 {
		return Result{SkippedReason: "not_enough_compactable_messages", EstimatedUsage: used}, nil
	}
	facts := ExtractFacts(partition.compact)
	summaryModel := strings.TrimSpace(cfg.SummaryModel)
	if summaryModel == "" {
		summaryModel = model
	}
	summary, err := c.generateSummary(ctx, summaryModel, system, systemBlocks, partition.compact, facts)
	if err != nil {
		c.recordFailure()
		return Result{SkippedReason: "summary_failed", EstimatedUsage: used}, err
	}
	summary = strings.TrimSpace(summary)
	if err := validateSummary(summary, facts); err != nil {
		c.recordFailure()
		return Result{SkippedReason: "summary_invalid", EstimatedUsage: used}, err
	}
	persistedSummary := buildPersistedSummary(summary, facts)
	next := make([]anthropic.MessageParam, 0, 1+len(partition.keep))
	next = append(next, summaryMessage(persistedSummary))
	next = append(next, partition.keep...)
	after := c.counter.EstimateMessages(model, next)
	c.failures = 0
	c.lastFailureAt = time.Time{}
	c.cooldown = cfg.CooldownTurns
	// The next request is the compacted one; the previous estimate no longer
	// describes anything the provider will report usage for.
	c.lastEstimate = 0
	return Result{
		Compacted:        true,
		Messages:         next,
		Summary:          summary,
		PersistedSummary: persistedSummary,
		Facts:            facts,
		EstimatedUsage:   used,
		Metadata: Metadata{
			Model:             model,
			SummaryModel:      summaryModel,
			ContextTokens:     cfg.ContextTokens(model),
			ThresholdRatio:    cfg.ThresholdRatio(model),
			ThresholdTokens:   threshold,
			TriggerTokens:     used,
			TokenAfter:        after,
			CompactedMessages: len(partition.compact),
			PreservedMessages: len(partition.keep),
			PreservedRounds:   cfg.PreserveRecentRounds,
			CreatedAt:         time.Now().UTC(),
		},
	}, nil
}

func (c *Compactor) generateSummary(ctx context.Context, model string, system string, systemBlocks []anthropic.SystemBlock, messages []anthropic.MessageParam, facts Facts) (string, error) {
	prompt := summaryPrompt(messages, facts)
	var streamed strings.Builder
	res, err := c.streamer.StreamMessages(ctx, anthropic.MessagesRequest{
		Model:        model,
		MaxTokens:    c.cfg.WithDefaults().MaxSummaryTokens,
		System:       system,
		SystemBlocks: systemBlocks,
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: prompt}},
		}},
	}, anthropic.StreamCallbacks{
		OnText: func(text string) error {
			streamed.WriteString(text)
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(streamed.String()) != "" {
		return streamed.String(), nil
	}
	if res == nil {
		return "", fmt.Errorf("compact summary response is empty")
	}
	var out strings.Builder
	for _, block := range res.Message.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}

func summaryPrompt(messages []anthropic.MessageParam, facts Facts) string {
	var out strings.Builder
	out.WriteString("Summarize the conversation history below for continuing an agent session.\n")
	out.WriteString("Preserve concrete details and unresolved work. Do not invent facts.\n")
	out.WriteString("Return markdown with exactly these headings:\n")
	out.WriteString("## Current Goal\n## User Preferences / Constraints\n## Decisions Made\n## Files / Code Changed\n## Commands / Test Results\n## Open Tasks\n## Known Issues / Risks\n## Important Raw Facts\n\n")
	out.WriteString("Hard facts extracted by the runtime. Include all relevant items in the summary:\n")
	out.WriteString(facts.Markdown())
	out.WriteString("\n\nConversation to compact:\n")
	for i, message := range messages {
		out.WriteString(fmt.Sprintf("\n### Message %d (%s)\n", i+1, message.Role))
		for _, block := range message.Content {
			out.WriteString(formatBlockForPrompt(block))
			out.WriteString("\n")
		}
	}
	return out.String()
}

func formatBlockForPrompt(block anthropic.ContentBlock) string {
	switch block.Type {
	case "text":
		return block.Text
	case "thinking", "redacted_thinking":
		return "[thinking omitted]"
	case "tool_use":
		return fmt.Sprintf("TOOL CALL %s %s: %s", block.Name, block.ID, string(block.Input))
	case "tool_result":
		prefix := "TOOL RESULT"
		if block.IsError {
			prefix = "TOOL RESULT ERROR"
		}
		return fmt.Sprintf("%s %s: %s", prefix, block.ToolUseID, block.Content)
	case "image", "document":
		return "[" + block.Type + " omitted from compact prompt]"
	default:
		text := strings.TrimSpace(blockText(block))
		if text == "" {
			return "[" + block.Type + "]"
		}
		return text
	}
}

func buildPersistedSummary(summary string, facts Facts) string {
	content := strings.TrimSpace(summary)
	if !facts.Empty() {
		content += "\n\n## Runtime Extracted Facts\n" + facts.Markdown()
	}
	return content
}

func summaryMessage(summary string) anthropic.MessageParam {
	return anthropic.MessageParam{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type: "text",
			Text: "Conversation summary so far:\n" + strings.TrimSpace(summary),
		}},
	}
}

func validateSummary(summary string, facts Facts) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("compact summary is empty")
	}
	required := []string{"## Current Goal", "## Open Tasks", "## Important Raw Facts"}
	for _, heading := range required {
		if !strings.Contains(summary, heading) {
			return fmt.Errorf("compact summary missing heading %q", heading)
		}
	}
	for _, file := range firstN(facts.Files, 5) {
		if !strings.Contains(summary, file) {
			return fmt.Errorf("compact summary missing file fact %q", file)
		}
	}
	for _, command := range firstN(facts.Commands, 3) {
		if !strings.Contains(summary, command) {
			return fmt.Errorf("compact summary missing command fact %q", command)
		}
	}
	return nil
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

type messagePartition struct {
	compact []anthropic.MessageParam
	keep    []anthropic.MessageParam
}

func partitionMessages(messages []anthropic.MessageParam, preserveRounds int) messagePartition {
	start := recentRoundStart(messages, preserveRounds)
	start = adjustStartForToolPair(messages, start)
	if start <= 0 {
		return messagePartition{keep: append([]anthropic.MessageParam(nil), messages...)}
	}
	return messagePartition{
		compact: append([]anthropic.MessageParam(nil), messages[:start]...),
		keep:    append([]anthropic.MessageParam(nil), messages[start:]...),
	}
}

func recentRoundStart(messages []anthropic.MessageParam, rounds int) int {
	if rounds <= 0 {
		rounds = DefaultRecentRounds
	}
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && hasTextBlock(messages[i].Content) {
			seen++
			if seen >= rounds {
				return i
			}
		}
	}
	return 0
}

func adjustStartForToolPair(messages []anthropic.MessageParam, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	for start > 0 && hasToolResultBlock(messages[start].Content) {
		start--
	}
	pending := pendingToolUses(messages[:start])
	for start < len(messages) && intersectsToolResults(messages[start].Content, pending) {
		start++
	}
	return start
}

func hasTextBlock(blocks []anthropic.ContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "text" {
			return true
		}
	}
	return false
}

func hasToolResultBlock(blocks []anthropic.ContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}

func pendingToolUses(messages []anthropic.MessageParam) map[string]bool {
	pending := map[string]bool{}
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				if strings.TrimSpace(block.ID) != "" {
					pending[block.ID] = true
				}
			case "tool_result":
				delete(pending, block.ToolUseID)
			}
		}
	}
	return pending
}

func intersectsToolResults(blocks []anthropic.ContentBlock, pending map[string]bool) bool {
	if len(pending) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Type == "tool_result" && pending[block.ToolUseID] {
			return true
		}
	}
	return false
}

func MetadataJSON(metadata Metadata) string {
	data, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(data)
}
