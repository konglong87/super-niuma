package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/session"
)

// TestListRewindCandidatesHidesSystemReminder drives the exact function the
// interactive `/rewind` (no message-id) invokes — listRewindCandidates — against a
// transcript polluted (pre-fix) with a persisted completion-gate <system-reminder>
// user message, and asserts the listing shows only the real human turn.
func TestListRewindCandidatesHidesSystemReminder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"session_meta","schema":"niuma.transcript.v2","id":"meta"}`,
		`{"type":"message","schema":"niuma.transcript.v2","id":"u1","role":"user","content":"继续 P0-2（pm-data 整合 HEART 到步骤5）"}`,
		`{"type":"message","schema":"niuma.transcript.v2","id":"a1","parent_id":"u1","role":"assistant","content":"好的"}`,
		`{"type":"message","schema":"niuma.transcript.v2","id":"gate1","parent_id":"a1","role":"user","content":"<system-reminder>Completion is blocked by Post-Action Delta Gate: files or shared state changed...</system-reminder>"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := listRewindCandidates(path, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "system-reminder") {
		t.Fatalf("/rewind listing must not surface the gate reminder:\n%s", got)
	}
	if !strings.Contains(got, "P0-2") {
		t.Fatalf("/rewind listing should still offer the real user turn:\n%s", got)
	}
}

// buildBranchedV2Recorder writes a v2 session with an abandoned turn ("old
// answer") and an active turn ("new answer") off the same rewind point, then
// returns an open recorder on it and the offset captured after turn 1.
func buildBranchedV2Recorder(t *testing.T) (session.Store, *session.Recorder, int) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("NIUMA_CONFIG_DIR", root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", filepath.Join(root, "projects"))
	store := session.Store{TranscriptProjectsRoot: filepath.Join(root, "projects"), SchemaV2: true}
	id := "dddddddd-1111-4111-8111-111111111111"
	rec, err := store.NewRecorderWithID("/tmp/branchcli", id)
	if err != nil {
		t.Fatal(err)
	}
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	u2 := "bbbbbbbb-2222-4222-8222-222222222222"
	recordCLITurn(t, rec, u1, "before", "a1")
	offset, err := transcriptEntryCount(rec)
	if err != nil {
		t.Fatal(err)
	}
	recordCLITurn(t, rec, u2, "q2", "old answer")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.RewindConversationToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	reopened, ok, err := store.OpenRecorder(id)
	if err != nil || !ok {
		t.Fatalf("reopen ok=%v err=%v", ok, err)
	}
	recordCLITurn(t, reopened, "cccccccc-3333-4333-8333-333333333333", "q2b", "new answer")
	return store, reopened, offset
}

func recordCLITurn(t *testing.T, rec *session.Recorder, userID, userText, assistantText string) {
	t.Helper()
	if _, err := rec.Checkpoint("auto-"+userID, userID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{ID: userID, Type: "message", Role: "user", Content: userText}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(userID); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{Type: "message", Role: "assistant", Content: assistantText}); err != nil {
		t.Fatal(err)
	}
}

// TestResumeContinuationV2ExcludesAbandonedBranch proves the mid-session
// continuation context follows the current chain, not raw file order: a rewound
// branch is not replayed into the next turn.
func TestResumeContinuationV2ExcludesAbandonedBranch(t *testing.T) {
	_, rec, offset := buildBranchedV2Recorder(t)
	defer rec.Close()
	messages, _, err := resumeContinuationContext(rec, offset)
	if err != nil {
		t.Fatal(err)
	}
	text := messagesRequestText(messages)
	if strings.Contains(text, "old answer") {
		t.Fatalf("continuation replayed the abandoned branch:\n%s", text)
	}
	if !strings.Contains(text, "new answer") {
		t.Fatalf("continuation missing the active branch:\n%s", text)
	}
	if strings.Contains(text, "before") {
		t.Fatalf("continuation must exclude pre-offset entries:\n%s", text)
	}
}

