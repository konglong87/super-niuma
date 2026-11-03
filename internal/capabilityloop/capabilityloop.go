// Package capabilityloop parses and renders the capability_loop evidence
// protocol that sub-agents use to hand structured findings back to their
// parent: what was observed, what was assumed, what is still unknown, what was
// verified, what the risks are, and what must happen next.
//
// The code here was extracted verbatim from internal/query (AUDIT-P2-01 step 1)
// and is deliberately free of any *query.Session dependency: every function is
// pure, taking protocol text or a decoded task row and returning hints,
// decision contexts, or the runtime-status lines shown to the parent model.
package capabilityloop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
)

type HintData struct {
	Evidence              []string `json:"evidence"`
	Assumptions           []string `json:"assumptions"`
	Unknowns              []string `json:"unknowns"`
	Verification          []string `json:"verification"`
	Risks                 []string `json:"risks"`
	NextAction            string   `json:"next_action"`
	ResolvedFollowUp      string   `json:"resolved_follow_up"`
	SupersedesEvidenceID  string   `json:"supersedes_evidence_id"`
	SupersedesEvidenceIDs []string `json:"supersedes_evidence_ids"`
}

type ArtifactHints struct {
	SessionID      string
	TranscriptPath string
	OutputFile     string
	WorktreePath   string
	WorktreeBranch string
}

type DecisionContext struct {
	TaskID         uint64
	ToolUseID      string
	ToolName       string
	EvidenceSource string
	AgentName      string
	Description    string
	Status         string
	SessionID      string
	TranscriptPath string
	OutputFile     string
	WorktreePath   string
	WorktreeBranch string
	Loop           HintData
}

func CompactSummaryDecisionContext(artifacts ArtifactHints, loop HintData) DecisionContext {
	return DecisionContext{
		ToolUseID:      "compact-summary-capability-loop",
		ToolName:       "compact_summary",
		EvidenceSource: "compact_summary",
		AgentName:      "compact-summary",
		Description:    "Recovered capability loop facts from compact summary",
		Status:         "compacted",
		SessionID:      artifacts.SessionID,
		TranscriptPath: artifacts.TranscriptPath,
		OutputFile:     artifacts.OutputFile,
		WorktreePath:   artifacts.WorktreePath,
		WorktreeBranch: artifacts.WorktreeBranch,
		Loop:           loop,
	}
}

func ToolUseIDFromFollowUpID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "tool:") {
		return strings.TrimSpace(strings.TrimPrefix(id, "tool:"))
	}
	if id != "" {
		return "compact-summary-" + id
	}
	return "compact-summary-capability-loop"
}

func BlockTextForCompactFacts(block anthropic.ContentBlock) string {
	parts := []string{block.Text, block.Content, block.Name, block.ToolUseID}
	return strings.Join(parts, "\n")
}

func ArtifactHintsFromCompactFacts(values []string) ArtifactHints {
	var hints ArtifactHints
	for _, raw := range values {
		key, value, ok := strings.Cut(strings.TrimSpace(raw), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "session_id":
			hints.SessionID = value
		case "transcript_path":
			hints.TranscriptPath = value
		case "output_file":
			hints.OutputFile = value
		case "worktree_path":
			hints.WorktreePath = value
		case "worktree_branch":
			hints.WorktreeBranch = value
		}
	}
	return hints
}

func IsSubAgentResultTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "task", "agent":
		return true
	default:
		return false
	}
}

func SubAgentToolEvidenceSource(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "task":
		return "task_tool"
	case "agent":
		return "agent_tool"
	default:
		return strings.ToLower(strings.TrimSpace(toolName))
	}
}

func DescriptionFromToolInput(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var decoded struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &decoded); err != nil {
		return ""
	}
	return decoded.Description
}

func FromToolResult(content string) (HintData, ArtifactHints, bool) {
	for _, candidate := range JSONCandidates(content) {
		if loop, artifacts, ok := capabilityLoopFromJSON(candidate); ok {
			return loop, artifacts, true
		}
	}
	if loop, ok := capabilityLoopFromPersistedSummary(content); ok {
		return loop, ArtifactHints{}, true
	}
	return HintData{}, ArtifactHints{}, false
}

