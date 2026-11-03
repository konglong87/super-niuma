package recap

import (
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/session"
)

func Latest(entries []session.Entry) (session.Entry, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type != EntryType || strings.TrimSpace(entry.Content) == "" {
			continue
		}
		if isInvalidated(entry) {
			return session.Entry{}, false
		}
		if entry.Type == EntryType {
			return entry, true
		}
	}
	return session.Entry{}, false
}

func isInvalidated(entry session.Entry) bool {
	if len(entry.Metadata) == 0 {
		return false
	}
	var metadata struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(entry.Metadata, &metadata); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(metadata.Status), "invalidated")
}

func FormatForDisplay(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "※ recap:\n" + indent(text, "  ")
}

func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
