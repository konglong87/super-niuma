package toolresult

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/capabilityloop"
)

const (
	DefaultLimit         = 50_000
	DefaultMessageBudget = 200_000
	DefaultHistoryBudget = 200_000
	PreviewSizeBytes     = 2_000
	outputOpenTag        = "<persisted-output>"
	outputCloseTag       = "</persisted-output>"
	shellTruncatedMarker = "[output truncated after "
)

type SessionRef struct {
	SessionID      string
	TranscriptPath string
}

type ProcessOptions struct {
	ToolName  string
	ToolUseID string
	Limit     int
	Session   SessionRef
}

type BudgetOptions struct {
	Limit          int
	Session        SessionRef
	SkipToolNames  map[string]bool
	Replacements   map[string]string
	SeenToolUseIDs map[string]bool
	OnReplacement  func(ReplacementRecord)
}

type ReplacementRecord struct {
	Kind        string `json:"kind"`
	ToolUseID   string `json:"tool_use_id"`
	Replacement string `json:"replacement"`
}

type budgetCandidate struct {
	messageIndex int
	blockIndex   int
	toolUseID    string
	content      string
	size         int
}

func Process(content string, opts ProcessOptions) string {
	toolName := strings.TrimSpace(opts.ToolName)
	if toolName == "" {
		toolName = "tool"
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Sprintf("(%s completed with no output)", toolName)
	}
	limit := opts.Limit
	if limit <= 0 || len(content) <= limit {
		return content
	}
	persisted, err := persist(content, opts)
	if err == nil {
		return persisted
	}
	return Truncate(content, limit)
}

func ApplyMessageBudget(messages []anthropic.MessageParam, opts BudgetOptions) []anthropic.MessageParam {
	if !opts.Session.valid() && len(opts.Replacements) == 0 {
		return messages
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultMessageBudget
	}
	toolNamesByID := toolUseNames(messages)
	var out []anthropic.MessageParam
	for messageIndex, message := range messages {
		if message.Role != "user" {
			continue
		}
		total, candidates := budgetCandidates(message.Content, toolNamesByID, opts.SkipToolNames)
		if len(candidates) == 0 {
			continue
		}
		replacements := map[int]string{}
		current := total
		candidates = applyKnownReplacements(candidates, opts.Replacements, replacements, &current)
		candidates = filterSeenCandidates(candidates, opts.SeenToolUseIDs)
		if current <= limit || len(candidates) == 0 {
			markCandidatesSeen(opts.SeenToolUseIDs, candidates)
			if len(replacements) == 0 {
				continue
			}
			if out == nil {
				out = append([]anthropic.MessageParam(nil), messages...)
			}
			updated := out[messageIndex]
			updated.Content = append([]anthropic.ContentBlock(nil), updated.Content...)
			for blockIndex, replacement := range replacements {
				updated.Content[blockIndex].Content = replacement
			}
			out[messageIndex] = updated
			continue
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].size > candidates[j].size
		})
		for _, candidate := range candidates {
			if current <= limit {
				break
			}
			replacement, err := persist(candidate.content, ProcessOptions{
				ToolUseID: candidate.toolUseID,
				Session:   opts.Session,
			})
			if err != nil {
				continue
			}
			replacements[candidate.blockIndex] = replacement
			recordReplacement(opts, candidate.toolUseID, replacement)
			current = current - candidate.size + len(replacement)
		}
		markCandidatesSeen(opts.SeenToolUseIDs, candidates)
		if len(replacements) == 0 {
			continue
		}
		if out == nil {
			out = append([]anthropic.MessageParam(nil), messages...)
		}
		updated := out[messageIndex]
		updated.Content = append([]anthropic.ContentBlock(nil), updated.Content...)
		for blockIndex, replacement := range replacements {
			updated.Content[blockIndex].Content = replacement
		}
		out[messageIndex] = updated
	}
	if out == nil {
		return messages
	}
	return out
}

