package goal

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/capabilityloop"
)

type ToolEvidenceTrace struct {
	ID      string
	Name    string
	Input   string
	Output  string
	IsError bool
}

func EvidenceFromToolTraces(goalID string, traces []ToolEvidenceTrace, now time.Time) []GoalEvidence {
	goalID = strings.TrimSpace(goalID)
	if goalID == "" || len(traces) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	out := make([]GoalEvidence, 0, len(traces))
	for i, trace := range traces {
		evidence := evidenceFromToolTrace(goalID, trace, i, now)
		if err := evidence.Validate(); err != nil {
			continue
		}
		out = append(out, evidence)
	}
	return out
}

func evidenceFromToolTrace(goalID string, trace ToolEvidenceTrace, index int, now time.Time) GoalEvidence {
	command := traceCommand(trace)
	summary := evidenceSummary(trace)
	passed := !trace.IsError
	payloadMap := map[string]any{
		"tool_id":      strings.TrimSpace(trace.ID),
		"tool_name":    strings.TrimSpace(trace.Name),
		"input":        trimEvidenceText(trace.Input, 4000),
		"output":       trimEvidenceText(trace.Output, 4000),
		"output_bytes": len(trace.Output),
	}
	if agentEvidence, ok := agentGetCapabilityEvidence(trace); ok {
		summary = agentEvidence.Summary
		passed = agentEvidence.Passed
		payloadMap["evidence_source"] = agentEvidence.Source
		payloadMap["agent_task_id"] = agentEvidence.TaskID
		payloadMap["agent_status"] = agentEvidence.Status
		payloadMap["capability_loop"] = agentEvidence.CapabilityLoop
		payloadMap["partial_evidence"] = agentEvidence.Partial
	}
	payload, _ := json.Marshal(payloadMap)
	return GoalEvidence{
		ID:        evidenceID(trace, index),
		GoalID:    goalID,
		Type:      evidenceTypeForTrace(trace),
		Summary:   summary,
		Command:   command,
		Passed:    passed,
		Payload:   payload,
		CreatedAt: now,
	}
}

type agentCapabilityTrace struct {
	TaskID         any                 `json:"task_id,omitempty"`
	Status         string              `json:"status,omitempty"`
	Source         string              `json:"source,omitempty"`
	CapabilityLoop agentCapabilityLoop `json:"capability_loop"`
	Summary        string              `json:"summary"`
	Passed         bool                `json:"passed"`
	Partial        bool                `json:"partial"`
}

type agentCapabilityLoop struct {
	Evidence     []string `json:"evidence,omitempty"`
	Assumptions  []string `json:"assumptions,omitempty"`
	Unknowns     []string `json:"unknowns,omitempty"`
	Verification []string `json:"verification,omitempty"`
	Risks        []string `json:"risks,omitempty"`
	NextAction   string   `json:"next_action,omitempty"`
}

