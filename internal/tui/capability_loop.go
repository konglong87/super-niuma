// capability_loop.go 解析 capability_loop 协议包裹的工具输出与 agent evidence。

package tui

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/capabilityloop"
)

type capabilityLoopToolSummary struct {
	Evidence              []string `json:"evidence"`
	Assumptions           []string `json:"assumptions"`
	Unknowns              []string `json:"unknowns"`
	Verification          []string `json:"verification"`
	Risks                 []string `json:"risks"`
	NextAction            string   `json:"next_action"`
	FollowUpID            string   `json:"follow_up_id"`
	ResolvedFollowUp      string   `json:"resolved_follow_up"`
	SupersedesEvidenceID  string   `json:"supersedes_evidence_id"`
	SupersedesEvidenceIDs []string `json:"supersedes_evidence_ids"`
}

func capabilityLoopToolResultSummary(rawOutput string) string {
	root, ok := capabilityLoopRoot(rawOutput)
	if !ok {
		return ""
	}
	for _, path := range [][]string{
		{"capability_loop"},
		{"result", "capability_loop"},
		{"task", "result", "capability_loop"},
	} {
		loopRaw, ok := nestedRawMessage(root, path)
		if !ok {
			continue
		}
		var loop capabilityLoopToolSummary
		if err := json.Unmarshal(loopRaw, &loop); err != nil {
			continue
		}
		if summary := formatCapabilityLoopToolSummary(loop); summary != "" {
			return summary
		}
	}
	return ""
}

func recentAgentEvidenceSummary(toolName, rawOutput string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "task", "agent", "agentget":
	default:
		return ""
	}
	loop := strings.TrimSpace(capabilityLoopToolResultSummary(rawOutput))
	if loop == "" {
		return ""
	}
	var parts []string
	if provenance := agentEvidenceProvenanceSummary(rawOutput); provenance != "" {
		parts = append(parts, provenance)
	}
	if source := agentEvidenceSourceSummary(toolName, rawOutput); source != "" {
		parts = append(parts, source)
	}
	parts = append(parts, loop)
	if artifacts := agentEvidenceArtifactSummary(rawOutput); artifacts != "" {
		parts = append(parts, artifacts)
	}
	return strings.Join(parts, " | ")
}

func agentEvidenceProvenanceSummary(rawOutput string) string {
	root, ok := capabilityLoopRoot(rawOutput)
	if !ok {
		return ""
	}
	source := firstNonEmpty(
		jsonFieldString(root, "evidence_source"),
		nestedJSONFieldString(root, []string{"result", "evidence_source"}),
		nestedJSONFieldString(root, []string{"task", "result", "evidence_source"}),
	)
	if source == "" {
		return ""
	}
	return "source: " + evidenceSourceLabel(source)
}

func evidenceSourceLabel(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	if source == "terminal_agent_task_store" {
		return "task_store"
	}
	return truncate(oneLine(source), 32)
}

func capabilityLoopRoot(rawOutput string) (map[string]json.RawMessage, bool) {
	rawOutput = strings.TrimSpace(rawOutput)
	if rawOutput == "" {
		return nil, false
	}
	if wrapped := extractCapabilityLoopWrapperJSON(rawOutput); wrapped != "" {
		rawOutput = wrapped
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawOutput), &root); err != nil {
		return nil, false
	}
	return root, true
}

func agentEvidenceSourceSummary(toolName, rawOutput string) string {
	root, ok := capabilityLoopRoot(rawOutput)
	if !ok {
		return ""
	}
	taskID := firstNonEmpty(
		jsonFieldString(root, "task_id"),
		nestedJSONFieldString(root, []string{"task", "id"}),
		nestedJSONFieldString(root, []string{"result", "task_id"}),
	)
	agentName := firstNonEmpty(
		nestedJSONFieldString(root, []string{"task", "agent_name"}),
		jsonFieldString(root, "agent_name"),
		nestedJSONFieldString(root, []string{"result", "agent_name"}),
	)
	status := firstNonEmpty(
		nestedJSONFieldString(root, []string{"task", "status"}),
		jsonFieldString(root, "status"),
		nestedJSONFieldString(root, []string{"result", "status"}),
	)
	description := firstNonEmpty(
		nestedJSONFieldString(root, []string{"task", "description"}),
		jsonFieldString(root, "description"),
		nestedJSONFieldString(root, []string{"result", "description"}),
	)
	if taskID == "" && agentName == "" && status == "" && description == "" {
		return ""
	}
	label := strings.TrimSpace(toolName)
	if label == "" {
		label = "agent"
	}
	parts := []string{label}
	if taskID != "" {
		parts = append(parts, "#"+taskID)
	}
	if agentName != "" && !strings.EqualFold(agentName, label) {
		parts = append(parts, agentName)
	}
	if status != "" {
		parts = append(parts, status)
	}
	summary := strings.Join(parts, " ")
	if description != "" {
		summary += ": " + description
	}
	return truncate(oneLine(summary), 96)
}

