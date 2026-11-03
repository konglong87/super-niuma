package session

import (
	"strings"
	"testing"
)

// buildBranchedSession creates a v2 session with two branches off the same turn:
// an abandoned one ("old answer") and the active one ("new answer"). Returns the
// store, session id, and the two branch leaf ids keyed by their preview.
func buildBranchedSession(t *testing.T) (Store, string) {
	t.Helper()
	store := v2Store(t)
	id := "abababab-1111-4111-8111-111111111111"
	rec, err := store.NewRecorderWithID("/tmp/branchcmp", id)
	if err != nil {
		t.Fatal(err)
	}
	recordTurn(t, rec, "aaaaaaaa-1111-4111-8111-111111111111", "q1", "a1")
	recordTurn(t, rec, "bbbbbbbb-2222-4222-8222-222222222222", "q2", "old answer")
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.RewindConversationToMessage(id, "bbbbbbbb-2222-4222-8222-222222222222"); err != nil || !ok {
		t.Fatalf("rewind ok=%v err=%v", ok, err)
	}
	reopened, ok, err := store.OpenRecorder(id)
	if err != nil || !ok {
		t.Fatalf("reopen ok=%v err=%v", ok, err)
	}
	recordTurn(t, reopened, "cccccccc-3333-4333-8333-333333333333", "q2-alt", "new answer")
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	return store, id
}

func TestBranchesReportForkPoints(t *testing.T) {
	store, id := buildBranchedSession(t)
	branches, ok, err := store.Branches(id)
	if err != nil || !ok {
		t.Fatalf("Branches ok=%v err=%v", ok, err)
	}
	if len(branches) != 2 {
		t.Fatalf("want 2 branches, got %d", len(branches))
	}
	var active, abandoned BranchInfo
	for _, b := range branches {
		if b.Active {
			active = b
		} else {
			abandoned = b
		}
	}
	if active.LeafID == "" || abandoned.LeafID == "" {
		t.Fatalf("could not identify both branches: %+v", branches)
	}
	// The active branch is the trunk (no fork point); the abandoned branch forked
	// off it at a shared node.
	if active.ForkPoint != "" {
		t.Fatalf("active branch should have no fork point, got %q", active.ForkPoint)
	}
	if abandoned.ForkPoint == "" {
		t.Fatal("abandoned branch must record its fork point")
	}
	if active.Preview != "new answer" || abandoned.Preview != "old answer" {
		t.Fatalf("branch previews wrong: active=%q abandoned=%q", active.Preview, abandoned.Preview)
	}
}

func TestCompareBranchesShowsDivergence(t *testing.T) {
	store, id := buildBranchedSession(t)
	branches, _, err := store.Branches(id)
	if err != nil {
		t.Fatal(err)
	}
	var activeLeaf, abandonedLeaf string
	for _, b := range branches {
		if b.Active {
			activeLeaf = b.LeafID
		} else {
			abandonedLeaf = b.LeafID
		}
	}

	cmp, ok, err := store.CompareBranches(id, activeLeaf, abandonedLeaf)
	if err != nil || !ok {
		t.Fatalf("CompareBranches ok=%v err=%v", ok, err)
	}
	// They share the first turn (q1/a1) and the second turn's user checkpoint.
	if cmp.CommonLength == 0 {
		t.Fatal("expected a shared prefix between the branches")
	}
	if cmp.ForkPoint == "" {
		t.Fatal("expected a fork point")
	}
	// Divergent nodes carry the branch-specific content and nothing from the other side.
	if !branchHasPreview(cmp.OnlyA, "new answer") {
		t.Fatalf("OnlyA should contain the active branch's answer: %+v", cmp.OnlyA)
	}
	if !branchHasPreview(cmp.OnlyB, "old answer") {
		t.Fatalf("OnlyB should contain the abandoned branch's answer: %+v", cmp.OnlyB)
	}
	if branchHasPreview(cmp.OnlyA, "old answer") || branchHasPreview(cmp.OnlyB, "new answer") {
		t.Fatal("divergent sets must not overlap")
	}
	// Shared content is not reported as divergent.
	if branchHasPreview(cmp.OnlyA, "a1") || branchHasPreview(cmp.OnlyB, "a1") {
		t.Fatal("shared history must not appear in the divergence")
	}
}

func TestCompareBranchesUnknownLeafErrors(t *testing.T) {
	store, id := buildBranchedSession(t)
	if _, _, err := store.CompareBranches(id, "no-such-leaf", "also-missing"); err == nil {
		t.Fatal("expected an error for unknown branch leaves")
	}
}

func branchHasPreview(nodes []BranchNode, preview string) bool {
	for _, n := range nodes {
		if strings.Contains(n.Preview, preview) {
			return true
		}
	}
	return false
}
