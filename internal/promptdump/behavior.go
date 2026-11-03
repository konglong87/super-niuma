package promptdump

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type BehaviorProfile struct {
	Path              string            `json:"path"`
	Format            string            `json:"format"`
	Model             string            `json:"model,omitempty"`
	MaxTokens         int               `json:"max_tokens,omitempty"`
	RequestCount      int               `json:"request_count"`
	MainRequestCount  int               `json:"main_request_count"`
	ScopeCounts       map[string]int    `json:"scope_counts,omitempty"`
	InitialToolNames  []string          `json:"initial_tool_names,omitempty"`
	Turns             []BehaviorTurn    `json:"turns,omitempty"`
	Final             *BehaviorTurn     `json:"final,omitempty"`
	FinalMain         *BehaviorTurn     `json:"final_main,omitempty"`
	Totals            BehaviorTotals    `json:"totals"`
	CapabilitySignals CapabilitySignals `json:"capability_signals,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
	Limitations       []string          `json:"limitations,omitempty"`
}

type BehaviorTurn struct {
	Index               int                 `json:"index"`
	Turn                int                 `json:"turn,omitempty"`
	Scope               string              `json:"scope,omitempty"`
	PromptMode          string              `json:"prompt_mode,omitempty"`
	QuerySource         string              `json:"query_source,omitempty"`
	AgentName           string              `json:"agent_name,omitempty"`
	AgentMode           string              `json:"agent_mode,omitempty"`
	MessageCount        int                 `json:"message_count"`
	ToolCount           int                 `json:"tool_count"`
	RuntimeSections     []string            `json:"runtime_sections,omitempty"`
	ToolUses            []BehaviorToolEvent `json:"tool_uses,omitempty"`
	ToolResults         []BehaviorToolEvent `json:"tool_results,omitempty"`
	ToolUseSequence     []string            `json:"tool_use_sequence,omitempty"`
	ToolResultSequence  []string            `json:"tool_result_sequence,omitempty"`
	ToolResultErrors    int                 `json:"tool_result_errors,omitempty"`
	ToolResultBytes     int                 `json:"tool_result_bytes,omitempty"`
	AssistantTextBlocks int                 `json:"assistant_text_blocks,omitempty"`
	AssistantTextBytes  int                 `json:"assistant_text_bytes,omitempty"`
	UserTextBlocks      int                 `json:"user_text_blocks,omitempty"`
	UserTextBytes       int                 `json:"user_text_bytes,omitempty"`
	CapabilitySignals   CapabilitySignals   `json:"capability_signals,omitempty"`
}

type BehaviorToolEvent struct {
	Name    string `json:"name,omitempty"`
	ID      string `json:"id,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
}

type BehaviorTotals struct {
	ToolUses            int `json:"tool_uses"`
	ToolResults         int `json:"tool_results"`
	ToolResultErrors    int `json:"tool_result_errors"`
	ToolResultBytes     int `json:"tool_result_bytes"`
	AssistantTextBlocks int `json:"assistant_text_blocks"`
	UserTextBlocks      int `json:"user_text_blocks"`
}

type CapabilitySignals struct {
	Score                int      `json:"score,omitempty"`
	AnchoredScore        int      `json:"anchored_score"`
	CapabilityLoop       bool     `json:"capability_loop,omitempty"`
	Evidence             bool     `json:"evidence,omitempty"`
	EvidenceAnchored     bool     `json:"evidence_anchored,omitempty"`
	Assumptions          bool     `json:"assumptions,omitempty"`
	Unknowns             bool     `json:"unknowns,omitempty"`
	Verification         bool     `json:"verification,omitempty"`
	VerificationAnchored bool     `json:"verification_anchored,omitempty"`
	Risks                bool     `json:"risks,omitempty"`
	NextAction           bool     `json:"next_action,omitempty"`
	Markers              []string `json:"markers,omitempty"`
	AnchoredMarkers      []string `json:"anchored_markers,omitempty"`
	Anchors              []string `json:"anchors,omitempty"`
}

type BehaviorCompareReport struct {
	OK          bool                 `json:"ok"`
	Go          BehaviorProfile      `json:"go"`
	Upstream    BehaviorProfile      `json:"upstream"`
	Differences []BehaviorDifference `json:"differences,omitempty"`
	Limitations []string             `json:"limitations,omitempty"`
}