func ApplyHistoryBudget(messages []anthropic.MessageParam, opts BudgetOptions) []anthropic.MessageParam {
	if !opts.Session.valid() && len(opts.Replacements) == 0 {
		return messages
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultHistoryBudget
	}
	toolNamesByID := toolUseNames(messages)
	total, latestToolResultMessageIndex, candidates := historyBudgetCandidates(messages, toolNamesByID, opts.SkipToolNames)
	if len(candidates) == 0 {
		return messages
	}
	var out []anthropic.MessageParam
	copiedMessages := map[int]bool{}
	current := total
	candidates = applyKnownHistoryReplacements(messages, candidates, opts.Replacements, &out, copiedMessages, &current)
	candidates = filterSeenCandidates(candidates, opts.SeenToolUseIDs)
	if current <= limit || len(candidates) == 0 {
		markCandidatesSeen(opts.SeenToolUseIDs, candidates)
		if out == nil {
			return messages
		}
		return out
	}
	older := make([]budgetCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.messageIndex != latestToolResultMessageIndex {
			older = append(older, candidate)
		}
	}
	if len(older) == 0 {
		markCandidatesSeen(opts.SeenToolUseIDs, candidates)
		return messages
	}
	sort.SliceStable(older, func(i, j int) bool {
		if older[i].messageIndex != older[j].messageIndex {
			return older[i].messageIndex < older[j].messageIndex
		}
		return older[i].size > older[j].size
	})
	for _, candidate := range older {
		if current <= limit {
			break
		}
		replacement, err := persist(candidate.content, ProcessOptions{
			ToolUseID: candidate.toolUseID,
			Session:   opts.Session,
		})
		if err != nil {
			continue
		}
		if out == nil {
			out = append([]anthropic.MessageParam(nil), messages...)
		}
		if !copiedMessages[candidate.messageIndex] {
			updated := out[candidate.messageIndex]
			updated.Content = append([]anthropic.ContentBlock(nil), updated.Content...)
			out[candidate.messageIndex] = updated
			copiedMessages[candidate.messageIndex] = true
		}
		out[candidate.messageIndex].Content[candidate.blockIndex].Content = replacement
		recordReplacement(opts, candidate.toolUseID, replacement)
		current = current - candidate.size + len(replacement)
	}
	markCandidatesSeen(opts.SeenToolUseIDs, candidates)
	if out == nil {
		return messages
	}
	return out
}

func applyKnownReplacements(candidates []budgetCandidate, known map[string]string, replacements map[int]string, current *int) []budgetCandidate {
	if len(known) == 0 {
		return candidates
	}
	remaining := candidates[:0]
	for _, candidate := range candidates {
		replacement, ok := known[candidate.toolUseID]
		if !ok {
			remaining = append(remaining, candidate)
			continue
		}
		replacements[candidate.blockIndex] = replacement
		*current = *current - candidate.size + len(replacement)
	}
	return remaining
}

func applyKnownHistoryReplacements(messages []anthropic.MessageParam, candidates []budgetCandidate, known map[string]string, out *[]anthropic.MessageParam, copiedMessages map[int]bool, current *int) []budgetCandidate {
	if len(known) == 0 {
		return candidates
	}
	remaining := candidates[:0]
	for _, candidate := range candidates {
		replacement, ok := known[candidate.toolUseID]
		if !ok {
			remaining = append(remaining, candidate)
			continue
		}
		if *out == nil {
			*out = append([]anthropic.MessageParam(nil), messages...)
		}
		if !copiedMessages[candidate.messageIndex] {
			updated := (*out)[candidate.messageIndex]
			updated.Content = append([]anthropic.ContentBlock(nil), updated.Content...)
			(*out)[candidate.messageIndex] = updated
			copiedMessages[candidate.messageIndex] = true
		}
		(*out)[candidate.messageIndex].Content[candidate.blockIndex].Content = replacement
		*current = *current - candidate.size + len(replacement)
	}
	return remaining
}

func filterSeenCandidates(candidates []budgetCandidate, seen map[string]bool) []budgetCandidate {
	if len(seen) == 0 {
		return candidates
	}
	remaining := candidates[:0]
	for _, candidate := range candidates {
		if seen[candidate.toolUseID] {
			continue
		}
		remaining = append(remaining, candidate)
	}
	return remaining
}

func markCandidatesSeen(seen map[string]bool, candidates []budgetCandidate) {
	if seen == nil {
		return
	}
	for _, candidate := range candidates {
		if candidate.toolUseID != "" {
			seen[candidate.toolUseID] = true
		}
	}
}

func recordReplacement(opts BudgetOptions, toolUseID, replacement string) {
	if opts.OnReplacement == nil {
		return
	}
	toolUseID = strings.TrimSpace(toolUseID)
	if toolUseID == "" || replacement == "" {
		return
	}
	opts.OnReplacement(ReplacementRecord{
		Kind:        "tool-result",
		ToolUseID:   toolUseID,
		Replacement: replacement,
	})
}

func Truncate(content string, limit int) string {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	omitted := len(content) - limit
	return content[:limit] + fmt.Sprintf("\n\n[Tool output truncated: %d bytes omitted]", omitted)
}

