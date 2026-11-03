// Package tokenestimate owns dependency-free token estimation primitives that
// may be shared by persistence codecs and higher-level context planners.
package tokenestimate

import "unicode/utf8"

// Text applies the runtime's coarse estimator: ASCII rounds up at four bytes
// per token and each non-ASCII rune costs one token.
func Text(value string) int {
	if value == "" {
		return 0
	}
	runes := utf8.RuneCountInString(value)
	ascii := 0
	for _, r := range value {
		if r <= 127 {
			ascii++
		}
	}
	tokens := (ascii+3)/4 + runes - ascii
	if tokens <= 0 {
		return 1
	}
	return tokens
}