type BehaviorDifference struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Go       string `json:"go,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Detail   string `json:"detail"`
}

var (
	capabilityFileAnchorRE = regexp.MustCompile(`(?i)(?:^|[\s"'([{<])(?:[./~\w-]+/)?[\w.-]+\.(?:go|ts|tsx|js|jsx|py|md|jsonl?|ya?ml|toml|sh|sql|rs|java|kt|swift|mjs|cjs)(?::\d+)?`)
	capabilityCmdAnchorRE  = regexp.MustCompile(`(?i)\b(go test|go run|git diff --check|bash\s+scripts/|scripts/[^\s"']+\.sh|node\s+|npm\s+test|pnpm\s+test|yarn\s+test|pytest|curl\s+|make\s+)`)
)

func LoadBehaviorProfile(path string) (BehaviorProfile, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return BehaviorProfile{}, err
	}
	if len(lines) == 0 {
		return BehaviorProfile{}, fmt.Errorf("prompt dump %q has no records", path)
	}
	format, err := detectDumpFormat(lines[0])
	if err != nil {
		return BehaviorProfile{}, fmt.Errorf("detect prompt dump %q: %w", path, err)
	}
	switch format {
	case DumpFormatGo:
		records, err := recordsFromLines(path, lines)
		if err != nil {
			return BehaviorProfile{}, err
		}
		return behaviorProfileGo(path, records), nil
	case DumpFormatUpstreamRequest:
		requests, err := upstreamRequestCapturesFromLines(path, lines)
		if err != nil {
			return BehaviorProfile{}, err
		}
		return behaviorProfileUpstreamRequests(path, requests), nil
	case DumpFormatUpstream:
		entries, err := upstreamEntriesFromLines(path, lines)
		if err != nil {
			return BehaviorProfile{}, err
		}
		return behaviorProfileUpstreamDelta(path, entries), nil
	default:
		return BehaviorProfile{}, fmt.Errorf("unsupported prompt dump format %q", format)
	}
}

func CompareBehaviorDumps(goPath, upstreamPath string) (BehaviorCompareReport, error) {
	goProfile, err := LoadBehaviorProfile(goPath)
	if err != nil {
		return BehaviorCompareReport{}, err
	}
	upstreamProfile, err := LoadBehaviorProfile(upstreamPath)
	if err != nil {
		return BehaviorCompareReport{}, err
	}
	if goProfile.Format != DumpFormatGo {
		return BehaviorCompareReport{}, fmt.Errorf("go dump %q has format %q, want %q", goPath, goProfile.Format, DumpFormatGo)
	}
	report := BehaviorCompareReport{
		Go:          goProfile,
		Upstream:    upstreamProfile,
		Limitations: append(append([]string{}, goProfile.Limitations...), upstreamProfile.Limitations...),
	}
	report.Differences = compareBehaviorProfiles(goProfile, upstreamProfile)
	report.OK = true
	for _, diff := range report.Differences {
		if diff.Severity == "error" {
			report.OK = false
			break
		}
	}
	return report, nil
}

func behaviorProfileGo(path string, records []Record) BehaviorProfile {
	profile := BehaviorProfile{
		Path:         path,
		Format:       DumpFormatGo,
		RequestCount: len(records),
	}
	if len(records) == 0 {
		return profile
	}
	first := records[0]
	profile.Model = first.Model
	profile.MaxTokens = first.MaxTokens
	profile.InitialToolNames = toolNamesFromSummaries(first.ToolsSummary)
	for i, record := range records {
		turn := BehaviorTurn{
			Index:           i + 1,
			Turn:            record.Turn,
			Scope:           record.Scope,
			PromptMode:      record.PromptMode,
			QuerySource:     record.QuerySource,
			AgentName:       record.AgentName,
			AgentMode:       record.AgentMode,
			MessageCount:    record.MessageCount,
			ToolCount:       record.ToolCount,
			RuntimeSections: append([]string{}, record.RuntimeStatus.Sections...),
		}
		if record.Request != nil {
			fillBehaviorTurnFromAnthropicMessages(&turn, record.Request.Messages)
			if turn.MessageCount == 0 {
				turn.MessageCount = len(record.Request.Messages)
			}
			turn.ToolCount = len(record.Request.Tools)
		} else {
			fillBehaviorTurnFromSummaries(&turn, record.MessagesSummary)
			turn.ToolResultErrors = record.ToolResultStats.ErrorCount
			turn.ToolResultBytes = record.ToolResultStats.TotalBytes
		}
		finalizeBehaviorTurn(&turn)
		profile.addTurn(turn)
	}
	return profile
}

