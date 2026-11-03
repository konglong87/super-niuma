package promptdump

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/toolresult"
)

const (
	PathEnv = "NIUMA_DUMP_PROMPT_JSON"
	FullEnv = "NIUMA_DUMP_PROMPT_FULL"
)

type Record struct {
	SchemaVersion    int                        `json:"schema_version"`
	Timestamp        string                     `json:"timestamp"`
	Scope            string                     `json:"scope,omitempty"`
	SessionID        string                     `json:"session_id,omitempty"`
	TenantSessionID  uint64                     `json:"tenant_session_id,omitempty"`
	ParentSessionID  uint64                     `json:"parent_session_id,omitempty"`
	TaskID           uint64                     `json:"task_id,omitempty"`
	Turn             int                        `json:"turn"`
	QuerySource      string                     `json:"query_source,omitempty"`
	PromptMode       string                     `json:"prompt_mode,omitempty"`
	PromptProfile    string                     `json:"prompt_profile,omitempty"`
	RuntimeProfile   string                     `json:"runtime_profile,omitempty"`
	AgentName        string                     `json:"agent_name,omitempty"`
	AgentMode        string                     `json:"agent_mode,omitempty"`
	Model            string                     `json:"model"`
	MaxTokens        int                        `json:"max_tokens"`
	SystemBytes      int                        `json:"system_bytes"`
	SystemHash       string                     `json:"system_hash,omitempty"`
	SystemBlockCount int                        `json:"system_block_count"`
	SystemBlocks     []SystemBlockSummary       `json:"system_blocks_summary,omitempty"`
	MessageCount     int                        `json:"message_count"`
	ToolCount        int                        `json:"tool_count"`
	MessagesSummary  []MessageSummary           `json:"messages_summary"`
	ToolsSummary     []ToolSummary              `json:"tools_summary,omitempty"`
	ContextManifest  any                        `json:"context_manifest,omitempty"`
	CompactState     CompactState               `json:"compact_state"`
	RuntimeStatus    RuntimeStatusSummary       `json:"runtime_status"`
	ToolResultStats  ToolResultStats            `json:"tool_result_stats"`
	RequestRedaction RequestRedaction           `json:"request_redaction"`
	Request          *anthropic.MessagesRequest `json:"request,omitempty"`
}

type MessageSummary struct {
	Role       string                `json:"role"`
	BlockCount int                   `json:"block_count"`
	Bytes      int                   `json:"bytes"`
	Blocks     []ContentBlockSummary `json:"blocks"`
}

type ContentBlockSummary struct {
	Type                string `json:"type"`
	TextBytes           int    `json:"text_bytes,omitempty"`
	ThinkingBytes       int    `json:"thinking_bytes,omitempty"`
	ConnectorTextBytes  int    `json:"connector_text_bytes,omitempty"`
	ContentBytes        int    `json:"content_bytes,omitempty"`
	InputBytes          int    `json:"input_bytes,omitempty"`
	InputHash           string `json:"input_hash,omitempty"`
	ContentHash         string `json:"content_hash,omitempty"`
	ToolName            string `json:"tool_name,omitempty"`
	ToolUseID           string `json:"tool_use_id,omitempty"`
	IsError             bool   `json:"is_error,omitempty"`
	ImageSourceType     string `json:"image_source_type,omitempty"`
	ImageMediaType      string `json:"image_media_type,omitempty"`
	HasCacheControl     bool   `json:"has_cache_control,omitempty"`
	SuspectedTruncated  bool   `json:"suspected_truncated,omitempty"`
	ActualTruncated     bool   `json:"actual_truncated,omitempty"`
	PersistedOutput     bool   `json:"persisted_output,omitempty"`
	RawOverDefaultLimit bool   `json:"raw_over_default_limit,omitempty"`
	RawOverLimit        bool   `json:"raw_over_limit,omitempty"`
}

type ToolSummary struct {
	Name              string   `json:"name"`
	DescriptionBytes  int      `json:"description_bytes"`
	InputSchemaBytes  int      `json:"input_schema_bytes"`
	InputSchemaHash   string   `json:"input_schema_hash,omitempty"`
	InputSchemaFields []string `json:"input_schema_fields,omitempty"`
}

