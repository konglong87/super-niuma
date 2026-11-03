package query

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/repair"
	"github.com/konglong87/super-niuma/internal/shellcmd"
	"github.com/konglong87/super-niuma/internal/toolpolicy"
	"github.com/konglong87/super-niuma/internal/tools"
)

type closureLevel string

const (
	closureLevelTrivialAnswer     closureLevel = "L0_trivial_answer"
	closureLevelNoToolAnswer      closureLevel = "L1_no_tool_answer"
	closureLevelReadOnlyScoped    closureLevel = "L2_read_only_scoped"
	closureLevelInvestigation     closureLevel = "L3_investigation_audit"
	closureLevelLocalChange       closureLevel = "L4_local_change"
	closureLevelSharedStateChange closureLevel = "L5_shared_state_change"
)

type gateSeverity string

const (
	gateSeverityAllow         gateSeverity = "allow"
	gateSeverityWarn          gateSeverity = "warn"
	gateSeverityBlockContinue gateSeverity = "block_continue"
	gateSeverityBlockTool     gateSeverity = "block_tool"
	gateSeverityRefuse        gateSeverity = "refuse"
)

type taskContract struct {
	TaskType              runtimeTaskType `json:"TaskType,omitempty"`
	ClosureLevel          closureLevel
	RequiredReadPath      []string
	SharedStateOperations []sharedStateOperation
	Reasons               []classificationReason
	Confidence            classificationConfidence
}

type classificationConfidence string

const (
	classificationConfidenceHigh   classificationConfidence = "high"
	classificationConfidenceMedium classificationConfidence = "medium"
	classificationConfidenceLow    classificationConfidence = "low"
)