func behaviorProfileUpstreamRequests(path string, requests []upstreamRequestCapture) BehaviorProfile {
	profile := BehaviorProfile{
		Path:         path,
		Format:       DumpFormatUpstreamRequest,
		RequestCount: len(requests),
		Limitations: []string{
			"upstream request capture is collected by a local fetch wrapper; it proves request shape and history, not final UI rendering or user-visible transcript quality",
		},
	}
	for i, request := range requests {
		body := requestBodyMap(request.Body)
		if i == 0 {
			profile.Model = stringField(body["model"])
			profile.MaxTokens = intField(body["max_tokens"])
			profile.InitialToolNames = summarizeToolNamesRaw(body["tools"])
		}
		turn := BehaviorTurn{
			Index:     i + 1,
			Turn:      i + 1,
			ToolCount: len(summarizeToolNamesRaw(body["tools"])),
		}
		fillBehaviorTurnFromRawMessages(&turn, body["messages"])
		finalizeBehaviorTurn(&turn)
		profile.addTurn(turn)
	}
	return profile
}

func behaviorProfileUpstreamDelta(path string, entries []upstreamEntry) BehaviorProfile {
	profile := BehaviorProfile{
		Path:         path,
		Format:       DumpFormatUpstream,
		RequestCount: len(entries),
		Limitations: []string{
			"upstream dumpPrompts delta format does not contain complete per-turn request history; behavior profile is partial",
		},
	}
	for _, entry := range entries {
		switch entry.Type {
		case "init":
			initProfile, warnings := summarizeUpstreamInit(entry.Data)
			profile.Model = initProfile.Model
			profile.MaxTokens = initProfile.MaxTokens
			profile.InitialToolNames = initProfile.ToolNames
			profile.Warnings = append(profile.Warnings, warnings...)
		case "message":
			turn := BehaviorTurn{Index: len(profile.Turns) + 1, Turn: len(profile.Turns) + 1}
			fillBehaviorTurnFromRawSingleMessage(&turn, entry.Data)
			finalizeBehaviorTurn(&turn)
			profile.addTurn(turn)
		case "response":
			turn := BehaviorTurn{Index: len(profile.Turns) + 1, Turn: len(profile.Turns) + 1}
			fillBehaviorTurnFromUpstreamResponse(&turn, entry.Data)
			finalizeBehaviorTurn(&turn)
			profile.addTurn(turn)
		}
	}
	return profile
}

func (p *BehaviorProfile) addTurn(turn BehaviorTurn) {
	scope := behaviorScope(turn.Scope)
	if p.ScopeCounts == nil {
		p.ScopeCounts = map[string]int{}
	}
	p.ScopeCounts[scope]++
	if scope == "main" {
		p.MainRequestCount++
	}
	p.Turns = append(p.Turns, turn)
	p.Totals.ToolUses += len(turn.ToolUses)
	p.Totals.ToolResults += len(turn.ToolResults)
	p.Totals.ToolResultErrors += turn.ToolResultErrors
	p.Totals.ToolResultBytes += turn.ToolResultBytes
	p.Totals.AssistantTextBlocks += turn.AssistantTextBlocks
	p.Totals.UserTextBlocks += turn.UserTextBlocks
	p.CapabilitySignals = mergeCapabilitySignals(p.CapabilitySignals, turn.CapabilitySignals)
	final := turn
	p.Final = &final
	if scope == "main" {
		p.FinalMain = &final
	}
}

