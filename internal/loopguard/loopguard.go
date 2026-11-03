// Package loopguard detects a model that has stopped making progress: it keeps a
// rolling window of recent turn fingerprints and counts how many consecutive
// turns brought no new information into the context. One windowed rule catches a
// monotone repeat (A,A,A) and any cycle whose period fits the window (A,B,A,B,
// A,B,C,D, …) without period-specific logic.
//
// The mechanism and its trade-offs are documented in docs/loop_guard.md. It lives
// in its own package because the main query loop and sub-agent runs both need it:
// MaxTurns alone only bounds the damage, it cannot stop a loop early, and a
// token budget cannot stop a loop that spins without spending much.
package loopguard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

const (
	// SoftLimit is the number of consecutive no-progress turns after which the
	// stretch is worth recording for audit.
	SoftLimit = 3
	// HardLimit is the number of consecutive no-progress turns after which the
	// run is aborted outright.
	HardLimit = 6
	// Window is how many recent turn fingerprints are retained. It bounds the
	// largest loop period caught early (period <= window); longer periods fall
	// back to the MaxTurns backstop.
	Window = 16
)

// Tracker is the rolling no-progress detector for one run. The zero value is
// ready to use. It is not safe for concurrent use; each run owns its own.
type Tracker struct {
	window []string
	streak int
	label  string
	warned bool
}

// Observe folds one turn's fingerprint into the window and returns the resulting
// consecutive no-progress streak. label is a human-readable description of what
// the turn did, surfaced through Label for the escalating reminder. An empty
// fingerprint means "cannot tell what this turn did" and counts as progress, so
// an unclassifiable turn never accuses the model.
func (t *Tracker) Observe(fingerprint, label string) int {
	if fingerprint == "" {
		t.reset()
		return 0
	}
	seen := false
	for _, sig := range t.window {
		if sig == fingerprint {
			seen = true
			break
		}
	}
	if seen {
		if t.streak == 0 {
			// Count this repeat plus the earlier turn it matched, so a monotone
			// A,A,A loop aborts on the same turn as the pre-window implementation.
			t.streak = 2
		} else {
			t.streak++
		}
		t.label = label
	} else {
		t.reset()
	}
	t.window = append(t.window, fingerprint)
	if len(t.window) > Window {
		t.window = t.window[len(t.window)-Window:]
	}
	return t.streak
}

func (t *Tracker) reset() {
	t.streak = 0
	t.label = ""
	t.warned = false
}

// Streak reports the current consecutive no-progress streak.
func (t *Tracker) Streak() int { return t.streak }

// Label reports the description of the most recently repeated turn, or "" while
// the run is making progress.
func (t *Tracker) Label() string { return t.label }

// Tripped reports whether the streak reached HardLimit and the run must abort.
func (t *Tracker) Tripped() bool { return t.streak >= HardLimit }

// TakeWarning reports whether the streak has crossed SoftLimit, exactly once per
// no-progress stretch, so callers record one audit event instead of one per turn.
func (t *Tracker) TakeWarning() bool {
	if t.streak < SoftLimit || t.warned {
		return false
	}
	t.warned = true
	return true
}

// CallSignature returns a stable, order-sensitive fingerprint of a turn's tool
// calls (name + canonical input, ignoring the per-call tool id). Two turns with
// the same signature requested exactly the same work. Empty when the turn issued
// no tool calls.
//
// The tool id is deliberately excluded: it is a fresh correlation number every
// turn, so folding it in would make every turn unique and the guard would never
// fire.
func CallSignature(toolUses []anthropic.ContentBlock) string {
	if len(toolUses) == 0 {
		return ""
	}
	parts := make([]string, 0, len(toolUses))
	for _, block := range toolUses {
		parts = append(parts, block.Name+"\x00"+canonicalToolInput(block.Input))
	}
	return strings.Join(parts, "\x1e")
}

// TurnFingerprint combines a turn's tool calls with their outputs. Including the
// results is what keeps a legitimate poll or wait (same input, changing output)
// from being mistaken for a stuck loop.
func TurnFingerprint(toolUses, toolResults []anthropic.ContentBlock) string {
	return CallSignature(toolUses) + "\x1f" + resultsSignature(toolResults)
}

// TextFingerprint fingerprints a turn that issued no tool calls and was rejected
// by a completion gate. Such turns never reach the tool-execution guard, so
// without their own fingerprint a model that keeps closing with the same
// non-compliant text burns every remaining turn unnoticed. The rule is part of
// the fingerprint so the same text rejected for a different reason counts as a
// new situation.
func TextFingerprint(rule, text string) string {
	return "text\x1f" + rule + "\x1f" + strings.TrimSpace(text)
}

// canonicalToolInput normalizes a tool input so semantically-identical payloads
// with different key ordering compare equal (json.Marshal sorts map keys).
func canonicalToolInput(input json.RawMessage) string {
	var v any
	if err := json.Unmarshal(input, &v); err != nil {
		return string(input)
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return string(input)
	}
	return string(canonical)
}

// resultsSignature fingerprints a turn's tool outputs (ordered, including error
// state).
func resultsSignature(results []anthropic.ContentBlock) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		errFlag := "0"
		if r.IsError {
			errFlag = "1"
		}
		parts = append(parts, errFlag+"\x00"+r.Content)
	}
	return strings.Join(parts, "\x1e")
}

// Describe builds a short, human-readable label of a turn's tool calls (name +
// compact input, tool ids excluded) for loop-awareness messages.
func Describe(toolUses []anthropic.ContentBlock) string {
	parts := make([]string, 0, len(toolUses))
	for _, block := range toolUses {
		parts = append(parts, strings.TrimSpace(block.Name+" "+oneLine(string(block.Input), 120)))
	}
	return strings.Join(parts, "; ")
}

// oneLine collapses whitespace and truncates, for labels and audit fields.
func oneLine(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}

// Awareness returns a runtime-status section (plain text; the caller wraps
// everything in one <system-reminder>) that warns the model it is repeating the
// same action with no new result. It escalates with the no-progress streak and
// names the repeated call so a weak model sees exactly what to stop. Empty until
// the streak reaches 2 or when the call is unknown.
func Awareness(streak int, call string) string {
	if streak < 2 || strings.TrimSpace(call) == "" {
		return ""
	}
	switch {
	case streak < SoftLimit: // streak == 2
		return fmt.Sprintf("## Loop check\nYou just repeated the same action (%s) and got the same result. Confirm this repeat is actually necessary; if not, do something different.", call)
	case streak < HardLimit-1: // streak 3..4
		return fmt.Sprintf("## Loop check\nThe last %d turns produced no new information (most recently you repeated: %s). Use what you already have to move the task forward, or take a genuinely different action.", streak, call)
	default: // streak >= 5
		return fmt.Sprintf("## Loop check\nAbout to stop: the last %d turns made no progress (most recently you repeated: %s). One more repeat will abort this turn (loop guard). Change approach now, or finish with what you already have.", streak, call)
	}
}