func StatusFromToolResult(content string) string {
	for _, candidate := range JSONCandidates(content) {
		if status := statusFromCapabilityLoopJSON(candidate); status != "" {
			return status
		}
	}
	return statusFromPersistedCapabilitySummary(content)
}

// JSONCandidates returns the JSON payloads worth decoding out of a tool result,
// most explicit first. Callers decode them in order; what "success" means is the
// caller's business, so this function only widens the net and never interprets.
//
// It is the one candidate extractor for the whole protocol (was TODO-092): all
// tag pairs, ASCII-case-insensitive tags, a short-circuit when the key is absent,
// then the un-tagged fallbacks.
func JSONCandidates(content string) []string {
	content = strings.TrimSpace(content)
	// Every decoder downstream requires a capability_loop key, so content without
	// it can never yield a hint. Short-circuiting keeps a 50KB tool result from
	// being scanned and unmarshalled for nothing.
	if content == "" || indexFold(content, Key) < 0 {
		return nil
	}
	var candidates []string
	seen := make(map[string]bool, 4)
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			return
		}
		seen[candidate] = true
		candidates = append(candidates, candidate)
	}
	for rest := content; ; {
		start := indexFold(rest, OpenTag)
		if start < 0 {
			break
		}
		body := rest[start+len(OpenTag):]
		end := indexFold(body, CloseTag)
		if end < 0 {
			break
		}
		add(body[:end])
		rest = body[end+len(CloseTag):]
	}
	// Un-tagged fallbacks, and both are needed: the whole content is the only one
	// that can parse a JSON array root, and the outermost {...} slice is the only
	// one that can reach JSON embedded in prose. They are never both valid and
	// different — for an object root the slice is the trimmed content itself.
	add(content)
	if start, end := strings.Index(content, "{"), strings.LastIndex(content, "}"); start >= 0 && end > start {
		add(content[start : end+1])
	}
	return candidates
}

// indexFold reports the first index of an all-ASCII needle in haystack, ignoring
// ASCII case. Unlike lowercasing the haystack and indexing that, the offset it
// returns is valid against haystack itself: lowercasing can change a rune's byte
// length ("İ" becomes two runes), which slid every later offset and sliced tag
// bodies apart.
func indexFold(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	first := needle[0] | 0x20
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i]|0x20 != first {
			continue
		}
		if strings.EqualFold(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func capabilityLoopFromJSON(content string) (HintData, ArtifactHints, bool) {
	var decoded any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decoded); err != nil {
		return HintData{}, ArtifactHints{}, false
	}
	var loop HintData
	if findCapabilityLoopJSON(decoded, &loop) && HasHint(loop) {
		return loop, artifactHintsNearCapabilityLoop(decoded), true
	}
	return HintData{}, ArtifactHints{}, false
}

func statusFromCapabilityLoopJSON(content string) string {
	var decoded any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decoded); err != nil {
		return ""
	}
	return firstStatusNearCapabilityLoop(decoded)
}

func firstStatusNearCapabilityLoop(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed[Key]; ok {
			if status, ok := typed["status"].(string); ok {
				return strings.TrimSpace(status)
			}
		}
		for key, child := range typed {
			if strings.EqualFold(key, "result") {
				if status := firstStatusNearCapabilityLoop(child); status != "" {
					return status
				}
			}
		}
		for _, child := range typed {
			if status := firstStatusNearCapabilityLoop(child); status != "" {
				return status
			}
		}
	case []any:
		for _, child := range typed {
			if status := firstStatusNearCapabilityLoop(child); status != "" {
				return status
			}
		}
	}
	return ""
}

func artifactHintsNearCapabilityLoop(value any) ArtifactHints {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed[Key]; ok {
			return artifactHintsFromMap(typed)
		}
		for key, child := range typed {
			if strings.EqualFold(key, "result") {
				if hints := artifactHintsNearCapabilityLoop(child); hasArtifactHints(hints) {
					return hints
				}
			}
		}
		for _, child := range typed {
			if hints := artifactHintsNearCapabilityLoop(child); hasArtifactHints(hints) {
				return hints
			}
		}
	case []any:
		for _, child := range typed {
			if hints := artifactHintsNearCapabilityLoop(child); hasArtifactHints(hints) {
				return hints
			}
		}
	}
	return ArtifactHints{}
}