type SystemBlockSummary struct {
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

type CompactState struct {
	AutoCompactEnabled bool `json:"auto_compact_enabled"`
	CompactSummaries   int  `json:"compact_summaries"`
}

type RuntimeStatusSummary struct {
	Present   bool     `json:"present"`
	TextBytes int      `json:"text_bytes,omitempty"`
	Sections  []string `json:"sections,omitempty"`
}

type ToolResultStats struct {
	Count               int `json:"count"`
	TotalBytes          int `json:"total_bytes"`
	ErrorCount          int `json:"error_count"`
	SuspectedTruncated  int `json:"suspected_truncated"`
	ActualTruncated     int `json:"actual_truncated"`
	PersistedOutput     int `json:"persisted_output"`
	MaxBytes            int `json:"max_bytes"`
	Limit               int `json:"limit,omitempty"`
	MessageBudget       int `json:"message_budget,omitempty"`
	HistoryBudget       int `json:"history_budget,omitempty"`
	RawOverDefaultLimit int `json:"raw_over_default_limit"`
	RawOverLimit        int `json:"raw_over_limit"`
}

type RequestRedaction struct {
	Mode               string `json:"mode"`
	TextOmitted        bool   `json:"text_omitted"`
	RawRequestIncluded bool   `json:"raw_request_included"`
}

type Metadata struct {
	Scope              string
	SessionID          string
	TenantSessionID    uint64
	ParentSessionID    uint64
	TaskID             uint64
	Turn               int
	QuerySource        string
	PromptMode         string
	PromptProfile      string
	RuntimeProfile     string
	AgentName          string
	AgentMode          string
	ContextManifest    any
	AutoCompactEnabled bool
	ToolResultLimit    int
	MessageBudget      int
	HistoryBudget      int
}

func Append(path string, record Record) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("build prompt dump: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open prompt dump %q: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write prompt dump %q: %w", path, err)
	}
	return nil
}

func Build(meta Metadata, req anthropic.MessagesRequest, full bool) Record {
	record := Record{
		SchemaVersion:    1,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Scope:            meta.Scope,
		SessionID:        meta.SessionID,
		TenantSessionID:  meta.TenantSessionID,
		ParentSessionID:  meta.ParentSessionID,
		TaskID:           meta.TaskID,
		Turn:             meta.Turn,
		QuerySource:      meta.QuerySource,
		PromptMode:       meta.PromptMode,
		PromptProfile:    meta.PromptProfile,
		RuntimeProfile:   meta.RuntimeProfile,
		AgentName:        meta.AgentName,
		AgentMode:        meta.AgentMode,
		Model:            req.Model,
		MaxTokens:        req.MaxTokens,
		SystemBytes:      len(req.System),
		SystemHash:       hashString(req.System),
		SystemBlockCount: len(req.SystemBlocks),
		SystemBlocks:     SummarizeSystemBlocks(req.System, req.SystemBlocks),
		MessageCount:     len(req.Messages),
		ToolCount:        len(req.Tools),
		MessagesSummary:  SummarizeMessagesWithLimit(req.Messages, effectiveLimit(meta.ToolResultLimit)),
		ToolsSummary:     SummarizeTools(req.Tools),
		ContextManifest:  meta.ContextManifest,
		CompactState: CompactState{
			AutoCompactEnabled: meta.AutoCompactEnabled,
			CompactSummaries:   countCompactSummaries(req.Messages),
		},
		RuntimeStatus: SummarizeRuntimeStatus(req.Messages),
		RequestRedaction: RequestRedaction{
			Mode:               "summary",
			TextOmitted:        true,
			RawRequestIncluded: false,
		},
	}
	record.ToolResultStats = SummarizeToolResults(record.MessagesSummary)
	record.ToolResultStats.Limit = effectiveLimit(meta.ToolResultLimit)
	record.ToolResultStats.MessageBudget = effectiveMessageBudget(meta.MessageBudget)
	record.ToolResultStats.HistoryBudget = effectiveHistoryBudget(meta.HistoryBudget)
	if full {
		request := req
		record.Request = &request
		record.RequestRedaction = RequestRedaction{
			Mode:               "full",
			TextOmitted:        false,
			RawRequestIncluded: true,
		}
	}
	return record
}

func SummarizeSystemBlocks(system string, blocks []anthropic.SystemBlock) []SystemBlockSummary {
	if len(blocks) == 0 {
		if strings.TrimSpace(system) == "" {
			return nil
		}
		return []SystemBlockSummary{summarizeSystemBlock(0, system, "", nil)}
	}
	out := make([]SystemBlockSummary, 0, len(blocks))
	for i, block := range blocks {
		out = append(out, summarizeSystemBlock(i, block.Text, block.Source, block.CacheControl))
	}
	return out
}

