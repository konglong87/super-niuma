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
	transcriptSpacingPTYHelperEnv      = "GOCC_TUI_TRANSCRIPT_SPACING_PTY_HELPER"
	transcriptSpacingPTYAnswerSentinel = "PTY_SHORT_TURN_DONE"
)

func TestTranscriptSpacingPTYHelper(t *testing.T) {
	if os.Getenv(transcriptSpacingPTYHelperEnv) != "1" {
		t.Skip("PTY helper")
	}
	opts := Options{
		Welcome: WelcomeInfo{
			Version:        "pty-transcript-spacing",
			Model:          "deterministic",
			Provider:       "local",
			PromptMode:     "chat",
			PermissionMode: "ask",
			CWD:            "/workspace/niuma",
		},
		RunStream: func(ctx context.Context, _ string, events chan<- StreamEvent) error {
			for i := 1; i <= 2; i++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case events <- StreamEvent{Type: StreamText, Text: fmt.Sprintf("response line %02d: transcript spacing coverage\n", i)}:
				}
			}
			events <- StreamEvent{Type: StreamText, Text: transcriptSpacingPTYAnswerSentinel + "\n"}
			return nil
		},
	}
	if err := Run(context.Background(), os.Stdin, os.Stdout, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func TestShortResponseKeepsTranscriptSpacingCompactPTY(t *testing.T) {
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
	session := "gocc-transcript-spacing-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	})

	start := exec.Command(
		"tmux", "new-session", "-d", "-x", "100", "-y", "54",
		"-e", transcriptSpacingPTYHelperEnv+"=1", "-s", session,
		executable, "-test.run=^TestTranscriptSpacingPTYHelper$", "-test.count=1",
	)
	start.Env = append(os.Environ(), "TERM=xterm-256color")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start transcript-spacing PTY helper: %v\n%s", err, output)
	}

	const prompt = "short response spacing probe"
	waitForTranscriptSpacingPTYText(t, session, "Ask niuma", 10*time.Second)
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "-l", prompt)
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "Enter")
	waitForTranscriptSpacingPTYText(t, session, transcriptSpacingPTYAnswerSentinel, 10*time.Second)
	waitForTranscriptSpacingPTYText(t, session, "status  Ready", 10*time.Second)

	history := stripANSI(captureTranscriptSpacingTmux(t, session, true))
	assertCompactTranscriptSpacing(t, history, prompt, "response line 01: transcript spacing coverage", 2)
	assertCompactTranscriptSpacing(t, history, transcriptSpacingPTYAnswerSentinel, "Ask niuma", 2)
	if got := strings.Count(history, "Ask niuma"); got != 1 {
		t.Fatalf("bottom input count=%d, want 1:\n%s", got, history)
	}
}

func assertCompactTranscriptSpacing(t *testing.T, screen, before, after string, maxBlankLines int) {
	t.Helper()
	start := strings.LastIndex(screen, before)
	if start < 0 {
		t.Fatalf("screen missing spacing start marker %q:\n%s", before, screen)
	}
	remaining := screen[start+len(before):]
	end := strings.Index(remaining, after)
	if end < 0 {
		t.Fatalf("screen missing spacing end marker %q after %q:\n%s", after, before, screen)
	}
	firstNewline := strings.IndexByte(remaining[:end], '\n')
	if firstNewline < 0 {
		return
	}
	maxRun := 0
	run := 0
	for _, line := range strings.Split(remaining[firstNewline+1:end], "\n") {
		if strings.TrimSpace(line) == "" {
			run++
			maxRun = max(maxRun, run)
			continue
		}
		run = 0
	}
	if maxRun > maxBlankLines {
		t.Fatalf("blank line run between %q and %q = %d, want <= %d:\n%s", before, after, maxRun, maxBlankLines, screen)
	}
}

func waitForTranscriptSpacingPTYText(t *testing.T, session, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(captureTranscriptSpacingTmux(t, session, false), needle) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q:\n%s", needle, captureTranscriptSpacingTmux(t, session, true))
}

func captureTranscriptSpacingTmux(t *testing.T, session string, history bool) string {
	t.Helper()
	args := []string{"capture-pane", "-p", "-e", "-t", session}
	if history {
		args = append(args, "-S", "-")
	}
	output, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("capture tmux pane: %v\n%s", err, output)
	}
	return string(output)
}

func runTranscriptSpacingTmux(t *testing.T, args ...string) {
	t.Helper()
	if output, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
