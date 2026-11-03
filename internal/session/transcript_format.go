package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/konglong87/super-niuma/internal/product"
)

type TranscriptFormat string

const (
	TranscriptFormatEmpty            TranscriptFormat = "empty"
	TranscriptFormatGolangCCV1       TranscriptFormat = "golang_cc_v1"
	TranscriptFormatGolangCCV2       TranscriptFormat = "golang_cc_v2"
	TranscriptFormatClaudeCodeNative TranscriptFormat = "claude_code_native"
	TranscriptFormatUnknown          TranscriptFormat = "unknown"
	TranscriptFormatMixed            TranscriptFormat = "mixed"
)

func LoadWithFormat(path string) ([]Entry, TranscriptFormat, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, TranscriptFormatUnknown, err
	}
	return ParseWithFormat(data)
}

// ParseWithFormat decodes one already captured transcript byte snapshot. It is
// used by read-only consumers that must not mix entries from separate file reads.
func ParseWithFormat(data []byte) ([]Entry, TranscriptFormat, error) {

	var entries []Entry
	format := TranscriptFormatEmpty
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			return nil, TranscriptFormatUnknown, err
		}
		entry := Entry{Type: rawString(raw["type"])}
		lineFormat := detectTranscriptLineFormat(entry, raw)
		if err := json.Unmarshal(line, &entry); err != nil && lineFormat == TranscriptFormatGolangCCV1 {
			return nil, TranscriptFormatUnknown, err
		}
		entries = append(entries, entry)
		format = combineTranscriptFormat(format, lineFormat)
	}
	if err := scanner.Err(); err != nil {
		return nil, TranscriptFormatUnknown, err
	}
	return entries, format, nil
}

func ValidateResumeFormat(format TranscriptFormat) error {
	switch format {
	case TranscriptFormatEmpty, TranscriptFormatGolangCCV1, TranscriptFormatGolangCCV2:
		// v2 is resumed via leaf-walk (see session.CurrentChain): the current
		// conversation is the chain from the active leaf, not raw file order.
		return nil
	case TranscriptFormatClaudeCodeNative:
		return fmt.Errorf("unsupported transcript schema for resume: %s (Trace Viewer can inspect it read-only; migrate/import before resume)", format)
	case TranscriptFormatMixed:
		return fmt.Errorf("unsupported transcript schema for resume: %s (mixed transcript schemas cannot safely become model context)", format)
	default:
		return fmt.Errorf("unsupported transcript schema for resume: %s", format)
	}
}

func combineTranscriptFormat(current, next TranscriptFormat) TranscriptFormat {
	if next == TranscriptFormatEmpty {
		return current
	}
	if current == TranscriptFormatEmpty {
		return next
	}
	if current == next {
		return current
	}
	// niuma v1 and v2 are the same family: v2 is a superset, and a v2 file may
	// legitimately carry untagged legacy-typed lines (checkpoint/recap/…) written
	// by auxiliary appenders. Such a file is v2, not a dangerous schema mix. Only a
	// niuma <-> Claude-Code-native / unknown combination is truly mixed.
	if isGolangCCFormat(current) && isGolangCCFormat(next) {
		return TranscriptFormatGolangCCV2
	}
	return TranscriptFormatMixed
}

func isGolangCCFormat(format TranscriptFormat) bool {
	return format == TranscriptFormatGolangCCV1 || format == TranscriptFormatGolangCCV2
}

func detectTranscriptLineFormat(entry Entry, raw map[string]json.RawMessage) TranscriptFormat {
	entryType := strings.TrimSpace(entry.Type)
	schema := rawString(raw["schema"])
	switch {
	case strings.HasPrefix(schema, product.TranscriptSchemaV2),
		strings.HasPrefix(schema, product.LegacyTranscriptSchemaV2):
		return TranscriptFormatGolangCCV2
	case strings.HasPrefix(schema, product.TranscriptSchemaV1),
		strings.HasPrefix(schema, product.LegacyTranscriptSchemaV1):
		return TranscriptFormatGolangCCV1
	case isClaudeCodeNativeLine(entryType, raw):
		return TranscriptFormatClaudeCodeNative
	case isGolangCCV1EntryType(entryType):
		return TranscriptFormatGolangCCV1
	default:
		return TranscriptFormatUnknown
	}
}

func isGolangCCV1EntryType(entryType string) bool {
	switch entryType {
	case "session", "message", "thinking", "redacted_thinking", "orphaned_thinking",
		// image 承载 MCP 图片的侧车引用（TODO-080）。漏登记它的后果不是「少认一种
		// 行」而是整个文件被判成 Mixed，ValidateResumeFormat 会直接拒绝 resume。
		"image",
		EntryTypeSessionEvent,
		"tool_call", "tool_result", "usage", "permission", "file_change",
		"checkpoint", "rewind", "fork", "compact_summary", "recap_summary",
		"content_replacement",
		"prompt_context",
		"task_contract", "action_record", "evidence_record", "delta_record", "completion_gate",
		"provider_error", EntryTypeProviderContinuation, EntryTypeRuntimeSpan:
		return true
	default:
		return false
	}
}

func isClaudeCodeNativeLine(entryType string, raw map[string]json.RawMessage) bool {
	switch entryType {
	case "user", "assistant", "system":
		return rawHas(raw, "message") || rawHas(raw, "uuid") || rawHas(raw, "parentUuid") || rawHas(raw, "sessionId")
	default:
		return false
	}
}

func rawHas(raw map[string]json.RawMessage, key string) bool {
	value, ok := raw[key]
	return ok && len(value) > 0 && string(value) != "null"
}

func rawString(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