func summarizeSystemBlock(index int, text string, source string, cache *anthropic.CacheControl) SystemBlockSummary {
	summary := SystemBlockSummary{
		Index:     index,
		TextBytes: len(text),
		TextHash:  hashString(text),
		Kind:      classifySystemBlock(text),
		Source:    source,
	}
	if cache != nil {
		summary.HasCacheControl = true
		summary.CacheType = cache.Type
		summary.CacheTTL = cache.TTL
		summary.CacheScope = cache.Scope
	}
	return summary
}

func classifySystemBlock(text string) string {
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		return "empty"
	case strings.Contains(trimmed, "Claude agent, built on Anthropic"):
		return "attribution"
	case strings.Contains(trimmed, "# auto memory"):
		return "auto_memory"
	case strings.Contains(trimmed, "# Available Skills") || strings.Contains(trimmed, "Skill names:"):
		return "skills_catalog"
	case strings.Contains(trimmed, "Current date:") || strings.Contains(trimmed, "currentDate"):
		return "environment"
	case strings.Contains(trimmed, "Claude Code") || strings.Contains(trimmed, "software engineering"):
		return "core_prompt"
	default:
		return "other"
	}
}

func SummarizeMessages(messages []anthropic.MessageParam) []MessageSummary {
	return SummarizeMessagesWithLimit(messages, toolresult.DefaultLimit)
}

func SummarizeMessagesWithLimit(messages []anthropic.MessageParam, limit int) []MessageSummary {
	limit = effectiveLimit(limit)
	out := make([]MessageSummary, 0, len(messages))
	toolNamesByID := map[string]string{}
	for _, message := range messages {
		summary := MessageSummary{
			Role:       message.Role,
			BlockCount: len(message.Content),
			Blocks:     make([]ContentBlockSummary, 0, len(message.Content)),
		}
		for _, block := range message.Content {
			blockSummary := summarizeBlock(block, toolNamesByID, limit)
			summary.Bytes += blockSummary.TextBytes + blockSummary.ThinkingBytes + blockSummary.ConnectorTextBytes + blockSummary.ContentBytes + blockSummary.InputBytes
			summary.Blocks = append(summary.Blocks, blockSummary)
		}
		out = append(out, summary)
	}
	return out
}

func SummarizeRuntimeStatus(messages []anthropic.MessageParam) RuntimeStatusSummary {
	var summary RuntimeStatusSummary
	seen := map[string]bool{}
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type != "text" || !isRuntimeStatusText(block.Text) {
				continue
			}
			summary.Present = true
			summary.TextBytes += len(block.Text)
			for _, section := range runtimeStatusSections(block.Text) {
				if seen[section] {
					continue
				}
				seen[section] = true
				summary.Sections = append(summary.Sections, section)
			}
		}
	}
	return summary
}

func isRuntimeStatusText(text string) bool {
	return strings.Contains(text, "Current runtime status for this coding session") ||
		strings.Contains(text, "Current sub-agent runtime status")
}

func runtimeStatusSections(text string) []string {
	known := []struct {
		heading string
		name    string
	}{
		{heading: "## Turn budget reminder", name: "turn_budget"},
		{heading: "## Tool planning reminder", name: "tool_planning"},
		{heading: "## Sub-agent turn budget reminder", name: "turn_budget"},
		{heading: "## Sub-agent tool planning reminder", name: "tool_planning"},
		{heading: "## Permission context", name: "permission_context"},
		{heading: "## Active todos", name: "active_todos"},
		{heading: "## Plan mode", name: "plan_mode"},
		{heading: "## Background agent tasks", name: "background_agent_tasks"},
		{heading: "## Recent agent evidence decision context", name: "agent_evidence_decision_context"},
		{heading: "## Agent capability follow-up gate", name: "agent_capability_follow_up_gate"},
	}
	var sections []string
	for _, candidate := range known {
		if strings.Contains(text, candidate.heading) {
			sections = append(sections, candidate.name)
		}
	}
	return sections
}

