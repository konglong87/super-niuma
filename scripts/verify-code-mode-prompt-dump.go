package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/konglong87/super-niuma/internal/promptdump"
)

func main() {
	defaults := promptdump.DefaultVerifyOptions()
	minTurns := flag.Int("min-turns", defaults.MinTurns, "minimum expected prompt dump turns")
	requireFinalToolsDisabled := flag.Bool("require-final-tools-disabled", defaults.RequireFinalToolsDisabled, "require final dump record to expose zero tools")
	requireFinalTurnBudget := flag.Bool("require-final-turn-budget", defaults.RequireFinalTurnBudget, "require final dump record runtime_status.sections to include turn_budget")
	requireSubagentTurnBudgetToolsDisabled := flag.Bool("require-subagent-turn-budget-tools-disabled", defaults.RequireSubagentTurnBudgetToolsDisabled, "require subagent dump records with turn_budget runtime status to expose zero tools")
	requireSubagentRecord := flag.Bool("require-subagent-record", defaults.RequireSubagentRecord, "require the dump to contain at least one subagent-scoped record")
	requireNoToolErrors := flag.Bool("require-no-tool-errors", defaults.RequireNoToolErrors, "require all dump records to have tool_result_stats.error_count=0")
	requireNoActualTruncation := flag.Bool("require-no-actual-truncation", defaults.RequireNoActualTruncation, "require all dump records to have tool_result_stats.actual_truncated=0")
	requireNoRawOverLimit := flag.Bool("require-no-raw-over-limit", defaults.RequireNoRawOverLimit, "require all dump records to have tool_result_stats.raw_over_limit=0")
	requireToolNoRawOverLimit := flag.String("require-tool-no-raw-over-limit", "", "comma-separated tool names that must have no raw tool_results over configured limit")
	requireToolNoPersistedOutput := flag.String("require-tool-no-persisted-output", "", "comma-separated tool names that must have no persisted-output tool_results")
	requireToolPersistedOutput := flag.String("require-tool-persisted-output", "", "comma-separated tool names that must have at least one persisted-output tool_result")
	requireToolMaxBytes := flag.String("require-tool-max-bytes", "", "comma-separated Tool=Bytes specs for maximum visible tool_result content bytes")
	requireToolResult := flag.String("require-tool-result", "", "comma-separated tool names that must appear as tool_result blocks")
	requireToolUseInputText := flag.String("require-tool-use-input-text", "", "comma-separated Tool=snippet specs that must appear in full prompt dump tool_use inputs")
	forbidTool := flag.String("forbid-tool", "", "comma-separated tool names that must not be exposed in full prompt dump requests")
	requireRequestText := flag.String("require-request-text", "", "comma-separated text snippets that must appear in a full prompt dump request")
	forbidRequestText := flag.String("forbid-request-text", "", "comma-separated text snippets that must not appear in a full prompt dump request")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./scripts/verify-code-mode-prompt-dump.go [flags] <prompt-dump.jsonl>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	report, err := promptdump.VerifyCodeModeRecoveryDump(flag.Arg(0), promptdump.VerifyOptions{
		MinTurns:                               *minTurns,
		RequireFinalToolsDisabled:              *requireFinalToolsDisabled,
		RequireFinalTurnBudget:                 *requireFinalTurnBudget,
		RequireSubagentTurnBudgetToolsDisabled: *requireSubagentTurnBudgetToolsDisabled,
		RequireSubagentRecord:                  *requireSubagentRecord,
		RequireNoToolErrors:                    *requireNoToolErrors,
		RequireNoActualTruncation:              *requireNoActualTruncation,
		RequireNoRawOverLimit:                  *requireNoRawOverLimit,
		RequireToolNoRawOverLimit:              splitCommaFlag(*requireToolNoRawOverLimit),
		RequireToolNoPersistedOutput:           splitCommaFlag(*requireToolNoPersistedOutput),
		RequireToolPersistedOutput:             splitCommaFlag(*requireToolPersistedOutput),
		RequireToolMaxBytes:                    splitCommaFlag(*requireToolMaxBytes),
		RequireToolResult:                      splitCommaFlag(*requireToolResult),
		RequireToolUseInputText:                splitCommaFlag(*requireToolUseInputText),
		ForbidTool:                             splitCommaFlag(*forbidTool),
		RequireRequestText:                     splitCommaFlag(*requireRequestText),
		ForbidRequestText:                      splitCommaFlag(*forbidRequestText),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify prompt dump: %v\n", err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(2)
	}
	if !report.OK {
		os.Exit(1)
	}
}

func splitCommaFlag(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