type classificationReason struct {
	RuleID   string `json:"rule_id"`
	Matched  string `json:"matched,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

type taskClassifierInput struct {
	Raw        string
	Normalized string
	Paths      []string
	TaskType   runtimeTaskType
}

type taskClassifierMatch struct {
	ClosureLevel          closureLevel
	RequiredReadPath      []string
	SharedStateOperations []sharedStateOperation
	Reasons               []classificationReason
	Confidence            classificationConfidence
}

type taskClassifierRule struct {
	ID       string
	Priority int
	Match    func(taskClassifierInput) (taskClassifierMatch, bool)
}

type preToolGateInput struct {
	Contract taskContract
	Prompt   string
	Calls    []ToolTrace
	ToolName string
	Input    string
	Command  string
	Intent   shellIntent
	Git      gitpolicy.Analysis
}

type preToolGateRule struct {
	ID       string
	Priority int
	Check    func(preToolGateInput) (completionGateResult, bool)
}

type completionGateInput struct {
	Contract  taskContract
	Calls     []ToolTrace
	FinalText string
}

type completionGateRule struct {
	ID       string
	Priority int
	Check    func(completionGateInput) (completionGateResult, bool)
}

type sharedStateOperation string

const (
	sharedStateCommit  sharedStateOperation = "commit"
	sharedStatePush    sharedStateOperation = "push"
	sharedStateTag     sharedStateOperation = "tag"
	sharedStateRelease sharedStateOperation = "release"
	sharedStateDeploy  sharedStateOperation = "deploy"
)

type completionGateResult struct {
	Severity         gateSeverity
	Reminder         string
	RuleID           string
	MissingEvidence  []string
	PreflightCommand string
	PreflightTitle   string
	PreflightSummary string
}

type readEvidence struct {
	Path    string
	Source  string
	Partial bool
}

type actionRecord struct {
	ToolName string   `json:"tool_name"`
	Kind     string   `json:"kind"`
	Paths    []string `json:"paths,omitempty"`
	Success  bool     `json:"success"`
}

type evidenceRecord struct {
	SourceKind string `json:"source_kind"`
	ToolName   string `json:"tool_name"`
	Path       string `json:"path,omitempty"`
	Partial    bool   `json:"partial,omitempty"`
}

type deltaRecord struct {
	Path        string `json:"path,omitempty"`
	ToolName    string `json:"tool_name"`
	ChangeKind  string `json:"change_kind"`
	ModeChanged bool   `json:"mode_changed,omitempty"`
	Verified    bool   `json:"verified"`
}

func buildTaskContract(prompt string) taskContract {
	input := taskClassifierInput{
		Raw:        prompt,
		Normalized: normalizeRuntimeTaskPrompt(prompt),
		Paths:      extractRequestedReadPaths(prompt),
		TaskType:   classifyRuntimeTaskPrompt(prompt).Type,
	}
	for _, rule := range taskClassifierRules {
		if match, ok := rule.Match(input); ok {
			return taskContract{
				TaskType:              input.TaskType,
				ClosureLevel:          match.ClosureLevel,
				RequiredReadPath:      match.RequiredReadPath,
				SharedStateOperations: match.SharedStateOperations,
				Reasons:               match.Reasons,
				Confidence:            match.Confidence,
			}
		}
	}
	return defaultNoToolTaskContract()
}

var taskClassifierRules = []taskClassifierRule{
	{
		ID:       "empty_prompt",
		Priority: 1000,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			if input.Normalized != "" {
				return taskClassifierMatch{}, false
			}
			return taskClassifierMatch{
				ClosureLevel: closureLevelNoToolAnswer,
				Reasons: []classificationReason{{
					RuleID:   "empty_prompt",
					Detail:   "prompt normalizes to empty text",
					Priority: 1000,
				}},
				Confidence: classificationConfidenceHigh,
			}, true
		},
	},
	{
		ID:       "trivial_arithmetic",
		Priority: 950,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			if !isTrivialAnswerPrompt(input.Normalized) {
				return taskClassifierMatch{}, false
			}
			return taskClassifierMatch{
				ClosureLevel: closureLevelTrivialAnswer,
				Reasons: []classificationReason{{
					RuleID:   "trivial_arithmetic",
					Matched:  input.Normalized,
					Detail:   "prompt matches simple arithmetic expression",
					Priority: 950,
				}},
				Confidence: classificationConfidenceHigh,
			}, true
		},
	},
	{
		ID:       "read_only_scoped",
		Priority: 900,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			if !looksLikeReadOnlyScopedPrompt(input.Normalized) {
				return taskClassifierMatch{}, false
			}
			return taskClassifierMatch{
				ClosureLevel:     closureLevelReadOnlyScoped,
				RequiredReadPath: append([]string(nil), input.Paths...),
				Reasons: []classificationReason{{
					RuleID:   "read_only_scoped",
					Matched:  strings.Join(input.Paths, ","),
					Detail:   "read/summarize/explain/compare intent with local path",
					Priority: 900,
				}},
				Confidence: classificationConfidenceHigh,
			}, true
		},
	},
	{
		ID:       "runtime_task_investigation",
		Priority: 800,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			switch input.TaskType {
			case runtimeTaskRepoHealthAudit, runtimeTaskReleaseReadinessAudit, runtimeTaskEntrypointAudit, runtimeTaskCodeReview:
				return taskClassifierMatch{
					ClosureLevel: closureLevelInvestigation,
					Reasons: []classificationReason{{
						RuleID:   string(input.TaskType),
						Detail:   "runtime task classifier selected investigation/audit strategy",
						Priority: runtimeTaskReasonPriority(input.TaskType),
					}},
					Confidence: classificationConfidenceHigh,
				}, true
			default:
				return taskClassifierMatch{}, false
			}
		},
	},
	{
		ID:       "runtime_task_local_change",
		Priority: 700,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			switch input.TaskType {
			case runtimeTaskTargetedRepair, runtimeTaskReleaseRepair, runtimeTaskEntrypointRepair, runtimeTaskMetadataRepair, runtimeTaskCodeChange:
				return taskClassifierMatch{
					ClosureLevel: closureLevelLocalChange,
					Reasons: []classificationReason{{
						RuleID:   string(input.TaskType),
						Detail:   "runtime task classifier selected local change strategy",
						Priority: runtimeTaskReasonPriority(input.TaskType),
					}},
					Confidence: classificationConfidenceHigh,
				}, true
			default:
				return taskClassifierMatch{}, false
			}
		},
	},
	{
		ID:       "shared_state_request",
		Priority: 600,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			if !looksLikeSharedStatePrompt(input.Normalized) {
				return taskClassifierMatch{}, false
			}
			ops := extractSharedStateOperations(input.Normalized)
			return taskClassifierMatch{
				ClosureLevel:          closureLevelSharedStateChange,
				SharedStateOperations: ops,
				Reasons: []classificationReason{{
					RuleID:   "shared_state_request",
					Matched:  sharedStateOperationsString(ops),
					Detail:   "prompt mentions commit/push/tag/deploy/publish style shared-state operation",
					Priority: 600,
				}},
				Confidence: classificationConfidenceHigh,
			}, true
		},
	},
	{
		ID:       "default_no_tool",
		Priority: 0,
		Match: func(input taskClassifierInput) (taskClassifierMatch, bool) {
			return taskClassifierMatch{
				ClosureLevel: closureLevelNoToolAnswer,
				Reasons: []classificationReason{{
					RuleID:   "default_no_tool",
					Detail:   "no task classifier rule matched",
					Priority: 0,
				}},
				Confidence: classificationConfidenceLow,
			}, true
		},
	},
}

func defaultNoToolTaskContract() taskContract {
	return taskContract{
		ClosureLevel: closureLevelNoToolAnswer,
		Reasons: []classificationReason{{
			RuleID:   "default_no_tool",
			Detail:   "no task classifier rule matched",
			Priority: 0,
		}},
		Confidence: classificationConfidenceLow,
	}
}

func runtimeTaskReasonPriority(taskType runtimeTaskType) int {
	switch taskType {
	case runtimeTaskReleaseReadinessAudit:
		return 800
	case runtimeTaskEntrypointAudit:
		return 790
	case runtimeTaskRepoHealthAudit:
		return 780
	case runtimeTaskCodeReview:
		return 760
	case runtimeTaskReleaseRepair:
		return 700
	case runtimeTaskEntrypointRepair:
		return 690
	case runtimeTaskMetadataRepair:
		return 680
	case runtimeTaskTargetedRepair:
		return 670
	case runtimeTaskCodeChange:
		return 650
	default:
		return 0
	}
}

func sharedStateOperationsString(ops []sharedStateOperation) string {
	if len(ops) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, string(op))
	}
	return strings.Join(parts, ",")
}

func completionGate(contract taskContract, calls []ToolTrace, finalText string) completionGateResult {
	if strings.TrimSpace(finalText) == "" {
		return completionGateResult{Severity: gateSeverityAllow}
	}
	gateInput := completionGateInput{
		Contract:  contract,
		Calls:     calls,
		FinalText: finalText,
	}
	var warning completionGateResult
	for _, rule := range completionGateRules {
		if result, blocked := rule.Check(gateInput); blocked {
			result.RuleID = rule.ID
			if result.Severity == gateSeverityWarn {
				if warning.Severity == "" {
					warning = result
				}
				continue
			}
			return result
		}
	}
	if warning.Severity != "" {
		return warning
	}
	return completionGateResult{Severity: gateSeverityAllow}
}

var completionGateRules = []completionGateRule{
	{
		ID:       "read_scope",
		Priority: 300,
		Check: func(input completionGateInput) (completionGateResult, bool) {
			return readScopeGateResult(input.Contract, input.Calls, input.FinalText)
		},
	},
	{
		ID:       "failed_audit_evidence",
		Priority: 250,
		Check: func(input completionGateInput) (completionGateResult, bool) {
			return failedAuditEvidenceGateResult(input.Contract, input.Calls, input.FinalText)
		},
	},
	{
		ID:       "repair_semantic_evidence",
		Priority: 225,
		Check: func(input completionGateInput) (completionGateResult, bool) {
			return repairCompletionGateResult(input.Contract, input.Calls, input.FinalText)
		},
	},
	{
		ID:       "post_action_delta",
		Priority: 200,
		Check: func(input completionGateInput) (completionGateResult, bool) {
			return deltaClosureGateResult(input.Contract, input.Calls, input.FinalText)
		},
	},
	{
		ID:       "final_claim",
		Priority: 100,
		Check: func(input completionGateInput) (completionGateResult, bool) {
			return finalClaimGateResult(input.Calls, input.FinalText)
		},
	},
}

func preToolClosureGate(contract taskContract, prompt string, calls []ToolTrace, toolName, input string) completionGateResult {
	return preToolClosureGateWithPolicy(contract, prompt, calls, toolName, input, "", gitpolicy.ParseAuthorization(prompt))
}

func preToolClosureGateWithPolicy(contract taskContract, prompt string, calls []ToolTrace, toolName, input, cwd string, authorization gitpolicy.Authorization) completionGateResult {
	if decision := toolpolicy.Check(toolName, []byte(input), cwd, authorization); decision != nil {
		return completionGateResult{
			Severity: gateSeverityBlockTool,
			RuleID:   decision.RuleID,
			Reminder: "<system-reminder>" + decision.Message + "</system-reminder>",
		}
	}
	if result, matched := repairPreActionDefinitionGateResult(contract, calls, toolName, input); matched {
		result.RuleID = "repair_definition_deletion_evidence"
		return result
	}
	if !strings.EqualFold(toolName, "Bash") {
		return completionGateResult{Severity: gateSeverityAllow}
	}
	command := bashCommandFromInput(input)
	gateInput := preToolGateInput{
		Contract: contract,
		Prompt:   prompt,
		Calls:    calls,
		ToolName: toolName,
		Input:    input,
		Command:  command,
		Intent:   classifyShellIntent(command),
		Git:      gitpolicy.Analyze(command),
	}
	var warning completionGateResult
	for _, rule := range preToolGateRules {
		if result, blocked := rule.Check(gateInput); blocked {
			result.RuleID = rule.ID
			if result.Severity == gateSeverityWarn {
				if warning.Severity == "" {
					warning = result
				}
				continue
			}
			return result
		}
	}
	if warning.Severity != "" {
		return warning
	}
	return completionGateResult{Severity: gateSeverityAllow}
}

func repairPreActionDefinitionGateResult(contract taskContract, calls []ToolTrace, toolName, input string) (completionGateResult, bool) {
	if !repairEvidenceGateApplies(contract) || !strings.EqualFold(toolName, "Edit") && !strings.EqualFold(toolName, "MultiEdit") {
		return completionGateResult{}, false
	}
	path, deletions := replacementDefinitionDeletions(input)
	if path == "" || len(deletions) == 0 {
		return completionGateResult{}, false
	}
	var missing []string
	if !fullFileReadObserved(calls, path) {
		missing = append(missing, "full affected-unit read: "+path)
	}
	for _, deletion := range deletions {
		if !referenceSearchObserved(calls, deletion.Name) {
			missing = append(missing, "reference search: "+deletion.Name)
		}
	}
	if len(missing) == 0 {
		return completionGateResult{}, false
	}
	mode := repair.CurrentEnforcementMode()
	if !mode.Warns() {
		return completionGateResult{}, false
	}
	severity := gateSeverityWarn
	if mode.Enforces() {
		severity = gateSeverityBlockTool
	}
	return completionGateResult{
		Severity:        severity,
		Reminder:        "<system-reminder>Definition deletion needs more impact evidence: read the full affected file and search every deleted symbol's references before editing. A partial Read or unrelated grep is insufficient.</system-reminder>",
		MissingEvidence: missing,
	}, true
}

func replacementDefinitionDeletions(input string) (string, []repair.DeletedDefinition) {
	var params struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Edits     []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", nil
	}
	seen := map[string]repair.DeletedDefinition{}
	collect := func(oldText, newText string) {
		for _, deletion := range repair.AnalyzeReplacement(oldText, newText) {
			seen[deletion.Name] = deletion
		}
	}
	collect(params.OldString, params.NewString)
	for _, edit := range params.Edits {
		collect(edit.OldString, edit.NewString)
	}
	deletions := make([]repair.DeletedDefinition, 0, len(seen))
	for _, deletion := range seen {
		deletions = append(deletions, deletion)
	}
	sort.Slice(deletions, func(i, j int) bool { return deletions[i].Name < deletions[j].Name })
	return normalizeEvidencePath(params.FilePath), deletions
}

func fullFileReadObserved(calls []ToolTrace, required string) bool {
	required = normalizeEvidencePath(required)
	for _, call := range calls {
		if call.IsError || !strings.EqualFold(call.Name, "Read") || readInputIsPartial(call.Input) {
			continue
		}
		path := normalizeEvidencePath(toolInputString(call.Input, "file_path"))
		if evidencePathMatches(path, required) && !strings.Contains(strings.ToLower(call.Output), "truncated") {
			return true
		}
	}
	return false
}

func referenceSearchObserved(calls []ToolTrace, symbol string) bool {
	for _, call := range calls {
		if call.IsError {
			continue
		}
		switch strings.ToLower(call.Name) {
		case "grep":
			pattern := firstNonEmpty(toolInputString(call.Input, "pattern"), toolInputString(call.Input, "query"))
			if strings.Contains(pattern, symbol) {
				return true
			}
		case "bash":
			command := bashCommandFromTrace(call)
			lower := strings.ToLower(command)
			if (strings.Contains(lower, "rg ") || strings.Contains(lower, "grep ") || strings.Contains(lower, "git grep")) && strings.Contains(command, symbol) {
				return true
			}
		}
	}
	return false
}

func evidencePathMatches(observed, required string) bool {
	return observed == required || strings.HasSuffix(observed, "/"+required) || strings.HasSuffix(required, "/"+observed)
}

var preToolGateRules = []preToolGateRule{
	{
		ID:       "pre_commit_scope",
		Priority: 500,
		Check: func(input preToolGateInput) (completionGateResult, bool) {
			if !input.Git.Has(gitpolicy.OperationCommit) || preCommitScopeVerified(input.Calls) {
				return completionGateResult{}, false
			}
			result := completionGateResult{
				Severity: gateSeverityBlockTool,
				Reminder: preCommitScopeGateReminder,
			}
			if canAutoPreflightGitCommand(input.Command, "commit") {
				result.PreflightCommand = preCommitScopePreflightCommand
				result.PreflightTitle = "提交前范围检查"
				result.PreflightSummary = "已完成提交前检查，原提交命令尚未执行"
			}
			return result, true
		},
	},
	{
		ID:       "repair_pre_commit_evidence",
		Priority: 450,
		Check: func(input preToolGateInput) (completionGateResult, bool) {
			if !input.Git.Has(gitpolicy.OperationCommit) {
				return completionGateResult{}, false
			}
			return repairPreCommitGateResult(input.Contract, input.Calls)
		},
	},
	{
		ID:       "shared_state_git_push",
		Priority: 300,
		Check: func(input preToolGateInput) (completionGateResult, bool) {
			if !input.Git.Has(gitpolicy.OperationPush) {
				return completionGateResult{}, false
			}
			if prePushGitVerified(input.Calls) {
				return completionGateResult{}, false
			}
			result := completionGateResult{
				Severity: gateSeverityBlockTool,
				Reminder: "<system-reminder>Tool blocked by Shared-State Git Gate: git push requires current branch, object-state, and remote/upstream evidence first. Run git status --short --branch, git rev-parse HEAD, and a read-only remote/upstream check such as git rev-parse @{u}, git branch -vv, or git ls-remote before retrying the shared-state operation.</system-reminder>",
			}
			if canAutoPreflightGitCommand(input.Command, "push") {
				result.PreflightCommand = "git status --short --branch && git rev-parse HEAD && (git rev-parse @{u} || git branch -vv)"
				result.PreflightTitle = "推送前状态检查"
				result.PreflightSummary = "已完成推送前检查，原推送命令尚未执行"
			}
			return result, true
		},
	},
	{
		ID:       "shared_state_git_tag",
		Priority: 200,
		Check: func(input preToolGateInput) (completionGateResult, bool) {
			if !input.Git.Has(gitpolicy.OperationTag) || preTagGitVerified(input.Calls) {
				return completionGateResult{}, false
			}
			result := completionGateResult{
				Severity: gateSeverityBlockTool,
				Reminder: "<system-reminder>Tool blocked by Shared-State Git Gate: git tag mutation requires current branch/object evidence plus existing local/remote tag-state evidence first. Run git status --short --branch, git rev-parse HEAD, and a read-only tag check such as git tag --list, git show-ref --tags, git rev-parse refs/tags/<tag>, or git ls-remote --tags before retrying.</system-reminder>",
			}
			if canAutoPreflightGitCommand(input.Command, "tag") {
				result.PreflightCommand = "git status --short --branch && git rev-parse HEAD && (git tag --list || git show-ref --tags)"
				result.PreflightTitle = "标签操作前状态检查"
				result.PreflightSummary = "已完成标签操作前检查，原标签命令尚未执行"
			}
			return result, true
		},
	},
	{
		ID:       "external_action_authorization",
		Priority: 100,
		Check: func(input preToolGateInput) (completionGateResult, bool) {
			if input.Intent.Kind != shellIntentExternalWrite || promptAuthorizesExternalAction(input.Prompt, input.Command) {
				return completionGateResult{}, false
			}
			return completionGateResult{
				Severity: gateSeverityBlockTool,
				Reminder: "<system-reminder>Tool blocked by External Action Gate: this command appears to deploy, publish, send, or mutate an external system, but the current user request did not explicitly authorize that external side effect. Ask for the target/scope or provide a dry-run/preview instead.</system-reminder>",
			}, true
		},
	},
}

const preCommitScopePreflightCommand = "git status --short --branch && git diff --name-status && git diff --cached --name-status"

const preCommitScopeGateReminder = "<system-reminder>Tool blocked by Pre-Commit Scope Gate: git commit requires a completed current-session scope verification after the latest Edit/Write/write-like Bash/git add. Run this exact verification as a separate Bash tool call first: " + preCommitScopePreflightCommand + ". Inspect the output and confirm the staged scope matches the user's request. Do not chain this verification with git commit in the same Bash command; retry git commit only after the verification tool call succeeds.</system-reminder>"

func repairCompletionGateResult(contract taskContract, calls []ToolTrace, finalText string) (completionGateResult, bool) {
	if !repairEvidenceGateApplies(contract) || !finalTextClaimsCompletion(finalText) && !finalTextClaimsFixed(finalText) {
		return completionGateResult{}, false
	}
	ledger := repairEvidenceLedger(calls)
	if ledger.ContentEpoch == 0 || ledger.HasCurrentPassingEvidence() {
		return completionGateResult{}, false
	}
	return repairEvidenceGateResult(ledger, "completion")
}

func repairPreCommitGateResult(contract taskContract, calls []ToolTrace) (completionGateResult, bool) {
	if !repairEvidenceGateApplies(contract) {
		return completionGateResult{}, false
	}
	ledger := repairEvidenceLedger(calls)
	if ledger.ContentEpoch == 0 || ledger.HasCurrentPassingEvidence() {
		return completionGateResult{}, false
	}
	return repairEvidenceGateResult(ledger, "git commit")
}

func repairEvidenceGateResult(ledger repair.Ledger, action string) (completionGateResult, bool) {
	mode := repair.CurrentEnforcementMode()
	if !mode.Warns() {
		return completionGateResult{}, false
	}
	severity := gateSeverityWarn
	if mode.Enforces() {
		if action == "git commit" {
			severity = gateSeverityBlockTool
		} else {
			severity = gateSeverityBlockContinue
		}
	}
	missing := []string{fmt.Sprintf("current repair evidence at content_epoch=%d (assertion, behavioral test, or paired probe; plain grep/rg is observation only)", ledger.ContentEpoch)}
	return completionGateResult{
		Severity:        severity,
		Reminder:        "<system-reminder>Repair verification is incomplete before " + action + ": run a focused assertion or behavioral test against the current content. Plain grep/rg output can locate references but cannot independently prove repaired behavior.</system-reminder>",
		MissingEvidence: missing,
	}, true
}

func repairEvidenceGateApplies(contract taskContract) bool {
	if contract.Confidence != classificationConfidenceHigh {
		return false
	}
	switch contract.TaskType {
	case runtimeTaskTargetedRepair, runtimeTaskReleaseRepair, runtimeTaskEntrypointRepair, runtimeTaskMetadataRepair:
		return true
	default:
		return false
	}
}

func repairEvidenceLedger(calls []ToolTrace) repair.Ledger {
	observations := make([]repair.Observation, 0, len(calls))
	for i := range calls {
		call := calls[i]
		observation := repair.Observation{ContentChange: traceCreatesContentDelta(call)}
		if strings.EqualFold(call.Name, "Bash") {
			if call.verification != nil {
				copy := *call.verification
				observation.Verification = &copy
			} else {
				exitCode := 0
				if call.IsError {
					exitCode = 1
				}
				inspected := repair.InspectShellCommand(bashCommandFromTrace(call), exitCode)
				observation.Verification = &inspected
			}
		}
		observations = append(observations, observation)
	}
	return repair.BuildLedger(observations)
}

func traceCreatesContentDelta(call ToolTrace) bool {
	if len(call.FileChanges) > 0 {
		return true
	}
	if call.IsError {
		return false
	}
	switch strings.ToLower(call.Name) {
	case "edit", "multiedit", "write", "notebookedit":
		return true
	case "bash":
		if traceStagesChanges(call) {
			return false
		}
		intent := classifyShellIntent(bashCommandFromTrace(call))
		return intent.Kind == shellIntentWorkspaceWrite || intent.Kind == shellIntentUnknownRisky
	default:
		return false
	}
}

// canAutoPreflightGitCommand reports whether the runtime can safely run the
// read-only preflight for a blocked git command on the model's behalf. It
// accepts a single `git <sub>` command or a git-only pipeline of staging and
// shared-state operations (optionally prefixed with cd), e.g.
// `cd repo && git add f && git commit -m m && git push`. It deliberately
// refuses a pipeline that chains a read-only verification with the commit
// (those keep their "do not chain" teaching block) or that mixes in any
// non-git command, since the preflight only re-runs read-only checks and then
// asks the model to retry the original command.
func canAutoPreflightGitCommand(command, gitSubcommand string) bool {
	script, err := shellcmd.Parse(command)
	if err != nil || len(script.Commands) == 0 {
		return false
	}
	targetFound := false
	containsAdd := false
	for _, command := range script.Commands {
		if command.Name == "cd" {
			continue
		}
		if command.Name != "git" {
			return false
		}
		sub, _, ok := gitpolicy.SplitSubcommand(command.Args)
		if !ok {
			return false
		}
		if !autoPreflightSafeGitSubcommand(sub) {
			return false
		}
		containsAdd = containsAdd || sub == "add"
		if sub == gitSubcommand {
			targetFound = true
		}
	}
	if gitSubcommand == "commit" && containsAdd {
		return false
	}
	return targetFound
}

// autoPreflightSafeGitSubcommand lists the git subcommands allowed inside an
// auto-preflightable pipeline: staging and shared-state mutations only.
// Read-only git verifications (status/diff/log/rev-parse/…) are excluded so a
// chained "verify && commit" keeps its dedicated "do not chain" teaching block.
func autoPreflightSafeGitSubcommand(sub string) bool {
	switch sub {
	case "add", "commit", "push", "tag":
		return true
	default:
		return false
	}
}

func readScopeGateResult(contract taskContract, calls []ToolTrace, finalText string) (completionGateResult, bool) {
	if contract.ClosureLevel != closureLevelReadOnlyScoped {
		return completionGateResult{}, false
	}
	if !finalTextClaimsReadSummary(finalText) {
		return completionGateResult{}, false
	}
	reads := collectReadEvidence(calls)
	if len(contract.RequiredReadPath) == 0 {
		if len(reads) == 0 {
			missing := []string{"local read evidence: Read/Grep/Glob/LS or read-only Bash"}
			return completionGateResult{
				Severity:        gateSeverityBlockContinue,
				Reminder:        "<system-reminder>Completion is blocked: the user asked for a read-only summary/explanation/comparison, but no Read/Grep/Glob/LS or read-only Bash evidence is present in this turn. Read the requested scope first, then answer only from observed evidence.</system-reminder>",
				MissingEvidence: missing,
			}, true
		}
		return completionGateResult{}, false
	}
	var missing []string
	var partial []string
	for _, required := range contract.RequiredReadPath {
		evidence, ok := matchingReadEvidence(reads, required)
		if !ok {
			missing = append(missing, required)
			continue
		}
		if evidence.Partial && finalTextClaimsFullScope(finalText) {
			partial = append(partial, required)
		}
	}
	if len(missing) == 0 && len(partial) == 0 {
		return completionGateResult{}, false
	}
	var parts []string
	var missingEvidence []string
	if len(missing) > 0 {
		parts = append(parts, "missing required read evidence for: "+strings.Join(missing, ", "))
		for _, path := range missing {
			missingEvidence = append(missingEvidence, "required read evidence: "+path)
		}
	}
	if len(partial) > 0 {
		parts = append(parts, "only partial read evidence is present for: "+strings.Join(partial, ", ")+"; disclose the read range or continue reading before claiming full-file coverage")
		for _, path := range partial {
			missingEvidence = append(missingEvidence, "full-scope read evidence: "+path)
		}
	}
	return completionGateResult{
		Severity:        gateSeverityBlockContinue,
		Reminder:        "<system-reminder>Completion is blocked by Read Scope Gate: " + strings.Join(parts, "; ") + ". Use Read/Grep/Glob/LS or a read-only Bash command to gather the required local evidence, then produce a bounded final answer.</system-reminder>",
		MissingEvidence: missingEvidence,
	}, true
}

func failedAuditEvidenceGateResult(contract taskContract, calls []ToolTrace, finalText string) (completionGateResult, bool) {
	if !failedAuditGateApplies(contract, finalText) || finalTextDisclosesFailedAuditBoundary(finalText) {
		return completionGateResult{}, false
	}
	failedIndex, failedCommand := lastFailedBroadAuditCall(calls)
	if failedIndex < 0 || successfulBroadAuditObservedAfter(calls, failedIndex) {
		return completionGateResult{}, false
	}
	failedSummary := compactOneLine(firstNonEmpty(failedCommand, "broad read/search audit command"), 180)
	missing := "successful broad read/search audit after failed command: " + failedSummary
	return completionGateResult{
		Severity: gateSeverityBlockContinue,
		Reminder: "<system-reminder>Completion is blocked by Failed Audit Evidence Gate: a broad read/search audit command failed, but the final answer presents whole-scope or comparative conclusions. " +
			"Run an equivalent broad read-only audit successfully, or revise the final answer to explicitly disclose the failed command and bound every conclusion to the successful scoped evidence.</system-reminder>",
		MissingEvidence: []string{missing},
	}, true
}

func finalClaimGateResult(calls []ToolTrace, finalText string) (completionGateResult, bool) {
	var blockers []string
	if finalTextClaimsSearched(finalText) && !searchEvidenceObserved(calls) {
		blockers = append(blockers, "search/inspection is claimed but no Grep/Glob/LS/Read or read-only Bash evidence was observed")
	}
	if finalTextClaimsFullRepositoryScope(finalText) && !broadSearchEvidenceObserved(calls) {
		blockers = append(blockers, "full-repository/all-files scope is claimed but no broad local search/list evidence was observed")
	}
	if finalTextClaimsTested(finalText) && !successfulTestEvidenceObserved(calls) {
		blockers = append(blockers, "tests/checks are claimed but no successful test or verification Bash command was observed")
	}
	if finalTextClaimsCommitted(finalText) && !successfulBashCommandObserved(calls, func(command string) bool {
		return strings.Contains(command, "git commit")
	}) {
		blockers = append(blockers, "commit is claimed but no successful git commit Bash command was observed")
	}
	if finalTextClaimsCommitted(finalText) && !postCommitVerificationObserved(calls) {
		blockers = append(blockers, "commit is claimed but no post-commit git status/log verification was observed")
	}
	if finalTextClaimsPushed(finalText) && !successfulBashCommandObserved(calls, func(command string) bool {
		return strings.Contains(command, "git push")
	}) {
		blockers = append(blockers, "push is claimed but no successful git push Bash command was observed")
	}
	if finalTextClaimsPushed(finalText) && !postPushVerificationObserved(calls) {
		blockers = append(blockers, "push is claimed but no post-push remote/branch verification was observed")
	}
	if finalTextClaimsTagged(finalText) && !successfulBashCommandObserved(calls, looksLikeMutatingGitTagCommand) {
		blockers = append(blockers, "tag creation/update is claimed but no successful mutating git tag command was observed")
	}
	if finalTextClaimsTagged(finalText) && !postTagVerificationObserved(calls) {
		blockers = append(blockers, "tag creation/update is claimed but no post-tag object verification was observed")
	}
	if finalTextClaimsExternalAction(finalText) && !successfulExternalActionObserved(calls) {
		blockers = append(blockers, "external deploy/publish/send is claimed but no successful matching tool action was observed")
	}
	if finalTextClaimsReadEvidence(finalText) && len(collectReadEvidence(calls)) == 0 {
		blockers = append(blockers, "local read/search is claimed but no Read/Grep/Glob/LS or read-only Bash evidence was observed")
	}
	if finalTextClaimsNoIssuesFound(finalText) && len(collectReadEvidence(calls)) == 0 {
		blockers = append(blockers, "no-issues-found is claimed but no local read/search evidence was observed")
	}
	if len(blockers) == 0 {
		return completionGateResult{}, false
	}
	return completionGateResult{
		Severity:        gateSeverityBlockContinue,
		Reminder:        "<system-reminder>Completion is blocked by Final Claim Gate: " + strings.Join(blockers, "; ") + ". Continue with the missing evidence, or revise the final answer to clearly state the unverified boundary instead of claiming completion.</system-reminder>",
		MissingEvidence: blockers,
	}, true
}

func failedAuditGateApplies(contract taskContract, finalText string) bool {
	return contract.ClosureLevel == closureLevelInvestigation ||
		finalTextClaimsAuditSynthesis(finalText) ||
		finalTextClaimsFullRepositoryScope(finalText)
}

func lastFailedBroadAuditCall(calls []ToolTrace) (int, string) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if !call.IsError || !failedBroadAuditCall(call) {
			continue
		}
		if strings.EqualFold(call.Name, "Bash") {
			return i, bashCommandFromTrace(call)
		}
		return i, call.Name
	}
	return -1, ""
}

func failedBroadAuditCall(call ToolTrace) bool {
	switch strings.ToLower(call.Name) {
	case "bash":
		return commandLooksLikeBroadReadAudit(bashCommandFromTrace(call))
	case "grep", "glob", "ls":
		return searchToolCallLooksBroad(call)
	default:
		return false
	}
}

func successfulBroadAuditObservedAfter(calls []ToolTrace, index int) bool {
	for i := index + 1; i < len(calls); i++ {
		call := calls[i]
		if call.IsError {
			continue
		}
		switch strings.ToLower(call.Name) {
		case "bash":
			if commandLooksLikeBroadReadAudit(bashCommandFromTrace(call)) {
				return true
			}
		case "grep", "glob", "ls":
			if searchToolCallLooksBroad(call) {
				return true
			}
		}
	}
	return false
}

func commandLooksLikeBroadReadAudit(command string) bool {
	lower := strings.ToLower(command)
	if strings.Contains(lower, "for dir in ") && (strings.Contains(lower, " find ") || strings.Contains(lower, "find \"$dir\"") || strings.Contains(lower, "find $dir")) {
		return true
	}
	if strings.Contains(lower, "find .") || strings.Contains(lower, "find \"./\"") || strings.Contains(lower, "find './'") {
		return true
	}
	if strings.Contains(lower, "git grep") {
		return true
	}
	if strings.Contains(lower, "rg ") && (strings.Contains(lower, " --files") || strings.Contains(lower, " -n ") || strings.Contains(lower, " -l ") || strings.Contains(lower, " --count")) {
		return true
	}
	return false
}

func searchToolCallLooksBroad(call ToolTrace) bool {
	path := normalizeEvidencePath(firstNonEmpty(
		toolInputString(call.Input, "path"),
		toolInputString(call.Input, "file_path"),
		toolInputString(call.Input, "pattern"),
	))
	return path == "" || path == "." || strings.HasPrefix(path, "**/")
}

func finalTextClaimsAuditSynthesis(finalText string) bool {
	lower := strings.ToLower(finalText)
	for _, token := range []string{
		"data is complete",
		"full audit",
		"overall",
		"entire repo",
		"only directory",
		"数据摆完",
		"直接说结论",
		"最缺",
		"唯一",
		"整个",
		"全局",
		"所有",
		"六大",
		"对比",
		"审计",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func finalTextDisclosesFailedAuditBoundary(finalText string) bool {
	lower := strings.ToLower(finalText)
	hasFailure := false
	for _, token := range []string{"failed", "failure", "unverified", "incomplete", "partial", "失败", "未成功", "无法验证", "未验证", "不完整", "局部"} {
		if strings.Contains(lower, token) {
			hasFailure = true
			break
		}
	}
	if !hasFailure {
		return false
	}
	for _, token := range []string{"audit", "search", "command", "tool", "evidence", "统计", "审计", "搜索", "命令", "工具", "证据", "结论"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func compactOneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}

func deltaClosureGateResult(contract taskContract, calls []ToolTrace, finalText string) (completionGateResult, bool) {
	if !finalTextClaimsCompletion(finalText) && !finalTextClaimsFixed(finalText) {
		return completionGateResult{}, false
	}
	lastDelta := lastDeltaIndex(calls)
	if lastDelta < 0 {
		return completionGateResult{}, false
	}
	scope, metadata, semantic := postDeltaChecksObserved(calls, lastDelta)
	var missing []string
	if !scope {
		missing = append(missing, "scope check: git status --short and git diff --name-status")
	}
	if !metadata {
		missing = append(missing, "metadata check: git diff --summary")
	}
	if !semantic {
		missing = append(missing, "semantic check: focused test or deterministic read-only command for the changed invariant")
	}
	if len(missing) == 0 {
		return completionGateResult{}, false
	}
	_ = contract
	return completionGateResult{
		Severity:        gateSeverityBlockContinue,
		Reminder:        "<system-reminder>Completion is blocked by Post-Action Delta Gate: files or shared state changed in this turn, but these post-change checks are still missing after the latest change: " + strings.Join(missing, "; ") + ". Run the missing checks, inspect outputs, fix any problems, and only then claim completion.</system-reminder>",
		MissingEvidence: missing,
	}, true
}

func collectReadEvidence(calls []ToolTrace) []readEvidence {
	var out []readEvidence
	for _, call := range calls {
		if call.IsError {
			continue
		}
		switch strings.ToLower(call.Name) {
		case "read":
			if path := toolInputString(call.Input, "file_path"); path != "" {
				out = append(out, readEvidence{Path: path, Source: "read", Partial: readInputIsPartial(call.Input)})
			}
		case "grep", "glob", "ls":
			if path := firstNonEmpty(
				toolInputString(call.Input, "path"),
				toolInputString(call.Input, "file_path"),
				toolInputString(call.Input, "pattern"),
			); path != "" {
				out = append(out, readEvidence{Path: path, Source: "search"})
			} else {
				out = append(out, readEvidence{Path: ".", Source: "search"})
			}
		case "bash":
			command := bashCommandFromTrace(call)
			if classifyShellIntent(command).ReadOnly {
				out = append(out, readEvidence{Path: readOnlyBashEvidencePath(command), Source: "bash_readonly", Partial: bashReadEvidenceIsPartial(command)})
			}
		}
	}
	return out
}

func closureActionRecord(trace ToolTrace) actionRecord {
	return actionRecord{
		ToolName: trace.Name,
		Kind:     actionKindForTrace(trace),
		Paths:    pathsForTrace(trace),
		Success:  !trace.IsError,
	}
}

func closureEvidenceRecords(trace ToolTrace) []evidenceRecord {
	var out []evidenceRecord
	for _, evidence := range collectReadEvidence([]ToolTrace{trace}) {
		out = append(out, evidenceRecord{
			SourceKind: "local_read",
			ToolName:   trace.Name,
			Path:       evidence.Path,
			Partial:    evidence.Partial,
		})
	}
	if strings.EqualFold(trace.Name, "Bash") && !trace.IsError && looksLikeVerificationCommand(json.RawMessage(fmt.Sprintf(`{"command":%q}`, bashCommandFromTrace(trace)))) {
		out = append(out, evidenceRecord{SourceKind: "test_result", ToolName: trace.Name})
	}
	return out
}

func closureDeltaRecords(trace ToolTrace, verified bool) []deltaRecord {
	var out []deltaRecord
	for _, change := range trace.FileChanges {
		out = append(out, deltaRecord{
			Path:        displayChangePath(change.Path),
			ToolName:    trace.Name,
			ChangeKind:  fileChangeKind(change),
			ModeChanged: change.ModeChanged,
			Verified:    verified,
		})
	}
	if len(out) == 0 && strings.EqualFold(trace.Name, "Bash") && shellIntentMayNeedPostActionVerification(bashCommandFromTrace(trace)) {
		out = append(out, deltaRecord{ToolName: trace.Name, ChangeKind: "bash_write_or_shared_state", Verified: verified})
	}
	return out
}

func fileChangeKind(change tools.FileChange) string {
	switch {
	case !change.BeforeExists && change.AfterExists:
		return "create"
	case change.BeforeExists && !change.AfterExists:
		return "delete"
	case change.ModeChanged:
		return "metadata"
	default:
		return "modify"
	}
}

func actionKindForTrace(trace ToolTrace) string {
	switch strings.ToLower(trace.Name) {
	case "read":
		return "read_file"
	case "grep", "glob", "ls":
		return "search_local"
	case "edit", "multiedit", "write", "notebookedit":
		return "write_file"
	case "bash":
		command := bashCommandFromTrace(trace)
		intent := classifyShellIntent(command)
		switch {
		case intent.Kind == shellIntentGitCommit:
			return "git_commit"
		case intent.Kind == shellIntentGitPush || intent.Kind == shellIntentGitTagMutate || intent.Kind == shellIntentGitDestructiveSharedState:
			return "git_push"
		case intent.Kind == shellIntentExternalWrite:
			return "external_write"
		case intent.Kind == shellIntentWorkspaceWrite:
			return "bash_write"
		case intent.Kind == shellIntentVerification:
			return "test_verify"
		case intent.ReadOnly:
			return "bash_readonly"
		case intent.Kind == shellIntentUnknownRisky:
			return "bash_unknown_risky"
		default:
			return "bash_unknown"
		}
	default:
		return strings.ToLower(trace.Name)
	}
}

func pathsForTrace(trace ToolTrace) []string {
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		path = normalizeEvidencePath(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, change := range trace.FileChanges {
		add(change.Path)
	}
	for _, key := range []string{"file_path", "path", "pattern"} {
		add(toolInputString(trace.Input, key))
	}
	if strings.EqualFold(trace.Name, "Bash") {
		command := bashCommandFromTrace(trace)
		if classifyShellIntent(command).Kind != shellIntentGitCommit {
			for _, match := range requestedPathishRE.FindAllString(command, -1) {
				add(match)
			}
		}
	}
	return paths
}

func matchingReadEvidence(reads []readEvidence, required string) (readEvidence, bool) {
	required = normalizeEvidencePath(required)
	requiredIsDirectory := pathLooksLikeDirectory(required)
	for _, evidence := range reads {
		path := normalizeEvidencePath(evidence.Path)
		if path == required || strings.HasSuffix(path, "/"+required) || strings.HasSuffix(required, "/"+path) {
			return evidence, true
		}
		if requiredIsDirectory && evidence.Source != "read" && (path == "." || strings.HasPrefix(path, required+"/")) {
			return evidence, true
		}
	}
	return readEvidence{}, false
}

func extractRequestedReadPaths(prompt string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, match := range requestedPathishRE.FindAllString(prompt, -1) {
		path := strings.Trim(match, "`'\"，。；;:()[]{}<>")
		if !looksLikeLocalPath(path) {
			continue
		}
		normalized := normalizeEvidencePath(path)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		paths = append(paths, normalized)
	}
	return paths
}

