package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ImportResult reports the outcome of importing an original Claude Code
// transcript into a new niuma message-graph session using the stable v2 schema.
type ImportResult struct {
	SessionID    string         `json:"session_id"`
	Path         string         `json:"path"`
	SourcePath   string         `json:"source_path"`
	Messages     int            `json:"messages"`
	ToolCalls    int            `json:"tool_calls"`
	ToolResults  int            `json:"tool_results"`
	SkippedCount int            `json:"skipped_count"`
	Skipped      map[string]int `json:"skipped,omitempty"`
}

// nativeEntry is the subset of an original Claude Code transcript line the
// importer understands. Unknown fields are ignored.
type nativeEntry struct {
	Type        string        `json:"type"`
	UUID        string        `json:"uuid"`
	ParentUUID  string        `json:"parentUuid"`
	IsSidechain bool          `json:"isSidechain"`
	Message     nativeMessage `json:"message"`
}

type nativeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type nativeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ImportClaudeCode reads an original Claude Code transcript (read-only) and
// writes a best-effort v2 message-graph copy as a NEW niuma session. Only the
// main chain (user/assistant text, tool_use, tool_result, thinking) is converted;
// sidechains, system lines, and unknown block types are skipped and counted in
// the report. The source file is never modified.
func ImportClaudeCode(store Store, srcPath, cwd string) (ImportResult, error) {
	entries, err := loadNativeEntries(srcPath)
	if err != nil {
		return ImportResult{}, err
	}
	if len(entries) == 0 {
		return ImportResult{}, fmt.Errorf("no importable Claude Code entries in %s", srcPath)
	}
	chain, skipped := nativeMainChain(entries)
	if len(chain) == 0 {
		return ImportResult{}, fmt.Errorf("could not reconstruct a main chain from %s", srcPath)
	}

	importStore := store
	importStore.SchemaV2 = true
	rec, err := importStore.NewRecorder(cwd)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{SessionID: rec.SessionID, Path: rec.Path, SourcePath: srcPath, Skipped: map[string]int{}}
	for reason, n := range skipped {
		result.Skipped[reason] += n
	}
	// Record provenance so the imported session is traceable to its origin.
	provenance, _ := json.Marshal(map[string]any{
		"migrated_from": map[string]any{
			"schema": "claude_code_native",
			"path":   srcPath,
		},
	})
	_ = rec.Append(Entry{Type: "session", Name: "imported from Claude Code", Metadata: provenance})

	for _, native := range chain {
		msgs, tcs, trs, blockSkips := appendNativeEntry(rec, native)
		result.Messages += msgs
		result.ToolCalls += tcs
		result.ToolResults += trs
		for reason, n := range blockSkips {
			result.Skipped[reason] += n
		}
	}
	if err := rec.Close(); err != nil {
		return ImportResult{}, err
	}
	for _, n := range result.Skipped {
		result.SkippedCount += n
	}
	if len(result.Skipped) == 0 {
		result.Skipped = nil
	}
	return result, nil
}

// loadNativeEntries parses an original Claude Code JSONL, keeping only lines that
// look native (have a uuid or a message). It never writes the file.
func loadNativeEntries(path string) ([]nativeEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []nativeEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var entry nativeEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.UUID == "" && entry.Type == "" {
			continue
		}
		out = append(out, entry)
	}
	return out, scanner.Err()
}

// nativeMainChain reconstructs the main conversation chain by walking parentUuid
// from the last non-sidechain leaf to the root. Sidechain entries are excluded
// and reported. Physical order is used as a tiebreaker for choosing the leaf.
func nativeMainChain(entries []nativeEntry) ([]nativeEntry, map[string]int) {
	skipped := map[string]int{}
	byID := make(map[string]nativeEntry, len(entries))
	hasChild := make(map[string]bool)
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsSidechain {
			skipped["sidechain"]++
			continue
		}
		// Only user/assistant entries are conversation nodes. system/summary and
		// other line types are not part of the main chain (reported, not chained),
		// so they cannot hijack leaf selection.
		if entry.Type != "user" && entry.Type != "assistant" {
			reason := strings.TrimSpace(entry.Type)
			if reason == "" {
				reason = "non-conversation"
			}
			skipped[reason]++
			continue
		}
		if entry.UUID == "" {
			skipped["no-uuid"]++
			continue
		}
		byID[entry.UUID] = entry
		order = append(order, entry.UUID)
		if entry.ParentUUID != "" {
			hasChild[entry.ParentUUID] = true
		}
	}
	// Leaf: the last-in-file entry that has no children (a branch tip). Fall back
	// to the physically last entry.
	leaf := ""
	for i := len(order) - 1; i >= 0; i-- {
		if !hasChild[order[i]] {
			leaf = order[i]
			break
		}
	}
	if leaf == "" && len(order) > 0 {
		leaf = order[len(order)-1]
	}
	var reversed []nativeEntry
	seen := map[string]bool{}
	for id := leaf; id != ""; {
		entry, ok := byID[id]
		if !ok || seen[id] {
			break
		}
		seen[id] = true
		reversed = append(reversed, entry)
		id = entry.ParentUUID
	}
	chain := make([]nativeEntry, len(reversed))
	for i, entry := range reversed {
		chain[len(reversed)-1-i] = entry
	}
	return chain, skipped
}

// appendNativeEntry converts one native entry's content into v2 entries and
// appends them through the recorder (which chains them via parent_id). Returns
// counts of messages/tool_calls/tool_results emitted and any skipped block types.
func appendNativeEntry(rec *Recorder, native nativeEntry) (messages, toolCalls, toolResults int, skipped map[string]int) {
	skipped = map[string]int{}
	role := strings.TrimSpace(native.Message.Role)
	if role == "" {
		role = native.Type
	}
	if role != "user" && role != "assistant" {
		skipped[role]++
		return
	}
	// Content may be a bare string or an array of typed blocks.
	if text, ok := decodeStringContent(native.Message.Content); ok {
		if strings.TrimSpace(text) != "" {
			_ = rec.Append(Entry{Type: "message", Role: role, Content: text})
			messages++
		}
		return
	}
	var blocks []nativeBlock
	if err := json.Unmarshal(native.Message.Content, &blocks); err != nil {
		skipped["unparsable-content"]++
		return
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				_ = rec.Append(Entry{Type: "message", Role: role, Content: block.Text})
				messages++
			}
		case "thinking", "redacted_thinking":
			_ = rec.Append(Entry{Type: block.Type, Role: "assistant", Content: block.Thinking, Signature: block.Signature})
		case "tool_use":
			_ = rec.Append(Entry{Type: "tool_call", ToolID: block.ID, ToolName: block.Name, Content: string(block.Input)})
			toolCalls++
		case "tool_result":
			_ = rec.Append(Entry{Type: "tool_result", ToolID: block.ToolUseID, Content: decodeToolResultContent(block.Content), IsError: block.IsError})
			toolResults++
		default:
			skipped["block:"+block.Type]++
		}
	}
	return
}

// decodeStringContent reports whether content is a bare JSON string and returns it.
func decodeStringContent(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "\"") {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// decodeToolResultContent flattens a tool_result content (string or array of text
// blocks) into plain text, best-effort.
func decodeToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if s, ok := decodeStringContent(raw); ok {
		return s
	}
	var blocks []nativeBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return string(raw)
	}
	var parts []string
	for _, block := range blocks {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
