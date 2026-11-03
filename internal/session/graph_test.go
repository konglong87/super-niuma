package session

import (
	"strings"
	"testing"
)

// v2Store returns a Store rooted at a temp dir that writes v2 message-graph
// transcripts, isolated from the user's real snapshot/config directories.
func v2Store(t *testing.T) Store {
	t.Helper()
	t.Setenv("NIUMA_CONFIG_DIR", t.TempDir())
	return Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
}

// recordTurn simulates one linear turn: an auto-checkpoint, the user message
// (id given), a per-turn branch_head anchor, then an assistant reply.
func recordTurn(t *testing.T, r *Recorder, userID, userText, assistantText string) {
	t.Helper()
	if _, err := r.Checkpoint("auto-"+userID, userID); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(Entry{ID: userID, Type: "message", Role: "user", Content: userText}); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkTurn(userID); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(Entry{Type: "message", Role: "assistant", Content: assistantText}); err != nil {
		t.Fatal(err)
	}
}

func TestV2RecorderWritesGraph(t *testing.T) {
	store := v2Store(t)
	rec, err := store.NewRecorderWithID("/tmp/example", "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "aaaaaaaa-1111-4111-8111-111111111111", "hello", "hi there")
	recordTurn(t, rec, "bbbbbbbb-2222-4222-8222-222222222222", "again", "sure")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	entries, format, err := LoadWithFormat(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV2 {
		t.Fatalf("format = %q, want %q", format, TranscriptFormatGolangCCV2)
	}

	// First line is the session_meta header.
	if entries[0].Type != EntryTypeSessionMeta {
		t.Fatalf("first entry type = %q, want %q", entries[0].Type, EntryTypeSessionMeta)
	}
	// Every entry carries the v2 schema tag.
	for i, e := range entries {
		if e.Schema != SchemaV2 {
			t.Fatalf("entry[%d] type=%q schema=%q, want %q", i, e.Type, e.Schema, SchemaV2)
		}
	}

	// session_meta, session_event, and branch_head are not chain nodes: no parent_id.
	// Every other entry has a parent_id except the root chain node.
	rootCount := 0
	for _, e := range entries {
		switch e.Type {
		case EntryTypeSessionMeta, EntryTypeSessionEvent, EntryTypeBranchHead:
			if e.ParentID != "" {
				t.Fatalf("%s must not have parent_id, got %q", e.Type, e.ParentID)
			}
		default:
			if e.ID == "" {
				t.Fatalf("chain entry type=%q missing id", e.Type)
			}
			if e.ParentID == "" {
				rootCount++
			}
		}
	}
	if rootCount != 1 {
		t.Fatalf("expected exactly one root chain node, got %d", rootCount)
	}

	// Two per-turn branch_head anchors were written.
	branchHeads := 0
	for _, e := range entries {
		if e.Type == EntryTypeBranchHead {
			branchHeads++
			if e.Reason != BranchHeadReasonNewTurn {
				t.Fatalf("branch_head reason = %q, want %q", e.Reason, BranchHeadReasonNewTurn)
			}
		}
	}
	if branchHeads != 2 {
		t.Fatalf("branch_head count = %d, want 2", branchHeads)
	}

	// The active leaf is the last assistant message (the tip keeps advancing past
	// the per-turn anchors), and the current chain reproduces linear file order.
	chain := CurrentChain(entries)
	if len(chain) == 0 {
		t.Fatal("current chain is empty")
	}
	last := chain[len(chain)-1]
	if last.Role != "assistant" || last.Content != "sure" {
		t.Fatalf("chain tip = %+v, want assistant \"sure\"", last)
	}
	// Chain excludes session_meta and branch_head; count remaining entries.
	wantChain := 0
	for _, e := range entries {
		if e.Type != EntryTypeSessionMeta && e.Type != EntryTypeBranchHead {
			wantChain++
		}
	}
	if len(chain) != wantChain {
		t.Fatalf("chain length = %d, want %d (linear order)", len(chain), wantChain)
	}
	// Chain parent_id links are contiguous.
	for i := 1; i < len(chain); i++ {
		if chain[i].ParentID != chain[i-1].ID {
			t.Fatalf("chain break at %d: parent=%q prev.id=%q", i, chain[i].ParentID, chain[i-1].ID)
		}
	}
}

func TestSessionEventIsSideBandInV2Graph(t *testing.T) {
	store := v2Store(t)
	rec, err := store.NewRecorderWithID("/tmp/example", "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: "message-1", Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rec.AppendEvent(TranscriptEvent{TaskID: 41, EventType: "text_delta", PayloadJSON: `{"content":"hi"}`}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Append(Entry{ID: "message-2", Type: "message", Role: "assistant", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := ActiveLeaf(entries); got != "message-2" {
		t.Fatalf("ActiveLeaf = %q, want message-2", got)
	}
	chain := CurrentChain(entries)
	if len(chain) != 2 || chain[0].ID != "message-1" || chain[1].ID != "message-2" {
		t.Fatalf("CurrentChain = %+v", chain)
	}
}

func TestV2RecorderResumeContinuesChain(t *testing.T) {
	store := v2Store(t)
	rec, err := store.NewRecorderWithID("/tmp/example", "22222222-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "aaaaaaaa-1111-4111-8111-111111111111", "one", "first")
	leafBefore := rec.Leaf()
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: OpenRecorder must detect v2 and recover the active leaf.
	reopened, ok, err := store.OpenRecorder("22222222-1111-4111-8111-111111111111")
	if err != nil || !ok {
		t.Fatalf("OpenRecorder ok=%v err=%v", ok, err)
	}
	if !reopened.IsV2() {
		t.Fatal("reopened recorder is not v2")
	}
	if reopened.Leaf() != leafBefore {
		t.Fatalf("resumed leaf = %q, want %q", reopened.Leaf(), leafBefore)
	}
	recordTurn(t, reopened, "bbbbbbbb-2222-4222-8222-222222222222", "two", "second")
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	entries, _, err := LoadWithFormat(reopened.Path)
	if err != nil {
		t.Fatal(err)
	}
	chain := CurrentChain(entries)
	// The second turn must chain onto the first turn's assistant reply: exactly one
	// node (the second turn's auto-checkpoint) has parent == leafBefore.
	continued := 0
	for _, e := range chain {
		if e.ParentID == leafBefore {
			continued++
		}
	}
	if continued != 1 {
		t.Fatalf("expected exactly one node chaining onto resumed leaf %q, got %d", leafBefore, continued)
	}
	// Both turns' content is present on the single contiguous chain.
	var haveOne, haveTwo bool
	for _, e := range chain {
		if e.Content == "one" {
			haveOne = true
		}
		if e.Content == "two" {
			haveTwo = true
		}
	}
	if !haveOne || !haveTwo {
		t.Fatalf("chain missing turn content: one=%v two=%v", haveOne, haveTwo)
	}
	// Whole conversation resolves to a single contiguous chain.
	for i := 1; i < len(chain); i++ {
		if chain[i].ParentID != chain[i-1].ID {
			t.Fatalf("resumed chain break at %d", i)
		}
	}
}

func TestActiveLeafAndChainWithBranch(t *testing.T) {
	// Hand-built graph: root A -> B(user) -> C(assistant); then a branch_head
	// redirects to B and a new assistant C' chains from B. C is abandoned.
	entries := []Entry{
		{Type: EntryTypeSessionMeta, Schema: SchemaV2, ID: "meta"},
		{Type: "message", Role: "user", Schema: SchemaV2, ID: "A", ParentID: ""},
		{Type: "message", Role: "assistant", Schema: SchemaV2, ID: "B", ParentID: "A"},
		{Type: "message", Role: "assistant", Schema: SchemaV2, ID: "C", ParentID: "B"},
		{Type: EntryTypeBranchHead, Schema: SchemaV2, LeafID: "B", Reason: BranchHeadReasonRewind},
		{Type: "message", Role: "assistant", Schema: SchemaV2, ID: "Cprime", ParentID: "B"},
	}
	if got := ActiveLeaf(entries); got != "Cprime" {
		t.Fatalf("ActiveLeaf = %q, want Cprime", got)
	}
	chain := CurrentChain(entries)
	var ids []string
	for _, e := range chain {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "A,B,Cprime" {
		t.Fatalf("chain = %v, want [A B Cprime] (C abandoned)", ids)
	}

	// Redo back to C: the abandoned branch is still reachable.
	redo := append(entries, Entry{Type: EntryTypeBranchHead, Schema: SchemaV2, LeafID: "C", Reason: BranchHeadReasonRedo})
	if got := ActiveLeaf(redo); got != "C" {
		t.Fatalf("ActiveLeaf after redo = %q, want C", got)
	}

	// Both C and Cprime are leaves of the tree.
	leaves := Leaves(entries)
	if len(leaves) != 2 {
		t.Fatalf("Leaves = %v, want 2 (C and Cprime)", leaves)
	}
}

func TestV1RecorderUnaffected(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir()} // SchemaV2 defaults false
	rec, err := store.NewRecorderWithID("/tmp/example", "33333333-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if rec.IsV2() {
		t.Fatal("v1 recorder must not be v2")
	}
	if err := rec.Append(Entry{Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	entries, format, err := LoadWithFormat(rec.Path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV1 {
		t.Fatalf("format = %q, want v1", format)
	}
	for _, e := range entries {
		if e.Schema != "" || e.ParentID != "" {
			t.Fatalf("v1 entry must not carry schema/parent_id: %+v", e)
		}
	}
	// v1 files have no session_meta header and CurrentChain is a passthrough.
	if len(CurrentChain(entries)) != len(entries) {
		t.Fatal("CurrentChain must be identity for v1 transcripts")
	}
}
