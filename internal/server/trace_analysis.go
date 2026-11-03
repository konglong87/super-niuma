package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

const (
	traceDiagnosticInfo    = "info"
	traceDiagnosticWarning = "warning"

	traceDiagnosticParallelCandidate = "parallel_read_candidate"
	traceDiagnosticRepeatedCall      = "repeated_tool_call"
	traceDiagnosticRepeatedFailure   = "repeated_failed_action"
	traceDiagnosticNoProgress        = "no_progress_turn"
)

type traceAnalysis struct {
	Summary     traceSummary
	Quality     traceQuality
	Diagnostics []traceDiagnostic
}

func analyzeTrace(events []traceEvent, spans []traceSpan) traceAnalysis {
	summary := summarizeTrace(events, spans)
	diagnostics := traceDiagnostics(events, spans)
	summary.TaskWallMS = traceTaskWall(spans, summary.DurationMS)
	summary.ModelWallMS = coveredTraceSpanDuration(traceSpansByType(spans, "model"))
	summary.ToolWorkMS = traceSpanWork(traceSpansByType(spans, "tool"))
	summary.ToolWallMS = coveredTraceSpanDuration(traceSpansByType(spans, "tool"))
	summary.CriticalPathMS = traceCriticalPath(spans)
	summary.UnattributedMS = traceRootUnattributed(spans)
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == traceDiagnosticParallelCandidate {
			summary.ParallelSavingsMS += diagnostic.EstimatedSavingsMS
		}
	}
	return traceAnalysis{
		Summary:     summary,
		Quality:     traceQualitySignals(events, summary),
		Diagnostics: diagnostics,
	}
}

func traceSpansByType(spans []traceSpan, spanType string) []traceSpan {
	out := make([]traceSpan, 0)
	for _, span := range spans {
		if strings.EqualFold(span.Type, spanType) {
			out = append(out, span)
		}
	}
	return out
}

func traceSpanWork(spans []traceSpan) int64 {
	var total int64
	for _, span := range spans {
		if span.DurationMS > 0 {
			total += span.DurationMS
		}
	}
	return total
}

func traceTaskWall(spans []traceSpan, fallback int64) int64 {
	var best int64
	for _, span := range spans {
		if span.ParentID == "" && (strings.EqualFold(span.Type, "query") || best == 0) && span.DurationMS > best {
			best = span.DurationMS
		}
	}
	if best == 0 {
		return fallback
	}
	return best
}

func traceRootUnattributed(spans []traceSpan) int64 {
	var best int64
	for _, span := range spans {
		if span.ParentID == "" && strings.EqualFold(span.Type, "query") && span.SelfDurationMS > best {
			best = span.SelfDurationMS
		}
	}
	return best
}

func traceCriticalPath(spans []traceSpan) int64 {
	var queryRoot, fallbackRoot int64
	for _, span := range spans {
		if strings.EqualFold(span.Type, "query") && span.DurationMS > queryRoot {
			queryRoot = span.DurationMS
		}
		if span.ParentID == "" && span.DurationMS > fallbackRoot {
			fallbackRoot = span.DurationMS
		}
	}
	if queryRoot > 0 {
		return queryRoot
	}
	if fallbackRoot > 0 {
		return fallbackRoot
	}
	return coveredTraceSpanDuration(spans)
}

func traceDiagnostics(events []traceEvent, spans []traceSpan) []traceDiagnostic {
	diagnostics := append(traceParallelDiagnostics(spans), traceRepeatedCallDiagnostics(events)...)
	diagnostics = append(diagnostics, traceNoProgressDiagnostics(spans)...)
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].TurnIndex != diagnostics[j].TurnIndex {
			return diagnostics[i].TurnIndex < diagnostics[j].TurnIndex
		}
		return diagnostics[i].Code < diagnostics[j].Code
	})
	return diagnostics
}

func traceParallelDiagnostics(spans []traceSpan) []traceDiagnostic {
	byTurn := map[int][]traceSpan{}
	for _, span := range spans {
		if span.TurnIndex > 0 && strings.EqualFold(span.Type, "tool") && span.ConcurrencyClass == "read_only" {
			byTurn[span.TurnIndex] = append(byTurn[span.TurnIndex], span)
		}
	}
	turns := make([]int, 0, len(byTurn))
	for turn := range byTurn {
		turns = append(turns, turn)
	}
	sort.Ints(turns)
	out := make([]traceDiagnostic, 0)
	for _, turn := range turns {
		items := byTurn[turn]
		if len(items) < 2 {
			continue
		}
		var longest int64
		ids := make([]string, 0, len(items))
		for _, span := range items {
			longest = maxInt64(longest, span.DurationMS)
			ids = append(ids, span.ID)
		}
		// Remaining opportunity is the current wall time minus the ideal wall
		// time. Fully overlapping tools are already parallel and have no further
		// savings; sequential tools retain the full work-minus-longest opportunity.
		savings := maxInt64(0, coveredTraceSpanDuration(items)-longest)
		if savings == 0 {
			continue
		}
		out = append(out, traceDiagnostic{
			Code:               traceDiagnosticParallelCandidate,
			Severity:           traceDiagnosticInfo,
			Message:            "read-only tools in this turn can overlap while preserving result commit order",
			TurnIndex:          turn,
			Count:              len(items),
			EstimatedSavingsMS: savings,
			SpanIDs:            ids,
		})
	}
	return out
}

