package compact

import (
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact/tokenestimate"
)

const (
	// estimatedImageBlockTokens is the per-image ceiling described in
	// estimateInlineSourceTokens.
	estimatedImageBlockTokens = 1_600
	// documentBytesPerToken is the coarse PDF transport-bytes-per-token ratio.
	documentBytesPerToken = 100
)

type TokenCounter interface {
	EstimateMessages(model string, messages []anthropic.MessageParam) int
	EstimateRequest(model string, system string, systemBlocks []anthropic.SystemBlock, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam) int
}

type RoughTokenCounter struct{}

func (RoughTokenCounter) EstimateMessages(_ string, messages []anthropic.MessageParam) int {
	total := 0
	for _, message := range messages {
		total += EstimateTextTokens(message.Role)
		for _, block := range message.Content {
			total += estimateBlockTokens(block)
		}
	}
	return total
}

func (c RoughTokenCounter) EstimateRequest(model string, system string, systemBlocks []anthropic.SystemBlock, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam) int {
	total := c.EstimateMessages(model, messages)
	total += EstimateTextTokens(system)
	for _, block := range systemBlocks {
		total += EstimateTextTokens(block.Type)
		total += EstimateTextTokens(block.Text)
		if block.CacheControl != nil {
			total += EstimateTextTokens(block.CacheControl.Type)
		}
	}
	for _, tool := range tools {
		total += EstimateTextTokens(tool.Name)
		total += EstimateTextTokens(tool.Description)
		total += EstimateTextTokens(string(tool.InputSchema))
	}
	return total
}

func estimateBlockTokens(block anthropic.ContentBlock) int {
	total := EstimateTextTokens(block.Type) +
		EstimateTextTokens(block.Text) +
		EstimateTextTokens(block.Thinking) +
		EstimateTextTokens(block.ConnectorText) +
		EstimateTextTokens(block.ID) +
		EstimateTextTokens(block.Name) +
		EstimateTextTokens(block.ToolUseID) +
		EstimateTextTokens(block.Content)
	if len(block.Input) > 0 {
		total += EstimateTextTokens(string(block.Input))
	}
	if block.Source != nil {
		total += EstimateTextTokens(block.Source.Type)
		total += EstimateTextTokens(block.Source.MediaType)
		total += EstimateTextTokens(block.Source.URL)
		total += estimateInlineSourceTokens(block.Source.MediaType, block.Source.Data)
	}
	if len(block.Citations) > 0 {
		data, _ := json.Marshal(block.Citations)
		total += EstimateTextTokens(string(data))
	}
	if total == 0 {
		return 1
	}
	return total
}

// estimateInlineSourceTokens approximates the prompt cost of a base64 inline
// attachment. It must never route the payload through estimateTextTokens:
// providers charge images by pixel dimensions, not by transport size, so a 1 MB
// screenshot estimated as text became ~350k phantom tokens — a single image
// could force a compaction on its own.
//
// These are deliberately coarse estimates (the base64 blob carries no pixel
// dimensions and no page count), and they are allowed to be coarse because the
// reactive overflow fallback now catches under-estimation: an under-estimate
// costs one forced re-compaction and retry, while an over-estimate costs an
// unnecessary LLM summarization call on every subsequent turn.
func estimateInlineSourceTokens(mediaType, data string) int {
	if data == "" {
		return 0
	}
	// base64 carries 3 raw bytes per 4 characters.
	rawBytes := len(data) / 4 * 3
	if isImageMediaType(mediaType) {
		// Anthropic bills images at roughly width*height/750 tokens and caps
		// effective resolution around 1.15 megapixels, so ~1600 tokens is the
		// ceiling for any single image regardless of how large the file is.
		return estimatedImageBlockTokens
	}
	// Documents (PDF) scale with page count, which we cannot see either. PDFs
	// run on the order of 100 transport bytes per prompt token; floor the
	// result at the single-image ceiling so a tiny document is not free.
	tokens := rawBytes / documentBytesPerToken
	if tokens < estimatedImageBlockTokens {
		return estimatedImageBlockTokens
	}
	return tokens
}

func isImageMediaType(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "image/")
}

// EstimateTextTokens applies the compact package's shared coarse estimator.
// ASCII rounds up at four bytes per token; each non-ASCII rune costs one token.
func EstimateTextTokens(text string) int {
	return tokenestimate.Text(text)
}

func estimateTextTokens(text string) int { return EstimateTextTokens(text) }
