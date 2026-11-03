package promptdump

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type VerifyOptions struct {
	MinTurns                               int      `json:"min_turns"`
	RequireFinalToolsDisabled              bool     `json:"require_final_tools_disabled"`
	RequireFinalTurnBudget                 bool     `json:"require_final_turn_budget"`
	RequireSubagentTurnBudgetToolsDisabled bool     `json:"require_subagent_turn_budget_tools_disabled"`
	RequireSubagentRecord                  bool     `json:"require_subagent_record"`
	RequireNoToolErrors                    bool     `json:"require_no_tool_errors"`
	RequireNoActualTruncation              bool     `json:"require_no_actual_truncation"`
	RequireNoRawOverLimit                  bool     `json:"require_no_raw_over_limit"`
	RequireToolNoRawOverLimit              []string `json:"require_tool_no_raw_over_limit,omitempty"`
	RequireToolNoPersistedOutput           []string `json:"require_tool_no_persisted_output,omitempty"`
	RequireToolPersistedOutput             []string `json:"require_tool_persisted_output,omitempty"`
	RequireToolMaxBytes                    []string `json:"require_tool_max_bytes,omitempty"`
	RequireToolResult                      []string `json:"require_tool_result,omitempty"`
	RequireToolUseInputText                []string `json:"require_tool_use_input_text,omitempty"`
	ForbidTool                             []string `json:"forbid_tool,omitempty"`
	RequireRequestText                     []string `json:"require_request_text,omitempty"`
	ForbidRequestText                      []string `json:"forbid_request_text,omitempty"`
}

type VerifyReport struct {
	Path     string              `json:"path"`
	Options  VerifyOptions       `json:"options"`
	OK       bool                `json:"ok"`
	Failures []string            `json:"failures,omitempty"`
	Records  []VerifyRecordBrief `json:"records"`
	Final    *VerifyRecordBrief  `json:"final,omitempty"`
}

type VerifyRecordBrief struct {
	Turn              int                              `json:"turn"`
	Scope             string                           `json:"scope,omitempty"`
	PromptMode        string                           `json:"prompt_mode,omitempty"`
	QuerySource       string                           `json:"query_source,omitempty"`
	AgentName         string                           `json:"agent_name,omitempty"`
	AgentMode         string                           `json:"agent_mode,omitempty"`
	MessageCount      int                              `json:"message_count"`
	ToolCount         int                              `json:"tool_count"`
	RuntimeStatus     RuntimeStatusSummary             `json:"runtime_status"`
	ToolResultStats   ToolResultStats                  `json:"tool_result_stats"`
	ToolResultsByTool map[string]VerifyToolResultBrief `json:"tool_results_by_tool,omitempty"`
	CompactSummaries  int                              `json:"compact_summaries"`
	AutoCompact       bool                             `json:"auto_compact"`
	PersistedOutput   int                              `json:"persisted_output"`
	ActualTruncated   int                              `json:"actual_truncated"`
	RawOverLimit      int                              `json:"raw_over_limit"`
	RawOverDefault    int                              `json:"raw_over_default_limit"`
	ToolResultErrors  int                              `json:"tool_result_errors"`
}

type VerifyToolResultBrief struct {
	Count               int `json:"count"`
	TotalBytes          int `json:"total_bytes"`
	MaxBytes            int `json:"max_bytes"`
	ErrorCount          int `json:"error_count"`
	ActualTruncated     int `json:"actual_truncated"`
	PersistedOutput     int `json:"persisted_output"`
	RawOverDefaultLimit int `json:"raw_over_default_limit"`
	RawOverLimit        int `json:"raw_over_limit"`
}

func DefaultVerifyOptions() VerifyOptions {
	return VerifyOptions{
		MinTurns:                               2,
		RequireFinalToolsDisabled:              true,
		RequireFinalTurnBudget:                 true,
		RequireSubagentTurnBudgetToolsDisabled: true,
		RequireNoToolErrors:                    true,
		RequireNoActualTruncation:              true,
		RequireNoRawOverLimit:                  true,
	}
}

