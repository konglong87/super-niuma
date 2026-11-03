package imagegen

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxSafeErrorMessageBytes = 1024

var (
	sensitiveImageValuePattern = regexp.MustCompile(`(?i)\b(authorization|api[_-]?key|token|password|secret)\s*[:=]\s*(?:bearer\s+)?[^\s,;]+`)
	jsonSensitiveValuePattern  = regexp.MustCompile(`(?i)(["'](?:authorization|api[_-]?key|token|password|secret|prompt|body|response(?:\s+body)?)["']\s*:\s*)("(?:\\.|[^"\\])*"|[^,}\]\s]+)`)
	dataURLPattern             = regexp.MustCompile(`(?i)data:[^\s;,]+;base64,[a-z0-9+/=_-]+`)
	urlPattern                 = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
	promptOrBodyPattern        = regexp.MustCompile(`(?is)\b(prompt|response(?:\s+body)?|body)\s*[:=].*`)
	longBase64Pattern          = regexp.MustCompile(`[A-Za-z0-9+/_-]{80,}={0,2}`)
)

// SanitizeErrorMessage keeps persisted diagnostics bounded and safe to expose
// to operators. Prompts, provider response bodies, credentials and private
// locations are never durable error text.
func SanitizeErrorMessage(message string) string {
	message = strings.ToValidUTF8(message, "?")
	message = jsonSensitiveValuePattern.ReplaceAllString(message, `${1}"[REDACTED]"`)
	message = promptOrBodyPattern.ReplaceAllString(message, "$1=[REDACTED]")
	message = dataURLPattern.ReplaceAllString(message, "[REDACTED_BASE64]")
	message = sensitiveImageValuePattern.ReplaceAllString(message, "$1=[REDACTED]")
	message = urlPattern.ReplaceAllString(message, "[REDACTED_URL]")
	message = longBase64Pattern.ReplaceAllString(message, "[REDACTED_BASE64]")
	return truncateUTF8(message, MaxSafeErrorMessageBytes)
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.RuneStart(value[maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}