func artifactHintsFromMap(raw map[string]any) ArtifactHints {
	return ArtifactHints{
		SessionID:      stringFromAny(raw["session_id"]),
		TranscriptPath: stringFromAny(raw["transcript_path"]),
		OutputFile:     stringFromAny(raw["output_file"]),
		WorktreePath:   stringFromAny(raw["worktree_path"]),
		WorktreeBranch: stringFromAny(raw["worktree_branch"]),
	}
}

func hasArtifactHints(hints ArtifactHints) bool {
	return strings.TrimSpace(hints.SessionID) != "" ||
		strings.TrimSpace(hints.TranscriptPath) != "" ||
		strings.TrimSpace(hints.OutputFile) != "" ||
		strings.TrimSpace(hints.WorktreePath) != "" ||
		strings.TrimSpace(hints.WorktreeBranch) != ""
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func findCapabilityLoopJSON(value any, out *HintData) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.EqualFold(key, Key) {
				if loop, ok := decodeCapabilityLoopMap(child); ok {
					*out = loop
					return true
				}
			}
			if findCapabilityLoopJSON(child, out) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if findCapabilityLoopJSON(child, out) {
				return true
			}
		}
	}
	return false
}

func decodeCapabilityLoopMap(value any) (HintData, bool) {
	raw, ok := value.(map[string]any)
	if !ok {
		return HintData{}, false
	}
	loop := HintData{
		Evidence:     stringSliceFromCapabilityValue(raw["evidence"]),
		Assumptions:  stringSliceFromCapabilityValue(raw["assumptions"]),
		Unknowns:     stringSliceFromCapabilityValue(raw["unknowns"]),
		Verification: stringSliceFromCapabilityValue(raw["verification"]),
		Risks:        stringSliceFromCapabilityValue(raw["risks"]),
	}
	if nextAction, ok := raw["next_action"].(string); ok {
		loop.NextAction = strings.TrimSpace(nextAction)
	}
	if resolved, ok := raw["resolved_follow_up"].(string); ok {
		loop.ResolvedFollowUp = strings.TrimSpace(resolved)
	}
	if supersedes, ok := raw["supersedes_evidence_id"].(string); ok {
		loop.SupersedesEvidenceID = strings.TrimSpace(supersedes)
	}
	loop.SupersedesEvidenceIDs = stringSliceFromCapabilityValue(raw["supersedes_evidence_ids"])
	return loop, HasHint(loop)
}

func stringSliceFromCapabilityValue(value any) []string {
	switch typed := value.(type) {
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			return []string{trimmed}
		}
	case []any:
		var out []string
		for _, item := range typed {
			if text, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(text); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	}
	return nil
}

func capabilityLoopFromPersistedSummary(content string) (HintData, bool) {
	var loop HintData
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		normalized := strings.TrimSpace(line)
		lower := strings.ToLower(normalized)
		if strings.Contains(lower, "capability loop summary preserved from full output") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if normalized == "" {
			continue
		}
		if strings.HasPrefix(lower, "preview (first ") || strings.Contains(lower, "</persisted-output>") {
			break
		}
		normalized = strings.TrimPrefix(strings.TrimPrefix(normalized, "- "), "* ")
		key, value, ok := strings.Cut(normalized, ":")
		if !ok {
			continue
		}
		addCapabilityLoopSummaryField(&loop, key, value)
	}
	return loop, HasHint(loop)
}