func fillBehaviorTurnFromAnthropicMessages(turn *BehaviorTurn, messages []anthropic.MessageParam) {
	toolNamesByID := map[string]string{}
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				addBehaviorText(turn, message.Role, block.Text)
			case "tool_use":
				id := firstNonEmpty(block.ID, block.ToolUseID)
				name := strings.TrimSpace(block.Name)
				if id != "" && name != "" {
					toolNamesByID[id] = name
				}
				turn.ToolUses = append(turn.ToolUses, BehaviorToolEvent{Name: name, ID: id, Bytes: len(block.Input)})
				addCapabilitySignalText(turn, string(block.Input))
			case "tool_result":
				id := firstNonEmpty(block.ToolUseID, block.ID)
				name := strings.TrimSpace(block.Name)
				if name == "" {
					name = toolNamesByID[id]
				}
				turn.ToolResults = append(turn.ToolResults, BehaviorToolEvent{Name: name, ID: id, IsError: block.IsError, Bytes: len(block.Content)})
				addCapabilitySignalText(turn, block.Content)
				if block.IsError {
					turn.ToolResultErrors++
				}
				turn.ToolResultBytes += len(block.Content)
			}
		}
	}
}

func fillBehaviorTurnFromSummaries(turn *BehaviorTurn, messages []MessageSummary) {
	for _, message := range messages {
		for _, block := range message.Blocks {
			switch block.Type {
			case "text":
				addBehaviorTextBytes(turn, message.Role, block.TextBytes)
			case "tool_use":
				turn.ToolUses = append(turn.ToolUses, BehaviorToolEvent{Name: block.ToolName, ID: block.ToolUseID, Bytes: block.InputBytes})
			case "tool_result":
				turn.ToolResults = append(turn.ToolResults, BehaviorToolEvent{Name: block.ToolName, ID: block.ToolUseID, IsError: block.IsError, Bytes: block.ContentBytes})
			}
		}
	}
}

func fillBehaviorTurnFromRawMessages(turn *BehaviorTurn, raw json.RawMessage) {
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return
	}
	turn.MessageCount = len(messages)
	toolNamesByID := map[string]string{}
	for _, message := range messages {
		role := stringField(message["role"])
		fillBehaviorTurnFromRawContent(turn, role, message["content"], toolNamesByID)
	}
}

func fillBehaviorTurnFromRawSingleMessage(turn *BehaviorTurn, raw json.RawMessage) {
	var message map[string]json.RawMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return
	}
	turn.MessageCount = 1
	fillBehaviorTurnFromRawContent(turn, stringField(message["role"]), message["content"], map[string]string{})
}

func fillBehaviorTurnFromRawContent(turn *BehaviorTurn, role string, raw json.RawMessage, toolNamesByID map[string]string) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		addBehaviorText(turn, role, text)
		return
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return
	}
	for _, block := range blocks {
		switch stringField(block["type"]) {
		case "text":
			addBehaviorText(turn, role, stringField(block["text"]))
		case "tool_use":
			id := firstNonEmpty(stringField(block["id"]), stringField(block["tool_use_id"]))
			name := strings.TrimSpace(stringField(block["name"]))
			if id != "" && name != "" {
				toolNamesByID[id] = name
			}
			turn.ToolUses = append(turn.ToolUses, BehaviorToolEvent{Name: name, ID: id, Bytes: rawJSONLen(block["input"])})
			addCapabilitySignalText(turn, string(block["input"]))
		case "tool_result":
			id := firstNonEmpty(stringField(block["tool_use_id"]), stringField(block["id"]))
			name := strings.TrimSpace(stringField(block["name"]))
			if name == "" {
				name = toolNamesByID[id]
			}
			contentBytes := rawContentBytes(block["content"])
			isError := boolField(block["is_error"])
			turn.ToolResults = append(turn.ToolResults, BehaviorToolEvent{Name: name, ID: id, IsError: isError, Bytes: contentBytes})
			addCapabilitySignalText(turn, string(block["content"]))
			if isError {
				turn.ToolResultErrors++
			}
			turn.ToolResultBytes += contentBytes
		}
	}
}

func fillBehaviorTurnFromUpstreamResponse(turn *BehaviorTurn, raw json.RawMessage) {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	if streamRaw, ok := data["stream"]; ok {
		var streaming bool
		_ = json.Unmarshal(streamRaw, &streaming)
		if streaming {
			fillBehaviorTurnFromUpstreamChunks(turn, data["chunks"])
			return
		}
	}
	fillBehaviorTurnFromRawContent(turn, "assistant", data["content"], map[string]string{})
}