func agentEvidenceArtifactSummary(rawOutput string) string {
	root, ok := capabilityLoopRoot(rawOutput)
	if !ok {
		return ""
	}
	type artifactField struct {
		key   string
		label string
		limit int
	}
	fields := []artifactField{
		{key: "session_id", label: "session_id", limit: 48},
		{key: "transcript_path", label: "transcript_path", limit: 64},
		{key: "output_file", label: "output_file", limit: 64},
		{key: "worktree_path", label: "worktree_path", limit: 64},
		{key: "worktree_branch", label: "worktree_branch", limit: 48},
	}
	var parts []string
	for _, field := range fields {
		value := firstNonEmpty(
			jsonFieldString(root, field.key),
			nestedJSONFieldString(root, []string{"result", field.key}),
			nestedJSONFieldString(root, []string{"task", "result", field.key}),
		)
		if value == "" {
			continue
		}
		parts = append(parts, field.label+": "+truncate(oneLine(value), field.limit))
	}
	if len(parts) == 0 {
		return ""
	}
	return "artifacts: " + strings.Join(parts, " | ")
}

func extractCapabilityLoopWrapperJSON(rawOutput string) string {
	const (
		startTag = "<capability_loop>"
		endTag   = "</capability_loop>"
	)
	start := strings.Index(rawOutput, startTag)
	if start < 0 {
		return ""
	}
	start += len(startTag)
	end := strings.Index(rawOutput[start:], endTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rawOutput[start : start+end])
}

func jsonFieldString(root map[string]json.RawMessage, key string) string {
	raw, ok := root[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strings.TrimSpace(strconv.FormatFloat(value, 'f', -1, 64))
	}
	return ""
}

func nestedJSONFieldString(root map[string]json.RawMessage, path []string) string {
	if len(path) == 0 {
		return ""
	}
	if len(path) == 1 {
		return jsonFieldString(root, path[0])
	}
	parent, ok := nestedRawMessage(root, path[:len(path)-1])
	if !ok {
		return ""
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(parent, &nested); err != nil {
		return ""
	}
	return jsonFieldString(nested, path[len(path)-1])
}

func nestedRawMessage(root map[string]json.RawMessage, path []string) (json.RawMessage, bool) {
	current := root
	for i, key := range path {
		raw, ok := current[key]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			return nil, false
		}
		if i == len(path)-1 {
			return raw, true
		}
		var next map[string]json.RawMessage
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func formatCapabilityLoopToolSummary(loop capabilityLoopToolSummary) string {
	var parts []string
	if evidence := firstCapabilityLoopSignal(loop.Evidence...); evidence != "" {
		parts = append(parts, "evidence: "+truncate(oneLine(evidence), 72))
	}
	if assumptions := firstCapabilityLoopSignal(loop.Assumptions...); assumptions != "" {
		parts = append(parts, "assumptions: "+truncate(oneLine(assumptions), 64))
	}
	if unknowns := firstCapabilityLoopSignal(loop.Unknowns...); unknowns != "" {
		parts = append(parts, "unknowns: "+truncate(oneLine(unknowns), 64))
	}
	if verification := firstCapabilityLoopSignal(loop.Verification...); verification != "" {
		parts = append(parts, "verification: "+truncate(oneLine(verification), 64))
	}
	if risks := firstCapabilityLoopSignal(loop.Risks...); risks != "" {
		parts = append(parts, "risks: "+truncate(oneLine(risks), 64))
	}
	if nextAction := firstCapabilityLoopSignal(loop.NextAction); nextAction != "" {
		parts = append(parts, "next_action: "+truncate(oneLine(nextAction), 72))
	}
	if followUpID := firstCapabilityLoopSignal(loop.FollowUpID); followUpID != "" {
		parts = append(parts, "follow_up_id: "+truncate(oneLine(followUpID), 64))
	}
	if resolved := firstCapabilityLoopSignal(loop.ResolvedFollowUp); resolved != "" {
		parts = append(parts, "resolved_follow_up: "+truncate(oneLine(resolved), 72))
	}
	if supersedes := capabilityLoopSupersedesSummary(loop); supersedes != "" {
		parts = append(parts, "supersedes_evidence_id: "+truncate(oneLine(supersedes), 96))
	}
	if len(parts) == 0 {
		return ""
	}
	return "capability_loop: " + strings.Join(parts, " | ")
}

func capabilityLoopSupersedesSummary(loop capabilityLoopToolSummary) string {
	var ids []string
	if id := firstCapabilityLoopSignal(loop.SupersedesEvidenceID); id != "" {
		ids = append(ids, id)
	}
	for _, id := range loop.SupersedesEvidenceIDs {
		if id = firstCapabilityLoopSignal(id); id != "" && !capabilityLoopContainsID(ids, id) {
			ids = append(ids, id)
		}
	}
	return strings.Join(ids, ",")
}

func capabilityLoopContainsID(ids []string, id string) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

// firstCapabilityLoopSignal picks the first value carrying real sub-agent signal,
// deferring the placeholder set to capabilityloop.IsPlaceholder — the one such set
// for the protocol (was TODO-110). The private copy this replaced agreed with the
// protocol value-for-value, so nothing displayed changes; what goes away is the
// copy itself. Keeping it meant the next placeholder added protocol-side would
// not reach this surface, which is exactly how the TODO-101 divergence arose in
// internal/goal and internal/agentruntime.
func firstCapabilityLoopSignal(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !capabilityloop.IsPlaceholder(value) {
			return value
		}
	}
	return ""
}

func capabilityLoopSummaryFromPayload(payload map[string]any) string {
	value, ok := payload["capability_loop"]
	if !ok || value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var loop capabilityLoopToolSummary
	if err := json.Unmarshal(data, &loop); err != nil {
		return ""
	}
	return formatCapabilityLoopToolSummary(loop)
}
