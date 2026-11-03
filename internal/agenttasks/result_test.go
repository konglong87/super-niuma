package agenttasks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCancelledResultJSONIncludesCapabilityLoop(t *testing.T) {
	raw := CancelledResultJSON(CancelledResultOptions{
		Source:     "api",
		Reason:     "user stopped noisy sub-agent",
		Content:    "GO_AGENT_CANCELLED_PARTIAL_EVIDENCE: found risky migration",
		TraceID:    "trace-1",
		DurationMS: 42,
	})
	var decoded struct {
		Cancelled      bool           `json:"cancelled"`
		Status         string         `json:"status"`
		Content        string         `json:"content"`
		Source         string         `json:"source"`
		Reason         string         `json:"reason"`
		TraceID        string         `json:"trace_id"`
		DurationMS     int64          `json:"duration_ms"`
		CapabilityLoop CapabilityLoop `json:"capability_loop"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("invalid json: %v body=%s", err, raw)
	}
	if !decoded.Cancelled || decoded.Status != StatusCancelled || decoded.Source != "api" || decoded.Reason == "" || decoded.TraceID != "trace-1" || decoded.DurationMS != 42 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Content == "" || len(decoded.CapabilityLoop.Evidence) == 0 || !strings.Contains(decoded.CapabilityLoop.Evidence[0], "GO_AGENT_CANCELLED_PARTIAL_EVIDENCE") {
		t.Fatalf("capability loop = %+v content=%q", decoded.CapabilityLoop, decoded.Content)
	}
	if decoded.CapabilityLoop.NextAction == "" || len(decoded.CapabilityLoop.Unknowns) == 0 || len(decoded.CapabilityLoop.Verification) == 0 || len(decoded.CapabilityLoop.Risks) == 0 {
		t.Fatalf("incomplete capability loop = %+v", decoded.CapabilityLoop)
	}
}

func TestCancelledResultJSONWithoutPartialStillRequiresVerification(t *testing.T) {
	raw := CancelledResultJSON(CancelledResultOptions{Source: "tenant_service"})
	if !strings.Contains(raw, `"capability_loop"`) || !strings.Contains(raw, "no partial output was captured") || !strings.Contains(raw, "Inspect task events") {
		t.Fatalf("raw = %s", raw)
	}
}