var requestedPathRE = regexp.MustCompile(`(?i)(?:[A-Za-z0-9_.@-]+/)*[A-Za-z0-9_.@-]+\.(?:md|txt|go|js|ts|tsx|jsx|json|yaml|yml|toml|py|rs|java|c|cc|cpp|h|hpp|sh|sql|html|css)`)
var requestedPathishRE = regexp.MustCompile(`(?i)(?:\.?/)?(?:[A-Za-z0-9_.@-]+/)+[A-Za-z0-9_.@-]+/?|(?:[A-Za-z0-9_.@-]+/)*[A-Za-z0-9_.@-]+\.(?:md|txt|go|js|ts|tsx|jsx|json|yaml|yml|toml|py|rs|java|c|cc|cpp|h|hpp|sh|sql|html|css)`)

func looksLikeLocalPath(path string) bool {
	if strings.Contains(path, "://") {
		return false
	}
	return strings.Contains(path, ".") || strings.Contains(path, "/")
}

func pathLooksLikeDirectory(path string) bool {
	if path == "." || strings.HasSuffix(path, "/") {
		return true
	}
	base := filepath.Base(path)
	return !strings.Contains(base, ".")
}

func normalizeEvidencePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`'\"")
	if path == "" {
		return ""
	}
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
}

func toolInputString(input string, key string) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return ""
	}
	value, _ := raw[key].(string)
	return strings.TrimSpace(value)
}

func readInputIsPartial(input string) bool {
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return false
	}
	for _, key := range []string{"offset", "limit", "byte_offset", "byte_limit", "chunk_index", "pages"} {
		if value, ok := raw[key]; ok && fmt.Sprint(value) != "" && fmt.Sprint(value) != "0" {
			return true
		}
	}
	return false
}

func bashCommandFromInput(input string) string {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(input), &params); err == nil && strings.TrimSpace(params.Command) != "" {
		return params.Command
	}
	return input
}

func looksLikeReadOnlyBashCommand(command string) bool {
	return classifyShellIntent(command).ReadOnly
}

func looksLikeReadOnlyBashCommandString(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	for _, prefix := range []string{
		"git status", "git diff", "git log", "git show", "git rev-parse", "git tag", "git ls-remote",
		"rg ", "grep ", "find ", "wc ", "ls ", "cat ", "head ", "tail ",
		"test -", "bash -n",
	} {
		if strings.HasPrefix(lower, prefix) || strings.Contains(lower, "&& "+prefix) || strings.Contains(lower, "; "+prefix) {
			return true
		}
	}
	return false
}

func looksLikeExternalWriteCommand(command string) bool {
	lower := strings.ToLower(command)
	if containsAny(lower, []string{"部署", "发布", "发送"}) {
		return true
	}
	if commandSegmentStartsWith(lower, "deploy") ||
		commandSegmentStartsWith(lower, "publish") ||
		commandSegmentStartsWith(lower, "release") ||
		commandSegmentStartsWith(lower, "send-message") ||
		commandSegmentStartsWith(lower, "send", "message") ||
		commandSegmentStartsWith(lower, "npm", "publish") ||
		commandSegmentStartsWith(lower, "gh", "release") ||
		commandSegmentStartsWith(lower, "kubectl", "apply") ||
		commandSegmentStartsWith(lower, "terraform", "apply") ||
		curlCommandLooksExternalWrite(lower) {
		return true
	}
	return false
}

func commandSegmentStartsWith(command string, want ...string) bool {
	for _, segment := range shellCommandSegments(command) {
		tokens := shellIntentTokens(segment)
		if len(tokens) < len(want) {
			continue
		}
		matched := true
		for i := range want {
			if tokens[i] != want[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func shellCommandSegments(command string) []string {
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	raw := strings.Split(replacer.Replace(command), "\n")
	segments := make([]string, 0, len(raw))
	for _, segment := range raw {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func shellIntentTokens(command string) []string {
	fields := strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '"', '\'', '`', '(', ')', '[', ']', '{', '}', ';', '|', '&':
			return true
		default:
			return false
		}
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

