package promptdump

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

const (
	DumpFormatGo              = "niuma-summary"
	DumpFormatUpstream        = "claude-code-delta"
	DumpFormatUpstreamRequest = "claude-code-request-capture"
)

type DumpProfile struct {
	Path        string            `json:"path"`
	Format      string            `json:"format"`
	Init        DumpInitProfile   `json:"init"`
	Turns       []DumpTurnProfile `json:"turns,omitempty"`
	Totals      DumpTotals        `json:"totals"`
	Limitations []string          `json:"limitations,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
}

type DumpInitProfile struct {
	Model            string            `json:"model,omitempty"`
	MaxTokens        int               `json:"max_tokens,omitempty"`
	SystemBytes      int               `json:"system_bytes"`
	SystemBlockCount int               `json:"system_block_count"`
	SystemBlocks     []DumpSystemBlock `json:"system_blocks,omitempty"`
	ToolCount        int               `json:"tool_count"`
	ToolNames        []string          `json:"tool_names,omitempty"`
	Tools            []DumpToolProfile `json:"tools,omitempty"`
}

type DumpSystemBlock struct {
	Index           int    `json:"index"`
	TextBytes       int    `json:"text_bytes"`
	TextHash        string `json:"text_hash,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Source          string `json:"source,omitempty"`
	HasCacheControl bool   `json:"has_cache_control,omitempty"`
	CacheType       string `json:"cache_type,omitempty"`
	CacheTTL        string `json:"cache_ttl,omitempty"`
	CacheScope      string `json:"cache_scope,omitempty"`
}

type DumpToolProfile struct {
	Name              string   `json:"name"`
	DescriptionBytes  int      `json:"description_bytes,omitempty"`
	InputSchemaBytes  int      `json:"input_schema_bytes,omitempty"`
	InputSchemaHash   string   `json:"input_schema_hash,omitempty"`
	InputSchemaFields []string `json:"input_schema_fields,omitempty"`
}

type DumpTurnProfile struct {
	Index             int      `json:"index"`
	Turn              int      `json:"turn,omitempty"`
	SourceType        string   `json:"source_type,omitempty"`
	Role              string   `json:"role,omitempty"`
	MessageCount      int      `json:"message_count,omitempty"`
	ToolCount         int      `json:"tool_count,omitempty"`
	RuntimeSections   []string `json:"runtime_sections,omitempty"`
	ToolResultCount   int      `json:"tool_result_count,omitempty"`
	ToolResultBytes   int      `json:"tool_result_bytes,omitempty"`
	ToolResultErrors  int      `json:"tool_result_errors,omitempty"`
	PersistedOutput   int      `json:"persisted_output,omitempty"`
	RawOverLimit      int      `json:"raw_over_limit,omitempty"`
	RawOverDefault    int      `json:"raw_over_default_limit,omitempty"`
	TextBytes         int      `json:"text_bytes,omitempty"`
	ToolUseCount      int      `json:"tool_use_count,omitempty"`
	ContentBlockCount int      `json:"content_block_count,omitempty"`
}

type DumpTotals struct {
	Records             int `json:"records"`
	Requests            int `json:"requests,omitempty"`
	InitEntries         int `json:"init_entries,omitempty"`
	SystemUpdateEntries int `json:"system_update_entries,omitempty"`
	MessageEntries      int `json:"message_entries,omitempty"`
	ResponseEntries     int `json:"response_entries,omitempty"`
	UserMessages        int `json:"user_messages,omitempty"`
	AssistantMessages   int `json:"assistant_messages,omitempty"`
	ToolResults         int `json:"tool_results,omitempty"`
	ToolResultBytes     int `json:"tool_result_bytes,omitempty"`
	ToolResultErrors    int `json:"tool_result_errors,omitempty"`
	ToolUses            int `json:"tool_uses,omitempty"`
}

type CompareReport struct {
	OK          bool                `json:"ok"`
	Go          DumpProfile         `json:"go"`
	Upstream    DumpProfile         `json:"upstream"`
	Differences []CompareDifference `json:"differences,omitempty"`
	Limitations []string            `json:"limitations,omitempty"`
}

