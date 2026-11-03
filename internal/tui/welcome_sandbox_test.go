package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// AUDIT-P1-35: the welcome card advertised a sandbox label and nothing else, so
// a host where the sandbox was configured but unenforced looked protected.

func welcomeModelWithSandbox(t *testing.T, width int, label string, warnings []string) string {
	t.Helper()
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{
			Version:         "test-version",
			Model:           "gpt-test",
			CWD:             "/workspace/project",
			PermissionMode:  "ask",
			Sandbox:         label,
			SandboxWarnings: warnings,
			ToolSummary:     "27",
		},
		Run: func(ctx context.Context, prompt string) (string, error) { return "answer", nil },
	})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: width, Height: 28})
	return updated.(Model).welcomeView()
}

// TestWelcomeCardShowsSandboxWarnings covers both layouts: a warning that
// disappears on a narrow terminal is no warning at all.
func TestWelcomeCardShowsSandboxWarnings(t *testing.T) {
	warning := "sandbox.enabled is set but sandbox-exec was not found on PATH, so no shell command is sandboxed on this machine."
	for _, width := range []int{120, 80} {
		view := welcomeModelWithSandbox(t, width, "degraded/not-enforced", []string{warning})
		// The card border interrupts wrapped text, so compare on the prose alone.
		flat := strings.Join(strings.Fields(strings.ReplaceAll(view, "│", " ")), " ")
		if !strings.Contains(flat, "sandbox not enforced:") {
			t.Fatalf("width %d: welcome card hides the sandbox warning:\n%s", width, view)
		}
		if !strings.Contains(flat, "sandbox-exec was not found on PATH") {
			t.Fatalf("width %d: welcome card drops the reason:\n%s", width, view)
		}
		if !strings.Contains(flat, "no shell command is sandboxed") {
			t.Fatalf("width %d: welcome card drops the consequence:\n%s", width, view)
		}
	}
}

// TestWelcomeCardUnchangedWithoutSandboxWarnings is the reverse assertion: a
// healthy install must render byte-for-byte what it rendered before the field
// existed, or the warning becomes background noise.
func TestWelcomeCardUnchangedWithoutSandboxWarnings(t *testing.T) {
	for _, width := range []int{120, 80} {
		healthy := welcomeModelWithSandbox(t, width, "on/seccomp/network", nil)
		empty := welcomeModelWithSandbox(t, width, "on/seccomp/network", []string{})
		if healthy != empty {
			t.Fatalf("width %d: an empty warning slice changed the card", width)
		}
		if strings.Contains(healthy, "not enforced") {
			t.Fatalf("width %d: healthy card warns anyway:\n%s", width, healthy)
		}
		if !strings.Contains(healthy, "on/seccomp/network") {
			t.Fatalf("width %d: healthy card lost its sandbox label:\n%s", width, healthy)
		}
	}
}