func fillBehaviorTurnFromUpstreamChunks(turn *BehaviorTurn, raw json.RawMessage) {
	var chunks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &chunks); err != nil {
		return
	}
	for _, chunk := range chunks {
		switch stringField(chunk["type"]) {
		case "content_block_start":
			var block map[string]json.RawMessage
			if err := json.Unmarshal(chunk["content_block"], &block); err != nil {
				continue
			}
			switch stringField(block["type"]) {
			case "text":
				addBehaviorText(turn, "assistant", stringField(block["text"]))
			case "tool_use":
				turn.ToolUses = append(turn.ToolUses, BehaviorToolEvent{
					Name:  stringField(block["name"]),
					ID:    stringField(block["id"]),
					Bytes: rawJSONLen(block["input"]),
				})
				addCapabilitySignalText(turn, string(block["input"]))
			}
		case "content_block_delta":
			var delta map[string]json.RawMessage
			if err := json.Unmarshal(chunk["delta"], &delta); err != nil {
				continue
			}
			if text := stringField(delta["text"]); text != "" {
				addBehaviorText(turn, "assistant", text)
			}
		}
	}
}

func addBehaviorText(turn *BehaviorTurn, role, text string) {
	addBehaviorTextBytes(turn, role, len(text))
	addCapabilitySignalText(turn, text)
}

func addBehaviorTextBytes(turn *BehaviorTurn, role string, bytes int) {
	switch role {
	case "assistant":
		turn.AssistantTextBlocks++
		turn.AssistantTextBytes += bytes
	case "user":
		turn.UserTextBlocks++
		turn.UserTextBytes += bytes
	}
}

func finalizeBehaviorTurn(turn *BehaviorTurn) {
	turn.RuntimeSections = uniqueSortedStrings(turn.RuntimeSections)
	turn.ToolUseSequence = toolEventNames(turn.ToolUses)
	turn.ToolResultSequence = toolEventNames(turn.ToolResults)
}

func addCapabilitySignalText(turn *BehaviorTurn, text string) {
	turn.CapabilitySignals = scanCapabilitySignals(turn.CapabilitySignals, text)
}

func scanCapabilitySignals(signals CapabilitySignals, text string) CapabilitySignals {
	normalized := strings.ToLower(text)
	anchors := extractCapabilityAnchors(text)
	markers := map[string]bool{}
	for _, marker := range signals.Markers {
		markers[marker] = true
	}
	addMarker := func(marker string) {
		if marker == "" || markers[marker] {
			return
		}
		markers[marker] = true
		signals.Markers = append(signals.Markers, marker)
	}
	if strings.Contains(normalized, "capability_loop") {
		signals.CapabilityLoop = true
		addMarker("capability_loop")
	}
	if containsAny(normalized, "evidence:", "\"evidence\"", " evidence ", "证据") {
		signals.Evidence = true
		if capabilityEvidenceAnchored(text) {
			signals.EvidenceAnchored = true
		}
		addMarker("evidence")
	}
	if containsAny(normalized, "assumptions:", "\"assumptions\"", " assumption ", " assumptions ", "假设") {
		signals.Assumptions = true
		addMarker("assumptions")
	}
	if containsAny(normalized, "unknowns:", "\"unknowns\"", " unknown ", " unknowns ", "未知", "无法证明") {
		signals.Unknowns = true
		addMarker("unknowns")
	}
	if containsAny(normalized, "verification:", "\"verification\"", " verification ", " verified ", "验证", "测试") {
		signals.Verification = true
		if capabilityVerificationAnchored(text) {
			signals.VerificationAnchored = true
		}
		addMarker("verification")
	}
	if containsAny(normalized, "risks:", "\"risks\"", " risk ", " risks ", "风险") {
		signals.Risks = true
		addMarker("risks")
	}
	if containsAny(normalized, "next_action", "next action:", "\"next_action\"", "下一步") {
		signals.NextAction = true
		addMarker("next_action")
	}
	signals.Anchors = limitedStrings(uniqueSortedStrings(append(signals.Anchors, anchors...)), 24)
	signals.Score = capabilitySignalScore(signals)
	signals.AnchoredMarkers = capabilitySignalAnchoredMarkers(signals)
	signals.AnchoredScore = len(signals.AnchoredMarkers)
	signals.Markers = uniqueSortedStrings(signals.Markers)
	return signals
}