func budgetCandidates(blocks []anthropic.ContentBlock, toolNamesByID map[string]string, skipToolNames map[string]bool) (int, []budgetCandidate) {
	total := 0
	candidates := []budgetCandidate{}
	for i, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		toolName := toolNamesByID[block.ToolUseID]
		if shouldSkipToolResultBudget(toolName, skipToolNames) {
			continue
		}
		size := len(block.Content)
		total += size
		if isPersistedOutput(block.Content) {
			continue
		}
		candidates = append(candidates, budgetCandidate{
			blockIndex: i,
			toolUseID:  block.ToolUseID,
			content:    block.Content,
			size:       size,
		})
	}
	return total, candidates
}

func historyBudgetCandidates(messages []anthropic.MessageParam, toolNamesByID map[string]string, skipToolNames map[string]bool) (int, int, []budgetCandidate) {
	total := 0
	latestToolResultMessageIndex := -1
	candidates := []budgetCandidate{}
	for messageIndex, message := range messages {
		if message.Role != "user" {
			continue
		}
		messageHasToolResult := false
		for blockIndex, block := range message.Content {
			if block.Type != "tool_result" {
				continue
			}
			messageHasToolResult = true
			toolName := toolNamesByID[block.ToolUseID]
			if shouldSkipToolResultBudget(toolName, skipToolNames) {
				continue
			}
			size := len(block.Content)
			total += size
			if isPersistedOutput(block.Content) {
				continue
			}
			candidates = append(candidates, budgetCandidate{
				messageIndex: messageIndex,
				blockIndex:   blockIndex,
				toolUseID:    block.ToolUseID,
				content:      block.Content,
				size:         size,
			})
		}
		if messageHasToolResult {
			latestToolResultMessageIndex = messageIndex
		}
	}
	return total, latestToolResultMessageIndex, candidates
}

func toolUseNames(messages []anthropic.MessageParam) map[string]string {
	out := map[string]string{}
	for _, message := range messages {
		if message.Role != "assistant" {
			continue
		}
		for _, block := range message.Content {
			if block.Type != "tool_use" {
				continue
			}
			toolUseID := strings.TrimSpace(block.ID)
			toolName := strings.TrimSpace(block.Name)
			if toolUseID != "" && toolName != "" {
				out[toolUseID] = toolName
			}
		}
	}
	return out
}

func shouldSkipToolResultBudget(toolName string, skipToolNames map[string]bool) bool {
	if len(skipToolNames) == 0 {
		return false
	}
	return skipToolNames[strings.TrimSpace(toolName)]
}

func isPersistedOutput(content string) bool {
	return strings.Contains(content, outputOpenTag) && strings.Contains(content, outputCloseTag)
}

func ToolResultIDs(messages []anthropic.MessageParam) map[string]bool {
	out := map[string]bool{}
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		for _, block := range message.Content {
			if block.Type != "tool_result" {
				continue
			}
			toolUseID := strings.TrimSpace(block.ToolUseID)
			if toolUseID != "" {
				out[toolUseID] = true
			}
		}
	}
	return out
}

func persist(content string, opts ProcessOptions) (string, error) {
	sessionID := strings.TrimSpace(opts.Session.SessionID)
	transcriptPath := strings.TrimSpace(opts.Session.TranscriptPath)
	if sessionID == "" || transcriptPath == "" {
		return "", fmt.Errorf("missing session transcript")
	}
	dir := filepath.Join(filepath.Dir(transcriptPath), safePathPart(sessionID), "tool-results")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	toolUseID := strings.TrimSpace(opts.ToolUseID)
	if toolUseID == "" {
		toolUseID = "tool-result"
	}
	path := filepath.Join(dir, safePathPart(toolUseID)+".txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", err
	}
	return persistedMessage(content, path), nil
}

func (s SessionRef) valid() bool {
	return strings.TrimSpace(s.SessionID) != "" && strings.TrimSpace(s.TranscriptPath) != ""
}

