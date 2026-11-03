package loopguard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

// observeAll folds a sequence of fingerprints into a fresh tracker and returns
// the 1-based position of the turn that tripped the hard limit (0 = never).
func observeAll(sigs []string) int {
	var tracker Tracker
	for i, sig := range sigs {
		tracker.Observe(sig, "call")
		if tracker.Tripped() {
			return i + 1
		}
	}
	return 0
}

func repeatSigs(pattern []string, turns int) []string {
	out := make([]string, 0, turns)
	for i := 0; i < turns; i++ {
		out = append(out, pattern[i%len(pattern)])
	}
	return out
}

func TestTrackerTripsOnDocumentedTurns(t *testing.T) {
	// The turn numbers below are the contract documented in docs/loop_guard.md §6;
	// they must not drift when the tracker moves between packages.
	cases := []struct {
		name    string
		pattern []string
		want    int
	}{
		{"monotone A,A,A", []string{"A"}, 6},
		{"alternating A,B", []string{"A", "B"}, 7},
		{"period four A,B,C,D", []string{"A", "B", "C", "D"}, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := observeAll(repeatSigs(tc.pattern, 40)); got != tc.want {
				t.Fatalf("tripped on turn %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTrackerTripsOnOutOfOrderRepeats(t *testing.T) {
	// docs/loop_guard.md §6: A,B,B,A,A,B,A trips on turn 7 — order does not
	// matter, only that no new fingerprint appears.
	if got := observeAll([]string{"A", "B", "B", "A", "A", "B", "A"}); got != 7 {
		t.Fatalf("tripped on turn %d, want 7", got)
	}
}

func TestTrackerNeverTripsWhenEveryTurnIsNew(t *testing.T) {
	var tracker Tracker
	for i := 0; i < 100; i++ {
		if streak := tracker.Observe(string(rune('a'+i%26))+strings.Repeat("x", i), "call"); streak != 0 {
			t.Fatalf("turn %d: streak = %d, want 0 for a always-new fingerprint", i+1, streak)
		}
	}
	if tracker.Tripped() {
		t.Fatal("a run whose every turn brings new information must never trip")
	}
}

func TestTrackerForgetsPeriodsLongerThanWindow(t *testing.T) {
	// A cycle longer than the window rolls its own earliest fingerprint out
	// before it recurs, so it is deliberately left to the MaxTurns backstop.
	pattern := make([]string, Window+1)
	for i := range pattern {
		pattern[i] = string(rune('A' + i))
	}
	if got := observeAll(repeatSigs(pattern, 200)); got != 0 {
		t.Fatalf("tripped on turn %d, want 0 (period %d exceeds window %d)", got, len(pattern), Window)
	}
}

func TestTrackerResetsStreakAndWarningOnProgress(t *testing.T) {
	var tracker Tracker
	tracker.Observe("A", "call A")
	tracker.Observe("A", "call A")
	tracker.Observe("A", "call A")
	if !tracker.TakeWarning() {
		t.Fatal("streak 3 must yield exactly one warning")
	}
	if tracker.TakeWarning() {
		t.Fatal("the warning is one-shot per no-progress stretch")
	}
	if tracker.Label() != "call A" {
		t.Fatalf("Label() = %q, want the repeated call label", tracker.Label())
	}
	if streak := tracker.Observe("NEW", "call B"); streak != 0 {
		t.Fatalf("streak = %d, want 0 after a new fingerprint", streak)
	}
	if tracker.Label() != "" {
		t.Fatalf("Label() = %q, want empty once progress resumed", tracker.Label())
	}
	// A fresh stretch must be able to warn again.
	tracker.Observe("A", "call A")
	tracker.Observe("A", "call A")
	tracker.Observe("A", "call A")
	if !tracker.TakeWarning() {
		t.Fatal("a new no-progress stretch must be able to warn again")
	}
}

func TestTrackerTreatsEmptyFingerprintAsProgress(t *testing.T) {
	var tracker Tracker
	tracker.Observe("A", "call A")
	tracker.Observe("A", "call A")
	if tracker.Streak() == 0 {
		t.Fatal("precondition: repeated fingerprint must build a streak")
	}
	if streak := tracker.Observe("", "call A"); streak != 0 {
		t.Fatalf("streak = %d, want 0 — an unknown fingerprint must not accuse the model", streak)
	}
}

func block(kind, name, input string) anthropic.ContentBlock {
	return anthropic.ContentBlock{Type: kind, ID: "call_ignored", Name: name, Input: json.RawMessage(input)}
}

func TestCallSignatureIgnoresToolIDButKeepsKeyOrderStable(t *testing.T) {
	a := []anthropic.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "Read", Input: json.RawMessage(`{"a":1,"b":2}`)}}
	b := []anthropic.ContentBlock{{Type: "tool_use", ID: "call_999", Name: "Read", Input: json.RawMessage(`{"b":2,"a":1}`)}}
	if CallSignature(a) != CallSignature(b) {
		t.Fatal("per-call tool ids and key ordering must not change the signature, or repeats are never recognised")
	}
	if CallSignature(nil) != "" {
		t.Fatal("a turn with no tool calls has no call signature")
	}
	c := []anthropic.ContentBlock{block("tool_use", "Read", `{"a":2}`)}
	if CallSignature(a) == CallSignature(c) {
		t.Fatal("different inputs must produce different signatures")
	}
}

func TestTurnFingerprintIncludesResults(t *testing.T) {
	uses := []anthropic.ContentBlock{block("tool_use", "Poll", `{"id":"job"}`)}
	first := TurnFingerprint(uses, []anthropic.ContentBlock{{Type: "tool_result", Content: "attempt 1"}})
	second := TurnFingerprint(uses, []anthropic.ContentBlock{{Type: "tool_result", Content: "attempt 2"}})
	if first == second {
		t.Fatal("results must be part of the fingerprint, otherwise a progressing poll is mistaken for a loop")
	}
	errored := TurnFingerprint(uses, []anthropic.ContentBlock{{Type: "tool_result", Content: "attempt 1", IsError: true}})
	if first == errored {
		t.Fatal("error state must be part of the fingerprint")
	}
}

func TestDescribeNamesToolsWithoutIDs(t *testing.T) {
	got := Describe([]anthropic.ContentBlock{
		block("tool_use", "Read", `{"file_path":"a.go"}`),
		block("tool_use", "Bash", `{"command":"ls"}`),
	})
	if !strings.Contains(got, "Read") || !strings.Contains(got, "a.go") || !strings.Contains(got, "Bash") {
		t.Fatalf("Describe() = %q, want both calls named", got)
	}
	if strings.Contains(got, "call_ignored") {
		t.Fatalf("Describe() = %q must not leak per-call tool ids", got)
	}
}

func TestAwarenessTiers(t *testing.T) {
	call := "Bash git status --short"
	if Awareness(0, call) != "" || Awareness(1, call) != "" {
		t.Fatal("streak <2 must be empty")
	}
	if Awareness(2, "") != "" {
		t.Fatal("empty call must be empty")
	}
	light := Awareness(2, call)
	if !strings.Contains(light, "Loop check") || !strings.Contains(light, call) || !strings.Contains(light, "necessary") {
		t.Fatalf("streak 2 light tier wrong: %q", light)
	}
	if strings.Contains(light, "<system-reminder>") {
		t.Fatalf("section must not wrap itself in system-reminder: %q", light)
	}
	warn := Awareness(3, call)
	if !strings.Contains(warn, "no new information") || !strings.Contains(warn, "3 turns") || !strings.Contains(warn, call) {
		t.Fatalf("streak 3 warning tier wrong: %q", warn)
	}
	if strings.Contains(warn, "times") {
		t.Fatalf("warning tier must not couple the turn count to a single call via 'times': %q", warn)
	}
	last := Awareness(5, call)
	if !strings.Contains(last, "abort") || !strings.Contains(last, "5 turns") || !strings.Contains(last, call) {
		t.Fatalf("streak 5 last-warning tier wrong: %q", last)
	}
}