func mergeCapabilitySignals(a, b CapabilitySignals) CapabilitySignals {
	out := CapabilitySignals{
		CapabilityLoop:       a.CapabilityLoop || b.CapabilityLoop,
		Evidence:             a.Evidence || b.Evidence,
		EvidenceAnchored:     a.EvidenceAnchored || b.EvidenceAnchored,
		Assumptions:          a.Assumptions || b.Assumptions,
		Unknowns:             a.Unknowns || b.Unknowns,
		Verification:         a.Verification || b.Verification,
		VerificationAnchored: a.VerificationAnchored || b.VerificationAnchored,
		Risks:                a.Risks || b.Risks,
		NextAction:           a.NextAction || b.NextAction,
		Markers:              append(append([]string{}, a.Markers...), b.Markers...),
		Anchors:              append(append([]string{}, a.Anchors...), b.Anchors...),
	}
	out.Markers = uniqueSortedStrings(out.Markers)
	out.Anchors = limitedStrings(uniqueSortedStrings(out.Anchors), 24)
	out.AnchoredMarkers = capabilitySignalAnchoredMarkers(out)
	out.Score = capabilitySignalScore(out)
	out.AnchoredScore = len(out.AnchoredMarkers)
	return out
}

func capabilitySignalScore(signals CapabilitySignals) int {
	score := 0
	for _, present := range []bool{
		signals.CapabilityLoop,
		signals.Evidence,
		signals.Assumptions,
		signals.Unknowns,
		signals.Verification,
		signals.Risks,
		signals.NextAction,
	} {
		if present {
			score++
		}
	}
	return score
}

func capabilitySignalAnchoredMarkers(signals CapabilitySignals) []string {
	if !signals.EvidenceAnchored && !signals.VerificationAnchored {
		return nil
	}
	var markers []string
	if signals.CapabilityLoop {
		markers = append(markers, "capability_loop")
	}
	if signals.EvidenceAnchored {
		markers = append(markers, "evidence")
	}
	if signals.Assumptions {
		markers = append(markers, "assumptions")
	}
	if signals.Unknowns {
		markers = append(markers, "unknowns")
	}
	if signals.VerificationAnchored {
		markers = append(markers, "verification")
	}
	if signals.Risks {
		markers = append(markers, "risks")
	}
	if signals.NextAction {
		markers = append(markers, "next_action")
	}
	return uniqueSortedStrings(markers)
}

func extractCapabilityAnchors(text string) []string {
	var anchors []string
	for _, match := range capabilityFileAnchorRE.FindAllString(text, -1) {
		anchor := strings.Trim(match, " \t\n\r\"'([{<")
		anchors = appendLimitedUniqueStrings(anchors, anchor, 12)
	}
	for _, match := range capabilityCmdAnchorRE.FindAllString(text, -1) {
		anchor := strings.TrimSpace(match)
		anchors = appendLimitedUniqueStrings(anchors, anchor, 12)
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"prompt dump", "request dump", "transcript", "jsonl", "verifier ok=true", "ok=true"} {
		if strings.Contains(lower, marker) {
			anchors = appendLimitedUniqueStrings(anchors, marker, 12)
		}
	}
	return uniqueSortedStrings(anchors)
}

func capabilityEvidenceAnchored(text string) bool {
	return capabilityLabeledLineAnchored(text, []string{"evidence:", "\"evidence\"", "证据"}, func(line string) bool {
		return len(extractCapabilityAnchors(line)) > 0
	})
}

func capabilityVerificationAnchored(text string) bool {
	return capabilityLabeledLineAnchored(text, []string{"verification:", "\"verification\"", "验证", "测试"}, func(line string) bool {
		if capabilityCmdAnchorRE.MatchString(line) {
			return true
		}
		lower := strings.ToLower(line)
		return strings.Contains(lower, "prompt dump") ||
			strings.Contains(lower, "request dump") ||
			strings.Contains(lower, "transcript") ||
			strings.Contains(lower, ".jsonl") ||
			strings.Contains(lower, "verifier ok=true") ||
			strings.Contains(lower, "ok=true")
	})
}

