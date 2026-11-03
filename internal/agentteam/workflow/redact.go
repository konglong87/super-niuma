package workflow

import "regexp"

// Redact is a last-resort shape filter. Production additionally registers exact
// configured credentials in the provider adapter, before any Run is persisted.
var sensitiveValue = regexp.MustCompile(`(?i)(?:sk-[a-z0-9_-]{12,}|(?:bearer\s+)[a-z0-9._~+/=-]{8,}|(?:api[_-]?key|auth[_-]?token|access[_-]?token|password|secret)["'\s]*[:=]["'\s]*[^\s,"';&}]{4,})`)

func Redact(value string) string { return sensitiveValue.ReplaceAllString(value, "[redacted]") }
