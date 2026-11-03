package agenttasks

import (
	"encoding/json"
	"strings"
)

type CapabilityLoop struct {
	Evidence     []string `json:"evidence,omitempty"`
	Assumptions  []string `json:"assumptions,omitempty"`
	Unknowns     []string `json:"unknowns,omitempty"`
	Verification []string `json:"verification,omitempty"`
	Risks        []string `json:"risks,omitempty"`
	NextAction   string   `json:"next_action,omitempty"`
}

type CancelledResultOptions struct {
	Source     string
	Reason     string
	Content    string
	TraceID    string
	DurationMS int64
}

func CancelledResultJSON(opts CancelledResultOptions) string {
	content := strings.TrimSpace(opts.Content)
	if content == "" {
		content = "Cancelled: agent task cancelled"
	}
	payload := map[string]any{
		"cancelled":       true,
		"status":          StatusCancelled,
		"content":         content,
		"capability_loop": CancelledCapabilityLoop(content),
	}
	if source := strings.TrimSpace(opts.Source); source != "" {
		payload["source"] = source
	}
	if reason := strings.TrimSpace(opts.Reason); reason != "" {
		payload["reason"] = reason
	}
	if traceID := strings.TrimSpace(opts.TraceID); traceID != "" {
		payload["trace_id"] = traceID
	}
	if opts.DurationMS > 0 {
		payload["duration_ms"] = opts.DurationMS
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return `{"cancelled":true,"status":"cancelled"}`
	}
	return string(data)
}

func CancelledCapabilityLoop(content string) CapabilityLoop {
	content = strings.TrimSpace(content)
	evidence := "Cancellation recorded before completion; no partial output was captured."
	if content != "" && !strings.EqualFold(content, "Cancelled: agent task cancelled") {
		evidence = "Partial agent task content before cancelled: " + trimCapabilityLoopText(content, 200)
	}
	return CapabilityLoop{
		Evidence:     []string{evidence},
		Assumptions:  []string{"Cancelled agent task may not have reached a complete final answer."},
		Unknowns:     []string{"Additional findings may exist in task events, transcript, or output file."},
		Verification: []string{"Inspect task events, transcript, or output file before relying on cancelled findings."},
		Risks:        []string{"Cancelled result may be incomplete or stale."},
		NextAction:   "Parent agent should preserve any partial evidence and decide whether to retry, ask for clarification, or answer with the remaining limitation.",
	}
}

func trimCapabilityLoopText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