// TestInteractiveBranchesAndRedoSlash smoke-tests the /branches and /redo TUI
// slash commands operating on the current session.
func TestInteractiveBranchesAndRedoSlash(t *testing.T) {
	_, rec, _ := buildBranchedV2Recorder(t)
	defer rec.Close()

	var out, errb bytes.Buffer
	handled, _, err := handleInteractiveSlash(context.Background(), options{cwd: "/tmp/branchcli"}, "/branches", &out, &errb, rec)
	if !handled || err != nil {
		t.Fatalf("/branches handled=%v err=%v", handled, err)
	}
	listing := out.String()
	if !strings.Contains(listing, "new answer") || !strings.Contains(listing, "old answer") {
		t.Fatalf("/branches should list both branches:\n%s", listing)
	}

	// Find the abandoned branch leaf via the store, then /redo onto it.
	branches, _, err := session.DefaultStore().Branches(rec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var oldLeaf string
	for _, b := range branches {
		if !b.Active {
			oldLeaf = b.LeafID
		}
	}
	if oldLeaf == "" {
		t.Fatal("no abandoned branch found")
	}
	out.Reset()
	handled, _, err = handleInteractiveSlash(context.Background(), options{cwd: "/tmp/branchcli"}, "/redo "+oldLeaf, &out, &errb, rec)
	if !handled || err != nil {
		t.Fatalf("/redo handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out.String(), "Redone to branch") {
		t.Fatalf("unexpected /redo output: %s", out.String())
	}
	// /redo with no leaf is a clear error.
	handled, _, err = handleInteractiveSlash(context.Background(), options{cwd: "/tmp/branchcli"}, "/redo", &out, &errb, rec)
	if !handled || err == nil {
		t.Fatalf("/redo without a leaf should error, handled=%v err=%v", handled, err)
	}
}

// TestLoadResumeEntriesV2FollowsCurrentChain proves the CLI resume loader derives
// the conversation from the active leaf (leaf-walk), not raw file order: a
// rewound-and-continued v2 transcript resumes onto the new branch, and an explicit
// resume-at node can still reach the abandoned branch.
func TestLoadResumeEntriesV2FollowsCurrentChain(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	store := session.Store{Root: t.TempDir(), SchemaV2: true}
	id := "66666666-6666-4666-8666-666666666666"
	rec, err := store.NewRecorderWithID("/tmp/v2app", id)
	if err != nil {
		t.Fatal(err)
	}
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	oldReply := "cccccccc-3333-4333-8333-333333333333"
	newReply := "dddddddd-4444-4444-8444-444444444444"
	if _, err := rec.Checkpoint("auto-"+u1, u1); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{ID: u1, Type: "message", Role: "user", Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(u1); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{ID: oldReply, Type: "message", Role: "assistant", Content: "reply-old"}); err != nil {
		t.Fatal(err)
	}
	// Non-destructive rewind back to the user message, then a new reply branch.
	if err := rec.Append(session.Entry{Type: session.EntryTypeBranchHead, LeafID: u1, Reason: session.BranchHeadReasonRewind}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{ID: newReply, Type: "message", Role: "assistant", Content: "reply-new"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	// Default resume follows the current chain: new reply present, old reply gone.
	chain, err := loadResumeEntries(store, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsContent(chain, "reply-new") || !containsContent(chain, "first") {
		t.Fatalf("current chain missing expected content: %+v", chain)
	}
	if containsContent(chain, "reply-old") {
		t.Fatalf("current chain leaked abandoned branch: %+v", chain)
	}

	// Explicit resume-at the abandoned reply reaches the old branch instead.
	oldChain, err := loadResumeEntries(store, id, oldReply)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContent(oldChain, "reply-old") || containsContent(oldChain, "reply-new") {
		t.Fatalf("resume-at old branch wrong: %+v", oldChain)
	}

	// A resume-at that does not exist is a clear error, not a silent empty resume.
	if _, err := loadResumeEntries(store, id, "no-such-node"); err == nil {
		t.Fatal("expected error for unknown resume-at node")
	}
}

// TestSessionBranchesAndRedoCommands smoke-tests the CLI wiring for the new
// `session branches` and `session redo` subcommands over a rewound v2 transcript.
func TestSessionBranchesAndRedoCommands(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	t.Setenv("NIUMA_CONFIG_DIR", root)
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", projects)

	store := session.Store{TranscriptProjectsRoot: projects, SchemaV2: true}
	id := "99999999-9999-4999-8999-999999999999"
	rec, err := store.NewRecorderWithID("/tmp/branchapp", id)
	if err != nil {
		t.Fatal(err)
	}
	u1 := "aaaaaaaa-1111-4111-8111-111111111111"
	oldLeaf := "cccccccc-3333-4333-8333-333333333333"
	if _, err := rec.Checkpoint("auto-"+u1, u1); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{ID: u1, Type: "message", Role: "user", Content: "q"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkTurn(u1); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{ID: oldLeaf, Type: "message", Role: "assistant", Content: "old"}); err != nil {
		t.Fatal(err)
	}
	// Rewind to the user message then reply differently → two branches.
	if err := rec.Append(session.Entry{Type: session.EntryTypeBranchHead, LeafID: u1, Reason: session.BranchHeadReasonRewind}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(session.Entry{Type: "message", Role: "assistant", Content: "new"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := sessionCommand([]string{"branches", id}, &out); err != nil {
		t.Fatalf("session branches: %v", err)
	}
	listing := out.String()
	if !strings.Contains(listing, oldLeaf) {
		t.Fatalf("branches listing missing abandoned leaf %q:\n%s", oldLeaf, listing)
	}
	// The active branch is marked with '*'; the abandoned old leaf is not.
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		if strings.Contains(line, oldLeaf) && strings.HasPrefix(strings.TrimSpace(line), "*") {
			t.Fatalf("abandoned leaf should not be active: %q", line)
		}
	}

	// Compare the two branches via the CLI: divergence must be reported.
	out.Reset()
	branches, _, err := session.DefaultStore().Branches(id)
	if err != nil {
		t.Fatal(err)
	}
	var activeLeaf string
	for _, b := range branches {
		if b.Active {
			activeLeaf = b.LeafID
		}
	}
	if err := sessionCommand([]string{"branches", id, "--compare", activeLeaf, oldLeaf}, &out); err != nil {
		t.Fatalf("session branches --compare: %v", err)
	}
	if !strings.Contains(out.String(), "Shared history") || !strings.Contains(out.String(), "Only on") {
		t.Fatalf("unexpected compare output: %s", out.String())
	}

	// Redo back onto the abandoned branch via the CLI.
	out.Reset()
	if err := sessionCommand([]string{"redo", id, oldLeaf}, &out); err != nil {
		t.Fatalf("session redo: %v", err)
	}
	if !strings.Contains(out.String(), "Redone to branch") {
		t.Fatalf("unexpected redo output: %s", out.String())
	}
	entries, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsContent(session.CurrentChain(entries), "old") {
		t.Fatal("after CLI redo, current chain should hold the old branch")
	}
}

// TestRewindCandidatesSkipRuntimeReminders guards that runtime-injected
// reminders (completion/closure-gate nudges recorded as user messages in older
// transcripts) never appear as /rewind targets — only genuine human turns do.
func TestRewindCandidatesSkipRuntimeReminders(t *testing.T) {
	entries := []session.Entry{
		{ID: "u1", Type: "message", Role: "user", Content: "真实用户问题一"},
		{ID: "a1", Type: "message", Role: "assistant", Content: "回答一"},
		{ID: "gate1", Type: "message", Role: "user", Content: "<system-reminder>Completion is blocked by Post-Action Delta Gate: files changed…</system-reminder>"},
		{ID: "u2", Type: "message", Role: "user", Content: "真实用户问题二"},
	}
	got := rewindCandidates(entries, 8)
	if len(got) != 2 {
		t.Fatalf("expected 2 real user turns, got %d: %+v", len(got), got)
	}
	for _, c := range got {
		if c.ID == "gate1" || strings.Contains(c.Preview, "system-reminder") {
			t.Fatalf("runtime reminder must not be a rewind candidate: %+v", c)
		}
	}
	// The genuine turns are still offered (most-recent-first).
	if got[0].ID != "u2" || got[1].ID != "u1" {
		t.Fatalf("expected [u2,u1], got [%s,%s]", got[0].ID, got[1].ID)
	}
}

func containsContent(entries []session.Entry, content string) bool {
	for _, e := range entries {
		if e.Content == content {
			return true
		}
	}
	return false
}
