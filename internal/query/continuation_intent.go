package query

import (
	"regexp"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
)

type continuationIntent struct {
	Active             bool               `json:"active"`
	UserPrompt         string             `json:"user_prompt,omitempty"`
	PendingAction      string             `json:"pending_action,omitempty"`
	Source             string             `json:"source,omitempty"`
	Decision           string             `json:"decision,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	SharedStateEffects []gitpolicy.Effect `json:"shared_state_effects,omitempty"`
}

func resolveContinuationIntent(prompt string, history []anthropic.MessageParam) continuationIntent {
	trimmed := strings.TrimSpace(prompt)
	if !isShortContinuationConfirmation(trimmed) {
		return continuationIntent{}
	}
	if effects := confirmedSharedStateEffects(history); len(effects) > 0 {
		return continuationIntent{
			Active:             true,
			UserPrompt:         trimmed,
			PendingAction:      confirmedSharedStateAction(effects),
			Source:             "latest_assistant_authorization_request",
			Decision:           "confirmed_pending_shared_state",
			Reason:             "short confirmation authorizes the exact Git effects proposed by the latest assistant message",
			SharedStateEffects: effects,
		}
	}
	action, source := pendingActionFromHistory(history)
	if action == "" {
		return continuationIntent{}
	}
	return continuationIntent{
		Active:        true,
		UserPrompt:    trimmed,
		PendingAction: action,
		Source:        source,
		Decision:      "confirmed_pending_action",
		Reason:        "short confirmation follows an actionable pending assistant proposal",
	}
}

func continuationReminder(intent continuationIntent) string {
	if !intent.Active {
		return ""
	}
	return "<system-reminder>Continuation intent detected: the user's short reply confirms the pending action from the previous assistant message: " +
		intent.PendingAction +
		". Proceed now using the available tools. Do not answer with only an acknowledgement, and do not ask what to do again unless the action is unsafe or genuinely ambiguous.</system-reminder>"
}

func continuationCompletionGate(intent continuationIntent, calls []ToolTrace, finalText string) completionGateResult {
	if !intent.Active {
		return completionGateResult{Severity: gateSeverityAllow}
	}
	if len(calls) > 0 {
		return completionGateResult{Severity: gateSeverityAllow}
	}
	if looksLikeContinuationBlockedExplanation(finalText) {
		return completionGateResult{Severity: gateSeverityAllow}
	}
	return completionGateResult{
		Severity: gateSeverityBlockContinue,
		RuleID:   "confirmed_pending_action_requires_progress",
		Reminder: "<system-reminder>Completion is blocked: the user confirmed a pending action (" +
			intent.PendingAction +
			"), but the assistant only acknowledged or asked for the next instruction. Continue the confirmed action now and use tools to make concrete progress.</system-reminder>",
		MissingEvidence: []string{"tool progress for confirmed pending action"},
	}
}

func continuationTaskContract(base taskContract, intent continuationIntent) taskContract {
	if len(intent.SharedStateEffects) > 0 {
		return taskContract{
			TaskType:              base.TaskType,
			ClosureLevel:          closureLevelSharedStateChange,
			SharedStateOperations: sharedStateOperationsFromEffects(intent.SharedStateEffects),
			Reasons: []classificationReason{{
				RuleID:  "confirmed_pending_shared_state",
				Matched: intent.PendingAction,
				Detail:  "short confirmation inherits exact Git effects from the latest assistant authorization request",
			}},
			Confidence: classificationConfidenceHigh,
		}
	}
	if !intent.Active || base.ClosureLevel != closureLevelNoToolAnswer {
		return base
	}
	if !looksLikeActionablePendingText(intent.PendingAction) {
		return base
	}
	return taskContract{
		ClosureLevel: closureLevelLocalChange,
		Reasons: []classificationReason{{
			RuleID:  "confirmed_pending_action",
			Matched: intent.PendingAction,
			Detail:  "short confirmation inherits actionable pending assistant proposal",
		}},
		Confidence: classificationConfidenceMedium,
	}
}

var shellFenceRE = regexp.MustCompile("(?s)```([A-Za-z0-9_-]*)[ \\t]*\\r?\\n(.*?)```")
var inlineCodeRE = regexp.MustCompile("`([^`\\r\\n]+)`")

const inlineAuthorizationWindow = 512

func confirmedSharedStateEffects(history []anthropic.MessageParam) []gitpolicy.Effect {
	text := latestAssistantText(history)
	requestIndex := authorizationRequestIndex(text)
	if text == "" || requestIndex < 0 {
		return nil
	}
	closestDistance := -1
	var closestCommand string
	for _, match := range shellFenceRE.FindAllStringSubmatchIndex(text, -1) {
		if len(match) != 6 {
			continue
		}
		language := text[match[2]:match[3]]
		if !isExecutableShellFence(language) {
			continue
		}
		distance := requestIndex - match[1]
		if distance < 0 {
			distance = match[0] - requestIndex
		}
		if closestDistance < 0 || distance < closestDistance {
			closestDistance = distance
			closestCommand = text[match[4]:match[5]]
		}
	}
	if closestCommand == "" {
		return confirmedInlineSharedStateEffects(text, requestIndex)
	}
	analysis := gitpolicy.Analyze(strings.TrimSpace(closestCommand))
	if analysis.ParseError {
		return nil
	}
	return analysis.Effects
}

func confirmedInlineSharedStateEffects(text string, requestIndex int) []gitpolicy.Effect {
	var effects []gitpolicy.Effect
	inlineRanges := inlineCodeRE.FindAllStringIndex(text, -1)
	for _, bounds := range inlineRanges {
		if len(bounds) != 2 || absInt(bounds[0]-requestIndex) > inlineAuthorizationWindow {
			continue
		}
		command := strings.TrimSpace(text[bounds[0]+1 : bounds[1]-1])
		analysis := gitpolicy.Analyze(command)
		if analysis.ParseError || len(analysis.Effects) == 0 {
			continue
		}
		effects = append(effects, analysis.Effects...)
	}
	return effects
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func latestAssistantText(history []anthropic.MessageParam) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" {
			return strings.TrimSpace(assistantText(history[i].Content))
		}
	}
	return ""
}

func authorizationRequestIndex(text string) int {
	lower := strings.ToLower(text)
	markers := []string{
		"确认授权", "确认我执行", "授权我执行",
		"需要你授权", "请你授权", "需要授权",
		"do you authorize", "authorize me to execute", "authorize me to run",
		"confirm authorization", "confirm that i execute", "confirm that i run",
	}
	fences := shellFenceRE.FindAllStringIndex(lower, -1)
	inlineRanges := inlineCodeRE.FindAllStringIndex(lower, -1)
	last := -1
	for _, marker := range markers {
		for offset := 0; offset < len(lower); {
			index := strings.Index(lower[offset:], marker)
			if index < 0 {
				break
			}
			index += offset
			if !insideAnyRange(index, fences) && !insideAnyRange(index, inlineRanges) && index > last {
				last = index
			}
			offset = index + len(marker)
		}
	}
	return last
}

func insideAnyRange(index int, ranges [][]int) bool {
	for _, bounds := range ranges {
		if len(bounds) == 2 && index >= bounds[0] && index < bounds[1] {
			return true
		}
	}
	return false
}

func isExecutableShellFence(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "bash", "sh", "shell", "zsh":
		return true
	default:
		return false
	}
}