func VerifyCodeModeRecoveryDump(path string, options VerifyOptions) (VerifyReport, error) {
	if options.MinTurns <= 0 {
		options.MinTurns = DefaultVerifyOptions().MinTurns
	}
	requireNoRawByTool := normalizeToolSet(options.RequireToolNoRawOverLimit)
	requireNoPersistedByTool := normalizeToolSet(options.RequireToolNoPersistedOutput)
	requirePersistedByTool := normalizeToolSet(options.RequireToolPersistedOutput)
	requireMaxBytesByTool, maxBytesFailures := normalizeToolMaxBytes(options.RequireToolMaxBytes)
	requireToolResults := normalizeToolSet(options.RequireToolResult)
	requireToolUseInputTexts, toolUseInputFailures := normalizeToolTextSpecs(options.RequireToolUseInputText, "require_tool_use_input_text")
	forbidTools := normalizeToolSet(options.ForbidTool)
	requireRequestTexts := normalizeTextSet(options.RequireRequestText)
	forbidRequestTexts := normalizeTextSet(options.ForbidRequestText)
	records, err := LoadRecords(path)
	if err != nil {
		return VerifyReport{}, err
	}
	report := VerifyReport{
		Path:    path,
		Options: options,
		Records: make([]VerifyRecordBrief, 0, len(records)),
	}
	report.Failures = append(report.Failures, maxBytesFailures...)
	report.Failures = append(report.Failures, toolUseInputFailures...)
	subagentRecords := 0
	seenToolResults := map[string]bool{}
	seenPersistedToolResults := map[string]bool{}
	seenToolUseInputTexts := map[string]bool{}
	seenRequestTexts := map[string]bool{}
	for _, record := range records {
		brief := verifyRecordBrief(record)
		report.Records = append(report.Records, brief)
		if brief.Scope == "subagent" {
			subagentRecords++
		}
		for toolName, stats := range brief.ToolResultsByTool {
			if stats.Count > 0 {
				seenToolResults[toolName] = true
			}
			if stats.PersistedOutput > 0 {
				seenPersistedToolResults[toolName] = true
			}
		}
		for _, text := range sortedTextNames(requireRequestTexts) {
			if recordContainsRequestText(record, text) {
				seenRequestTexts[text] = true
			}
		}
		for _, spec := range sortedToolTextSpecs(requireToolUseInputTexts) {
			if recordContainsToolUseInputText(record, spec.Tool, spec.Text) {
				seenToolUseInputTexts[spec.Key()] = true
			}
		}
		for _, toolName := range sortedToolNames(forbidTools) {
			if recordExposesTool(record, toolName) {
				report.Failures = append(report.Failures, fmt.Sprintf("%s exposes forbidden tool %s", recordLabel(record), toolName))
			}
		}
		for _, text := range sortedTextNames(forbidRequestTexts) {
			if recordContainsRequestText(record, text) {
				report.Failures = append(report.Failures, fmt.Sprintf("dump full request text unexpectedly contains %q", text))
			}
		}
		if options.RequireNoToolErrors && brief.ToolResultErrors != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("turn %d has %d tool_result errors", brief.Turn, brief.ToolResultErrors))
		}
		if options.RequireNoActualTruncation && brief.ActualTruncated != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("turn %d has %d actually truncated tool_results", brief.Turn, brief.ActualTruncated))
		}
		if options.RequireNoRawOverLimit && brief.RawOverLimit != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("turn %d has %d raw tool_results over configured limit", brief.Turn, brief.RawOverLimit))
		}
		if options.RequireSubagentTurnBudgetToolsDisabled && brief.Scope == "subagent" && containsRuntimeSection(brief.RuntimeStatus.Sections, "turn_budget") && brief.ToolCount != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("subagent %s turn %d has turn_budget but exposes %d tools, want 0", verifyAgentLabel(brief), brief.Turn, brief.ToolCount))
		}
		for _, toolName := range sortedToolNames(requireNoRawByTool) {
			stats, ok := brief.ToolResultsByTool[toolName]
			if ok && stats.RawOverLimit != 0 {
				report.Failures = append(report.Failures, fmt.Sprintf("turn %d tool %s has %d raw tool_results over configured limit", brief.Turn, toolName, stats.RawOverLimit))
			}
		}
		for _, toolName := range sortedToolNames(requireNoPersistedByTool) {
			stats, ok := brief.ToolResultsByTool[toolName]
			if ok && stats.PersistedOutput != 0 {
				report.Failures = append(report.Failures, fmt.Sprintf("turn %d tool %s has %d persisted tool_results, want 0", brief.Turn, toolName, stats.PersistedOutput))
			}
		}
		for _, toolName := range sortedIntToolNames(requireMaxBytesByTool) {
			stats, ok := brief.ToolResultsByTool[toolName]
			maxBytes := requireMaxBytesByTool[toolName]
			if ok && stats.MaxBytes > maxBytes {
				report.Failures = append(report.Failures, fmt.Sprintf("turn %d tool %s has max visible tool_result bytes %d over required max %d", brief.Turn, toolName, stats.MaxBytes, maxBytes))
			}
		}
	}
	if len(records) == 0 {
		report.Failures = append(report.Failures, "dump has no records")
	} else {
		final := report.Records[len(report.Records)-1]
		report.Final = &final
		if len(records) < options.MinTurns {
			report.Failures = append(report.Failures, fmt.Sprintf("dump has %d turns, want at least %d", len(records), options.MinTurns))
		}
		if options.RequireFinalToolsDisabled && final.ToolCount != 0 {
			report.Failures = append(report.Failures, fmt.Sprintf("final turn %d exposes %d tools, want 0", final.Turn, final.ToolCount))
		}
		if options.RequireFinalTurnBudget && !containsRuntimeSection(final.RuntimeStatus.Sections, "turn_budget") {
			report.Failures = append(report.Failures, fmt.Sprintf("final turn %d missing runtime_status section turn_budget", final.Turn))
		}
	}
	if options.RequireSubagentRecord && subagentRecords == 0 {
		report.Failures = append(report.Failures, "dump has no subagent records")
	}
	for _, toolName := range sortedToolNames(requireToolResults) {
		if !seenToolResults[toolName] {
			report.Failures = append(report.Failures, fmt.Sprintf("dump has no tool_result for required tool %s", toolName))
		}
	}
	for _, spec := range sortedToolTextSpecs(requireToolUseInputTexts) {
		if !seenToolUseInputTexts[spec.Key()] {
			report.Failures = append(report.Failures, fmt.Sprintf("dump has no tool_use input for required tool %s containing %q", spec.Tool, spec.Text))
		}
	}
	for _, toolName := range sortedToolNames(requirePersistedByTool) {
		if !seenPersistedToolResults[toolName] {
			report.Failures = append(report.Failures, fmt.Sprintf("dump has no persisted tool_result for required tool %s", toolName))
		}
	}
	for _, text := range sortedTextNames(requireRequestTexts) {
		if !seenRequestTexts[text] {
			report.Failures = append(report.Failures, fmt.Sprintf("dump full request text missing %q", text))
		}
	}
	report.OK = len(report.Failures) == 0
	return report, nil
}