func summarizeBlock(block anthropic.ContentBlock, toolNamesByID map[string]string, limit int) ContentBlockSummary {
	toolUseID := firstNonEmpty(block.ToolUseID, block.ID)
	toolName := block.Name
	if block.Type == "tool_use" && block.ID != "" && block.Name != "" {
		toolNamesByID[block.ID] = block.Name
	}
	if block.Type == "tool_result" && toolName == "" {
		toolName = toolNamesByID[toolUseID]
	}
	containsTruncationText := strings.Contains(block.Content, "[Tool output truncated:")
	actualTruncated := hasTrailingToolTruncationMarker(block.Content)
	persistedOutput := isPersistedOutputReference(block.Content)
	rawOverDefaultLimit := block.Type == "tool_result" && len(block.Content) > toolresult.DefaultLimit && !persistedOutput
	rawOverLimit := block.Type == "tool_result" && len(block.Content) > effectiveLimit(limit) && !persistedOutput
	summary := ContentBlockSummary{
		Type:                block.Type,
		TextBytes:           len(block.Text),
		ThinkingBytes:       len(block.Thinking),
		ConnectorTextBytes:  len(block.ConnectorText),
		ContentBytes:        len(block.Content),
		InputBytes:          len(block.Input),
		ToolName:            toolName,
		ToolUseID:           toolUseID,
		IsError:             block.IsError,
		HasCacheControl:     block.CacheControl != nil,
		SuspectedTruncated:  containsTruncationText,
		ActualTruncated:     actualTruncated,
		PersistedOutput:     persistedOutput,
		RawOverDefaultLimit: rawOverDefaultLimit,
		RawOverLimit:        rawOverLimit,
	}
	if len(block.Input) > 0 {
		summary.InputHash = hashBytes(block.Input)
	}
	if block.Content != "" {
		summary.ContentHash = hashString(block.Content)
	}
	if block.Source != nil {
		summary.ImageSourceType = block.Source.Type
		summary.ImageMediaType = block.Source.MediaType
	}
	return summary
}

func SummarizeTools(tools []anthropic.ToolDefinition) []ToolSummary {
	out := make([]ToolSummary, 0, len(tools))
	for _, tool := range tools {
		out = append(out, ToolSummary{
			Name:              tool.Name,
			DescriptionBytes:  len(tool.Description),
			InputSchemaBytes:  len(tool.InputSchema),
			InputSchemaHash:   hashRawJSON(tool.InputSchema),
			InputSchemaFields: inputSchemaFieldNames(tool.InputSchema),
		})
	}
	return out
}

func inputSchemaFieldNames(raw json.RawMessage) []string {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil || len(schema.Properties) == 0 {
		return nil
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func SummarizeToolResults(messages []MessageSummary) ToolResultStats {
	var stats ToolResultStats
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Type != "tool_result" {
				continue
			}
			stats.Count++
			stats.TotalBytes += block.ContentBytes
			if block.ContentBytes > stats.MaxBytes {
				stats.MaxBytes = block.ContentBytes
			}
			if block.IsError {
				stats.ErrorCount++
			}
			if block.SuspectedTruncated {
				stats.SuspectedTruncated++
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
		}
	}
	return stats
}

func effectiveLimit(limit int) int {
	if limit <= 0 {
		return toolresult.DefaultLimit
	}
	return limit
}

func effectiveMessageBudget(limit int) int {
	if limit <= 0 {
		return toolresult.DefaultMessageBudget
	}
	return limit
}

func effectiveHistoryBudget(limit int) int {
	if limit <= 0 {
		return toolresult.DefaultHistoryBudget
	}
	return limit
}

func hasTrailingToolTruncationMarker(content string) bool {
	trimmed := strings.TrimSpace(content)
	const prefix = "[Tool output truncated:"
	idx := strings.LastIndex(trimmed, prefix)
	if idx < 0 {
		return false
	}
	marker := trimmed[idx:]
	if !strings.HasPrefix(marker, prefix) || !strings.HasSuffix(marker, "bytes omitted]") {
		return false
	}
	omitted := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(marker, prefix), "bytes omitted]"))
	if omitted == "" {
		return false
	}
	_, err := strconv.Atoi(omitted)
	return err == nil
}

func isPersistedOutputReference(content string) bool {
	trimmed := strings.TrimSpace(content)
	return strings.HasPrefix(trimmed, "<persisted-output>") && strings.HasSuffix(trimmed, "</persisted-output>")
}

func IsEnvTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func countCompactSummaries(messages []anthropic.MessageParam) int {
	count := 0
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "text" && strings.HasPrefix(strings.TrimSpace(block.Text), "Conversation summary so far:") {
				count++
			}
		}
	}
	return count
}

func hashBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashString(value string) string {
	return hashBytes([]byte(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