func curlCommandLooksExternalWrite(command string) bool {
	for _, segment := range shellCommandSegments(command) {
		tokens := shellIntentTokens(segment)
		if len(tokens) == 0 || tokens[0] != "curl" {
			continue
		}
		for i, token := range tokens {
			switch token {
			case "-d", "--data", "--data-raw", "--data-binary", "--form":
				return true
			case "-x", "--request":
				if i+1 < len(tokens) && tokens[i+1] == "post" {
					return true
				}
			default:
				if token == "-xpost" {
					return true
				}
			}
		}
	}
	return false
}

func promptAuthorizesExternalAction(prompt, command string) bool {
	text := strings.ToLower(prompt)
	command = strings.ToLower(command)
	for _, marker := range []string{"deploy", "publish", "release", "send", "部署", "发布", "发送"} {
		if strings.Contains(command, marker) && strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func readOnlyBashEvidencePath(command string) string {
	for _, match := range requestedPathishRE.FindAllString(command, -1) {
		return normalizeEvidencePath(match)
	}
	return "."
}

func bashReadEvidenceIsPartial(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "head ") || strings.Contains(lower, "tail ") || strings.Contains(lower, "sed -n")
}

func isTrivialAnswerPrompt(text string) bool {
	if matched, _ := regexp.MatchString(`^\s*\d+\s*[-+*/]\s*\d+\s*=\??\s*$`, text); matched {
		return true
	}
	return false
}

func looksLikeReadOnlyScopedPrompt(text string) bool {
	readIntent := containsAny(text, []string{"读取", "查看", "读一下", "看一下", "总结", "解释", "对比"}) ||
		containsEnglishTaskWord(text, []string{"summarize", "summarise", "summary", "explain", "compare", "read"})
	return readIntent && len(extractRequestedReadPaths(text)) > 0
}

func looksLikeSharedStatePrompt(text string) bool {
	return len(extractSharedStateOperations(text)) > 0
}

func extractSharedStateOperations(text string) []sharedStateOperation {
	var ops []sharedStateOperation
	add := func(op sharedStateOperation) {
		for _, existing := range ops {
			if existing == op {
				return
			}
		}
		ops = append(ops, op)
	}
	for _, operation := range []sharedStateOperation{
		sharedStateCommit,
		sharedStatePush,
		sharedStateTag,
		sharedStateRelease,
		sharedStateDeploy,
	} {
		if promptAuthorizesSharedStateOperation(text, operation) {
			add(operation)
		}
	}
	return ops
}

func promptAuthorizesSharedStateOperation(prompt string, operation sharedStateOperation) bool {
	if operation == sharedStateCommit || operation == sharedStatePush || operation == sharedStateTag {
		want := gitpolicy.Operation(operation)
		for _, scope := range gitpolicy.ParseAuthorization(prompt).Scopes {
			if scope.Operation == want {
				return true
			}
		}
		return false
	}
	for _, clause := range splitAuthorizationClauses(strings.ToLower(prompt)) {
		if !clauseMentionsSharedStateOperation(clause, operation) || sharedStateClauseIsNegatedOrDiscussion(clause) {
			continue
		}
		if sharedStateClauseIsDirective(clause, operation) {
			return true
		}
	}
	return false
}

func splitAuthorizationClauses(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '。', '!', '！', '?', '？', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func clauseMentionsSharedStateOperation(clause string, operation sharedStateOperation) bool {
	english, chinese := sharedStateOperationMarkers(operation)
	return containsEnglishTaskWord(clause, english) || containsAny(clause, chinese)
}

func sharedStateOperationMarkers(operation sharedStateOperation) ([]string, []string) {
	switch operation {
	case sharedStateCommit:
		return []string{"commit"}, []string{"提交"}
	case sharedStatePush:
		return []string{"push"}, []string{"推送"}
	case sharedStateTag:
		return []string{"tag"}, []string{"标签"}
	case sharedStateRelease:
		return []string{"release", "publish"}, []string{"发布", "发版"}
	case sharedStateDeploy:
		return []string{"deploy"}, []string{"部署"}
	default:
		return nil, nil
	}
}

func sharedStateClauseIsNegatedOrDiscussion(clause string) bool {
	return containsAny(clause, []string{
		"不要", "不允许", "没有让", "没让", "未让", "未经", "禁止", "无需", "不用", "别", "不能", "不应", "不该",
		"do not", "don't", "did not", "didn't", "never", "not allowed", "not authorize", "not ask", "without authorization",
		"为什么", "为啥", "怎么会", "什么意思", "解释", "讨论", "是否", "能否", "可否", "有没有", "吗",
		"why", "what does", "explain", "discuss", "did you", "have you", "can you", "could you", "should we", "should i",
	})
}

func sharedStateClauseIsDirective(clause string, operation sharedStateOperation) bool {
	trimmed := strings.TrimSpace(clause)
	english, chinese := sharedStateOperationMarkers(operation)
	for _, marker := range english {
		if containsEnglishTaskWord(trimmed, []string{marker}) && strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	for _, marker := range chinese {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return containsAny(trimmed, []string{
		"请", "帮我", "执行", "允许", "同意", "确认", "继续", "现在", "直接", "然后", "并", "创建", "把", "将", "一下", "可以",
		"please", "go ahead", "execute", "run ", "allow", "authorize", "yes", "now", "then", " and ", "create", "need to",
	})
}

func finalTextClaimsReadSummary(text string) bool {
	return claimedInAffirmativeClause(text, []string{"summary", "summarized", "summarised", "总结", "概述", "解释", "对比", "read", "已读", "读取"})
}

func finalTextClaimsFullScope(text string) bool {
	return claimedInAffirmativeClause(text, []string{"entire file", "whole file", "full file", "full text", "entire directory", "whole directory", "full directory", "全文", "完整文件", "整个文件", "整个目录", "全目录"})
}

func finalTextClaimsTested(text string) bool {
	return claimedInAffirmativeClause(text, []string{"tests passed", "test passed", "tests pass", "all tests pass", "checks passed", "验证通过", "测试通过"})
}

func finalTextClaimsCommitted(text string) bool {
	return claimedInAffirmativeClause(text, []string{"committed", "commit created", "已提交"})
}

func finalTextClaimsPushed(text string) bool {
	return claimedInAffirmativeClause(text, []string{"pushed", "push completed", "已推送"})
}

func finalTextClaimsTagged(text string) bool {
	return claimedInAffirmativeClause(text, []string{"tagged", "tag created", "created tag", "tag updated", "标签已创建", "已创建标签", "tag 已创建"})
}

func finalTextClaimsReadEvidence(text string) bool {
	return claimedInAffirmativeClause(text, []string{"i read", "i inspected", "i searched", "read the file", "inspected the file", "已读取", "已查看", "我读取", "我查看"})
}

func finalTextClaimsNoIssuesFound(text string) bool {
	return claimedInAffirmativeClause(text, []string{"no issues found", "no problems found", "没有发现问题", "未发现问题"})
}

func finalTextClaimsFixed(text string) bool {
	return claimedInAffirmativeClause(text, []string{"fixed", "fix complete", "修复完成", "已修复"})
}

func finalTextClaimsSearched(text string) bool {
	return claimedInAffirmativeClause(text, []string{"searched", "inspected", "looked through", "查找了", "搜索了", "检查了"})
}

func finalTextClaimsFullRepositoryScope(text string) bool {
	return claimedInAffirmativeClause(text, []string{"entire repository", "whole repository", "full repository", "all files", "full repo", "全仓库", "整个仓库", "所有文件"})
}

func finalTextClaimsExternalAction(text string) bool {
	lower := strings.ToLower(text)
	for _, sentence := range splitClaimSentences(lower) {
		if externalActionClaimSentence(sentence) {
			return true
		}
	}
	return false
}

var (
	externalActionEnglishFirstPersonRE = regexp.MustCompile(`\b(i|we|go claude|codex)\s+((have|already|successfully)\s+)?(deployed|published|sent)\b`)
	externalActionEnglishStartRE       = regexp.MustCompile(`^(done[:,]?\s*)?((successfully|already)\s+)?(deployed|published|sent)\b`)
)

func splitClaimSentences(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case '\n', '\r', '.', '!', '?', ';', '。', '！', '？', '；':
			return true
		default:
			return false
		}
	})
}

// claimContrastSplitRE cuts a sentence at a contrastive conjunction so a negation
// governs only its own clause: "I committed the changes but did not push" must
// still count as a commit claim while not counting as a push claim.
//
// Commas are deliberately NOT separators. "I have not committed, pushed, or
// tagged anything" puts one negation in front of a list, and splitting on the
// commas would strand "pushed" and "tagged" in clauses that look affirmative.
var claimContrastSplitRE = regexp.MustCompile(`\bbut\b|\bhowever\b|\balthough\b|\bthough\b|但是|但|不过|然而|虽然|尽管`)

// claimNegationMarkers is the negation-only half of the vocabulary in
// sharedStateClauseIsNegatedOrDiscussion. That function additionally treats
// interrogative and discussion markers ("why", "explain", "是否", "能否") as
// disqualifying, which is correct when classifying what the user asked for but
// wrong here: a final answer is written in the declarative, so "Why this matters:
// tests pass" would be waved through as a question and the gate would stop asking
// for evidence.
// The zero-quantifier forms ("no tag created", "nothing was committed") are as
// much a truthful negative report as "did not commit", and were flagged as claims
// until a test caught them.
var claimNegationMarkers = []string{
	"not ", "n't", "never", "no longer", "cannot", "can not", "unable to",
	"failed to", "skipped", "skipping",
	"no ", "nothing", "none of", "zero ",
	"没有", "未能", "未曾", "尚未", "还没", "并未", "均未", "都没", "无法", "没能", "毫无",
}

// claimedInAffirmativeClause reports whether any phrase appears in a clause that
// is not negated ahead of it.
//
// The claim matchers used to run containsAny over the whole final text, so a
// truthful "I have not committed the changes" matched "committed" and the gate
// demanded commit evidence for a report that nothing was committed — the more
// honest the model, the more likely it was blocked, and the retry burned a turn.
// The negation must appear BEFORE the phrase in the clause: that is what keeps
// "I committed the changes but did not push" a commit claim, and what makes the
// list form above suppress all three claims.
//
// Accepted limitation: a negation that governs something else in the same clause
// still suppresses the claim ("this is not a refactor and tests passed"). That
// direction loses gate coverage for an unusual phrasing, which is preferable to
// blocking every honest negative report, and it is pinned by test.
//
// text is lower-cased here; callers pass raw final text.
func claimedInAffirmativeClause(text string, phrases []string) bool {
	for _, sentence := range splitClaimSentences(strings.ToLower(text)) {
		for _, clause := range claimContrastSplitRE.Split(sentence, -1) {
			for _, phrase := range phrases {
				index := strings.Index(clause, phrase)
				if index < 0 {
					continue
				}
				if containsAny(clause[:index], claimNegationMarkers) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func externalActionClaimSentence(sentence string) bool {
	sentence = strings.TrimSpace(sentence)
	if sentence == "" {
		return false
	}
	if externalActionEnglishClaim(sentence) {
		return true
	}
	return externalActionChineseClaim(sentence)
}

func externalActionEnglishClaim(sentence string) bool {
	if externalActionEnglishFirstPersonRE.MatchString(sentence) {
		return true
	}
	if externalActionEnglishStartRE.MatchString(sentence) {
		return true
	}
	if containsAny(sentence, []string{"deployment complete", "publish complete", "published successfully", "deploy complete"}) {
		return true
	}
	return false
}

func externalActionChineseClaim(sentence string) bool {
	if containsAny(sentence, []string{"我已部署", "我已经部署", "我已发布", "我已经发布", "我已发送", "我已经发送", "我们已部署", "我们已经部署", "我们已发布", "我们已经发布", "我们已发送", "我们已经发送"}) {
		return true
	}
	if containsAny(sentence, []string{"部署完成", "发布完成", "发送完成", "已部署完成", "已发布完成", "已发送完成", "部署好了", "发布好了", "发送好了"}) {
		return true
	}
	sentence = strings.TrimLeft(sentence, " \t-—*")
	return chineseCompletedExternalActionPrefix(sentence, "已部署") ||
		chineseCompletedExternalActionPrefix(sentence, "已发布") ||
		strings.HasPrefix(sentence, "已发送")
}

func chineseCompletedExternalActionPrefix(sentence, prefix string) bool {
	if !strings.HasPrefix(sentence, prefix) {
		return false
	}
	if strings.HasPrefix(sentence, prefix+"在") ||
		strings.HasPrefix(sentence, prefix+"于") ||
		strings.HasPrefix(sentence, prefix+"到") {
		return false
	}
	return true
}

func successfulTestEvidenceObserved(calls []ToolTrace) bool {
	return successfulBashCommandObserved(calls, func(command string) bool {
		return looksLikeVerificationCommand(json.RawMessage(fmt.Sprintf(`{"command":%q}`, command)))
	})
}

func searchEvidenceObserved(calls []ToolTrace) bool {
	for _, call := range calls {
		if call.IsError {
			continue
		}
		switch strings.ToLower(call.Name) {
		case "read", "grep", "glob", "ls":
			return true
		case "bash":
			if looksLikeReadOnlyBashCommand(bashCommandFromTrace(call)) {
				return true
			}
		}
	}
	return false
}

func broadSearchEvidenceObserved(calls []ToolTrace) bool {
	for _, call := range calls {
		if call.IsError {
			continue
		}
		switch strings.ToLower(call.Name) {
		case "grep", "glob", "ls":
			path := normalizeEvidencePath(firstNonEmpty(toolInputString(call.Input, "path"), toolInputString(call.Input, "pattern")))
			if path == "" || path == "." || strings.HasPrefix(path, "**/") {
				return true
			}
		case "bash":
			command := strings.ToLower(bashCommandFromTrace(call))
			if classifyShellIntent(command).ReadOnly && (strings.Contains(command, "rg ") || strings.Contains(command, "find ") || strings.Contains(command, "git grep")) {
				return true
			}
		}
	}
	return false
}

func successfulExternalActionObserved(calls []ToolTrace) bool {
	for _, call := range calls {
		if call.IsError {
			continue
		}
		if strings.EqualFold(call.Name, "Bash") && looksLikeExternalWriteCommand(bashCommandFromTrace(call)) {
			return true
		}
		switch strings.ToLower(call.Name) {
		case "agentmessage", "sendmessage":
			return true
		}
	}
	return false
}

func lastDeltaIndex(calls []ToolTrace) int {
	for i := len(calls) - 1; i >= 0; i-- {
		if traceCreatesDelta(calls[i]) || traceStagesChanges(calls[i]) {
			return i
		}
	}
	return -1
}

func traceCreatesDelta(call ToolTrace) bool {
	if call.IsError {
		return false
	}
	if len(call.FileChanges) > 0 {
		return true
	}
	return strings.EqualFold(call.Name, "Bash") && shellIntentMayNeedPostActionVerification(bashCommandFromTrace(call))
}

func shellIntentMayNeedPostActionVerification(command string) bool {
	intent := classifyShellIntent(command)
	return intent.Kind == shellIntentWorkspaceWrite || intent.Kind == shellIntentUnknownRisky
}

func traceStagesChanges(call ToolTrace) bool {
	if call.IsError || !strings.EqualFold(call.Name, "Bash") {
		return false
	}
	return strings.Contains(strings.ToLower(bashCommandFromTrace(call)), "git add")
}

func postDeltaChecksObserved(calls []ToolTrace, after int) (scope bool, metadata bool, semantic bool) {
	for i := after + 1; i < len(calls); i++ {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git status --short") && strings.Contains(command, "git diff --name-status") {
			scope = true
		}
		if strings.Contains(command, "git diff --summary") {
			metadata = true
		}
		if isSemanticCheckCommand(command) {
			semantic = true
		}
	}
	return scope, metadata, semantic
}

func isSemanticCheckCommand(command string) bool {
	command = strings.ToLower(command)
	if looksLikeVerificationCommand(json.RawMessage(fmt.Sprintf(`{"command":%q}`, command))) {
		return true
	}
	for _, marker := range []string{"go test", "npm test", "pytest", "cargo test", "bash -n", "test -x", "rg ", "grep ", "node ", "python ", "go run"} {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}

func preCommitScopeVerified(calls []ToolTrace) bool {
	lastDelta := lastContentDeltaIndex(calls)
	return fullPreCommitScopeVerificationObserved(calls, lastDelta)
}

// lastContentDeltaIndex is like lastDeltaIndex but ignores git add. Staging an
// already-edited file changes no working-tree content, so it must not
// invalidate a completed pre-commit scope verification — otherwise the natural
// edit → verify → git add → commit flow can never satisfy the gate. git add is
// still treated as a delta for the post-action delta gate via lastDeltaIndex.
func lastContentDeltaIndex(calls []ToolTrace) int {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if traceStagesChanges(call) {
			continue
		}
		if traceCreatesDelta(call) {
			return i
		}
	}
	return -1
}

func fullPreCommitScopeVerificationObserved(calls []ToolTrace, after int) bool {
	for i := after + 1; i < len(calls); i++ {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git commit") {
			continue
		}
		if strings.Contains(command, "git status --short") &&
			strings.Contains(command, "git diff --name-status") &&
			(strings.Contains(command, "git diff --cached") || strings.Contains(command, "git diff --staged")) {
			return true
		}
	}
	return false
}

func preSharedStateGitVerified(calls []ToolTrace) bool {
	return gitBranchObjectPrecheckObserved(calls)
}

func prePushGitVerified(calls []ToolTrace) bool {
	return gitBranchObjectPrecheckObserved(calls) && gitRemoteOrUpstreamPrecheckObserved(calls)
}

func preTagGitVerified(calls []ToolTrace) bool {
	return gitBranchObjectPrecheckObserved(calls) && gitTagStatePrecheckObserved(calls)
}

func gitBranchObjectPrecheckObserved(calls []ToolTrace) bool {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git status --short --branch") && (strings.Contains(command, "git rev-parse") || strings.Contains(command, "git log")) {
			return true
		}
	}
	return false
}

func gitRemoteOrUpstreamPrecheckObserved(calls []ToolTrace) bool {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git rev-parse @{u}") ||
			strings.Contains(command, "git rev-parse --abbrev-ref --symbolic-full-name @{u}") ||
			strings.Contains(command, "git branch -vv") ||
			strings.Contains(command, "git remote -v") ||
			strings.Contains(command, "git ls-remote") {
			return true
		}
	}
	return false
}