func confirmedSharedStateAction(effects []gitpolicy.Effect) string {
	operations := make([]string, 0, len(effects))
	seen := map[gitpolicy.Operation]bool{}
	for _, effect := range effects {
		if seen[effect.Operation] {
			continue
		}
		seen[effect.Operation] = true
		operations = append(operations, "git "+string(effect.Operation))
	}
	return "execute the exact confirmed " + strings.Join(operations, " and ") + " workflow"
}

func sharedStateOperationsFromEffects(effects []gitpolicy.Effect) []sharedStateOperation {
	operations := make([]sharedStateOperation, 0, len(effects))
	seen := map[sharedStateOperation]bool{}
	for _, effect := range effects {
		var operation sharedStateOperation
		switch effect.Operation {
		case gitpolicy.OperationCommit:
			operation = sharedStateCommit
		case gitpolicy.OperationPush:
			operation = sharedStatePush
		case gitpolicy.OperationTag:
			operation = sharedStateTag
		default:
			continue
		}
		if !seen[operation] {
			seen[operation] = true
			operations = append(operations, operation)
		}
	}
	return operations
}

func isShortContinuationConfirmation(prompt string) bool {
	text := normalizeContinuationText(prompt)
	if text == "" {
		return false
	}
	if looksLikeContinuationRejectionOrModification(text) {
		return false
	}
	confirmations := map[string]bool{
		"好":        true,
		"好的":       true,
		"好啊":       true,
		"好吧":       true,
		"可以":       true,
		"可以的":      true,
		"行":        true,
		"行的":       true,
		"嗯":        true,
		"嗯嗯":       true,
		"确认":       true,
		"授权":       true,
		"确认授权":     true,
		"同意":       true,
		"批准":       true,
		"开始":       true,
		"开始吧":      true,
		"继续":       true,
		"继续吧":      true,
		"执行":       true,
		"执行吧":      true,
		"开工":       true,
		"开干":       true,
		"按这个来":     true,
		"就这个":      true,
		"就按这个":     true,
		"就按这个来":    true,
		"那就开始":     true,
		"那开始":      true,
		"好开始":      true,
		"好继续":      true,
		"好执行":      true,
		"好按这个来":    true,
		"ok":       true,
		"okay":     true,
		"yes":      true,
		"y":        true,
		"go":       true,
		"start":    true,
		"proceed":  true,
		"continue": true,
	}
	if confirmations[text] {
		return true
	}
	if len([]rune(text)) > 16 {
		return false
	}
	return strings.HasPrefix(text, "好") && containsAny(text, []string{"开始", "继续", "执行", "按这个"})
}