func traceRepeatedCallDiagnostics(events []traceEvent) []traceDiagnostic {
	type call struct {
		name        string
		fingerprint string
		count       int
		failures    int
	}
	calls := map[string]*call{}
	toolCalls := map[string]string{}
	for _, event := range events {
		switch event.Type {
		case "tool_call":
			fingerprint := traceToolFingerprint(event.ToolName, event.Input)
			item := calls[fingerprint]
			if item == nil {
				item = &call{name: event.ToolName, fingerprint: fingerprint}
				calls[fingerprint] = item
			}
			item.count++
			if event.ToolID != "" {
				toolCalls[event.ToolID] = fingerprint
			}
		case "tool_result":
			if event.IsError {
				if item := calls[toolCalls[event.ToolID]]; item != nil {
					item.failures++
				}
			}
		}
	}
	keys := make([]string, 0, len(calls))
	for key := range calls {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]traceDiagnostic, 0)
	for _, key := range keys {
		item := calls[key]
		if item.count > 1 {
			out = append(out, traceDiagnostic{Code: traceDiagnosticRepeatedCall, Severity: traceDiagnosticInfo, Message: "the same tool input was called repeatedly", ToolName: item.name, Count: item.count, Fingerprint: item.fingerprint})
		}
		if item.failures > 1 {
			out = append(out, traceDiagnostic{Code: traceDiagnosticRepeatedFailure, Severity: traceDiagnosticWarning, Message: "the same tool input failed repeatedly", ToolName: item.name, Count: item.failures, Fingerprint: item.fingerprint})
		}
	}
	return out
}

func traceNoProgressDiagnostics(spans []traceSpan) []traceDiagnostic {
	byTurn := map[int][]string{}
	for _, span := range spans {
		if span.TurnIndex > 0 && strings.EqualFold(span.Type, "tool") {
			byTurn[span.TurnIndex] = append(byTurn[span.TurnIndex], strings.ToLower(firstNonEmptyTrace(span.ToolName, span.Name))+":"+strings.ToLower(span.Status))
		}
	}
	turns := make([]int, 0, len(byTurn))
	for turn := range byTurn {
		turns = append(turns, turn)
		sort.Strings(byTurn[turn])
	}
	sort.Ints(turns)
	out := make([]traceDiagnostic, 0)
	for i := 1; i < len(turns); i++ {
		if turns[i] != turns[i-1]+1 {
			continue
		}
		previous := strings.Join(byTurn[turns[i-1]], "|")
		current := strings.Join(byTurn[turns[i]], "|")
		if previous != "" && previous == current {
			out = append(out, traceDiagnostic{Code: traceDiagnosticNoProgress, Severity: traceDiagnosticWarning, Message: "adjacent turns repeated the same tool/status set", TurnIndex: turns[i], Fingerprint: shortTraceHash(current)})
		}
	}
	return out
}

func traceToolFingerprint(name, input string) string {
	canonical := strings.TrimSpace(input)
	var decoded any
	if json.Unmarshal([]byte(canonical), &decoded) == nil {
		if data, err := json.Marshal(decoded); err == nil {
			canonical = string(data)
		}
	}
	return shortTraceHash(strings.ToLower(strings.TrimSpace(name)) + "\x00" + canonical)
}

func shortTraceHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func traceQualitySignals(events []traceEvent, summary traceSummary) traceQuality {
	quality := traceQuality{ToolErrors: summary.ToolErrors}
	testCalls := map[string]bool{}
	toolNames := map[string]string{}
	latestTestID := ""
	latestTestCompleted := false
	latestTestPassed := false
	seenToolError := map[string]bool{}
	for _, event := range events {
		switch event.Type {
		case "tool_call":
			if event.ToolID != "" {
				toolNames[event.ToolID] = event.ToolName
			}
			if strings.EqualFold(event.ToolName, "TodoWrite") {
				quality.TodoWrites++
			}
			if isTraceTestCommand(event.ToolName, event.Input) {
				quality.TestsRun = true
				testCalls[event.ToolID] = true
				latestTestID = event.ToolID
				latestTestCompleted = false
				latestTestPassed = false
			}
		case "tool_result":
			toolName := strings.ToLower(strings.TrimSpace(firstNonEmptyTrace(event.ToolName, toolNames[event.ToolID])))
			if event.IsError && toolName != "" {
				seenToolError[toolName] = true
			}
			if testCalls[event.ToolID] {
				quality.TestAttempts++
				if event.IsError {
					quality.FailedTestAttempts++
				}
				if event.ToolID == latestTestID {
					latestTestCompleted = true
					latestTestPassed = !event.IsError
				}
			}
			if !event.IsError && toolName != "" && seenToolError[toolName] {
				quality.Recoveries++
				delete(seenToolError, toolName)
			}
		case "completion_gate":
			if strings.HasPrefix(strings.ToLower(event.Status), "block") {
				quality.GateBlocks++
			}
		case "evidence_record", "delta_record", "repair_verification":
			if !event.IsError {
				quality.CompletionVerified = true
			}
		}
	}
	quality.TestsPassed = quality.TestsRun && latestTestCompleted && latestTestPassed
	quality.TestFailureRecovered = quality.FailedTestAttempts > 0 && quality.TestsPassed
	finalVerificationPassed := quality.CompletionVerified && (!quality.TestsRun || quality.TestsPassed)
	quality.FinalVerificationPassed = &finalVerificationPassed
	return quality
}

func isTraceTestCommand(toolName, input string) bool {
	if !strings.EqualFold(toolName, "Bash") && !strings.EqualFold(toolName, "PowerShell") {
		return false
	}
	command := strings.ToLower(input)
	for _, marker := range []string{"go test", "npm test", "npm run test", "pnpm test", "yarn test", "pytest", "cargo test", "dotnet test"} {
		if strings.Contains(command, marker) {
			return true
		}
	}
	return false
}