func gitTagStatePrecheckObserved(calls []ToolTrace) bool {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git tag --list") ||
			strings.Contains(command, "git tag -l") ||
			strings.Contains(command, "git show-ref --tags") ||
			strings.Contains(command, "git rev-parse refs/tags/") ||
			strings.Contains(command, "git ls-remote --tags") {
			return true
		}
	}
	return false
}

func postCommitVerificationObserved(calls []ToolTrace) bool {
	commitIndex := -1
	for i, call := range calls {
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		if strings.Contains(strings.ToLower(bashCommandFromTrace(call)), "git commit") {
			commitIndex = i
		}
	}
	if commitIndex < 0 {
		return false
	}
	for i := commitIndex + 1; i < len(calls); i++ {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git status --short") && (strings.Contains(command, "git log -1") || strings.Contains(command, "git rev-parse head")) {
			return true
		}
	}
	return false
}

func postTagVerificationObserved(calls []ToolTrace) bool {
	tagIndex := -1
	for i, call := range calls {
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		if looksLikeMutatingGitTagCommand(bashCommandFromTrace(call)) {
			tagIndex = i
		}
	}
	if tagIndex < 0 {
		return false
	}
	for i := tagIndex + 1; i < len(calls); i++ {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		hasHead := strings.Contains(command, "git rev-parse head") || strings.Contains(command, "git log -1")
		hasTagObject := strings.Contains(command, "git rev-parse refs/tags/") ||
			strings.Contains(command, "git show-ref --tags") ||
			strings.Contains(command, "git tag --points-at") ||
			strings.Contains(command, "git tag --list")
		if hasHead && hasTagObject {
			return true
		}
	}
	return false
}

