package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/konglong87/super-niuma/internal/session"
)

func sortedSkipReasonsCLI(skipped map[string]int) []string {
	reasons := make([]string, 0, len(skipped))
	for reason := range skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	return reasons
}

// transcriptCommand dispatches transcript interop subcommands. Currently only
// import-claude-code, which reads an original Claude Code transcript read-only
// and writes a best-effort v2 message-graph copy as a new niuma session.
func transcriptCommand(args []string, cwd string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: transcript import-claude-code <path> [--cwd <dir>]")
	}
	switch args[0] {
	case "import-claude-code":
		return transcriptImportClaudeCodeCommand(args[1:], cwd, stdout)
	}
	return fmt.Errorf("unknown transcript command: %s", args[0])
}

func transcriptImportClaudeCodeCommand(args []string, cwd string, stdout io.Writer) error {
	var path string
	projectCWD := cwd
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cwd":
			if i+1 >= len(args) {
				return errors.New("--cwd requires a value")
			}
			projectCWD = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag: %s", args[i])
			}
			if path != "" {
				return errors.New("import-claude-code accepts a single transcript path")
			}
			path = args[i]
		}
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("import-claude-code requires a transcript path")
	}
	result, err := session.ImportClaudeCode(session.DefaultStore(), path, projectCWD)
	if err != nil {
		return err
	}
	if hasArg(args, "--json") {
		return writePrettyJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Imported %s -> new session %s\n", result.SourcePath, result.SessionID)
	fmt.Fprintf(stdout, "Transcript: %s\n", result.Path)
	fmt.Fprintf(stdout, "Messages: %d  tool calls: %d  tool results: %d\n", result.Messages, result.ToolCalls, result.ToolResults)
	if result.SkippedCount > 0 {
		fmt.Fprintf(stdout, "Skipped %d unsupported entr(y/ies):\n", result.SkippedCount)
		for _, reason := range sortedSkipReasonsCLI(result.Skipped) {
			fmt.Fprintf(stdout, "  %s: %d\n", reason, result.Skipped[reason])
		}
	}
	return nil
}