func statusFromPersistedCapabilitySummary(content string) string {
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		normalized := strings.TrimSpace(line)
		lower := strings.ToLower(normalized)
		if strings.Contains(lower, "capability loop summary preserved from full output") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if normalized == "" {
			continue
		}
		if strings.HasPrefix(lower, "preview (first ") || strings.Contains(lower, "</persisted-output>") {
			break
		}
		normalized = strings.TrimPrefix(strings.TrimPrefix(normalized, "- "), "* ")
		key, value, ok := strings.Cut(normalized, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "status") {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func addCapabilityLoopSummaryField(loop *HintData, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "evidence":
		loop.Evidence = append(loop.Evidence, value)
	case "assumptions":
		loop.Assumptions = append(loop.Assumptions, value)
	case "unknowns":
		loop.Unknowns = append(loop.Unknowns, value)
	case "verification":
		loop.Verification = append(loop.Verification, value)
	case "risks":
		loop.Risks = append(loop.Risks, value)
	case "next_action":
		loop.NextAction = value
	case "resolved_follow_up":
		loop.ResolvedFollowUp = value
	// Both spellings land in the plural slice, comma-split: TaskHint renders the
	// whole superseded set as one comma-joined "supersedes_evidence_id" value, so
	// reading it back as a single opaque string produced one bogus id that matched
	// no follow-up, and the plural key was not read at all (was TODO-094).
	// Evidence ids are "task:<n>" or "tool:<id>" and never contain a comma.
	case "supersedes_evidence_id", "supersedes_evidence_ids":
		for _, id := range strings.Split(value, ",") {
			if id = strings.TrimSpace(id); id != "" {
				loop.SupersedesEvidenceIDs = append(loop.SupersedesEvidenceIDs, id)
			}
		}
	}
}

func HasHint(loop HintData) bool {
	return len(loop.Evidence) > 0 ||
		len(loop.Assumptions) > 0 ||
		len(loop.Unknowns) > 0 ||
		len(loop.Verification) > 0 ||
		len(loop.Risks) > 0 ||
		strings.TrimSpace(loop.NextAction) != "" ||
		agentCapabilityResolutionSignal(loop)
}

func SameDecisionContext(a, b DecisionContext) bool {
	if a.TaskID != 0 && b.TaskID != 0 {
		return a.TaskID == b.TaskID
	}
	return strings.TrimSpace(a.ToolUseID) != "" && strings.TrimSpace(a.ToolUseID) == strings.TrimSpace(b.ToolUseID)
}

func TaskStatusIsTerminal(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return true
	default:
		return false
	}
}

func TaskStatusAction(status string, agentGetAvailable bool) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case agenttasks.StatusCompleted:
		if !agentGetAvailable {
			return "completion notification: result is ready; use the result preview, transcript_path, and output_file below because AgentGet is not available"
		}
		return "completion notification: result is ready; call AgentGet before using findings"
	case agenttasks.StatusFailed:
		if !agentGetAvailable {
			return "failure notification: use the status, result preview, transcript_path, and output_file below because AgentGet is not available"
		}
		return "failure notification: call AgentGet before explaining failure details"
	case agenttasks.StatusCancelled:
		if !agentGetAvailable {
			return "cancellation notification: use the status, result preview, transcript_path, and output_file below because AgentGet is not available"
		}
		return "cancellation notification: call AgentGet before explaining cancellation details"
	case agenttasks.StatusTimeout:
		if !agentGetAvailable {
			return "timeout notification: preserve partial evidence; inspect result preview, transcript_path, or output_file and decide whether to retry with a longer profile or narrow the scope"
		}
		return "timeout notification: call AgentGet, preserve partial evidence, then decide whether to retry with a longer profile or narrow the scope"
	default:
		return ""
	}
}