func postPushVerificationObserved(calls []ToolTrace) bool {
	pushIndex := -1
	for i, call := range calls {
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		if strings.Contains(strings.ToLower(bashCommandFromTrace(call)), "git push") {
			pushIndex = i
		}
	}
	if pushIndex < 0 {
		return false
	}
	for i := pushIndex + 1; i < len(calls); i++ {
		call := calls[i]
		if call.IsError || !strings.EqualFold(call.Name, "Bash") {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, "git status --short --branch") || strings.Contains(command, "git ls-remote") || strings.Contains(command, "git rev-parse @{u}") {
			return true
		}
	}
	return false
}

func successfulBashCommandObserved(calls []ToolTrace, match func(string) bool) bool {
	for _, call := range calls {
		if !strings.EqualFold(call.Name, "Bash") || call.IsError {
			continue
		}
		if match(strings.ToLower(bashCommandFromTrace(call))) {
			return true
		}
	}
	return false
}

func looksLikeMutatingGitSharedStateCommand(command string) bool {
	return looksLikeMutatingGitPushCommand(command) || looksLikeMutatingGitTagCommand(command)
}

func looksLikeMutatingGitPushCommand(command string) bool {
	return gitpolicy.Analyze(command).Has(gitpolicy.OperationPush)
}

func looksLikeMutatingGitTagCommand(command string) bool {
	return gitpolicy.Analyze(command).Has(gitpolicy.OperationTag)
}

