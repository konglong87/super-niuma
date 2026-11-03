package compact

import "strings"

// overflowMarkers are the provider error signatures that mean "this prompt does
// not fit in the model's context window". They are matched against the error
// text rather than a status code because the wording, not the code, is what
// distinguishes a too-long prompt from every other 400 invalid_request_error.
//
// Deliberately narrow: a false positive throws away conversation history for an
// error compaction cannot fix, so only signatures actually emitted by providers
// for context overflow belong here.
var overflowMarkers = []string{
	// OpenAI / OpenAI-compatible gateways.
	"context_length_exceeded",
	"maximum context length",
	"reduce the length of the messages",
	// Anthropic.
	"prompt is too long",
	"input length and `max_tokens` exceed context limit",
	// Common wording across self-hosted and proxy gateways.
	"context window",
	"too many tokens",
}

// IsContextOverflowError reports whether err says the request exceeded the
// model's context window, i.e. whether forcing a compaction and retrying is
// worth attempting.
//
// This is intentionally separate from the provider-fallback decision in
// internal/anthropic: context overflow is a deterministic 400 that every
// fallback provider with a similar window would reject identically, so
// canFallbackAfterError correctly declines it. Compaction is the only recovery.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range overflowMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
