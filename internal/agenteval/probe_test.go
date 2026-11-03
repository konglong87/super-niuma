package agenteval

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/query"
)

func TestCountGateBlocksSeparatesPlainBlocksFromPreflights(t *testing.T) {
	calls := []query.ToolTrace{
		{Name: "Bash", Output: "clean output"},
		// plain block：纯 reminder，无 preflight marker，必须计入。
		{Name: "Bash", Output: "<system-reminder>Tool blocked by Pre-Commit Scope Gate: git commit requires ...</system-reminder>", IsError: true},
		// auto-preflight：含 marker，由 GatePreflights 口径统计，此处不得重复计入。
		{Name: "Bash", Output: "<gate-preflight>... " + query.GatePreflightMarker + ": git push origin main ...</gate-preflight>"},
	}
	if got := countGateBlocks(calls); got != 1 {
		t.Fatalf("countGateBlocks = %d, want 1 (plain block only)", got)
	}
}

func TestBashCommandsExtractsOnlyBashInputs(t *testing.T) {
	calls := []query.ToolTrace{
		{Name: "Bash", Input: `{"command":"git status --short --branch"}`},
		{Name: "Read", Input: `{"file_path":"note.txt"}`},
		{Name: "Bash", Input: `not json`},
	}
	commands := bashCommands(calls)
	if len(commands) != 1 || commands[0] != "git status --short --branch" {
		t.Fatalf("bashCommands = %v, want single git status command", commands)
	}
}

func TestNormalizeProfileAcceptsGoldenProbe(t *testing.T) {
	for _, alias := range []string{"golden-probe", "probe"} {
		got, err := normalizeProfile(alias)
		if err != nil || got != "golden-probe" {
			t.Fatalf("normalizeProfile(%q) = %q, %v", alias, got, err)
		}
	}
}