func looksLikeDestructiveGitSharedStateCommand(command string) bool {
	return gitAnalysisIsDestructive(gitpolicy.Analyze(command))
}

// Completion verification: given what the prompt asked for and what the model
// actually ran, decide whether a "done" claim is backed by evidence, and nudge
// the model back to work when it is not.
//
// Moved here from query.go verbatim by AUDIT-P2-01 step 4. It is the same
// semantic as the closure gate above — "the model says it finished, prove it" —
// and the gate was already calling bashCommandFromTrace,
// looksLikeVerificationCommand, displayChangePath and finalTextClaimsCompletion
// across the file boundary.
type completionVerification struct {
	Command string
	Match   string
}

func requiredCompletionVerifications(prompt string) []completionVerification {
	lower := strings.ToLower(prompt)
	if !mentionsCompletionVerificationBoundary(lower) {
		return nil
	}
	candidates := []completionVerification{
		{Command: "go test", Match: "go test"},
		{Command: "git diff --check", Match: "git diff --check"},
		{Command: "go vet", Match: "go vet"},
		{Command: "npm test", Match: "npm test"},
		{Command: "npm run build", Match: "npm run build"},
		{Command: "pytest", Match: "pytest"},
		{Command: "python -m pytest", Match: "python -m pytest"},
		{Command: "cargo test", Match: "cargo test"},
		{Command: "make test", Match: "make test"},
	}
	required := make([]completionVerification, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.Contains(lower, candidate.Match) {
			required = append(required, candidate)
		}
	}
	return required
}

