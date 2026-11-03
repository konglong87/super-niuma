package defaults

const (
	// Effort is the default reasoning effort for main and nested agent loops.
	// Explicit "off"/"none" values remain available when a caller needs to
	// disable extended thinking for a specific run.
	Effort = "high"

	// MaxTurns is the default tool-use continuation budget for CLI, TUI,
	// background jobs, and nested agents. Keep all entrypoints on the same value
	// so a missing flag cannot silently fall back to a smaller loop limit.
	MaxTurns = 100

	// CodeMaxTokens matches the observed Claude Code default for code-mode
	// Sonnet requests. Keep chat mode smaller so tenant/mobile chat defaults do
	// not inherit code-agent output budgets.
	CodeMaxTokens = 32000
	ChatMaxTokens = 4096
)
