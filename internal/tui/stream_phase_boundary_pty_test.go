package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	phaseBoundaryPTYHelperEnv   = "GOCC_TUI_PHASE_BOUNDARY_PTY_HELPER"
	phaseBoundaryAnswerSentinel = "PHASE_BOUNDARY_ANSWER_TAIL"
)

func TestStreamPhaseBoundaryPTYHelper(t *testing.T) {
	if os.Getenv(phaseBoundaryPTYHelperEnv) != "1" {
		t.Skip("PTY helper")
	}
	if err := Run(context.Background(), os.Stdin, os.Stdout, Options{
		Welcome: WelcomeInfo{Version: "phase-boundary-pty", Model: "deterministic", Provider: "local", PermissionMode: "ask", CWD: "/workspace/niuma"},
		RunStream: func(ctx context.Context, _ string, events chan<- StreamEvent) error {
			for i := 1; i <= 18; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case events <- StreamEvent{Type: StreamThinking, Text: fmt.Sprintf("THINKING_LINE_%02d\n", i)}:
				}
			}
			for i := 1; i <= 18; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case events <- StreamEvent{Type: StreamText, Text: fmt.Sprintf("ANSWER_LINE_%02d\n", i)}:
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case events <- StreamEvent{Type: StreamText, Text: phaseBoundaryAnswerSentinel + "\n"}:
				return nil
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStreamPhaseBoundaryPTY(t *testing.T) {
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
	session := "gocc-phase-boundary-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	start := exec.Command("tmux", "new-session", "-d", "-x", "100", "-y", "32", "-e", phaseBoundaryPTYHelperEnv+"=1", "-s", session, executable, "-test.run=^TestStreamPhaseBoundaryPTYHelper$", "-test.count=1")
	start.Env = append(os.Environ(), "TERM=xterm-256color")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start phase-boundary PTY helper: %v\n%s", err, output)
	}
	waitForTranscriptSpacingPTYText(t, session, "Ask niuma", 10*time.Second)
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "-l", "phase boundary probe")
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "Enter")
	waitForTranscriptSpacingPTYText(t, session, phaseBoundaryAnswerSentinel, 10*time.Second)
	waitForTranscriptSpacingPTYText(t, session, "status  Ready", 10*time.Second)

	history := stripANSI(captureTranscriptSpacingTmux(t, session, true))
	for _, sentinel := range []string{"THINKING_LINE_01", "THINKING_LINE_18", "ANSWER_LINE_01", "ANSWER_LINE_18", phaseBoundaryAnswerSentinel} {
		if strings.Count(history, sentinel) != 1 {
			t.Fatalf("sentinel %q count=%d, want 1:\n%s", sentinel, strings.Count(history, sentinel), history)
		}
	}
	thinkingTail := strings.Index(history, "THINKING_LINE_18")
	answerHead := strings.Index(history, "ANSWER_LINE_01")
	answerTail := strings.Index(history, phaseBoundaryAnswerSentinel)
	if thinkingTail < 0 || answerHead < 0 || answerTail < 0 || !(thinkingTail < answerHead && answerHead < answerTail) {
		t.Fatalf("phase order is unstable: thinking_tail=%d answer_head=%d answer_tail=%d\n%s", thinkingTail, answerHead, answerTail, history)
	}
}