func mentionsCompletionVerificationBoundary(lowerPrompt string) bool {
	for _, phrase := range []string{
		"before finishing",
		"before you finish",
		"before reporting completion",
		"before reporting done",
		"before final answer",
		"before your final answer",
		"before finishing.",
	} {
		if strings.Contains(lowerPrompt, phrase) {
			return true
		}
	}
	return false
}

func completionVerificationNudge(required []completionVerification, calls []ToolTrace, finalText string) string {
	if len(required) == 0 || !finalTextClaimsCompletion(finalText) {
		return ""
	}
	var unsatisfied []string
	for _, req := range required {
		if !successfulVerificationObserved(req, calls) {
			unsatisfied = append(unsatisfied, req.Command)
		}
	}
	if len(unsatisfied) == 0 {
		return ""
	}
	return "<system-reminder>Completion is blocked: the user explicitly requested verification before finishing, but these required commands have not successfully appeared in Bash tool calls: " +
		strings.Join(unsatisfied, ", ") +
		". Run the missing or failing verification commands now, inspect their outputs, fix any failures, and only then provide the final answer. Do not claim tests pass, checks succeeded, or work is complete until the command outputs support that exact claim.</system-reminder>"
}

func appendVerificationFailureRecoveryReminder(toolName string, input json.RawMessage, output string) string {
	if !strings.EqualFold(toolName, "Bash") || !looksLikeVerificationCommand(input) {
		return output
	}
	return strings.TrimRight(output, "\n") + "\n\n<system-reminder>Verification command failed. Treat this as blocking: inspect the failing assertion or command output, make the smallest code-only fix that addresses the observed expected/actual mismatch, rerun the same verification command, and do not provide the final answer until it passes. If the failure output or existing test file shows exact expected and actual strings, copy the exact expected literal from that evidence into the implementation instead of manually counting, deriving, or reformatting it. Avoid extended prose or mental counting; use the existing test file, command output, or a read-only deterministic shell pipeline as the source of truth. Do not create temporary test files, scratch source files, helper programs, or any new *_test.go file just to verify or count expected values. Keep recovery commands inside the current workspace; if sandbox rejects a redirect or path, rerun without that rejected path or write diagnostics to a workspace-relative file. Static /dev/null output discard is allowed when recognized by the shell guard. After cd/pwd, use paths relative to the current workspace instead of prefixing the workspace directory name again.</system-reminder>"
}

func appendPostEditVerificationReminder(toolName string, input json.RawMessage, output string, changes []tools.FileChange) string {
	if !shouldAppendPostEditVerificationReminder(toolName, input, changes) {
		return output
	}
	var lines []string
	lines = append(lines,
		"<system-reminder>",
		"## Post-edit verification required",
		"- required_scope_check: run git status --short and git diff --name-status; confirm only this repair's authorized files changed.",
		"- required_metadata_check: run git diff --summary; inspect mode/type changes, executable bit changes, symlink changes, renames, deletes, and creates.",
		"- required_semantic_check: verify the repaired invariant with a deterministic read-only command or focused test.",
		"- completion_blocker: do not commit or claim completion while unrelated files, unexpected mode changes, unverified tag/branch state, or failed checks remain.",
	)
	if len(changes) == 0 && strings.EqualFold(toolName, "Bash") {
		lines = append(lines, "- changed_scope: write-like Bash command detected; use git status/diff to identify exact changed files before continuing.")
	}
	for _, change := range changes {
		path := displayChangePath(change.Path)
		if path == "" {
			continue
		}
		lines = append(lines, "- changed_path: "+path)
		if change.ModeChanged {
			lines = append(lines, "- mode_change_detected: "+formatFileMode(change.BeforeMode)+" -> "+formatFileMode(change.AfterMode)+"; treat as blocking unless explicitly requested.")
		}
		for _, semantic := range postEditSemanticChecks(path) {
			lines = append(lines, "- "+semantic)
		}
	}
	lines = append(lines, "</system-reminder>")
	return strings.TrimRight(output, "\n") + "\n\n" + strings.Join(lines, "\n")
}

func shouldAppendPostEditVerificationReminder(toolName string, input json.RawMessage, changes []tools.FileChange) bool {
	switch strings.ToLower(toolName) {
	case "edit", "multiedit", "write", "notebookedit":
		return true
	case "bash":
		return looksLikeWriteLikeBashCommand(input)
	default:
		return len(changes) > 0
	}
}

func looksLikeWriteLikeBashCommand(input json.RawMessage) bool {
	var params struct {
		Command string `json:"command"`
	}
	command := string(input)
	if err := json.Unmarshal(input, &params); err == nil && params.Command != "" {
		command = params.Command
	}
	lower := strings.ToLower(command)
	writeMarkers := []string{
		"chmod ",
		"mv ",
		"cp ",
		"rm ",
		"sed -i",
		"perl -pi",
		"git tag",
		"git commit",
		"git push",
		"git add",
		"swag init",
	}
	for _, marker := range writeMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(lower, " > ") || strings.Contains(lower, ">>") || strings.Contains(lower, "tee ")
}

func postEditSemanticChecks(path string) []string {
	normalized := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(normalized))
	var checks []string
	if isScriptOrHookPath(normalized, base) {
		checks = append(checks, "required_script_hook_check: verify executable bit is preserved when relevant, run test -x or git diff --summary, and verify referenced commands/scripts exist; use bash -n for shell scripts.")
	}
	if isMetadataPath(normalized, base) {
		checks = append(checks, "required_metadata_consistency_check: verify package metadata, docs, manifests, lockfiles, and source-of-truth declarations still agree.")
	}
	if isReleasePath(normalized, base) {
		checks = append(checks, "required_release_consistency_check: verify VERSION/package files, local HEAD, origin/main, local tag object, and remote tag object before claiming release completion.")
	}
	if isDocIndexPath(base) {
		checks = append(checks, "required_doc_claim_check: verify documented counts, commands, or indexes against actual directories, scripts, skills, and metadata.")
	}
	return checks
}

func isScriptOrHookPath(path, base string) bool {
	return strings.HasSuffix(base, ".sh") ||
		strings.HasPrefix(path, "hooks/") ||
		strings.Contains(path, "/hooks/") ||
		strings.HasPrefix(path, "scripts/") ||
		strings.Contains(path, "/scripts/") ||
		strings.HasPrefix(path, ".github/workflows/") ||
		strings.Contains(path, "/.github/workflows/")
}

func isMetadataPath(path, base string) bool {
	switch base {
	case "package.json", "go.mod", "pyproject.toml", "cargo.toml", "plugin.json", "manifest.json":
		return true
	}
	return strings.Contains(path, "manifest") || strings.Contains(path, "metadata")
}

func isReleasePath(path, base string) bool {
	return base == "version" ||
		strings.HasPrefix(base, "changelog") ||
		strings.Contains(path, "release") ||
		strings.Contains(path, "version")
}

func isDocIndexPath(base string) bool {
	return base == "readme.md" ||
		base == "index.md" ||
		base == "catalog.md" ||
		strings.Contains(base, "index")
}

func displayChangePath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func formatFileMode(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}

func looksLikeVerificationCommand(input json.RawMessage) bool {
	var params struct {
		Command string `json:"command"`
	}
	command := string(input)
	if err := json.Unmarshal(input, &params); err == nil && params.Command != "" {
		command = params.Command
	}
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"go test",
		"git diff --check",
		"go vet",
		"npm test",
		"npm run build",
		"pytest",
		"python -m pytest",
		"cargo test",
		"make test",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func finalTextClaimsCompletion(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{
		"tests passed",
		"test passed",
		"tests pass",
		"all tests pass",
		"checks passed",
		"checks pass",
		"git diff --check is clean",
		"diff --check is clean",
		"complete",
		"completed",
		"done",
		"succeeded",
		"success",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func successfulVerificationObserved(req completionVerification, calls []ToolTrace) bool {
	for _, call := range calls {
		if !strings.EqualFold(call.Name, "Bash") || call.IsError {
			continue
		}
		command := strings.ToLower(bashCommandFromTrace(call))
		if strings.Contains(command, req.Match) {
			return true
		}
	}
	return false
}

func bashCommandFromTrace(call ToolTrace) string {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(call.Input), &params); err == nil && strings.TrimSpace(params.Command) != "" {
		return params.Command
	}
	return call.Input
}
