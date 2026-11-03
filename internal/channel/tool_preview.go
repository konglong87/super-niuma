package channel

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	ToolCommandPreviewLimit = 240
	ToolOutputPreviewLimit  = 600
)

var (
	toolPreviewSecretAssignment = regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret|cookie)\b\s*[:=]\s*)([^\s,;]+)`)
	toolPreviewBearerToken      = regexp.MustCompile(`(?i)(\bbearer\s+)([^\s]+)`)
	toolPreviewJSONSecret       = regexp.MustCompile(`(?i)("(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret|cookie)"\s*:\s*")([^"]+)(")`)
)

// ToolCommandPreview extracts the primary human-readable input for a tool.
// It intentionally avoids rendering arbitrary raw JSON in external cards.
func ToolCommandPreview(toolName string, input json.RawMessage) string {
	var values map[string]any
	if err := json.Unmarshal(input, &values); err == nil {
		for _, key := range []string{"command", "file_path", "path", "pattern", "url", "query", "prompt"} {
			if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
				return truncateToolPreview(sanitizeToolPreview(value), ToolCommandPreviewLimit, false)
			}
		}
	}
	return truncateToolPreview(sanitizeToolPreview(strings.TrimSpace(toolName)), ToolCommandPreviewLimit, false)
}

// ToolOutputPreview returns a bounded, credential-scrubbed display preview.
// Failed output keeps the tail because command errors commonly end there.
func ToolOutputPreview(output string, isError bool) (string, bool) {
	clean := sanitizeToolPreview(strings.TrimSpace(output))
	return truncateToolPreview(clean, ToolOutputPreviewLimit, isError), utf8.RuneCountInString(clean) > ToolOutputPreviewLimit
}

func sanitizeToolPreview(value string) string {
	value = toolPreviewSecretAssignment.ReplaceAllString(value, `${1}[redacted]`)
	value = toolPreviewBearerToken.ReplaceAllString(value, `${1}[redacted]`)
	return toolPreviewJSONSecret.ReplaceAllString(value, `${1}[redacted]${3}`)
}

func truncateToolPreview(value string, limit int, tail bool) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	marker := "..."
	keep := limit - utf8.RuneCountInString(marker)
	if keep <= 0 {
		return string([]rune(value)[:limit])
	}
	runes := []rune(value)
	if tail {
		return marker + string(runes[len(runes)-keep:])
	}
	return string(runes[:keep]) + marker
}
