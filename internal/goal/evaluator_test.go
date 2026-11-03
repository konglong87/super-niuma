package goal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEvaluatorCompletesFromModelSignal(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "all done\nGOAL_STATUS: complete",
	}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorCompletesFromMarkdownWrappedProtocolLine(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "Goal marker found.\n**GOAL_STATUS: complete**\n**GOAL_SMOKE_DONE**",
	}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorCompletesFromMarkdownProtocolLineWithTrailingMarker(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "`goal_complete.txt` matched.\n**GOAL_STATUS: complete** — **GOAL_SMOKE_DONE**",
	}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorIgnoresIncidentalStatusText(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "The API payload may contain status: complete, but the work is not done.",
	}, nil)
	if decision.Status != DecisionContinue {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorIgnoresInlineMarkdownProtocolText(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "The final answer should include **GOAL_STATUS: complete** only after verification.",
	}, nil)
	if decision.Status != DecisionContinue {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorBlocksFromExplicitProtocolLine(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "Need user input.\nGOAL_STATUS: blocked\nblocker_key: missing_api_key",
	}, nil)
	if decision.Status != DecisionBlocked || decision.BlockerKey != "missing_api_key" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorBlocksFromMarkdownWrappedProtocolLine(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "- **GOAL_STATUS:** blocked\n- **blocker_key:** missing_api_key",
	}, nil)
	if decision.Status != DecisionBlocked || decision.BlockerKey != "missing_api_key" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorAllowsCompleteOnFinalBudgetTurn(t *testing.T) {
	decision := DeterministicEvaluator{}.Evaluate(context.Background(), Goal{TurnBudget: 1, TurnsUsed: 1}, TurnResult{
		Response: "GOAL_STATUS: complete",
	}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvaluatorBlocksAfterRepeatedSoftFailure(t *testing.T) {
	goal := Goal{LastBlocker: "temporary failure", RepeatedBlockerCount: 2}
	decision := DeterministicEvaluator{BlockedThreshold: 3}.Evaluate(context.Background(), goal, TurnResult{
		Error: errors.New("temporary failure"),
	}, nil)
	if decision.Status != DecisionBlocked || decision.BlockerKey != "temporary failure" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestApplyDecisionKeepsActiveForFirstSoftFailure(t *testing.T) {
	goal := Goal{Status: StatusActive}
	updated := ApplyDecision(goal, Decision{Status: DecisionContinue, BlockerKey: "test failed", Reason: "test failed"})
	if updated.Status != StatusActive || updated.LastBlocker != "test failed" || updated.RepeatedBlockerCount != 1 {
		t.Fatalf("updated = %+v", updated)
	}
}

type fakeGoalClassifier struct {
	prompt string
	output string
	err    error
	calls  int
}

func (f *fakeGoalClassifier) ClassifyGoal(ctx context.Context, goal Goal, prompt string) (string, error) {
	_ = ctx
	_ = goal
	f.calls++
	f.prompt = prompt
	return f.output, f.err
}

func TestModelClassifiedEvaluatorClassifiesAmbiguousContinue(t *testing.T) {
	classifier := &fakeGoalClassifier{output: `{"status":"complete","reason":"all required work is done","confidence":0.91}`}
	evaluator := ModelClassifiedEvaluator{Classifier: classifier}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_1", Objective: "ship feature", Status: StatusActive}, TurnResult{
		Response: "Implemented the feature and tests pass.",
	}, nil)
	if decision.Status != DecisionComplete || decision.Reason != "all required work is done" || decision.Confidence != 0.91 {
		t.Fatalf("decision = %+v", decision)
	}
	if classifier.calls != 1 || !strings.Contains(classifier.prompt, "Classify the latest Goal Mode turn") || !strings.Contains(classifier.prompt, "ship feature") {
		t.Fatalf("classifier calls=%d prompt=%s", classifier.calls, classifier.prompt)
	}
}

func TestModelClassifiedEvaluatorSkipsExplicitProtocolSignal(t *testing.T) {
	classifier := &fakeGoalClassifier{output: `{"status":"continue"}`}
	evaluator := ModelClassifiedEvaluator{Classifier: classifier}
	decision := evaluator.Evaluate(context.Background(), Goal{}, TurnResult{
		Response: "GOAL_STATUS: complete",
	}, nil)
	if decision.Status != DecisionComplete || classifier.calls != 0 {
		t.Fatalf("decision=%+v calls=%d", decision, classifier.calls)
	}
}

func TestModelClassifiedEvaluatorFallsBackOnInvalidJSON(t *testing.T) {
	classifier := &fakeGoalClassifier{output: `not json`}
	evaluator := ModelClassifiedEvaluator{Classifier: classifier}
	decision := evaluator.Evaluate(context.Background(), Goal{}, TurnResult{Response: "still working"}, nil)
	if decision.Status != DecisionContinue || decision.Reason != "goal still active" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestModelClassifiedEvaluatorSkipsClassifierOnClosingBudget(t *testing.T) {
	classifier := &fakeGoalClassifier{output: `{"status":"complete"}`}
	evaluator := ModelClassifiedEvaluator{Classifier: classifier}
	decision := evaluator.Evaluate(context.Background(), Goal{TurnBudget: 2, TurnsUsed: 1, TokenBudget: 1000}, TurnResult{Response: "still working"}, nil)
	if decision.Status != DecisionContinue || classifier.calls != 0 {
		t.Fatalf("decision=%+v calls=%d", decision, classifier.calls)
	}
}

func TestEvidenceEvaluatorRejectsCompleteWhenRequiredCriteriaPending(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Plan: GoalPlan{
			GoalID: "goal_test",
			AcceptanceCriteria: []GoalCriterion{{
				ID:          "crit_tests",
				Description: "tests pass",
				Required:    true,
				Status:      CriterionStatusPending,
			}},
		},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{Response: "GOAL_STATUS: complete"}, nil)
	if decision.Status != DecisionContinue || !strings.Contains(decision.Reason, "required acceptance criteria") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvidenceEvaluatorCompletesWhenRequiredCriteriaPassed(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Plan: GoalPlan{
			GoalID: "goal_test",
			AcceptanceCriteria: []GoalCriterion{{
				ID:          "crit_tests",
				Description: "tests pass",
				Required:    true,
				Status:      CriterionStatusPassed,
			}},
		},
		Evidence: []GoalEvidence{{
			ID:      "ev_tests",
			GoalID:  "goal_test",
			Type:    EvidenceTypeTest,
			Summary: "tests passed",
			Passed:  true,
		}},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvidenceEvaluatorKeepsActiveWhenEvidenceFailed(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Plan: GoalPlan{
			GoalID: "goal_test",
			AcceptanceCriteria: []GoalCriterion{{
				ID:          "crit_tests",
				Description: "tests pass",
				Required:    true,
				Status:      CriterionStatusPassed,
			}},
		},
		Evidence: []GoalEvidence{{
			ID:      "ev_tests",
			GoalID:  "goal_test",
			Type:    EvidenceTypeTest,
			Summary: "tests failed",
			Passed:  false,
		}},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{}, nil)
	if decision.Status != DecisionContinue || !strings.Contains(decision.Reason, "failures") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvidenceEvaluatorRejectsCompleteWithPendingCapabilityFollowUp(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Evidence: []GoalEvidence{capabilityFollowUpEvidence(t, "goal_test", map[string]any{
			"evidence":      []string{"child found root cause"},
			"unknowns":      []string{"GOAL_EVAL_UNKNOWN: still unresolved"},
			"verification":  []string{"GOAL_EVAL_VERIFICATION: rerun focused acceptance"},
			"risks":         []string{"GOAL_EVAL_RISK: completion would be premature"},
			"next_action":   "GOAL_EVAL_NEXT_ACTION: inspect child evidence before completing",
			"evidence_only": []string{"ignored"},
		})},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{Response: "GOAL_STATUS: complete"}, nil)
	if decision.Status != DecisionContinue ||
		!strings.Contains(decision.Reason, "pending capability follow-up") ||
		decision.NextAction != "GOAL_EVAL_NEXT_ACTION: inspect child evidence before completing" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvidenceEvaluatorRejectsEvidenceFirstCompleteWithPendingCapabilityFollowUp(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Plan: GoalPlan{
			GoalID: "goal_test",
			AcceptanceCriteria: []GoalCriterion{{
				ID:          "crit_tests",
				Description: "tests pass",
				Required:    true,
				Status:      CriterionStatusPassed,
			}},
		},
		Evidence: []GoalEvidence{
			{
				ID:      "ev_tests",
				GoalID:  "goal_test",
				Type:    EvidenceTypeTest,
				Summary: "tests passed",
				Passed:  true,
			},
			capabilityFollowUpEvidence(t, "goal_test", map[string]any{
				"evidence":     []string{"child found root cause"},
				"verification": []string{"GOAL_EVAL_VERIFICATION: rerun focused acceptance"},
			}),
		},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{}, nil)
	if decision.Status != DecisionContinue || decision.NextAction != "GOAL_EVAL_VERIFICATION: rerun focused acceptance" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvidenceEvaluatorAllowsCompleteWhenCapabilityFollowUpResolved(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Evidence: []GoalEvidence{
			capabilityFollowUpEvidence(t, "goal_test", map[string]any{
				"evidence":     []string{"child found root cause"},
				"verification": []string{"GOAL_EVAL_VERIFICATION: rerun focused acceptance"},
				"next_action":  "GOAL_EVAL_NEXT_ACTION: inspect child evidence before completing",
			}),
			capabilityFollowUpResolutionEvidence(t, "goal_test", "ev_capability_follow_up", true),
		},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{Response: "GOAL_STATUS: complete"}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestEvidenceEvaluatorRejectsCompleteWhenCapabilityFollowUpResolutionIsWeak(t *testing.T) {
	for name, resolver := range map[string]GoalEvidence{
		"failed_resolution": capabilityFollowUpResolutionEvidence(t, "goal_test", "ev_capability_follow_up", false),
		"missing_proof": {
			ID:      "ev_capability_resolution_weak",
			GoalID:  "goal_test",
			Type:    EvidenceTypeManual,
			Summary: "claimed resolution without proof",
			Passed:  true,
			Payload: mustJSON(t, map[string]any{
				"resolved_follow_up":     "claimed handled",
				"supersedes_evidence_id": "ev_capability_follow_up",
				"capability_loop":        map[string]any{},
			}),
		},
		"unrelated_resolution": capabilityFollowUpResolutionEvidence(t, "goal_test", "ev_other_follow_up", true),
	} {
		t.Run(name, func(t *testing.T) {
			evaluator := EvidenceEvaluator{
				Evidence: []GoalEvidence{
					capabilityFollowUpEvidence(t, "goal_test", map[string]any{
						"verification": []string{"GOAL_EVAL_VERIFICATION: rerun focused acceptance"},
					}),
					resolver,
				},
			}
			decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{Response: "GOAL_STATUS: complete"}, nil)
			if decision.Status != DecisionContinue || decision.NextAction != "GOAL_EVAL_VERIFICATION: rerun focused acceptance" {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}

func TestEvidenceEvaluatorIgnoresPlaceholderCapabilityFollowUp(t *testing.T) {
	evaluator := EvidenceEvaluator{
		Evidence: []GoalEvidence{capabilityFollowUpEvidence(t, "goal_test", map[string]any{
			"evidence":     []string{"concrete evidence only"},
			"unknowns":     []string{"None observed"},
			"verification": []string{"N/A"},
			"risks":        []string{"not applicable"},
			"next_action":  "None observed",
		})},
	}
	decision := evaluator.Evaluate(context.Background(), Goal{ID: "goal_test"}, TurnResult{Response: "GOAL_STATUS: complete"}, nil)
	if decision.Status != DecisionComplete {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestParseClassificationDecisionNormalizesFencedJSON(t *testing.T) {
	decision, err := ParseClassificationDecision("```json\n{\"status\":\"blocked\",\"blocker_key\":\" Missing API Key \",\"confidence\":2}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != DecisionBlocked || decision.BlockerKey != "missing api key" || decision.Confidence != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}

func capabilityFollowUpEvidence(t *testing.T, goalID string, loop map[string]any) GoalEvidence {
	t.Helper()
	payload := mustJSON(t, map[string]any{
		"evidence_source":  "terminal_agent_task_store",
		"agent_task_id":    77,
		"agent_status":     "failed",
		"partial_evidence": true,
		"capability_loop":  loop,
	})
	return GoalEvidence{
		ID:      "ev_capability_follow_up",
		GoalID:  goalID,
		Type:    EvidenceTypeManual,
		Summary: "TaskStore capability evidence",
		Passed:  true,
		Payload: payload,
	}
}

func capabilityFollowUpResolutionEvidence(t *testing.T, goalID string, supersedesID string, passed bool) GoalEvidence {
	t.Helper()
	return GoalEvidence{
		ID:      "ev_capability_follow_up_resolution",
		GoalID:  goalID,
		Type:    EvidenceTypeManual,
		Summary: "resolved capability follow-up with verification",
		Passed:  passed,
		Payload: mustJSON(t, map[string]any{
			"evidence_source":        "manual_resolution",
			"resolved_follow_up":     "handled pending capability follow-up",
			"supersedes_evidence_id": supersedesID,
			"capability_loop": map[string]any{
				"evidence":     []string{"GOAL_EVAL_RESOLUTION_EVIDENCE: inspected child output"},
				"verification": []string{"GOAL_EVAL_RESOLUTION_VERIFICATION: focused acceptance passed"},
			},
		}),
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