func TaskResultHint(status, resultJSON string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
	default:
		return ""
	}
	resultJSON = strings.TrimSpace(resultJSON)
	if resultJSON == "" {
		return ""
	}
	var result struct {
		Content        string   `json:"content"`
		TranscriptPath string   `json:"transcript_path"`
		OutputFile     string   `json:"output_file"`
		WorktreePath   string   `json:"worktree_path"`
		WorktreeBranch string   `json:"worktree_branch"`
		Turns          int      `json:"turns"`
		CapabilityLoop HintData `json:"capability_loop"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return ""
	}
	var parts []string
	if content := trimRuntimeStatusText(result.Content, 240); content != "" {
		parts = append(parts, "result preview: "+content)
	}
	if loop := TaskHint(result.CapabilityLoop); loop != "" {
		parts = append(parts, loop)
	}
	if transcriptPath := trimRuntimeStatusText(result.TranscriptPath, 180); transcriptPath != "" {
		parts = append(parts, "transcript_path: "+transcriptPath)
	}
	if outputFile := trimRuntimeStatusText(result.OutputFile, 180); outputFile != "" {
		parts = append(parts, "output_file: "+outputFile)
	}
	if worktreePath := trimRuntimeStatusText(result.WorktreePath, 180); worktreePath != "" {
		parts = append(parts, "worktree_path: "+worktreePath)
	}
	if worktreeBranch := trimRuntimeStatusText(result.WorktreeBranch, 120); worktreeBranch != "" {
		parts = append(parts, "worktree_branch: "+worktreeBranch)
	}
	if result.Turns > 0 {
		parts = append(parts, fmt.Sprintf("turns: %d", result.Turns))
	}
	return strings.Join(parts, "; ")
}

// StoredTask is the persisted-task shape this package reads, and deliberately
// names only the five columns the protocol needs. It exists so that a pure
// protocol package does not have to know the storage layer: taking
// mysqlstore.AgentTask here made capabilityloop import internal/storage/mysql,
// which imports internal/goal, which closed an import cycle the moment goal
// tried to reference the FollowUpField* constants (was TODO-102). Callers
// holding a storage row fill this in at the call site.
type StoredTask struct {
	ID          uint64
	AgentName   string
	Description string
	Status      string
	ResultJSON  string
}

func DecisionContextFromStoredTask(task StoredTask) (DecisionContext, bool) {
	if !TaskStatusIsTerminal(task.Status) {
		return DecisionContext{}, false
	}
	resultJSON := strings.TrimSpace(task.ResultJSON)
	if resultJSON == "" {
		return DecisionContext{}, false
	}
	var result struct {
		Status         string   `json:"status"`
		AgentName      string   `json:"agent_name"`
		SessionID      string   `json:"session_id"`
		TranscriptPath string   `json:"transcript_path"`
		OutputFile     string   `json:"output_file"`
		WorktreePath   string   `json:"worktree_path"`
		WorktreeBranch string   `json:"worktree_branch"`
		CapabilityLoop HintData `json:"capability_loop"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil || !HasHint(result.CapabilityLoop) {
		return DecisionContext{}, false
	}
	return DecisionContext{
		TaskID:         task.ID,
		EvidenceSource: "terminal_agent_task_store",
		AgentName:      firstNonEmpty(result.AgentName, task.AgentName),
		Description:    task.Description,
		Status:         firstNonEmpty(result.Status, task.Status),
		SessionID:      result.SessionID,
		TranscriptPath: result.TranscriptPath,
		OutputFile:     result.OutputFile,
		WorktreePath:   result.WorktreePath,
		WorktreeBranch: result.WorktreeBranch,
		Loop:           result.CapabilityLoop,
	}, true
}