func capabilityLabeledLineAnchored(text string, labels []string, anchored func(string) bool) bool {
	for _, line := range strings.Split(text, "\n") {
		normalized := strings.ToLower(line)
		if !containsAny(normalized, labels...) {
			continue
		}
		if anchored(line) {
			return true
		}
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func compareBehaviorProfiles(goProfile, upstreamProfile BehaviorProfile) []BehaviorDifference {
	var diffs []BehaviorDifference
	if goProfile.Model != "" && upstreamProfile.Model != "" && goProfile.Model != upstreamProfile.Model {
		diffs = append(diffs, BehaviorDifference{
			Severity: "error",
			Field:    "model",
			Go:       goProfile.Model,
			Upstream: upstreamProfile.Model,
			Detail:   "model mismatch prevents attributing behavior differences to prompt/runtime changes",
		})
	}
	if goProfile.MaxTokens != 0 && upstreamProfile.MaxTokens != 0 && goProfile.MaxTokens != upstreamProfile.MaxTokens {
		diffs = append(diffs, BehaviorDifference{
			Severity: "warning",
			Field:    "max_tokens",
			Go:       fmt.Sprint(goProfile.MaxTokens),
			Upstream: fmt.Sprint(upstreamProfile.MaxTokens),
			Detail:   "different generation budget can change tool planning and stopping behavior",
		})
	}
	if goProfile.MainRequestCount != upstreamProfile.MainRequestCount {
		diffs = append(diffs, BehaviorDifference{
			Severity: "warning",
			Field:    "main_request_count",
			Go:       fmt.Sprint(goProfile.MainRequestCount),
			Upstream: fmt.Sprint(upstreamProfile.MainRequestCount),
			Detail:   "different number of main-thread model calls means the scenario transcript is not behavior-equivalent yet",
		})
	}
	if !sameStringSet(goProfile.InitialToolNames, upstreamProfile.InitialToolNames) {
		diffs = append(diffs, BehaviorDifference{
			Severity: "warning",
			Field:    "initial_tool_names",
			Go:       strings.Join(goProfile.InitialToolNames, ","),
			Upstream: strings.Join(upstreamProfile.InitialToolNames, ","),
			Detail:   "different tool surface can directly change planning even under the same prompt",
		})
	}
	diffs = append(diffs, compareFinalBehavior(goProfile.FinalMain, upstreamProfile.FinalMain)...)
	if goProfile.Totals.ToolResultErrors != 0 || upstreamProfile.Totals.ToolResultErrors != 0 {
		diffs = append(diffs, BehaviorDifference{
			Severity: "error",
			Field:    "tool_result_errors",
			Go:       fmt.Sprint(goProfile.Totals.ToolResultErrors),
			Upstream: fmt.Sprint(upstreamProfile.Totals.ToolResultErrors),
			Detail:   "tool errors in prompt history are behavior-changing evidence and must be explained or fixed",
		})
	}
	diffs = append(diffs, compareCapabilitySignals("capability_signals", goProfile.CapabilitySignals, upstreamProfile.CapabilitySignals)...)
	return diffs
}

func behaviorScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "main"
	}
	return scope
}