type CompareDifference struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Go       string `json:"go,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Detail   string `json:"detail"`
}

type upstreamEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	Data      json.RawMessage `json:"data"`
}

type upstreamRequestCapture struct {
	Timestamp string          `json:"timestamp,omitempty"`
	Call      int             `json:"call,omitempty"`
	Body      json.RawMessage `json:"body"`
}

type contentProfile struct {
	blocks          int
	textBytes       int
	toolUses        int
	toolResults     int
	toolResultBytes int
	toolErrors      int
}

func LoadDumpProfile(path string) (DumpProfile, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return DumpProfile{}, err
	}
	if len(lines) == 0 {
		return DumpProfile{}, fmt.Errorf("prompt dump %q has no records", path)
	}
	format, err := detectDumpFormat(lines[0])
	if err != nil {
		return DumpProfile{}, fmt.Errorf("detect prompt dump %q: %w", path, err)
	}
	switch format {
	case DumpFormatGo:
		records, err := recordsFromLines(path, lines)
		if err != nil {
			return DumpProfile{}, err
		}
		return profileGoDump(path, records), nil
	case DumpFormatUpstream:
		entries, err := upstreamEntriesFromLines(path, lines)
		if err != nil {
			return DumpProfile{}, err
		}
		return profileUpstreamDump(path, entries), nil
	case DumpFormatUpstreamRequest:
		requests, err := upstreamRequestCapturesFromLines(path, lines)
		if err != nil {
			return DumpProfile{}, err
		}
		return profileUpstreamRequestCapture(path, requests), nil
	default:
		return DumpProfile{}, fmt.Errorf("unsupported prompt dump format %q", format)
	}
}

func ComparePromptDumps(goPath, upstreamPath string) (CompareReport, error) {
	goProfile, err := LoadDumpProfile(goPath)
	if err != nil {
		return CompareReport{}, err
	}
	upstreamProfile, err := LoadDumpProfile(upstreamPath)
	if err != nil {
		return CompareReport{}, err
	}
	if goProfile.Format != DumpFormatGo {
		return CompareReport{}, fmt.Errorf("go dump %q has format %q, want %q", goPath, goProfile.Format, DumpFormatGo)
	}
	if upstreamProfile.Format != DumpFormatUpstream && upstreamProfile.Format != DumpFormatUpstreamRequest {
		return CompareReport{}, fmt.Errorf("upstream dump %q has format %q, want %q or %q", upstreamPath, upstreamProfile.Format, DumpFormatUpstream, DumpFormatUpstreamRequest)
	}
	report := CompareReport{
		Go:       goProfile,
		Upstream: upstreamProfile,
	}
	if upstreamProfile.Format == DumpFormatUpstream {
		report.Limitations = append(report.Limitations, "upstream Claude Code dumpPrompts JSONL is delta-shaped: init/system_update plus new user messages and responses, not a full request snapshot per model turn")
	}
	report.Limitations = append(report.Limitations, goProfile.Limitations...)
	report.Limitations = append(report.Limitations, upstreamProfile.Limitations...)
	report.Differences = compareInitProfiles(goProfile.Init, upstreamProfile.Init)
	report.OK = true
	for _, diff := range report.Differences {
		if diff.Severity == "error" {
			report.OK = false
			break
		}
	}
	return report, nil
}

func readJSONLLines(path string) ([]json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open prompt dump %q: %w", path, err)
	}
	defer file.Close()
	var lines []json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &probe); err != nil {
			return nil, fmt.Errorf("parse prompt dump %q line %d: %w", path, lineNo, err)
		}
		lines = append(lines, append(json.RawMessage(nil), []byte(text)...))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read prompt dump %q: %w", path, err)
	}
	return lines, nil
}

func detectDumpFormat(line json.RawMessage) (string, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		return "", err
	}
	if _, ok := probe["schema_version"]; ok {
		return DumpFormatGo, nil
	}
	if _, ok := probe["type"]; ok {
		return DumpFormatUpstream, nil
	}
	if bodyRaw, ok := probe["body"]; ok {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(bodyRaw, &body); err == nil {
			if _, hasModel := body["model"]; hasModel {
				if _, hasMessages := body["messages"]; hasMessages {
					return DumpFormatUpstreamRequest, nil
				}
			}
		}
	}
	return "", fmt.Errorf("missing schema_version or type")
}

func recordsFromLines(path string, lines []json.RawMessage) ([]Record, error) {
	records := make([]Record, 0, len(lines))
	for i, line := range lines {
		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, fmt.Errorf("parse go prompt dump %q line %d: %w", path, i+1, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func upstreamEntriesFromLines(path string, lines []json.RawMessage) ([]upstreamEntry, error) {
	entries := make([]upstreamEntry, 0, len(lines))
	for i, line := range lines {
		var entry upstreamEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("parse upstream prompt dump %q line %d: %w", path, i+1, err)
		}
		if entry.Type == "" {
			return nil, fmt.Errorf("parse upstream prompt dump %q line %d: missing type", path, i+1)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func upstreamRequestCapturesFromLines(path string, lines []json.RawMessage) ([]upstreamRequestCapture, error) {
	requests := make([]upstreamRequestCapture, 0, len(lines))
	for i, line := range lines {
		var request upstreamRequestCapture
		if err := json.Unmarshal(line, &request); err != nil {
			return nil, fmt.Errorf("parse upstream request capture %q line %d: %w", path, i+1, err)
		}
		if len(request.Body) == 0 || string(request.Body) == "null" {
			return nil, fmt.Errorf("parse upstream request capture %q line %d: missing body", path, i+1)
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func profileGoDump(path string, records []Record) DumpProfile {
	profile := DumpProfile{
		Path:   path,
		Format: DumpFormatGo,
		Totals: DumpTotals{
			Records:  len(records),
			Requests: len(records),
		},
	}
	if len(records) == 0 {
		return profile
	}
	first := records[0]
	profile.Init = DumpInitProfile{
		Model:            first.Model,
		MaxTokens:        first.MaxTokens,
		SystemBytes:      first.SystemBytes,
		SystemBlockCount: first.SystemBlockCount,
		SystemBlocks:     systemBlocksFromSummaries(first.SystemBlocks),
		ToolCount:        first.ToolCount,
		ToolNames:        toolNamesFromSummaries(first.ToolsSummary),
		Tools:            toolProfilesFromSummaries(first.ToolsSummary),
	}
	profile.Turns = make([]DumpTurnProfile, 0, len(records))
	for i, record := range records {
		turn := DumpTurnProfile{
			Index:            i + 1,
			Turn:             record.Turn,
			SourceType:       "request",
			MessageCount:     record.MessageCount,
			ToolCount:        record.ToolCount,
			RuntimeSections:  record.RuntimeStatus.Sections,
			ToolResultCount:  record.ToolResultStats.Count,
			ToolResultBytes:  record.ToolResultStats.TotalBytes,
			ToolResultErrors: record.ToolResultStats.ErrorCount,
			PersistedOutput:  record.ToolResultStats.PersistedOutput,
			RawOverLimit:     record.ToolResultStats.RawOverLimit,
			RawOverDefault:   record.ToolResultStats.RawOverDefaultLimit,
		}
		for _, message := range record.MessagesSummary {
			switch message.Role {
			case "user":
				profile.Totals.UserMessages++
			case "assistant":
				profile.Totals.AssistantMessages++
			}
			for _, block := range message.Blocks {
				switch block.Type {
				case "tool_use":
					turn.ToolUseCount++
					profile.Totals.ToolUses++
				case "tool_result":
					if block.IsError {
						profile.Totals.ToolResultErrors++
					}
				case "text":
					turn.TextBytes += block.TextBytes
				}
				turn.ContentBlockCount++
			}
		}
		profile.Totals.ToolResults += record.ToolResultStats.Count
		profile.Totals.ToolResultBytes += record.ToolResultStats.TotalBytes
		profile.Totals.ToolResultErrors += record.ToolResultStats.ErrorCount
		profile.Turns = append(profile.Turns, turn)
	}
	return profile
}

func profileUpstreamDump(path string, entries []upstreamEntry) DumpProfile {
	profile := DumpProfile{
		Path:   path,
		Format: DumpFormatUpstream,
		Totals: DumpTotals{Records: len(entries)},
		Limitations: []string{
			"original dumpPrompts writes only new user messages after init; complete per-turn message history cannot be reconstructed from this dump alone",
		},
	}
	for _, entry := range entries {
		switch entry.Type {
		case "init":
			profile.Totals.InitEntries++
			initProfile, warnings := summarizeUpstreamInit(entry.Data)
			if profile.Totals.InitEntries == 1 {
				profile.Init = initProfile
			}
			profile.Warnings = append(profile.Warnings, warnings...)
		case "system_update":
			profile.Totals.SystemUpdateEntries++
		case "message":
			profile.Totals.MessageEntries++
			turn := summarizeUpstreamMessage(profile.Totals.MessageEntries, entry.Data)
			profile.Turns = append(profile.Turns, turn)
			if turn.Role == "user" {
				profile.Totals.UserMessages++
			}
			if turn.Role == "assistant" {
				profile.Totals.AssistantMessages++
			}
			profile.Totals.ToolResults += turn.ToolResultCount
			profile.Totals.ToolResultBytes += turn.ToolResultBytes
			profile.Totals.ToolResultErrors += turn.ToolResultErrors
			profile.Totals.ToolUses += turn.ToolUseCount
		case "response":
			profile.Totals.ResponseEntries++
			summary := summarizeUpstreamResponse(entry.Data)
			profile.Totals.AssistantMessages += summary.assistantMessages
			profile.Totals.ToolUses += summary.toolUses
		default:
			profile.Warnings = append(profile.Warnings, fmt.Sprintf("unknown upstream entry type %q", entry.Type))
		}
	}
	return profile
}

func profileUpstreamRequestCapture(path string, requests []upstreamRequestCapture) DumpProfile {
	profile := DumpProfile{
		Path:   path,
		Format: DumpFormatUpstreamRequest,
		Totals: DumpTotals{
			Records:  len(requests),
			Requests: len(requests),
		},
		Limitations: []string{
			"upstream request capture is collected by a local fetch wrapper, not Claude Code dumpPrompts; it may include retry attempts or mock-SSE driven turns",
		},
	}
	for i, request := range requests {
		body := requestBodyMap(request.Body)
		if i == 0 {
			systemBlocks := summarizeSystemBlocksRaw(body["system"])
			tools := summarizeToolsRaw(body["tools"])
			profile.Init = DumpInitProfile{
				Model:            stringField(body["model"]),
				MaxTokens:        intField(body["max_tokens"]),
				SystemBytes:      systemBlocksTotalBytes(systemBlocks),
				SystemBlockCount: len(systemBlocks),
				SystemBlocks:     systemBlocks,
				ToolCount:        len(tools),
				ToolNames:        toolNamesFromProfiles(tools),
				Tools:            tools,
			}
		}
		turn := summarizeUpstreamRequest(i+1, body)
		profile.Turns = append(profile.Turns, turn)
		userMessages, assistantMessages := countRequestMessageRoles(body["messages"])
		profile.Totals.UserMessages += userMessages
		profile.Totals.AssistantMessages += assistantMessages
		profile.Totals.ToolResults += turn.ToolResultCount
		profile.Totals.ToolResultBytes += turn.ToolResultBytes
		profile.Totals.ToolResultErrors += turn.ToolResultErrors
		profile.Totals.ToolUses += turn.ToolUseCount
	}
	return profile
}

func requestBodyMap(raw json.RawMessage) map[string]json.RawMessage {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return map[string]json.RawMessage{}
	}
	return body
}

func summarizeUpstreamRequest(index int, body map[string]json.RawMessage) DumpTurnProfile {
	turn := DumpTurnProfile{
		Index:      index,
		Turn:       index,
		SourceType: "request",
		ToolCount:  len(summarizeToolNamesRaw(body["tools"])),
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(body["messages"], &messages); err != nil {
		return turn
	}
	turn.MessageCount = len(messages)
	for _, message := range messages {
		content := summarizeContentRaw(message["content"])
		turn.ContentBlockCount += content.blocks
		turn.TextBytes += content.textBytes
		turn.ToolUseCount += content.toolUses
		turn.ToolResultCount += content.toolResults
		turn.ToolResultBytes += content.toolResultBytes
		turn.ToolResultErrors += content.toolErrors
	}
	return turn
}

func countRequestMessageRoles(raw json.RawMessage) (int, int) {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return 0, 0
	}
	userMessages := 0
	assistantMessages := 0
	for _, message := range messages {
		switch stringField(message["role"]) {
		case "user":
			userMessages++
		case "assistant":
			assistantMessages++
		}
	}
	return userMessages, assistantMessages
}

func summarizeUpstreamInit(raw json.RawMessage) (DumpInitProfile, []string) {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return DumpInitProfile{}, []string{fmt.Sprintf("parse upstream init: %v", err)}
	}
	systemBlocks := summarizeSystemBlocksRaw(data["system"])
	tools := summarizeToolsRaw(data["tools"])
	return DumpInitProfile{
		Model:            stringField(data["model"]),
		MaxTokens:        intField(data["max_tokens"]),
		SystemBytes:      systemBlocksTotalBytes(systemBlocks),
		SystemBlockCount: len(systemBlocks),
		SystemBlocks:     systemBlocks,
		ToolCount:        len(tools),
		ToolNames:        toolNamesFromProfiles(tools),
		Tools:            tools,
	}, nil
}

func summarizeUpstreamMessage(index int, raw json.RawMessage) DumpTurnProfile {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return DumpTurnProfile{Index: index, SourceType: "message", Role: "parse_error"}
	}
	content := summarizeContentRaw(data["content"])
	return DumpTurnProfile{
		Index:             index,
		SourceType:        "message",
		Role:              stringField(data["role"]),
		ContentBlockCount: content.blocks,
		TextBytes:         content.textBytes,
		ToolUseCount:      content.toolUses,
		ToolResultCount:   content.toolResults,
		ToolResultBytes:   content.toolResultBytes,
		ToolResultErrors:  content.toolErrors,
	}
}

type upstreamResponseSummary struct {
	assistantMessages int
	toolUses          int
}

func summarizeUpstreamResponse(raw json.RawMessage) upstreamResponseSummary {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return upstreamResponseSummary{}
	}
	if streamRaw, ok := data["stream"]; ok {
		var streaming bool
		_ = json.Unmarshal(streamRaw, &streaming)
		if streaming {
			var chunks []map[string]json.RawMessage
			if err := json.Unmarshal(data["chunks"], &chunks); err != nil {
				return upstreamResponseSummary{assistantMessages: 1}
			}
			summary := upstreamResponseSummary{assistantMessages: 1}
			for _, chunk := range chunks {
				if stringField(chunk["type"]) != "content_block_start" {
					continue
				}
				var block map[string]json.RawMessage
				if err := json.Unmarshal(chunk["content_block"], &block); err == nil && stringField(block["type"]) == "tool_use" {
					summary.toolUses++
				}
			}
			return summary
		}
	}
	content := summarizeContentRaw(data["content"])
	if content.blocks > 0 || content.toolUses > 0 || content.textBytes > 0 {
		return upstreamResponseSummary{assistantMessages: 1, toolUses: content.toolUses}
	}
	return upstreamResponseSummary{}
}

func summarizeSystemRaw(raw json.RawMessage) (int, int) {
	blocks := summarizeSystemBlocksRaw(raw)
	return systemBlocksTotalBytes(blocks), len(blocks)
}

func summarizeSystemBlocksRaw(raw json.RawMessage) []DumpSystemBlock {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []DumpSystemBlock{dumpSystemBlockFromText(0, text, nil)}
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return []DumpSystemBlock{dumpSystemBlockFromText(0, string(raw), nil)}
	}
	out := make([]DumpSystemBlock, 0, len(blocks))
	for i, block := range blocks {
		text := stringField(block["text"])
		if text == "" {
			if encoded, err := json.Marshal(block); err == nil {
				text = string(encoded)
			}
		}
		out = append(out, dumpSystemBlockFromText(i, text, cacheControlRaw(block["cache_control"])))
	}
	return out
}

func dumpSystemBlockFromText(index int, text string, cache *anthropicCacheProfile) DumpSystemBlock {
	out := DumpSystemBlock{
		Index:     index,
		TextBytes: len(text),
		TextHash:  hashString(text),
		Kind:      classifySystemBlock(text),
	}
	if cache != nil {
		out.HasCacheControl = true
		out.CacheType = cache.Type
		out.CacheTTL = cache.TTL
		out.CacheScope = cache.Scope
	}
	return out
}

type anthropicCacheProfile struct {
	Type  string `json:"type"`
	TTL   string `json:"ttl,omitempty"`
	Scope string `json:"scope,omitempty"`
}

func cacheControlRaw(raw json.RawMessage) *anthropicCacheProfile {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cache anthropicCacheProfile
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil
	}
	if cache.Type == "" && cache.TTL == "" && cache.Scope == "" {
		return nil
	}
	return &cache
}

func systemBlocksTotalBytes(blocks []DumpSystemBlock) int {
	total := 0
	for _, block := range blocks {
		total += block.TextBytes
	}
	return total
}

func summarizeToolNamesRaw(raw json.RawMessage) []string {
	return toolNamesFromProfiles(summarizeToolsRaw(raw))
}

func summarizeToolsRaw(raw json.RawMessage) []DumpToolProfile {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	out := make([]DumpToolProfile, 0, len(tools))
	for _, tool := range tools {
		name := stringField(tool["name"])
		if name != "" {
			schema := tool["input_schema"]
			out = append(out, DumpToolProfile{
				Name:              name,
				DescriptionBytes:  len(stringField(tool["description"])),
				InputSchemaBytes:  len(schema),
				InputSchemaHash:   hashRawJSON(schema),
				InputSchemaFields: inputSchemaFieldNames(schema),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func hashRawJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		if canonical, err := json.Marshal(value); err == nil {
			raw = canonical
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func summarizeContentRaw(raw json.RawMessage) contentProfile {
	if len(raw) == 0 || string(raw) == "null" {
		return contentProfile{}
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return contentProfile{blocks: 1, textBytes: len(text)}
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return contentProfile{blocks: 1, textBytes: len(raw)}
	}
	var profile contentProfile
	profile.blocks = len(blocks)
	for _, block := range blocks {
		switch stringField(block["type"]) {
		case "text":
			profile.textBytes += len(stringField(block["text"]))
		case "tool_use":
			profile.toolUses++
		case "tool_result":
			profile.toolResults++
			profile.toolResultBytes += contentBytes(block["content"])
			if boolField(block["is_error"]) {
				profile.toolErrors++
			}
		default:
			profile.textBytes += len(stringField(block["text"]))
		}
	}
	return profile
}

func contentBytes(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return len(text)
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err == nil {
		total := 0
		for _, block := range blocks {
			total += len(stringField(block["text"]))
		}
		return total
	}
	return len(raw)
}

func compareInitProfiles(goInit, upstreamInit DumpInitProfile) []CompareDifference {
	var diffs []CompareDifference
	if goInit.Model != "" && upstreamInit.Model != "" && goInit.Model != upstreamInit.Model {
		diffs = append(diffs, CompareDifference{
			Severity: "error",
			Field:    "model",
			Go:       goInit.Model,
			Upstream: upstreamInit.Model,
			Detail:   "model ids differ; same-model comparison is not established",
		})
	}
	if goInit.MaxTokens != 0 && upstreamInit.MaxTokens != 0 && goInit.MaxTokens != upstreamInit.MaxTokens {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    "max_tokens",
			Go:       fmt.Sprintf("%d", goInit.MaxTokens),
			Upstream: fmt.Sprintf("%d", upstreamInit.MaxTokens),
			Detail:   "max_tokens differs and can affect answer length or final-turn behavior",
		})
	}
	if goInit.SystemBytes > 0 && upstreamInit.SystemBytes > 0 {
		diff := math.Abs(float64(goInit.SystemBytes - upstreamInit.SystemBytes))
		larger := math.Max(float64(goInit.SystemBytes), float64(upstreamInit.SystemBytes))
		if diff > 500 && diff/larger > 0.10 {
			diffs = append(diffs, CompareDifference{
				Severity: "warn",
				Field:    "system_bytes",
				Go:       fmt.Sprintf("%d", goInit.SystemBytes),
				Upstream: fmt.Sprintf("%d", upstreamInit.SystemBytes),
				Detail:   "system prompt size differs by more than 10%; inspect full dumps before attributing behavior to runtime state only",
			})
		}
	}
	if goInit.SystemBlockCount != upstreamInit.SystemBlockCount {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    "system_block_count",
			Go:       fmt.Sprintf("%d", goInit.SystemBlockCount),
			Upstream: fmt.Sprintf("%d", upstreamInit.SystemBlockCount),
			Detail:   "system block count differs; prompt cache boundaries and attention layout may differ",
		})
	}
	diffs = append(diffs, compareSystemBlocks(goInit.SystemBlocks, upstreamInit.SystemBlocks)...)
	if goInit.ToolCount != upstreamInit.ToolCount {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    "tool_count",
			Go:       fmt.Sprintf("%d", goInit.ToolCount),
			Upstream: fmt.Sprintf("%d", upstreamInit.ToolCount),
			Detail:   "available tool count differs; tool choice behavior may not be comparable",
		})
	}
	onlyGo, onlyUpstream := stringSetDiff(goInit.ToolNames, upstreamInit.ToolNames)
	if len(onlyGo) > 0 {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    "tool_names.only_go",
			Go:       strings.Join(onlyGo, ","),
			Detail:   "tools exposed only by niuma",
		})
	}
	if len(onlyUpstream) > 0 {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    "tool_names.only_upstream",
			Upstream: strings.Join(onlyUpstream, ","),
			Detail:   "tools exposed only by upstream Claude Code",
		})
	}
	diffs = append(diffs, compareToolProfiles(goInit.Tools, upstreamInit.Tools)...)
	return diffs
}

func compareSystemBlocks(goBlocks, upstreamBlocks []DumpSystemBlock) []CompareDifference {
	var diffs []CompareDifference
	if len(goBlocks) == 0 || len(upstreamBlocks) == 0 {
		return diffs
	}
	goKinds := systemBlockKinds(goBlocks)
	upstreamKinds := systemBlockKinds(upstreamBlocks)
	if strings.Join(goKinds, ",") != strings.Join(upstreamKinds, ",") {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    "system_blocks.kinds",
			Go:       strings.Join(goKinds, ","),
			Upstream: strings.Join(upstreamKinds, ","),
			Detail:   "system block kinds differ; inspect block-level prompt assembly before editing prompt text",
		})
	}
	for _, kind := range comparableSystemBlockKinds(goBlocks, upstreamBlocks) {
		goKindBlocks := systemBlocksByKind(goBlocks, kind)
		upstreamKindBlocks := systemBlocksByKind(upstreamBlocks, kind)
		limit := len(goKindBlocks)
		if len(upstreamKindBlocks) < limit {
			limit = len(upstreamKindBlocks)
		}
		for i := 0; i < limit; i++ {
			goBlock := goKindBlocks[i]
			upstreamBlock := upstreamKindBlocks[i]
			ordinal := ""
			if limit > 1 {
				ordinal = fmt.Sprintf(".%d", i)
			}
			diffs = append(diffs, compareSystemBlockPair(kind+ordinal, goBlock, upstreamBlock)...)
		}
	}
	return diffs
}

func compareSystemBlockPair(label string, goBlock, upstreamBlock DumpSystemBlock) []CompareDifference {
	var diffs []CompareDifference
	if goBlock.TextBytes > 0 && upstreamBlock.TextBytes > 0 {
		diff := math.Abs(float64(goBlock.TextBytes - upstreamBlock.TextBytes))
		larger := math.Max(float64(goBlock.TextBytes), float64(upstreamBlock.TextBytes))
		if diff > 300 && diff/larger > 0.20 {
			diffs = append(diffs, CompareDifference{
				Severity: "warn",
				Field:    fmt.Sprintf("system_blocks.kind.%s.text_bytes", label),
				Go:       fmt.Sprintf("%d", goBlock.TextBytes),
				Upstream: fmt.Sprintf("%d", upstreamBlock.TextBytes),
				Detail:   "same-kind system block size differs substantially",
			})
		}
	}
	if goBlock.HasCacheControl != upstreamBlock.HasCacheControl || goBlock.CacheType != upstreamBlock.CacheType || goBlock.CacheScope != upstreamBlock.CacheScope || goBlock.CacheTTL != upstreamBlock.CacheTTL {
		diffs = append(diffs, CompareDifference{
			Severity: "warn",
			Field:    fmt.Sprintf("system_blocks.kind.%s.cache_control", label),
			Go:       systemBlockCacheSummary(goBlock),
			Upstream: systemBlockCacheSummary(upstreamBlock),
			Detail:   "same-kind system block cache control differs",
		})
	}
	return diffs
}

func comparableSystemBlockKinds(goBlocks, upstreamBlocks []DumpSystemBlock) []string {
	seen := map[string]bool{}
	for _, block := range goBlocks {
		kind := firstNonEmpty(block.Kind, "unknown")
		if isComparableSystemBlockKind(kind) && len(systemBlocksByKind(upstreamBlocks, kind)) > 0 {
			seen[kind] = true
		}
	}
	for _, block := range upstreamBlocks {
		kind := firstNonEmpty(block.Kind, "unknown")
		if isComparableSystemBlockKind(kind) && len(systemBlocksByKind(goBlocks, kind)) > 0 {
			seen[kind] = true
		}
	}
	kinds := make([]string, 0, len(seen))
	for kind := range seen {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func isComparableSystemBlockKind(kind string) bool {
	switch kind {
	case "attribution", "core_prompt", "auto_memory", "skills_catalog", "environment":
		return true
	default:
		return false
	}
}

func systemBlocksByKind(blocks []DumpSystemBlock, kind string) []DumpSystemBlock {
	var out []DumpSystemBlock
	for _, block := range blocks {
		if firstNonEmpty(block.Kind, "unknown") == kind {
			out = append(out, block)
		}
	}
	return out
}

func systemBlockKinds(blocks []DumpSystemBlock) []string {
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, firstNonEmpty(block.Kind, "unknown"))
	}
	return out
}

func systemBlockCacheSummary(block DumpSystemBlock) string {
	if !block.HasCacheControl {
		return "none"
	}
	parts := []string{block.CacheType}
	if block.CacheScope != "" {
		parts = append(parts, "scope="+block.CacheScope)
	}
	if block.CacheTTL != "" {
		parts = append(parts, "ttl="+block.CacheTTL)
	}
	return strings.Join(parts, ",")
}

func compareToolProfiles(goTools, upstreamTools []DumpToolProfile) []CompareDifference {
	var diffs []CompareDifference
	goByName := toolProfileMap(goTools)
	upstreamByName := toolProfileMap(upstreamTools)
	for name, goTool := range goByName {
		upstreamTool, ok := upstreamByName[name]
		if !ok {
			continue
		}
		if goTool.DescriptionBytes > 0 && upstreamTool.DescriptionBytes > 0 {
			diff := math.Abs(float64(goTool.DescriptionBytes - upstreamTool.DescriptionBytes))
			larger := math.Max(float64(goTool.DescriptionBytes), float64(upstreamTool.DescriptionBytes))
			if diff > 120 && diff/larger > 0.15 {
				diffs = append(diffs, CompareDifference{
					Severity: "warn",
					Field:    "tools." + name + ".description_bytes",
					Go:       fmt.Sprintf("%d", goTool.DescriptionBytes),
					Upstream: fmt.Sprintf("%d", upstreamTool.DescriptionBytes),
					Detail:   "same-named tool description size differs; planning/tool-use behavior may differ",
				})
			}
		}
		if goTool.InputSchemaHash != "" && upstreamTool.InputSchemaHash != "" && goTool.InputSchemaHash != upstreamTool.InputSchemaHash {
			diffs = append(diffs, CompareDifference{
				Severity: "warn",
				Field:    "tools." + name + ".input_schema_hash",
				Go:       goTool.InputSchemaHash,
				Upstream: upstreamTool.InputSchemaHash,
				Detail:   "same-named tool input schema differs",
			})
		}
		onlyGo, onlyUpstream := stringSetDiff(goTool.InputSchemaFields, upstreamTool.InputSchemaFields)
		if len(onlyGo) > 0 || len(onlyUpstream) > 0 {
			diffs = append(diffs, CompareDifference{
				Severity: "warn",
				Field:    "tools." + name + ".input_schema_fields",
				Go:       strings.Join(onlyGo, ","),
				Upstream: strings.Join(onlyUpstream, ","),
				Detail:   "same-named tool exposes different input fields",
			})
		}
	}
	return diffs
}

func toolProfileMap(tools []DumpToolProfile) map[string]DumpToolProfile {
	out := make(map[string]DumpToolProfile, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			out[tool.Name] = tool
		}
	}
	return out
}

func toolNamesFromSummaries(tools []ToolSummary) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return names
}

func toolProfilesFromSummaries(tools []ToolSummary) []DumpToolProfile {
	out := make([]DumpToolProfile, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		out = append(out, DumpToolProfile{
			Name:              tool.Name,
			DescriptionBytes:  tool.DescriptionBytes,
			InputSchemaBytes:  tool.InputSchemaBytes,
			InputSchemaHash:   tool.InputSchemaHash,
			InputSchemaFields: append([]string(nil), tool.InputSchemaFields...),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func systemBlocksFromSummaries(blocks []SystemBlockSummary) []DumpSystemBlock {
	out := make([]DumpSystemBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, DumpSystemBlock{
			Index:           block.Index,
			TextBytes:       block.TextBytes,
			TextHash:        block.TextHash,
			Kind:            block.Kind,
			Source:          block.Source,
			HasCacheControl: block.HasCacheControl,
			CacheType:       block.CacheType,
			CacheTTL:        block.CacheTTL,
			CacheScope:      block.CacheScope,
		})
	}
	return out
}

func toolNamesFromProfiles(tools []DumpToolProfile) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return names
}

func stringSetDiff(a, b []string) ([]string, []string) {
	aSet := map[string]bool{}
	bSet := map[string]bool{}
	for _, value := range a {
		aSet[value] = true
	}
	for _, value := range b {
		bSet[value] = true
	}
	var onlyA, onlyB []string
	for value := range aSet {
		if !bSet[value] {
			onlyA = append(onlyA, value)
		}
	}
	for value := range bSet {
		if !aSet[value] {
			onlyB = append(onlyB, value)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}

func stringField(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func intField(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var intValue int
	if err := json.Unmarshal(raw, &intValue); err == nil {
		return intValue
	}
	var floatValue float64
	if err := json.Unmarshal(raw, &floatValue); err == nil {
		return int(floatValue)
	}
	return 0
}

func boolField(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return false
}