func agentGetCapabilityEvidence(trace ToolEvidenceTrace) (agentCapabilityTrace, bool) {
	if !strings.EqualFold(strings.TrimSpace(trace.Name), "AgentGet") {
		return agentCapabilityTrace{}, false
	}
	var decoded struct {
		Task struct {
			ID     any    `json:"id"`
			Status string `json:"status"`
		} `json:"task"`
		Result struct {
			Status         string              `json:"status"`
			CapabilityLoop agentCapabilityLoop `json:"capability_loop"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(trace.Output), &decoded); err != nil {
		return agentCapabilityTrace{}, false
	}
	loop := decoded.Result.CapabilityLoop.normalized()
	if !loop.hasSignal() {
		return agentCapabilityTrace{}, false
	}
	status := strings.TrimSpace(firstNonEmptyEvidence(decoded.Result.Status, decoded.Task.Status))
	if status == "" {
		status = "unknown"
	}
	statusLower := strings.ToLower(status)
	partial := statusLower == "failed" || statusLower == "cancelled" || statusLower == "canceled"
	passed := !trace.IsError && !partial
	source := agentCapabilityEvidenceSource(trace)
	prefix := "AgentGet capability evidence"
	if source == "terminal_agent_task_store" {
		prefix = "TaskStore capability evidence"
	}
	if partial {
		prefix = "AgentGet " + statusLower + " partial evidence"
		if source == "terminal_agent_task_store" {
			prefix = "TaskStore " + statusLower + " partial evidence"
		}
	}
	parts := []string{prefix}
	if evidence := firstActionableCapabilityText(loop.Evidence...); evidence != "" {
		parts = append(parts, "evidence: "+trimEvidenceText(evidence, 120))
	}
	if risk := firstActionableCapabilityText(loop.Risks...); risk != "" {
		parts = append(parts, "risk: "+trimEvidenceText(risk, 100))
	}
	if unknown := firstActionableCapabilityText(loop.Unknowns...); unknown != "" {
		parts = append(parts, "unknown: "+trimEvidenceText(unknown, 100))
	}
	if next := actionableCapabilityText(loop.NextAction); next != "" {
		parts = append(parts, "next: "+trimEvidenceText(next, 120))
	}
	return agentCapabilityTrace{
		TaskID:         decoded.Task.ID,
		Status:         status,
		Source:         source,
		CapabilityLoop: loop,
		Summary:        strings.Join(parts, " | "),
		Passed:         passed,
		Partial:        partial,
	}, true
}

func agentCapabilityEvidenceSource(trace ToolEvidenceTrace) string {
	input := strings.TrimSpace(trace.Input)
	if input != "" {
		var payload map[string]any
		if err := json.Unmarshal([]byte(input), &payload); err == nil {
			if source, ok := payload["source"].(string); ok && strings.TrimSpace(source) != "" {
				return strings.TrimSpace(source)
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(trace.Name), "AgentGet") {
		return "agent_get"
	}
	if name := strings.TrimSpace(trace.Name); name != "" {
		return strings.ToLower(name)
	}
	return "unknown"
}

func (loop agentCapabilityLoop) hasSignal() bool {
	loop = loop.normalized()
	return len(loop.Evidence) > 0 ||
		len(loop.Assumptions) > 0 ||
		len(loop.Unknowns) > 0 ||
		len(loop.Verification) > 0 ||
		len(loop.Risks) > 0 ||
		loop.NextAction != ""
}

func (loop agentCapabilityLoop) normalized() agentCapabilityLoop {
	return agentCapabilityLoop{
		Evidence:     actionableCapabilityValues(loop.Evidence),
		Assumptions:  actionableCapabilityValues(loop.Assumptions),
		Unknowns:     actionableCapabilityValues(loop.Unknowns),
		Verification: actionableCapabilityValues(loop.Verification),
		Risks:        actionableCapabilityValues(loop.Risks),
		NextAction:   actionableCapabilityText(loop.NextAction),
	}
}

func actionableCapabilityValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := actionableCapabilityText(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func firstActionableCapabilityText(values ...string) string {
	for _, value := range values {
		if text := actionableCapabilityText(value); text != "" {
			return text
		}
	}
	return ""
}

// actionableCapabilityText drops values that carry no sub-agent signal, and
// defers the placeholder set itself to capabilityloop.IsPlaceholder (was
// TODO-101). It used to keep a private copy of that set which omitted the
// machine-injected "completed" default next_action, so the goal driver published
// that boilerplate as must_handle_next_action while the query driver suppressed
// it — the same sub-agent output yielded different obligations depending on
// which driver happened to be running it.
func actionableCapabilityText(value string) string {
	value = strings.TrimSpace(value)
	if capabilityloop.IsPlaceholder(value) {
		return ""
	}
	return value
}

func firstNonEmptyEvidence(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func evidenceID(trace ToolEvidenceTrace, index int) string {
	if strings.TrimSpace(trace.ID) != "" {
		return "ev_" + sanitizeEvidenceID(trace.ID)
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s:%s:%d", trace.Name, trace.Input, index)))
	return "ev_" + hex.EncodeToString(sum[:])[:16]
}

func sanitizeEvidenceID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func evidenceTypeForTrace(trace ToolEvidenceTrace) EvidenceType {
	name := strings.ToLower(strings.TrimSpace(trace.Name))
	command := strings.ToLower(traceCommand(trace))
	switch {
	case name == "bash" || name == "powershell":
		switch {
		case strings.Contains(command, "go test") || strings.Contains(command, "npm test") || strings.Contains(command, "pytest") || strings.Contains(command, "cargo test"):
			return EvidenceTypeTest
		case strings.HasPrefix(command, "git ") || strings.Contains(command, " git "):
			return EvidenceTypeGit
		case strings.Contains(command, "curl ") || strings.Contains(command, "http "):
			return EvidenceTypeAPI
		case strings.Contains(command, "mysql ") || strings.Contains(command, "psql "):
			return EvidenceTypeDB
		default:
			return EvidenceTypeCommand
		}
	case strings.Contains(name, "file") || name == "read" || name == "write" || name == "edit":
		return EvidenceTypeDoc
	case strings.Contains(name, "web") || strings.Contains(name, "http"):
		return EvidenceTypeAPI
	default:
		return EvidenceTypeManual
	}
}

func traceCommand(trace ToolEvidenceTrace) string {
	input := strings.TrimSpace(trace.Input)
	if input == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err == nil {
		for _, key := range []string{"command", "cmd"} {
			if value, ok := payload[key].(string); ok {
				return strings.TrimSpace(value)
			}
		}
	}
	return input
}

func evidenceSummary(trace ToolEvidenceTrace) string {
	name := strings.TrimSpace(trace.Name)
	if name == "" {
		name = "tool"
	}
	status := "passed"
	if trace.IsError {
		status = "failed"
	}
	command := traceCommand(trace)
	if command != "" {
		return fmt.Sprintf("%s %s: %s", name, status, trimEvidenceText(command, 160))
	}
	return fmt.Sprintf("%s %s", name, status)
}

func trimEvidenceText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