func persistedMessage(content, path string) string {
	previewLimit := PreviewSizeBytes
	if len(content) < previewLimit {
		previewLimit = len(content)
	}
	preview := content[:previewLimit]
	var b strings.Builder
	b.WriteString(outputOpenTag)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Output too large (%d bytes). Full output saved to: %s\n\n", len(content), path))
	if shellOutputTruncated(content) {
		b.WriteString("Important: the tool output was already truncated by the shell runner before persistence, so the saved file also omits the tail. If the omitted tail matters, rerun the command with narrower filters or redirect complete output to a file in the workspace/configured writable directory, then inspect that file with Read offset/limit.\n\n")
	}
	if summary := capabilityLoopSummary(content); summary != "" {
		b.WriteString("Capability loop summary preserved from full output:\n")
		b.WriteString(summary)
		b.WriteString("\n\n")
	}
	b.WriteString(fmt.Sprintf("Preview (first %d bytes):\n", previewLimit))
	b.WriteString(preview)
	if len(content) > previewLimit {
		b.WriteString("\n...\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(outputCloseTag)
	return b.String()
}

func capabilityLoopSummary(content string) string {
	for _, candidate := range capabilityloop.JSONCandidates(content) {
		if summary := capabilityLoopSummaryFromJSON(candidate); summary != "" {
			return summary
		}
	}
	if summary := capabilityLoopSummaryFromLine(content); summary != "" {
		return summary
	}
	return ""
}

func capabilityLoopSummaryFromJSON(content string) string {
	var decoded any
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		return ""
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return ""
	}
	loop := firstCapabilityLoopObject(
		root[capabilityloop.Key],
		objectField(root, "result", capabilityloop.Key),
		objectField(objectValue(root["task"]), "result", capabilityloop.Key),
	)
	if len(loop) == 0 {
		return ""
	}
	status := firstString(root["status"], objectField(root, "result", "status"), objectField(root, "task", "status"))
	return formatCapabilityLoopSummary(status, loop)
}

func capabilityLoopSummaryFromLine(content string) string {
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.Contains(line, capabilityloop.LineMarker) {
			continue
		}
		return "- " + trimCapabilityLoopSummaryText(line, 700)
	}
	return ""
}

func firstCapabilityLoopObject(values ...any) map[string]any {
	for _, value := range values {
		if object := objectValue(value); len(object) > 0 {
			return object
		}
	}
	return nil
}

func objectField(root map[string]any, path ...string) any {
	var current any = root
	for _, key := range path {
		object := objectValue(current)
		if len(object) == 0 {
			return nil
		}
		current = object[key]
	}
	return current
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return nil
}

func firstString(values ...any) string {
	for _, value := range values {
		if text, ok := value.(string); ok {
			if text = strings.TrimSpace(text); text != "" {
				return text
			}
		}
	}
	return ""
}

func formatCapabilityLoopSummary(status string, loop map[string]any) string {
	lines := []string{}
	if status = strings.TrimSpace(status); status != "" {
		lines = append(lines, "- status: "+trimCapabilityLoopSummaryText(status, 80))
	}
	for _, field := range []string{"evidence", "assumptions", "unknowns", "verification", "risks"} {
		if text := firstCapabilityLoopText(loop[field]); text != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", field, trimCapabilityLoopSummaryText(text, 220)))
		}
	}
	if text := firstCapabilityLoopText(loop["next_action"]); text != "" {
		lines = append(lines, "- next_action: "+trimCapabilityLoopSummaryText(text, 220))
	}
	// The follow-up identity and the supersede relation (was TODO-094). Omitting
	// them made externalization lossy in a way that flipped a decision: the parent
	// re-reads this block instead of the discarded payload, so a follow-up the
	// sub-agent had resolved came back as pending.
	for _, field := range []string{"resolved_follow_up", "follow_up_id"} {
		if text := firstCapabilityLoopText(loop[field]); text != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", field, trimCapabilityLoopSummaryText(text, 220)))
		}
	}
	// Every id, not just the first: superseding is a set relation, and dropping the
	// tail silently un-resolves those follow-ups.
	for _, field := range []string{"supersedes_evidence_id", "supersedes_evidence_ids"} {
		if ids := capabilityLoopTexts(loop[field]); len(ids) > 0 {
			lines = append(lines, fmt.Sprintf("- %s: %s", field, trimCapabilityLoopSummaryText(strings.Join(ids, ","), 220)))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func capabilityLoopTexts(value any) []string {
	switch typed := value.(type) {
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			return []string{text}
		}
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, capabilityLoopTexts(item)...)
		}
		return out
	}
	return nil
}

func firstCapabilityLoopText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if text := firstCapabilityLoopText(item); text != "" {
				return text
			}
		}
	case map[string]any:
		return firstNonEmptyCapabilityLoopText(
			firstCapabilityLoopText(typed["summary"]),
			firstCapabilityLoopText(typed["description"]),
			firstCapabilityLoopText(typed["text"]),
			firstCapabilityLoopText(typed["value"]),
		)
	}
	return ""
}

func firstNonEmptyCapabilityLoopText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func trimCapabilityLoopSummaryText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit > 0 && len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

func shellOutputTruncated(content string) bool {
	return strings.Contains(content, shellTruncatedMarker)
}

func safePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "unknown"
	}
	return out
}
