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

const tuiDraftPTYHelperEnv = "GOCC_TUI_DRAFT_PTY_HELPER"

func TestTUIDraftPTYHelper(t *testing.T) {
	if os.Getenv(tuiDraftPTYHelperEnv) != "1" {
		t.Skip("PTY helper")
	}
	store := NewFileDraftStore(os.Getenv("GOCC_TUI_DRAFT_DIR"))
	if err := Run(context.Background(), os.Stdin, os.Stdout, Options{
		Welcome:    WelcomeInfo{Version: "draft-pty", Model: "deterministic", Provider: "local", CWD: "/workspace/niuma", SessionID: "draft-pty-session"},
		DraftStore: store,
		Run:        func(context.Context, string) (string, error) { return "done", nil },
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTUIDraftPTY(t *testing.T) {
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
	draftDir := t.TempDir()
	session := "gocc-draft-" + strconv.Itoa(os.Getpid())
	start := func() {
		cmd := exec.Command("tmux", "new-session", "-d", "-x", "100", "-y", "32", "-e", tuiDraftPTYHelperEnv+"=1", "-e", "GOCC_TUI_DRAFT_DIR="+draftDir, "-s", session, executable, "-test.run=^TestTUIDraftPTYHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("start draft PTY helper: %v\n%s", err, output)
		}
	}
	kill := func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() }
	t.Cleanup(kill)

	start()
	waitForTranscriptSpacingPTYText(t, session, "Ask niuma", 10*time.Second)
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "-l", "draft survives restart")
	waitForTranscriptSpacingPTYText(t, session, "draft survives restart", 10*time.Second)
	kill()

	start()
	waitForTranscriptSpacingPTYText(t, session, "draft survives restart", 10*time.Second)
	runTranscriptSpacingTmux(t, "send-keys", "-t", session, "C-u")
	time.Sleep(300 * time.Millisecond)
	if screen := stripANSI(captureTranscriptSpacingTmux(t, session, false)); strings.Contains(screen, "draft survives restart") {
		t.Fatalf("clearing textarea left draft visible:\n%s", screen)
	}
}
