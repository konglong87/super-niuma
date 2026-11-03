package goal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

func TestEvidenceFromToolTracesClassifiesCommandTypes(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	evidence := EvidenceFromToolTraces("goal_test", []ToolEvidenceTrace{{
		ID:     "toolu_test",
		Name:   "Bash",
		Input:  `{"command":"go test ./internal/goal -count=1"}`,
		Output: "ok",
	}, {
		ID:      "toolu_git",
		Name:    "Bash",
		Input:   `{"command":"git diff --check"}`,
		Output:  "whitespace error",
		IsError: true,
	}, {
		ID:     "toolu_api",
		Name:   "Bash",
		Input:  `{"command":"curl -sS http://127.0.0.1:8080/health"}`,
		Output: `{"ok":true}`,
	}}, now)
	if len(evidence) != 3 {
		t.Fatalf("evidence = %+v", evidence)
	}
	if evidence[0].Type != EvidenceTypeTest || evidence[0].Command != "go test ./internal/goal -count=1" || !evidence[0].Passed {
		t.Fatalf("test evidence = %+v", evidence[0])
	}
	if evidence[1].Type != EvidenceTypeGit || evidence[1].Passed || evidence[1].ID != "ev_toolu_git" {
		t.Fatalf("git evidence = %+v", evidence[1])
	}
	if evidence[2].Type != EvidenceTypeAPI {
		t.Fatalf("api evidence = %+v", evidence[2])
	}
}

func TestEvidenceFromToolTracesKeepsFailedToolOutput(t *testing.T) {
	evidence := EvidenceFromToolTraces("goal_test", []ToolEvidenceTrace{{
		ID:      "toolu_fail",
		Name:    "Bash",
		Input:   `{"command":"go test ./..."}`,
		Output:  "FAIL package",
		IsError: true,
	}}, time.Time{})
	if len(evidence) != 1 || evidence[0].Passed {
		t.Fatalf("evidence = %+v", evidence)
	}
	if !strings.Contains(string(evidence[0].Payload), "FAIL package") {
		t.Fatalf("payload = %s", evidence[0].Payload)
	}
}

func TestEvidenceFromToolTracesExtractsAgentGetPartialCapabilityLoop(t *testing.T) {
	evidence := EvidenceFromToolTraces("goal_test", []ToolEvidenceTrace{{
		ID:    "toolu_agent_get",
		Name:  "AgentGet",
		Input: `{"task_id":42}`,
		Output: `{
			"task":{"id":42,"status":"failed"},
			"result":{
				"status":"failed",
				"capability_loop":{
					"evidence":["internal/query/query.go preserved partial evidence"],
					"unknowns":["provider error stopped final verification"],
					"verification":["go test ./internal/query -count=1 was not reached"],
					"risks":["partial result may be stale"],
					"next_action":"retry focused verification or report limitation"
				}
			}
		}`,
	}}, time.Time{})
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	item := evidence[0]
	if item.Passed {
		t.Fatalf("failed AgentGet partial evidence should not pass: %+v", item)
	}
	for _, want := range []string{
		"AgentGet failed partial evidence",
		"internal/query/query.go preserved partial evidence",
		"risk: partial result may be stale",
		"next: retry focused verification or report limitation",
	} {
		if !strings.Contains(item.Summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, item.Summary)
		}
	}
	payload := string(item.Payload)
	for _, want := range []string{`"evidence_source":"agent_get"`, `"agent_status":"failed"`, `"partial_evidence":true`, `"capability_loop"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
}

func TestEvidenceFromToolTracesSkipsCapabilityLoopPlaceholders(t *testing.T) {
	evidence := EvidenceFromToolTraces("goal_test", []ToolEvidenceTrace{{
		ID:    "toolu_agent_get_placeholders",
		Name:  "AgentGet",
		Input: `{"task_id":43}`,
		Output: `{
			"task":{"id":43,"status":"completed"},
			"result":{
				"status":"completed",
				"capability_loop":{
					"evidence":["None observed","internal/goal/evidence.go keeps concrete evidence"],
					"assumptions":["None observed"],
					"unknowns":["None observed"],
					"verification":["N/A"],
					"risks":["not applicable"],
					"next_action":"inspect Goal prompt dump"
				}
			}
		}`,
	}}, time.Time{})
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	item := evidence[0]
	if !strings.Contains(item.Summary, "internal/goal/evidence.go keeps concrete evidence") || !strings.Contains(item.Summary, "next: inspect Goal prompt dump") {
		t.Fatalf("summary missing concrete fields:\n%s", item.Summary)
	}
	for _, notWant := range []string{"None observed", "unknown:", "risk:"} {
		if strings.Contains(item.Summary, notWant) {
			t.Fatalf("summary contains placeholder %q:\n%s", notWant, item.Summary)
		}
	}
	var payload struct {
		CapabilityLoop agentCapabilityLoop `json:"capability_loop"`
	}
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload.CapabilityLoop; len(got.Evidence) != 1 || got.Evidence[0] != "internal/goal/evidence.go keeps concrete evidence" || got.NextAction != "inspect Goal prompt dump" {
		t.Fatalf("normalized capability_loop = %+v", got)
	}
	if got := payload.CapabilityLoop; len(got.Assumptions) != 0 || len(got.Unknowns) != 0 || len(got.Verification) != 0 || len(got.Risks) != 0 {
		t.Fatalf("placeholder fields were retained: %+v", got)
	}
}

func TestEvidenceFromToolTracesConsumesCancelledResultJSON(t *testing.T) {
	result := agenttasks.CancelledResultJSON(agenttasks.CancelledResultOptions{
		Source:  "api",
		Content: "GOAL_CANCELLED_PARTIAL_EVIDENCE: child found partial root cause",
	})
	output, err := json.Marshal(map[string]any{
		"task":   map[string]any{"id": 7, "status": agenttasks.StatusCancelled},
		"result": json.RawMessage(result),
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := EvidenceFromToolTraces("goal_test", []ToolEvidenceTrace{{
		ID:     "toolu_cancelled_agent_get",
		Name:   "AgentGet",
		Input:  `{"task_id":7}`,
		Output: string(output),
	}}, time.Time{})
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v output=%s", evidence, output)
	}
	item := evidence[0]
	if item.Passed || !strings.Contains(item.Summary, "AgentGet cancelled partial evidence") || !strings.Contains(item.Summary, "GOAL_CANCELLED_PARTIAL_EVIDENCE") {
		t.Fatalf("evidence item = %+v", item)
	}
	payload := string(item.Payload)
	for _, want := range []string{`"evidence_source":"agent_get"`, `"agent_status":"cancelled"`, `"partial_evidence":true`, `"capability_loop"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
}