func LoadRecords(path string) ([]Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open prompt dump %q: %w", path, err)
	}
	defer file.Close()
	var records []Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("parse prompt dump %q line %d: %w", path, line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read prompt dump %q: %w", path, err)
	}
	return records, nil
}

func verifyRecordBrief(record Record) VerifyRecordBrief {
	return VerifyRecordBrief{
		Turn:              record.Turn,
		Scope:             record.Scope,
		PromptMode:        record.PromptMode,
		QuerySource:       record.QuerySource,
		AgentName:         record.AgentName,
		AgentMode:         record.AgentMode,
		MessageCount:      record.MessageCount,
		ToolCount:         record.ToolCount,
		RuntimeStatus:     record.RuntimeStatus,
		ToolResultStats:   record.ToolResultStats,
		ToolResultsByTool: summarizeToolResultsByTool(record.MessagesSummary),
		CompactSummaries:  record.CompactState.CompactSummaries,
		AutoCompact:       record.CompactState.AutoCompactEnabled,
		PersistedOutput:   record.ToolResultStats.PersistedOutput,
		ActualTruncated:   record.ToolResultStats.ActualTruncated,
		RawOverLimit:      record.ToolResultStats.RawOverLimit,
		RawOverDefault:    record.ToolResultStats.RawOverDefaultLimit,
		ToolResultErrors:  record.ToolResultStats.ErrorCount,
	}
}

func summarizeToolResultsByTool(messages []MessageSummary) map[string]VerifyToolResultBrief {
	out := map[string]VerifyToolResultBrief{}
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Type != "tool_result" {
				continue
			}
			toolName := strings.TrimSpace(block.ToolName)
			if toolName == "" {
				toolName = "unknown"
			}
			stats := out[toolName]
			stats.Count++
			stats.TotalBytes += block.ContentBytes
			if block.ContentBytes > stats.MaxBytes {
				stats.MaxBytes = block.ContentBytes
			}
			if block.IsError {
				stats.ErrorCount++
			}
			if block.ActualTruncated {
				stats.ActualTruncated++
			}
			if block.PersistedOutput {
				stats.PersistedOutput++
			}
			if block.RawOverDefaultLimit {
				stats.RawOverDefaultLimit++
			}
			if block.RawOverLimit {
				stats.RawOverLimit++
			}
			out[toolName] = stats
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeToolSet(names []string) map[string]bool {
	out := map[string]bool{}
	for _, name := range names {
		for _, part := range strings.Split(name, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out[part] = true
			}
		}
	}
	return out
}

func normalizeTextSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out[part] = true
			}
		}
	}
	return out
}

type toolTextSpec struct {
	Tool string
	Text string
}