func TaskHint(loop HintData) string {
	var parts []string
	if evidence := firstCapabilityLoopSignal(loop.Evidence, 180); evidence != "" {
		parts = append(parts, "evidence: "+evidence)
	}
	if assumptions := firstCapabilityLoopSignal(loop.Assumptions, 160); assumptions != "" {
		parts = append(parts, "assumptions: "+assumptions)
	}
	if unknowns := firstCapabilityLoopSignal(loop.Unknowns, 160); unknowns != "" {
		parts = append(parts, "unknowns: "+unknowns)
	}
	if verification := firstCapabilityLoopSignal(loop.Verification, 160); verification != "" {
		parts = append(parts, "verification: "+verification)
	}
	if risks := firstCapabilityLoopSignal(loop.Risks, 160); risks != "" {
		parts = append(parts, "risks: "+risks)
	}
	if nextAction := trimCapabilityLoopSignal(loop.NextAction, 180); nextAction != "" {
		parts = append(parts, "next_action: "+nextAction)
	}
	if resolved := trimCapabilityLoopSignal(loop.ResolvedFollowUp, 180); resolved != "" {
		parts = append(parts, "resolved_follow_up: "+resolved)
	}
	if ids := agentCapabilitySupersededEvidenceIDs(loop); len(ids) > 0 {
		parts = append(parts, "supersedes_evidence_id: "+strings.Join(ids, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return LineMarker + " " + strings.Join(parts, " | ")
}

func FollowUpLine(item DecisionContext) string {
	var parts []string
	if nextAction := trimCapabilityLoopSignal(item.Loop.NextAction, 180); nextAction != "" {
		parts = append(parts, FollowUpFieldNextAction+": "+nextAction)
	}
	if !agentCapabilityResolutionSignal(item.Loop) {
		if verification := firstCapabilityLoopSignal(item.Loop.Verification, 160); verification != "" {
			parts = append(parts, FollowUpFieldVerification+": "+verification)
		}
	}
	if unknown := firstCapabilityLoopSignal(item.Loop.Unknowns, 140); unknown != "" {
		parts = append(parts, FollowUpFieldUnknown+": "+unknown)
	}
	if risk := firstCapabilityLoopSignal(item.Loop.Risks, 140); risk != "" {
		parts = append(parts, FollowUpFieldRisk+": "+risk)
	}
	if len(parts) == 0 {
		return ""
	}
	if source := SourceHint(item); source != "" {
		parts = append([]string{source}, parts...)
	}
	if id := FollowUpID(item); id != "" {
		parts = append(parts, "follow_up_id: "+id)
	}
	if action := agentEvidenceSourceActionHint(item); action != "" {
		parts = append(parts, action)
	}
	name := trimRuntimeStatusText(item.AgentName, 48)
	if name == "" {
		name = "agent"
	}
	status := trimRuntimeStatusText(item.Status, 32)
	if status == "" {
		status = "unknown"
	}
	description := trimRuntimeStatusText(item.Description, 100)
	var prefix string
	if item.TaskID > 0 {
		prefix = fmt.Sprintf("- pending_follow_up: #%d %s %s", item.TaskID, name, status)
	} else {
		toolName := trimRuntimeStatusText(firstNonEmpty(item.ToolName, "sub-agent"), 48)
		prefix = fmt.Sprintf("- pending_follow_up: %s result %s", toolName, status)
	}
	if description != "" {
		prefix += ": " + description
	}
	if artifacts := ArtifactHint(item); artifacts != "" {
		parts = append(parts, artifacts)
	}
	return prefix + "; " + strings.Join(parts, " | ")
}

func ResolvedFollowUps(items []DecisionContext) map[string]int {
	resolved := make(map[string]int)
	for i, item := range items {
		if !agentCapabilityResolutionProof(item) {
			continue
		}
		for _, id := range agentCapabilitySupersededEvidenceIDs(item.Loop) {
			resolved[id] = i
		}
	}
	return resolved
}

func agentCapabilityResolutionProof(item DecisionContext) bool {
	if agentEvidenceFailedOrCancelled(item) || !agentCapabilityResolutionSignal(item.Loop) {
		return false
	}
	return firstCapabilityLoopSignal(item.Loop.Evidence, 180) != "" ||
		firstCapabilityLoopSignal(item.Loop.Verification, 160) != ""
}

func agentEvidenceFailedOrCancelled(item DecisionContext) bool {
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return true
	default:
		return false
	}
}

func agentCapabilityResolutionSignal(loop HintData) bool {
	return trimCapabilityLoopSignal(loop.ResolvedFollowUp, 180) != "" &&
		len(agentCapabilitySupersededEvidenceIDs(loop)) > 0
}

func agentCapabilitySupersededEvidenceIDs(loop HintData) []string {
	ids := make([]string, 0, 1+len(loop.SupersedesEvidenceIDs))
	if id := strings.TrimSpace(loop.SupersedesEvidenceID); id != "" {
		ids = append(ids, id)
	}
	for _, id := range loop.SupersedesEvidenceIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if len(ids) <= 1 {
		return ids
	}
	seen := make(map[string]bool, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func FollowUpID(item DecisionContext) string {
	if item.TaskID > 0 {
		return fmt.Sprintf("task:%d", item.TaskID)
	}
	if toolUseID := strings.TrimSpace(item.ToolUseID); toolUseID != "" {
		return "tool:" + toolUseID
	}
	return ""
}

func firstCapabilityLoopSignal(values []string, limit int) string {
	for _, value := range values {
		if signal := trimCapabilityLoopSignal(value, limit); signal != "" {
			return signal
		}
	}
	return ""
}

func trimCapabilityLoopSignal(value string, limit int) string {
	if IsPlaceholder(value) {
		return ""
	}
	return trimRuntimeStatusText(value, limit)
}

// IsPlaceholder reports whether a capability_loop value carries no sub-agent
// signal, and is the one placeholder set for the whole protocol (was TODO-093 ①).
//
// The last entry is the next_action internal/agentruntime injects when a
// *completed* sub-agent reported none. It is filtered because it restates what
// the parent already knows, and because it alone satisfies HasHint: left in, it
// admits a decision context with no content into the parent's three-slot evidence
// ring and can evict real evidence.
//
// agentruntime injects four other status-specific defaults (failure, cancelled,
// timeout, unknown). Those are deliberately absent here: they tell the parent it
// must handle a failure, which is a real obligation, and the follow-up gate is
// tested to surface them.
func IsPlaceholder(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.Trim(value, ".。")))
	switch normalized {
	case "", "none", "none observed", "not observed", "n/a", "na", "not applicable", "no explicit assumptions reported", "no explicit unknowns reported", "no explicit verification reported",
		"parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing":
		return true
	default:
		return false
	}
}

func SourceHint(item DecisionContext) string {
	source := trimRuntimeStatusText(item.EvidenceSource, 80)
	if source == "" {
		return ""
	}
	return "evidence_source: " + source
}

func agentEvidenceSourceActionHint(item DecisionContext) string {
	switch trimRuntimeStatusText(item.EvidenceSource, 80) {
	case "agent_get":
		return "source_action: AgentGet result was explicitly retrieved; use it as the current sub-agent result, but still run or disclose listed verification and risks before finalizing."
	case "task_tool", "agent_tool":
		return "source_action: Synchronous tool_result evidence is already in the parent turn; carry it forward and handle next_action before finalizing."
	case "terminal_agent_task_store":
		return "source_action: This is fallback terminal task-store evidence; inspect the listed artifacts or rerun verification before relying on it as complete."
	case "compact_summary":
		return "source_action: This evidence was recovered from compact summary; treat it as condensed context and re-verify or disclose compaction limits for unresolved unknowns/risks."
	default:
		return ""
	}
}

func ArtifactHint(item DecisionContext) string {
	var parts []string
	if sessionID := trimRuntimeStatusText(item.SessionID, 80); sessionID != "" {
		parts = append(parts, "session_id: "+sessionID)
	}
	if transcriptPath := trimRuntimeStatusText(item.TranscriptPath, 180); transcriptPath != "" {
		parts = append(parts, "transcript_path: "+transcriptPath)
	}
	if outputFile := trimRuntimeStatusText(item.OutputFile, 180); outputFile != "" {
		parts = append(parts, "output_file: "+outputFile)
	}
	if worktreePath := trimRuntimeStatusText(item.WorktreePath, 180); worktreePath != "" {
		parts = append(parts, "worktree_path: "+worktreePath)
	}
	if worktreeBranch := trimRuntimeStatusText(item.WorktreeBranch, 120); worktreeBranch != "" {
		parts = append(parts, "worktree_branch: "+worktreeBranch)
	}
	if len(parts) == 0 {
		return ""
	}
	return "artifacts: " + strings.Join(parts, " | ")
}

// TODO(AUDIT-P2-03): trimRuntimeStatusText and firstNonEmpty are byte-identical
// copies of the internal/query helpers of the same name. AUDIT-P2-03 owns
// collapsing the repo-wide duplicates of these into one shared util package;
// copying them here keeps this extraction a pure move instead of pre-empting
// that decision.
func trimRuntimeStatusText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
