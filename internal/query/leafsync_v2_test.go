package query

import (
	"context"
	"io"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/tools"
)

// textReplyStreamer returns a distinct end_turn text per call, no tools.
type textReplyStreamer struct {
	replies []string
	i       int
}

func (s *textReplyStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	r := "ok"
	if s.i < len(s.replies) {
		r = s.replies[s.i]
	}
	s.i++
	if cb.OnText != nil {
		if err := cb.OnText(r); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: r}}},
		StopReason: "end_turn",
	}, nil
}

// TestTurnStartSyncsRecorderLeafAfterOutOfBandRewind reproduces the live-TUI bug
// class at the systemic fix's actual site: a single long-lived recorder is shared
// across turns (as the TUI does), a rewind moves the active leaf out-of-band
// between turns, and the next turn must fork from the moved leaf — proving
// query.Session.run realigns the recorder's leaf at turn start
// (Recorder.SyncLeafFromDisk). Without the fix the recorder keeps its stale tip
// and the turn extends the old line (1 leaf), so this test fails.
func TestTurnStartSyncsRecorderLeafAfterOutOfBandRewind(t *testing.T) {
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	store := session.Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	id := "faceb00c-1111-4111-8111-111111111111"
	rec, err := store.NewRecorderWithID(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	streamer := &textReplyStreamer{replies: []string{"reply-1", "reply-2-old", "reply-2-new"}}
	cwd := t.TempDir()
	runTurn := func(prompt string) {
		// A fresh Session per turn but the SAME long-lived recorder — exactly the
		// TUI's lifecycle.
		s := New(streamer, tools.NewRegistry(), Options{Model: "test", CWD: cwd, Recorder: rec})
		if _, err := s.Run(context.Background(), prompt, io.Discard); err != nil {
			t.Fatalf("run %q: %v", prompt, err)
		}
	}
	runTurn("turn one")
	runTurn("turn two")

	// Locate turn two's user message id and rewind before it — out-of-band, via the
	// store, without touching the still-open recorder's in-memory leaf.
	entries, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	var u2 string
	for _, e := range entries {
		if e.Type == "message" && e.Role == "user" && e.Content == "turn two" {
			u2 = e.ID
		}
	}
	if u2 == "" {
		t.Fatal("could not find turn-two user message")
	}
	if _, ok, err := store.RewindConversationToMessage(id, u2); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}

	// Continue: the next turn must fork from the rewind target (systemic leaf sync).
	runTurn("turn two prime")

	after, err := session.Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if leaves := session.Leaves(after); len(leaves) != 2 {
		t.Fatalf("expected a fork (2 leaves) after out-of-band rewind + turn, got %d — recorder leaf was not realigned at turn start", len(leaves))
	}
	chain := session.CurrentChain(after)
	var haveNew, haveOld bool
	for _, e := range chain {
		if e.Content == "reply-2-new" {
			haveNew = true
		}
		if e.Content == "reply-2-old" || e.Content == "turn two" {
			haveOld = true
		}
	}
	if !haveNew {
		t.Fatal("current chain should contain the new branch reply")
	}
	if haveOld {
		t.Fatal("current chain must exclude the rewound branch")
	}
}