func looksLikeContinuationRejectionOrModification(text string) bool {
	if strings.Contains(text, "?") || strings.Contains(text, "？") {
		return true
	}
	return containsAny(text, []string{
		"不", "别", "不要", "先不", "先别", "等下", "等等", "暂停",
		"先说", "先分析", "先看看", "方案", "了吗", "了没", "好了吗",
		"改成", "修改", "换成", "不是", "no", "not", "don't", "dont",
		"do not", "wait", "hold", "stop", "plan first",
	})
}

func normalizeContinuationText(prompt string) string {
	text := strings.ToLower(strings.TrimSpace(prompt))
	text = strings.Trim(text, " \t\r\n。.!！,，;；")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.ReplaceAll(text, "，", "")
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ReplaceAll(text, "。", "")
	text = strings.ReplaceAll(text, "！", "")
	text = strings.ReplaceAll(text, "!", "")
	return text
}

func pendingActionFromHistory(history []anthropic.MessageParam) (string, string) {
	for i := len(history) - 1; i >= 0 && i >= len(history)-12; i-- {
		message := history[i]
		if message.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(assistantText(message.Content))
		if text == "" || looksLikeIdleAssistantText(text) {
			continue
		}
		if action := extractPendingAction(text); action != "" {
			return action, "assistant_history"
		}
	}
	return "", ""
}

func extractPendingAction(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if looksLikePendingActionHeading(line) {
			continue
		}
		candidate := cleanPendingActionLine(line)
		if candidate == "" {
			continue
		}
		if looksLikeActionablePendingText(candidate) && lineLooksPrioritized(line, text) {
			return truncatePendingAction(candidate)
		}
	}
	sentences := splitActionSentences(text)
	for _, sentence := range sentences {
		candidate := cleanPendingActionLine(sentence)
		if candidate == "" {
			continue
		}
		if looksLikeActionablePendingText(candidate) && containsAny(sentence, []string{"接下来", "下一步", "优先", "建议", "可以", "开始", "继续推进"}) {
			return truncatePendingAction(candidate)
		}
	}
	return ""
}

func looksLikePendingActionHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	return (strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, "：")) &&
		containsAny(trimmed, []string{"优先", "建议", "下一步", "接下来", "继续推进"})
}

var pendingActionLinePrefix = regexp.MustCompile(`^\s*(?:[-*]\s+|\d+[.、)]\s*)`)

func cleanPendingActionLine(line string) string {
	line = strings.TrimSpace(line)
	line = pendingActionLinePrefix.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "`*_ ")
	line = strings.ReplaceAll(line, "**", "")
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, "——"); idx > 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, "—"); idx > 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, ":"); idx > 0 && idx < 16 {
		line = strings.TrimSpace(line[idx+1:])
	}
	if idx := strings.Index(line, "："); idx > 0 && idx < 16 {
		line = strings.TrimSpace(line[idx+len("："):])
	}
	return line
}

func lineLooksPrioritized(line, fullText string) bool {
	trimmed := strings.TrimSpace(line)
	if pendingActionLinePrefix.MatchString(trimmed) {
		return true
	}
	return containsAny(trimmed, []string{"接下来", "下一步", "优先", "建议", "可以开始", "开始"}) ||
		containsAny(fullText, []string{"优先级建议", "如果继续推进", "下一步", "接下来", "建议先", "最建议"})
}

func looksLikeActionablePendingText(text string) bool {
	normalized := normalizeRuntimeTaskPrompt(text)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"fix", "repair", "implement", "create", "update", "add", "write", "edit",
		"refactor", "verify", "test", "run", "continue", "proceed", "start",
		"修复", "实现", "创建", "新增", "更新", "补强", "完善", "扩充", "整理",
		"开发", "验证", "测试", "运行", "修改", "改", "继续", "开始", "执行", "推进",
	})
}

func looksLikeIdleAssistantText(text string) bool {
	normalized := normalizeRuntimeTaskPrompt(text)
	return containsAny(normalized, []string{
		"有什么需要帮忙", "有什么需要做", "随时说", "你想做什么", "等待你的完整指令",
		"how can i help", "what would you like", "let me know",
	})
}

func looksLikeContinuationBlockedExplanation(text string) bool {
	normalized := normalizeRuntimeTaskPrompt(text)
	if normalized == "" {
		return false
	}
	return containsAny(normalized, []string{
		"需要确认", "需要你确认", "需要批准", "权限", "不安全", "高风险",
		"无法继续", "不能继续", "不明确", "有歧义", "ambiguous", "unsafe",
		"permission", "approval required", "blocked",
	})
}

func splitActionSentences(text string) []string {
	replacer := strings.NewReplacer("。", "\n", "！", "\n", "？", "\n", ".", "\n", "!", "\n", "?", "\n")
	text = replacer.Replace(text)
	return strings.Split(text, "\n")
}

func truncatePendingAction(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= 140 {
		return text
	}
	return string(runes[:140])
}
