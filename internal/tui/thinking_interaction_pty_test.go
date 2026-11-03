package tui

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	thinkingInteractionPTYHelperEnv = "GOCC_TUI_THINKING_INTERACTION_PTY_HELPER"
	thinkingBodySentinel            = "THINKING_DETAIL_PRIVATE_SENTINEL"
	thinkingAnswerSentinel          = "THINKING_COLLAPSE_ANSWER_DONE"
)

func TestThinkingCollapseExpandPTYHelper(t *testing.T) {
	if os.Getenv(thinkingInteractionPTYHelperEnv) != "1" {
		t.Skip("PTY helper")
	}
	if err := Run(context.Background(), os.Stdin, os.Stdout, Options{
		Welcome: WelcomeInfo{
			Version:        "thinking-collapse-pty",
			Model:          "deterministic",
			Provider:       "local",
			PermissionMode: "ask",
			CWD:            "/workspace/niuma",
			ThinkingMode:   thinkingModeSummary,
		},
		RunStream: func(ctx context.Context, _ string, events chan<- StreamEvent) error {
			for _, text := range []string{"first private line\n", thinkingBodySentinel + "\n", "last private line\n"} {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case events <- StreamEvent{Type: StreamThinking, Text: text}:
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case events <- StreamEvent{Type: StreamText, Text: thinkingAnswerSentinel + "\n"}:
				return nil
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestThinkingCollapseExpandPTY(t *testing.T) {
	if testing.Short() {
		t.Skip("real PTY regression test")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is required for the real PTY regression test")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session := "gocc-thinking-collapse-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	start := exec.Command(
		"tmux", "new-session", "-d", "-x", "100", "-y", "32",
		"-e", thinkingInteractionPTYHelperEnv+"=1", "-s", session,
		executable, "-test.run=^TestThinkingCollapseExpandPTYHelper$", "-test.count=1",
	)
	start.Env = append(os.Environ(), "TERM=xterm-256color")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start thinking PTY helper: %v\n%s", err, output)
	}

	waitForTranscriptSpacingPTYText(t, session, "Ask niuma", 10*time.Second)
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "-l", "thinking collapse probe")
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "Enter")
	waitForTranscriptSpacingPTYText(t, session, thinkingAnswerSentinel, 10*time.Second)
	waitForTranscriptSpacingPTYText(t, session, "status  Ready", 10*time.Second)

	collapsedHistory := stripANSI(captureTranscriptSpacingTmux(t, session, true))
	if strings.Contains(collapsedHistory, thinkingBodySentinel) {
		t.Fatalf("summary mode leaked Thinking body into scrollback:\n%s", collapsedHistory)
	}
	if strings.Count(collapsedHistory, "Thought · turn 1 · phase 1") != 1 {
		t.Fatalf("Thinking summary count is not one:\n%s", collapsedHistory)
	}

	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "-l", "/thinking show 1")
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "Enter")
	waitForTranscriptSpacingPTYText(t, session, thinkingBodySentinel, 10*time.Second)
	expanded := stripANSI(captureTranscriptSpacingTmux(t, session, false))
	for _, want := range []string{"Thinking detail · turn 1", thinkingBodySentinel, "mouse=copy"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded Thinking detail missing %q:\n%s", want, expanded)
		}
	}

	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "-l", "/thinking summary 1")
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "Enter")
	waitForTranscriptSpacingPTYText(t, session, "Thinking mode: summary", 10*time.Second)
	collapsedAgain := stripANSI(captureTranscriptSpacingTmux(t, session, false))
	if strings.Contains(collapsedAgain, thinkingBodySentinel) {
		t.Fatalf("Thinking body remained visible after collapse:\n%s", collapsedAgain)
	}
	if !strings.Contains(collapsedAgain, "mouse=copy") {
		t.Fatalf("collapse changed native copy mode:\n%s", collapsedAgain)
	}
	finalHistory := stripANSI(captureTranscriptSpacingTmux(t, session, true))
	if strings.Count(finalHistory, "Thought · turn 1 · phase 1") != 1 || strings.Contains(finalHistory, thinkingBodySentinel) {
		t.Fatalf("collapse/expand mutated terminal scrollback:\n%s", finalHistory)
	}
}