func compareFinalBehavior(goFinal, upstreamFinal *BehaviorTurn) []BehaviorDifference {
	if goFinal == nil || upstreamFinal == nil {
		return nil
	}
	var diffs []BehaviorDifference
	if goFinal.MessageCount != upstreamFinal.MessageCount {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.message_count",
			Go:       fmt.Sprint(goFinal.MessageCount),
			Upstream: fmt.Sprint(upstreamFinal.MessageCount),
			Detail:   "final request history has a different number of role messages; inspect context injection and message grouping before claiming behavior parity",
		})
	}
	if !sameStringSlice(goFinal.ToolUseSequence, upstreamFinal.ToolUseSequence) {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.tool_use_sequence",
			Go:       strings.Join(goFinal.ToolUseSequence, " -> "),
			Upstream: strings.Join(upstreamFinal.ToolUseSequence, " -> "),
			Detail:   "history-visible tool-use order differs; inspect whether this is expected tool-model divergence or a planning regression",
		})
	}
	if !sameStringSlice(goFinal.ToolResultSequence, upstreamFinal.ToolResultSequence) {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.tool_result_sequence",
			Go:       strings.Join(goFinal.ToolResultSequence, " -> "),
			Upstream: strings.Join(upstreamFinal.ToolResultSequence, " -> "),
			Detail:   "history-visible tool-result order differs; next-step planning may see different evidence",
		})
	}
	if goFinal.ToolCount != upstreamFinal.ToolCount {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.tool_count",
			Go:       fmt.Sprint(goFinal.ToolCount),
			Upstream: fmt.Sprint(upstreamFinal.ToolCount),
			Detail:   "final request exposes a different tool surface",
		})
	}
	if !sameStringSet(goFinal.RuntimeSections, upstreamFinal.RuntimeSections) {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.runtime_sections",
			Go:       strings.Join(goFinal.RuntimeSections, ","),
			Upstream: strings.Join(upstreamFinal.RuntimeSections, ","),
			Detail:   "runtime status is prompt-visible context; missing sections may change continuation discipline",
		})
	}
	if textByteDeltaSignificant(goFinal.UserTextBytes, upstreamFinal.UserTextBytes) {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.user_text_bytes",
			Go:       fmt.Sprint(goFinal.UserTextBytes),
			Upstream: fmt.Sprint(upstreamFinal.UserTextBytes),
			Detail:   "final request exposes substantially different user-visible text volume; this can shift attention and planning even when tool sequences match",
		})
	}
	if textByteDeltaSignificant(goFinal.AssistantTextBytes, upstreamFinal.AssistantTextBytes) {
		diffs = append(diffs, BehaviorDifference{
			Severity: "info",
			Field:    "final.assistant_text_bytes",
			Go:       fmt.Sprint(goFinal.AssistantTextBytes),
			Upstream: fmt.Sprint(upstreamFinal.AssistantTextBytes),
			Detail:   "assistant text carried into the final request differs substantially",
		})
	}
	diffs = append(diffs, compareCapabilitySignals("final.capability_signals", goFinal.CapabilitySignals, upstreamFinal.CapabilitySignals)...)
	return diffs
}

func compareCapabilitySignals(field string, goSignals, upstreamSignals CapabilitySignals) []BehaviorDifference {
	if goSignals.AnchoredScore == upstreamSignals.AnchoredScore && sameStringSet(goSignals.AnchoredMarkers, upstreamSignals.AnchoredMarkers) {
		return nil
	}
	severity := "info"
	detail := "niuma exposes different evidence-anchored capability-loop signals than upstream; inspect whether this is desired capability gain or context noise"
	if goSignals.AnchoredScore < upstreamSignals.AnchoredScore {
		severity = "warning"
		detail = "niuma exposes fewer evidence-anchored capability signals than upstream"
	}
	return []BehaviorDifference{{
		Severity: severity,
		Field:    field,
		Go:       fmt.Sprintf("%d:%s", goSignals.AnchoredScore, strings.Join(goSignals.AnchoredMarkers, ",")),
		Upstream: fmt.Sprintf("%d:%s", upstreamSignals.AnchoredScore, strings.Join(upstreamSignals.AnchoredMarkers, ",")),
		Detail:   detail,
	}}
}

func textByteDeltaSignificant(goBytes, upstreamBytes int) bool {
	if goBytes == upstreamBytes {
		return false
	}
	larger := goBytes
	smaller := upstreamBytes
	if larger < smaller {
		larger, smaller = smaller, larger
	}
	if larger < 512 {
		return false
	}
	if smaller == 0 {
		return true
	}
	return float64(larger-smaller)/float64(smaller) > 0.2
}

func toolEventNames(events []BehaviorToolEvent) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		name := strings.TrimSpace(event.Name)
		if name == "" {
			name = "unknown"
		}
		names = append(names, name)
	}
	return names
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func limitedStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return append([]string{}, values[:limit]...)
}

func appendLimitedUniqueStrings(values []string, value string, limit int) []string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 || len(values) >= limit {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func sameStringSet(a, b []string) bool {
	return sameStringSlice(uniqueSortedStrings(a), uniqueSortedStrings(b))
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rawJSONLen(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	return len(raw)
}

func rawContentBytes(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return len(text)
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		total := 0
		for _, part := range parts {
			total += rawContentBytes(part["text"])
		}
		return total
	}
	return len(raw)
}