func (s toolTextSpec) Key() string {
	return s.Tool + "\x00" + s.Text
}

func normalizeToolTextSpecs(values []string, optionName string) (map[string]toolTextSpec, []string) {
	out := map[string]toolTextSpec{}
	var failures []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			toolName, text, ok := strings.Cut(part, "=")
			toolName = strings.TrimSpace(toolName)
			text = strings.TrimSpace(text)
			if !ok || toolName == "" || text == "" {
				failures = append(failures, fmt.Sprintf("invalid %s spec %q, want Tool=snippet", optionName, part))
				continue
			}
			spec := toolTextSpec{Tool: toolName, Text: text}
			out[spec.Key()] = spec
		}
	}
	return out, failures
}

func sortedToolNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedToolTextSpecs(set map[string]toolTextSpec) []toolTextSpec {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]toolTextSpec, 0, len(keys))
	for _, key := range keys {
		out = append(out, set[key])
	}
	return out
}

func sortedTextNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func normalizeToolMaxBytes(specs []string) (map[string]int, []string) {
	out := map[string]int{}
	var failures []string
	for _, spec := range specs {
		for _, part := range strings.Split(spec, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, ok := strings.Cut(part, "=")
			name = strings.TrimSpace(name)
			value = strings.TrimSpace(value)
			if !ok || name == "" || value == "" {
				failures = append(failures, fmt.Sprintf("invalid require_tool_max_bytes spec %q, want Tool=Bytes", part))
				continue
			}
			maxBytes, err := strconv.Atoi(value)
			if err != nil || maxBytes < 0 {
				failures = append(failures, fmt.Sprintf("invalid require_tool_max_bytes spec %q, bytes must be a non-negative integer", part))
				continue
			}
			out[name] = maxBytes
		}
	}
	return out, failures
}

func sortedIntToolNames(set map[string]int) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsRuntimeSection(sections []string, want string) bool {
	for _, section := range sections {
		if section == want {
			return true
		}
	}
	return false
}

func verifyAgentLabel(brief VerifyRecordBrief) string {
	name := strings.TrimSpace(brief.AgentName)
	if name == "" {
		return "unknown"
	}
	return name
}

func recordLabel(record Record) string {
	scope := strings.TrimSpace(record.Scope)
	if scope == "" || scope == "main" {
		return fmt.Sprintf("turn %d", record.Turn)
	}
	if name := strings.TrimSpace(record.AgentName); name != "" {
		return fmt.Sprintf("%s %s turn %d", scope, name, record.Turn)
	}
	return fmt.Sprintf("%s turn %d", scope, record.Turn)
}

func recordExposesTool(record Record, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || record.Request == nil {
		return false
	}
	for _, tool := range record.Request.Tools {
		if tool.Name == toolName {
			return true
		}
	}
	return false
}

func recordContainsRequestText(record Record, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" || record.Request == nil {
		return false
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record.Request); err != nil {
		return false
	}
	if strings.Contains(buf.String(), want) {
		return true
	}
	return strings.Contains(decodedRequestText(*record.Request), want)
}

func decodedRequestText(request anthropic.MessagesRequest) string {
	var b strings.Builder
	appendText := func(text string) {
		if text == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	appendText(request.System)
	for _, block := range request.SystemBlocks {
		appendText(block.Text)
	}
	for _, message := range request.Messages {
		appendText(message.Role)
		for _, block := range message.Content {
			appendText(block.Type)
			appendText(block.Text)
			appendText(block.Thinking)
			appendText(block.ConnectorText)
			appendText(block.Name)
			appendText(string(block.Input))
			appendText(block.Content)
		}
	}
	for _, tool := range request.Tools {
		appendText(tool.Name)
		appendText(tool.Description)
		appendText(string(tool.InputSchema))
	}
	return b.String()
}

func recordContainsToolUseInputText(record Record, toolName, want string) bool {
	toolName = strings.TrimSpace(toolName)
	want = strings.TrimSpace(want)
	if toolName == "" || want == "" || record.Request == nil {
		return false
	}
	for _, message := range record.Request.Messages {
		for _, block := range message.Content {
			if block.Type != "tool_use" || block.Name != toolName {
				continue
			}
			input := strings.TrimSpace(string(block.Input))
			if strings.Contains(input, want) {
				return true
			}
			if len(block.Input) == 0 {
				continue
			}
			var decoded any
			if err := json.Unmarshal(block.Input, &decoded); err != nil {
				continue
			}
			var buf bytes.Buffer
			encoder := json.NewEncoder(&buf)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(decoded); err == nil && strings.Contains(buf.String(), want) {
				return true
			}
		}
	}
	return false
}
